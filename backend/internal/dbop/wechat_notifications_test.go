package dbop

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestWechatNotificationSchemaHasUniqueRecipientAndSource(t *testing.T) {
	model, err := schema.Parse(&WechatNotification{}, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, index := range model.ParseIndexes() {
		if index.Name == "idx_wechat_notification_source" && index.Class == "UNIQUE" {
			found = true
			if len(index.Fields) != 3 || index.Fields[0].DBName != "source_type" || index.Fields[1].DBName != "source_id" || index.Fields[2].DBName != "recipient_id" {
				t.Fatalf("fields=%+v", index.Fields)
			}
		}
	}
	if !found {
		t.Fatal("no recipient/source unique constraint")
	}
}
func TestWechatNotificationDisabledDoesNotAccumulateHistory(t *testing.T) {
	db, mock := mockWechatDB(t)
	if err := db.enqueueWechatReminder(db.gdb, Reminder{}, WechatReminderContent{}); err != nil {
		t.Fatal(err)
	}
	if err := db.enqueueWechatCare(db.gdb, CareReport{Mode: "morning"}, Binding{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
func captureNotifications(t *testing.T, db *DB) *[]WechatNotification {
	t.Helper()
	saved := []WechatNotification{}
	if err := db.gdb.Callback().Create().Before("gorm:create").Register("test:capture_notifications", func(tx *gorm.DB) {
		if rows, ok := tx.Statement.Dest.(*[]WechatNotification); ok {
			saved = append(saved, (*rows)...)
		}
	}); err != nil {
		t.Fatal(err)
	}
	return &saved
}
func TestWechatNotificationOutboxDeduplicatesAndSharesTransaction(t *testing.T) {
	now := time.Now().UTC()
	db, mock := mockWechatDB(t)
	db.SetWechatNotifications(WechatNotificationOptions{Enabled: true, AppID: "wx-test", TemplateID: "template", SubscriptionType: "once"})
	saved := captureNotifications(t, db)
	binding := Binding{SessionID: "space", UserA: 8, UserB: 9, CreatedAt: now.Add(-time.Hour)}
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `wechat_notifications`.*ON DUPLICATE KEY UPDATE").WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()
	err := db.gdb.Transaction(func(tx *gorm.DB) error {
		return db.enqueueWechatNotifications(tx, "reminder", "rem-1", "space", binding, []int64{8, 8, 9, 10}, "title", "content", now, "pages/index/index?sessionId=space")
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(*saved) != 2 || (*saved)[0].RecipientID != 8 || (*saved)[1].RecipientID != 9 || (*saved)[0].ID == (*saved)[1].ID {
		t.Fatalf("rows=%+v", *saved)
	}
	if !(*saved)[0].BindingCreatedAt.Equal(binding.CreatedAt) || (*saved)[0].SourceID != "rem-1" {
		t.Fatal("lost source binding")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
func TestWechatNotificationOutboxFailureRollsBackCaller(t *testing.T) {
	now := time.Now().UTC()
	db, mock := mockWechatDB(t)
	db.SetWechatNotifications(WechatNotificationOptions{Enabled: true, AppID: "wx-test", TemplateID: "template"})
	failure := errors.New("outbox insertion failed")
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `wechat_notifications`").WillReturnError(failure)
	mock.ExpectRollback()
	err := db.gdb.Transaction(func(tx *gorm.DB) error {
		return db.enqueueWechatNotifications(tx, "reminder", "rem-1", "space", Binding{UserA: 8, UserB: 9, CreatedAt: now}, []int64{8}, "title", "content", now, "page")
	})
	if !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
func TestWechatNotificationPrivateLinkUsesSpaceAndOmitsPrivateEvent(t *testing.T) {
	now := time.Now().UTC()
	db, mock := mockWechatDB(t)
	db.SetWechatNotifications(WechatNotificationOptions{Enabled: true, AppID: "wx-test", TemplateID: "template"})
	saved := captureNotifications(t, db)
	reminder := Reminder{ID: "rem-private", SessionID: "private-channel", Visibility: "private", BindingCreatedAt: now.Add(-time.Hour), RecipientIDs: []int64{8}, Title: "private title", DueAt: now}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `bindings`").WithArgs(reminder.SessionID, 1).WillReturnRows(sqlmock.NewRows([]string{"user_a", "user_b", "session_id", "created_at"}))
	mock.ExpectQuery("SELECT .* FROM private_channels AS p.*JOIN bindings").WithArgs(reminder.SessionID, 1).WillReturnRows(sqlmock.NewRows([]string{"user_a", "user_b", "session_id", "created_at"}).AddRow(8, 9, reminder.SessionID, reminder.BindingCreatedAt))
	mock.ExpectQuery("SELECT .* FROM `private_channels`").WithArgs(reminder.SessionID, reminder.BindingCreatedAt, 1).WillReturnRows(sqlmock.NewRows([]string{"session_id", "space_id", "owner_id", "binding_created_at"}).AddRow(reminder.SessionID, "shared-space", 8, reminder.BindingCreatedAt))
	mock.ExpectExec("INSERT INTO `wechat_notifications`").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if err := db.gdb.Transaction(func(tx *gorm.DB) error {
		return db.enqueueWechatReminder(tx, reminder, WechatReminderContent{Text: "private reminder", MessageID: "private-event"})
	}); err != nil {
		t.Fatal(err)
	}
	if len(*saved) != 1 {
		t.Fatalf("rows=%+v", *saved)
	}
	link := (*saved)[0].Page
	query, err := url.ParseQuery(strings.SplitN(link, "?", 2)[1])
	if err != nil {
		t.Fatal(err)
	}
	if query.Get("sessionId") != "shared-space" || query.Get("messageId") != "" || query.Get("reminderId") != reminder.ID || strings.Contains(link, "private-channel") || strings.Contains(link, "private-event") {
		t.Fatalf("link=%q", link)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
func TestWechatNotificationClaimRecoversLeasesButDoesNotReplayUncertainSend(t *testing.T) {
	now := time.Now().UTC()
	job := testWechatJob(now)
	job.State = WechatNotificationPending
	job.Token = ""
	db, mock := mockWechatDB(t)
	lock := "tietie_claim_wechat_notifications"
	mock.ExpectQuery("SELECT GET_LOCK").WithArgs(lock).WillReturnRows(sqlmock.NewRows([]string{"lock"}).AddRow(1))
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `wechat_notifications` SET").WithArgs(epochTime, WechatNotificationPending, "", WechatNotificationLeased, now).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE `wechat_notifications` SET").WithArgs(now, "发送中断，微信接收结果未知", WechatNotificationUncertain, "", WechatNotificationSending, now).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT .* FROM `wechat_notifications`").WithArgs(WechatNotificationPending, now, 16).WillReturnRows(notificationRows(job))
	mock.ExpectExec("UPDATE `wechat_notifications` SET").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectQuery("SELECT RELEASE_LOCK").WithArgs(lock).WillReturnRows(sqlmock.NewRows([]string{"lock"}).AddRow(1))
	rows, err := db.ClaimWechatNotifications(context.Background(), now, 16)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].State != WechatNotificationLeased || rows[0].Token == "" || !rows[0].LeaseUntil.After(now) {
		t.Fatalf("rows=%+v", rows)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
func TestWechatNotificationRevokedPermissionClearsBalance(t *testing.T) {
	now := time.Now().UTC()
	job := testWechatJob(now)
	job.State = WechatNotificationSending
	job.QuotaReserved = true
	job.Attempts = 1
	db, mock := mockWechatDB(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `wechat_notifications`.*FOR UPDATE").WithArgs(job.ID, job.Token, WechatNotificationSending, 1).WillReturnRows(notificationRows(job))
	mock.ExpectExec("UPDATE `wechat_subscriptions` SET `permanent`=\\?,`remaining`=\\?,`revision`=revision \\+ 1").WithArgs(false, 0, sqlmock.AnyArg(), job.RecipientID, job.AppID, job.TemplateID, job.SubscriptionRevision).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE `wechat_notifications` SET").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if err := db.FinishWechatNotification(context.Background(), job, WechatNotificationFailed, "revoked", false, true, now); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestWechatNotificationNewAcceptanceIsNotErasedByOldUnauthorizedSend(t *testing.T) {
	now := time.Now().UTC()
	job := testWechatJob(now)
	job.State = WechatNotificationSending
	job.QuotaReserved = true
	job.Attempts = 1
	job.SubscriptionRevision = 4
	db, mock := mockWechatDB(t)
	// An accepted prompt during the old network request advances the generation.
	mock.ExpectBegin()
	expectSubscriptionIdentity(mock, 8, true)
	mock.ExpectExec("INSERT INTO `wechat_subscription_receipts`").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO `wechat_subscriptions`.*revision.*revision \\+ 1").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if err := db.RecordWechatSubscription(context.Background(), 8, "wx-test", "template", "accept", "new-prompt", "once"); err != nil {
		t.Fatal(err)
	}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `wechat_notifications`.*FOR UPDATE").WithArgs(job.ID, job.Token, WechatNotificationSending, 1).WillReturnRows(notificationRows(job))
	// Current generation is now 5: the old generation-4 denial matches no row.
	mock.ExpectExec("UPDATE `wechat_subscriptions` SET.*revision = \\?").WithArgs(false, 0, sqlmock.AnyArg(), job.RecipientID, job.AppID, job.TemplateID, int64(4)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("UPDATE `wechat_notifications` SET").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if err := db.FinishWechatNotification(context.Background(), job, WechatNotificationFailed, "revoked old grant", false, true, now); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
func TestWechatNotificationOldRefundCannotReviveRevokedAuthorization(t *testing.T) {
	now := time.Now().UTC()
	first := testWechatJob(now)
	first.State = WechatNotificationSending
	first.QuotaReserved = true
	first.Attempts = 1
	first.SubscriptionRevision = 4
	second := first
	second.ID = "push-2"
	second.Token = "lease-2"
	db, mock := mockWechatDB(t)
	// The first request revokes generation 4 and atomically changes it to 5.
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `wechat_notifications`.*FOR UPDATE").WithArgs(first.ID, first.Token, WechatNotificationSending, 1).WillReturnRows(notificationRows(first))
	mock.ExpectExec("UPDATE `wechat_subscriptions` SET.*revision = \\?").WithArgs(false, 0, sqlmock.AnyArg(), first.RecipientID, first.AppID, first.TemplateID, int64(4)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE `wechat_notifications` SET").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if err := db.FinishWechatNotification(context.Background(), first, WechatNotificationFailed, "permission revoked", false, true, now); err != nil {
		t.Fatal(err)
	}
	// The other old request is explicitly rejected for a transient failure. Its
	// refund is guarded by the old generation and cannot recreate the permission.
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `wechat_notifications`.*FOR UPDATE").WithArgs(second.ID, second.Token, WechatNotificationSending, 1).WillReturnRows(notificationRows(second))
	mock.ExpectExec("UPDATE `wechat_subscriptions` SET `remaining`=remaining \\+ 1.*revision = \\?").WithArgs(sqlmock.AnyArg(), second.RecipientID, second.AppID, second.TemplateID, int64(4)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("UPDATE `wechat_notifications` SET").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if err := db.FinishWechatNotification(context.Background(), second, WechatNotificationFailed, "busy", true, false, now); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestWechatNotificationPrivateSourceCannotBeSentToAnotherMember(t *testing.T) {
	now := time.Now().UTC()
	job := testWechatJob(now)
	job.SessionID = "private-channel"
	job.RecipientID = 9
	db, mock := mockWechatDB(t)
	mock.ExpectQuery("SELECT .* FROM `reminders`").WithArgs(job.SourceID, 1).WillReturnRows(sqlmock.NewRows([]string{"id", "session_id", "binding_created_at", "status", "recipient_ids", "delivered_at", "visibility"}).AddRow(job.SourceID, job.SessionID, job.BindingCreatedAt, ReminderDelivered, "[9]", now, "private"))
	// Even if erroneous stored recipients name the partner, the private channel
	// belongs only to its owner and is never a source of partner notifications.
	mock.ExpectQuery("SELECT .* FROM `private_channels`").WithArgs(job.SessionID, int64(9), job.BindingCreatedAt, 1).WillReturnRows(sqlmock.NewRows([]string{"session_id"}))
	valid, err := validWechatNotificationSource(db.gdb, job)
	if err != nil || valid {
		t.Fatalf("valid=%v err=%v", valid, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
func TestWechatNotificationManualCompletionWithoutAIDeliveryIsNotASource(t *testing.T) {
	now := time.Now().UTC()
	job := testWechatJob(now)
	db, mock := mockWechatDB(t)
	mock.ExpectQuery("SELECT .* FROM `reminders`").WithArgs(job.SourceID, 1).WillReturnRows(sqlmock.NewRows([]string{"id", "session_id", "binding_created_at", "status", "recipient_ids", "delivered_at"}).AddRow(job.SourceID, job.SessionID, job.BindingCreatedAt, ReminderCompleted, "[8]", nil))
	valid, err := validWechatNotificationSource(db.gdb, job)
	if err != nil || valid {
		t.Fatalf("valid=%v err=%v", valid, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
