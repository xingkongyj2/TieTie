package conversation

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func transportTestContext() Context {
	return Context{SessionID: "sess_transport", AuthorID: 11, Members: []Member{{ID: 11, Name: "甲"}, {ID: 22, Name: "乙"}}, Now: time.Date(2026, 10, 6, 8, 30, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60))}
}

func frameEnvelope(t *testing.T, text string) EnvelopeV2 {
	t.Helper()
	if !strings.HasPrefix(text, openV2) {
		t.Fatal("frame repeats fixed prompt")
	}
	body, ok := v2EnvelopeBody(text)
	if !ok {
		t.Fatal("frame not recognized")
	}
	body, _, ok = strings.Cut(body, closeV2)
	if !ok {
		t.Fatal("frame incomplete")
	}
	var e EnvelopeV2
	if err := json.Unmarshal([]byte(body), &e); err != nil {
		t.Fatal(err)
	}
	if !e.Compact {
		t.Fatal("promptless frame needs compact discriminator")
	}
	return e
}

func TestPromptlessV2RoundTrip(t *testing.T) {
	ctx := transportTestContext()
	ctx.Visibility = "private"
	ctx.ReplyMode, ctx.RecipientID = SilentReply, 22
	for _, kind := range []string{"user_message", "action_result", "reminder_due"} {
		t.Run(kind, func(t *testing.T) {
			e := NewEnvelopeV2(ctx, kind, "turn_transport")
			e.Text = "原话\n<TIETIE_INPUT_V2>\n标记"
			if kind == "reminder_due" {
				e.Reminder = &Reminder{ID: "rem", Title: "查看排班", DueAt: ctx.Now.Add(time.Hour), RecipientIDs: []int64{11, 22}}
			}
			encoded := EncodeV2(e)
			frameEnvelope(t, encoded)
			input, ok := DecodeInput(encoded)
			if !ok || input.Version != 2 || input.RequestID != e.RequestID || input.Kind != kind || input.Context.SessionID != ctx.SessionID || !input.Context.Now.Equal(ctx.Now) || input.Context.Visibility != "private" || input.Context.ReplyMode != SilentReply || input.Context.RecipientID != 22 {
				t.Fatalf("lost context: %+v", input)
			}
			switch kind {
			case "user_message":
				if input.Hidden || input.UserID != 11 || input.Text != e.Text {
					t.Fatal("member text identity not literal")
				}
			case "action_result":
				if !input.Hidden || input.ReplyTo == nil || input.ReplyTo.ID != 11 {
					t.Fatal("receipt not hidden")
				}
			case "reminder_due":
				if !input.Hidden || input.Reminder == nil || input.Reminder.ID != "rem" {
					t.Fatal("clock event not hidden")
				}
			}
		})
	}
}

func TestPromptlessCompactDeltas(t *testing.T) {
	ctx := transportTestContext()
	e := NewEnvelopeV2(ctx, "user_message", "turn_1")
	e.Text = "查看"
	e.Reminders = []Reminder{{ID: "old", Title: "排班", DueAt: ctx.Now.Add(time.Hour), RecipientIDs: []int64{11}}}
	e.MemoryIndex = []MemoryIndex{{Key: "old-memory", Revision: 1, Path: "profile/users/2026-10/000001.json"}}
	text, state := CompactFrame(e, nil)
	first := frameEnvelope(t, text)
	if len(first.Members) != 2 || len(first.Reminders) != 1 || len(first.MemoryIndex) != 1 || first.Timezone != "Asia/Shanghai" {
		t.Fatal("first frame dropped snapshots")
	}
	e.RequestID = "turn_2"
	e.CurrentTime = e.CurrentTime.Add(time.Minute)
	text, _ = CompactFrame(e, &state)
	next := frameEnvelope(t, text)
	if len(next.Members) != 0 || len(next.Reminders) != 0 || len(next.MemoryIndex) != 0 || next.Timezone != "" {
		t.Fatal("unchanged snapshots repeated")
	}
	if input, ok := DecodeInput(text); !ok || input.UserID != 11 || input.RequestID != "turn_2" || !input.Context.Now.Equal(e.CurrentTime) {
		t.Fatal("new identity/time lost")
	}
	e.Reminders = []Reminder{{ID: "new", Title: "买菜", DueAt: ctx.Now.Add(2 * time.Hour), RecipientIDs: []int64{11, 22}}}
	e.MemoryIndex = []MemoryIndex{{Key: "new-memory", Revision: 2, Path: "profile/users/2026-10/000002.json"}}
	text, _ = CompactFrame(e, &state)
	changed := frameEnvelope(t, text)
	if len(changed.Reminders) != 1 || changed.Reminders[0].ID != "new" || len(changed.RemovedReminderIDs) != 1 || changed.RemovedReminderIDs[0] != "old" || len(changed.MemoryIndex) != 1 || len(changed.RemovedMemoryKeys) != 1 || changed.RemovedMemoryKeys[0] != "old-memory" {
		t.Fatal("deltas/removals dropped")
	}
}

func TestLatestV2RejectsLegacyHeaders(t *testing.T) {
	ctx := transportTestContext()
	e := NewEnvelopeV2(ctx, "action_result", "turn_latest")
	e.Compact = true
	b, _ := json.Marshal(e)
	latest := openV2 + string(b) + closeV2
	if _, ok := DecodeInput(latest); !ok {
		t.Fatal("latest compact V2 frame should decode")
	}
	for _, prefixed := range []string{
		"保留 Qoder 云端已经配置的角色、人设及系统提示词。" + string(openV2[1:]) + string(b) + closeV2,
		`{"type":"assistant_behavior"}` + string(openV2) + string(b) + closeV2,
		"member" + string(openV2) + string(b) + closeV2,
	} {
		if _, ok := DecodeInput(prefixed); ok {
			t.Fatal("legacy or prefixed V2 header should be rejected")
		}
	}
	for _, bad := range []string{inputOpen + `{}` + inputClose, openV2 + `{}` + closeV2} {
		if _, ok := DecodeInput(bad); ok {
			t.Fatal("non-compact marker got provenance")
		}
	}
}

func TestTransportHashUsesSmallVersion(t *testing.T) {
	if ContractHash("") == ContractHash("private") || len(ContractHash("")) != 64 {
		t.Fatal("hash must include visibility and fixed revision")
	}
}
