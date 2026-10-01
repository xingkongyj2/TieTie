package api

import (
	"net/http"
	"strings"
	"tietie/backend/internal/conversation"
	"tietie/backend/internal/qoder"
)

// A bounded metadata API for future memory-management views. Bodies stay in
// the cloud for memory_only; a user can only list their currently bound space.
func (s *Server) handleMemoryIndex(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.ensureBoundSession(r.Context(), id); err != nil {
		writeError(w, err)
		return
	}
	query := r.URL.Query()
	for key, values := range query {
		if (key != "category" && key != "after") || len(values) != 1 {
			writeError(w, qoder.NewApiError(400, "invalid_query", "记忆查询参数无效。"))
			return
		}
	}
	category, after := query.Get("category"), query.Get("after")
	switch category {
	case "", "profile", "habit", "agreement", "realtime", "behavior":
	default:
		writeError(w, qoder.NewApiError(400, "invalid_query", "记忆分类无效。"))
		return
	}
	if after != "" && (!strings.HasPrefix(after, "memory_") || len(after) != 39 || strings.Trim(after[7:], "0123456789abcdef") != "") {
		writeError(w, qoder.NewApiError(400, "invalid_query", "记忆游标无效。"))
		return
	}
	rows, err := s.DB.ListMemoryIndex(r.Context(), id, category, after, 100)
	if err != nil {
		writeError(w, err)
		return
	}
	items := make([]conversation.MemoryIndex, 0, len(rows))
	for _, m := range rows {
		items = append(items, conversation.MemoryIndex{Key: m.ID, Path: m.DocumentPath(), Scope: m.Scope, OwnerID: m.OwnerID, State: m.State, Category: m.Category, ExpiresAt: m.ExpiresAt})
	}
	next := ""
	if len(rows) == 100 {
		next = rows[len(rows)-1].ID
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items, "nextCursor": next})
}
