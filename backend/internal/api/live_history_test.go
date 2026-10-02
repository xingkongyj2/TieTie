//go:build live

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"tietie/backend/internal/conversation"
	"time"

	"tietie/backend/internal/dbop"
	"tietie/backend/internal/memoryspace"
	"tietie/backend/internal/qoder"
)

func TestLiveMonthlyHistoryKeepsFullRecordsAndUpdatesOneCloudPage(t *testing.T) {
	if os.Getenv("QODER_LIVE_TEST") != "1" {
		t.Skip("requires QODER_LIVE_TEST=1 and -tags live")
	}
	cfg := loadLiveTestConfig(t)
	s, _, _, users, _ := setupV2(t)
	s.Cfg, s.Qoder = &cfg, qoder.NewClient(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	owner := fmt.Sprintf("monthly-history-check-%d", time.Now().UnixNano())
	store, err := s.Qoder.CreateMemoryStore(ctx, "TieTie-"+owner+"-memory", owner)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 20*time.Second)
		defer stop()
		if err := s.Qoder.DeleteMemoryStore(cleanup, store.ID); err != nil {
			t.Error("store cleanup", err)
		}
	}()
	if err := s.DB.SaveSpaceMemoryStore(ctx, dbop.SpaceMemoryStore{SessionID: "sess_shared", StoreID: store.ID}); err != nil {
		t.Fatal(err)
	}
	binding, _ := s.DB.GetBindingBySessionID(ctx, "sess_shared")
	if err := s.DB.QueueMemory(ctx, dbop.MemoryRecord{Kind: "template", SessionID: "sess_shared", Path: memoryspace.TodoPath, Scope: "space", Storage: "database_and_memory", PendingContent: memoryspace.TodoTemplate(), Operation: "upsert", BindingCreatedAt: binding.CreatedAt}); err != nil {
		t.Fatal(err)
	}
	var reminders []*dbop.Reminder
	for i := 0; i < 33; i++ {
		r, _, err := s.DB.ApplyReminderAction(ctx, "sess_shared", fmt.Sprintf("live_monthly_%02d", i), 0, dbop.ReminderAction{Type: "create", Title: fmt.Sprintf("隔离验证事项%d", i), DueAt: time.Now().Add(time.Duration(i+1) * time.Hour), RecipientIDs: []int64{users[0].ID, users[1].ID}, CreatedBy: users[0].ID})
		if err != nil {
			t.Fatal(err)
		}
		reminders = append(reminders, r)
	}
	jobs, err := s.DB.ClaimMemorySync(ctx, time.Now().Add(time.Second), 100)
	if err != nil || len(jobs) != 4 {
		t.Fatal("33 reminders should produce 3 template pages and board", len(jobs), err)
	}
	for _, job := range jobs {
		if err := s.runMemorySync(ctx, job); err != nil {
			t.Fatal(err)
		}
	}
	pages := map[string]*qoder.MemoryEntry{}
	seen := map[string]bool{}
	for _, r := range reminders {
		m, err := s.DB.GetReminderMemory(ctx, *r)
		if err != nil {
			t.Fatal(err)
		}
		entry := pages[m.Path]
		if entry == nil {
			entry, err = s.Qoder.GetMemory(ctx, store.ID, m.EntryID)
			if err != nil {
				t.Fatal(err)
			}
			pages[m.Path] = entry
		}
		fact, err := dbop.ExtractReminderMemory(entry.Content, r.ID)
		if err != nil {
			t.Fatal(err)
		}
		var fields struct {
			Title     string    `json:"title"`
			DueAt     time.Time `json:"dueAt"`
			CreatedBy int64     `json:"createdBy"`
		}
		if err := json.Unmarshal([]byte(fact), &fields); err != nil || fields.Title != r.Title || !fields.DueAt.Equal(r.DueAt) || fields.CreatedBy != r.CreatedBy {
			t.Fatal("cloud archive lost original fields", err)
		}
		seen[r.ID] = true
	}
	if len(pages) != 3 || len(seen) != 33 {
		t.Fatal("cloud history was not packed and complete")
	}
	// A record outside both bounded root lists must change exactly one page.
	root, _ := s.DB.GetMemoryRecord(ctx, dbop.MemoryID("sess_shared", memoryspace.TodoPath), "sess_shared")
	var board struct {
		Reminders []struct {
			ID string `json:"id"`
		} `json:"reminders"`
		Recent []struct {
			ID string `json:"id"`
		} `json:"recentReminders"`
	}
	if err := json.Unmarshal([]byte(root.Content), &board); err != nil {
		t.Fatal(err)
	}
	visible := map[string]bool{}
	for _, r := range board.Reminders {
		visible[r.ID] = true
	}
	for _, r := range board.Recent {
		visible[r.ID] = true
	}
	var target *dbop.Reminder
	for _, r := range reminders {
		if !visible[r.ID] {
			target = r
			break
		}
	}
	if target == nil {
		t.Fatal("missing out-of-view fixture")
	}
	if _, err := s.DB.CancelReminder(ctx, "sess_shared", target.ID); err != nil {
		t.Fatal(err)
	}
	jobs, err = s.DB.ClaimMemorySync(ctx, time.Now().Add(time.Second), 100)
	if err != nil || len(jobs) != 1 {
		t.Fatal("old reminder changed unrelated cloud documents", len(jobs), err)
	}
	if err := s.runMemorySync(ctx, jobs[0]); err != nil {
		t.Fatal(err)
	}
	for path, original := range pages {
		entry, err := s.Qoder.GetMemory(ctx, store.ID, original.ID)
		if err != nil {
			t.Fatal(err)
		}
		if path == jobs[0].Path {
			fact, err := dbop.ExtractReminderMemory(entry.Content, target.ID)
			var fields struct {
				Status string `json:"status"`
			}
			if err != nil || json.Unmarshal([]byte(fact), &fields) != nil || fields.Status != dbop.ReminderCancelled {
				t.Fatal("cancelled history not preserved", err)
			}
		} else if entry.Content != original.Content || entry.Version != original.Version {
			t.Fatal("unrelated cloud page was rewritten")
		}
	}
	t.Log("real cloud: 33 complete reminders packed into 3 pages; cancel updated one page and preserved all other pages")
}

func TestLiveFactsAndProtocolReuseSevenTemplates(t *testing.T) {
	if os.Getenv("QODER_LIVE_TEST") != "1" {
		t.Skip("requires QODER_LIVE_TEST=1 and -tags live")
	}
	cfg := loadLiveTestConfig(t)
	s, _, _, users, _ := setupV2(t)
	s.Cfg, s.Qoder = &cfg, qoder.NewClient(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	owner := fmt.Sprintf("template-facts-check-%d", time.Now().UnixNano())
	store, err := s.Qoder.CreateMemoryStore(ctx, "TieTie-"+owner+"-memory", owner)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 20*time.Second)
		defer stop()
		if err := s.Qoder.DeleteMemoryStore(cleanup, store.ID); err != nil {
			t.Error("store cleanup", err)
		}
	}()
	if err := s.DB.SaveSpaceMemoryStore(ctx, dbop.SpaceMemoryStore{SessionID: "sess_shared", StoreID: store.ID, TemplateVersion: memoryspace.Version}); err != nil {
		t.Fatal(err)
	}
	space, binding, err := s.conversationContext(ctx, "sess_shared", users[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	docs, err := memoryspace.Render(space.SessionID, owner, memoryspace.Member{ID: users[0].ID, Name: "fixture A"}, memoryspace.Member{ID: users[1].ID, Name: "fixture B"})
	if err != nil {
		t.Fatal(err)
	}
	for _, doc := range docs {
		if err := s.DB.QueueMemory(ctx, dbop.MemoryRecord{Kind: "template", SessionID: space.SessionID, Path: doc.Path, Scope: "space", Storage: "database_and_memory", PendingContent: doc.Content, Operation: "upsert", BindingCreatedAt: binding.CreatedAt}); err != nil {
			t.Fatal(err)
		}
	}
	jobs, err := s.DB.ClaimMemorySync(ctx, time.Now().Add(time.Second), 100)
	if err != nil || len(jobs) != 7 {
		t.Fatal(err, len(jobs))
	}
	for _, job := range jobs {
		if err := s.runMemorySync(ctx, job); err != nil {
			t.Fatal(err)
		}
	}
	input := conversation.Input{UserID: users[0].ID, Context: space}
	var facts []string
	for i := 0; i < 18; i++ {
		j := dbop.ControlJob{ID: fmt.Sprintf("live_fact_%d", i), SessionID: space.SessionID, RequestID: fmt.Sprintf("live_request_%d", i), BindingCreatedAt: binding.CreatedAt}
		r := s.executeAction(ctx, j, input, 0, conversation.Action{Type: "save_memory", Key: fmt.Sprintf("preference_%d", i), Category: "profile", Scope: "self", Storage: "database_and_memory", Content: fmt.Sprintf("隔离验证的偏好%d", i), Data: map[string]string{"hobbies": fmt.Sprintf("绘画%d", i)}}, space)
		if r.Status != "succeeded" {
			t.Fatal(r.Status, r.ErrorCode)
		}
		facts = append(facts, r.MemoryKey)
	}
	for _, category := range []string{"habit", "agreement", "realtime", "behavior"} {
		a := conversation.Action{Type: "save_memory", Key: "fixture_" + category, Category: category, Scope: "space", Storage: "database_and_memory", Content: "仅用于验证的已确认记录", Data: map[string]string{"content": "仅用于验证的已确认记录"}}
		if category == "realtime" {
			a.ExpiresAt = time.Now().Add(time.Hour).Format(time.RFC3339)
		} else {
			a.Data["confirmation"] = "已确认"
		}
		j := dbop.ControlJob{ID: "live_" + category, SessionID: space.SessionID, RequestID: "live_request_" + category, BindingCreatedAt: binding.CreatedAt}
		r := s.executeAction(ctx, j, input, 0, a, space)
		if r.Status != "succeeded" {
			t.Fatal(category, r.Status, r.ErrorCode)
		}
	}
	e := conversation.NewEnvelopeV2(space, "user_message", "live_protocol")
	if err := s.storeProtocolMemory(ctx, e, binding.CreatedAt); err != nil {
		t.Fatal(err)
	}
	index, err := s.DB.MemoryIndex(ctx, space.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]bool{}
	profilePages := map[string]bool{}
	for _, row := range index {
		full, err := s.DB.GetMemoryRecord(ctx, row.ID, space.SessionID)
		if err != nil || full == nil {
			t.Fatal(err)
		}
		entry, err := s.Qoder.GetMemory(ctx, store.ID, full.EntryID)
		if err != nil {
			t.Fatal(err)
		}
		if err := memoryspace.ValidateDocument(entry.Path, entry.Content); err != nil {
			t.Fatal(entry.Path, err)
		}
		paths[entry.Path] = true
		if row.Kind == "fact" && row.Category == "profile" {
			profilePages[entry.Path] = true
		}
	}
	if len(paths) != 14 || len(profilePages) != 3 {
		t.Fatal("documents were not grouped by the seven templates", len(paths), len(profilePages))
	}
	j := dbop.ControlJob{ID: "live_recall", SessionID: space.SessionID, RequestID: "live_recall_request", BindingCreatedAt: binding.CreatedAt}
	recall := s.executeAction(ctx, j, input, 0, conversation.Action{Type: "read_memory", Key: "read", MemoryKeys: []string{facts[0]}}, space)
	if recall.Status != "succeeded" || len(recall.Memories) != 1 || !strings.Contains(recall.Memories[0].Content, "隔离验证的偏好0") || strings.Contains(recall.Memories[0].Content, "隔离验证的偏好1") {
		t.Fatal("cloud recall did not select one fact")
	}
	corrected := s.executeAction(ctx, j, input, 0, conversation.Action{Type: "save_memory", Key: "correct", MemoryKey: facts[0], Category: "profile", Scope: "self", Storage: "database_and_memory", Content: "隔离验证更正为游泳", Data: map[string]string{"hobbies": "游泳"}}, space)
	if corrected.Status != "succeeded" {
		t.Fatal(corrected.Status)
	}
	recall = s.executeAction(ctx, j, input, 0, conversation.Action{Type: "read_memory", Key: "latest", MemoryKeys: []string{facts[0]}}, space)
	if recall.Status != "succeeded" || !strings.Contains(recall.Memories[0].Content, "游泳") {
		t.Fatal("cloud correction was not persisted")
	}
	root, err := s.Qoder.FindMemoryEntry(ctx, store.ID, memoryspace.BehaviorPath)
	if err != nil || root == nil {
		t.Fatal(err)
	}
	var rules map[string]any
	json.Unmarshal([]byte(root.Content), &rules)
	if rules["instructions"] != conversation.TransportInstructions("") || rules["corePrinciples"] == nil {
		t.Fatal("protocol update lost the original behavior template")
	}
	t.Log("real cloud: seven roots and seven same-schema pages; facts remain packed and individually readable, protocol preserves behavior rules")
}
