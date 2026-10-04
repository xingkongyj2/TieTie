package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"tietie/backend/internal/auth"
	"tietie/backend/internal/conversation"
	"tietie/backend/internal/dbop"
	"tietie/backend/internal/qoder"
)

type operationStatus struct {
	Status    string          `json:"status"`
	Reminders []dbop.Reminder `json:"reminders"`
	Message   string          `json:"message,omitempty"`
}

// This read uses only committed database rows. It deliberately takes no
// conversation lock, so sending the later AI receipt cannot delay confirmation.
func (s *Server) handleOperationStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	id := r.PathValue("id")
	if !qoder.ValidSessionID(id) {
		writeRouteNotFound(w)
		return
	}
	requestID := r.URL.Query().Get("requestId")
	if requestID == "" || len(requestID) > 160 || strings.TrimSpace(requestID) != requestID {
		writeError(w, qoder.NewApiError(400, "invalid_query", "操作编号无效，请刷新会话。"))
		return
	}
	if apiErr := s.ensureConversationViewer(r.Context(), id); apiErr != nil {
		writeError(w, apiErr)
		return
	}
	binding, err := s.DB.GetBindingBySessionID(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	job, err := s.DB.GetControl(r.Context(), id, requestID)
	if err != nil {
		writeError(w, err)
		return
	}
	viewer := auth.UserIDFrom(r.Context())
	if binding == nil || viewer != binding.UserA && viewer != binding.UserB ||
		job != nil && (job.SessionID != id || job.RequestID != requestID) {
		writeError(w, qoder.NewApiError(403, "operation_forbidden", "你没有访问此操作的权限。"))
		return
	}
	status, err := savedOperationStatus(job, binding, viewer, func(reminderID string) (*dbop.Reminder, error) {
		return s.DB.GetReminder(r.Context(), id, reminderID)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func savedOperationStatus(job *dbop.ControlJob, binding *dbop.Binding, viewer int64, lookup func(string) (*dbop.Reminder, error)) (operationStatus, error) {
	status := operationStatus{Status: "pending", Reminders: []dbop.Reminder{}}
	if binding == nil || viewer <= 0 || viewer != binding.UserA && viewer != binding.UserB {
		return status, qoder.NewApiError(403, "operation_forbidden", "你没有访问此操作的权限。")
	}
	if job == nil {
		return status, nil
	}
	if job.SessionID != binding.SessionID || job.CreatedBy != viewer || !job.BindingCreatedAt.Equal(binding.CreatedAt) {
		return status, qoder.NewApiError(403, "operation_forbidden", "你没有访问此操作的权限。")
	}
	if job.Results == "" {
		return status, nil
	}
	var results []conversation.ActionResult
	if err := json.Unmarshal([]byte(job.Results), &results); err != nil {
		return status, err
	}
	failed := false
	seen := map[string]bool{}
	for _, result := range results {
		if result.Status == "failed" {
			failed = true
		}
		if result.Type != "create_reminder" || result.Status != "succeeded" ||
			result.DatabaseStatus != "saved" || result.MemoryStatus != "synced" ||
			result.ReminderID == "" || seen[result.ReminderID] {
			continue
		}
		reminder, err := lookup(result.ReminderID)
		if err != nil {
			return status, err
		}
		if reminder == nil || reminder.ID != result.ReminderID || reminder.SessionID != job.SessionID || reminder.CreatedBy != viewer ||
			reminder.Status != dbop.ReminderScheduled || !reminder.BindingCreatedAt.Equal(binding.CreatedAt) {
			continue
		}
		seen[reminder.ID] = true
		status.Reminders = append(status.Reminders, *reminder)
	}
	if len(status.Reminders) > 0 {
		status.Status = "saved"
	} else if failed {
		status.Status = "failed"
		status.Message = "这次操作未完成，请稍后重试。"
	}
	return status, nil
}
