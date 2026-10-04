package api

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"tietie/backend/internal/auth"
	"tietie/backend/internal/conversation"
	"tietie/backend/internal/dbop"
	"tietie/backend/internal/dto"
	"tietie/backend/internal/logging"
	"tietie/backend/internal/qoder"
)

// handleMessages 分发 GET/POST /api/qoder/sessions/{id}/messages。
// 需登录，且会话必须属于当前用户的绑定关系。
func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !qoder.ValidSessionID(id) {
		writeRouteNotFound(w)
		return
	}
	if apiErr := s.ensureBoundSession(r.Context(), id); apiErr != nil {
		writeError(w, apiErr)
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.getMessages(w, r, id)
	case http.MethodPost:
		s.postMessage(w, r, id)
	default:
		writeMethodNotAllowed(w)
	}
}

// handleMessagePreview returns recent, already persisted chat bubbles before
// the full cloud chronology has finished loading. It never changes the session
// cursor or readiness state; the normal history response remains authoritative.
func (s *Server) handleMessagePreview(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !qoder.ValidSessionID(id) {
		writeRouteNotFound(w)
		return
	}
	viewer := auth.UserIDFrom(r.Context())
	binding, err := s.DB.GetLatestBindingByUser(r.Context(), viewer)
	if err != nil {
		writeError(w, err)
		return
	}
	if binding == nil || binding.SessionID != id {
		writeError(w, qoder.NewApiError(403, "session_forbidden", "你没有访问此会话的权限。"))
		return
	}
	private, err := s.DB.PrivateChannelForOwner(r.Context(), id, viewer, binding.CreatedAt)
	if err != nil {
		writeError(w, err)
		return
	}
	privateID := ""
	if private != nil {
		privateID = private.SessionID
	}
	rows, err := s.DB.ListRecentVisibleMessages(r.Context(), id, privateID, viewer, 300)
	if err != nil {
		writeError(w, err)
		return
	}
	messages := make([]qoder.PublicMessage, 0, len(rows))
	for _, row := range rows {
		if message, ok := cachedPublicMessage(row, binding, viewer); ok {
			messages = append(messages, message)
		}
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, struct {
		Messages []qoder.PublicMessage `json:"messages"`
	}{messages})
}

func cachedPublicMessage(row dbop.Message, binding *dbop.Binding, viewer int64) (qoder.PublicMessage, bool) {
	if row.Sender != "ai" && row.Sender != "user" {
		return qoder.PublicMessage{}, false
	}
	if row.Source != "chat" && row.Source != "reminder" {
		return qoder.PublicMessage{}, false
	}
	if row.Text == "" && len(row.Files) == 0 {
		return qoder.PublicMessage{}, false
	}
	sender := "ai"
	if row.Sender != "ai" {
		if row.UserID == 0 || row.UserID != binding.UserA && row.UserID != binding.UserB {
			return qoder.PublicMessage{}, false
		}
		sender = "partner"
		if row.UserID == viewer {
			sender = "self"
		}
	}
	createdAt, err := time.Parse(time.RFC3339Nano, row.CloudCreatedAt)
	if err != nil {
		return qoder.PublicMessage{}, false
	}
	visibility := row.Visibility
	if visibility == "" {
		visibility = "shared"
	}
	return qoder.PublicMessage{
		ID: row.ID, Sender: sender, UserID: row.UserID, DisplayName: row.DisplayName,
		Text: row.Text, Files: row.Files, Source: row.Source, Visibility: visibility,
		RecipientIDs: row.RecipientIDs, Kind: "text", CreatedAt: row.CloudCreatedAt,
		Time: createdAt.In(time.FixedZone("Asia/Shanghai", 8*60*60)).Format("15:04"),
	}, true
}

// getMessages 拉取会话消息（支持 after 游标增量）。
func (s *Server) getMessages(w http.ResponseWriter, r *http.Request, id string) {
	if careAfter := r.Header.Get("X-Tietie-Care-After"); careAfter != "" && !qoder.ValidEventID(careAfter) {
		writeError(w, qoder.NewApiError(400, "invalid_query", "天气关怀游标无效，请刷新会话。"))
		return
	}
	after, apiErr := parseAfterCursor(r, "会话游标无效，请刷新会话。")
	if apiErr != nil {
		writeError(w, apiErr)
		return
	}
	unlock := s.lockConversation(id)
	defer unlock()
	if apiErr := s.ensureBoundSession(r.Context(), id); apiErr != nil {
		writeError(w, apiErr)
		return
	}
	// Full event context is required to attribute AI actions to the preceding
	// authenticated member even when a browser requests an incremental history.
	result, err := s.Qoder.GetMessages(r.Context(), id, "")
	if err != nil {
		writeError(w, err)
		return
	}
	space, warning, err := s.processConversation(r.Context(), id, result, auth.UserIDFrom(r.Context()))
	if err != nil {
		writeError(w, err)
		return
	}
	s.recordSession(r.Context(), result.Session)
	s.recordMessages(r.Context(), id, result.Messages)
	reminders, err := s.DB.ListVisibleReminders(r.Context(), id, auth.UserIDFrom(r.Context()))
	if err != nil {
		writeError(w, err)
		return
	}
	if active, err := s.DB.HasActiveControl(r.Context(), id); err != nil {
		writeError(w, err)
		return
	} else if active && result.Session != nil {
		result.Session.Status = "running"
	}
	filterHistoryAfter(result, after)
	if err := s.appendPrivateHistory(r.Context(), id, auth.UserIDFrom(r.Context()), result); err != nil {
		writeError(w, err)
		return
	}
	if err := s.appendCareHistory(r.Context(), id, result, r.Header.Get("X-Tietie-Care-After")); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		*qoder.MessagesResult
		Members        []conversation.Member `json:"members"`
		Reminders      []dbop.Reminder       `json:"reminders"`
		RemindersError string                `json:"remindersError,omitempty"`
	}{result, space.Members, reminders, warning})
}

// postMessage 发送消息（校验逻辑在 upload_handler.go parseMessage）。
func (s *Server) postMessage(w http.ResponseWriter, r *http.Request, id string) {
	if r.URL.RawQuery != "" {
		writeError(w, qoder.NewApiError(400, "invalid_query", "会话游标无效，请刷新会话。"))
		return
	}
	input, apiErr := parseMessage(r)
	if apiErr != nil {
		writeError(w, apiErr)
		return
	}
	unlock := s.lockConversation(id)
	defer unlock()
	if input.Visibility == "private" {
		shared, _, err := s.conversationContext(r.Context(), id, auth.UserIDFrom(r.Context()))
		if err != nil {
			writeError(w, err)
			return
		}
		if conversation.PartnerMention(shared, input.Text) != 0 {
			writeError(w, qoder.NewApiError(400, "partner_message_private", "@对方的消息需要双方可见，请关闭仅自己可见后发送。"))
			return
		}
		channel, err := s.ensurePrivateChannel(r.Context(), id, auth.UserIDFrom(r.Context()))
		if err != nil {
			writeError(w, err)
			return
		}
		id = channel.SessionID
		unlockPrivate := s.lockConversation(id)
		defer unlockPrivate()
	}
	space, _, err := s.conversationContext(r.Context(), id, auth.UserIDFrom(r.Context()))
	if err != nil {
		writeError(w, err)
		return
	}
	if active, err := s.DB.HasActiveControl(r.Context(), id); err != nil {
		writeError(w, err)
		return
	} else if active {
		writeError(w, qoder.NewApiError(409, "system_processing", "系统正在保存上一条操作，请稍后发送。"))
		return
	}
	originalText := input.Text
	if s.useV2() && space.Visibility != "private" {
		space.RecipientID = conversation.PartnerMention(space, input.Text)
		if space.RecipientID != 0 {
			space.ReplyMode = conversation.SilentReply
		}
	}
	var protocolState *dbop.ConversationProtocol
	if s.useV2() {
		frame := conversation.NewEnvelopeV2(space, "user_message", conversation.RequestID(space))
		frame.Text = input.Text
		frame.HasAttachments = len(input.Attachments) > 0
		input.Text, protocolState, err = s.prepareProtocolInput(r.Context(), frame)
		if err != nil {
			writeError(w, err)
			return
		}
	} else {
		input.Text = conversation.EncodeUser(space, input.Text)
	}
	previous, err := s.DB.GetConversationJob(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	// Persist before dispatch, including the uncertain outcome of a connection loss.
	if err := s.DB.MarkConversationPending(r.Context(), id, space.Now); err != nil {
		writeError(w, err)
		return
	}
	result, err := s.Qoder.SendMessage(r.Context(), id, *input)
	if err != nil {
		s.resolveRejectedInput(r.Context(), id, space.Now, previous, err)
		writeError(w, err)
		return
	}
	logging.System().Info("成员消息已被 AI 接受，后台开始跟踪回复", "event", "conversation.accepted", "session_id", id, "user_id", space.AuthorID)
	s.acceptProtocolInput(r.Context(), protocolState)
	s.wakeConversation()
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
	defer cancel()
	var direct *dbop.Reminder
	var saveErr error
	if !s.useV2() {
		direct, saveErr = s.saveDirectReminder(saveCtx, space, originalText)
	}
	if saveErr != nil {
		logging.Scheduler().Error("明确的提醒请求落库失败", "event", "reminder.save_failed", "session_id", id, "error", saveErr)
	}
	for index := range result.Messages {
		mapMessageViewer(&result.Messages[index], space, space.AuthorID)
		if result.Messages[index].Sender != "ai" {
			if direct != nil {
				result.Messages[index].ReminderIDs = []string{direct.ID}
			}
			if saveErr != nil {
				result.Messages[index].ReminderError = "提醒未保存成功，请重试。"
			}
		}
	}
	if err := s.applyChannelVisibility(r.Context(), id, result.Messages); err != nil {
		writeError(w, err)
		return
	}
	result.Messages = acceptedMemberMessages(result.Messages)
	result.ReplyMode = space.ReplyMode
	s.recordMessages(r.Context(), id, result.Messages)
	writeJSON(w, http.StatusOK, result)
}

// handleToolResult 处理 POST /api/qoder/sessions/{id}/tool-result：
// 把用户对 AskUserQuestion 这类提问的选择回传给云端，让挂起的那一轮继续。
func (s *Server) handleToolResult(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
		return
	}
	id := r.PathValue("id")
	if !qoder.ValidSessionID(id) {
		writeRouteNotFound(w)
		return
	}
	if apiErr := s.ensureBoundSession(r.Context(), id); apiErr != nil {
		writeError(w, apiErr)
		return
	}
	var body struct {
		ToolUseID string `json:"toolUseId"`
		Text      string `json:"text"`
	}
	if apiErr := decodeJSONBody(r, &body, 4096); apiErr != nil {
		writeError(w, apiErr)
		return
	}
	toolUseID := strings.TrimSpace(body.ToolUseID)
	if !qoder.ValidEventID(toolUseID) {
		writeError(w, qoder.NewApiError(400, "invalid_tool_use_id", "这道提问已经失效，请刷新会话。"))
		return
	}
	text := strings.TrimSpace(body.Text)
	if text == "" || utf8.RuneCountInString(text) > dto.MaxTextRunes {
		writeError(w, qoder.NewApiError(400, "invalid_answer", "答案不能为空，且不超过 2000 字。"))
		return
	}
	unlock := s.lockConversation(id)
	defer unlock()
	channelID, routeErr := s.answerChannel(r.Context(), id, auth.UserIDFrom(r.Context()), toolUseID)
	if routeErr != nil {
		writeError(w, routeErr)
		return
	}
	if channelID != id {
		id = channelID
		unlockPrivate := s.lockConversation(id)
		defer unlockPrivate()
	}
	space, _, err := s.conversationContext(r.Context(), id, auth.UserIDFrom(r.Context()))
	if err != nil {
		writeError(w, err)
		return
	}
	previous, err := s.DB.GetConversationJob(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	answer := s.encodeMember(space, text)
	var protocolState *dbop.ConversationProtocol
	if s.useV2() {
		frame := conversation.NewEnvelopeV2(space, "user_message", conversation.RequestID(space))
		frame.Text = text
		answer, protocolState, err = s.prepareProtocolInput(r.Context(), frame)
		if err != nil {
			writeError(w, err)
			return
		}
	}
	if err := s.DB.MarkConversationPending(r.Context(), id, space.Now); err != nil {
		writeError(w, err)
		return
	}
	result, err := s.Qoder.SendCustomToolResult(r.Context(), id, toolUseID, answer)
	if err != nil {
		s.resolveRejectedInput(r.Context(), id, space.Now, previous, err)
		writeError(w, err)
		return
	}
	s.acceptProtocolInput(r.Context(), protocolState)
	s.wakeConversation()
	for index := range result.Messages {
		mapMessageViewer(&result.Messages[index], space, space.AuthorID)
	}
	if err := s.applyChannelVisibility(r.Context(), id, result.Messages); err != nil {
		writeError(w, err)
		return
	}
	result.Messages = acceptedMemberMessages(result.Messages)
	writeJSON(w, http.StatusOK, result)
}

// AI replies are published by history/SSE only after their actions are validated.
func acceptedMemberMessages(messages []qoder.PublicMessage) []qoder.PublicMessage {
	out := make([]qoder.PublicMessage, 0, len(messages))
	for _, message := range messages {
		if (message.Sender == "self" || message.Sender == "partner") && message.UserID > 0 {
			out = append(out, message)
		}
	}
	return out
}

func (s *Server) resolveRejectedInput(ctx context.Context, id string, attemptedAt time.Time, previous *dbop.ConversationJob, err error) {
	var apiErr *qoder.ApiError
	if !errors.As(err, &apiErr) {
		return
	}
	if apiErr.Status < 400 || (apiErr.Status >= 500 && apiErr.Code != "not_configured") {
		return
	}
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := s.DB.RejectConversationInput(saveCtx, id, attemptedAt, previous); err != nil {
		log.Printf("撤销未接受消息的同步任务失败: %v", err)
	}
}

// Preserve the public incremental contract after analyzing complete chronology.
func filterHistoryAfter(result *qoder.MessagesResult, after string) {
	if after == "" {
		return
	}
	seen, include := false, map[string]bool{}
	for _, event := range result.Events {
		if seen {
			include[event.ID] = true
		}
		if event.ID == after {
			seen = true
		}
	}
	if !seen {
		return
	} // Unknown/expired cursor: resynchronize the complete history.
	filtered := make([]qoder.PublicMessage, 0)
	for _, message := range result.Messages {
		if include[message.ID] {
			filtered = append(filtered, message)
		}
	}
	result.Messages = filtered
}

// parseAfterCursor 校验查询串：只允许出现一次 after，且必须是合法事件 ID。
func parseAfterCursor(r *http.Request, message string) (string, *qoder.ApiError) {
	query := r.URL.Query()
	for key, vals := range query {
		if key != "after" || len(vals) > 1 {
			return "", qoder.NewApiError(400, "invalid_query", message)
		}
	}
	after := query.Get("after")
	if after != "" && !qoder.ValidEventID(after) {
		return "", qoder.NewApiError(400, "invalid_query", message)
	}
	return after, nil
}

// ---- 数据库落库（尽力而为，失败只记日志不影响响应）----

func (s *Server) recordSession(ctx context.Context, sess *qoder.PublicSession) {
	if s.DB == nil || sess == nil {
		return
	}
	record := &dbop.Session{
		ID: sess.ID, Title: sess.Title, Status: sess.Status,
		CloudCreatedAt: sess.CreatedAt, CloudUpdatedAt: sess.UpdatedAt,
	}
	if err := s.DB.UpsertSession(ctx, record); err != nil {
		log.Printf("会话落库失败 %s: %v", sess.ID, err)
	}
}

func (s *Server) recordMessages(ctx context.Context, sessionID string, messages []qoder.PublicMessage) {
	if s.DB == nil {
		return
	}
	records := make([]*dbop.Message, 0, len(messages))
	for _, msg := range messages {
		sender := "user"
		if msg.Sender == "ai" {
			sender = "ai"
		}
		records = append(records, &dbop.Message{
			ID: msg.ID, SessionID: sessionID, Sender: sender,
			UserID: msg.UserID, DisplayName: msg.DisplayName, Visibility: msg.Visibility, PrivateOwnerID: msg.PrivateOwnerID, RecipientIDs: msg.RecipientIDs, Source: msg.Source,
			Text: msg.Text, Files: msg.Files, CloudCreatedAt: msg.CreatedAt,
		})
	}
	if err := s.DB.SaveMessages(ctx, records); err != nil {
		log.Printf("消息落库失败 %s 共 %d 条: %v", sessionID, len(records), err)
	}
}
