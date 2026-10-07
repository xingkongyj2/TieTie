package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
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

func testAvatarBase64(t *testing.T, width, height int) string {
	t.Helper()
	picture := image.NewNRGBA(image.Rect(0, 0, width, height))
	picture.Set(0, 0, color.NRGBA{R: 220, A: 255})
	var data bytes.Buffer
	if err := png.Encode(&data, picture); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(data.Bytes())
}

func TestNormalizeAvatarValidatesBytesAndBounds(t *testing.T) {
	valid := testAvatarBase64(t, 1024, 512)
	for _, encoded := range []string{"", "not base64", base64.StdEncoding.EncodeToString([]byte("<svg/>")), strings.Repeat("A", (maxAvatarBytes+2)/3*4+4), testAvatarBase64(t, 4097, 1)} {
		if _, err := normalizeAvatar(encoded); err == nil {
			t.Fatal("invalid image accepted")
		}
	}
	data, err := normalizeAvatar(valid)
	if err != nil {
		t.Fatal(err)
	}
	cfg, format, decodeErr := image.DecodeConfig(bytes.NewReader(data))
	if decodeErr != nil || format != "jpeg" || cfg.Width != 512 || cfg.Height != 256 {
		t.Fatalf("bad normalized image: %#v %s %v", cfg, format, decodeErr)
	}
	// Re-encoding strips trailing metadata rather than publishing client bytes.
	raw, _ := base64.StdEncoding.DecodeString(valid)
	data, err = normalizeAvatar(base64.StdEncoding.EncodeToString(append(raw, []byte("private metadata")...)))
	if err != nil || bytes.Contains(data, []byte("private metadata")) {
		t.Fatal("image metadata was retained")
	}
}

type chosenWechatAccounts struct {
	mockWechatAccounts
	saved      bool
	saveErr    error
	nickname   string
	image      []byte
	profile    *dbop.UserProfile
	profileErr error
}

func (m *chosenWechatAccounts) GetUserProfile(_ context.Context, id int64) (*dbop.UserProfile, error) {
	if !m.saved || id != m.user.ID {
		m.t.Fatal("read profile must follow chosen-profile persistence")
	}
	if m.profileErr != nil {
		return nil, m.profileErr
	}
	if m.profile != nil {
		return m.profile, nil
	}
	return &dbop.UserProfile{UserID: id, Name: m.nickname, Avatar: "/api/assets/avatars/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, nil
}

func (m *chosenWechatAccounts) SaveWechatChosenProfile(_ context.Context, id int64, nickname, mime string, content []byte) error {
	if id != m.user.ID || mime != "image/jpeg" {
		m.t.Fatal("profile must use verified user and normalized image")
	}
	m.saved, m.nickname, m.image = true, nickname, content
	return m.saveErr
}

func TestChosenWechatProfileIsRequiredBeforeExchange(t *testing.T) {
	called := false
	s := &Server{Wechat: exchangeWechatFunc(func(context.Context, string) (wechat.Identity, error) { called = true; return wechat.Identity{}, nil })}
	for _, tc := range []struct{ nickname, avatar string }{{"", ""}, {"   ", testAvatarBase64(t, 1, 1)}, {strings.Repeat("名", 25), testAvatarBase64(t, 1, 1)}, {"选定昵称", ""}, {"选定昵称", "bad-image"}} {
		result, err := s.wechatChosenAccountResult(context.Background(), "code", tc.nickname, tc.avatar, nil)
		if err == nil || result != nil || called {
			t.Fatal("missing/invalid profile consumed a code or issued a token")
		}
	}
}

func TestChosenWechatLoginRequiresDurableProfileAndPreservesBinding(t *testing.T) {
	for _, failed := range []bool{false, true} {
		s := &Server{Auth: auth.NewService("test-signing-secret", time.Hour), Wechat: exchangeWechatFunc(func(context.Context, string) (wechat.Identity, error) {
			return wechat.Identity{AppID: "server-appid", OpenID: "server-verified-openid"}, nil
		})}
		store := &chosenWechatAccounts{mockWechatAccounts: mockWechatAccounts{t: t, user: &dbop.User{ID: 8, Username: "existing-account"}, binding: &dbop.Binding{UserA: 8, UserB: 9, SessionID: "existing-space"}}}
		if failed {
			store.saveErr = errors.New("database unavailable")
		}
		result, err := s.wechatChosenAccountResult(context.Background(), "code", " 新昵称 ", testAvatarBase64(t, 2, 2), store)
		if !store.saved || store.nickname != "新昵称" || len(store.image) == 0 {
			t.Fatal("explicit profile choice was not persisted")
		}
		if failed {
			if result != nil || err == nil {
				t.Fatal("profile failure must not issue a login token")
			}
		} else {
			if err != nil || result.Token == "" || result.Binding == nil || result.Binding.SessionID != "existing-space" {
				t.Fatalf("login failed: %#v %v", result, err)
			}
			claims, err := s.Auth.ParseToken(result.Token)
			if err != nil || claims.UserID != 8 {
				t.Fatal("incorrect account token")
			}
		}
	}
}

func TestAvatarRoutesRequireAuthenticationAndRejectIncompleteLogin(t *testing.T) {
	s := &Server{Cfg: &config.Config{StaticDir: t.TempDir()}}
	router := NewRouter(s)
	for _, tc := range []struct {
		path, body string
		status     int
	}{
		{"/api/account/avatar", `{}`, 401},
		{"/api/auth/wechat", `{"code":"code","nickname":"昵称","avatarUrl":"https://example.com/avatar.jpg"}`, 400},
		{"/api/auth/wechat", `{"code":"code","avatarBase64":"not-image"}`, 400},
	} {
		r := httptest.NewRecorder()
		router.ServeHTTP(r, httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body)))
		if r.Code != tc.status || strings.Contains(r.Body.String(), `"token"`) {
			t.Fatalf("%s: %d %s", tc.path, r.Code, r.Body)
		}
	}
	body, _ := json.Marshal(map[string]string{"code": "code", "nickname": "昵称", "avatarBase64": testAvatarBase64(t, 1, 1)})
	r := httptest.NewRecorder()
	router.ServeHTTP(r, httptest.NewRequest(http.MethodPost, "/api/auth/wechat", bytes.NewReader(body)))
	if r.Code != 503 {
		t.Fatalf("valid profile should reach unavailable WeChat configuration: %d %s", r.Code, r.Body)
	}
}

func TestWechatLoginRetriesIncompleteAccountSetup(t *testing.T) {
	for _, complete := range []bool{false, true} {
		for _, readFails := range []bool{false, true} {
			s := &Server{Auth: auth.NewService("test-signing-secret", time.Hour), Wechat: exchangeWechatFunc(func(context.Context, string) (wechat.Identity, error) {
				return wechat.Identity{AppID: "server-appid", OpenID: "server-verified-openid"}, nil
			})}
			store := &chosenWechatAccounts{mockWechatAccounts: mockWechatAccounts{t: t, user: &dbop.User{ID: 8, Username: "existing-after-failed-attempt"}}}
			if complete {
				store.profile = &dbop.UserProfile{UserID: 8, Gender: "female", Birthday: "2000-01-02", Region: regions.Location{CityCode: "valid-city"}}
			}
			if readFails {
				store.profileErr = errors.New("profile read failed")
			}
			result, err := s.wechatChosenAccountResult(context.Background(), "code", "新昵称", testAvatarBase64(t, 1, 1), store)
			if readFails {
				if result != nil || err == nil {
					t.Fatal("cannot issue token when saved profile cannot be read")
				}
				continue
			}
			if err != nil || result == nil || *result.IsNewUser || result.NeedsProfileSetup == complete {
				t.Fatalf("existing account needs onboarding until all required fields exist: %#v %v", result, err)
			}
		}
	}
}

func TestInvalidWechatCodeDoesNotFullyDecodeAvatar(t *testing.T) {
	raw, _ := base64.StdEncoding.DecodeString(testAvatarBase64(t, 4, 4))
	// PNG header remains valid, but the pixel stream/file is truncated. Full
	// decoding would return invalid_avatar, revealing an incorrect operation order.
	encoded := base64.StdEncoding.EncodeToString(raw[:len(raw)-15])
	if _, err := validateAvatarInput(encoded); err != nil {
		t.Fatalf("fixture header is invalid: %v", err)
	}
	if _, err := normalizeAvatar(encoded); err == nil {
		t.Fatal("fixture must fail full decoding")
	}
	s := &Server{Wechat: exchangeWechatFunc(func(context.Context, string) (wechat.Identity, error) {
		return wechat.Identity{}, wechat.ErrInvalidCode
	})}
	result, err := s.wechatChosenAccountResult(context.Background(), "bad-code", "昵称", encoded, nil)
	var apiErr *qoder.ApiError
	if result != nil || !errors.As(err, &apiErr) || apiErr.Code != "invalid_wechat_code" {
		t.Fatalf("expensive image decoding preceded verification: %#v %v", result, err)
	}
}

type testAvatarReader struct {
	owner int64
	err   error
}

func (s testAvatarReader) GetUserAvatar(context.Context, string) (*dbop.UserAvatar, error) {
	if s.owner == 0 {
		return nil, s.err
	}
	return &dbop.UserAvatar{UserID: s.owner}, s.err
}

func TestProfileAvatarMustBelongToCurrentUser(t *testing.T) {
	path := avatarAssetPrefix + strings.Repeat("a", 32)
	if err := validateAvatarOwnership(context.Background(), 8, path, testAvatarReader{owner: 8}); err != nil {
		t.Fatal(err)
	}
	for _, store := range []testAvatarReader{{owner: 9}, {}, {owner: 8, err: errors.New("DB failed")}} {
		if err := validateAvatarOwnership(context.Background(), 8, path, store); err == nil {
			t.Fatal("foreign, missing, or unreadable avatar accepted")
		}
	}
	for _, path := range []string{avatarAssetPrefix + "../secret", avatarAssetPrefix + strings.Repeat("a", 32) + "?owner=8", "/api/assets/avatars-other/anything"} {
		if err := validateAvatarOwnership(context.Background(), 8, path, nil); err == nil {
			t.Fatal("invalid managed avatar path accepted")
		}
	}
}
