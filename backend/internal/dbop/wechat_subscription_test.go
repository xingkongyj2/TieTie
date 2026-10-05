package dbop

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func expectSubscriptionIdentity(mock sqlmock.Sqlmock, userID int64, exists bool) {
	rows := sqlmock.NewRows([]string{"user_id", "app_id", "open_id"})
	if exists {
		rows.AddRow(userID, "wx-test", "private-openid")
	}
	mock.ExpectQuery("SELECT .* FROM `wechat_identities`").WithArgs(userID, "wx-test", 1).WillReturnRows(rows)
}
func TestWechatSubscriptionReceiptGrantsOnlyOneSendAndDeclinesPreserveBalance(t *testing.T) {
	for _, tc := range []struct {
		name, result string
		created      int64
		increment    bool
	}{{"accepted", "accept", 1, true}, {"duplicate", "accept", 0, false}, {"rejected", "reject", 1, false}, {"banned", "ban", 1, false}} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock := mockWechatDB(t)
			mock.ExpectBegin()
			expectSubscriptionIdentity(mock, 8, true)
			mock.ExpectExec("INSERT INTO `wechat_subscription_receipts`").WithArgs(int64(8), "wx-test", "template", "same-prompt", tc.result, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, tc.created))
			if tc.increment {
				mock.ExpectExec("INSERT INTO `wechat_subscriptions`.*remaining.*ON DUPLICATE KEY UPDATE.*remaining.*remaining \\+ 1").WillReturnResult(sqlmock.NewResult(0, 1))
			}
			mock.ExpectCommit()
			if err := db.RecordWechatSubscription(context.Background(), 8, "wx-test", "template", tc.result, "same-prompt", "once"); err != nil {
				t.Fatal(err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestWechatSubscriptionRequiresVerifiedAccountIdentity(t *testing.T) {
	db, mock := mockWechatDB(t)
	mock.ExpectBegin()
	expectSubscriptionIdentity(mock, 8, false)
	mock.ExpectRollback()
	if err := db.RecordWechatSubscription(context.Background(), 8, "wx-test", "template", "accept", "prompt", "once"); err != ErrWechatIdentityMissing {
		t.Fatalf("err=%v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
func TestWechatSubscriptionOnceNeverGrantsPermanentAccess(t *testing.T) {
	db, mock := mockWechatDB(t)
	mock.ExpectBegin()
	expectSubscriptionIdentity(mock, 8, true)
	mock.ExpectExec("INSERT INTO `wechat_subscription_receipts`").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO `wechat_subscriptions`").WithArgs(int64(8), "wx-test", "template", int64(1), false, int64(1), sqlmock.AnyArg(), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if err := db.RecordWechatSubscription(context.Background(), 8, "wx-test", "template", "accept", "prompt", "once"); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
func TestWechatSubscriptionInvalidDoesNotWrite(t *testing.T) {
	db, mock := mockWechatDB(t)
	for _, result := range []string{"accepted", "", "fake"} {
		if err := db.RecordWechatSubscription(context.Background(), 8, "wx-test", "template", result, "prompt", "once"); err != ErrWechatSubscriptionInvalid {
			t.Fatalf("err=%v", err)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
func notificationRows(job WechatNotification) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "source_type", "source_id", "recipient_id", "session_id", "binding_created_at", "app_id", "template_id", "state", "token", "created_at", "attempts", "quota_reserved", "permanent", "subscription_revision"}).AddRow(job.ID, job.SourceType, job.SourceID, job.RecipientID, job.SessionID, job.BindingCreatedAt, job.AppID, job.TemplateID, job.State, job.Token, job.CreatedAt, job.Attempts, job.QuotaReserved, job.Permanent, job.SubscriptionRevision)
}
func testWechatJob(now time.Time) WechatNotification {
	return WechatNotification{ID: "push-1", SourceType: "reminder", SourceID: "rem-1", RecipientID: 8, SessionID: "space", BindingCreatedAt: now.Add(-time.Hour), AppID: "wx-test", TemplateID: "template", State: WechatNotificationLeased, Token: "lease-1", CreatedAt: now.Add(-time.Minute)}
}
func expectNotificationPrepare(mock sqlmock.Sqlmock, job WechatNotification) {
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `wechat_notifications`.*FOR UPDATE").WithArgs(job.ID, job.Token, WechatNotificationLeased, 1).WillReturnRows(notificationRows(job))
}
func expectNotificationBinding(mock sqlmock.Sqlmock, epoch time.Time) {
	mock.ExpectQuery("SELECT .* FROM `bindings`").WithArgs("space", 1).WillReturnRows(sqlmock.NewRows([]string{"user_a", "user_b", "session_id", "created_at"}).AddRow(8, 9, "space", epoch))
}
func expectNotificationSource(mock sqlmock.Sqlmock, job WechatNotification, recipients string) {
	mock.ExpectQuery("SELECT .* FROM `reminders`").WithArgs(job.SourceID, 1).WillReturnRows(sqlmock.NewRows([]string{"id", "session_id", "binding_created_at", "status", "recipient_ids", "delivered_at"}).AddRow(job.SourceID, job.SessionID, job.BindingCreatedAt, ReminderDelivered, recipients, job.CreatedAt))
}
func TestWechatNotificationSkipsChangedBindingBeforeIdentityOrContentAccess(t *testing.T) {
	now := time.Now().UTC()
	job := testWechatJob(now)
	db, mock := mockWechatDB(t)
	db.SetWechatNotifications(WechatNotificationOptions{Enabled: true, AppID: job.AppID, TemplateID: job.TemplateID, SubscriptionType: "once"})
	expectNotificationPrepare(mock, job)
	expectNotificationBinding(mock, now)
	mock.ExpectExec("UPDATE `wechat_notifications`").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	openID, ready, err := db.PrepareWechatNotification(context.Background(), job, now)
	if err != nil || ready || openID != "" {
		t.Fatalf("openID=%q ready=%v err=%v", openID, ready, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
func TestWechatNotificationSkipsRemovedRecipient(t *testing.T) {
	now := time.Now().UTC()
	job := testWechatJob(now)
	db, mock := mockWechatDB(t)
	db.SetWechatNotifications(WechatNotificationOptions{Enabled: true, AppID: job.AppID, TemplateID: job.TemplateID, SubscriptionType: "once"})
	expectNotificationPrepare(mock, job)
	expectNotificationBinding(mock, job.BindingCreatedAt)
	expectNotificationSource(mock, job, "[9]")
	mock.ExpectExec("UPDATE `wechat_notifications`").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	_, ready, err := db.PrepareWechatNotification(context.Background(), job, now)
	if err != nil || ready {
		t.Fatalf("ready=%v err=%v", ready, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
func TestWechatNotificationReservesOneQuotaBeforeSend(t *testing.T) {
	now := time.Now().UTC()
	job := testWechatJob(now)
	db, mock := mockWechatDB(t)
	db.SetWechatNotifications(WechatNotificationOptions{Enabled: true, AppID: job.AppID, TemplateID: job.TemplateID, SubscriptionType: "once"})
	expectNotificationPrepare(mock, job)
	expectNotificationBinding(mock, job.BindingCreatedAt)
	expectNotificationSource(mock, job, "[8]")
	expectSubscriptionIdentity(mock, 8, true)
	mock.ExpectQuery("SELECT .* FROM `wechat_subscriptions`.*FOR UPDATE").WithArgs(int64(8), "wx-test", "template", 1).WillReturnRows(sqlmock.NewRows([]string{"user_id", "app_id", "template_id", "remaining", "permanent"}).AddRow(8, "wx-test", "template", 2, true))
	mock.ExpectExec("UPDATE `wechat_subscriptions` SET `remaining`=remaining - 1").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE `wechat_notifications` SET.*attempts.*attempts \\+ 1").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	openID, ready, err := db.PrepareWechatNotification(context.Background(), job, now)
	if err != nil || !ready || openID != "private-openid" {
		t.Fatalf("openID=%q ready=%v err=%v", openID, ready, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
func TestWechatNotificationUnknownOutcomeKeepsQuotaAndDoesNotRetry(t *testing.T) {
	now := time.Now().UTC()
	job := testWechatJob(now)
	job.State = WechatNotificationSending
	job.QuotaReserved = true
	job.Attempts = 1
	db, mock := mockWechatDB(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `wechat_notifications`.*FOR UPDATE").WithArgs(job.ID, job.Token, WechatNotificationSending, 1).WillReturnRows(notificationRows(job))
	mock.ExpectExec("UPDATE `wechat_notifications` SET").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if err := db.FinishWechatNotification(context.Background(), job, WechatNotificationUncertain, "network response unknown", false, false, now); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
func TestWechatNotificationKnownFailureReturnsQuotaAndRetriesBoundedly(t *testing.T) {
	for _, attempt := range []int{1, 3} {
		t.Run(string(rune('0'+attempt)), func(t *testing.T) {
			now := time.Now().UTC()
			job := testWechatJob(now)
			job.State = WechatNotificationSending
			job.QuotaReserved = true
			job.Attempts = attempt
			db, mock := mockWechatDB(t)
			mock.ExpectBegin()
			mock.ExpectQuery("SELECT .* FROM `wechat_notifications`.*FOR UPDATE").WithArgs(job.ID, job.Token, WechatNotificationSending, 1).WillReturnRows(notificationRows(job))
			mock.ExpectExec("UPDATE `wechat_subscriptions` SET `remaining`=remaining \\+ 1").WillReturnResult(sqlmock.NewResult(0, 1))
			if attempt == 1 {
				mock.ExpectExec("UPDATE `wechat_notifications` SET.*quota_reserved.*run_at").WillReturnResult(sqlmock.NewResult(0, 1))
			} else {
				mock.ExpectExec("UPDATE `wechat_notifications` SET.*lease_until.*state").WillReturnResult(sqlmock.NewResult(0, 1))
			}
			mock.ExpectCommit()
			if err := db.FinishWechatNotification(context.Background(), job, WechatNotificationFailed, "rejected", true, false, now); err != nil {
				t.Fatal(err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
