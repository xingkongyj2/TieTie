package api

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"unicode"

	"tietie/backend/internal/auth"
	"tietie/backend/internal/conversation"
	"tietie/backend/internal/logging"
	"tietie/backend/internal/qoder"
)

// handleStream 处理 GET /api/qoder/sessions/{id}/stream：
// 打开上游 SSE，把帧转换成公开事件后实时转发给前端（对应 qoder.mjs stream 分支 + relayStream）。
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
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

	if strings.HasSuffix(r.URL.Path, "/private-stream") {
		binding, err := s.DB.GetBindingBySessionID(r.Context(), id)
		if err != nil {
			writeError(w, err)
			return
		}
		if binding == nil {
			writeError(w, qoder.NewApiError(403, "session_forbidden", "你没有访问此会话的权限。"))
			return
		}
		channel, err := s.DB.PrivateChannelForOwner(r.Context(), id, auth.UserIDFrom(r.Context()), binding.CreatedAt)
		if err != nil {
			writeError(w, err)
			return
		}
		if channel == nil {
			writeError(w, qoder.NewApiError(404, "private_channel_not_found", "还没有仅自己可见的消息。"))
			return
		}
		id = channel.SessionID
	}

	// 游标校验：查询串只允许一个合法的 after；Last-Event-ID 优先。
	invalidCursor := qoder.NewApiError(400, "invalid_query", "实时会话游标无效。")
	query := r.URL.Query()
	for key, vals := range query {
		if key != "after" || len(vals) > 1 {
			writeError(w, invalidCursor)
			return
		}
	}
	after := query.Get("after")
	if after != "" && !qoder.ValidEventID(after) {
		writeError(w, invalidCursor)
		return
	}
	lastEventID := r.Header.Get("Last-Event-ID")
	if lastEventID != "" && !qoder.ValidEventID(lastEventID) {
		writeError(w, invalidCursor)
		return
	}
	resume := lastEventID
	if resume == "" {
		resume = after
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, qoder.NewApiError(500, "internal_error", "会话服务发生错误，请稍后重试。"))
		return
	}

	body, err := s.Qoder.OpenEventStream(r.Context(), id, resume)
	if err != nil {
		if isClientGone(err) {
			return
		}
		writeError(w, err)
		return
	}
	defer body.Close()

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	_, _ = io.WriteString(w, ": connected\n\n")
	flusher.Flush()

	s.relayConversationStream(r, w, flusher, body, id)
}

// relayStream 逐行解析上游 SSE，把完整帧交给 qoder.ParseStreamEvent 过滤转换后写给前端。
func (s *Server) relayConversationStream(r *http.Request, w io.Writer, flusher http.Flusher, body io.Reader, sessionID string) {
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 64*1024), 300*1024)

	frameID := ""
	var dataLines []string
	// Keep the exact server envelope from this ordered stream, including hidden
	// action receipts. It supplies provenance without reloading old cloud events.
	priorInput := ""
	flushFrame := func() {
		defer func() { frameID = ""; dataLines = nil }()
		if len(dataLines) == 0 {
			return
		}
		data := []byte(strings.Join(dataLines, "\n"))
		var event qoder.Event
		if json.Unmarshal(data, &event) == nil && qoder.ValidEventID(event.ID) {
			if event.Type == "user.message" || event.Type == "user.custom_tool_result" {
				priorInput = eventText(event)
			}
			if event.Type == "agent.message" && conversation.ParseAssistant(eventText(event)).Control && streamOriginMatches(priorInput, sessionID, event) {
				// Control envelopes have no public bubble, but must still enter the
				// durable queue as soon as the model emits them.
				unlock := s.lockConversation(sessionID)
				if s.ensureConversationViewer(r.Context(), sessionID) == nil {
					_, _, err := s.processConversationFrom(r.Context(), sessionID, &qoder.MessagesResult{Events: []qoder.Event{event}}, auth.UserIDFrom(r.Context()), priorInput)
					if err != nil {
						logging.Scheduler().Warn("实时控制消息暂未入队，后台同步将继续恢复", "event", "control.stream_failed", "session_id", sessionID, "source_event_id", event.ID, "error", err)
					} else {
						logging.Scheduler().Info("实时通道已处理 AI 控制消息", "event", "control.stream_received", "session_id", sessionID, "source_event_id", event.ID)
					}
				}
				unlock()
			}
		}
		item := qoder.ParseStreamEvent(data)
		if item == nil {
			return
		}
		// AI emits a structured envelope. Buffer until the complete event so raw
		// action JSON never flashes in chat and only validated results are shown.
		if item["type"] == "delta" {
			return
		}
		if item["type"] == "start" {
			if kind := proactiveStreamKind(priorInput, sessionID); kind != "" {
				item["proactive"] = kind
			}
		}
		if item["type"] == "message" {
			if message, ok := item["message"].(qoder.PublicMessage); ok {
				if message.Sender == "ai" {
					unlock := s.lockConversation(sessionID)
					if apiErr := s.ensureConversationViewer(r.Context(), sessionID); apiErr != nil {
						unlock()
						return
					}
					var history *qoder.MessagesResult
					var err error
					if streamOriginMatches(priorInput, sessionID, event) {
						history = &qoder.MessagesResult{Events: []qoder.Event{event}, Messages: []qoder.PublicMessage{message}}
						_, _, err = s.processConversationFrom(r.Context(), sessionID, history, auth.UserIDFrom(r.Context()), priorInput)
					} else {
						// Resuming halfway through a turn may omit its input envelope.
						// Reconstruct provenance from full chronology in that case.
						history, err = s.Qoder.GetMessages(r.Context(), sessionID, "")
						if err == nil {
							_, _, err = s.processConversation(r.Context(), sessionID, history, auth.UserIDFrom(r.Context()))
						}
					}
					matched := false
					if err == nil {
						for _, verified := range history.Messages {
							if verified.ID == message.ID {
								item["message"] = verified
								matched = true
								break
							}
						}
						s.recordMessages(r.Context(), sessionID, history.Messages)
					} else {
						// Polling will recover; do not display unverified action results.
						unlock()
						return
					}
					unlock()
					if !matched {
						return
					}
				} else {
					space, _, err := s.conversationContext(r.Context(), sessionID, auth.UserIDFrom(r.Context()))
					if err != nil {
						return
					}
					mapMessageViewer(&message, space, auth.UserIDFrom(r.Context()))
					if message.Sender != "self" && message.Sender != "partner" {
						return
					}
					item["message"] = message
				}
			}
		}
		if channel, err := s.DB.GetPrivateChannel(r.Context(), sessionID); err != nil {
			return
		} else if channel != nil {
			if s.ensureConversationViewer(r.Context(), sessionID) != nil {
				return
			}
			item["private"] = true
			if m, ok := item["message"].(qoder.PublicMessage); ok {
				m.Visibility = "private"
				m.PrivateOwnerID = channel.OwnerID
				item["message"] = m
			}
		}
		if qoder.ValidEventID(frameID) {
			fmt.Fprintf(w, "id: %s\n", frameID)
		}
		encoded, err := json.Marshal(item)
		if err != nil {
			return
		}
		fmt.Fprintf(w, "data: %s\n\n", encoded)
		flusher.Flush()
	}

	for sc.Scan() {
		if r.Context().Err() != nil {
			return // 前端已断开
		}
		line := strings.TrimSuffix(sc.Text(), "\r")
		switch {
		case line == "":
			flushFrame()
		case strings.HasPrefix(line, ":"):
			_, _ = io.WriteString(w, ": heartbeat\n\n")
			flusher.Flush()
		case strings.HasPrefix(line, "id:"):
			frameID = strings.TrimSpace(line[len("id:"):])
		case strings.HasPrefix(line, "data:"):
			dataLines = append(dataLines, strings.TrimLeftFunc(line[len("data:"):], unicode.IsSpace))
		}
	}
	if err := sc.Err(); err != nil && !isClientGone(err) {
		log.Printf("SSE 上游流读取中断: %v", err)
	}
}

// Fast verification is allowed only when the ordered stream contains the exact
// server-owned input for this session and the V2 reply is correlated to its turn.
func streamOriginMatches(priorInput, sessionID string, event qoder.Event) bool {
	if event.Type != "agent.message" || !qoder.ValidEventID(event.ID) {
		return false
	}
	input, ok := conversation.DecodeInput(priorInput)
	if !ok || input.Context.SessionID != sessionID || input.Version != 2 {
		return false
	}
	assistant := conversation.ParseAssistant(eventText(event))
	return assistant.Version == 2 && assistant.RequestID == input.RequestID
}

func proactiveStreamKind(priorInput, sessionID string) string {
	origin, ok := conversation.DecodeInput(priorInput)
	if !ok || origin.Context.SessionID != sessionID {
		return ""
	}
	if origin.Kind == "reminder_due" && origin.Reminder != nil {
		return "reminder"
	}
	if manualReminderNotice(origin) {
		return "update"
	}
	return ""
}
