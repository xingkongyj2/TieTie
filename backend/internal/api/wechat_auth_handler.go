package api

import (
	"context"
	cryptorand "crypto/rand"
	"errors"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"tietie/backend/internal/dbop"
	"tietie/backend/internal/qoder"
	"tietie/backend/internal/wechat"
)

type wechatAccountStore interface {
	GetOrCreateWechatUser(context.Context, string, string) (*dbop.User, bool, error)
	GetLatestBindingByUser(context.Context, int64) (*dbop.Binding, error)
}

// wechatProfileStore is implemented by the database. It is kept separate from
// wechatAccountStore so the login flow remains easy to exercise with the small
// account mocks used by the auth tests.
type wechatProfileStore interface {
	GetUserProfile(context.Context, int64) (*dbop.UserProfile, error)
	SaveUserProfile(context.Context, dbop.UserProfile, ...bool) (string, error)
}

// handleAuthWechat accepts only a temporary wx.login code as proof of identity.
// OpenID and the mini-program secret are obtained exclusively on the server.
func (s *Server) handleAuthWechat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
		return
	}
	var body struct {
		Code      string `json:"code"`
		Nickname  string `json:"nickname"`
		AvatarURL string `json:"avatarUrl"`
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
	result, err := s.wechatAccountResultWithProfile(r.Context(), body.Code, s.DB, body.Nickname, body.AvatarURL)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) wechatAccountResult(ctx context.Context, code string, accounts wechatAccountStore) (*AccountResult, error) {
	return s.wechatAccountResultWithProfile(ctx, code, accounts, "", "")
}

// wechatAccountResultWithProfile exchanges the one-time login code and, when
// the client was able to obtain wx.getUserProfile data, stores that data in the
// account profile. The profile is display data only; the server still obtains
// the verified OpenID exclusively from jscode2session.
func (s *Server) wechatAccountResultWithProfile(ctx context.Context, code string, accounts wechatAccountStore, nickname, avatarURL string) (*AccountResult, error) {
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
	// wx.getUserProfile is optional: users can decline it, and older bases may
	// not expose it. Keep login successful and choose a stable local fallback
	// avatar when no usable image was supplied.
	s.saveWechatProfile(ctx, accounts, user, isNewUser, nickname, avatarURL)
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

// Default avatars are local assets, so they work even when the remote WeChat
// avatar host is not listed in a mini-program's download domain allowlist.
var wechatDefaultAvatars = []string{
	"/avatars/cream-cat.png", "/avatars/peach-cat.png", "/avatars/golden-longhair-cat.png",
	"/avatars/zodiac-rabbit.png", "/avatars/corgi-dog.png", "/avatars/otter.png", "/avatars/penguin.png",
}

func (s *Server) saveWechatProfile(ctx context.Context, accounts wechatAccountStore, user *dbop.User, isNewUser bool, nickname, avatarURL string) {
	store, ok := accounts.(wechatProfileStore)
	if !ok || user == nil {
		return
	}
	profile, err := store.GetUserProfile(ctx, user.ID)
	if err != nil || profile == nil {
		return
	}
	name := strings.TrimSpace(nickname)
	if utf8.RuneCountInString(name) == 0 || utf8.RuneCountInString(name) > 24 {
		name = ""
	}
	avatar := strings.TrimSpace(avatarURL)
	if !validWechatAvatarURL(avatar) {
		avatar = ""
	}
	// Do not overwrite a name/avatar the user has chosen in the app on every
	// subsequent login. Fill only new or still-empty profile fields.
	if !isNewUser && profile.Name != "" {
		name = profile.Name
	}
	if name == "" {
		name = profile.Name
		if name == "" {
			name = user.Username // generated username is already a random fallback
		}
	}
	if !isNewUser && profile.Avatar != "" {
		avatar = profile.Avatar
	}
	if avatar == "" {
		avatar = randomWechatAvatar()
	}
	if name == profile.Name && avatar == profile.Avatar {
		return
	}
	profile.UserID = user.ID
	profile.Name = name
	profile.Avatar = avatar
	if profile.Gender == "" {
		profile.Gender = "unspecified"
	}
	if profile.Hobbies == nil {
		profile.Hobbies = []string{}
	}
	// Profile persistence must not turn an otherwise valid login into a failed
	// login. The next successful login can retry the best-effort update.
	_, _ = store.SaveUserProfile(ctx, *profile, true)
}

func validWechatAvatarURL(value string) bool {
	if value == "" || len(value) > 512 {
		return false
	}
	if strings.HasPrefix(value, "/avatars/") && !strings.HasPrefix(value, "//") {
		return true
	}
	u, err := url.Parse(value)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil
}

func randomWechatAvatar() string {
	index, err := cryptorand.Int(cryptorand.Reader, big.NewInt(int64(len(wechatDefaultAvatars))))
	if err != nil {
		return wechatDefaultAvatars[0]
	}
	return wechatDefaultAvatars[index.Int64()]
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
