package dbop

import (
	"context"
	"time"

	"gorm.io/gorm/clause"
)

// Session 是云端会话的本地快照（sessions 表），用于离线查看与审计。
type Session struct {
	ID                  string    `json:"id"             gorm:"column:id;primaryKey;size:160"`
	Title               string    `json:"title"          gorm:"column:title;size:255;not null;default:''"`
	Status              string    `json:"status"         gorm:"column:status;size:32;not null;default:''"`
	CloudCreatedAt      string    `json:"cloudCreatedAt" gorm:"column:cloud_created_at;size:64;not null;default:''"`
	CloudUpdatedAt      string    `json:"cloudUpdatedAt" gorm:"column:cloud_updated_at;size:64;not null;default:''"`
	SyncedAt            time.Time `json:"syncedAt"       gorm:"column:synced_at;autoCreateTime;autoUpdateTime"`
	ConversationPending bool      `json:"-" gorm:"column:conversation_pending;index:idx_sessions_sync,priority:1"`
	NextSyncAt          time.Time `json:"-" gorm:"index:idx_sessions_sync,priority:2"`
	PendingSince        time.Time `json:"-"`
	ConversationVersion int64     `json:"-"`
	SyncCursor          string    `json:"-"`
	SyncOrigin          string    `json:"-"`
}

// TableName 指定表名。
func (Session) TableName() string { return "sessions" }

// UpsertSession 记录/刷新云端会话的本地快照。
func (db *DB) UpsertSession(ctx context.Context, s *Session) error {
	if !db.enabled() {
		return errNoDB
	}
	return db.gdb.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "id"}},
			DoUpdates: clause.AssignmentColumns([]string{"title", "status", "cloud_created_at", "cloud_updated_at", "synced_at"}),
		}).
		Create(s).Error
}

// ListSessions 按同步时间倒序列出本地会话快照。
func (db *DB) ListSessions(ctx context.Context, limit int) ([]Session, error) {
	if !db.enabled() {
		return nil, errNoDB
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var out []Session
	err := db.gdb.WithContext(ctx).Order("synced_at DESC").Limit(limit).Find(&out).Error
	return out, err
}
