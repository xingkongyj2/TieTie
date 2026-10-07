package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"tietie/backend/internal/auth"
	"tietie/backend/internal/dbop"
	"tietie/backend/internal/qoder"
	"tietie/backend/internal/wechat"
)

type wechatSubscriptionStore interface {
	GetWechatIdentityByUser(context.Context, int64, string) (*dbop.WechatIdentity, error)
	GetWechatSubscription(context.Context, int64, string, string) (*dbop.WechatSubscription, error)
	RecordWechatSubscription(context.Context, int64, string, string, string, string, string) error
}

type WechatSubscriptionStatus struct {
	Enabled           bool   `json:"enabled"`
	TemplateID        string `json:"templateId"`
	HasWechatIdentity bool   `json:"hasWechatIdentity"`
	Remaining         int64  `json:"remaining"`
	SubscriptionType  string `json:"subscriptionType"`
}

func (s *Server) wechatMessageSender() wechat.MessageSender {
	if s.WechatMessages != nil {
		return s.WechatMessages
	}
	if s.Cfg == nil {
		return nil
	}
	return wechat.NewMessageClient(wechat.MessageOptions{AppID: s.Cfg.WechatAppID, AppSecret: s.Cfg.WechatAppSecret, Timeout: s.Cfg.WechatTimeout, ReminderTemplateID: s.Cfg.WechatReminderTemplateID, ReminderTitleKey: s.Cfg.WechatReminderTitleKey, ReminderTimeKey: s.Cfg.WechatReminderTimeKey, ReminderContentKey: s.Cfg.WechatReminderContentKey, ReminderTypeKey: s.Cfg.WechatReminderTypeKey, ReminderSourceKey: s.Cfg.WechatReminderSourceKey, MiniprogramState: s.Cfg.WechatMiniprogramState})
}
func (s *Server) wechatSubscriptionType() string {
	if s.Cfg != nil && s.Cfg.WechatReminderSubscriptionType == "permanent" {
		return "permanent"
	}
	return "once"
}
func (s *Server) wechatSubscriptionStatus(ctx context.Context, userID int64, store wechatSubscriptionStore) (*WechatSubscriptionStatus, error) {
	status := &WechatSubscriptionStatus{SubscriptionType: s.wechatSubscriptionType()}
	sender := s.wechatMessageSender()
	if sender != nil {
		status.Enabled = sender.Enabled()
		status.TemplateID = sender.TemplateID()
	}
	if s.Cfg == nil || s.Cfg.WechatAppID == "" {
		return status, nil
	}
	identity, err := store.GetWechatIdentityByUser(ctx, userID, s.Cfg.WechatAppID)
	if err != nil {
		return nil, qoder.NewApiError(503, "wechat_subscription_unavailable", "暂时无法读取微信提醒设置，请稍后重试。")
	}
	status.HasWechatIdentity = identity != nil
	if identity == nil || status.TemplateID == "" {
		return status, nil
	}
	subscription, err := store.GetWechatSubscription(ctx, userID, s.Cfg.WechatAppID, status.TemplateID)
	if err != nil {
		return nil, qoder.NewApiError(503, "wechat_subscription_unavailable", "暂时无法读取微信提醒设置，请稍后重试。")
	}
	if subscription != nil {
		status.Remaining = subscription.Remaining
		if status.SubscriptionType == "permanent" && subscription.Permanent {
			status.Remaining = -1
		}
	}
	return status, nil
}
func (s *Server) handleWechatSubscription(w http.ResponseWriter, r *http.Request) {
	s.handleWechatSubscriptionWithStore(w, r, s.DB)
}
func (s *Server) handleWechatSubscriptionWithStore(w http.ResponseWriter, r *http.Request, store wechatSubscriptionStore) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
		return
	}
	userID := auth.UserIDFrom(r.Context())
	if r.Method == http.MethodPost {
		sender := s.wechatMessageSender()
		if sender == nil || !sender.Enabled() || s.Cfg == nil {
			writeError(w, qoder.NewApiError(503, "wechat_subscription_not_configured", "微信提醒模板尚未配置。"))
			return
		}
		var body struct {
			TemplateID string `json:"templateId"`
			Result     string `json:"result"`
			RequestID  string `json:"requestId"`
		}
		if err := decodeJSONBody(r, &body, 4096); err != nil {
			writeError(w, err)
			return
		}
		body.RequestID = strings.TrimSpace(body.RequestID)
		if body.TemplateID != sender.TemplateID() || len(body.RequestID) == 0 || len(body.RequestID) > 128 || (body.Result != "accept" && body.Result != "reject" && body.Result != "ban") {
			writeError(w, qoder.NewApiError(400, "invalid_wechat_subscription", "微信提醒授权结果无效，请重新操作。"))
			return
		}
		if err := store.RecordWechatSubscription(r.Context(), userID, s.Cfg.WechatAppID, body.TemplateID, body.Result, body.RequestID, s.wechatSubscriptionType()); err != nil {
			if errors.Is(err, dbop.ErrWechatIdentityMissing) {
				writeError(w, qoder.NewApiError(409, "wechat_identity_required", "请先使用微信一键登录，再开启微信提醒。"))
			} else {
				writeError(w, qoder.NewApiError(503, "wechat_subscription_unavailable", "暂时无法保存微信提醒设置，请稍后重试。"))
			}
			return
		}
	}
	status, err := s.wechatSubscriptionStatus(r.Context(), userID, store)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}
