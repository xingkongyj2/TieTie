package qoder

import (
	"encoding/base64"
	"regexp"
	"strings"
	"time"
)

// 上游 ID 与 base64 的格式约束（对应 qoder.mjs 顶部正则）。
var (
	sessionIDRe = regexp.MustCompile(`^sess_[A-Za-z0-9_-]{1,160}$`)
	eventIDRe   = regexp.MustCompile(`^evt_[A-Za-z0-9_-]{1,160}$`)
	fileIDRe    = regexp.MustCompile(`^file_[A-Za-z0-9_-]{1,160}$`)
	base64Re    = regexp.MustCompile(`^[A-Za-z0-9+/]+={0,2}$`)

	imageDimensionRe = regexp.MustCompile(`(?i)image length and width|height:1 or width:1|image.*(size|dimension)`)
	uploadLineRe     = regexp.MustCompile(`^.+：/mnt/session/uploads/[A-Za-z0-9._-]+$`)
)

const (
	maxImageBase64 = 10 * 1024 * 1024
	maxFileBytes   = 4 * 1024 * 1024
)

var imageTypes = map[string]bool{
	"image/png": true, "image/jpeg": true, "image/webp": true, "image/gif": true,
}

// ValidSessionID 校验云端会话 ID 格式，供 api 层路由参数校验。
func ValidSessionID(id string) bool { return sessionIDRe.MatchString(id) }

// ValidEventID 校验云端事件 ID 格式，供 api 层游标校验。
func ValidEventID(id string) bool { return eventIDRe.MatchString(id) }

// ValidBase64 校验宽松的 base64 字符串（允许无填充），供 api 层附件校验。
func ValidBase64(s string) bool { return base64Re.MatchString(s) }

// DecodeBase64 解码附件里的 base64 数据（兼容带/不带 = 填充）。
func DecodeBase64(s string) ([]byte, error) {
	if pad := len(s) % 4; pad != 0 {
		s += strings.Repeat("=", 4-pad)
	}
	return base64.StdEncoding.DecodeString(s)
}

// ---- 上游原始结构 ----

type rawAgent struct {
	Name string `json:"name"`
}

type rawSession struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	Status     string    `json:"status"`
	ArchivedAt string    `json:"archived_at"`
	CreatedAt  string    `json:"created_at"`
	UpdatedAt  string    `json:"updated_at"`
	Agent      *rawAgent `json:"agent"`
}

// ImageSource 是图片内容块的 base64 源。
type ImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

// ContentBlock 是事件内容块（text / image / 其他）。
type ContentBlock struct {
	Type   string       `json:"type"`
	Text   string       `json:"text,omitempty"`
	Source *ImageSource `json:"source,omitempty"`
}

type eventError struct {
	Message string `json:"message"`
}

// Event 是上游会话事件。
type Event struct {
	ID          string         `json:"id"`
	Type        string         `json:"type"`
	Content     []ContentBlock `json:"content"`
	ProcessedAt string         `json:"processed_at"`
	Error       *eventError    `json:"error"`
}

// ---- 对前端的公开结构 ----

// PublicSession 是脱敏后的会话信息。
type PublicSession struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
	AgentName string `json:"agentName"`
}

// PublicMessage 是脱敏后的聊天消息。
type PublicMessage struct {
	ID        string   `json:"id"`
	Sender    string   `json:"sender"` // self | ai
	Text      string   `json:"text"`
	Images    []string `json:"images,omitempty"`
	Time      string   `json:"time"`
	CreatedAt string   `json:"createdAt"`
	Kind      string   `json:"kind"`
}

// publicSession 转换并校验上游会话（对应 qoder.mjs publicSession）。
func publicSession(s *rawSession) (*PublicSession, error) {
	if s == nil || !sessionIDRe.MatchString(s.ID) {
		return nil, invalidResponse()
	}
	out := &PublicSession{
		ID:        s.ID,
		Title:     "未命名会话",
		Status:    "unknown",
		CreatedAt: s.CreatedAt,
		UpdatedAt: s.UpdatedAt,
		AgentName: "云端助手",
	}
	if strings.TrimSpace(s.Title) != "" {
		out.Title = s.Title
	}
	if s.ArchivedAt != "" {
		out.Status = "archived"
	} else if s.Status != "" {
		out.Status = s.Status
	}
	if s.Agent != nil && s.Agent.Name != "" {
		out.AgentName = s.Agent.Name
	}
	return out, nil
}

// publicMessages 把上游事件转换为前端消息列表（对应 qoder.mjs publicMessages）。
func publicMessages(events []Event) []PublicMessage {
	out := []PublicMessage{}
	for _, ev := range events {
		if ev.Type != "user.message" && ev.Type != "agent.message" {
			continue
		}
		if !eventIDRe.MatchString(ev.ID) {
			continue
		}
		var images []string
		for _, block := range ev.Content {
			if block.Type == "image" && block.Source != nil && block.Source.Type == "base64" &&
				imageTypes[block.Source.MediaType] && len(block.Source.Data) <= maxImageBase64 &&
				base64Re.MatchString(block.Source.Data) {
				images = append(images, "data:"+block.Source.MediaType+";base64,"+block.Source.Data)
				if len(images) == 4 {
					break
				}
			}
		}
		hasImages := len(images) > 0
		var parts []string
		for _, block := range ev.Content {
			switch {
			case block.Type == "text":
				text := block.Text
				if ev.Type == "user.message" {
					text = displayUserText(text)
				}
				if text != "" {
					parts = append(parts, text)
				}
			case block.Type == "image":
				if !(block.Source != nil && block.Source.Type == "base64" && hasImages) {
					parts = append(parts, "[图片]")
				}
			default:
				parts = append(parts, "[暂不支持的消息内容]")
			}
		}
		text := strings.Join(parts, "\n")
		if strings.TrimSpace(text) == "" && !hasImages {
			continue
		}
		msg := PublicMessage{
			ID:        ev.ID,
			Sender:    "ai",
			Text:      text,
			Time:      formatClock(ev.ProcessedAt),
			CreatedAt: ev.ProcessedAt,
			Kind:      "text",
		}
		if ev.Type == "user.message" {
			msg.Sender = "self"
		}
		if hasImages {
			msg.Images = images
		}
		out = append(out, msg)
	}
	return out
}

// uploadMarker 是发送附件时拼接在用户文本后的固定标记（与 sendMessage 保持一致）。
const uploadMarker = "我还附上了这些文件，请按需读取：\n"

// displayUserText 把历史消息里的挂载路径列表还原成 📎 文件名 的展示形式。
func displayUserText(value string) string {
	at := strings.LastIndex(value, uploadMarker)
	if at == -1 {
		return value
	}
	lines := strings.Split(value[at+len(uploadMarker):], "\n")
	for _, line := range lines {
		if !uploadLineRe.MatchString(line) {
			return value
		}
	}
	prefix := strings.TrimRight(value[:at], " \t\r\n")
	names := make([]string, 0, len(lines))
	for _, line := range lines {
		name, _, _ := strings.Cut(line, "：/mnt/session/uploads/")
		names = append(names, "📎 "+name)
	}
	joined := strings.Join(names, "\n")
	if prefix != "" {
		return prefix + "\n\n" + joined
	}
	return joined
}

// publicTurnError 把上游 session.error 事件转换成用户可读的提示。
func publicTurnError(ev *Event) string {
	reason := ""
	if ev != nil && ev.Error != nil {
		reason = ev.Error.Message
	}
	if imageDimensionRe.MatchString(reason) {
		return "图片尺寸不符合当前模型要求。若这张图片留在会话历史中，请切换到新会话继续。"
	}
	return "云端处理这条消息时出错，请检查附件后重试。"
}

// formatClock 把 ISO 时间格式化成 zh-CN 风格的 HH:mm（本地时区），解析失败返回空串。
func formatClock(iso string) string {
	if iso == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return ""
	}
	return t.Local().Format("15:04")
}
