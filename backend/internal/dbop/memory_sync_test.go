package dbop

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestCloudMemoryOutboxRevisionRecoveryAndDeletion(t *testing.T) {
	db, _ := openReminderTestDB(t)
	ctx := context.Background()
	binding, err := db.GetBindingByPair(ctx, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	r := MemoryRecord{SessionID: "space", Path: "members/1/facts/tea.json", OwnerID: 1, Scope: "self", Storage: "memory_only", Operation: "upsert", PendingContent: "first", BindingCreatedAt: binding.CreatedAt}
	if err := db.QueueMemory(ctx, r); err != nil {
		t.Fatal(err)
	}
	claimed, err := db.ClaimMemorySync(ctx, time.Now().Add(time.Second), 1)
	if err != nil || len(claimed) != 1 {
		t.Fatal(claimed, err)
	}
	old := claimed[0]
	r.PendingContent = "second"
	if err := db.QueueMemory(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := db.FinishMemorySync(ctx, old, "memstore_test", "mem_old"); err != nil {
		t.Fatal(err)
	}
	latest, err := db.GetMemoryRecord(ctx, old.ID, "space")
	if err != nil || latest.State != "pending" || latest.PendingContent != "second" || latest.Content != "second" || latest.Revision <= old.Revision {
		t.Fatal(latest, err)
	}
	claimed, err = db.ClaimMemorySync(ctx, time.Now().Add(time.Second), 1)
	if err != nil || len(claimed) != 1 {
		t.Fatal(claimed, err)
	}
	if err := db.RecoverMemorySync(ctx); err != nil {
		t.Fatal(err)
	}
	recovered, err := db.ClaimMemorySync(ctx, time.Now().Add(time.Second), 10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, job := range recovered {
		if job.ID == old.ID {
			found = true
			if job.PendingContent != "second" {
				t.Fatal("lost latest pending fact")
			}
		}
		if err := db.FinishMemorySync(ctx, job, "memstore_test", "mem_new"); err != nil {
			t.Fatal(err)
		}
	}
	if !found {
		t.Fatal("pending fact was not recovered")
	}
	latest, _ = db.GetMemoryRecord(ctx, old.ID, "space")
	if latest.State != "synced" || latest.PendingContent != "" || latest.Content != "second" {
		t.Fatal(latest)
	}
	latest.Operation = "delete"
	if err := db.QueueMemory(ctx, *latest); err != nil {
		t.Fatal(err)
	}
	index, err := db.MemoryIndex(ctx, "space")
	if err != nil || len(index) != 0 {
		t.Fatal("pending deletion must disappear from recall index", index, err)
	}
}

func TestMemoryMetadataPaginationIsBoundedAndSpaceScoped(t *testing.T) {
	db, _ := openReminderTestDB(t)
	ctx := context.Background()
	binding, _ := db.GetBindingByPair(ctx, 1, 2)
	for i := 0; i < 137; i++ {
		r := MemoryRecord{SessionID: "space", Path: fmt.Sprintf("profile/habits/1/habit_%03d.json", i), Category: "habit", Scope: "self", OwnerID: 1, Storage: "database_and_memory", PendingContent: "body must not be loaded by metadata queries", Operation: "upsert", BindingCreatedAt: binding.CreatedAt}
		if err := db.QueueMemory(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.QueueMemory(ctx, MemoryRecord{SessionID: "other", Path: "profile/habits/1/secret.json", Category: "habit", Storage: "database_and_memory", PendingContent: "other space", Operation: "upsert"}); err != nil {
		t.Fatal(err)
	}
	first, err := db.ListMemoryIndex(ctx, "space", "habit", "", 100)
	if err != nil || len(first) != 100 {
		t.Fatal(len(first), err)
	}
	second, err := db.ListMemoryIndex(ctx, "space", "habit", first[len(first)-1].ID, 100)
	if err != nil || len(second) != 37 {
		t.Fatal(len(second), err)
	}
	seen := map[string]bool{}
	for _, r := range append(first, second...) {
		if seen[r.ID] || r.Content != "" || r.PendingContent != "" || r.Path == "profile/habits/1/secret.json" {
			t.Fatal("duplicate/body/foreign metadata", r)
		}
		seen[r.ID] = true
	}
	recent, err := db.MemoryIndex(ctx, "space")
	if err != nil || len(recent) != 100 {
		t.Fatal(len(recent), err)
	}
}
