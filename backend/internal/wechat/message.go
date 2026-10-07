package wechat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
)

const (
	stableTokenURL     = "https://api.weixin.qq.com/cgi-bin/stable_token"
	subscribeSendURL   = "https://api.weixin.qq.com/cgi-bin/message/subscribe/send"
	maxMessageResponse = 64 * 1024
)

// ReminderNotification is the reminder preview shown in WeChat. Page must name
// a page in this mini-program; full reminder content remains in the chat.
type ReminderNotification struct {
	NotificationType string
	Title            string
	Content          string
	DueAt            time.Time
	Page             string
}

type MessageSender interface {
	Enabled() bool
	TemplateID() string
	SendReminder(context.Context, string, ReminderNotification) error
}

// MessageOptions are server-only credentials and the keyword names of the
// template selected in the WeChat console. Empty keyword names omit a field.
type MessageOptions struct {
	AppID              string
	AppSecret          string
	ReminderTemplateID string
	ReminderTitleKey   string
	ReminderTimeKey    string
	ReminderContentKey string
	ReminderTypeKey    string
	ReminderSourceKey  string
	MiniprogramState   string
	Timeout            time.Duration
}

type MessageErrorKind string

const (
	MessageNotConfigured MessageErrorKind = "not_configured"
	MessageUnauthorized  MessageErrorKind = "unauthorized"
	MessagePermanent     MessageErrorKind = "permanent"
	MessageRetryable     MessageErrorKind = "retryable"
	MessageUncertain     MessageErrorKind = "uncertain"
)

// MessageError carries only safe classifications and numeric WeChat codes.
// In particular, transport errors, URLs and WeChat errmsg are never retained.
// Uncertain means the send request may have been accepted: do not retry it.
type MessageError struct {
	Kind MessageErrorKind
	Code int
}

func (e *MessageError) Error() string {
	if e.Code != 0 {
		return fmt.Sprintf("wechat reminder message %s (code %d)", e.Kind, e.Code)
	}
	return "wechat reminder message " + string(e.Kind)
}

func MessageErrorKindOf(err error) MessageErrorKind {
	var messageError *MessageError
	if errors.As(err, &messageError) {
		return messageError.Kind
	}
	return ""
}

func IsMessageRetryable(err error) bool    { return MessageErrorKindOf(err) == MessageRetryable }
func IsMessageUnauthorized(err error) bool { return MessageErrorKindOf(err) == MessageUnauthorized }
func IsMessageUncertain(err error) bool    { return MessageErrorKindOf(err) == MessageUncertain }

// MessageClient uses the stable-token API separately from login code exchange.
// Its token cache is shared by all reminder deliveries in this server process.
type MessageClient struct {
	options        MessageOptions
	httpClient     *http.Client
	configured     bool
	tokenMu        sync.Mutex
	accessToken    string
	tokenExpiresAt time.Time
	now            func() time.Time
}

var (
	templateKeyword = regexp.MustCompile(`^([a-z_]+)[0-9]+$`)
	templateNumber  = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)?$`)
)

func NewMessageClient(options MessageOptions) *MessageClient {
	options.AppID = strings.TrimSpace(options.AppID)
	options.AppSecret = strings.TrimSpace(options.AppSecret)
	options.ReminderTemplateID = strings.TrimSpace(options.ReminderTemplateID)
	options.ReminderTitleKey = strings.TrimSpace(options.ReminderTitleKey)
	options.ReminderTimeKey = strings.TrimSpace(options.ReminderTimeKey)
	options.ReminderContentKey = strings.TrimSpace(options.ReminderContentKey)
	options.ReminderTypeKey = strings.TrimSpace(options.ReminderTypeKey)
	options.ReminderSourceKey = strings.TrimSpace(options.ReminderSourceKey)
	options.MiniprogramState = strings.TrimSpace(options.MiniprogramState)
	if options.MiniprogramState == "" {
		options.MiniprogramState = "formal"
	}
	if options.Timeout <= 0 {
		options.Timeout = 10 * time.Second
	}
	c := &MessageClient{
		options: options,
		httpClient: &http.Client{
			Timeout: options.Timeout,
			// Both endpoints carry credentials. Never forward them to redirects.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		now: time.Now,
	}
	c.configured = validMessageOptions(options)
	return c
}

func validMessageOptions(options MessageOptions) bool {
	if options.AppID == "" || options.AppSecret == "" || options.ReminderTemplateID == "" {
		return false
	}
	if options.MiniprogramState != "formal" && options.MiniprogramState != "trial" && options.MiniprogramState != "developer" {
		return false
	}
	keys := []string{options.ReminderTitleKey, options.ReminderTimeKey, options.ReminderContentKey, options.ReminderTypeKey, options.ReminderSourceKey}
	seen := map[string]bool{}
	for i, key := range keys {
		if key == "" {
			continue
		}
		if seen[key] {
			return false
		}
		seen[key] = true
		kind := keywordKind(key)
		if i == 1 {
			if kind != "time" && kind != "date" {
				return false
			}
		} else {
			switch kind {
			case "thing", "name", "phrase", "character_string", "letter", "symbol", "number":
			default:
				return false
			}
		}
	}
	return len(seen) > 0
}

func (c *MessageClient) Enabled() bool { return c != nil && c.configured }
func (c *MessageClient) TemplateID() string {
	if c == nil {
		return ""
	}
	return c.options.ReminderTemplateID
}

func (c *MessageClient) SendReminder(ctx context.Context, openID string, notification ReminderNotification) error {
	if !c.Enabled() {
		return &MessageError{Kind: MessageNotConfigured}
	}
	openID = strings.TrimSpace(openID)
	if openID == "" || len(openID) > 128 {
		return &MessageError{Kind: MessagePermanent, Code: 40003}
	}
	data, err := c.reminderData(notification)
	if err != nil {
		return err
	}
	page := strings.TrimSpace(notification.Page)
	if page == "" {
		page = "pages/index/index"
	}
	pageURL, err := url.Parse(page)
	if err != nil || pageURL.IsAbs() || pageURL.Host != "" || pageURL.Fragment != "" || !strings.HasPrefix(pageURL.Path, "pages/") || len(page) > 1024 || strings.IndexFunc(page, unicode.IsControl) >= 0 {
		return &MessageError{Kind: MessagePermanent, Code: 41030}
	}
	payload := struct {
		ToUser           string                   `json:"touser"`
		TemplateID       string                   `json:"template_id"`
		Page             string                   `json:"page"`
		Data             map[string]templateValue `json:"data"`
		MiniprogramState string                   `json:"miniprogram_state"`
		Lang             string                   `json:"lang"`
	}{openID, c.options.ReminderTemplateID, page, data, c.options.MiniprogramState, "zh_CN"}
	encoded, _ := json.Marshal(payload)
	token, err := c.token(ctx, "")
	if err != nil {
		return err
	}
	for attempt := 0; attempt < 2; attempt++ {
		code, err := c.send(ctx, token, encoded)
		if err != nil {
			return err
		}
		if code == 0 {
			return nil
		}
		if isInvalidMessageToken(code) && attempt == 0 {
			// WeChat explicitly rejected this attempt, so one resend with a
			// renewed token is safe. Do not force-refresh others' valid tokens.
			token, err = c.token(ctx, token)
			if err != nil {
				return err
			}
			continue
		}
		return classifyMessageCode(code)
	}
	return &MessageError{Kind: MessagePermanent}
}

func isInvalidMessageToken(code int) bool { return code == 40001 || code == 40014 || code == 42001 }

func classifyMessageCode(code int) error {
	switch code {
	case 43101:
		return &MessageError{Kind: MessageUnauthorized, Code: code}
	case -1, 45011, 43108:
		// These are explicit rejections, not an ambiguous transport failure.
		return &MessageError{Kind: MessageRetryable, Code: code}
	default:
		return &MessageError{Kind: MessagePermanent, Code: code}
	}
}

func (c *MessageClient) token(ctx context.Context, rejectedToken string) (string, error) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	if ctx.Err() != nil {
		return "", &MessageError{Kind: MessageRetryable}
	}
	if rejectedToken != "" && c.accessToken == rejectedToken {
		c.accessToken = ""
		c.tokenExpiresAt = time.Time{}
	}
	if c.accessToken != "" && c.now().Before(c.tokenExpiresAt) {
		return c.accessToken, nil
	}
	encoded, _ := json.Marshal(struct {
		GrantType    string `json:"grant_type"`
		AppID        string `json:"appid"`
		Secret       string `json:"secret"`
		ForceRefresh bool   `json:"force_refresh"`
	}{"client_credential", c.options.AppID, c.options.AppSecret, false})
	response, err := c.post(ctx, stableTokenURL, encoded)
	// No message has been attempted yet, so retrying a failed token fetch is safe.
	if err != nil {
		return "", &MessageError{Kind: MessageRetryable}
	}
	var body struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		ErrCode     int    `json:"errcode"`
	}
	if json.Unmarshal(response, &body) != nil {
		return "", &MessageError{Kind: MessageRetryable}
	}
	if body.ErrCode != 0 {
		return "", classifyMessageCode(body.ErrCode)
	}
	if body.AccessToken == "" || len(body.AccessToken) > 4096 || strings.IndexFunc(body.AccessToken, unicode.IsSpace) >= 0 || body.ExpiresIn <= 0 || body.ExpiresIn > 7200 {
		return "", &MessageError{Kind: MessageRetryable}
	}
	// Refresh before expiration; short lived responses retain a proportional margin.
	lifetime := time.Duration(body.ExpiresIn) * time.Second
	margin := 2 * time.Minute
	if lifetime <= margin {
		margin = lifetime / 10
	}
	c.accessToken = body.AccessToken
	c.tokenExpiresAt = c.now().Add(lifetime - margin)
	return c.accessToken, nil
}

func (c *MessageClient) send(ctx context.Context, token string, encoded []byte) (int, error) {
	if ctx.Err() != nil {
		return 0, &MessageError{Kind: MessageRetryable}
	}
	query := url.Values{"access_token": {token}}
	response, err := c.post(ctx, subscribeSendURL+"?"+query.Encode(), encoded)
	// Sending is not idempotent. A timeout, HTTP error, malformed response or
	// body-read failure may happen after acceptance; never blindly resend it.
	if err != nil {
		return 0, &MessageError{Kind: MessageUncertain}
	}
	var body struct {
		ErrCode *int `json:"errcode"`
	}
	if json.Unmarshal(response, &body) != nil || body.ErrCode == nil {
		return 0, &MessageError{Kind: MessageUncertain}
	}
	return *body.ErrCode, nil
}

func (c *MessageClient) post(ctx context.Context, endpoint string, encoded []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return nil, errors.New("wechat request failed")
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := c.httpClient.Do(req)
	if err != nil {
		return nil, errors.New("wechat request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, errors.New("wechat response failed")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxMessageResponse+1))
	if err != nil || len(data) > maxMessageResponse {
		return nil, errors.New("wechat response failed")
	}
	return data, nil
}

type templateValue struct {
	Value string `json:"value"`
}

func keywordKind(key string) string {
	parts := templateKeyword.FindStringSubmatch(key)
	if len(parts) != 2 {
		return ""
	}
	return parts[1]
}

func (c *MessageClient) reminderData(notification ReminderNotification) (map[string]templateValue, error) {
	data := make(map[string]templateValue)
	notificationType := strings.TrimSpace(notification.NotificationType)
	if notificationType == "" {
		notificationType = "待办到期提醒"
	}
	for _, field := range []struct{ key, text string }{
		{c.options.ReminderTitleKey, notification.Title},
		{c.options.ReminderContentKey, notification.Content},
		{c.options.ReminderTypeKey, notificationType},
		{c.options.ReminderSourceKey, "贴贴清单"},
	} {
		if field.key == "" {
			continue
		}
		text, ok := formatTemplateText(keywordKind(field.key), field.text)
		if !ok {
			return nil, &MessageError{Kind: MessagePermanent, Code: 47003}
		}
		data[field.key] = templateValue{Value: text}
	}
	if c.options.ReminderTimeKey != "" {
		if notification.DueAt.IsZero() {
			return nil, &MessageError{Kind: MessagePermanent, Code: 47003}
		}
		// Reminders are presented in the product's China timezone, independent
		// of the server host timezone. Both time/date accept this official format.
		data[c.options.ReminderTimeKey] = templateValue{Value: notification.DueAt.In(time.FixedZone("Asia/Shanghai", 8*60*60)).Format("2006-01-02 15:04")}
	}
	return data, nil
}

func formatTemplateText(kind, value string) (string, bool) {
	value = strings.Join(strings.Fields(value), " ")
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, value)
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	runes := []rune(value)
	limit := 20
	switch kind {
	case "thing":
	case "name":
		for _, r := range runes {
			if !unicode.Is(unicode.Han, r) && !isASCIILetter(r) && !unicode.IsSymbol(r) && !unicode.IsPunct(r) && r != ' ' {
				return "", false
			}
			if unicode.Is(unicode.Han, r) {
				limit = 10
			}
		}
	case "phrase":
		limit = 5
		for _, r := range runes {
			if !unicode.Is(unicode.Han, r) {
				return "", false
			}
		}
	case "character_string":
		limit = 32
		for _, r := range runes {
			if !isASCIILetter(r) && (r < '0' || r > '9') && !unicode.IsSymbol(r) && !unicode.IsPunct(r) {
				return "", false
			}
		}
	case "letter":
		limit = 32
		for _, r := range runes {
			if !isASCIILetter(r) {
				return "", false
			}
		}
	case "symbol":
		limit = 5
		for _, r := range runes {
			if !unicode.IsSymbol(r) && !unicode.IsPunct(r) {
				return "", false
			}
		}
	case "number":
		limit = 32
		if !templateNumber.MatchString(value) {
			return "", false
		}
	default:
		return "", false
	}
	if len(runes) > limit {
		runes = runes[:limit]
	}
	result := string(runes)
	if kind == "number" {
		result = strings.TrimSuffix(result, ".")
	}
	return result, true
}

func isASCIILetter(r rune) bool { return r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' }
