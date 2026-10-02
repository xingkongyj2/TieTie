package dbop

import (
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"tietie/backend/internal/memoryspace"
	"time"
)

// Upgrade each store once. Bodies that lived only in the cloud are read by the
// worker before its old document is removed; failures leave the old entry intact.
func migrateTemplateSchemas(tx *gorm.DB) error {
	var stores []SpaceMemoryStore
	if err := tx.Where("template_version IS NULL OR template_version<?", memoryspace.Version).Find(&stores).Error; err != nil {
		return err
	}
	for _, store := range stores {
		if err := tx.Transaction(func(g *gorm.DB) error {
			binding, err := bindingForSession(g, store.SessionID)
			if err != nil {
				return err
			}
			if binding == nil {
				var row Binding
				origin := store.SessionID
				var channel PrivateChannel
				if err := g.Where("session_id=?", store.SessionID).Take(&channel).Error; err == nil {
					origin = channel.SpaceID
				} else if !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
				err := g.Table("archived_bindings").Where("session_id=?", origin).Take(&row).Error
				if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
				binding = &row
			}
			epoch := binding.CreatedAt
			var users []User
			if err := g.Select("id", "username").Where("id IN ?", []int64{binding.UserA, binding.UserB}).Find(&users).Error; err != nil {
				return err
			}
			names := map[int64]string{}
			for _, u := range users {
				names[u.ID] = u.Username
			}
			docs, err := memoryspace.Render(store.SessionID, store.SpaceID, memoryspace.Member{ID: binding.UserA, Name: names[binding.UserA]}, memoryspace.Member{ID: binding.UserB, Name: names[binding.UserB]})
			if err != nil {
				return err
			}
			for _, doc := range docs {
				var old MemoryRecord
				if e := g.Where("id=?", MemoryID(store.SessionID, doc.Path)).First(&old).Error; e == nil {
					var existing, fresh map[string]any
					if json.Unmarshal([]byte(old.Content), &existing) == nil && json.Unmarshal([]byte(doc.Content), &fresh) == nil {
						for _, key := range []string{"sharedEntries", "agreements", "entries", "additions", "instructions", "speakingStyle", "styleUpdatedBy", "styleUpdatedAt", "groupChatId", "memorySpaceId", "memberIds", "timezone", "groupDefaultRules"} {
							if value, ok := existing[key]; ok {
								fresh[key] = value
							}
						}
						if previous, ok := existing["users"].([]any); ok {
							for _, u := range fresh["users"].([]any) {
								target := u.(map[string]any)
								for _, p := range previous {
									prior := p.(map[string]any)
									if prior["userId"] == target["userId"] {
										for k, v := range prior {
											target[k] = v
										}
									}
								}
							}
						}
						body, _ := json.Marshal(fresh)
						doc.Content = string(body)
					}
				} else if !errors.Is(e, gorm.ErrRecordNotFound) {
					return e
				}
				if err := queueHistoryDocument(g, store.SessionID, doc.Path, "template", doc.Content, epoch, true); err != nil {
					return err
				}
			}
			// Rebuild historical reminders into the same todo_board schema and keep the
			// bounded month index in the root. Each old archive page is safely retired.
			var locations []ReminderHistoryLocation
			if err := g.Where("session_id=?", store.SessionID).Select("session_id,month,page").Group("session_id,month,page").Find(&locations).Error; err != nil {
				return err
			}
			for _, l := range locations {
				if err := syncHistoryPage(g, l, epoch, true); err != nil {
					return err
				}
			}
			if err := syncTodoBoard(g, store.SessionID, epoch, true); err != nil {
				return err
			}
			now := time.Now().UTC()
			if err := g.Model(&MemoryRecord{}).Where("session_id=? AND kind='fact' AND operation='upsert' AND state!='deleted'", store.SessionID).Updates(map[string]any{"state": "pending", "run_at": now, "allow_unbound": true, "binding_created_at": epoch}).Error; err != nil {
				return err
			}
			var obsolete []MemoryRecord
			if err := g.Where("session_id=? AND kind IN ('template','projection') AND operation!='delete'", store.SessionID).Find(&obsolete).Error; err != nil {
				return err
			}
			for _, r := range obsolete {
				if memoryspace.IsDocumentPath(r.Path) {
					continue
				}
				if err := g.Model(&MemoryRecord{}).Where("id=?", r.ID).Updates(map[string]any{"operation": "delete", "archive_retirement": true, "allow_unbound": true, "state": "pending", "pending_content": "", "revision": gorm.Expr("revision+1"), "run_at": now}).Error; err != nil {
					return err
				}
			}
			return g.Model(&SpaceMemoryStore{}).Where("session_id=?", store.SessionID).Update("template_version", memoryspace.Version).Error
		}); err != nil {
			return err
		}
	}
	return nil
}
