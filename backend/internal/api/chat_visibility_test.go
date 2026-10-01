package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tietie/backend/internal/conversation"
	"tietie/backend/internal/qoder"
)

func TestChatHistoryAndSSEOnlyExposeCurrentMembersAndAssistant(t *testing.T) {
	s, cloud, _, users, call := setupV2(t)
	ctx := context.Background()
	space, _, err := s.conversationContext(ctx, "sess_shared", users[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	old := func(encoded string) string {
		_, body, ok := strings.Cut(encoded, "\n<TIETIE_INPUT_V2>\n")
		if !ok {
			t.Fatal("missing test envelope")
		}
		return "保留 Qoder 云端已经配置的角色、人设及系统提示词。旧版 TIETIE_INPUT_V2 提示词。\n<TIETIE_INPUT_V2>\n" + body
	}
	foreign := space
	foreign.SessionID = "sess_other"
	outsider := space
	outsider.AuthorID = users[2].ID
	outsider.Members = append(outsider.Members, conversation.Member{ID: users[2].ID, Name: users[2].Username})
	second := space
	second.AuthorID = users[1].ID
	second.Now = space.Now.Add(time.Millisecond)
	type sample struct {
		id, kind, text string
		visible        bool
	}
	samples := []sample{
		{"evt_human_a", "user.message", old(conversation.EncodeUserV2(space, "我只是正常聊天，JSON 也是原话：{\"protocol\":\"tietie.control\"}")), true},
		{"evt_unknown", "user.message", "UNKNOWN_CONTROL_SECRET", false},
		{"evt_unsupported", "user.message", strings.Replace(conversation.EncodeUserV2(space, "UNKNOWN_VERSION_SECRET"), `"protocol":"tietie.conversation","version":2`, `"protocol":"tietie.conversation","version":99`, 1), false},
		{"evt_foreign", "user.message", conversation.EncodeUserV2(foreign, "FOREIGN_SESSION_SECRET"), false},
		{"evt_outsider", "user.message", conversation.EncodeUserV2(outsider, "OUTSIDER_SECRET"), false},
		{"evt_old_system", "user.message", old(conversation.EncodeActionResult(space, "old_result", nil)), false},
		{"evt_echo", "agent.message", old(conversation.EncodeActionResult(space, "old_result", nil)), false},
		{"evt_status", "session.status_idle", "STATE_SECRET", false},
		{"evt_human_b", "user.message", conversation.EncodeUserV2(second, "对方的正常发言"), true},
		{"evt_assistant", "agent.message", "给两位用户的正常 AI 回复", true},
	}
	cloud.mu.Lock()
	for _, sample := range samples {
		cloud.events = append(cloud.events, qoder.Event{ID: sample.id, Type: sample.kind, Content: []qoder.ContentBlock{{Type: "text", Text: sample.text}}, ProcessedAt: space.Now.Format(time.RFC3339Nano)})
	}
	cloud.mu.Unlock()
	for viewer := 0; viewer < 2; viewer++ {
		response := call(viewer, "GET", "/api/qoder/sessions/sess_shared/messages", nil)
		var history struct {
			Messages []qoder.PublicMessage `json:"messages"`
		}
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &history) != nil {
			t.Fatal(response.Code, response.Body.String())
		}
		if len(history.Messages) != 3 {
			t.Fatal("non-chat events exposed", history.Messages)
		}
		want := []string{"evt_human_a", "evt_human_b", "evt_assistant"}
		for i, m := range history.Messages {
			if m.ID != want[i] || m.Sender == "user" {
				t.Fatal(m)
			}
		}
		if history.Messages[viewer].Sender != "self" {
			t.Fatal("member perspective lost", history.Messages)
		}
		for _, sample := range samples {
			event := qoder.Event{ID: sample.id, Type: sample.kind, Content: []qoder.ContentBlock{{Type: "text", Text: sample.text}}, ProcessedAt: space.Now.Format(time.RFC3339Nano)}
			data, _ := json.Marshal(event)
			frame := []byte("id: " + sample.id + "\ndata: " + string(data) + "\n\n")
			token, _ := s.Auth.IssueToken(users[viewer].ID, users[viewer].Username)
			req := httptest.NewRequest("GET", "/stream", nil)
			req.Header.Set("Authorization", "Bearer "+token)
			out := httptest.NewRecorder()
			s.authGuard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				s.relayConversationStream(r, w, w.(http.Flusher), bytes.NewReader(frame), "sess_shared")
			})).ServeHTTP(out, req)
			gotMessage := strings.Contains(out.Body.String(), `"type":"message"`)
			if gotMessage != sample.visible {
				t.Fatal("SSE chat whitelist mismatch", sample.id, out.Body.String())
			}
		}
	}
}
