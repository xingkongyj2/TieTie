package dbop

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"tietie/backend/internal/memoryspace"
	"tietie/backend/internal/weather"
)

// A durable indexed queue per countdown avoids scanning every space on each
// scheduler tick. Revision invalidates claimed work and queued WeChat messages
// when members edit the countdown.
type CountdownReminderSchedule struct {
	CountdownID      string    `gorm:"primaryKey;size:64"`
	SessionID        string    `gorm:"index;size:160"`
	BindingCreatedAt time.Time `gorm:"type:datetime(6)"`
	Active           bool      `gorm:"index:idx_countdown_reminder_due,priority:1"`
	NextDue          time.Time `gorm:"type:datetime(6)"`
	RunAt            time.Time `gorm:"index:idx_countdown_reminder_due,priority:2;type:datetime(6)"`
	Token            string    `gorm:"size:64"`
	Revision         int64
	Title            string
	Date             string `gorm:"size:32"`
	Repeat           string `gorm:"size:32"`
	Kind             string `gorm:"size:32"`
}

// Delivery keeps the exact source revision behind a report even after an
// annual countdown advances to its next occurrence.
type CountdownReminderDelivery struct {
	ReportID         string    `gorm:"primaryKey;size:64"`
	CountdownID      string    `gorm:"index;size:64"`
	SessionID        string    `gorm:"size:160"`
	BindingCreatedAt time.Time `gorm:"type:datetime(6)"`
	Revision         int64
}

func nextCountdownReminder(row Countdown, now time.Time) (time.Time, bool) {
	if _, err := time.ParseInLocation("2006-01-02", row.Date, weather.Shanghai); err != nil {
		return epochTime, false
	}
	local := now.In(weather.Shanghai)
	calculated := CalculateCountdown(row, local)
	if calculated.Expired {
		return epochTime, false // One-time historical dates are never replayed.
	}
	date, _ := time.ParseInLocation("2006-01-02", calculated.NextDate, weather.Shanghai)
	if calculated.DaysRemaining == 0 && local.Hour() >= 23 {
		if row.Repeat != "annual" {
			return epochTime, false
		}
		// Calculate from tomorrow to preserve Feb 29 -> Feb 28 in non-leap years.
		calculated = CalculateCountdown(row, date.AddDate(0, 0, 1))
		date, _ = time.ParseInLocation("2006-01-02", calculated.NextDate, weather.Shanghai)
	}
	return time.Date(date.Year(), date.Month(), date.Day(), 8, 0, 0, 0, weather.Shanghai).UTC(), true
}

func countdownScheduleMatches(schedule CountdownReminderSchedule, row Countdown) bool {
	return schedule.CountdownID == row.ID && schedule.SessionID == row.SessionID && schedule.BindingCreatedAt.Equal(row.BindingCreatedAt) && schedule.Title == row.Title && schedule.Date == row.Date && schedule.Repeat == row.Repeat && schedule.Kind == row.Kind
}

func newCountdownReminderSchedule(row Countdown, now time.Time, revision int64) CountdownReminderSchedule {
	due, active := nextCountdownReminder(row, now)
	return CountdownReminderSchedule{CountdownID: row.ID, SessionID: row.SessionID, BindingCreatedAt: row.BindingCreatedAt, Active: active, NextDue: due, RunAt: due, Revision: revision, Title: row.Title, Date: row.Date, Repeat: row.Repeat, Kind: row.Kind}
}

func syncCountdownReminderSchedule(tx *gorm.DB, row Countdown, now time.Time) error {
	var previous CountdownReminderSchedule
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("countdown_id=?", row.ID).Take(&previous).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if err == nil && countdownScheduleMatches(previous, row) {
		return nil // An idempotent save must not reset a delivered occurrence.
	}
	next := newCountdownReminderSchedule(row, now, previous.Revision+1)
	return tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&next).Error
}

// Run once at startup to initialize pre-existing countdowns, without replacing
// schedules that have already been delivered or changed by another instance.
func backfillCountdownReminderSchedules(gdb *gorm.DB, now time.Time) error {
	for {
		var rows []Countdown
		if err := gdb.Table("countdowns AS c").Select("c.*").Joins("JOIN bindings AS b ON b.session_id=c.session_id AND b.created_at=c.binding_created_at").Joins("LEFT JOIN countdown_reminder_schedules AS s ON s.countdown_id=c.id").Where("s.countdown_id IS NULL").Order("c.id ASC").Limit(128).Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		schedules := make([]CountdownReminderSchedule, 0, len(rows))
		for _, row := range rows {
			schedules = append(schedules, newCountdownReminderSchedule(row, now, 1))
		}
		if err := gdb.Clauses(clause.OnConflict{DoNothing: true}).Create(&schedules).Error; err != nil {
			return err
		}
	}
}

func (db *DB) ClaimCountdownReminders(ctx context.Context, now time.Time, limit int) ([]CountdownReminderSchedule, error) {
	if limit <= 0 || limit > 64 {
		limit = 8
	}
	jobs := []CountdownReminderSchedule{}
	err := claimWithLock(ctx, db.gdb, claimLockCountdownReminders, func(tx *gorm.DB) error {
		jobs = jobs[:0] // claimWithLock may retry a transaction after a deadlock.
		var rows []CountdownReminderSchedule
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("active=1 AND run_at<=? AND EXISTS (SELECT 1 FROM bindings b WHERE b.session_id=countdown_reminder_schedules.session_id AND b.created_at=countdown_reminder_schedules.binding_created_at)", now.UTC()).Order("run_at ASC,countdown_id ASC").Limit(limit).Find(&rows).Error; err != nil {
			return err
		}
		local := now.In(weather.Shanghai)
		for _, row := range rows {
			if row.NextDue.In(weather.Shanghai).Format("2006-01-02") != local.Format("2006-01-02") || local.Hour() >= 23 {
				row.NextDue, row.Active = nextCountdownReminder(Countdown{Date: row.Date, Repeat: row.Repeat}, now)
				row.RunAt, row.Token = row.NextDue, ""
				if err := tx.Save(&row).Error; err != nil {
					return err
				}
				if !row.Active || row.NextDue.After(now) {
					continue
				}
			}
			bytes := make([]byte, 16)
			if _, err := rand.Read(bytes); err != nil {
				return err
			}
			row.Token, row.RunAt = hex.EncodeToString(bytes), now.Add(3*time.Minute).UTC()
			if err := tx.Save(&row).Error; err != nil {
				return err
			}
			jobs = append(jobs, row)
		}
		return nil
	})
	return jobs, err
}

func (db *DB) DeliverCountdownReminder(ctx context.Context, job CountdownReminderSchedule, now time.Time) error {
	return db.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current CountdownReminderSchedule
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("countdown_id=? AND active=1 AND token=? AND revision=? AND binding_created_at=?", job.CountdownID, job.Token, job.Revision, job.BindingCreatedAt).Take(&current).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return nil // Deleted, edited or re-claimed while this worker waited.
		} else if err != nil {
			return err
		}
		var row Countdown
		if err := tx.Where("id=? AND session_id=? AND binding_created_at=?", current.CountdownID, current.SessionID, current.BindingCreatedAt).Take(&row).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return tx.Delete(&current).Error
		} else if err != nil {
			return err
		}
		if !countdownScheduleMatches(current, row) {
			return syncCountdownReminderSchedule(tx, row, now)
		}
		b, err := bindingForSession(tx, current.SessionID)
		if err != nil {
			return err
		}
		if b == nil || !b.CreatedAt.Equal(current.BindingCreatedAt) {
			return tx.Model(&current).Updates(map[string]any{"active": false, "token": ""}).Error
		}
		local := now.In(weather.Shanghai)
		validTime := current.NextDue.In(weather.Shanghai).Format("2006-01-02") == local.Format("2006-01-02") && local.Hour() >= 8 && local.Hour() < 23
		if validTime {
			members, err := careMembers(tx, *b)
			if err != nil {
				return err
			}
			names := make([]string, 0, len(members))
			for _, member := range members {
				names = append(names, "@"+member.Name)
			}
			text := fmt.Sprintf("%s 今天就是「%s」啦 🎉\n%s，一起记得这个日子。", strings.Join(names, " "), row.Title, local.Format("2006年1月2日"))
			if CalculateCountdown(row, now).LeapAdjusted {
				text += "\n今年不是闰年，2月29日按2月28日提醒。"
			}
			report := CareReport{ID: "evt_care_cd_" + MemoryID(current.SessionID, row.ID+local.Format("2006-01-02")+b.CreatedAt.Format(time.RFC3339Nano)), SessionID: current.SessionID, Mode: "countdown", Date: local.Format("2006-01-02"), Text: text, Cards: []weather.Card{}, BindingCreatedAt: b.CreatedAt, CreatedAt: now.UTC()}
			result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&report)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected > 0 {
				if err := tx.Create(&CountdownReminderDelivery{ReportID: report.ID, CountdownID: row.ID, SessionID: row.SessionID, BindingCreatedAt: b.CreatedAt, Revision: current.Revision}).Error; err != nil {
					return err
				}
				if err := db.enqueueWechatCare(tx, report, *b, current.NextDue); err != nil {
					return err
				}
				expires := time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, weather.Shanghai)
				fact, _ := json.Marshal(map[string]any{"content": text, "sourceType": "countdown_reminder", "countdownId": row.ID, "date": report.Date, "expiresAt": expires})
				if err := enqueueMemory(tx, MemoryRecord{SessionID: row.SessionID, Path: memoryspace.FactPath("realtime", "space", 0, report.ID), Scope: "space", Category: "realtime", ExpiresAt: &expires, PendingContent: string(fact), Operation: "upsert", BindingCreatedAt: b.CreatedAt}); err != nil {
					return err
				}
			}
		}
		// Skip missed dates after an outage; an annual item advances to the next
		// year and a delivered one-time item leaves the active queue permanently.
		scheduleFrom := now
		if validTime {
			scheduleFrom = time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, weather.Shanghai)
		}
		next, active := nextCountdownReminder(row, scheduleFrom)
		return tx.Model(&current).Updates(map[string]any{"active": active, "next_due": next, "run_at": next, "token": ""}).Error
	})
}

func validWechatCountdownSource(tx *gorm.DB, report CareReport, now time.Time) (bool, error) {
	local := now.In(weather.Shanghai)
	if report.Date != local.Format("2006-01-02") || local.Hour() < 8 || local.Hour() >= 23 {
		return false, nil
	}
	var source CountdownReminderDelivery
	if err := tx.Where("report_id=? AND session_id=? AND binding_created_at=?", report.ID, report.SessionID, report.BindingCreatedAt).Take(&source).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	var schedule CountdownReminderSchedule
	if err := tx.Where("countdown_id=? AND session_id=? AND binding_created_at=? AND revision=?", source.CountdownID, source.SessionID, source.BindingCreatedAt, source.Revision).Take(&schedule).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	var row Countdown
	if err := tx.Where("id=? AND session_id=? AND binding_created_at=?", source.CountdownID, source.SessionID, source.BindingCreatedAt).Take(&row).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	return countdownScheduleMatches(schedule, row), nil
}
