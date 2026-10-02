package dbop

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrAlreadyBound = errors.New("user already bound to another partner")

// sessionTitlePrefix 是配对会话标题的前缀，便于在云端会话列表里认出是贴贴建的会话。
const sessionTitlePrefix = "TieTie-"

// Binding 是一对用户的绑定关系（bindings 表）。
// 两个用户 ID 按数值顺序组成复合主键，即"一对用户只有一个共享会话"。
type Binding struct {
	UserA     int64     `json:"userA"     gorm:"column:user_a;primaryKey"`
	UserB     int64     `json:"userB"     gorm:"column:user_b;primaryKey"`
	SessionID string    `json:"sessionId" gorm:"column:session_id;size:160;not null;index:idx_bindings_session"`
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

// SessionTitle 是一对绑定共用的云端会话标题：小 ID 在前、大 ID 在后。
// 标题仅用于展示；新绑定不靠标题恢复旧会话或旧记忆。
func SessionTitle(id1, id2 int64) string {
	a, b := PairKey(id1, id2)
	return fmt.Sprintf("%s%d-%d", sessionTitlePrefix, a, b)
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

// Unbind 解除用户当前生效的绑定：原记录归档进 archived_bindings 后从 bindings 删除。
// 一行绑定由双方共享，所以解绑后两人都回到未绑定状态；云端会话不删除，历史仍在云端。
// 没有绑定时返回 (nil, nil)。
func (db *DB) Unbind(ctx context.Context, userID int64) (*Binding, error) {
	if !db.enabled() {
		return nil, errNoDB
	}
	binding, err := db.GetLatestBindingByUser(ctx, userID)
	if err != nil || binding == nil {
		return nil, err
	}
	err = db.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&CareMode{}).Where("session_id=? AND binding_created_at=?", binding.SessionID, binding.CreatedAt).Updates(map[string]any{"enabled": false, "state": "off", "token": ""}).Error; err != nil {
			return err
		}
		if err := tx.Model(&AnniversaryReminderSettings{}).Where("session_id=? AND binding_created_at=?", binding.SessionID, binding.CreatedAt).Updates(map[string]any{"enabled": false, "token": ""}).Error; err != nil {
			return err
		}
		if err := tx.Exec(`INSERT OR IGNORE INTO archived_bindings (user_a, user_b, session_id, created_at) VALUES (?, ?, ?, ?)`,
			binding.UserA, binding.UserB, binding.SessionID, binding.CreatedAt).Error; err != nil {
			return err
		}
		return tx.Where("user_a = ? AND user_b = ?", binding.UserA, binding.UserB).Delete(&Binding{}).Error
	})
	if err != nil {
		return nil, err
	}
	return binding, nil
}

// CreateInitializedBinding publishes the binding, store mapping and seeded
// index atomically, so no member can enter a half-initialized space.
func (db *DB) CreateInitializedBinding(ctx context.Context, id1, id2 int64, session string, store SpaceMemoryStore, records []MemoryRecord) (bool, error) {
	if !db.enabled() {
		return false, errNoDB
	}
	a, b := PairKey(id1, id2)
	created := false
	err := db.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		binding := Binding{UserA: a, UserB: b, SessionID: session}
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&binding)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		if err := tx.Create(&store).Error; err != nil {
			return err
		}
		for i := range records {
			records[i].BindingCreatedAt = binding.CreatedAt
		}
		if err := tx.Create(&records).Error; err != nil {
			return err
		}
		if err := seedUserProfiles(tx, binding); err != nil {
			return err
		}
		created = true
		return nil
	})
	if err != nil && strings.Contains(err.Error(), "binding_user_already_bound") {
		if existing, lookupErr := db.GetBindingByPair(ctx, a, b); lookupErr != nil {
			return false, lookupErr
		} else if existing != nil {
			return false, nil
		}
		return false, ErrAlreadyBound
	}
	return created, err
}
