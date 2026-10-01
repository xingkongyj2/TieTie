package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"tietie/backend/internal/auth"
	"tietie/backend/internal/config"
	"tietie/backend/internal/dbop"
	"tietie/backend/internal/qoder"
)

func TestBindRejectsEitherAlreadyBoundUser(t *testing.T) {
	db, err := dbop.Open(filepath.Join(t.TempDir(), "accounts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	users := []*dbop.User{
		{Username: "first", Password: "secret", Code: "1001"},
		{Username: "second", Password: "secret", Code: "1002"},
		{Username: "third", Password: "secret", Code: "1003"},
	}
	for _, user := range users {
		if err := db.CreateUser(ctx, user); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.CreateBinding(ctx, users[0].ID, users[1].ID, "existing-session"); err != nil {
		t.Fatal(err)
	}
	authService := auth.NewService("test-secret", time.Hour)
	router := NewRouter(&Server{Cfg: &config.Config{}, Auth: authService, DB: db})
	for _, test := range []struct {
		self *dbop.User
		code string
		want int
	}{
		{users[0], users[2].Code, http.StatusConflict},
		{users[2], users[1].Code, http.StatusConflict},
		{users[0], users[1].Code, http.StatusOK},
	} {
		token, err := authService.IssueToken(test.self.ID, test.self.Username)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/account/bind", bytes.NewBufferString(`{"code":"`+test.code+`"}`))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		result := httptest.NewRecorder()
		router.ServeHTTP(result, req)
		if result.Code != test.want {
			t.Fatalf("bind %s to %s: got %d, want %d: %s", test.self.Username, test.code, result.Code, test.want, result.Body.String())
		}
	}
}

func TestUnbindClearsBindingForBothUsers(t *testing.T) {
	db, err := dbop.Open(filepath.Join(t.TempDir(), "unbind.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	users := []*dbop.User{
		{Username: "left", Password: "secret", Code: "1011"},
		{Username: "right", Password: "secret", Code: "1012"},
	}
	for _, user := range users {
		if err := db.CreateUser(ctx, user); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.CreateBinding(ctx, users[0].ID, users[1].ID, "shared-session"); err != nil {
		t.Fatal(err)
	}

	authService := auth.NewService("test-secret", time.Hour)
	router := NewRouter(&Server{Cfg: &config.Config{}, Auth: authService, DB: db})
	token, err := authService.IssueToken(users[1].ID, users[1].Username)
	if err != nil {
		t.Fatal(err)
	}
	post := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/account/unbind", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		result := httptest.NewRecorder()
		router.ServeHTTP(result, req)
		return result
	}

	for _, attempt := range []string{"首次退出", "重复退出"} {
		result := post()
		if result.Code != http.StatusOK {
			t.Fatalf("%s: got %d: %s", attempt, result.Code, result.Body.String())
		}
		var body AccountResult
		if err := json.Unmarshal(result.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Binding != nil {
			t.Fatalf("%s: 期望 binding 为空，实际 %+v", attempt, body.Binding)
		}
	}
	// 绑定行由双方共享，解绑后两人都回到未绑定状态。
	for _, user := range users {
		binding, err := db.GetLatestBindingByUser(ctx, user.ID)
		if err != nil {
			t.Fatal(err)
		}
		if binding != nil {
			t.Fatalf("%s 仍有绑定 %+v", user.Username, binding)
		}
	}
}

// TestRebindRestoresSessionByTitle 验证绑定以配对标题为钥匙：
// 云端已有 tietie-<小ID>-<大ID> 的会话时直接恢复（已归档的不算），没有才新建并带上标题。
func TestRebindRestoresSessionByTitle(t *testing.T) {
	db, err := dbop.Open(filepath.Join(t.TempDir(), "rebind.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	var mu sync.Mutex
	var created []map[string]any
	createdBodies := func() []map[string]any {
		mu.Lock()
		defer mu.Unlock()
		return append([]map[string]any(nil), created...)
	}
	upstream := http.NewServeMux()
	upstream.HandleFunc("/api/v1/cloud/sessions", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			mu.Lock()
			created = append(created, body)
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "sess_created_1", "title": body["title"], "status": "idle",
				"created_at": "2026-10-01T00:00:00Z",
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"has_more": false, "data": []map[string]any{
			{"id": "sess_history", "title": "tietie-1-2", "status": "idle", "created_at": "2026-09-01T00:00:00Z"},
			{"id": "sess_history_archived", "title": "tietie-1-2", "status": "idle", "archived_at": "2026-09-06T00:00:00Z", "created_at": "2026-09-05T00:00:00Z"},
			{"id": "sess_unrelated", "title": "其他会话", "status": "idle", "created_at": "2026-09-02T00:00:00Z"},
		}})
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
		{Username: "one", Password: "secret", Code: "1021"},
		{Username: "two", Password: "secret", Code: "1022"},
		{Username: "three", Password: "secret", Code: "1023"},
		{Username: "four", Password: "secret", Code: "1024"},
	}
	for _, user := range users {
		if err := db.CreateUser(ctx, user); err != nil {
			t.Fatal(err)
		}
	}

	bind := func(self *dbop.User, code string) AccountResult {
		token, err := authService.IssueToken(self.ID, self.Username)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/account/bind", bytes.NewBufferString(`{"code":"`+code+`"}`))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		result := httptest.NewRecorder()
		router.ServeHTTP(result, req)
		if result.Code != http.StatusOK {
			t.Fatalf("bind %s: got %d: %s", self.Username, result.Code, result.Body.String())
		}
		var body AccountResult
		if err := json.Unmarshal(result.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body
	}
	unbind := func(self *dbop.User) {
		token, err := authService.IssueToken(self.ID, self.Username)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/account/unbind", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		result := httptest.NewRecorder()
		router.ServeHTTP(result, req)
		if result.Code != http.StatusOK {
			t.Fatalf("unbind %s: got %d: %s", self.Username, result.Code, result.Body.String())
		}
	}

	// 云端已有这对的标题会话（含一个更晚创建的已归档会话）→ 恢复历史，不新建。
	rebound := bind(users[0], users[1].Code)
	if rebound.Binding == nil || rebound.Binding.SessionID != "sess_history" {
		t.Fatalf("期望恢复历史会话，实际 %+v", rebound.Binding)
	}
	if got := createdBodies(); len(got) != 0 {
		t.Fatalf("恢复历史时不应新建会话，实际新建了 %+v", got)
	}

	// 解绑后重新绑定同一人：依旧恢复同一个历史会话。
	unbind(users[1])
	again := bind(users[0], users[1].Code)
	if again.Binding == nil || again.Binding.SessionID != "sess_history" {
		t.Fatalf("重新绑定应恢复历史会话，实际 %+v", again.Binding)
	}
	unbind(users[0])
	if _, err := db.Unbind(ctx, users[1].ID); err != nil {
		t.Fatal(err)
	}

	// 云端没有这对的标题会话 → 新建并带上小 ID 在前的标题。
	fresh := bind(users[2], users[3].Code)
	if fresh.Binding == nil || fresh.Binding.SessionID != "sess_created_1" {
		t.Fatalf("期望新建会话，实际 %+v", fresh.Binding)
	}
	recent := createdBodies()
	if len(recent) != 1 {
		t.Fatalf("期望新建 1 个会话，实际 %+v", recent)
	}
	if got := recent[0]["title"]; got != "tietie-3-4" {
		t.Fatalf("新建会话标题应为 tietie-3-4，实际 %v", got)
	}
}

func TestGeneratedInviteCodeIsFourDigits(t *testing.T) {
	seen := make(map[string]bool, 200)
	for i := 0; i < 200; i++ {
		code, err := generateCode()
		if err != nil {
			t.Fatal(err)
		}
		if len(code) != 4 || strings.TrimLeft(code, "0123456789") != "" {
			t.Fatalf("邀请码应为 4 位数字，实际 %q", code)
		}
		seen[code] = true
	}
	// 4 位数字共 1 万个，200 次生成几乎不该重复；大量重复说明随机数没铺开。
	if len(seen) < 180 {
		t.Fatalf("200 次生成只得到 %d 个不同邀请码", len(seen))
	}
}
