package asr

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSignatureKnownVectorAndEscaping(t *testing.T) {
	// Independently calculated HMAC-SHA1 vector includes an unescaped space/+.
	params := url.Values{"voice_id": {"a + b"}, "timestamp": {"1"}, "signature": {"ignored"}}
	got := signature("test-only-key", "asr.cloud.tencent.com/asr/v2/123", params)
	const want = "HJi09+zoISnX4C2QDDfb6RMjdvg="
	if got != want {
		t.Fatalf("signature mismatch: %s", got)
	}
	params.Set("signature", got)
	decoded, err := url.ParseQuery(params.Encode())
	if err != nil || decoded.Get("signature") != got || decoded.Get("voice_id") != "a + b" {
		t.Fatal("URL encoding damaged signature")
	}
}

func TestSessionIsFreshBoundedAndFixedToTencentPCM(t *testing.T) {
	cfg := Config{AppID: "1250000000", SecretID: "test-only-id", SecretKey: "secret-never-returned"}
	now := time.Date(2026, 10, 7, 8, 0, 0, 0, time.UTC)
	first, err := NewSession(cfg, now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewSession(cfg, now)
	if err != nil {
		t.Fatal(err)
	}
	if first.VoiceID == second.VoiceID || !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(first.VoiceID) {
		t.Fatal("sessions must use fresh cryptographic IDs")
	}
	u, err := url.Parse(first.URL)
	if err != nil || u.Scheme != "wss" || u.Host != "asr.cloud.tencent.com" || u.Path != "/asr/v2/1250000000" {
		t.Fatal("incorrect speech endpoint")
	}
	params := u.Query()
	if params.Get("engine_model_type") != "16k_zh" || params.Get("voice_format") != "1" || params.Get("needvad") != "1" || params.Get("voice_id") != first.VoiceID {
		t.Fatal("speech stream contract changed")
	}
	expires, _ := strconv.ParseInt(params.Get("expired"), 10, 64)
	if expires-now.Unix() != 120 || !first.ExpiresAt.Equal(now.Add(2*time.Minute)) {
		t.Fatal("session lifetime must be bounded")
	}
	if params.Get("signature") != signature(cfg.SecretKey, u.Host+u.Path, params) {
		t.Fatal("signature does not authenticate returned parameters")
	}
	if !regexp.MustCompile(`^[1-9][0-9]{9}$`).MatchString(params.Get("nonce")) {
		t.Fatal("nonce must have ten decimal digits")
	}
	data, _ := json.Marshal(first)
	if bytes.Contains(data, []byte(cfg.SecretKey)) {
		t.Fatal("secret key must never be returned")
	}
}

func TestInvalidConfigAndEntropyNeverIncludeCredentialsInErrors(t *testing.T) {
	good := Config{AppID: "1250000000", SecretID: "test-id", SecretKey: "test-secret"}
	for _, field := range []string{"appid", "secretid", "secretkey", "engine"} {
		cfg := good
		switch field {
		case "appid":
			cfg.AppID = "wx-app"
		case "secretid":
			cfg.SecretID = "bad&signature=value"
		case "secretkey":
			cfg.SecretKey = ""
		case "engine":
			cfg.EngineModel = "8k_zh"
		}
		_, err := NewSession(cfg, time.Now())
		if !errors.Is(err, ErrConfiguration) || strings.Contains(err.Error(), "test-secret") {
			t.Fatal("configuration failure must be generic")
		}
	}
	if _, err := newSession(good, time.Now(), bytes.NewReader(nil)); !errors.Is(err, ErrSession) {
		t.Fatal("entropy failure must fail closed")
	}
}
