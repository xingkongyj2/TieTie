package weather

import (
	"strings"
	"testing"
	"tietie/backend/internal/regions"
	"time"
)

func number(x float64) *float64 { return &x }
func TestAnalysisDatesTemperatureChangesFogAirAndMissingValues(t *testing.T) {
	now := time.Date(2026, 10, 2, 21, 0, 0, 0, Shanghai)
	region, _ := regions.Resolve("420000", "420100", "420111")
	f := Forecast{Precision: "district", FetchedAt: now, Days: []Day{{Date: "2026-10-01", Min: 18, Max: 30}, {Date: "2026-10-02", Min: 15, Max: 25}, {Date: "2026-10-03", Min: 10, Max: 20, Code: 45, RainChance: number(85), Wind: number(40), UV: number(7)}}, Hours: []Hour{{Time: "2026-10-03T07:00", Temperature: number(11), FeelsLike: number(9), Visibility: number(600), PM25: number(80), AQI: number(140), Code: number(45)}, {Time: "2026-10-02T07:00", PM25: number(800)}}}
	night, err := Analyze(f, region, "night", now)
	if err != nil || night.Day.Date != "2026-10-03" || len(night.Hours) != 1 || night.Hours[0].Time != "2026-10-03T07:00" {
		t.Fatal(night, err)
	}
	if !strings.Contains(night.Comparison, "降低 5°C") || !night.AirAvailable || !strings.Contains(night.Clothing, "保暖") {
		t.Fatal(night)
	}
	joined := strings.Join(night.Alerts, " ")
	for _, term := range []string{"温差 10°C", "85%", "有雾", "600 米", "80 μg", "140", "风较大", "紫外线"} {
		if !strings.Contains(joined, term) {
			t.Fatal("missing advice", term, joined)
		}
	}
	if strings.Contains(joined, "800") {
		t.Fatal("yesterday's PM was used")
	}
	morning, err := Analyze(f, region, "morning", now)
	if err != nil || morning.Day.Date != "2026-10-02" {
		t.Fatal(morning, err)
	}
	f.Hours = nil
	missing, err := Analyze(f, region, "night", now)
	if err != nil || missing.AirAvailable || strings.Contains(strings.Join(missing.Alerts, " "), "PM2.5") {
		t.Fatal("missing air became zero/good", missing, err)
	}
	if _, err := Analyze(f, region, "night", now.Add(2*time.Hour)); err == nil {
		t.Fatal("stale forecast accepted")
	}
	f.Days[2].Min = 50
	if _, err := Analyze(f, region, "night", now); err == nil {
		t.Fatal("invalid min/max accepted")
	}
}
func TestLocationRequiresCodeAndNameAndMakesCityFallbackExplicit(t *testing.T) {
	region, _ := regions.Resolve("420000", "420100", "420111")
	p, precision, err := Locate(region)
	if err != nil || precision != "district" || p.Latitude < 30 || p.Latitude > 31 {
		t.Fatal(p, precision, err)
	}
	region.District = "名字不一致"
	_, precision, err = Locate(region)
	if err != nil || precision != "city" {
		t.Fatal(precision, err)
	}
	region.City = "伪造城市"
	if _, _, err := Locate(region); err == nil {
		t.Fatal("unverified location resolved")
	}
}

func TestHourlyComparisonRequiresIdenticalForecastCoverage(t *testing.T) {
	f := Forecast{Days: []Day{{Date: "2026-10-02", Min: 10, Max: 20}, {Date: "2026-10-03", Min: 10, Max: 22}}, Hours: []Hour{
		{Time: "2026-10-02T22:00", Humidity: number(80)},
		{Time: "2026-10-03T07:00", Humidity: number(40)},
		{Time: "2026-10-03T22:00", Humidity: number(60)},
	}}
	check := func(wantChange bool) {
		t.Helper()
		for _, c := range comparisons(f, f.Days[1]) {
			if c.Metric == "humidity" {
				changed := strings.Contains(c.Text, "比前一天")
				if changed != wantChange {
					t.Fatal("incomparable forecast window", c.Text)
				}
				if wantChange && !strings.Contains(c.Text, "20 个百分点") {
					t.Fatal("percentage difference unit", c.Text)
				}
				return
			}
		}
		t.Fatal("missing humidity")
	}
	check(false)
	f.Hours = append(f.Hours, Hour{Time: "2026-10-02T07:00", Humidity: number(50)})
	check(true)
}
