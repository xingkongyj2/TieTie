package weather

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"tietie/backend/internal/regions"
	"time"
)

func TestQWeatherAuthenticationUnitsDatesAirAndCache(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("X-QW-Api-Key") != "fixture-key" || r.URL.Query().Get("key") != "" {
			t.Error("credential missing or placed in query")
		}
		value := func(v any, u string) map[string]any { return map[string]any{"value": v, "unit": u} }
		metadata := map[string]any{"attributions": []string{"https://developer.qweather.com/attribution.html"}}
		var body any
		switch {
		case strings.Contains(r.URL.Path, "/daily/"):
			body = map[string]any{"metadata": metadata, "days": []any{map[string]any{"forecastStartTime": "2026-10-02T16:00Z", "temperatureMin": value(15, "°C"), "temperatureMax": value(23, "°C"), "daytime": map[string]any{"condition": map[string]any{"code": "305", "text": "小雨"}, "wind": map[string]any{"speed": value(5, "m/s")}, "precipitation": map[string]any{"probability": .7}}}}}
		case strings.Contains(r.URL.Path, "/weather/v1/current/"):
			body = map[string]any{"metadata": metadata, "condition": map[string]any{"code": "305", "text": "小雨"}, "temperature": value(18, "°C"), "feelsLike": value(17, "°C"), "humidity": .8, "visibility": value(8000, "m"), "wind": map[string]any{"speed": value(2, "m/s")}}
		case strings.Contains(r.URL.Path, "/weather/v1/hourly/"):
			if r.URL.Query().Get("hours") != "48" {
				t.Error("night needs tomorrow's full hours")
			}
			body = map[string]any{"metadata": metadata, "hours": []any{map[string]any{"forecastTime": "2026-10-02T23:00Z", "temperature": value(nil, "°C"), "feelsLike": value(14, "°C"), "visibility": value(800, "m"), "humidity": .85, "wind": map[string]any{"speed": value(3, "m/s")}, "condition": map[string]any{"code": "501", "text": "雾"}}}}
		case strings.Contains(r.URL.Path, "/airquality/v1/hourly/"):
			body = map[string]any{"metadata": metadata, "hours": []any{map[string]any{"forecastTime": "2026-10-02T23:00Z", "indexes": []any{map[string]any{"code": "us-epa", "aqi": 900}, map[string]any{"code": "cn-mee", "aqi": 75}}}}}
		default:
			body = map[string]any{"metadata": metadata, "indexes": []any{map[string]any{"code": "cn-mee", "aqi": 43}}, "pollutants": []any{map[string]any{"code": "pm2p5", "concentration": value(17, "μg/m³")}}}
		}
		w.Header().Set("Content-Encoding", "gzip")
		gz := gzip.NewWriter(w)
		_ = json.NewEncoder(gz).Encode(body)
		_ = gz.Close()
	}))
	defer srv.Close()
	client := NewQWeather(QWeatherConfig{Host: srv.URL, APIKey: "fixture-key"})
	r, _ := regions.ResolveNames("湖北", "武汉", "洪山")
	for i := 0; i < 2; i++ {
		f, err := client.Forecast(context.Background(), r)
		if err != nil {
			t.Fatal(err)
		}
		if f.Source != "和风天气" || len(f.Days) != 1 || f.Days[0].Date != "2026-10-03" || *f.Days[0].RainChance != 70 || *f.Days[0].Wind != 18 || f.Days[0].Description != "小雨" {
			t.Fatal(f)
		}
		h := f.Hours[0]
		if h.Time != "2026-10-03T07:00" || h.Temperature != nil || h.PM25 != nil || *h.AQI != 75 || *h.Humidity != 85 || *h.Code != 45 {
			t.Fatal("air/units/clock fabricated", h)
		}
		if f.CurrentAir == nil || *f.CurrentAir.PM25 != 17 || len(f.Attributions) != 1 || f.Days[0].Max != 23 || f.Attributions[0] != "https://developer.qweather.com/attribution.html" {
			t.Fatal(f)
		}
		if i == 0 {
			f.Days[0].Max = 99
			*f.Hours[0].Humidity = 999
			*f.CurrentAir.PM25 = 999
			f.Attributions[0] = "https://invalid.example"
		}
	}
	if calls.Load() != 4 {
		t.Fatal("cached forecast fetched again", calls.Load())
	}
	for i := 0; i < 2; i++ {
		f, err := client.ForecastFresh(context.Background(), r)
		if err != nil || f.CurrentWeather == nil || *f.CurrentWeather.Temperature != 18 || *f.CurrentWeather.Humidity != 80 || *f.CurrentWeather.Wind != 7.2 {
			t.Fatal("fresh current data missing or units wrong", f, err)
		}
		*f.CurrentWeather.Temperature = 999
	}
	if calls.Load() != 14 {
		t.Fatal("manual query reused cache", calls.Load())
	}
	cached, err := client.Forecast(context.Background(), r)
	if err != nil || cached.CurrentWeather == nil || *cached.CurrentWeather.Temperature != 18 || calls.Load() != 14 {
		t.Fatal("current snapshot mutated cached data", cached, err)
	}

}
func TestSelectedMetricsExcludeOtherDetailsAndClimateRequiresForecast(t *testing.T) {
	now := time.Date(2026, 10, 2, 21, 0, 0, 0, Shanghai)
	r, _ := regions.ResolveNames("湖北", "武汉", "洪山")
	f := Forecast{FetchedAt: now, Source: "和风天气", AQILabel: "中国 AQI", Days: []Day{{Date: "2026-10-02", Min: 19, Max: 25}, {Date: "2026-10-03", Min: 10, Max: 21, RainChance: number(80)}}, Hours: []Hour{{Time: "2026-10-03T07:00", Humidity: number(85), PM25: number(90), AQI: number(120)}}}
	card, err := Analyze(f, r, "night", now)
	if err != nil {
		t.Fatal(err)
	}
	selected := Select(card, []int64{1}, []string{"一"}, []string{"rain", "temperature"})
	if selected.Clothing != "" || selected.LocalAdvice != "" || !strings.Contains(selected.PreferenceHint, "温度、降雨") {
		t.Fatal(selected)
	}
	for _, comparison := range selected.Comparisons {
		if comparison.Metric != "rain" && comparison.Metric != "temperature" {
			t.Fatal("unselected comparison", comparison)
		}
	}
	for _, alert := range selected.Alerts {
		if strings.Contains(alert, "PM2.5") || strings.Contains(alert, "AQI") {
			t.Fatal("unselected alert", alert)
		}
	}
	if !strings.Contains(card.LocalAdvice, "湿度高") {
		t.Fatal("city/actual humidity advice missing", card.LocalAdvice)
	}
	card.Day.Min, card.Day.Max, card.Day.RainChance = 20, 25, nil
	f.Hours = nil
	if advice := LocalAdvice(r, card, f); advice != "" {
		t.Fatal("city reputation made up weather", advice)
	}
	if len(climateCatalog.Cities) != 394 {
		t.Fatal("city coverage lost")
	}
}
