//go:build live

package api

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"tietie/backend/internal/qoder"
)

// Isolated fictional CSV verifies actual file reading, durable memory and the
// final Markdown reply, without touching an existing member's conversation.
func TestLiveUploadedScheduleUsesStructuredPresentation(t *testing.T) {
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
	if out := call(0, "POST", "/api/account/bind", map[string]any{"code": users[1].Code}); out.Code != 200 {
		t.Fatal("isolated initialization failed", out.Code)
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
	text, filename := "帮我记住张梦妍排班", "隔离验证 排班表(1).csv"
	fixture := "姓名,日期,班次\n张梦妍,2026-10-02,白备夜\n张梦妍,2026-10-03,休息\n其他成员,2026-10-02,白班\n"
	path := "/api/qoder/sessions/" + binding.SessionID + "/messages"
	if out := call(0, "POST", path, map[string]any{"text": text, "attachments": []map[string]any{{"kind": "file", "name": filename, "mimeType": "text/csv", "content": fixture}}}); out.Code != 200 {
		t.Fatal("isolated upload failed", out.Code)
	}
	for ctx.Err() == nil {
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
		for _, message := range history.Messages {
			if message.Sender == "self" && (message.Text != text || len(message.Files) != 1 || message.Files[0] != filename) {
				t.Fatal("uploaded member message includes transport metadata")
			}
			if message.Sender != "ai" || message.Source != "chat" {
				continue
			}
			if !strings.Contains(message.Text, "|") || !strings.Contains(message.Text, "白备夜") || !strings.Contains(message.Text, "休息") || strings.Contains(message.Text, "/mnt/session/uploads/") {
				t.Fatalf("schedule is not presented clearly: %s", message.Text)
			}
			facts, err := s.DB.MemoryIndex(ctx, binding.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			remembered := false
			for _, fact := range facts {
				if fact.Kind != "fact" || fact.State != "synced" {
					continue
				}
				// The bounded index intentionally omits bodies. Retrieve the
				// addressed record instead of testing its empty index content.
				full, err := s.DB.GetMemoryRecord(ctx, fact.ID, binding.SessionID)
				if err != nil || full == nil {
					t.Fatal("saved schedule body missing", err)
				}
				if strings.Contains(full.Content, "张梦妍") && strings.Contains(full.Content, "白备夜") && strings.Contains(full.Content, "休息") {
					remembered = true
				}
			}
			t.Logf("real uploaded schedule response:\n%s", message.Text)
			if !remembered {
				t.Fatal("uploaded schedule was not retained in durable memory")
			}
			return
		}
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
	}
	t.Fatal("uploaded schedule analysis deadline exceeded")
}
