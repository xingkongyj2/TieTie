//go:build live

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"tietie/backend/internal/dbop"
	"tietie/backend/internal/memoryspace"
	"tietie/backend/internal/qoder"
)

// Explicitly opt-in, strictly read-only audit of the running application's
// stores. No schema migration, queued jobs, messages, account or cloud writes.
func TestLiveExistingStoresUseTemplateSchemas(t *testing.T) {
	if os.Getenv("QODER_LIVE_TEST") != "1" || os.Getenv("QODER_AUDIT_DB") != "1" {
		t.Skip("requires live cloud and existing-store audit opt-ins")
	}
	cfg := loadLiveTestConfig(t)
	path := cfg.DBDSN
	if !filepath.IsAbs(path) {
		path = filepath.Join("../..", path)
	}
	db, err := gorm.Open(sqlite.Open("file:"+path+"?mode=ro"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal("read-only database open failed")
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	db = db.WithContext(ctx)
	client := qoder.NewClient(cfg)
	var stores []dbop.SpaceMemoryStore
	if err := db.Find(&stores).Error; err != nil {
		t.Fatal(err)
	}
	var reminders []dbop.Reminder
	if err := db.Find(&reminders).Error; err != nil {
		t.Fatal(err)
	}
	byID := map[string]dbop.Reminder{}
	for _, r := range reminders {
		byID[r.ID] = r
	}
	seenGlobal := map[string]bool{}
	for _, store := range stores {
		if store.TemplateVersion != memoryspace.Version {
			t.Fatal("store was not upgraded", store.SessionID)
		}
		entries := listLiveMemoryDocuments(t, ctx, client, store.StoreID)
		roots, pages := 0, 0
		seen := map[string]bool{}
		for _, ref := range entries {
			entry, err := client.GetMemory(ctx, store.StoreID, ref.ID)
			if err != nil {
				t.Fatal("cloud document read failed", ref.Path, err)
			}
			if err := memoryspace.ValidateDocument(entry.Path, entry.Content); err != nil {
				t.Fatal("cloud contains non-template document", entry.Path, err)
			}
			if memoryspace.IsTemplatePath(entry.Path) {
				roots++
				continue
			}
			pages++
			if !strings.HasPrefix(entry.Path, "tasks/todo-board/") {
				continue
			}
			var body struct {
				Reminders []json.RawMessage `json:"reminders"`
			}
			if err := json.Unmarshal([]byte(entry.Content), &body); err != nil {
				t.Fatal(err)
			}
			for _, raw := range body.Reminders {
				var fact struct {
					ID              string     `json:"reminderId"`
					Title           string     `json:"title"`
					Due             time.Time  `json:"dueAt"`
					CreatedBy       int64      `json:"createdBy"`
					Recipients      []int64    `json:"recipientIds"`
					Delivery        string     `json:"deliveryStatus"`
					Status          string     `json:"status"`
					Activity        string     `json:"activityStatus"`
					TaskStatus      string     `json:"taskStatus"`
					CreatedAt       time.Time  `json:"createdAt"`
					UpdatedAt       time.Time  `json:"updatedAt"`
					DeliveredAt     *time.Time `json:"deliveredAt"`
					TaskCompletedAt *time.Time `json:"taskCompletedAt"`
					CompletedBy     *int64     `json:"completedBy"`
				}
				if err := json.Unmarshal(raw, &fact); err != nil {
					t.Fatal(err)
				}
				original, ok := byID[fact.ID]
				if !ok || seen[fact.ID] {
					t.Fatal("unknown or duplicate archived reminder")
				}
				wantStatus, wantActivity := "pending", "pending"
				if original.Status == dbop.ReminderCancelled {
					wantStatus, wantActivity = "cancelled", "cancelled"
				} else {
					if original.Status == dbop.ReminderCompleted || original.Status == dbop.ReminderDelivered || original.TaskStatus == "completed" || original.DeliveredAt != nil || original.TaskCompletedAt != nil {
						wantStatus = "completed"
					}
					if original.Status == dbop.ReminderCompleted || original.CompletedBy != nil {
						wantActivity = "completed"
					}
				}
				if fact.Status != wantStatus || fact.Activity != wantActivity {
					t.Fatal("cloud reminder still pending after completion or incorrectly implies real-world activity completion")
				}
				if fact.Title != original.Title || !fact.Due.Equal(original.DueAt) || fact.CreatedBy != original.CreatedBy || !reflect.DeepEqual(fact.Recipients, original.RecipientIDs) || fact.Delivery != original.Status || fact.TaskStatus != original.TaskStatus || !fact.CreatedAt.Equal(original.CreatedAt) || !fact.UpdatedAt.Equal(original.UpdatedAt) || !sameTime(fact.DeliveredAt, original.DeliveredAt) || !sameTime(fact.TaskCompletedAt, original.TaskCompletedAt) || !reflect.DeepEqual(fact.CompletedBy, original.CompletedBy) {
					t.Fatal("template conversion changed reminder fields")
				}
				seen[fact.ID], seenGlobal[fact.ID] = true, true
			}
		}
		var locations []dbop.ReminderHistoryLocation
		if err := db.Where("session_id=?", store.SessionID).Find(&locations).Error; err != nil {
			t.Fatal(err)
		}
		for _, l := range locations {
			if !seen[l.ReminderID] {
				t.Fatal("cloud history lost a reminder")
			}
		}
		if roots != 7 {
			t.Fatal("store does not contain all seven root templates", roots)
		}
		t.Logf("%s: seven root templates + %d same-template pages; %d complete reminder records verified", store.SessionID, pages, len(seen))
	}
	if len(seenGlobal) != len(reminders) {
		t.Fatal("some database reminders are absent from cloud history")
	}
	t.Logf("read-only cloud audit: %d independent stores; all %d unique historical reminders retained", len(stores), len(reminders))
}
func sameTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}
func listLiveMemoryDocuments(t *testing.T, ctx context.Context, client *qoder.Client, store string) []qoder.MemoryEntry {
	t.Helper()
	var out []qoder.MemoryEntry
	page := ""
	for i := 0; i < 100; i++ {
		values := url.Values{"limit": {"100"}}
		if page != "" {
			values.Set("page", page)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, client.BaseURL+"/memory_stores/"+url.PathEscape(store)+"/memories?"+values.Encode(), nil)
		if err != nil {
			t.Fatal("invalid cloud inventory request")
		}
		req.Header.Set("Authorization", "Bearer "+client.Token)
		response, err := client.HC.Do(req)
		if err != nil {
			t.Fatal("cloud inventory failed")
		}
		var body struct {
			Data []qoder.MemoryEntry `json:"data"`
			More bool                `json:"has_more"`
			Next string              `json:"next_page"`
		}
		err = json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&body)
		response.Body.Close()
		if response.StatusCode != 200 || err != nil {
			t.Fatal(fmt.Sprintf("cloud inventory status=%d", response.StatusCode))
		}
		out = append(out, body.Data...)
		if !body.More {
			return out
		}
		if body.Next == "" || body.Next == page {
			t.Fatal("invalid cloud inventory cursor")
		}
		page = body.Next
	}
	t.Fatal("cloud inventory exceeded audit page budget")
	return nil
}
