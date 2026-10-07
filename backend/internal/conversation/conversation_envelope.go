package conversation

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"tietie/backend/internal/memoryspace"
	"tietie/backend/internal/regions"
	"tietie/backend/internal/weather"
	"time"
)

// The cloud agent owns fixed behavior and action protocol instructions.
// Backend messages carry only authenticated, current conversation facts.
const openV2 = "\n<TIETIE_INPUT_V2>\n"
const closeV2 = "\n</TIETIE_INPUT_V2>"

// The envelope is deliberately the only outbound wrapper. The cloud system
// prompt owns persona, behavior and output rules; this frame carries facts.
func v2EnvelopeBody(value string) (string, bool) {
	at := strings.Index(value, openV2)
	if at != 0 {
		return "", false
	}
	// Compact frames carry an explicit discriminator; a user's bare marker or a
	// nested envelope is never inferred to be server provenance.
	var frame struct {
		Compact  bool   `json:"compact"`
		Protocol string `json:"protocol"`
	}
	body, _, found := strings.Cut(value[len(openV2):], closeV2)
	if !found || json.Unmarshal([]byte(body), &frame) != nil || !frame.Compact || frame.Protocol != "tietie.conversation" {
		return "", false
	}
	return value[len(openV2):], true
}

type MemoryIndex struct {
	Kind      string     `json:"-"`
	Revision  int64      `json:"revision,omitempty"`
	Key       string     `json:"memoryKey"`
	Path      string     `json:"path"`
	Scope     string     `json:"scope"`
	OwnerID   int64      `json:"ownerId,omitempty"`
	State     string     `json:"state"`
	Category  string     `json:"category,omitempty"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
}
type MemoryRead struct {
	Key     string `json:"memoryKey"`
	Path    string `json:"path"`
	Content string `json:"content"`
}
type WeatherProfile struct {
	UserID  int64            `json:"userId"`
	Region  regions.Location `json:"region"`
	Metrics []string         `json:"metrics"`
}
type Countdown struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	Date          string `json:"date"`
	Repeat        string `json:"repeat"`
	Kind          string `json:"kind"`
	DaysRemaining int    `json:"daysRemaining"`
	NextDate      string `json:"nextDate"`
	Expired       bool   `json:"expired"`
	LeapAdjusted  bool   `json:"leapAdjusted"`
}
type CountdownBoard struct {
	Items   []Countdown `json:"items"`
	HasMore bool        `json:"hasMore"`
}
type ActionResult struct {
	WeatherCards   []weather.Card    `json:"weatherCards,omitempty"`
	TargetUserID   int64             `json:"targetUserId,omitempty"`
	Region         *regions.Location `json:"region,omitempty"`
	Metrics        []string          `json:"metrics,omitempty"`
	Countdown      *Countdown        `json:"countdown,omitempty"`
	Anniversary    *Anniversary      `json:"anniversary,omitempty"`
	Reminder       *Reminder         `json:"reminder,omitempty"`
	Key            string            `json:"key"`
	Type           string            `json:"type"`
	Status         string            `json:"status"`
	ReminderID     string            `json:"reminderId,omitempty"`
	ReminderIDs    []string          `json:"reminderIds,omitempty"`
	MemoryKey      string            `json:"memoryKey,omitempty"`
	DatabaseStatus string            `json:"databaseStatus,omitempty"`
	MemoryStatus   string            `json:"memoryStatus,omitempty"`
	ErrorCode      string            `json:"errorCode,omitempty"`
	Message        string            `json:"message,omitempty"`
	Memories       []MemoryRead      `json:"memories,omitempty"`
}
type Actor struct {
	Kind   string `json:"kind"`
	UserID int64  `json:"userId,omitempty"`
	Name   string `json:"name,omitempty"`
}
type PronounTargets struct {
	SelfUserID    int64 `json:"selfUserId"`
	PartnerUserID int64 `json:"partnerUserId"`
}
type EnvelopeV2 struct {
	HasAttachments     bool                       `json:"-"`
	ReminderRequest    *DirectReminder            `json:"reminderRequest,omitempty"`
	ReplyInstructions  string                     `json:"replyInstructions,omitempty"`
	Pronouns           *PronounTargets            `json:"pronouns,omitempty"`
	WeatherProfiles    []WeatherProfile           `json:"weatherProfiles,omitempty"`
	CountdownBoard     *CountdownBoard            `json:"countdownBoard,omitempty"`
	AnniversaryBoard   *AnniversaryBoard          `json:"anniversaryBoard,omitempty"`
	ReplyMode          string                     `json:"replyMode,omitempty"`
	RecipientID        int64                      `json:"recipientId,omitempty"`
	Compact            bool                       `json:"compact,omitempty"`
	Visibility         string                     `json:"visibility,omitempty"`
	Protocol           string                     `json:"protocol"`
	Version            int                        `json:"version"`
	Kind               string                     `json:"kind"`
	RequestID          string                     `json:"requestId"`
	SessionID          string                     `json:"sessionId"`
	Actor              Actor                      `json:"actor"`
	ReplyTo            *Member                    `json:"replyTo,omitempty"`
	Members            []Member                   `json:"members,omitempty"`
	CurrentTime        time.Time                  `json:"currentTime"`
	Timezone           string                     `json:"timezone,omitempty"`
	RemovedReminderIDs []string                   `json:"removedReminderIds,omitempty"`
	RemovedMemoryKeys  []string                   `json:"removedMemoryKeys,omitempty"`
	Text               string                     `json:"text,omitempty"`
	Reminders          []Reminder                 `json:"reminders,omitempty"`
	Reminder           *Reminder                  `json:"reminder,omitempty"`
	Results            []ActionResult             `json:"results,omitempty"`
	MemoryIndex        []MemoryIndex              `json:"memoryIndex,omitempty"`
	MemoryReads        []MemoryRead               `json:"memoryReads,omitempty"`
	AssistantStyle     *memoryspace.SpeakingStyle `json:"assistantStyle,omitempty"`
}

func RequestID(ctx Context) string {
	return fmt.Sprintf("turn_%d_%d", ctx.AuthorID, ctx.Now.UnixNano())
}
func NewEnvelopeV2(ctx Context, kind, requestID string) EnvelopeV2 {
	e := EnvelopeV2{Visibility: ctx.Visibility, Protocol: "tietie.conversation", Version: 2, Kind: kind, RequestID: requestID, SessionID: ctx.SessionID, Actor: Actor{Kind: "system", Name: "贴贴后台"}, Members: ctx.Members, CurrentTime: ctx.Now, Timezone: "Asia/Shanghai", Reminders: ctx.Reminders, MemoryIndex: ctx.MemoryIndex}
	e.ReplyMode, e.RecipientID = ctx.ReplyMode, ctx.RecipientID
	if kind == "user_message" || kind == "action_result" {
		e.Pronouns = &PronounTargets{SelfUserID: ctx.AuthorID}
		for _, m := range ctx.Members {
			if m.ID != ctx.AuthorID {
				e.Pronouns.PartnerUserID = m.ID
			}
		}
		for _, m := range ctx.Members {
			if m.ID == ctx.AuthorID {
				if kind == "user_message" {
					e.Actor = Actor{Kind: "member", UserID: m.ID, Name: m.Name}
				} else {
					e.ReplyTo = &Member{ID: m.ID, Name: m.Name}
				}
			}
		}
	}
	return e
}
func EncodeV2(e EnvelopeV2) string {
	// The explicit discriminator preserves input provenance without repeating
	// cloud-owned instructions, including for locally stored control origins.
	e.Compact = true
	b, _ := json.Marshal(e)
	return openV2 + string(b) + closeV2
}
func EncodeUserV2(ctx Context, text string) string {
	e := NewEnvelopeV2(ctx, "user_message", RequestID(ctx))
	e.Text = text
	return EncodeV2(e)
}
func EncodeActionResult(ctx Context, requestID string, results []ActionResult) string {
	e := NewEnvelopeV2(ctx, "action_result", requestID)
	e.Results = results
	return EncodeV2(e)
}
func EncodeReminderV2(ctx Context, r Reminder, memories []MemoryRead) string {
	e := NewEnvelopeV2(ctx, "reminder_due", "due_"+r.ID)
	e.Reminder = &r
	e.MemoryReads = memories
	return EncodeV2(e)
}
func decodeV2(value string) (Input, bool) {
	rest, ok := v2EnvelopeBody(value)
	if !ok {
		return Input{}, false
	}
	at := strings.Index(rest, closeV2)
	if at < 0 {
		return Input{}, false
	}
	var e EnvelopeV2
	if json.Unmarshal([]byte(rest[:at]), &e) != nil || e.Protocol != "tietie.conversation" || e.Version != 2 || e.RequestID == "" || e.SessionID == "" {
		return Input{}, false
	}
	input := Input{Version: 2, RequestID: e.RequestID, Kind: e.Kind, Text: e.Text, Reminder: e.Reminder, Results: e.Results, Tail: rest[at+len(closeV2):], Context: Context{Visibility: e.Visibility, SessionID: e.SessionID, Members: e.Members, Now: e.CurrentTime, Reminders: e.Reminders, MemoryIndex: e.MemoryIndex}}
	if e.ReplyMode != "" && e.ReplyMode != SilentReply {
		return Input{}, false
	}
	input.Context.ReplyMode, input.Context.RecipientID = e.ReplyMode, e.RecipientID
	switch e.Kind {
	case "user_message":
		if e.Actor.Kind != "member" || e.Actor.UserID <= 0 || e.Actor.Name == "" {
			return Input{}, false
		}
		if e.Compact && len(e.Members) == 0 {
			input.Context.Members = []Member{{ID: e.Actor.UserID, Name: e.Actor.Name}}
			input.UserID, input.DisplayName, input.Context.AuthorID = e.Actor.UserID, e.Actor.Name, e.Actor.UserID
		}
		for _, m := range e.Members {
			if m.ID == e.Actor.UserID && m.Name == e.Actor.Name {
				input.UserID = m.ID
				input.DisplayName = m.Name
				input.Context.AuthorID = m.ID
			}
		}
		if input.UserID == 0 {
			return Input{}, false
		}
	case "reminder_due":
		if e.Actor.Kind != "system" || e.Reminder == nil {
			return Input{}, false
		}
		input.Hidden = true
	case "action_result":
		if e.Actor.Kind != "system" {
			return Input{}, false
		}
		if e.ReplyTo != nil {
			if e.ReplyTo.ID <= 0 || e.ReplyTo.Name == "" {
				return Input{}, false
			}
			if len(e.Members) > 0 {
				found := false
				for _, m := range e.Members {
					found = found || m == *e.ReplyTo
				}
				if !found {
					return Input{}, false
				}
			}
			input.ReplyTo = e.ReplyTo
		}
		input.Hidden = true
	case "binding_welcome":
		// The binding bootstrap is a backend-authored prompt. It must never be
		// rendered as a member message, while the assistant reply remains a
		// normal visible chat bubble.
		if e.Actor.Kind != "system" || strings.TrimSpace(e.Text) == "" {
			return Input{}, false
		}
		input.Hidden = true
	default:
		return Input{}, false
	}
	return input, true
}

// Normalize the cloud's numeric string recipients at the reply boundary. The
// public API still exposes int64 IDs, and validRecipients plus the conversation
// processor retain recipient and membership validation.
type assistantRecipientIDs []int64

func (ids *assistantRecipientIDs) UnmarshalJSON(data []byte) error {
	var values []json.RawMessage
	if err := json.Unmarshal(data, &values); err != nil {
		return err
	}
	normalized := make(assistantRecipientIDs, 0, len(values))
	for _, value := range values {
		var id int64
		if err := json.Unmarshal(value, &id); err != nil {
			var quoted string
			if err := json.Unmarshal(value, &quoted); err != nil {
				return fmt.Errorf("invalid recipient ID")
			}
			parsed, err := strconv.ParseInt(quoted, 10, 64)
			if err != nil || parsed <= 0 || strconv.FormatInt(parsed, 10) != quoted {
				return fmt.Errorf("invalid recipient ID")
			}
			id = parsed
		}
		normalized = append(normalized, id)
	}
	*ids = normalized
	return nil
}

func parseV2Assistant(body string) Assistant {
	// Reserved protocol objects are always hidden on parse failure: never expose
	// partial control instructions or JSON to a member.
	fail := func(reason string) Assistant {
		return Assistant{Control: true, Version: 2, Structured: true, ProtocolError: reason}
	}
	if len(body) > 64*1024 {
		return fail("protocol size limit")
	}
	// The cloud can insert an extra quote/comma between text and recipientIds.
	// Recover only that separator in an otherwise complete user-facing message;
	// control actions and all field/correlation checks retain strict validation.
	repaired := false
	const extraSeparator = `",","recipientIds":`
	if !json.Valid([]byte(body)) && strings.Count(body, extraSeparator) == 1 {
		candidate := strings.Replace(body, extraSeparator, `","recipientIds":`, 1)
		if json.Valid([]byte(candidate)) {
			body, repaired = candidate, true
		}
	}
	if err := rejectDuplicateFields(body); err != nil {
		return fail("duplicate or invalid JSON")
	}
	var p struct {
		Protocol     string                 `json:"protocol"`
		Version      int                    `json:"version"`
		RequestID    string                 `json:"requestId"`
		Actions      *[]Action              `json:"actions,omitempty"`
		Text         *string                `json:"text,omitempty"`
		RecipientIDs *assistantRecipientIDs `json:"recipientIds,omitempty"`
		Source       string                 `json:"source,omitempty"`
	}
	if err := strictDecode(body, &p); err != nil {
		return fail("invalid protocol fields")
	}
	if repaired && p.Protocol != "tietie.message" {
		return fail("only user messages allow separator recovery")
	}
	if p.Version != 2 || p.RequestID == "" || len(p.RequestID) > 160 {
		return fail("invalid version or requestId")
	}
	switch p.Protocol {
	case "tietie.silent":
		if p.Actions != nil || p.Text != nil || p.RecipientIDs != nil || p.Source != "" {
			return fail("silent output cannot contain text or actions")
		}
		return Assistant{Silent: true, Control: true, Version: 2, RequestID: p.RequestID, Structured: true}
	case "tietie.control":
		if p.Actions == nil || len(*p.Actions) == 0 || len(*p.Actions) > 8 || p.Text != nil || p.RecipientIDs != nil || p.Source != "" {
			return fail("control cannot contain user text")
		}
		seen := map[string]bool{}
		for _, a := range *p.Actions {
			if err := validateV2Action(a); err != nil {
				return fail(err.Error())
			}
			if seen[a.Key] {
				return fail("duplicate action key")
			}
			seen[a.Key] = true
		}
		return Assistant{Control: true, Version: 2, RequestID: p.RequestID, Structured: true, Actions: *p.Actions}
	case "tietie.message":
		if p.Text == nil || strings.TrimSpace(*p.Text) == "" || p.RecipientIDs == nil || !validRecipients(*p.RecipientIDs, false) || p.Actions != nil || (p.Source != "chat" && p.Source != "reminder") {
			return fail("invalid user message")
		}
		return Assistant{Version: 2, RequestID: p.RequestID, Structured: true, Text: *p.Text, RecipientIDs: *p.RecipientIDs, Source: p.Source}
	default:
		return fail("unknown protocol")
	}
}
func validateV2Action(a Action) error {
	if !actionKeyRe.MatchString(a.Key) || strings.Contains(a.Key, "..") {
		return fmt.Errorf("invalid action key")
	}
	if a.Type != "read_memory" && a.Revision != 0 {
		return fmt.Errorf("unexpected revision")
	}
	if a.Type != "save_memory" && (a.Category != "" || a.ExpiresAt != "" || len(a.Data) > 0) {
		return fmt.Errorf("unexpected memory fields")
	}
	if (a.Type != "save_anniversary" && a.Type != "save_countdown" && a.Type != "delete_reminders" && a.Date != "") || (a.Type != "save_anniversary" && a.AnniversaryKind != "") {
		return fmt.Errorf("unexpected anniversary fields")
	}
	if a.Type != "delete_reminders" && (len(a.ReminderIDs) > 0 || a.DeleteMode != "" || a.TitleContains != "" || a.DateField != "" || a.DueFrom != "" || a.DueBefore != "" || len(a.Statuses) > 0) {
		return fmt.Errorf("unexpected deletion fields")
	}
	if a.Type != "create_reminder" && a.Recurrence != nil {
		return fmt.Errorf("unexpected recurrence")
	}
	if a.Type != "save_anniversary" && a.Type != "delete_anniversary" && a.AnniversaryID != "" {
		return fmt.Errorf("unexpected anniversary target")
	}
	if a.Type != "set_region" && a.Type != "query_weather" && a.Region != nil || a.Type != "set_weather_metrics" && len(a.Metrics) > 0 || a.Type != "set_region" && a.Type != "set_weather_metrics" && a.TargetUserID != 0 {
		return fmt.Errorf("unexpected profile fields")
	}
	if a.Type != "query_weather" && a.WeatherWhen != "" {
		return fmt.Errorf("unexpected weather query fields")
	}
	if a.Type != "save_countdown" && (a.CountdownRepeat != "" || a.CountdownKind != "") || a.Type != "save_countdown" && a.Type != "delete_countdown" && a.CountdownID != "" {
		return fmt.Errorf("unexpected countdown fields")
	}
	switch a.Type {
	case "query_weather":
		if a.WeatherWhen != "" && a.WeatherWhen != "now" && a.WeatherWhen != "today" && a.WeatherWhen != "tomorrow" {
			return fmt.Errorf("invalid weather date")
		}
		if a.Storage != "" || a.Scope != "" || a.Content != "" || a.DueAt != "" || a.Title != "" || a.ReminderID != "" || a.MemoryKey != "" || len(a.MemoryKeys) > 0 || len(a.RecipientIDs) > 2 || len(a.RecipientIDs) > 0 && !validRecipients(a.RecipientIDs, false) {
			return fmt.Errorf("invalid weather query")
		}
		if a.Region != nil {
			if a.Region.Clear {
				return fmt.Errorf("invalid weather location")
			}
			_, err := regions.ResolveNames(a.Region.Province, a.Region.City, a.Region.District)
			return err
		}
		return nil
	case "set_region", "set_weather_metrics", "save_countdown", "delete_countdown":
		if a.Scope != "" || a.Content != "" || a.DueAt != "" || a.ReminderID != "" || a.MemoryKey != "" || len(a.MemoryKeys) > 0 || len(a.RecipientIDs) > 0 {
			return fmt.Errorf("invalid business action fields")
		}
		if a.Type == "delete_countdown" {
			if a.CountdownID == "" || len(a.CountdownID) > 160 || a.Title != "" || a.Storage != "" {
				return fmt.Errorf("invalid countdown deletion")
			}
			return nil
		}
		if a.Storage != "database_and_memory" {
			return fmt.Errorf("invalid storage")
		}
		if a.Type == "save_countdown" {
			if !memoryspace.ValidAnniversaryDate(a.Date) || strings.TrimSpace(a.Title) == "" || len([]rune(a.Title)) > 80 || len(a.CountdownID) > 160 || (a.CountdownRepeat != "" && a.CountdownRepeat != "auto" && a.CountdownRepeat != "annual" && a.CountdownRepeat != "once") || (a.CountdownKind != "" && a.CountdownKind != "birthday" && a.CountdownKind != "deadline" && a.CountdownKind != "other") {
				return fmt.Errorf("invalid countdown")
			}
			return nil
		}
		if a.TargetUserID <= 0 || a.Title != "" {
			return fmt.Errorf("invalid profile target")
		}
		if a.Type == "set_region" {
			if a.Region == nil || a.Region.Clear && (a.Region.Province != "" || a.Region.City != "" || a.Region.District != "") {
				return fmt.Errorf("invalid region")
			}
			if !a.Region.Clear {
				_, err := regions.ResolveNames(a.Region.Province, a.Region.City, a.Region.District)
				return err
			}
			return nil
		}
		_, err := weather.NormalizeMetrics(a.Metrics)
		return err
	case "delete_anniversary":
		if strings.TrimSpace(a.AnniversaryID) == "" || len(a.AnniversaryID) > 160 || a.Storage != "" || a.Scope != "" || a.Content != "" || a.DueAt != "" || a.ReminderID != "" || a.Title != "" || len(a.RecipientIDs) > 0 || a.MemoryKey != "" || len(a.MemoryKeys) > 0 {
			return fmt.Errorf("invalid anniversary deletion")
		}
	case "save_anniversary":
		kind := a.AnniversaryKind
		if kind == "" {
			kind = "other"
		}
		if _, ok := memoryspace.AnniversaryKindLabel(kind); !ok || !memoryspace.ValidAnniversaryDate(a.Date) || strings.TrimSpace(a.Title) == "" || len([]rune(a.Title)) > 80 || len(a.AnniversaryID) > 160 || a.Storage != "database_and_memory" || a.Scope != "" || a.Content != "" || a.DueAt != "" || a.ReminderID != "" || len(a.RecipientIDs) > 0 || a.MemoryKey != "" || len(a.MemoryKeys) > 0 {
			return fmt.Errorf("invalid anniversary")
		}
	case "create_reminder":
		if a.Storage != "database_and_memory" || a.Content != "" || a.Scope != "" || a.MemoryKey != "" || len(a.MemoryKeys) > 0 {
			return fmt.Errorf("invalid reminder storage")
		}
		return validateAction(a)
	case "cancel_reminder":
		if a.Storage != "" || a.Content != "" || a.Scope != "" || a.MemoryKey != "" || len(a.MemoryKeys) > 0 {
			return fmt.Errorf("invalid cancellation")
		}
		return validateAction(a)
	case "delete_reminders":
		if a.DeleteMode != "single" && a.DeleteMode != "all" || a.Storage != "" || a.Scope != "" || a.Content != "" || a.MemoryKey != "" || len(a.MemoryKeys) > 0 || a.ReminderID != "" || a.Title != "" || a.DueAt != "" || len(a.RecipientIDs) > 0 || len(a.ReminderIDs) > 200 || len(a.Statuses) > 4 || len(a.TitleContains) > 500 || len(a.ReminderIDs) == 0 && strings.TrimSpace(a.TitleContains) == "" && a.Date == "" && a.DueFrom == "" && a.DueBefore == "" && len(a.Statuses) == 0 {
			return fmt.Errorf("invalid reminder deletion")
		}
		if a.DateField != "" && a.DateField != "due" && a.DateField != "finished" && a.DateField != "created" || a.DateField != "" && a.Date == "" {
			return fmt.Errorf("invalid reminder date field")
		}
		if a.DateField == "finished" && (len(a.Statuses) == 0 || slices.Contains(a.Statuses, "pending")) {
			return fmt.Errorf("finished date requires completed or cancelled status")
		}
		for _, id := range a.ReminderIDs {
			if strings.TrimSpace(id) == "" || len(id) > 160 {
				return fmt.Errorf("invalid reminder id")
			}
		}
		for _, status := range a.Statuses {
			if status != "pending" && status != "completed" && status != "reminded" && status != "cancelled" {
				return fmt.Errorf("invalid reminder status")
			}
		}
		if a.Date != "" {
			date, err := time.Parse("2006-01-02", a.Date)
			if err != nil || date.Format("2006-01-02") != a.Date {
				return fmt.Errorf("invalid reminder date")
			}
		}
		for _, value := range []string{a.DueFrom, a.DueBefore} {
			if value != "" {
				if _, err := time.Parse(time.RFC3339, value); err != nil {
					return fmt.Errorf("invalid reminder time range")
				}
			}
		}
		return nil
	case "save_memory":
		if err := memoryspace.ValidateData(a.Category, a.Data); err != nil {
			return err
		}
		switch a.Category {
		case "", "profile", "habit", "agreement", "realtime", "behavior":
		default:
			return fmt.Errorf("invalid category")
		}
		if (a.Category == "agreement" || a.Category == "behavior") && a.Scope != "space" {
			return fmt.Errorf("shared category requires space scope")
		}
		if a.Category == "realtime" {
			if a.Storage != "database_and_memory" || a.ExpiresAt == "" {
				return fmt.Errorf("realtime requires expiry and database storage")
			}
		} else if a.ExpiresAt != "" {
			return fmt.Errorf("only realtime memories expire")
		}
		if a.ExpiresAt != "" {
			if _, err := time.Parse(time.RFC3339, a.ExpiresAt); err != nil {
				return fmt.Errorf("invalid expiry")
			}
		}
		if len(a.MemoryKey) > 160 {
			return fmt.Errorf("invalid update target")
		}
		if (a.Storage != "memory_only" && a.Storage != "database_and_memory") || (a.Scope != "self" && a.Scope != "space") || strings.TrimSpace(a.Content) == "" || len(a.Content) > 16*1024 || a.Title != "" || a.DueAt != "" || a.ReminderID != "" || len(a.MemoryKeys) > 0 || len(a.RecipientIDs) > 0 {
			return fmt.Errorf("invalid memory")
		}
	case "read_memory":
		if a.Revision < 0 {
			return fmt.Errorf("invalid revision")
		}
		if len(a.MemoryKeys) == 0 || len(a.MemoryKeys) > 5 || a.MemoryKey != "" || a.Storage != "" || a.Content != "" || a.Scope != "" || a.ReminderID != "" || a.Title != "" || a.DueAt != "" || len(a.RecipientIDs) > 0 {
			return fmt.Errorf("invalid memory read")
		}
	case "delete_memory":
		if a.MemoryKey == "" || len(a.MemoryKey) > 160 || a.Storage != "" || a.Content != "" || a.Scope != "" || a.ReminderID != "" || a.Title != "" || a.DueAt != "" || len(a.MemoryKeys) > 0 || len(a.RecipientIDs) > 0 {
			return fmt.Errorf("invalid memory deletion")
		}
	default:
		return fmt.Errorf("unsupported action")
	}
	return nil
}

// ValidateV2Action also guards direct execution in workers and tests.
func ValidateV2Action(a Action) error { return validateV2Action(a) }

func strictDecode(body string, target any) error {
	d := json.NewDecoder(strings.NewReader(body))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return fmt.Errorf("trailing content")
	}
	return nil
}
