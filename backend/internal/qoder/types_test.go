package qoder

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDisplayUserText(t *testing.T) {
	raw := "帮我看看\n\n我还附上了这些文件，请按需读取：\n报告.docx：/mnt/session/uploads/a.txt\n数据.xlsx：/mnt/session/uploads/b.txt"
	got := displayUserText(raw)
	want := "帮我看看\n\n📎 报告.docx\n📎 数据.xlsx"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	// 不含标记时原样返回
	if displayUserText("普通消息") != "普通消息" {
		t.Fatal("plain text changed")
	}
	// 挂载行格式不符时原样返回
	bad := "我还附上了这些文件，请按需读取：\n随便一行"
	if displayUserText(bad) != bad {
		t.Fatal("malformed mount list should pass through")
	}
}

func TestPublicMessages(t *testing.T) {
	events := []Event{
		{ID: "evt_1", Type: "user.message", ProcessedAt: "2026-09-30T10:00:00Z",
			Content: []ContentBlock{{Type: "text", Text: " 你好 "}}},
		{ID: "evt_2", Type: "agent.message", ProcessedAt: "2026-09-30T10:01:00Z",
			Content: []ContentBlock{
				{Type: "text", Text: "回复"},
				{Type: "image", Source: &ImageSource{Type: "base64", MediaType: "image/png", Data: "aGVsbG8="}},
			}},
		{ID: "bad", Type: "agent.message", Content: []ContentBlock{{Type: "text", Text: "x"}}}, // ID 非法，应跳过
		{ID: "evt_3", Type: "session.status_idle"},                                             // 非消息，应跳过
	}
	msgs := publicMessages(events)
	if len(msgs) != 2 {
		t.Fatalf("want 2 messages, got %d", len(msgs))
	}
	if msgs[0].Sender != "self" || msgs[0].Text != " 你好 " || msgs[0].Kind != "text" {
		t.Fatalf("unexpected user message: %+v", msgs[0])
	}
	if msgs[0].Time == "" {
		t.Fatal("time should be formatted")
	}
	if msgs[1].Sender != "ai" || msgs[1].Text != "回复" || len(msgs[1].Images) != 1 ||
		!strings.HasPrefix(msgs[1].Images[0], "data:image/png;base64,") {
		t.Fatalf("unexpected agent message: %+v", msgs[1])
	}
}

func TestPublicTurnError(t *testing.T) {
	ev := &Event{Error: &eventError{Message: "image length and width must be larger"}}
	if !strings.Contains(publicTurnError(ev), "图片尺寸") {
		t.Fatal("image error not mapped")
	}
	ev2 := &Event{Error: &eventError{Message: "boom"}}
	if publicTurnError(ev2) != "云端处理这条消息时出错，请检查附件后重试。" {
		t.Fatal("generic error not mapped")
	}
}

func TestParseStreamEvent(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		want    string // "" 表示应忽略
	}{
		{"start", `{"type":"event_start","event":{"id":"evt_1","type":"agent.thinking"}}`, `{"id":"evt_1","kind":"thinking","type":"start"}`},
		{"start_bad_id", `{"type":"event_start","event":{"id":"x","type":"agent.message"}}`, ""},
		{"delta", `{"type":"event_delta","event_id":"evt_1","delta":{"type":"content_delta","content":{"type":"text","text":"你好"}}}`, `{"id":"evt_1","text":"你好","type":"delta"}`},
		{"delta_non_text", `{"type":"event_delta","event_id":"evt_1","delta":{"type":"content_delta","content":{"type":"image"}}}`, ""},
		{"status", `{"type":"session.status_idle","id":"evt_2"}`, `{"id":"evt_2","status":"idle","type":"status"}`},
		{"deleted", `{"type":"session.deleted","id":"evt_3"}`, `{"id":"evt_3","status":"terminated","type":"status"}`},
		{"session_error", `{"type":"session.error","id":"evt_4","error":{"message":"boom"}}`, `{"id":"evt_4","message":"云端处理这条消息时出错，请检查附件后重试。","type":"session_error"}`},
		{"thinking_end", `{"type":"agent.thinking","id":"evt_5"}`, `{"id":"evt_5","type":"thinking_end"}`},
		{"garbage", `not json`, ""},
		{"unknown", `{"type":"something.new","id":"evt_7"}`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ParseStreamEvent([]byte(c.payload))
			if c.want == "" {
				if got != nil {
					t.Fatalf("want ignored, got %v", got)
				}
				return
			}
			if got == nil {
				t.Fatal("want event, got nil")
			}
			encoded, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			if string(encoded) != c.want {
				t.Fatalf("got %s, want %s", encoded, c.want)
			}
		})
	}
}

func TestParseStreamEventMessage(t *testing.T) {
	got := ParseStreamEvent([]byte(
		`{"type":"agent.message","id":"evt_6","content":[{"type":"text","text":"回复"}],"processed_at":"2026-09-30T10:00:00Z"}`))
	if got == nil || got["type"] != "message" || got["id"] != "evt_6" {
		t.Fatalf("unexpected stream event: %v", got)
	}
	msg, ok := got["message"].(PublicMessage)
	if !ok {
		t.Fatalf("message field missing: %v", got["message"])
	}
	if msg.Sender != "ai" || msg.Text != "回复" || msg.CreatedAt != "2026-09-30T10:00:00Z" || msg.Time == "" {
		t.Fatalf("unexpected message: %+v", msg)
	}
}

func TestBaseNameNoExt(t *testing.T) {
	if got := baseNameNoExt("年度报告.docx", 70); got != "年度报告" {
		t.Fatalf("got %q", got)
	}
	long := strings.Repeat("名", 100)
	if got := baseNameNoExt(long+".docx", 70); len([]rune(got)) != 70 {
		t.Fatalf("want 70 runes, got %d", len([]rune(got)))
	}
}

func TestDecodeBase64(t *testing.T) {
	if _, err := DecodeBase64("aGVsbG8"); err != nil { // 无填充
		t.Fatal(err)
	}
	if _, err := DecodeBase64("aGVsbG8="); err != nil { // 有填充
		t.Fatal(err)
	}
}

func TestPublicMessagesAskToolUse(t *testing.T) {
	input := json.RawMessage(`{"questions":[{"header":"晚餐详情","multiSelect":false,"question":"要不要补充一下时间或细节再发呀？","options":[{"label":"就发这条，不用时间","description":"不设具体时间"},{"label":"   ","description":"空标签应被丢掉"},{"label":"设置成提醒"}]}]}`)
	answered := publicMessages([]Event{
		{ID: "evt_ask", Type: "agent.custom_tool_use", Name: "AskUserQuestion", ProcessedAt: "2026-10-01T04:53:08Z", Input: input},
		{ID: "evt_ans", Type: "user.custom_tool_result", CustomToolUseID: "evt_ask", Content: []ContentBlock{{Type: "text", Text: "就发这条，不用时间"}}},
		{ID: "evt_unknown", Type: "agent.custom_tool_use", Name: "SomeOtherTool", Input: json.RawMessage(`{"foo":1}`)},
	})
	if len(answered) != 1 {
		t.Fatalf("只应留下可应答的提问，且工具应答本身不进正文: %+v", answered)
	}
	msg := answered[0]
	if msg.Kind != "ask" || msg.Sender != "ai" || !msg.Answered {
		t.Fatalf("提问消息不对: %+v", msg)
	}
	if msg.Text != "要不要补充一下时间或细节再发呀？" || len(msg.Ask) != 1 || msg.Ask[0].Header != "晚餐详情" {
		t.Fatalf("题目内容不对: %+v", msg)
	}
	if len(msg.Ask[0].Options) != 2 || msg.Ask[0].Options[1].Label != "设置成提醒" {
		t.Fatalf("选项应过滤空标签: %+v", msg.Ask[0].Options)
	}
	pending := publicMessages([]Event{{ID: "evt_ask", Type: "agent.custom_tool_use", Input: input}})
	if len(pending) != 1 || pending[0].Answered {
		t.Fatalf("没有应答事件时应为待回答: %+v", pending)
	}
}
