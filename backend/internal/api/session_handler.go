package api

import (
	"context"
	"log"
	"net/http"

	"tietie/backend/internal/dbop"
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

// getMessages 拉取会话消息（支持 after 游标增量）。
func (s *Server) getMessages(w http.ResponseWriter, r *http.Request, id string) {
	after, apiErr := parseAfterCursor(r, "会话游标无效，请刷新会话。")
	if apiErr != nil {
		writeError(w, apiErr)
		return
	}
	result, err := s.Qoder.GetMessages(r.Context(), id, after)
	if err != nil {
		writeError(w, err)
		return
	}
	s.recordSession(r.Context(), result.Session)
	s.recordMessages(r.Context(), id, result.Messages)
	writeJSON(w, http.StatusOK, result)
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
	result, err := s.Qoder.SendMessage(r.Context(), id, *input)
	if err != nil {
		writeError(w, err)
		return
	}
	s.recordMessages(r.Context(), id, result.Messages)
	writeJSON(w, http.StatusOK, result)
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
	for _, msg := range messages {
		record := &dbop.Message{
			ID: msg.ID, SessionID: sessionID, Sender: msg.Sender,
			Text: msg.Text, CloudCreatedAt: msg.CreatedAt,
		}
		if err := s.DB.SaveMessage(ctx, record); err != nil {
			log.Printf("消息落库失败 %s: %v", msg.ID, err)
		}
	}
}
