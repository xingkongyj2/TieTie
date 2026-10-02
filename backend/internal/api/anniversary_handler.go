package api

import (
	"errors"
	"net/http"
	"strconv"
	"tietie/backend/internal/auth"
	"tietie/backend/internal/dbop"
	"tietie/backend/internal/qoder"
)

func (s *Server) handleAnniversaries(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}
	session := r.PathValue("id")
	if err := s.ensureBoundSession(r.Context(), session); err != nil {
		writeError(w, err)
		return
	}
	limit := 50
	removed, deletionCursor, reset, err := s.DB.AnniversaryDeletionChanges(r.Context(), session, r.URL.Query().Get("afterDeletion"))
	if err != nil {
		writeError(w, anniversaryAPIError(err))
		return
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 50 {
			writeError(w, anniversaryAPIError(dbop.ErrAnniversaryInvalid))
			return
		}
		limit = value
	}
	after := r.URL.Query().Get("after")
	if reset {
		after = ""
	}
	rows, next, err := s.DB.ListAnniversaries(r.Context(), session, after, limit)
	if err != nil {
		writeError(w, anniversaryAPIError(err))
		return
	}
	featured, err := s.DB.FeaturedAnniversary(r.Context(), session)
	if err != nil {
		writeError(w, err)
		return
	}
	binding, err := s.DB.GetBindingBySessionID(r.Context(), session)
	if err != nil {
		writeError(w, err)
		return
	}
	if binding == nil {
		writeError(w, qoder.NewApiError(403, "session_forbidden", "当前会话已退出。"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"anniversaries": rows, "featured": featured, "nextCursor": next, "spaceCreatedAt": binding.CreatedAt, "removedIds": removed, "deletionCursor": deletionCursor, "resetRequired": reset})
}

func (s *Server) handleAnniversaryPin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		writeMethodNotAllowed(w)
		return
	}
	session := r.PathValue("id")
	if err := s.ensureBoundSession(r.Context(), session); err != nil {
		writeError(w, err)
		return
	}
	var body struct {
		Pinned *bool `json:"pinned"`
	}
	if err := decodeJSONBody(r, &body, 1024); err != nil {
		writeError(w, err)
		return
	}
	if body.Pinned == nil {
		writeError(w, anniversaryAPIError(dbop.ErrAnniversaryInvalid))
		return
	}
	unlock := s.lockConversation(session)
	defer unlock()
	if err := s.ensureBoundSession(r.Context(), session); err != nil {
		writeError(w, err)
		return
	}
	row, err := s.DB.PinAnniversary(r.Context(), session, r.PathValue("anniversaryId"), auth.UserIDFrom(r.Context()), *body.Pinned)
	if err != nil {
		writeError(w, anniversaryAPIError(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"anniversary": row, "memoryStatus": "pending"})
}

func anniversaryAPIError(err error) error {
	switch {
	case errors.Is(err, dbop.ErrAnniversaryInvalid):
		return qoder.NewApiError(400, "invalid_anniversary", "请提供纪念日名称、完整日期和有效的类型。")
	case errors.Is(err, dbop.ErrAnniversaryNotFound):
		return qoder.NewApiError(404, "anniversary_not_found", "这个纪念日不存在，请刷新后重试。")
	case errors.Is(err, dbop.ErrReminderForbidden):
		return qoder.NewApiError(403, "anniversary_forbidden", "无法修改其他会话的纪念日。")
	default:
		return err
	}
}
