package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"tietie/backend/internal/conversation"
	"tietie/backend/internal/dbop"
	"tietie/backend/internal/qoder"
	"time"
)

type fakeMemoryAPI struct {
	mu      sync.Mutex
	store   *qoder.MemoryStore
	entries map[string]qoder.MemoryEntry
	fail    bool
	updates int
}

func (m *fakeMemoryAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if m.fail {
		w.WriteHeader(503)
		return
	}
	if r.URL.Path == "/memory_stores" {
		if r.Method == "GET" {
			rows := []qoder.MemoryStore{}
			if m.store != nil {
				rows = append(rows, *m.store)
			}
			json.NewEncoder(w).Encode(map[string]any{"data": rows, "has_more": false})
			return
		}
		var body qoder.MemoryStore
		json.NewDecoder(r.Body).Decode(&body)
		body.ID = "memstore_test"
		body.Status = "active"
		m.store = &body
		json.NewEncoder(w).Encode(body)
		return
	}
	if m.store != nil && r.URL.Path == "/memory_stores/"+m.store.ID && r.Method == "POST" {
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if len(body) != 1 || body["name"] == "" {
			w.WriteHeader(400)
			return
		}
		m.store.Name = body["name"]
		json.NewEncoder(w).Encode(m.store)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/memories") {
		if r.Method == "GET" {
			rows := []qoder.MemoryEntry{}
			for _, e := range m.entries {
				if strings.HasPrefix(e.Path, r.URL.Query().Get("path_prefix")) {
					e.Content = ""
					rows = append(rows, e)
				}
			}
			json.NewEncoder(w).Encode(map[string]any{"data": rows, "has_more": false})
			return
		}
		var entry qoder.MemoryEntry
		json.NewDecoder(r.Body).Decode(&entry)
		for _, e := range m.entries {
			if e.Path == entry.Path {
				w.WriteHeader(409)
				return
			}
		}
		entry.ID = fmt.Sprintf("mem_%d", len(m.entries)+1)
		entry.Version = 1
		sum := sha256.Sum256([]byte(entry.Content))
		entry.SHA256 = hex.EncodeToString(sum[:])
		m.entries[entry.ID] = entry
		m.updates++
		json.NewEncoder(w).Encode(entry)
		return
	}
	parts := strings.Split(r.URL.Path, "/")
	id := parts[len(parts)-1]
	entry, ok := m.entries[id]
	if !ok {
		w.WriteHeader(404)
		return
	}
	switch r.Method {
	case "GET":
		json.NewEncoder(w).Encode(entry)
	case "POST":
		var b struct {
			Content string `json:"content"`
			Hash    string `json:"content_sha256"`
		}
		json.NewDecoder(r.Body).Decode(&b)
		if b.Hash != entry.SHA256 {
			w.WriteHeader(409)
			return
		}
		entry.Content = b.Content
		sum := sha256.Sum256([]byte(b.Content))
		entry.SHA256 = hex.EncodeToString(sum[:])
		entry.Version++
		m.entries[id] = entry
		m.updates++
		json.NewEncoder(w).Encode(entry)
	case "DELETE":
		delete(m.entries, id)
		w.WriteHeader(204)
	default:
		w.WriteHeader(405)
	}
}
func setupV2(t *testing.T) (*Server, *fakeConversation, *fakeMemoryAPI, []*dbop.User, func(int, string, string, any) *httptest.ResponseRecorder) {
	s, c, u, call := setupConversation(t)
	s.Cfg.ConversationProtocolVersion = 2
	s.Cfg.CloudMemoryEnabled = true
	memory := &fakeMemoryAPI{entries: map[string]qoder.MemoryEntry{}}
	c.memory = memory
	return s, c, memory, u, call
}
func appendProtocolReply(c *fakeConversation, body any) {
	data, _ := json.Marshal(body)
	c.mu.Lock()
	c.appendPlainReply(string(data))
	c.mu.Unlock()
}
func latestInput(t *testing.T, c *fakeConversation) conversation.Input {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := len(c.events) - 1; i >= 0; i-- {
		if c.events[i].Type == "user.message" {
			input, ok := conversation.DecodeInput(eventText(c.events[i]))
			if !ok {
				t.Fatal("input envelope missing")
			}
			return input
		}
	}
	t.Fatal("no input")
	return conversation.Input{}
}
func runClaimedControl(t *testing.T, s *Server) {
	t.Helper()
	jobs, err := s.DB.ClaimControls(context.Background(), time.Now().Add(time.Second), 4)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("control claim %+v %v", jobs, err)
	}
	if err := s.runControl(context.Background(), jobs[0]); err != nil {
		t.Fatal(err)
	}
}

func TestControlReceiptAddressesRequesterIndependentOfRecipientsAndViewer(t *testing.T) {
	for author := 0; author < 2; author++ {
		for _, target := range []string{"self", "partner", "both"} {
			t.Run(fmt.Sprintf("author-%d/%s", author, target), func(t *testing.T) {
				s, c, _, users, call := setupV2(t)
				ctx := context.Background()
				path := "/api/qoder/sessions/sess_shared/messages"
				out := call(author, "POST", path, map[string]any{"text": "1分钟后提醒我，她今天的排班"})
				if out.Code != 200 {
					t.Fatal(out.Body.String())
				}
				input := latestInput(t, c)
				ids := []int64{users[author].ID}
				if target == "partner" {
					ids = []int64{users[1-author].ID}
				} else if target == "both" {
					ids = []int64{users[0].ID, users[1].ID}
				}
				appendProtocolReply(c, map[string]any{"protocol": "tietie.control", "version": 2, "requestId": input.RequestID, "actions": []conversation.Action{{Type: "create_reminder", Key: "shift", Title: "TA今天是白备夜", DueAt: input.Context.Now.Add(time.Minute).Format(time.RFC3339Nano), RecipientIDs: ids, Storage: "database_and_memory"}}})
				// The other member viewing the shared conversation must not become
				// the addressee of the original member's confirmation.
				if out := call(1-author, "GET", path, nil); out.Code != 200 {
					t.Fatal(out.Body.String())
				}
				runClaimedControl(t, s)
				ack := latestInput(t, c)
				want := conversation.Member{ID: users[author].ID, Name: users[author].Username}
				if !ack.Hidden || ack.UserID != 0 || ack.Kind != "action_result" || ack.RequestID != input.RequestID || ack.ReplyTo == nil || *ack.ReplyTo != want {
					t.Fatalf("confirmation lost original requester: %+v", ack)
				}
				rows, err := s.DB.ListReminders(ctx, "sess_shared")
				if err != nil || len(rows) != 1 || fmt.Sprint(rows[0].RecipientIDs) != fmt.Sprint(ids) {
					t.Fatalf("reminder recipients changed: %+v %v", rows, err)
				}
			})
		}
	}
}

func TestV2ReminderControlAckCloudMemoryAndDueRead(t *testing.T) {
	s, c, m, u, call := setupV2(t)
	ctx := context.Background()
	path := "/api/qoder/sessions/sess_shared/messages"
	response := call(0, "POST", path, map[string]any{"text": "1分钟后提醒我，去看视频"})
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	input := latestInput(t, c)
	if input.Version != 2 || input.UserID != u[0].ID || input.Hidden {
		t.Fatal(input)
	}
	rows, _ := s.DB.ListReminders(ctx, "sess_shared")
	if len(rows) != 0 {
		t.Fatal("backend bypassed AI judgement")
	}
	due := input.Context.Now.Add(time.Minute)
	appendProtocolReply(c, map[string]any{"protocol": "tietie.control", "version": 2, "requestId": input.RequestID, "actions": []conversation.Action{{Type: "create_reminder", Key: "video", Title: "去看视频", DueAt: due.Format(time.RFC3339Nano), RecipientIDs: []int64{u[0].ID}, Storage: "database_and_memory"}}})
	// Persist the control with no browser polling or live SSE connection.
	if err := s.syncPendingConversation(ctx, "sess_shared"); err != nil {
		t.Fatal(err)
	}
	response = call(0, "GET", path, nil)
	if strings.Contains(response.Body.String(), "tietie.control") {
		t.Fatal("control leaked")
	}
	runClaimedControl(t, s)
	rows, _ = s.DB.ListReminders(ctx, "sess_shared")
	if len(rows) != 1 {
		t.Fatal(rows)
	}
	ack := latestInput(t, c)
	if !ack.Hidden || ack.Kind != "action_result" || ack.RequestID != input.RequestID || len(ack.Results) != 1 || ack.Results[0].Status != "succeeded" {
		t.Fatal(ack)
	}
	m.mu.Lock()
	entryCount := len(m.entries)
	m.mu.Unlock()
	if entryCount != 2 {
		t.Fatal("cloud protocol and reminder memories missing")
	}
	appendProtocolReply(c, map[string]any{"protocol": "tietie.message", "version": 2, "requestId": input.RequestID, "text": "已经保存好啦，一分钟后提醒你去看视频。", "recipientIds": []int64{u[0].ID}, "source": "chat"})
	if err := s.syncPendingConversation(ctx, "sess_shared"); err != nil {
		t.Fatal(err)
	}
	response = call(1, "GET", path, nil)
	if strings.Contains(response.Body.String(), "tietie.control") || strings.Contains(response.Body.String(), "action_result") || !strings.Contains(response.Body.String(), "已经保存好啦") {
		t.Fatal(response.Body.String())
	}
	claim, err := s.DB.ClaimDueReminder(ctx, due)
	if err != nil || claim == nil {
		t.Fatalf("timer %+v %v", claim, err)
	}
	if err := s.dispatchReminder(ctx, *claim); err != nil {
		t.Fatal(err)
	}
	wake := latestInput(t, c)
	if wake.Kind != "reminder_due" || !wake.Hidden {
		t.Fatal(wake)
	}
	c.mu.Lock()
	raw := eventText(c.events[len(c.events)-1])
	c.mu.Unlock()
	if !strings.Contains(raw, `"memoryReads"`) || !strings.Contains(raw, "去看视频") {
		t.Fatal("due event did not read real cloud memory")
	}
	appendProtocolReply(c, map[string]any{"protocol": "tietie.message", "version": 2, "requestId": wake.RequestID, "text": "到时间啦，去看视频吧！", "recipientIds": []int64{u[1].ID}, "source": "reminder"})
	if err := s.syncPendingConversation(ctx, "sess_shared"); err != nil {
		t.Fatal(err)
	}
	saved, _ := s.DB.GetReminder(ctx, "sess_shared", rows[0].ID)
	if saved.TaskStatus != "completed" {
		t.Fatal(saved)
	}
	jobs, _ := s.DB.ClaimMemorySync(ctx, time.Now().Add(time.Minute), 10)
	for _, job := range jobs {
		if err := s.runMemorySync(ctx, job); err != nil {
			t.Fatal(err)
		}
	}
	m.mu.Lock()
	archived := false
	for _, entry := range m.entries {
		if !strings.HasPrefix(entry.Path, "tasks/todo-board/") || entry.Path == dbop.ReminderHistoryIndexPath {
			continue
		}
		body, err := dbop.ExtractReminderMemory(entry.Content, saved.ID)
		if err != nil || !strings.Contains(body, `"taskStatus":"completed"`) {
			t.Fatal(entry)
		}
		archived = true
	}
	m.mu.Unlock()
	if !archived {
		t.Fatal("completed reminder missing from grouped cloud history")
	}
	call(0, "GET", path, nil)
	rows, _ = s.DB.ListReminders(ctx, "sess_shared")
	if len(rows) != 1 {
		t.Fatal("replayed control duplicated timer")
	}
}
func TestLegacyMemoryOnlyUsesTemplatePageAndDurableFactsWithoutTimer(t *testing.T) {
	s, c, _, u, call := setupV2(t)
	path := "/api/qoder/sessions/sess_shared/messages"
	call(0, "POST", path, map[string]any{"text": "记住我喜欢无糖茶，不用提醒"})
	input := latestInput(t, c)
	appendProtocolReply(c, map[string]any{"protocol": "tietie.control", "version": 2, "requestId": input.RequestID, "actions": []conversation.Action{{Type: "save_memory", Key: "tea", Content: "喜欢无糖茶", Scope: "self", Storage: "memory_only"}}})
	call(0, "GET", path, nil)
	runClaimedControl(t, s)
	rows, _ := s.DB.ListReminders(context.Background(), "sess_shared")
	if len(rows) != 0 {
		t.Fatal("fact became a timer")
	}
	index, _ := s.DB.MemoryIndex(context.Background(), "sess_shared")
	var facts []dbop.MemoryRecord
	for _, row := range index {
		if row.Kind == "fact" {
			facts = append(facts, row)
		}
	}
	if len(facts) != 1 {
		t.Fatal(index)
	}
	record, _ := s.DB.GetMemoryRecord(context.Background(), facts[0].ID, "sess_shared")
	if record.Content == "" || record.PendingContent != "" || record.State != "synced" || record.OwnerID != u[0].ID {
		t.Fatal(record)
	}
	ack := latestInput(t, c)
	if ack.Results[0].DatabaseStatus != "saved" || ack.Results[0].MemoryStatus != "synced" {
		t.Fatal(ack)
	}
}
func TestMemoryFailureKeepsOutboxAndReportsPartialSuccess(t *testing.T) {
	s, c, m, u, call := setupV2(t)
	path := "/api/qoder/sessions/sess_shared/messages"
	call(0, "POST", path, map[string]any{"text": "一分钟后提醒我们喝水"})
	input := latestInput(t, c)
	appendProtocolReply(c, map[string]any{"protocol": "tietie.control", "version": 2, "requestId": input.RequestID, "actions": []conversation.Action{{Type: "create_reminder", Key: "water", Title: "喝水", DueAt: input.Context.Now.Add(time.Minute).Format(time.RFC3339Nano), RecipientIDs: []int64{u[0].ID, u[1].ID}, Storage: "database_and_memory"}}})
	call(0, "GET", path, nil)
	m.mu.Lock()
	m.fail = true
	m.mu.Unlock()
	runClaimedControl(t, s)
	ack := latestInput(t, c)
	if ack.Results[0].Status != "partial" || ack.Results[0].DatabaseStatus != "saved" || ack.Results[0].MemoryStatus != "pending" {
		t.Fatal(ack)
	}
	rows, _ := s.DB.ListReminders(context.Background(), "sess_shared")
	if len(rows) != 1 {
		t.Fatal(rows)
	}
	m.mu.Lock()
	m.fail = false
	m.mu.Unlock()
	jobs, err := s.DB.ClaimMemorySync(context.Background(), time.Now().Add(time.Minute), 5)
	if err != nil || len(jobs) != 2 {
		t.Fatalf("durable retry %+v %v", jobs, err)
	}
	for _, job := range jobs {
		if err := s.runMemorySync(context.Background(), job); err != nil {
			t.Fatal(err)
		}
		record, _ := s.DB.GetMemoryRecord(context.Background(), job.ID, "sess_shared")
		if record.State != "synced" {
			t.Fatal(record)
		}
	}
}

func TestHabitCorrectionPreservesIdentityAndCannotEditPartnerOrTemplates(t *testing.T) {
	s, _, _, u, _ := setupV2(t)
	ctx := context.Background()
	space, binding, err := s.conversationContext(ctx, "sess_shared", u[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	input := conversation.Input{UserID: u[0].ID, Context: space}
	job := dbop.ControlJob{ID: "control_initial", SessionID: "sess_shared", RequestID: "request_initial", BindingCreatedAt: binding.CreatedAt}
	original := s.executeAction(ctx, job, input, 0, conversation.Action{Type: "save_memory", Key: "sleep", Category: "habit", Content: "通常23点睡", Data: map[string]string{"schedule": "23:00"}, Scope: "self", Storage: "memory_only"}, space)
	if original.Status != "succeeded" {
		t.Fatal(original)
	}
	job.ID = "control_update"
	job.RequestID = "request_update"
	corrected := s.executeAction(ctx, job, input, 0, conversation.Action{Type: "save_memory", Key: "change_sleep", MemoryKey: original.MemoryKey, Category: "habit", Content: "改为22点睡", Data: map[string]string{"schedule": "22:00"}, Scope: "self", Storage: "memory_only"}, space)
	if corrected.Status != "succeeded" || corrected.MemoryKey != original.MemoryKey {
		t.Fatal(corrected)
	}
	record, _ := s.DB.GetMemoryRecord(ctx, original.MemoryKey, "sess_shared")
	if record.Revision != 2 || record.Content == "" || record.PendingContent != "" || record.SourceUserID != u[0].ID {
		t.Fatal(record)
	}
	read := s.executeAction(ctx, job, input, 0, conversation.Action{Type: "read_memory", Key: "recall", MemoryKeys: []string{record.ID}}, space)
	if read.Status != "succeeded" || !strings.Contains(read.Memories[0].Content, `"schedule":"22:00"`) {
		t.Fatal(read)
	}
	job.ID = "partner_update"
	input.UserID = u[1].ID
	rejected := s.executeAction(ctx, job, input, 0, conversation.Action{Type: "save_memory", Key: "change", MemoryKey: record.ID, Category: "habit", Content: "修改对方", Scope: "self", Storage: "memory_only"}, space)
	if rejected.Status != "failed" {
		t.Fatal("partner memory edit allowed")
	}
	template := dbop.MemoryRecord{Kind: "template", SessionID: "sess_shared", Path: "profile/users.json", Scope: "space", Storage: "database_and_memory", PendingContent: `{"users":[]}`, Operation: "upsert", BindingCreatedAt: binding.CreatedAt}
	if err := s.DB.QueueMemory(ctx, template); err != nil {
		t.Fatal(err)
	}
	rejected = s.executeAction(ctx, job, input, 0, conversation.Action{Type: "delete_memory", Key: "delete_template", MemoryKey: dbop.MemoryID("sess_shared", template.Path)}, space)
	if rejected.Status != "failed" {
		t.Fatal("template deletion allowed")
	}
}

func TestRealtimeExpiresFromRecallAndCloud(t *testing.T) {
	s, _, m, u, call := setupV2(t)
	ctx := context.Background()
	space, binding, err := s.conversationContext(ctx, "sess_shared", u[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	job := dbop.ControlJob{ID: "realtime_job", SessionID: "sess_shared", RequestID: "realtime_request", BindingCreatedAt: binding.CreatedAt}
	expiry := time.Now().Add(time.Hour)
	input := conversation.Input{UserID: u[0].ID, Context: space}
	saved := s.executeAction(ctx, job, input, 0, conversation.Action{Type: "save_memory", Key: "takeout", Category: "realtime", Content: "取餐码1234", Data: map[string]string{"category": "外卖取餐码", "content": "1234"}, Scope: "space", Storage: "database_and_memory", ExpiresAt: expiry.Format(time.RFC3339)}, space)
	if saved.Status != "succeeded" {
		t.Fatal(saved)
	}
	page := call(0, "GET", "/api/qoder/sessions/sess_shared/memories?category=realtime", nil)
	if page.Code != 200 || !strings.Contains(page.Body.String(), saved.MemoryKey) || strings.Contains(page.Body.String(), "1234") {
		t.Fatal(page.Body.String())
	}
	record, _ := s.DB.GetMemoryRecord(ctx, saved.MemoryKey, "sess_shared")
	past := time.Now().Add(-time.Minute)
	record.ExpiresAt = &past
	record.PendingContent = record.Content
	if err := s.DB.QueueMemory(ctx, *record); err != nil {
		t.Fatal(err)
	}
	page = call(0, "GET", "/api/qoder/sessions/sess_shared/memories?category=realtime", nil)
	if strings.Contains(page.Body.String(), saved.MemoryKey) {
		t.Fatal("expired fact remains visible")
	}
	read := s.executeAction(ctx, job, input, 0, conversation.Action{Type: "read_memory", Key: "recall", MemoryKeys: []string{saved.MemoryKey}}, space)
	if read.Status != "failed" {
		t.Fatal("expired fact was recalled")
	}
	jobs, err := s.DB.ClaimMemorySync(ctx, time.Now(), 10)
	if err != nil || len(jobs) != 2 {
		t.Fatal(jobs, err)
	}
	for _, item := range jobs {
		if err := s.runMemorySync(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	record, _ = s.DB.GetMemoryRecord(ctx, saved.MemoryKey, "sess_shared")
	if record.State != "deleted" || record.Content != "" || record.PendingContent != "" {
		t.Fatal(record)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, entry := range m.entries {
		if strings.Contains(entry.Content, "1234") {
			t.Fatal("expired cloud content remains")
		}
	}
}
