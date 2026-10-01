//go:build live

package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"tietie/backend/internal/config"
	"tietie/backend/internal/dbop"
	"tietie/backend/internal/memoryspace"
	"tietie/backend/internal/qoder"
)

// Opt-in cloud check with a temporary local DB and fictional source facts.
// runImpression creates and cleans up its own unmounted cloud session.
func TestLivePartnerImpression(t *testing.T) {
	if os.Getenv("QODER_LIVE_TEST") != "1" {
		t.Skip("requires QODER_LIVE_TEST=1 and -tags live")
	}
	cfg := loadLiveTestConfig(t)
	s, _, _, users, call := setupV2(t)
	out := call(0, "POST", "/api/qoder/sessions/sess_shared/partner-impression", map[string]any{
		"text":      "用于隔离验证的虚构信息：TA是一名设计师，周末喜欢骑车，不喜欢熬夜。",
		"requestId": "live_observation_abcdefgh",
	})
	if out.Code != 200 {
		t.Fatal(out.Body.String())
	}
	s.Cfg = &cfg
	s.Qoder = qoder.NewClient(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	jobs, err := s.DB.ClaimImpressions(ctx, time.Now().Add(time.Second), 1)
	if err != nil || len(jobs) != 1 {
		t.Fatal("missing generation job", err)
	}
	if err := s.runImpression(ctx, jobs[0]); err != nil {
		t.Fatal(err)
	}
	row, err := s.DB.GetImpression(ctx, "sess_shared", users[1].ID)
	if err != nil || row == nil || row.Status != "ready" || row.Summary == "" {
		t.Fatal("cloud impression did not finish", err)
	}
	if !strings.Contains(row.Summary, "设计") || !strings.Contains(row.Summary, "骑") {
		t.Fatal("cloud impression did not use the supplied facts")
	}
	for _, phrase := range []string{"未经确认", "未经本人确认", "需要核实", "待确认", "本人确认后", "需要TA确认"} {
		if strings.Contains(row.Summary, phrase) {
			t.Fatal("confirmed role supplement was treated as unverified", row.Summary)
		}
	}
	t.Log("real cloud generation finished with a cached summary and isolated session cleanup")
}

func TestLiveMemoryStoreNameIncludesSessionID(t *testing.T) {
	if os.Getenv("QODER_LIVE_TEST") != "1" {
		t.Skip("requires QODER_LIVE_TEST=1 and -tags live")
	}
	cfg := loadLiveTestConfig(t)
	client := qoder.NewClient(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	owner := fmt.Sprintf("name-check-%d", time.Now().UnixNano())
	store, err := client.CreateMemoryStore(ctx, "TieTie-"+owner+"-memory", owner)
	if err != nil {
		t.Fatal(err)
	}
	sessionID := ""
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		if sessionID != "" {
			if err := client.DeleteSession(cleanup, sessionID); err != nil {
				t.Error("session cleanup", err)
			}
		}
		if err := client.DeleteMemoryStore(cleanup, store.ID); err != nil {
			t.Error("store cleanup", err)
		}
	}()
	docs, err := memoryspace.Render("pending", owner, memoryspace.Member{ID: 1, Name: "fixture A"}, memoryspace.Member{ID: 2, Name: "fixture B"})
	if err != nil {
		t.Fatal(err)
	}
	entries := make([]*qoder.MemoryEntry, 0, len(docs))
	for _, doc := range docs {
		entry, err := client.UpsertMemory(ctx, store.ID, doc.Path, doc.Content)
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, entry)
	}
	agent, env, err := client.ResolveAgentAndEnv(ctx, cfg.AgentID, cfg.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	session, err := client.CreateSession(ctx, agent, env, "TieTie memory name check", store.ID)
	if err != nil {
		t.Fatal(err)
	}
	sessionID = session.ID
	name := qoder.MemoryStoreName(sessionID)
	if err := client.RenameMemoryStore(ctx, store.ID, name); err != nil {
		t.Fatal(err)
	}
	found, err := client.FindMemoryStore(ctx, name, owner)
	if err != nil || found == nil || found.ID != store.ID {
		t.Fatal("session name or ownership was not preserved", err)
	}
	for _, original := range entries {
		entry, err := client.GetMemory(ctx, store.ID, original.ID)
		if err != nil || entry.Content != original.Content || entry.Path != original.Path {
			t.Fatal("rename changed a seeded entry", err)
		}
	}
	t.Log("mounted store includes real session ID and retains ownership and all seven templates")
}

func loadLiveTestConfig(t *testing.T) config.Config {
	t.Helper()
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
					t.Setenv(k, strings.Trim(strings.TrimSpace(v), `"'`))
				}
			}
		}
		f.Close()
	}
	cfg := config.Load()
	if cfg.Token == "" {
		t.Fatal("missing Qoder token")
	}
	return cfg
}

func TestLiveSparsePartnerImpressionUsesThirdPerson(t *testing.T) {
	if os.Getenv("QODER_LIVE_TEST") != "1" {
		t.Skip("requires QODER_LIVE_TEST=1 and -tags live")
	}
	cfg := loadLiveTestConfig(t)
	s, _, _, users, call := setupV2(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := s.DB.SaveMessage(ctx, &dbop.Message{ID: "evt_sparse_fixture", SessionID: "sess_shared", Sender: "user", UserID: users[1].ID, Text: "2分钟后提醒她放下手机", CloudCreatedAt: time.Now().Format(time.RFC3339)}); err != nil {
		t.Fatal(err)
	}
	out := call(0, "GET", "/api/qoder/sessions/sess_shared/partner-impression", nil)
	if out.Code != 200 {
		t.Fatal(out.Body.String())
	}
	s.Cfg, s.Qoder = &cfg, qoder.NewClient(cfg)
	jobs, err := s.DB.ClaimImpressions(ctx, time.Now().Add(time.Second), 1)
	if err != nil || len(jobs) != 1 {
		t.Fatal(jobs, err)
	}
	if err := s.runImpression(ctx, jobs[0]); err != nil {
		t.Fatal(err)
	}
	row, err := s.DB.GetImpression(ctx, "sess_shared", users[1].ID)
	if err != nil || row == nil || row.Status != "ready" {
		t.Fatal("impression did not complete", err)
	}
	if !strings.Contains(row.Summary, "TA") || strings.Contains(row.Summary, "关于你") || strings.Contains(row.Summary, "你的作息") || strings.Contains(row.Summary, "你的爱好") || strings.Contains(row.Summary, "你的工作") {
		t.Fatal("incorrect viewer perspective", row.Summary)
	}
	t.Log("sparse partner facts produced a third-person impression for the viewer")
}

func TestLiveSilentPartnerMemory(t *testing.T) {
	if os.Getenv("QODER_LIVE_TEST") != "1" {
		t.Skip("requires QODER_LIVE_TEST=1 and -tags live")
	}
	cfg := loadLiveTestConfig(t)
	cfg.SchedulerPollInterval = 250 * time.Millisecond
	cloud := qoder.NewClient(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	agent, env, err := cloud.ResolveAgentAndEnv(ctx, cfg.AgentID, cfg.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	store, err := cloud.CreateMemoryStore(ctx, fmt.Sprintf("tietie-silent-smoke-%d", time.Now().UnixNano()), "isolated-silent-check")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 20*time.Second)
		defer stop()
		if err := cloud.DeleteMemoryStore(cleanup, store.ID); err != nil {
			t.Error("temporary store cleanup failed")
		}
	})
	session, err := cloud.CreateSession(ctx, agent, env, "TieTie isolated silent memory verification", store.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 20*time.Second)
		defer stop()
		if err := cloud.DeleteSession(cleanup, session.ID); err != nil {
			t.Error("temporary session cleanup failed")
		}
	})
	s, _, users, call := setupConversation(t)
	s.Cfg, s.Qoder = &cfg, cloud
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
	done := make(chan struct{})
	go func() { defer close(done); s.RunConversationWorker(workerCtx) }()
	defer func() { stopWorker(); <-done }()
	path := "/api/qoder/sessions/" + session.ID + "/messages"
	response := call(0, "POST", path, map[string]any{"text": "@小二 我每个周末都喜欢骑车，通常晚上23点睡觉。"})
	if response.Code != 200 {
		t.Fatal("silent member message not accepted", response.Code)
	}
	for ctx.Err() == nil {
		rows, err := s.DB.MemoryIndex(ctx, session.ID)
		if err != nil {
			t.Fatal(err)
		}
		remembered := false
		for _, row := range rows {
			if row.Kind == "fact" && row.OwnerID == users[0].ID && row.State == "synced" {
				remembered = true
			}
		}
		job, err := s.DB.GetConversationJob(ctx, session.ID)
		if err != nil {
			t.Fatal(err)
		}
		active, err := s.DB.HasActiveControl(ctx, session.ID)
		if err != nil {
			t.Fatal(err)
		}
		if remembered && job == nil && !active {
			response = call(1, "GET", path, nil)
			if response.Code != 200 {
				t.Fatal(response.Code)
			}
			var result qoder.MessagesResult
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			for _, message := range result.Messages {
				if message.Sender == "ai" {
					t.Fatal("AI replied to a silent partner message")
				}
			}
			if len(result.Messages) != 1 {
				t.Fatal("member message was not preserved")
			}
			t.Log("real cloud quietly saved a fact, completed the control receipt, and kept chat free of AI replies")
			return
		}
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
	}
	t.Fatal("silent memory did not finish within the time budget")
}
