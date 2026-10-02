package dbop

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"gorm.io/gorm"
	"tietie/backend/internal/memoryspace"
)

var ErrInvalidTone = errors.New("invalid assistant speaking style")

func (db *DB) GetAssistantStyle(ctx context.Context, session string) (memoryspace.SpeakingStyle, error) {
	record, err := db.GetMemoryRecord(ctx, MemoryID(session, memoryspace.BehaviorPath), session)
	if err != nil {
		return memoryspace.SpeakingStyle{}, err
	}
	tone := "warm"
	if record != nil && record.Content != "" {
		var doc struct {
			SpeakingStyle memoryspace.SpeakingStyle `json:"speakingStyle"`
		}
		if err := json.Unmarshal([]byte(record.Content), &doc); err != nil {
			return memoryspace.SpeakingStyle{}, err
		}
		if doc.SpeakingStyle.Tone != "" {
			tone = doc.SpeakingStyle.Tone
		}
	}
	style, ok := memoryspace.Style(tone)
	if !ok {
		return style, ErrInvalidTone
	}
	return style, nil
}

// Preserve the existing protocol, additions and history index. Commit every
// active channel in one transaction; an unbound or foreign space cannot change.
func (db *DB) SaveAssistantStyle(ctx context.Context, session string, actor int64, tone string) error {
	style, ok := memoryspace.Style(tone)
	if !ok {
		return ErrInvalidTone
	}
	return db.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var binding Binding
		if err := tx.Where("session_id=? AND (user_a=? OR user_b=?)", session, actor, actor).First(&binding).Error; err != nil {
			return err
		}
		sessions := []string{session}
		var channels []PrivateChannel
		if err := tx.Where("space_id=? AND binding_created_at=?", session, binding.CreatedAt).Find(&channels).Error; err != nil {
			return err
		}
		for _, channel := range channels {
			sessions = append(sessions, channel.SessionID)
		}
		for _, id := range sessions {
			if err := writeAssistantStyle(tx, id, binding.CreatedAt, style, actor); err != nil {
				return err
			}
		}
		return nil
	})
}

func writeAssistantStyle(tx *gorm.DB, session string, epoch time.Time, style memoryspace.SpeakingStyle, actor int64) error {
	var root MemoryRecord
	err := tx.Where("id=?", MemoryID(session, memoryspace.BehaviorPath)).First(&root).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	body := root.Content
	if body == "" {
		body = memoryspace.Behavior()
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return err
	}
	current, _ := doc["speakingStyle"].(map[string]any)
	if current["tone"] == style.Tone && current["instructions"] == style.Instructions && current["label"] == style.Label && root.ID != "" {
		return nil
	}
	doc["speakingStyle"] = style
	doc["styleUpdatedBy"], doc["styleUpdatedAt"] = actor, time.Now().UTC()
	content, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	if err := queueHistoryDocument(tx, session, memoryspace.BehaviorPath, "template", string(content), epoch, false); err != nil {
		return err
	}
	// One stable behavior fact retains change history using the existing schema
	// and same-template pages; the root holds only the current setting.
	fact, err := json.Marshal(map[string]any{"content": "说话方式：" + style.Label + "。" + style.Instructions, "sourceType": "assistant_settings", "confirmation": "角色信息页已设置", "tone": style.Tone})
	if err != nil {
		return err
	}
	return enqueueMemory(tx, MemoryRecord{SessionID: session, Path: memoryspace.FactPath("behavior", "space", 0, "assistant_speaking_style"), Scope: "space", SourceUserID: actor, Category: "behavior", Storage: "database_and_memory", PendingContent: string(fact), Operation: "upsert", BindingCreatedAt: epoch})
}

// A shared style edit can also update a private channel. Merge the protocol in
// a transaction so a private bootstrap cannot overwrite that newer setting.
func (db *DB) SetBehaviorInstructions(ctx context.Context, session string, epoch time.Time, instructions string) error {
	return db.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var root MemoryRecord
		err := tx.Where("id=?", MemoryID(session, memoryspace.BehaviorPath)).First(&root).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		body := root.Content
		if body == "" {
			body = memoryspace.Behavior()
		}
		var document map[string]any
		if err := json.Unmarshal([]byte(body), &document); err != nil {
			return err
		}
		document["instructions"] = instructions
		content, err := json.Marshal(document)
		if err != nil {
			return err
		}
		return queueHistoryDocument(tx, session, memoryspace.BehaviorPath, "template", string(content), epoch, false)
	})
}

func seedAssistantStyle(tx *gorm.DB, channel PrivateChannel) error {
	var source MemoryRecord
	if err := tx.Where("id=?", MemoryID(channel.SpaceID, memoryspace.BehaviorPath)).First(&source).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	var doc struct {
		SpeakingStyle memoryspace.SpeakingStyle `json:"speakingStyle"`
		UpdatedBy     int64                     `json:"styleUpdatedBy"`
	}
	if err := json.Unmarshal([]byte(source.Content), &doc); err != nil {
		return err
	}
	if doc.SpeakingStyle.Tone == "" {
		return nil
	}
	style, ok := memoryspace.Style(doc.SpeakingStyle.Tone)
	if !ok {
		return ErrInvalidTone
	}
	return writeAssistantStyle(tx, channel.SessionID, channel.BindingCreatedAt, style, doc.UpdatedBy)
}
