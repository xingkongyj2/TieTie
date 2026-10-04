package dbop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"tietie/backend/internal/memoryspace"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	ReminderHistoryPageSize  = 16
	TodoBoardLimit           = 16
	TodoRecentLimit          = 8
	ReminderHistoryIndexPath = memoryspace.TodoPath
)

// Positions are append-only. Completing/cancelling an old reminder never moves
// other rows or rewrites the rest of a month's archive.
type ReminderHistoryLocation struct {
	SessionID   string    `gorm:"primaryKey;index:idx_history_page,priority:1;index:idx_history_board,priority:1;index:idx_history_recent,priority:1;size:160"`
	ReminderID  string    `gorm:"primaryKey;index:idx_history_board,priority:4;index:idx_history_recent,priority:3;size:64"`
	Month       string    `gorm:"index:idx_history_page,priority:2;size:32"`
	Page        int       `gorm:"index:idx_history_page,priority:3"`
	Position    int64     `gorm:"index:idx_history_page,priority:4"`
	BoardStatus string    `gorm:"index:idx_history_board,priority:2;size:32"`
	DueAt       time.Time `gorm:"index:idx_history_board,priority:3;type:datetime(6)"`
	CreatedAt   time.Time `gorm:"index:idx_history_recent,priority:2;type:datetime(6)"`
}

func (ReminderHistoryLocation) TableName() string { return "reminder_history_locations" }
func (l ReminderHistoryLocation) Path() string {
	return fmt.Sprintf("tasks/todo-board/%s/%06d.json", l.Month, l.Page+1)
}

type ReminderHistoryMonth struct {
	SessionID   string `gorm:"primaryKey;size:160"`
	Month       string `gorm:"primaryKey;size:32"`
	RecordCount int64
}

func (ReminderHistoryMonth) TableName() string { return "reminder_history_months" }

func reminderBoardStatus(r *Reminder) string {
	if r.Status == ReminderDeleted {
		return ReminderDeleted
	}
	if r.Status == ReminderCancelled {
		return "cancelled"
	}
	if r.Status == ReminderCompleted || r.hasCompletedDelivery() {
		return "completed"
	}
	return "pending"
}

// Finishing a reminder does not prove the recipient did the real-world task.
func reminderActivityStatus(r *Reminder) string {
	if r.Status == ReminderCancelled {
		return "cancelled"
	}
	if r.Status == ReminderCompleted || r.CompletedBy != nil {
		return "completed"
	}
	return "pending"
}

func ensureHistoryLocation(tx *gorm.DB, session string, r *Reminder) (*ReminderHistoryLocation, error) {
	var location ReminderHistoryLocation
	err := tx.Where("session_id=? AND reminder_id=?", session, r.ID).First(&location).Error
	if err == nil {
		status := reminderBoardStatus(r)
		if location.BoardStatus != status || !location.DueAt.Equal(r.DueAt) {
			if err := tx.Model(&location).Updates(map[string]any{"board_status": status, "due_at": r.DueAt}).Error; err != nil {
				return nil, err
			}
		}
		return &location, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	created := r.CreatedAt
	if created.IsZero() {
		created = time.Now().UTC()
	}
	month := created.In(time.FixedZone("Asia/Shanghai", 8*60*60)).Format("2006-01")
	counter := ReminderHistoryMonth{SessionID: session, Month: month}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&counter).Error; err != nil {
		return nil, err
	}
	if err := tx.Model(&ReminderHistoryMonth{}).Where("session_id=? AND month=?", session, month).UpdateColumn("record_count", gorm.Expr("record_count+1")).Error; err != nil {
		return nil, err
	}
	if err := tx.Where("session_id=? AND month=?", session, month).First(&counter).Error; err != nil {
		return nil, err
	}
	location = ReminderHistoryLocation{SessionID: session, ReminderID: r.ID, Month: month, Position: counter.RecordCount, Page: int((counter.RecordCount - 1) / ReminderHistoryPageSize), BoardStatus: reminderBoardStatus(r), DueAt: r.DueAt, CreatedAt: created}
	if err := tx.Create(&location).Error; err != nil {
		return nil, err
	}
	return &location, nil
}

func syncReminderHistory(tx *gorm.DB, r *Reminder) error {
	sessions := []string{r.MemorySession()}
	// A private reminder is published only after actual delivery. Its canonical
	// private history is preserved, and only this reminder enters shared history.
	if r.SessionID != r.MemorySession() {
		sessions = append(sessions, r.SessionID)
	}
	for _, session := range sessions {
		location, err := ensureHistoryLocation(tx, session, r)
		if err != nil {
			return err
		}
		if err := syncHistoryPage(tx, *location, r.BindingCreatedAt, r.Status == ReminderCancelled || r.Status == ReminderDeleted); err != nil {
			return err
		}
		if err := syncHistoryIndex(tx, session, r.BindingCreatedAt, r.Status == ReminderCancelled || r.Status == ReminderDeleted); err != nil {
			return err
		}
		if err := syncTodoBoard(tx, session, r.BindingCreatedAt, r.Status == ReminderCancelled || r.Status == ReminderDeleted); err != nil {
			return err
		}
	}
	return nil
}

func syncHistoryPage(tx *gorm.DB, location ReminderHistoryLocation, epoch time.Time, allowUnbound bool) error {
	var rows []Reminder
	err := tx.Table("reminder_history_locations AS l").Select("r.*").Joins("JOIN reminders r ON r.id=l.reminder_id").Where("l.session_id=? AND l.month=? AND l.page=? AND l.board_status != ?", location.SessionID, location.Month, location.Page, ReminderDeleted).Order("l.position ASC").Limit(ReminderHistoryPageSize).Find(&rows).Error
	if err != nil {
		return err
	}
	facts := make([]json.RawMessage, 0, len(rows))
	for _, r := range rows {
		facts = append(facts, json.RawMessage(reminderCloudContent(&r)))
	}
	var document map[string]any
	if err := json.Unmarshal([]byte(memoryspace.TodoTemplate()), &document); err != nil {
		return err
	}
	document["reminders"] = facts
	document["pagination"] = map[string]any{"month": location.Month, "page": location.Page + 1, "pageSize": ReminderHistoryPageSize}
	body, err := json.Marshal(document)
	if err != nil {
		return err
	}
	return queueHistoryDocument(tx, location.SessionID, location.Path(), "projection", string(body), epoch, allowUnbound)
}

// The month index is part of the root template, never a separate file.
func syncHistoryIndex(tx *gorm.DB, session string, epoch time.Time, allowUnbound bool) error {
	return nil
}

func queueHistoryDocument(tx *gorm.DB, session, path, kind, body string, epoch time.Time, allowUnbound bool) error {
	if len(body) > 96*1024 {
		return fmt.Errorf("reminder history document too large: %s", path)
	}
	var current MemoryRecord
	err := tx.Where("id=?", MemoryID(session, path)).First(&current).Error
	if err == nil && current.Content == body && current.Operation == "upsert" && current.BindingCreatedAt.Equal(epoch) {
		return nil
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	return enqueueMemory(tx, MemoryRecord{Kind: kind, SessionID: session, Path: path, Scope: "space", Storage: "database_and_memory", Operation: "upsert", PendingContent: body, BindingCreatedAt: epoch, AllowUnbound: allowUnbound})
}

func (db *DB) GetReminderMemory(ctx context.Context, r Reminder) (*MemoryRecord, error) {
	var location ReminderHistoryLocation
	err := db.gdb.WithContext(ctx).Where("session_id=? AND reminder_id=?", r.MemorySession(), r.ID).First(&location).Error
	if err != nil {
		return nil, err
	}
	return db.GetMemoryRecord(ctx, MemoryID(location.SessionID, location.Path()), location.SessionID)
}

// ReminderMemorySynced checks both the item's history page and the current
// board before a manual completion is confirmed in chat.
func (db *DB) ReminderMemorySynced(ctx context.Context, r Reminder) (bool, error) {
	page, err := db.GetReminderMemory(ctx, r)
	if err != nil {
		return false, err
	}
	board, err := db.GetMemoryRecord(ctx, MemoryID(r.MemorySession(), memoryspace.TodoPath), r.MemorySession())
	if err != nil {
		return false, err
	}
	return page != nil && page.State == "synced" && page.Operation == "upsert" &&
		board != nil && board.State == "synced" && board.Operation == "upsert", nil
}

// Clock events include just the addressed reminder, not the other items on its
// archive page. The actual cloud body is verified against the stable reminder ID.
func ExtractReminderMemory(body, id string) (string, error) {
	var page struct {
		Reminders []json.RawMessage `json:"reminders"`
	}
	if err := json.Unmarshal([]byte(body), &page); err != nil {
		return "", err
	}
	for _, raw := range page.Reminders {
		var fact struct {
			ID string `json:"reminderId"`
		}
		if err := json.Unmarshal(raw, &fact); err != nil {
			return "", err
		}
		if fact.ID == id {
			return string(raw), nil
		}
	}
	return "", fmt.Errorf("reminder %s missing from cloud history page", id)
}

// ReminderHistoryReady 报告该空间的历史页与任务板投影是否都已同步到云端；
// 没同步完时，归档文档的退役会被领取条件挡住。
func (db *DB) ReminderHistoryReady(ctx context.Context, session string) (bool, error) {
	var row MemoryRecord
	err := db.gdb.WithContext(ctx).Select("id").Where("session_id=? AND (kind='template' OR (path >= 'tasks/todo-board/' AND path < 'tasks/todo-board0')) AND operation='upsert' AND state!='synced'", session).Take(&row).Error
	if err == gorm.ErrRecordNotFound {
		return true, nil
	}
	return false, err
}

// RecoverReminderArchiveStore 把失活的归档空间换到新仓库并重排其本地页；
// 挂载中的空间不走这条路径。换仓与重排在一个事务里提交。
func (db *DB) RecoverReminderArchiveStore(ctx context.Context, previous SpaceMemoryStore, replacement string) error {
	return db.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&SpaceMemoryStore{}).Where("session_id=? AND store_id=?", previous.SessionID, previous.StoreID).Updates(map[string]any{"store_id": replacement, "native_mounted": false})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		now := time.Now().UTC()
		return tx.Model(&MemoryRecord{}).Where("session_id=? AND operation='upsert' AND content!='' AND (path >= 'tasks/todo-board/' AND path < 'tasks/todo-board0' OR path='tasks/todo-board.json' OR path='rules/memory-policy.json')", previous.SessionID).Updates(map[string]any{"store_id": replacement, "entry_id": "", "pending_content": gorm.Expr("content"), "state": "pending", "allow_unbound": true, "revision": gorm.Expr("revision+1"), "run_at": now, "updated_at": now}).Error
	})
}
