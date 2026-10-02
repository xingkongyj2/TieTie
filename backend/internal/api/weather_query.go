package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"tietie/backend/internal/conversation"
	"tietie/backend/internal/dbop"
	"tietie/backend/internal/memoryspace"
	"tietie/backend/internal/regions"
	"tietie/backend/internal/weather"
	"time"
)

// Both typed questions and the quick action use the model's validated control
// channel. A query never changes profiles or enables scheduled care modes.
func (s *Server) executeWeatherQuery(ctx context.Context, job dbop.ControlJob, input conversation.Input, action conversation.Action) conversation.ActionResult {
	result := conversation.ActionResult{Key: action.Key, Type: action.Type, Status: "failed", ErrorCode: "weather_unavailable"}
	fail := func(message string) conversation.ActionResult { result.Message = message; return result }
	if err := conversation.ValidateV2Action(action); err != nil {
		return fail("天气查询参数无效，请重新说明要查的地区和日期。")
	}
	_, binding, err := s.conversationContext(ctx, job.SessionID, input.UserID)
	if err != nil || binding == nil || !binding.CreatedAt.Equal(job.BindingCreatedAt) {
		return fail("当前空间已变化，请重新打开后再查询天气。")
	}
	spaceID := binding.SessionID
	if channel, channelErr := s.DB.GetPrivateChannel(ctx, job.SessionID); channelErr != nil {
		return fail("地区信息暂时没加载出来，请稍后重试。")
	} else if channel != nil {
		spaceID = channel.SpaceID
	}
	members, _, err := s.DB.CareMembers(ctx, spaceID)
	if err != nil {
		return fail("地区信息暂时没加载出来，请稍后重试。")
	}
	wanted := map[int64]bool{}
	if len(action.RecipientIDs) == 0 {
		wanted[input.UserID] = true
	}
	for _, id := range action.RecipientIDs {
		if id != binding.UserA && id != binding.UserB {
			return fail("只能查询当前空间成员的天气。")
		}
		wanted[id] = true
	}
	var location *regions.Location
	if action.Region != nil {
		resolved, err := regions.ResolveNames(action.Region.Province, action.Region.City, action.Region.District)
		if err != nil {
			return fail("这个地区没有匹配到，请说明省份和城市。")
		}
		location = &resolved
	}
	now := time.Now()
	mode := "query"
	if action.WeatherWhen == "tomorrow" {
		mode = "query_tomorrow"
	} else if action.WeatherWhen == "today" {
		mode = "query_today"
	}
	groups := map[regions.Location]int{}
	missing := []string{}
	cards := []weather.Card{}
	for _, member := range members {
		if !wanted[member.UserID] {
			continue
		}
		region := member.Region
		if location != nil {
			region = *location
		}
		if region.CityCode == "" {
			missing = append(missing, "@"+member.Name)
			continue
		}
		index, exists := groups[region]
		if !exists {
			provider := s.weatherProvider()
			var forecast weather.Forecast
			if fresh, ok := provider.(weather.FreshProvider); ok {
				forecast, err = fresh.ForecastFresh(ctx, region)
			} else {
				forecast, err = provider.Forecast(ctx, region)
			}
			if err != nil {
				return fail("天气暂时没查到，请稍后再试；不会使用旧数据冒充本次结果。")
			}
			card, err := weather.Analyze(forecast, region, mode, now)
			if err != nil {
				return fail("当前没有可用的目标日期预报，请稍后重试。")
			}
			index = len(cards)
			groups[region] = index
			cards = append(cards, card)
		}
		card := &cards[index]
		card.RecipientIDs = append(card.RecipientIDs, member.UserID)
		card.RecipientNames = append(card.RecipientNames, member.Name)
		metrics := []string{}
		if member.PreferenceKey != "" {
			metrics = strings.Split(member.PreferenceKey, ",")
		}
		view := weather.Select(*card, []int64{member.UserID}, []string{member.Name}, metrics)
		merged := false
		for i := range card.Views {
			if strings.Join(card.Views[i].Metrics, ",") == strings.Join(view.Metrics, ",") {
				card.Views[i].RecipientIDs = append(card.Views[i].RecipientIDs, member.UserID)
				card.Views[i].RecipientNames = append(card.Views[i].RecipientNames, member.Name)
				merged = true
				break
			}
		}
		if !merged {
			card.Views = append(card.Views, view)
		}
	}
	notice := ""
	if len(missing) > 0 {
		notice = fmt.Sprintf("%s 还没填写地区，可以在“我的”里填写，也可以直接告诉我“我在湖北潜江”或“TA在浙江杭州”。", strings.Join(missing, " "))
	}
	if len(cards) == 0 {
		result.ErrorCode = "weather_region_required"
		return fail(notice)
	}
	// Avoid publishing a forecast for an old region after a simultaneous edit.
	current, currentBinding, err := s.DB.CareMembers(ctx, spaceID)
	if err != nil || currentBinding == nil || !currentBinding.CreatedAt.Equal(binding.CreatedAt) || len(current) != len(members) {
		return fail("地区或空间已变化，请再查一次天气。")
	}
	for i := range current {
		if current[i] != members[i] {
			return fail("地区信息刚刚更新，请再查一次天气。")
		}
	}
	cards[0].QueryNotice = notice
	for i := range cards {
		if mode == "query" && cards[i].CurrentWeather == nil {
			cards[i].QueryNotice = strings.TrimSpace(cards[i].QueryNotice + " 实时天气暂未获取，以下展示今天的预报。")
		}
	}
	expires := now.Add(2 * time.Hour)
	if mode == "query_tomorrow" {
		expires = time.Date(now.In(weather.Shanghai).Year(), now.In(weather.Shanghai).Month(), now.In(weather.Shanghai).Day()+2, 0, 0, 0, 0, weather.Shanghai)
	}
	body, _ := json.Marshal(map[string]any{"schemaVersion": 1, "kind": "fact", "category": "realtime", "content": "主动查询的天气快照；当前天气与日期预报分别标明，不代表后续实时天气。", "cards": cards, "sourceType": "weather_query", "generatedAt": now, "expiresAt": expires, "expired": false, "sourceUserId": input.UserID, "sourceRequestId": job.RequestID})
	memory, err := s.DB.ApplyMemoryAction(ctx, job.ID+"/"+action.Key, dbop.MemoryRecord{SessionID: job.SessionID, Path: memoryspace.FactPath("realtime", "space", 0, "weather_query_"+job.RequestID+"_"+action.Key), Scope: "space", Category: "realtime", ExpiresAt: &expires, SourceUserID: input.UserID, SourceRequestID: job.RequestID, Storage: "database_and_memory", Operation: "upsert", PendingContent: string(body), BindingCreatedAt: job.BindingCreatedAt})
	if err != nil {
		return fail("查询结果暂时没保存好，请重试。")
	}
	result.WeatherCards = cards
	result.Status, result.ErrorCode, result.DatabaseStatus, result.MemoryKey, result.MemoryStatus = "succeeded", "", "saved", memory.ID, "pending"
	result.Message = "已重新查询天气，本次结果显示在卡片中。" + notice
	// Weather remains usable if cloud memory is temporarily unavailable; the
	// existing outbox retries synchronization independently.
	if err := s.syncMemoryLocked(ctx, *memory); err == nil {
		result.MemoryStatus = "synced"
	}
	return result
}
