package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"tietie/backend/internal/config"
	"tietie/backend/internal/conversation"

	"tietie/backend/internal/qoder"
	"time"
)

type isolatedTestCloud struct {
	shared, private *fakeConversation
	memory          *fakeMemoryAPI
	creates         int
}

func (c *isolatedTestCloud) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/memory_stores") {
		c.memory.ServeHTTP(w, r)
		return
	}
	if r.URL.Path == "/sessions" && r.Method == "POST" {
		c.creates++
		json.NewEncoder(w).Encode(map[string]any{"id": "sess_private", "status": "idle"})
		return
	}
	branch := c.shared
	if strings.Contains(r.URL.Path, "/sess_private") {
		branch = c.private
		query := r.URL.Query()
		query.Set("after_id", strings.ReplaceAll(query.Get("after_id"), "evt_private_", "evt_"))
		r.URL.RawQuery = query.Encode()
	}
	response := httptest.NewRecorder()
	branch.ServeHTTP(response, r)
	body := response.Body.String()
	if branch == c.private {
		body = strings.ReplaceAll(body, "sess_shared", "sess_private")
		body = strings.ReplaceAll(body, "evt_", "evt_private_")
	}
	for k, v := range response.Header() {
		w.Header()[k] = v
	}
	w.WriteHeader(response.Code)
	fmt.Fprint(w, body)
}

func TestPrivateReminderIsIsolatedUntilDue(t *testing.T) {
	s, shared, m, users, call := setupV2(t)
	private := &fakeConversation{status: "idle"}
	fixture := &isolatedTestCloud{shared: shared, private: private, memory: m}
	upstream := httptest.NewServer(fixture)
	defer upstream.Close()
	cfg := config.Config{Upstream: upstream.URL, Token: "test", Timeout: time.Second, AgentID: "agent_test", EnvironmentID: "env_test", ConversationProtocolVersion: 2, CloudMemoryEnabled: true}
	s.Cfg = &cfg
	s.Qoder = qoder.NewClient(cfg)
	ctx := context.Background()
	path := "/api/qoder/sessions/sess_shared/messages"
	secret := "惊喜暗号：不要让对方知道是我安排的。一分钟后提醒对方喝水。"
	sent := call(0, "POST", path, map[string]any{"text": secret, "visibility": "private"})
	if sent.Code != 200 || !strings.Contains(sent.Body.String(), `"visibility":"private"`) {
		t.Fatal(sent.Code, sent.Body.String())
	}
	m.mu.Lock()
	if m.store.Name != qoder.MemoryStoreName("sess_private") || m.store.Metadata["tietie_owner"] != fmt.Sprintf("private:sess_shared:%d", users[0].ID) {
		t.Error("private store must identify its session and preserve ownership")
	}
	m.mu.Unlock()
	input := latestInput(t, private)
	if input.Context.SessionID != "sess_private" || input.Context.Visibility != "private" || input.UserID != users[0].ID {
		t.Fatal(input)
	}
	shared.mu.Lock()
	sharedCount := len(shared.events)
	shared.mu.Unlock()
	if sharedCount != 0 {
		t.Fatal("private original entered shared cloud history")
	}
	due := input.Context.Now.Add(time.Minute)
	appendProtocolReply(private, map[string]any{"protocol": "tietie.control", "version": 2, "requestId": input.RequestID, "actions": []conversation.Action{{Type: "create_reminder", Key: "water", Title: "喝水", DueAt: due.Format(time.RFC3339Nano), RecipientIDs: []int64{users[1].ID}, Storage: "database_and_memory"}}})
	if err := s.syncPendingConversation(ctx, "sess_private"); err != nil {
		t.Fatal(err)
	}
	runClaimedControl(t, s)
	ack := latestInput(t, private)
	if ack.Kind != "action_result" || ack.Results[0].Status != "succeeded" {
		t.Fatal(ack)
	}
	appendProtocolReply(private, map[string]any{"protocol": "tietie.message", "version": 2, "requestId": input.RequestID, "text": "我已记好，这个惊喜确认只有你能看到。", "recipientIds": []int64{users[0].ID}, "source": "chat"})
	if err := s.syncPendingConversation(ctx, "sess_private"); err != nil {
		t.Fatal(err)
	}
	rows, err := s.DB.ListReminders(ctx, "sess_private")
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	r := rows[0]
	if r.DeliverySession() != "sess_shared" || r.MemorySession() != "sess_private" {
		t.Fatal(r)
	}
	publicMemory, err := s.DB.MemoryIndex(ctx, "sess_shared")
	if err != nil || len(publicMemory) != 0 {
		t.Fatal("private memory entered shared index", publicMemory, err)
	}
	own := call(0, "GET", path, nil)
	other := call(1, "GET", path, nil)
	if own.Code != 200 || !strings.Contains(own.Body.String(), "惊喜暗号") || !strings.Contains(own.Body.String(), "这个惊喜确认") {
		t.Fatal("owner lost private history", own.Code, own.Body.String())
	}
	if other.Code != 200 || strings.Contains(other.Body.String(), "惊喜") || strings.Contains(other.Body.String(), r.ID) {
		t.Fatal("other member read private content", other.Code, other.Body.String())
	}
	board := call(1, "GET", "/api/qoder/sessions/sess_shared/reminders", nil)
	if strings.Contains(board.Body.String(), r.ID) {
		t.Fatal("private plan leaked through reminder board")
	}
	if rejected := call(1, "PATCH", "/api/qoder/sessions/sess_shared/reminders/"+r.ID, map[string]any{"status": "cancelled"}); rejected.Code != 404 {
		t.Fatal("guessed task ID gave access", rejected.Code)
	}
	for user := 0; user < 2; user++ {
		if denied := call(user, "GET", "/api/qoder/sessions/sess_private/messages", nil); denied.Code != 403 {
			t.Fatal("internal channel URL exposed", denied.Code)
		}
	}
	if noPrivate := call(1, "GET", "/api/qoder/sessions/sess_shared/private-stream", nil); noPrivate.Code != 404 {
		t.Fatal("other member attached owner's private stream", noPrivate.Code)
	}
	claim, err := s.DB.ClaimDueReminder(ctx, due)
	if err != nil || claim == nil {
		t.Fatal(claim, err)
	}
	if err := s.dispatchReminder(ctx, *claim); err != nil {
		t.Fatal(err)
	}
	wake := latestInput(t, shared)
	if wake.Kind != "reminder_due" || wake.Reminder.Title != "喝水" || strings.Contains(wake.Text, secret) {
		t.Fatal(wake)
	}
	shared.mu.Lock()
	raw := eventText(shared.events[len(shared.events)-1])
	shared.mu.Unlock()
	if strings.Contains(raw, "惊喜暗号") || strings.Contains(raw, "这个惊喜确认") {
		t.Fatal("private original/confirmation leaked in due event")
	}
	appendProtocolReply(shared, map[string]any{"protocol": "tietie.message", "version": 2, "requestId": wake.RequestID, "text": "该喝水啦。", "recipientIds": []int64{users[1].ID}, "source": "reminder"})
	if err := s.syncPendingConversation(ctx, "sess_shared"); err != nil {
		t.Fatal(err)
	}
	other = call(1, "GET", path, nil)
	if other.Code != 200 || !strings.Contains(other.Body.String(), "该喝水啦") || strings.Contains(other.Body.String(), "惊喜") {
		t.Fatal("due delivery failed or leaked original", other.Code, other.Body.String())
	}
	rows, _ = s.DB.ListReminders(ctx, "sess_shared")
	if len(rows) != 1 || rows[0].TaskStatus != "completed" {
		t.Fatal("completed reminder not published", rows)
	}
	record, err := s.DB.GetReminderMemory(ctx, r)
	if err != nil || record == nil {
		t.Fatal("private memory lost after publish", err)
	}
	// Replaying the private control after the row moved must not duplicate a timer.
	if err := s.syncPendingConversation(ctx, "sess_private"); err != nil {
		t.Fatal(err)
	}
	own = call(0, "GET", path, nil)
	if own.Code != 200 {
		t.Fatal(own.Code)
	}
	if fixture.creates != 1 {
		t.Fatal("private channel was recreated", fixture.creates)
	}
}

func TestMessageVisibilityRejectsForgedValues(t *testing.T) {
	for _, value := range []string{`"other"`, `null`, `101`, `["private"]`} {
		r := httptest.NewRequest("POST", "/messages", strings.NewReader(`{"text":"秘密","visibility":`+value+`}`))
		r.Header.Set("Content-Type", "application/json")
		if _, err := parseMessage(r); err == nil {
			t.Fatal("invalid visibility accepted", value)
		}
	}
}

func TestPrivateSelfReminderAndFactsStayPrivateAfterDelivery(t *testing.T) {
	s, shared, m, users, call := setupV2(t)
	private := &fakeConversation{status: "idle"}
	fixture := &isolatedTestCloud{shared: shared, private: private, memory: m}
	upstream := httptest.NewServer(fixture)
	defer upstream.Close()
	cfg := config.Config{Upstream: upstream.URL, Token: "test", Timeout: time.Second, AgentID: "agent_test", EnvironmentID: "env_test", ConversationProtocolVersion: 2, CloudMemoryEnabled: true}
	s.Cfg = &cfg
	s.Qoder = qoder.NewClient(cfg)
	ctx := context.Background()
	path := "/api/qoder/sessions/sess_shared/messages"
	if res := call(0, "POST", path, map[string]any{"text": "我的私密偏好是茉莉茶，一分钟后只提醒我喝茶", "visibility": "private"}); res.Code != 200 {
		t.Fatal(res.Body.String())
	}
	input := latestInput(t, private)
	due := input.Context.Now.Add(time.Minute)
	appendProtocolReply(private, map[string]any{"protocol": "tietie.control", "version": 2, "requestId": input.RequestID, "actions": []conversation.Action{
		{Type: "create_reminder", Key: "tea", Title: "喝茉莉茶", DueAt: due.Format(time.RFC3339Nano), RecipientIDs: []int64{users[0].ID}, Storage: "database_and_memory"},
		{Type: "save_memory", Key: "tea_preference", Content: "私密偏好是茉莉茶", Scope: "self", Storage: "memory_only"},
	}})
	if err := s.syncPendingConversation(ctx, "sess_private"); err != nil {
		t.Fatal(err)
	}
	runClaimedControl(t, s)
	ack := latestInput(t, private)
	for _, result := range ack.Results {
		if result.Status != "succeeded" {
			t.Fatal(result)
		}
	}
	appendProtocolReply(private, map[string]any{"protocol": "tietie.message", "version": 2, "requestId": input.RequestID, "text": "已记好你的偏好和提醒。", "recipientIds": []int64{users[0].ID}, "source": "chat"})
	if err := s.syncPendingConversation(ctx, "sess_private"); err != nil {
		t.Fatal(err)
	}
	// A recreated Server has no in-memory routing state. Claims still recover
	// the persisted private conversation and its binding epoch.
	s = &Server{Cfg: s.Cfg, Qoder: s.Qoder, DB: s.DB, Auth: s.Auth}
	claim, err := s.DB.ClaimDueReminder(ctx, due)
	if err != nil || claim == nil || claim.DeliverySession() != "sess_private" {
		t.Fatal(claim, err)
	}
	if err := s.dispatchReminder(ctx, *claim); err != nil {
		t.Fatal(err)
	}
	wake := latestInput(t, private)
	appendProtocolReply(private, map[string]any{"protocol": "tietie.message", "version": 2, "requestId": wake.RequestID, "text": "该喝茉莉茶啦。", "recipientIds": []int64{users[0].ID}, "source": "reminder"})
	if err := s.syncPendingConversation(ctx, "sess_private"); err != nil {
		t.Fatal(err)
	}
	rows, _ := s.DB.ListReminders(ctx, "sess_private")
	if len(rows) != 1 || rows[0].TaskStatus != "completed" || rows[0].Visibility != "private" {
		t.Fatal(rows)
	}
	other := call(1, "GET", path, nil)
	if other.Code != 200 || strings.Contains(other.Body.String(), "茉莉") || strings.Contains(other.Body.String(), rows[0].ID) {
		t.Fatal("self reminder leaked", other.Body.String())
	}
	shared.mu.Lock()
	count := len(shared.events)
	shared.mu.Unlock()
	if count != 0 {
		t.Fatal("private-only conversation entered shared cloud")
	}
	index, err := s.DB.MemoryIndex(ctx, "sess_shared")
	if err != nil || len(index) != 0 {
		t.Fatal("private fact in shared index", index, err)
	}
	if _, err := s.DB.Unbind(ctx, users[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := s.ensureConversationViewer(ctx, "sess_private"); err == nil {
		t.Fatal("old private branch accessible after unbind")
	}
}

func TestPrivateSSERequiresTheOwner(t *testing.T) {
	s, shared, m, users, _ := setupV2(t)
	private := &fakeConversation{status: "idle"}
	fixture := &isolatedTestCloud{shared: shared, private: private, memory: m}
	upstream := httptest.NewServer(fixture)
	defer upstream.Close()
	cfg := config.Config{Upstream: upstream.URL, Token: "test", Timeout: time.Second, AgentID: "agent_test", EnvironmentID: "env_test", ConversationProtocolVersion: 2, CloudMemoryEnabled: true}
	s.Cfg = &cfg
	s.Qoder = qoder.NewClient(cfg)
	channel, err := s.ensurePrivateChannel(context.Background(), "sess_shared", users[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	space, _, err := s.conversationContext(context.Background(), channel.SessionID, users[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(qoder.Event{ID: "evt_private_user", Type: "user.message", Content: []qoder.ContentBlock{{Type: "text", Text: conversation.EncodeUserV2(space, "私密流正文")}}, ProcessedAt: time.Now().Format(time.RFC3339Nano)})
	frame := []byte("id: evt_private_user\ndata: " + string(data) + "\n\n")
	for user := 0; user < 2; user++ {
		token, _ := s.Auth.IssueToken(users[user].ID, users[user].Username)
		r := httptest.NewRequest("GET", "/stream", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		out := httptest.NewRecorder()
		s.authGuard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s.relayConversationStream(r, w, w.(http.Flusher), bytes.NewReader(frame), channel.SessionID)
		})).ServeHTTP(out, r)
		hasSecret := strings.Contains(out.Body.String(), "私密流正文")
		if hasSecret != (user == 0) {
			t.Fatal("private SSE visibility", user, out.Body.String())
		}
	}
}
