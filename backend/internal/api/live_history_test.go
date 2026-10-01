//go:build live

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
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
