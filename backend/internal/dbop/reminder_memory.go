package dbop

import (
	"context"
	"encoding/json"
	"fmt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"tietie/backend/internal/conversation"
	"tietie/backend/internal/memoryspace"
	"time"
)

// ReminderMemory is factual long-term memory backed by a real reminder. It is
// updated in the same transaction as the reminder; it never claims the activity
// itself is done just because a notification was delivered.
type ReminderMemory struct {
	ReminderID   string                   `json:"reminderId" gorm:"primaryKey;size:64"`
	SessionID    string                   `json:"-" gorm:"not null;index:idx_reminder_memory_space,priority:1;size:160"`
	Title        string                   `json:"title"`
	DueAt        time.Time                `json:"dueAt" gorm:"type:datetime(6)"`
	Recurrence   *conversation.Recurrence `json:"recurrence,omitempty" gorm:"serializer:json;type:mediumtext"`
	SeriesID     string                   `json:"seriesId,omitempty" gorm:"size:64"`
	Occurrence   int                      `json:"occurrence,omitempty"`
	RecipientIDs []int64                  `json:"recipientIds" gorm:"serializer:json;type:mediumtext"`
	CreatedBy    int64                    `json:"createdBy"`
	Status       string                   `json:"status"`
	TaskStatus   string                   `json:"taskStatus"`
	DeliveredAt  *time.Time               `json:"deliveredAt,omitempty" gorm:"type:datetime(6)"`
	CompletedBy  *int64                   `json:"completedBy,omitempty"`
	UpdatedAt    time.Time                `json:"updatedAt" gorm:"index:idx_reminder_memory_space,priority:2;type:datetime(6)"`
}

func (ReminderMemory) TableName() string { return "reminder_memories" }
func syncReminderMemory(tx *gorm.DB, r *Reminder) error {
	if r.Status == ReminderDeleted {
		if err := tx.Where("reminder_id = ?", r.ID).Delete(&ReminderMemory{}).Error; err != nil {
			return err
		}
		return syncReminderHistory(tx, r)
	}
	m := ReminderMemory{ReminderID: r.ID, SessionID: r.SessionID, Title: r.Title, DueAt: r.DueAt,
		Recurrence: r.Recurrence, SeriesID: r.SeriesID, Occurrence: r.Occurrence,
		RecipientIDs: r.RecipientIDs, CreatedBy: r.CreatedBy, Status: r.Status, TaskStatus: r.TaskStatus,
		DeliveredAt: r.DeliveredAt, CompletedBy: r.CompletedBy, UpdatedAt: time.Now().UTC()}
	if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "reminder_id"}}, UpdateAll: true}).Create(&m).Error; err != nil {
		return err
	}
	return syncReminderHistory(tx, r)
}
func (db *DB) ListReminderMemories(ctx context.Context, id string) ([]ReminderMemory, error) {
	if !db.enabled() {
		return nil, errNoDB
	}
	memories := make([]ReminderMemory, 0)
	err := db.gdb.WithContext(ctx).Where("session_id = ? AND status != ?", id, ReminderDeleted).Order("updated_at DESC").Limit(20).Find(&memories).Error
	return memories, err
}

// NextScheduledReminder uses the same ordered index as the due queue.
func (db *DB) NextScheduledReminder(ctx context.Context) (*Reminder, error) {
	if !db.enabled() {
		return nil, errNoDB
	}
	var r Reminder
	err := db.gdb.WithContext(ctx).Where("status = ?", ReminderScheduled).Order("run_at ASC, id ASC").First(&r).Error
	return firstOrNil(&r, err)
}

// The root is a bounded view. Complete records live in stable monthly pages.
func syncTodoBoard(tx *gorm.DB, session string, epoch time.Time, allowUnbound bool) error {
	var root MemoryRecord
	if err := tx.Where("id=?", MemoryID(session, memoryspace.TodoPath)).First(&root).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			root.Content = memoryspace.TodoTemplate()
		} else {
			return err
		}
	}
	var upcoming, recent []ReminderHistoryLocation
	if err := tx.Where("session_id=? AND board_status='pending'", session).Order("due_at ASC,reminder_id ASC").Limit(TodoBoardLimit + 1).Find(&upcoming).Error; err != nil {
		return err
	}
	if err := tx.Where("session_id=? AND board_status != ?", session, ReminderDeleted).Order("created_at DESC,reminder_id DESC").Limit(TodoRecentLimit).Find(&recent).Error; err != nil {
		return err
	}
	var board map[string]any
	if err := json.Unmarshal([]byte(memoryspace.TodoTemplate()), &board); err != nil {
		return err
	}
	board["hasMore"] = len(upcoming) > TodoBoardLimit
	if len(upcoming) > TodoBoardLimit {
		upcoming = upcoming[:TodoBoardLimit]
	}
	items, err := boardItems(tx, upcoming)
	if err != nil {
		return err
	}
	recentItems, err := boardItems(tx, recent)
	if err != nil {
		return err
	}
	board["reminders"] = items
	board["recentReminders"] = recentItems
	var months []struct {
		Month       string
		RecordCount int64
		PageCount   int64
	}
	if err := tx.Model(&ReminderHistoryLocation{}).Select("month, COUNT(*) AS record_count, MAX(page) + 1 AS page_count").Where("session_id=? AND board_status != ?", session, ReminderDeleted).Group("month").Order("month DESC").Limit(13).Scan(&months).Error; err != nil {
		return err
	}
	more := len(months) > 12
	if more {
		months = months[:12]
	}
	itemsHistory := make([]map[string]any, 0, len(months))
	for _, month := range months {
		itemsHistory = append(itemsHistory, map[string]any{"month": month.Month, "recordCount": month.RecordCount, "pageCount": month.PageCount})
	}
	history := board["history"].(map[string]any)
	history["months"], history["hasEarlierMonths"] = itemsHistory, more
	if len(months) > 0 {
		var earliest ReminderHistoryLocation
		if err := tx.Where("session_id=? AND board_status != ?", session, ReminderDeleted).Order("month ASC").First(&earliest).Error; err != nil {
			return err
		}
		history["earliestMonth"], history["latestMonth"] = earliest.Month, months[0].Month
	}
	body, _ := json.Marshal(board)
	return queueHistoryDocument(tx, session, memoryspace.TodoPath, "template", string(body), epoch, allowUnbound)
}

func boardItems(tx *gorm.DB, locations []ReminderHistoryLocation) ([]map[string]any, error) {
	items := make([]map[string]any, 0, len(locations))
	if len(locations) == 0 {
		return items, nil
	}
	ids := make([]string, 0, len(locations))
	for _, l := range locations {
		ids = append(ids, l.ReminderID)
	}
	var rows []Reminder
	if err := tx.Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	byID := map[string]Reminder{}
	for _, r := range rows {
		byID[r.ID] = r
	}
	for _, l := range locations {
		r, ok := byID[l.ReminderID]
		if !ok {
			return nil, fmt.Errorf("missing board reminder %s", l.ReminderID)
		}
		items = append(items, map[string]any{"id": r.ID, "title": r.Title, "dueAt": r.DueAt, "recurrence": r.Recurrence, "seriesId": r.SeriesID, "occurrence": r.Occurrence, "status": reminderBoardStatus(&r), "createdBy": r.CreatedBy, "recipientIds": r.RecipientIDs, "deliveryStatus": r.Status, "taskStatus": r.TaskStatus, "completedBy": r.CompletedBy, "historyPath": l.Path()})
	}
	return items, nil
}
