package dbop

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"tietie/backend/internal/logging"
)

const (
	WechatNotificationPending   = "pending"
	WechatNotificationLeased    = "leased"
	WechatNotificationSending   = "sending"
	WechatNotificationSent      = "sent"
	WechatNotificationSkipped   = "skipped"
	WechatNotificationFailed    = "failed"
	WechatNotificationUncertain = "uncertain"
)

// This configuration is set once before the server starts. Disabled instances
// never enqueue historical notifications to be replayed after configuration.
type WechatNotificationOptions struct {
	Enabled                             bool
	AppID, TemplateID, SubscriptionType string
}

func (db *DB) SetWechatNotifications(options WechatNotificationOptions) {
	if db != nil {
		db.wechatNotifications = options
	}
}

type WechatNotification struct {
	ID                   string    `gorm:"primaryKey;size:64"`
	SourceType           string    `gorm:"not null;uniqueIndex:idx_wechat_notification_source,priority:1;size:32"`
	SourceID             string    `gorm:"not null;uniqueIndex:idx_wechat_notification_source,priority:2;size:160"`
	RecipientID          int64     `gorm:"not null;uniqueIndex:idx_wechat_notification_source,priority:3"`
	SessionID            string    `gorm:"size:160;not null"`
	BindingCreatedAt     time.Time `gorm:"not null;type:datetime(6)"`
	AppID                string    `gorm:"size:32;not null"`
	TemplateID           string    `gorm:"size:128;not null"`
	Title                string    `gorm:"type:text"`
	Content              string    `gorm:"type:mediumtext"`
	DueAt                time.Time `gorm:"type:datetime(6)"`
	Page                 string    `gorm:"size:1024"`
	State                string    `gorm:"size:32;index:idx_wechat_notification_due,priority:1"`
	RunAt                time.Time `gorm:"type:datetime(6);index:idx_wechat_notification_due,priority:2"`
	LeaseUntil           time.Time `gorm:"type:datetime(6)"`
	Token                string    `gorm:"size:64"`
	Attempts             int
	QuotaReserved        bool
	Permanent            bool
	SubscriptionRevision int64
	LastError            string     `gorm:"size:255"`
	CreatedAt            time.Time  `gorm:"type:datetime(6);autoCreateTime"`
	FinishedAt           *time.Time `gorm:"type:datetime(6)"`
}

func (WechatNotification) TableName() string { return "wechat_notifications" }

type WechatReminderContent struct{ Text, MessageID string }

func wechatNotificationPage(space, messageID, reminderID string) string {
	query := url.Values{"from": {"reminder"}, "sessionId": {space}}
	if messageID != "" {
		query.Set("messageId", messageID)
	}
	if reminderID != "" {
		query.Set("reminderId", reminderID)
	}
	return "pages/index/index?" + query.Encode()
}

func (db *DB) enqueueWechatReminder(tx *gorm.DB, reminder Reminder, content WechatReminderContent) error {
	if !db.wechatNotifications.Enabled {
		return nil
	}
	b, err := bindingForSession(tx, reminder.SessionID)
	if err != nil {
		return err
	}
	if b == nil || !b.CreatedAt.Equal(reminder.BindingCreatedAt) {
		return nil
	}
	spaceID := reminder.SessionID
	messageID := content.MessageID
	if reminder.Visibility == "private" {
		var private PrivateChannel
		if err := tx.Where("session_id = ? AND binding_created_at = ?", reminder.SessionID, b.CreatedAt).Take(&private).Error; err != nil {
			return err
		}
		spaceID = private.SpaceID
		messageID = "" // Private cloud event IDs are never published as shared links.
	}
	text := strings.TrimSpace(content.Text)
	if text == "" {
		text = reminder.Title
	}
	return db.enqueueWechatNotifications(tx, "reminder", reminder.ID, reminder.SessionID, *b, reminder.RecipientIDs, reminder.Title, text, reminder.DueAt, wechatNotificationPage(spaceID, messageID, reminder.ID))
}
func (db *DB) enqueueWechatCare(tx *gorm.DB, report CareReport, b Binding, due time.Time) error {
	if !db.wechatNotifications.Enabled || (report.Mode != "morning" && report.Mode != "night" && report.Mode != "anniversary" && report.Mode != "countdown") {
		return nil
	}
	title := "纪念日提醒"
	if report.Mode == "morning" {
		title = "早安提醒"
	}
	if report.Mode == "night" {
		title = "晚安提醒"
	}
	if report.Mode == "countdown" {
		title = "倒计时提醒"
	}
	page := wechatNotificationPage(report.SessionID, report.ID, "")
	if len(report.Cards) == 0 {
		return db.enqueueWechatNotifications(tx, "care", report.ID, report.SessionID, b, []int64{b.UserA, b.UserB}, title, report.Text, due, page)
	}
	for _, recipient := range []int64{b.UserA, b.UserB} {
		if err := db.enqueueWechatNotifications(tx, "care", report.ID, report.SessionID, b, []int64{recipient}, title, wechatWeatherContent(report, recipient), due, page); err != nil {
			return err
		}
	}
	return nil
}

func wechatWeatherContent(report CareReport, recipient int64) string {
	summary := "天气提醒已更新"
	for _, card := range report.Cards {
		if len(card.RecipientIDs) > 0 && !slices.Contains(card.RecipientIDs, recipient) {
			continue
		}
		day := "今天"
		if card.Mode == "night" || card.Mode == "query_tomorrow" {
			day = "明天"
		}
		if strings.TrimSpace(card.Description) != "" {
			summary = fmt.Sprintf("%s%s %.0f-%.0f℃", day, card.Description, card.Day.Min, card.Day.Max)
		}
		for _, view := range card.Views {
			if len(view.RecipientIDs) > 0 && !slices.Contains(view.RecipientIDs, recipient) {
				continue
			}
			for _, line := range view.Summary {
				if strings.TrimSpace(line) != "" {
					summary = line
					break
				}
			}
			break
		}
		break
	}
	// Reserve the call to action before the template's 20-character truncation.
	const suffix = "，进入小程序查看"
	summary = strings.TrimRight(strings.Join(strings.Fields(summary), " "), "，。；、,. ;")
	runes := []rune(summary)
	limit := 20 - len([]rune(suffix))
	if len(runes) > limit {
		summary = string(runes[:limit-1]) + "…"
	}
	return summary + suffix
}

func (db *DB) enqueueWechatNotifications(tx *gorm.DB, kind, id, session string, b Binding, recipients []int64, title, text string, due time.Time, page string) error {
	options := db.wechatNotifications
	if !options.Enabled {
		return nil
	}
	now := time.Now().UTC()
	rows := make([]WechatNotification, 0, len(recipients))
	seen := map[int64]bool{}
	for _, recipient := range recipients {
		if seen[recipient] || (recipient != b.UserA && recipient != b.UserB) {
			continue
		}
		seen[recipient] = true
		rows = append(rows, WechatNotification{ID: MemoryID(kind+id, fmt.Sprint(recipient)), SourceType: kind, SourceID: id, RecipientID: recipient, SessionID: session, BindingCreatedAt: b.CreatedAt, AppID: options.AppID, TemplateID: options.TemplateID, Title: title, Content: text, DueAt: due.UTC(), Page: page, State: WechatNotificationPending, RunAt: now, LeaseUntil: epochTime})
	}
	if len(rows) == 0 {
		return nil
	}
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&rows).Error
}

// Recover expired leases on every claim, including after a crash. A send which
// may have reached WeChat is never automatically repeated.
func (db *DB) ClaimWechatNotifications(ctx context.Context, now time.Time, limit int) ([]WechatNotification, error) {
	if !db.enabled() {
		return nil, errNoDB
	}
	if limit <= 0 || limit > 128 {
		limit = 16
	}
	out := []WechatNotification{}
	err := claimWithLock(ctx, db.gdb, "tietie_claim_wechat_notifications", func(tx *gorm.DB) error {
		if err := tx.Model(&WechatNotification{}).Where("state = ? AND lease_until <= ?", WechatNotificationLeased, now.UTC()).Updates(map[string]any{"state": WechatNotificationPending, "token": "", "lease_until": epochTime}).Error; err != nil {
			return err
		}
		if err := tx.Model(&WechatNotification{}).Where("state = ? AND lease_until <= ?", WechatNotificationSending, now.UTC()).Updates(map[string]any{"state": WechatNotificationUncertain, "last_error": "发送中断，微信接收结果未知", "finished_at": now.UTC(), "token": ""}).Error; err != nil {
			return err
		}
		var rows []WechatNotification
		if err := tx.Where("state = ? AND run_at <= ?", WechatNotificationPending, now.UTC()).Order("run_at ASC,id ASC").Limit(limit).Find(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			bytes := make([]byte, 16)
			if _, err := rand.Read(bytes); err != nil {
				return err
			}
			row.Token = hex.EncodeToString(bytes)
			row.State = WechatNotificationLeased
			row.LeaseUntil = now.Add(2 * time.Minute).UTC()
			result := tx.Model(&WechatNotification{}).Where("id = ? AND state = ?", row.ID, WechatNotificationPending).Updates(map[string]any{"state": row.State, "token": row.Token, "lease_until": row.LeaseUntil})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected > 0 {
				out = append(out, row)
			}
		}
		return nil
	})
	return out, err
}

// Prepare performs the last membership and recipient checks, then atomically
// reserves one accepted prompt before moving into the potentially uncertain send.
func (db *DB) PrepareWechatNotification(ctx context.Context, job WechatNotification, now time.Time) (openID string, ready bool, err error) {
	if !db.enabled() {
		return "", false, errNoDB
	}
	skippedReason := ""
	err = db.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current WechatNotification
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND token = ? AND state = ?", job.ID, job.Token, WechatNotificationLeased).Take(&current).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		} else if err != nil {
			return err
		}
		skip := func(reason string) error {
			err := tx.Model(&current).Updates(map[string]any{"state": WechatNotificationSkipped, "last_error": reason, "finished_at": now.UTC(), "token": ""}).Error
			if err == nil {
				skippedReason = reason
			}
			return err
		}
		options := db.wechatNotifications
		if !options.Enabled || current.AppID != options.AppID || current.TemplateID != options.TemplateID {
			return skip("微信提醒配置已关闭或变更")
		}
		// Old notifications are not replayed after a long outage or later opt-in.
		if now.Sub(current.CreatedAt) > 24*time.Hour {
			return skip("提醒已超过发送有效期")
		}
		binding, err := bindingForSession(tx, current.SessionID)
		if err != nil {
			return err
		}
		if binding == nil || !binding.CreatedAt.Equal(current.BindingCreatedAt) || (current.RecipientID != binding.UserA && current.RecipientID != binding.UserB) {
			return skip("绑定关系已变更")
		}
		valid, err := validWechatNotificationSource(tx, current, now)
		if err != nil {
			return err
		}
		if !valid {
			return skip("提醒或接收对象已变更")
		}
		var identity WechatIdentity
		if err := tx.Where("user_id = ? AND app_id = ?", current.RecipientID, current.AppID).Take(&identity).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return skip("用户没有当前小程序的微信身份")
		} else if err != nil {
			return err
		}
		var subscription WechatSubscription
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id = ? AND app_id = ? AND template_id = ?", current.RecipientID, current.AppID, current.TemplateID).Take(&subscription).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return skip("尚未订阅微信提醒")
		} else if err != nil {
			return err
		}
		permanent := options.SubscriptionType == "permanent" && subscription.Permanent
		if !permanent && subscription.Remaining <= 0 {
			return skip("微信提醒订阅次数已用完")
		}
		if !permanent {
			if err := tx.Model(&subscription).Update("remaining", gorm.Expr("remaining - 1")).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&current).Updates(map[string]any{"state": WechatNotificationSending, "quota_reserved": !permanent, "permanent": permanent, "subscription_revision": subscription.Revision, "attempts": gorm.Expr("attempts + 1"), "lease_until": now.Add(2 * time.Minute).UTC()}).Error; err != nil {
			return err
		}
		openID, ready = identity.OpenID, true
		return nil
	})
	if err == nil && skippedReason != "" {
		logging.Scheduler().Info("微信提醒未发送", "event", "wechat.notification_skipped", "notification_id", job.ID, "source_type", job.SourceType, "source_id", job.SourceID, "reason", skippedReason)
	}
	return
}
func validWechatNotificationSource(tx *gorm.DB, job WechatNotification, now time.Time) (bool, error) {
	if job.SourceType == "reminder" {
		var reminder Reminder
		if err := tx.Where("id = ?", job.SourceID).Take(&reminder).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		} else if err != nil {
			return false, err
		}
		valid := reminder.BindingCreatedAt.Equal(job.BindingCreatedAt) && reminder.SessionID == job.SessionID && reminder.DeliveredAt != nil && (reminder.Status == ReminderDelivered || reminder.Status == ReminderCompleted) && slices.Contains(reminder.RecipientIDs, job.RecipientID)
		if !valid || reminder.Visibility != "private" {
			return valid, nil
		}
		var private PrivateChannel
		if err := tx.Where("session_id = ? AND owner_id = ? AND binding_created_at = ?", job.SessionID, job.RecipientID, job.BindingCreatedAt).Take(&private).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		} else if err != nil {
			return false, err
		}
		return true, nil
	}
	if job.SourceType == "care" {
		var report CareReport
		if err := tx.Where("id = ?", job.SourceID).Take(&report).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		} else if err != nil {
			return false, err
		}
		if report.SessionID != job.SessionID || !report.BindingCreatedAt.Equal(job.BindingCreatedAt) {
			return false, nil
		}
		if report.Mode == "countdown" {
			return validWechatCountdownSource(tx, report, now)
		}
		return report.Mode == "morning" || report.Mode == "night" || report.Mode == "anniversary", nil
	}
	return false, nil
}

// A known rejection returns quota only within the same authorization generation.
// Unknown network outcomes retain it to avoid delivering the reminder twice.
func (db *DB) FinishWechatNotification(ctx context.Context, job WechatNotification, state, reason string, retryable, unauthorized bool, now time.Time) error {
	if !db.enabled() {
		return errNoDB
	}
	return db.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current WechatNotification
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND token = ? AND state = ?", job.ID, job.Token, WechatNotificationSending).Take(&current).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		} else if err != nil {
			return err
		}
		// A new accepted prompt or a revocation invalidates older reservations.
		// Preserve newer grants; an old failed send never revives revoked balance.
		subscription := tx.Model(&WechatSubscription{}).Where("user_id = ? AND app_id = ? AND template_id = ? AND revision = ?", current.RecipientID, current.AppID, current.TemplateID, current.SubscriptionRevision)
		if unauthorized {
			if err := subscription.Updates(map[string]any{"remaining": 0, "permanent": false, "revision": gorm.Expr("revision + 1")}).Error; err != nil {
				return err
			}
		} else if state == WechatNotificationFailed && current.QuotaReserved {
			if err := subscription.Update("remaining", gorm.Expr("remaining + 1")).Error; err != nil {
				return err
			}
		}
		updates := map[string]any{"state": state, "last_error": reason, "token": "", "finished_at": now.UTC(), "lease_until": epochTime}
		if state == WechatNotificationFailed && retryable && !unauthorized && current.Attempts < 3 {
			updates["state"] = WechatNotificationPending
			updates["run_at"] = now.Add(time.Duration(current.Attempts) * 30 * time.Second).UTC()
			updates["finished_at"] = nil
			updates["quota_reserved"] = false
		}
		return tx.Model(&current).Updates(updates).Error
	})
}
func (db *DB) ReleaseWechatNotification(ctx context.Context, job WechatNotification, now time.Time) error {
	if !db.enabled() {
		return errNoDB
	}
	return db.gdb.WithContext(ctx).Model(&WechatNotification{}).Where("id = ? AND token = ? AND state = ?", job.ID, job.Token, WechatNotificationLeased).Updates(map[string]any{"state": WechatNotificationPending, "run_at": now.Add(5 * time.Second).UTC(), "token": "", "lease_until": epochTime}).Error
}
