package qoder

import (
	"encoding/base64"
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"tietie/backend/internal/conversation"
	"tietie/backend/internal/weather"
)

// 上游 ID 与 base64 的格式约束（对应 qoder.mjs 顶部正则）。
var (
	sessionIDRe = regexp.MustCompile(`^sess_[A-Za-z0-9_-]{1,160}$`)
	eventIDRe   = regexp.MustCompile(`^evt_[A-Za-z0-9_-]{1,160}$`)
	fileIDRe    = regexp.MustCompile(`^file_[A-Za-z0-9_-]{1,160}$`)
	base64Re    = regexp.MustCompile(`^[A-Za-z0-9+/]+={0,2}$`)

	imageDimensionRe = regexp.MustCompile(`(?i)image length and width|height:1 or width:1|image.*(size|dimension)`)
	uploadLineRe     = regexp.MustCompile(`^(.+)：/mnt/session/uploads/[^\r\n]+$`)
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
	ID              string          `json:"id"`
	Type            string          `json:"type"`
	Content         []ContentBlock  `json:"content"`
	ProcessedAt     string          `json:"processed_at"`
	Error           *eventError     `json:"error"`
	Name            string          `json:"name"`
	Input           json.RawMessage `json:"input"`
	CustomToolUseID string          `json:"custom_tool_use_id"`
}

// ---- 对前端的公开结构 ----

// PublicSession 是脱敏后的会话信息。
type PublicSession struct {
	ReplyMode string `json:"replyMode,omitempty"`
	ID        string `json:"id"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
	AgentName string `json:"agentName"`
}

// PublicMessage 是脱敏后的聊天消息。
type PublicMessage struct {
	WeatherCards    []weather.Card        `json:"weatherCards,omitempty"`
	ReplyMode       string                `json:"replyMode,omitempty"`
	InputSessionID  string                `json:"-"`
	Visibility      string                `json:"visibility,omitempty"`
	PrivateOwnerID  int64                 `json:"-"`
	ID              string                `json:"id"`
	Sender          string                `json:"sender"` // self | partner | user (unknown legacy author) | ai
	Text            string                `json:"text"`
	Images          []string              `json:"images,omitempty"`
	Files           []string              `json:"files,omitempty"`
	Time            string                `json:"time"`
	CreatedAt       string                `json:"createdAt"`
	Kind            string                `json:"kind"`
	UserID          int64                 `json:"userId,omitempty"`
	DisplayName     string                `json:"displayName,omitempty"`
	RecipientIDs    []int64               `json:"recipientIds,omitempty"`
	Source          string                `json:"source,omitempty"`
	ReminderIDs     []string              `json:"reminderIds,omitempty"`
	ReminderError   string                `json:"reminderError,omitempty"`
	Actions         []conversation.Action `json:"-"`
	ProtocolError   string                `json:"-"`
	ProtocolVersion int                   `json:"-"`
	RequestID       string                `json:"-"`
	// Kind 为 ask 时：Agent 通过自定义工具（AskUserQuestion）抛出的选择题。
	Ask      []AskQuestion `json:"ask,omitempty"`
	Answered bool          `json:"answered,omitempty"`
}

// AskQuestion 是一道选择题，字段名与云端工具 input 保持一致。
type AskQuestion struct {
	Header      string      `json:"header,omitempty"`
	Question    string      `json:"question"`
	MultiSelect bool        `json:"multiSelect,omitempty"`
	Options     []AskOption `json:"options,omitempty"`
}

// AskOption 是一道题里的一个选项。
type AskOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
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

// IsTurnCancellationMarker identifies Qoder's internal acknowledgement.
func IsTurnCancellationMarker(text string) bool {
	return strings.EqualFold(strings.TrimSpace(text), "Turn cancelled")
}

// publicMessages 把上游事件转换为前端消息列表（对应 qoder.mjs publicMessages）。
func publicMessages(events []Event) []PublicMessage {
	// 云端在等待工具应答时会把会话标成 idle，所以"是否已回答"只能靠事件配对判断。
	answered := make(map[string]bool)
	for _, ev := range events {
		if ev.Type == "user.custom_tool_result" && ev.CustomToolUseID != "" {
			answered[ev.CustomToolUseID] = true
		}
	}
	out := []PublicMessage{}
	for _, ev := range events {
		if !eventIDRe.MatchString(ev.ID) {
			continue
		}
		if ev.Type == "agent.custom_tool_use" {
			questions, ok := parseAskInput(ev.Input)
			if !ok {
				continue
			}
			out = append(out, PublicMessage{
				ID: ev.ID, Sender: "ai", Text: questions[0].Question, Kind: "ask",
				Source: "chat",
				Ask:    questions, Answered: answered[ev.ID],
				Time: formatClock(ev.ProcessedAt), CreatedAt: ev.ProcessedAt,
			})
			continue
		}
		if ev.Type != "user.message" && ev.Type != "agent.message" {
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
		var decorations []string
		for _, block := range ev.Content {
			switch {
			case block.Type == "text":
				if block.Text != "" {
					parts = append(parts, block.Text)
				}
			case block.Type == "image":
				if !(block.Source != nil && block.Source.Type == "base64" && hasImages) {
					decorations = append(decorations, "[图片]")
				}
			default:
				decorations = append(decorations, "[暂不支持的消息内容]")
			}
		}
		msg := PublicMessage{
			ID:        ev.ID,
			Sender:    "ai",
			Text:      strings.Join(parts, "\n"),
			Time:      formatClock(ev.ProcessedAt),
			CreatedAt: ev.ProcessedAt,
			Kind:      "text",
			Source:    "chat",
		}
		if ev.Type == "user.message" {
			// Old unwrapped events do not identify their author. They must not be
			// presented as the current viewer's own messages in a shared space.
			msg.Sender = "user"
			if input, ok := conversation.DecodeInput(msg.Text); ok {
				if input.Hidden {
					continue
				}
				msg.Text = input.Text
				// Transport mount instructions are for the AI. Keep authenticated
				// member text literal and expose only separate attachment names.
				_, msg.Files = splitUserAttachments(input.Tail)
				msg.UserID, msg.DisplayName = input.UserID, input.DisplayName
				msg.InputSessionID = input.Context.SessionID
				msg.ReplyMode = input.Context.ReplyMode
				if input.Context.RecipientID != 0 {
					msg.RecipientIDs = []int64{input.Context.RecipientID}
				}
				if msg.UserID > 0 {
					msg.Sender = "self" // The API maps this identity relative to its viewer.
				}
			} else {
				msg.Text, msg.Files = splitUserAttachments(msg.Text)
			}
		} else {
			// Qoder emits this internal acknowledgement as an agent.message after
			// cancellation. It is not an assistant reply for the chat history.
			if IsTurnCancellationMarker(msg.Text) {
				continue
			}
			assistant := conversation.ParseAssistant(msg.Text)
			msg.Text, msg.RecipientIDs, msg.Source = assistant.Text, assistant.RecipientIDs, assistant.Source
			if assistant.Control {
				continue
			}
			msg.ProtocolVersion, msg.RequestID = assistant.Version, assistant.RequestID
			msg.Actions, msg.ProtocolError = assistant.Actions, assistant.ProtocolError
		}
		if len(decorations) > 0 {
			if msg.Text != "" {
				msg.Text += "\n"
			}
			msg.Text += strings.Join(decorations, "\n")
		}
		if strings.TrimSpace(msg.Text) == "" && !hasImages && len(msg.Files) == 0 && len(msg.Actions) == 0 {
			continue
		}
		if hasImages {
			msg.Images = images
		}
		out = append(out, msg)
	}
	return out
}

// parseAskInput 解析 agent.custom_tool_use 的 input：只认带 questions 的提问类工具。
// 其他自定义工具应用内无法应答，返回 false 让调用方跳过，避免渲染出答不了的卡片。
func parseAskInput(raw json.RawMessage) ([]AskQuestion, bool) {
	var payload struct {
		Questions []AskQuestion `json:"questions"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &payload) != nil {
		return nil, false
	}
	questions := make([]AskQuestion, 0, len(payload.Questions))
	for _, q := range payload.Questions {
		q.Question = strings.TrimSpace(q.Question)
		if q.Question == "" {
			continue
		}
		options := make([]AskOption, 0, len(q.Options))
		for _, o := range q.Options {
			o.Label = strings.TrimSpace(o.Label)
			if o.Label == "" {
				continue
			}
			options = append(options, o)
		}
		q.Options = options
		questions = append(questions, q)
	}
	if len(questions) == 0 {
		return nil, false
	}
	return questions, true
}

// uploadMarker 是发送附件时拼接在用户文本后的固定标记（与 sendMessage 保持一致）。
const uploadMarker = "我还附上了这些文件，请按需读取：\n"

// splitUserAttachments separates transport metadata from display text. Mount
// paths can contain Unicode, spaces and parentheses, just like uploaded names.
func splitUserAttachments(value string) (string, []string) {
	at := strings.LastIndex(value, uploadMarker)
	if at == -1 {
		return value, nil
	}
	lines := strings.Split(strings.TrimSpace(value[at+len(uploadMarker):]), "\n")
	names := make([]string, 0, len(lines))
	for _, line := range lines {
		match := uploadLineRe.FindStringSubmatch(strings.TrimSuffix(line, "\r"))
		if len(match) != 2 {
			return value, nil
		}
		name := strings.TrimSuffix(match[1], "（已提取为文本）")
		names = append(names, name)
	}
	prefix := strings.TrimRight(value[:at], " \t\r\n")
	return prefix, names
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
