package dbop

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"tietie/backend/internal/conversation"
	"tietie/backend/internal/logging"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	ReminderScheduled   = "scheduled"
	ReminderDispatching = "dispatching"
	ReminderDelivered   = "delivered"
	ReminderCompleted   = "completed"
	ReminderCancelled   = "cancelled"
	ReminderUncertain   = "uncertain"
	ReminderFailed      = "failed"
)

var (
	ErrReminderNotFound  = errors.New("reminder not found")
	ErrReminderForbidden = errors.New("reminder recipient is not an active space member")
	ErrReminderState     = errors.New("reminder state does not allow this operation")
	ErrReminderInvalid   = errors.New("invalid reminder action")
)

// Reminder 是可恢复的提醒队列。绑定创建时间隔离解绑后重新使用同一云会话的旧任务。
type Reminder struct {
	DeliverySessionID string     `json:"-" gorm:"size:160"`
	MemorySessionID   string     `json:"-" gorm:"size:160"`
	Visibility        string     `json:"visibility,omitempty" gorm:"size:32"`
	MemoryBucket      string     `json:"-" gorm:"index:idx_reminders_board,priority:2;size:160"`
	ID                string     `json:"id" gorm:"primaryKey;size:64;index:idx_reminders_board,priority:3;index:idx_reminders_created,priority:3;index:idx_reminders_space_status,priority:4;index:idx_reminders_ready,priority:3"`
	SessionID         string     `json:"sessionId" gorm:"not null;index:idx_reminders_session;index:idx_reminders_space_status,priority:1;index:idx_reminders_board,priority:1;index:idx_reminders_created,priority:1;size:160"`
	Title             string     `json:"title" gorm:"not null;size:500"`
	DueAt             time.Time  `json:"dueAt" gorm:"not null;type:datetime(6)"`
	RunAt             time.Time  `json:"-" gorm:"index:idx_reminders_ready,priority:2;index:idx_reminders_space_status,priority:3;type:datetime(6)"`
	RecipientIDs      []int64    `json:"recipientIds" gorm:"serializer:json;not null;type:mediumtext"`
	CreatedBy         int64      `json:"createdBy"`
	SourceEventID     string     `json:"sourceEventId" gorm:"not null;size:160"`
	Status            string     `json:"status" gorm:"not null;index:idx_reminders_ready,priority:1;index:idx_reminders_space_status,priority:2;size:32"`
	TaskStatus        string     `json:"taskStatus" gorm:"not null;default:pending;size:32"`
	TaskCompletedAt   *time.Time `json:"taskCompletedAt,omitempty" gorm:"type:datetime(6)"`
	DeliveredAt       *time.Time `json:"deliveredAt,omitempty" gorm:"type:datetime(6)"`
	CompletedBy       *int64     `json:"completedBy,omitempty"`
	CreatedAt         time.Time  `json:"createdAt" gorm:"autoCreateTime;index:idx_reminders_created,priority:2;type:datetime(6)"`
	UpdatedAt         time.Time  `json:"updatedAt" gorm:"autoUpdateTime;type:datetime(6)"`
	BindingCreatedAt  time.Time  `json:"-" gorm:"not null;type:datetime(6)"`
	ActionIndex       int        `json:"-"`
	Attempts          int        `json:"-"`
	NextAttemptAt     *time.Time `json:"-" gorm:"type:datetime(6)"`
	DispatchEventIDs  []string   `json:"-" gorm:"serializer:json;type:mediumtext"`
}

func (Reminder) TableName() string { return "reminders" }

func (r Reminder) hasCompletedDelivery() bool {
	return r.Status == ReminderDelivered || r.TaskStatus == "completed" || r.TaskCompletedAt != nil || r.DeliveredAt != nil
}

// ReminderActionReceipt 与提醒的修改在同一事务提交，重放云端历史不会重复建任务。
type ReminderActionReceipt struct {
	SessionID     string    `gorm:"primaryKey;uniqueIndex:idx_reminder_request,priority:1;size:160"`
	RequestKey    *string   `gorm:"uniqueIndex:idx_reminder_request,priority:2;size:255"`
	SourceEventID string    `gorm:"primaryKey;size:160"`
	ActionIndex   int       `gorm:"primaryKey;autoIncrement:false"`
	ActionType    string    `gorm:"not null;size:32"`
	ReminderID    string    `gorm:"not null;size:64"`
	CreatedAt     time.Time `gorm:"autoCreateTime;type:datetime(6)"`
}

func (ReminderActionReceipt) TableName() string { return "reminder_action_receipts" }

type ReminderAction struct {
	Type         string
	ReminderID   string
	Title        string
	DueAt        time.Time
	RecipientIDs []int64
	CreatedBy    int64
	RequestKey   string
}

// ApplyReminderAction 原子应用一次 AI 提醒动作。applied=false 表示持久化回执已存在。
func (db *DB) ApplyReminderAction(ctx context.Context, sessionID, sourceEventID string, actionIndex int, action ReminderAction) (reminder *Reminder, applied bool, err error) {
	if !db.enabled() {
		return nil, false, errNoDB
	}
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(sourceEventID) == "" || actionIndex < 0 || (action.Type != "create" && action.Type != "cancel") {
		return nil, false, ErrReminderInvalid
	}
	err = db.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		receipt := ReminderActionReceipt{SessionID: sessionID, SourceEventID: sourceEventID, ActionIndex: actionIndex, ActionType: action.Type}
		if action.RequestKey != "" {
			receipt.RequestKey = &action.RequestKey
		}
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&receipt)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			var existingReceipt ReminderActionReceipt
			query := tx.Where("session_id = ? AND source_event_id = ? AND action_index = ?", sessionID, sourceEventID, actionIndex)
			if action.RequestKey != "" {
				query = query.Or("session_id = ? AND request_key = ?", sessionID, action.RequestKey)
			}
			if err := query.First(&existingReceipt).Error; err != nil {
				return err
			}
			receipt = existingReceipt
			var existing Reminder
			if err := tx.Where("id = ? AND (session_id = ? OR memory_session_id = ?)", receipt.ReminderID, sessionID, sessionID).First(&existing).Error; err != nil {
				return err
			}
			reminder = &existing
			return nil
		}
		if action.Type == "create" {
			binding, bindingErr := bindingForSession(tx, sessionID)
			if err := bindingErr; err != nil || binding == nil {
				if err == nil {
					return ErrReminderForbidden
				}
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrReminderForbidden
				}
				return err
			}
			title := strings.TrimSpace(action.Title)
			if title == "" || len([]rune(title)) > 500 || action.DueAt.IsZero() || len(action.RecipientIDs) == 0 || len(action.RecipientIDs) > 2 {
				return ErrReminderInvalid
			}
			recipients := slices.Clone(action.RecipientIDs)
			slices.Sort(recipients)
			recipients = slices.Compact(recipients)
			for _, recipientID := range recipients {
				if recipientID != binding.UserA && recipientID != binding.UserB {
					return ErrReminderForbidden
				}
			}
			if action.CreatedBy != 0 && action.CreatedBy != binding.UserA && action.CreatedBy != binding.UserB {
				return ErrReminderForbidden
			}
			var randomID [16]byte
			if _, err := rand.Read(randomID[:]); err != nil {
				return err
			}
			reminder = &Reminder{ID: "rem_" + hex.EncodeToString(randomID[:]), SessionID: sessionID, RunAt: action.DueAt.UTC(),
				Title: title, DueAt: action.DueAt.UTC(), RecipientIDs: recipients, CreatedBy: action.CreatedBy,
				SourceEventID: sourceEventID, ActionIndex: actionIndex, Status: ReminderScheduled, TaskStatus: "pending", BindingCreatedAt: binding.CreatedAt}
			var channel PrivateChannel
			if err := tx.Where("session_id=?", sessionID).First(&channel).Error; err == nil {
				reminder.MemorySessionID, reminder.Visibility = channel.SessionID, "private"
				for _, recipient := range recipients {
					if recipient != channel.OwnerID {
						reminder.DeliverySessionID = channel.SpaceID
					}
				}
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if err := tx.Create(reminder).Error; err != nil {
				return err
			}
			if err := syncReminderMemory(tx, reminder); err != nil {
				return err
			}
		} else {
			var cancelErr error
			reminder, cancelErr = cancelReminder(tx, sessionID, action.ReminderID)
			if cancelErr != nil {
				return cancelErr
			}
		}
		if err := tx.Model(&ReminderActionReceipt{}).
			Where("session_id = ? AND source_event_id = ? AND action_index = ?", sessionID, sourceEventID, actionIndex).
			Update("reminder_id", reminder.ID).Error; err != nil {
			return err
		}
		applied = true
		return nil
	})
	if err == nil && applied {
		logging.Scheduler().Info("提醒已落库，本地事实已更新，云端记忆已加入同步队列", "event", "reminder.saved", "action", action.Type, "session_id", sessionID, "reminder_id", reminder.ID, "due_at", reminder.DueAt, "recipient_ids", reminder.RecipientIDs, "task_status", reminder.TaskStatus)
	}
	return reminder, applied, err
}

func (db *DB) GetReminder(ctx context.Context, sessionID, id string) (*Reminder, error) {
	if !db.enabled() {
		return nil, errNoDB
	}
	var reminder Reminder
	err := db.gdb.WithContext(ctx).Where("session_id = ? AND id = ?", sessionID, id).First(&reminder).Error
	return firstOrNil(&reminder, err)
}

func (db *DB) ListReminders(ctx context.Context, sessionID string) ([]Reminder, error) {
	if !db.enabled() {
		return nil, errNoDB
	}
	reminders := make([]Reminder, 0)
	err := db.gdb.WithContext(ctx).Where("session_id = ?", sessionID).Order("due_at ASC, created_at ASC").Find(&reminders).Error
	return reminders, err
}

// ListContextReminders bounds AI prompt size independently of a space's lifetime
// history: upcoming tasks plus recent delivered reminders for reference.
func (db *DB) ListContextReminders(ctx context.Context, sessionID string) ([]Reminder, error) {
	if !db.enabled() {
		return nil, errNoDB
	}
	reminders := make([]Reminder, 0)
	if err := db.gdb.WithContext(ctx).Where("session_id = ? AND status = ?", sessionID, ReminderScheduled).
		Order("due_at ASC").Limit(100).Find(&reminders).Error; err != nil {
		return nil, err
	}
	var recent []Reminder
	if err := db.gdb.WithContext(ctx).Where("session_id = ? AND status = ?", sessionID, ReminderDelivered).
		Order("due_at DESC").Limit(10).Find(&recent).Error; err != nil {
		return nil, err
	}
	return append(reminders, recent...), nil
}

func (db *DB) CancelReminder(ctx context.Context, sessionID, id string) (*Reminder, error) {
	if !db.enabled() {
		return nil, errNoDB
	}
	var reminder *Reminder
	err := db.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		reminder, err = cancelReminder(tx, sessionID, id)
		return err
	})
	return reminder, err
}

// Commit the manual cancellation and its confirmation queue together. Repeated
// PATCH requests reuse the same job, and no member message is fabricated.
func (db *DB) CancelReminderAndNotify(ctx context.Context, sessionID, id string, job ControlJob) (*Reminder, error) {
	if !db.enabled() {
		return nil, errNoDB
	}
	if job.SessionID != sessionID || job.RequestID != "manual_cancel_"+id || job.CreatedBy <= 0 {
		return nil, ErrReminderInvalid
	}
	var reminder *Reminder
	err := db.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		reminder, err = cancelReminder(tx, sessionID, id)
		if err != nil {
			return err
		}
		results, err := json.Marshal([]conversation.ActionResult{{Key: "manual_cancel", Type: "cancel_reminder", Status: "succeeded", ReminderID: reminder.ID, DatabaseStatus: "saved", MemoryStatus: "pending", Message: "用户在提醒页面手动取消了此提醒。请简短确认取消成功，不再执行操作。"}})
		if err != nil {
			return err
		}
		job.ID = ControlID(sessionID, job.RequestID)
		job.NotificationOnly, job.Results, job.Actions = true, string(results), "[]"
		job.Status, job.RunAt = "pending", time.Now().UTC()
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&job).Error
	})
	return reminder, err
}

func cancelReminder(tx *gorm.DB, sessionID, id string) (*Reminder, error) {
	result := tx.Model(&Reminder{}).Where("session_id = ? AND id = ? AND status IN ? AND task_status != 'completed' AND task_completed_at IS NULL AND delivered_at IS NULL", sessionID, id,
		[]string{ReminderScheduled, ReminderUncertain, ReminderFailed}).Updates(map[string]any{"status": ReminderCancelled, "task_status": "cancelled"})
	if result.Error != nil {
		return nil, result.Error
	}
	var reminder Reminder
	if err := tx.Where("session_id = ? AND id = ?", sessionID, id).First(&reminder).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrReminderNotFound
		}
		return nil, err
	}
	if reminder.Status != ReminderCancelled {
		return nil, ErrReminderState
	}
	if err := syncReminderMemory(tx, &reminder); err != nil {
		return nil, err
	}
	return &reminder, nil
}

// CompleteReminder 只有提醒接收者可以标记完成；同一用户的重复操作是幂等的。
func (db *DB) CompleteReminder(ctx context.Context, sessionID, id string, userID int64) (*Reminder, error) {
	if !db.enabled() {
		return nil, errNoDB
	}
	var reminder Reminder
	err := db.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("session_id = ? AND id = ?", sessionID, id).First(&reminder).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrReminderNotFound
			}
			return err
		}
		if !slices.Contains(reminder.RecipientIDs, userID) {
			return ErrReminderForbidden
		}
		if reminder.Status == ReminderCompleted || reminder.Status != ReminderCancelled && reminder.hasCompletedDelivery() {
			return nil
		}
		if reminder.Status != ReminderScheduled && reminder.Status != ReminderUncertain {
			return ErrReminderState
		}
		result := tx.Model(&Reminder{}).Where("id = ? AND status = ?", id, reminder.Status).
			Updates(map[string]any{"status": ReminderCompleted, "completed_by": userID, "task_status": "cancelled"})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrReminderState
		}
		reminder.Status = ReminderCompleted
		reminder.CompletedBy = &userID
		reminder.TaskStatus = "cancelled"
		return syncReminderMemory(tx, &reminder)
	})
	return &reminder, err
}

// RestoreCompletedReminder 只恢复尚未到期的提醒，避免把已触发过的任务再次投递。
func (db *DB) RestoreCompletedReminder(ctx context.Context, sessionID, id string, userID int64) (*Reminder, error) {
	if !db.enabled() {
		return nil, errNoDB
	}
	var reminder Reminder
	err := db.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("session_id = ? AND id = ?", sessionID, id).First(&reminder).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrReminderNotFound
			}
			return err
		}
		if !slices.Contains(reminder.RecipientIDs, userID) {
			return ErrReminderForbidden
		}
		if reminder.Status != ReminderCompleted || !reminder.DueAt.After(time.Now()) || reminder.hasCompletedDelivery() {
			return ErrReminderState
		}
		result := tx.Model(&Reminder{}).Where("id = ? AND status = ?", id, ReminderCompleted).
			Updates(map[string]any{"status": ReminderScheduled, "completed_by": nil, "run_at": reminder.DueAt, "task_status": "pending"})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrReminderState
		}
		reminder.Status, reminder.CompletedBy, reminder.TaskStatus = ReminderScheduled, nil, "pending"
		return syncReminderMemory(tx, &reminder)
	})
	return &reminder, err
}

// CancelSessionReminders 在解绑时停止所有尚未投递的任务（含正在被工作线程处理的任务）。
func (db *DB) CancelSessionReminders(ctx context.Context, sessionID string) error {
	if !db.enabled() {
		return errNoDB
	}
	return db.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var rows []Reminder
		if err := tx.Where("(session_id = ? OR session_id IN (SELECT session_id FROM private_channels WHERE space_id=?)) AND status IN ?", sessionID, sessionID, []string{ReminderScheduled, ReminderDispatching, ReminderUncertain}).Find(&rows).Error; err != nil {
			return err
		}
		for _, r := range rows {
			if err := tx.Model(&Reminder{}).Where("id = ?", r.ID).Updates(map[string]any{"status": ReminderCancelled, "task_status": "cancelled"}).Error; err != nil {
				return err
			}
			r.Status, r.TaskStatus = ReminderCancelled, "cancelled"
			if err := syncReminderMemory(tx, &r); err != nil {
				return err
			}
		}
		return nil
	})
}

// ClaimDueReminder 领取一条到期提醒；领取逻辑与批量一致，见 ClaimDueReminders。
func (db *DB) ClaimDueReminder(ctx context.Context, now time.Time) (*Reminder, error) {
	reminders, err := db.ClaimDueReminders(ctx, now, 1)
	if err != nil || len(reminders) == 0 {
		return nil, err
	}
	return &reminders[0], nil
}

// ClaimDueReminders 只领已建索引的到期任务，且每个空间最多一条。
// 候选判断和状态改写放在同一把领取锁里，避免两个 worker 同时看到"该空间没有投递中的任务"。
const dueReminderIDs = `SELECT r.id FROM reminders r FORCE INDEX (idx_reminders_ready)
WHERE r.status = ? AND r.run_at <= ?
AND (EXISTS(SELECT 1 FROM bindings b WHERE b.session_id=r.session_id AND b.created_at=r.binding_created_at) OR EXISTS(SELECT 1 FROM private_channels p JOIN bindings b ON b.session_id=p.space_id AND b.created_at=p.binding_created_at WHERE p.session_id=r.session_id AND b.created_at=r.binding_created_at))
AND NOT EXISTS (SELECT 1 FROM reminders pending WHERE pending.session_id = r.session_id AND pending.status = ?)
AND NOT EXISTS (SELECT 1 FROM reminders earlier WHERE earlier.session_id = r.session_id AND earlier.status = ?
AND earlier.binding_created_at = r.binding_created_at
AND (earlier.run_at < r.run_at OR (earlier.run_at = r.run_at AND earlier.id < r.id)))
ORDER BY r.run_at ASC, r.id ASC LIMIT ?`

func (db *DB) ClaimDueReminders(ctx context.Context, now time.Time, limit int) ([]Reminder, error) {
	if !db.enabled() {
		return nil, errNoDB
	}
	if limit <= 0 || limit > 512 {
		limit = 64
	}
	var reminders []Reminder
	err := claimWithLock(ctx, db.gdb, claimLockReminders, func(tx *gorm.DB) error {
		var ids []string
		if err := tx.Raw(dueReminderIDs, ReminderScheduled, now.UTC(), ReminderDispatching, ReminderScheduled, limit).Scan(&ids).Error; err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		// 领取写入保持原 RETURNING 语句的列集合：走 GORM 的 Updates 会顺带刷新自动更新时间列。
		if err := tx.Exec(`UPDATE reminders SET status = ?, task_status = 'running', attempts = attempts + 1, updated_at = ?
WHERE id IN (?) AND status = ?`, ReminderDispatching, now.UTC(), ids, ReminderScheduled).Error; err != nil {
			return err
		}
		// 只回读本事务真正改成投递中的行，避免把领取间隙里被改走状态的提醒也交给 worker。
		return tx.Where("id IN ? AND status = ?", ids, ReminderDispatching).Find(&reminders).Error
	})
	return reminders, err
}

// RecordReminderDispatch 只记录 Qoder 已接受的唤醒事件，等待 AI 实际回复后才算投递完成。
func (db *DB) RecordReminderDispatch(ctx context.Context, id string, eventIDs []string) error {
	if len(eventIDs) == 0 {
		return ErrReminderInvalid
	}
	for _, eventID := range eventIDs {
		if strings.TrimSpace(eventID) == "" {
			return ErrReminderInvalid
		}
	}
	encoded, err := json.Marshal(eventIDs)
	if err != nil {
		return err
	}
	return db.updateReminderDispatch(ctx, id, map[string]any{"dispatch_event_ids": string(encoded)})
}

// FinishReminderDispatch 记录 AI 实际回复完成及全部云端事件 ID；已完成的重放保持幂等。
func (db *DB) FinishReminderDispatch(ctx context.Context, id string, eventIDs []string, now time.Time) error {
	if !db.enabled() {
		return errNoDB
	}
	if len(eventIDs) == 0 {
		return ErrReminderInvalid
	}
	for _, eventID := range eventIDs {
		if strings.TrimSpace(eventID) == "" {
			return ErrReminderInvalid
		}
	}
	// 显式序列化，map 更新不走字段 serializer。
	encoded, err := json.Marshal(eventIDs)
	if err != nil {
		return err
	}
	err = db.updateReminderStates(ctx, id, []string{ReminderDispatching, ReminderUncertain},
		map[string]any{"status": ReminderDelivered, "task_status": "completed", "task_completed_at": now.UTC(), "delivered_at": now.UTC(), "dispatch_event_ids": string(encoded), "next_attempt_at": nil})
	if !errors.Is(err, ErrReminderState) {
		return err
	}
	var reminder Reminder
	if findErr := db.gdb.WithContext(ctx).Select("id", "status").Where("id = ?", id).First(&reminder).Error; findErr != nil {
		return findErr
	}
	if reminder.Status == ReminderDelivered {
		return nil
	}
	return err
}

// RetryReminderDispatch 只用于已确定没有发送的失败；超时和丢失响应必须转为 uncertain。
func (db *DB) RetryReminderDispatch(ctx context.Context, id string, nextAttemptAt time.Time) error {
	if !db.enabled() {
		return errNoDB
	}
	var reminder Reminder
	err := db.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&Reminder{}).
			Where("id = ? AND status = ? AND (dispatch_event_ids IS NULL OR dispatch_event_ids IN ?)", id, ReminderDispatching, []string{"", "null", "[]"}).
			Updates(map[string]any{"status": ReminderScheduled, "task_status": "pending", "next_attempt_at": nextAttemptAt.UTC(), "run_at": nextAttemptAt.UTC()})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrReminderState
		}
		if err := tx.Where("id = ?", id).First(&reminder).Error; err != nil {
			return err
		}
		return syncReminderMemory(tx, &reminder)
	})
	if err == nil {
		logging.Scheduler().Warn("本次未发送，任务已重新排队", "event", "reminder.retry", "reminder_id", id, "next_attempt_at", nextAttemptAt)
	}
	return err
}

func (db *DB) MarkReminderDispatchUncertain(ctx context.Context, id string) error {
	return db.updateReminderDispatch(ctx, id, map[string]any{"status": ReminderUncertain, "task_status": "uncertain"})
}

// FailReminderDispatch 用于 Qoder 明确返回会话错误，不自动重复唤醒。
func (db *DB) FailReminderDispatch(ctx context.Context, id string) error {
	return db.updateReminderStates(ctx, id, []string{ReminderDispatching, ReminderUncertain}, map[string]any{"status": ReminderFailed, "task_status": "failed"})
}

// ListDispatchingReminders 供启动时恢复已被 Qoder 接受、但还在等待回复的任务轮询。
func (db *DB) ListDispatchingReminders(ctx context.Context) ([]Reminder, error) {
	if !db.enabled() {
		return nil, errNoDB
	}
	reminders := make([]Reminder, 0)
	err := db.gdb.WithContext(ctx).Where("status = ?", ReminderDispatching).Order("due_at ASC").Find(&reminders).Error
	return reminders, err
}

func (db *DB) updateReminderDispatch(ctx context.Context, id string, updates map[string]any) error {
	return db.updateReminderStates(ctx, id, []string{ReminderDispatching}, updates)
}

func (db *DB) updateReminderStates(ctx context.Context, id string, allowedStates []string, updates map[string]any) error {
	if !db.enabled() {
		return errNoDB
	}
	var reminder Reminder
	err := db.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&Reminder{}).Where("id = ? AND status IN ?", id, allowedStates).Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("%w: dispatch %s is no longer active", ErrReminderState, id)
		}
		if err := tx.Where("id = ?", id).First(&reminder).Error; err != nil {
			return err
		}
		if reminder.Status == ReminderDelivered && reminder.DeliverySessionID != "" {
			if err := tx.Model(&reminder).Updates(map[string]any{"session_id": reminder.DeliverySessionID, "visibility": "shared"}).Error; err != nil {
				return err
			}
			reminder.SessionID, reminder.Visibility = reminder.DeliverySessionID, "shared"
		}
		return syncReminderMemory(tx, &reminder)
	})
	if err == nil {
		message := "提醒执行状态已保存，本地事实已更新，云端记忆等待同步"
		if reminder.Status == ReminderDelivered {
			message = "AI 提醒已生成，定时任务完成，本地事实已更新，云端记忆等待同步"
		}
		logging.Scheduler().Info(message, "event", "reminder.transition", "reminder_id", id, "session_id", reminder.SessionID, "status", reminder.Status, "task_status", reminder.TaskStatus, "recipient_ids", reminder.RecipientIDs)
	}
	return err
}

// RecoverReminderDispatches 只将缺少接受回执的中断任务转为 uncertain，避免重复发送；
// 已有云端事件 ID 的任务继续等待 AI 回复，调用方需恢复这些任务的会话轮询。
func (db *DB) RecoverReminderDispatches(ctx context.Context) (int64, error) {
	if !db.enabled() {
		return 0, errNoDB
	}
	var count int64
	err := db.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var rows []Reminder
		if err := tx.Where("status = ? AND (dispatch_event_ids IS NULL OR dispatch_event_ids IN ?)", ReminderDispatching, []string{"", "null", "[]"}).Find(&rows).Error; err != nil {
			return err
		}
		for _, r := range rows {
			if err := tx.Model(&Reminder{}).Where("id = ?", r.ID).Updates(map[string]any{"status": ReminderUncertain, "task_status": "uncertain"}).Error; err != nil {
				return err
			}
			r.Status, r.TaskStatus = ReminderUncertain, "uncertain"
			if err := syncReminderMemory(tx, &r); err != nil {
				return err
			}
			count++
		}
		return nil
	})
	return count, err
}
