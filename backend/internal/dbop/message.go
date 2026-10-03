package dbop

import (
	"context"
	"time"

	"gorm.io/gorm/clause"
)

// Message 是聊天消息的本地留痕（messages 表），主键为云端事件 ID，天然去重。
type Message struct {
	Visibility     string    `json:"visibility,omitempty"`
	PrivateOwnerID int64     `json:"-"`
	ID             string    `json:"id"             gorm:"column:id;primaryKey;size:160"`
	SessionID      string    `json:"sessionId"      gorm:"column:session_id;size:160;not null;index:idx_messages_session,priority:1"`
	Sender         string    `json:"sender"         gorm:"column:sender;size:8;not null"`
	UserID         int64     `json:"userId,omitempty"`
	DisplayName    string    `json:"displayName,omitempty"`
	RecipientIDs   []int64   `json:"recipientIds,omitempty" gorm:"serializer:json;type:mediumtext"`
	Source         string    `json:"source,omitempty"`
	Text           string    `json:"text"           gorm:"column:text;type:mediumtext;not null"`
	Files          []string  `json:"files,omitempty" gorm:"serializer:json;type:mediumtext"`
	CloudCreatedAt string    `json:"cloudCreatedAt" gorm:"column:cloud_created_at;size:64;not null;default:''"`
	SavedAt        time.Time `json:"savedAt"        gorm:"column:saved_at;autoCreateTime;index:idx_messages_session,priority:2;type:datetime(6)"`
}

// TableName 指定表名。
func (Message) TableName() string { return "messages" }

// messageHistoryColumns 是按云端 ID 去重时需要刷新的列。
var messageHistoryColumns = []string{"sender", "user_id", "display_name", "recipient_ids", "source", "text", "files", "visibility", "private_owner_id"}

// SaveMessage 按云端 ID 去重，并补充可信身份和接收对象等元数据。
func (db *DB) SaveMessage(ctx context.Context, m *Message) error {
	return db.SaveMessages(ctx, []*Message{m})
}

// SaveMessages 一次批量 upsert 整页历史：读历史的路径每次都会重放全部消息，
// 逐条写入在远端 MySQL 上会把一次页面加载拖到几十秒。
func (db *DB) SaveMessages(ctx context.Context, records []*Message) error {
	if len(records) == 0 {
		return nil
	}
	if !db.enabled() {
		return errNoDB
	}
	return db.gdb.WithContext(ctx).
		Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}},
			DoUpdates: clause.AssignmentColumns(messageHistoryColumns)}).
		Create(&records).Error
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

// HasMessage uses the primary key, so repeated history reads do not repeat the
// same protocol warning in operational logs.
func (db *DB) HasMessage(ctx context.Context, id string) (bool, error) {
	if !db.enabled() {
		return false, errNoDB
	}
	var count int64
	err := db.gdb.WithContext(ctx).Raw("SELECT COUNT(*) FROM messages WHERE id = ?", id).Scan(&count).Error
	return count > 0, err
}
