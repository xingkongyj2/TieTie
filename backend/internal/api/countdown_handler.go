package api

import (
	"errors"
	"gorm.io/gorm"
	"net/http"
	"tietie/backend/internal/auth"
	"tietie/backend/internal/dbop"
	"tietie/backend/internal/qoder"
	"time"
)

func (s *Server) handleCountdowns(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "POST" && r.Method != "DELETE" {
		writeMethodNotAllowed(w)
		return
	}
	session := r.PathValue("id")
	if err := s.ensureBoundSession(r.Context(), session); err != nil {
		writeError(w, err)
		return
	}
	actor := auth.UserIDFrom(r.Context())
	if r.Method != "GET" {
		var body struct {
			RequestID string `json:"requestId"`
			ID        string `json:"id"`
			Title     string `json:"title"`
			Date      string `json:"date"`
			Repeat    string `json:"repeat"`
			Kind      string `json:"kind"`
		}
		if err := decodeJSONBody(r, &body, 2048); err != nil {
			writeError(w, err)
			return
		}
		if !qoder.ValidEventID(body.RequestID) {
			writeError(w, qoder.NewApiError(400, "invalid_countdown_request", "请重试保存倒计时。"))
			return
		}
		var row *dbop.Countdown
		var err error
		if r.Method == "DELETE" {
			row, err = s.DB.DeleteCountdown(r.Context(), session, body.RequestID, actor, time.Time{}, body.ID)
		} else {
			row, err = s.DB.ApplyCountdown(r.Context(), session, body.RequestID, actor, time.Time{}, body.ID, body.Title, body.Date, body.Repeat, body.Kind, time.Now())
		}
		if err != nil {
			if errors.Is(err, dbop.ErrCountdownInvalid) {
				err = qoder.NewApiError(400, "invalid_countdown", err.Error())
			} else if errors.Is(err, gorm.ErrRecordNotFound) {
				err = qoder.NewApiError(404, "countdown_missing", "这条倒计时已不存在。")
			}
			writeError(w, err)
			return
		}

		writeJSON(w, 200, map[string]any{"countdown": row, "memoryStatus": "pending"})
		return
	}
	rows, next, err := s.DB.ListCountdowns(r.Context(), session, r.URL.Query().Get("after"), time.Now())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": rows, "nextCursor": next})
}
