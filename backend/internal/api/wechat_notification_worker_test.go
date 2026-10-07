package api

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"tietie/backend/internal/dbop"
	"tietie/backend/internal/wechat"
)

type notificationStoreMock struct {
	ready                        bool
	prepareErr                   error
	prepared, released, finished bool
	state                        string
	reason                       string
	retryable, unauthorized      bool
}

func (m *notificationStoreMock) PrepareWechatNotification(context.Context, dbop.WechatNotification, time.Time) (string, bool, error) {
	m.prepared = true
	return "verified-openid", m.ready, m.prepareErr
}
func (m *notificationStoreMock) FinishWechatNotification(_ context.Context, _ dbop.WechatNotification, state, reason string, retryable, unauthorized bool, _ time.Time) error {
	m.finished = true
	m.state = state
	m.reason = reason
	m.retryable = retryable
	m.unauthorized = unauthorized
	return nil
}
func (m *notificationStoreMock) ReleaseWechatNotification(context.Context, dbop.WechatNotification, time.Time) error {
	m.released = true
	return nil
}

type notificationSenderMock struct {
	sendErr      error
	sent         bool
	notification wechat.ReminderNotification
}

func (*notificationSenderMock) Enabled() bool      { return true }
func (*notificationSenderMock) TemplateID() string { return "template" }
func (m *notificationSenderMock) SendReminder(_ context.Context, openid string, notification wechat.ReminderNotification) error {
	if openid != "verified-openid" {
		return errors.New("unverified identity")
	}
	m.sent = true
	m.notification = notification
	return m.sendErr
}
func TestWechatNotificationWorkerRecordsOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		err                     error
		state                   string
		retryable, unauthorized bool
	}{{"sent", nil, dbop.WechatNotificationSent, false, false}, {"known_retry", &wechat.MessageError{Kind: wechat.MessageRetryable}, dbop.WechatNotificationFailed, true, false}, {"authorization_revoked", &wechat.MessageError{Kind: wechat.MessageUnauthorized}, dbop.WechatNotificationFailed, false, true}, {"timeout", &wechat.MessageError{Kind: wechat.MessageUncertain}, dbop.WechatNotificationUncertain, false, false}, {"unknown", errors.New("unknown transport failure"), dbop.WechatNotificationUncertain, false, false}} {
		t.Run(tc.name, func(t *testing.T) {
			store := &notificationStoreMock{ready: true}
			sender := &notificationSenderMock{sendErr: tc.err}
			job := dbop.WechatNotification{Title: "吃药", Content: "记得吃药", Page: "pages/index/index?from=reminder&sessionId=space", DueAt: time.Now()}
			err := deliverWechatNotification(context.Background(), store, sender, job)
			if !errors.Is(err, tc.err) || !store.finished || !sender.sent || store.state != tc.state || store.retryable != tc.retryable || store.unauthorized != tc.unauthorized {
				t.Fatalf("store=%+v sent=%v err=%v", store, sender.sent, err)
			}
			if sender.notification.Page != job.Page || sender.notification.Content != job.Content || sender.notification.NotificationType != "待办提醒" {
				t.Fatal("lost notification content or chat link")
			}
		})
	}
}

func TestWechatNotificationWorkerUsesCareNotificationType(t *testing.T) {
	for _, title := range []string{"早安提醒", "晚安提醒", "纪念日提醒", "倒计时提醒"} {
		t.Run(title, func(t *testing.T) {
			store := &notificationStoreMock{ready: true}
			sender := &notificationSenderMock{}
			job := dbop.WechatNotification{SourceType: "care", Title: title, Content: "提醒内容", DueAt: time.Now()}
			if err := deliverWechatNotification(context.Background(), store, sender, job); err != nil {
				t.Fatal(err)
			}
			if !sender.sent || sender.notification.NotificationType != title {
				t.Fatalf("wrong care notification type: %+v", sender.notification)
			}
		})
	}
}

func TestWechatNotificationWorkerPersistsSafeErrorCode(t *testing.T) {
	store := &notificationStoreMock{ready: true}
	sender := &notificationSenderMock{sendErr: &wechat.MessageError{Kind: wechat.MessagePermanent, Code: 47003}}
	if err := deliverWechatNotification(context.Background(), store, sender, dbop.WechatNotification{}); err == nil {
		t.Fatal("lost WeChat rejection")
	}
	if store.state != dbop.WechatNotificationFailed || !strings.Contains(store.reason, "47003") {
		t.Fatalf("missing persisted WeChat error code: %+v", store)
	}

	store = &notificationStoreMock{ready: true}
	sender.sendErr = errors.New("private-openid access_token=secret-token")
	_ = deliverWechatNotification(context.Background(), store, sender, dbop.WechatNotification{})
	if store.state != dbop.WechatNotificationUncertain || strings.Contains(store.reason, "private-openid") || strings.Contains(store.reason, "secret-token") {
		t.Fatalf("unsafe failure reason: %+v", store)
	}
}
func TestWechatNotificationWorkerDoesNotSendWithoutReservation(t *testing.T) {
	store := &notificationStoreMock{ready: false}
	sender := &notificationSenderMock{}
	if err := deliverWechatNotification(context.Background(), store, sender, dbop.WechatNotification{}); err != nil {
		t.Fatal(err)
	}
	if sender.sent || store.finished {
		t.Fatal("sent a skipped reminder")
	}
}
func TestWechatNotificationWorkerReleasesUnstartedCancelledLease(t *testing.T) {
	store := &notificationStoreMock{ready: true}
	sender := &notificationSenderMock{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := deliverWechatNotification(ctx, store, sender, dbop.WechatNotification{}); err != nil {
		t.Fatal(err)
	}
	if sender.sent || store.prepared || !store.released {
		t.Fatalf("store=%+v sender=%+v", store, sender)
	}
}
func TestWechatNotificationWorkerReleasesFailedPreparation(t *testing.T) {
	failure := errors.New("database unavailable")
	store := &notificationStoreMock{prepareErr: failure}
	sender := &notificationSenderMock{}
	if err := deliverWechatNotification(context.Background(), store, sender, dbop.WechatNotification{}); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if sender.sent || !store.released {
		t.Fatalf("store=%+v sender=%+v", store, sender)
	}
}
