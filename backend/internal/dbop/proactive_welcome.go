package dbop

import (
	"context"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ProactiveWelcome is the durable outbox for the first AI-authored message in
// a newly bound space. The deterministic ID makes retries and simultaneous bind
// requests idempotent.
type ProactiveWelcome struct {
	ID               string    `gorm:"primaryKey;size:220"`
	SessionID        string    `gorm:"not null;index:idx_welcome_session;size:160"`
	BindingCreatedAt time.Time `gorm:"not null;index:idx_welcome_binding;type:datetime(6)"`
	Status           string    `gorm:"not null;index:idx_welcome_ready,priority:1;size:24"`
	RunAt            time.Time `gorm:"not null;index:idx_welcome_ready,priority:2;type:datetime(6)"`
	Attempts         int       `gorm:"not null;default:0"`
	LastError        string    `gorm:"type:text"`
	CreatedAt        time.Time `gorm:"autoCreateTime;type:datetime(6)"`
	UpdatedAt        time.Time `gorm:"autoUpdateTime;type:datetime(6)"`
}

func (ProactiveWelcome) TableName() string { return "proactive_welcome_jobs" }

func ProactiveWelcomeID(sessionID string) string { return "welcome_" + sessionID }

// EnqueueProactiveWelcome inserts one welcome job for a binding epoch. A
// duplicate request never resets a job that is already executing or complete.
func (db *DB) EnqueueProactiveWelcome(ctx context.Context, sessionID string, bindingCreatedAt time.Time) error {
	if !db.enabled() {
		return errNoDB
	}
	now := time.Now().UTC()
	job := ProactiveWelcome{ID: ProactiveWelcomeID(sessionID), SessionID: sessionID, BindingCreatedAt: bindingCreatedAt.UTC(), Status: "pending", RunAt: now, CreatedAt: now, UpdatedAt: now}
	return db.gdb.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&job).Error
}

const dueProactiveWelcomeIDs = `SELECT w.id FROM proactive_welcome_jobs w FORCE INDEX (idx_welcome_ready)
JOIN bindings b ON b.session_id=w.session_id AND b.created_at=w.binding_created_at
WHERE w.status='pending' AND w.run_at<=?
ORDER BY w.run_at,w.id LIMIT ?`

func (db *DB) ClaimProactiveWelcomes(ctx context.Context, now time.Time, limit int) ([]ProactiveWelcome, error) {
	if !db.enabled() {
		return nil, errNoDB
	}
	if limit <= 0 || limit > 512 {
		limit = 64
	}
	lease := now.Add(2 * time.Minute).UTC()
	var jobs []ProactiveWelcome
	err := claimWithLock(ctx, db.gdb, claimLockProactiveWelcomes, func(tx *gorm.DB) error {
		var ids []string
		if err := tx.Raw(dueProactiveWelcomeIDs, now.UTC(), limit).Scan(&ids).Error; err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		if err := tx.Exec(`UPDATE proactive_welcome_jobs SET status='executing', run_at=?, attempts=attempts+1 WHERE id IN (?) AND status='pending'`, lease, ids).Error; err != nil {
			return err
		}
		return tx.Where("id IN ? AND status='executing'", ids).Find(&jobs).Error
	})
	return jobs, err
}

func (db *DB) CompleteProactiveWelcome(ctx context.Context, id string) error {
	if !db.enabled() {
		return errNoDB
	}
	return db.gdb.WithContext(ctx).Model(&ProactiveWelcome{}).Where("id=? AND status='executing'", id).Updates(map[string]any{"status": "completed", "last_error": ""}).Error
}

func (db *DB) RetryProactiveWelcome(ctx context.Context, id string, delay time.Duration, problem error) error {
	if !db.enabled() {
		return errNoDB
	}
	if delay <= 0 {
		delay = 30 * time.Second
	}
	message := ""
	if problem != nil {
		message = problem.Error()
		if len(message) > 2000 {
			message = message[:2000]
		}
	}
	return db.gdb.WithContext(ctx).Model(&ProactiveWelcome{}).Where("id=? AND status='executing'", id).Updates(map[string]any{"status": "pending", "run_at": time.Now().UTC().Add(delay), "last_error": message}).Error
}

func (db *DB) RecoverProactiveWelcomes(ctx context.Context) error {
	if !db.enabled() {
		return errNoDB
	}
	return db.gdb.WithContext(ctx).Model(&ProactiveWelcome{}).Where("status='executing'").Updates(map[string]any{"status": "pending", "run_at": time.Now().UTC()}).Error
}
