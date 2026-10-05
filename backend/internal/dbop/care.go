package dbop

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"strings"
	"tietie/backend/internal/memoryspace"
	"tietie/backend/internal/regions"
	"tietie/backend/internal/weather"
	"time"
)

var ErrCareRegion = errors.New("both members must set a region")
var ErrCareStale = errors.New("care settings, binding or member regions changed")

type CareMode struct {
	SessionID        string     `json:"-" gorm:"primaryKey;size:160"`
	Mode             string     `json:"mode" gorm:"primaryKey;size:32"`
	Enabled          bool       `json:"enabled" gorm:"index:idx_care_due,priority:1"`
	Time             string     `json:"time" gorm:"size:32"`
	NextDue          time.Time  `json:"nextDue" gorm:"type:datetime(6)"`
	RunAt            time.Time  `json:"-" gorm:"index:idx_care_due,priority:2;type:datetime(6)"`
	State            string     `json:"state" gorm:"size:32"`
	LastError        string     `json:"lastError,omitempty" gorm:"size:255"`
	LastDeliveredAt  *time.Time `json:"lastDeliveredAt,omitempty" gorm:"type:datetime(6)"`
	BindingCreatedAt time.Time  `json:"-" gorm:"type:datetime(6)"`
	Token            string     `json:"-" gorm:"size:64"`
	Revision         int64      `json:"-"`
}
type CareReport struct {
	ID               string         `json:"id" gorm:"primaryKey;size:64"`
	SessionID        string         `json:"-" gorm:"index:idx_care_reports,priority:1;size:160"`
	Mode             string         `json:"mode" gorm:"size:32"`
	Date             string         `json:"date" gorm:"size:32"`
	Text             string         `json:"text"`
	Cards            []weather.Card `json:"cards" gorm:"serializer:json;type:mediumtext"`
	CreatedAt        time.Time      `json:"createdAt" gorm:"index:idx_care_reports,priority:2;type:datetime(6)"`
	BindingCreatedAt time.Time      `json:"-" gorm:"type:datetime(6)"`
}
type CareMember struct {
	UserID        int64            `json:"userId"`
	Name          string           `json:"name"`
	Region        regions.Location `json:"region"`
	PreferenceKey string           `json:"-"`
}

func NextCareTime(now time.Time, clock string) time.Time {
	t, _ := time.Parse("15:04", clock)
	local := now.In(weather.Shanghai)
	due := time.Date(local.Year(), local.Month(), local.Day(), t.Hour(), t.Minute(), 0, 0, weather.Shanghai)
	if !due.After(now) {
		due = due.AddDate(0, 0, 1)
	}
	return due.UTC()
}
func ValidCareClock(clock string) bool {
	t, err := time.Parse("15:04", clock)
	return err == nil && t.Format("15:04") == clock
}
func careMembers(tx *gorm.DB, b Binding) ([]CareMember, error) {
	out := make([]CareMember, 0, 2)
	for _, id := range []int64{b.UserA, b.UserB} {
		var u User
		if err := tx.Where("id=?", id).First(&u).Error; err != nil {
			return nil, err
		}
		var p UserProfile
		if err := tx.Where("user_id=?", id).First(&p).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		var pref CarePreference
		if err := tx.Where("session_id=? AND user_id=? AND binding_created_at=?", b.SessionID, id, b.CreatedAt).First(&pref).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		name := p.Name
		if name == "" {
			name = u.Username
		}
		out = append(out, CareMember{UserID: id, Name: name, Region: p.Region, PreferenceKey: strings.Join(pref.Metrics, ",")})
	}
	return out, nil
}
func (db *DB) CareMembers(ctx context.Context, session string) ([]CareMember, *Binding, error) {
	var b Binding
	if err := db.gdb.WithContext(ctx).Where("session_id=?", session).First(&b).Error; err != nil {
		return nil, nil, err
	}
	members, err := careMembers(db.gdb.WithContext(ctx), b)
	return members, &b, err
}
func (db *DB) GetCareModes(ctx context.Context, session string) ([]CareMode, error) {
	var b Binding
	if err := db.gdb.WithContext(ctx).Where("session_id=?", session).First(&b).Error; err != nil {
		return nil, err
	}
	out := []CareMode{}
	for _, kind := range []string{"morning", "night"} {
		clock := "08:00"
		if kind == "night" {
			clock = "21:00"
		}
		mode := CareMode{SessionID: session, Mode: kind, Time: clock, State: "off"}
		var saved CareMode
		err := db.gdb.WithContext(ctx).Where("session_id=? AND mode=? AND binding_created_at=?", session, kind, b.CreatedAt).First(&saved).Error
		if err == nil {
			mode = saved
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		out = append(out, mode)
	}
	return out, nil
}
func (db *DB) SaveCareMode(ctx context.Context, session string, actor int64, kind, clock string, enabled bool, now time.Time) error {
	if (kind != "morning" && kind != "night") || !ValidCareClock(clock) {
		return errors.New("invalid care mode/time")
	}
	return db.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var b Binding
		if err := tx.Where("session_id=? AND (user_a=? OR user_b=?)", session, actor, actor).First(&b).Error; err != nil {
			return err
		}
		members, err := careMembers(tx, b)
		if err != nil {
			return err
		}
		if enabled {
			for _, m := range members {
				if m.Region.CityCode == "" {
					return ErrCareRegion
				}
				if _, _, err := weather.Locate(m.Region); err != nil {
					return err
				}
			}
		}
		var previous CareMode
		err = tx.Where("session_id=? AND mode=?", session, kind).First(&previous).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil && previous.BindingCreatedAt.Equal(b.CreatedAt) && previous.Enabled == enabled && previous.Time == clock {
			return nil
		}
		mode := CareMode{SessionID: session, Mode: kind, Time: clock, Enabled: enabled, BindingCreatedAt: b.CreatedAt, Revision: previous.Revision + 1, State: "off", NextDue: epochTime, RunAt: epochTime}
		if enabled {
			mode.State = "scheduled"
			mode.NextDue = NextCareTime(now, clock)
			mode.RunAt = mode.NextDue
		}
		if previous.BindingCreatedAt.Equal(b.CreatedAt) {
			mode.LastDeliveredAt = previous.LastDeliveredAt
		}
		if err := tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&mode).Error; err != nil {
			return err
		}
		if err := syncCareHabitsRoot(tx, b); err != nil {
			return err
		}
		label := "早安提醒"
		if kind == "night" {
			label = "晚安提醒"
		}
		fact, _ := json.Marshal(map[string]any{"content": fmt.Sprintf("共享空间%s：开启=%t，每天 %s（Asia/Shanghai）。后台定时执行，地区以双方当前账号资料为准。", label, enabled, clock), "sourceType": "care_settings", "confirmation": "提醒页已设置", "schedule": "daily", "preferredTime": clock, "enabled": enabled, "mode": kind, "updatedAt": now})
		return enqueueMemory(tx, MemoryRecord{SessionID: session, Path: memoryspace.FactPath("habit", "space", 0, "care_"+kind), Scope: "space", SourceUserID: actor, Category: "habit", PendingContent: string(fact), Operation: "upsert", BindingCreatedAt: b.CreatedAt})
	})
}

// Keep the existing habits template and its history; expose only two current
// mode settings in the root, while revisions live in ordinary habit pages.
func syncCareHabitsRoot(tx *gorm.DB, b Binding) error {
	path := memoryspace.TemplatePath("habit")
	fresh, err := memoryspace.Template(path)
	if err != nil {
		return err
	}
	var doc, base map[string]any
	if err := json.Unmarshal([]byte(fresh), &base); err != nil {
		return err
	}
	doc = base
	var root MemoryRecord
	err = tx.Where("id=?", MemoryID(b.SessionID, path)).First(&root).Error
	if err == nil {
		doc = map[string]any{}
		if err := json.Unmarshal([]byte(root.Content), &doc); err != nil {
			return err
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	doc["maintenance"] = base["maintenance"]
	var modes []CareMode
	if err := tx.Where("session_id=? AND binding_created_at=?", b.SessionID, b.CreatedAt).Order("mode ASC").Find(&modes).Error; err != nil {
		return err
	}
	current := []any{}
	for _, mode := range modes {
		current = append(current, map[string]any{"mode": mode.Mode, "enabled": mode.Enabled, "preferredTime": mode.Time, "timezone": "Asia/Shanghai", "schedule": "daily", "sourceType": "care_settings"})
	}
	doc["careModes"] = current
	content, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	return queueHistoryDocument(tx, b.SessionID, path, "template", string(content), b.CreatedAt, false)
}

// Reserve only due rows on an indexed queue; leases survive process restarts.
func (db *DB) ClaimCareModes(ctx context.Context, now time.Time, limit int) ([]CareMode, error) {
	if limit <= 0 || limit > 64 {
		limit = 8
	}
	out := []CareMode{}
	err := claimWithLock(ctx, db.gdb, claimLockCareModes, func(tx *gorm.DB) error {
		var due []CareMode
		if err := tx.Where("enabled=1 AND run_at<=? AND EXISTS (SELECT 1 FROM bindings b WHERE b.session_id=care_modes.session_id AND b.created_at=care_modes.binding_created_at)", now.UTC()).Order("run_at ASC").Limit(limit).Find(&due).Error; err != nil {
			return err
		}
		for _, m := range due {
			// Never replay yesterday's greeting after a long outage.
			if now.Sub(m.NextDue) > 2*time.Hour {
				next := NextCareTime(now, m.Time)
				if err := tx.Model(&m).Updates(map[string]any{"next_due": next, "run_at": next, "state": "scheduled", "token": "", "last_error": "错过本次时间，已安排下一次"}).Error; err != nil {
					return err
				}
				continue
			}
			bytes := make([]byte, 16)
			if _, err := rand.Read(bytes); err != nil {
				return err
			}
			m.Token = hex.EncodeToString(bytes)
			m.RunAt = now.Add(3 * time.Minute).UTC()
			m.State = "running"
			if err := tx.Model(&m).Updates(map[string]any{"token": m.Token, "run_at": m.RunAt, "state": m.State}).Error; err != nil {
				return err
			}
			out = append(out, m)
		}
		return nil
	})
	return out, err
}
func (db *DB) FailCareMode(ctx context.Context, job CareMode, message, state string, now time.Time) error {
	return db.gdb.WithContext(ctx).Model(&CareMode{}).Where("session_id=? AND mode=? AND token=? AND revision=?", job.SessionID, job.Mode, job.Token, job.Revision).Updates(map[string]any{"state": state, "last_error": message, "run_at": now.Add(5 * time.Minute).UTC(), "token": ""}).Error
}
func (db *DB) CompleteCareReport(ctx context.Context, job CareMode, members []CareMember, report CareReport, now time.Time) error {
	return db.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current CareMode
		if err := tx.Where("session_id=? AND mode=? AND enabled=1 AND token=? AND revision=?", job.SessionID, job.Mode, job.Token, job.Revision).First(&current).Error; err != nil {
			return ErrCareStale
		}
		var b Binding
		if err := tx.Where("session_id=? AND created_at=?", job.SessionID, job.BindingCreatedAt).First(&b).Error; err != nil {
			return ErrCareStale
		}
		actual, err := careMembers(tx, b)
		if err != nil {
			return err
		}
		if len(actual) != len(members) {
			return ErrCareStale
		}
		for i, m := range actual {
			if m != members[i] {
				return ErrCareStale
			}
		}
		report.SessionID, report.Mode, report.BindingCreatedAt, report.CreatedAt = job.SessionID, job.Mode, b.CreatedAt, now.UTC()
		report.Date = job.NextDue.In(weather.Shanghai).Format("2006-01-02")
		report.ID = "evt_care_" + MemoryID(job.SessionID, job.Mode+report.Date+b.CreatedAt.Format(time.RFC3339Nano))
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&report)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected > 0 {
			if err := db.enqueueWechatCare(tx, report, b, job.NextDue); err != nil {
				return err
			}
			expires := time.Date(now.In(weather.Shanghai).Year(), now.In(weather.Shanghai).Month(), now.In(weather.Shanghai).Day()+1, 0, 0, 0, 0, weather.Shanghai)
			if job.Mode == "night" {
				expires = expires.AddDate(0, 0, 1)
			}
			fact, _ := json.Marshal(map[string]any{"content": report.Text, "cards": report.Cards, "category": "天气", "sourceType": "weather_care", "source": "和风天气", "generatedAt": now, "expiresAt": expires, "ownerId": 0, "confirmation": "后台查询的模式预报"})
			if err := enqueueMemory(tx, MemoryRecord{SessionID: job.SessionID, Path: memoryspace.FactPath("realtime", "space", 0, report.ID), Scope: "space", Category: "realtime", ExpiresAt: &expires, PendingContent: string(fact), Operation: "upsert", BindingCreatedAt: b.CreatedAt}); err != nil {
				return err
			}
		}
		next := NextCareTime(now, job.Time)
		return tx.Model(&current).Updates(map[string]any{"next_due": next, "run_at": next, "state": "scheduled", "last_error": "", "token": "", "last_delivered_at": now.UTC()}).Error
	})
}

// Weather comparisons need previous forecasts even when many anniversary
// notices have been added to the shared message history in the meantime.
func (db *DB) ListWeatherCareReports(ctx context.Context, session string, limit int) ([]CareReport, error) {
	if limit <= 0 || limit > 100 {
		limit = 8
	}
	rows := []CareReport{}
	err := db.gdb.WithContext(ctx).Where("session_id=? AND binding_created_at=(SELECT created_at FROM bindings WHERE session_id=?) AND mode IN ('morning','night')", session, session).Order("created_at DESC,id DESC").Limit(limit).Find(&rows).Error
	return rows, err
}

func (db *DB) ListCareReports(ctx context.Context, session string, limit int, after ...string) ([]CareReport, error) {
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	rows := []CareReport{}
	query := db.gdb.WithContext(ctx).Where("session_id=? AND binding_created_at=(SELECT created_at FROM bindings WHERE session_id=?)", session, session)
	if len(after) > 0 && after[0] != "" {
		var cursor CareReport
		err := query.Session(&gorm.Session{}).Where("id=?", after[0]).First(&cursor).Error
		if err == nil {
			err = query.Where("created_at>? OR (created_at=? AND id>?)", cursor.CreatedAt, cursor.CreatedAt, cursor.ID).Order("created_at ASC,id ASC").Limit(limit).Find(&rows).Error
			return rows, err
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	}
	err := query.Order("created_at DESC,id DESC").Limit(limit).Find(&rows).Error
	return rows, err
}
func (db *DB) CareTodayReminders(ctx context.Context, session string, epoch time.Time, now time.Time) ([]Reminder, error) {
	local := now.In(weather.Shanghai)
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, weather.Shanghai)
	var rows []Reminder
	err := db.gdb.WithContext(ctx).Where("session_id=? AND binding_created_at=? AND (visibility IS NULL OR visibility!='private') AND status IN ('scheduled','dispatching','uncertain','failed') AND due_at>=? AND due_at<?", session, epoch, start.UTC(), start.AddDate(0, 0, 1).UTC()).Order("due_at ASC").Limit(101).Find(&rows).Error
	return rows, err
}
