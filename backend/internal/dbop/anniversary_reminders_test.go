package dbop

import (
	"context"
	"strings"
	"testing"
	"time"

	"tietie/backend/internal/weather"
)

func anniversaryJob(t *testing.T, db *DB, now time.Time) AnniversaryReminderSettings {
	t.Helper()
	jobs, err := db.ClaimAnniversaryReminders(context.Background(), now, 8)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("expected one due job: %+v %v", jobs, err)
	}
	return jobs[0]
}

func TestAnniversaryReminderAnnualDates(t *testing.T) {
	for _, tc := range []struct {
		name, now, date, occurrence string
		want                        int
		leap                        bool
	}{
		{"ordinary", "2026-10-02T08:00:00+08:00", "2025-10-05", "2026-10-05", 1, false},
		{"cross year", "2026-12-29T08:00:00+08:00", "2025-01-01", "2027-01-01", 1, false},
		{"non leap", "2027-02-25T08:00:00+08:00", "2024-02-29", "2027-02-28", 1, true},
		{"leap early", "2028-02-25T08:00:00+08:00", "2024-02-29", "", 0, false},
		{"leap", "2028-02-26T08:00:00+08:00", "2024-02-29", "2028-02-29", 1, false},
		{"future origin", "2026-10-02T08:00:00+08:00", "2028-10-05", "", 0, false},
		{"wrong day", "2026-10-02T08:00:00+08:00", "2025-10-06", "", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, _, _ := setupCareDB(t)
			ctx := context.Background()
			now, _ := time.Parse(time.RFC3339, tc.now)
			if _, err := db.ApplyAnniversary(ctx, "space", "date", 1, "", "纪念日", tc.date, "together"); err != nil {
				t.Fatal(err)
			}
			if err := db.SaveAnniversaryReminderSettings(ctx, "space", 1, true, now.Add(-time.Hour)); err != nil {
				t.Fatal(err)
			}
			if err := db.DeliverAnniversaryReminders(ctx, anniversaryJob(t, db, now), now); err != nil {
				t.Fatal(err)
			}
			reports, err := db.ListCareReports(ctx, "space", 100)
			if err != nil || len(reports) != tc.want {
				t.Fatal(reports, err)
			}
			if tc.want == 1 && (reports[0].Date != tc.occurrence || strings.Contains(reports[0].Text, "不是闰年") != tc.leap || !strings.Contains(reports[0].Text, "@一 @二")) {
				t.Fatal(reports[0])
			}
		})
	}
}

func TestAnniversaryReminderRestartIdempotencyAndAnnualRepeat(t *testing.T) {
	db, path, now := setupCareDB(t)
	ctx := context.Background()
	if defaults, err := db.GetAnniversaryReminderSettings(ctx, "space"); err != nil || defaults.Enabled {
		t.Fatal(defaults, err)
	}
	if _, err := db.ApplyAnniversary(ctx, "space", "date", 1, "", "在一起", "2025-10-05", "together"); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveAnniversaryReminderSettings(ctx, "space", 9, true, now); err == nil {
		t.Fatal("outsider enabled reminders")
	}
	if err := db.SaveAnniversaryReminderSettings(ctx, "space", 1, true, now); err != nil {
		t.Fatal(err)
	}
	if early, err := db.ClaimAnniversaryReminders(ctx, now.UTC(), 8); err != nil || len(early) != 0 {
		t.Fatal("delivered before Shanghai 08:00", early, err)
	}
	due := now.Add(time.Hour)
	job := anniversaryJob(t, db, due)
	if duplicate, err := db.ClaimAnniversaryReminders(ctx, due, 8); err != nil || len(duplicate) != 0 {
		t.Fatal("duplicate claim", duplicate, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	recovered := anniversaryJob(t, db, due.Add(4*time.Minute))
	if recovered.Token == job.Token {
		t.Fatal("lease was not recovered")
	}
	if err := db.DeliverAnniversaryReminders(ctx, job, due); err != nil {
		t.Fatal(err)
	}
	reports, _ := db.ListCareReports(ctx, "space", 100)
	if len(reports) != 0 {
		t.Fatal("stale lease delivered")
	}
	if err := db.DeliverAnniversaryReminders(ctx, recovered, due.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	// Repeated toggles cannot replay this year's notification.
	for _, enabled := range []bool{false, true} {
		if err := db.SaveAnniversaryReminderSettings(ctx, "space", 2, enabled, due.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.DeliverAnniversaryReminders(ctx, anniversaryJob(t, db, due.Add(time.Hour)), due.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	reports, _ = db.ListCareReports(ctx, "space", 100)
	if len(reports) != 1 {
		t.Fatal("toggle duplicated notice", reports)
	}
	nextYear := due.AddDate(1, 0, 0)
	if err := db.DeliverAnniversaryReminders(ctx, anniversaryJob(t, db, nextYear), nextYear); err != nil {
		t.Fatal(err)
	}
	reports, _ = db.ListCareReports(ctx, "space", 100)
	if len(reports) != 2 || reports[0].Date != "2027-10-05" {
		t.Fatal("annual recurrence missing", reports)
	}
}

func TestAnniversaryReminderChangesAndBindingIsolation(t *testing.T) {
	for _, change := range []string{"disable", "delete", "correct", "unbind"} {
		t.Run(change, func(t *testing.T) {
			db, _, now := setupCareDB(t)
			ctx := context.Background()
			row, err := db.ApplyAnniversary(ctx, "space", "date", 1, "", "生日", "2025-10-05", "birthday")
			if err != nil {
				t.Fatal(err)
			}
			if err := db.SaveAnniversaryReminderSettings(ctx, "space", 1, true, now); err != nil {
				t.Fatal(err)
			}
			due := now.Add(time.Hour)
			job := anniversaryJob(t, db, due)
			switch change {
			case "disable":
				err = db.SaveAnniversaryReminderSettings(ctx, "space", 2, false, due)
			case "delete":
				_, err = db.DeleteAnniversary(ctx, "space", "delete", 2, row.ID)
			case "correct":
				_, err = db.ApplyAnniversary(ctx, "space", "correct", 2, row.ID, row.Title, "2025-11-05", row.Kind)
			case "unbind":
				_, err = db.Unbind(ctx, 1)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := db.DeliverAnniversaryReminders(ctx, job, due); err != nil {
				t.Fatal(err)
			}
			var count int64
			if err := db.gdb.Model(&CareReport{}).Count(&count).Error; err != nil || count != 0 {
				t.Fatal("obsolete notice delivered", count, err)
			}
			if change == "unbind" {
				if _, err := db.CreateBinding(ctx, 1, 2, "space"); err != nil {
					t.Fatal(err)
				}
				settings, err := db.GetAnniversaryReminderSettings(ctx, "space")
				if err != nil || settings.Enabled {
					t.Fatal("old setting leaked into new binding", settings, err)
				}
				if err := db.SaveAnniversaryReminderSettings(ctx, "space", 1, true, now); err != nil {
					t.Fatal(err)
				}
				if err := db.DeliverAnniversaryReminders(ctx, anniversaryJob(t, db, due), due); err != nil {
					t.Fatal(err)
				}
				reports, _ := db.ListCareReports(ctx, "space", 100)
				if len(reports) != 0 {
					t.Fatal("old anniversary leaked into new binding")
				}
			}
		})
	}
}

func TestAnniversaryReminderAtomicDeliveryAndOutage(t *testing.T) {
	db, _, now := setupCareDB(t)
	ctx := context.Background()
	if _, err := db.ApplyAnniversary(ctx, "space", "date", 1, "", "纪念日", "2025-10-05", "other"); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveAnniversaryReminderSettings(ctx, "space", 1, true, now); err != nil {
		t.Fatal(err)
	}
	due := now.Add(time.Hour)
	job := anniversaryJob(t, db, due)
	if err := db.gdb.Exec("CREATE TRIGGER fail_anniversary_notice BEFORE INSERT ON memory_records WHEN NEW.category='realtime' BEGIN SELECT RAISE(ABORT,'memory unavailable'); END").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.DeliverAnniversaryReminders(ctx, job, due); err == nil {
		t.Fatal("memory failure ignored")
	}
	reports, _ := db.ListCareReports(ctx, "space", 100)
	if len(reports) != 0 {
		t.Fatal("partial report committed")
	}
	if err := db.gdb.Exec("DROP TRIGGER fail_anniversary_notice").Error; err != nil {
		t.Fatal(err)
	}
	// Restart on a later date: only today's relevant notices may be delivered.
	later := due.AddDate(0, 0, 1)
	if err := db.DeliverAnniversaryReminders(ctx, anniversaryJob(t, db, later), later); err != nil {
		t.Fatal(err)
	}
	reports, _ = db.ListCareReports(ctx, "space", 100)
	if len(reports) != 0 {
		t.Fatal("outage replayed yesterday's notice")
	}
	for _, hour := range []int{0, 7, 8, 22, 23} {
		now := time.Date(2026, 12, 31, hour, 30, 0, 0, weather.Shanghai)
		got := anniversaryReminderTime(now).In(weather.Shanghai)
		wantDay := 31
		if hour == 23 {
			wantDay = 1
		}
		if got.Hour() != 8 || got.Day() != wantDay {
			t.Fatal("wrong Shanghai schedule", hour, got)
		}
	}
}

func TestAnniversaryReminderNewDateAfterDailyCheckAndPrivateIsolation(t *testing.T) {
	db, _, _ := setupCareDB(t)
	ctx := context.Background()
	due := anniversaryReminderTime(time.Now())
	if err := db.SaveAnniversaryReminderSettings(ctx, "space", 1, true, due.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := db.DeliverAnniversaryReminders(ctx, anniversaryJob(t, db, due), due); err != nil {
		t.Fatal(err)
	}
	_, binding, err := db.CareMembers(ctx, "space")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SavePrivateChannel(ctx, PrivateChannel{SessionID: "private", SpaceID: "space", OwnerID: 1, BindingCreatedAt: binding.CreatedAt}); err != nil {
		t.Fatal(err)
	}
	date := due.In(weather.Shanghai).AddDate(0, 0, 3).Format("2006-01-02")
	if _, err := db.ApplyAnniversary(ctx, "private", "private_date", 1, "", "私密日期", date, "other"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ApplyAnniversary(ctx, "space", "late_date", 1, "", "新纪念日", date, "other"); err != nil {
		t.Fatal(err)
	}
	settings, err := db.GetAnniversaryReminderSettings(ctx, "space")
	if err != nil || !settings.NextDue.Equal(due) {
		t.Fatal("new date did not reschedule today's check", settings, err)
	}
	if err := db.DeliverAnniversaryReminders(ctx, anniversaryJob(t, db, due.Add(time.Minute)), due.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	reports, err := db.ListCareReports(ctx, "space", 100)
	if err != nil || len(reports) != 1 || strings.Contains(reports[0].Text, "私密日期") || !strings.Contains(reports[0].Text, "新纪念日") {
		t.Fatal("new shared anniversary missing or private date leaked", reports, err)
	}
	weatherReports, err := db.ListWeatherCareReports(ctx, "space", 8)
	if err != nil || len(weatherReports) != 0 {
		t.Fatal("anniversary notice included in weather comparison history", weatherReports, err)
	}
}
