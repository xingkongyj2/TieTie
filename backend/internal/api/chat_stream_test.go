package api

import (
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
