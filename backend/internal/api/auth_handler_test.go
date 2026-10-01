package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"tietie/backend/internal/auth"
	"tietie/backend/internal/config"
	"tietie/backend/internal/dbop"
)

func TestOpenCORSAndPlaintextAccounts(t *testing.T) {
	db, err := dbop.Open(filepath.Join(t.TempDir(), "accounts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	router := NewRouter(&Server{Cfg: &config.Config{}, Auth: auth.NewService("test-secret", time.Hour), DB: db})

	preflight := httptest.NewRequest(http.MethodOptions, "/api/auth/register", nil)
	preflight.Header.Set("Origin", "https://another.example")
	preflight.Header.Set("Access-Control-Request-Headers", "Authorization, Content-Type")
	preflightResult := httptest.NewRecorder()
	router.ServeHTTP(preflightResult, preflight)
	if preflightResult.Code != http.StatusNoContent || preflightResult.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("preflight: %d, %v", preflightResult.Code, preflightResult.Header())
	}

	register := httptest.NewRequest(http.MethodPost, "/api/auth/register", bytes.NewBufferString(`{"username":"newuser","password":"secret123"}`))
	register.Header.Set("Origin", "https://another.example")
	register.Header.Set("Content-Type", "application/json")
	registered := httptest.NewRecorder()
	router.ServeHTTP(registered, register)
	if registered.Code != http.StatusOK || registered.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("register: %d, %s", registered.Code, registered.Body.String())
	}
	var result AccountResult
	if err := json.Unmarshal(registered.Body.Bytes(), &result); err != nil || result.User.UserID != 1 {
		t.Fatalf("register result: %+v, %v", result, err)
	}
	me := httptest.NewRequest(http.MethodGet, "/api/account/me", nil)
	me.Header.Set("Authorization", "Bearer "+result.Token)
	meResult := httptest.NewRecorder()
	router.ServeHTTP(meResult, me)
	if meResult.Code != http.StatusOK {
		t.Fatalf("numeric ID token: %d, %s", meResult.Code, meResult.Body.String())
	}
	user, err := db.GetUserByID(context.Background(), result.User.UserID)
	if err != nil || user == nil || user.Password != "secret123" {
		t.Fatalf("stored password: %+v, %v", user, err)
	}
	plainLogin := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewBufferString(`{"username":"newuser","password":"secret123"}`))
	plainLogin.Header.Set("Content-Type", "application/json")
	plainResult := httptest.NewRecorder()
	router.ServeHTTP(plainResult, plainLogin)
	if plainResult.Code != http.StatusOK {
		t.Fatalf("plaintext login: %d, %s", plainResult.Code, plainResult.Body.String())
	}

	legacyPath := filepath.Join(t.TempDir(), "legacy-password.db")
	legacySQL, err := gorm.Open(sqlite.Open("file:"+legacyPath), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := legacySQL.Exec("CREATE TABLE users (id INTEGER PRIMARY KEY AUTOINCREMENT, username TEXT NOT NULL, password TEXT NOT NULL, legacy_password_hash TEXT, code TEXT NOT NULL, created_at DATETIME)").Error; err != nil {
		t.Fatal(err)
	}
	legacyHash, err := bcrypt.GenerateFromPassword([]byte("old-secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if err := legacySQL.Exec("INSERT INTO users (username, password, legacy_password_hash, code) VALUES (?, '', ?, 'CODE0002')", "olduser", string(legacyHash)).Error; err != nil {
		t.Fatal(err)
	}
	legacyHandle, _ := legacySQL.DB()
	_ = legacyHandle.Close()
	legacyDB, err := dbop.Open(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	defer legacyDB.Close()
	legacyRouter := NewRouter(&Server{Cfg: &config.Config{}, Auth: auth.NewService("test-secret", time.Hour), DB: legacyDB})
	login := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewBufferString(`{"username":"olduser","password":"old-secret"}`))
	login.Header.Set("Origin", "https://another.example")
	login.Header.Set("Content-Type", "application/json")
	loggedIn := httptest.NewRecorder()
	legacyRouter.ServeHTTP(loggedIn, login)
	if loggedIn.Code != http.StatusOK {
		t.Fatalf("legacy login: %d, %s", loggedIn.Code, loggedIn.Body.String())
	}
	migrated, err := legacyDB.GetUserByUsername(context.Background(), "olduser")
	if err != nil || migrated == nil || migrated.Password != "old-secret" {
		t.Fatalf("legacy password migration: %+v, %v", migrated, err)
	}
	if hash, err := legacyDB.GetLegacyPasswordHash(context.Background(), migrated.ID); err != nil || hash != "" {
		t.Fatalf("legacy password not cleared: %q, %v", hash, err)
	}
}
