package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"tietie/backend/internal/asr"
	"tietie/backend/internal/auth"
	"tietie/backend/internal/config"
)

func voiceTestServer() *Server {
	return &Server{Cfg: &config.Config{TencentASRAppID: "1250000000", TencentASRSecretID: "test-id", TencentASRSecretKey: "never-return-me"}, Auth: auth.NewService("test-jwt-secret", time.Hour)}
}

func voiceRequest(owner int64) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/account/voice-session", strings.NewReader(`{"host":"evil.example","voice_id":"reuse","expired":9999999999}`))
	return r.WithContext(auth.ContextWithClaims(context.Background(), &auth.Claims{UserID: owner}))
}

func TestVoiceSessionAuthenticationConfigurationAndNoCache(t *testing.T) {
	for _, tc := range []struct {
		name  string
		owner int64
		cfg   *config.Config
		want  int
	}{
		{"anonymous", 0, voiceTestServer().Cfg, 401},
		{"negative owner", -1, voiceTestServer().Cfg, 401},
		{"missing config", 1, nil, 503},
		{"missing appid", 1, &config.Config{TencentASRSecretID: "test-id", TencentASRSecretKey: "never-return-me"}, 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			(&Server{Cfg: tc.cfg}).handleVoiceSession(w, voiceRequest(tc.owner))
			if w.Code != tc.want || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "never-return-me") {
				t.Fatal("incorrect unauthenticated/unconfigured response")
			}
		})
	}
	s := voiceTestServer()
	w := httptest.NewRecorder()
	NewRouter(s).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/account/voice-session", nil))
	if w.Code != 401 {
		t.Fatal("voice route must enforce JWT middleware")
	}
}

func TestVoiceSessionIgnoresClientCredentialsAndHasFreshID(t *testing.T) {
	s := voiceTestServer()
	var previous string
	for range 2 {
		w := httptest.NewRecorder()
		s.handleVoiceSession(w, voiceRequest(8))
		var session asr.Session
		if json.Unmarshal(w.Body.Bytes(), &session) != nil || w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("missing speech session")
		}
		if strings.Contains(session.URL, "evil.example") || session.VoiceID == "reuse" || session.VoiceID == previous || strings.Contains(w.Body.String(), s.Cfg.TencentASRSecretKey) {
			t.Fatal("session trusted client input or disclosed signing secret")
		}
		previous = session.VoiceID
	}
}

func TestVoiceSessionRateLimitIsPerUserRollingAndConcurrent(t *testing.T) {
	s := voiceTestServer()
	now := time.Now().UTC()
	allowed := make(chan bool, 30)
	var group sync.WaitGroup
	for range 30 {
		group.Add(1)
		go func() { defer group.Done(); ok, _ := s.allowVoiceSession(8, now); allowed <- ok }()
	}
	group.Wait()
	close(allowed)
	total := 0
	for ok := range allowed {
		if ok {
			total++
		}
	}
	if total != 20 {
		t.Fatalf("allowed %d simultaneous sessions", total)
	}
	if ok, _ := s.allowVoiceSession(9, now); !ok {
		t.Fatal("one user's limit blocked another")
	}
	if ok, remaining := s.allowVoiceSession(8, now.Add(59*time.Second)); ok || remaining != time.Second {
		t.Fatal("rolling limit allowed a boundary burst")
	}
	w := httptest.NewRecorder()
	s.handleVoiceSession(w, voiceRequest(8))
	if w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Fatal("rate limit must be actionable")
	}
	if ok, _ := s.allowVoiceSession(8, now.Add(time.Minute)); !ok {
		t.Fatal("expired window did not recover")
	}
	if ok, _ := s.allowVoiceSession(10, now.Add(2*time.Minute)); !ok || len(s.voiceSessionRequests) != 1 {
		t.Fatal("inactive users were not removed")
	}
}
