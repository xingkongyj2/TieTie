package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"tietie/backend/internal/config"
	"tietie/backend/internal/dbop"
	"tietie/backend/internal/qoder"
)

type impressionResponse struct {
	Impression   dbop.Impression `json:"impression"`
	MemoryStatus string          `json:"memoryStatus"`
}

func TestPartnerSupplementIsDurableAttributedIdempotentAndIsolated(t *testing.T) {
	s, _, _, users, call := setupV2(t)
	ctx := context.Background()
	path := "/api/qoder/sessions/sess_shared/partner-impression"
	for _, attempt := range []struct {
		user int
		url  string
	}{{2, path}, {0, "/api/qoder/sessions/sess_other/partner-impression"}} {
		if out := call(attempt.user, "GET", attempt.url, nil); out.Code != 403 {
			t.Fatal(out.Code, out.Body.String())
		}
	}
	body := map[string]any{"text": "TA是设计师，喜欢周末骑车，工作日通常十一点睡", "requestId": "observation_123456"}
	for i := 0; i < 2; i++ {
		out := call(0, "POST", path, body)
		if out.Code != 200 {
			t.Fatal(out.Body.String())
		}
		var result impressionResponse
		json.Unmarshal(out.Body.Bytes(), &result)
		if result.Impression.TargetID != users[1].ID || result.MemoryStatus != "synced" {
			t.Fatal(result)
		}
	}
	rows, err := s.DB.ListMemoryIndex(ctx, "sess_shared", "profile", "", 100)
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	record, _ := s.DB.GetMemoryRecord(ctx, rows[0].ID, "sess_shared")
	if record.OwnerID != users[1].ID || record.SourceUserID != users[0].ID || record.Scope != "space" || record.Revision != 1 || !strings.Contains(record.Content, `"confirmation":"已确认"`) || !strings.Contains(record.Content, `"sourceType":"role_supplement"`) {
		t.Fatal(record)
	}
	// Private messages and records must never enter the other member's impression.
	if err := s.DB.SaveMessage(ctx, &dbop.Message{ID: "evt_private", SessionID: "sess_private", UserID: users[1].ID, Sender: "user", Visibility: "private", Text: "private secret"}); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.SaveMessage(ctx, &dbop.Message{ID: "evt_legacy_private", SessionID: "sess_shared", UserID: users[1].ID, Sender: "user", Visibility: "private", Text: "private legacy secret"}); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.SaveMessage(ctx, &dbop.Message{ID: "evt_shared", SessionID: "sess_shared", UserID: users[1].ID, Sender: "user", Text: "我喜欢徒步", CloudCreatedAt: time.Now().Format(time.RFC3339)}); err != nil {
		t.Fatal(err)
	}
	out := call(0, "GET", path, nil)
	if out.Code != 200 {
		t.Fatal(out.Body.String())
	}
	sources, err := s.DB.ImpressionSources(ctx, "sess_shared", users[1].ID)
	if err != nil || len(sources.Messages) != 1 || sources.Messages[0].Text != "我喜欢徒步" {
		t.Fatal(sources, err)
	}
	// Remote failure leaves the durable outbox and returns explicit pending status.
	s.Cfg.CloudMemoryEnabled = false
	pending := call(1, "POST", path, map[string]any{"text": "TA喜欢烹饪", "requestId": "observation_abcdef"})
	var result impressionResponse
	json.Unmarshal(pending.Body.Bytes(), &result)
	if pending.Code != 200 || result.MemoryStatus != "pending" {
		t.Fatal(pending.Body.String())
	}
	rows, _ = s.DB.ListMemoryIndex(ctx, "sess_shared", "profile", "", 100)
	if len(rows) != 2 {
		t.Fatal("lost durable observation")
	}
}

func TestImpressionUsesRealSourcesInSeparateCloudSessionAndCaches(t *testing.T) {
	s, _, _, users, call := setupV2(t)
	ctx := context.Background()
	path := "/api/qoder/sessions/sess_shared/partner-impression"
	out := call(0, "POST", path, map[string]any{"text": "TA是设计师，喜欢周末骑车", "requestId": "source_abcdefgh"})
	if out.Code != 200 {
		t.Fatal(out.Body.String())
	}
	binding, _ := s.DB.GetBindingBySessionID(ctx, "sess_shared")
	// Supplements saved before the confirmation policy changed remain usable.
	legacy, err := s.DB.ApplyMemoryAction(ctx, "legacy_role_supplement", dbop.MemoryRecord{SessionID: "sess_shared", Path: "profile/observations/2/1/legacy_abcdefgh.json", Category: "profile", Scope: "space", OwnerID: users[1].ID, SourceUserID: users[0].ID, Storage: "database_and_memory", PendingContent: `{"content":"TA喜欢绘画","confirmation":"由另一成员补充，未经本人确认"}`, Operation: "upsert", BindingCreatedAt: binding.CreatedAt})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.syncMemoryLocked(ctx, *legacy); err != nil {
		t.Fatal(err)
	}
	selfFact := `{"schemaVersion":1,"kind":"fact","category":"habit","ownerId":2,"sourceUserId":2,"content":"我喜欢徒步","confirmation":"本人确认"}`
	fact, err := s.DB.ApplyMemoryAction(ctx, "portrait_self_fact", dbop.MemoryRecord{SessionID: "sess_shared", Path: "profile/habits/2/hiking.json", Category: "habit", Scope: "self", OwnerID: users[1].ID, SourceUserID: users[1].ID, Storage: "memory_only", PendingContent: selfFact, Operation: "upsert", BindingCreatedAt: binding.CreatedAt})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.syncMemoryLocked(ctx, *fact); err != nil {
		t.Fatal(err)
	}
	fact, _ = s.DB.GetMemoryRecord(ctx, fact.ID, "sess_shared")
	if fact.Content == "" || fact.PendingContent != "" || fact.EntryID == "" {
		t.Fatal("legacy memory-only source must be retained for template aggregation")
	}
	var mu sync.Mutex
	prompt := ""
	deleted := false
	posts := 0
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/memory_stores/"+fact.StoreID+"/memories/"+fact.EntryID:
			json.NewEncoder(w).Encode(qoder.MemoryEntry{ID: fact.EntryID, Path: fact.Path, Content: selfFact})
		case r.URL.Path == "/sessions" && r.Method == "POST":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if body["resources"] != nil {
				t.Error("portrait must not mount private or broad stores")
			}
			json.NewEncoder(w).Encode(map[string]any{"id": "sess_portrait", "status": "idle"})
		case r.URL.Path == "/sessions/sess_portrait/events" && r.Method == "POST":
			var body struct {
				Events []qoder.Event `json:"events"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			prompt = eventText(body.Events[0])
			posts++
			json.NewEncoder(w).Encode(map[string]any{"data": []qoder.Event{{ID: "evt_input", Type: "user.message"}}})
		case r.URL.Path == "/sessions/sess_portrait/events":
			json.NewEncoder(w).Encode(map[string]any{"data": []qoder.Event{{ID: "evt_summary", Type: "agent.message", Content: []qoder.ContentBlock{{Type: "text", Text: `{"summary":"你提到 TA 从事设计工作，周末喜欢骑车。我会把这些小习惯慢慢记住。"}`}}}}, "has_more": false})
		case r.URL.Path == "/sessions/sess_portrait" && r.Method == "DELETE":
			deleted = true
			w.WriteHeader(204)
		case r.URL.Path == "/sessions/sess_portrait":
			json.NewEncoder(w).Encode(map[string]any{"id": "sess_portrait", "status": "idle"})
		default:
			t.Error("unexpected portrait cloud request", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer cloud.Close()
	s.Cfg.AgentID = "agent_test"
	s.Cfg.EnvironmentID = "env_test"
	s.Qoder = qoder.NewClient(config.Config{Upstream: cloud.URL, Token: "fake", Timeout: time.Second})
	if out := call(0, "GET", path, nil); out.Code != 200 {
		t.Fatal(out.Body.String())
	}
	jobs, err := s.DB.ClaimImpressions(ctx, time.Now().Add(time.Second), 4)
	if err != nil || len(jobs) != 1 {
		t.Fatal(jobs, err)
	}
	if err := s.runImpression(ctx, jobs[0]); err != nil {
		t.Fatal(err)
	}
	row, _ := s.DB.GetImpression(ctx, "sess_shared", users[1].ID)
	if row.Status != "ready" || !strings.Contains(row.Summary, "设计") {
		t.Fatal(row)
	}
	mu.Lock()
	defer mu.Unlock()
	if !deleted || posts != 1 || !strings.Contains(prompt, `"confirmation":"已确认"`) || !strings.Contains(prompt, `"sourceType":"role_supplement"`) || !strings.Contains(prompt, "设计师") || !strings.Contains(prompt, "我喜欢徒步") {
		t.Fatal("missing real evidence or cloud cleanup")
	}
	if !strings.Contains(prompt, `"viewerUserId":1`) || !strings.Contains(prompt, `"targetUserId":2`) {
		t.Fatal("missing explicit viewer and target identities")
	}
	if strings.Contains(prompt, `"confirmation":"由另一成员补充，未经本人确认"`) || !strings.Contains(prompt, `"content":"TA喜欢绘画"`) {
		t.Fatal("legacy role supplement was not normalized as confirmed")
	}
	if out := call(0, "GET", path, nil); out.Code != 200 {
		t.Fatal(out.Body.String())
	}
	more, _ := s.DB.ClaimImpressions(ctx, time.Now().Add(time.Second), 4)
	if len(more) != 0 {
		t.Fatal("cached impression regenerated without new sources")
	}
}

func TestImpressionRegeneratesOldPerspectiveWithoutNewFacts(t *testing.T) {
	s, _, _, users, call := setupV2(t)
	ctx := context.Background()
	sources, err := s.DB.ImpressionSources(ctx, "sess_shared", users[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.EnsureImpression(ctx, "sess_shared", users[1].ID, sources.Hash(), false); err != nil {
		t.Fatal(err)
	}
	jobs, err := s.DB.ClaimImpressions(ctx, time.Now().Add(time.Second), 1)
	if err != nil || len(jobs) != 1 {
		t.Fatal(jobs, err)
	}
	if err := s.DB.FinishImpression(ctx, jobs[0], "关于你，我了解得很少。", ""); err != nil {
		t.Fatal(err)
	}
	out := call(0, "GET", "/api/qoder/sessions/sess_shared/partner-impression", nil)
	var result impressionResponse
	if err := json.Unmarshal(out.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if out.Code != 200 || result.Impression.Status != "pending" || result.Impression.Summary != "" {
		t.Fatal(out.Body.String())
	}
}
