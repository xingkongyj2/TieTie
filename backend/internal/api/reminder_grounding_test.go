package api

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"tietie/backend/internal/conversation"
	"tietie/backend/internal/dbop"
)

func groundingContext() (conversation.Context, conversation.Input) {
	now := time.Date(2026, 10, 4, 8, 8, 0, 0, time.FixedZone("CST", 8*60*60))
	space := conversation.Context{Now: now.Add(5 * time.Second), Members: []conversation.Member{{ID: 1, Name: "甲"}, {ID: 2, Name: "乙"}}}
	input := conversation.Input{UserID: 1, Text: "明天上午8点提醒我喝一杯温水", Context: conversation.Context{Now: now, Members: []conversation.Member{{ID: 1, Name: "甲"}}}}
	return space, input
}

func TestGroundReminderCorrectsModelDateFromExactMemberRequest(t *testing.T) {
	space, input := groundingContext()
	actions := []conversation.Action{{Type: "create_reminder", Key: "drink_water", Storage: "database_and_memory", Title: "喝水提醒", DueAt: "2026-10-04T09:00:00+08:00", RecipientIDs: []int64{1, 2}}}
	grounded, changed, err := groundSingleReminderAction(actions, input, space)
	if err != nil || !changed || grounded[0].DueAt != "2026-10-05T08:00:00+08:00" || grounded[0].Title != "喝一杯温水" || !reflect.DeepEqual(grounded[0].RecipientIDs, []int64{1}) {
		t.Fatalf("exact original request must override model guesses: grounded=%+v changed=%v err=%v", grounded, changed, err)
	}
	if grounded[0].Key != actions[0].Key || grounded[0].Storage != actions[0].Storage || actions[0].DueAt != "2026-10-04T09:00:00+08:00" {
		t.Fatal("grounding must preserve operation identity without mutating the stored proposal")
	}
}

func TestGroundReminderLeavesAmbiguousOrMultipleActionsUntouched(t *testing.T) {
	space, input := groundingContext()
	action := conversation.Action{Type: "create_reminder", DueAt: "2026-10-04T09:00:00+08:00"}
	for _, test := range []struct {
		text    string
		actions []conversation.Action
	}{
		{"明天8点提醒我喝水", []conversation.Action{action}},
		{input.Text, []conversation.Action{action, action}},
		{input.Text, []conversation.Action{action, {Type: "cancel_reminder"}}},
	} {
		input.Text = test.text
		grounded, changed, err := groundSingleReminderAction(test.actions, input, space)
		if err != nil || changed || !reflect.DeepEqual(grounded, test.actions) {
			t.Fatalf("nonunique request/actions must remain untouched: grounded=%+v changed=%v err=%v", grounded, changed, err)
		}
	}
}

func TestGroundReminderRejectsElapsedExactTime(t *testing.T) {
	space, input := groundingContext()
	input.Text = "今天上午8点提醒我喝水"
	_, _, err := groundSingleReminderAction([]conversation.Action{{Type: "create_reminder", DueAt: "2026-10-04T09:00:00+08:00"}}, input, space)
	if !errors.Is(err, dbop.ErrReminderInvalid) {
		t.Fatalf("a model must not replace the specified elapsed time: %v", err)
	}
}
