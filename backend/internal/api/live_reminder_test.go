//go:build live

package api

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"tietie/backend/internal/config"
	"tietie/backend/internal/conversation"
	"tietie/backend/internal/dbop"
	"tietie/backend/internal/qoder"
)

// Explicit opt-in only: real cloud traffic, isolated session/store and temp DB.
// No GET messages / SSE client participates until the timer has completed.
func TestLiveOneMinuteReminderWithoutBrowser(t *testing.T) {
	liveOneMinuteReminder(t, false)
}

func TestLivePrivateOneMinuteReminderForPartner(t *testing.T) {
	liveOneMinuteReminder(t, true)
}

func liveOneMinuteReminder(t *testing.T, private bool) {
	t.Helper()
	if os.Getenv("QODER_LIVE_TEST") != "1" {
		t.Skip("requires QODER_LIVE_TEST=1 and -tags live")
	}
	for _, path := range []string{"../../.env.local", "../../.env"} {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		scan := bufio.NewScanner(f)
		for scan.Scan() {
			k, v, ok := strings.Cut(scan.Text(), "=")
			k = strings.TrimSpace(k)
			if ok && k != "" && !strings.HasPrefix(k, "#") {
				if _, set := os.LookupEnv(k); !set {
					os.Setenv(k, strings.Trim(strings.TrimSpace(v), `"'`))
				}
			}
		}
		f.Close()
	}
	cfg := config.Load()
	if cfg.Token == "" {
		t.Fatal("missing Qoder token")
	}
	cfg.CloudMemoryEnabled = true
	cfg.SchedulerPollInterval = 250 * time.Millisecond
	cloud := qoder.NewClient(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	agent, env, err := cloud.ResolveAgentAndEnv(ctx, cfg.AgentID, cfg.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	store, err := cloud.CreateMemoryStore(ctx, fmt.Sprintf("tietie-isolated-timer-smoke-%d", time.Now().UnixNano()), "isolated-integration-check")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		req, _ := http.NewRequest("DELETE", cfg.Upstream+"/memory_stores/"+store.ID, nil)
		req.Header.Set("Authorization", "Bearer "+cfg.Token)
		response, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
		if err != nil {
			t.Error("temporary store cleanup failed")
			return
		}
		response.Body.Close()
		if response.StatusCode >= 300 {
			t.Error("temporary store cleanup status", response.StatusCode)
		}
	})
	session, err := cloud.CreateSession(ctx, agent, env, "TieTie isolated one-minute timer verification", store.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		if err := cloud.DeleteSession(cleanup, session.ID); err != nil {
			t.Error("temporary session cleanup failed")
		}
	})
	s, _, users, call := setupConversation(t)
	s.Cfg = &cfg
	s.Qoder = cloud
	if _, err := s.DB.Unbind(ctx, users[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.CreateBinding(ctx, users[0].ID, users[1].ID, session.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.SaveSpaceMemoryStore(ctx, dbop.SpaceMemoryStore{SessionID: session.ID, StoreID: store.ID, NativeMounted: true}); err != nil {
		t.Fatal(err)
	}
	workerCtx, stopWorker := context.WithCancel(ctx)
	workerDone := make(chan struct{})
	go func() { defer close(workerDone); s.RunConversationWorker(workerCtx) }()
	defer func() { stopWorker(); <-workerDone }()
	path := "/api/qoder/sessions/" + session.ID + "/messages"
	start := time.Now()
	input := map[string]any{"text": "1分钟后提醒我，去看视频"}
	if private {
		input = map[string]any{"text": "这是惊喜暗号，不要让对方看到我这句话。1分钟后提醒对方喝水。", "visibility": "private"}
	}
	response := call(0, "POST", path, input)
	if response.Code != 200 {
		t.Fatal("message acceptance failed", response.Code)
	}
	diagnosticID := session.ID
	if private {
		binding, _ := s.DB.GetBindingBySessionID(ctx, session.ID)
		channel, err := s.DB.PrivateChannelForOwner(ctx, session.ID, users[0].ID, binding.CreatedAt)
		if err != nil || channel == nil {
			t.Fatal("private channel not persisted", err)
		}
		diagnosticID = channel.SessionID
		privateStore, err := s.DB.GetSpaceMemoryStore(ctx, channel.SessionID)
		if err != nil || privateStore == nil || privateStore.StoreID == store.ID {
			t.Fatal("private memory not isolated", err)
		}
		t.Cleanup(func() { s.cleanupInitializedSpace(channel.SessionID, privateStore.StoreID) })
	}
	lastState := ""
	for ctx.Err() == nil {
		rows, err := s.DB.ListVisibleReminders(ctx, session.ID, users[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) > 1 {
			t.Fatal("duplicate timers", len(rows))
		}
		if len(rows) == 0 && time.Since(start) > 30*time.Second {
			// Diagnose failed setup without making a stalled test wait three minutes.
			if diagnostic, err := cloud.GetMessages(ctx, diagnosticID, ""); err == nil {
				for _, event := range diagnostic.Events {
					if event.Type == "agent.message" {
						parsed := conversation.ParseAssistant(eventText(event))
						t.Logf("isolated setup response: error=%q body=%s", parsed.ProtocolError, eventText(event))
					}
				}
			}
			t.Fatal("AI did not create the isolated reminder within 30 seconds")
		}
		if len(rows) == 1 {
			r := rows[0]
			state := r.Status + "/" + r.TaskStatus
			if state != lastState {
				t.Log("timer lifecycle", state, "elapsed", time.Since(start).Round(time.Second))
				lastState = state
			}
			recipient := users[0].ID
			recipientName := users[0].Username
			if private {
				recipient = users[1].ID
				recipientName = users[1].Username
			}
			if len(r.RecipientIDs) != 1 || r.RecipientIDs[0] != recipient {
				t.Fatal("wrong timer recipient")
			}
			if r.TaskStatus == "completed" {
				if r.Status != "delivered" || r.TaskCompletedAt == nil || time.Since(start) < 55*time.Second {
					t.Fatal("premature or invalid completion")
				}
				memorySession := r.MemorySession()
				memory, err := s.DB.GetReminderMemory(ctx, r)
				if err != nil {
					t.Fatal(err)
				}
				if memory != nil && memory.State == "synced" {
					memoryStore, err := s.DB.GetSpaceMemoryStore(ctx, memorySession)
					if err != nil || memoryStore == nil {
						t.Fatal("missing memory store", err)
					}
					entry, err := cloud.GetMemory(ctx, memoryStore.StoreID, memory.EntryID)
					if err != nil {
						t.Fatal(err)
					}
					if !strings.Contains(entry.Content, `"taskStatus":"completed"`) {
						t.Fatal("cloud memory did not record completed timer")
					}
					history := call(1, "GET", path, nil)
					if history.Code != 200 || strings.Contains(history.Body.String(), "tietie.control") || strings.Contains(history.Body.String(), "action_result") || !strings.Contains(history.Body.String(), "@"+recipientName) || (private && strings.Contains(history.Body.String(), "惊喜暗号")) {
						t.Fatal("invalid public reminder history")
					}
					t.Log("one-minute timer completed and cloud memory updated without browser polling")
					// Fetch raw cloud events only after completion; the scheduler did
					// all work without a browser. Verify the receipt was incremental.
					protocolHistory, err := cloud.GetMessages(ctx, memorySession, "")
					if err != nil {
						t.Fatal(err)
					}
					compactReceipt := false
					for _, event := range protocolHistory.Events {
						raw := eventText(event)
						input, ok := conversation.DecodeInput(raw)
						if ok && input.Kind == "action_result" {
							if strings.Contains(raw, conversation.TransportInstructions(input.Context.Visibility)) {
								t.Fatal("live receipt repeated full instructions")
							}
							compactReceipt = true
						}
					}
					if !compactReceipt {
						t.Fatal("no incremental receipt in live cloud history")
					}
					t.Log("AI handled compact system receipt with the configured cloud persona")
					return
				}
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatal("live reminder deadline exceeded")
}
