//go:build live

package api

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"tietie/backend/internal/dbop"
	"tietie/backend/internal/qoder"
)

// Uses an isolated cloud session/store and fixture DB. Only the confirmation
// queues run, so no fictional reminder can reach a real user or become due.
func TestLiveManualCancellationProducesChatConfirmation(t *testing.T) {
	if os.Getenv("QODER_LIVE_TEST") != "1" {
		t.Skip("requires QODER_LIVE_TEST=1 and -tags live")
	}
	cfg := loadLiveTestConfig(t)
	cfg.ConversationProtocolVersion, cfg.CloudMemoryEnabled = 2, true
	s, _, _, users, call := setupV2(t)
	s.Cfg, s.Qoder = &cfg, qoder.NewClient(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if _, err := s.DB.Unbind(ctx, users[0].ID); err != nil {
		t.Fatal(err)
	}
	if res := call(0, "POST", "/api/account/bind", map[string]any{"code": users[1].Code}); res.Code != 200 {
		t.Fatal("isolated initialization failed", res.Code)
	}
	binding, err := s.DB.GetLatestBindingByUser(ctx, users[0].ID)
	if err != nil || binding == nil {
		t.Fatal(err)
	}
	store, err := s.DB.GetSpaceMemoryStore(ctx, binding.SessionID)
	if err != nil || store == nil {
		t.Fatal(err)
	}
	defer s.cleanupInitializedSpace(binding.SessionID, store.StoreID)
	path := "/api/qoder/sessions/" + binding.SessionID
	title := "隔离验证：今天的排班"
	res := call(0, "POST", path+"/reminders", map[string]any{"title": title, "dueAt": time.Now().Add(time.Hour).Format(time.RFC3339), "recipientIds": []int64{users[0].ID}})
	var body struct {
		Reminder dbop.Reminder `json:"reminder"`
	}
	if res.Code != 201 || json.Unmarshal(res.Body.Bytes(), &body) != nil {
		t.Fatal("fixture reminder failed", res.Code)
	}
	update := path + "/reminders/" + body.Reminder.ID
	for i := 0; i < 2; i++ {
		if res := call(1, "PATCH", update, map[string]string{"status": "cancelled"}); res.Code != 200 {
			t.Fatal("manual cancel failed", res.Code)
		}
	}
	requestID := "manual_cancel_" + body.Reminder.ID
	for ctx.Err() == nil {
		jobs, err := s.DB.ClaimControls(ctx, time.Now(), 4)
		if err != nil {
			t.Fatal(err)
		}
		for _, job := range jobs {
			if err := s.runControl(ctx, job); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.syncPendingConversation(ctx, binding.SessionID); err != nil {
			t.Fatal(err)
		}
		history, err := s.Qoder.GetMessages(ctx, binding.SessionID, "")
		if err != nil {
			t.Fatal(err)
		}
		for _, msg := range history.Messages {
			if msg.Sender != "ai" {
				t.Fatal("manual cancellation fabricated a member message")
			}
			if msg.RequestID != requestID {
				continue
			}
			if !strings.Contains(msg.Text, "取消") || !strings.Contains(msg.Text, title) || !strings.Contains(msg.Text, "你") || msg.Source != "chat" || len(msg.RecipientIDs) != 1 || msg.RecipientIDs[0] != users[1].ID {
				t.Fatalf("incorrect real cancellation confirmation: %+v", msg)
			}
			job, err := s.DB.GetControl(ctx, binding.SessionID, requestID)
			if err != nil || job == nil || job.Status != "completed" {
				t.Fatal("confirmation was not durably completed", job, err)
			}
			if res := call(1, "PATCH", update, map[string]string{"status": "cancelled"}); res.Code != 200 {
				t.Fatal(res.Code)
			}
			jobs, err := s.DB.ClaimControls(ctx, time.Now().Add(time.Minute), 4)
			if err != nil || len(jobs) != 0 {
				t.Fatal("repeated cancellation duplicated confirmation", jobs, err)
			}
			t.Logf("real AI cancellation confirmation: %s", msg.Text)
			return
		}
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
	}
	t.Fatal("cancellation confirmation timed out")
}
