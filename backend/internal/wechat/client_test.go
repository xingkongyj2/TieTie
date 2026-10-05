package wechat

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func testWechatResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

func TestExchangeCodeUsesServerCredentialsAndFixedHTTPSAddress(t *testing.T) {
	client := NewClient(" wx-test ", " server-only-secret ", time.Second)
	client.httpClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Scheme != "https" || r.URL.Host != "api.weixin.qq.com" || r.URL.Path != "/sns/jscode2session" {
			t.Fatal("wechat code must be sent to its fixed official HTTPS endpoint")
		}
		query := r.URL.Query()
		if query.Get("appid") != "wx-test" || query.Get("secret") != "server-only-secret" || query.Get("js_code") != "temporary-code&special=1" || query.Get("grant_type") != "authorization_code" {
			t.Fatal("login exchange did not use the server credentials and escaped code")
		}
		return testWechatResponse(200, `{"openid":"verified-openid","session_key":"must-not-escape","unionid":"unused"}`), nil
	})
	identity, err := client.ExchangeCode(context.Background(), " temporary-code&special=1 ")
	if err != nil || identity != (Identity{AppID: "wx-test", OpenID: "verified-openid"}) {
		t.Fatalf("identity = %#v, error = %v", identity, err)
	}
}

func TestExchangeCodeFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"invalid code", 200, `{"errcode":40029,"errmsg":"invalid code"}`, ErrInvalidCode},
		{"reused code", 200, `{"errcode":40163,"errmsg":"code been used"}`, ErrInvalidCode},
		{"rate limited", 200, `{"errcode":45011}`, ErrBusy},
		{"upstream error", 200, `{"errcode":-1,"errmsg":"sensitive response"}`, ErrUnavailable},
		{"bad server credentials", 200, `{"errcode":40125}`, ErrUnavailable},
		{"HTTP error", 503, `server-only-secret`, ErrUnavailable},
		{"bad JSON", 200, `server-only-secret`, ErrUnavailable},
		{"missing identity", 200, `{"session_key":"must-not-escape"}`, ErrUnavailable},
		{"error with identity", 200, `{"openid":"invalid","errcode":40029}`, ErrInvalidCode},
		{"oversized response", 200, strings.Repeat("x", 64*1024+1), ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := NewClient("wx-test", "server-only-secret", time.Second)
			client.httpClient.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
				return testWechatResponse(tc.status, tc.body), nil
			})
			identity, err := client.ExchangeCode(context.Background(), "temporary-code")
			if !errors.Is(err, tc.want) || identity != (Identity{}) {
				t.Fatalf("identity = %#v, error = %v, want %v", identity, err, tc.want)
			}
			if strings.Contains(err.Error(), "server-only-secret") || strings.Contains(err.Error(), "must-not-escape") {
				t.Fatal("upstream error leaked credentials")
			}
		})
	}
}

func TestExchangeCodeNoRequestWithoutConfigurationOrCode(t *testing.T) {
	for _, tc := range []struct {
		appID, secret, code string
		want                error
	}{
		{"", "secret", "code", ErrNotConfigured},
		{"appid", "", "code", ErrNotConfigured},
		{"appid", "secret", "  ", ErrInvalidCode},
		{"appid", "secret", strings.Repeat("x", 513), ErrInvalidCode},
	} {
		client := NewClient(tc.appID, tc.secret, time.Second)
		client.httpClient.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("invalid requests must not contact wechat")
			return nil, nil
		})
		if _, err := client.ExchangeCode(context.Background(), tc.code); !errors.Is(err, tc.want) {
			t.Errorf("error = %v, want %v", err, tc.want)
		}
	}
}

func TestExchangeCodeTimeoutAndTransportErrorDoNotLeakSecretBearingURL(t *testing.T) {
	client := NewClient("wx-test", "server-only-secret", 5*time.Millisecond)
	client.httpClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	if _, err := client.ExchangeCode(context.Background(), "temporary-code"); !errors.Is(err, ErrTimeout) || strings.Contains(err.Error(), "server-only-secret") {
		t.Fatalf("timeout error = %v", err)
	}
	client.httpClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, errors.New("transport failure " + r.URL.String())
	})
	if _, err := client.ExchangeCode(context.Background(), "temporary-code"); !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), "server-only-secret") {
		t.Fatalf("transport error = %v", err)
	}
}

func TestExchangeCodeDoesNotFollowRedirects(t *testing.T) {
	client := NewClient("wx-test", "server-only-secret", time.Second)
	client.httpClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.weixin.qq.com" {
			t.Fatal("secret-bearing request followed a redirect")
		}
		response := testWechatResponse(http.StatusFound, "")
		response.Header.Set("Location", "https://untrusted.example/login")
		return response, nil
	})
	if _, err := client.ExchangeCode(context.Background(), "temporary-code"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("redirect error = %v", err)
	}
}
