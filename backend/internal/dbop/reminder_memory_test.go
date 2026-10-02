package dbop

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestReminderDeliveryCompletesTaskAndMemoryAtomically(t *testing.T) {
	db, path := openReminderTestDB(t)
	ctx := context.Background()
	now := time.Now()
	r := makeReminder(t, db, "request", 0, now.Add(-time.Second), 1, 2)
	memories, err := db.ListReminderMemories(ctx, "space")
	if err != nil || len(memories) != 1 || memories[0].TaskStatus != "pending" {
		t.Fatalf("initial memory %+v %v", memories, err)
	}
	if _, err := db.ClaimDueReminder(ctx, now); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordReminderDispatch(ctx, r.ID, []string{"evt_wake"}); err != nil {
		t.Fatal(err)
	}
	if err := db.gdb.Exec(`CREATE TRIGGER reject_memory BEFORE UPDATE ON reminder_memories BEGIN SELECT RAISE(ABORT,'memory blocked'); END`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.FinishReminderDispatch(ctx, r.ID, []string{"evt_wake", "evt_ai"}, now); err == nil {
		t.Fatal("memory failure accepted")
	}
	current, _ := db.GetReminder(ctx, "space", r.ID)
	if current.Status != ReminderDispatching || current.TaskCompletedAt != nil {
		t.Fatalf("non atomic delivery %+v", current)
	}
	db.gdb.Exec(`DROP TRIGGER reject_memory`)
	if err := db.FinishReminderDispatch(ctx, r.ID, []string{"evt_wake", "evt_ai"}, now); err != nil {
		t.Fatal(err)
	}
	memories, _ = db.ListReminderMemories(ctx, "space")
	if memories[0].TaskStatus != "completed" || memories[0].Status != ReminderDelivered || memories[0].DeliveredAt == nil || memories[0].CompletedBy != nil {
		t.Fatal(memories)
	}
	if err := db.FinishReminderDispatch(ctx, r.ID, []string{"evt_replay"}, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	db.Close()
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	current, _ = reopened.GetReminder(ctx, "space", r.ID)
	if current.TaskStatus != "completed" || current.TaskCompletedAt == nil || !current.TaskCompletedAt.Equal(now.UTC()) {
		t.Fatalf("lost completed task %+v", current)
	}
	memories, _ = reopened.ListReminderMemories(ctx, "space")
	if len(memories) != 1 || memories[0].DeliveredAt == nil || !memories[0].DeliveredAt.Equal(now.UTC()) {
		t.Fatal(memories)
	}
	if _, err := reopened.CompleteReminder(ctx, "space", r.ID, 2); err != nil {
		t.Fatal(err)
	}
	memories, _ = reopened.ListReminderMemories(ctx, "space")
	if memories[0].CompletedBy != nil || memories[0].Status != ReminderDelivered || memories[0].TaskStatus != "completed" {
		t.Fatal(memories)
	}
	if _, err := reopened.NextScheduledReminder(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestTodoBoardHistoryPagesHaveConsistentNumbersAndBoundedSize(t *testing.T) {
	db, _ := openReminderTestDB(t)
	ctx := context.Background()
	binding, _ := db.GetBindingByPair(ctx, 1, 2)
	if err := db.QueueMemory(ctx, MemoryRecord{Kind: "template", SessionID: "space", Path: "tasks/todo-board.json", Scope: "space", Storage: "database_and_memory", PendingContent: `{"reminders":[]}`, Operation: "upsert", BindingCreatedAt: binding.CreatedAt}); err != nil {
		t.Fatal(err)
	}
	// A full month keeps every reminder, with stable pages that fill before splitting.
	var rows []Reminder
	for i := 0; i < 35; i++ {
		rows = append(rows, Reminder{ID: fmt.Sprintf("rem_fixture_%03d", i), MemoryBucket: "abcd", SessionID: "space", Title: "分页验证", DueAt: time.Now().Add(time.Hour), RecipientIDs: []int64{1, 2}, Status: ReminderScheduled, BindingCreatedAt: binding.CreatedAt})
	}
	if err := db.gdb.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if err := migrateReminderHistory(db.gdb); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for page, size := range []int{16, 16, 3} {
		record, err := db.GetMemoryRecord(ctx, MemoryID("space", fmt.Sprintf("tasks/todo-board/%s/%06d.json", time.Now().In(time.FixedZone("Asia/Shanghai", 28800)).Format("2006-01"), page+1)), "space")
		if err != nil || record == nil {
			t.Fatal("missing history page", err)
		}
		var body struct {
			Pagination struct {
				Page int `json:"page"`
			} `json:"pagination"`
			Reminders []struct {
				ID string `json:"reminderId"`
			} `json:"reminders"`
		}
		if err := json.Unmarshal([]byte(record.Content), &body); err != nil || body.Pagination.Page != page+1 || len(body.Reminders) != size {
			t.Fatal("inconsistent page metadata or size", body, err)
		}
		for _, r := range body.Reminders {
			if seen[r.ID] {
				t.Fatal("duplicated reminder across pages", r.ID)
			}
			seen[r.ID] = true
		}
	}
	if len(seen) != len(rows) {
		t.Fatal("history lost reminders")
	}
	root, _ := db.GetMemoryRecord(ctx, MemoryID("space", "tasks/todo-board.json"), "space")
	var body struct {
		HasMore   bool              `json:"hasMore"`
		Reminders []json.RawMessage `json:"reminders"`
	}
	if err := json.Unmarshal([]byte(root.Content), &body); err != nil || !body.HasMore || len(body.Reminders) != 16 {
		t.Fatal("root board was not bounded", err)
	}
}

func TestJSONTodoBoardCompletesReminderOnlyAndRollsBackWithDelivery(t *testing.T) {
	db, _ := openReminderTestDB(t)
	ctx := context.Background()
	binding, _ := db.GetBindingByPair(ctx, 1, 2)
	template := MemoryRecord{Kind: "template", SessionID: "space", Path: "tasks/todo-board.json", Scope: "space", Storage: "database_and_memory", PendingContent: `{"reminders":[]}`, Operation: "upsert", BindingCreatedAt: binding.CreatedAt}
	if err := db.QueueMemory(ctx, template); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	r := makeReminder(t, db, "board", 0, now.Add(-time.Second), 1, 2)
	check := func(want string) {
		t.Helper()
		record, err := db.GetMemoryRecord(ctx, MemoryID("space", template.Path), "space")
		if err != nil {
			t.Fatal(err)
		}
		var board struct {
			RecentReminders []struct{ ID, Status string }
		}
		if err := json.Unmarshal([]byte(record.Content), &board); err != nil || len(board.RecentReminders) != 1 || board.RecentReminders[0].ID != r.ID || board.RecentReminders[0].Status != want {
			t.Fatal(board, err)
		}
	}
	check("pending")
	if _, err := db.ClaimDueReminder(ctx, now); err != nil {
		t.Fatal(err)
	}
	if err := db.gdb.Exec(`CREATE TRIGGER reject_board BEFORE UPDATE ON memory_records WHEN NEW.path='tasks/todo-board.json' BEGIN SELECT RAISE(ABORT,'board blocked'); END`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.FinishReminderDispatch(ctx, r.ID, []string{"evt_due"}, now); err == nil {
		t.Fatal("projection failure did not roll back")
	}
	current, _ := db.GetReminder(ctx, "space", r.ID)
	if current.CompletedBy != nil || current.Status != ReminderDispatching || current.hasCompletedDelivery() {
		t.Fatal("delivery committed without board")
	}
	db.gdb.Exec(`DROP TRIGGER reject_board`)
	if err := db.FinishReminderDispatch(ctx, r.ID, []string{"evt_due"}, now); err != nil {
		t.Fatal(err)
	}
	check("completed")
	page, err := db.GetReminderMemory(ctx, *r)
	if err != nil || page == nil {
		t.Fatal(err)
	}
	fact, err := ExtractReminderMemory(page.Content, r.ID)
	if err != nil || !strings.Contains(fact, `"status":"completed"`) || !strings.Contains(fact, `"activityStatus":"pending"`) {
		t.Fatal("reminder completion incorrectly proves activity completion", fact, err)
	}
}
