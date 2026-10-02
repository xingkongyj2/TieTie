package dbop

import (
	"context"
	"encoding/json"
	"testing"
	"tietie/backend/internal/memoryspace"
	"time"
)

func TestTemplateUpgradeKeepsFactsAndRetiresOnlyReplacedDocuments(t *testing.T) {
	db, path := openReminderTestDB(t)
	ctx := context.Background()
	binding, _ := db.GetBindingBySessionID(ctx, "space")
	store := SpaceMemoryStore{SessionID: "space", StoreID: "memstore_existing", TemplateVersion: 1, NativeMounted: true}
	if err := db.SaveSpaceMemoryStore(ctx, store); err != nil {
		t.Fatal(err)
	}
	reminder := makeReminder(t, db, "legacy_template_reminder", 0, time.Now().Add(time.Hour), 1, 2)
	oldPage := MemoryRecord{Kind: "projection", SessionID: "space", Path: "tasks/history/2026-10/000001.json", Storage: "database_and_memory", Operation: "upsert", PendingContent: `{"type":"reminder_history_page","reminders":[]}`, BindingCreatedAt: binding.CreatedAt}
	if err := db.QueueMemory(ctx, oldPage); err != nil {
		t.Fatal(err)
	}
	protocol := oldPage
	protocol.Kind = "template"
	protocol.Path = "rules/conversation-protocol.json"
	protocol.PendingContent = `{"type":"conversation_protocol","instructions":"old rules"}`
	if err := db.QueueMemory(ctx, protocol); err != nil {
		t.Fatal(err)
	}
	fact := MemoryRecord{SessionID: "space", Path: "profile/observations/2/1/legacy.json", Category: "profile", OwnerID: 2, Scope: "space", Operation: "upsert", Storage: "database_and_memory", PendingContent: `{"content":"TA爱骑车","sourceType":"role_supplement","confirmation":"已确认"}`, BindingCreatedAt: binding.CreatedAt}
	if err := db.QueueMemory(ctx, fact); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	mapping, _ := reopened.GetSpaceMemoryStore(ctx, "space")
	if mapping.StoreID != store.StoreID || !mapping.NativeMounted || mapping.TemplateVersion != memoryspace.Version {
		t.Fatal("upgrade replaced original store", mapping)
	}
	var templates []MemoryRecord
	reopened.gdb.Where("session_id=? AND kind='template' AND operation='upsert'", "space").Find(&templates)
	if len(templates) != 7 {
		t.Fatal("upgrade did not restore seven root schemas", len(templates))
	}
	for _, doc := range templates {
		if err := memoryspace.ValidateDocument(doc.Path, doc.Content); err != nil {
			t.Fatal(doc.Path, err)
		}
	}
	page, err := reopened.GetReminderMemory(ctx, *reminder)
	if err != nil || page == nil {
		t.Fatal(err)
	}
	if err := memoryspace.ValidateDocument(page.Path, page.Content); err != nil {
		t.Fatal(err)
	}
	if _, err := ExtractReminderMemory(page.Content, reminder.ID); err != nil {
		t.Fatal("lost reminder", err)
	}
	retained, _ := reopened.GetMemoryRecord(ctx, MemoryID("space", fact.Path), "space")
	if retained.Content == "" || retained.Operation != "upsert" {
		t.Fatal("lost profile fact")
	}
	for _, old := range []MemoryRecord{oldPage, protocol} {
		row, _ := reopened.GetMemoryRecord(ctx, MemoryID("space", old.Path), "space")
		if !row.ArchiveRetirement || row.Operation != "delete" || row.Content == "" {
			t.Fatal("old document discarded before replacement", old.Path)
		}
	}
	var count int64
	reopened.gdb.Model(&Reminder{}).Count(&count)
	if count != 1 {
		t.Fatal("migration changed history")
	}
	var root struct {
		History struct {
			Months []json.RawMessage `json:"months"`
		} `json:"history"`
	}
	for _, doc := range templates {
		if doc.Path == memoryspace.TodoPath {
			json.Unmarshal([]byte(doc.Content), &root)
		}
	}
	if len(root.History.Months) != 1 {
		t.Fatal("month index was not moved into root")
	}
}

func TestFactPageByteLimitRelocationAndCorrectionHistory(t *testing.T) {
	db, _ := openReminderTestDB(t)
	ctx := context.Background()
	binding, _ := db.GetBindingBySessionID(ctx, "space")
	var facts []MemoryRecord
	for i := 0; i < 3; i++ {
		content := make([]byte, 18*1024)
		for j := range content {
			content[j] = 'a' + byte(i)
		}
		body, _ := json.Marshal(map[string]any{"content": string(content)})
		r := MemoryRecord{SessionID: "space", Path: memoryspace.FactPath("habit", "self", 1, string(rune('a'+i))), Category: "habit", OwnerID: 1, Scope: "self", Storage: "database_and_memory", Operation: "upsert", PendingContent: string(body), BindingCreatedAt: binding.CreatedAt}
		if err := db.QueueMemory(ctx, r); err != nil {
			t.Fatal(err)
		}
		full, _ := db.GetMemoryRecord(ctx, MemoryID(r.SessionID, r.Path), r.SessionID)
		facts = append(facts, *full)
	}
	// Growing one member cannot overflow or rewrite the unrelated page payload.
	firstPage := facts[0].CloudPath
	content := make([]byte, 48*1024)
	for i := range content {
		content[i] = 'd'
	}
	body, _ := json.Marshal(map[string]any{"content": string(content)})
	corrected := facts[0]
	corrected.PendingContent = string(body)
	if err := db.QueueMemory(ctx, corrected); err != nil {
		t.Fatal(err)
	}
	latest, _ := db.GetMemoryRecord(ctx, corrected.ID, "space")
	if latest.CloudPath == firstPage {
		t.Fatal("oversized correction was not moved")
	}
	old, _ := db.GetMemoryRevision(ctx, "space", corrected.ID, 1)
	if old == nil || old.Content != facts[0].Content {
		t.Fatal("correction lost original history")
	}
	var pages []MemoryRecord
	db.gdb.Where("session_id=? AND kind='projection'", "space").Find(&pages)
	seen := map[string]bool{}
	for _, p := range pages {
		if len(p.Content) > 96*1024 {
			t.Fatal("page exceeded byte limit")
		}
		if err := memoryspace.ValidateDocument(p.Path, p.Content); err != nil {
			t.Fatal(err)
		}
		entries, _ := memoryspace.Entries(p.Content)
		for _, entry := range entries {
			var key struct {
				ID string `json:"memoryKey"`
			}
			json.Unmarshal(entry, &key)
			if seen[key.ID] {
				t.Fatal("relocation duplicated current facts")
			}
			seen[key.ID] = true
		}
	}
	if len(seen) != 3 {
		t.Fatal("relocation dropped siblings")
	}
}
