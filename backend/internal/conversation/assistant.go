package conversation

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

type RegionUpdate struct {
	Province string `json:"province"`
	City     string `json:"city"`
	District string `json:"district"`
	Clear    bool   `json:"clear,omitempty"`
}

type Action struct {
	WeatherWhen     string            `json:"weatherWhen,omitempty"`
	TargetUserID    int64             `json:"targetUserId,omitempty"`
	Region          *RegionUpdate     `json:"region,omitempty"`
	Metrics         []string          `json:"metrics,omitempty"`
	CountdownID     string            `json:"countdownId,omitempty"`
	CountdownRepeat string            `json:"repeat,omitempty"`
	CountdownKind   string            `json:"countdownKind,omitempty"`
	Date            string            `json:"date,omitempty"`
	AnniversaryID   string            `json:"anniversaryId,omitempty"`
	AnniversaryKind string            `json:"anniversaryKind,omitempty"`
	Type            string            `json:"type"`
	Key             string            `json:"key"`
	Title           string            `json:"title,omitempty"`
	DueAt           string            `json:"dueAt,omitempty"`
	RecipientIDs    []int64           `json:"recipientIds"`
	ReminderID      string            `json:"reminderId,omitempty"`
	Storage         string            `json:"storage,omitempty"`
	Content         string            `json:"content,omitempty"`
	Scope           string            `json:"scope,omitempty"`
	MemoryKey       string            `json:"memoryKey,omitempty"`
	Data            map[string]string `json:"data,omitempty"`
	Category        string            `json:"category,omitempty"`
	ExpiresAt       string            `json:"expiresAt,omitempty"`
	MemoryKeys      []string          `json:"memoryKeys,omitempty"`
	Revision        int64             `json:"revision,omitempty"`
}

// Assistant is an AI message after protocol parsing. Actions are suggestions;
// callers must still validate space membership and persist them idempotently.
type Assistant struct {
	Silent        bool
	Control       bool
	RequestID     string
	Version       int
	Text          string
	RecipientIDs  []int64
	Source        string
	Actions       []Action
	Structured    bool
	ProtocolError string
}

var actionKeyRe = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)
var reservedProtocolRe = regexp.MustCompile(`"protocol"\s*:\s*"tietie\.`)

const malformedReply = "这条回复未能正确解析，请重试。"

// ParseAssistant accepts strict V2 JSON and the legacy V1 tietie fence.
// Plain conversational replies remain readable but cannot execute actions. In
// particular, arbitrary JSON or JSON quoted by a user never becomes an action.
func ParseAssistant(value string) Assistant {
	if isServerInputEcho(value) {
		return Assistant{Control: true, Structured: true, ProtocolError: "input envelope echoed by assistant"}
	}
	trimmed := strings.TrimSpace(value)
	if strings.HasPrefix(trimmed, "{") && strings.Contains(trimmed, `"tietie.`) || reservedProtocolRe.MatchString(trimmed) {
		return parseV2Assistant(trimmed)
	}
	const marker = "TIETIE_OUTPUT_V1"
	if strings.HasPrefix(trimmed, marker+"\n") {
		trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, marker+"\n"))
	}
	if !strings.HasPrefix(trimmed, "```tietie") {
		return Assistant{Text: value, Source: "chat"}
	}
	fail := func(reason string) Assistant {
		return Assistant{Text: malformedReply, Source: "chat", Structured: true, ProtocolError: reason}
	}
	const opening = "```tietie\n"
	trimmed = strings.ReplaceAll(trimmed, "\r\n", "\n")
	if !strings.HasPrefix(trimmed, opening) || !strings.HasSuffix(trimmed, "\n```") {
		return fail("invalid or incomplete tietie fence")
	}
	body := strings.TrimSuffix(strings.TrimPrefix(trimmed, opening), "\n```")
	if len(body) > 64*1024 {
		return fail("tietie JSON exceeds size limit")
	}
	if err := rejectDuplicateFields(body); err != nil {
		return fail("invalid tietie JSON: " + err.Error())
	}
	var payload struct {
		Text         *string   `json:"text"`
		RecipientIDs *[]int64  `json:"recipientIds"`
		Source       string    `json:"source"`
		Actions      *[]Action `json:"actions"`
	}
	decoder := json.NewDecoder(strings.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return fail("invalid tietie JSON: " + err.Error())
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fail("extra content after tietie JSON")
	}
	if payload.Text == nil || payload.RecipientIDs == nil || payload.Actions == nil ||
		(payload.Source != "chat" && payload.Source != "reminder") {
		return fail("missing text, recipientIds, actions, or valid source")
	}
	if !validRecipients(*payload.RecipientIDs, false) {
		return fail("recipientIds must contain one or two distinct positive IDs")
	}
	if len(*payload.Actions) > 8 {
		return fail("too many reminder actions")
	}
	if payload.Source == "reminder" && len(*payload.Actions) != 0 {
		return fail("reminder wakeups cannot create or cancel reminders")
	}
	keys := make(map[string]bool)
	for i, action := range *payload.Actions {
		if err := validateAction(action); err != nil {
			return fail(fmt.Sprintf("invalid action %d: %v", i, err))
		}
		if keys[action.Key] {
			return fail("duplicate action key")
		}
		keys[action.Key] = true
	}
	return Assistant{
		Text: *payload.Text, RecipientIDs: *payload.RecipientIDs,
		Source: payload.Source, Actions: *payload.Actions, Structured: true,
	}
}

// Unknown input versions also stay internal when echoed by an assistant. This
// does not inspect member text: the outer authenticated envelope owns identity.
func isServerInputEcho(value string) bool {
	at := strings.Index(value, "\n<TIETIE_INPUT_V")
	if at < 0 {
		return false
	}
	header := strings.TrimSpace(value[:at])
	if header == "" {
		return true
	}
	if strings.HasPrefix(header, "{") {
		var supplement struct {
			Type string `json:"type"`
		}
		decoder := json.NewDecoder(strings.NewReader(header))
		if decoder.Decode(&supplement) != nil || supplement.Type != "assistant_behavior" {
			return false
		}
		header = strings.TrimSpace(header[decoder.InputOffset():])
	}
	return strings.HasPrefix(header, v2ContractIntro) || strings.HasPrefix(header, "以下是贴贴应用的消息传输补充协议。") || strings.HasPrefix(header, "你是贴贴，一个两人共享空间里的贴心 AI 助手。")
}

// encoding/json accepts duplicate keys and keeps the last value. Rejecting them
// prevents the displayed message and intended action from having two meanings.
func rejectDuplicateFields(value string) error {
	decoder := json.NewDecoder(strings.NewReader(value))
	var readValue func() error
	readValue = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delimiter, nested := token.(json.Delim)
		if !nested {
			return nil
		}
		switch delimiter {
		case '{':
			seen := make(map[string]bool)
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok || seen[key] {
					return fmt.Errorf("duplicate or invalid object key")
				}
				seen[key] = true
				if err := readValue(); err != nil {
					return err
				}
			}
		case '[':
			for decoder.More() {
				if err := readValue(); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("unexpected closing delimiter")
		}
		_, err = decoder.Token()
		return err
	}
	if err := readValue(); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return fmt.Errorf("extra content after JSON")
	}
	return nil
}

func validRecipients(ids []int64, allowEmpty bool) bool {
	if len(ids) > 2 || (!allowEmpty && len(ids) == 0) {
		return false
	}
	seen := make(map[int64]bool, len(ids))
	for _, id := range ids {
		if id <= 0 || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}

func validateAction(action Action) error {
	if !actionKeyRe.MatchString(action.Key) {
		return fmt.Errorf("invalid key")
	}
	switch action.Type {
	case "create_reminder":
		if strings.TrimSpace(action.Title) == "" || len([]rune(action.Title)) > 500 ||
			!validRecipients(action.RecipientIDs, false) || action.ReminderID != "" {
			return fmt.Errorf("invalid title, recipients, or reminderId")
		}
		if _, err := time.Parse(time.RFC3339, action.DueAt); err != nil {
			return fmt.Errorf("dueAt must be RFC3339 with timezone")
		}
	case "cancel_reminder":
		if strings.TrimSpace(action.ReminderID) == "" || len(action.ReminderID) > 160 ||
			!validRecipients(action.RecipientIDs, true) || action.Title != "" || action.DueAt != "" {
			return fmt.Errorf("invalid cancellation")
		}
	default:
		return fmt.Errorf("unsupported action type")
	}
	return nil
}
