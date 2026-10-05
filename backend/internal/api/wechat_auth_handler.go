package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"tietie/backend/internal/dbop"
	"tietie/backend/internal/qoder"
	"tietie/backend/internal/wechat"
)

type wechatAccountStore interface {
	GetOrCreateWechatUser(context.Context, string, string) (*dbop.User, bool, error)
	GetLatestBindingByUser(context.Context, int64) (*dbop.Binding, error)
}

// handleAuthWechat accepts only a temporary wx.login code as proof of identity.
// OpenID and the mini-program secret are obtained exclusively on the server.
func (s *Server) handleAuthWechat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if apiErr := decodeJSONBody(r, &body, 4096); apiErr != nil {
		writeError(w, apiErr)
		return
	}
	body.Code = strings.TrimSpace(body.Code)
	if body.Code == "" || len(body.Code) > 512 {
		writeError(w, qoder.NewApiError(400, "invalid_wechat_code", "微信登录凭证无效，请重新点击登录。"))
		return
	}
	result, err := s.wechatAccountResult(r.Context(), body.Code, s.DB)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) wechatAccountResult(ctx context.Context, code string, accounts wechatAccountStore) (*AccountResult, error) {
	exchanger := s.Wechat
	if exchanger == nil {
		if s.Cfg == nil {
			return nil, wechatAPIError(wechat.ErrNotConfigured)
		}
		exchanger = wechat.NewClient(s.Cfg.WechatAppID, s.Cfg.WechatAppSecret, s.Cfg.WechatTimeout)
	}
	identity, err := exchanger.ExchangeCode(ctx, code)
	if err != nil {
		return nil, wechatAPIError(err)
	}
	// The concrete DB is nil-safe, including when carried inside this interface.
	user, isNewUser, err := accounts.GetOrCreateWechatUser(ctx, identity.AppID, identity.OpenID)
	if err != nil {
		return nil, qoder.NewApiError(503, "wechat_account_unavailable", "暂时无法完成微信登录，请稍后重试。")
	}
	binding, err := accounts.GetLatestBindingByUser(ctx, user.ID)
	if err != nil {
		return nil, qoder.NewApiError(503, "wechat_account_unavailable", "暂时无法完成微信登录，请稍后重试。")
	}
	if s.Auth == nil {
		return nil, qoder.NewApiError(503, "wechat_account_unavailable", "暂时无法完成微信登录，请稍后重试。")
	}
	token, err := s.Auth.IssueToken(user.ID, user.Username)
	if err != nil {
		return nil, qoder.NewApiError(500, "internal_error", "会话服务发生错误，请稍后重试。")
	}
	result := &AccountResult{Token: token, User: userPayload(user), IsNewUser: &isNewUser}
	if binding != nil {
		result.Binding = &BindingPayload{SessionID: binding.SessionID, PartnerID: binding.OtherUser(user.ID)}
	}
	return result, nil
}

func wechatAPIError(err error) *qoder.ApiError {
	switch {
	case errors.Is(err, wechat.ErrNotConfigured):
		return qoder.NewApiError(503, "wechat_login_unavailable", "微信登录尚未配置，请联系管理员。")
	case errors.Is(err, wechat.ErrInvalidCode):
		return qoder.NewApiError(401, "invalid_wechat_code", "微信登录凭证已失效，请重新点击登录。")
	case errors.Is(err, wechat.ErrTimeout):
		return qoder.NewApiError(504, "wechat_login_timeout", "微信登录超时，请稍后重试。")
	case errors.Is(err, wechat.ErrBusy):
		return qoder.NewApiError(429, "wechat_login_busy", "微信登录过于频繁，请稍后再试。")
	default:
		return qoder.NewApiError(502, "wechat_login_failed", "微信登录服务暂时不可用，请稍后重试。")
	}
}
