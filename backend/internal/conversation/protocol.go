// Package conversation translates the shared two-person conversation into a
// versioned protocol for a single-user AI session. It has no network or storage
// side effects: the caller authenticates members and persists validated actions.
package conversation

import (
	"encoding/json"
	"strings"
	"time"
)

type Member struct {
	ID   int64  `json:"userId"`
	Name string `json:"name"`
}

// Reminder is an existing backend reminder, never an instruction supplied by a
// user. Including existing IDs lets the assistant resolve cancellation requests.
type Reminder struct {
	ID           string      `json:"id"`
	Title        string      `json:"title"`
	DueAt        time.Time   `json:"dueAt"`
	Recurrence   *Recurrence `json:"recurrence,omitempty"`
	SeriesID     string      `json:"seriesId,omitempty"`
	Occurrence   int         `json:"occurrence,omitempty"`
	RecipientIDs []int64     `json:"recipientIds"`
	CreatedBy    int64       `json:"createdBy,omitempty"`
	Status       string      `json:"status,omitempty"`
}

// Memory is a backend-confirmed reminder fact, not a new user request.
type Memory struct {
	ReminderID   string      `json:"reminderId"`
	Title        string      `json:"title"`
	DueAt        time.Time   `json:"dueAt"`
	Recurrence   *Recurrence `json:"recurrence,omitempty"`
	SeriesID     string      `json:"seriesId,omitempty"`
	Occurrence   int         `json:"occurrence,omitempty"`
	RecipientIDs []int64     `json:"recipientIds"`
	Status       string      `json:"status"`
	TaskStatus   string      `json:"taskStatus"`
	DeliveredAt  *time.Time  `json:"deliveredAt,omitempty"`
	CompletedBy  *int64      `json:"completedBy,omitempty"`
}
type Context struct {
	ReplyMode   string
	RecipientID int64
	Visibility  string
	SessionID   string
	Members     []Member
	AuthorID    int64
	Now         time.Time
	Reminders   []Reminder
	Memories    []Memory
	MemoryIndex []MemoryIndex
}

const inputOpen = "\n<TIETIE_INPUT_V1>\n"
const inputClose = "\n</TIETIE_INPUT_V1>"

// V1 is retained for compatibility; its fixed rules live in the cloud too.
// The marker identifies only the server wrapper, never the user text within it.
const legacyInputHeader = "TIETIE_PROTOCOL_V1"

type inputEnvelope struct {
	Protocol    string     `json:"protocol"`
	Version     int        `json:"version"`
	Kind        string     `json:"kind"`
	SessionID   string     `json:"sessionId"`
	Members     []Member   `json:"members"`
	Author      *Member    `json:"author,omitempty"`
	CurrentTime string     `json:"currentTime"`
	Timezone    string     `json:"timezone"`
	Text        string     `json:"text,omitempty"`
	Reminder    *Reminder  `json:"reminder,omitempty"`
	Reminders   []Reminder `json:"reminders,omitempty"`
	Memories    []Memory   `json:"backendConfirmedReminderMemories,omitempty"`
}

func envelope(ctx Context, kind string) inputEnvelope {
	now := ctx.Now
	if now.IsZero() {
		now = time.Now()
	}
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		location = time.FixedZone("Asia/Shanghai", 8*60*60)
	}
	now = now.In(location)
	return inputEnvelope{
		Protocol: "tietie.conversation", Version: 1, Kind: kind,
		SessionID: ctx.SessionID, Members: ctx.Members,
		CurrentTime: now.Format(time.RFC3339Nano), Timezone: now.Location().String(),
		Reminders: ctx.Reminders, Memories: ctx.Memories,
	}
}

// EncodeUser preserves arbitrary original user text inside a JSON string. The
// author is resolved only from trusted member data, never from names in the text.
func EncodeUser(ctx Context, text string) string {
	e := envelope(ctx, "user_message")
	e.Text = text
	for _, member := range ctx.Members {
		if member.ID == ctx.AuthorID {
			author := member
			e.Author = &author
			break
		}
	}
	return encode(e)
}

// EncodeReminder creates a hidden server wakeup. No user is impersonated.
func EncodeReminder(ctx Context, reminder Reminder) string {
	e := envelope(ctx, "reminder_due")
	e.Reminder = &reminder
	return encode(e)
}

func encode(e inputEnvelope) string {
	data, _ := json.Marshal(e) // This envelope contains no unsupported JSON values.
	return legacyInputHeader + inputOpen + string(data) + inputClose
}

// Input is the public part of a decoded user event. Tail contains only content
// appended by the transport after the envelope (for example attachment mounts).
type Input struct {
	Version     int
	RequestID   string
	Results     []ActionResult
	ReplyTo     *Member
	Kind        string
	Context     Context
	Reminder    *Reminder
	Text        string
	UserID      int64
	DisplayName string
	Hidden      bool
	Tail        string
}

// DecodeInput recognizes the first versioned server envelope, independently of
// editable prompt wording. Nested envelopes stay literal member text. Events
// without a recognized header never acquire an inferred human identity.
func DecodeInput(value string) (Input, bool) {
	if _, ok := v2EnvelopeBody(value); ok {
		return decodeV2(value)
	}
	atOpen := strings.Index(value, inputOpen)
	if atOpen < 0 {
		return Input{}, false
	}
	header := value[:atOpen]
	if strings.TrimSpace(header) != legacyInputHeader {
		return Input{}, false
	}
	rest := value[atOpen+len(inputOpen):]
	// JSON-encoded user text contains escaped newlines, so it cannot contain this
	// closing delimiter as a literal line. The first delimiter closes the envelope.
	at := strings.Index(rest, inputClose)
	if at < 0 {
		return Input{}, false
	}
	var e inputEnvelope
	if json.Unmarshal([]byte(rest[:at]), &e) != nil ||
		e.Protocol != "tietie.conversation" || e.Version != 1 {
		return Input{}, false
	}
	now, _ := time.Parse(time.RFC3339, e.CurrentTime)
	input := Input{
		Version: 1, Kind: e.Kind, Text: e.Text, Reminder: e.Reminder, Tail: rest[at+len(inputClose):],
		Context: Context{SessionID: e.SessionID, Members: e.Members, Now: now, Reminders: e.Reminders, Memories: e.Memories},
	}
	switch e.Kind {
	case "reminder_due":
		if e.Reminder == nil {
			return Input{}, false
		}
		input.Hidden = true
	case "user_message":
		if e.Author != nil && e.Author.ID > 0 {
			for _, member := range e.Members {
				if member.ID == e.Author.ID && member.Name == e.Author.Name {
					input.UserID, input.DisplayName = member.ID, member.Name
					input.Context.AuthorID = member.ID
					break
				}
			}
		}
	default:
		return Input{}, false
	}
	return input, true
}
