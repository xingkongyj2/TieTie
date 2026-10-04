package dbop

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"strings"
	"tietie/backend/internal/memoryspace"
	"time"
)

type SpaceMemoryStore struct {
	SessionID       string `gorm:"primaryKey;size:160"`
	StoreID         string `gorm:"size:160"`
	NativeMounted   bool
	SpaceID         string `gorm:"size:160"`
	TemplateVersion int
	CreatedAt       time.Time `gorm:"type:datetime(6)"`
	UpdatedAt       time.Time `gorm:"type:datetime(6)"`
}

func (SpaceMemoryStore) TableName() string { return "space_memory_stores" }

// MemoryRecord keeps indexed facts and the durable outbox for template pages.
type MemoryRecord struct {
	Kind              string     `json:"kind" gorm:"not null;default:fact;index:idx_memory_space,priority:2;index:idx_memory_impression,priority:2;size:32"`
	AllowUnbound      bool       `json:"-"`
	ArchiveRetirement bool       `json:"-" gorm:"not null;default:false"`
	ID                string     `json:"memoryKey" gorm:"primaryKey;index:idx_memory_sync,priority:3;index:idx_memory_category,priority:3;index:idx_memory_space_id,priority:2;size:64"`
	SessionID         string     `json:"-" gorm:"not null;index:idx_memory_space,priority:1;index:idx_memory_category,priority:1;index:idx_memory_space_id,priority:1;index:idx_memory_impression,priority:1;index:idx_memory_paths,priority:1;size:160"`
	Path              string     `json:"path" gorm:"index:idx_memory_paths,priority:2;size:255"`
	CloudPath         string     `json:"-" gorm:"size:255"`
	Scope             string     `json:"scope" gorm:"size:32"`
	SourceUserID      int64      `json:"sourceUserId"`
	SourceRequestID   string     `json:"-"`
	Category          string     `json:"category" gorm:"index:idx_memory_category,priority:2;index:idx_memory_impression,priority:4;size:64"`
	ExpiresAt         *time.Time `json:"expiresAt,omitempty" gorm:"index;type:datetime(6)"`
	OwnerID           int64      `json:"ownerId,omitempty" gorm:"index:idx_memory_impression,priority:3"`
	Storage           string     `json:"storage" gorm:"size:32"`
	Content           string     `json:"-" gorm:"type:mediumtext"`
	PendingContent    string     `json:"-" gorm:"type:mediumtext"`
	Operation         string     `json:"-" gorm:"size:32"`
	EntryID           string     `json:"-" gorm:"size:160"`
	StoreID           string     `json:"-" gorm:"size:160"`
	State             string     `json:"state" gorm:"index:idx_memory_sync,priority:1;size:32"`
	Revision          int64      `json:"-"`
	RunAt             time.Time  `json:"-" gorm:"index:idx_memory_sync,priority:2;type:datetime(6)"`
	BindingCreatedAt  time.Time  `json:"-" gorm:"type:datetime(6)"`
	UpdatedAt         time.Time  `json:"updatedAt" gorm:"index:idx_memory_space,priority:3;index:idx_memory_impression,priority:5;type:datetime(6)"`
	CreatedAt         time.Time  `json:"-" gorm:"type:datetime(6)"`
}

func (MemoryRecord) TableName() string { return "memory_records" }

// BeforeCreate 兜住"没有排期"的记录：绑定初始化时模板文档是直接以 synced 落库的，
// 不填 run_at 就是 Go 零值，驱动会写成 '0000-00-00'，MySQL 严格模式当场拒收。
func (m *MemoryRecord) BeforeCreate(*gorm.DB) error {
	if m.RunAt.IsZero() {
		m.RunAt = epochTime
	}
	return nil
}

func MemoryID(session, path string) string {
	sum := sha256.Sum256([]byte(session + "/" + path))
	return "memory_" + hex.EncodeToString(sum[:16])
}
func enqueueMemory(tx *gorm.DB, r MemoryRecord) error {
	if r.ExpiresAt != nil {
		utc := r.ExpiresAt.UTC()
		r.ExpiresAt = &utc
	}
	if r.Kind == "template" || r.Kind == "projection" {
	} else if strings.HasPrefix(r.Path, "shared/reminders/") {
		r.Kind = "reminder"
	} else {
		r.Kind = "fact"
	}
	if r.Kind == "fact" {
		if r.Category == "" {
			r.Category = "profile"
		}
		r.Storage = "database_and_memory"
	}
	r.ID = MemoryID(r.SessionID, r.Path)
	r.State = "pending"
	r.RunAt = time.Now().UTC()
	r.Revision = 1
	local := ""
	if r.Storage == "database_and_memory" {
		local = r.PendingContent
	}
	r.Content = local
	if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoUpdates: clause.Assignments(map[string]any{
		"content": local, "pending_content": r.PendingContent, "operation": r.Operation, "storage": r.Storage, "state": "pending", "revision": gorm.Expr("memory_records.revision + 1"), "run_at": r.RunAt, "updated_at": r.RunAt, "binding_created_at": r.BindingCreatedAt, "allow_unbound": r.AllowUnbound, "archive_retirement": r.ArchiveRetirement, "kind": r.Kind, "category": r.Category, "expires_at": r.ExpiresAt, "source_user_id": r.SourceUserID, "source_request_id": r.SourceRequestID,
	})}).Create(&r).Error; err != nil {
		return err
	}
	if r.Kind != "fact" {
		return nil
	}
	var current MemoryRecord
	if err := tx.Where("id=?", r.ID).First(&current).Error; err != nil {
		return err
	}
	version := MemoryRevision{MemoryID: current.ID, Revision: current.Revision, SessionID: current.SessionID, Content: current.Content, Operation: current.Operation}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&version).Error; err != nil {
		return err
	}
	return materializeFact(tx, current)
}
func (db *DB) QueueMemory(ctx context.Context, r MemoryRecord) error {
	return db.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return enqueueMemory(tx, r) })
}
func (db *DB) GetMemoryRecord(ctx context.Context, id, session string) (*MemoryRecord, error) {
	var r MemoryRecord
	err := db.gdb.WithContext(ctx).Where("id = ? AND session_id = ?", id, session).First(&r).Error
	return firstOrNil(&r, err)
}

// MemoryIndex includes all fixed templates plus a bounded recent-fact page.
// Query kinds separately so the session/kind/update index can stop at LIMIT.
func (db *DB) MemoryIndex(ctx context.Context, session string) ([]MemoryRecord, error) {
	var templates, facts []MemoryRecord
	columns := []string{"id", "session_id", "kind", "path", "cloud_path", "scope", "owner_id", "state", "category", "expires_at", "updated_at", "revision"}
	if err := db.gdb.WithContext(ctx).Select(columns).Where("session_id=? AND kind='template' AND state!='deleted' AND operation!='delete'", session).Order("updated_at DESC").Limit(16).Find(&templates).Error; err != nil {
		return nil, err
	}
	if err := db.gdb.WithContext(ctx).Select(columns).Where("session_id=? AND kind='fact' AND state!='deleted' AND operation!='delete' AND (expires_at IS NULL OR expires_at>?)", session, time.Now().UTC()).Order("updated_at DESC").Limit(100 - len(templates)).Find(&facts).Error; err != nil {
		return nil, err
	}
	return append(templates, facts...), nil
}
func (db *DB) GetSpaceMemoryStore(ctx context.Context, session string) (*SpaceMemoryStore, error) {
	var r SpaceMemoryStore
	err := db.gdb.WithContext(ctx).Where("session_id = ?", session).First(&r).Error
	return firstOrNil(&r, err)
}
func (db *DB) SaveSpaceMemoryStore(ctx context.Context, r SpaceMemoryStore) error {
	return db.gdb.WithContext(ctx).Clauses(clause.OnConflict{UpdateAll: true}).Create(&r).Error
}

// dueMemoryIDs 挑出已建索引的待发记忆；候选挑选与状态改写同在一把领取锁里，见 db.go 的 claimWithLock。
const dueMemoryIDs = `SELECT m.id FROM memory_records m FORCE INDEX (idx_memory_sync) WHERE m.state='pending' AND m.run_at<=?
 AND (m.allow_unbound=1 OR (EXISTS(SELECT 1 FROM bindings b WHERE b.session_id=m.session_id AND b.created_at=m.binding_created_at) OR EXISTS(SELECT 1 FROM private_channels p JOIN bindings b ON b.session_id=p.space_id AND b.created_at=p.binding_created_at WHERE p.session_id=m.session_id AND b.created_at=m.binding_created_at)))
 AND (m.archive_retirement=0 OR NOT EXISTS(SELECT 1 FROM memory_records n WHERE n.session_id=m.session_id AND (n.kind='template' OR (n.path >= 'tasks/todo-board/' AND n.path < 'tasks/todo-board0')) AND n.operation='upsert' AND n.state!='synced'))
 ORDER BY m.run_at,m.id LIMIT ?`

func (db *DB) ClaimMemorySync(ctx context.Context, now time.Time, limit int) ([]MemoryRecord, error) {
	if limit <= 0 || limit > 512 {
		limit = 64
	}
	if err := db.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var expired []MemoryRecord
		if err := tx.Where("expires_at IS NOT NULL AND expires_at<=? AND operation!='delete'", now.UTC()).Order("expires_at ASC").Limit(limit).Find(&expired).Error; err != nil {
			return err
		}
		for _, r := range expired {
			r.Operation, r.PendingContent, r.AllowUnbound = "delete", "", true
			if err := enqueueMemory(tx, r); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	var rows []MemoryRecord
	claimAt := time.Now().UTC()
	if now.After(claimAt) {
		claimAt = now.UTC()
	}
	lease := claimAt.Add(2 * time.Minute)
	err := claimWithLock(ctx, db.gdb, claimLockMemorySync, func(tx *gorm.DB) error {
		var ids []string
		if err := tx.Raw(dueMemoryIDs, claimAt, limit).Scan(&ids).Error; err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		// 领取写入保持原 RETURNING 语句的列集合：走 GORM 的 Updates 会顺带刷新 updated_at，而它是画像重算的判据。
		if err := tx.Exec(`UPDATE memory_records SET state='syncing',run_at=? WHERE id IN (?) AND state='pending'`, lease, ids).Error; err != nil {
			return err
		}
		return tx.Where("id IN ? AND state = 'syncing'", ids).Find(&rows).Error
	})
	return rows, err
}
func (db *DB) FinishMemorySync(ctx context.Context, r MemoryRecord, store, entry string) error {
	state := "synced"
	if r.Operation == "delete" {
		state = "deleted"
	}
	updates := map[string]any{"state": state, "pending_content": "", "entry_id": entry, "store_id": store}
	if r.ArchiveRetirement && r.Operation == "delete" {
		updates["content"] = ""
	}
	return db.gdb.WithContext(ctx).Model(&MemoryRecord{}).Where("id = ? AND revision = ?", r.ID, r.Revision).Updates(updates).Error
}
func (db *DB) RetryMemorySync(ctx context.Context, r MemoryRecord) error {
	return db.gdb.WithContext(ctx).Model(&MemoryRecord{}).Where("id = ? AND revision = ? AND state='syncing'", r.ID, r.Revision).Updates(map[string]any{"state": "pending", "run_at": time.Now().Add(30 * time.Second).UTC()}).Error
}
func (db *DB) RecoverMemorySync(ctx context.Context) error {
	return db.gdb.WithContext(ctx).Model(&MemoryRecord{}).Where("state='syncing'").Updates(map[string]any{"state": "pending", "run_at": time.Now().UTC()}).Error
}
func reminderCloudContent(r *Reminder) string {
	b, _ := json.Marshal(map[string]any{"schemaVersion": 1, "id": r.ID, "reminderId": r.ID, "title": r.Title, "dueAt": r.DueAt, "recurrence": r.Recurrence, "seriesId": r.SeriesID, "occurrence": r.Occurrence, "recipientIds": r.RecipientIDs, "createdBy": r.CreatedBy, "status": reminderBoardStatus(r), "deliveryStatus": r.Status, "activityStatus": reminderActivityStatus(r), "taskStatus": r.TaskStatus, "taskCompletedAt": r.TaskCompletedAt, "deliveredAt": r.DeliveredAt, "completedBy": r.CompletedBy, "createdAt": r.CreatedAt, "updatedAt": r.UpdatedAt, "visibility": r.Visibility})
	return string(b)
}

type MemoryOperationReceipt struct {
	ID        string    `gorm:"primaryKey;size:255"`
	MemoryID  string    `gorm:"size:64"`
	CreatedAt time.Time `gorm:"type:datetime(6)"`
}

func (MemoryOperationReceipt) TableName() string { return "memory_operation_receipts" }
func (db *DB) ApplyMemoryAction(ctx context.Context, request string, r MemoryRecord) (*MemoryRecord, error) {
	r.ID = MemoryID(r.SessionID, r.Path)
	err := db.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&MemoryOperationReceipt{ID: request, MemoryID: r.ID})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		return enqueueMemory(tx, r)
	})
	if err != nil {
		return nil, err
	}
	return db.GetMemoryRecord(ctx, r.ID, r.SessionID)
}

// ListMemoryIndex pages metadata without loading any memory body. Full recall
// for mounted stores can inspect the category directory, even beyond this page.
func (db *DB) ListMemoryIndex(ctx context.Context, session, category, after string, limit int) ([]MemoryRecord, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	query := db.gdb.WithContext(ctx).Select("id", "path", "cloud_path", "scope", "owner_id", "state", "category", "expires_at").Where("session_id=? AND kind='fact' AND state!='deleted' AND operation!='delete' AND (expires_at IS NULL OR expires_at>?)", session, time.Now().UTC())
	if category != "" {
		query = query.Where("category=?", category)
	}
	if after != "" {
		query = query.Where("id>?", after)
	}
	var rows []MemoryRecord
	err := query.Order("id ASC").Limit(limit).Find(&rows).Error
	return rows, err
}

// The internal logical key is never advertised as a physical file.
func (r MemoryRecord) DocumentPath() string {
	if r.Kind == "fact" {
		if r.CloudPath != "" {
			return r.CloudPath
		}
		return memoryspace.TemplatePath(r.Category)
	}
	return r.Path
}
