package dbop

import (
	"context"
	"encoding/json"
	"strings"
	"tietie/backend/internal/memoryspace"
	"time"

	"gorm.io/gorm"
)

// Restartable local backfill: bound batches, one rewrite per affected page in
// each batch. Original reminders and execution records are never deleted.
func migrateReminderHistory(gdb *gorm.DB) error {
	cursor := ""
	for {
		var rows []Reminder
		query := gdb.Where("id>?", cursor).Where(`NOT EXISTS(SELECT 1 FROM reminder_history_locations l WHERE l.reminder_id=reminders.id AND l.session_id=CASE WHEN reminders.memory_session_id!='' THEN reminders.memory_session_id ELSE reminders.session_id END)
 OR NOT EXISTS(SELECT 1 FROM reminder_history_locations l WHERE l.reminder_id=reminders.id AND l.session_id=reminders.session_id)`)
		if err := query.Order("id ASC").Limit(200).Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			break
		}
		if err := gdb.Transaction(func(tx *gorm.DB) error {
			pages := map[string]ReminderHistoryLocation{}
			epochs := map[string]time.Time{}
			for _, r := range rows {
				sessions := []string{r.MemorySession()}
				if r.SessionID != r.MemorySession() {
					sessions = append(sessions, r.SessionID)
				}
				for _, session := range sessions {
					l, err := ensureHistoryLocation(tx, session, &r)
					if err != nil {
						return err
					}
					pages[MemoryID(session, l.Path())] = *l
					epochs[session] = r.BindingCreatedAt
				}
			}
			for _, l := range pages {
				if err := syncHistoryPage(tx, l, epochs[l.SessionID], true); err != nil {
					return err
				}
			}
			for session, epoch := range epochs {
				if err := syncHistoryIndex(tx, session, epoch, true); err != nil {
					return err
				}
				if err := syncTodoBoard(tx, session, epoch, true); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return err
		}
		cursor = rows[len(rows)-1].ID
	}
	if err := upgradeReminderHistoryPolicy(gdb); err != nil {
		return err
	}
	return retireLegacyReminderDocuments(gdb)
}

func upgradeReminderHistoryPolicy(gdb *gorm.DB) error {
	var records []MemoryRecord
	if err := gdb.Where("kind='template' AND path='rules/memory-policy.json' AND operation='upsert'").Find(&records).Error; err != nil {
		return err
	}
	for _, r := range records {
		var doc map[string]any
		if json.Unmarshal([]byte(r.Content), &doc) != nil {
			continue
		}
		rules, ok := doc["rules"].([]any)
		if !ok {
			continue
		}
		changed := false
		for i, rule := range rules {
			if rule == "tasks/todo-board.json：后台生成的提醒投影；shared/reminders/：完整提醒事实。" {
				rules[i] = "tasks/todo-board.json：当前待办及近期提醒的有界视图，history 内含月份索引；tasks/todo-board/YYYY-MM/NNNNNN.json 按同一待办板模板分页保存所有完整提醒，包括完成与取消记录。"
				changed = true
			}
		}
		if !changed {
			continue
		}
		body, _ := json.MarshalIndent(doc, "", "  ")
		if err := gdb.Transaction(func(tx *gorm.DB) error {
			return queueHistoryDocument(tx, r.SessionID, r.Path, "template", string(body)+"\n", r.BindingCreatedAt, true)
		}); err != nil {
			return err
		}
	}
	return nil
}

// Retire a redundant document only if every reminder it contains has a new
// archive location. Unknown/orphaned legacy content is preserved for inspection.
func retireLegacyReminderDocuments(gdb *gorm.DB) error {
	cursor := ""
	for {
		var records []MemoryRecord
		if err := gdb.Where("id>? AND operation!='delete' AND (path GLOB 'shared/reminders/*.json' OR path GLOB 'tasks/todo-board/*.json')", cursor).Order("id ASC").Limit(200).Find(&records).Error; err != nil {
			return err
		}
		if len(records) == 0 {
			return nil
		}
		if err := gdb.Transaction(func(tx *gorm.DB) error {
			for _, r := range records {
				if memoryspace.IsDocumentPath(r.Path) {
					continue
				}
				var ids []string
				body := r.Content
				if body == "" {
					body = r.PendingContent
				}
				if strings.HasPrefix(r.Path, "shared/reminders/") {
					var fact struct {
						ID string `json:"reminderId"`
					}
					if json.Unmarshal([]byte(body), &fact) != nil || fact.ID == "" {
						continue
					}
					ids = []string{fact.ID}
				} else {
					var page struct {
						Reminders []struct {
							ID string `json:"id"`
						} `json:"reminders"`
					}
					if json.Unmarshal([]byte(body), &page) != nil || len(page.Reminders) == 0 {
						continue
					}
					for _, item := range page.Reminders {
						ids = append(ids, item.ID)
					}
				}
				var covered int64
				if err := tx.Model(&ReminderHistoryLocation{}).Where("session_id=? AND reminder_id IN ?", r.SessionID, ids).Count(&covered).Error; err != nil {
					return err
				}
				if covered != int64(len(ids)) {
					continue
				}
				now := time.Now().UTC()
				if err := tx.Model(&MemoryRecord{}).Where("id=? AND revision=?", r.ID, r.Revision).Updates(map[string]any{"operation": "delete", "archive_retirement": true, "allow_unbound": true, "state": "pending", "pending_content": "", "revision": gorm.Expr("revision+1"), "run_at": now, "updated_at": now}).Error; err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return err
		}
		cursor = records[len(records)-1].ID
	}
}

func (db *DB) ReminderHistoryReady(ctx context.Context, session string) (bool, error) {
	var row MemoryRecord
	err := db.gdb.WithContext(ctx).Select("id").Where("session_id=? AND path >= 'tasks/todo-board/' AND path < 'tasks/todo-board0' AND operation='upsert' AND state!='synced'", session).Take(&row).Error
	if err == gorm.ErrRecordNotFound {
		return true, nil
	}
	return false, err
}

// Moving an inactive archive's mapping and requeueing its complete local pages
// is atomic. A live mounted store is never replaced through this path.
func (db *DB) RecoverReminderArchiveStore(ctx context.Context, previous SpaceMemoryStore, replacement string) error {
	return db.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&SpaceMemoryStore{}).Where("session_id=? AND store_id=?", previous.SessionID, previous.StoreID).Updates(map[string]any{"store_id": replacement, "native_mounted": false})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		now := time.Now().UTC()
		return tx.Model(&MemoryRecord{}).Where("session_id=? AND operation='upsert' AND content!='' AND (path GLOB 'tasks/todo-board/*' OR path='tasks/todo-board.json' OR path='rules/memory-policy.json')", previous.SessionID).Updates(map[string]any{"store_id": replacement, "entry_id": "", "pending_content": gorm.Expr("content"), "state": "pending", "allow_unbound": true, "revision": gorm.Expr("revision+1"), "run_at": now, "updated_at": now}).Error
	})
}
