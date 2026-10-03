package dbop

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Impression is a derived view, never evidence for a new memory. Each target
// belongs to one shared session; private branches are excluded from its sources.
type Impression struct {
	SessionID   string     `json:"-" gorm:"primaryKey;index:idx_impression_ready,priority:3;size:160"`
	TargetID    int64      `json:"targetId" gorm:"primaryKey"`
	SourceHash  string     `json:"-" gorm:"size:64"`
	Summary     string     `json:"summary"`
	Status      string     `json:"status" gorm:"index:idx_impression_ready,priority:1;size:32"`
	Error       string     `json:"error,omitempty" gorm:"size:255"`
	RunAt       time.Time  `json:"-" gorm:"index:idx_impression_ready,priority:2;type:datetime(6)"`
	GeneratedAt *time.Time `json:"generatedAt,omitempty" gorm:"type:datetime(6)"`
	UpdatedAt   time.Time  `json:"-" gorm:"type:datetime(6)"`
}

func (Impression) TableName() string { return "impressions" }

type ImpressionSources struct {
	Memories []MemoryRecord
	Messages []Message
}

func (db *DB) ImpressionSources(ctx context.Context, session string, target int64) (ImpressionSources, error) {
	var out ImpressionSources
	// Relevant personal facts and shared observations, without unrelated members'
	// private stores, ephemeral information, task history or assistant speculation.
	err := db.gdb.WithContext(ctx).Select("id", "session_id", "path", "revision", "state", "operation", "category", "owner_id", "scope", "source_user_id", "store_id", "entry_id", "updated_at").Where("session_id=? AND kind='fact' AND category IN ('profile','habit') AND owner_id=? AND state!='deleted' AND operation!='delete' AND (expires_at IS NULL OR expires_at>?)", session, target, time.Now().UTC()).Order("updated_at DESC,id DESC").Limit(80).Find(&out.Memories).Error
	if err != nil {
		return out, err
	}
	err = db.gdb.WithContext(ctx).Where("session_id=? AND sender!='ai' AND user_id>0 AND (visibility IS NULL OR visibility!='private')", session).Order("saved_at DESC,id DESC").Limit(80).Find(&out.Messages).Error
	return out, err
}
func (s ImpressionSources) Hash() string {
	type ref struct {
		ID       string
		Revision int64
	}
	refs := make([]ref, 0, len(s.Memories))
	for _, m := range s.Memories {
		refs = append(refs, ref{m.ID, m.Revision})
	}
	body, _ := json.Marshal(struct {
		Memories []ref
		Messages []Message
	}{refs, s.Messages})
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
func (db *DB) GetImpression(ctx context.Context, session string, target int64) (*Impression, error) {
	var row Impression
	err := db.gdb.WithContext(ctx).Where("session_id=? AND target_id=?", session, target).First(&row).Error
	return firstOrNil(&row, err)
}
func (db *DB) EnsureImpression(ctx context.Context, session string, target int64, hash string, retry bool) (*Impression, error) {
	row := Impression{SessionID: session, TargetID: target, SourceHash: hash, Status: "pending", RunAt: time.Now().UTC()}
	if err := db.gdb.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
		return nil, err
	}
	q := db.gdb.WithContext(ctx).Model(&Impression{}).Where("session_id=? AND target_id=?", session, target)
	if retry {
		q = q.Where("source_hash!=? OR status='failed'", hash)
	} else {
		q = q.Where("source_hash!=?", hash)
	}
	if err := q.Updates(map[string]any{"source_hash": hash, "status": "pending", "error": "", "run_at": time.Now().UTC(), "summary": "", "generated_at": nil}).Error; err != nil {
		return nil, err
	}
	return db.GetImpression(ctx, session, target)
}

// dueImpressions 挑出已到期的画像生成任务；原语句按 run_at 排序没有决胜列，这里补上主键保证领取顺序稳定。
const dueImpressions = `SELECT i.* FROM impressions i WHERE i.status='pending' AND i.run_at<=? AND EXISTS(SELECT 1 FROM bindings b WHERE b.session_id=i.session_id AND (b.user_a=i.target_id OR b.user_b=i.target_id)) ORDER BY i.run_at, i.session_id, i.target_id LIMIT ?`

func (db *DB) ClaimImpressions(ctx context.Context, now time.Time, limit int) ([]Impression, error) {
	if limit <= 0 || limit > 8 {
		limit = 8
	}
	lease := now.Add(3 * time.Minute).UTC()
	var rows []Impression
	err := claimWithLock(ctx, db.gdb, claimLockImpressions, func(tx *gorm.DB) error {
		var due []Impression
		if err := tx.Raw(dueImpressions, now.UTC(), limit).Scan(&due).Error; err != nil {
			return err
		}
		// 主键是 (session_id,target_id) 复合键，只能逐行带状态守卫领取：RowsAffected==1 才说明这条归本次领取。
		for _, row := range due {
			res := tx.Exec(`UPDATE impressions SET status='generating',run_at=? WHERE session_id=? AND target_id=? AND status='pending'`,
				lease, row.SessionID, row.TargetID)
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected != 1 {
				continue
			}
			row.Status, row.RunAt = "generating", lease
			rows = append(rows, row)
		}
		return nil
	})
	return rows, err
}
func (db *DB) FinishImpression(ctx context.Context, job Impression, summary, problem string) error {
	status := "ready"
	var generated *time.Time
	if problem != "" {
		status = "failed"
	} else {
		now := time.Now().UTC()
		generated = &now
	}
	return db.gdb.WithContext(ctx).Model(&Impression{}).Where("session_id=? AND target_id=? AND source_hash=? AND status='generating'", job.SessionID, job.TargetID, job.SourceHash).Updates(map[string]any{"status": status, "summary": summary, "error": problem, "generated_at": generated}).Error
}
func (db *DB) RecoverImpressions(ctx context.Context) error {
	return db.gdb.WithContext(ctx).Model(&Impression{}).Where("status='generating'").Updates(map[string]any{"status": "pending", "run_at": time.Now().UTC()}).Error
}
