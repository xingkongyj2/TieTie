package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"tietie/backend/internal/conversation"
	"tietie/backend/internal/dbop"
	"time"
)

func TestProtocolStateCommitsOnlyOnAcceptanceAndSurvivesRestart(t *testing.T) {
	s, cloud, m, _, call := setupV2(t)
	ctx := context.Background()
	path := "/api/qoder/sessions/sess_shared/messages"
	cloud.reject = 409
	response := call(0, "POST", path, map[string]any{"text": "你好"})
	if response.Code != 409 {
		t.Fatal(response.Code)
	}
	if state, _ := s.DB.GetConversationProtocol(ctx, "sess_shared"); state != nil {
		t.Fatal("rejected bootstrap marked accepted")
	}
	cloud.reject = 0
	if response = call(0, "POST", path, map[string]any{"text": "你好"}); response.Code != 200 {
		t.Fatal(response.Code)
	}
	cloud.mu.Lock()
	first := eventText(cloud.events[len(cloud.events)-1])
	cloud.mu.Unlock()
	if !strings.Contains(first, conversation.TransportInstructions("")) {
		t.Fatal("retry lacks complete bootstrap")
	}
	cursor, err := s.DB.GetConversationProtocol(ctx, "sess_shared")
	if err != nil || cursor == nil {
		t.Fatal(cursor, err)
	}
	m.mu.Lock()
	updates := m.updates
	m.mu.Unlock()
	// Clear all process-local coordination state, retain only the database/cloud.
	restarted := &Server{DB: s.DB, Cfg: s.Cfg, Qoder: s.Qoder}
	space, _, err := restarted.conversationContext(ctx, "sess_shared", 2)
	if err != nil {
		t.Fatal(err)
	}
	frame := conversation.NewEnvelopeV2(space, "user_message", "after_restart")
	frame.Text = "我是另一位成员"
	text, state, err := restarted.prepareProtocolInput(ctx, frame)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, conversation.TransportInstructions("")) {
		t.Fatal("restart repeated full prompt")
	}
	input, ok := conversation.DecodeInput(text)
	if !ok || input.UserID != 2 || input.Text != frame.Text {
		t.Fatal("incremental member identity broken", input, ok)
	}
	restarted.acceptProtocolInput(ctx, state)
	text, _, err = restarted.prepareProtocolInput(ctx, frame)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, `"memoryIndex"`) || strings.Contains(text, `"members"`) || strings.Contains(text, `"timezone"`) {
		t.Fatal("unchanged context repeated", text)
	}
	m.mu.Lock()
	if m.updates != updates {
		t.Error("protocol rewritten every turn")
	}
	m.mu.Unlock()
	// A changed protocol or relationship epoch must establish fresh context.
	cursor.ContractHash = "old_contract"
	if err := s.DB.SaveConversationProtocol(ctx, *cursor); err != nil {
		t.Fatal(err)
	}
	text, _, err = restarted.prepareProtocolInput(ctx, frame)
	if err != nil || !strings.Contains(text, conversation.TransportInstructions("")) {
		t.Fatal("protocol upgrade not initialized", err)
	}
	cursor.ContractHash = conversation.ContractHash("")
	cursor.BindingCreatedAt = cursor.BindingCreatedAt.Add(-time.Second)
	s.DB.SaveConversationProtocol(ctx, *cursor)
	text, _, err = restarted.prepareProtocolInput(ctx, frame)
	if err != nil || !strings.Contains(text, conversation.TransportInstructions("")) {
		t.Fatal("binding epoch reused old context", err)
	}
}

func TestProtocolMemoryFailureRetainsOutboxAndDoesNotBlockMessage(t *testing.T) {
	s, _, m, _, call := setupV2(t)
	m.fail = true
	response := call(0, "POST", "/api/qoder/sessions/sess_shared/messages", map[string]any{"text": "你好"})
	if response.Code != 200 {
		t.Fatal("chat blocked by memory outage", response.Code)
	}
	record, err := s.DB.GetMemoryRecord(context.Background(), dbop.MemoryID("sess_shared", conversation.ProtocolMemoryPath), "sess_shared")
	if err != nil || record == nil || record.State != "pending" {
		t.Fatal("protocol retry not durable", record, err)
	}
	var copy struct {
		Instructions string `json:"instructions"`
	}
	if json.Unmarshal([]byte(record.PendingContent), &copy) != nil || copy.Instructions != conversation.TransportInstructions("") {
		t.Fatal("protocol memory is not authoritative")
	}
}
