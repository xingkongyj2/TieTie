package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"tietie/backend/internal/conversation"
	"tietie/backend/internal/dbop"
	"tietie/backend/internal/memoryspace"
)

func TestAIAnniversaryControlCreatesRealDateAndManualPin(t *testing.T) {
	s, cloud, memory, users, call := setupV2(t)
	ctx := context.Background()
	path := "/api/qoder/sessions/sess_shared/messages"
	if res := call(0, "POST", path, map[string]string{"text": "帮我记住2025年5月24日第一次一起旅行"}); res.Code != 200 {
		t.Fatal(res.Body.String())
	}
	input := latestInput(t, cloud)
	appendProtocolReply(cloud, map[string]any{"protocol": "tietie.control", "version": 2, "requestId": input.RequestID, "actions": []conversation.Action{{Type: "save_anniversary", Key: "first_trip", Title: "第一次一起旅行", Date: "2025-05-24", AnniversaryKind: "other", Storage: "database_and_memory"}}})
	if err := s.syncPendingConversation(ctx, "sess_shared"); err != nil {
		t.Fatal(err)
	}
	runClaimedControl(t, s)
	ack := latestInput(t, cloud)
	if len(ack.Results) != 1 || ack.Results[0].Status != "succeeded" || ack.Results[0].Anniversary == nil || ack.Results[0].Anniversary.Date != "2025-05-24" {
		t.Fatal("AI saved no usable anniversary", ack.Results)
	}
	rows, _, err := s.DB.ListAnniversaries(ctx, "sess_shared", "", 50)
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	if reminders, err := s.DB.ListReminders(ctx, "sess_shared"); err != nil || len(reminders) != 0 {
		t.Fatal("date-only save created a timed reminder", reminders, err)
	}
	listPath := "/api/qoder/sessions/sess_shared/anniversaries"
	for viewer := 0; viewer < 2; viewer++ {
		res := call(viewer, "GET", listPath, nil)
		var result struct {
			Anniversaries  []dbop.Anniversary `json:"anniversaries"`
			Featured       *dbop.Anniversary  `json:"featured"`
			SpaceCreatedAt time.Time          `json:"spaceCreatedAt"`
		}
		if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &result) != nil || len(result.Anniversaries) != 1 || result.Featured != nil || result.SpaceCreatedAt.IsZero() {
			t.Fatal("list did not use real dates and space creation", res.Body.String())
		}
	}
	if res := call(2, "GET", listPath, nil); res.Code != 403 {
		t.Fatal("outsider read anniversaries", res.Code)
	}
	pinPath := listPath + "/" + rows[0].ID
	if res := call(1, "PATCH", pinPath, map[string]any{"pinned": true}); res.Code != 200 {
		t.Fatal(res.Body.String())
	}
	featured, err := s.DB.FeaturedAnniversary(ctx, "sess_shared")
	if err != nil || featured == nil || featured.ID != rows[0].ID {
		t.Fatal(featured, err)
	}
	if res := call(2, "PATCH", pinPath, map[string]any{"pinned": true}); res.Code != 403 {
		t.Fatal("outsider changed homepage pin", res.Code)
	}
	if res := call(0, "PATCH", pinPath, map[string]any{}); res.Code != 400 {
		t.Fatal("missing pin state accepted", res.Code)
	}
	space, _, err := s.conversationContext(ctx, "sess_shared", users[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	text, _, err := s.prepareProtocolInput(ctx, conversation.NewEnvelopeV2(space, "user_message", "after-pin"))
	if err != nil || !strings.Contains(text, `"pinned":true`) || !strings.Contains(text, rows[0].ID) {
		t.Fatal("AI did not receive current board", text, err)
	}
	record, err := s.DB.GetMemoryRecord(ctx, dbop.MemoryID("sess_shared", dbop.AnniversaryMemoryPath(rows[0].ID)), "sess_shared")
	if err != nil || !strings.Contains(record.Content, `"pinned":true`) {
		t.Fatal("pin was not saved to memory outbox", record, err)
	}
	if err := s.syncMemoryLocked(ctx, *record); err != nil {
		t.Fatal(err)
	}
	memory.mu.Lock()
	defer memory.mu.Unlock()
	for _, entry := range memory.entries {
		if !memoryspace.IsDocumentPath(entry.Path) {
			t.Fatal("new anniversary file type", entry.Path)
		}
	}
}

func TestAnniversaryDeletionRemovesDatabaseBeforeCloudAndSupportsLegacyAction(t *testing.T) {
	for _, actionType := range []string{"delete_anniversary", "delete_memory"} {
		t.Run(actionType, func(t *testing.T) {
			s, _, cloudMemory, users, call := setupV2(t)
			ctx := context.Background()
			keep, _ := s.DB.ApplyAnniversary(ctx, "sess_shared", "keep", users[0].ID, "", "旅行纪念", "2025-01-01", "other")
			row, err := s.DB.ApplyAnniversary(ctx, "sess_shared", "wedding", users[0].ID, "", "结婚的日子", "2026-05-01", "wedding")
			if err != nil {
				t.Fatal(err)
			}
			for _, item := range []*dbop.Anniversary{keep, row} {
				memory, _ := s.DB.GetMemoryRecord(ctx, dbop.MemoryID("sess_shared", dbop.AnniversaryMemoryPath(item.ID)), "sess_shared")
				if err := s.syncMemoryLocked(ctx, *memory); err != nil {
					t.Fatal(err)
				}
			}
			space, _, err := s.conversationContext(ctx, "sess_shared", users[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			input := conversation.Input{UserID: users[0].ID, Context: space}
			job := dbop.ControlJob{ID: "delete_job", SessionID: "sess_shared"}
			action := conversation.Action{Type: actionType, Key: "remove_wedding", AnniversaryID: row.ID}
			if actionType == "delete_memory" {
				action.AnniversaryID = ""
				action.MemoryKey = dbop.MemoryID("sess_shared", dbop.AnniversaryMemoryPath(row.ID))
			}
			cloudMemory.mu.Lock()
			cloudMemory.fail = true
			cloudMemory.mu.Unlock()
			result := s.executeAction(ctx, job, input, 0, action, space)
			if result.Status != "partial" || result.DatabaseStatus != "deleted" || result.MemoryStatus != "pending" || result.Anniversary == nil || result.Anniversary.ID != row.ID {
				t.Fatal("cloud outage reported false success or skipped database deletion", result)
			}
			res := call(1, "GET", "/api/qoder/sessions/sess_shared/anniversaries?afterDeletion=0", nil)
			var board struct {
				Anniversaries  []dbop.Anniversary
				RemovedIDs     []string
				DeletionCursor string
			}
			if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &board) != nil || len(board.Anniversaries) != 1 || board.Anniversaries[0].ID != keep.ID || len(board.RemovedIDs) != 1 || board.RemovedIDs[0] != row.ID {
				t.Fatal("other member still sees deleted database card", res.Body.String())
			}
			cloudMemory.mu.Lock()
			cloudMemory.fail = false
			cloudMemory.mu.Unlock()
			result = s.executeAction(ctx, job, input, 0, action, space)
			if result.Status != "succeeded" || result.MemoryStatus != "synced" {
				t.Fatal("idempotent retry did not complete cloud deletion", result)
			}
			cloudMemory.mu.Lock()
			defer cloudMemory.mu.Unlock()
			for _, entry := range cloudMemory.entries {
				if strings.Contains(entry.Content, row.ID) {
					t.Fatal("current cloud page kept deleted date", entry.Path)
				}
			}
		})
	}
}
