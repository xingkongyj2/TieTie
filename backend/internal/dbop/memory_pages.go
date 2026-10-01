package dbop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"strings"
	"tietie/backend/internal/memoryspace"
	"time"
)

const memoryPageBudget = 80 * 1024

type MemoryPageLocation struct {
	MemoryID      string `gorm:"primaryKey"`
	SessionID     string `gorm:"index:idx_fact_page,priority:1"`
	Category      string `gorm:"index:idx_fact_page,priority:2"`
	Month         string `gorm:"index:idx_fact_page,priority:3"`
	Page          int    `gorm:"index:idx_fact_page,priority:4"`
	ReservedBytes int
}

func (MemoryPageLocation) TableName() string { return "memory_page_locations" }
func (l MemoryPageLocation) Path() string {
	return fmt.Sprintf("%s/%s/%06d.json", strings.TrimSuffix(memoryspace.TemplatePath(l.Category), ".json"), l.Month, l.Page)
}

type MemoryPage struct {
	SessionID     string `gorm:"primaryKey"`
	Category      string `gorm:"primaryKey"`
	Month         string `gorm:"primaryKey"`
	Page          int    `gorm:"primaryKey"`
	RecordCount   int
	ReservedBytes int
}

func (MemoryPage) TableName() string { return "memory_pages" }

// Revisions preserve corrections and pending work, independently of cloud pages.
type MemoryRevision struct {
	MemoryID  string `gorm:"primaryKey"`
	Revision  int64  `gorm:"primaryKey"`
	SessionID string `gorm:"index:idx_fact_versions,priority:1"`
	Content   string
	Operation string
	CreatedAt time.Time
}

func (MemoryRevision) TableName() string { return "memory_revisions" }

func materializeFact(tx *gorm.DB, r MemoryRecord) error {
	if r.Operation == "delete" {
		return rebuildFactPage(tx, r)
	}
	body := r.Content
	if body == "" {
		body = r.PendingContent
	}
	// Old memory_only entries are hydrated by the worker before migration.
	if body == "" {
		return nil
	}
	if factReservation(r.Category, body) > memoryPageBudget {
		return errors.New("memory fact exceeds page capacity")
	}
	var previous MemoryPageLocation
	err := tx.Where("memory_id=?", r.ID).First(&previous).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	size := factReservation(r.Category, body)
	location := previous
	if err == nil {
		var p MemoryPage
		if err := tx.Where("session_id=? AND category=? AND month=? AND page=?", previous.SessionID, previous.Category, previous.Month, previous.Page).First(&p).Error; err != nil {
			return err
		}
		if p.ReservedBytes-previous.ReservedBytes+size <= memoryPageBudget {
			if err := tx.Model(&p).UpdateColumn("reserved_bytes", p.ReservedBytes-previous.ReservedBytes+size).Error; err != nil {
				return err
			}
			location.ReservedBytes = size
		} else {
			location = MemoryPageLocation{}
		}
	}
	if location.MemoryID == "" {
		month := r.CreatedAt.In(time.FixedZone("Asia/Shanghai", 8*3600)).Format("2006-01")
		var p MemoryPage
		e := tx.Where("session_id=? AND category=? AND month=?", r.SessionID, r.Category, month).Order("page DESC").First(&p).Error
		if e != nil && !errors.Is(e, gorm.ErrRecordNotFound) {
			return e
		}
		if e != nil || p.RecordCount >= 8 || p.ReservedBytes+size > memoryPageBudget {
			next := p.Page + 1
			p = MemoryPage{SessionID: r.SessionID, Category: r.Category, Month: month, Page: next}
			if e := tx.Create(&p).Error; e != nil {
				return e
			}
		}
		if err := tx.Model(&p).Updates(map[string]any{"record_count": p.RecordCount + 1, "reserved_bytes": p.ReservedBytes + size}).Error; err != nil {
			return err
		}
		location = MemoryPageLocation{MemoryID: r.ID, SessionID: r.SessionID, Category: r.Category, Month: month, Page: p.Page, ReservedBytes: size}
	}
	if err := tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&location).Error; err != nil {
		return err
	}
	if err := tx.Model(&MemoryRecord{}).Where("id=?", r.ID).UpdateColumn("cloud_path", location.Path()).Error; err != nil {
		return err
	}
	if previous.MemoryID != "" && previous.Path() != location.Path() {
		if err := syncFactPage(tx, previous, r.BindingCreatedAt, r.AllowUnbound); err != nil {
			return err
		}
	}
	return syncFactPage(tx, location, r.BindingCreatedAt, r.AllowUnbound)
}
func rebuildFactPage(tx *gorm.DB, r MemoryRecord) error {
	var l MemoryPageLocation
	err := tx.Where("memory_id=?", r.ID).First(&l).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return syncFactPage(tx, l, r.BindingCreatedAt, r.AllowUnbound)
}
func syncFactPage(tx *gorm.DB, l MemoryPageLocation, epoch time.Time, unbound bool) error {
	var facts []MemoryRecord
	err := tx.Table("memory_page_locations l").Select("m.*").Joins("JOIN memory_records m ON m.id=l.memory_id").Where("l.session_id=? AND l.category=? AND l.month=? AND l.page=? AND m.operation!='delete' AND (m.expires_at IS NULL OR m.expires_at>?)", l.SessionID, l.Category, l.Month, l.Page, time.Now().UTC()).Order("m.updated_at ASC,m.id ASC").Limit(8).Find(&facts).Error
	if err != nil {
		return err
	}
	raw := make([]json.RawMessage, 0, len(facts))
	for _, f := range facts {
		var doc map[string]any
		if err := json.Unmarshal([]byte(f.Content), &doc); err != nil {
			doc = map[string]any{"content": f.Content}
		}
		delete(doc, "kind")
		delete(doc, "schemaVersion")
		doc["memoryKey"], doc["revision"] = f.ID, f.Revision
		for key, value := range map[string]any{"ownerId": f.OwnerID, "sourceUserId": f.SourceUserID, "sourceRequestId": f.SourceRequestID, "scope": f.Scope} {
			doc[key] = value
		}
		// Flatten the template's known fields while keeping source text and metadata.
		if data, ok := doc["data"].(map[string]any); ok {
			for k, v := range data {
				if k != "content" {
					doc[k] = v
				}
			}
		}
		delete(doc, "data")
		if f.ExpiresAt != nil {
			doc["expiresAt"], doc["expired"] = f.ExpiresAt, false
		}
		b, _ := json.Marshal(doc)
		raw = append(raw, b)
	}
	path := memoryspace.TemplatePath(l.Category)
	var root MemoryRecord
	base, e := memoryspace.Template(path)
	if strings.Contains(base, "{{") {
		binding, err := bindingForSession(tx, l.SessionID)
		if err != nil {
			return err
		}
		if binding != nil {
			var users []User
			if err := tx.Select("id", "username").Where("id IN ?", []int64{binding.UserA, binding.UserB}).Find(&users).Error; err != nil {
				return err
			}
			names := map[int64]string{}
			for _, u := range users {
				names[u.ID] = u.Username
			}
			docs, err := memoryspace.Render(l.SessionID, "", memoryspace.Member{ID: binding.UserA, Name: names[binding.UserA]}, memoryspace.Member{ID: binding.UserB, Name: names[binding.UserB]})
			if err != nil {
				return err
			}
			for _, doc := range docs {
				if doc.Path == path {
					base = doc.Content
				}
			}
		}
	}
	if e != nil {
		return e
	}
	if err := tx.Where("id=?", MemoryID(l.SessionID, path)).First(&root).Error; err == nil {
		base = root.Content
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	body, e := memoryspace.Page(base, l.Month, l.Page, l.Category, raw)
	if e != nil {
		return e
	}
	return queueHistoryDocument(tx, l.SessionID, l.Path(), "projection", body, epoch, unbound)
}

func (db *DB) HydrateMemoryFact(ctx context.Context, r MemoryRecord, body string) error {
	if r.Category == "" {
		switch {
		case strings.HasPrefix(r.Path, "profile/habits/"):
			r.Category = "habit"
		case strings.HasPrefix(r.Path, "agreements/"):
			r.Category = "agreement"
		case strings.HasPrefix(r.Path, "context/"):
			r.Category = "realtime"
		case strings.HasPrefix(r.Path, "rules/additions/"):
			r.Category = "behavior"
		default:
			r.Category = "profile"
		}
	}
	if strings.HasPrefix(r.Path, "profile/observations/") {
		var fact map[string]any
		if err := json.Unmarshal([]byte(body), &fact); err != nil {
			return err
		}
		fact["sourceType"], fact["confirmation"] = "role_supplement", "已确认"
		value, _ := json.Marshal(fact)
		body = string(value)
	}
	if !json.Valid([]byte(body)) {
		return errors.New("invalid legacy fact")
	}
	return db.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&MemoryRecord{}).Where("id=? AND revision=?", r.ID, r.Revision).Updates(map[string]any{"content": body, "pending_content": body, "storage": "database_and_memory", "category": r.Category}).Error; err != nil {
			return err
		}
		r.Content, r.PendingContent = body, body
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "memory_id"}, {Name: "revision"}}, DoUpdates: clause.Assignments(map[string]any{"content": gorm.Expr("CASE WHEN memory_revisions.content='' THEN excluded.content ELSE memory_revisions.content END")})}).Create(&MemoryRevision{MemoryID: r.ID, Revision: r.Revision, SessionID: r.SessionID, Content: body, Operation: r.Operation}).Error; err != nil {
			return err
		}
		return materializeFact(tx, r)
	})
}
func (db *DB) FactProjection(ctx context.Context, r MemoryRecord) (*MemoryRecord, error) {
	var l MemoryPageLocation
	err := db.gdb.WithContext(ctx).Where("memory_id=?", r.ID).First(&l).Error
	if err != nil {
		return nil, err
	}
	return db.GetMemoryRecord(ctx, MemoryID(r.SessionID, l.Path()), r.SessionID)
}

func factReservation(category, body string) int {
	multiplier := 1
	if category == "profile" {
		multiplier = 2
	}
	return multiplier*len(body) + 1024
}

func (db *DB) GetMemoryRevision(ctx context.Context, session, key string, revision int64) (*MemoryRevision, error) {
	var row MemoryRevision
	err := db.gdb.WithContext(ctx).Where("session_id=? AND memory_id=? AND revision=?", session, key, revision).First(&row).Error
	return firstOrNil(&row, err)
}
