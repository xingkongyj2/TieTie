//go:build live

package api

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"tietie/backend/internal/dbop"
	"tietie/backend/internal/regions"
	"tietie/backend/internal/weather"
	"time"
)

// Uses isolated accounts/database with real forecast APIs. No real space is
// enabled or messaged; actual scheduled delivery and memory use test fixtures.
func TestLiveWeatherCarePreviewAndScheduledDelivery(t *testing.T) {
	if os.Getenv("TIETIE_WEATHER_LIVE_TEST") != "1" {
		t.Skip("requires TIETIE_WEATHER_LIVE_TEST=1")
	}
	s, _, _, users, call := setupV2(t)
	cfg := loadLiveTestConfig(t)
	s.Weather = weather.NewQWeather(weather.QWeatherConfig{Host: cfg.QWeatherHost, APIKey: cfg.QWeatherKey})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	region, _ := regions.Resolve("420000", "420100", "420111")
	for _, u := range users[:2] {
		if _, err := s.DB.SaveUserProfile(ctx, dbop.UserProfile{UserID: u.ID, Region: region}); err != nil {
			t.Fatal(err)
		}
	}
	res := call(0, "GET", "/api/qoder/sessions/sess_shared/care-preview?mode=night", nil)
	var preview struct {
		Cards []weather.Card `json:"cards"`
	}
	if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &preview) != nil || len(preview.Cards) != 1 {
		t.Fatal(res.Code, res.Body.String())
	}
	tomorrow := time.Now().In(weather.Shanghai).AddDate(0, 0, 1).Format("2006-01-02")
	card := preview.Cards[0]
	if card.Day.Date != tomorrow || card.Day.Min > card.Day.Max || len(card.Hours) != 4 || len(card.RecipientIDs) != 2 || card.Precision != "district" || card.Clothing == "" {
		t.Fatal(card)
	}
	if !card.AirAvailable || card.Source != "和风天气" || card.CurrentAir == nil || card.CurrentAir.PM25 == nil {
		t.Fatal("real air forecast unavailable", card)
	}
	t.Logf("real night forecast: %s %s %.1f–%.1f°C, air=%v, hours=%d", card.Region.District, card.Description, card.Day.Min, card.Day.Max, card.AirAvailable, len(card.Hours))
	_ = os.WriteFile("/tmp/tietie-live-care-preview.json", res.Body.Bytes(), 0600)
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
	for viewer := 0; viewer < 2; viewer++ {
		res := call(viewer, "GET", "/api/qoder/sessions/sess_shared/messages", nil)
		if res.Code != 200 || !strings.Contains(res.Body.String(), "weatherCards") || !strings.Contains(res.Body.String(), "洪山区") {
			t.Fatal("live forecast absent from group", res.Code, res.Body.String())
		}
	}
	region, _ = regions.Resolve("330000", "330100", "330106")
	_, _ = s.DB.SaveUserProfile(ctx, dbop.UserProfile{UserID: users[1].ID, Region: region})
	res = call(0, "GET", "/api/qoder/sessions/sess_shared/care-preview?mode=morning", nil)
	if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &preview) != nil || len(preview.Cards) != 2 || preview.Cards[0].Day.Date != time.Now().In(weather.Shanghai).Format("2006-01-02") {
		t.Fatal("cross-region live preview failed", res.Code, res.Body.String())
	}
	t.Log("real cross-region morning forecast: two cards, one recipient each")
}
