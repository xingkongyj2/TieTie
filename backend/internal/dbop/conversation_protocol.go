package dbop

import (
	"context"
	"gorm.io/gorm/clause"
	"time"
)

// One durable accepted transport cursor per cloud session and binding epoch.
// Shared and private channels have distinct session IDs and never share state.
type ConversationProtocol struct {
	SessionID        string `gorm:"primaryKey"`
	BindingCreatedAt time.Time
	ContractHash     string
	StateJSON        string
	UpdatedAt        time.Time
}

func (ConversationProtocol) TableName() string { return "conversation_protocols" }
func (db *DB) GetConversationProtocol(ctx context.Context, id string) (*ConversationProtocol, error) {
	var record ConversationProtocol
	err := db.gdb.WithContext(ctx).Where("session_id = ?", id).First(&record).Error
	return firstOrNil(&record, err)
}
func (db *DB) SaveConversationProtocol(ctx context.Context, record ConversationProtocol) error {
	return db.gdb.WithContext(ctx).Clauses(clause.OnConflict{UpdateAll: true}).Create(&record).Error
}
