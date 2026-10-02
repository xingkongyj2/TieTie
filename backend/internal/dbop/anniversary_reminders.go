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

// One shared preference and an indexed daily queue per binding. Delivery is a
// local transaction, so a restart cannot duplicate a notice or lose its history.
type AnniversaryReminderSettings struct {
	SessionID        string    `json:"-" gorm:"primaryKey"`
	Enabled          bool      `json:"enabled" gorm:"index:idx_anniversary_reminder_due,priority:1"`
	NextDue          time.Time `json:"nextDue"`
	RunAt            time.Time `json:"-" gorm:"index:idx_anniversary_reminder_due,priority:2"`
	BindingCreatedAt time.Time `json:"-"`
	Revision         int64     `json:"-"`
	Token            string    `json:"-"`
}

func anniversaryReminderTime(now time.Time) time.Time {
	local := now.In(weather.Shanghai)
	due := time.Date(local.Year(), local.Month(), local.Day(), 8, 0, 0, 0, weather.Shanghai)
	// Enabling after 08:00 still checks today's three-day notice. Never send
	// late-night catch-up messages or replay days missed during an outage.
	if local.Hour() >= 23 {
		due = due.AddDate(0, 0, 1)
	}
	return due.UTC()
}

func (db *DB) GetAnniversaryReminderSettings(ctx context.Context, session string) (*AnniversaryReminderSettings, error) {
	b, err := db.GetBindingBySessionID(ctx, session)
	if err != nil {
		return nil, err
	}
	if b == nil {
		return nil, ErrReminderForbidden
	}
	row := AnniversaryReminderSettings{SessionID: session}
	err = db.gdb.WithContext(ctx).Where("session_id=? AND binding_created_at=?", session, b.CreatedAt).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &AnniversaryReminderSettings{SessionID: session}, nil
	}
	return &row, err
}

func (db *DB) SaveAnniversaryReminderSettings(ctx context.Context, session string, actor int64, enabled bool, now time.Time) error {
	return db.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		b, err := bindingForSession(tx, session)
		if err != nil {
			return err
		}
		if b == nil || (actor != b.UserA && actor != b.UserB) {
			return ErrReminderForbidden
		}
		var previous AnniversaryReminderSettings
		err = tx.Where("session_id=?", session).First(&previous).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil && previous.BindingCreatedAt.Equal(b.CreatedAt) && previous.Enabled == enabled {
			return nil
		}
		row := AnniversaryReminderSettings{SessionID: session, Enabled: enabled, BindingCreatedAt: b.CreatedAt, Revision: previous.Revision + 1}
		if enabled {
			row.NextDue = anniversaryReminderTime(now)
			row.RunAt = row.NextDue
		}
		if err := tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&row).Error; err != nil {
			return err
		}
		fact, _ := json.Marshal(map[string]any{"content": fmt.Sprintf("共享空间纪念日提醒：开启=%t，每年纪念日前3天北京时间08:00由后台向双方发送群内提示；仅提醒共享纪念日卡片，2月29日在非闰年按2月28日。", enabled), "enabled": enabled, "sourceType": "anniversary_reminder_settings", "confirmation": "贴贴页已设置", "updatedAt": now})
		return enqueueMemory(tx, MemoryRecord{SessionID: session, Path: memoryspace.FactPath("habit", "space", 0, "anniversary_reminders"), Scope: "space", Category: "habit", SourceUserID: actor, PendingContent: string(fact), Operation: "upsert", BindingCreatedAt: b.CreatedAt})
	})
}

func (db *DB) ClaimAnniversaryReminders(ctx context.Context, now time.Time, limit int) ([]AnniversaryReminderSettings, error) {
	if limit <= 0 || limit > 64 {
		limit = 8
	}
	jobs := []AnniversaryReminderSettings{}
	err := db.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var rows []AnniversaryReminderSettings
		if err := tx.Where("enabled=1 AND run_at<=? AND EXISTS (SELECT 1 FROM bindings b WHERE b.session_id=anniversary_reminder_settings.session_id AND b.created_at=anniversary_reminder_settings.binding_created_at)", now.UTC()).Order("run_at ASC").Limit(limit).Find(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			local := now.In(weather.Shanghai)
			if row.NextDue.In(weather.Shanghai).Format("2006-01-02") != local.Format("2006-01-02") || local.Hour() >= 23 {
				row.NextDue = anniversaryReminderTime(now)
				row.Token = ""
				row.RunAt = row.NextDue
				if err := tx.Save(&row).Error; err != nil {
					return err
				}
				if row.NextDue.After(now) {
					continue
				}
			}
			bytes := make([]byte, 16)
			if _, err := rand.Read(bytes); err != nil {
				return err
			}
			row.Token = hex.EncodeToString(bytes)
			row.RunAt = now.Add(3 * time.Minute).UTC()
			if err := tx.Save(&row).Error; err != nil {
				return err
			}
			jobs = append(jobs, row)
		}
		return nil
	})
	return jobs, err
}

func (db *DB) DeliverAnniversaryReminders(ctx context.Context, job AnniversaryReminderSettings, now time.Time) error {
	return db.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current AnniversaryReminderSettings
		if err := tx.Where("session_id=? AND enabled=1 AND token=? AND revision=? AND binding_created_at=?", job.SessionID, job.Token, job.Revision, job.BindingCreatedAt).First(&current).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return nil // Disabled or superseded while this worker was waiting.
		} else if err != nil {
			return err
		}
		b, err := bindingForSession(tx, job.SessionID)
		if err != nil {
			return err
		}
		if b == nil || !b.CreatedAt.Equal(job.BindingCreatedAt) {
			return nil
		}
		local := now.In(weather.Shanghai)
		if job.NextDue.In(weather.Shanghai).Format("2006-01-02") == local.Format("2006-01-02") && local.Hour() >= 8 && local.Hour() < 23 {
			target := time.Date(local.Year(), local.Month(), local.Day()+3, 0, 0, 0, 0, weather.Shanghai)
			monthDays := []string{target.Format("01-02")}
			if target.Month() == time.February && target.Day() == 28 && target.AddDate(0, 0, 1).Month() == time.March {
				monthDays = append(monthDays, "02-29")
			}
			var anniversaries []Anniversary
			if err := tx.Where("session_id=? AND binding_created_at=? AND substr(date,6,5) IN ? AND date<=?", job.SessionID, b.CreatedAt, monthDays, target.Format("2006-01-02")).Order("id ASC").Find(&anniversaries).Error; err != nil {
				return err
			}
			members, err := careMembers(tx, *b)
			if err != nil {
				return err
			}
			names := []string{}
			for _, member := range members {
				names = append(names, "@"+member.Name)
			}
			for _, anniversary := range anniversaries {
				text := fmt.Sprintf("%s 还有 3 天就是「%s」啦 🎉\n%s，一起记得这个特别的日子。", strings.Join(names, " "), anniversary.Title, target.Format("2006年1月2日"))
				if strings.HasSuffix(anniversary.Date, "02-29") && target.Day() == 28 {
					text += "\n今年不是闰年，2月29日按2月28日提醒。"
				}
				report := CareReport{ID: "evt_care_ann_" + MemoryID(job.SessionID, anniversary.ID+target.Format("2006-01-02")+b.CreatedAt.Format(time.RFC3339Nano)), SessionID: job.SessionID, Mode: "anniversary", Date: target.Format("2006-01-02"), Text: text, Cards: []weather.Card{}, BindingCreatedAt: b.CreatedAt, CreatedAt: now.UTC()}
				result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&report)
				if result.Error != nil {
					return result.Error
				}
				if result.RowsAffected > 0 {
					expires := target.AddDate(0, 0, 1)
					fact, _ := json.Marshal(map[string]any{"content": text, "sourceType": "anniversary_reminder", "anniversaryId": anniversary.ID, "date": report.Date, "expiresAt": expires})
					if err := enqueueMemory(tx, MemoryRecord{SessionID: job.SessionID, Path: memoryspace.FactPath("realtime", "space", 0, report.ID), Scope: "space", Category: "realtime", ExpiresAt: &expires, PendingContent: string(fact), Operation: "upsert", BindingCreatedAt: b.CreatedAt}); err != nil {
						return err
					}
				}
			}
		}
		next := NextCareTime(now, "08:00")
		return tx.Model(&current).Updates(map[string]any{"next_due": next, "run_at": next, "token": ""}).Error
	})
}
