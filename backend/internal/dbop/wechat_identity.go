package dbop

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
)

// WechatIdentity is independent from the public username. The composite primary
// key prevents the same mini-program identity from belonging to two accounts.
type WechatIdentity struct {
	AppID     string    `json:"-" gorm:"column:app_id;primaryKey;size:32"`
	OpenID    string    `json:"-" gorm:"column:open_id;primaryKey;type:varbinary(128)"`
	UserID    int64     `json:"-" gorm:"column:user_id;not null;uniqueIndex"`
	CreatedAt time.Time `json:"-" gorm:"column:created_at;autoCreateTime;type:datetime(6)"`
	User      User      `json:"-" gorm:"foreignKey:UserID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
}

func (WechatIdentity) TableName() string { return "wechat_identities" }

// GetOrCreateWechatUser returns the existing account or atomically creates the
// account and identity. Conflicting concurrent logins roll back their new user
// before reading the winner, so they cannot leave orphan accounts.
func (db *DB) GetOrCreateWechatUser(ctx context.Context, appID, openID string) (*User, bool, error) {
	if !db.enabled() {
		return nil, false, errNoDB
	}
	if appID == "" || len(appID) > 32 || openID == "" || len(openID) > 128 {
		return nil, false, errors.New("invalid verified wechat identity")
	}
	if user, err := db.getWechatUser(ctx, appID, openID); user != nil || err != nil {
		return user, false, err
	}
	for attempt := 0; attempt < 10; attempt++ {
		user, err := newWechatUser()
		if err != nil {
			return nil, false, err
		}
		err = db.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := tx.Create(user).Error; err != nil {
				return err
			}
			return tx.Create(&WechatIdentity{AppID: appID, OpenID: openID, UserID: user.ID}).Error
		})
		if err == nil {
			return user, true, nil
		}
		var mysqlError *mysql.MySQLError
		if !errors.As(err, &mysqlError) || (mysqlError.Number != 1062 && mysqlError.Number != 1213 && mysqlError.Number != 1205) {
			return nil, false, err
		}
		// A competing identity insert may already have committed. Otherwise the
		// conflict was a random username/code collision or a rolled-back deadlock.
		if existing, lookupErr := db.getWechatUser(ctx, appID, openID); existing != nil || lookupErr != nil {
			return existing, false, lookupErr
		}
	}
	return nil, false, errors.New("could not allocate a wechat account")
}

func (db *DB) getWechatUser(ctx context.Context, appID, openID string) (*User, error) {
	var identity WechatIdentity
	err := db.gdb.WithContext(ctx).Where("app_id = ? AND open_id = ?", appID, openID).Take(&identity).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	// A missing referenced account is a data error, not a new registration.
	var user User
	if err := db.gdb.WithContext(ctx).First(&user, identity.UserID).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

func newWechatUser() (*User, error) {
	bytes := make([]byte, 6)
	if _, err := rand.Read(bytes); err != nil {
		return nil, err
	}
	code, err := rand.Int(rand.Reader, big.NewInt(10000))
	if err != nil {
		return nil, err
	}
	return &User{
		Username: "微信用户_" + hex.EncodeToString(bytes),
		Password: "", // Existing password login explicitly rejects empty passwords.
		Code:     fmt.Sprintf("%04d", code.Int64()),
	}, nil
}
