package conversation

import (
	"strings"
	"testing"
)

func TestReminderTitleValidationUsesTheSharedLimit(t *testing.T) {
	base := strings.Repeat("好", ReminderTitleMaxRunes)
	for _, test := range []struct {
		name  string
		title string
		want  bool
	}{
		{"at limit", base, true},
		{"over limit", base + "好", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			action := Action{Type: "create_reminder", Key: "reminder", Title: test.title, DueAt: "2026-10-07T08:00:00+08:00", RecipientIDs: []int64{1}, Storage: "database_and_memory"}
			if got := ValidateV2Action(action) == nil; got != test.want {
				t.Fatalf("V2 title length=%d valid=%t, want %t", len([]rune(test.title)), got, test.want)
			}
			action.Storage = ""
			if got := validateAction(action) == nil; got != test.want {
				t.Fatalf("V1 title length=%d valid=%t, want %t", len([]rune(test.title)), got, test.want)
			}
		})
	}
}
