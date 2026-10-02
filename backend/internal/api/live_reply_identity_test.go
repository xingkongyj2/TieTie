//go:build live

package api

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"tietie/backend/internal/conversation"
	"tietie/backend/internal/qoder"
)

// Real AI confirmation, isolated users/DB/session/store. Only conversation and
// control queues run; the fictional reminders are never dispatched.
func TestLiveReminderConfirmationAddressesOriginalSpeaker(t *testing.T) {
	if os.Getenv("QODER_LIVE_TEST") != "1" {
		t.Skip("requires QODER_LIVE_TEST=1 and -tags live")
	}
	cfg := loadLiveTestConfig(t)
	cfg.ConversationProtocolVersion, cfg.CloudMemoryEnabled = 2, true
	s, _, _, users, call := setupV2(t)
	s.Cfg, s.Qoder = &cfg, qoder.NewClient(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if _, err := s.DB.Unbind(ctx, users[0].ID); err != nil {
		t.Fatal(err)
	}
	out := call(0, "POST", "/api/account/bind", map[string]any{"code": users[1].Code})
	if out.Code != 200 {
		t.Fatal("isolated space initialization failed", out.Code)
	}
	binding, err := s.DB.GetLatestBindingByUser(ctx, users[0].ID)
	if err != nil || binding == nil {
		t.Fatal("isolated binding missing", err)
	}
	store, err := s.DB.GetSpaceMemoryStore(ctx, binding.SessionID)
	if err != nil || store == nil {
		t.Fatal("isolated store missing", err)
	}
	defer s.cleanupInitializedSpace(binding.SessionID, store.StoreID)
	path := "/api/qoder/sessions/" + binding.SessionID + "/messages"
	for author := 0; author < 2; author++ {
		text := fmt.Sprintf("用于本次提醒的虚构事项：TA今天（%s）的排班是白备夜。10分钟后提醒我，TA今天的排班。", time.Now().In(time.FixedZone("Asia/Shanghai", 8*3600)).Format("1/2"))
		if out := call(author, "POST", path, map[string]any{"text": text}); out.Code != 200 {
			t.Fatal("member message rejected", out.Code)
		}
		requestID, confirmed := "", false
		for ctx.Err() == nil && !confirmed {
			if err := s.syncPendingConversation(ctx, binding.SessionID); err != nil {
				t.Fatal(err)
			}
			jobs, err := s.DB.ClaimControls(ctx, time.Now(), 4)
			if err != nil {
				t.Fatal(err)
			}
			for _, job := range jobs {
				if err := s.runControl(ctx, job); err != nil {
					t.Fatal(err)
				}
			}
			history, err := s.Qoder.GetMessages(ctx, binding.SessionID, "")
			if err != nil {
				t.Fatal(err)
			}
			for _, event := range history.Events {
				input, ok := conversation.DecodeInput(eventText(event))
				if ok && !input.Hidden && input.UserID == users[author].ID && input.Text == text {
					requestID = input.RequestID
				}
				if requestID != "" && ok && input.Kind == "action_result" && input.RequestID == requestID && (input.ReplyTo == nil || input.ReplyTo.ID != users[author].ID) {
					t.Fatal("live receipt lost original requester")
				}
			}
			for _, message := range history.Messages {
				if requestID == "" || message.Sender != "ai" || message.RequestID != requestID || message.Source != "chat" {
					continue
				}
				if !strings.Contains(message.Text, "你") || strings.Contains(message.Text, "提醒"+users[author].Username) || !strings.Contains(message.Text, "白备夜") {
					t.Fatalf("incorrect confirmation for %s: %s", users[author].Username, message.Text)
				}
				t.Logf("%s requested → %s", users[author].Username, message.Text)
				confirmed = true
			}
			if !confirmed {
				select {
				case <-ctx.Done():
				case <-time.After(time.Second):
				}
			}
		}
		if !confirmed {
			t.Fatal("AI confirmation deadline exceeded")
		}
		rows, err := s.DB.ListReminders(ctx, binding.SessionID)
		if err != nil || len(rows) != author+1 {
			t.Fatal("missing or duplicate reminder", err, len(rows))
		}
		for _, row := range rows {
			if len(row.RecipientIDs) != 1 || row.RecipientIDs[0] != row.CreatedBy {
				t.Fatal("remind me selected the wrong member")
			}
		}
	}
}
