package dbop

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"tietie/backend/internal/conversation"
	"time"
)

type ControlJob struct {
	ID               string `gorm:"primaryKey;index:idx_controls_ready,priority:3"`
	SessionID        string `gorm:"not null;index:idx_controls_session;index:idx_controls_space_status,priority:1"`
	RequestID        string `gorm:"not null"`
	Origin           string `gorm:"not null"`
	SourceEventID    string
	Actions          string
	Results          string
	CreatedBy        int64
	BindingCreatedAt time.Time
	Status           string    `gorm:"index:idx_controls_ready,priority:1;index:idx_controls_space_status,priority:2"`
	RunAt            time.Time `gorm:"index:idx_controls_ready,priority:2"`
	ReceiptEventIDs  []string  `gorm:"serializer:json"`
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

func (ControlJob) TableName() string { return "control_jobs" }
func ControlID(session, request string) string {
	sum := sha256.Sum256([]byte(session + "/" + request))
	return "ctl_" + hex.EncodeToString(sum[:16])
}
func (db *DB) EnqueueControl(ctx context.Context, id, request, origin, event string, actions []conversation.Action, author int64, binding time.Time) error {
	payload, err := json.Marshal(actions)
	if err != nil {
		return err
	}
	job := ControlJob{ID: ControlID(id, request), SessionID: id, RequestID: request, Origin: origin, SourceEventID: event, Actions: string(payload), CreatedBy: author, BindingCreatedAt: binding, Status: "pending", RunAt: time.Now().UTC()}
	return db.gdb.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&job).Error
}
func (db *DB) GetControl(ctx context.Context, id, request string) (*ControlJob, error) {
	var j ControlJob
	err := db.gdb.WithContext(ctx).Where("id = ?", ControlID(id, request)).First(&j).Error
	return firstOrNil(&j, err)
}
func (db *DB) ClaimControls(ctx context.Context, now time.Time, limit int) ([]ControlJob, error) {
	if limit <= 0 || limit > 512 {
		limit = 64
	}
	var jobs []ControlJob
	err := db.gdb.WithContext(ctx).Raw(`UPDATE control_jobs SET status='executing', run_at=? WHERE id IN (
 SELECT c.id FROM control_jobs c INDEXED BY idx_controls_ready
 WHERE c.status='pending' AND c.run_at<=? AND (EXISTS (SELECT 1 FROM bindings b WHERE b.session_id=c.session_id AND b.created_at=c.binding_created_at) OR EXISTS(SELECT 1 FROM private_channels p JOIN bindings b ON b.session_id=p.space_id AND b.created_at=p.binding_created_at WHERE p.session_id=c.session_id AND b.created_at=c.binding_created_at))
 ORDER BY c.run_at,c.id LIMIT ?) RETURNING *`, now.Add(2*time.Minute).UTC(), now.UTC(), limit).Scan(&jobs).Error
	return jobs, err
}
func (db *DB) SaveControlResults(ctx context.Context, j ControlJob, results []conversation.ActionResult) error {
	b, err := json.Marshal(results)
	if err != nil {
		return err
	}
	return db.gdb.WithContext(ctx).Model(&ControlJob{}).Where("id = ? AND status = 'executing'", j.ID).Update("results", string(b)).Error
}
func (db *DB) BeginControlReceipt(ctx context.Context, id string) error {
	return db.gdb.WithContext(ctx).Model(&ControlJob{}).Where("id = ? AND status='executing'", id).Update("status", "sending").Error
}
func (db *DB) AcceptControlReceipt(ctx context.Context, id string, eventIDs []string) error {
	b, _ := json.Marshal(eventIDs)
	return db.gdb.WithContext(ctx).Model(&ControlJob{}).Where("id = ? AND status IN ?", id, []string{"sending", "uncertain"}).Updates(map[string]any{"status": "awaiting_reply", "receipt_event_ids": string(b)}).Error
}
func (db *DB) CompleteControl(ctx context.Context, id string) error {
	var job ControlJob
	if err := db.gdb.WithContext(ctx).Where("id = ?", id).First(&job).Error; err != nil {
		return err
	}
	var results []conversation.ActionResult
	if err := json.Unmarshal([]byte(job.Results), &results); err != nil {
		return err
	}
	for i := range results {
		results[i].Memories = nil
	}
	sanitized, _ := json.Marshal(results)
	return db.gdb.WithContext(ctx).Model(&ControlJob{}).Where("id = ? AND status IN ?", id, []string{"awaiting_reply", "uncertain", "sending"}).Updates(map[string]any{"status": "completed", "origin": "", "actions": "", "results": string(sanitized)}).Error
}
func (db *DB) RetryControl(ctx context.Context, id string, uncertain bool) error {
	status := "pending"
	if uncertain {
		status = "uncertain"
	}
	return db.gdb.WithContext(ctx).Model(&ControlJob{}).Where("id = ? AND status IN ?", id, []string{"executing", "sending"}).Updates(map[string]any{"status": status, "run_at": time.Now().Add(15 * time.Second).UTC()}).Error
}
func (db *DB) RecoverControls(ctx context.Context) error {
	return db.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&ControlJob{}).Where("status='executing'").Updates(map[string]any{"status": "pending", "run_at": time.Now().UTC()}).Error; err != nil {
			return err
		}
		return tx.Model(&ControlJob{}).Where("status='sending'").Update("status", "uncertain").Error
	})
}
func (db *DB) AbandonControl(ctx context.Context, id string) error {
	return db.gdb.WithContext(ctx).Model(&ControlJob{}).Where("id = ?", id).Updates(map[string]any{"status": "cancelled", "origin": "", "actions": ""}).Error
}
func (db *DB) HasActiveControl(ctx context.Context, session string) (bool, error) {
	var exists bool
	err := db.gdb.WithContext(ctx).Raw("SELECT EXISTS(SELECT 1 FROM control_jobs WHERE session_id=? AND status IN ('pending','executing','sending'))", session).Scan(&exists).Error
	return exists, err
}
