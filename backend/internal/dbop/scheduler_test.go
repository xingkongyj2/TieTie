package dbop

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestReminderBatchUsesTimeIndexAndExcludesFutureJobs(t *testing.T) {
	db, _ := openReminderTestDB(t)
	ctx, now := context.Background(), time.Now().UTC()
	binding, err := db.GetBindingBySessionID(ctx, "space")
	if err != nil {
		t.Fatal(err)
	}
	rows := make([]Reminder, 10000)
	for i := range rows {
		rows[i] = Reminder{ID: fmt.Sprintf("future_%05d", i), SessionID: "space", Title: "未来提醒", DueAt: now.Add(24 * time.Hour), RunAt: now.Add(24 * time.Hour), Status: ReminderScheduled, RecipientIDs: []int64{1}, BindingCreatedAt: binding.CreatedAt}
	}
	if err := db.gdb.CreateInBatches(rows, 200).Error; err != nil {
		t.Fatal(err)
	}
	first := makeReminder(t, db, "due_first", 0, now.Add(-time.Minute), 1)
	makeReminder(t, db, "due_second", 0, now.Add(-time.Second), 1, 2)
	if _, err := db.CreateBinding(ctx, 3, 4, "second-space"); err != nil {
		t.Fatal(err)
	}
	second, _, err := db.ApplyReminderAction(ctx, "second-space", "due_other", 0, ReminderAction{Type: "create", Title: "对方空间", DueAt: now.Add(-time.Minute), RecipientIDs: []int64{3, 4}})
	if err != nil {
		t.Fatal(err)
	}
	var plan []struct{ Detail string }
	if err := db.gdb.Raw("EXPLAIN QUERY PLAN "+dueReminderSelection, ReminderScheduled, now, ReminderDispatching, ReminderScheduled, 64).Scan(&plan).Error; err != nil {
		t.Fatal(err)
	}
	var details []string
	for _, step := range plan {
		details = append(details, step.Detail)
	}
	joined := strings.Join(details, "\n")
	if !strings.Contains(joined, "idx_reminders_ready (status=? AND run_at<?)") || !strings.Contains(joined, "idx_bindings_session") || !strings.Contains(joined, "idx_reminders_space_status") {
		t.Fatalf("due query must use time and space indexes:\n%s", joined)
	}
	t.Logf("10,000 future jobs excluded using query plan:\n%s", joined)
	claimed, err := db.ClaimDueReminders(ctx, now, 64)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 2 {
		t.Fatalf("want only two due spaces, got %d", len(claimed))
	}
	ids := map[string]bool{}
	for _, r := range claimed {
		ids[r.ID] = true
	}
	if !ids[first.ID] || !ids[second.ID] {
		t.Fatalf("wrong earliest due jobs: %+v", ids)
	}
	if next, err := db.ClaimDueReminders(ctx, now, 64); err != nil || len(next) != 0 {
		t.Fatalf("a space with active dispatch cannot be claimed: %+v %v", next, err)
	}
}

func TestConversationSyncLeaseAndVersionPreventLosingNewMessage(t *testing.T) {
	db, path := openReminderTestDB(t)
	ctx, now := context.Background(), time.Now().UTC()
	if err := db.MarkConversationPending(ctx, "space", now); err != nil {
		t.Fatal(err)
	}
	jobs, err := db.ClaimConversationSync(ctx, now.Add(time.Second), 1)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("claim sync: %+v %v", jobs, err)
	}
	if repeated, err := db.ClaimConversationSync(ctx, now.Add(time.Second), 1); err != nil || len(repeated) != 0 {
		t.Fatalf("lease duplicate: %+v %v", repeated, err)
	}
	if err := db.MarkConversationPending(ctx, "space", now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	old := jobs[0]
	old.SyncCursor = "evt_old"
	if err := db.FinishConversationSync(ctx, old, true, now); err != nil {
		t.Fatal(err)
	}
	job, err := db.GetConversationJob(ctx, "space")
	if err != nil || job == nil || job.ConversationVersion == old.ConversationVersion || job.SyncCursor != "" {
		t.Fatalf("old job erased a new pending input: %+v %v", job, err)
	}
	job.SyncCursor, job.SyncOrigin = "evt_latest", "origin"
	if err := db.FinishConversationSync(ctx, *job, false, now.Add(time.Minute)); err != nil {
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
	jobs, err = reopened.ClaimConversationSync(ctx, now.Add(2*time.Minute), 1)
	if err != nil || len(jobs) != 1 || jobs[0].SyncCursor != "evt_latest" || jobs[0].SyncOrigin != "origin" {
		t.Fatalf("incremental state lost on restart: %+v %v", jobs, err)
	}
}

func TestStableActionKeyDeduplicatesMultipleAIReplies(t *testing.T) {
	db, _ := openReminderTestDB(t)
	ctx := context.Background()
	action := ReminderAction{Type: "create", Title: "喝水", DueAt: time.Now().Add(time.Hour), RecipientIDs: []int64{1}, RequestKey: "one-user-turn/drink-water"}
	first, applied, err := db.ApplyReminderAction(ctx, "space", "evt_first_ai", 0, action)
	if err != nil || !applied {
		t.Fatal(err)
	}
	second, applied, err := db.ApplyReminderAction(ctx, "space", "evt_second_ai", 0, action)
	if err != nil || applied || second.ID != first.ID {
		t.Fatalf("duplicate model proposal: %+v %v %v", second, applied, err)
	}
	action.RequestKey = "another-user-turn/drink-water"
	third, applied, err := db.ApplyReminderAction(ctx, "space", "evt_third_ai", 0, action)
	if err != nil || !applied || third.ID == first.ID {
		t.Fatalf("independent user request suppressed: %+v %v %v", third, applied, err)
	}
}

func TestReminderRunAtBackfilledFromEarlierSchema(t *testing.T) {
	db, path := openReminderTestDB(t)
	ctx := context.Background()
	due := time.Now().UTC().Add(-time.Hour)
	reminder := makeReminder(t, db, "migration", 0, due, 1)
	next := time.Now().UTC().Add(time.Hour)
	if err := db.gdb.Model(&Reminder{}).Where("id = ?", reminder.ID).Update("next_attempt_at", next).Error; err != nil {
		t.Fatal(err)
	}
	for _, index := range []string{"idx_reminders_ready", "idx_reminders_space_status"} {
		if err := db.gdb.Exec("DROP INDEX " + index).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.gdb.Exec("ALTER TABLE reminders DROP COLUMN run_at").Error; err != nil {
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
	saved, err := reopened.GetReminder(ctx, "space", reminder.ID)
	if err != nil || saved == nil || !saved.RunAt.Equal(next) {
		t.Fatalf("retry time lost on migration: %+v %v", saved, err)
	}
	if claims, err := reopened.ClaimDueReminders(ctx, next.Add(-time.Second), 64); err != nil || len(claims) != 0 {
		t.Fatalf("migrated retry fired early: %+v %v", claims, err)
	}
}
