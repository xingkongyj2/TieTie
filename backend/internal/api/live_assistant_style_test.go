//go:build live

package api

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"tietie/backend/internal/conversation"
	"tietie/backend/internal/memoryspace"
	"tietie/backend/internal/qoder"
)

// Uses isolated accounts, database, conversation and store, never user data.
func TestLiveAssistantSpeakingStyleChanges(t *testing.T) {
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
	settingsPath := "/api/qoder/sessions/" + binding.SessionID + "/assistant-settings"
	messagePath := "/api/qoder/sessions/" + binding.SessionID + "/messages"
	for _, tone := range []string{"concise", "playful"} {
		if res := call(0, "PUT", settingsPath, map[string]string{"tone": tone}); res.Code != 200 || !strings.Contains(res.Body.String(), `"synced"`) {
			t.Fatal("style cloud save failed", res.Body.String())
		}
		found := false
		for _, ref := range listLiveMemoryDocuments(t, ctx, s.Qoder, store.StoreID) {
			if !memoryspace.IsDocumentPath(ref.Path) {
				t.Fatal("non-template style file", ref.Path)
			}
			if ref.Path != memoryspace.BehaviorPath {
				continue
			}
			entry, err := s.Qoder.GetMemory(ctx, store.StoreID, ref.ID)
			if err != nil {
				t.Fatal(err)
			}
			var doc struct {
				SpeakingStyle memoryspace.SpeakingStyle `json:"speakingStyle"`
				Instructions  string                    `json:"instructions"`
			}
			if err := json.Unmarshal([]byte(entry.Content), &doc); err != nil || doc.SpeakingStyle.Tone != tone {
				t.Fatal("cloud style is stale", doc, err)
			}
			if tone == "playful" && doc.Instructions == "" {
				t.Fatal("style save erased protocol")
			}
			found = true
		}
		if !found {
			t.Fatal("behavior memory missing")
		}
		prompt := "下班啦，给我一句适合现在的放松建议。"
		if res := call(0, "POST", messagePath, map[string]string{"text": prompt}); res.Code != 200 {
			t.Fatal("style reply request failed", res.Code)
		}
		matched := false
		for ctx.Err() == nil {
			if err := s.syncPendingConversation(ctx, binding.SessionID); err != nil {
				t.Fatal(err)
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
			for _, message := range history.Messages {
				if request == "" || message.Sender != "ai" || message.RequestID != request {
					continue
				}
				if message.Text == "" || strings.Contains(message.Text, "assistantStyle") || strings.Contains(message.Text, "speakingStyle") {
					t.Fatal("style did not produce a natural reply", message.Text)
				}
				if tone == "concise" && utf8.RuneCountInString(message.Text) > 120 {
					t.Fatal("concise reply is too long", message.Text)
				}
				t.Logf("real AI %s: %s", tone, message.Text)
				matched = true
			}
			if matched {
				break
			}
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
			}
		}
		if !matched {
			t.Fatal("style response timed out")
		}
	}
}
