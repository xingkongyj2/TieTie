package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tietie/backend/internal/auth"
	"tietie/backend/internal/config"
	"tietie/backend/internal/dbop"
	"tietie/backend/internal/qoder"
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
			result, err := s.wechatAccountResult(context.Background(), "temporary-code", accounts)
			if err != nil {
				t.Fatal(err)
			}
			if !accounts.called || result.IsNewUser == nil || *result.IsNewUser != fresh || result.User.UserID != 8 || result.User.Code != "0123" {
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
			result, err := s.wechatAccountResult(context.Background(), "temporary-code", accounts)
			var apiErr *qoder.ApiError
			if result != nil || !errors.As(err, &apiErr) || apiErr.Status != tc.status || apiErr.Code != tc.code || accounts.called {
				t.Fatalf("unexpected failure: result=%#v, error=%v, account-called=%t", result, err, accounts.called)
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
		{http.MethodPost, `{"code":"temporary-code","openid":"forged-client-identity","session_key":"forged-session"}`, 503, "wechat_login_unavailable"},
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
	s := &Server{Wechat: exchangeWechatFunc(func(context.Context, string) (wechat.Identity, error) {
		return wechat.Identity{AppID: "server-appid", OpenID: "server-verified-openid"}, nil
	})}
	response := httptest.NewRecorder()
	s.handleAuthWechat(response, httptest.NewRequest(http.MethodPost, "/api/auth/wechat", strings.NewReader(`{"code":"temporary-code"}`)))
	if response.Code != 503 || !strings.Contains(response.Body.String(), "wechat_account_unavailable") {
		t.Fatalf("unexpected failure without database: %d %s", response.Code, response.Body)
	}
}
