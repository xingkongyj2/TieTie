//go:build live

package api

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"tietie/backend/internal/conversation"
	"tietie/backend/internal/dbop"
	"tietie/backend/internal/memoryspace"
	"tietie/backend/internal/qoder"
)

// Real AI and cloud storage, with isolated accounts and resources only.
func TestLiveAIAnniversaryAdditionCorrectionAndPin(t *testing.T) {
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
	if res := call(0, "POST", "/api/account/bind", map[string]string{"code": users[1].Code}); res.Code != 200 {
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
	ask := func(prompt, date string) dbop.Anniversary {
		t.Helper()
		if res := call(0, "POST", path+"/messages", map[string]string{"text": prompt}); res.Code != 200 {
			t.Fatal(res.Body.String())
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
			request := ""
			for _, event := range history.Events {
				input, ok := conversation.DecodeInput(eventText(event))
				if ok && !input.Hidden && input.UserID == users[0].ID && input.Text == prompt {
					request = input.RequestID
				}
			}
			rows, _, err := s.DB.ListAnniversaries(ctx, binding.SessionID, "", 50)
			if err != nil {
				t.Fatal(err)
			}
			for _, message := range history.Messages {
				if request == "" || message.Sender != "ai" || message.Source != "chat" || message.RequestID != request {
					continue
				}
				if len(rows) != 1 || rows[0].Date != date || rows[0].Title != "第一次一起旅行" {
					t.Fatalf("AI replied without saving the requested date: rows=%+v reply=%s", rows, message.Text)
				}
				t.Logf("real AI anniversary: %s", message.Text)
				return rows[0]
			}
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
			}
		}
		t.Fatal("anniversary AI response timed out")
		return dbop.Anniversary{}
	}
	row := ask("帮我记住一个纪念日：2025年5月24日是我们第一次一起旅行，名称就叫第一次一起旅行。", "2025-05-24")
	if res := call(1, "PATCH", path+"/anniversaries/"+row.ID, map[string]any{"pinned": true}); res.Code != 200 {
		t.Fatal(res.Body.String())
	}
	changed := ask("把第一次一起旅行改成2025年5月25日，还是同一个纪念日。", "2025-05-25")
	if changed.ID != row.ID || !changed.Pinned {
		t.Fatal("correction duplicated the date or lost the pin", changed)
	}
	memory, err := s.DB.GetMemoryRecord(ctx, dbop.MemoryID(binding.SessionID, dbop.AnniversaryMemoryPath(row.ID)), binding.SessionID)
	if err != nil || memory == nil {
		t.Fatal("anniversary memory missing", err)
	}
	if err := s.syncMemoryLocked(ctx, *memory); err != nil {
		t.Fatal(err)
	}
	memory, err = s.DB.GetMemoryRecord(ctx, memory.ID, binding.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := s.Qoder.GetMemory(ctx, store.StoreID, memory.EntryID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(entry.Path, "agreements/shared/") || !memoryspace.IsDocumentPath(entry.Path) {
		t.Fatal("anniversary used a new memory type", entry.Path)
	}
	fact, err := extractFactMemory(entry.Content, memory.ID)
	if err != nil {
		t.Fatal(err)
	}
	var fields struct {
		Date      string
		Pinned    bool
		EntryType string
	}
	if err := json.Unmarshal([]byte(fact), &fields); err != nil || fields.Date != changed.Date || !fields.Pinned || fields.EntryType != "anniversary" {
		t.Fatal("cloud anniversary stale", fields, err)
	}
	if reminders, err := s.DB.ListReminders(ctx, binding.SessionID); err != nil || len(reminders) != 0 {
		t.Fatal("date save created an unrequested reminder", reminders, err)
	}
	t.Log("real AI addition, correction, single pin and same-template cloud memory verified")
	// Reproduce the user's exact cancellation language against a real wedding card.
	wedding, err := s.DB.ApplyAnniversary(ctx, binding.SessionID, "live_wedding", users[0].ID, "", "结婚的日子", "2026-05-01", "wedding")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.PinAnniversary(ctx, binding.SessionID, wedding.ID, users[0].ID, true); err != nil {
		t.Fatal(err)
	}
	weddingMemory, _ := s.DB.GetMemoryRecord(ctx, dbop.MemoryID(binding.SessionID, dbop.AnniversaryMemoryPath(wedding.ID)), binding.SessionID)
	if err := s.syncMemoryLocked(ctx, *weddingMemory); err != nil {
		t.Fatal(err)
	}
	prompt := "取消结婚的日期"
	if res := call(0, "POST", path+"/messages", map[string]string{"text": prompt}); res.Code != 200 {
		t.Fatal(res.Body.String())
	}
	deleted := false
	for ctx.Err() == nil && !deleted {
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
		request := ""
		for _, event := range history.Events {
			input, ok := conversation.DecodeInput(eventText(event))
			if ok && !input.Hidden && input.Text == prompt {
				request = input.RequestID
			}
		}
		for _, message := range history.Messages {
			if request == "" || message.Sender != "ai" || message.Source != "chat" || message.RequestID != request {
				continue
			}
			rows, _, err := s.DB.ListAnniversaries(ctx, binding.SessionID, "", 50)
			if err != nil || len(rows) != 1 || rows[0].ID != changed.ID {
				t.Fatal("AI falsely confirmed a deletion", rows, message.Text, err)
			}
			job, err := s.DB.GetControl(ctx, binding.SessionID, request)
			if err != nil || job == nil || !strings.Contains(job.Results, `"type":"delete_anniversary"`) || !strings.Contains(job.Results, `"databaseStatus":"deleted"`) {
				t.Fatal("AI skipped the dedicated deletion action", job, err)
			}
			t.Logf("real AI wedding cancellation: %s", message.Text)
			deleted = true
		}
		if !deleted {
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
			}
		}
	}
	if !deleted {
		t.Fatal("real anniversary deletion timed out")
	}
	weddingMemory, _ = s.DB.GetMemoryRecord(ctx, weddingMemory.ID, binding.SessionID)
	if weddingMemory.Operation != "delete" || weddingMemory.State != "deleted" {
		t.Fatal("AI deleted the card but not its current cloud memory", weddingMemory)
	}
	entry, err = s.Qoder.GetMemory(ctx, store.StoreID, weddingMemory.EntryID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := extractFactMemory(entry.Content, weddingMemory.ID); err == nil {
		t.Fatal("deleted wedding survived in current memory page")
	}
	if featured, _ := s.DB.FeaturedAnniversary(ctx, binding.SessionID); featured != nil {
		t.Fatal("deleted wedding remained pinned")
	}
	t.Log("real AI cancellation removed the database card and current cloud fact, preserving the other anniversary")
}
