package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tietie/backend/internal/auth"
	"tietie/backend/internal/config"
	"tietie/backend/internal/dbop"
)

func TestBindRejectsEitherAlreadyBoundUser(t *testing.T) {
	db, err := dbop.Open(filepath.Join(t.TempDir(), "accounts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	users := []*dbop.User{
		{Username: "first", Password: "secret", Code: "1001"},
		{Username: "second", Password: "secret", Code: "1002"},
		{Username: "third", Password: "secret", Code: "1003"},
	}
	for _, user := range users {
		if err := db.CreateUser(ctx, user); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.CreateBinding(ctx, users[0].ID, users[1].ID, "existing-session"); err != nil {
		t.Fatal(err)
	}
	authService := auth.NewService("test-secret", time.Hour)
	router := NewRouter(&Server{Cfg: &config.Config{}, Auth: authService, DB: db})
	for _, test := range []struct {
		self *dbop.User
		code string
		want int
	}{
		{users[0], users[2].Code, http.StatusConflict},
		{users[2], users[1].Code, http.StatusConflict},
		{users[0], users[1].Code, http.StatusOK},
	} {
		token, err := authService.IssueToken(test.self.ID, test.self.Username)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/account/bind", bytes.NewBufferString(`{"code":"`+test.code+`"}`))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		result := httptest.NewRecorder()
		router.ServeHTTP(result, req)
		if result.Code != test.want {
			t.Fatalf("bind %s to %s: got %d, want %d: %s", test.self.Username, test.code, result.Code, test.want, result.Body.String())
		}
	}
}

func TestUnbindClearsBindingForBothUsers(t *testing.T) {
	db, err := dbop.Open(filepath.Join(t.TempDir(), "unbind.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	users := []*dbop.User{
		{Username: "left", Password: "secret", Code: "1011"},
		{Username: "right", Password: "secret", Code: "1012"},
	}
	for _, user := range users {
		if err := db.CreateUser(ctx, user); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.CreateBinding(ctx, users[0].ID, users[1].ID, "shared-session"); err != nil {
		t.Fatal(err)
	}

	authService := auth.NewService("test-secret", time.Hour)
	router := NewRouter(&Server{Cfg: &config.Config{}, Auth: authService, DB: db})
	token, err := authService.IssueToken(users[1].ID, users[1].Username)
	if err != nil {
		t.Fatal(err)
	}
	post := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/account/unbind", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		result := httptest.NewRecorder()
		router.ServeHTTP(result, req)
		return result
	}

	for _, attempt := range []string{"首次退出", "重复退出"} {
		result := post()
		if result.Code != http.StatusOK {
			t.Fatalf("%s: got %d: %s", attempt, result.Code, result.Body.String())
		}
		var body AccountResult
		if err := json.Unmarshal(result.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Binding != nil {
			t.Fatalf("%s: 期望 binding 为空，实际 %+v", attempt, body.Binding)
		}
	}
	// 绑定行由双方共享，解绑后两人都回到未绑定状态。
	for _, user := range users {
		binding, err := db.GetLatestBindingByUser(ctx, user.ID)
		if err != nil {
			t.Fatal(err)
		}
		if binding != nil {
			t.Fatalf("%s 仍有绑定 %+v", user.Username, binding)
		}
	}
}

func TestGeneratedInviteCodeIsFourDigits(t *testing.T) {
	seen := make(map[string]bool, 200)
	for i := 0; i < 200; i++ {
		code, err := generateCode()
		if err != nil {
			t.Fatal(err)
		}
		if len(code) != 4 || strings.TrimLeft(code, "0123456789") != "" {
			t.Fatalf("邀请码应为 4 位数字，实际 %q", code)
		}
		seen[code] = true
	}
	// 4 位数字共 1 万个，200 次生成几乎不该重复；大量重复说明随机数没铺开。
	if len(seen) < 180 {
		t.Fatalf("200 次生成只得到 %d 个不同邀请码", len(seen))
	}
}
