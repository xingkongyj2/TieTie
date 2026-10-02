package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"tietie/backend/internal/dbop"
	"tietie/backend/internal/qoder"
	"tietie/backend/internal/weather"
)

func TestAnniversaryReminderSettingsAPIAndSharedHistory(t *testing.T) {
	s, cloud, _, users, call := setupV2(t)
	ctx := context.Background()
	path := "/api/qoder/sessions/sess_shared/anniversary-reminder-settings"
	read := func(viewer int, enabled bool) {
		t.Helper()
		res := call(viewer, "GET", path, nil)
		var settings dbop.AnniversaryReminderSettings
		if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &settings) != nil || settings.Enabled != enabled {
			t.Fatal(res.Code, res.Body.String())
		}
	}
	read(0, false)
	for _, method := range []string{"GET", "PUT"} {
		if res := call(2, method, path, map[string]any{"enabled": true}); res.Code != 403 {
			t.Fatal("outsider accessed settings", method, res.Code)
		}
	}
	for _, body := range []map[string]any{{}, {"enabled": "true"}, {"enabled": nil}} {
		if res := call(0, "PUT", path, body); res.Code != 400 {
			t.Fatal("invalid setting accepted", res.Code, res.Body.String())
		}
	}
	if res := call(0, "POST", path, nil); res.Code != 405 {
		t.Fatal("unsupported method accepted", res.Code)
	}
	now := time.Date(2026, 10, 2, 8, 0, 0, 0, weather.Shanghai)
	if _, err := s.DB.ApplyAnniversary(ctx, "sess_shared", "anniversary", users[0].ID, "", "在一起的日子", "2025-10-05", "together"); err != nil {
		t.Fatal(err)
	}
	if res := call(0, "PUT", path, map[string]any{"enabled": true}); res.Code != 200 {
		t.Fatal(res.Body.String())
	}
	read(1, true)
	// Use a controlled clock for the queue; no browser and no cloud turn are
	// needed to publish the actual scheduled notification.
	if err := s.DB.SaveAnniversaryReminderSettings(ctx, "sess_shared", users[0].ID, false, now); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.SaveAnniversaryReminderSettings(ctx, "sess_shared", users[0].ID, true, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	jobs, err := s.DB.ClaimAnniversaryReminders(ctx, now, 8)
	if err != nil || len(jobs) != 1 {
		t.Fatal(jobs, err)
	}
	if err := s.DB.DeliverAnniversaryReminders(ctx, jobs[0], now); err != nil {
		t.Fatal(err)
	}
	reports, err := s.DB.ListCareReports(ctx, "sess_shared", 100)
	if err != nil || len(reports) != 1 {
		t.Fatal(reports, err)
	}
	for viewer := 0; viewer < 2; viewer++ {
		res := call(viewer, "GET", "/api/qoder/sessions/sess_shared/messages", nil)
		var history qoder.MessagesResult
		if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &history) != nil {
			t.Fatal(res.Code, res.Body.String())
		}
		found := false
		for _, message := range history.Messages {
			if message.ID == reports[0].ID {
				found = true
				if message.Sender != "ai" || message.Source != "reminder" || len(message.RecipientIDs) != 2 || len(message.WeatherCards) != 0 || !strings.Contains(message.Text, "@小一 @小二") || !strings.Contains(message.Text, "还有 3 天") {
					t.Fatal(message)
				}
			}
		}
		if !found {
			t.Fatal("anniversary reminder missing from member history", viewer)
		}
	}
	// The existing care cursor also handles these plain-text notices.
	var delta qoder.MessagesResult
	if err := s.appendCareHistory(ctx, "sess_shared", &delta, reports[0].ID); err != nil || len(delta.Messages) != 0 {
		t.Fatal("read notification repeated", delta, err)
	}
	if res := call(1, "PUT", path, map[string]any{"enabled": false}); res.Code != 200 {
		t.Fatal(res.Body.String())
	}
	read(0, false)
	cloud.mu.Lock()
	posts := cloud.postings
	cloud.mu.Unlock()
	if posts != 0 {
		t.Fatal("anniversary notification unexpectedly started a cloud turn", posts)
	}
}
