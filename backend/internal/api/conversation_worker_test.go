package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"tietie/backend/internal/auth"
	"tietie/backend/internal/config"
	"tietie/backend/internal/conversation"
	"tietie/backend/internal/dbop"
	"tietie/backend/internal/qoder"
)

type fakeConversation struct {
	memory      http.Handler
	mu          sync.Mutex
	events      []qoder.Event
	status      string
	postings    int
	reject      int
	failHistory bool
	autoWake    bool
}

func (cloud *fakeConversation) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/memory_stores") && cloud.memory != nil {
		cloud.memory.ServeHTTP(w, r)
		return
	}
	cloud.mu.Lock()
	defer cloud.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if strings.HasSuffix(r.URL.Path, "/events") {
		if r.Method == http.MethodPost {
			if cloud.reject != 0 {
				w.WriteHeader(cloud.reject)
				return
			}
			var body struct {
				Events []qoder.Event `json:"events"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				w.WriteHeader(400)
				return
			}
			cloud.postings++
			for i := range body.Events {
				body.Events[i].ID = fmt.Sprintf("evt_input_%d", len(cloud.events)+i)
				body.Events[i].ProcessedAt = time.Now().Format(time.RFC3339Nano)
			}
			cloud.events = append(cloud.events, body.Events...)
			cloud.status = "running"
			if cloud.autoWake {
				for _, event := range body.Events {
					input, ok := conversation.DecodeInput(eventText(event))
					if ok && input.Hidden {
						cloud.appendPlainReply("到时间啦，记得去看视频～")
					}
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": body.Events})
			return
		}
		if cloud.failHistory {
			w.WriteHeader(500)
			return
		}
		events := make([]qoder.Event, 0)
		seen := r.URL.Query().Get("after_id") == ""
		for _, event := range cloud.events {
			if seen {
				events = append(events, event)
			}
			if event.ID == r.URL.Query().Get("after_id") {
				seen = true
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": events, "has_more": false})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"id": "sess_shared", "title": "TieTie", "status": cloud.status})
}

func (cloud *fakeConversation) reply(text string, recipients []int64, actions []conversation.Action) {
	cloud.mu.Lock()
	defer cloud.mu.Unlock()
	payload, _ := json.Marshal(map[string]any{"text": text, "recipientIds": recipients, "source": "chat", "actions": actions})
	cloud.events = append(cloud.events, qoder.Event{ID: fmt.Sprintf("evt_reply_%d", len(cloud.events)), Type: "agent.message", ProcessedAt: time.Now().Format(time.RFC3339Nano), Content: []qoder.ContentBlock{{Type: "text", Text: "```tietie\n" + string(payload) + "\n```"}}})
	cloud.events = append(cloud.events, qoder.Event{ID: fmt.Sprintf("evt_idle_%d", len(cloud.events)), Type: "session.status_idle", ProcessedAt: time.Now().Format(time.RFC3339Nano)})
	cloud.status = "idle"
}

func setupConversation(t *testing.T) (*Server, *fakeConversation, []*dbop.User, func(int, string, string, any) *httptest.ResponseRecorder) {
	t.Helper()
	db, err := dbop.Open(filepath.Join(t.TempDir(), "conversation.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	users := []*dbop.User{{Username: "小一", Password: "secret", Code: "3001"}, {Username: "小二", Password: "secret", Code: "3002"}, {Username: "外人", Password: "secret", Code: "3003"}}
	for _, user := range users {
		if err := db.CreateUser(context.Background(), user); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.CreateBinding(context.Background(), users[0].ID, users[1].ID, "sess_shared"); err != nil {
		t.Fatal(err)
	}
	cloud := &fakeConversation{status: "idle"}
	upstream := httptest.NewServer(cloud)
	t.Cleanup(upstream.Close)
	cfg := config.Config{Upstream: upstream.URL, Token: "fake", Timeout: time.Second}
	server := &Server{Cfg: &cfg, DB: db, Auth: auth.NewService("test", time.Hour), Qoder: qoder.NewClient(cfg)}
	router := NewRouter(server)
	call := func(index int, method, path string, body any) *httptest.ResponseRecorder {
		token, err := server.Auth.IssueToken(users[index].ID, users[index].Username)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(body)
		req := httptest.NewRequest(method, path, bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		return response
	}
	return server, cloud, users, call
}

func TestTwoMembersReminderRunsWithoutBrowserAndShowsCorrectIdentity(t *testing.T) {
	server, cloud, users, call := setupConversation(t)
	ctx := context.Background()
	path := "/api/qoder/sessions/sess_shared"
	first := call(0, http.MethodPost, path+"/messages", map[string]any{"text": "一小时后提醒对方喝水"})
	if first.Code != 200 {
		t.Fatal(first.Body.String())
	}
	cloud.mu.Lock()
	input, ok := conversation.DecodeInput(eventText(cloud.events[0]))
	cloud.mu.Unlock()
	if !ok || input.UserID != users[0].ID || input.Text != "一小时后提醒对方喝水" || len(input.Context.Members) != 2 {
		t.Fatalf("real author lost: %+v", input)
	}
	if err := server.syncPendingConversation(ctx, "sess_shared"); err != nil {
		t.Fatal(err)
	} // Save input cursor while the AI is running.
	due := time.Now().Add(time.Hour)
	cloud.reply("我来帮你安排", []int64{users[0].ID}, []conversation.Action{{Type: "create_reminder", Key: "water", Title: "喝水", DueAt: due.Format(time.RFC3339), RecipientIDs: []int64{users[1].ID}}})
	if err := server.syncPendingConversation(ctx, "sess_shared"); err != nil {
		t.Fatal(err)
	} // No browser GET: background sync must create the reminder.
	reminders, err := server.DB.ListReminders(ctx, "sess_shared")
	if err != nil || len(reminders) != 1 || reminders[0].CreatedBy != users[0].ID {
		t.Fatalf("missing persistent AI reminder: %+v %v", reminders, err)
	}
	for viewer := 0; viewer < 2; viewer++ {
		response := call(viewer, http.MethodGet, path+"/messages", nil)
		if response.Code != 200 {
			t.Fatal(response.Body.String())
		}
		var history struct {
			Messages  []qoder.PublicMessage `json:"messages"`
			Members   []conversation.Member `json:"members"`
			Reminders []dbop.Reminder       `json:"reminders"`
		}
		if json.Unmarshal(response.Body.Bytes(), &history) != nil {
			t.Fatal("history JSON")
		}
		want := "self"
		if viewer == 1 {
			want = "partner"
		}
		if history.Messages[0].Sender != want || history.Messages[0].UserID != users[0].ID || history.Messages[0].Text != "一小时后提醒对方喝水" || len(history.Reminders) != 1 || len(history.Messages[1].ReminderIDs) != 1 {
			t.Fatalf("incorrect viewer history: %+v", history)
		}
	}
	// History replays and the other person's GET must not duplicate the AI action.
	reminders, _ = server.DB.ListReminders(ctx, "sess_shared")
	if len(reminders) != 1 {
		t.Fatal("AI action was applied twice")
	}
	claimed, err := server.DB.ClaimDueReminder(ctx, due.Add(time.Second))
	if err != nil || claimed == nil {
		t.Fatalf("due claim: %+v %v", claimed, err)
	}
	if err := server.dispatchReminder(ctx, *claimed); err != nil {
		t.Fatal(err)
	}
	current, _ := server.DB.GetReminder(ctx, "sess_shared", claimed.ID)
	if current.Status != dbop.ReminderDispatching {
		t.Fatalf("acceptance mistaken for delivery: %+v", current)
	}
	cloud.mu.Lock()
	wake, ok := conversation.DecodeInput(eventText(cloud.events[len(cloud.events)-1]))
	postings := cloud.postings
	cloud.mu.Unlock()
	if !ok || !wake.Hidden || wake.UserID != 0 || wake.Reminder == nil || wake.Reminder.RecipientIDs[0] != users[1].ID || postings != 2 {
		t.Fatalf("wrong wakeup: %+v posts %d", wake, postings)
	}
	cloud.reply("小二，到喝水的时间啦", []int64{users[0].ID}, []conversation.Action{}) // Model recipients are corrected using persisted task.
	if err := server.syncPendingConversation(ctx, "sess_shared"); err != nil {
		t.Fatal(err)
	}
	current, _ = server.DB.GetReminder(ctx, "sess_shared", claimed.ID)
	if current.Status != dbop.ReminderDelivered || current.DeliveredAt == nil {
		t.Fatalf("AI reply not marked delivered: %+v", current)
	}
	response := call(1, http.MethodGet, path+"/messages", nil)
	var history struct {
		Messages []qoder.PublicMessage `json:"messages"`
	}
	json.Unmarshal(response.Body.Bytes(), &history)
	if len(history.Messages) != 3 || history.Messages[2].Source != "reminder" || history.Messages[2].RecipientIDs[0] != users[1].ID || strings.Contains(response.Body.String(), "TIETIE_INPUT_V1") {
		t.Fatalf("hidden wakeup leaked or audience incorrect: %s", response.Body.String())
	}
	if outsider := call(2, http.MethodGet, path+"/messages", nil); outsider.Code != 403 {
		t.Fatal("outsider read shared space")
	}
}

func TestReminderBusyRetryAndUnknownNetworkResultDoNotDoubleSend(t *testing.T) {
	server, cloud, users, _ := setupConversation(t)
	ctx := context.Background()
	now := time.Now()
	reminder, _, err := server.DB.ApplyReminderAction(ctx, "sess_shared", "manual", 0, dbop.ReminderAction{Type: "create", Title: "吃饭", DueAt: now, RecipientIDs: []int64{users[0].ID, users[1].ID}})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := server.DB.ClaimDueReminder(ctx, now.Add(time.Second))
	if err != nil || claim == nil {
		t.Fatal(err)
	}
	cloud.mu.Lock()
	cloud.status = "running"
	cloud.mu.Unlock()
	if err := server.dispatchReminder(ctx, *claim); err != nil {
		t.Fatal(err)
	}
	saved, _ := server.DB.GetReminder(ctx, "sess_shared", reminder.ID)
	if saved.Status != dbop.ReminderScheduled || saved.NextAttemptAt == nil {
		t.Fatalf("busy queue did not retry safely: %+v", saved)
	}
	cloud.mu.Lock()
	posts := cloud.postings
	cloud.status = "idle"
	cloud.reject = 500
	cloud.mu.Unlock()
	if posts != 0 {
		t.Fatal("busy session was interrupted")
	}
	claim, err = server.DB.ClaimDueReminder(ctx, now.Add(time.Minute))
	if err != nil || claim == nil {
		t.Fatalf("retry claim %+v %v", claim, err)
	}
	if err := server.dispatchReminder(ctx, *claim); err == nil {
		t.Fatal("expected upstream unknown send outcome")
	}
	saved, _ = server.DB.GetReminder(ctx, "sess_shared", reminder.ID)
	if saved.Status != dbop.ReminderUncertain {
		t.Fatalf("unknown network outcome unsafe retry: %+v", saved)
	}
	if claim, err = server.DB.ClaimDueReminder(ctx, now.Add(time.Hour)); err != nil || claim != nil {
		t.Fatalf("uncertain job was sent again: %+v %v", claim, err)
	}
}

func TestUnbindStopsQueueAndNewBindingCannotReplayOldRequest(t *testing.T) {
	server, cloud, users, call := setupConversation(t)
	ctx := context.Background()
	path := "/api/qoder/sessions/sess_shared"
	response := call(0, http.MethodPost, path+"/messages", map[string]any{"text": "一小时后提醒我们吃饭"})
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	_, _, err := server.DB.ApplyReminderAction(ctx, "sess_shared", "manual", 0, dbop.ReminderAction{Type: "create", Title: "未来任务", DueAt: time.Now().Add(time.Hour), RecipientIDs: []int64{users[0].ID}})
	if err != nil {
		t.Fatal(err)
	}
	response = call(0, http.MethodPost, "/api/account/unbind", nil)
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	if claim, err := server.DB.ClaimDueReminder(ctx, time.Now().Add(2*time.Hour)); err != nil || claim != nil {
		t.Fatalf("unbound timer fired: %+v %v", claim, err)
	}
	if _, err := server.DB.CreateBinding(ctx, users[0].ID, users[1].ID, "sess_shared"); err != nil {
		t.Fatal(err)
	}
	cloud.reply("我来安排", []int64{users[0].ID}, []conversation.Action{{Type: "create_reminder", Key: "old-request", Title: "吃饭", DueAt: time.Now().Add(time.Hour).Format(time.RFC3339), RecipientIDs: []int64{users[0].ID, users[1].ID}}})
	response = call(0, http.MethodGet, path+"/messages", nil)
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	reminders, _ := server.DB.ListReminders(ctx, "sess_shared")
	if len(reminders) != 2 || reminders[0].Status != dbop.ReminderCancelled || reminders[1].Status != dbop.ReminderCancelled {
		t.Fatalf("old request replayed after rebind: %+v", reminders)
	}
}

func TestRejectedMemberMessageDoesNotLeaveAnUnresolvableSyncJob(t *testing.T) {
	server, cloud, _, call := setupConversation(t)
	ctx := context.Background()
	cloud.mu.Lock()
	cloud.reject = 409
	cloud.mu.Unlock()
	response := call(0, http.MethodPost, "/api/qoder/sessions/sess_shared/messages", map[string]any{"text": "测试拒绝"})
	if response.Code != 409 {
		t.Fatal(response.Body.String())
	}
	if job, err := server.DB.GetConversationJob(ctx, "sess_shared"); err != nil || job != nil {
		t.Fatalf("rejected input left an endless sync job: %+v %v", job, err)
	}
	cloud.mu.Lock()
	cloud.reject = 0
	cloud.mu.Unlock()
	response = call(0, http.MethodPost, "/api/qoder/sessions/sess_shared/messages", map[string]any{"text": "真实待回复"})
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	prior, err := server.DB.GetConversationJob(ctx, "sess_shared")
	if err != nil || prior == nil {
		t.Fatal(err)
	}
	cloud.mu.Lock()
	cloud.reject = 409
	cloud.mu.Unlock()
	response = call(1, http.MethodPost, "/api/qoder/sessions/sess_shared/messages", map[string]any{"text": "另一个成员同时发送"})
	if response.Code != 409 {
		t.Fatal(response.Body.String())
	}
	current, err := server.DB.GetConversationJob(ctx, "sess_shared")
	if err != nil || current == nil || !current.PendingSince.Equal(prior.PendingSince) {
		t.Fatalf("rejection lost previous accepted turn: %+v %v", current, err)
	}
}

func TestStreamSuppressesProtocolAndWaitsForPersistedCompletedEvent(t *testing.T) {
	server, cloud, users, _ := setupConversation(t)
	ctx := auth.ContextWithClaims(context.Background(), &auth.Claims{UserID: users[0].ID})
	request := httptest.NewRequest(http.MethodGet, "/stream", nil).WithContext(ctx)
	event := qoder.Event{ID: "evt_not_in_history", Type: "agent.message", ProcessedAt: time.Now().Format(time.RFC3339Nano), Content: []qoder.ContentBlock{{Type: "text", Text: "```tietie\n{\"text\":\"尚未确认\",\"recipientIds\":[1],\"source\":\"chat\",\"actions\":[]}\n```"}}}
	delta := `{"type":"event_delta","event_id":"evt_not_in_history","delta":{"type":"content_delta","content":{"type":"text","text":"secret raw protocol"}}}`
	raw, _ := json.Marshal(event)
	response := httptest.NewRecorder()
	server.relayConversationStream(request, response, response, strings.NewReader("data: "+delta+"\n\ndata: "+string(raw)+"\n\n"), "sess_shared")
	if strings.Contains(response.Body.String(), "raw protocol") || strings.Contains(response.Body.String(), "尚未确认") {
		t.Fatalf("unverified SSE content leaked: %s", response.Body.String())
	}
	cloud.mu.Lock()
	cloud.events = append(cloud.events, event)
	cloud.mu.Unlock()
	response = httptest.NewRecorder()
	server.relayConversationStream(request, response, response, strings.NewReader("data: "+string(raw)+"\n\n"), "sess_shared")
	if !strings.Contains(response.Body.String(), "尚未确认") || strings.Contains(response.Body.String(), "```tietie") {
		t.Fatalf("completed event was not safely parsed: %s", response.Body.String())
	}
}

func TestSharedReminderAPIEnforcesRecipientsAndCompletion(t *testing.T) {
	_, _, users, call := setupConversation(t)
	path := "/api/qoder/sessions/sess_shared/reminders"
	input := map[string]any{"title": "吃饭", "dueAt": time.Now().Add(time.Hour).Format(time.RFC3339), "recipientIds": []int64{users[1].ID}}
	response := call(0, http.MethodPost, path, input)
	if response.Code != 201 {
		t.Fatal(response.Body.String())
	}
	var payload struct {
		Reminder dbop.Reminder `json:"reminder"`
	}
	if json.Unmarshal(response.Body.Bytes(), &payload) != nil || payload.Reminder.CreatedBy != users[0].ID {
		t.Fatal("manual creation identity")
	}
	updatePath := path + "/" + payload.Reminder.ID
	response = call(0, http.MethodPatch, updatePath, map[string]string{"status": "completed"})
	if response.Code != 403 {
		t.Fatalf("non-recipient completed reminder: %s", response.Body.String())
	}
	response = call(1, http.MethodPatch, updatePath, map[string]string{"status": "completed"})
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	response = call(1, http.MethodPatch, updatePath, map[string]string{"status": "scheduled"})
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	response = call(0, http.MethodPatch, updatePath, map[string]string{"status": "cancelled"})
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	input["recipientIds"] = []int64{users[2].ID}
	response = call(0, http.MethodPost, path, input)
	if response.Code != 400 {
		t.Fatalf("outsider recipient accepted: %s", response.Body.String())
	}
	response = call(2, http.MethodGet, path, nil)
	if response.Code != 403 {
		t.Fatal("outsider reminder access")
	}
}

// Caller holds cloud.mu. Simulates the preconfigured cloud assistant keeping
// its natural response style instead of returning an application action block.
func (cloud *fakeConversation) appendPlainReply(text string) {
	cloud.events = append(cloud.events, qoder.Event{ID: fmt.Sprintf("evt_plain_%d", len(cloud.events)), Type: "agent.message", ProcessedAt: time.Now().Format(time.RFC3339Nano), Content: []qoder.ContentBlock{{Type: "text", Text: text}}})
	cloud.events = append(cloud.events, qoder.Event{ID: fmt.Sprintf("evt_idle_%d", len(cloud.events)), Type: "session.status_idle", ProcessedAt: time.Now().Format(time.RFC3339Nano)})
	cloud.status = "idle"
}
func TestScreenshotOneMinuteRequestPersistsDespitePlainCloudReply(t *testing.T) {
	s, cloud, users, call := setupConversation(t)
	ctx := context.Background()
	path := "/api/qoder/sessions/sess_shared"
	response := call(0, http.MethodPost, path+"/messages", map[string]any{"text": "1分钟后提醒我，去看视频"})
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	var sent struct {
		Messages []qoder.PublicMessage `json:"messages"`
	}
	json.Unmarshal(response.Body.Bytes(), &sent)
	if len(sent.Messages) != 1 || len(sent.Messages[0].ReminderIDs) != 1 {
		t.Fatalf("missing immediate receipt: %s", response.Body.String())
	}
	cloud.mu.Lock()
	input, ok := conversation.DecodeInput(eventText(cloud.events[0]))
	cloud.appendPlainReply("好嘞，1分钟后提醒你去看视频，倒计时开始啦～")
	cloud.mu.Unlock()
	if !ok {
		t.Fatal("input missing")
	}
	reminders, _ := s.DB.ListReminders(ctx, "sess_shared")
	if len(reminders) != 1 || reminders[0].Title != "去看视频" || reminders[0].RecipientIDs[0] != users[0].ID || reminders[0].DueAt.Sub(input.Context.Now) != time.Minute {
		t.Fatalf("missing minute task: %+v", reminders)
	}
	if err := s.syncPendingConversation(ctx, "sess_shared"); err != nil {
		t.Fatal(err)
	}
	due := reminders[0].DueAt
	if early, err := s.DB.ClaimDueReminder(ctx, due.Add(-time.Nanosecond)); err != nil || early != nil {
		t.Fatalf("early task %+v %v", early, err)
	}
	claim, err := s.DB.ClaimDueReminder(ctx, due)
	if err != nil || claim == nil {
		t.Fatalf("due task missing %+v %v", claim, err)
	}
	if err := s.dispatchReminder(ctx, *claim); err != nil {
		t.Fatal(err)
	}
	cloud.mu.Lock()
	cloud.appendPlainReply("到点啦，去看视频吧～")
	cloud.mu.Unlock()
	if err := s.syncPendingConversation(ctx, "sess_shared"); err != nil {
		t.Fatal(err)
	}
	current, _ := s.DB.GetReminder(ctx, "sess_shared", claim.ID)
	if current.Status != dbop.ReminderDelivered || current.TaskStatus != "completed" || current.TaskCompletedAt == nil {
		t.Fatal(current)
	}
	memory, _ := s.DB.ListReminderMemories(ctx, "sess_shared")
	if len(memory) != 1 || memory[0].Status != dbop.ReminderDelivered || memory[0].DeliveredAt == nil {
		t.Fatal(memory)
	}
	space, _, err := s.conversationContext(ctx, "sess_shared", users[1].ID)
	if err != nil || len(space.Memories) != 1 || space.Memories[0].TaskStatus != "completed" {
		t.Fatalf("memory not forwarded to AI %+v %v", space, err)
	}
	for i := 0; i < 2; i++ {
		response = call(i, http.MethodGet, path+"/messages", nil)
		if response.Code != 200 {
			t.Fatal(response.Body.String())
		}
	}
	reminders, _ = s.DB.ListReminders(ctx, "sess_shared")
	if len(reminders) != 1 {
		t.Fatal("duplicated plain reply task")
	}
}
func TestClockWorkerDeliversWithoutBrowser(t *testing.T) {
	s, cloud, _, call := setupConversation(t)
	response := call(0, http.MethodPost, "/api/qoder/sessions/sess_shared/messages", map[string]any{"text": "1秒后提醒我去看视频"})
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	cloud.mu.Lock()
	cloud.appendPlainReply("好，我来安排")
	cloud.autoWake = true
	cloud.mu.Unlock()
	s.Cfg.SchedulerPollInterval = 20 * time.Millisecond
	s.Cfg.SchedulerConcurrency = 2
	s.Cfg.SchedulerBatchSize = 4
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); s.RunConversationWorker(ctx) }()
	defer func() { cancel(); <-done }()
	for {
		rows, err := s.DB.ListReminders(ctx, "sess_shared")
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) == 1 && rows[0].TaskStatus == "completed" {
			memories, err := s.DB.ListReminderMemories(ctx, "sess_shared")
			if err != nil || len(memories) != 1 || memories[0].DeliveredAt == nil {
				t.Fatalf("memory %+v %v", memories, err)
			}
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("worker did not deliver without browser: %+v", rows)
		case <-time.After(20 * time.Millisecond):
		}
	}
}
func TestComplexPlainPromiseShowsMissingPersistenceWarning(t *testing.T) {
	_, cloud, _, call := setupConversation(t)
	path := "/api/qoder/sessions/sess_shared/messages"
	response := call(0, http.MethodPost, path, map[string]any{"text": "明天早上九点提醒我看视频"})
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	cloud.mu.Lock()
	cloud.appendPlainReply("已经设置好了，我会提醒你的")
	cloud.mu.Unlock()
	response = call(0, http.MethodGet, path, nil)
	if !strings.Contains(response.Body.String(), "后台没有保存这条提醒") {
		t.Fatal(response.Body.String())
	}
}
