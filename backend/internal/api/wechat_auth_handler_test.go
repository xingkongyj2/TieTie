package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"tietie/backend/internal/auth"
	"tietie/backend/internal/config"
	"tietie/backend/internal/dbop"
	"tietie/backend/internal/qoder"
	"tietie/backend/internal/regions"
	"tietie/backend/internal/wechat"
)

type exchangeWechatFunc func(context.Context, string) (wechat.Identity, error)

func (f exchangeWechatFunc) ExchangeCode(ctx context.Context, code string) (wechat.Identity, error) {
	return f(ctx, code)
}

type mockWechatAccounts struct {
	t       *testing.T
	user    *dbop.User
	binding *dbop.Binding
	isNew   bool
	err     error
	called  bool
}

func (m *mockWechatAccounts) GetOrCreateWechatUser(_ context.Context, appID, openID string) (*dbop.User, bool, error) {
	m.called = true
	if appID != "server-appid" || openID != "server-verified-openid" {
		m.t.Fatal("accounts must use the server-verified appid and openid")
	}
	return m.user, m.isNew, m.err
}
func (m *mockWechatAccounts) GetLatestBindingByUser(_ context.Context, id int64) (*dbop.Binding, error) {
	if id != m.user.ID {
		m.t.Fatal("binding must belong to the verified user")
	}
	return m.binding, nil
}

func TestWechatAccountResultIssuesJWTForVerifiedAccountAndKeepsBinding(t *testing.T) {
	for _, fresh := range []bool{true, false} {
		t.Run(map[bool]string{true: "new user", false: "returning user"}[fresh], func(t *testing.T) {
			s := &Server{Auth: auth.NewService("test-signing-secret", time.Hour), Wechat: exchangeWechatFunc(func(_ context.Context, code string) (wechat.Identity, error) {
				if code != "temporary-code" {
					t.Fatal("wrong login code")
				}
				return wechat.Identity{AppID: "server-appid", OpenID: "server-verified-openid"}, nil
			})}
			accounts := &mockWechatAccounts{t: t, user: &dbop.User{ID: 8, Username: "微信用户_8ad004dd2050", Password: "", Code: "0123"}, isNew: fresh}
			if !fresh {
				accounts.binding = &dbop.Binding{UserA: 3, UserB: 8, SessionID: "existing-session"}
			}
			result, err := s.wechatChosenAccountResult(context.Background(), "temporary-code", "用户选定昵称", testAvatarBase64(t, 1, 1), &chosenWechatAccounts{mockWechatAccounts: *accounts})
			if err != nil {
				t.Fatal(err)
			}
			if result.IsNewUser == nil || *result.IsNewUser != fresh || result.User.UserID != 8 || result.User.Code != "0123" {
				t.Fatalf("unexpected result: %#v", result)
			}
			claims, err := s.Auth.ParseToken(result.Token)
			if err != nil || claims.UserID != 8 || claims.Username != accounts.user.Username {
				t.Fatal("JWT does not identify the authenticated account")
			}
			if fresh && result.Binding != nil || !fresh && (result.Binding == nil || result.Binding.PartnerID != 3 || result.Binding.SessionID != "existing-session") {
				t.Fatal("login lost the existing binding")
			}
			response := httptest.NewRecorder()
			writeJSON(response, http.StatusOK, result)
			var payload map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if value, ok := payload["isNewUser"].(bool); !ok || value != fresh {
				t.Fatal("isNewUser must be an explicit boolean for both new and existing users")
			}
			for _, sensitive := range []string{"server-verified-openid", "session_key", "appSecret", "password"} {
				if strings.Contains(response.Body.String(), sensitive) {
					t.Fatal("login response leaked credentials or raw identity")
				}
			}
		})
	}
}

func TestWechatExchangeFailuresDoNotCreateAccounts(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{wechat.ErrNotConfigured, 503, "wechat_login_unavailable"},
		{wechat.ErrInvalidCode, 401, "invalid_wechat_code"},
		{wechat.ErrTimeout, 504, "wechat_login_timeout"},
		{wechat.ErrUnavailable, 502, "wechat_login_failed"},
		{wechat.ErrBusy, 429, "wechat_login_busy"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			s := &Server{Wechat: exchangeWechatFunc(func(context.Context, string) (wechat.Identity, error) { return wechat.Identity{}, tc.err })}
			accounts := &mockWechatAccounts{t: t}
			chosen := &chosenWechatAccounts{mockWechatAccounts: *accounts}
			result, err := s.wechatChosenAccountResult(context.Background(), "temporary-code", "用户选定昵称", testAvatarBase64(t, 1, 1), chosen)
			var apiErr *qoder.ApiError
			if result != nil || !errors.As(err, &apiErr) || apiErr.Status != tc.status || apiErr.Code != tc.code || chosen.called {
				t.Fatalf("unexpected failure: result=%#v, error=%v, account-called=%t", result, err, chosen.called)
			}
		})
	}
}

func TestWechatAuthRouteInputAndUnavailableConfiguration(t *testing.T) {
	router := NewRouter(&Server{Cfg: &config.Config{StaticDir: t.TempDir()}})
	for _, tc := range []struct {
		method, body string
		status       int
		code         string
	}{
		{http.MethodGet, "", 405, "unsupported_route"},
		{http.MethodPost, `{`, 400, "invalid_json"},
		{http.MethodPost, `{}`, 400, "invalid_wechat_code"},
		{http.MethodPost, `{"code":"  "}`, 400, "invalid_wechat_code"},
		{http.MethodPost, `{"openid":"forged-client-identity"}`, 400, "invalid_wechat_code"},
		{http.MethodPost, `{"code":"temporary-code"}`, 503, "wechat_login_unavailable"},
		{http.MethodPost, `{"code":"temporary-code","openid":"forged-client-identity","session_key":"forged-session"}`, 503, "wechat_login_unavailable"},
		{http.MethodPost, `{"code":"temporary-code","nickname":"","avatarBase64":""}`, 400, "wechat_profile_required"},
	} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(tc.method, "/api/auth/wechat", strings.NewReader(tc.body)))
		if response.Code != tc.status || !strings.Contains(response.Body.String(), `"code":"`+tc.code+`"`) {
			t.Fatalf("%s %s: status=%d, body=%s", tc.method, tc.body, response.Code, response.Body)
		}
		if strings.Contains(response.Body.String(), "forged-") {
			t.Fatal("unverified client identity leaked into response")
		}
	}
}

func TestWechatLoginWithoutDatabaseFailsSafely(t *testing.T) {
	s := &Server{Auth: auth.NewService("test-secret", time.Hour), Wechat: exchangeWechatFunc(func(context.Context, string) (wechat.Identity, error) {
		return wechat.Identity{AppID: "server-appid", OpenID: "server-verified-openid"}, nil
	})}
	response := httptest.NewRecorder()
	body, _ := json.Marshal(map[string]string{"code": "temporary-code", "nickname": "昵称", "avatarBase64": testAvatarBase64(t, 1, 1)})
	s.handleAuthWechat(response, httptest.NewRequest(http.MethodPost, "/api/auth/wechat", strings.NewReader(string(body))))
	if response.Code != 503 || !strings.Contains(response.Body.String(), "wechat_account_unavailable") {
		t.Fatalf("unexpected failure without database: %d %s", response.Code, response.Body)
	}
	response = httptest.NewRecorder()
	s.handleAuthWechat(response, httptest.NewRequest(http.MethodPost, "/api/auth/wechat", strings.NewReader(`{"code":"temporary-code"}`)))
	if response.Code != 503 || !strings.Contains(response.Body.String(), "wechat_account_unavailable") || strings.Contains(response.Body.String(), `"token"`) {
		t.Fatalf("unexpected code-only failure without database: %d %s", response.Code, response.Body)
	}
}

type returningWechatAccounts struct {
	chosenWechatAccounts
	lookupCalled bool
	profileRead  bool
	bindingErr   error
}

func (m *returningWechatAccounts) GetWechatUser(_ context.Context, appID, openID string) (*dbop.User, error) {
	m.lookupCalled = true
	if appID != "server-appid" || openID != "server-verified-openid" {
		m.t.Fatal("lookup must use the server-verified identity")
	}
	return m.user, m.err
}

func (m *returningWechatAccounts) GetUserProfile(_ context.Context, id int64) (*dbop.UserProfile, error) {
	if !m.lookupCalled || m.user == nil || id != m.user.ID {
		m.t.Fatal("read the profile only after finding the verified account")
	}
	m.profileRead = true
	return m.profile, m.profileErr
}

func (m *returningWechatAccounts) GetLatestBindingByUser(ctx context.Context, id int64) (*dbop.Binding, error) {
	if m.bindingErr != nil {
		return nil, m.bindingErr
	}
	return m.mockWechatAccounts.GetLatestBindingByUser(ctx, id)
}

func savedWechatProfile() *dbop.UserProfile {
	return &dbop.UserProfile{
		UserID: 8, Name: "已保存的昵称", Avatar: "/api/assets/avatars/saved-avatar",
		Gender: "female", Birthday: "2000-01-02", Region: regions.Location{CityCode: "valid-city"},
	}
}

func returningWechatStore(t *testing.T) *returningWechatAccounts {
	return &returningWechatAccounts{chosenWechatAccounts: chosenWechatAccounts{
		mockWechatAccounts: mockWechatAccounts{t: t, user: &dbop.User{ID: 8, Username: "existing-account", Code: "0123"}, binding: &dbop.Binding{UserA: 3, UserB: 8, SessionID: "existing-session"}},
		profile:            savedWechatProfile(),
	}}
}

func TestReturningWechatLoginPreservesSavedProfileAndBinding(t *testing.T) {
	for _, missing := range []string{"none", "gender", "birthday", "region"} {
		t.Run(missing, func(t *testing.T) {
			store := returningWechatStore(t)
			switch missing {
			case "gender":
				store.profile.Gender = "unspecified"
			case "birthday":
				store.profile.Birthday = ""
			case "region":
				store.profile.Region.CityCode = ""
			}
			saved := *store.profile
			exchanged := false
			s := &Server{Auth: auth.NewService("test-signing-secret", time.Hour), Wechat: exchangeWechatFunc(func(_ context.Context, code string) (wechat.Identity, error) {
				if code != "fresh-returning-code" {
					t.Fatal("returning login must verify the new code")
				}
				exchanged = true
				return wechat.Identity{AppID: "server-appid", OpenID: "server-verified-openid"}, nil
			})}
			result, err := s.wechatReturningAccountResult(context.Background(), "fresh-returning-code", store)
			if err != nil || result == nil {
				t.Fatalf("returning login failed: %#v %v", result, err)
			}
			if !exchanged || !store.lookupCalled || !store.profileRead || store.called || store.saved || !reflect.DeepEqual(saved, *store.profile) {
				t.Fatal("returning login must verify identity and read its existing profile without creating or rewriting it")
			}
			if result.IsNewUser == nil || *result.IsNewUser || result.NeedsProfileSetup != (missing != "none") {
				t.Fatalf("incorrect onboarding flags: %#v", result)
			}
			if result.User.UserID != 8 || result.User.Code != "0123" || result.Binding == nil || result.Binding.SessionID != "existing-session" || result.Binding.PartnerID != 3 {
				t.Fatalf("returning login lost its account or binding: %#v", result)
			}
			claims, err := s.Auth.ParseToken(result.Token)
			if err != nil || claims.UserID != 8 || claims.Username != store.user.Username {
				t.Fatal("returning login token must identify the saved account")
			}
		})
	}
}

func TestReturningWechatLoginRequiresSavedProfileAndFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		setup  func(*returningWechatAccounts, *Server)
		status int
		code   string
	}{
		{"unknown identity", func(m *returningWechatAccounts, _ *Server) { m.user = nil }, 400, "wechat_profile_required"},
		{"missing profile", func(m *returningWechatAccounts, _ *Server) { m.profile = nil }, 400, "wechat_profile_required"},
		{"missing nickname", func(m *returningWechatAccounts, _ *Server) { m.profile.Name = "" }, 400, "wechat_profile_required"},
		{"blank nickname", func(m *returningWechatAccounts, _ *Server) { m.profile.Name = " \n " }, 400, "wechat_profile_required"},
		{"missing avatar", func(m *returningWechatAccounts, _ *Server) { m.profile.Avatar = "" }, 400, "wechat_profile_required"},
		{"blank avatar", func(m *returningWechatAccounts, _ *Server) { m.profile.Avatar = " \t " }, 400, "wechat_profile_required"},
		{"lookup error", func(m *returningWechatAccounts, _ *Server) { m.err = errors.New("lookup unavailable") }, 503, "wechat_account_unavailable"},
		{"profile error", func(m *returningWechatAccounts, _ *Server) { m.profileErr = errors.New("profile unavailable") }, 503, "wechat_account_unavailable"},
		{"binding error", func(m *returningWechatAccounts, _ *Server) { m.bindingErr = errors.New("binding unavailable") }, 503, "wechat_account_unavailable"},
		{"missing auth", func(_ *returningWechatAccounts, s *Server) { s.Auth = nil }, 503, "wechat_account_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := returningWechatStore(t)
			s := &Server{Auth: auth.NewService("test-signing-secret", time.Hour), Wechat: exchangeWechatFunc(func(context.Context, string) (wechat.Identity, error) {
				return wechat.Identity{AppID: "server-appid", OpenID: "server-verified-openid"}, nil
			})}
			tc.setup(store, s)
			result, err := s.wechatReturningAccountResult(context.Background(), "fresh-code", store)
			var apiErr *qoder.ApiError
			if result != nil || !errors.As(err, &apiErr) || apiErr.Status != tc.status || apiErr.Code != tc.code || store.called || store.saved {
				t.Fatalf("probe must fail without creating/saving an account or issuing a token: %#v %v", result, err)
			}
			if (store.user == nil || store.err != nil) && store.profileRead {
				t.Fatal("failed account lookup must not read another profile")
			}
		})
	}
}

func TestReturningWechatLoginExchangeErrorsDoNotLookUpAccounts(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code string
	}{
		{wechat.ErrNotConfigured, "wechat_login_unavailable"},
		{wechat.ErrInvalidCode, "invalid_wechat_code"},
		{wechat.ErrTimeout, "wechat_login_timeout"},
		{wechat.ErrUnavailable, "wechat_login_failed"},
		{wechat.ErrBusy, "wechat_login_busy"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			store := returningWechatStore(t)
			s := &Server{Wechat: exchangeWechatFunc(func(context.Context, string) (wechat.Identity, error) { return wechat.Identity{}, tc.err })}
			result, err := s.wechatReturningAccountResult(context.Background(), "fresh-code", store)
			var apiErr *qoder.ApiError
			if result != nil || !errors.As(err, &apiErr) || apiErr.Code != tc.code || store.lookupCalled || store.called || store.saved {
				t.Fatalf("failed identity verification reached account data: %#v %v", result, err)
			}
		})
	}
}

func TestWechatLoginRejectsInvalidExchangedIdentity(t *testing.T) {
	for _, identity := range []wechat.Identity{
		{}, {AppID: "server-appid"}, {OpenID: "server-verified-openid"},
		{AppID: strings.Repeat("a", 33), OpenID: "server-verified-openid"},
		{AppID: "server-appid", OpenID: strings.Repeat("o", 129)},
		{AppID: " ", OpenID: "server-verified-openid"}, {AppID: "server-appid", OpenID: " \t "},
	} {
		store := returningWechatStore(t)
		s := &Server{Wechat: exchangeWechatFunc(func(context.Context, string) (wechat.Identity, error) { return identity, nil })}
		result, err := s.wechatReturningAccountResult(context.Background(), "fresh-code", store)
		if result != nil || err == nil || store.lookupCalled || store.called || store.saved {
			t.Fatal("invalid identity must not look up an account or issue a token")
		}
		result, err = s.wechatChosenAccountResult(context.Background(), "fresh-code", "昵称", testAvatarBase64(t, 1, 1), store)
		if result != nil || err == nil || store.lookupCalled || store.called || store.saved {
			t.Fatal("invalid identity must not create or update an account")
		}
	}
}
