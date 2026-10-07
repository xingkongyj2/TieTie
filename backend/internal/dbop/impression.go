package dbop

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Impression is a derived view, never evidence for a new memory. Each target
// belongs to one shared session; private branches are excluded from its sources.
type Impression struct {
	SessionID      string     `json:"-" gorm:"primaryKey;index:idx_impression_ready,priority:3;size:160"`
	TargetID       int64      `json:"targetId" gorm:"primaryKey"`
	SourceHash     string     `json:"-" gorm:"size:64"`
	Summary        string     `json:"summary"`
	Status         string     `json:"status" gorm:"index:idx_impression_ready,priority:1;size:32"`
	Error          string     `json:"error,omitempty" gorm:"size:255"`
	RunAt          time.Time  `json:"-" gorm:"index:idx_impression_ready,priority:2;type:datetime(6)"`
	GeneratedAt    *time.Time `json:"generatedAt,omitempty" gorm:"type:datetime(6)"`
	UpdatedAt      time.Time  `json:"-" gorm:"type:datetime(6)"`
	NextAnalysisAt *time.Time `json:"nextAnalysisAt,omitempty" gorm:"-"`
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
	if !db.enabled() {
		return nil, errNoDB
	}
	var row Impression
	err := db.gdb.WithContext(ctx).Where("session_id=? AND target_id=?", session, target).First(&row).Error
	return firstOrNil(&row, err)
}

var impressionLocation = time.FixedZone("Asia/Shanghai", 8*60*60)

func impressionDay(now time.Time) (time.Time, time.Time) {
	local := now.In(impressionLocation)
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, impressionLocation)
	return start.UTC(), start.AddDate(0, 0, 1).UTC()
}

func hasDailyProfile(user User, now time.Time) bool {
	if user.AIProfileAnalyzedAt == nil || strings.TrimSpace(user.AIProfile) == "" {
		return false
	}
	start, end := impressionDay(now)
	return !user.AIProfileAnalyzedAt.Before(start) && user.AIProfileAnalyzedAt.Before(end)
}

func dailyImpression(user User, session string, now time.Time) *Impression {
	if user.AIProfileSessionID != session || !hasDailyProfile(user, now) {
		return nil
	}
	generated := user.AIProfileAnalyzedAt.UTC()
	return &Impression{SessionID: session, TargetID: user.ID, Summary: user.AIProfile, Status: "ready", GeneratedAt: &generated}
}

// GetDailyImpression only exposes today's successful cache within its source space.
func (db *DB) GetDailyImpression(ctx context.Context, session string, target int64, now time.Time) (*Impression, error) {
	user, err := db.GetUserByID(ctx, target)
	if err != nil || user == nil {
		return nil, err
	}
	return dailyImpression(*user, session, now), nil
}

func impressionJobToken() (string, error) {
	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(token[:]), nil
}

func pendingImpression(row *Impression, now time.Time) *Impression {
	if row.Status == "pending" && row.RunAt.After(now) {
		next := row.RunAt.UTC()
		row.NextAnalysisAt = &next
	}
	return row
}

// EnsureDailyImpression serializes requests on the target user, independently
// of changing source facts. Pending/generating jobs survive midnight unchanged.
func (db *DB) EnsureDailyImpression(ctx context.Context, session string, target int64, retry bool, now time.Time) (*Impression, error) {
	if !db.enabled() {
		return nil, errNoDB
	}
	now = now.UTC().Truncate(time.Microsecond)
	var result *Impression
	err := db.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=?", target).First(&user).Error; err != nil {
			return err
		}
		if cached := dailyImpression(user, session, now); cached != nil {
			result = cached
			return nil
		}
		var existing Impression
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("session_id=? AND target_id=?", session, target).First(&existing).Error
		found := err == nil
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		start, end := impressionDay(now)
		// Upgrade today's existing successful impression without analyzing again.
		if found && existing.Status == "ready" && strings.TrimSpace(existing.Summary) != "" && existing.GeneratedAt != nil && !existing.GeneratedAt.Before(start) && existing.GeneratedAt.Before(end) {
			if !hasDailyProfile(user, now) {
				if err := tx.Model(&User{}).Where("id=?", target).Updates(map[string]any{"ai_profile": existing.Summary, "ai_profile_analyzed_at": existing.GeneratedAt.UTC(), "ai_profile_session_id": session}).Error; err != nil {
					return err
				}
			}
			result = &existing
			return nil
		}
		deferUntilTomorrow := hasDailyProfile(user, now) && user.AIProfileSessionID != session
		if found && (existing.Status == "pending" || existing.Status == "generating") {
			if deferUntilTomorrow && existing.Status == "pending" && existing.RunAt.Before(end) {
				if err := tx.Model(&Impression{}).Where("session_id=? AND target_id=?", session, target).Updates(map[string]any{"run_at": end, "summary": "", "generated_at": nil, "updated_at": now}).Error; err != nil {
					return err
				}
				existing.RunAt, existing.Summary, existing.GeneratedAt = end, "", nil
			}
			result = pendingImpression(&existing, now)
			return nil
		}
		if found && existing.Status == "failed" && !retry && !existing.UpdatedAt.Before(start) && existing.UpdatedAt.Before(end) {
			result = &existing
			return nil
		}
		token, err := impressionJobToken()
		if err != nil {
			return err
		}
		row := Impression{SessionID: session, TargetID: target, SourceHash: token, Status: "pending", RunAt: now, UpdatedAt: now}
		if found {
			row.Summary, row.GeneratedAt = existing.Summary, existing.GeneratedAt
		} else if user.AIProfileSessionID == session {
			row.Summary, row.GeneratedAt = user.AIProfile, user.AIProfileAnalyzedAt
		}
		if deferUntilTomorrow {
			row.RunAt, row.Summary, row.GeneratedAt = end, "", nil
		}
		if found {
			err = tx.Model(&Impression{}).Where("session_id=? AND target_id=?", session, target).Updates(map[string]any{"source_hash": row.SourceHash, "status": row.Status, "error": "", "run_at": row.RunAt, "summary": row.Summary, "generated_at": row.GeneratedAt, "updated_at": now}).Error
		} else {
			err = tx.Create(&row).Error
		}
		if err == nil {
			result = pendingImpression(&row, now)
		}
		return err
	})
	return result, err
}

// Kept for callers being migrated; changing source hashes no longer schedules analysis.
func (db *DB) EnsureImpression(ctx context.Context, session string, target int64, _ string, retry bool) (*Impression, error) {
	return db.EnsureDailyImpression(ctx, session, target, retry, time.Now().UTC())
}

// dueImpressions 挑出已到期的画像生成任务；原语句按 run_at 排序没有决胜列，这里补上主键保证领取顺序稳定。
const dueImpressions = `SELECT i.* FROM impressions i WHERE i.status='pending' AND i.run_at<=? AND EXISTS(SELECT 1 FROM bindings b WHERE b.session_id=i.session_id AND (b.user_a=i.target_id OR b.user_b=i.target_id)) ORDER BY i.run_at, i.session_id, i.target_id LIMIT ?`

func (db *DB) ClaimImpressions(ctx context.Context, now time.Time, limit int) ([]Impression, error) {
	if limit <= 0 || limit > 8 {
		limit = 8
	}
	lease := now.Add(3 * time.Minute).UTC().Truncate(time.Microsecond)
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
	if !db.enabled() {
		return errNoDB
	}
	if problem == "" && strings.TrimSpace(summary) == "" {
		return errors.New("empty impression summary")
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	return db.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Use the same lock ordering as EnsureDailyImpression.
		var user User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=?", job.TargetID).First(&user).Error; err != nil {
			return err
		}
		updates := map[string]any{"status": "failed", "error": problem, "updated_at": now}
		writeProfile := false
		if problem == "" {
			updates = map[string]any{"status": "ready", "summary": summary, "error": "", "generated_at": now, "updated_at": now}
			if cached := dailyImpression(user, job.SessionID, now); cached != nil {
				updates["summary"], updates["generated_at"] = cached.Summary, cached.GeneratedAt
			} else if hasDailyProfile(user, now) {
				_, tomorrow := impressionDay(now)
				token, err := impressionJobToken()
				if err != nil {
					return err
				}
				updates = map[string]any{"status": "pending", "source_hash": token, "summary": "", "error": "", "generated_at": nil, "run_at": tomorrow, "updated_at": now}
			} else {
				writeProfile = true
			}
		}
		res := tx.Model(&Impression{}).Where("session_id=? AND target_id=? AND source_hash=? AND status='generating' AND run_at=?", job.SessionID, job.TargetID, job.SourceHash, job.RunAt.UTC()).Updates(updates)
		if res.Error != nil || res.RowsAffected != 1 {
			return res.Error
		}
		if writeProfile {
			return tx.Model(&User{}).Where("id=?", job.TargetID).Updates(map[string]any{"ai_profile": summary, "ai_profile_analyzed_at": now, "ai_profile_session_id": job.SessionID}).Error
		}
		return nil
	})
}

// FinishCachedImpression settles an existing job without extending the cached
// analysis date, even when the foreground cache read happened before midnight.
func (db *DB) FinishCachedImpression(ctx context.Context, job, cached Impression) error {
	if !db.enabled() {
		return errNoDB
	}
	if cached.SessionID != job.SessionID || cached.TargetID != job.TargetID || cached.GeneratedAt == nil || cached.Status != "ready" || strings.TrimSpace(cached.Summary) == "" {
		return errors.New("invalid cached impression")
	}
	return db.gdb.WithContext(ctx).Model(&Impression{}).Where("session_id=? AND target_id=? AND source_hash=? AND status='generating' AND run_at=?", job.SessionID, job.TargetID, job.SourceHash, job.RunAt.UTC()).Updates(map[string]any{"status": "ready", "summary": cached.Summary, "error": "", "generated_at": cached.GeneratedAt.UTC()}).Error
}
func (db *DB) RecoverImpressions(ctx context.Context) error {
	return db.gdb.WithContext(ctx).Model(&Impression{}).Where("status='generating'").Updates(map[string]any{"status": "pending", "run_at": time.Now().UTC()}).Error
}
