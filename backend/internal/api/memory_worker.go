package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"tietie/backend/internal/conversation"
	"tietie/backend/internal/dbop"
	"tietie/backend/internal/logging"
	"tietie/backend/internal/memoryspace"
	"tietie/backend/internal/qoder"
	"time"
)

func (s *Server) ensureMemoryStore(ctx context.Context, session string) (*dbop.SpaceMemoryStore, error) {
	existing, err := s.DB.GetSpaceMemoryStore(ctx, session)
	if err != nil || existing != nil {
		return existing, err
	}
	name := qoder.MemoryStoreName(session)
	store, err := s.Qoder.FindMemoryStore(ctx, name, session)
	if err != nil {
		return nil, err
	}
	if store == nil {
		store, err = s.Qoder.CreateMemoryStore(ctx, name, session)
		if err != nil {
			return nil, err
		}
	}
	mapped := dbop.SpaceMemoryStore{SessionID: session, StoreID: store.ID}
	if err := s.DB.SaveSpaceMemoryStore(ctx, mapped); err != nil {
		return nil, err
	}
	logging.Scheduler().Info("空间云端记忆仓库已关联", "event", "memory.store_ready", "session_id", session, "store_id", store.ID, "native_mounted", false)
	return &mapped, nil
}
func (s *Server) runMemorySync(ctx context.Context, job dbop.MemoryRecord) error {
	unlock := s.lockConversation(job.SessionID)
	defer unlock()
	return s.syncMemoryLocked(ctx, job)
}
func (s *Server) syncMemoryLocked(ctx context.Context, job dbop.MemoryRecord) (syncErr error) {
	defer func() {
		if syncErr != nil {
			save, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			_ = s.DB.RetryMemorySync(save, job)
			logging.Scheduler().Error("云端记忆同步失败，保留同步任务", "event", "memory.sync_failed", "session_id", job.SessionID, "memory_key", job.ID, "error", syncErr)
		}
	}()
	current, err := s.DB.GetMemoryRecord(ctx, job.ID, job.SessionID)
	if err != nil {
		return err
	}
	if current == nil || current.State == "deleted" || current.Revision != job.Revision {
		return nil
	}
	job = *current
	if job.ArchiveRetirement {
		ready, err := s.DB.ReminderHistoryReady(ctx, job.SessionID)
		if err != nil {
			return err
		}
		if !ready {
			return errors.New("replacement reminder history is not yet synchronized")
		}
	}
	if current.State == "synced" {
		return nil
	}
	binding, err := s.DB.GetBindingBySessionID(ctx, job.SessionID)
	if err != nil {
		return err
	}
	if !job.AllowUnbound && (binding == nil || !binding.CreatedAt.Equal(job.BindingCreatedAt)) {
		return errors.New("memory binding no longer active")
	}
	if s.Cfg != nil && !s.Cfg.CloudMemoryEnabled {
		return errors.New("cloud memory synchronization disabled")
	}
	store, err := s.ensureMemoryStore(ctx, job.SessionID)
	if err != nil {
		return err
	}
	if job.Kind == "fact" {
		return s.syncFactLocked(ctx, job, *store)
	}
	if job.Operation != "delete" {
		if err := memoryspace.ValidateDocument(job.Path, job.PendingContent); err != nil {
			return err
		}
	}
	var entryID string
	if job.Operation == "delete" {
		err = s.Qoder.DeleteMemory(ctx, store.StoreID, job.Path)
	} else {
		entry, e := s.Qoder.UpsertMemory(ctx, store.StoreID, job.Path, job.PendingContent)
		err = e
		if entry != nil {
			entryID = entry.ID
		}
	}
	var upstream *qoder.ApiError
	if binding == nil && job.AllowUnbound && job.Operation == "upsert" && strings.HasPrefix(job.Path, "tasks/todo-board/") && errors.As(err, &upstream) && upstream.Status == 404 {
		// Live mounted stores are never replaced. An inactive archive can be
		// restored from its database copy when its old cloud store was deleted.
		name := qoder.MemoryStoreName(job.SessionID)
		fresh, e := s.Qoder.FindMemoryStore(ctx, name, job.SessionID)
		if e != nil {
			return e
		}
		if fresh == nil {
			fresh, e = s.Qoder.CreateMemoryStore(ctx, name, job.SessionID)
			if e != nil {
				return e
			}
		}
		if fresh.ID == store.StoreID {
			return err
		}
		if e := s.DB.RecoverReminderArchiveStore(ctx, *store, fresh.ID); e != nil {
			return e
		}
		logging.Scheduler().Info("已退出空间的云端仓库不存在，完整提醒归档转入独立仓库等待同步", "event", "memory.archive_recovered", "session_id", job.SessionID, "store_id", fresh.ID)
		return nil
	}
	if err != nil {
		return err
	}
	save, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := s.DB.FinishMemorySync(save, job, store.StoreID, entryID); err != nil {
		return err
	}
	logging.Scheduler().Info("云端长期记忆同步完成", "event", "memory.synced", "session_id", job.SessionID, "memory_key", job.ID, "path", job.Path, "revision", job.Revision, "storage", job.Storage)
	return nil
}
func (s *Server) reminderMemoryRead(ctx context.Context, r dbop.Reminder) ([]conversation.MemoryRead, error) {
	if r.MemorySession() != r.DeliverySession() {
		unlock := s.lockConversation(r.MemorySession())
		defer unlock()
	}
	record, err := s.DB.GetReminderMemory(ctx, r)
	if err != nil {
		return nil, err
	}
	if record == nil {
		return nil, nil
	}
	if record.State != "synced" {
		if err := s.syncMemoryLocked(ctx, *record); err != nil {
			return nil, err
		}
		record, err = s.DB.GetMemoryRecord(ctx, record.ID, r.MemorySession())
		if err != nil {
			return nil, err
		}
	}
	if record.EntryID == "" {
		return nil, nil
	}
	entry, err := s.Qoder.GetMemory(ctx, record.StoreID, record.EntryID)
	if err != nil {
		return nil, err
	}
	body, err := dbop.ExtractReminderMemory(entry.Content, r.ID)
	if err != nil {
		return nil, err
	}
	return []conversation.MemoryRead{{Key: record.ID, Path: entry.Path, Content: body}}, nil
}

// Facts are indexed database rows; only their shared template page is a cloud
// document. A successful page write precedes retiring the old per-fact entry.
func (s *Server) syncFactLocked(ctx context.Context, job dbop.MemoryRecord, store dbop.SpaceMemoryStore) error {
	if job.Operation != "delete" {
		body := job.Content
		if body == "" {
			body = job.PendingContent
		}
		if body == "" && job.EntryID != "" && job.StoreID != "" {
			entry, err := s.Qoder.GetMemory(ctx, job.StoreID, job.EntryID)
			if err != nil {
				return err
			}
			body = entry.Content
		}
		if body == "" {
			return errors.New("legacy fact body is unavailable")
		}
		if err := s.DB.HydrateMemoryFact(ctx, job, body); err != nil {
			return err
		}
	}
	page, err := s.DB.FactProjection(ctx, job)
	if err != nil && job.Operation != "delete" {
		return err
	}
	entryID := ""
	if page != nil {
		if err := s.syncMemoryLocked(ctx, *page); err != nil {
			return err
		}
		current, err := s.DB.GetMemoryRecord(ctx, page.ID, page.SessionID)
		if err != nil {
			return err
		}
		if current == nil || current.State != "synced" {
			return errors.New("template page is still pending")
		}
		entryID = current.EntryID
	}
	// Logical #keys never become files. Old fact files are removed after all of
	// their original data has been preserved in the correct template page.
	if strings.HasSuffix(job.Path, ".json") && !memoryspace.IsDocumentPath(job.Path) {
		if err := s.Qoder.DeleteMemory(ctx, store.StoreID, job.Path); err != nil {
			return err
		}
	}
	return s.DB.FinishMemorySync(ctx, job, store.StoreID, entryID)
}

func extractFactMemory(body, key string) (string, error) {
	entries, err := memoryspace.Entries(body)
	if err != nil {
		return "", err
	}
	for _, raw := range entries {
		var fact struct {
			Key string `json:"memoryKey"`
		}
		if json.Unmarshal(raw, &fact) == nil && fact.Key == key {
			return string(raw), nil
		}
	}
	return "", errors.New("fact missing from its template page")
}
