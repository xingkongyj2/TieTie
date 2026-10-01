package dbop

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm/clause"
)

var ErrAlreadyBound = errors.New("user already bound to another partner")

// Binding 是一对用户的绑定关系（bindings 表）。
// 两个用户 ID 按数值顺序组成复合主键，即"一对用户只有一个共享会话"。
type Binding struct {
	UserA     int64     `json:"userA"     gorm:"column:user_a;primaryKey"`
	UserB     int64     `json:"userB"     gorm:"column:user_b;primaryKey"`
	SessionID string    `json:"sessionId" gorm:"column:session_id;size:160;not null"`
	CreatedAt time.Time `json:"createdAt" gorm:"column:created_at;autoCreateTime"`
}

// TableName 指定表名。
func (Binding) TableName() string { return "bindings" }

// PairKey 把两个用户 ID 归一化成有序键（a < b），保证 (u1,u2) 与 (u2,u1) 是同一行。
func PairKey(id1, id2 int64) (a, b int64) {
	if id1 <= id2 {
		return id1, id2
	}
	return id2, id1
}

// OtherUser 返回绑定中对方的用户 ID。
func (b *Binding) OtherUser(userID int64) int64 {
	if b == nil {
		return 0
	}
	if b.UserA == userID {
		return b.UserB
	}
	return b.UserA
}

// GetBindingByPair 查一对用户的绑定；不存在时返回 (nil, nil)。
func (db *DB) GetBindingByPair(ctx context.Context, id1, id2 int64) (*Binding, error) {
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
func (db *DB) GetLatestBindingByUser(ctx context.Context, userID int64) (*Binding, error) {
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
func (db *DB) CreateBinding(ctx context.Context, id1, id2 int64, sessionID string) (bool, error) {
	if !db.enabled() {
		return false, errNoDB
	}
	a, b := PairKey(id1, id2)
	res := db.gdb.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(&Binding{UserA: a, UserB: b, SessionID: sessionID})
	if res.Error != nil {
		if strings.Contains(res.Error.Error(), "binding_user_already_bound") {
			existing, err := db.GetBindingByPair(ctx, a, b)
			if err != nil {
				return false, err
			}
			if existing != nil {
				return false, nil
			}
			return false, ErrAlreadyBound
		}
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}
