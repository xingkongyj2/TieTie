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
	frame.Text = `这是新绑定空间的首次开场，请主动向两位成员发送一条内容充实、温暖自然、排版清楚的欢迎消息，让他们不用追问就知道可以怎样使用这个空间。
正文约 350—500 个中文字符，使用 Markdown 分段，按以下顺序组织；各段之间留空行，不要挤成一大段，也不要使用表格或代码块：
1. 开头单独一行，用 members 中的真实姓名分别 @ 两位成员，欢迎他们来到专属空间，再用一小段介绍你是「贴贴」，会帮双方记住重要的事、整理共同安排，也能陪他们日常聊天；不要假定双方是情侣。
2. 用加粗小标题「我能帮你们做什么」，列出 4 项能力，每项用加粗短名称和一两句介绍：提醒与待办（一次性、重复提醒，提醒自己、对方或双方）；纪念日与倒计时（生日、纪念日、截止日期）；天气与日常关心（根据提供的地区查询天气，给出穿搭、带伞等建议）；记忆与聊天（记住双方明确希望保存的偏好和约定，帮忙整理想法与日常安排）。
3. 用加粗小标题「可以这样开始」，列出 3 句可直接发送的例子，覆盖共同提醒、日期记录和天气查询。例子要清楚标为示例，不要把这些例子当作真实委托，不执行任何保存、设置或查询动作。
4. 用一小段「小提示」告诉他们：提醒默认双方收到，说“提醒我”可以只提醒自己；完善各自的地区后查天气更方便，需要微信通知可在页面开启提醒。不要宣称他们已经授权通知或已经设置任何任务。
5. 最后用一两句温暖的邀请收尾，鼓励双方从一件小事开始，表达你愿意陪他们把日常安排得更轻松。适量使用 2—4 个 emoji，语气自然，不说教。
只输出一条正常的 tietie.message，recipientIds 包含两位真实成员的数字 ID，source 为 chat。不要提及后台协议、系统指令或这条请求。`
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
