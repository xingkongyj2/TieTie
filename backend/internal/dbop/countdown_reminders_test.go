package dbop

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
	"tietie/backend/internal/weather"
)

func countdownTestTime(value string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", value, weather.Shanghai)
	if err != nil {
		panic(err)
	}
	return t.UTC()
}

func TestCountdownReminderOccurrence(t *testing.T) {
	for _, test := range []struct {
		name, date, repeat, now, due string
		active                       bool
	}{
		{"before eight", "2026-10-07", "once", "2026-10-07 07:59", "2026-10-07 08:00", true},
		{"same day catchup", "2026-10-07", "once", "2026-10-07 13:00", "2026-10-07 08:00", true},
		{"last eligible minute", "2026-10-07", "once", "2026-10-07 22:59", "2026-10-07 08:00", true},
		{"late night once", "2026-10-07", "once", "2026-10-07 23:00", "", false},
		{"expired once", "2026-10-06", "once", "2026-10-07 08:00", "", false},
		{"future once", "2026-10-09", "once", "2026-10-07 23:00", "2026-10-09 08:00", true},
		{"annual future occurrence", "2020-10-08", "annual", "2026-10-07 08:00", "2026-10-08 08:00", true},
		{"future annual first year", "2030-10-07", "annual", "2026-10-07 08:00", "2030-10-07 08:00", true},
		{"future annual leap first year", "2028-02-29", "annual", "2027-02-28 09:00", "2028-02-29 08:00", true},
		{"annual past occurrence", "2020-10-06", "annual", "2026-10-07 08:00", "2027-10-06 08:00", true},
		{"late night annual", "2020-10-07", "annual", "2026-10-07 23:00", "2027-10-07 08:00", true},
		{"nonleap birthday", "2024-02-29", "annual", "2027-02-28 09:00", "2027-02-28 08:00", true},
		{"leap year occurrence", "2024-02-29", "annual", "2028-02-28 09:00", "2028-02-29 08:00", true},
		{"nonleap late night advances to leap year", "2024-02-29", "annual", "2027-02-28 23:00", "2028-02-29 08:00", true},
		{"invalid date", "bad", "once", "2026-10-07 09:00", "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			due, active := nextCountdownReminder(Countdown{Date: test.date, Repeat: test.repeat}, countdownTestTime(test.now))
			if active != test.active {
				t.Fatalf("active=%t want %t", active, test.active)
			}
			if !test.active {
				if !due.Equal(epochTime) {
					t.Fatalf("inactive due=%v", due)
				}
			} else if !due.Equal(countdownTestTime(test.due)) {
				t.Fatalf("due=%v want %s Beijing", due, test.due)
			}
		})
	}
}

func TestCalculateCountdownAnnualPreservesFirstDate(t *testing.T) {
	for _, test := range []struct {
		date, now string
		days      int
	}{
		{"2030-10-07", "2026-10-07 09:00", 1461},
		{"2028-02-29", "2027-02-28 09:00", 366},
	} {
		row := CalculateCountdown(Countdown{Date: test.date, Repeat: "annual"}, countdownTestTime(test.now))
		if row.NextDate != test.date || row.DaysRemaining != test.days || row.Expired || row.LeapAdjusted {
			t.Fatalf("source=%s now=%s result=%+v", test.date, test.now, row)
		}
	}
}

func TestCountdownReminderQueueSchemaIsIndexed(t *testing.T) {
	model, err := schema.Parse(&CountdownReminderSchedule{}, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatal(err)
	}
	if len(model.PrimaryFields) != 1 || model.PrimaryFields[0].DBName != "countdown_id" {
		t.Fatal("each countdown must have one durable schedule")
	}
	for _, index := range model.ParseIndexes() {
		if index.Name == "idx_countdown_reminder_due" && len(index.Fields) == 2 && index.Fields[0].DBName == "active" && index.Fields[1].DBName == "run_at" {
			return
		}
	}
	t.Fatal("due queue lacks active/run_at ordered index")
}

func countdownScheduleRows(row CountdownReminderSchedule) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"countdown_id", "session_id", "binding_created_at", "active", "next_due", "run_at", "token", "revision", "title", "date", "repeat", "kind"}).AddRow(row.CountdownID, row.SessionID, row.BindingCreatedAt, row.Active, row.NextDue, row.RunAt, row.Token, row.Revision, row.Title, row.Date, row.Repeat, row.Kind)
}

func countdownRows(row Countdown) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "session_id", "binding_created_at", "title", "date", "repeat", "kind"}).AddRow(row.ID, row.SessionID, row.BindingCreatedAt, row.Title, row.Date, row.Repeat, row.Kind)
}

func countdownTestSource(now time.Time, repeat string) (Countdown, CountdownReminderSchedule) {
	row := Countdown{ID: "cd-1", SessionID: "space", BindingCreatedAt: now.Add(-24 * time.Hour), Title: "出发", Date: now.In(weather.Shanghai).Format("2006-01-02"), Repeat: repeat, Kind: "other"}
	schedule := newCountdownReminderSchedule(row, now, 3)
	schedule.Token = "lease"
	return row, schedule
}

func TestCountdownReminderClaimLeasesPersistedQueue(t *testing.T) {
	now := countdownTestTime("2026-10-07 09:00")
	_, source := countdownTestSource(now, "annual")
	db, mock := mockWechatDB(t)
	mock.ExpectQuery("SELECT GET_LOCK").WithArgs(claimLockCountdownReminders).WillReturnRows(sqlmock.NewRows([]string{"lock"}).AddRow(1))
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `countdown_reminder_schedules` WHERE active=1 AND run_at<=.*EXISTS .*bindings.*ORDER BY run_at ASC,countdown_id ASC.*FOR UPDATE").WithArgs(now, 8).WillReturnRows(countdownScheduleRows(source))
	mock.ExpectExec("UPDATE `countdown_reminder_schedules` SET").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectQuery("SELECT RELEASE_LOCK").WithArgs(claimLockCountdownReminders).WillReturnRows(sqlmock.NewRows([]string{"lock"}).AddRow(1))
	jobs, err := db.ClaimCountdownReminders(context.Background(), now, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].Token == "" || jobs[0].Token == source.Token || !jobs[0].RunAt.Equal(now.Add(3*time.Minute)) {
		t.Fatalf("jobs=%+v", jobs)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCountdownReminderClaimSkipsOutageAndLateNight(t *testing.T) {
	for _, repeat := range []string{"once", "annual"} {
		for _, value := range []string{"2026-10-08 09:00", "2026-10-07 23:00"} {
			t.Run(repeat+value, func(t *testing.T) {
				now := countdownTestTime(value)
				_, source := countdownTestSource(countdownTestTime("2026-10-07 09:00"), repeat)
				db, mock := mockWechatDB(t)
				var updated CountdownReminderSchedule
				db.gdb.Callback().Update().Before("gorm:update").Register("test:capture_schedule", func(tx *gorm.DB) {
					if row, ok := tx.Statement.Dest.(*CountdownReminderSchedule); ok {
						updated = *row
					}
				})
				mock.ExpectQuery("SELECT GET_LOCK").WillReturnRows(sqlmock.NewRows([]string{"lock"}).AddRow(1))
				mock.ExpectBegin()
				mock.ExpectQuery("SELECT .* FROM `countdown_reminder_schedules`").WillReturnRows(countdownScheduleRows(source))
				mock.ExpectExec("UPDATE `countdown_reminder_schedules` SET").WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()
				mock.ExpectQuery("SELECT RELEASE_LOCK").WillReturnRows(sqlmock.NewRows([]string{"lock"}).AddRow(1))
				jobs, err := db.ClaimCountdownReminders(context.Background(), now, 8)
				if err != nil || len(jobs) != 0 {
					t.Fatalf("jobs=%+v err=%v", jobs, err)
				}
				if repeat == "once" && updated.Active || repeat == "annual" && (!updated.Active || !updated.NextDue.Equal(countdownTestTime("2027-10-07 08:00"))) {
					t.Fatalf("updated=%+v", updated)
				}
				if err := mock.ExpectationsWereMet(); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestCountdownReminderStaleClaimCannotDeliver(t *testing.T) {
	now := countdownTestTime("2026-10-07 09:00")
	_, source := countdownTestSource(now, "once")
	db, mock := mockWechatDB(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `countdown_reminder_schedules`.*FOR UPDATE").WithArgs(source.CountdownID, source.Token, source.Revision, source.BindingCreatedAt, 1).WillReturnRows(sqlmock.NewRows([]string{"countdown_id"}))
	mock.ExpectCommit()
	if err := db.DeliverCountdownReminder(context.Background(), source, now); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func expectCountdownDeliverySource(mock sqlmock.Sqlmock, row Countdown, source CountdownReminderSchedule) {
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `countdown_reminder_schedules`.*FOR UPDATE").WillReturnRows(countdownScheduleRows(source))
	mock.ExpectQuery("SELECT .* FROM `countdowns`").WillReturnRows(countdownRows(row))
	mock.ExpectQuery("SELECT .* FROM `bindings`").WillReturnRows(sqlmock.NewRows([]string{"session_id", "user_a", "user_b", "created_at"}).AddRow(row.SessionID, 8, 9, row.BindingCreatedAt))
	for _, member := range []struct {
		id   int64
		name string
	}{{8, "小明"}, {9, "小红"}} {
		mock.ExpectQuery("SELECT .* FROM `users`").WithArgs(member.id, 1).WillReturnRows(sqlmock.NewRows([]string{"id", "username"}).AddRow(member.id, member.name))
		mock.ExpectQuery("SELECT .* FROM `user_profiles`").WithArgs(member.id, 1).WillReturnRows(sqlmock.NewRows([]string{"user_id"}))
		mock.ExpectQuery("SELECT .* FROM `care_preferences`").WithArgs(row.SessionID, member.id, row.BindingCreatedAt, 1).WillReturnRows(sqlmock.NewRows([]string{"user_id"}))
	}
}

func TestCountdownReminderWechatFailureRollsBackGroupReport(t *testing.T) {
	now := countdownTestTime("2026-10-07 09:00")
	row, source := countdownTestSource(now, "once")
	db, mock := mockWechatDB(t)
	db.SetWechatNotifications(WechatNotificationOptions{Enabled: true, AppID: "wx-test", TemplateID: "template"})
	saved := captureNotifications(t, db)
	failure := errors.New("outbox unavailable")
	expectCountdownDeliverySource(mock, row, source)
	mock.ExpectExec("INSERT INTO `care_reports`.*ON DUPLICATE KEY UPDATE").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO `countdown_reminder_deliveries`").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO `wechat_notifications`").WillReturnError(failure)
	mock.ExpectRollback()
	if err := db.DeliverCountdownReminder(context.Background(), source, now); !errors.Is(err, failure) {
		t.Fatalf("err=%v", err)
	}
	if len(*saved) != 2 || (*saved)[0].Title != "倒计时提醒" || (*saved)[0].RecipientID != 8 || (*saved)[1].RecipientID != 9 || !strings.Contains((*saved)[0].Content, "@小明 @小红") {
		t.Fatalf("notifications=%+v", *saved)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCountdownReminderDuplicateOccurrenceDoesNotEnqueueAgain(t *testing.T) {
	now := countdownTestTime("2026-10-07 09:00")
	for _, repeat := range []string{"once", "annual"} {
		t.Run(repeat, func(t *testing.T) {
			row, source := countdownTestSource(now, repeat)
			db, mock := mockWechatDB(t)
			db.SetWechatNotifications(WechatNotificationOptions{Enabled: true})
			expectCountdownDeliverySource(mock, row, source)
			mock.ExpectExec("INSERT INTO `care_reports`").WillReturnResult(sqlmock.NewResult(0, 0))
			active, due := false, epochTime
			if repeat == "annual" {
				active, due = true, countdownTestTime("2027-10-07 08:00")
			}
			mock.ExpectExec("UPDATE `countdown_reminder_schedules` SET").WithArgs(active, due, due, "", row.ID).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectCommit()
			if err := db.DeliverCountdownReminder(context.Background(), source, now); err != nil {
				t.Fatal(err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCountdownWechatSourceChecksCurrentRevisionAndSendWindow(t *testing.T) {
	now := countdownTestTime("2026-10-07 09:00")
	row, schedule := countdownTestSource(now, "once")
	report := CareReport{ID: "report", SessionID: row.SessionID, BindingCreatedAt: row.BindingCreatedAt, Mode: "countdown", Date: row.Date}
	for _, stage := range []string{"valid", "deleted source", "modified revision", "deleted countdown", "changed countdown", "before eight", "late night", "next day"} {
		t.Run(stage, func(t *testing.T) {
			db, mock := mockWechatDB(t)
			at := now
			if stage == "before eight" {
				at = countdownTestTime("2026-10-07 07:59")
			} else if stage == "late night" {
				at = countdownTestTime("2026-10-07 23:00")
			} else if stage == "next day" {
				at = countdownTestTime("2026-10-08 09:00")
			} else {
				deliveries := sqlmock.NewRows([]string{"report_id", "countdown_id", "session_id", "binding_created_at", "revision"})
				if stage != "deleted source" {
					deliveries.AddRow(report.ID, row.ID, row.SessionID, row.BindingCreatedAt, schedule.Revision)
				}
				mock.ExpectQuery("SELECT .* FROM `countdown_reminder_deliveries`").WithArgs(report.ID, row.SessionID, row.BindingCreatedAt, 1).WillReturnRows(deliveries)
				if stage != "deleted source" {
					rows := countdownScheduleRows(schedule)
					if stage == "modified revision" {
						rows = sqlmock.NewRows([]string{"countdown_id"})
					}
					mock.ExpectQuery("SELECT .* FROM `countdown_reminder_schedules`").WithArgs(row.ID, row.SessionID, row.BindingCreatedAt, schedule.Revision, 1).WillReturnRows(rows)
					if stage != "modified revision" {
						current := row
						if stage == "changed countdown" {
							current.Title = "另一件事"
						}
						rows := countdownRows(current)
						if stage == "deleted countdown" {
							rows = sqlmock.NewRows([]string{"id"})
						}
						mock.ExpectQuery("SELECT .* FROM `countdowns`").WillReturnRows(rows)
					}
				}
			}
			valid, err := validWechatCountdownSource(db.gdb, report, at)
			if err != nil || valid != (stage == "valid") {
				t.Fatalf("valid=%t err=%v", valid, err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCountdownScheduleUnchangedSaveKeepsDeliveredOccurrence(t *testing.T) {
	now := countdownTestTime("2026-10-07 09:00")
	row, previous := countdownTestSource(now, "once")
	previous.Active = false
	previous.Token = ""
	previous.NextDue, previous.RunAt = epochTime, epochTime
	db, mock := mockWechatDB(t)
	mock.ExpectQuery("SELECT .* FROM `countdown_reminder_schedules`.*FOR UPDATE").WithArgs(row.ID, 1).WillReturnRows(countdownScheduleRows(previous))
	if err := syncCountdownReminderSchedule(db.gdb, row, now); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCountdownScheduleEditInvalidatesClaimAndReschedules(t *testing.T) {
	now := countdownTestTime("2026-10-07 09:00")
	row, previous := countdownTestSource(now, "once")
	row.Title, row.Date = "新的旅程", "2026-10-09"
	db, mock := mockWechatDB(t)
	var saved CountdownReminderSchedule
	db.gdb.Callback().Create().Before("gorm:create").Register("test:capture_schedule", func(tx *gorm.DB) {
		if row, ok := tx.Statement.Dest.(*CountdownReminderSchedule); ok {
			saved = *row
		}
	})
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `countdown_reminder_schedules`.*FOR UPDATE").WithArgs(row.ID, 1).WillReturnRows(countdownScheduleRows(previous))
	mock.ExpectExec("INSERT INTO `countdown_reminder_schedules`.*ON DUPLICATE KEY UPDATE").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if err := db.gdb.Transaction(func(tx *gorm.DB) error { return syncCountdownReminderSchedule(tx, row, now) }); err != nil {
		t.Fatal(err)
	}
	if saved.Revision != previous.Revision+1 || saved.Token != "" || !saved.Active || !saved.NextDue.Equal(countdownTestTime("2026-10-09 08:00")) || saved.Title != row.Title {
		t.Fatalf("saved=%+v", saved)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCountdownSaveCreatesMissingScheduleInSameTransaction(t *testing.T) {
	now := countdownTestTime("2026-10-07 09:00")
	row, _ := countdownTestSource(now, "once")
	db, mock := mockWechatDB(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `bindings`").WillReturnRows(sqlmock.NewRows([]string{"session_id", "user_a", "user_b", "created_at"}).AddRow(row.SessionID, 8, 9, row.BindingCreatedAt))
	mock.ExpectQuery("SELECT .* FROM `countdown_receipts`").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery("SELECT .* FROM `countdowns`").WillReturnRows(countdownRows(row))
	mock.ExpectQuery("SELECT .* FROM `countdown_reminder_schedules`").WillReturnRows(sqlmock.NewRows([]string{"countdown_id"}))
	mock.ExpectExec("INSERT INTO `countdown_reminder_schedules`").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO `countdown_receipts`").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	result, err := db.ApplyCountdown(context.Background(), row.SessionID, "request", 8, row.BindingCreatedAt, row.ID, row.Title, row.Date, row.Repeat, row.Kind, now)
	if err != nil || result.ID != row.ID || result.DaysRemaining != 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCountdownDeleteRemovesScheduleInSameTransaction(t *testing.T) {
	now := countdownTestTime("2026-10-07 09:00")
	row, _ := countdownTestSource(now, "once")
	db, mock := mockWechatDB(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `bindings`").WillReturnRows(sqlmock.NewRows([]string{"session_id", "user_a", "user_b", "created_at"}).AddRow(row.SessionID, 8, 9, row.BindingCreatedAt))
	mock.ExpectQuery("SELECT .* FROM `countdown_receipts`").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery("SELECT .* FROM `countdowns`").WillReturnRows(countdownRows(row))
	mock.ExpectExec("DELETE FROM `countdowns`").WithArgs(row.ID).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM `countdown_reminder_schedules`").WithArgs(row.ID, row.BindingCreatedAt).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO `memory_records`").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT .* FROM `memory_records`").WillReturnRows(sqlmock.NewRows([]string{"id", "revision"}).AddRow("memory", 1))
	mock.ExpectExec("INSERT INTO `memory_revisions`").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO `countdown_receipts`").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	result, err := db.DeleteCountdown(context.Background(), row.SessionID, "request", 8, row.BindingCreatedAt, row.ID)
	if err != nil || result.ID != row.ID {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCountdownScheduleBackfillOnlyInitializesMissingLiveSources(t *testing.T) {
	now := countdownTestTime("2026-10-07 09:00")
	row, _ := countdownTestSource(now, "annual")
	row.Date = "2024-02-29"
	db, mock := mockWechatDB(t)
	var saved []CountdownReminderSchedule
	db.gdb.Callback().Create().Before("gorm:create").Register("test:capture_backfill", func(tx *gorm.DB) {
		if rows, ok := tx.Statement.Dest.(*[]CountdownReminderSchedule); ok {
			saved = append(saved, (*rows)...)
		}
	})
	mock.ExpectQuery("SELECT c.\\* FROM countdowns AS c JOIN bindings.*LEFT JOIN countdown_reminder_schedules.*WHERE s.countdown_id IS NULL").WithArgs(128).WillReturnRows(countdownRows(row))
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `countdown_reminder_schedules`.*ON DUPLICATE KEY UPDATE `countdown_id`=`countdown_id`").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectQuery("SELECT c.\\* FROM countdowns AS c.*WHERE s.countdown_id IS NULL").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	if err := backfillCountdownReminderSchedules(db.gdb, now); err != nil {
		t.Fatal(err)
	}
	if len(saved) != 1 || saved[0].Revision != 1 || !saved[0].NextDue.Equal(countdownTestTime("2027-02-28 08:00")) {
		t.Fatalf("saved=%+v", saved)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
