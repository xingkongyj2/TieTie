package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"tietie/backend/internal/conversation"
)

func TestPartnerMessageSilencesRepliesAndReceiptsButSavesMemory(t *testing.T) {
	s, cloud, _, users, call := setupV2(t)
	ctx := context.Background()
	path := "/api/qoder/sessions/sess_shared/messages"
	out := call(0, "POST", path, map[string]any{"text": "@小二 我通常23点睡觉"})
	if out.Code != 200 || !strings.Contains(out.Body.String(), `"replyMode":"silent"`) {
		t.Fatal(out.Code, out.Body.String())
	}
	input := latestInput(t, cloud)
	if input.Context.ReplyMode != conversation.SilentReply || input.Context.RecipientID != users[1].ID || input.UserID != users[0].ID {
		t.Fatal(input)
	}
	appendProtocolReply(cloud, map[string]any{"protocol": "tietie.control", "version": 2, "requestId": input.RequestID, "actions": []conversation.Action{
		{Type: "save_memory", Key: "sleep_schedule", Category: "habit", Scope: "self", Storage: "memory_only", Content: "本人通常23点睡觉"},
		{Type: "create_reminder", Key: "not_authorized", Title: "喝水", DueAt: time.Now().Add(time.Hour).Format(time.RFC3339), RecipientIDs: []int64{users[1].ID}, Storage: "database_and_memory"},
	}})
	out = call(0, "GET", path, nil)
	if out.Code != 200 {
		t.Fatal(out.Body.String())
	}
	runClaimedControl(t, s)
	receipt := latestInput(t, cloud)
	if receipt.Kind != "action_result" || receipt.Context.ReplyMode != conversation.SilentReply {
		t.Fatal(receipt)
	}
	// Even an assistant that ignores silence and emits a plain confirmation is
	// suppressed by server routing, not just hidden by one browser.
	cloud.mu.Lock()
	cloud.appendPlainReply("我已记住你的作息，晚安！")
	cloud.mu.Unlock()
	for _, viewer := range []int{0, 1} {
		out = call(viewer, "GET", path, nil)
		if out.Code != 200 {
			t.Fatal(out.Body.String())
		}
		var history struct {
			Messages []struct{ Sender, Text string } `json:"messages"`
		}
		if err := json.Unmarshal(out.Body.Bytes(), &history); err != nil {
			t.Fatal(err)
		}
		if len(history.Messages) != 1 || history.Messages[0].Text != "@小二 我通常23点睡觉" || history.Messages[0].Sender == "ai" {
			t.Fatal(out.Body.String())
		}
	}
	job, _ := s.DB.GetControl(ctx, "sess_shared", input.RequestID)
	if job == nil || job.Status != "completed" {
		t.Fatal(job)
	}
	memories, err := s.DB.ListMemoryIndex(ctx, "sess_shared", "habit", "", 100)
	if err != nil || len(memories) != 1 {
		t.Fatal(memories, err)
	}
	fact, err := s.DB.GetMemoryRecord(ctx, memories[0].ID, "sess_shared")
	if err != nil || fact.OwnerID != users[0].ID || fact.SourceUserID != users[0].ID || fact.State != "synced" {
		t.Fatal(memories, err)
	}
	reminders, err := s.DB.ListContextReminders(ctx, "sess_shared")
	if err != nil || len(reminders) != 0 {
		t.Fatal("partner chat created an AI reminder", reminders, err)
	}
	// Silence belongs to one turn; the next ordinary message gets AI replies.
	out = call(1, "POST", path, map[string]any{"text": "贴贴，给我一个建议"})
	if out.Code != 200 {
		t.Fatal(out.Body.String())
	}
	normal := latestInput(t, cloud)
	if normal.Context.ReplyMode != "" {
		t.Fatal("silent mode leaked into a later turn")
	}
	cloud.mu.Lock()
	cloud.appendPlainReply("可以先从你喜欢的事情开始。 ")
	cloud.mu.Unlock()
	out = call(1, "GET", path, nil)
	if !strings.Contains(out.Body.String(), "可以先从你喜欢的事情开始") {
		t.Fatal(out.Body.String())
	}
}

func TestSilentNoMemoryHasNoControlAndPrivateMentionCannotLeak(t *testing.T) {
	s, cloud, _, _, call := setupV2(t)
	path := "/api/qoder/sessions/sess_shared/messages"
	out := call(0, "POST", path, map[string]any{"text": "@小二 晚安", "visibility": "private"})
	if out.Code != 400 {
		t.Fatal(out.Code, out.Body.String())
	}
	cloud.mu.Lock()
	posts := cloud.postings
	cloud.mu.Unlock()
	if posts != 0 {
		t.Fatal("a private partner message was dispatched")
	}
	out = call(0, "POST", path, map[string]any{"text": "@小二 晚安"})
	if out.Code != 200 {
		t.Fatal(out.Body.String())
	}
	input := latestInput(t, cloud)
	appendProtocolReply(cloud, map[string]any{"protocol": "tietie.silent", "version": 2, "requestId": input.RequestID})
	out = call(0, "GET", path, nil)
	if out.Code != 200 || strings.Contains(out.Body.String(), "tietie.silent") {
		t.Fatal(out.Body.String())
	}
	job, _ := s.DB.GetControl(context.Background(), "sess_shared", input.RequestID)
	if job != nil {
		t.Fatal("a silent no-op created a control job")
	}
}
