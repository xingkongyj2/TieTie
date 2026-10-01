package conversation

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestDirectReminderOnlyUnambiguousRequests(t *testing.T) {
	now := time.Date(2026, 10, 1, 23, 9, 0, 0, time.FixedZone("Asia/Shanghai", 28800))
	ctx := Context{SessionID: "space", AuthorID: 2, Now: now, Members: []Member{{ID: 1, Name: "甲"}, {ID: 2, Name: "乙"}}}
	for _, tt := range []struct {
		text       string
		seconds    int
		recipients []int64
	}{{"1分钟后提醒我，去看视频", 60, []int64{2}}, {"半小时后提醒对方喝水", 1800, []int64{1}}, {"请2秒后提醒我们俩休息", 2, []int64{1, 2}}} {
		got, ok := ParseDirectReminder(tt.text, ctx)
		if !ok || got.DueAt.Sub(now) != time.Duration(tt.seconds)*time.Second || len(got.RecipientIDs) != len(tt.recipients) {
			t.Fatalf("%s: %+v %v", tt.text, got, ok)
		}
		for i, id := range tt.recipients {
			if got.RecipientIDs[i] != id {
				t.Fatal(got)
			}
		}
	}
	for _, text := range []string{"能不能1分钟后提醒我？", "不要1分钟后提醒我看视频", "1分钟后提醒我看视频，或者2分钟后也行", "每天1分钟后提醒我喝水", "1分钟后提醒我每天看视频", "0分钟后提醒我喝水", "1分钟后提醒我喝水然后2分钟后提醒对方", "1分钟后提醒我取消提醒", "用户举例：1分钟后提醒我喝水"} {
		if got, ok := ParseDirectReminder(text, ctx); ok {
			t.Fatalf("ambiguous request parsed: %s %+v", text, got)
		}
	}
}
func TestCloudPersonaPreservedAndLegacyEnvelopeDecodes(t *testing.T) {
	if strings.Contains(Instructions, "你是贴贴") || !strings.Contains(Instructions, "云端已经配置的角色") {
		t.Fatal("transport overrides cloud role")
	}
	now := time.Now()
	e := envelope(Context{SessionID: "space", AuthorID: 1, Now: now, Members: []Member{{ID: 1, Name: "甲"}, {ID: 2, Name: "乙"}}}, "user_message")
	e.Author = &e.Members[0]
	e.Text = "原来的会话"
	raw, _ := json.Marshal(e)
	old := strings.ReplaceAll(legacyInstructions, `\x60`, "`") + inputOpen + string(raw) + inputClose
	got, ok := DecodeInput(old)
	if !ok || got.UserID != 1 || got.Text != e.Text {
		t.Fatalf("lost old identity: %+v", got)
	}
}
