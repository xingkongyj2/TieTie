package dbop

import (
	"context"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"strings"
	"tietie/backend/internal/memoryspace"
	"tietie/backend/internal/weather"
	"time"
	"unicode/utf8"
)

type Countdown struct {
	ID               string    `json:"id" gorm:"primaryKey;size:64"`
	SessionID        string    `json:"-" gorm:"index:idx_countdowns,priority:1;size:160"`
	Title            string    `json:"title"`
	Date             string    `json:"date"`
	Repeat           string    `json:"repeat"`
	Kind             string    `json:"kind"`
	CreatedBy        int64     `json:"createdBy"`
	UpdatedBy        int64     `json:"updatedBy"`
	BindingCreatedAt time.Time `json:"-" gorm:"type:datetime(6)"`
	CreatedAt        time.Time `json:"createdAt" gorm:"index:idx_countdowns,priority:2,sort:desc;type:datetime(6)"`
	UpdatedAt        time.Time `json:"updatedAt" gorm:"type:datetime(6)"`
	DaysRemaining    int       `json:"daysRemaining" gorm:"-"`
	NextDate         string    `json:"nextDate" gorm:"-"`
	Expired          bool      `json:"expired" gorm:"-"`
	LeapAdjusted     bool      `json:"leapAdjusted" gorm:"-"`
}
type CountdownReceipt struct {
	ID          string `gorm:"primaryKey;size:64"`
	SessionID   string `gorm:"index;size:160"`
	CountdownID string `gorm:"index;size:64"`
	Operation   string
	Snapshot    string    `gorm:"type:mediumtext"`
	CreatedAt   time.Time `gorm:"type:datetime(6)"`
}

var ErrCountdownInvalid = errors.New("请填写倒计时名称、有效日期和重复方式")

func CountdownMemoryPath(id string) string {
	return memoryspace.FactPath("agreement", "space", 0, "countdown_"+id)
}
func CalculateCountdown(row Countdown, now time.Time) Countdown {
	local := now.In(weather.Shanghai)
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, weather.Shanghai)
	date, err := time.ParseInLocation("2006-01-02", row.Date, weather.Shanghai)
	if err != nil {
		return row
	}
	if row.Repeat == "annual" {
		month, sourceDay := date.Month(), date.Day()
		year := today.Year()
		if date.Year() > year {
			year = date.Year() // The first occurrence cannot precede its source date.
		}
		occurrence := func(year int) time.Time {
			day := sourceDay
			if month == time.February && day == 29 && time.Date(year, 3, 0, 0, 0, 0, 0, weather.Shanghai).Day() == 28 {
				day = 28
			}
			return time.Date(year, month, day, 0, 0, 0, 0, weather.Shanghai)
		}
		date = occurrence(year)
		if date.Before(today) {
			date = occurrence(year + 1)
		}
		row.LeapAdjusted = strings.HasSuffix(row.Date, "02-29") && date.Day() == 28
	}
	row.NextDate = date.Format("2006-01-02")
	row.DaysRemaining = int(date.Sub(today).Hours() / 24)
	row.Expired = row.DaysRemaining < 0
	return row
}
func (db *DB) ApplyCountdown(ctx context.Context, session, request string, actor int64, epoch time.Time, id, title, date, repeat, kind string, now time.Time) (*Countdown, error) {
	title = strings.TrimSpace(title)
	if kind == "" {
		kind = "other"
	}
	if repeat == "" {
		repeat = "auto"
	}
	if title == "" || utf8.RuneCountInString(title) > 80 || !memoryspace.ValidAnniversaryDate(date) || (repeat != "auto" && repeat != "once" && repeat != "annual") || (kind != "birthday" && kind != "deadline" && kind != "other") || len(id) > 160 {
		return nil, ErrCountdownInvalid
	}
	if repeat == "auto" {
		repeat = "once"
		if date < now.In(weather.Shanghai).Format("2006-01-02") {
			repeat = "annual"
		}
	}
	var row Countdown
	err := db.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		b, err := profileActorBinding(tx, session, actor, actor, epoch)
		if err != nil {
			return err
		}
		receiptID := "countdown_save_" + ControlID(session, request+b.CreatedAt.String())
		var receipt CountdownReceipt
		if err := tx.Where("id=?", receiptID).First(&receipt).Error; err == nil {
			return json.Unmarshal([]byte(receipt.Snapshot), &row)
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		query := tx.Where("session_id=? AND binding_created_at=?", session, b.CreatedAt)
		if id != "" {
			if err := query.Where("id=?", id).First(&row).Error; err != nil {
				return err
			}
		} else {
			if err := query.Where("title=? AND date=?", title, date).First(&row).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		if row.ID == "" {
			row = Countdown{ID: "cd_" + strings.TrimPrefix(ControlID(session, request+b.CreatedAt.String()), "ctl_"), SessionID: session, CreatedBy: actor, BindingCreatedAt: b.CreatedAt}
		}
		if row.Title != title || row.Date != date || row.Repeat != repeat || row.Kind != kind {
			row.Title, row.Date, row.Repeat, row.Kind, row.UpdatedBy = title, date, repeat, kind, actor
			if err := tx.Save(&row).Error; err != nil {
				return err
			}
			if err := syncCountdown(tx, row, "upsert"); err != nil {
				return err
			}
		}
		if err := syncCountdownReminderSchedule(tx, row, now); err != nil {
			return err
		}
		body, _ := json.Marshal(row)
		return tx.Create(&CountdownReceipt{ID: receiptID, SessionID: session, CountdownID: row.ID, Operation: "save", Snapshot: string(body)}).Error
	})
	row = CalculateCountdown(row, now)
	return &row, err
}
func syncCountdown(tx *gorm.DB, row Countdown, operation string) error {
	body, _ := json.Marshal(map[string]any{"entryType": "countdown", "countdownId": row.ID, "title": row.Title, "date": row.Date, "repeat": row.Repeat, "kind": row.Kind, "content": "倒计时「" + row.Title + "」，日期 " + row.Date + "，重复方式 " + row.Repeat, "createdBy": row.CreatedBy, "updatedBy": row.UpdatedBy, "confirmation": "用户明确设置", "updatedAt": row.UpdatedAt})
	return enqueueMemory(tx, MemoryRecord{SessionID: row.SessionID, Path: CountdownMemoryPath(row.ID), Scope: "space", Category: "agreement", SourceUserID: row.UpdatedBy, Storage: "database_and_memory", Operation: operation, PendingContent: string(body), BindingCreatedAt: row.BindingCreatedAt})
}
func (db *DB) DeleteCountdown(ctx context.Context, session, request string, actor int64, epoch time.Time, id string) (*Countdown, error) {
	if id == "" || len(id) > 160 {
		return nil, ErrCountdownInvalid
	}
	var row Countdown
	err := db.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		b, err := profileActorBinding(tx, session, actor, actor, epoch)
		if err != nil {
			return err
		}
		key := "countdown_delete_" + ControlID(session, request+b.CreatedAt.String())
		var receipt CountdownReceipt
		if err := tx.Where("id=?", key).First(&receipt).Error; err == nil {
			if receipt.CountdownID != id {
				return ErrCountdownInvalid
			}
			return json.Unmarshal([]byte(receipt.Snapshot), &row)
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err := tx.Where("id=? AND session_id=? AND binding_created_at=?", id, session, b.CreatedAt).First(&row).Error; err != nil {
			return err
		}
		if err := tx.Delete(&row).Error; err != nil {
			return err
		}
		if err := tx.Where("countdown_id=? AND binding_created_at=?", row.ID, b.CreatedAt).Delete(&CountdownReminderSchedule{}).Error; err != nil {
			return err
		}
		row.UpdatedBy = actor
		if err := syncCountdown(tx, row, "delete"); err != nil {
			return err
		}
		body, _ := json.Marshal(row)
		return tx.Create(&CountdownReceipt{ID: key, SessionID: session, CountdownID: id, Operation: "delete", Snapshot: string(body)}).Error
	})
	return &row, err
}
func (db *DB) ListCountdowns(ctx context.Context, session, after string, now time.Time) ([]Countdown, string, error) {
	b, err := bindingForSession(db.gdb.WithContext(ctx), session)
	if err != nil {
		return nil, "", err
	}
	if b == nil {
		return nil, "", ErrReminderForbidden
	}
	query := db.gdb.WithContext(ctx).Where("session_id=? AND binding_created_at=?", session, b.CreatedAt)
	if after != "" {
		var cursor Countdown
		if err := db.gdb.WithContext(ctx).Where("session_id=? AND binding_created_at=? AND id=?", session, b.CreatedAt, after).First(&cursor).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			var receipt CountdownReceipt
			if err := db.gdb.WithContext(ctx).Where("session_id=? AND countdown_id=? AND operation='delete'", session, after).First(&receipt).Error; err != nil {
				return nil, "", ErrCountdownInvalid
			}
			if err := json.Unmarshal([]byte(receipt.Snapshot), &cursor); err != nil {
				return nil, "", err
			}
		} else if err != nil {
			return nil, "", err
		}
		query = query.Where("created_at<? OR (created_at=? AND id<?)", cursor.CreatedAt, cursor.CreatedAt, cursor.ID)
	}
	rows := []Countdown{}
	if err := query.Order("created_at DESC,id DESC").Limit(51).Find(&rows).Error; err != nil {
		return nil, "", err
	}
	next := ""
	if len(rows) > 50 {
		rows = rows[:50]
		next = rows[len(rows)-1].ID
	}
	for i := range rows {
		rows[i] = CalculateCountdown(rows[i], now)
	}
	return rows, next, nil
}
