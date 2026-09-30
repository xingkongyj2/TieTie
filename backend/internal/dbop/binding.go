package dbop

import (
	"context"
	"time"

	"gorm.io/gorm/clause"
)

// Binding 是一对用户的绑定关系（bindings 表）。
// 两个用户 ID 按字典序组成复合主键，即"一对用户只有一个共享会话"。
type Binding struct {
	UserA     string    `json:"userA"     gorm:"column:user_a;primaryKey;size:40"`
	UserB     string    `json:"userB"     gorm:"column:user_b;primaryKey;size:40"`
	SessionID string    `json:"sessionId" gorm:"column:session_id;size:160;not null"`
	CreatedAt time.Time `json:"createdAt" gorm:"column:created_at;autoCreateTime"`
}

// TableName 指定表名。
func (Binding) TableName() string { return "bindings" }

// PairKey 把两个用户 ID 归一化成有序键（a < b），保证 (u1,u2) 与 (u2,u1) 是同一行。
func PairKey(id1, id2 string) (a, b string) {
	if id1 <= id2 {
		return id1, id2
	}
	return id2, id1
}

// OtherUser 返回绑定中对方的用户 ID。
func (b *Binding) OtherUser(userID string) string {
	if b == nil {
		return ""
	}
	if b.UserA == userID {
		return b.UserB
	}
	return b.UserA
}

// GetBindingByPair 查一对用户的绑定；不存在时返回 (nil, nil)。
func (db *DB) GetBindingByPair(ctx context.Context, id1, id2 string) (*Binding, error) {
	if !db.enabled() {
		return nil, errNoDB
	}
	a, b := PairKey(id1, id2)
	var bind Binding
	err := db.gdb.WithContext(ctx).
		Where(map[string]any{"user_a": a, "user_b": b}).
		First(&bind).Error
	return firstOrNil(&bind, err)
}

// GetLatestBindingByUser 查某用户最近一次绑定（当前生效的小窝）；没有则 (nil, nil)。
func (db *DB) GetLatestBindingByUser(ctx context.Context, userID string) (*Binding, error) {
	if !db.enabled() {
		return nil, errNoDB
	}
	var bind Binding
	err := db.gdb.WithContext(ctx).
		Where(map[string]any{"user_a": userID}).
		Or(map[string]any{"user_b": userID}).
		Order("created_at DESC").
		First(&bind).Error
	return firstOrNil(&bind, err)
}

// CreateBinding 写入绑定；同一对用户已存在时不覆盖。
// 返回 created=false 表示该对已有绑定（并发绑定时调用方需复核并清理多余会话）。
func (db *DB) CreateBinding(ctx context.Context, id1, id2, sessionID string) (bool, error) {
	if !db.enabled() {
		return false, errNoDB
	}
	a, b := PairKey(id1, id2)
	res := db.gdb.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(&Binding{UserA: a, UserB: b, SessionID: sessionID})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}
