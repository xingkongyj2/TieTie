package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"tietie/backend/internal/dbop"
	"tietie/backend/internal/memoryspace"
	"tietie/backend/internal/qoder"
	"tietie/backend/internal/regions"
	"tietie/backend/internal/weather"
	"time"
)

type careForecastFixture struct {
	Calls int
	Fail  bool
}

func (f *careForecastFixture) Forecast(ctx context.Context, r regions.Location) (weather.Forecast, error) {
	f.Calls++
	if f.Fail {
		return weather.Forecast{}, errors.New("temporary weather outage")
	}
	now := time.Now()
	today := now.In(weather.Shanghai)
	out := weather.Forecast{Precision: "district", FetchedAt: now}
	for i := -1; i <= 2; i++ {
		date := today.AddDate(0, 0, i).Format("2006-01-02")
		out.Days = append(out.Days, weather.Day{Date: date, Min: 15, Max: 23, Code: 3})
	}
	return out, nil
}
func TestCareAPIRegionGateGroupsAndTodaySharedReminders(t *testing.T) {
	s, _, _, users, call := setupV2(t)
	ctx := context.Background()
	provider := &careForecastFixture{}
	s.Weather = provider
	path := "/api/qoder/sessions/sess_shared/care-settings"
	if res := call(0, "PUT", path, map[string]any{"mode": "morning", "enabled": true, "time": "08:00"}); res.Code != 409 {
		t.Fatal("missing regions accepted", res.Code, res.Body.String())
	}
	for viewer := 0; viewer < 2; viewer++ {
		res := call(viewer, "GET", "/api/qoder/sessions/sess_shared/messages", nil)
		var history qoder.MessagesResult
		if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &history) != nil {
			t.Fatal(res.Code, res.Body.String())
		}
		found := false
		for _, msg := range history.Messages {
			if strings.HasPrefix(msg.ID, "evt_care_") {
				found = true
				if len(msg.RecipientIDs) != 2 || len(msg.WeatherCards) != 0 || !strings.Contains(msg.Text, "@小一") || !strings.Contains(msg.Text, "@小二") || !strings.Contains(msg.Text, "还没有填写地区") {
					t.Fatal("region notice did not explain both members", msg)
				}
			}
		}
		if !found {
			t.Fatal("missing region guidance absent from group history", viewer)
		}
	}
	modes, _ := s.DB.GetCareModes(ctx, "sess_shared")
	if modes[0].Enabled {
		t.Fatal("invalid mode saved")
	}
	for _, u := range users[:2] {
		region, _ := regions.Resolve("420000", "420100", "420111")
		_, err := s.DB.SaveUserProfile(ctx, dbop.UserProfile{UserID: u.ID, Region: region, Hobbies: []string{}})
		if err != nil {
			t.Fatal(err)
		}
	}
	if res := call(2, "GET", path, nil); res.Code != 403 {
		t.Fatal("outsider accessed modes", res.Code)
	}
	if res := call(1, "PUT", path, map[string]any{"mode": "morning", "enabled": true, "time": "08:00"}); res.Code != 200 {
		t.Fatal(res.Body.String())
	}
	if res := call(0, "PUT", path, map[string]any{"mode": "night", "enabled": true, "time": "25:00"}); res.Code != 400 {
		t.Fatal("invalid clock accepted")
	}
	members, b, _ := s.DB.CareMembers(ctx, "sess_shared")
	now := time.Now()
	local := now.In(weather.Shanghai)
	today := time.Date(local.Year(), local.Month(), local.Day(), 23, 50, 0, 0, weather.Shanghai)
	for i, title := range []string{"今天共享", "今天已取消", "明天事项"} {
		due := today
		if i == 2 {
			due = due.AddDate(0, 0, 1)
		}
		reminder, _, err := s.DB.ApplyReminderAction(ctx, "sess_shared", "care_reminders", i, dbop.ReminderAction{Type: "create", Title: title, DueAt: due, RecipientIDs: []int64{users[0].ID}, CreatedBy: users[0].ID})
		if err != nil {
			t.Fatal(err)
		}
		if i == 1 {
			_, _, err = s.DB.ApplyReminderAction(ctx, "sess_shared", "care_cancel", 0, dbop.ReminderAction{Type: "cancel", ReminderID: reminder.ID})
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := s.DB.SavePrivateChannel(ctx, dbop.PrivateChannel{SessionID: "sess_private_care", SpaceID: "sess_shared", OwnerID: users[0].ID, BindingCreatedAt: b.CreatedAt}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.DB.ApplyReminderAction(ctx, "sess_private_care", "care_private", 0, dbop.ReminderAction{Type: "create", Title: "私密事项", DueAt: today, RecipientIDs: []int64{users[0].ID}, CreatedBy: users[0].ID}); err != nil {
		t.Fatal(err)
	}
	cards, err := s.buildCareCards(ctx, dbop.CareMode{SessionID: "sess_shared", Mode: "morning", BindingCreatedAt: b.CreatedAt}, members, now)
	if err != nil || len(cards) != 1 || len(cards[0].RecipientIDs) != 2 || provider.Calls != 1 || len(cards[0].Reminders) != 1 || cards[0].Reminders[0].Title != "今天共享" {
		t.Fatal(cards, provider.Calls, err)
	}
	region, _ := regions.Resolve("330000", "330100", "330106")
	_, _ = s.DB.SaveUserProfile(ctx, dbop.UserProfile{UserID: users[1].ID, Region: region, Hobbies: []string{}})
	members, _, _ = s.DB.CareMembers(ctx, "sess_shared")
	cards, err = s.buildCareCards(ctx, dbop.CareMode{SessionID: "sess_shared", Mode: "morning", BindingCreatedAt: b.CreatedAt}, members, now)
	if err != nil || len(cards) != 2 || len(cards[0].RecipientIDs) != 1 || len(cards[1].RecipientIDs) != 1 || len(cards[0].Reminders) != 1 || len(cards[1].Reminders) != 0 {
		t.Fatal("cross-region reminder attribution failed", cards, err)
	}
	res := call(1, "GET", "/api/qoder/sessions/sess_shared/care-preview?mode=night", nil)
	if res.Code != 200 || !strings.Contains(res.Body.String(), "穿") {
		t.Fatal("night preview failed", res.Code, res.Body.String())
	}
	reports, _ := s.DB.ListCareReports(ctx, "sess_shared", 10)
	if len(reports) != 1 || reports[0].Mode != "region_notice" {
		t.Fatal("preview posted to chat")
	}
}
func TestCareWorkerPersistsGroupCardAndExistingTemplateMemory(t *testing.T) {
	s, cloud, memory, users, call := setupV2(t)
	ctx := context.Background()
	s.Weather = &careForecastFixture{}
	region, _ := regions.Resolve("420000", "420100", "420111")
	for _, u := range users[:2] {
		_, _ = s.DB.SaveUserProfile(ctx, dbop.UserProfile{UserID: u.ID, Region: region, Hobbies: []string{}})
	}
	now := time.Now()
	clock := now.Add(-time.Minute).In(weather.Shanghai).Format("15:04")
	if err := s.DB.SaveCareMode(ctx, "sess_shared", users[0].ID, "night", clock, true, now.Add(-2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	jobs, err := s.DB.ClaimCareModes(ctx, now, 8)
	if err != nil || len(jobs) != 1 {
		t.Fatal(jobs, err)
	}
	if err := s.runCareMode(ctx, jobs[0]); err != nil {
		t.Fatal(err)
	}
	reports, _ := s.DB.ListCareReports(ctx, "sess_shared", 10)
	if len(reports) != 1 || len(reports[0].Cards) != 1 {
		t.Fatal(reports)
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
				if message.Sender != "ai" || len(message.WeatherCards) != 1 || len(message.RecipientIDs) != 2 {
					t.Fatal(message)
				}
			}
		}
		if !found {
			t.Fatal("group card absent from chat")
		}
	}
	cloud.mu.Lock()
	posts := cloud.postings
	cloud.mu.Unlock()
	if posts != 0 {
		t.Fatal("care wakeup unexpectedly prompted the AI")
	}
	rows, err := s.DB.ClaimMemorySync(ctx, time.Now().Add(time.Minute), 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if err := s.runMemorySync(ctx, row); err != nil {
			t.Fatal(err)
		}
	}
	memory.mu.Lock()
	defer memory.mu.Unlock()
	foundWeather, foundSettings := false, false
	for _, entry := range memory.entries {
		if !memoryspace.IsDocumentPath(entry.Path) {
			t.Fatal("care invented memory type", entry.Path)
		}
		if strings.Contains(entry.Content, "weather_care") {
			foundWeather = true
		}
		if strings.Contains(entry.Content, "care_settings") {
			foundSettings = true
		}
	}
	if !foundWeather || !foundSettings {
		t.Fatal("settings/report memory missing", foundWeather, foundSettings)
	}
}
func TestCareWeatherFailureRetriesWithoutPosting(t *testing.T) {
	s, _, _, users, _ := setupV2(t)
	ctx := context.Background()
	s.Weather = &careForecastFixture{Fail: true}
	region, _ := regions.Resolve("420000", "420100", "420111")
	for _, u := range users[:2] {
		_, _ = s.DB.SaveUserProfile(ctx, dbop.UserProfile{UserID: u.ID, Region: region})
	}
	now := time.Now()
	clock := now.Add(-time.Minute).In(weather.Shanghai).Format("15:04")
	_ = s.DB.SaveCareMode(ctx, "sess_shared", users[0].ID, "morning", clock, true, now.Add(-2*time.Minute))
	jobs, _ := s.DB.ClaimCareModes(ctx, now, 8)
	if len(jobs) != 1 {
		t.Fatal(jobs)
	}
	if err := s.runCareMode(ctx, jobs[0]); err == nil {
		t.Fatal("weather failure ignored")
	}
	modes, _ := s.DB.GetCareModes(ctx, "sess_shared")
	if modes[0].State != "retrying" || !modes[0].Enabled {
		t.Fatal(modes)
	}
	reports, _ := s.DB.ListCareReports(ctx, "sess_shared", 10)
	if len(reports) != 0 {
		t.Fatal("fabricated weather posted")
	}
	_, _ = s.DB.SaveUserProfile(ctx, dbop.UserProfile{UserID: users[1].ID})
	jobs, _ = s.DB.ClaimCareModes(ctx, now.Add(6*time.Minute), 8)
	if len(jobs) != 1 {
		t.Fatal(jobs)
	}
	if err := s.runCareMode(ctx, jobs[0]); err != nil {
		t.Fatal(err)
	}
	modes, _ = s.DB.GetCareModes(ctx, "sess_shared")
	if modes[0].State != "region_missing" {
		t.Fatal("deleted region was not paused", modes)
	}
}
