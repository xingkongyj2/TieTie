package api

import (
	"encoding/json"
	"testing"
	"time"

	"tietie/backend/internal/conversation"
	"tietie/backend/internal/dbop"
)

func TestOperationConfirmationUsesCommittedReminder(t *testing.T) {
	epoch := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	binding := &dbop.Binding{SessionID: "sess_test", UserA: 11, UserB: 22, CreatedAt: epoch}
	reminder := &dbop.Reminder{ID: "rem_test", SessionID: binding.SessionID, CreatedBy: 11,
		Title: "数据库中的实际事项", DueAt: epoch.Add(24 * time.Hour), RecipientIDs: []int64{11},
		Status: dbop.ReminderScheduled, BindingCreatedAt: epoch}
	result := conversation.ActionResult{Type: "create_reminder", Status: "succeeded", ReminderID: reminder.ID,
		DatabaseStatus: "saved", MemoryStatus: "synced"}
	job := &dbop.ControlJob{SessionID: binding.SessionID, CreatedBy: 11, BindingCreatedAt: epoch}
	lookup := func(id string) (*dbop.Reminder, error) {
		if id != reminder.ID {
			t.Fatalf("unexpected reminder lookup: %q", id)
		}
		return reminder, nil
	}
	for _, test := range []struct {
		name    string
		results []conversation.ActionResult
		want    string
	}{
		{"verified save before AI reply", []conversation.ActionResult{result}, "saved"},
		{"partial failure keeps verified save", []conversation.ActionResult{result, {Type: "save_memory", Status: "failed", Message: "internal details"}}, "saved"},
		{"duplicate result stays one confirmation", []conversation.ActionResult{result, result}, "saved"},
		{"cloud memory is pending", []conversation.ActionResult{{Type: result.Type, Status: "partial", ReminderID: result.ReminderID, DatabaseStatus: "saved", MemoryStatus: "pending"}}, "pending"},
		{"database is not saved", []conversation.ActionResult{{Type: result.Type, Status: result.Status, ReminderID: result.ReminderID, MemoryStatus: "synced"}}, "pending"},
		{"failed action has generic feedback", []conversation.ActionResult{{Type: result.Type, Status: "failed", Message: "internal details"}}, "failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			encoded, err := json.Marshal(test.results)
			if err != nil {
				t.Fatal(err)
			}
			job.Results = string(encoded)
			got, err := savedOperationStatus(job, binding, 11, lookup)
			if err != nil || got.Status != test.want {
				t.Fatalf("confirmation = %#v, %v; want %s", got, err, test.want)
			}
			if test.want == "saved" && (len(got.Reminders) != 1 || got.Reminders[0].Title != reminder.Title || !got.Reminders[0].DueAt.Equal(reminder.DueAt)) {
				t.Fatalf("confirmation must contain the committed row: %#v", got.Reminders)
			}
			if got.Message == "internal details" {
				t.Fatal("control result details must not be exposed")
			}
		})
	}
	job.CreatedBy = 22
	if _, err := savedOperationStatus(job, binding, 11, lookup); err == nil {
		t.Fatal("a member must not inspect the other member's operation")
	}
	job.CreatedBy = 11
	job.BindingCreatedAt = epoch.Add(-time.Hour)
	if _, err := savedOperationStatus(job, binding, 11, lookup); err == nil {
		t.Fatal("operations from an older binding must not be exposed")
	}
}

func TestOperationConfirmationRejectsChangedOrUnrelatedReminder(t *testing.T) {
	epoch := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	binding := &dbop.Binding{SessionID: "sess_test", UserA: 11, UserB: 22, CreatedAt: epoch}
	job := &dbop.ControlJob{SessionID: binding.SessionID, CreatedBy: 11, BindingCreatedAt: epoch,
		Results: `[{"type":"create_reminder","status":"succeeded","databaseStatus":"saved","memoryStatus":"synced","reminderId":"rem_test"}]`}
	valid := dbop.Reminder{ID: "rem_test", SessionID: binding.SessionID, CreatedBy: 11, Status: dbop.ReminderScheduled, BindingCreatedAt: epoch}
	for _, change := range []func(*dbop.Reminder){
		func(r *dbop.Reminder) { r.ID = "rem_other" },
		func(r *dbop.Reminder) { r.SessionID = "sess_other" },
		func(r *dbop.Reminder) { r.CreatedBy = 22 },
		func(r *dbop.Reminder) { r.BindingCreatedAt = epoch.Add(-time.Hour) },
		func(r *dbop.Reminder) { r.Status = dbop.ReminderCancelled },
	} {
		row := valid
		change(&row)
		got, err := savedOperationStatus(job, binding, 11, func(string) (*dbop.Reminder, error) { return &row, nil })
		if err != nil || got.Status != "pending" || len(got.Reminders) != 0 {
			t.Fatalf("unrelated or changed row must not confirm success: %#v, %v", got, err)
		}
	}
	got, err := savedOperationStatus(nil, binding, 11, func(string) (*dbop.Reminder, error) {
		t.Fatal("a missing job must not query reminders")
		return nil, nil
	})
	if err != nil || got.Status != "pending" || got.Reminders == nil {
		t.Fatalf("missing job = %#v, %v", got, err)
	}
}
