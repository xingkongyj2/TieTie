package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"tietie/backend/internal/conversation"
	"tietie/backend/internal/dbop"
	"tietie/backend/internal/memoryspace"
)

func TestFactsUsePackedTemplatePagesAndRecallOnlyRequestedFact(t *testing.T) {
	s, _, cloud, users, call := setupV2(t)
	ctx := context.Background()
	space, binding, err := s.conversationContext(ctx, "sess_shared", users[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	input := conversation.Input{UserID: users[0].ID, Context: space}
	var keys []string
	for i := 0; i < 18; i++ {
		job := dbop.ControlJob{ID: fmt.Sprintf("fact_job_%d", i), SessionID: space.SessionID, RequestID: fmt.Sprintf("fact_request_%d", i), BindingCreatedAt: binding.CreatedAt}
		out := s.executeAction(ctx, job, input, 0, conversation.Action{Type: "save_memory", Key: fmt.Sprintf("hobby_%d", i), Content: fmt.Sprintf("稳定偏好%d", i), Category: "profile", Scope: "self", Storage: "database_and_memory", Data: map[string]string{"hobbies": fmt.Sprintf("爱好%d", i)}}, space)
		if out.Status != "succeeded" {
			t.Fatal(out)
		}
		keys = append(keys, out.MemoryKey)
	}
	// The role-page source shares the same user_profiles schema and target owner.
	response := call(0, "POST", "/api/qoder/sessions/sess_shared/partner-impression", map[string]string{"text": "TA喜欢画画，职业是设计师", "requestId": "template_source_abcdefgh"})
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	cloud.mu.Lock()
	pages, seen := 0, map[string]bool{}
	foundTarget := false
	for _, entry := range cloud.entries {
		if err := memoryspace.ValidateDocument(entry.Path, entry.Content); err != nil {
			t.Fatal(entry.Path, err)
		}
		if !strings.HasPrefix(entry.Path, "profile/users/") {
			continue
		}
		pages++
		items, err := memoryspace.Entries(entry.Content)
		if err != nil || len(items) > 8 {
			t.Fatal(len(items), err)
		}
		for _, raw := range items {
			var f struct {
				Key          string `json:"memoryKey"`
				Owner        int64  `json:"ownerId"`
				SourceType   string `json:"sourceType"`
				Confirmation string `json:"confirmation"`
			}
			json.Unmarshal(raw, &f)
			if seen[f.Key] {
				t.Fatal("duplicate fact across pages")
			}
			seen[f.Key] = true
			if f.SourceType == "role_supplement" {
				foundTarget = f.Owner == users[1].ID && f.Confirmation == "已确认"
			}
		}
	}
	cloud.mu.Unlock()
	if pages != 3 || len(seen) != 19 || !foundTarget {
		t.Fatal("facts were not packed/attributed", pages, len(seen), foundTarget)
	}
	job := dbop.ControlJob{ID: "recall_job", SessionID: space.SessionID, RequestID: "recall_request", BindingCreatedAt: binding.CreatedAt}
	result := s.executeAction(ctx, job, input, 0, conversation.Action{Type: "read_memory", Key: "recall", MemoryKeys: []string{keys[0]}}, space)
	if result.Status != "succeeded" || len(result.Memories) != 1 || !strings.Contains(result.Memories[0].Content, "稳定偏好0") || strings.Contains(result.Memories[0].Content, "稳定偏好1") {
		t.Fatal("recall exposed other page entries", result)
	}
	corrected := s.executeAction(ctx, job, input, 0, conversation.Action{Type: "save_memory", Key: "correct", MemoryKey: keys[0], Content: "更正为游泳", Data: map[string]string{"hobbies": "游泳"}, Category: "profile", Scope: "self", Storage: "database_and_memory"}, space)
	if corrected.Status != "succeeded" {
		t.Fatal(corrected)
	}
	old := s.executeAction(ctx, job, input, 0, conversation.Action{Type: "read_memory", Key: "past", MemoryKeys: []string{keys[0]}, Revision: 1}, space)
	if old.Status != "succeeded" || !strings.Contains(old.Memories[0].Content, "稳定偏好0") {
		t.Fatal("correction lost history", old)
	}
	deleted := s.executeAction(ctx, job, input, 0, conversation.Action{Type: "delete_memory", Key: "forget", MemoryKey: keys[0]}, space)
	if deleted.Status != "succeeded" {
		t.Fatal(deleted)
	}
	other := s.executeAction(ctx, job, input, 0, conversation.Action{Type: "read_memory", Key: "other", MemoryKeys: []string{keys[1]}}, space)
	if other.Status != "succeeded" {
		t.Fatal("deleting one fact deleted its siblings", other)
	}
	forgotten := s.executeAction(ctx, job, input, 0, conversation.Action{Type: "read_memory", Key: "forgotten", MemoryKeys: []string{keys[0]}, Revision: 1}, space)
	if forgotten.Status != "failed" {
		t.Fatal("forgotten history was returned as readable evidence")
	}
}

func TestLegacyCloudOnlyFactMovesIntoTemplateBeforeRetirement(t *testing.T) {
	s, _, cloud, _, _ := setupV2(t)
	ctx := context.Background()
	binding, _ := s.DB.GetBindingBySessionID(ctx, "sess_shared")
	store, err := s.ensureMemoryStore(ctx, "sess_shared")
	if err != nil {
		t.Fatal(err)
	}
	path := "profile/observations/2/1/legacy_cloud_only.json"
	body := `{"content":"TA喜欢陶艺","confirmation":"未经本人确认"}`
	old, err := s.Qoder.UpsertMemory(ctx, store.StoreID, path, body)
	if err != nil {
		t.Fatal(err)
	}
	r := dbop.MemoryRecord{SessionID: "sess_shared", Path: path, Category: "profile", OwnerID: 2, Scope: "space", SourceUserID: 1, Operation: "upsert", BindingCreatedAt: binding.CreatedAt}
	// Reproduce the old database's metadata-only cloud entry.
	if err := s.DB.QueueMemory(ctx, r); err != nil {
		t.Fatal(err)
	}
	record, _ := s.DB.GetMemoryRecord(ctx, dbop.MemoryID(r.SessionID, r.Path), r.SessionID)
	if err := s.DB.FinishMemorySync(ctx, *record, store.StoreID, old.ID); err != nil {
		t.Fatal(err)
	}
	record, _ = s.DB.GetMemoryRecord(ctx, record.ID, r.SessionID)
	if err := s.DB.QueueMemory(ctx, *record); err != nil {
		t.Fatal(err)
	}
	record, _ = s.DB.GetMemoryRecord(ctx, record.ID, r.SessionID)
	if err := s.runMemorySync(ctx, *record); err != nil {
		t.Fatal(err)
	}
	cloud.mu.Lock()
	defer cloud.mu.Unlock()
	found := false
	for _, entry := range cloud.entries {
		if entry.Path == path {
			t.Fatal("legacy standalone file survived")
		}
		if err := memoryspace.ValidateDocument(entry.Path, entry.Content); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(entry.Content, "陶艺") && strings.Contains(entry.Content, `"confirmation":"已确认"`) {
			found = true
		}
	}
	if !found {
		t.Fatal("legacy body/source was lost")
	}
}

func TestWorkerRejectsPathsAndTypesOutsideTemplates(t *testing.T) {
	s, _, cloud, _, _ := setupV2(t)
	binding, _ := s.DB.GetBindingBySessionID(context.Background(), "sess_shared")
	for i, doc := range []struct{ path, body string }{{"profile/new-type.json", `{"type":"new_type"}`}, {"profile/users/2026-10/000001.json", `{"type":"fact","content":"wrong schema"}`}} {
		r := dbop.MemoryRecord{Kind: "projection", SessionID: "sess_shared", Path: doc.path, PendingContent: doc.body, Operation: "upsert", Storage: "database_and_memory", BindingCreatedAt: binding.CreatedAt}
		if err := s.DB.QueueMemory(context.Background(), r); err != nil {
			t.Fatal(err)
		}
		saved, _ := s.DB.GetMemoryRecord(context.Background(), dbop.MemoryID(r.SessionID, r.Path), r.SessionID)
		if err := s.runMemorySync(context.Background(), *saved); err == nil {
			t.Fatal("illegal document was accepted", i)
		}
	}
	cloud.mu.Lock()
	defer cloud.mu.Unlock()
	if len(cloud.entries) != 0 {
		t.Fatal("guard wrote an illegal document")
	}
}
