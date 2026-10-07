// Package asr issues short-lived Tencent Cloud realtime speech credentials.
// Persistent credentials and signing stay on the server; audio goes directly
// from the client to the fixed Tencent WebSocket endpoint.
package asr

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"math/big"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultEngineModel = "16k_zh"
	SessionTTL         = 2 * time.Minute
	endpointHost       = "asr.cloud.tencent.com"
)

var (
	ErrConfiguration = errors.New("speech recognition is not configured")
	ErrSession       = errors.New("speech session could not be created")
	appIDPattern     = regexp.MustCompile(`^[1-9][0-9]{0,19}$`)
	identifier       = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	enginePattern    = regexp.MustCompile(`^16k_[A-Za-z0-9_-]+$`)
)

type Config struct {
	AppID       string
	SecretID    string
	SecretKey   string
	EngineModel string
}

// Validate only reports generic errors, never configured values or credentials.
// All supported models must consume the client's fixed 16 kHz PCM stream.
func (c Config) Validate() error {
	if !appIDPattern.MatchString(c.AppID) || !identifier.MatchString(c.SecretID) || strings.TrimSpace(c.SecretKey) == "" || len(c.SecretID) > 256 || len(c.EngineModel) > 64 {
		return ErrConfiguration
	}
	if c.EngineModel != "" && !enginePattern.MatchString(c.EngineModel) {
		return ErrConfiguration
	}
	return nil
}

type Session struct {
	URL       string    `json:"url"`
	VoiceID   string    `json:"voiceId"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// NewSession signs a fresh connection. The client cannot customize the host,
// model, audio format, voice ID, or credential lifetime through this operation.
func NewSession(cfg Config, now time.Time) (*Session, error) {
	return newSession(cfg, now, rand.Reader)
}

func newSession(cfg Config, now time.Time, entropy io.Reader) (*Session, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if now.Unix() <= 0 {
		return nil, ErrSession
	}
	voice := make([]byte, 16)
	if _, err := io.ReadFull(entropy, voice); err != nil {
		return nil, ErrSession
	}
	nonce, err := rand.Int(entropy, big.NewInt(9_000_000_000))
	if err != nil {
		return nil, ErrSession
	}
	nonce.Add(nonce, big.NewInt(1_000_000_000)) // Exactly ten decimal digits.
	voiceID := hex.EncodeToString(voice)
	expires := time.Unix(now.Unix()+int64(SessionTTL/time.Second), 0).UTC()
	model := cfg.EngineModel
	if model == "" {
		model = DefaultEngineModel
	}
	params := url.Values{
		"engine_model_type": {model},
		"expired":           {strconv.FormatInt(expires.Unix(), 10)},
		"needvad":           {"1"},
		"nonce":             {nonce.String()},
		"secretid":          {cfg.SecretID},
		"timestamp":         {strconv.FormatInt(now.Unix(), 10)},
		"voice_format":      {"1"},
		"voice_id":          {voiceID},
	}
	path := "/asr/v2/" + cfg.AppID
	params.Set("signature", signature(cfg.SecretKey, endpointHost+path, params))
	return &Session{
		URL:       "wss://" + endpointHost + path + "?" + params.Encode(),
		VoiceID:   voiceID,
		ExpiresAt: expires,
	}, nil
}

// Tencent signs sorted, unescaped key=value pairs without the wss:// prefix.
// Only the final URL is escaped, including +, / and = in the Base64 signature.
// https://cloud.tencent.com/document/product/1093/48982
func signature(secret, hostPath string, params url.Values) string {
	keys := make([]string, 0, len(params))
	for key := range params {
		if key != "signature" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	var raw strings.Builder
	raw.WriteString(hostPath)
	raw.WriteByte('?')
	for i, key := range keys {
		if i > 0 {
			raw.WriteByte('&')
		}
		raw.WriteString(key)
		raw.WriteByte('=')
		raw.WriteString(params.Get(key))
	}
	mac := hmac.New(sha1.New, []byte(secret))
	_, _ = mac.Write([]byte(raw.String()))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}
