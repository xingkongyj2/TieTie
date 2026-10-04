package api

import (
	"testing"

	"tietie/backend/internal/conversation"
)

func TestManualReminderNoticeOnlyMatchesServerManualReceipts(t *testing.T) {
	for _, tc := range []struct {
		name, requestID, actionType, status string
		want                                bool
	}{
		{"completed", "manual_complete_rem_1_20261004123000", "complete_reminder", "succeeded", true},
		{"restored", "manual_restore_rem_1_20261004123001", "restore_reminder", "succeeded", true},
		{"cancelled", "manual_cancel_rem_1", "cancel_reminder", "succeeded", true},
		{"ordinary completion", "turn_1_123", "complete_reminder", "succeeded", false},
		{"failed restore", "manual_restore_rem_1_20261004123001", "restore_reminder", "failed", false},
		{"different reminder", "manual_complete_rem_2_20261004123000", "complete_reminder", "succeeded", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := conversation.Input{Version: 2, Kind: "action_result", RequestID: tc.requestID,
				Results: []conversation.ActionResult{{Type: tc.actionType, Status: tc.status, ReminderID: "rem_1"}}}
			if got := manualReminderNotice(input); got != tc.want {
				t.Fatalf("manualReminderNotice() = %t, want %t", got, tc.want)
			}
		})
	}
}
