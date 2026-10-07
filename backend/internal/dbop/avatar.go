package dbop

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Avatar images live with the account database, independently of server disks.
// IDs are random capabilities used by native Image components without JWT headers.
type UserAvatar struct {
	ID        string    `gorm:"primaryKey;size:32"`
	UserID    int64     `gorm:"index;not null"`
	MimeType  string    `gorm:"size:32;not null"`
	Content   []byte    `gorm:"type:mediumblob;not null"`
	CreatedAt time.Time `gorm:"type:datetime(6)"`
}

func (db *DB) SaveUserAvatar(ctx context.Context, userID int64, mimeType string, content []byte) (*UserAvatar, error) {
	if db == nil || db.gdb == nil || userID <= 0 {
		return nil, errors.New("avatar storage unavailable")
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, err
	}
	avatar := &UserAvatar{ID: hex.EncodeToString(random[:]), UserID: userID, MimeType: mimeType, Content: content}
	if err := db.gdb.WithContext(ctx).Create(avatar).Error; err != nil {
		return nil, err
	}
	return avatar, nil
}

func (db *DB) GetUserAvatar(ctx context.Context, id string) (*UserAvatar, error) {
	if db == nil || db.gdb == nil {
		return nil, errors.New("avatar storage unavailable")
	}
	var avatar UserAvatar
	err := db.gdb.WithContext(ctx).Where("id=?", id).First(&avatar).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &avatar, err
}

// Explicit login choices replace only name/avatar. The image, profile, and
// profile memory changes commit together before a login token is issued.
func (db *DB) SaveWechatChosenProfile(ctx context.Context, userID int64, nickname, mimeType string, content []byte) error {
	if db == nil || db.gdb == nil {
		return errors.New("profile storage unavailable")
	}
	return db.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=?", userID).First(&user).Error; err != nil {
			return err
		}
		store := &DB{gdb: tx}
		profile, err := store.GetUserProfile(ctx, userID)
		if err != nil {
			return err
		}
		avatar, err := store.SaveUserAvatar(ctx, userID, mimeType, content)
		if err != nil {
			return err
		}
		profile.Name, profile.Avatar = nickname, "/api/assets/avatars/"+avatar.ID
		_, err = store.SaveUserProfile(ctx, *profile, true)
		return err
	})
}
