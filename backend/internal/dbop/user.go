package dbop

import (
	"context"
	"time"
)

// User 是注册用户（users 表）。
type User struct {
	ID           string    `json:"userId"    gorm:"column:id;primaryKey;size:40"`
	Username     string    `json:"username"  gorm:"column:username;uniqueIndex;size:24;not null"`
	PasswordHash string    `json:"-"         gorm:"column:password_hash;size:100;not null"`
	Code         string    `json:"code"      gorm:"column:code;uniqueIndex;size:8;not null"`
	CreatedAt    time.Time `json:"createdAt" gorm:"column:created_at;autoCreateTime"`
}

// TableName 指定表名。
func (User) TableName() string { return "users" }

// CreateUser 写入新用户；username / code 冲突时返回错误。
func (db *DB) CreateUser(ctx context.Context, u *User) error {
	if !db.enabled() {
		return errNoDB
	}
	return db.gdb.WithContext(ctx).Create(u).Error
}

// GetUserByID 按用户 ID 查询；不存在时返回 (nil, nil)。
func (db *DB) GetUserByID(ctx context.Context, id string) (*User, error) {
	if !db.enabled() {
		return nil, errNoDB
	}
	var u User
	err := db.gdb.WithContext(ctx).Where(map[string]any{"id": id}).First(&u).Error
	return firstOrNil(&u, err)
}

// GetUserByUsername 按用户名查询（登录用）；不存在时返回 (nil, nil)。
func (db *DB) GetUserByUsername(ctx context.Context, username string) (*User, error) {
	if !db.enabled() {
		return nil, errNoDB
	}
	var u User
	err := db.gdb.WithContext(ctx).Where(map[string]any{"username": username}).First(&u).Error
	return firstOrNil(&u, err)
}

// GetUserByCode 按邀请码查询（绑定时找对方）；不存在时返回 (nil, nil)。
func (db *DB) GetUserByCode(ctx context.Context, code string) (*User, error) {
	if !db.enabled() {
		return nil, errNoDB
	}
	var u User
	err := db.gdb.WithContext(ctx).Where(map[string]any{"code": code}).First(&u).Error
	return firstOrNil(&u, err)
}

// CodeExists 检查邀请码是否已被占用（生成时防撞）。
func (db *DB) CodeExists(ctx context.Context, code string) (bool, error) {
	if !db.enabled() {
		return false, errNoDB
	}
	var n int64
	err := db.gdb.WithContext(ctx).Model(&User{}).Where(map[string]any{"code": code}).Count(&n).Error
	return n > 0, err
}
