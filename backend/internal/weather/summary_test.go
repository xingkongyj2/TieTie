package weather

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"tietie/backend/internal/regions"
)

func TestNightBriefKeepsPracticalTakeawaysAndPreservesDetailedForecast(t *testing.T) {
	now := time.Date(2026, 10, 2, 21, 0, 0, 0, Shanghai)
	region, _ := regions.Resolve("420000", "420100", "420111")
	f := Forecast{FetchedAt: now, AQILabel: "中国 AQI", Days: []Day{
		{Date: "2026-10-02", Min: 15, Max: 19, RainChance: number(30)},
		{Date: "2026-10-03", Min: 17, Max: 19, Code: 61, RainChance: number(98), Wind: number(7), UV: number(2)},
	}}
	for _, clock := range []string{"07:00", "12:00", "18:00", "22:00"} {
		f.Hours = append(f.Hours, Hour{Time: "2026-10-03T" + clock, Temperature: number(18), FeelsLike: number(20), Humidity: number(99), Visibility: number(7092), AQI: number(53), UV: number(2)})
	}
	card, err := Analyze(f, region, "night", now)
	if err != nil {
		t.Fatal(err)
	}
	view := Select(card, []int64{1, 2}, []string{"小一", "小二"}, nil)
	if len(view.Summary) != 1 || !strings.Contains(view.Summary[0], "带伞") || len(view.Comparisons) < 8 {
		t.Fatal("default briefing still dumps routine values, or loses analysis", view)
	}
	card.Views = []View{view}
	body, err := json.Marshal(card)
	var stored Card
	if err != nil || json.Unmarshal(body, &stored) != nil || len(stored.Hours) != 4 || len(stored.Comparisons) != len(card.Comparisons) || len(stored.Views[0].Comparisons) != len(view.Comparisons) || stored.PreviousDay == nil {
		t.Fatal("presentation summary discarded historical detail", err)
	}
	custom := Select(card, []int64{1}, []string{"小一"}, []string{"humidity", "aqi"})
	joined := strings.Join(custom.Summary, " ")
	if !strings.Contains(joined, "湿度") || !strings.Contains(joined, "中国 AQI") || strings.Contains(joined, "降水") || custom.Clothing != "" {
		t.Fatal("explicit interests were hidden or broadened", custom)
	}
}

func TestNightBriefPrioritizesUnusualConditionsAndNeverInventsComparisons(t *testing.T) {
	now := time.Date(2026, 10, 2, 21, 0, 0, 0, Shanghai)
	region, _ := regions.Resolve("420000", "420100", "420111")
	f := Forecast{FetchedAt: now, Days: []Day{{Date: "2026-10-03", Min: 10, Max: 20, Code: 45, RainChance: number(98), Wind: number(40), UV: number(7)}}, Hours: []Hour{{Time: "2026-10-03T07:00", AQI: number(140), PM25: number(80), Visibility: number(500)}}}
	card, err := Analyze(f, region, "night", now)
	if err != nil {
		t.Fatal(err)
	}
	view := Select(card, []int64{1}, []string{"小一"}, nil)
	joined := strings.Join(view.Summary, " ")
	if len(view.Summary) != 3 || !strings.Contains(joined, "AQI") || !strings.Contains(joined, "PM2.5") || !strings.Contains(joined, "雾") || strings.Contains(joined, "前一天") {
		t.Fatal("abnormal conditions buried, or missing history invented", view.Summary)
	}
	card.CurrentAir = &AirSnapshot{PM25: number(12), RetrievedAt: now}
	view = Select(card, []int64{1}, []string{"小一"}, []string{"rain"})
	if len(view.Summary) != 1 || !strings.Contains(view.Summary[0], "带伞") || strings.Contains(view.Summary[0], "AQI") {
		t.Fatal("personalization ignored", view)
	}
	card.Hours, card.Alerts, card.AlertMetrics = nil, nil, nil
	card.Comparisons = nil
	view = Select(card, []int64{1}, []string{"小一"}, []string{"pm25"})
	if len(view.Summary) != 1 || !strings.Contains(view.Summary[0], "12") || !strings.Contains(view.Summary[0], "不代表明天") {
		t.Fatal("real-time PM2.5 presented as a next-day forecast", view)
	}
}
