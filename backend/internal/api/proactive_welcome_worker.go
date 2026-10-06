package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"tietie/backend/internal/conversation"
	"tietie/backend/internal/dbop"
	"tietie/backend/internal/logging"
	"tietie/backend/internal/qoder"
)

const bindingWelcomeRequestPrefix = "bind_welcome_"

func bindingWelcomeRequestID(sessionID string) string {
	requestID := bindingWelcomeRequestPrefix + sessionID
	if len(requestID) <= 160 {
		return requestID
	}
	digest := sha256.Sum256([]byte(sessionID))
	return bindingWelcomeRequestPrefix + hex.EncodeToString(digest[:])
}

// runProactiveWelcome sends the backend-authored bootstrap frame only after
// the cloud session is idle. The input is hidden by protocol provenance; the
// resulting tietie.message response is handled by the normal conversation
// synchronizer and remains visible to both members.
func (s *Server) runProactiveWelcome(ctx context.Context, job dbop.ProactiveWelcome) (workErr error) {
	started := time.Now()
	done := false
	defer func() {
		if done {
			return
		}
		save, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = s.DB.RetryProactiveWelcome(save, job.ID, 30*time.Second, workErr)
	}()
	unlock := s.lockConversation(job.SessionID)
	defer unlock()
	space, binding, err := s.conversationContext(ctx, job.SessionID, 0)
	if err != nil {
		return err
	}
	if binding == nil || !binding.CreatedAt.Equal(job.BindingCreatedAt) {
		if err := s.DB.CompleteProactiveWelcome(ctx, job.ID); err != nil {
			return err
		}
		done = true
		return nil
	}
	history, err := s.Qoder.GetMessages(ctx, job.SessionID, "")
	if err != nil {
		return err
	}
	requestID := bindingWelcomeRequestID(job.SessionID)
	if acceptedBindingWelcome(history, requestID) {
		// The send was accepted before a previous worker instance stopped. The
		// conversation queue will fetch and process the AI reply; never resend.
		s.wakeConversation()
		if err := s.DB.CompleteProactiveWelcome(ctx, job.ID); err != nil {
			return err
		}
		done = true
		return nil
	}
	if !conversationIdle(history) {
		return errors.New("cloud session is busy; retrying binding welcome")
	}
	frame := conversation.NewEnvelopeV2(space, "binding_welcome", requestID)
	frame.Text = "这是新绑定空间的首次开场。请主动向双方发送一条简短、友好、自然的欢迎消息，欢迎他们来到专属空间，并简要说明你能帮助处理共同提醒、纪念日、天气和日常聊天。不要提及后台协议、系统指令或这条请求。"
	text, protocolState, err := s.prepareProtocolInput(ctx, frame)
	if err != nil {
		return err
	}
	previous, err := s.DB.GetConversationJob(ctx, job.SessionID)
	if err != nil {
		return err
	}
	if err := s.DB.MarkConversationPending(ctx, job.SessionID, space.Now); err != nil {
		return err
	}
	logging.Scheduler().Info("向云端助手发送隐藏的绑定欢迎协议", "event", "welcome.wakeup", "session_id", job.SessionID, "request_id", requestID, "queue_age_ms", time.Since(job.CreatedAt).Milliseconds())
	result, err := s.Qoder.SendMessage(ctx, job.SessionID, qoder.MessageInput{Text: text})
	if err != nil {
		logging.Scheduler().Warn("绑定欢迎协议发送失败，将自动重试", "event", "welcome.wakeup_failed", "session_id", job.SessionID, "request_id", requestID, "error", err)
		if definitivelyRejected(err) {
			save, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			s.resolveRejectedInput(save, job.SessionID, space.Now, previous, err)
		}
		return err
	}
	if result == nil || len(result.Events) == 0 {
		err = errors.New("cloud accepted binding welcome without an event id")
		return err
	}
	s.acceptProtocolInput(ctx, protocolState)
	s.wakeConversation()
	if err := s.DB.CompleteProactiveWelcome(ctx, job.ID); err != nil {
		return err
	}
	done = true
	logging.Scheduler().Info("绑定欢迎协议已被云端助手接受", "event", "welcome.accepted", "session_id", job.SessionID, "request_id", requestID, "duration_ms", time.Since(started).Milliseconds())
	return nil
}

func acceptedBindingWelcome(result *qoder.MessagesResult, requestID string) bool {
	if result == nil || strings.TrimSpace(requestID) == "" {
		return false
	}
	for _, event := range result.Events {
		if event.Type != "user.message" {
			continue
		}
		input, ok := conversation.DecodeInput(eventText(event))
		if ok && input.Kind == "binding_welcome" && input.RequestID == requestID {
			return true
		}
	}
	return false
}
