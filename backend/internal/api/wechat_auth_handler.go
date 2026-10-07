package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"tietie/backend/internal/dbop"
	"tietie/backend/internal/qoder"
	"tietie/backend/internal/wechat"
)

type wechatAccountReader interface {
	GetLatestBindingByUser(context.Context, int64) (*dbop.Binding, error)
	GetUserProfile(context.Context, int64) (*dbop.UserProfile, error)
}

type wechatReturningAccountStore interface {
	wechatAccountReader
	GetWechatUser(context.Context, string, string) (*dbop.User, error)
}

type wechatChosenAccountStore interface {
	wechatAccountReader
	GetOrCreateWechatUser(context.Context, string, string) (*dbop.User, bool, error)
	SaveWechatChosenProfile(context.Context, int64, string, string, []byte) error
}

// handleAuthWechat accepts only a temporary wx.login code as proof of identity.
// OpenID and the mini-program secret are obtained exclusively on the server.
func (s *Server) handleAuthWechat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
		return
	}
	var body struct {
		Code         string  `json:"code"`
		Nickname     *string `json:"nickname"`
		AvatarBase64 *string `json:"avatarBase64"`
	}
	if apiErr := decodeJSONBody(r, &body, maxAvatarJSONBytes); apiErr != nil {
		writeError(w, apiErr)
		return
	}
	body.Code = strings.TrimSpace(body.Code)
	if body.Code == "" || len(body.Code) > 512 {
		writeError(w, qoder.NewApiError(400, "invalid_wechat_code", "微信登录凭证无效，请重新点击登录。"))
		return
	}
	var result *AccountResult
	var err error
	if body.Nickname == nil && body.AvatarBase64 == nil {
		result, err = s.wechatReturningAccountResult(r.Context(), body.Code, s.DB)
	} else {
		var nickname, avatar string
		if body.Nickname != nil {
			nickname = *body.Nickname
		}
		if body.AvatarBase64 != nil {
			avatar = *body.AvatarBase64
		}
		result, err = s.wechatChosenAccountResult(r.Context(), body.Code, nickname, avatar, s.DB)
	}
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// Code-only login reads the saved profile and never creates or changes accounts.
// A fresh code plus explicit profile choices is required to finish registration.
func (s *Server) wechatReturningAccountResult(ctx context.Context, code string, accounts wechatReturningAccountStore) (*AccountResult, error) {
	identity, err := s.exchangeWechatCode(ctx, code)
	if err != nil {
		return nil, err
	}
	if accounts == nil {
		return nil, wechatAccountUnavailableError()
	}
	user, err := accounts.GetWechatUser(ctx, identity.AppID, identity.OpenID)
	if err != nil {
		return nil, wechatAccountUnavailableError()
	}
	if user == nil {
		return nil, wechatProfileRequiredError()
	}
	profile, err := accounts.GetUserProfile(ctx, user.ID)
	if err != nil {
		return nil, wechatAccountUnavailableError()
	}
	if profile == nil || strings.TrimSpace(profile.Name) == "" || strings.TrimSpace(profile.Avatar) == "" {
		return nil, wechatProfileRequiredError()
	}
	return s.wechatLoginResult(ctx, user, false, profile, accounts)
}

// Explicit profile choices are validated before consuming a code and stored
// before issuing a login token, preserving the existing registration contract.
func (s *Server) wechatChosenAccountResult(ctx context.Context, code, nickname, encoded string, accounts wechatChosenAccountStore) (*AccountResult, error) {
	nickname = strings.TrimSpace(nickname)
	if nickname == "" || !utf8.ValidString(nickname) || utf8.RuneCountInString(nickname) > 24 {
		return nil, wechatProfileRequiredError()
	}
	input, apiErr := validateAvatarInput(encoded)
	if apiErr != nil {
		return nil, apiErr
	}
	identity, err := s.exchangeWechatCode(ctx, code)
	if err != nil {
		return nil, err
	}
	avatar, apiErr := normalizeAvatarInput(input)
	if apiErr != nil {
		return nil, apiErr
	}
	if accounts == nil {
		return nil, wechatAccountUnavailableError()
	}
	user, isNewUser, err := accounts.GetOrCreateWechatUser(ctx, identity.AppID, identity.OpenID)
	if err != nil || user == nil || s.Auth == nil {
		return nil, wechatAccountUnavailableError()
	}
	if err := accounts.SaveWechatChosenProfile(ctx, user.ID, nickname, "image/jpeg", avatar); err != nil {
		return nil, qoder.NewApiError(503, "wechat_profile_save_failed", "昵称和头像保存失败，请重试登录。")
	}
	profile, err := accounts.GetUserProfile(ctx, user.ID)
	if err != nil || profile == nil {
		return nil, wechatAccountUnavailableError()
	}
	return s.wechatLoginResult(ctx, user, isNewUser, profile, accounts)
}

func (s *Server) wechatLoginResult(ctx context.Context, user *dbop.User, isNewUser bool, profile *dbop.UserProfile, accounts wechatAccountReader) (*AccountResult, error) {
	if s.Auth == nil {
		return nil, wechatAccountUnavailableError()
	}
	needsSetup := (profile.Gender != "male" && profile.Gender != "female") || profile.Birthday == "" || profile.Region.CityCode == ""
	binding, err := accounts.GetLatestBindingByUser(ctx, user.ID)
	if err != nil {
		return nil, wechatAccountUnavailableError()
	}
	token, err := s.Auth.IssueToken(user.ID, user.Username)
	if err != nil {
		return nil, qoder.NewApiError(500, "internal_error", "会话服务发生错误，请稍后重试。")
	}
	s.wakeMemory()
	result := &AccountResult{Token: token, User: userPayload(user), IsNewUser: &isNewUser, NeedsProfileSetup: needsSetup}
	if binding != nil {
		result.Binding = &BindingPayload{SessionID: binding.SessionID, PartnerID: binding.OtherUser(user.ID)}
	}
	return result, nil
}

func (s *Server) exchangeWechatCode(ctx context.Context, code string) (wechat.Identity, error) {
	exchanger := s.Wechat
	if exchanger == nil {
		if s.Cfg == nil {
			return wechat.Identity{}, wechatAPIError(wechat.ErrNotConfigured)
		}
		exchanger = wechat.NewClient(s.Cfg.WechatAppID, s.Cfg.WechatAppSecret, s.Cfg.WechatTimeout)
	}
	identity, err := exchanger.ExchangeCode(ctx, code)
	if err != nil {
		return wechat.Identity{}, wechatAPIError(err)
	}
	if strings.TrimSpace(identity.AppID) == "" || len(identity.AppID) > 32 || strings.TrimSpace(identity.OpenID) == "" || len(identity.OpenID) > 128 {
		return wechat.Identity{}, wechatAPIError(wechat.ErrUnavailable)
	}
	return identity, nil
}

func wechatProfileRequiredError() *qoder.ApiError {
	return qoder.NewApiError(400, "wechat_profile_required", "请填写 1-24 个字的昵称并选择头像后登录。")
}

func wechatAccountUnavailableError() *qoder.ApiError {
	return qoder.NewApiError(503, "wechat_account_unavailable", "暂时无法完成微信登录，请稍后重试。")
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
