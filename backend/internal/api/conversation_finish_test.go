package api

import (
	"testing"
	"time"

	"tietie/backend/internal/conversation"
	"tietie/backend/internal/qoder"
)

func TestConversationFinishedAllowsDatabaseMicrosecondRounding(t *testing.T) {
	now := time.Date(2026, 10, 4, 7, 47, 56, 563532634, time.FixedZone("CST", 8*60*60))
	result := &qoder.MessagesResult{
		Session: &qoder.PublicSession{Status: "idle"},
		Events:  []qoder.Event{{Type: "session.status_idle"}},
	}
	prior := conversation.EncodeActionResult(conversation.Context{
		Now: now, SessionID: "sess_test", AuthorID: 1,
		Members: []conversation.Member{{ID: 1, Name: "测试用户"}},
	}, "turn_test", nil)
	if !conversationFinishedFrom(result, now.Add(366*time.Nanosecond), prior) {
		t.Fatal("an idle reply should finish when MySQL rounded pending_since up by one microsecond")
	}
	if conversationFinishedFrom(result, now.Add(time.Second), prior) {
		t.Fatal("an older idle reply must not finish a later turn")
	}
}
