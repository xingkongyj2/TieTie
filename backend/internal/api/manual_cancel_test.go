package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tietie/backend/internal/config"
	"tietie/backend/internal/dbop"
	"tietie/backend/internal/qoder"
)

func TestManualCancellationConfirmsOnceAndKeepsPrivateRouting(t *testing.T) {
	for _, private := range []bool{false, true} {
		name := "shared"
		if private {
			name = "private"
		}
		t.Run(name, func(t *testing.T) {
			s, shared, memory, users, call := setupV2(t)
			cloud, storageID, author := shared, "sess_shared", 1
			ctx := context.Background()
			if private {
				author, storageID = 0, "sess_private"
				cloud = &fakeConversation{status: "idle"}
				upstream := httptest.NewServer(&isolatedTestCloud{shared: shared, private: cloud, memory: memory})
				defer upstream.Close()
				cfg := config.Config{Upstream: upstream.URL, Token: "test", Timeout: time.Second, AgentID: "agent_test", EnvironmentID: "env_test", ConversationProtocolVersion: 2, CloudMemoryEnabled: true}
				s.Cfg, s.Qoder = &cfg, qoder.NewClient(cfg)
				if res := call(author, "POST", "/api/qoder/sessions/sess_shared/messages", map[string]any{"text": "私密聊天", "visibility": "private"}); res.Code != 200 {
					t.Fatal(res.Body.String())
				}
				input := latestInput(t, cloud)
				appendProtocolReply(cloud, map[string]any{"protocol": "tietie.message", "version": 2, "requestId": input.RequestID, "text": "在呢", "recipientIds": []int64{users[author].ID}, "source": "chat"})
				if err := s.syncPendingConversation(ctx, storageID); err != nil {
					t.Fatal(err)
				}
			}
			r, _, err := s.DB.ApplyReminderAction(ctx, storageID, "manual-fixture", 0, dbop.ReminderAction{Type: "create", Title: "记得喝水", DueAt: time.Now().Add(time.Hour), RecipientIDs: []int64{users[0].ID}, CreatedBy: users[0].ID})
			if err != nil {
				t.Fatal(err)
			}
			path := "/api/qoder/sessions/sess_shared/reminders/" + r.ID
			if private {
				if res := call(1, "PATCH", path, map[string]string{"status": "cancelled"}); res.Code != 404 {
					t.Fatal("partner accessed private cancellation", res.Code)
				}
			}
			for i := 0; i < 2; i++ {
				if res := call(author, "PATCH", path, map[string]string{"status": "cancelled"}); res.Code != 200 {
					t.Fatal(res.Code, res.Body.String())
				}
			}
			requestID := "manual_cancel_" + r.ID
			job, err := s.DB.GetControl(ctx, storageID, requestID)
			if err != nil || job == nil || !job.NotificationOnly || job.CreatedBy != users[author].ID {
				t.Fatal("missing durable confirmation", job, err)
			}
			runClaimedControl(t, s)
			input := latestInput(t, cloud)
			if !input.Hidden || input.Kind != "action_result" || input.ReplyTo == nil || input.ReplyTo.ID != users[author].ID || input.Reminder == nil || input.Reminder.ID != r.ID || input.Reminder.Title != r.Title || input.Reminder.Status != "cancelled" || len(input.Results) != 1 || input.Results[0].Status != "succeeded" {
				t.Fatal("incorrect cancellation receipt", input)
			}
			if (input.Context.Visibility == "private") != private {
				t.Fatal("confirmation visibility mismatch", input)
			}
			text := "已帮你取消「记得喝水」。"
			appendProtocolReply(cloud, map[string]any{"protocol": "tietie.message", "version": 2, "requestId": requestID, "text": text, "recipientIds": []int64{users[author].ID}, "source": "chat"})
			if err := s.syncPendingConversation(ctx, storageID); err != nil {
				t.Fatal(err)
			}
			if res := call(author, "PATCH", path, map[string]string{"status": "cancelled"}); res.Code != 200 {
				t.Fatal(res.Body.String())
			}
			jobs, err := s.DB.ClaimControls(ctx, time.Now().Add(time.Minute), 10)
			if err != nil || len(jobs) != 0 {
				t.Fatal("duplicate cancellation acknowledgment", jobs, err)
			}
			for viewer := 0; viewer < 2; viewer++ {
				res := call(viewer, "GET", "/api/qoder/sessions/sess_shared/messages", nil)
				if res.Code != 200 {
					t.Fatal(res.Body.String())
				}
				var body struct {
					Messages []qoder.PublicMessage `json:"messages"`
				}
				if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				count := 0
				for _, msg := range body.Messages {
					if msg.Text == text && msg.Source == "chat" && len(msg.RecipientIDs) == 1 && msg.RecipientIDs[0] == users[author].ID {
						count++
					}
					if strings.Contains(msg.Text, "在提醒页手动取消") || strings.Contains(msg.Text, "action_result") {
						t.Fatal("internal event became a chat message")
					}
				}
				want := 1
				if private && viewer != author {
					want = 0
				}
				if count != want {
					t.Fatalf("viewer %d: confirmations %d, want %d: %s", viewer, count, want, res.Body.String())
				}
			}
			saved, err := s.DB.GetReminder(ctx, storageID, r.ID)
			if err != nil || saved.Status != dbop.ReminderCancelled || saved.CompletedBy != nil {
				t.Fatal("cancellation changed activity facts", saved, err)
			}
		})
	}
}
