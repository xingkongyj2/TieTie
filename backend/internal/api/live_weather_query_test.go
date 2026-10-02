//go:build live

package api

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"tietie/backend/internal/conversation"
	"tietie/backend/internal/dbop"
	"tietie/backend/internal/qoder"
	"tietie/backend/internal/regions"
	"tietie/backend/internal/weather"
	"time"
)

// Real model + real current/forecast APIs, on an isolated database and space.
func TestLiveNaturalLanguageWeatherQuery(t *testing.T) {
	if os.Getenv("QODER_LIVE_TEST") != "1" || os.Getenv("TIETIE_WEATHER_LIVE_TEST") != "1" {
		t.Skip("requires both live flags")
	}
	cfg := loadLiveTestConfig(t)
	cfg.ConversationProtocolVersion, cfg.CloudMemoryEnabled = 2, true
	s, _, _, users, call := setupV2(t)
	s.Cfg, s.Qoder = &cfg, qoder.NewClient(cfg)
	s.Weather = weather.NewQWeather(weather.QWeatherConfig{Host: cfg.QWeatherHost, APIKey: cfg.QWeatherKey})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	_, _ = s.DB.Unbind(ctx, users[0].ID)
	for i, city := range []string{"武汉", "潜江"} {
		r, err := regions.ResolveNames("湖北", city, "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.DB.SaveUserProfile(ctx, dbop.UserProfile{UserID: users[i].ID, Region: r}); err != nil {
			t.Fatal(err)
		}
	}
	if res := call(0, "POST", "/api/account/bind", map[string]string{"code": users[1].Code}); res.Code != 200 {
		t.Fatal("isolated initialization failed", res.Code)
	}
	binding, err := s.DB.GetLatestBindingByUser(ctx, users[0].ID)
	if err != nil || binding == nil {
		t.Fatal(err)
	}
	store, err := s.DB.GetSpaceMemoryStore(ctx, binding.SessionID)
	if err != nil || store == nil {
		t.Fatal(err)
	}
	defer s.cleanupInitializedSpace(binding.SessionID, store.StoreID)
	for _, prompt := range []string{"重新查天气", "帮TA查一下现在的天气", "我们两边现在天气怎么样", "我明天出门需要带伞吗？", "浙江杭州西湖区这会儿多少度？"} {
		turnDeadline := time.Now().Add(75 * time.Second)
		if res := call(0, "POST", "/api/qoder/sessions/"+binding.SessionID+"/messages", map[string]string{"text": prompt}); res.Code != 200 {
			t.Fatal("send failed", res.Code)
		}
		found := false
		for ctx.Err() == nil && time.Now().Before(turnDeadline) {
			if err := s.syncPendingConversation(ctx, binding.SessionID); err != nil {
				t.Fatal(err)
			}
			jobs, err := s.DB.ClaimControls(ctx, time.Now(), 4)
			if err != nil {
				t.Fatal(err)
			}
			for _, job := range jobs {
				if err := s.runControl(ctx, job); err != nil {
					t.Fatal(err)
				}
			}
			res := call(0, "GET", "/api/qoder/sessions/"+binding.SessionID+"/messages", nil)
			var history qoder.MessagesResult
			if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &history) != nil {
				t.Fatal("history failed", res.Code)
			}
			raw, err := s.Qoder.GetMessages(ctx, binding.SessionID, "")
			if err != nil {
				t.Fatal(err)
			}
			request := ""
			for _, event := range raw.Events {
				input, ok := conversation.DecodeInput(eventText(event))
				if ok && !input.Hidden && input.Text == prompt {
					request = input.RequestID
				}
			}
			seenRequest := false
			for _, event := range raw.Events {
				input, ok := conversation.DecodeInput(eventText(event))
				if ok && !input.Hidden && request != "" && input.RequestID == request {
					seenRequest = true
				}
				if seenRequest && event.Type == "agent.message" {
					out := conversation.ParseAssistant(eventText(event))
					if out.Version == 2 && out.RequestID != request {
						t.Fatal("live model used wrong request ID", prompt)
					}
				}
			}
			replyID := ""
			for _, message := range raw.Messages {
				if request != "" && message.Sender == "ai" && message.RequestID == request {
					replyID = message.ID
				}
			}
			for _, message := range history.Messages {
				if replyID != "" && message.ID == replyID {
					if len(message.WeatherCards) == 0 {
						t.Fatal("model did not query weather", prompt, message.Text)
					}
					if prompt == "重新查天气" && (len(message.WeatherCards) != 1 || len(message.WeatherCards[0].RecipientIDs) != 1 || message.WeatherCards[0].RecipientIDs[0] != users[0].ID || message.WeatherCards[0].Region.City != "武汉市") {
						t.Fatal("default must query requester only", message.WeatherCards)
					}
					if prompt == "帮TA查一下现在的天气" && (len(message.WeatherCards) != 1 || len(message.WeatherCards[0].RecipientIDs) != 1 || message.WeatherCards[0].RecipientIDs[0] != users[1].ID || message.WeatherCards[0].Region.City != "潜江市") {
						t.Fatal("explicit partner query wrong", message.WeatherCards)
					}
					if prompt == "我们两边现在天气怎么样" && len(message.WeatherCards) != 2 {
						t.Fatal("explicit both query wrong", message.WeatherCards)
					}
					if prompt == "我明天出门需要带伞吗？" && (len(message.WeatherCards) != 1 || message.WeatherCards[0].Mode != "query_tomorrow" || message.WeatherCards[0].RecipientIDs[0] != users[0].ID) {
						t.Fatal("semantic time/self routing wrong", message.WeatherCards)
					}
					if prompt == "浙江杭州西湖区这会儿多少度？" && message.WeatherCards[0].Region.District != "西湖区" {
						t.Fatal("explicit city ignored")
					}
					for _, card := range message.WeatherCards {
						if card.Mode == "query" && card.CurrentWeather == nil {
							t.Fatal("real current data unavailable", card.QueryNotice)
						}
						t.Logf("real weather: %s %s mode=%s current=%v", card.Region.City, card.Region.District, card.Mode, card.CurrentWeather != nil)
					}
					if prompt == "重新查天气" {
						_ = os.WriteFile("/tmp/tietie-live-weather-query.json", res.Body.Bytes(), 0600)
					}
					found = true
					break
				}
			}
			if found {
				break
			}
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
			}
		}
		if !found {
			t.Fatal("live model query timed out", prompt)
		}
	}
	self, _ := s.DB.GetUserProfile(ctx, users[0].ID)
	if self.Region.City != "武汉市" {
		t.Fatal("query changed residence", self.Region)
	}
}
