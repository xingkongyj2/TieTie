package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"tietie/backend/internal/config"
	"tietie/backend/internal/conversation"
	"tietie/backend/internal/dbop"
	"tietie/backend/internal/qoder"
	"tietie/backend/internal/regions"
	"tietie/backend/internal/weather"
	"time"
)

type freshWeatherFixture struct {
	careForecastFixture
	FreshCalls int
}

func (f *freshWeatherFixture) ForecastFresh(ctx context.Context, r regions.Location) (weather.Forecast, error) {
	f.FreshCalls++
	out, err := f.Forecast(ctx, r)
	temp, feel, pm, aqi := 18.0, 17.0, 22.0, 45.0
	out.CurrentWeather = &weather.WeatherSnapshot{RetrievedAt: time.Now(), Temperature: &temp, FeelsLike: &feel, Description: "小雨", Code: 63}
	out.CurrentAir = &weather.AirSnapshot{RetrievedAt: time.Now(), PM25: &pm, AQI: &aqi, AQILabel: "中国 AQI"}
	return out, err
}
func TestWeatherQueryControlRefreshGroupingHistoryAndPreferences(t *testing.T) {
	s, cloud, _, users, call := setupV2(t)
	ctx := context.Background()
	provider := &freshWeatherFixture{}
	s.Weather = provider
	region, _ := regions.ResolveNames("湖北", "武汉", "洪山")
	for _, user := range users[:2] {
		if _, err := s.DB.SaveUserProfile(ctx, dbop.UserProfile{UserID: user.ID, Region: region}); err != nil {
			t.Fatal(err)
		}
	}
	modesBefore, _ := s.DB.GetCareModes(ctx, "sess_shared")
	query := func(prompt string, action conversation.Action) conversation.Input {
		t.Helper()
		if res := call(0, "POST", "/api/qoder/sessions/sess_shared/messages", map[string]string{"text": prompt}); res.Code != 200 {
			t.Fatal(res.Code, res.Body.String())
		}
		input := latestInput(t, cloud)
		appendProtocolReply(cloud, map[string]any{"protocol": "tietie.control", "version": 2, "requestId": input.RequestID, "actions": []conversation.Action{action}})
		if err := s.syncPendingConversation(ctx, "sess_shared"); err != nil {
			t.Fatal(err)
		}
		runClaimedControl(t, s)
		ack := latestInput(t, cloud)
		appendProtocolReply(cloud, map[string]any{"protocol": "tietie.message", "version": 2, "requestId": input.RequestID, "text": "刚刚查好了，出门记得带伞。", "recipientIds": []int64{users[0].ID, users[1].ID}, "source": "chat"})
		if err := s.syncPendingConversation(ctx, "sess_shared"); err != nil {
			t.Fatal(err)
		}
		return ack
	}
	ack := query("我们两边都重新查天气", conversation.Action{Type: "query_weather", Key: "weather", WeatherWhen: "now", RecipientIDs: []int64{users[0].ID, users[1].ID}})
	if len(ack.Results) != 1 || ack.Results[0].Status != "succeeded" || len(ack.Results[0].WeatherCards) != 1 || len(ack.Results[0].WeatherCards[0].RecipientIDs) != 2 || provider.FreshCalls != 1 {
		t.Fatal(ack.Results, provider)
	}
	snapshot := ack.Results[0].WeatherCards[0]
	if snapshot.CurrentWeather == nil || *snapshot.CurrentWeather.Temperature == snapshot.Day.Max || snapshot.CurrentAir == nil {
		t.Fatal("current weather is not distinct", snapshot)
	}
	for repeat := 0; repeat < 2; repeat++ {
		res := call(1, "GET", "/api/qoder/sessions/sess_shared/messages", nil)
		var history qoder.MessagesResult
		if json.Unmarshal(res.Body.Bytes(), &history) != nil {
			t.Fatal(res.Body.String())
		}
		count := 0
		for _, message := range history.Messages {
			if len(message.WeatherCards) > 0 {
				count++
				if len(message.WeatherCards) != 1 || message.WeatherCards[0].CurrentWeather == nil {
					t.Fatal(message)
				}
			}
		}
		if count != 1 || provider.FreshCalls != 1 {
			t.Fatal("reload duplicated/fetched weather", count, provider.FreshCalls)
		}
	}
	binding, err := s.DB.GetLatestBindingByUser(ctx, users[0].ID)
	if err != nil || binding == nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ApplyCarePreference(ctx, "sess_shared", "query_pref", users[0].ID, users[0].ID, binding.CreatedAt, []string{"pm25"}); err != nil {
		t.Fatal(err)
	}
	partnerRegion, _ := regions.ResolveNames("浙江", "杭州", "西湖")
	_, _ = s.DB.SaveUserProfile(ctx, dbop.UserProfile{UserID: users[1].ID, Region: partnerRegion})
	ack = query("今天两边的天气怎样", conversation.Action{Type: "query_weather", Key: "weather", WeatherWhen: "today", RecipientIDs: []int64{users[0].ID, users[1].ID}})
	if len(ack.Results[0].WeatherCards) != 2 || provider.FreshCalls != 3 {
		t.Fatal(ack.Results, provider)
	}
	for _, card := range ack.Results[0].WeatherCards {
		if card.RecipientIDs[0] == users[0].ID && strings.Join(card.Views[0].Metrics, ",") != "pm25" {
			t.Fatal("preference ignored", card.Views)
		}
	}
	ack = query("潜江明天下雨吗", conversation.Action{Type: "query_weather", Key: "weather", WeatherWhen: "tomorrow", Region: &conversation.RegionUpdate{Province: "湖北", City: "潜江"}})
	if len(ack.Results[0].WeatherCards) != 1 || ack.Results[0].WeatherCards[0].Region.City != "潜江市" || ack.Results[0].WeatherCards[0].Day.Date != time.Now().In(weather.Shanghai).AddDate(0, 0, 1).Format("2006-01-02") {
		t.Fatal(ack.Results)
	}
	profile, _ := s.DB.GetUserProfile(ctx, users[0].ID)
	if profile.Region != region {
		t.Fatal("query changed profile")
	}
	modesAfter, _ := s.DB.GetCareModes(ctx, "sess_shared")
	before, _ := json.Marshal(modesBefore)
	after, _ := json.Marshal(modesAfter)
	if string(before) != string(after) {
		t.Fatal("query changed schedule")
	}
	memory, err := s.DB.GetMemoryRecord(ctx, ack.Results[0].MemoryKey, "sess_shared")
	if err != nil || memory == nil || memory.Category != "realtime" || memory.ExpiresAt == nil {
		t.Fatal(memory, err)
	}
}
func TestWeatherQueryMissingLocationUnauthorizedFailuresAndSilentRoute(t *testing.T) {
	s, _, _, users, _ := setupV2(t)
	ctx := context.Background()
	provider := &freshWeatherFixture{}
	s.Weather = provider
	binding, err := s.DB.GetLatestBindingByUser(ctx, users[0].ID)
	if err != nil || binding == nil {
		t.Fatal(err)
	}
	input := conversation.Input{UserID: users[0].ID, Context: conversation.Context{AuthorID: users[0].ID}}
	job := dbop.ControlJob{ID: "query-test", RequestID: "manual-test", CreatedBy: users[0].ID, SessionID: "sess_shared", BindingCreatedAt: binding.CreatedAt}
	action := conversation.Action{Type: "query_weather", Key: "weather"}
	result := s.executeAction(ctx, job, input, 0, action, input.Context)
	if result.Status != "failed" || result.ErrorCode != "weather_region_required" || !strings.Contains(result.Message, "@小一") || strings.Contains(result.Message, "@小二") || provider.FreshCalls != 0 {
		t.Fatal(result)
	}
	region, _ := regions.ResolveNames("湖北", "武汉", "洪山")
	_, _ = s.DB.SaveUserProfile(ctx, dbop.UserProfile{UserID: users[0].ID, Region: region})
	result = s.executeAction(ctx, job, input, 0, action, input.Context)
	if result.Status != "succeeded" || len(result.WeatherCards) != 1 || result.WeatherCards[0].QueryNotice != "" || len(result.WeatherCards[0].RecipientIDs) != 1 || result.WeatherCards[0].RecipientIDs[0] != users[0].ID {
		t.Fatal(result)
	}
	action.RecipientIDs = []int64{users[2].ID}
	result = s.executeAction(ctx, job, input, 0, action, input.Context)
	if result.Status != "failed" || provider.FreshCalls != 1 {
		t.Fatal("outsider queried", result)
	}
	action.RecipientIDs = nil
	input.Context.ReplyMode = conversation.SilentReply
	result = s.executeAction(ctx, job, input, 0, action, input.Context)
	if result.Status != "failed" || provider.FreshCalls != 1 {
		t.Fatal("silent route executed", result)
	}
	input.Context.ReplyMode = ""
	provider.Fail = true
	result = s.executeAction(ctx, job, input, 0, action, input.Context)
	if result.Status != "failed" || len(result.WeatherCards) != 0 {
		t.Fatal("failure returned old data", result)
	}
}

func TestPrivateWeatherQueryKeepsResultAndMemoryPrivate(t *testing.T) {
	s, shared, memory, users, call := setupV2(t)
	private := &fakeConversation{status: "idle"}
	upstream := httptest.NewServer(&isolatedTestCloud{shared: shared, private: private, memory: memory})
	defer upstream.Close()
	cfg := config.Config{Upstream: upstream.URL, Token: "test", Timeout: time.Second, AgentID: "agent_test", EnvironmentID: "env_test", ConversationProtocolVersion: 2, CloudMemoryEnabled: true}
	s.Cfg, s.Qoder, s.Weather = &cfg, qoder.NewClient(cfg), &freshWeatherFixture{}
	ctx := context.Background()
	region, _ := regions.ResolveNames("湖北", "武汉", "洪山")
	for _, user := range users[:2] {
		if _, err := s.DB.SaveUserProfile(ctx, dbop.UserProfile{UserID: user.ID, Region: region}); err != nil {
			t.Fatal(err)
		}
	}
	path := "/api/qoder/sessions/sess_shared/messages"
	if res := call(0, "POST", path, map[string]any{"text": "悄悄查一下天气", "visibility": "private"}); res.Code != 200 {
		t.Fatal(res.Code)
	}
	input := latestInput(t, private)
	appendProtocolReply(private, map[string]any{"protocol": "tietie.control", "version": 2, "requestId": input.RequestID, "actions": []conversation.Action{{Type: "query_weather", Key: "private_weather"}}})
	if err := s.syncPendingConversation(ctx, "sess_private"); err != nil {
		t.Fatal(err)
	}
	runClaimedControl(t, s)
	ack := latestInput(t, private)
	if len(ack.Results) != 1 || ack.Results[0].Status != "succeeded" || len(ack.Results[0].WeatherCards) != 1 || len(ack.Results[0].WeatherCards[0].RecipientIDs) != 1 || ack.Results[0].WeatherCards[0].RecipientIDs[0] != users[0].ID {
		t.Fatal(ack.Results)
	}
	appendProtocolReply(private, map[string]any{"protocol": "tietie.message", "version": 2, "requestId": input.RequestID, "text": "查好了", "recipientIds": []int64{users[0].ID}, "source": "chat"})
	if err := s.syncPendingConversation(ctx, "sess_private"); err != nil {
		t.Fatal(err)
	}
	for user := 0; user < 2; user++ {
		res := call(user, "GET", path, nil)
		var history qoder.MessagesResult
		if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &history) != nil {
			t.Fatal(res.Code)
		}
		count := 0
		for _, message := range history.Messages {
			count += len(message.WeatherCards)
		}
		if user == 0 && count != 1 || user == 1 && count != 0 {
			t.Fatal("weather query visibility wrong", user, count)
		}
	}
	if record, err := s.DB.GetMemoryRecord(ctx, ack.Results[0].MemoryKey, "sess_private"); err != nil || record == nil {
		t.Fatal("private weather memory missing", err)
	}
	if record, _ := s.DB.GetMemoryRecord(ctx, ack.Results[0].MemoryKey, "sess_shared"); record != nil {
		t.Fatal("private weather memory leaked into shared space")
	}
}

func TestWeatherQueryDefaultsToRequestingMember(t *testing.T) {
	s, _, _, users, _ := setupV2(t)
	ctx := context.Background()
	provider := &freshWeatherFixture{}
	s.Weather = provider
	regionsByUser := []regions.Location{}
	for i, city := range []string{"武汉", "潜江"} {
		region, _ := regions.ResolveNames("湖北", city, "")
		regionsByUser = append(regionsByUser, region)
		if _, err := s.DB.SaveUserProfile(ctx, dbop.UserProfile{UserID: users[i].ID, Region: region}); err != nil {
			t.Fatal(err)
		}
	}
	binding, _ := s.DB.GetLatestBindingByUser(ctx, users[0].ID)
	for i, user := range users[:2] {
		input := conversation.Input{UserID: user.ID}
		job := dbop.ControlJob{ID: "weather-default-" + user.Username, RequestID: "default-" + user.Username, CreatedBy: user.ID, SessionID: "sess_shared", BindingCreatedAt: binding.CreatedAt}
		action := conversation.Action{Type: "query_weather", Key: "weather"}
		result := s.executeWeatherQuery(ctx, job, input, action)
		if result.Status != "succeeded" || len(result.WeatherCards) != 1 || len(result.WeatherCards[0].RecipientIDs) != 1 || result.WeatherCards[0].RecipientIDs[0] != user.ID || result.WeatherCards[0].Region != regionsByUser[i] {
			t.Fatal("default must follow speaking account", i, result)
		}
		job.ID += "-partner"
		job.RequestID += "-partner"
		action.RecipientIDs = []int64{users[1-i].ID}
		result = s.executeWeatherQuery(ctx, job, input, action)
		if result.Status != "succeeded" || len(result.WeatherCards) != 1 || len(result.WeatherCards[0].RecipientIDs) != 1 || result.WeatherCards[0].RecipientIDs[0] != users[1-i].ID || result.WeatherCards[0].Region != regionsByUser[1-i] {
			t.Fatal("explicit partner query must target partner", i, result)
		}
	}
	// Having a partner's location never substitutes for the requester's missing one.
	_, _ = s.DB.SaveUserProfile(ctx, dbop.UserProfile{UserID: users[0].ID})
	before := provider.FreshCalls
	input := conversation.Input{UserID: users[0].ID}
	job := dbop.ControlJob{ID: "weather-default-missing", RequestID: "default-missing", CreatedBy: users[0].ID, SessionID: "sess_shared", BindingCreatedAt: binding.CreatedAt}
	result := s.executeWeatherQuery(ctx, job, input, conversation.Action{Type: "query_weather", Key: "weather"})
	if result.Status != "failed" || result.ErrorCode != "weather_region_required" || len(result.WeatherCards) != 0 || provider.FreshCalls != before || strings.Contains(result.Message, "@小二") {
		t.Fatal("default queried partner as fallback", result)
	}
	job.ID += "-both"
	result = s.executeWeatherQuery(ctx, job, input, conversation.Action{Type: "query_weather", Key: "weather", RecipientIDs: []int64{users[0].ID, users[1].ID}})
	if result.Status != "succeeded" || len(result.WeatherCards) != 1 || result.WeatherCards[0].RecipientIDs[0] != users[1].ID || !strings.Contains(result.WeatherCards[0].QueryNotice, "@小一") {
		t.Fatal("explicit both query lost partial result", result)
	}
}
