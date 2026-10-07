package api

import (
	"context"
	"errors"
	"fmt"
	"time"

	"tietie/backend/internal/dbop"
	"tietie/backend/internal/logging"
	"tietie/backend/internal/scheduler"
	"tietie/backend/internal/wechat"
)

type wechatNotificationStore interface {
	PrepareWechatNotification(context.Context, dbop.WechatNotification, time.Time) (string, bool, error)
	FinishWechatNotification(context.Context, dbop.WechatNotification, string, string, bool, bool, time.Time) error
	ReleaseWechatNotification(context.Context, dbop.WechatNotification, time.Time) error
}

// The push channel runs independently of Qoder's shared concurrency budget.
func (s *Server) RunWechatNotificationWorker(ctx context.Context) {
	sender := s.wechatMessageSender()
	if s.DB == nil || sender == nil || !sender.Enabled() {
		logging.Scheduler().Info("微信提醒投递未启用，请检查模板配置", "event", "wechat.worker_disabled")
		return
	}
	logging.Scheduler().Info("微信提醒投递工作线程已启动", "event", "wechat.worker_started")
	options := scheduler.Options{PollInterval: 5 * time.Second, BatchSize: 16, Concurrency: 2, JobTimeout: time.Minute}
	worker := scheduler.Worker[dbop.WechatNotification]{Options: options, Claim: s.DB.ClaimWechatNotifications, Work: func(c context.Context, job dbop.WechatNotification) error {
		return deliverWechatNotification(c, s.DB, sender, job)
	}, OnError: func(err error) {
		if ctx.Err() == nil {
			logging.Scheduler().Error("微信提醒投递失败，已保存发送状态", "event", "wechat.notification_failed", "error", err)
		}
	}}
	worker.Run(ctx)
}
func deliverWechatNotification(ctx context.Context, store wechatNotificationStore, sender wechat.MessageSender, job dbop.WechatNotification) error {
	if ctx.Err() != nil {
		save, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		return store.ReleaseWechatNotification(save, job, time.Now())
	}
	openID, ready, err := store.PrepareWechatNotification(ctx, job, time.Now())
	if err != nil {
		save, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = store.ReleaseWechatNotification(save, job, time.Now())
		return err
	}
	if !ready {
		return nil
	}
	notificationType := "待办提醒"
	if job.SourceType == "care" {
		notificationType = job.Title
	}
	sendErr := sender.SendReminder(ctx, openID, wechat.ReminderNotification{NotificationType: notificationType, Title: job.Title, Content: job.Content, DueAt: job.DueAt, Page: job.Page})
	state, reason := dbop.WechatNotificationSent, ""
	retryable, unauthorized := false, false
	wechatCode := 0
	if sendErr != nil {
		reason = "微信明确拒绝发送"
		state = dbop.WechatNotificationFailed
		retryable = wechat.IsMessageRetryable(sendErr)
		unauthorized = wechat.IsMessageUnauthorized(sendErr)
		if wechat.IsMessageUncertain(sendErr) || wechat.MessageErrorKindOf(sendErr) == "" {
			state = dbop.WechatNotificationUncertain
			reason = "网络响应未知，避免重复推送"
		}
		var messageErr *wechat.MessageError
		if errors.As(sendErr, &messageErr) && messageErr.Code != 0 {
			wechatCode = messageErr.Code
			reason = fmt.Sprintf("%s（错误码 %d）", reason, wechatCode)
		}
	}
	save, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := store.FinishWechatNotification(save, job, state, reason, retryable, unauthorized, time.Now()); err != nil {
		return err
	}
	logging.Scheduler().Info("微信提醒投递结果已保存", "event", "wechat.notification_result", "notification_id", job.ID, "source_type", job.SourceType, "source_id", job.SourceID, "state", state, "wechat_code", wechatCode, "retryable", retryable, "unauthorized", unauthorized)
	return sendErr
}
