package api

import (
	"encoding/json"
	"testing"
	"time"

	"tietie/backend/internal/conversation"
	"tietie/backend/internal/qoder"
)

func TestStreamOriginRequiresMatchingSessionAndTurn(t *testing.T) {
	space := conversation.Context{SessionID: "sess_test", Now: time.Now(), AuthorID: 1, Members: []conversation.Member{{ID: 1, Name: "测试用户"}}}
	input := conversation.EncodeUserV2(space, "明天八点提醒我喝水")
	request := conversation.RequestID(space)
	event := qoder.Event{ID: "evt_00rcspzv620aoe97sqq4", Type: "agent.message", Content: []qoder.ContentBlock{{Type: "text", Text: `{"protocol":"tietie.control","version":2,"requestId":"` + request + `","actions":[{"type":"create_reminder","key":"water","title":"喝水","dueAt":"2026-10-05T08:00:00+08:00","recipientIds":[1],"storage":"database_and_memory"}]}`}}}
	if !streamOriginMatches(input, space.SessionID, event) {
		t.Fatal("the control event should match the exact input captured by the stream")
	}
	if streamOriginMatches(input, "sess_other", event) {
		t.Fatal("an input from another session must use full history verification")
	}
	other := space
	other.Now = other.Now.Add(time.Second)
	if streamOriginMatches(conversation.EncodeUserV2(other, "另一条消息"), space.SessionID, event) {
		t.Fatal("a reply for another turn must use full history verification")
	}
	if streamOriginMatches("用户原话，没有服务端信封", space.SessionID, event) {
		t.Fatal("unwrapped text must not supply control provenance")
	}
	resultInput := conversation.EncodeActionResult(space, request, nil)
	event.Content[0].Text = `{"protocol":"tietie.message","version":2,"requestId":"` + request + `","text":"已设置","recipientIds":[1],"source":"chat"}`
	if !streamOriginMatches(resultInput, space.SessionID, event) {
		t.Fatal("the final reply should match its hidden server receipt")
	}
}

func TestProactiveStreamKindDistinguishesBackgroundNoticesFromReplies(t *testing.T) {
	space := conversation.Context{SessionID: "sess_test", AuthorID: 1, Now: time.Now(), Members: []conversation.Member{{ID: 1, Name: "测试用户"}}}
	encode := func(frame conversation.EnvelopeV2) string {
		frame.Compact = true
		body, err := json.Marshal(frame)
		if err != nil {
			t.Fatal(err)
		}
		return "\n<TIETIE_INPUT_V2>\n" + string(body) + "\n</TIETIE_INPUT_V2>"
	}
	manual := conversation.NewEnvelopeV2(space, "action_result", "manual_restore_rem_1_20261004123001")
	manual.Results = []conversation.ActionResult{{Type: "restore_reminder", Status: "succeeded", ReminderID: "rem_1"}}
	clock := conversation.NewEnvelopeV2(space, "reminder_due", "clock_rem_1")
	clock.Reminder = &conversation.Reminder{ID: "rem_1", Title: "喝水", DueAt: time.Now().Add(time.Hour), RecipientIDs: []int64{1}, Status: "dispatching"}
	chat := conversation.NewEnvelopeV2(space, "user_message", "turn_1_123")
	chat.Text = "你好"
	for _, tc := range []struct {
		name, input, session, want string
	}{
		{"manual update", encode(manual), space.SessionID, "update"},
		{"clock reminder", encode(clock), space.SessionID, "reminder"},
		{"ordinary reply", encode(chat), space.SessionID, ""},
		{"other session", encode(manual), "sess_other", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := proactiveStreamKind(tc.input, tc.session); got != tc.want {
				t.Fatalf("proactiveStreamKind() = %q, want %q", got, tc.want)
			}
		})
	}
}
