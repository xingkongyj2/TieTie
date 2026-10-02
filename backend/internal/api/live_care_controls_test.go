//go:build live

package api

import (
	"context"
	"os"
	"strings"
	"testing"
	"tietie/backend/internal/conversation"
	"tietie/backend/internal/dbop"
	"tietie/backend/internal/qoder"
	"time"
)

func TestLiveNaturalLanguageRegionsWeatherPreferencesAndCountdown(t *testing.T) {
	if os.Getenv("QODER_LIVE_TEST") != "1" {
		t.Skip("requires QODER_LIVE_TEST=1")
	}
	cfg := loadLiveTestConfig(t)
	cfg.ConversationProtocolVersion, cfg.CloudMemoryEnabled = 2, true
	s, _, _, users, call := setupV2(t)
	s.Cfg, s.Qoder = &cfg, qoder.NewClient(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	_, _ = s.DB.Unbind(ctx, users[0].ID)
	if res := call(0, "POST", "/api/account/bind", map[string]string{"code": users[1].Code}); res.Code != 200 {
		t.Fatal("test initialization failed", res.Code)
	}
	b, err := s.DB.GetLatestBindingByUser(ctx, users[0].ID)
	if err != nil || b == nil {
		t.Fatal(err)
	}
	store, err := s.DB.GetSpaceMemoryStore(ctx, b.SessionID)
	if err != nil || store == nil {
		t.Fatal(err)
	}
	defer s.cleanupInitializedSpace(b.SessionID, store.StoreID)
	ask := func(actor int, prompt string) {
		t.Helper()
		if res := call(actor, "POST", "/api/qoder/sessions/"+b.SessionID+"/messages", map[string]string{"text": prompt}); res.Code != 200 {
			t.Fatal("chat send failed", res.Code)
		}
		for ctx.Err() == nil {
			if err := s.syncPendingConversation(ctx, b.SessionID); err != nil {
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
			history, err := s.Qoder.GetMessages(ctx, b.SessionID, "")
			if err != nil {
				t.Fatal(err)
			}
			request := ""
			for _, event := range history.Events {
				input, ok := conversation.DecodeInput(eventText(event))
				if ok && !input.Hidden && input.UserID == users[actor].ID && input.Text == prompt {
					request = input.RequestID
				}
			}
			seenCurrent := false
			for _, event := range history.Events {
				input, ok := conversation.DecodeInput(eventText(event))
				if ok && !input.Hidden && input.RequestID == request && request != "" {
					seenCurrent = true
				}
				if seenCurrent && event.Type == "agent.custom_tool_use" {
					t.Fatalf("AI asked a question instead of executing explicit request: %s", string(event.Input))
				}
			}
			for _, message := range history.Messages {
				if request != "" && message.Sender == "ai" && message.RequestID == request {
					t.Log("real AI reply:", message.Text)
					return
				}
			}
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
			}
		}
		t.Fatal("AI response timed out")
	}
	ask(0, "我住湖北武汉洪山区，TA住浙江杭州西湖区，帮我把我们俩的地区填好。我以后晚安天气只想看温度和降雨。再建个倒计时，名称叫小二的生日，日期1998年11月16日，每年倒数。")
	self, _ := s.DB.GetUserProfile(ctx, users[0].ID)
	partner, _ := s.DB.GetUserProfile(ctx, users[1].ID)
	if self.Region.District != "洪山区" || partner.Region.District != "西湖区" {
		t.Fatal("natural language region did not update database", self.Region, partner.Region)
	}
	metrics, _ := s.DB.GetCarePreference(ctx, b.SessionID, users[0].ID)
	if strings.Join(metrics, ",") != "temperature,rain" {
		t.Fatal("metrics did not persist", metrics)
	}
	rows, _, err := s.DB.ListCountdowns(ctx, b.SessionID, "", time.Now())
	if err != nil || len(rows) != 1 || rows[0].Title != "小二的生日" || rows[0].Repeat != "annual" {
		t.Fatal(rows, err)
	}
	id := rows[0].ID
	ask(1, "我的地区改成湖北武汉洪山区，以后我的天气只要PM2.5。另外删除小二的生日倒计时。")
	partner, _ = s.DB.GetUserProfile(ctx, users[1].ID)
	if partner.Region.District != "洪山区" {
		t.Fatal("region correction stale", partner.Region)
	}
	metrics, _ = s.DB.GetCarePreference(ctx, b.SessionID, users[1].ID)
	if strings.Join(metrics, ",") != "pm25" {
		t.Fatal("corrected metrics not persisted", metrics)
	}
	rows, _, err = s.DB.ListCountdowns(ctx, b.SessionID, "", time.Now())
	if err != nil || len(rows) != 0 {
		t.Fatal("countdown deletion did not remove database card", rows, err)
	}
	tombstone, _ := s.DB.GetMemoryRecord(ctx, dbop.MemoryID(b.SessionID, dbop.CountdownMemoryPath(id)), b.SessionID)
	if tombstone == nil || tombstone.Operation != "delete" {
		t.Fatal("countdown memory still current")
	}
}
