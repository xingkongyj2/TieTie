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

// Exercise real model intent classification and persistence in an isolated space.
func TestLiveDateFactsChooseStorageAndClarifyAmbiguity(t *testing.T) {
	if os.Getenv("QODER_LIVE_TEST") != "1" {
		t.Skip("requires QODER_LIVE_TEST=1 and -tags live")
	}
	for _, fixture := range []struct {
		name       string
		memoryOnly bool
	}{{"cards", false}, {"memory_only", true}} {
		t.Run(fixture.name, func(t *testing.T) { testLiveDateIntent(t, fixture.memoryOnly) })
	}
}

func testLiveDateIntent(t *testing.T, memoryOnly bool) {
	cfg := loadLiveTestConfig(t)
	cfg.ConversationProtocolVersion, cfg.CloudMemoryEnabled = 2, true
	s, _, _, users, call := setupV2(t)
	s.Cfg, s.Qoder = &cfg, qoder.NewClient(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	if _, err := s.DB.Unbind(ctx, users[0].ID); err != nil {
		t.Fatal(err)
	}
	if res := call(0, "POST", "/api/account/bind", map[string]string{"code": users[1].Code}); res.Code != 200 {
		t.Fatal("isolated initialization failed", res.Code)
	}
	b, err := s.DB.GetLatestBindingByUser(ctx, users[0].ID)
	if err != nil || b == nil {
		t.Fatal(err)
	}
	store, err := s.DB.GetSpaceMemoryStore(ctx, b.SessionID)
	if err != nil || store == nil {
		t.Fatal(err)
	}
	path := "/api/qoder/sessions/" + b.SessionID
	pendingQuestion := ""
	defer func() {
		if pendingQuestion != "" {
			call(0, "POST", path+"/tool-result", map[string]string{"toolUseId": pendingQuestion, "text": "取消本次请求，不再执行任何操作。"})
			time.Sleep(3 * time.Second)
		}
		s.cleanupInitializedSpace(b.SessionID, store.StoreID)
	}()
	awaitReply := func(prompt string, allowQuestion bool) string {
		t.Helper()
		deadline := time.Now().Add(time.Minute)
		for ctx.Err() == nil && time.Now().Before(deadline) {
			if err := s.syncPendingConversation(ctx, b.SessionID); err != nil {
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
			history, err := s.Qoder.GetMessages(ctx, b.SessionID, "")
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
			seen := false
			for _, event := range history.Events {
				input, ok := conversation.DecodeInput(eventText(event))
				if request != "" && ok && !input.Hidden && input.RequestID == request {
					seen = true
				}
				if seen && event.Type == "agent.custom_tool_use" {
					pendingQuestion = event.ID
					if !allowQuestion {
						t.Fatalf("clear date unnecessarily questioned: %s", string(event.Input))
					}
					t.Log("real AI clarification:", string(event.Input))
					return event.ID
				}
			}
			for _, message := range history.Messages {
				if request != "" && message.Sender == "ai" && message.RequestID == request {
					t.Log("real AI reply:", message.Text)
					if allowQuestion && !strings.ContainsAny(message.Text, "？?") {
						t.Fatal("ambiguous date did not ask for its meaning")
					}
					return ""
				}
			}
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
			}
		}
		t.Fatal("date intent response timed out")
		return ""
	}
	ask := func(prompt string, allowQuestion bool) string {
		t.Helper()
		if res := call(0, "POST", path+"/messages", map[string]string{"text": prompt}); res.Code != 200 {
			t.Fatal("chat send failed", res.Code)
		}
		return awaitReply(prompt, allowQuestion)
	}
	countdowns := func() []dbop.Countdown {
		t.Helper()
		rows, _, err := s.DB.ListCountdowns(ctx, b.SessionID, "", time.Now())
		if err != nil {
			t.Fatal(err)
		}
		return rows
	}
	assertCountdown := func(date, kind, repeat string) dbop.Countdown {
		t.Helper()
		for _, row := range countdowns() {
			if row.Date == date && row.Kind == kind && row.Repeat == repeat {
				return row
			}
		}
		t.Fatalf("missing persisted %s date %s (%s): %+v", kind, date, repeat, countdowns())
		return dbop.Countdown{}
	}

	if !memoryOnly {
		ask("妈妈是1987年12月24号出生", false)
		mother := assertCountdown("1987-12-24", "birthday", "annual")
		if len(countdowns()) != 1 || !strings.Contains(mother.Title, "妈妈") {
			t.Fatal("birth date not saved as exactly one mother's birthday", countdowns())
		}
		for _, user := range users {
			profile, err := s.DB.GetUserProfile(ctx, user.ID)
			if err != nil || profile.Birthday != "" {
				t.Fatal("mother's birth date contaminated member profile", err)
			}
		}
		// Verify that the card is actually present in the mounted, paged JSON memory.
		memory, err := s.DB.GetMemoryRecord(ctx, dbop.MemoryID(b.SessionID, dbop.CountdownMemoryPath(mother.ID)), b.SessionID)
		if err != nil || memory == nil {
			t.Fatal("birthday memory missing", err)
		}
		if err := s.syncMemoryLocked(ctx, *memory); err != nil {
			t.Fatal(err)
		}
		memory, err = s.DB.GetMemoryRecord(ctx, memory.ID, b.SessionID)
		if err != nil {
			t.Fatal(err)
		}
		entry, err := s.Qoder.GetMemory(ctx, store.StoreID, memory.EntryID)
		if err != nil || !strings.HasPrefix(entry.Path, "agreements/shared/") || !memoryspace.IsDocumentPath(entry.Path) {
			t.Fatal("birthday did not use the existing shared template", err)
		}
		fact, err := extractFactMemory(entry.Content, memory.ID)
		if err != nil {
			t.Fatal(err)
		}
		var fields struct{ Date, Kind, Repeat, EntryType string }
		if err := json.Unmarshal([]byte(fact), &fields); err != nil || fields.Date != mother.Date || fields.Kind != "birthday" || fields.Repeat != "annual" || fields.EntryType != "countdown" {
			t.Fatal("cloud birthday fact differs from card", fields, err)
		}

		question := ask("爸爸的日期是1990年3月8日", true)
		if len(countdowns()) != 1 {
			t.Fatal("ambiguous date prematurely created a card", countdowns())
		}
		if question != "" {
			if res := call(0, "POST", path+"/tool-result", map[string]string{"toolUseId": question, "text": "是生日"}); res.Code != 200 {
				t.Fatal("clarification answer failed", res.Code)
			}
			pendingQuestion = ""
			awaitReply("是生日", false)
		} else {
			ask("是生日", false)
		}
		assertCountdown("1990-03-08", "birthday", "annual")
		// Even an already expired deadline stays once, rather than becoming annual.
		ask("我的护照2024年3月1日到期", false)
		assertCountdown("2024-03-01", "deadline", "once")
		ask("我们2025年5月24日在一起的", false)
		anniversaries, _, err := s.DB.ListAnniversaries(ctx, b.SessionID, "", 50)
		if err != nil || len(anniversaries) != 1 || anniversaries[0].Date != "2025-05-24" || anniversaries[0].Kind != "together" {
			t.Fatal("clear commemorative fact not saved", anniversaries, err)
		}
	}
	if memoryOnly {
		ask("哥哥是1992年6月6日出生，只记住这条信息，不建卡片", false)
		if len(countdowns()) != 0 {
			t.Fatal("explicit memory-only preference ignored", countdowns())
		}
		index, err := s.DB.ListMemoryIndex(ctx, b.SessionID, "agreement", "", 100)
		if err != nil {
			t.Fatal(err)
		}
		brotherRemembered := false
		for _, record := range index {
			fact, err := s.DB.GetMemoryRecord(ctx, record.ID, b.SessionID)
			if err != nil || fact == nil {
				t.Fatal("date memory unreadable", err)
			}
			if strings.Contains(fact.Content, "哥哥") && (strings.Contains(fact.Content, "1992年6月6日") || strings.Contains(fact.Content, "1992-06-06")) {
				brotherRemembered = true
			}
		}
		if !brotherRemembered {
			t.Fatal("memory-only date was acknowledged without actually saving")
		}
	}
	if reminders, err := s.DB.ListReminders(ctx, b.SessionID); err != nil || len(reminders) != 0 {
		t.Fatal("date facts created unrequested scheduled reminders", reminders, err)
	}
	t.Log("clear birth/deadline/anniversary saved; ambiguity clarified; memory-only override respected")
}
