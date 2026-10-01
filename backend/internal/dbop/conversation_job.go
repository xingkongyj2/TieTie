package dbop

import (
	"context"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// MarkConversationPending persists background syncing independently of browser lifetime.
func (db *DB) MarkConversationPending(ctx context.Context, sessionID string, since ...time.Time) error {
	if !db.enabled() {
		return errNoDB
	}
	now := time.Now().UTC()
	if len(since) > 0 {
		now = since[0].UTC()
	}
	return db.gdb.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id"}},
		DoUpdates: clause.Assignments(map[string]any{"conversation_pending": true, "pending_since": now,
			"next_sync_at": now, "conversation_version": gorm.Expr("COALESCE(conversation_version, 0) + 1")}),
	}).Create(&Session{ID: sessionID, ConversationPending: true, PendingSince: now, NextSyncAt: now, ConversationVersion: 1}).Error
}

func (db *DB) ClearConversationPending(ctx context.Context, sessionID string) error {
	if !db.enabled() {
		return errNoDB
	}
	return db.gdb.WithContext(ctx).Model(&Session{}).Where("id = ?", sessionID).
		Update("conversation_pending", false).Error
}

// RejectConversationInput removes only a definitively rejected attempt. A
// previous accepted turn remains queued, and a newer concurrent input is kept.
func (db *DB) RejectConversationInput(ctx context.Context, sessionID string, attemptedAt time.Time, previous *ConversationJob) error {
	if !db.enabled() {
		return errNoDB
	}
	values := map[string]any{"conversation_pending": previous != nil, "next_sync_at": time.Now().UTC()}
	if previous != nil {
		values["pending_since"] = previous.PendingSince
	}
	return db.gdb.WithContext(ctx).Model(&Session{}).
		Where("id = ? AND pending_since = ?", sessionID, attemptedAt.UTC()).Updates(values).Error
}

type ConversationJob struct {
	ID                  string
	ConversationVersion int64
	PendingSince        time.Time
	SyncCursor          string
	SyncOrigin          string
}

// ClaimConversationSync leases only indexed due sessions, with a bounded batch.
// A new message advances the version and schedules its own immediate sync.
func (db *DB) ClaimConversationSync(ctx context.Context, now time.Time, limit int) ([]ConversationJob, error) {
	if !db.enabled() {
		return nil, errNoDB
	}
	if limit <= 0 || limit > 512 {
		limit = 64
	}
	var jobs []ConversationJob
	err := db.gdb.WithContext(ctx).Raw(`UPDATE sessions SET next_sync_at = ? WHERE id IN (
SELECT s.id FROM sessions s INDEXED BY idx_sessions_sync
WHERE s.conversation_pending = ? AND s.next_sync_at <= ?
AND (EXISTS (SELECT 1 FROM bindings b WHERE b.session_id=s.id) OR EXISTS(SELECT 1 FROM private_channels p JOIN bindings b ON b.session_id=p.space_id AND b.created_at=p.binding_created_at WHERE p.session_id=s.id))
ORDER BY s.next_sync_at ASC, s.id ASC LIMIT ?) RETURNING id, conversation_version, pending_since, sync_cursor, sync_origin`,
		now.Add(2*time.Minute).UTC(), true, now.UTC(), limit).Scan(&jobs).Error
	return jobs, err
}

func (db *DB) FinishConversationSync(ctx context.Context, job ConversationJob, finished bool, next time.Time) error {
	if finished {
		job.SyncOrigin = ""
	}
	if !db.enabled() {
		return errNoDB
	}
	return db.gdb.WithContext(ctx).Model(&Session{}).
		Where("id = ? AND conversation_version = ?", job.ID, job.ConversationVersion).
		Updates(map[string]any{"conversation_pending": !finished, "next_sync_at": next.UTC(),
			"sync_cursor": job.SyncCursor, "sync_origin": job.SyncOrigin}).Error
}

func (db *DB) GetConversationJob(ctx context.Context, id string) (*ConversationJob, error) {
	if !db.enabled() {
		return nil, errNoDB
	}
	var job ConversationJob
	err := db.gdb.WithContext(ctx).Model(&Session{}).Where("id = ? AND conversation_pending = ?", id, true).
		Select("id", "conversation_version", "pending_since", "sync_cursor", "sync_origin").First(&job).Error
	return firstOrNil(&job, err)
}
