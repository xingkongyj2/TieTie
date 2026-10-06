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
			Title        string                   `json:"title"`
			DueAt        string                   `json:"dueAt"`
			RecipientIDs []int64                  `json:"recipientIds"`
			Recurrence   *conversation.Recurrence `json:"recurrence,omitempty"`
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
			utf8.RuneCountInString(body.Title) > conversation.ReminderTitleMaxRunes || !validRecipients(body.RecipientIDs, space) || conversation.ValidateRecurrence(body.Recurrence, dueAt) != nil {
			writeError(w, qoder.NewApiError(400, "invalid_reminder", "请填写不超过80字的提醒标题、未来的首次提醒时间和本空间的提醒对象，并检查重复日期是否与首次时间一致。"))
			return
		}
		var randomID [16]byte
		if _, err := rand.Read(randomID[:]); err != nil {
			writeError(w, err)
			return
		}
		reminder, _, err := s.DB.ApplyReminderAction(r.Context(), id, "manual_"+hex.EncodeToString(randomID[:]), 0,
			dbop.ReminderAction{Type: "create", Title: body.Title, DueAt: dueAt, Recurrence: body.Recurrence, RecipientIDs: body.RecipientIDs, CreatedBy: space.AuthorID})
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
	if r.Method == http.MethodDelete {
		s.handleReminderDelete(w, r)
		return
	}
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
		if s.useV2() {
			var space conversation.Context
			var binding *dbop.Binding
			space, binding, err = s.conversationContext(r.Context(), storageID, auth.UserIDFrom(r.Context()))
			if err == nil {
				requestID := "manual_restore_" + visible.ID + "_" + time.Now().UTC().Format("20060102150405.000000000")
				origin := conversation.NewEnvelopeV2(space, "user_message", requestID)
				origin.Text = "在提醒页手动恢复事项为待完成"
				origin.Compact = true
				encoded, _ := json.Marshal(origin)
				reminder, err = s.DB.RestoreCompletedReminderAndNotify(r.Context(), storageID, visible.ID, space.AuthorID, dbop.ControlJob{SessionID: storageID, RequestID: requestID, Origin: "\n<TIETIE_INPUT_V2>\n" + string(encoded) + "\n</TIETIE_INPUT_V2>", CreatedBy: space.AuthorID, BindingCreatedAt: binding.CreatedAt})
			}
		} else {
			reminder, err = s.DB.RestoreCompletedReminder(r.Context(), storageID, visible.ID, auth.UserIDFrom(r.Context()))
		}
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
	if s.useV2() && (body.Status == dbop.ReminderCompleted || body.Status == dbop.ReminderScheduled || body.Status == dbop.ReminderCancelled) {
		s.wakeControl()
	}
	reminder.SessionID = id
	if reminder.MemorySessionID != "" {
		reminder.SourceEventID = ""
	}
	writeJSON(w, http.StatusOK, map[string]any{"reminder": reminder})
}

func (s *Server) handleReminderDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !qoder.ValidSessionID(id) {
		writeRouteNotFound(w)
		return
	}
	if apiErr := s.ensureBoundSession(r.Context(), id); apiErr != nil {
		writeError(w, apiErr)
		return
	}
	unlock := s.lockConversation(id)
	defer unlock()
	if apiErr := s.ensureBoundSession(r.Context(), id); apiErr != nil {
		writeError(w, apiErr)
		return
	}
	reminderID := r.PathValue("reminderId")
	visible, err := s.DB.VisibleReminder(r.Context(), id, reminderID, auth.UserIDFrom(r.Context()))
	if err != nil || visible == nil || visible.Status == dbop.ReminderDeleted {
		if err == nil {
			err = dbop.ErrReminderNotFound
		}
		writeError(w, reminderAPIError(err))
		return
	}
	var randomID [16]byte
	if _, err := rand.Read(randomID[:]); err != nil {
		writeError(w, err)
		return
	}
	_, err = s.DB.DeleteVisibleReminders(r.Context(), id, auth.UserIDFrom(r.Context()), "manual_delete_"+hex.EncodeToString(randomID[:]), dbop.ReminderDeleteFilter{IDs: []string{reminderID}, Mode: "single"})
	if err != nil {
		writeError(w, reminderAPIError(err))
		return
	}
	s.wakeMemory()
	w.WriteHeader(http.StatusNoContent)
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
		return qoder.NewApiError(400, "invalid_reminder", "提醒标题（最多80字）、首次时间、重复规则或提醒对象无效。")
	case errors.Is(err, dbop.ErrReminderAmbiguous):
		return qoder.NewApiError(409, "reminder_ambiguous", "匹配到多条提醒，请说清楚要删除哪一条，或明确说全部删除。")
	case errors.Is(err, dbop.ErrReminderTooMany):
		return qoder.NewApiError(400, "too_many_reminders", "匹配的提醒超过200条，请缩小日期或状态范围后分批删除。")
	default:
		return err
	}
}
