package api

import (
	"context"
	"encoding/json"
	"errors"
	"tietie/backend/internal/conversation"
	"tietie/backend/internal/dbop"
	"tietie/backend/internal/logging"
	"tietie/backend/internal/memoryspace"
	"time"
	"unicode/utf8"
)

// Caller holds the conversation lock. Do not persist this candidate until the
// upstream accepts the send: a rejected first frame must still bootstrap later.
func (s *Server) prepareProtocolInput(ctx context.Context, e conversation.EnvelopeV2) (string, *dbop.ConversationProtocol, error) {
	binding, err := s.DB.GetBindingBySessionID(ctx, e.SessionID)
	if err != nil {
		return "", nil, err
	}
	if binding == nil {
		return "", nil, errors.New("protocol session is no longer bound")
	}
	stored, err := s.DB.GetConversationProtocol(ctx, e.SessionID)
	if err != nil {
		return "", nil, err
	}
	hash := conversation.ContractHash(e.Visibility)
	var previous *conversation.TransportState
	if stored != nil && stored.ContractHash == hash && stored.BindingCreatedAt.Equal(binding.CreatedAt) {
		var state conversation.TransportState
		if json.Unmarshal([]byte(stored.StateJSON), &state) == nil {
			previous = &state
		}
	}
	if previous == nil && s.Cfg != nil && s.Cfg.CloudMemoryEnabled {
		// Durable outbox gives the fixed protocol a recoverable memory copy. Failure
		// does not block chatting: this first frame contains the authoritative rules.
		if err := s.storeProtocolMemory(ctx, e, binding.CreatedAt); err != nil {
			logging.System().Warn("固定协议记忆暂未同步，首轮使用完整协议并保留重试", "event", "protocol.memory_pending", "session_id", e.SessionID, "error", err)
		}
	}
	text, state := conversation.CompactFrame(e, previous)
	body, err := json.Marshal(state)
	if err != nil {
		return "", nil, err
	}
	candidate := &dbop.ConversationProtocol{SessionID: e.SessionID, BindingCreatedAt: binding.CreatedAt, ContractHash: hash, StateJSON: string(body)}
	logging.System().Info("准备 AI 消息：仅首次或升级附带完整协议", "event", "protocol.outbound", "session_id", e.SessionID, "kind", e.Kind, "bootstrap", previous == nil, "payload_chars", utf8.RuneCountInString(text), "payload_bytes", len(text))
	return text, candidate, nil
}
func (s *Server) storeProtocolMemory(ctx context.Context, e conversation.EnvelopeV2, epoch time.Time) error {
	existing, err := s.DB.GetMemoryRecord(ctx, dbop.MemoryID(e.SessionID, conversation.ProtocolMemoryPath), e.SessionID)
	if err != nil {
		return err
	}
	// Keep persona rules and runtime additions in their original template.
	body := memoryspace.Behavior()
	if existing != nil && existing.Content != "" {
		body = existing.Content
	}
	var document map[string]any
	if err := json.Unmarshal([]byte(body), &document); err != nil {
		return err
	}
	document["instructions"] = conversation.TransportInstructions(e.Visibility)
	content, _ := json.Marshal(document)
	record := dbop.MemoryRecord{Kind: "template", SessionID: e.SessionID, Path: conversation.ProtocolMemoryPath, Scope: "space", Category: "behavior", Storage: "database_and_memory", Operation: "upsert", PendingContent: string(content), BindingCreatedAt: epoch}
	if existing == nil || existing.Content != string(content) || !existing.BindingCreatedAt.Equal(epoch) {
		if err := s.DB.QueueMemory(ctx, record); err != nil {
			return err
		}
		existing, err = s.DB.GetMemoryRecord(ctx, dbop.MemoryID(e.SessionID, record.Path), e.SessionID)
		if err != nil {
			return err
		}
	}
	if existing == nil {
		return errors.New("protocol memory missing after enqueue")
	}
	return s.syncMemoryLocked(ctx, *existing)
}
func (s *Server) acceptProtocolInput(ctx context.Context, state *dbop.ConversationProtocol) {
	if state == nil {
		return
	}
	save, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := s.DB.SaveConversationProtocol(save, *state); err != nil {
		// The send already succeeded. Reporting it as failed would duplicate user
		// input; leave the previous cursor so the next send safely repeats context.
		logging.System().Error("AI 已接受消息，但增量状态保存失败，下轮将重发必要上下文", "event", "protocol.state_failed", "session_id", state.SessionID, "error", err)
	}
}
