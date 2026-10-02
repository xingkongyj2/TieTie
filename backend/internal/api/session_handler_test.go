package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"tietie/backend/internal/auth"
	"tietie/backend/internal/config"
	"tietie/backend/internal/conversation"
	"tietie/backend/internal/dbop"
	"tietie/backend/internal/qoder"
)

// Re-reading cloud history preserves original text and attachment names once.
func TestRecordMessageRetainsFilesSeparatelyFromMemberText(t *testing.T) {
	s, _, users, _ := setupConversation(t)
	ctx := context.Background()
	msg := qoder.PublicMessage{ID: "evt_uploaded_schedule", Sender: "self", UserID: users[0].ID, Text: "帮我记住张梦妍排班", Files: []string{"排班表(1).xlsx"}}
	s.recordMessages(ctx, "sess_shared", []qoder.PublicMessage{msg})
	s.recordMessages(ctx, "sess_shared", []qoder.PublicMessage{msg})
	rows, err := s.DB.ListMessages(ctx, "sess_shared", 10)
	if err != nil || len(rows) != 1 || rows[0].Text != msg.Text || len(rows[0].Files) != 1 || rows[0].Files[0] != msg.Files[0] {
		t.Fatalf("attachment identity or original text lost during persistence: %+v %v", rows, err)
	}
}

// TestToolResultRelay verifies authenticated tool-result forwarding.
func TestToolResultRelay(t *testing.T) {
	db, err := dbop.Open(filepath.Join(t.TempDir(), "tool-result.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()

	var mu sync.Mutex
	var events []map[string]any
	upstream := http.NewServeMux()
	upstream.HandleFunc("/api/v1/cloud/sessions", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "sess_tool_1", "title": "TieTie-1-2", "status": "idle"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"has_more": false, "data": []map[string]any{}})
	})
	upstream.HandleFunc("/api/v1/cloud/sessions/sess_tool_1/events", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "evt_ask_1", "type": "agent.custom_tool_use", "name": "AskUserQuestion", "input": map[string]any{"questions": []map[string]any{{"question": "选一个", "options": []map[string]string{{"label": "继续"}}}}}}}, "has_more": false})
			return
		}

		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		events = append(events, body)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{}})
	})
	upstream.HandleFunc("/api/v1/cloud/sessions/sess_tool_1", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"id": "sess_tool_1", "status": "idle"})
	})
	server := httptest.NewServer(upstream)
	defer server.Close()

	authService := auth.NewService("test-secret", time.Hour)
	router := NewRouter(&Server{
		Cfg:   &config.Config{AgentID: "agent_1", EnvironmentID: "env_1"},
		Auth:  authService,
		DB:    db,
		Qoder: qoder.NewClient(config.Config{Upstream: server.URL + "/api/v1/cloud", Token: "test-token", Timeout: 5 * time.Second}),
	})
	users := []*dbop.User{
		{Username: "asker", Password: "secret", Code: "2201"},
		{Username: "asked", Password: "secret", Code: "2202"},
	}
	for _, user := range users {
		if err := db.CreateUser(ctx, user); err != nil {
			t.Fatal(err)
		}
	}
	token, err := authService.IssueToken(users[0].ID, users[0].Username)
	if err != nil {
		t.Fatal(err)
	}
	call := func(path, payload string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(payload))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		result := httptest.NewRecorder()
		router.ServeHTTP(result, req)
		return result
	}
	if _, err := db.CreateBinding(ctx, users[0].ID, users[1].ID, "sess_tool_1"); err != nil {
		t.Fatal(err)
	}

	ok := call("/api/qoder/sessions/sess_tool_1/tool-result", `{"toolUseId":"evt_ask_1","text":"就发这条，不用时间"}`)
	if ok.Code != http.StatusOK {
		t.Fatalf("应答应成功: got %d: %s", ok.Code, ok.Body.String())
	}
	mu.Lock()
	postings := append([]map[string]any(nil), events...)
	mu.Unlock()
	if len(postings) != 1 {
		t.Fatalf("期望转发 1 次事件，实际 %+v", postings)
	}
	raw, _ := json.Marshal(postings[0])
	var sent struct {
		Events []qoder.Event `json:"events"`
	}
	if err := json.Unmarshal(raw, &sent); err != nil || len(sent.Events) != 1 {
		t.Fatalf("invalid events: %s", raw)
	}
	event := sent.Events[0]
	if event.Type != "user.custom_tool_result" || event.CustomToolUseID != "evt_ask_1" || len(event.Content) != 1 {
		t.Fatalf("wrong tool result structure")
	}
	input, decoded := conversation.DecodeInput(event.Content[0].Text)
	if !decoded || input.Text != "就发这条，不用时间" || input.UserID != users[0].ID || len(input.Context.Members) != 2 {
		t.Fatalf("answer must retain real member identity and original text")
	}

	for _, bad := range []struct{ name, payload string }{
		{"工具调用 ID 非法", `{"toolUseId":"nope","text":"随便"}`},
		{"答案为空", `{"toolUseId":"evt_ask_1","text":"   "}`},
	} {
		if got := call("/api/qoder/sessions/sess_tool_1/tool-result", bad.payload); got.Code != http.StatusBadRequest {
			t.Fatalf("%s: got %d, want 400: %s", bad.name, got.Code, got.Body.String())
		}
	}
	if got := call("/api/qoder/sessions/sess_someone_else/tool-result", `{"toolUseId":"evt_ask_1","text":"随便"}`); got.Code != http.StatusForbidden {
		t.Fatalf("非绑定会话应 403: got %d: %s", got.Code, got.Body.String())
	}
}
