package api

import (
	"context"
	"encoding/json"
	"errors"
	"tietie/backend/internal/conversation"
	"tietie/backend/internal/dbop"
	"tietie/backend/internal/logging"
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
	style, err := s.DB.GetAssistantStyle(ctx, e.SessionID)
	if err != nil {
		return "", nil, err
	}
	e.AssistantStyle = &style
	for _, id := range []int64{binding.UserA, binding.UserB} {
		profile, err := s.DB.GetUserProfile(ctx, id)
		if err != nil {
			return "", nil, err
		}
		metrics, err := s.DB.GetCarePreference(ctx, e.SessionID, id)
		if err != nil {
			return "", nil, err
		}
		e.WeatherProfiles = append(e.WeatherProfiles, conversation.WeatherProfile{UserID: id, Region: profile.Region, Metrics: metrics})
	}
	countdowns, cursor, err := s.DB.ListCountdowns(ctx, e.SessionID, "", time.Now())
	if err != nil {
		return "", nil, err
	}
	e.CountdownBoard = &conversation.CountdownBoard{Items: []conversation.Countdown{}, HasMore: cursor != ""}
	for _, row := range countdowns {
		e.CountdownBoard.Items = append(e.CountdownBoard.Items, protocolCountdown(row))
	}
	rows, next, err := s.DB.ListAnniversaries(ctx, e.SessionID, "", 16)
	if err != nil {
		return "", nil, err
	}
	featured, err := s.DB.FeaturedAnniversary(ctx, e.SessionID)
	if err != nil {
		return "", nil, err
	}
	board := &conversation.AnniversaryBoard{Items: []conversation.Anniversary{}, HasMore: next != "", SpaceCreatedAt: binding.CreatedAt}
	if featured != nil {
		board.Items = append(board.Items, conversation.Anniversary{ID: featured.ID, Title: featured.Title, Date: featured.Date, Kind: featured.Kind, Pinned: featured.Pinned})
	}
	for _, row := range rows {
		if featured != nil && row.ID == featured.ID {
			continue
		}
		board.Items = append(board.Items, conversation.Anniversary{ID: row.ID, Title: row.Title, Date: row.Date, Kind: row.Kind, Pinned: row.Pinned})
	}
	e.AnniversaryBoard = board
	if e.Kind == "user_message" && s.Cfg != nil && s.Cfg.CloudMemoryEnabled {
		// A new turn must not read the previously mounted account profile after an
		// edit. The outbox remains durable if this cloud write cannot complete.
		profiles, err := s.DB.ProfileMemoryRecords(ctx, e.SessionID, binding.UserA, binding.UserB)
		if err != nil {
			return "", nil, err
		}
		for _, profile := range profiles {
			if profile.State != "synced" {
				if err := s.syncMemoryLocked(ctx, profile); err != nil {
					return "", nil, err
				}
			}
		}
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
	if err := s.DB.SetBehaviorInstructions(ctx, e.SessionID, epoch, conversation.TransportInstructions(e.Visibility)); err != nil {
		return err
	}
	existing, err := s.DB.GetMemoryRecord(ctx, dbop.MemoryID(e.SessionID, conversation.ProtocolMemoryPath), e.SessionID)
	if err != nil {
		return err
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

func protocolCountdown(row dbop.Countdown) conversation.Countdown {
	return conversation.Countdown{ID: row.ID, Title: row.Title, Date: row.Date, Repeat: row.Repeat, Kind: row.Kind, DaysRemaining: row.DaysRemaining, NextDate: row.NextDate, Expired: row.Expired, LeapAdjusted: row.LeapAdjusted}
}
