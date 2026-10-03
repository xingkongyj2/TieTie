package api

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"tietie/backend/internal/conversation"
	"tietie/backend/internal/dbop"
	"tietie/backend/internal/logging"
	"tietie/backend/internal/qoder"
)

// One local writer per shared session, including the clock worker.
type conversationLock struct {
	mutex      sync.Mutex
	references int
}

func (s *Server) lockConversation(id string) func() {
	s.locksMu.Lock()
	if s.sessionLocks == nil {
		s.sessionLocks = make(map[string]*conversationLock)
	}
	lock := s.sessionLocks[id]
	if lock == nil {
		lock = &conversationLock{}
		s.sessionLocks[id] = lock
	}
	lock.references++
	s.locksMu.Unlock()
	lock.mutex.Lock()
	return func() {
		lock.mutex.Unlock()
		s.locksMu.Lock()
		lock.references--
		if lock.references == 0 {
			delete(s.sessionLocks, id)
		}
		s.locksMu.Unlock()
	}
}

func (s *Server) conversationContext(ctx context.Context, id string, authorID int64) (conversation.Context, *dbop.Binding, error) {
	binding, err := s.DB.GetBindingBySessionID(ctx, id)
	if err != nil {
		return conversation.Context{}, nil, err
	}
	if binding == nil {
		return conversation.Context{}, nil, qoder.NewApiError(403, "session_forbidden", "你没有访问此会话的权限。")
	}
	if authorID != 0 && authorID != binding.UserA && authorID != binding.UserB {
		return conversation.Context{}, nil, qoder.NewApiError(403, "session_forbidden", "你没有访问此会话的权限。")
	}
	out := conversation.Context{SessionID: id, AuthorID: authorID, Now: time.Now().In(time.FixedZone("Asia/Shanghai", 8*60*60))}
	if channel, err := s.DB.GetPrivateChannel(ctx, id); err != nil {
		return out, binding, err
	} else if channel != nil {
		out.Visibility = "private"
	}
	for _, userID := range []int64{binding.UserA, binding.UserB} {
		user, err := s.DB.GetUserByID(ctx, userID)
		if err != nil {
			return out, binding, err
		}
		if user == nil {
			return out, binding, errors.New("space member no longer exists")
		}
		out.Members = append(out.Members, conversation.Member{ID: user.ID, Name: user.Username})
	}
	reminders, err := s.DB.ListContextReminders(ctx, id)
	if err != nil {
		return out, binding, err
	}
	for _, reminder := range reminders {
		if reminder.Status == dbop.ReminderScheduled || reminder.Status == dbop.ReminderDelivered {
			out.Reminders = append(out.Reminders, protocolReminder(reminder))
		}
	}
	memories, err := s.DB.ListReminderMemories(ctx, id)
	if err != nil {
		return out, binding, err
	}
	for _, m := range memories {
		out.Memories = append(out.Memories, conversation.Memory{ReminderID: m.ReminderID, Title: m.Title, DueAt: m.DueAt, RecipientIDs: m.RecipientIDs, Status: m.Status, TaskStatus: m.TaskStatus, DeliveredAt: m.DeliveredAt, CompletedBy: m.CompletedBy})
	}
	index, err := s.DB.MemoryIndex(ctx, id)
	if err != nil {
		return out, binding, err
	}
	for _, m := range index {
		out.MemoryIndex = append(out.MemoryIndex, conversation.MemoryIndex{Kind: m.Kind, Revision: m.Revision, Key: m.ID, Path: m.DocumentPath(), Scope: m.Scope, OwnerID: m.OwnerID, State: m.State, Category: m.Category, ExpiresAt: m.ExpiresAt})
	}
	return out, binding, nil
}

func protocolReminder(reminder dbop.Reminder) conversation.Reminder {
	return conversation.Reminder{ID: reminder.ID, Title: reminder.Title, DueAt: reminder.DueAt,
		RecipientIDs: reminder.RecipientIDs, CreatedBy: reminder.CreatedBy}
}

func eventText(event qoder.Event) string {
	var parts []string
	for _, block := range event.Content {
		if block.Type == "text" {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "\n\n")
}

func memberIDs(ctx conversation.Context) []int64 {
	ids := make([]int64, 0, len(ctx.Members))
	for _, member := range ctx.Members {
		ids = append(ids, member.ID)
	}
	return ids
}

func validRecipients(ids []int64, ctx conversation.Context) bool {
	if len(ids) == 0 || len(ids) > 2 {
		return false
	}
	seen := map[int64]bool{}
	for _, id := range ids {
		found := false
		for _, member := range ctx.Members {
			if member.ID == id {
				found = true
			}
		}
		if !found || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}

// processConversation uses chronological full events to establish who requested
// an AI action. The model cannot choose its own author or impersonate a clock event.
func (s *Server) processConversation(ctx context.Context, id string, result *qoder.MessagesResult, viewerID int64) (conversation.Context, string, error) {
	return s.processConversationFrom(ctx, id, result, viewerID, "")
}

func (s *Server) processConversationFrom(ctx context.Context, id string, result *qoder.MessagesResult, viewerID int64, priorInput string) (conversation.Context, string, error) {
	space, binding, err := s.conversationContext(ctx, id, 0)
	if err != nil {
		return space, "", err
	}
	byID := map[string]int{}
	for index, message := range result.Messages {
		byID[message.ID] = index
	}
	// 历史重放里每一轮 AI 动作原本要 4 条语句（存在性查询、幂等插入、结果读取、完成改写），
	// 远端数据库一次往返上百毫秒，页面就会被拖到十几秒。这里开头一次查全，之后只在确实要写时才写。
	controls, err := s.DB.ListSessionControls(ctx, id)
	if err != nil {
		return space, "", err
	}
	origin, hasOrigin := conversation.DecodeInput(priorInput)
	if hasOrigin && origin.Context.SessionID != id {
		hasOrigin = false
	}
	warnings := []string{}
	var direct *dbop.Reminder
	hiddenTurns := map[string]bool{}
	silentMessages := map[string]bool{}
	messageTurns := map[string]string{}
	for _, event := range result.Events {
		if event.Type == "user.message" || event.Type == "user.custom_tool_result" {
			origin, hasOrigin = conversation.DecodeInput(eventText(event))
			if hasOrigin && origin.Context.SessionID != id {
				hasOrigin = false
			}
			direct = nil
			if hasOrigin && origin.Version != 2 && !origin.Hidden && !origin.Context.Now.Before(binding.CreatedAt) && (origin.UserID == binding.UserA || origin.UserID == binding.UserB) {
				direct, err = s.saveDirectReminder(ctx, origin.Context, origin.Text)
				if err != nil {
					return space, "", err
				}
				if direct != nil {
					if i, ok := byID[event.ID]; ok {
						result.Messages[i].ReminderIDs = []string{direct.ID}
					}
				}
			}
			warnings = nil // Global feedback concerns only the latest turn.
			continue
		}
		if event.Type == "session.error" && hasOrigin && origin.Kind == "reminder_due" && origin.Reminder != nil {
			_ = s.DB.FailReminderDispatch(ctx, origin.Reminder.ID)
		}
		if hasOrigin && origin.Context.ReplyMode == conversation.SilentReply && (event.Type == "agent.message" || event.Type == "agent.custom_tool_use") {
			silentMessages[event.ID] = true
			if origin.Kind == "action_result" && event.Type == "agent.message" {
				if err := s.attachControlReply(ctx, id, origin, &qoder.PublicMessage{}, controls); err != nil {
					return space, "", err
				}
			}
		}
		if event.Type == "agent.message" {
			assistant := conversation.ParseAssistant(eventText(event))
			if assistant.Control {
				if assistant.Silent {
					continue
				}
				if hasOrigin && origin.Version == 2 && !origin.Hidden {
					hiddenTurns[origin.RequestID] = true
				}
				if err := s.acceptControlEvent(ctx, id, binding, origin, hasOrigin, event, assistant, controls); err != nil {
					return space, "", err
				}
				continue
			}
		}
		index, exists := byID[event.ID]
		if !exists || event.Type != "agent.message" {
			continue
		}
		message := &result.Messages[index]
		if silentMessages[event.ID] {
			continue
		}
		// Translate old fenced actions into the new hidden control/receipt sequence
		// when a preconfigured cloud assistant responds using an older example.
		if hasOrigin && origin.Version == 2 && !origin.Hidden && len(message.Actions) > 0 {
			actions := append([]conversation.Action(nil), message.Actions...)
			for i := range actions {
				if actions[i].Type == "create_reminder" {
					actions[i].Storage = "database_and_memory"
				}
			}
			if err := s.acceptControlEvent(ctx, id, binding, origin, true, event, conversation.Assistant{Control: true, Version: 2, RequestID: origin.RequestID, Actions: actions}, controls); err != nil {
				return space, "", err
			}
			hiddenTurns[origin.RequestID] = true
			messageTurns[event.ID] = origin.RequestID
			continue
		}
		if hasOrigin && origin.Version == 2 {
			if !origin.Hidden {
				messageTurns[event.ID] = origin.RequestID
				if _, existing := controls[origin.RequestID]; existing {
					hiddenTurns[origin.RequestID] = true
				}
			}
			if message.ProtocolVersion == 2 && message.RequestID != origin.RequestID {
				delete(byID, event.ID)
				result.Messages[index].Text = ""
				continue
			}
			if origin.Kind == "action_result" {
				if err := s.attachControlReply(ctx, id, origin, message, controls); err != nil {
					return space, "", err
				}
			}
		}
		warned := false
		addWarning := func(value string) {
			if !warned {
				if exists, err := s.DB.HasMessage(ctx, event.ID); err == nil && !exists {
					logging.Scheduler().Warn(value, "event", "conversation.warning", "session_id", id, "source_event_id", event.ID)
				}
				warned = true
			}
			warnings = append(warnings, value)
			if message.ReminderError != "" {
				message.ReminderError += " "
			}
			message.ReminderError += value
		}
		message.Source = "chat"
		if hasOrigin && origin.Kind == "reminder_due" && origin.Reminder != nil {
			// Recheck against storage rather than trusting model recipient claims.
			reminder, loadErr := s.DB.GetDispatchReminder(ctx, id, origin.Reminder.ID)
			if loadErr != nil {
				return space, "", loadErr
			}
			if reminder != nil && validRecipients(reminder.RecipientIDs, space) {
				message.Source, message.RecipientIDs, message.Kind = "reminder", reminder.RecipientIDs, "reminder"
				message.ReminderIDs = []string{reminder.ID}
				message.Text = reminderMentions(message.Text, reminder.RecipientIDs, space)
				if reminder.Status == dbop.ReminderDispatching || reminder.Status == dbop.ReminderUncertain {
					if message.ProtocolError == "" && strings.TrimSpace(message.Text) != "" {
						if err := s.DB.FinishReminderDispatch(ctx, reminder.ID, append(reminder.DispatchEventIDs, event.ID), time.Now()); err != nil {
							return space, "", err
						}
					} else {
						_ = s.DB.FailReminderDispatch(ctx, reminder.ID)
					}
				}
			}
		}
		if !validRecipients(message.RecipientIDs, space) {
			message.RecipientIDs = memberIDs(space)
		}
		if hasOrigin && origin.Version != 2 && !origin.Hidden && !origin.Context.Now.Before(binding.CreatedAt) && (origin.UserID == binding.UserA || origin.UserID == binding.UserB) {
			if direct == nil {
				direct, err = s.saveDirectReminder(ctx, origin.Context, origin.Text)
				if err != nil {
					return space, "", err
				}
			}
			if direct != nil {
				message.ReminderIDs = append(message.ReminderIDs, direct.ID)
			}
		}
		if message.ProtocolError != "" {
			if direct != nil {
				addWarning("AI 回复格式异常，但这条提醒已由后台保存。")
			} else {
				addWarning("这条回复解析失败，提醒未保存，请重试。")
			}
		}
		if direct != nil {
			continue
		} // The exact single request was already applied by the backend.
		if len(message.Actions) == 0 {
			if direct == nil && hasOrigin && !origin.Hidden && strings.Contains(origin.Text, "提醒") && unconfirmedReminderPromise(message.Text) {
				addWarning("AI 尚未提供有效的提醒操作，后台没有保存这条提醒，请在提醒板确认或补充具体时间。")
				logging.Scheduler().Debug("AI 口头承诺缺少可保存的提醒动作", "event", "reminder.missing_action", "session_id", id, "source_event_id", event.ID)
			}
			continue
		}
		createdAt, dateErr := time.Parse(time.RFC3339Nano, event.ProcessedAt)
		// Older binding epochs must not recreate reminders when history is replayed.
		if dateErr != nil || createdAt.Before(binding.CreatedAt) {
			continue
		}
		if !hasOrigin || origin.Hidden || origin.Context.Now.Before(binding.CreatedAt) || (origin.UserID != binding.UserA && origin.UserID != binding.UserB) {
			addWarning("这条回复没有当前空间的成员请求，提醒未保存。")
			continue
		}
		keys := map[string]bool{}
		keysValid := len(message.Actions) <= 8
		for _, action := range message.Actions {
			if strings.TrimSpace(action.Key) == "" || keys[action.Key] {
				keysValid = false
			}
			keys[action.Key] = true
		}
		if !keysValid {
			addWarning("提醒操作格式不完整，提醒未保存。可重新说明时间和提醒对象。")
			continue
		}
		for actionIndex, action := range message.Actions {
			proposal := dbop.ReminderAction{CreatedBy: origin.UserID,
				RequestKey: fmt.Sprintf("%d/%s/%s", origin.UserID, origin.Context.Now.Format(time.RFC3339Nano), action.Key)}
			switch action.Type {
			case "create_reminder":
				dueAt, err := time.Parse(time.RFC3339, action.DueAt)
				// Validate against the original AI event, so delayed syncing can still
				// recover an overdue reminder and history replay stays deterministic.
				if err != nil || !dueAt.After(createdAt) || strings.TrimSpace(action.Title) == "" ||
					utf8.RuneCountInString(action.Title) > 500 || !validRecipients(action.RecipientIDs, space) {
					addWarning("提醒时间或对象无效，提醒未保存。可重新说明具体时间和提醒对象。")
					continue
				}
				proposal.Type, proposal.Title, proposal.DueAt, proposal.RecipientIDs = "create", action.Title, dueAt, action.RecipientIDs
			case "cancel_reminder":
				proposal.Type, proposal.ReminderID = "cancel", action.ReminderID
			default:
				addWarning("暂不支持这项提醒操作。")
				continue
			}
			reminder, _, err := s.DB.ApplyReminderAction(ctx, id, event.ID, actionIndex, proposal)
			if err != nil {
				logging.Scheduler().Error("AI 提醒操作保存失败", "event", "reminder.save_failed", "session_id", id, "source_event_id", event.ID, "error", err)
				addWarning("这条提醒操作没有保存成功，请在提醒板确认后重试。")
			} else {
				message.ReminderIDs = append(message.ReminderIDs, reminder.ID)
			}
		}
	}
	// Empty messages were rejected by correlation checks; internal control JSON
	// never appears in either polling history or the verified SSE channel.
	visible := result.Messages[:0]
	for _, m := range result.Messages {
		if silentMessages[m.ID] {
			continue
		}
		mapMessageViewer(&m, space, viewerID)
		if m.Sender != "ai" && m.Sender != "self" && m.Sender != "partner" {
			continue
		}
		if !hiddenTurns[messageTurns[m.ID]] && (m.Text != "" || m.Kind == "ask" || len(m.Images) > 0) {
			visible = append(visible, m)
		}
	}
	result.Messages = visible
	if hasOrigin {
		space.ReplyMode = origin.Context.ReplyMode
		if result.Session != nil {
			result.Session.ReplyMode = space.ReplyMode
		}
		if space.ReplyMode == conversation.SilentReply {
			empty := ""
			result.TurnError = &empty
		}
	}
	if err := s.applyChannelVisibility(ctx, id, result.Messages); err != nil {
		return space, "", err
	}
	return space, strings.Join(uniqueStrings(warnings), " "), nil
}

func mapMessageViewer(message *qoder.PublicMessage, ctx conversation.Context, viewerID int64) {
	if message.Sender == "ai" {
		return
	}
	message.Sender = "user"
	message.DisplayName = ""
	if message.InputSessionID != ctx.SessionID {
		message.UserID = 0
		return
	}
	for _, member := range ctx.Members {
		if member.ID == message.UserID {
			message.DisplayName = member.Name
			message.Sender = "partner"
			if member.ID == viewerID {
				message.Sender = "self"
			}
			return
		}
	}
	message.UserID = 0
}

func uniqueStrings(values []string) []string {
	out, seen := []string{}, map[string]bool{}
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}

func conversationFinished(result *qoder.MessagesResult) bool {
	return conversationFinishedAfter(result, time.Time{})
}

func conversationFinishedAfter(result *qoder.MessagesResult, since time.Time) bool {
	return conversationFinishedFrom(result, since, "")
}

func conversationFinishedFrom(result *qoder.MessagesResult, since time.Time, priorInput string) bool {
	if result.Session == nil || result.Session.Status != "idle" {
		return false
	}
	lastInput := -1
	for i, event := range result.Events {
		if event.Type == "user.message" || event.Type == "user.custom_tool_result" {
			lastInput = i
		}
	}
	if !since.IsZero() {
		inputText := priorInput
		if lastInput >= 0 {
			inputText = eventText(result.Events[lastInput])
		}
		input, decoded := conversation.DecodeInput(inputText)
		if !decoded || input.Context.Now.Before(since) {
			return false
		}
	}
	for _, event := range result.Events[lastInput+1:] {
		if event.Type == "session.status_idle" || event.Type == "agent.custom_tool_use" || event.Type == "session.error" {
			return true
		}
	}
	return false
}

func unconfirmedReminderPromise(text string) bool {
	if !strings.Contains(text, "提醒") {
		return false
	}
	for _, word := range []string{"？", "?", "未设置", "没有设置", "不能", "无法", "什么时间", "几点", "请告诉", "请补充"} {
		if strings.Contains(text, word) {
			return false
		}
	}
	for _, word := range []string{"已设置", "已登记", "登记好", "设置好", "安排好", "倒计时", "我会", "会来", "到时候"} {
		if strings.Contains(text, word) {
			return true
		}
	}
	return false
}

// Notification recipients are facts from storage, so a model cannot @ the wrong
// member by changing text while its recipient IDs are corrected server-side.
func reminderMentions(text string, ids []int64, space conversation.Context) string {
	targets := map[string]bool{}
	names := []string{}
	for _, m := range space.Members {
		tag := "@" + m.Name
		names = append(names, tag)
		for _, id := range ids {
			if id == m.ID {
				targets[tag] = true
			}
		}
	}
	sort.Slice(names, func(i, j int) bool { return len(names[i]) > len(names[j]) })
	escaped := []string{}
	for _, name := range names {
		escaped = append(escaped, regexp.QuoteMeta(name))
	}
	seen := map[string]bool{}
	if len(escaped) > 0 {
		pattern := regexp.MustCompile(strings.Join(escaped, "|"))
		text = pattern.ReplaceAllStringFunc(text, func(tag string) string {
			if targets[tag] {
				seen[tag] = true
				return tag
			}
			return ""
		})
	}
	prefix := []string{}
	for _, m := range space.Members {
		tag := "@" + m.Name
		if targets[tag] && !seen[tag] {
			prefix = append(prefix, tag)
		}
	}
	if len(prefix) > 0 {
		return strings.Join(prefix, " ") + " " + strings.TrimSpace(text)
	}
	return text
}
