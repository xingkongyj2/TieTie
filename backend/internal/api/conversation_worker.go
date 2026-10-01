package api

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"tietie/backend/internal/conversation"
	"tietie/backend/internal/dbop"
	"tietie/backend/internal/logging"
	"tietie/backend/internal/qoder"
	"tietie/backend/internal/scheduler"
)

// RunConversationWorker persists AI reminder actions without an open browser and
// wakes the shared AI session when the backend clock reaches a scheduled due time.
// The application process must remain running; this is not a device push channel.
func (s *Server) RunConversationWorker(ctx context.Context) {
	if s.DB == nil || s.Qoder == nil {
		return
	}
	recovered, err := s.DB.RecoverReminderDispatches(ctx)
	if err != nil {
		logging.Scheduler().Error("恢复提醒队列失败", "event", "scheduler.recovery_failed", "error", err)
	} else {
		logging.Scheduler().Info("启动恢复完成，无回执的中断任务转为待核实", "event", "scheduler.recovered", "uncertain_count", recovered)
	}
	if err := s.DB.RecoverControls(ctx); err != nil {
		logging.Scheduler().Error("系统控制任务恢复失败", "event", "control.recovery_failed", "error", err)
	}
	if err := s.DB.RecoverMemorySync(ctx); err != nil {
		logging.Scheduler().Error("云端记忆同步恢复失败", "event", "memory.recovery_failed", "error", err)
	}
	if err := s.DB.RecoverImpressions(ctx); err != nil {
		logging.Scheduler().Error("印象任务恢复失败", "error", err)
	}
	options := scheduler.Options{}
	if s.Cfg != nil {
		options.PollInterval = s.Cfg.SchedulerPollInterval
		options.BatchSize = s.Cfg.SchedulerBatchSize
		options.Concurrency = s.Cfg.SchedulerConcurrency
	}
	report := func(err error) {
		if ctx.Err() == nil {
			logging.Scheduler().Error("后台任务失败，将按任务状态恢复或重试", "event", "scheduler.job_failed", "error", err)
		}
	}
	// All queues share one budget of concurrent background cloud requests.
	slots := make(chan struct{}, options.Normalized().Concurrency)
	run := func(jobCtx context.Context, work func() error) error {
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
			return work()
		case <-jobCtx.Done():
			return work() // Work releases its unstarted reservation.
		}
	}
	logging.Scheduler().Info("定时提醒、AI 回复、系统控制与云端记忆工作线程已启动", "event", "scheduler.started", "poll_interval", options.Normalized().PollInterval, "batch_size", options.Normalized().BatchSize, "concurrency", options.Normalized().Concurrency, "control_enabled", s.useV2(), "cloud_memory_enabled", s.Cfg != nil && s.Cfg.CloudMemoryEnabled)
	var heartbeat sync.WaitGroup
	heartbeat.Add(1)
	go func() {
		defer heartbeat.Done()
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			s.logSchedulerHeartbeat(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	reminders := scheduler.Worker[dbop.Reminder]{Options: options, Claim: func(claimCtx context.Context, now time.Time, limit int) ([]dbop.Reminder, error) {
		jobs, err := s.DB.ClaimDueReminders(claimCtx, now, limit)
		if len(jobs) > 0 {
			logging.Scheduler().Info("已领取到期任务", "event", "scheduler.claimed", "count", len(jobs))
		}
		return jobs, err
	},
		Work: func(jobCtx context.Context, reminder dbop.Reminder) error {
			return run(jobCtx, func() error { return s.dispatchReminder(jobCtx, reminder) })
		}, OnError: report}
	syncs := scheduler.Worker[dbop.ConversationJob]{Options: options, Claim: s.DB.ClaimConversationSync,
		Work: func(jobCtx context.Context, job dbop.ConversationJob) error {
			return run(jobCtx, func() error { return s.syncConversationJob(jobCtx, job) })
		}, OnError: report}
	var workers sync.WaitGroup
	workers.Add(1)
	go func() {
		defer workers.Done()
		portraitOptions := options
		portraitOptions.Concurrency = 1
		portraitOptions.BatchSize = 4
		portraitOptions.JobTimeout = 2 * time.Minute
		worker := scheduler.Worker[dbop.Impression]{Options: portraitOptions, Claim: s.DB.ClaimImpressions, Work: func(c context.Context, j dbop.Impression) error {
			return run(c, func() error { return s.runImpression(c, j) })
		}, OnError: report}
		worker.Run(ctx)
	}()
	if s.useV2() {
		workers.Add(1)
		go func() {
			defer workers.Done()
			w := scheduler.Worker[dbop.ControlJob]{Options: options, Claim: s.DB.ClaimControls, Work: func(c context.Context, j dbop.ControlJob) error {
				return run(c, func() error { return s.runControl(c, j) })
			}, OnError: report}
			w.Run(ctx)
		}()
	}
	if s.Cfg != nil && s.Cfg.CloudMemoryEnabled {
		workers.Add(1)
		go func() {
			defer workers.Done()
			w := scheduler.Worker[dbop.MemoryRecord]{Options: options, Claim: s.DB.ClaimMemorySync, Work: func(c context.Context, j dbop.MemoryRecord) error {
				return run(c, func() error { return s.runMemorySync(c, j) })
			}, OnError: report}
			w.Run(ctx)
		}()
	}
	workers.Add(1)
	go func() { defer workers.Done(); syncs.Run(ctx) }()
	reminders.Run(ctx)
	workers.Wait()
	heartbeat.Wait()
	logging.Scheduler().Info("定时任务与 AI 回复同步工作线程已停止", "event", "scheduler.stopped")
}

func (s *Server) syncPendingConversation(ctx context.Context, id string) error {
	job, err := s.DB.GetConversationJob(ctx, id)
	if err != nil || job == nil {
		return err
	}
	return s.syncConversationJob(ctx, *job)
}

func (s *Server) syncConversationJob(ctx context.Context, job dbop.ConversationJob) (syncErr error) {
	logging.Scheduler().Debug("同步 AI 回复", "event", "conversation.sync", "session_id", job.ID, "cursor", job.SyncCursor)
	unlock := s.lockConversation(job.ID)
	defer unlock()
	finished := false
	defer func() {
		next := time.Now().Add(5 * time.Second)
		if time.Since(job.PendingSince) > 5*time.Minute {
			next = time.Now().Add(time.Minute)
		}
		if syncErr != nil {
			next = time.Now().Add(30 * time.Second)
		}
		// Always release the sync lease, including cancelled reads. Version guards
		// prevent an older job from clearing a newer member message.
		saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := s.DB.FinishConversationSync(saveCtx, job, finished, next); err != nil && syncErr == nil {
			syncErr = err
		}
	}()
	result, err := s.Qoder.GetMessages(ctx, job.ID, job.SyncCursor)
	if err != nil {
		return err
	}
	if _, _, err := s.processConversationFrom(ctx, job.ID, result, 0, job.SyncOrigin); err != nil {
		return err
	}
	s.recordSession(ctx, result.Session)
	s.recordMessages(ctx, job.ID, result.Messages)
	finished = conversationFinishedFrom(result, job.PendingSince, job.SyncOrigin)
	if finished {
		logging.Scheduler().Info("本轮 AI 回复处理完成，动作与消息已保存", "event", "conversation.finished", "session_id", job.ID)
	}
	if result.Cursor != nil {
		job.SyncCursor = *result.Cursor
	}
	for _, event := range result.Events {
		if event.Type == "user.message" || event.Type == "user.custom_tool_result" {
			job.SyncOrigin = eventText(event)
		}
	}
	return nil
}

func (s *Server) dispatchReminder(ctx context.Context, reminder dbop.Reminder) (dispatchErr error) {
	logging.Scheduler().Info("开始处理到期提醒", "event", "reminder.due", "reminder_id", reminder.ID, "session_id", reminder.SessionID, "due_at", reminder.DueAt, "recipient_ids", reminder.RecipientIDs, "attempt", reminder.Attempts)
	deliveryID := reminder.DeliverySession()
	attempted := false
	defer func() {
		if dispatchErr != nil && !attempted {
			saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			_ = s.DB.RetryReminderDispatch(saveCtx, reminder.ID, time.Now().Add(30*time.Second))
		}
	}()
	if ctx.Err() != nil {
		return s.DB.RetryReminderDispatch(context.WithoutCancel(ctx), reminder.ID, time.Now().Add(5*time.Second))
	}
	unlock := s.lockConversation(deliveryID)
	defer unlock()
	space, binding, err := s.conversationContext(ctx, deliveryID, 0)
	if err != nil {
		var apiErr *qoder.ApiError
		if errors.As(err, &apiErr) && apiErr.Status == 403 {
			return s.DB.CancelSessionReminders(ctx, deliveryID)
		}
		_ = s.DB.RetryReminderDispatch(ctx, reminder.ID, time.Now().Add(30*time.Second))
		return err
	}
	current, err := s.DB.GetReminder(ctx, reminder.SessionID, reminder.ID)
	if err != nil {
		return err
	}
	if current == nil || current.Status != dbop.ReminderDispatching {
		return nil
	}
	if !binding.CreatedAt.Equal(reminder.BindingCreatedAt) {
		return s.DB.CancelSessionReminders(ctx, deliveryID)
	}
	// The cloud may be idle between an AI control response and its system receipt.
	// Finish that receipt before starting a due turn in the same conversation.
	active, err := s.DB.HasActiveControl(ctx, deliveryID)
	if err != nil {
		return err
	}
	if active {
		logging.Scheduler().Info("空间正在处理系统控制回执，稍后重试提醒", "event", "reminder.control_pending", "reminder_id", reminder.ID)
		return s.DB.RetryReminderDispatch(ctx, reminder.ID, time.Now().Add(15*time.Second))
	}
	// Avoid interrupting an active human turn or an unanswered tool question.
	history, err := s.Qoder.GetMessages(ctx, deliveryID, "")
	if err != nil {
		_ = s.DB.RetryReminderDispatch(ctx, reminder.ID, time.Now().Add(30*time.Second))
		return err // This was read-only: retrying is safe.
	}
	busy := history.Session == nil || strings.ToLower(history.Session.Status) != "idle"
	for _, message := range history.Messages {
		if message.Kind == "ask" && !message.Answered {
			busy = true
		}
	}
	if busy {
		logging.Scheduler().Info("AI 会话正在对话，稍后重试提醒", "event", "reminder.busy", "reminder_id", reminder.ID, "retry_in", "15s")
		return s.DB.RetryReminderDispatch(ctx, reminder.ID, time.Now().Add(15*time.Second))
	}
	wakeText := conversation.EncodeReminder(space, protocolReminder(reminder))
	var protocolState *dbop.ConversationProtocol
	if s.useV2() {
		reads, readErr := s.reminderMemoryRead(ctx, reminder)
		if readErr != nil {
			logging.Scheduler().Warn("读取云端提醒记忆失败，使用真实数据库任务继续提醒", "event", "memory.read_fallback", "reminder_id", reminder.ID, "error", readErr)
		}
		frame := conversation.NewEnvelopeV2(space, "reminder_due", "due_"+reminder.ID)
		actual := protocolReminder(reminder)
		frame.Reminder, frame.MemoryReads = &actual, reads
		wakeText, protocolState, err = s.prepareProtocolInput(ctx, frame)
		if err != nil {
			return err
		}
	}
	previous, err := s.DB.GetConversationJob(ctx, deliveryID)
	if err != nil {
		return err
	}
	if err := s.DB.MarkConversationPending(ctx, deliveryID, space.Now); err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	logging.Scheduler().Info("向云端助手发送隐藏的到期事件", "event", "reminder.wakeup", "reminder_id", reminder.ID, "session_id", deliveryID)
	attempted = true
	result, err := s.Qoder.SendMessage(ctx, deliveryID, qoder.MessageInput{Text: wakeText})
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err != nil {
		logging.Scheduler().Error("提醒唤醒请求失败", "event", "reminder.wakeup_failed", "reminder_id", reminder.ID, "error", err)
		s.resolveRejectedInput(saveCtx, deliveryID, space.Now, previous, err)
		var apiErr *qoder.ApiError
		// Only explicit rejections are safe to retry. A timeout or upstream 5xx
		// may follow acceptance; mark uncertain instead of sending twice.
		if errors.As(err, &apiErr) && (apiErr.Status == 400 || apiErr.Status == 401 || apiErr.Status == 403 || apiErr.Status == 404 || apiErr.Status == 409 || apiErr.Status == 429 || apiErr.Status == 503) {
			if apiErr.Status == 400 || apiErr.Status == 404 {
				_ = s.DB.FailReminderDispatch(saveCtx, reminder.ID)
			} else {
				_ = s.DB.RetryReminderDispatch(saveCtx, reminder.ID, time.Now().Add(30*time.Second))
			}
		} else {
			_ = s.DB.MarkReminderDispatchUncertain(saveCtx, reminder.ID)
		}
		return err
	}
	ids := []string{}
	for _, event := range result.Events {
		ids = append(ids, event.ID)
	}
	if len(ids) == 0 {
		return s.DB.MarkReminderDispatchUncertain(saveCtx, reminder.ID)
	}
	s.acceptProtocolInput(saveCtx, protocolState)
	logging.Scheduler().Info("云端已接受到期事件，等待 AI 实际提醒回复", "event", "reminder.accepted", "reminder_id", reminder.ID, "event_ids", ids)
	return s.DB.RecordReminderDispatch(saveCtx, reminder.ID, ids)
}

// Heartbeats use one ordered-index lookup, never scan the user table or count
// every historical task. An empty queue still emits a visible liveness signal.
func (s *Server) logSchedulerHeartbeat(ctx context.Context) {
	next, err := s.DB.NextScheduledReminder(ctx)
	if err != nil {
		if ctx.Err() == nil {
			logging.Scheduler().Error("读取下一任务失败", "event", "scheduler.heartbeat_failed", "error", err)
		}
		return
	}
	if next == nil {
		logging.Scheduler().Info("调度器运行中，目前没有待触发任务", "event", "scheduler.heartbeat")
		return
	}
	logging.Scheduler().Info("调度器运行中，等待下一个任务", "event", "scheduler.heartbeat", "next_reminder_id", next.ID, "next_run_at", next.RunAt)
}
