// Package wechat exchanges a short-lived mini-program login code for a verified
// identity. App secrets and session keys never leave this server-side boundary.
package wechat

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const code2SessionURL = "https://api.weixin.qq.com/sns/jscode2session"

var (
	ErrNotConfigured = errors.New("wechat login is not configured")
	ErrInvalidCode   = errors.New("wechat login code is invalid or expired")
	ErrUnavailable   = errors.New("wechat login service is unavailable")
	ErrTimeout       = errors.New("wechat login service timed out")
	ErrBusy          = errors.New("wechat login requests are too frequent")
)

// Identity contains only the verified account identifiers; it has no session key.
type Identity struct {
	AppID  string
	OpenID string
}

type CodeExchanger interface {
	ExchangeCode(context.Context, string) (Identity, error)
}

type Client struct {
	appID      string
	appSecret  string
	httpClient *http.Client
}

func NewClient(appID, appSecret string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Client{
		appID: strings.TrimSpace(appID), appSecret: strings.TrimSpace(appSecret),
		httpClient: &http.Client{
			Timeout: timeout,
			// Never forward the secret-bearing query to a redirect destination.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

func (c *Client) ExchangeCode(ctx context.Context, code string) (Identity, error) {
	if c == nil || c.appID == "" || c.appSecret == "" {
		return Identity{}, ErrNotConfigured
	}
	code = strings.TrimSpace(code)
	if code == "" || len(code) > 512 {
		return Identity{}, ErrInvalidCode
	}
	query := url.Values{"appid": {c.appID}, "secret": {c.appSecret}, "js_code": {code}, "grant_type": {"authorization_code"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, code2SessionURL+"?"+query.Encode(), nil)
	if err != nil {
		return Identity{}, ErrUnavailable
	}
	response, err := c.httpClient.Do(req)
	if err != nil {
		// http.Client errors include the request URL (and thus the app secret).
		// Return fixed errors instead of wrapping or logging transport errors.
		var networkError net.Error
		if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &networkError) && networkError.Timeout() {
			return Identity{}, ErrTimeout
		}
		return Identity{}, ErrUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Identity{}, ErrUnavailable
	}
	const maxResponse = 64 * 1024
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponse+1))
	if err != nil {
		var networkError net.Error
		if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &networkError) && networkError.Timeout() {
			return Identity{}, ErrTimeout
		}
		return Identity{}, ErrUnavailable
	}
	if len(data) > maxResponse {
		return Identity{}, ErrUnavailable
	}
	// Deliberately do not decode or persist session_key, unionid or errmsg.
	var body struct {
		OpenID  string `json:"openid"`
		ErrCode int    `json:"errcode"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		return Identity{}, ErrUnavailable
	}
	switch body.ErrCode {
	case 0:
		if body.OpenID == "" || len(body.OpenID) > 128 {
			return Identity{}, ErrUnavailable
		}
		return Identity{AppID: c.appID, OpenID: body.OpenID}, nil
	case 40029, 40163:
		return Identity{}, ErrInvalidCode
	case 45011:
		return Identity{}, ErrBusy
	default:
		return Identity{}, ErrUnavailable
	}
}
