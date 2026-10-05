package wechat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func reminderTestOptions() MessageOptions {
	return MessageOptions{
		AppID: " wx-test ", AppSecret: " server-secret ", ReminderTemplateID: " template-test ",
		ReminderTitleKey: "thing1", ReminderTimeKey: "time2", ReminderContentKey: "thing3",
		MiniprogramState: "trial", Timeout: time.Second,
	}
}

func reminderTestNotification() ReminderNotification {
	return ReminderNotification{
		Title: "记得准时吃药", Content: "饭后吃药", DueAt: time.Date(2026, 10, 5, 4, 30, 0, 0, time.UTC),
		Page: "pages/index/index?reminder=123",
	}
}

func assertMessageKind(t *testing.T, err error, kind MessageErrorKind) {
	t.Helper()
	if MessageErrorKindOf(err) != kind {
		t.Fatalf("error kind = %q, want %q, error %v", MessageErrorKindOf(err), kind, err)
	}
	for _, sensitive := range []string{"server-secret", "secret-token", "private-body", "api.weixin.qq.com"} {
		if strings.Contains(err.Error(), sensitive) {
			t.Fatal("message error leaked upstream credentials or body")
		}
	}
}

func TestSendReminderUsesFixedHTTPSCredentialsTemplateAndChatPage(t *testing.T) {
	client := NewMessageClient(reminderTestOptions())
	var tokenRequests, sendRequests int
	client.httpClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Scheme != "https" || r.URL.Host != "api.weixin.qq.com" || r.Header.Get("Content-Type") != "application/json" {
			t.Fatal("message requests must use JSON POST to official HTTPS endpoints")
		}
		if r.URL.Path == "/cgi-bin/stable_token" {
			tokenRequests++
			if r.URL.RawQuery != "" {
				t.Fatal("token credentials must only be in the server request body")
			}
			var body struct {
				GrantType    string `json:"grant_type"`
				AppID        string `json:"appid"`
				Secret       string `json:"secret"`
				ForceRefresh bool   `json:"force_refresh"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil || body.GrantType != "client_credential" || body.AppID != "wx-test" || body.Secret != "server-secret" || body.ForceRefresh {
				t.Fatal("stable-token request must use server credentials without force-refresh")
			}
			return testWechatResponse(200, `{"access_token":"secret-token","expires_in":7200}`), nil
		}
		if r.URL.Path != "/cgi-bin/message/subscribe/send" || r.URL.Query().Get("access_token") != "secret-token" {
			t.Fatal("invalid subscription send endpoint")
		}
		sendRequests++
		var body struct {
			ToUser     string                   `json:"touser"`
			TemplateID string                   `json:"template_id"`
			Page       string                   `json:"page"`
			Data       map[string]templateValue `json:"data"`
			State      string                   `json:"miniprogram_state"`
			Lang       string                   `json:"lang"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Fatal("could not decode reminder")
		}
		if body.ToUser != "verified-openid" || body.TemplateID != "template-test" || body.Page != "pages/index/index?reminder=123" || body.State != "trial" || body.Lang != "zh_CN" {
			t.Fatalf("unexpected reminder addressing: %#v", body)
		}
		if body.Data["thing1"].Value != "记得准时吃药" || body.Data["time2"].Value != "2026-10-05 12:30" || body.Data["thing3"].Value != "饭后吃药" {
			t.Fatal("incorrect template preview or China timezone")
		}
		return testWechatResponse(200, `{"errcode":0,"errmsg":"ok"}`), nil
	})
	for i := 0; i < 2; i++ {
		if err := client.SendReminder(context.Background(), " verified-openid ", reminderTestNotification()); err != nil {
			t.Fatal(err)
		}
	}
	if tokenRequests != 1 || sendRequests != 2 {
		t.Fatalf("token requests = %d, send requests = %d", tokenRequests, sendRequests)
	}
}

func TestMessageTokenCacheConcurrentAndRefreshBeforeExpiration(t *testing.T) {
	client := NewMessageClient(reminderTestOptions())
	now := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	client.now = func() time.Time { return now }
	var tokenRequests atomic.Int32
	client.httpClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/cgi-bin/stable_token" {
			tokenRequests.Add(1)
			return testWechatResponse(200, `{"access_token":"secret-token","expires_in":7200}`), nil
		}
		return testWechatResponse(200, `{"errcode":0}`), nil
	})
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := client.SendReminder(context.Background(), "openid", reminderTestNotification()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if tokenRequests.Load() != 1 {
		t.Fatal("concurrent sends must share one cached token")
	}
	now = now.Add(118 * time.Minute)
	if err := client.SendReminder(context.Background(), "openid", reminderTestNotification()); err != nil {
		t.Fatal(err)
	}
	if tokenRequests.Load() != 2 {
		t.Fatal("token was not refreshed before expiration")
	}
}

func TestSendReminderRetriesInvalidTokenOnlyOnce(t *testing.T) {
	for _, eventualSuccess := range []bool{true, false} {
		t.Run(fmt.Sprint(eventualSuccess), func(t *testing.T) {
			client := NewMessageClient(reminderTestOptions())
			var tokens, sends int
			client.httpClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path == "/cgi-bin/stable_token" {
					tokens++
					return testWechatResponse(200, fmt.Sprintf(`{"access_token":"token%d","expires_in":7200}`, tokens)), nil
				}
				sends++
				if r.URL.Query().Get("access_token") != fmt.Sprintf("token%d", sends) {
					t.Fatal("send did not use the renewed token")
				}
				if sends == 2 && eventualSuccess {
					return testWechatResponse(200, `{"errcode":0}`), nil
				}
				return testWechatResponse(200, `{"errcode":40014,"errmsg":"private-body secret-token"}`), nil
			})
			err := client.SendReminder(context.Background(), "openid", reminderTestNotification())
			if eventualSuccess {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				assertMessageKind(t, err, MessagePermanent)
			}
			if sends != 2 || tokens != 2 {
				t.Fatalf("sends = %d, tokens = %d; expected one retry", sends, tokens)
			}
		})
	}
}

func TestSendReminderExplicitRejections(t *testing.T) {
	for _, tc := range []struct {
		code int
		kind MessageErrorKind
	}{
		{43101, MessageUnauthorized}, {-1, MessageRetryable}, {45011, MessageRetryable}, {43108, MessageRetryable},
		{40003, MessagePermanent}, {40037, MessagePermanent}, {41030, MessagePermanent}, {47003, MessagePermanent},
		{43107, MessagePermanent}, {45168, MessagePermanent}, {45009, MessagePermanent},
	} {
		t.Run(fmt.Sprint(tc.code), func(t *testing.T) {
			client := NewMessageClient(reminderTestOptions())
			sends := 0
			client.httpClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path == "/cgi-bin/stable_token" {
					return testWechatResponse(200, `{"access_token":"secret-token","expires_in":7200}`), nil
				}
				sends++
				return testWechatResponse(200, fmt.Sprintf(`{"errcode":%d,"errmsg":"private-body secret-token"}`, tc.code)), nil
			})
			err := client.SendReminder(context.Background(), "openid", reminderTestNotification())
			assertMessageKind(t, err, tc.kind)
			if IsMessageRetryable(err) != (tc.kind == MessageRetryable) || IsMessageUnauthorized(err) != (tc.kind == MessageUnauthorized) {
				t.Fatal("queue error classifiers disagree")
			}
			var messageError *MessageError
			if !errors.As(err, &messageError) || messageError.Code != tc.code {
				t.Fatal("numeric WeChat code was lost")
			}
			if sends != 1 {
				t.Fatal("explicit rejection must be returned to the queue without hidden retries")
			}
		})
	}
}

type failedMessageBody struct{}

func (failedMessageBody) Read([]byte) (int, error) { return 0, errors.New("private-body secret-token") }
func (failedMessageBody) Close() error             { return nil }

func TestSendReminderUnknownDeliveryNeverRetriedAndNeverLeaksCredentials(t *testing.T) {
	for _, tc := range []struct {
		name     string
		response func(*http.Request) (*http.Response, error)
	}{
		{"transport", func(r *http.Request) (*http.Response, error) {
			return nil, errors.New("private-body " + r.URL.String())
		}},
		{"HTTP error", func(*http.Request) (*http.Response, error) {
			return testWechatResponse(503, "private-body secret-token"), nil
		}},
		{"bad JSON", func(*http.Request) (*http.Response, error) {
			return testWechatResponse(200, "private-body secret-token"), nil
		}},
		{"missing status", func(*http.Request) (*http.Response, error) { return testWechatResponse(200, `{}`), nil }},
		{"null status", func(*http.Request) (*http.Response, error) { return testWechatResponse(200, `{"errcode":null}`), nil }},
		{"oversized", func(*http.Request) (*http.Response, error) {
			return testWechatResponse(200, strings.Repeat("x", maxMessageResponse+1)), nil
		}},
		{"read failure", func(*http.Request) (*http.Response, error) {
			response := testWechatResponse(200, "")
			response.Body = failedMessageBody{}
			return response, nil
		}},
		{"redirect", func(*http.Request) (*http.Response, error) {
			response := testWechatResponse(302, "")
			response.Header.Set("Location", "https://untrusted.example/message")
			return response, nil
		}},
		{"timeout", func(r *http.Request) (*http.Response, error) { <-r.Context().Done(); return nil, r.Context().Err() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			options := reminderTestOptions()
			options.Timeout = 10 * time.Millisecond
			client := NewMessageClient(options)
			sends := 0
			client.httpClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != "api.weixin.qq.com" {
					t.Fatal("credentials were sent to redirect destination")
				}
				if r.URL.Path == "/cgi-bin/stable_token" {
					return testWechatResponse(200, `{"access_token":"secret-token","expires_in":7200}`), nil
				}
				sends++
				return tc.response(r)
			})
			err := client.SendReminder(context.Background(), "openid", reminderTestNotification())
			assertMessageKind(t, err, MessageUncertain)
			if !IsMessageUncertain(err) || IsMessageRetryable(err) || sends != 1 {
				t.Fatal("uncertain send must not be automatically repeated")
			}
		})
	}
}

func TestTokenFailuresHaveNoAmbiguousMessageDelivery(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		kind       MessageErrorKind
	}{
		{"busy", `{"errcode":-1,"errmsg":"private-body"}`, 200, MessageRetryable},
		{"bad secret", `{"errcode":40125,"errmsg":"server-secret"}`, 200, MessagePermanent},
		{"IP not whitelisted", `{"errcode":40164}`, 200, MessagePermanent},
		{"bad JSON", "private-body", 200, MessageRetryable},
		{"missing token", `{"expires_in":7200}`, 200, MessageRetryable},
		{"missing expiry", `{"access_token":"secret-token"}`, 200, MessageRetryable},
		{"invalid expiry", `{"access_token":"secret-token","expires_in":-1}`, 200, MessageRetryable},
		{"HTTP error", "private-body", 503, MessageRetryable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := NewMessageClient(reminderTestOptions())
			client.httpClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/cgi-bin/stable_token" {
					t.Fatal("message was sent after a failed token fetch")
				}
				return testWechatResponse(tc.status, tc.body), nil
			})
			assertMessageKind(t, client.SendReminder(context.Background(), "openid", reminderTestNotification()), tc.kind)
		})
	}
}

func TestMissingOrInvalidConfigurationNeverClaimsSuccess(t *testing.T) {
	for _, mutate := range []func(*MessageOptions){
		func(o *MessageOptions) { o.AppID = "" }, func(o *MessageOptions) { o.AppSecret = "" },
		func(o *MessageOptions) { o.ReminderTemplateID = "" }, func(o *MessageOptions) { o.MiniprogramState = "production" },
		func(o *MessageOptions) { o.ReminderTitleKey = "bogus1" }, func(o *MessageOptions) { o.ReminderTimeKey = "thing2" },
		func(o *MessageOptions) { o.ReminderContentKey = o.ReminderTitleKey },
		func(o *MessageOptions) { o.ReminderTitleKey = ""; o.ReminderTimeKey = ""; o.ReminderContentKey = "" },
	} {
		options := reminderTestOptions()
		mutate(&options)
		client := NewMessageClient(options)
		client.httpClient.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { t.Fatal("disabled client must not send"); return nil, nil })
		if client.Enabled() {
			t.Fatal("invalid configuration enabled message delivery")
		}
		assertMessageKind(t, client.SendReminder(context.Background(), "openid", reminderTestNotification()), MessageNotConfigured)
	}
	var nilClient *MessageClient
	if nilClient.Enabled() || nilClient.TemplateID() != "" {
		t.Fatal("nil sender must be disabled")
	}
	assertMessageKind(t, nilClient.SendReminder(context.Background(), "openid", reminderTestNotification()), MessageNotConfigured)
}

func TestReminderTemplateFormatsTextByUnicodeType(t *testing.T) {
	for _, tc := range []struct {
		kind, input, want string
		ok                bool
	}{
		{"thing", strings.Repeat("贴", 21), strings.Repeat("贴", 20), true},
		{"thing", strings.Repeat("😀", 21), strings.Repeat("😀", 20), true},
		{"thing", "  准时\n吃饭\t\x00 ", "准时 吃饭", true},
		{"name", "贴贴Alice贴贴Alice", "贴贴Alice贴贴A", true},
		{"name", strings.Repeat("A", 22), strings.Repeat("A", 20), true},
		{"phrase", "提醒已到时间", "提醒已到时", true},
		{"phrase", "提醒Time", "", false},
		{"character_string", strings.Repeat("a", 33), strings.Repeat("a", 32), true},
		{"character_string", "贴贴", "", false},
		{"letter", "aBC", "aBC", true}, {"letter", "a1", "", false},
		{"symbol", "!?$&+-", "!?$&+", true}, {"symbol", "abc", "", false},
		{"number", "12.34", "12.34", true}, {"number", "提醒", "", false},
		{"thing", "\n\t", "", false},
	} {
		t.Run(tc.kind+tc.input, func(t *testing.T) {
			got, ok := formatTemplateText(tc.kind, tc.input)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("got %q, %t; want %q, %t", got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestReminderOptionalFieldsAndDefaultChatPage(t *testing.T) {
	options := reminderTestOptions()
	options.ReminderTitleKey = ""
	options.ReminderTimeKey = "date2"
	options.MiniprogramState = ""
	client := NewMessageClient(options)
	notification := reminderTestNotification()
	notification.Page = ""
	client.httpClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/cgi-bin/stable_token" {
			return testWechatResponse(200, `{"access_token":"secret-token","expires_in":7200}`), nil
		}
		var body struct {
			Page  string                   `json:"page"`
			State string                   `json:"miniprogram_state"`
			Data  map[string]templateValue `json:"data"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Fatal("could not decode message")
		}
		if body.Page != "pages/index/index" || body.State != "formal" || len(body.Data) != 2 || body.Data["date2"].Value != "2026-10-05 12:30" {
			t.Fatal("optional keyword, default route or date field is incorrect")
		}
		return testWechatResponse(200, `{"errcode":0}`), nil
	})
	if err := client.SendReminder(context.Background(), "openid", notification); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidNotificationRejectedBeforeHTTP(t *testing.T) {
	for _, mutate := range []func(*ReminderNotification){
		func(n *ReminderNotification) { n.DueAt = time.Time{} }, func(n *ReminderNotification) { n.Title = "" },
		func(n *ReminderNotification) { n.Page = "https://untrusted.example/path" }, func(n *ReminderNotification) { n.Page = "pages/index/index#fragment" },
	} {
		client := NewMessageClient(reminderTestOptions())
		notification := reminderTestNotification()
		mutate(&notification)
		client.httpClient.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("invalid reminder must not contact WeChat")
			return nil, nil
		})
		assertMessageKind(t, client.SendReminder(context.Background(), "openid", notification), MessagePermanent)
	}
}

func TestTokenTransportErrorAndRedirectCannotLeakCredentials(t *testing.T) {
	for _, response := range []func(*http.Request) (*http.Response, error){
		func(r *http.Request) (*http.Response, error) {
			body, _ := io.ReadAll(r.Body)
			return nil, errors.New("failure " + r.URL.String() + string(body))
		},
		func(*http.Request) (*http.Response, error) {
			response := testWechatResponse(302, "")
			response.Header.Set("Location", "https://untrusted.example/token")
			return response, nil
		},
	} {
		client := NewMessageClient(reminderTestOptions())
		client.httpClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Host != "api.weixin.qq.com" {
				t.Fatal("token fetch followed redirect")
			}
			return response(r)
		})
		assertMessageKind(t, client.SendReminder(context.Background(), "openid", reminderTestNotification()), MessageRetryable)
	}
}
