package dbop

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ReminderDeletionReceipt makes a natural-language batch delete safe to replay.
type ReminderDeletionReceipt struct {
	RequestKey string `gorm:"primaryKey;size:64"`
	SessionID  string `gorm:"index;size:160"`
	ActorID    int64
	IDsJSON    string    `gorm:"type:mediumtext"`
	CreatedAt  time.Time `gorm:"autoCreateTime;type:datetime(6)"`
}

type ReminderDeleteFilter struct {
	IDs           []string
	TitleContains string
	Date          string
	DateField     string
	DueFrom       *time.Time
	DueBefore     *time.Time
	Statuses      []string
	Mode          string
}

func (f ReminderDeleteFilter) valid() bool {
	if f.Mode != "single" && f.Mode != "all" {
		return false
	}
	if len(f.IDs) == 0 && strings.TrimSpace(f.TitleContains) == "" && f.Date == "" && f.DueFrom == nil && f.DueBefore == nil && len(f.Statuses) == 0 {
		return false
	}
	if len(f.IDs) > 200 || f.DateField != "" && f.DateField != "due" && f.DateField != "finished" && f.DateField != "created" {
		return false
	}
	if f.Date != "" {
		date, err := time.Parse("2006-01-02", f.Date)
		if err != nil || date.Format("2006-01-02") != f.Date {
			return false
		}
	}
	if f.DueFrom != nil && f.DueBefore != nil && !f.DueFrom.Before(*f.DueBefore) {
		return false
	}
	for _, status := range f.Statuses {
		if status != "pending" && status != "completed" && status != "reminded" && status != "cancelled" {
			return false
		}
	}
	return true
}

func reminderDeleteStatus(r Reminder) string {
	switch {
	case r.Status == ReminderCancelled:
		return "cancelled"
	case r.Status == ReminderCompleted || r.CompletedBy != nil:
		return "completed"
	case r.hasCompletedDelivery():
		return "reminded"
	default:
		return "pending"
	}
}

func reminderDeleteDate(r Reminder, field string) time.Time {
	switch field {
	case "created":
		return r.CreatedAt
	case "finished":
		if r.TaskCompletedAt != nil {
			return *r.TaskCompletedAt
		}
		if r.DeliveredAt != nil && r.Status != ReminderCompleted && r.Status != ReminderCancelled {
			return *r.DeliveredAt
		}
		return r.UpdatedAt
	default:
		return r.DueAt
	}
}

func (f ReminderDeleteFilter) matches(r Reminder) bool {
	if len(f.IDs) > 0 && !slices.Contains(f.IDs, r.ID) {
		return false
	}
	if f.TitleContains != "" && !strings.Contains(strings.ToLower(r.Title), strings.ToLower(strings.TrimSpace(f.TitleContains))) {
		return false
	}
	if len(f.Statuses) > 0 && !slices.Contains(f.Statuses, reminderDeleteStatus(r)) {
		return false
	}
	if f.DueFrom != nil && r.DueAt.Before(*f.DueFrom) || f.DueBefore != nil && !r.DueAt.Before(*f.DueBefore) {
		return false
	}
	if f.Date != "" {
		day := reminderDeleteDate(r, f.DateField)
		if day.IsZero() || day.In(time.FixedZone("Asia/Shanghai", 8*3600)).Format("2006-01-02") != f.Date {
			return false
		}
	}
	return true
}

// DeleteVisibleReminders removes selected cards and their memory projections in
// one transaction. A private card is visible only to its owning member.
func (db *DB) DeleteVisibleReminders(ctx context.Context, space string, actor int64, request string, filter ReminderDeleteFilter) ([]string, error) {
	if !db.enabled() {
		return nil, errNoDB
	}
	if request == "" || !filter.valid() {
		return nil, ErrReminderInvalid
	}
	key := "reminder_delete_" + ControlID(space, request)
	var deleted []string
	err := db.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		binding, err := bindingForSession(tx, space)
		if err != nil {
			return err
		}
		if binding == nil || actor != binding.UserA && actor != binding.UserB {
			return ErrReminderForbidden
		}
		var receipt ReminderDeletionReceipt
		if err := tx.Where("request_key=? AND session_id=?", key, space).First(&receipt).Error; err == nil {
			return json.Unmarshal([]byte(receipt.IDsJSON), &deleted)
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		sessions := []string{space}
		var private PrivateChannel
		if err := tx.Where("space_id=? AND owner_id=? AND binding_created_at=?", space, actor, binding.CreatedAt).First(&private).Error; err == nil {
			sessions = append(sessions, private.SessionID)
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var candidates []Reminder
		if err := tx.Where("session_id IN ? AND binding_created_at=? AND status != ?", sessions, binding.CreatedAt, ReminderDeleted).Order("created_at ASC,id ASC").Find(&candidates).Error; err != nil {
			return err
		}
		selected := make([]Reminder, 0)
		for _, row := range candidates {
			if filter.matches(row) {
				selected = append(selected, row)
				if len(selected) > 200 {
					return ErrReminderTooMany
				}
			}
		}
		if len(selected) == 0 {
			return ErrReminderNotFound
		}
		if filter.Mode == "single" && len(selected) != 1 {
			return ErrReminderAmbiguous
		}
		deleted = make([]string, 0, len(selected))
		for _, row := range selected {
			var current Reminder
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND session_id=? AND binding_created_at=?", row.ID, row.SessionID, binding.CreatedAt).First(&current).Error; err != nil {
				return err
			}
			if current.Status == ReminderDispatching || current.Status == ReminderDeleted || !filter.matches(current) {
				return ErrReminderState
			}
			if err := tx.Model(&current).Updates(map[string]any{"status": ReminderDeleted, "task_status": "cancelled"}).Error; err != nil {
				return err
			}
			current.Status, current.TaskStatus = ReminderDeleted, "cancelled"
			if err := syncReminderMemory(tx, &current); err != nil {
				return err
			}
			deleted = append(deleted, current.ID)
		}
		body, err := json.Marshal(deleted)
		if err != nil {
			return err
		}
		return tx.Create(&ReminderDeletionReceipt{RequestKey: key, SessionID: space, ActorID: actor, IDsJSON: string(body)}).Error
	})
	return deleted, err
}
