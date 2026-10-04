package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"tietie/backend/internal/auth"
	"tietie/backend/internal/conversation"
	"tietie/backend/internal/dbop"
	"tietie/backend/internal/qoder"
)

func (s *Server) handleReminders(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !qoder.ValidSessionID(id) {
		writeRouteNotFound(w)
		return
	}
	if apiErr := s.ensureBoundSession(r.Context(), id); apiErr != nil {
		writeError(w, apiErr)
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, qoder.NewApiError(400, "invalid_query", "提醒请求参数无效。"))
		return
	}
	switch r.Method {
	case http.MethodGet:
		reminders, err := s.DB.ListVisibleReminders(r.Context(), id, auth.UserIDFrom(r.Context()))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"reminders": reminders})
	case http.MethodPost:
		var body struct {
			Title        string  `json:"title"`
			DueAt        string  `json:"dueAt"`
			RecipientIDs []int64 `json:"recipientIds"`
		}
		if apiErr := decodeJSONBody(r, &body, 8192); apiErr != nil {
			writeError(w, apiErr)
			return
		}
		unlock := s.lockConversation(id)
		defer unlock()
		space, _, err := s.conversationContext(r.Context(), id, auth.UserIDFrom(r.Context()))
		if err != nil {
			writeError(w, err)
			return
		}
		dueAt, dateErr := time.Parse(time.RFC3339, body.DueAt)
		if dateErr != nil || !dueAt.After(time.Now()) || strings.TrimSpace(body.Title) == "" ||
			utf8.RuneCountInString(body.Title) > 500 || !validRecipients(body.RecipientIDs, space) {
			writeError(w, qoder.NewApiError(400, "invalid_reminder", "请填写提醒内容、未来的具体时间和本空间的提醒对象。"))
			return
		}
		var randomID [16]byte
		if _, err := rand.Read(randomID[:]); err != nil {
			writeError(w, err)
			return
		}
		reminder, _, err := s.DB.ApplyReminderAction(r.Context(), id, "manual_"+hex.EncodeToString(randomID[:]), 0,
			dbop.ReminderAction{Type: "create", Title: body.Title, DueAt: dueAt, RecipientIDs: body.RecipientIDs, CreatedBy: space.AuthorID})
		if err != nil {
			writeError(w, reminderAPIError(err))
			return
		}
		s.wakeMemory()
		writeJSON(w, http.StatusCreated, map[string]any{"reminder": reminder})
	default:
		writeMethodNotAllowed(w)
	}
}

func (s *Server) handleReminderUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
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
		Status string `json:"status"`
	}
	if apiErr := decodeJSONBody(r, &body, 4096); apiErr != nil {
		writeError(w, apiErr)
		return
	}
	unlock := s.lockConversation(id)
	defer unlock()
	// Binding may have changed while the request waited for the writer.
	if apiErr := s.ensureBoundSession(r.Context(), id); apiErr != nil {
		writeError(w, apiErr)
		return
	}
	visible, accessErr := s.DB.VisibleReminder(r.Context(), id, r.PathValue("reminderId"), auth.UserIDFrom(r.Context()))
	if accessErr != nil {
		writeError(w, accessErr)
		return
	}
	if visible == nil {
		writeError(w, reminderAPIError(dbop.ErrReminderNotFound))
		return
	}
	storageID := visible.SessionID
	var reminder *dbop.Reminder
	var err error
	switch body.Status {
	case dbop.ReminderCompleted:
		if s.useV2() {
			var space conversation.Context
			var binding *dbop.Binding
			space, binding, err = s.conversationContext(r.Context(), storageID, auth.UserIDFrom(r.Context()))
			if err == nil {
				requestID := "manual_complete_" + visible.ID + "_" + time.Now().UTC().Format("20060102150405.000000000")
				origin := conversation.NewEnvelopeV2(space, "user_message", requestID)
				origin.Text = "在提醒页手动标记事项完成"
				origin.Compact = true
				encoded, _ := json.Marshal(origin)
				reminder, err = s.DB.CompleteReminderAndNotify(r.Context(), storageID, visible.ID, space.AuthorID, dbop.ControlJob{SessionID: storageID, RequestID: requestID, Origin: "\n<TIETIE_INPUT_V2>\n" + string(encoded) + "\n</TIETIE_INPUT_V2>", CreatedBy: space.AuthorID, BindingCreatedAt: binding.CreatedAt})
			}
		} else {
			reminder, err = s.DB.CompleteReminder(r.Context(), storageID, visible.ID, auth.UserIDFrom(r.Context()))
		}
	case dbop.ReminderScheduled:
		reminder, err = s.DB.RestoreCompletedReminder(r.Context(), storageID, r.PathValue("reminderId"), auth.UserIDFrom(r.Context()))
	case dbop.ReminderCancelled:
		if s.useV2() {
			var space conversation.Context
			var binding *dbop.Binding
			space, binding, err = s.conversationContext(r.Context(), storageID, auth.UserIDFrom(r.Context()))
			if err == nil {
				requestID := "manual_cancel_" + visible.ID
				origin := conversation.NewEnvelopeV2(space, "user_message", requestID)
				origin.Text = "在提醒页手动取消提醒"
				origin.Compact = true
				encoded, _ := json.Marshal(origin)
				reminder, err = s.DB.CancelReminderAndNotify(r.Context(), storageID, visible.ID, dbop.ControlJob{SessionID: storageID, RequestID: requestID, Origin: "\n<TIETIE_INPUT_V2>\n" + string(encoded) + "\n</TIETIE_INPUT_V2>", CreatedBy: space.AuthorID, BindingCreatedAt: binding.CreatedAt})
			}
		} else {
			reminder, err = s.DB.CancelReminder(r.Context(), storageID, visible.ID)
		}
	default:
		writeError(w, qoder.NewApiError(400, "invalid_reminder_status", "不支持这项提醒操作。"))
		return
	}
	if err != nil {
		writeError(w, reminderAPIError(err))
		return
	}
	s.wakeMemory()
	if s.useV2() && (body.Status == dbop.ReminderCompleted || body.Status == dbop.ReminderCancelled) {
		s.wakeControl()
	}
	reminder.SessionID = id
	if reminder.MemorySessionID != "" {
		reminder.SourceEventID = ""
	}
	writeJSON(w, http.StatusOK, map[string]any{"reminder": reminder})
}

func reminderAPIError(err error) error {
	switch {
	case errors.Is(err, dbop.ErrReminderNotFound):
		return qoder.NewApiError(404, "reminder_not_found", "这条提醒不存在，请刷新提醒板。")
	case errors.Is(err, dbop.ErrReminderForbidden):
		return qoder.NewApiError(403, "reminder_forbidden", "只有本条提醒的接收者可以标记完成。")
	case errors.Is(err, dbop.ErrReminderState):
		return qoder.NewApiError(409, "reminder_state", "提醒状态已经变化，请刷新提醒板后重试。")
	case errors.Is(err, dbop.ErrReminderInvalid):
		return qoder.NewApiError(400, "invalid_reminder", "提醒内容、时间或提醒对象无效。")
	default:
		return err
	}
}
