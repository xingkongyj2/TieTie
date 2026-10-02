package api

import (
	"net/http"
	"time"

	"tietie/backend/internal/auth"
	"tietie/backend/internal/qoder"
)

func (s *Server) handleAnniversaryReminderSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPut {
		writeMethodNotAllowed(w)
		return
	}
	session := r.PathValue("id")
	if err := s.ensureBoundSession(r.Context(), session); err != nil {
		writeError(w, err)
		return
	}
	if r.Method == http.MethodPut {
		var body struct {
			Enabled *bool `json:"enabled"`
		}
		if err := decodeJSONBody(r, &body, 1024); err != nil {
			writeError(w, err)
			return
		}
		if body.Enabled == nil {
			writeError(w, qoder.NewApiError(400, "invalid_anniversary_reminder_settings", "请选择是否开启纪念日提醒。"))
			return
		}
		if err := s.DB.SaveAnniversaryReminderSettings(r.Context(), session, auth.UserIDFrom(r.Context()), *body.Enabled, time.Now()); err != nil {
			writeError(w, anniversaryAPIError(err))
			return
		}
	}
	settings, err := s.DB.GetAnniversaryReminderSettings(r.Context(), session)
	if err != nil {
		writeError(w, anniversaryAPIError(err))
		return
	}
	writeJSON(w, http.StatusOK, settings)
}
