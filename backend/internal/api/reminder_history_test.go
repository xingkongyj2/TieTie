package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tietie/backend/internal/config"
	"tietie/backend/internal/dbop"
	"tietie/backend/internal/qoder"
)

func TestDueReadSelectsOnlyAddressedReminderFromMonthlyPage(t *testing.T) {
	s, _, _, users, _ := setupV2(t)
	ctx := context.Background()
	first, _, err := s.DB.ApplyReminderAction(ctx, "sess_shared", "first_read_fixture", 0, dbop.ReminderAction{Type: "create", Title: "提醒第一位喝水", DueAt: time.Now().Add(time.Hour), RecipientIDs: []int64{users[0].ID}})
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := s.DB.ApplyReminderAction(ctx, "sess_shared", "second_read_fixture", 0, dbop.ReminderAction{Type: "create", Title: "其他提醒不可进入本次通知", DueAt: time.Now().Add(2 * time.Hour), RecipientIDs: []int64{users[1].ID}})
	if err != nil {
		t.Fatal(err)
	}
	page, err := s.DB.GetReminderMemory(ctx, *first)
	if err != nil || page == nil || !strings.Contains(page.Content, second.ID) {
		t.Fatal("fixtures must share an archive page", err)
	}
	reads, err := s.reminderMemoryRead(ctx, *first)
	if err != nil || len(reads) != 1 || !strings.Contains(reads[0].Content, first.ID) || strings.Contains(reads[0].Content, second.ID) || strings.Contains(reads[0].Content, second.Title) {
		t.Fatal("due read included other archived reminders", reads, err)
	}
}

func TestDeletedInactiveArchiveStoreRecoversWithoutReplacingLiveMount(t *testing.T) {
	for _, inactive := range []bool{false, true} {
		t.Run(fmt.Sprintf("inactive=%t", inactive), func(t *testing.T) {
			s, _, memory, users, _ := setupV2(t)
			ctx := context.Background()
			r, _, err := s.DB.ApplyReminderAction(ctx, "sess_shared", "recovery_fixture", 0, dbop.ReminderAction{Type: "create", Title: "历史归档必须保留", DueAt: time.Now().Add(time.Hour), RecipientIDs: []int64{users[0].ID}})
			if err != nil {
				t.Fatal(err)
			}
			if err := s.DB.SaveSpaceMemoryStore(ctx, dbop.SpaceMemoryStore{SessionID: "sess_shared", StoreID: "memstore_deleted", NativeMounted: true}); err != nil {
				t.Fatal(err)
			}
			page, _ := s.DB.GetReminderMemory(ctx, *r)
			if inactive {
				if _, err := s.DB.Unbind(ctx, users[0].ID); err != nil {
					t.Fatal(err)
				}
				page.AllowUnbound = true
				page.PendingContent = page.Content
				if err := s.DB.QueueMemory(ctx, *page); err != nil {
					t.Fatal(err)
				}
				page, _ = s.DB.GetReminderMemory(ctx, *r)
			}
			cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if strings.HasPrefix(req.URL.Path, "/memory_stores/memstore_deleted/") {
					w.WriteHeader(404)
					return
				}
				memory.ServeHTTP(w, req)
			}))
			defer cloud.Close()
			s.Qoder = qoder.NewClient(config.Config{Upstream: cloud.URL, Token: "test", Timeout: time.Second})
			err = s.runMemorySync(ctx, *page)
			mapping, _ := s.DB.GetSpaceMemoryStore(ctx, "sess_shared")
			if !inactive {
				if err == nil || mapping.StoreID != "memstore_deleted" || !mapping.NativeMounted {
					t.Fatal("live mount was silently replaced", err, mapping)
				}
				return
			}
			if err != nil || mapping.StoreID == "memstore_deleted" || mapping.NativeMounted {
				t.Fatal("inactive archive not recovered", err, mapping)
			}
			latest, _ := s.DB.GetReminderMemory(ctx, *r)
			if latest.Revision <= page.Revision || latest.State != "pending" || latest.Content != page.Content {
				t.Fatal("recovery lost history or accepted stale write")
			}
			jobs, err := s.DB.ClaimMemorySync(ctx, time.Now().Add(time.Second), 100)
			if err != nil || len(jobs) != 2 {
				t.Fatal("all complete archive pages/index must be requeued", len(jobs), err)
			}
			for _, job := range jobs {
				if err := s.runMemorySync(ctx, job); err != nil {
					t.Fatal(err)
				}
			}
			reads, err := s.reminderMemoryRead(ctx, *r)
			if err != nil || len(reads) != 1 || !strings.Contains(reads[0].Content, r.Title) {
				t.Fatal("recovered cloud archive cannot be read", err)
			}
		})
	}
}
