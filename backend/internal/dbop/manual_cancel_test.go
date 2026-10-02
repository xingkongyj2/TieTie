package dbop

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestManualCancellationAndConfirmationAreAtomicAndRecoverable(t *testing.T) {
	db, path := openReminderTestDB(t)
	ctx := context.Background()
	r := makeReminder(t, db, "manual-cancel", 0, time.Now().Add(time.Hour), 1)
	binding, err := db.GetLatestBindingByUser(ctx, 1)
	if err != nil || binding == nil {
		t.Fatal(err)
	}
	job := ControlJob{SessionID: "space", RequestID: "manual_cancel_" + r.ID, CreatedBy: 1, BindingCreatedAt: binding.CreatedAt}
	if err := db.gdb.Exec("CREATE TRIGGER reject_cancel_notice BEFORE INSERT ON control_jobs BEGIN SELECT RAISE(ABORT, 'notice unavailable'); END").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := db.CancelReminderAndNotify(ctx, "space", r.ID, job); err == nil {
		t.Fatal("queue failure ignored")
	}
	saved, err := db.GetReminder(ctx, "space", r.ID)
	if err != nil || saved.Status != ReminderScheduled {
		t.Fatal("cancellation committed without confirmation", saved, err)
	}
	if err := db.gdb.Exec("DROP TRIGGER reject_cancel_notice").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := db.CancelReminderAndNotify(ctx, "space", r.ID, job); err != nil {
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
	jobs, err := reopened.ClaimControls(ctx, time.Now().Add(time.Second), 10)
	if err != nil || len(jobs) != 1 || !jobs[0].NotificationOnly || jobs[0].Results == "" {
		t.Fatal("confirmation lost across restart", jobs, err)
	}
	saved, err = reopened.GetReminder(ctx, "space", r.ID)
	if err != nil || saved.Status != ReminderCancelled {
		t.Fatal(saved, err)
	}
}

func TestManualCancellationCannotNotifyForAlreadyDeliveredReminder(t *testing.T) {
	db, _ := openReminderTestDB(t)
	ctx := context.Background()
	r := makeReminder(t, db, "already-delivered", 0, time.Now().Add(-time.Minute), 1)
	if _, err := db.ClaimDueReminder(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := db.FinishReminderDispatch(ctx, r.ID, []string{"sent", "reply"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	job := ControlJob{SessionID: "space", RequestID: "manual_cancel_" + r.ID, CreatedBy: 1}
	if _, err := db.CancelReminderAndNotify(ctx, "space", r.ID, job); !errors.Is(err, ErrReminderState) {
		t.Fatal("delivered cancellation accepted", err)
	}
	if got, err := db.GetControl(ctx, "space", job.RequestID); err != nil || got != nil {
		t.Fatal("false cancellation confirmation queued", got, err)
	}
}
