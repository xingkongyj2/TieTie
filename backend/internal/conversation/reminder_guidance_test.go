package conversation

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func guidanceUserEnvelope() EnvelopeV2 {
	ctx := reminderTestContext(time.Date(2026, 10, 4, 8, 8, 0, 0, time.FixedZone("CST", 8*60*60)))
	ctx.SessionID = "sess_test"
	e := NewEnvelopeV2(ctx, "user_message", RequestID(ctx))
	e.Text = "明天上午8点提醒我喝一杯温水"
	return e
}

func guidanceReceipt() EnvelopeV2 {
	e := guidanceUserEnvelope()
	e.Kind, e.Actor = "action_result", Actor{Kind: "system", Name: "贴贴后台"}
	e.ReplyTo = &Member{ID: 1, Name: "甲"}
	e.Results = []ActionResult{{Type: "create_reminder", Key: "drink_water", Status: "succeeded", DatabaseStatus: "saved", MemoryStatus: "synced", ReminderID: "rem_test", MemoryKey: "memory_test",
		Reminder: &Reminder{ID: "rem_test", Title: "喝一杯温水", DueAt: time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC), RecipientIDs: []int64{1}, CreatedBy: 1, Status: "scheduled"}}}
	return e
}

func TestReminderGuidanceGroundsOnlyExplicitFutureRequest(t *testing.T) {
	e := guidanceUserEnvelope()
	guided := ReminderGuidance(e)
	if guided.ReminderRequest == nil || guided.ReminderRequest.DueAt.Format(time.RFC3339) != "2026-10-05T08:00:00+08:00" || guided.ReminderRequest.Title != "喝一杯温水" || len(guided.Results) != 0 {
		t.Fatalf("guidance must provide the exact requested reminder without executing it: %+v", guided)
	}
	if guided.Text != e.Text || guided.RequestID != e.RequestID || guided.Actor != e.Actor {
		t.Fatal("guidance must retain literal member input and provenance")
	}
	for _, text := range []string{"明天8点提醒我喝水", "今天上午8点提醒我喝水", "每天上午8点提醒我喝水", "明天上午8点提醒我喝水，9点吃药", "明天上午8点提醒我喝水，顺便查查天气", "明天上午8点提醒我喝水并且查查天气", "明天上午8点提醒我喝水但先别设置"} {
		e.Text = text
		if got := ReminderGuidance(e); got.ReminderRequest != nil {
			t.Fatalf("unclear or elapsed request must not receive authoritative guidance: %s", text)
		}
	}
	e = guidanceUserEnvelope()
	e.HasAttachments = true
	if got := ReminderGuidance(e); got.ReminderRequest != nil {
		t.Fatal("attachments may contain additional intent and must keep normal interpretation")
	}
	e = guidanceUserEnvelope()
	e.ReplyMode = SilentReply
	if got := ReminderGuidance(e); got.ReminderRequest != nil {
		t.Fatal("member-to-member messages must not receive reminder guidance")
	}
}

func TestReminderGuidanceKeepsActualSuccessfulReceipt(t *testing.T) {
	e := guidanceReceipt()
	guided := ReminderGuidance(e)
	if guided.ReplyInstructions == "" || guided.Results[0].Reminder.DueAt.Format(time.RFC3339) != "2026-10-05T08:00:00+08:00" {
		t.Fatalf("successful receipt must retain the saved instant in Shanghai time: %+v", guided)
	}
	if !guided.Results[0].Reminder.DueAt.Equal(e.Results[0].Reminder.DueAt) || guided.Results[0].MemoryStatus != "synced" || guided.Results[0].MemoryKey != "memory_test" || guided.Results[0].DatabaseStatus != "saved" {
		t.Fatal("reply guidance must preserve durable execution facts")
	}
	if e.Results[0].Reminder.DueAt.Location() != time.UTC {
		t.Fatal("guidance must not mutate the original durable result")
	}
}

func TestReminderGuidanceNeverShortcutsUnconfirmedResults(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*EnvelopeV2)
	}{
		{"failed", func(e *EnvelopeV2) { e.Results[0].Status = "failed" }},
		{"partial", func(e *EnvelopeV2) { e.Results[0].Status = "partial" }},
		{"memory pending", func(e *EnvelopeV2) { e.Results[0].MemoryStatus = "pending" }},
		{"database not saved", func(e *EnvelopeV2) { e.Results[0].DatabaseStatus = "not_requested" }},
		{"missing actual reminder", func(e *EnvelopeV2) { e.Results[0].Reminder = nil }},
		{"different reminder", func(e *EnvelopeV2) { e.Results[0].ReminderID = "rem_other" }},
		{"unknown recipient", func(e *EnvelopeV2) { e.Results[0].Reminder.RecipientIDs = []int64{3} }},
		{"unknown reply member", func(e *EnvelopeV2) { e.ReplyTo.ID = 3 }},
		{"different owner", func(e *EnvelopeV2) { e.Results[0].Reminder.CreatedBy = 2 }},
		{"mixed actions", func(e *EnvelopeV2) { e.Results = append(e.Results, ActionResult{Type: "save_memory"}) }},
		{"silent", func(e *EnvelopeV2) { e.ReplyMode = SilentReply }},
	} {
		t.Run(test.name, func(t *testing.T) {
			e := guidanceReceipt()
			test.change(&e)
			if got := ReminderGuidance(e); got.ReplyInstructions != "" {
				t.Fatal("unconfirmed/mixed results must keep the ordinary truthful reply path")
			}
		})
	}
}

func TestReminderGuidanceSurvivesCompactTransport(t *testing.T) {
	e := ReminderGuidance(guidanceUserEnvelope())
	_, state := CompactFrame(e, nil)
	text, _ := CompactFrame(e, &state)
	input, ok := DecodeInput(text)
	if !ok || input.RequestID != e.RequestID || input.Text != e.Text || !input.Context.Now.Equal(e.CurrentTime) {
		t.Fatal("hint fields must not weaken input provenance or change member text")
	}
	body, ok := v2EnvelopeBody(text)
	if !ok {
		t.Fatal("compact frame missing envelope")
	}
	body, _, _ = strings.Cut(body, closeV2)
	var compact EnvelopeV2
	if err := json.Unmarshal([]byte(body), &compact); err != nil || compact.ReminderRequest == nil {
		t.Fatal("normalized request guidance must be present in existing-session compact frames")
	}
}
