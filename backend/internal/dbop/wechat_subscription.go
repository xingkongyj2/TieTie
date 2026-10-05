package dbop

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrWechatIdentityMissing = errors.New("wechat identity missing")
var ErrWechatSubscriptionInvalid = errors.New("invalid wechat subscription")

// Balances are local reservations, not proof that WeChat will accept a send.
// WeChat remains authoritative and an unauthorized response revokes the balance.
type WechatSubscription struct {
	UserID     int64     `gorm:"primaryKey;autoIncrement:false"`
	AppID      string    `gorm:"primaryKey;size:32"`
	TemplateID string    `gorm:"primaryKey;size:128"`
	Remaining  int64     `gorm:"not null;default:0"`
	Permanent  bool      `gorm:"not null;default:false"`
	Revision   int64     `gorm:"not null;default:0"`
	UpdatedAt  time.Time `gorm:"type:datetime(6);autoUpdateTime"`
}

func (WechatSubscription) TableName() string { return "wechat_subscriptions" }

type WechatSubscriptionReceipt struct {
	UserID     int64     `gorm:"primaryKey;autoIncrement:false"`
	AppID      string    `gorm:"primaryKey;size:32"`
	TemplateID string    `gorm:"primaryKey;size:128"`
	RequestID  string    `gorm:"primaryKey;size:128"`
	Result     string    `gorm:"size:16;not null"`
	CreatedAt  time.Time `gorm:"type:datetime(6);autoCreateTime"`
}

func (WechatSubscriptionReceipt) TableName() string { return "wechat_subscription_receipts" }

func (db *DB) GetWechatIdentityByUser(ctx context.Context, userID int64, appID string) (*WechatIdentity, error) {
	if !db.enabled() {
		return nil, errNoDB
	}
	var identity WechatIdentity
	err := db.gdb.WithContext(ctx).Where("user_id = ? AND app_id = ?", userID, appID).Take(&identity).Error
	return firstOrNil(&identity, err)
}
func (db *DB) GetWechatSubscription(ctx context.Context, userID int64, appID, templateID string) (*WechatSubscription, error) {
	if !db.enabled() {
		return nil, errNoDB
	}
	var subscription WechatSubscription
	err := db.gdb.WithContext(ctx).Where("user_id = ? AND app_id = ? AND template_id = ?", userID, appID, templateID).Take(&subscription).Error
	return firstOrNil(&subscription, err)
}

// Every prompt has one stable request ID. Retrying its HTTP result never grants
// an extra send. A declined prompt does not erase an earlier accepted prompt.
func (db *DB) RecordWechatSubscription(ctx context.Context, userID int64, appID, templateID, result, requestID, subscriptionType string) error {
	if !db.enabled() {
		return errNoDB
	}
	if userID <= 0 || appID == "" || templateID == "" || len(templateID) > 128 || strings.TrimSpace(requestID) == "" || len(requestID) > 128 || (result != "accept" && result != "reject" && result != "ban") || (subscriptionType != "once" && subscriptionType != "permanent") {
		return ErrWechatSubscriptionInvalid
	}
	return db.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var identity WechatIdentity
		if err := tx.Where("user_id = ? AND app_id = ?", userID, appID).Take(&identity).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrWechatIdentityMissing
		} else if err != nil {
			return err
		}
		receipt := WechatSubscriptionReceipt{UserID: userID, AppID: appID, TemplateID: templateID, RequestID: requestID, Result: result}
		created := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&receipt)
		if created.Error != nil {
			return created.Error
		}
		if created.RowsAffected == 0 || result != "accept" {
			return nil
		}
		subscription := WechatSubscription{UserID: userID, AppID: appID, TemplateID: templateID, Revision: 1}
		changes := map[string]any{"updated_at": time.Now().UTC(), "revision": gorm.Expr("revision + 1")}
		if subscriptionType == "permanent" {
			subscription.Permanent = true
			changes["permanent"] = true
		} else {
			subscription.Remaining = 1
			changes["remaining"] = gorm.Expr("remaining + 1")
		}
		return tx.Clauses(clause.OnConflict{DoUpdates: clause.Assignments(changes)}).Create(&subscription).Error
	})
}
