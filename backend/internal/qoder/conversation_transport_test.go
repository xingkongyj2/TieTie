package qoder

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tietie/backend/internal/conversation"
)

func transportContext() conversation.Context {
	return conversation.Context{
		SessionID: "sess_pair", AuthorID: 22,
		Members: []conversation.Member{{ID: 11, Name: "阿白"}, {ID: 22, Name: "阿青"}},
		Now:     time.Date(2026, 10, 1, 2, 0, 0, 987654321, time.UTC),
	}
}

func testClient(server *httptest.Server) *Client {
	return &Client{BaseURL: server.URL, Token: "test-token", Timeout: time.Second, UploadTimeout: time.Second, HC: server.Client()}
}

func TestSendSharedMessagePreservesIdentityAndAttachments(t *testing.T) {
	ctx := transportContext()
	original := "今天有点累，只想和你聊聊。\n别把这句话自动变成提醒。"
	var sent []Event
	uploaded := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("test request missing upstream authorization")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/files":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("multipart request: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			file, header, err := r.FormFile("file")
			if err != nil {
				t.Errorf("file part: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			defer file.Close()
			content, _ := io.ReadAll(file)
			if header.Filename != "便签.txt" || string(content) != "共享的便签" {
				t.Errorf("attachment content changed: %q / %q", header.Filename, content)
			}
			uploaded = true
			_, _ = io.WriteString(w, `{"id":"file_note"}`)
		case "/sessions/sess_pair/resources":
			var payload struct {
				Type   string `json:"type"`
				FileID string `json:"file_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload.Type != "file" || payload.FileID != "file_note" {
				t.Errorf("bad mount body: %+v / %v", payload, err)
			}
			_, _ = io.WriteString(w, `{"mount_path":"/mnt/session/uploads/note.txt"}`)
		case "/sessions/sess_pair/events":
			var payload struct {
				Events []Event `json:"events"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || len(payload.Events) != 1 {
				t.Errorf("bad event body: %+v / %v", payload, err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			sent = payload.Events
			event := payload.Events[0]
			event.ID, event.ProcessedAt = "evt_sent", ctx.Now.Format(time.RFC3339Nano)
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []Event{event}})
		default:
			t.Errorf("unexpected upstream route: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	result, err := testClient(server).SendMessage(context.Background(), ctx.SessionID, MessageInput{
		Text: conversation.EncodeUser(ctx, original),
		Attachments: []Attachment{
			{Kind: "file", Name: "便签.txt", MimeType: "text/plain", Content: "共享的便签"},
			{Kind: "image", Name: "照片.png", MimeType: "image/png", Data: []byte("test image")},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !uploaded || len(sent) != 1 || sent[0].Type != "user.message" || len(sent[0].Content) != 2 {
		t.Fatalf("message and attachments must share one event: %+v", sent)
	}
	input, ok := conversation.DecodeInput(sent[0].Content[0].Text)
	if !ok || input.UserID != 22 || input.Text != original || input.Hidden || input.Context.Now.Nanosecond() != ctx.Now.Nanosecond() {
		t.Fatalf("transport lost authenticated identity or natural text: %+v", input)
	}
	if !strings.Contains(input.Tail, "便签.txt：/mnt/session/uploads/note.txt") {
		t.Fatalf("mount must be appended outside original user JSON: %q", input.Tail)
	}
	if len(result.Events) != 1 || len(result.Messages) != 1 || result.Messages[0].UserID != 22 || result.Messages[0].Text != original || len(result.Messages[0].Files) != 1 || result.Messages[0].Files[0] != "便签.txt" || len(result.Messages[0].Images) != 1 {
		t.Fatalf("public history must recover original content and identity: %+v", result)
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "TIETIE_INPUT_V1") || strings.Contains(string(data), "/mnt/session/uploads/") {
		t.Fatalf("raw protocol must be internal only: %s", data)
	}
}

func TestToolAnswerCarriesAuthenticatedMemberIdentity(t *testing.T) {
	ctx := transportContext()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Events []Event `json:"events"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || len(payload.Events) != 1 {
			t.Errorf("invalid tool answer: %+v / %v", payload, err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		event := payload.Events[0]
		input, ok := conversation.DecodeInput(event.Content[0].Text)
		if event.Type != "user.custom_tool_result" || event.CustomToolUseID != "evt_question" || !ok || input.UserID != 22 || input.Text != "明天早上九点，提醒我们两人" {
			t.Errorf("tool result lost author attribution: %+v / %+v", event, input)
		}
		event.ID = "evt_answer"
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []Event{event}})
	}))
	defer server.Close()
	result, err := testClient(server).SendCustomToolResult(context.Background(), ctx.SessionID, "evt_question", conversation.EncodeUser(ctx, "明天早上九点，提醒我们两人"))
	if err != nil || len(result.Events) != 1 || len(result.Messages) != 0 {
		t.Fatalf("tool provenance must remain internal: %+v / %v", result, err)
	}
}

func TestFullStreamProtocolAndHiddenWakeup(t *testing.T) {
	ctx := transportContext()
	reminder := conversation.Reminder{ID: "rem_due", Title: "喝水", DueAt: ctx.Now, RecipientIDs: []int64{11, 22}}
	wakeup := Event{ID: "evt_wakeup", Type: "user.message", Content: []ContentBlock{{Type: "text", Text: conversation.EncodeReminder(ctx, reminder)}}}
	data, _ := json.Marshal(wakeup)
	if got := ParseStreamEvent(data); got != nil {
		t.Fatalf("hidden wakeup must have no stream placeholder: %+v", got)
	}
	reply := Event{ID: "evt_reply", Type: "agent.message", Content: []ContentBlock{{Type: "text", Text: "```tietie\n" + `{"text":"阿白、阿青，记得喝水哦","recipientIds":[11,22],"source":"reminder","actions":[]}` + "\n```"}}}
	data, _ = json.Marshal(reply)
	got := ParseStreamEvent(data)
	message, ok := got["message"].(PublicMessage)
	if !ok || message.Text != "阿白、阿青，记得喝水哦" || message.Source != "reminder" || len(message.RecipientIDs) != 2 {
		t.Fatalf("full stream reply must parse protocol once: %+v", got)
	}
	encoded, _ := json.Marshal(got)
	if strings.Contains(string(encoded), "```tietie") || strings.Contains(string(encoded), "actions") {
		t.Fatalf("stream public message leaked raw protocol: %s", encoded)
	}
}

func TestUserLiteralAttachmentMarkersRemainOriginalText(t *testing.T) {
	ctx := transportContext()
	original := "请解释下面这段路径：\n" + uploadMarker + "记录.txt：/mnt/session/uploads/example.txt"
	event := Event{ID: "evt_literal_path", Type: "user.message", Content: []ContentBlock{{Type: "text", Text: conversation.EncodeUser(ctx, original)}}}
	messages := publicMessages([]Event{event})
	if len(messages) != 1 || messages[0].Text != original {
		t.Fatalf("user text was mistaken for transport attachment metadata: %+v", messages)
	}
}

func TestUnicodeDocumentAttachmentsStaySeparateInHistoryAndStream(t *testing.T) {
	ctx := transportContext()
	filename := "副本洪山手术麻醉科9月28号-10月11号排班表(1).xlsx"
	mounts := "\n\n" + uploadMarker + filename + "（已提取为文本）：/mnt/session/uploads/副本洪山手术麻醉科9月28号-10月11号排班表(1).txt\n复核 版本.2.csv：/mnt/session/uploads/复核 版本.2.csv"
	for _, text := range []string{"帮我记住张梦妍排班", ""} {
		event := Event{ID: "evt_unicode_document", Type: "user.message", Content: []ContentBlock{{Type: "text", Text: conversation.EncodeUserV2(ctx, text) + mounts}}}
		messages := publicMessages([]Event{event})
		if len(messages) != 1 || messages[0].Text != text || len(messages[0].Files) != 2 || messages[0].Files[0] != filename || messages[0].Files[1] != "复核 版本.2.csv" {
			t.Fatalf("attachment metadata became user text: %+v", messages)
		}
		encoded, _ := json.Marshal(event)
		stream := ParseStreamEvent(encoded)
		if stream == nil {
			t.Fatal("attachment-only message was dropped from stream")
		}
		message, ok := stream["message"].(PublicMessage)
		if !ok || message.Text != text || len(message.Files) != 2 {
			t.Fatalf("stream differs from history: %+v", stream)
		}
		public, _ := json.Marshal(messages)
		if strings.Contains(string(public), "/mnt/session/uploads/") || strings.Contains(string(public), "已提取为文本") || strings.Contains(string(public), "请按需读取") {
			t.Fatal("transport metadata leaked to public message")
		}
	}
}
