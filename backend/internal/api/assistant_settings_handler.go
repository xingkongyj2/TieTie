package api

import (
	"net/http"
	"tietie/backend/internal/auth"
	"tietie/backend/internal/dbop"
	"tietie/backend/internal/memoryspace"
	"tietie/backend/internal/qoder"
)

func (s *Server) handleAssistantSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPut {
		writeMethodNotAllowed(w)
		return
	}
	session := r.PathValue("id")
	if err := s.ensureBoundSession(r.Context(), session); err != nil {
		writeError(w, err)
		return
	}
	status := ""
	if r.Method == http.MethodPut {
		// 只有写入需要与本会话的 AI 任务串行；读取不占这把锁，否则一次慢回复会把页面卡住。
		unlock := s.lockConversation(session)
		defer unlock()
		if err := s.ensureBoundSession(r.Context(), session); err != nil {
			writeError(w, err)
			return
		}
		var body struct {
			Tone string `json:"tone"`
		}
		if err := decodeJSONBody(r, &body, 1024); err != nil {
			writeError(w, err)
			return
		}
		if _, ok := memoryspace.Style(body.Tone); !ok {
			writeError(w, qoder.NewApiError(400, "invalid_tone", "请选择温柔陪伴、调皮一点或简单直接。"))
			return
		}
		if err := s.DB.SaveAssistantStyle(r.Context(), session, auth.UserIDFrom(r.Context()), body.Tone); err != nil {
			writeError(w, err)
			return
		}
		status = "pending"
		if s.Cfg != nil && s.Cfg.CloudMemoryEnabled {
			root, err := s.DB.GetMemoryRecord(r.Context(), dbop.MemoryID(session, memoryspace.BehaviorPath), session)
			if err == nil && root != nil && (root.State == "synced" || s.syncMemoryLocked(r.Context(), *root) == nil) {
				status = "synced"
			}
		}
	}
	style, err := s.DB.GetAssistantStyle(r.Context(), session)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": map[string]any{"tone": style.Tone}, "memoryStatus": status})
}
