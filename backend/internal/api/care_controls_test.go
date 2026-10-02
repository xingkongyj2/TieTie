package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"tietie/backend/internal/conversation"
	"tietie/backend/internal/dbop"
	"tietie/backend/internal/memoryspace"
	"tietie/backend/internal/regions"
	"time"
)

func TestChatControlsSavePartnerRegionPreferencesAndCountdownThenSyncBothViews(t *testing.T) {
	s, cloud, memory, users, call := setupV2(t)
	ctx := context.Background()
	path := "/api/qoder/sessions/sess_shared/messages"
	if res := call(0, "POST", path, map[string]string{"text": "TA搬到浙江杭州西湖区了，晚安以后只看温度和降雨，帮我加她11月16日生日倒计时"}); res.Code != 200 {
		t.Fatal(res.Body.String())
	}
	input := latestInput(t, cloud)
	appendProtocolReply(cloud, map[string]any{"protocol": "tietie.control", "version": 2, "requestId": input.RequestID, "actions": []conversation.Action{
		{Type: "set_region", Key: "region", TargetUserID: users[1].ID, Region: &conversation.RegionUpdate{Province: "浙江", City: "杭州", District: "西湖"}, Storage: "database_and_memory"},
		{Type: "set_weather_metrics", Key: "metrics", TargetUserID: users[0].ID, Metrics: []string{"temperature", "rain"}, Storage: "database_and_memory"},
		{Type: "save_countdown", Key: "birthday", Title: "TA的生日", Date: "1998-11-16", CountdownRepeat: "annual", CountdownKind: "birthday", Storage: "database_and_memory"},
	}})
	if err := s.syncPendingConversation(ctx, "sess_shared"); err != nil {
		t.Fatal(err)
	}
	runClaimedControl(t, s)
	ack := latestInput(t, cloud)
	if len(ack.Results) != 3 {
		t.Fatal(ack.Results)
	}
	for _, result := range ack.Results {
		if result.Status != "succeeded" {
			t.Fatal(result)
		}
	}
	profile, _ := s.DB.GetUserProfile(ctx, users[1].ID)
	if profile.Region.District != "西湖区" {
		t.Fatal(profile)
	}
	for viewer := 0; viewer < 2; viewer++ {
		res := call(viewer, "GET", "/api/account/profiles", nil)
		if res.Code != 200 || !strings.Contains(res.Body.String(), "西湖区") {
			t.Fatal("profile view stale", res.Body.String())
		}
		res = call(viewer, "GET", "/api/qoder/sessions/sess_shared/countdowns", nil)
		if res.Code != 200 || !strings.Contains(res.Body.String(), "TA的生日") {
			t.Fatal("countdown missing", res.Body.String())
		}
	}
	if res := call(2, "GET", "/api/qoder/sessions/sess_shared/countdowns", nil); res.Code != 403 {
		t.Fatal("foreign countdown access", res.Code)
	}
	memory.mu.Lock()
	for _, entry := range memory.entries {
		if !memoryspace.IsDocumentPath(entry.Path) {
			t.Error("new memory type", entry.Path)
		}
	}
	memory.mu.Unlock()
	r, _ := regions.ResolveNames("浙江", "杭州", "西湖")
	_, _ = s.DB.SaveUserProfile(ctx, dbop.UserProfile{UserID: users[0].ID, Region: r})
	provider := &careForecastFixture{}
	s.Weather = provider
	members, b, _ := s.DB.CareMembers(ctx, "sess_shared")
	cards, err := s.buildCareCards(ctx, dbop.CareMode{SessionID: "sess_shared", Mode: "night", BindingCreatedAt: b.CreatedAt}, members, time.Now())
	if err != nil || len(cards) != 1 || len(cards[0].RecipientIDs) != 2 || len(cards[0].Views) != 2 || provider.Calls != 1 {
		t.Fatal("same-region personalization failed", cards, err, provider.Calls)
	}
	body, _ := json.Marshal(cards[0].Views[0])
	if strings.Contains(string(body), "空气质量") || strings.Contains(string(body), "clothing") {
		t.Fatal("unrequested data pushed", string(body))
	}
}
