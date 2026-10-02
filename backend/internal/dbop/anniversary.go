package dbop

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"
	"tietie/backend/internal/memoryspace"
)

type Anniversary struct {
	ID               string    `json:"id" gorm:"primaryKey"`
	SessionID        string    `json:"-" gorm:"not null;uniqueIndex:idx_anniversary_identity,priority:1;index:idx_anniversary_list,priority:1;index:idx_anniversary_featured,priority:1"`
	Title            string    `json:"title" gorm:"not null;uniqueIndex:idx_anniversary_identity,priority:2"`
	Date             string    `json:"date" gorm:"not null;uniqueIndex:idx_anniversary_identity,priority:3"`
	Kind             string    `json:"kind"`
	Pinned           bool      `json:"pinned" gorm:"not null;default:false;index:idx_anniversary_featured,priority:2,sort:desc"`
	CreatedBy        int64     `json:"createdBy"`
	UpdatedBy        int64     `json:"updatedBy"`
	BindingCreatedAt time.Time `json:"-"`
	CreatedAt        time.Time `json:"createdAt" gorm:"index:idx_anniversary_list,priority:2;index:idx_anniversary_featured,priority:3"`
	UpdatedAt        time.Time `json:"updatedAt"`
}

type AnniversaryActionReceipt struct {
	ID            string `gorm:"primaryKey"`
	AnniversaryID string
	CreatedAt     time.Time
}

// Retains the deleted record and a monotonic cursor for clients' loaded pages.
type AnniversaryDeletionReceipt struct {
	ID            uint64 `gorm:"primaryKey;autoIncrement;index:idx_anniversary_deletions,priority:2"`
	RequestKey    string `gorm:"uniqueIndex"`
	SessionID     string `gorm:"index:idx_anniversary_deletions,priority:1"`
	AnniversaryID string `gorm:"index"`
	Snapshot      string
	DeletedBy     int64
	CreatedAt     time.Time
}

var ErrAnniversaryInvalid = errors.New("invalid anniversary")
var ErrAnniversaryNotFound = errors.New("anniversary not found")

func AnniversaryMemoryPath(id string) string {
	return memoryspace.FactPath("agreement", "space", 0, "anniversary_"+id)
}

func AnniversaryIDFromMemoryPath(path string) string {
	prefix := AnniversaryMemoryPath("")
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	return strings.TrimPrefix(path, prefix)
}

func (db *DB) DeleteAnniversary(ctx context.Context, session, request string, actor int64, id string) (*Anniversary, error) {
	if id == "" || len(id) > 160 {
		return nil, ErrAnniversaryInvalid
	}
	var row Anniversary
	err := db.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		binding, err := bindingForSession(tx, session)
		if err != nil {
			return err
		}
		if binding == nil || (actor != binding.UserA && actor != binding.UserB) {
			return ErrReminderForbidden
		}
		key := "anniversary_delete_" + ControlID(session, request)
		var receipt AnniversaryDeletionReceipt
		if err := tx.Where("request_key=? AND session_id=?", key, session).First(&receipt).Error; err == nil {
			if receipt.AnniversaryID != id {
				return ErrAnniversaryInvalid
			}
			return json.Unmarshal([]byte(receipt.Snapshot), &row)
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err := tx.Where("id=? AND session_id=?", id, session).First(&row).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrAnniversaryNotFound
		} else if err != nil {
			return err
		}
		if err := deleteAnniversaryRow(tx, row, key, actor); err != nil {
			return err
		}
		// Commit the database deletion and durable memory tombstone together.
		return enqueueMemory(tx, MemoryRecord{SessionID: session, Path: AnniversaryMemoryPath(id), Scope: "space", Category: "agreement", SourceUserID: actor, SourceRequestID: request, Storage: "database_and_memory", Operation: "delete", BindingCreatedAt: binding.CreatedAt})
	})
	return &row, err
}

func deleteAnniversaryRow(tx *gorm.DB, row Anniversary, request string, actor int64) error {
	body, err := json.Marshal(row)
	if err != nil {
		return err
	}
	if err := tx.Where("id=? AND session_id=?", row.ID, row.SessionID).Delete(&Anniversary{}).Error; err != nil {
		return err
	}
	return tx.Create(&AnniversaryDeletionReceipt{RequestKey: request, SessionID: row.SessionID, AnniversaryID: row.ID, Snapshot: string(body), DeletedBy: actor}).Error
}

// A bounded delta removes deleted records from all pages already loaded by a client.
func (db *DB) AnniversaryDeletionChanges(ctx context.Context, session, after string) ([]string, string, bool, error) {
	var since uint64
	if after != "" {
		var err error
		since, err = strconv.ParseUint(after, 10, 64)
		if err != nil {
			return nil, "", false, ErrAnniversaryInvalid
		}
	}
	var newest AnniversaryDeletionReceipt
	if err := db.gdb.WithContext(ctx).Where("session_id=?", session).Order("id DESC").Limit(1).Find(&newest).Error; err != nil {
		return nil, "", false, err
	}
	cursor := strconv.FormatUint(newest.ID, 10)
	removed := []string{}
	if after == "" {
		return removed, cursor, false, nil
	}
	var rows []AnniversaryDeletionReceipt
	if err := db.gdb.WithContext(ctx).Select("id", "anniversary_id").Where("session_id=? AND id>? AND id<=?", session, since, newest.ID).Order("id ASC").Limit(201).Find(&rows).Error; err != nil {
		return nil, "", false, err
	}
	if len(rows) > 200 {
		return removed, cursor, true, nil
	}
	for _, row := range rows {
		removed = append(removed, row.AnniversaryID)
	}
	return removed, cursor, false, nil
}

// Fix the previous generic-memory deletion path, without touching live facts.
func reconcileDeletedAnniversaries(gdb *gorm.DB) error {
	for {
		var rows []Anniversary
		if err := gdb.Table("anniversaries a").Select("a.*").Joins("JOIN memory_records m ON m.session_id=a.session_id AND m.path=? || a.id", AnniversaryMemoryPath("")).Where("m.kind='fact' AND m.operation='delete' AND m.binding_created_at=a.binding_created_at").Limit(200).Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		if err := gdb.Transaction(func(tx *gorm.DB) error {
			for _, row := range rows {
				if err := deleteAnniversaryRow(tx, row, "anniversary_repair_"+MemoryID(row.SessionID, AnniversaryMemoryPath(row.ID)), row.UpdatedBy); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return err
		}
	}
}

func (db *DB) ApplyAnniversary(ctx context.Context, session, request string, actor int64, id, title, date, kind string) (*Anniversary, error) {
	title = strings.TrimSpace(title)
	if kind == "" {
		kind = "other"
	}
	if _, ok := memoryspace.AnniversaryKindLabel(kind); !ok || title == "" || utf8.RuneCountInString(title) > 80 || !memoryspace.ValidAnniversaryDate(date) {
		return nil, ErrAnniversaryInvalid
	}
	var row Anniversary
	err := db.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		binding, err := bindingForSession(tx, session)
		if err != nil {
			return err
		}
		if binding == nil || (actor != binding.UserA && actor != binding.UserB) {
			return ErrReminderForbidden
		}
		receiptID := "anniversary_" + ControlID(session, request)
		var receipt AnniversaryActionReceipt
		if err := tx.Where("id=?", receiptID).First(&receipt).Error; err == nil {
			return tx.Where("id=? AND session_id=?", receipt.AnniversaryID, session).First(&row).Error
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if id != "" {
			if err := tx.Where("id=? AND session_id=?", id, session).First(&row).Error; errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAnniversaryNotFound
			} else if err != nil {
				return err
			}
		} else {
			if err := tx.Where("session_id=? AND title=? AND date=?", session, title, date).First(&row).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		if row.ID == "" {
			row = Anniversary{ID: "ann_" + strings.TrimPrefix(ControlID(session, request), "ctl_"), SessionID: session, CreatedBy: actor, BindingCreatedAt: binding.CreatedAt}
		}
		if row.Title != title || row.Date != date || row.Kind != kind {
			row.Title, row.Date, row.Kind, row.UpdatedBy = title, date, kind, actor
			if err := tx.Save(&row).Error; err != nil {
				return err
			}
			if err := syncAnniversary(tx, row); err != nil {
				return err
			}
			// A newly added or corrected date must also be checked today, even
			// if the daily scan has already completed. Report IDs deduplicate it.
			due := anniversaryReminderTime(time.Now())
			if err := tx.Model(&AnniversaryReminderSettings{}).Where("session_id=? AND binding_created_at=? AND enabled=1", session, binding.CreatedAt).Updates(map[string]any{"next_due": due, "run_at": due, "token": "", "revision": gorm.Expr("revision + 1")}).Error; err != nil {
				return err
			}
		}
		return tx.Create(&AnniversaryActionReceipt{ID: receiptID, AnniversaryID: row.ID}).Error
	})
	return &row, err
}

func syncAnniversary(tx *gorm.DB, row Anniversary) error {
	label, _ := memoryspace.AnniversaryKindLabel(row.Kind)
	content, err := json.Marshal(map[string]any{"entryType": "anniversary", "anniversaryId": row.ID, "title": row.Title, "date": row.Date, "anniversaryKind": row.Kind, "pinned": row.Pinned, "content": "「" + row.Title + "」的日期是 " + row.Date + "，类型：" + label + "。", "sourceType": "anniversary", "createdBy": row.CreatedBy, "updatedBy": row.UpdatedBy, "confirmation": "用户明确委托保存", "updatedAt": row.UpdatedAt})
	if err != nil {
		return err
	}
	return enqueueMemory(tx, MemoryRecord{SessionID: row.SessionID, Path: AnniversaryMemoryPath(row.ID), Scope: "space", Category: "agreement", SourceUserID: row.UpdatedBy, Storage: "database_and_memory", Operation: "upsert", PendingContent: string(content), BindingCreatedAt: row.BindingCreatedAt})
}

// Keyset pagination uses an immutable creation time; no full history scan.
func (db *DB) ListAnniversaries(ctx context.Context, session, after string, limit int) ([]Anniversary, string, error) {
	if limit <= 0 || limit > 50 {
		limit = 50
	}
	query := db.gdb.WithContext(ctx).Where("session_id=?", session)
	if after != "" {
		var cursor Anniversary
		if err := db.gdb.WithContext(ctx).Where("id=? AND session_id=?", after, session).First(&cursor).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			// Deleting a page boundary must not invalidate the remaining history.
			var deleted AnniversaryDeletionReceipt
			if err := db.gdb.WithContext(ctx).Where("anniversary_id=? AND session_id=?", after, session).First(&deleted).Error; err != nil {
				return nil, "", ErrAnniversaryInvalid
			}
			if err := json.Unmarshal([]byte(deleted.Snapshot), &cursor); err != nil {
				return nil, "", err
			}
		} else if err != nil {
			return nil, "", err
		}
		query = query.Where("created_at<? OR (created_at=? AND id<?)", cursor.CreatedAt, cursor.CreatedAt, cursor.ID)
	}
	rows := []Anniversary{}
	if err := query.Order("created_at DESC,id DESC").Limit(limit + 1).Find(&rows).Error; err != nil {
		return nil, "", err
	}
	next := ""
	if len(rows) > limit {
		rows = rows[:limit]
		next = rows[len(rows)-1].ID
	}
	return rows, next, nil
}

func (db *DB) FeaturedAnniversary(ctx context.Context, session string) (*Anniversary, error) {
	var row Anniversary
	err := db.gdb.WithContext(ctx).Where("session_id=? AND pinned=1", session).First(&row).Error
	return firstOrNil(&row, err)
}

func (db *DB) PinAnniversary(ctx context.Context, session, id string, actor int64, pinned bool) (*Anniversary, error) {
	var target Anniversary
	err := db.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		binding, err := bindingForSession(tx, session)
		if err != nil {
			return err
		}
		if binding == nil || (actor != binding.UserA && actor != binding.UserB) {
			return ErrReminderForbidden
		}
		if err := tx.Where("id=? AND session_id=?", id, session).First(&target).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrAnniversaryNotFound
		} else if err != nil {
			return err
		}
		if target.Pinned == pinned {
			return nil
		}
		if pinned {
			var previous []Anniversary
			if err := tx.Where("session_id=? AND pinned=1", session).Find(&previous).Error; err != nil {
				return err
			}
			for _, row := range previous {
				row.Pinned, row.UpdatedBy = false, actor
				if err := tx.Save(&row).Error; err != nil {
					return err
				}
				if err := syncAnniversary(tx, row); err != nil {
					return err
				}
			}
		}
		target.Pinned, target.UpdatedBy = pinned, actor
		if err := tx.Save(&target).Error; err != nil {
			return err
		}
		return syncAnniversary(tx, target)
	})
	return &target, err
}
