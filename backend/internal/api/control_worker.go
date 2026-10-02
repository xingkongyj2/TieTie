package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"tietie/backend/internal/conversation"
	"tietie/backend/internal/dbop"
	"tietie/backend/internal/logging"
	"tietie/backend/internal/memoryspace"
	"tietie/backend/internal/qoder"
	"tietie/backend/internal/regions"
	"time"
)

func (s *Server) useV2() bool { return s.Cfg != nil && s.Cfg.ConversationProtocolVersion == 2 }
func (s *Server) encodeMember(space conversation.Context, text string) string {
	if s.useV2() {
		return conversation.EncodeUserV2(space, text)
	}
	return conversation.EncodeUser(space, text)
}
func (s *Server) acceptControlEvent(ctx context.Context, id string, binding *dbop.Binding, origin conversation.Input, known bool, event qoder.Event, a conversation.Assistant) error {
	date, err := time.Parse(time.RFC3339Nano, event.ProcessedAt)
	if !known || origin.Version != 2 || origin.Hidden || origin.Context.Now.Before(binding.CreatedAt) || err != nil || date.Before(binding.CreatedAt) || (origin.UserID != binding.UserA && origin.UserID != binding.UserB) {
		logging.Scheduler().Warn("控制消息没有有效成员来源，已忽略", "event", "control.rejected", "session_id", id, "source_event_id", event.ID)
		return nil
	}
	if a.RequestID != origin.RequestID || a.ProtocolError != "" {
		a.Actions = nil
		logging.Scheduler().Warn("控制格式或请求关联错误，准备失败回执", "event", "control.invalid", "session_id", id, "source_event_id", event.ID, "protocol_error", a.ProtocolError, "request_matches", a.RequestID == origin.RequestID)
	}
	if origin.Context.ReplyMode == conversation.SilentReply {
		// Member-to-member chat authorizes memory extraction, not AI tasks.
		allowed := a.Actions[:0]
		for _, action := range a.Actions {
			if action.Type == "save_memory" || action.Type == "read_memory" {
				allowed = append(allowed, action)
			}
		}
		a.Actions = allowed
		if len(a.Actions) == 0 {
			return nil
		}
	}
	// Only server-generated provenance is retained. A raw user string is never
	// interpreted as an AI action, nor can the AI select the author or store ID.
	provenance := conversation.NewEnvelopeV2(origin.Context, "user_message", origin.RequestID)
	provenance.Text = origin.Text
	// Local provenance needs identity, not another copy of the long contract.
	provenance.Compact = true
	encoded, _ := json.Marshal(provenance)
	return s.DB.EnqueueControl(ctx, id, origin.RequestID, "\n<TIETIE_INPUT_V2>\n"+string(encoded)+"\n</TIETIE_INPUT_V2>", event.ID, a.Actions, origin.UserID, binding.CreatedAt)
}
func (s *Server) attachControlReply(ctx context.Context, id string, input conversation.Input, message *qoder.PublicMessage) error {
	job, err := s.DB.GetControl(ctx, id, input.RequestID)
	if err != nil {
		return err
	}
	if job == nil {
		return nil
	}
	var results []conversation.ActionResult
	if err := json.Unmarshal([]byte(job.Results), &results); err != nil {
		return err
	}
	for _, r := range results {
		if r.Type == "query_weather" && len(r.WeatherCards) > 0 {
			message.WeatherCards = append(message.WeatherCards, r.WeatherCards...)
		}
		if r.ReminderID != "" {
			message.ReminderIDs = append(message.ReminderIDs, r.ReminderID)
		}
		if r.Status != "succeeded" && r.Message != "" {
			if message.ReminderError != "" {
				message.ReminderError += " "
			}
			message.ReminderError += r.Message
		}
	}
	return s.DB.CompleteControl(ctx, job.ID)
}
func (s *Server) runControl(ctx context.Context, j dbop.ControlJob) (workErr error) {
	unlock := s.lockConversation(j.SessionID)
	defer unlock()
	attempted := false
	done := false
	defer func() {
		if !done {
			save, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			_ = s.DB.RetryControl(save, j.ID, attempted)
		}
	}()
	space, binding, err := s.conversationContext(ctx, j.SessionID, j.CreatedBy)
	if err != nil {
		return err
	}
	if !binding.CreatedAt.Equal(j.BindingCreatedAt) {
		done = true
		return s.DB.AbandonControl(ctx, j.ID)
	}
	history, err := s.Qoder.GetMessages(ctx, j.SessionID, "")
	if err != nil {
		return err
	}
	if !conversationIdle(history) {
		return nil
	}
	var results []conversation.ActionResult
	if j.Results != "" {
		if err := json.Unmarshal([]byte(j.Results), &results); err != nil {
			return err
		}
	} else {
		input, valid := conversation.DecodeInput(j.Origin)
		if !valid || input.Hidden || input.UserID != j.CreatedBy {
			return errors.New("invalid stored control origin")
		}
		var actions []conversation.Action
		if err := json.Unmarshal([]byte(j.Actions), &actions); err != nil {
			return err
		}
		if len(actions) == 0 {
			results = []conversation.ActionResult{{Key: "protocol", Type: "control", Status: "failed", ErrorCode: "invalid_control", Message: "AI 控制格式或请求关联无效，操作未执行，请重试。"}}
		}
		for index, a := range actions {
			results = append(results, s.executeAction(ctx, j, input, index, a, space))
		}
		if err := s.DB.SaveControlResults(ctx, j, results); err != nil {
			return err
		}
	}
	frame := conversation.NewEnvelopeV2(space, "action_result", j.RequestID)
	if input, ok := conversation.DecodeInput(j.Origin); ok {
		frame.ReplyMode, frame.RecipientID = input.Context.ReplyMode, input.Context.RecipientID
	}
	frame.Results = results
	if j.NotificationOnly {
		input, valid := conversation.DecodeInput(j.Origin)
		if !valid || input.Hidden || input.UserID != j.CreatedBy || len(results) != 1 || results[0].Type != "cancel_reminder" {
			return errors.New("invalid manual cancellation receipt")
		}
		reminder, err := s.DB.GetReminder(ctx, j.SessionID, results[0].ReminderID)
		if err != nil || reminder == nil {
			return errors.New("manual cancellation reminder not found")
		}
		if reminder.Status != dbop.ReminderCancelled {
			return errors.New("manual cancellation was not committed")
		}
		frame.Reminder = &conversation.Reminder{ID: reminder.ID, Title: reminder.Title, DueAt: reminder.DueAt, Status: reminder.Status, RecipientIDs: reminder.RecipientIDs}
		if memory, err := s.DB.GetReminderMemory(ctx, *reminder); err == nil && memory != nil && memory.State == "synced" {
			frame.Results[0].MemoryStatus = "synced"
		}
	}
	text, protocolState, err := s.prepareProtocolInput(ctx, frame)
	if err != nil {
		return err
	}
	if err := s.DB.BeginControlReceipt(ctx, j.ID); err != nil {
		return err
	}
	previous, err := s.DB.GetConversationJob(ctx, j.SessionID)
	if err != nil {
		return err
	}
	if err := s.DB.MarkConversationPending(ctx, j.SessionID, space.Now); err != nil {
		return err
	}
	attempted = true
	logging.Scheduler().Info("系统动作执行完成，向 AI 发送隐藏回执", "event", "control.receipt", "session_id", j.SessionID, "request_id", j.RequestID, "actions", len(results))
	sent, err := s.Qoder.SendMessage(ctx, j.SessionID, qoder.MessageInput{Text: text})
	save, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err != nil {
		s.resolveRejectedInput(save, j.SessionID, space.Now, previous, err)
		if definitivelyRejected(err) {
			attempted = false
		}
		return err
	}
	ids := []string{}
	for _, e := range sent.Events {
		ids = append(ids, e.ID)
	}
	if len(ids) == 0 {
		return errors.New("no system receipt event")
	}
	s.acceptProtocolInput(save, protocolState)
	if err := s.DB.AcceptControlReceipt(save, j.ID, ids); err != nil {
		return err
	}
	done = true
	return nil
}
func conversationIdle(h *qoder.MessagesResult) bool {
	if h.Session == nil || strings.ToLower(h.Session.Status) != "idle" {
		return false
	}
	for _, m := range h.Messages {
		if m.Kind == "ask" && !m.Answered {
			return false
		}
	}
	return true
}
func definitivelyRejected(err error) bool {
	var e *qoder.ApiError
	return errors.As(err, &e) && (e.Status == 400 || e.Status == 401 || e.Status == 403 || e.Status == 404 || e.Status == 409 || e.Status == 429 || e.Code == "not_configured")
}
func (s *Server) executeAction(ctx context.Context, j dbop.ControlJob, input conversation.Input, index int, a conversation.Action, space conversation.Context) conversation.ActionResult {
	result := conversation.ActionResult{Key: a.Key, Type: a.Type, Status: "failed"}
	fail := func(err error) conversation.ActionResult {
		result.ErrorCode = "operation_failed"
		result.Message = "这项操作未完成，请重试。"
		logging.Scheduler().Error("系统控制动作失败", "event", "control.action_failed", "session_id", j.SessionID, "request_id", j.RequestID, "action", a.Type, "error", err)
		return result
	}
	var memory *dbop.MemoryRecord
	deleteAnniversary := func(id string) error {
		row, err := s.DB.DeleteAnniversary(ctx, j.SessionID, j.ID+"/"+a.Key, input.UserID, id)
		if err != nil {
			return err
		}
		result.Anniversary = &conversation.Anniversary{ID: row.ID, Title: row.Title, Date: row.Date, Kind: row.Kind, Pinned: false}
		result.DatabaseStatus = "deleted"
		memory, err = s.DB.GetMemoryRecord(ctx, dbop.MemoryID(j.SessionID, dbop.AnniversaryMemoryPath(row.ID)), j.SessionID)
		return err
	}
	if input.Context.ReplyMode == conversation.SilentReply && a.Type != "save_memory" && a.Type != "read_memory" {
		return fail(errors.New("silent turn permits memory only"))
	}
	switch a.Type {
	case "query_weather":
		return s.executeWeatherQuery(ctx, j, input, a)
	case "set_region":
		region := regions.Location{}
		var err error
		if !a.Region.Clear {
			region, err = regions.ResolveNames(a.Region.Province, a.Region.City, a.Region.District)
			if err != nil {
				return fail(err)
			}
		}
		row, err := s.DB.ApplyProfileRegion(ctx, j.SessionID, j.ID+"/"+a.Key, input.UserID, a.TargetUserID, j.BindingCreatedAt, region)
		if err != nil {
			return fail(err)
		}
		result.TargetUserID, result.Region, result.DatabaseStatus = a.TargetUserID, &row.Region, "saved"
		memory, err = s.DB.GetMemoryRecord(ctx, dbop.MemoryID(j.SessionID, memoryspace.FactPath("profile", "self", a.TargetUserID, "account_profile")), j.SessionID)
		if err != nil {
			return fail(err)
		}
	case "set_weather_metrics":
		row, err := s.DB.ApplyCarePreference(ctx, j.SessionID, j.ID+"/"+a.Key, input.UserID, a.TargetUserID, j.BindingCreatedAt, a.Metrics)
		if err != nil {
			return fail(err)
		}
		result.TargetUserID, result.Metrics, result.DatabaseStatus = a.TargetUserID, row.Metrics, "saved"
		memory, err = s.DB.GetMemoryRecord(ctx, dbop.MemoryID(j.SessionID, dbop.CarePreferenceMemoryPath(a.TargetUserID)), j.SessionID)
		if err != nil {
			return fail(err)
		}
	case "save_countdown", "delete_countdown":
		var row *dbop.Countdown
		var err error
		if a.Type == "save_countdown" {
			row, err = s.DB.ApplyCountdown(ctx, j.SessionID, j.ID+"/"+a.Key, input.UserID, j.BindingCreatedAt, a.CountdownID, a.Title, a.Date, a.CountdownRepeat, a.CountdownKind, time.Now())
			result.DatabaseStatus = "saved"
		} else {
			row, err = s.DB.DeleteCountdown(ctx, j.SessionID, j.ID+"/"+a.Key, input.UserID, j.BindingCreatedAt, a.CountdownID)
			result.DatabaseStatus = "deleted"
		}
		if err != nil {
			return fail(err)
		}
		snapshot := protocolCountdown(*row)
		result.Countdown = &snapshot
		memory, err = s.DB.GetMemoryRecord(ctx, dbop.MemoryID(j.SessionID, dbop.CountdownMemoryPath(row.ID)), j.SessionID)
		if err != nil {
			return fail(err)
		}
	case "delete_anniversary":
		if err := deleteAnniversary(a.AnniversaryID); err != nil {
			return fail(err)
		}
	case "save_anniversary":
		row, err := s.DB.ApplyAnniversary(ctx, j.SessionID, j.ID+"/"+a.Key, input.UserID, a.AnniversaryID, a.Title, a.Date, a.AnniversaryKind)
		if err != nil {
			return fail(err)
		}
		result.Anniversary = &conversation.Anniversary{ID: row.ID, Title: row.Title, Date: row.Date, Kind: row.Kind, Pinned: row.Pinned}
		result.DatabaseStatus = "saved"
		memory, err = s.DB.GetMemoryRecord(ctx, dbop.MemoryID(j.SessionID, dbop.AnniversaryMemoryPath(row.ID)), j.SessionID)
		if err != nil {
			return fail(err)
		}
	case "create_reminder", "cancel_reminder":
		proposal := dbop.ReminderAction{CreatedBy: input.UserID, RequestKey: j.RequestID + "/" + a.Key}
		if a.Type == "create_reminder" {
			due, err := time.Parse(time.RFC3339, a.DueAt)
			if err != nil || !due.After(input.Context.Now) || !validRecipients(a.RecipientIDs, space) {
				return fail(dbop.ErrReminderInvalid)
			}
			proposal.Type = "create"
			proposal.Title = a.Title
			proposal.DueAt = due
			proposal.RecipientIDs = a.RecipientIDs
		} else {
			proposal.Type = "cancel"
			proposal.ReminderID = a.ReminderID
		}
		reminder, _, err := s.DB.ApplyReminderAction(ctx, j.SessionID, j.SourceEventID, index, proposal)
		if err != nil {
			return fail(err)
		}
		result.ReminderID = reminder.ID
		result.DatabaseStatus = "saved"
		memory, err = s.DB.GetReminderMemory(ctx, *reminder)
		if err != nil {
			return fail(err)
		}
	case "save_memory":
		owner := int64(0)
		if a.Scope == "self" {
			owner = input.UserID
		}
		category := a.Category
		if category == "" {
			category = "profile"
		}
		path := memoryspace.FactPath(category, a.Scope, owner, a.Key)
		var expires *time.Time
		if a.ExpiresAt != "" {
			value, err := time.Parse(time.RFC3339, a.ExpiresAt)
			if err != nil || category != "realtime" || a.Storage != "database_and_memory" || !value.After(time.Now()) {
				return fail(errors.New("invalid or expired realtime memory"))
			}
			expires = &value
		} else if category == "realtime" {
			return fail(errors.New("realtime memory requires expiry"))
		}
		generated := input.Context.Now
		if a.MemoryKey != "" {
			current, err := s.DB.GetMemoryRecord(ctx, a.MemoryKey, j.SessionID)
			if err != nil {
				return fail(err)
			}
			if current == nil || current.Kind != "fact" || current.Operation == "delete" || current.State == "deleted" || current.Scope != a.Scope || current.OwnerID != owner || (current.Category != "" && current.Category != category) {
				return fail(dbop.ErrReminderForbidden)
			}
			path = current.Path
			generated = current.CreatedAt
		}
		if dbop.AnniversaryIDFromMemoryPath(path) != "" {
			return fail(errors.New("anniversary corrections require save_anniversary"))
		}
		body, _ := json.Marshal(map[string]any{"schemaVersion": 1, "kind": "fact", "category": category, "scope": a.Scope, "ownerId": owner, "content": a.Content, "data": a.Data, "sourceUserId": input.UserID, "sourceRequestId": j.RequestID, "generatedAt": generated, "updatedAt": input.Context.Now, "expiresAt": expires, "expired": false})
		if len(body) > 79*1024 {
			return fail(errors.New("memory document too large"))
		}
		proposed := dbop.MemoryRecord{SessionID: j.SessionID, Path: path, Scope: a.Scope, OwnerID: owner, Category: category, ExpiresAt: expires, SourceUserID: input.UserID, SourceRequestID: j.RequestID, Storage: a.Storage, Operation: "upsert", PendingContent: string(body), BindingCreatedAt: j.BindingCreatedAt}

		var err error
		memory, err = s.DB.ApplyMemoryAction(ctx, j.ID+"/"+a.Key, proposed)
		if err != nil {
			return fail(err)
		}
		result.DatabaseStatus = "saved"
	case "read_memory":
		for _, key := range a.MemoryKeys {
			m, err := s.DB.GetMemoryRecord(ctx, key, j.SessionID)
			if err != nil {
				return fail(err)
			}
			if m == nil || m.State == "deleted" || m.Operation == "delete" || (m.ExpiresAt != nil && !m.ExpiresAt.After(time.Now())) {
				return fail(dbop.ErrReminderNotFound)
			}
			if a.Revision > 0 {
				revision, err := s.DB.GetMemoryRevision(ctx, j.SessionID, m.ID, a.Revision)
				if err != nil {
					return fail(err)
				}
				if m.Kind != "fact" || revision == nil || revision.Operation == "delete" || revision.Content == "" {
					return fail(dbop.ErrReminderNotFound)
				}
				result.Memories = append(result.Memories, conversation.MemoryRead{Key: m.ID, Path: m.DocumentPath(), Content: revision.Content})
				continue
			}
			if m.State != "synced" {
				if err := s.syncMemoryLocked(ctx, *m); err != nil {
					return fail(err)
				}
				m, err = s.DB.GetMemoryRecord(ctx, key, j.SessionID)
				if err != nil {
					return fail(err)
				}
			}
			if m == nil || m.StoreID == "" || m.EntryID == "" {
				return fail(errors.New("memory not synchronized"))
			}
			entry, err := s.Qoder.GetMemory(ctx, m.StoreID, m.EntryID)
			if err != nil {
				return fail(err)
			}
			body := entry.Content
			if m.Kind == "fact" {
				body, err = extractFactMemory(body, m.ID)
				if err != nil {
					return fail(err)
				}
			}
			result.Memories = append(result.Memories, conversation.MemoryRead{Key: m.ID, Path: entry.Path, Content: body})
		}
		result.Status = "succeeded"
		result.MemoryStatus = "read"
		return result
	case "delete_memory":
		current, err := s.DB.GetMemoryRecord(ctx, a.MemoryKey, j.SessionID)
		if err != nil {
			return fail(err)
		}
		if current == nil || current.Kind != "fact" || strings.HasPrefix(current.Path, "shared/reminders/") || (current.Scope == "self" && current.OwnerID != input.UserID) {
			return fail(dbop.ErrReminderForbidden)
		}
		if id := dbop.AnniversaryIDFromMemoryPath(current.Path); id != "" {
			// Compatibility with agents that still emit the previous memory action.
			if err := deleteAnniversary(id); err != nil {
				return fail(err)
			}
		} else {
			current.Operation = "delete"
			current.PendingContent = ""
			memory, err = s.DB.ApplyMemoryAction(ctx, j.ID+"/"+a.Key, *current)
			if err != nil {
				return fail(err)
			}
			result.DatabaseStatus = "deleted"
		}
	default:
		return fail(errors.New("unsupported control operation"))
	}
	if memory == nil {
		return fail(errors.New("missing memory outbox"))
	}
	result.MemoryKey = memory.ID
	if err := s.syncMemoryLocked(ctx, *memory); err != nil {
		result.Status = "partial"
		result.MemoryStatus = "pending"
		result.Message = "后台操作已记录，云端记忆暂未同步成功，系统会继续重试。"
		return result
	}
	result.MemoryStatus = "synced"
	result.Status = "succeeded"
	return result
}
