package qoder

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"tietie/backend/internal/document"
)

// Attachment 是一个已通过校验的附件。
type Attachment struct {
	Kind     string // image | file | document
	Name     string
	MimeType string
	Data     []byte // image/document 的解码后二进制
	Content  string // file 的文本内容

	extracted  bool   // document 提取为文本后内部标记
	uploadName string // document 转文本后的上传名
}

// MessageInput 是一次发消息请求的规范化结果。
type MessageInput struct {
	Visibility  string
	Text        string
	Attachments []Attachment
}

// SendResult 对应 POST /api/qoder/sessions/:id/messages 的响应体。
type SendResult struct {
	ReplyMode string          `json:"replyMode,omitempty"`
	Messages  []PublicMessage `json:"messages"`
	Events    []Event         `json:"-"`
}

// SendMessage 发送一条用户消息：document 先抽文本，file 上传并挂载，image 内联 base64。
// 对应 qoder.mjs sendMessage。
func (c *Client) SendMessage(ctx context.Context, sessionID string, input MessageInput) (*SendResult, error) {
	content := []ContentBlock{}
	var mounted []string

	// 第一轮：document 附件提取为纯文本 file 附件。
	prepared := make([]Attachment, 0, len(input.Attachments))
	for _, a := range input.Attachments {
		if a.Kind != "document" {
			prepared = append(prepared, a)
			continue
		}
		extracted, err := document.ExtractText(a.Name, a.Data)
		if err != nil {
			if errors.Is(err, document.ErrUnsupportedFormat) {
				return nil, NewApiError(400, "document_unsupported",
					fmt.Sprintf("%s 为旧版格式，暂不支持解析，请转存为 .docx/.xlsx 后重试。", a.Name))
			}
			return nil, NewApiError(400, "document_parse_failed",
				fmt.Sprintf("%s 无法解析，请确认文件未损坏或加密。", a.Name))
		}
		if strings.TrimSpace(extracted) == "" {
			return nil, NewApiError(400, "document_empty", fmt.Sprintf("%s 没有可提取的文字内容。", a.Name))
		}
		if len(extracted) > maxFileBytes {
			return nil, NewApiError(413, "document_text_too_large",
				fmt.Sprintf("%s 提取后的文字超过 4 MB，请拆分文件后重试。", a.Name))
		}
		prepared = append(prepared, Attachment{
			Kind: "file", Name: a.Name, MimeType: "text/plain",
			Content: extracted, extracted: true,
			uploadName: baseNameNoExt(a.Name, 70) + ".txt",
		})
	}

	// 第二轮：图片内联，文件上传并挂载到会话。
	for _, a := range prepared {
		if a.Kind == "image" {
			content = append(content, ContentBlock{
				Type: "image",
				Source: &ImageSource{
					Type:      "base64",
					MediaType: a.MimeType,
					Data:      base64.StdEncoding.EncodeToString(a.Data),
				},
			})
			continue
		}
		uploadName := a.uploadName
		if uploadName == "" {
			uploadName = a.Name
		}
		fileID, err := c.UploadFile(ctx, uploadName, a.MimeType, []byte(a.Content))
		if err != nil {
			return nil, err
		}
		mountPath, err := c.MountResource(ctx, sessionID, fileID)
		if err != nil {
			return nil, err
		}
		label := a.Name
		if a.extracted {
			label += "（已提取为文本）"
		}
		mounted = append(mounted, label+"："+mountPath)
	}

	var promptParts []string
	if input.Text != "" {
		promptParts = append(promptParts, input.Text)
	}
	if len(mounted) > 0 {
		promptParts = append(promptParts, uploadMarker+strings.Join(mounted, "\n"))
	}
	if prompt := strings.Join(promptParts, "\n\n"); prompt != "" {
		content = append([]ContentBlock{{Type: "text", Text: prompt}}, content...)
	}

	body := map[string]any{
		"events": []map[string]any{{"type": "user.message", "content": content}},
	}
	return c.postEvents(ctx, sessionID, body)
}

// SendCustomToolResult 回传自定义工具（AskUserQuestion）的执行结果，让云端挂起的那一轮继续。
// toolUseID 必须是 agent.custom_tool_use 事件自身的 ID。
func (c *Client) SendCustomToolResult(ctx context.Context, sessionID, toolUseID, text string) (*SendResult, error) {
	body := map[string]any{"events": []map[string]any{{
		"type":               "user.custom_tool_result",
		"custom_tool_use_id": toolUseID,
		"content":            []ContentBlock{{Type: "text", Text: text}},
	}}}
	return c.postEvents(ctx, sessionID, body)
}

// postEvents 是向 /sessions/{id}/events 投递事件的统一出口。
func (c *Client) postEvents(ctx context.Context, sessionID string, body map[string]any) (*SendResult, error) {
	var env struct {
		Data *[]Event `json:"data"`
	}
	path := "/sessions/" + url.PathEscape(sessionID) + "/events"
	if err := c.doJSON(ctx, http.MethodPost, path, body, &env, 0); err != nil {
		return nil, err
	}
	if env.Data == nil {
		return nil, invalidResponse()
	}
	return &SendResult{Messages: publicMessages(*env.Data), Events: *env.Data}, nil
}

// OpenEventStream 打开上游 SSE 事件流；调用方负责 Close 返回的 ReadCloser。
// after 非空时作为 Last-Event-ID 续传。对应 qoder.mjs openStream。
func (c *Client) OpenEventStream(ctx context.Context, sessionID, after string) (io.ReadCloser, error) {
	if strings.TrimSpace(c.Token) == "" {
		return nil, notConfigured()
	}
	q := url.Values{}
	q.Add("event_deltas[]", "agent.message")
	q.Add("event_deltas[]", "agent.thinking")
	streamURL := c.BaseURL + "/sessions/" + url.PathEscape(sessionID) + "/events/stream?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, streamURL, nil)
	if err != nil {
		return nil, NewApiError(502, "connection_failed", "实时连接云端失败，正在尝试重新连接。")
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(c.Token))
	req.Header.Set("Accept", "text/event-stream")
	if after != "" {
		req.Header.Set("Last-Event-ID", after)
	}
	resp, err := c.HC.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, err // 客户端断开
		}
		return nil, NewApiError(502, "connection_failed", "实时连接云端失败，正在尝试重新连接。")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, upstreamError(resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		resp.Body.Close()
		return nil, invalidResponse()
	}
	return resp.Body, nil
}

// ---- SSE 帧转换 ----

// streamPayload 是上游 SSE data 行的联合结构：
// 可能是 event_start / event_delta，也可能是一个完整事件。
type streamPayload struct {
	Type  string `json:"type"`
	Event *Event `json:"event"` // event_start

	EventID string `json:"event_id"` // event_delta
	Delta   *struct {
		Type    string        `json:"type"`
		Content *ContentBlock `json:"content"`
	} `json:"delta"`

	// 完整事件时 payload 自身即事件
	ID          string         `json:"id"`
	Content     []ContentBlock `json:"content"`
	ProcessedAt string         `json:"processed_at"`
	Error       *eventError    `json:"error"`
}

// ParseStreamEvent 把一条上游 SSE 帧转换为发给前端的公开事件。
// 返回 nil 表示该帧应被忽略。对应 qoder.mjs publicStreamEvent。
func ParseStreamEvent(data []byte) map[string]any {
	var p streamPayload
	if err := json.Unmarshal(data, &p); err != nil || p.Type == "" {
		return nil
	}
	switch p.Type {
	case "event_start":
		if p.Event == nil || !eventIDRe.MatchString(p.Event.ID) {
			return nil
		}
		if p.Event.Type != "agent.message" && p.Event.Type != "agent.thinking" {
			return nil
		}
		kind := "message"
		if p.Event.Type == "agent.thinking" {
			kind = "thinking"
		}
		return map[string]any{"type": "start", "id": p.Event.ID, "kind": kind}
	case "event_delta":
		if !eventIDRe.MatchString(p.EventID) {
			return nil
		}
		if p.Delta == nil || p.Delta.Type != "content_delta" ||
			p.Delta.Content == nil || p.Delta.Content.Type != "text" {
			return nil
		}
		text := p.Delta.Content.Text
		if r := []rune(text); len(r) > 16384 {
			text = string(r[:16384])
		}
		return map[string]any{"type": "delta", "id": p.EventID, "text": text}
	}
	if !eventIDRe.MatchString(p.ID) {
		return nil
	}
	switch {
	case p.Type == "user.message" || p.Type == "agent.message":
		ev := Event{ID: p.ID, Type: p.Type, Content: p.Content, ProcessedAt: p.ProcessedAt, Error: p.Error}
		msgs := publicMessages([]Event{ev})
		if len(msgs) == 0 {
			if message := replyParseError(ev); message != "" {
				return map[string]any{"type": "session_error", "id": p.ID, "message": message}
			}
			return nil // Hidden server wakeups have no public message or placeholder.
		}
		return map[string]any{"type": "message", "id": p.ID, "message": msgs[0]}
	case p.Type == "agent.thinking":
		return map[string]any{"type": "thinking_end", "id": p.ID}
	case strings.HasPrefix(p.Type, "session.status_"):
		return map[string]any{"type": "status", "id": p.ID, "status": strings.TrimPrefix(p.Type, "session.status_")}
	case p.Type == "session.error":
		ev := Event{ID: p.ID, Type: p.Type, Error: p.Error}
		return map[string]any{"type": "session_error", "id": p.ID, "message": publicTurnError(&ev)}
	case p.Type == "session.deleted":
		return map[string]any{"type": "status", "id": p.ID, "status": "terminated"}
	}
	return nil
}

// baseNameNoExt 去掉扩展名并按 rune 截断（对应 mjs 的 replace + slice(0,70)）。
func baseNameNoExt(name string, max int) string {
	base := name
	if i := strings.LastIndex(base, "."); i >= 0 && i < len(base)-1 {
		base = base[:i]
	}
	if r := []rune(base); len(r) > max {
		base = string(r[:max])
	}
	return base
}
