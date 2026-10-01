package dbop

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestFiveThousandReminderHistoryIsCompleteAndUpdateTouchesOnePage(t *testing.T) {
	db, path := openReminderTestDB(t)
	ctx := context.Background()
	binding, _ := db.GetBindingBySessionID(ctx, "space")
	if err := db.QueueMemory(ctx, MemoryRecord{Kind: "template", SessionID: "space", Path: "tasks/todo-board.json", Scope: "space", Storage: "database_and_memory", PendingContent: `{"reminders":[]}`, Operation: "upsert", BindingCreatedAt: binding.CreatedAt}); err != nil {
		t.Fatal(err)
	}
	// One month with 5,000 reminders is deliberately worse than years spread
	// across many months. Pending/completed/cancelled records must all survive.
	created := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	rows := make([]Reminder, 5000)
	completedBy := int64(2)
	for i := range rows {
		rows[i] = Reminder{ID: fmt.Sprintf("rem_history_%05d", i), SessionID: "space", Title: fmt.Sprintf("完整历史事项%d", i), DueAt: created.Add(time.Duration(i) * time.Hour), CreatedAt: created, UpdatedAt: created, RecipientIDs: []int64{1, 2}, CreatedBy: 1, Status: ReminderScheduled, TaskStatus: "pending", BindingCreatedAt: binding.CreatedAt}
		if i%3 == 1 {
			rows[i].Status = ReminderDelivered
			rows[i].TaskStatus = "completed"
			rows[i].CompletedBy = &completedBy
		}
		if i%3 == 2 {
			rows[i].Status = ReminderCancelled
			rows[i].TaskStatus = "cancelled"
		}
	}
	if err := db.gdb.CreateInBatches(rows, 200).Error; err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := migrateReminderHistory(db.gdb); err != nil {
		t.Fatal(err)
	}
	backfill := time.Since(start)
	var records []MemoryRecord
	if err := db.gdb.Where("session_id=? AND path GLOB 'tasks/todo-board/2025-06/*.json'", "space").Order("path").Find(&records).Error; err != nil {
		t.Fatal(err)
	}
	if len(records) != 313 {
		t.Fatalf("5,000 reminders should use 313 packed pages, got %d", len(records))
	}
	seen := map[string]bool{}
	versions := map[string]int64{}
	for _, record := range records {
		versions[record.ID] = record.Revision
		if len(record.Content) > 96*1024 {
			t.Fatal("oversized archive")
		}
		var page struct {
			Reminders []struct {
				ID         string    `json:"reminderId"`
				Title      string    `json:"title"`
				Status     string    `json:"status"`
				CreatedBy  int64     `json:"createdBy"`
				DueAt      time.Time `json:"dueAt"`
				Recipients []int64   `json:"recipientIds"`
			} `json:"reminders"`
		}
		if err := json.Unmarshal([]byte(record.Content), &page); err != nil {
			t.Fatal(err)
		}
		if len(page.Reminders) > ReminderHistoryPageSize || len(page.Reminders) == 0 {
			t.Fatal("incorrect page size")
		}
		for _, fact := range page.Reminders {
			if seen[fact.ID] {
				t.Fatal("duplicate history", fact.ID)
			}
			seen[fact.ID] = true
			var index int
			if _, err := fmt.Sscanf(fact.ID, "rem_history_%05d", &index); err != nil {
				t.Fatal(err)
			}
			original := rows[index]
			if fact.Title != original.Title || fact.Status != reminderBoardStatus(&original) || fact.CreatedBy != original.CreatedBy || !fact.DueAt.Equal(original.DueAt) || len(fact.Recipients) != 2 {
				t.Fatal("history lost original fields", fact.ID)
			}
		}
	}
	if len(seen) != len(rows) {
		t.Fatal("history was truncated", len(seen))
	}
	// An older month outside the root's recent list still updates in one page.
	start = time.Now()
	if _, err := db.CancelReminder(ctx, "space", rows[99].ID); err != nil {
		t.Fatal(err)
	}
	update := time.Since(start)
	var after []MemoryRecord
	if err := db.gdb.Where("session_id=? AND path GLOB 'tasks/todo-board/2025-06/*.json'", "space").Find(&after).Error; err != nil {
		t.Fatal(err)
	}
	changed := 0
	for _, record := range after {
		if record.Revision != versions[record.ID] {
			changed++
		}
	}
	if changed != 1 {
		t.Fatal("old reminder rewrote unrelated archive pages", changed)
	}
	root, _ := db.GetMemoryRecord(ctx, MemoryID("space", "tasks/todo-board.json"), "space")
	var board struct {
		Reminders []json.RawMessage `json:"reminders"`
		Recent    []json.RawMessage `json:"recentReminders"`
		HasMore   bool              `json:"hasMore"`
	}
	if err := json.Unmarshal([]byte(root.Content), &board); err != nil || len(board.Reminders) != TodoBoardLimit || len(board.Recent) != TodoRecentLimit || !board.HasMore {
		t.Fatal("unbounded current board", err)
	}
	// Explain actual lookup shapes: history and current/recent views use indexes.
	queries := []struct {
		sql, index string
		args       []any
	}{
		{"SELECT r.* FROM reminder_history_locations l JOIN reminders r ON r.id=l.reminder_id WHERE l.session_id=? AND l.month=? AND l.page=? ORDER BY l.position LIMIT 16", "idx_history_page", []any{"space", "2025-06", 6}},
		{"SELECT * FROM reminder_history_locations WHERE session_id=? AND board_status='pending' ORDER BY due_at,reminder_id LIMIT 17", "idx_history_board", []any{"space"}},
		{"SELECT * FROM reminder_history_locations WHERE session_id=? ORDER BY created_at DESC,reminder_id DESC LIMIT 8", "idx_history_recent", []any{"space"}},
	}
	for _, query := range queries {
		var plan []struct{ Detail string }
		if err := db.gdb.Raw("EXPLAIN QUERY PLAN "+query.sql, query.args...).Scan(&plan).Error; err != nil {
			t.Fatal(err)
		}
		details := ""
		for _, step := range plan {
			details += step.Detail + "\n"
		}
		if !strings.Contains(details, query.index) || strings.Contains(details, "USE TEMP B-TREE") {
			t.Fatal("lookup did not use its ordered index", details)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	var count int64
	if err := reopened.gdb.Model(&Reminder{}).Count(&count).Error; err != nil || count != 5000 {
		t.Fatal("history did not survive restart", count, err)
	}
	var total ReminderHistoryMonth
	if err := reopened.gdb.Where("session_id=? AND month=?", "space", "2025-06").First(&total).Error; err != nil || total.RecordCount != 5000 {
		t.Fatal("restart duplicated history positions", total, err)
	}
	t.Logf("5,000 complete reminders -> 313 history pages; local backfill=%s, indexed update=%s, changed history pages=%d", backfill.Round(time.Millisecond), update.Round(time.Microsecond), changed)
}

func TestLegacyReminderFilesWaitForAllReplacementPagesAndRecoverAfterRestart(t *testing.T) {
	db, path := openReminderTestDB(t)
	ctx := context.Background()
	r := makeReminder(t, db, "legacy", 0, time.Now().Add(time.Hour), 1, 2)
	legacy := MemoryRecord{SessionID: "space", Path: "shared/reminders/" + r.ID + ".json", Scope: "space", Storage: "database_and_memory", PendingContent: reminderCloudContent(r), Operation: "upsert", BindingCreatedAt: r.BindingCreatedAt}
	if err := db.QueueMemory(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	legacy.ID = MemoryID("space", legacy.Path)
	old, _ := db.GetMemoryRecord(ctx, legacy.ID, "space")
	if err := db.FinishMemorySync(ctx, *old, "memstore_test", "mem_old"); err != nil {
		t.Fatal(err)
	}
	// Unknown legacy content must not be discarded just because it has an old path.
	orphan := legacy
	orphan.Path = "tasks/todo-board/orphan-0.json"
	orphan.PendingContent = `{"reminders":[{"id":"missing_reminder"}]}`
	if err := db.QueueMemory(ctx, orphan); err != nil {
		t.Fatal(err)
	}
	if err := migrateReminderHistory(db.gdb); err != nil {
		t.Fatal(err)
	}
	retiring, _ := db.GetMemoryRecord(ctx, legacy.ID, "space")
	if !retiring.ArchiveRetirement || retiring.Operation != "delete" || retiring.Content == "" {
		t.Fatal("legacy copy was not retained until cloud replacement", retiring)
	}
	for _, pass := range []string{"before restart", "after restart"} {
		jobs, err := db.ClaimMemorySync(ctx, time.Now().Add(time.Second), 100)
		if err != nil {
			t.Fatal(err)
		}
		for _, job := range jobs {
			if job.ArchiveRetirement {
				t.Fatal("legacy deletion before replacement", pass)
			}
		}
		if err := db.RecoverMemorySync(ctx); err != nil {
			t.Fatal(err)
		}
		if pass == "before restart" {
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			db, err = Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
		}
	}
	page, _ := db.GetReminderMemory(ctx, *r)
	// A stale page synchronization cannot unblock deletion of current history.
	oldPage := *page
	if _, err := db.CancelReminder(ctx, "space", r.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.FinishMemorySync(ctx, oldPage, "memstore_test", "mem_stale"); err != nil {
		t.Fatal(err)
	}
	if ready, err := db.ReminderHistoryReady(ctx, "space"); err != nil || ready {
		t.Fatal("stale write accepted as complete archive")
	}
	jobs, err := db.ClaimMemorySync(ctx, time.Now().Add(time.Second), 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, job := range jobs {
		if job.ArchiveRetirement {
			t.Fatal("premature legacy delete")
		}
		if err := db.FinishMemorySync(ctx, job, "memstore_test", "mem_replacement"); err != nil {
			t.Fatal(err)
		}
	}
	jobs, err = db.ClaimMemorySync(ctx, time.Now().Add(time.Second), 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, job := range jobs {
		if job.ID == legacy.ID && job.ArchiveRetirement {
			found = true
			if err := db.FinishMemorySync(ctx, job, "memstore_test", ""); err != nil {
				t.Fatal(err)
			}
		}
	}
	if !found {
		t.Fatal("legacy copy was not retired after successful replacement")
	}
	unknown, _ := db.GetMemoryRecord(ctx, MemoryID("space", orphan.Path), "space")
	if unknown.Operation != "upsert" {
		t.Fatal("unknown legacy history was deleted")
	}
}

func TestReminderHistoryKeepsEarlierMonthsAndLimitsIndex(t *testing.T) {
	db, _ := openReminderTestDB(t)
	ctx := context.Background()
	binding, _ := db.GetBindingBySessionID(ctx, "space")
	var rows []Reminder
	for i := 0; i < 24; i++ {
		created := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, i, 0)
		rows = append(rows, Reminder{ID: fmt.Sprintf("rem_month_%02d", i), SessionID: "space", Title: "过去月份提醒", CreatedAt: created, DueAt: created.Add(time.Hour), Status: ReminderCancelled, RecipientIDs: []int64{1}, BindingCreatedAt: binding.CreatedAt})
	}
	if err := db.gdb.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if err := migrateReminderHistory(db.gdb); err != nil {
		t.Fatal(err)
	}
	index, _ := db.GetMemoryRecord(ctx, MemoryID("space", ReminderHistoryIndexPath), "space")
	var doc struct {
		Months []json.RawMessage `json:"months"`
		More   bool              `json:"hasEarlierMonths"`
	}
	var root struct {
		History json.RawMessage `json:"history"`
	}
	json.Unmarshal([]byte(index.Content), &root)
	if err := json.Unmarshal(root.History, &doc); err != nil || len(doc.Months) != 12 || !doc.More {
		t.Fatal("month index was not bounded", err)
	}
	first, err := db.GetReminderMemory(ctx, rows[0])
	if err != nil || first == nil {
		t.Fatal("earliest history disappeared beyond index", err)
	}
	if _, err := ExtractReminderMemory(first.Content, rows[0].ID); err != nil {
		t.Fatal(err)
	}
}

func TestPrivateHistoryPromotionPublishesOnlyTheDeliveredReminder(t *testing.T) {
	db, _ := openReminderTestDB(t)
	ctx := context.Background()
	binding, _ := db.GetBindingBySessionID(ctx, "space")
	if err := db.SavePrivateChannel(ctx, PrivateChannel{SessionID: "private", SpaceID: "space", OwnerID: 1, BindingCreatedAt: binding.CreatedAt}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	public, _, err := db.ApplyReminderAction(ctx, "private", "publish_fixture", 0, ReminderAction{Type: "create", Title: "到期可以告诉对方的事项", DueAt: now.Add(-time.Second), RecipientIDs: []int64{2}, CreatedBy: 1})
	if err != nil {
		t.Fatal(err)
	}
	secret, _, err := db.ApplyReminderAction(ctx, "private", "secret_fixture", 0, ReminderAction{Type: "create", Title: "同页其他私密事项", DueAt: now.Add(time.Hour), RecipientIDs: []int64{1}, CreatedBy: 1})
	if err != nil {
		t.Fatal(err)
	}
	var before int64
	db.gdb.Model(&ReminderHistoryLocation{}).Where("session_id=?", "space").Count(&before)
	if before != 0 {
		t.Fatal("private history published before delivery")
	}
	if _, err := db.ClaimDueReminder(ctx, now); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordReminderDispatch(ctx, public.ID, []string{"evt_due"}); err != nil {
		t.Fatal(err)
	}
	if err := db.FinishReminderDispatch(ctx, public.ID, []string{"evt_due", "evt_reply"}, now); err != nil {
		t.Fatal(err)
	}
	var published ReminderHistoryLocation
	if err := db.gdb.Where("session_id=? AND reminder_id=?", "space", public.ID).First(&published).Error; err != nil {
		t.Fatal(err)
	}
	shared, _ := db.GetMemoryRecord(ctx, MemoryID("space", published.Path()), "space")
	if shared == nil || !strings.Contains(shared.Content, public.ID) || strings.Contains(shared.Content, secret.ID) || strings.Contains(shared.Content, secret.Title) {
		t.Fatal("shared history leaked another private reminder")
	}
	canonical, err := db.GetReminderMemory(ctx, *public)
	if err != nil || canonical == nil || !strings.Contains(canonical.Content, secret.ID) {
		t.Fatal("canonical private history was truncated", err)
	}
	if _, err := db.CancelReminder(ctx, "space", public.ID); err != nil {
		t.Fatal(err)
	}
	for _, session := range []string{"space", "private"} {
		var l ReminderHistoryLocation
		if err := db.gdb.Where("session_id=? AND reminder_id=?", session, public.ID).First(&l).Error; err != nil {
			t.Fatal(err)
		}
		m, _ := db.GetMemoryRecord(ctx, MemoryID(session, l.Path()), session)
		body, err := ExtractReminderMemory(m.Content, public.ID)
		if err != nil || !strings.Contains(body, `"status":"cancelled"`) {
			t.Fatal("published/private histories diverged", err)
		}
	}
}

func TestMaximumLengthReminderTitlesFitCloudDocumentBudget(t *testing.T) {
	db, _ := openReminderTestDB(t)
	ctx := context.Background()
	binding, _ := db.GetBindingBySessionID(ctx, "space")
	if err := db.QueueMemory(ctx, MemoryRecord{Kind: "template", SessionID: "space", Path: "tasks/todo-board.json", Scope: "space", Storage: "database_and_memory", PendingContent: `{"reminders":[]}`, Operation: "upsert", BindingCreatedAt: binding.CreatedAt}); err != nil {
		t.Fatal(err)
	}
	// Escaped control characters consume six bytes per rune in JSON, more than
	// ordinary Chinese or emoji. Exercise the 500-rune title limit's worst case.
	for i := 0; i < 24; i++ {
		if _, _, err := db.ApplyReminderAction(ctx, "space", fmt.Sprintf("size_%d", i), 0, ReminderAction{Type: "create", Title: strings.Repeat("\x00", 500), DueAt: time.Now().Add(time.Hour), RecipientIDs: []int64{1, 2}}); err != nil {
			t.Fatal(err)
		}
	}
	var docs []MemoryRecord
	if err := db.gdb.Where("session_id=?", "space").Find(&docs).Error; err != nil {
		t.Fatal(err)
	}
	for _, doc := range docs {
		if len(doc.Content) > 96*1024 {
			t.Fatal("maximum legal input exceeded cloud budget", doc.Path)
		}
	}
}
