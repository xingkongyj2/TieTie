package api

import (
	"context"
	"fmt"
	"tietie/backend/internal/auth"
	"tietie/backend/internal/dbop"
	"tietie/backend/internal/logging"
	"tietie/backend/internal/memoryspace"
	"tietie/backend/internal/qoder"
	"time"
)

func (s *Server) ensureConversationViewer(ctx context.Context, id string) *qoder.ApiError {
	channel, err := s.DB.GetPrivateChannel(ctx, id)
	if err != nil {
		return qoder.NewApiError(500, "internal_error", "无法校验会话权限。")
	}
	if channel == nil {
		return s.ensureBoundSession(ctx, id)
	}
	owner := auth.UserIDFrom(ctx)
	binding, err := s.DB.GetLatestBindingByUser(ctx, owner)
	if err != nil {
		return qoder.NewApiError(500, "internal_error", "无法校验会话权限。")
	}
	if owner != channel.OwnerID || binding == nil || binding.SessionID != channel.SpaceID || !binding.CreatedAt.Equal(channel.BindingCreatedAt) {
		return qoder.NewApiError(403, "session_forbidden", "你没有访问此会话的权限。")
	}
	return nil
}

// Caller holds the space lock. Private history and memory use distinct cloud
// resources, so the other member's agent cannot recall a hidden conversation.
func (s *Server) ensurePrivateChannel(ctx context.Context, space string, owner int64) (*dbop.PrivateChannel, error) {
	if !s.useV2() {
		return nil, qoder.NewApiError(409, "private_unavailable", "请升级会话协议后再使用仅自己可见。")
	}
	binding, err := s.DB.GetBindingBySessionID(ctx, space)
	if err != nil {
		return nil, err
	}
	if binding == nil || (owner != binding.UserA && owner != binding.UserB) {
		return nil, qoder.NewApiError(403, "session_forbidden", "你没有访问此空间的权限。")
	}
	existing, err := s.DB.PrivateChannelForOwner(ctx, space, owner, binding.CreatedAt)
	if err != nil || existing != nil {
		return existing, err
	}
	agent, env, err := s.Qoder.ResolveAgentAndEnv(ctx, s.Cfg.AgentID, s.Cfg.EnvironmentID)
	if err != nil {
		return nil, err
	}
	store, err := s.Qoder.CreateMemoryStore(ctx, fmt.Sprintf("TieTie-private-%d-%d", owner, time.Now().UnixNano()), fmt.Sprintf("private:%s:%d", space, owner))
	if err != nil {
		return nil, err
	}
	session, err := s.Qoder.CreateSession(ctx, agent, env, "TieTie private conversation", store.ID)
	if err != nil {
		if definitivelyRejected(err) {
			s.cleanupInitializedSpace("", store.ID)
		}
		return nil, err
	}
	if err := s.Qoder.RenameMemoryStore(ctx, store.ID, qoder.MemoryStoreName(session.ID)); err != nil {
		s.cleanupInitializedSpace(session.ID, store.ID)
		return nil, err
	}
	a, err := s.DB.GetUserByID(ctx, binding.UserA)
	if err != nil || a == nil {
		s.cleanupInitializedSpace(session.ID, store.ID)
		return nil, fmt.Errorf("private template member unavailable")
	}
	b, err := s.DB.GetUserByID(ctx, binding.UserB)
	if err != nil || b == nil {
		s.cleanupInitializedSpace(session.ID, store.ID)
		return nil, fmt.Errorf("private template member unavailable")
	}
	docs, err := memoryspace.Render(session.ID, space, memoryspace.Member{ID: a.ID, Name: a.Username}, memoryspace.Member{ID: b.ID, Name: b.Username})
	if err != nil {
		s.cleanupInitializedSpace(session.ID, store.ID)
		return nil, err
	}
	records := make([]dbop.MemoryRecord, 0, len(docs))
	for _, doc := range docs {
		entry, err := s.Qoder.UpsertMemory(ctx, store.ID, doc.Path, doc.Content)
		if err != nil {
			s.cleanupInitializedSpace(session.ID, store.ID)
			return nil, err
		}
		records = append(records, dbop.MemoryRecord{ID: dbop.MemoryID(session.ID, doc.Path), SessionID: session.ID, Path: doc.Path, Kind: "template", Scope: "space", Storage: "database_and_memory", Content: doc.Content, Operation: "upsert", State: "synced", Revision: 1, EntryID: entry.ID, StoreID: store.ID, BindingCreatedAt: binding.CreatedAt})
	}
	channel := dbop.PrivateChannel{SessionID: session.ID, SpaceID: space, OwnerID: owner, BindingCreatedAt: binding.CreatedAt}
	if err := s.DB.CreateInitializedPrivateChannel(ctx, channel, dbop.SpaceMemoryStore{SessionID: session.ID, StoreID: store.ID, NativeMounted: true, TemplateVersion: memoryspace.Version}, records...); err != nil {
		s.cleanupInitializedSpace(session.ID, store.ID)
		return nil, err
	}
	logging.System().Info("仅自己可见会话已创建，历史和记忆与共享空间隔离", "event", "privacy.channel_ready", "space_id", space, "owner_id", owner)
	return &channel, nil
}

func (s *Server) applyChannelVisibility(ctx context.Context, id string, messages []qoder.PublicMessage) error {
	channel, err := s.DB.GetPrivateChannel(ctx, id)
	if err != nil {
		return err
	}
	if channel != nil {
		for i := range messages {
			messages[i].Visibility = "private"
			messages[i].PrivateOwnerID = channel.OwnerID
		}
	}
	return nil
}

// The space endpoint aggregates only the authenticated member's private branch.
// Shared cursors remain shared; private event IDs are reconciled by the frontend.
func (s *Server) appendPrivateHistory(ctx context.Context, id string, owner int64, result *qoder.MessagesResult) error {
	binding, err := s.DB.GetBindingBySessionID(ctx, id)
	if err != nil || binding == nil {
		return err
	}
	channel, err := s.DB.PrivateChannelForOwner(ctx, id, owner, binding.CreatedAt)
	if err != nil || channel == nil {
		return err
	}
	unlock := s.lockConversation(channel.SessionID)
	defer unlock()
	history, err := s.Qoder.GetMessages(ctx, channel.SessionID, "")
	if err != nil {
		return err
	}
	if _, _, err := s.processConversation(ctx, channel.SessionID, history, owner); err != nil {
		return err
	}
	s.recordSession(ctx, history.Session)
	s.recordMessages(ctx, channel.SessionID, history.Messages)
	result.Messages = append(result.Messages, history.Messages...)
	if history.Session != nil && (history.Session.Status != "idle" || hasUnansweredAsk(history.Messages)) {
		result.Session.Status = history.Session.Status
		result.Session.ReplyMode = history.Session.ReplyMode
	}
	if active, err := s.DB.HasActiveControl(ctx, channel.SessionID); err != nil {
		return err
	} else if active {
		result.Session.Status = "running"
		result.Session.ReplyMode = ""
	}
	if history.TurnError != nil && *history.TurnError != "" {
		result.TurnError = history.TurnError
	}
	return nil
}
func hasUnansweredAsk(messages []qoder.PublicMessage) bool {
	for _, m := range messages {
		if m.Kind == "ask" && !m.Answered {
			return true
		}
	}
	return false
}

func (s *Server) answerChannel(ctx context.Context, space string, owner int64, toolID string) (string, error) {
	binding, err := s.DB.GetBindingBySessionID(ctx, space)
	if err != nil {
		return "", err
	}
	candidates := []string{space}
	if binding != nil {
		channel, e := s.DB.PrivateChannelForOwner(ctx, space, owner, binding.CreatedAt)
		if e != nil {
			return "", e
		}
		if channel != nil {
			candidates = append(candidates, channel.SessionID)
		}
	}
	for _, id := range candidates {
		h, e := s.Qoder.GetMessages(ctx, id, "")
		if e != nil {
			return "", e
		}
		for _, m := range h.Messages {
			if m.Kind == "ask" && m.ID == toolID && !m.Answered {
				return id, nil
			}
		}
	}
	return "", qoder.NewApiError(404, "invalid_tool_use_id", "这道提问不可访问或已经失效，请刷新会话。")
}
