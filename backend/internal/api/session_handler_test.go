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
	"tietie/backend/internal/dbop"
	"tietie/backend/internal/qoder"
)

// TestToolResultRelay 验证把用户对选择题的应答按云端要求的结构转发出去，并守住鉴权与入参校验。
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
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "sess_tool_1", "title": "tietie-1-2", "status": "idle"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"has_more": false, "data": []map[string]any{}})
	})
	upstream.HandleFunc("/api/v1/cloud/sessions/sess_tool_1/events", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		events = append(events, body)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{}})
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
	if got := call("/api/account/bind", `{"code":"2202"}`); got.Code != http.StatusOK {
		t.Fatalf("bind: got %d: %s", got.Code, got.Body.String())
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
	want := `{"events":[{"content":[{"text":"就发这条，不用时间","type":"text"}],"custom_tool_use_id":"evt_ask_1","type":"user.custom_tool_result"}]}`
	if string(raw) != want {
		t.Fatalf("转发结构不对:\n got %s\nwant %s", raw, want)
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
