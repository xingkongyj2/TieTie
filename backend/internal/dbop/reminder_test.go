package dbop

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func openReminderTestDB(t *testing.T) (*DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "reminders.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.CreateBinding(context.Background(), 1, 2, "space"); err != nil {
		t.Fatal(err)
	}
	return db, path
}

func makeReminder(t *testing.T, db *DB, source string, index int, due time.Time, recipients ...int64) *Reminder {
	t.Helper()
	r, applied, err := db.ApplyReminderAction(context.Background(), "space", source, index, ReminderAction{
		Type: "create", Title: "记得喝水", DueAt: due, RecipientIDs: recipients, CreatedBy: 1,
	})
	if err != nil || !applied || r == nil {
		t.Fatalf("create reminder: %+v, applied=%v, err=%v", r, applied, err)
	}
	return r
}

func TestReminderActionReceiptIdempotencyAndIsolation(t *testing.T) {
	db, _ := openReminderTestDB(t)
	ctx := context.Background()
	due := time.Now().Add(time.Hour)
	// The zero action index must not overwrite another action's receipt.
	second := makeReminder(t, db, "ai-event", 1, due, 2)
	first := makeReminder(t, db, "ai-event", 0, due, 1, 2)
	for index, want := range []*Reminder{first, second} {
		got, applied, err := db.ApplyReminderAction(ctx, "space", "ai-event", index, ReminderAction{
			Type: "create", Title: "重放", DueAt: due.Add(time.Hour), RecipientIDs: []int64{1},
		})
		if err != nil || applied || got == nil || got.ID != want.ID || !got.DueAt.Equal(due) {
			t.Fatalf("replay %d: %+v, %v, %v", index, got, applied, err)
		}
	}
	if _, _, err := db.ApplyReminderAction(ctx, "other-space", "cancel", 0, ReminderAction{Type: "cancel", ReminderID: first.ID}); !errors.Is(err, ErrReminderNotFound) {
		t.Fatalf("cross-space cancel: %v", err)
	}
	for i := 0; i < 2; i++ {
		got, applied, err := db.ApplyReminderAction(ctx, "space", "cancel", 0, ReminderAction{Type: "cancel", ReminderID: first.ID})
		if err != nil || applied != (i == 0) || got.Status != ReminderCancelled {
			t.Fatalf("cancel replay %d: %+v, %v, %v", i, got, applied, err)
		}
	}
	rows, err := db.ListReminders(ctx, "space")
	if err != nil || len(rows) != 2 {
		t.Fatalf("duplicate reminders: %+v, %v", rows, err)
	}
	var receipts int64
	if err := db.gdb.Model(&ReminderActionReceipt{}).Count(&receipts).Error; err != nil || receipts != 3 {
		t.Fatalf("receipt count %d: %v", receipts, err)
	}
}

func TestReminderQueueSurvivesRestartWithoutDuplicateDispatch(t *testing.T) {
	db, path := openReminderTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	abandoned := makeReminder(t, db, "first", 0, now.Add(-2*time.Hour), 1)
	queued := makeReminder(t, db, "second", 0, now.Add(-time.Hour), 1, 2)
	claim, err := db.ClaimDueReminder(ctx, now)
	if err != nil || claim == nil || claim.ID != abandoned.ID || claim.Attempts != 1 {
		t.Fatalf("initial claim: %+v, %v", claim, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if count, err := reopened.RecoverReminderDispatches(ctx); err != nil || count != 1 {
		t.Fatalf("recovery: %d, %v", count, err)
	}
	if replayed, applied, err := reopened.ApplyReminderAction(ctx, "space", "second", 0, ReminderAction{Type: "create"}); err != nil || applied || replayed.ID != queued.ID {
		t.Fatalf("persisted action replay: %+v, %v, %v", replayed, applied, err)
	}
	claim, err = reopened.ClaimDueReminder(ctx, now)
	if err != nil || claim == nil || claim.ID != queued.ID || len(claim.RecipientIDs) != 2 {
		t.Fatalf("persisted queued claim: %+v, %v", claim, err)
	}
	if err := reopened.FinishReminderDispatch(ctx, claim.ID, []string{"sent-user", "sent-ai"}, now); err != nil {
		t.Fatal(err)
	}
	if next, err := reopened.ClaimDueReminder(ctx, now.Add(time.Hour)); err != nil || next != nil {
		t.Fatalf("already dispatched reminder reclaimed: %+v, %v", next, err)
	}
	saved, err := reopened.GetReminder(ctx, "space", queued.ID)
	if err != nil || saved.DeliveredAt == nil || len(saved.DispatchEventIDs) != 2 || saved.Status != ReminderDelivered {
		t.Fatalf("delivery state: %+v, %v", saved, err)
	}
	uncertain, err := reopened.GetReminder(ctx, "space", abandoned.ID)
	if err != nil || uncertain.Status != ReminderUncertain {
		t.Fatalf("abandoned state: %+v, %v", uncertain, err)
	}
	if err := reopened.FinishReminderDispatch(ctx, abandoned.ID, []string{"history-wakeup", "history-ai-reply"}, now); err != nil {
		t.Fatalf("uncertain delivery with actual reply evidence: %v", err)
	}
}

func TestReminderAcceptedWakeupWaitsForReplyAcrossRestart(t *testing.T) {
	db, path := openReminderTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	accepted := makeReminder(t, db, "accepted", 0, now.Add(-time.Hour), 1)
	queued := makeReminder(t, db, "queued", 0, now.Add(-time.Minute), 2)
	if claim, err := db.ClaimDueReminder(ctx, now); err != nil || claim == nil || claim.ID != accepted.ID {
		t.Fatalf("claim accepted reminder: %+v, %v", claim, err)
	}
	if err := db.RecordReminderDispatch(ctx, accepted.ID, []string{"wakeup-event"}); err != nil {
		t.Fatal(err)
	}
	if err := db.RetryReminderDispatch(ctx, accepted.ID, now); !errors.Is(err, ErrReminderState) {
		t.Fatalf("already accepted wakeup rescheduled: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if count, err := reopened.RecoverReminderDispatches(ctx); err != nil || count != 0 {
		t.Fatalf("accepted wakeup should resume polling: %d, %v", count, err)
	}
	dispatching, err := reopened.ListDispatchingReminders(ctx)
	if err != nil || len(dispatching) != 1 || dispatching[0].ID != accepted.ID || dispatching[0].DeliveredAt != nil || len(dispatching[0].DispatchEventIDs) != 1 {
		t.Fatalf("accepted wakeup state: %+v, %v", dispatching, err)
	}
	if next, err := reopened.ClaimDueReminder(ctx, now); err != nil || next != nil {
		t.Fatalf("second wakeup before first reply: %+v, %v", next, err)
	}
	if err := reopened.FinishReminderDispatch(ctx, accepted.ID, []string{"wakeup-event", "ai-reply"}, now); err != nil {
		t.Fatal(err)
	}
	if err := reopened.FinishReminderDispatch(ctx, accepted.ID, []string{"replay"}, now.Add(time.Hour)); err != nil {
		t.Fatalf("finished delivery replay should be idempotent: %v", err)
	}
	saved, err := reopened.GetReminder(ctx, "space", accepted.ID)
	if err != nil || saved.DeliveredAt == nil || !saved.DeliveredAt.Equal(now) || len(saved.DispatchEventIDs) != 2 {
		t.Fatalf("replay rewrote delivery: %+v, %v", saved, err)
	}
	if next, err := reopened.ClaimDueReminder(ctx, now); err != nil || next == nil || next.ID != queued.ID {
		t.Fatalf("queue after AI reply: %+v, %v", next, err)
	}
	if err := reopened.FailReminderDispatch(ctx, queued.ID); err != nil {
		t.Fatal(err)
	}
	if err := reopened.FinishReminderDispatch(ctx, queued.ID, []string{"late-reply"}, now); !errors.Is(err, ErrReminderState) {
		t.Fatalf("late history reply overwrote failed state: %v", err)
	}
	if err := reopened.CancelSessionReminders(ctx, "space"); err != nil {
		t.Fatal(err)
	}
}

func TestReminderConcurrentClaimsAcrossConnections(t *testing.T) {
	db, path := openReminderTestDB(t)
	other, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	now := time.Now().UTC()
	first := makeReminder(t, db, "first", 0, now.Add(-time.Hour), 1)
	makeReminder(t, db, "second", 0, now.Add(-time.Minute), 2)
	var wg sync.WaitGroup
	results := make(chan *Reminder, 20)
	errors := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(database *DB) {
			defer wg.Done()
			claim, err := database.ClaimDueReminder(context.Background(), now)
			results <- claim
			errors <- err
		}([]*DB{db, other}[i%2])
	}
	wg.Wait()
	close(results)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	claims := 0
	for claim := range results {
		if claim != nil {
			claims++
			if claim.ID != first.ID {
				t.Fatalf("wrong queue order: %+v", claim)
			}
		}
	}
	if claims != 1 {
		t.Fatalf("expected exactly one dispatch per space, got %d", claims)
	}
	if _, err := db.CreateBinding(context.Background(), 3, 4, "other-space"); err != nil {
		t.Fatal(err)
	}
	independent, _, err := db.ApplyReminderAction(context.Background(), "other-space", "third", 0, ReminderAction{
		Type: "create", Title: "独立空间", DueAt: now.Add(-time.Minute), RecipientIDs: []int64{3}, CreatedBy: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if claim, err := db.ClaimDueReminder(context.Background(), now); err != nil || claim == nil || claim.ID != independent.ID {
		t.Fatalf("another space blocked by active dispatch: %+v, %v", claim, err)
	}
}

func TestReminderConcurrentActionReplayAcrossConnections(t *testing.T) {
	db, path := openReminderTestDB(t)
	other, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	var wg sync.WaitGroup
	var mutex sync.Mutex
	var appliedCount int
	var reminderID string
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(database *DB) {
			defer wg.Done()
			r, applied, err := database.ApplyReminderAction(context.Background(), "space", "same-event", 0, ReminderAction{
				Type: "create", Title: "同一消息", DueAt: time.Now().Add(time.Hour), RecipientIDs: []int64{1}, CreatedBy: 1,
			})
			if err != nil {
				t.Error(err)
				return
			}
			mutex.Lock()
			defer mutex.Unlock()
			if applied {
				appliedCount++
			}
			if reminderID != "" && reminderID != r.ID {
				t.Errorf("duplicate reminder IDs: %s, %s", reminderID, r.ID)
			}
			reminderID = r.ID
		}([]*DB{db, other}[i%2])
	}
	wg.Wait()
	if appliedCount != 1 {
		t.Fatalf("expected one applied action, got %d", appliedCount)
	}
}

func TestReminderRejectsInactiveOrRecreatedBinding(t *testing.T) {
	db, _ := openReminderTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	old := makeReminder(t, db, "old", 0, now.Add(-time.Hour), 1)
	if _, err := db.Unbind(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if claim, err := db.ClaimDueReminder(ctx, now); err != nil || claim != nil {
		t.Fatalf("inactive space dispatch: %+v, %v", claim, err)
	}
	if _, err := db.CreateBinding(ctx, 1, 2, "space"); err != nil {
		t.Fatal(err)
	}
	if claim, err := db.ClaimDueReminder(ctx, now); err != nil || claim != nil {
		t.Fatalf("old binding dispatch: %+v, %v", claim, err)
	}
	fresh := makeReminder(t, db, "fresh", 0, now.Add(-time.Minute), 1)
	if claim, err := db.ClaimDueReminder(ctx, now); err != nil || claim == nil || claim.ID != fresh.ID || claim.ID == old.ID {
		t.Fatalf("new binding dispatch: %+v, %v", claim, err)
	}
}

func TestReminderCompletionOwnershipAndRestoration(t *testing.T) {
	db, _ := openReminderTestDB(t)
	ctx := context.Background()
	future := makeReminder(t, db, "future", 0, time.Now().Add(time.Hour), 1)
	if _, err := db.CompleteReminder(ctx, "space", future.ID, 2); !errors.Is(err, ErrReminderForbidden) {
		t.Fatalf("non-recipient completion: %v", err)
	}
	if _, err := db.CompleteReminder(ctx, "other-space", future.ID, 1); !errors.Is(err, ErrReminderNotFound) {
		t.Fatalf("cross-space completion: %v", err)
	}
	completed, err := db.CompleteReminder(ctx, "space", future.ID, 1)
	if err != nil || completed.CompletedBy == nil || *completed.CompletedBy != 1 || completed.Status != ReminderCompleted {
		t.Fatalf("completion: %+v, %v", completed, err)
	}
	if err := db.FinishReminderDispatch(ctx, future.ID, []string{"late-ai-event"}, time.Now()); !errors.Is(err, ErrReminderState) {
		t.Fatalf("history replay overwrote completed state: %v", err)
	}
	if _, err := db.RestoreCompletedReminder(ctx, "space", future.ID, 2); !errors.Is(err, ErrReminderForbidden) {
		t.Fatalf("non-recipient restoration: %v", err)
	}
	if restored, err := db.RestoreCompletedReminder(ctx, "space", future.ID, 1); err != nil || restored.Status != ReminderScheduled || restored.CompletedBy != nil {
		t.Fatalf("restoration: %+v, %v", restored, err)
	}
	past := makeReminder(t, db, "past", 0, time.Now().Add(-time.Hour), 1)
	if _, err := db.CompleteReminder(ctx, "space", past.ID, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := db.RestoreCompletedReminder(ctx, "space", past.ID, 1); !errors.Is(err, ErrReminderState) {
		t.Fatalf("past reminder restoration: %v", err)
	}
	cancelled := makeReminder(t, db, "cancelled", 0, time.Now().Add(time.Hour), 1)
	if _, err := db.CancelReminder(ctx, "space", cancelled.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.FinishReminderDispatch(ctx, cancelled.ID, []string{"late-ai-event"}, time.Now()); !errors.Is(err, ErrReminderState) {
		t.Fatalf("history replay overwrote cancelled state: %v", err)
	}
}

func TestReminderValidatesRecipientsAndSafeRetry(t *testing.T) {
	db, _ := openReminderTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if _, _, err := db.ApplyReminderAction(ctx, "space", "invalid", 0, ReminderAction{
		Type: "create", Title: "bad", DueAt: now, RecipientIDs: []int64{3},
	}); !errors.Is(err, ErrReminderForbidden) {
		t.Fatalf("foreign recipient: %v", err)
	}
	r := makeReminder(t, db, "valid", 0, now.Add(-time.Minute), 1)
	if claimed, err := db.ClaimDueReminder(ctx, now); err != nil || claimed == nil {
		t.Fatalf("claim: %+v, %v", claimed, err)
	}
	if err := db.RetryReminderDispatch(ctx, r.ID, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if claim, err := db.ClaimDueReminder(ctx, now); err != nil || claim != nil {
		t.Fatalf("retry ignored delay: %+v, %v", claim, err)
	}
	if claim, err := db.ClaimDueReminder(ctx, now.Add(2*time.Minute)); err != nil || claim == nil || claim.Attempts != 2 {
		t.Fatalf("delayed retry: %+v, %v", claim, err)
	}
	if err := db.MarkReminderDispatchUncertain(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	if claim, err := db.ClaimDueReminder(ctx, now.Add(time.Hour)); err != nil || claim != nil {
		t.Fatalf("ambiguous send retried: %+v, %v", claim, err)
	}
	if err := db.FailReminderDispatch(ctx, r.ID); err != nil {
		t.Fatalf("confirmed error on uncertain task: %v", err)
	}
	saved, _ := db.GetReminder(ctx, "space", r.ID)
	encoded, err := json.Marshal(saved)
	if err != nil {
		t.Fatal(err)
	}
	for _, internalName := range []string{"Attempts", "attempts", "ActionIndex", "DispatchEventIDs", "BindingCreatedAt"} {
		if strings.Contains(string(encoded), internalName) {
			t.Fatalf("internal queue field leaked: %s", encoded)
		}
	}
}
