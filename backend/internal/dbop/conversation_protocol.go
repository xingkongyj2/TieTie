package dbop

import (
	"context"
	"gorm.io/gorm/clause"
	"time"
)

// One durable accepted transport cursor per cloud session and binding epoch.
// Shared and private channels have distinct session IDs and never share state.
type ConversationProtocol struct {
	SessionID        string    `gorm:"primaryKey;size:160"`
	BindingCreatedAt time.Time `gorm:"type:datetime(6)"`
	ContractHash     string
	StateJSON        string    `gorm:"type:mediumtext"`
	UpdatedAt        time.Time `gorm:"type:datetime(6)"`
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
