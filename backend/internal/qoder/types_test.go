package qoder

import (
	"encoding/json"
	"testing"
	"time"

	"tietie/backend/internal/conversation"
)

func TestPublicMessagesHidesBackendWakeups(t *testing.T) {
	ctx := conversation.Context{
		SessionID: "sess_bootstrap",
		AuthorID:  1,
		Now:       time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC),
		Members: []conversation.Member{
			{ID: 1, Name: "甲"},
			{ID: 2, Name: "乙"},
		},
	}
	for name, text := range map[string]string{
		"reminder wakeup": conversation.EncodeReminderV2(ctx, conversation.Reminder{ID: "rem_1", Title: "欢迎", RecipientIDs: []int64{1, 2}}, nil),
		"action receipt":  conversation.EncodeActionResult(ctx, "turn_bind", nil),
		"binding welcome": func() string {
			frame := conversation.NewEnvelopeV2(ctx, "binding_welcome", "bind_welcome_1")
			frame.Text = "绑定完成，请主动欢迎双方。"
			return conversation.EncodeV2(frame)
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			messages := publicMessages([]Event{{
				ID: "evt_hidden_1", Type: "user.message", ProcessedAt: "2026-10-07T09:00:00Z",
				Content: []ContentBlock{{Type: "text", Text: text}},
			}})
			if len(messages) != 0 {
				t.Fatalf("backend wakeup leaked into public history: %#v", messages)
			}
		})
	}
}

func TestPublicMessagesKeepsAssistantWelcomeReply(t *testing.T) {
	text := `{"protocol":"tietie.message","version":2,"requestId":"bind_welcome_1","text":"欢迎来到你们的专属空间。","recipientIds":[1,2],"source":"chat"}`
	messages := publicMessages([]Event{{
		ID: "evt_welcome_reply", Type: "agent.message", ProcessedAt: "2026-10-07T09:00:01Z",
		Content: []ContentBlock{{Type: "text", Text: text}},
	}})
	if len(messages) != 1 {
		t.Fatalf("assistant welcome reply was hidden or dropped: %#v", messages)
	}
	if messages[0].Sender != "ai" || messages[0].Text != "欢迎来到你们的专属空间。" || messages[0].Source != "chat" {
		t.Fatalf("unexpected welcome reply: %#v", messages[0])
	}
}

func TestBindingWelcomeWithQuotedRecipientsReachesHistoryAndStream(t *testing.T) {
	// The cloud's first greeting used string IDs despite the numeric protocol
	// example. It must remain a visible AI reply after normalization.
	space := conversation.Context{SessionID: "sess_binding", Now: time.Now(), Members: []conversation.Member{{ID: 20, Name: "甲"}, {ID: 21, Name: "乙"}}}
	frame := conversation.NewEnvelopeV2(space, "binding_welcome", "bind_welcome_sess_binding")
	frame.Text = "请主动欢迎双方。"
	wakeup := Event{ID: "evt_hidden_welcome", Type: "user.message", Content: []ContentBlock{{Type: "text", Text: conversation.EncodeV2(frame)}}}
	reply := Event{ID: "evt_cloud_welcome", Type: "agent.message", ProcessedAt: "2026-10-07T09:00:01Z", Content: []ContentBlock{{Type: "text", Text: `{"protocol":"tietie.message","version":2,"requestId":"bind_welcome_sess_binding","text":"欢迎来到你们的专属空间。","recipientIds":["20","21"],"source":"chat"}`}}}
	messages := publicMessages([]Event{wakeup, reply})
	if len(messages) != 1 || messages[0].ID != reply.ID || messages[0].Sender != "ai" || messages[0].Text != "欢迎来到你们的专属空间。" {
		t.Fatalf("cloud greeting did not reach public history: %#v", messages)
	}
	if len(messages[0].RecipientIDs) != 2 || messages[0].RecipientIDs[0] != 20 || messages[0].RecipientIDs[1] != 21 || messages[0].RequestID != frame.RequestID {
		t.Fatalf("greeting lost recipient identity or turn correlation: %#v", messages[0])
	}
	data, err := json.Marshal(reply)
	if err != nil {
		t.Fatal(err)
	}
	item := ParseStreamEvent(data)
	if item == nil || item["type"] != "message" {
		t.Fatalf("cloud greeting did not reach the live stream: %#v", item)
	}
	message, ok := item["message"].(PublicMessage)
	if !ok || message.ID != reply.ID || message.Text != messages[0].Text || message.ProtocolError != "" {
		t.Fatalf("stream greeting differs from public history: %#v", item)
	}
}

func TestBindingWelcomeProtocolIsHiddenInput(t *testing.T) {
	ctx := conversation.Context{
		SessionID: "sess_binding",
		AuthorID:  0,
		Now:       time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC),
		Members:   []conversation.Member{{ID: 1, Name: "甲"}, {ID: 2, Name: "乙"}},
	}
	frame := conversation.NewEnvelopeV2(ctx, "binding_welcome", "bind_welcome_1")
	frame.Text = "绑定完成，请主动欢迎双方。"
	input, ok := conversation.DecodeInput(conversation.EncodeV2(frame))
	if !ok {
		t.Fatal("binding welcome protocol should decode")
	}
	if input.Kind != "binding_welcome" || !input.Hidden || input.Text != frame.Text {
		t.Fatalf("unexpected binding welcome input: %#v", input)
	}
}

func TestParseStreamEventHidesBackendWakeup(t *testing.T) {
	ctx := conversation.Context{
		SessionID: "sess_stream",
		AuthorID:  1,
		Now:       time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC),
		Members:   []conversation.Member{{ID: 1, Name: "甲"}, {ID: 2, Name: "乙"}},
	}
	frames := map[string]string{
		"reminder_due": conversation.EncodeReminderV2(ctx, conversation.Reminder{ID: "rem_stream", Title: "绑定欢迎", RecipientIDs: []int64{1, 2}}, nil),
	}
	binding := conversation.NewEnvelopeV2(ctx, "binding_welcome", "bind_welcome_stream")
	binding.Text = "请主动欢迎双方。"
	frames["binding_welcome"] = conversation.EncodeV2(binding)
	for kind, text := range frames {
		data := []byte(`{"type":"user.message","id":"evt_stream_hidden_` + kind + `","content":[{"type":"text","text":` + mustJSONText(text) + `}],"processed_at":"2026-10-07T09:00:00Z"}`)
		if got := ParseStreamEvent(data); got != nil {
			t.Fatalf("hidden %s backend wakeup leaked through stream: %#v", kind, got)
		}
	}
}

// mustJSONText returns a JSON string literal without making the test depend on a
// second event payload struct. It is intentionally tiny and only used above.
func mustJSONText(value string) string {
	quoted, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(quoted)
}
