package conversation

import (
	"encoding/json"
	"strings"
	"testing"
)

func frameBody(t *testing.T, raw string) EnvelopeV2 {
	t.Helper()
	body, ok := v2EnvelopeBody(raw)
	if !ok {
		t.Fatal("invalid transport frame")
	}
	body, _, _ = strings.Cut(body, closeV2)
	var frame EnvelopeV2
	if err := json.Unmarshal([]byte(body), &frame); err != nil {
		t.Fatal(err)
	}
	return frame
}
func TestCompactFramesRetainIdentityAndOnlySendChanges(t *testing.T) {
	ctx := testContext()
	ctx.MemoryIndex = []MemoryIndex{{Key: "tea", Path: "profile/members/1/tea.json", Revision: 1, State: "synced"}}
	e := NewEnvelopeV2(ctx, "user_message", "first")
	e.Text = "你好"
	first, state := CompactFrame(e, nil)
	if !strings.Contains(first, TransportInstructions("")) {
		t.Fatal("first turn lacks contract")
	}
	e.RequestID = "second"
	e.Text = "提醒我，不是对方"
	compact, next := CompactFrame(e, &state)
	body := frameBody(t, compact)
	if strings.Contains(compact, v2ContractIntro) || len(body.Members) != 0 || len(body.Reminders) != 0 || len(body.MemoryIndex) != 0 || body.Timezone != "" {
		t.Fatal("unchanged context repeated", compact)
	}
	input, ok := DecodeInput(compact)
	if !ok || input.Hidden || input.UserID != ctx.AuthorID || input.Text != e.Text {
		t.Fatal("member identity lost", input, ok)
	}
	if assistant := ParseAssistant(compact); !assistant.Control || assistant.Text != "" {
		t.Fatal("echo leaked")
	}
	if len(compact)*10 >= len(first) {
		t.Fatal("daily message not materially smaller", len(first), len(compact))
	}
	t.Logf("first %d bytes; unchanged daily message %d bytes", len(first), len(compact))
	e.Members[1].Name = "新名字"
	e.Reminders = nil
	e.MemoryIndex[0].Revision = 2
	updated, _ := CompactFrame(e, &next)
	delta := frameBody(t, updated)
	if len(delta.Members) != 2 || len(delta.MemoryIndex) != 1 || delta.MemoryIndex[0].Revision != 2 || len(delta.RemovedReminderIDs) != len(next.Reminders) {
		t.Fatal("context change lost", delta)
	}
	e.MemoryIndex = nil
	cleared, _ := CompactFrame(e, &next)
	if frameBody(t, cleared).RemovedMemoryKeys[0] != "tea" {
		t.Fatal("removed memory index lost")
	}
}
func TestCompactSystemEventsAreHiddenAndMemberTextIsLiteral(t *testing.T) {
	ctx := testContext()
	ctx.Visibility = "private"
	e := NewEnvelopeV2(ctx, "action_result", "receipt")
	e.Results = []ActionResult{{Key: "video", Status: "succeeded"}}
	first, state := CompactFrame(e, nil)
	compact, _ := CompactFrame(e, &state)
	for _, raw := range []string{first, compact} {
		input, ok := DecodeInput(raw)
		if !ok || !input.Hidden || input.Kind != "action_result" || input.Context.Visibility != "private" {
			t.Fatal(input, ok)
		}
	}
	e = NewEnvelopeV2(ctx, "user_message", "user")
	e.Text = compact
	raw, _ := CompactFrame(e, &state)
	input, ok := DecodeInput(raw)
	if !ok || input.Hidden || input.Text != compact {
		t.Fatal("nested control changed member message", input, ok)
	}
	bad := strings.Replace(raw, `"compact":true`, `"compact":false`, 1)
	if _, ok := DecodeInput(bad); ok {
		t.Fatal("bare noncompact frame accepted")
	}
}
