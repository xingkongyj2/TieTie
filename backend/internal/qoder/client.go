// Package qoder 封装对 Qoder 云端 (api.qoder.com.cn) 的全部访问。
// 上游 URL、令牌注入、超时、错误码映射都锁在本包内，其余包只看到干净的 Go 结构体和 *ApiError。
package qoder

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"tietie/backend/internal/config"
)

// Client 是 Qoder 云端客户端，并发安全。
type Client struct {
	BaseURL       string
	Token         string
	Timeout       time.Duration // 普通请求超时
	UploadTimeout time.Duration // 文件上传超时
	HC            *http.Client
}

// NewClient 根据配置构造客户端。
func NewClient(cfg config.Config) *Client {
	return &Client{
		BaseURL:       strings.TrimSuffix(cfg.Upstream, "/"),
		Token:         cfg.Token,
		Timeout:       cfg.Timeout,
		UploadTimeout: cfg.UploadTimeout,
		HC: &http.Client{
			// 对应 fetch 的 redirect:'error'：不跟随重定向，直接视为连接错误。
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return errors.New("redirect not allowed")
			},
		},
	}
}

// send 是所有上游请求的唯一 HTTP 出口。
// timeout <= 0 表示不设 per-request 超时（SSE 等长连接由调用方 ctx 控制）。
func (c *Client) send(ctx context.Context, method, path, contentType string, body io.Reader, timeout time.Duration) ([]byte, error) {
	if strings.TrimSpace(c.Token) == "" {
		return nil, notConfigured()
	}
	reqCtx := ctx
	if timeout <= 0 {
		timeout = c.Timeout // 默认与 Node 版一致：15s
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(reqCtx, method, c.BaseURL+path, body)
	if err != nil {
		return nil, NewApiError(502, "connection_failed", connMessage(method))
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(c.Token))
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.HC.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, err // 调用方(客户端断开)主动取消，原样上抛
		}
		if reqCtx.Err() == context.DeadlineExceeded {
			return nil, NewApiError(504, "upstream_timeout", timeoutMessage(method))
		}
		return nil, NewApiError(502, "connection_failed", connMessage(method))
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		if ctx.Err() != nil {
			return nil, err
		}
		return nil, NewApiError(502, "connection_failed", connMessage(method))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, upstreamError(resp.StatusCode)
	}
	return data, nil
}

// doJSON 发送 JSON 请求并把响应反序列化到 out（out 可为 nil）。
func (c *Client) doJSON(ctx context.Context, method, path string, body, out any, timeout time.Duration) error {
	var reader io.Reader
	contentType := ""
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return NewApiError(500, "internal_error", "会话服务发生错误，请稍后重试。")
		}
		reader = bytes.NewReader(buf)
		contentType = "application/json"
	}
	data, err := c.send(ctx, method, path, contentType, reader, timeout)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return invalidResponse()
	}
	return nil
}

// listEnvelope 是上游分页列表的统一外壳。
type listEnvelope[T any] struct {
	Data     *[]T   `json:"data"`
	HasMore  bool   `json:"has_more"`
	NextPage string `json:"next_page"`
}

type listOpts struct {
	after  string // 增量拉取的起始事件 ID
	events bool   // 事件列表：按 asc 排序
}

// listAll 自动翻页拉全量列表（对应 qoder.mjs listAll）。
func listAll[T any](c *Client, ctx context.Context, path string, opts listOpts) ([]T, error) {
	var entries []T
	seenPages := map[string]bool{}
	page := ""
	for i := 0; i < 1000; i++ {
		q := url.Values{}
		q.Set("limit", "100")
		if opts.events {
			q.Set("order", "asc")
		}
		if page != "" {
			q.Set("page", page)
		} else if opts.after != "" {
			q.Set("after_id", opts.after)
		}
		var env listEnvelope[T]
		if err := c.doJSON(ctx, http.MethodGet, path+"?"+q.Encode(), nil, &env, 0); err != nil {
			return nil, err
		}
		if env.Data == nil {
			return nil, invalidResponse()
		}
		entries = append(entries, *env.Data...)
		if !env.HasMore {
			return entries, nil
		}
		if env.NextPage == "" || seenPages[env.NextPage] {
			return nil, invalidResponse()
		}
		page = env.NextPage
		seenPages[page] = true
	}
	return nil, NewApiError(502, "pagination_limit", "云端会话数据量过大，请稍后重试。")
}

func timeoutMessage(method string) string {
	if method == http.MethodPost {
		return "发送请求超时，消息可能已送达。请先刷新会话确认，避免重复发送。"
	}
	return "读取云端会话超时，请稍后重试。"
}

func connMessage(method string) string {
	if method == http.MethodPost {
		return "发送连接中断，消息可能已送达。请先刷新会话确认，避免重复发送。"
	}
	return "无法连接云端服务，请检查网络后重试。"
}
