package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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
		{Username: "first", Password: "secret", Code: "CODE0001"},
		{Username: "second", Password: "secret", Code: "CODE0002"},
		{Username: "third", Password: "secret", Code: "CODE0003"},
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
