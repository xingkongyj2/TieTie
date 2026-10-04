package dbop

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"tietie/backend/internal/conversation"

	"gorm.io/gorm"
)

// advanceReminderSeries runs in the same transaction as the transition that
// finished the current occurrence. A deterministic child ID and the parent
// status transition together prevent duplicate future jobs on event replay.
func advanceReminderSeries(tx *gorm.DB, current *Reminder, now time.Time) error {
	if current.Recurrence == nil || current.SeriesID == "" {
		return nil
	}
	anchor := current.DueAt
	if current.RecurrenceStartAt != nil {
		anchor = *current.RecurrenceStartAt
	}
	nextDue, ok := conversation.NextRecurrence(current.Recurrence, anchor, current.DueAt, now)
	if !ok {
		return nil // A finite list of selected dates has ended.
	}
	// Unbinding invalidates the epoch and must never leave a new queued task.
	binding, err := bindingForSession(tx, current.MemorySession())
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if binding == nil || !binding.CreatedAt.Equal(current.BindingCreatedAt) {
		return nil
	}
	hash := sha256.Sum256([]byte(current.ID + "/next"))
	nextID := "rem_" + hex.EncodeToString(hash[:16])
	var existing Reminder
	loadErr := tx.Where("id=?", nextID).Take(&existing).Error
	if loadErr != nil && !errors.Is(loadErr, gorm.ErrRecordNotFound) {
		return loadErr
	}
	next := Reminder{
		ID: nextID, SessionID: current.MemorySession(), MemorySessionID: current.MemorySessionID,
		DeliverySessionID: current.DeliverySessionID, Visibility: current.Visibility,
		Title: current.Title, DueAt: nextDue, RunAt: nextDue,
		Recurrence: current.Recurrence, SeriesID: current.SeriesID,
		Occurrence: current.Occurrence + 1, RecurrenceStartAt: &anchor,
		RecipientIDs: current.RecipientIDs, CreatedBy: current.CreatedBy,
		SourceEventID: current.SourceEventID, ActionIndex: current.ActionIndex,
		Status: ReminderScheduled, TaskStatus: "pending", BindingCreatedAt: current.BindingCreatedAt,
	}
	if next.MemorySessionID != "" {
		next.Visibility = "private"
	}
	if loadErr == nil {
		// Undoing a manual completion hides its generated future card. If the
		// restored parent later fires, reuse that same deterministic child ID.
		updates, revive := deletedOccurrenceUpdates(existing, next)
		if !revive {
			return nil
		}
		if err := tx.Model(&existing).Updates(updates).Error; err != nil {
			return err
		}
		if err := tx.Where("id=?", nextID).First(&existing).Error; err != nil {
			return err
		}
		return syncReminderMemory(tx, &existing)
	}
	if err := tx.Create(&next).Error; err != nil {
		return err
	}
	return syncReminderMemory(tx, &next)
}

func deletedOccurrenceUpdates(existing, next Reminder) (map[string]any, bool) {
	if existing.Status != ReminderDeleted || existing.TaskStatus != "undo_hidden" || existing.SeriesID != next.SeriesID || existing.Occurrence != next.Occurrence {
		return nil, false
	}
	return map[string]any{
		"status":            ReminderScheduled,
		"task_status":       "pending",
		"due_at":            next.DueAt,
		"run_at":            next.RunAt,
		"completed_by":      nil,
		"delivered_at":      nil,
		"task_completed_at": nil,
	}, true
}
