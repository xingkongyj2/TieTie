package dbop

import (
	"context"
	"time"

	"gorm.io/gorm/clause"
)

// Message 是聊天消息的本地留痕（messages 表），主键为云端事件 ID，天然去重。
type Message struct {
	ID             string    `json:"id"             gorm:"column:id;primaryKey;size:160"`
	SessionID      string    `json:"sessionId"      gorm:"column:session_id;size:160;not null;index:idx_messages_session,priority:1"`
	Sender         string    `json:"sender"         gorm:"column:sender;size:8;not null"`
	Text           string    `json:"text"           gorm:"column:text;not null;default:''"`
	CloudCreatedAt string    `json:"cloudCreatedAt" gorm:"column:cloud_created_at;size:64;not null;default:''"`
	SavedAt        time.Time `json:"savedAt"        gorm:"column:saved_at;autoCreateTime;index:idx_messages_session,priority:2"`
}

// TableName 指定表名。
func (Message) TableName() string { return "messages" }

// SaveMessage 落库一条消息（主键为云端事件 ID，重复写入自动忽略）。
func (db *DB) SaveMessage(ctx context.Context, m *Message) error {
	if !db.enabled() {
		return errNoDB
	}
	return db.gdb.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(m).Error
}

// ListMessages 按写入顺序返回会话的本地消息（用于离线查看/审计）。
func (db *DB) ListMessages(ctx context.Context, sessionID string, limit int) ([]Message, error) {
	if !db.enabled() {
		return nil, errNoDB
	}
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	var out []Message
	err := db.gdb.WithContext(ctx).
		Where(map[string]any{"session_id": sessionID}).
		Order("saved_at ASC").
		Limit(limit).
		Find(&out).Error
	return out, err
}
