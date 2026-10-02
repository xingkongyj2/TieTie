package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"tietie/backend/internal/auth"
	"tietie/backend/internal/config"
	"tietie/backend/internal/dbop"
	"tietie/backend/internal/memoryspace"
	"tietie/backend/internal/qoder"
	"time"
)

// Isolated fake cloud proves distinct stores, complete seeding, failed
// initialization rollback and cleanup without touching any real cloud account.
func TestNewBindingsInitializeIsolatedJSONMemorySpaces(t *testing.T) {
	db, err := dbop.Open(filepath.Join(t.TempDir(), "init.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	users := []*dbop.User{{Username: "one", Password: "secret", Code: "4011"}, {Username: "two", Password: "secret", Code: "4012"}}
	for _, u := range users {
		if err := db.CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	var mu sync.Mutex
	stores := map[string]*fakeMemoryAPI{}
	sessions := map[string]string{}
	created, deletedStores, deletedSessions, historyRequests := 0, 0, 0, 0
	failSeed, failRename := false, false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/memory_stores" && r.Method == "POST":
			var store qoder.MemoryStore
			json.NewDecoder(r.Body).Decode(&store)
			store.ID = fmt.Sprintf("memstore_%d", created)
			store.Status = "active"
			stores[store.ID] = &fakeMemoryAPI{store: &store, entries: map[string]qoder.MemoryEntry{}}
			json.NewEncoder(w).Encode(store)
		case strings.HasPrefix(r.URL.Path, "/memory_stores/"):
			parts := strings.Split(r.URL.Path, "/")
			m := stores[parts[2]]
			if len(parts) == 3 && r.Method == "DELETE" {
				delete(stores, parts[2])
				deletedStores++
				w.WriteHeader(204)
				return
			}
			if failSeed && len(parts) == 4 && r.Method == "POST" {
				w.WriteHeader(503)
				return
			}
			if failRename && len(parts) == 3 && r.Method == "POST" {
				w.WriteHeader(503)
				return
			}
			if m == nil {
				w.WriteHeader(404)
				return
			}
			m.ServeHTTP(w, r)
		case r.URL.Path == "/sessions" && r.Method == "POST":
			var body struct {
				Title     string `json:"title"`
				Resources []struct {
					ID     string `json:"memory_store_id"`
					Access string `json:"access"`
				} `json:"resources"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			if len(body.Resources) != 1 || body.Resources[0].Access != "read_only" {
				t.Error("missing memory mount")
			}
			created++
			id := fmt.Sprintf("sess_created_%d", created)
			sessions[id] = body.Resources[0].ID
			json.NewEncoder(w).Encode(map[string]any{"id": id, "title": body.Title, "status": "idle"})
		case r.URL.Path == "/sessions":
			historyRequests++
			json.NewEncoder(w).Encode(map[string]any{"data": []any{}, "has_more": false})
		case strings.HasPrefix(r.URL.Path, "/sessions/") && r.Method == "DELETE":
			delete(sessions, strings.TrimPrefix(r.URL.Path, "/sessions/"))
			deletedSessions++
			w.WriteHeader(204)
		default:
			w.WriteHeader(404)
		}
	}))
	defer upstream.Close()
	authService := auth.NewService("secret", time.Hour)
	cfg := config.Config{AgentID: "agent_1", EnvironmentID: "env_1", CloudMemoryEnabled: true, ConversationProtocolVersion: 2, Upstream: upstream.URL, Token: "test", Timeout: time.Second}
	router := NewRouter(&Server{Cfg: &cfg, DB: db, Auth: authService, Qoder: qoder.NewClient(cfg)})
	call := func(method, path string, user *dbop.User, body string) *httptest.ResponseRecorder {
		token, _ := authService.IssueToken(user.ID, user.Username)
		req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		req.Header.Set("Authorization", "Bearer "+token)
		out := httptest.NewRecorder()
		router.ServeHTTP(out, req)
		return out
	}
	bind := func() string {
		out := call("POST", "/api/account/bind", users[1], `{"code":"4011"}`)
		if out.Code != 200 {
			t.Fatal(out.Body.String())
		}
		var result AccountResult
		json.Unmarshal(out.Body.Bytes(), &result)
		return result.Binding.SessionID
	}
	first := bind()
	mapping, _ := db.GetSpaceMemoryStore(ctx, first)
	if mapping == nil || !mapping.NativeMounted || mapping.TemplateVersion != memoryspace.Version {
		t.Fatal(mapping)
	}
	index, _ := db.MemoryIndex(ctx, first)
	if len(index) != 7 {
		t.Fatal("initial templates missing", index)
	}
	mu.Lock()
	if store := stores[mapping.StoreID].store; store.Name != qoder.MemoryStoreName(first) || store.Metadata["tietie_owner"] != mapping.SpaceID {
		t.Fatal("missing session name or changed ownership", store)
	}
	for _, entry := range stores[mapping.StoreID].entries {
		if !strings.HasSuffix(entry.Path, ".json") || !json.Valid([]byte(entry.Content)) || strings.Contains(entry.Content, "{{") {
			t.Fatal("invalid template", entry)
		}
		if entry.Path == "agreements/shared.json" && !strings.Contains(entry.Content, first) {
			t.Fatal("wrong session identity")
		}
		if entry.Path == "profile/users.json" && !strings.Contains(entry.Content, `"userId": 1`) {
			t.Fatal("members not ordered by real ID")
		}
	}
	mu.Unlock()
	if again := bind(); again != first {
		t.Fatal("active bind should be idempotent")
	}
	if out := call("POST", "/api/account/unbind", users[0], ""); out.Code != 200 {
		t.Fatal(out.Body.String())
	}
	if out := call("PUT", "/api/account/profile", users[0], `{"gender":"female","birthday":"1998-11-16","hobbies":["画画","骑车"],"bio":"喜欢慢慢逛博物馆","avatar":"/avatars/cream-cat.png"}`); out.Code != 200 {
		t.Fatal(out.Body.String())
	}
	if out := call("PUT", "/api/account/profile", users[1], `{"gender":"male","birthday":"1999-06-08","hobbies":["烹饪"],"bio":"","avatar":"/avatars/peach-cat.png"}`); out.Code != 200 {
		t.Fatal(out.Body.String())
	}
	second := bind()
	secondStore, _ := db.GetSpaceMemoryStore(ctx, second)
	if second == first || secondStore.StoreID == mapping.StoreID || secondStore.SpaceID == mapping.SpaceID {
		t.Fatal("rebind reused memory space")
	}
	mu.Lock()
	for _, entry := range stores[secondStore.StoreID].entries {
		if entry.Path != "profile/users.json" {
			continue
		}
		var doc struct {
			Users []struct {
				UserID   int64 `json:"userId"`
				Birthday string
				Hobbies  []string
				Bio      string
			}
		}
		if err := json.Unmarshal([]byte(entry.Content), &doc); err != nil {
			t.Fatal(err)
		}
		if len(doc.Users) != 2 || doc.Users[0].UserID != users[0].ID || doc.Users[0].Birthday != "1998-11-16" || len(doc.Users[0].Hobbies) != 2 || doc.Users[0].Bio != "喜欢慢慢逛博物馆" || doc.Users[1].Birthday != "1999-06-08" || len(doc.Users[1].Hobbies) != 1 || doc.Users[1].Hobbies[0] != "烹饪" {
			t.Fatal("initialization omitted or mixed account profiles", doc)
		}
	}
	mu.Unlock()
	for _, user := range users {
		key := dbop.MemoryID(second, memoryspace.FactPath("profile", "self", user.ID, "account_profile"))
		fact, err := db.GetMemoryRecord(ctx, key, second)
		if err != nil || fact == nil || fact.SourceUserID != user.ID || fact.OwnerID != user.ID || !strings.Contains(fact.Content, `"sourceType":"self_profile"`) {
			t.Fatal("profile missing from initialized memory index", fact, err)
		}
	}
	if out := call("GET", "/api/qoder/sessions/"+first+"/memories", users[0], ""); out.Code != 403 {
		t.Fatal("old space still accessible")
	}
	if out := call("POST", "/api/account/unbind", users[0], ""); out.Code != 200 {
		t.Fatal(out.Body.String())
	}
	mu.Lock()
	failSeed = true
	mu.Unlock()
	failed := call("POST", "/api/account/bind", users[0], `{"code":"4012"}`)
	if failed.Code == 200 {
		t.Fatal("partial seed was exposed")
	}
	binding, _ := db.GetLatestBindingByUser(ctx, users[0].ID)
	if binding != nil {
		t.Fatal("failed seed published binding")
	}
	mu.Lock()
	failSeed, failRename = false, true
	mu.Unlock()
	failed = call("POST", "/api/account/bind", users[0], `{"code":"4012"}`)
	if failed.Code == 200 {
		t.Fatal("rename failure was exposed")
	}
	binding, _ = db.GetLatestBindingByUser(ctx, users[0].ID)
	if binding != nil {
		t.Fatal("failed rename published binding")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(stores) != 2 || len(sessions) != 2 || deletedStores != 2 || deletedSessions != 1 || historyRequests != 0 {
		t.Fatalf("incorrect cleanup/history use: stores=%d sessions=%d deleted=%d/%d history=%d", len(stores), len(sessions), deletedStores, deletedSessions, historyRequests)
	}
}
