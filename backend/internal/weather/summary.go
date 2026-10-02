package weather

import (
	"fmt"
	"math"
)

// Brief selects at most three useful takeaways. All measurements, comparisons
// and hourly rows remain in the stored card, independent of this presentation.
func Brief(c Card, v View) []string {
	lines := []string{}
	seen := map[string]bool{}
	has := func(metric string) bool {
		for _, selected := range v.Metrics {
			if metric == selected {
				return true
			}
		}
		return false
	}
	add := func(metric, text string) {
		if text != "" && !seen[metric] && len(lines) < 3 {
			lines = append(lines, text)
			seen[metric] = true
		}
	}
	// Unusual air and visibility conditions take precedence over routine values.
	for _, metric := range []string{"aqi", "pm25", "fog", "visibility", "wind"} {
		for i, key := range v.AlertMetrics {
			if key == metric && i < len(v.Alerts) {
				add(metric, v.Alerts[i])
			}
		}
	}
	if has("temperature") && c.PreviousDay != nil && (math.Abs(c.Day.Max-c.PreviousDay.Max) >= 3 || math.Abs(c.Day.Min-c.PreviousDay.Min) >= 3) {
		add("temperature", c.Comparison)
	}
	for _, metric := range []string{"rain", "temperature", "uv"} {
		for i, key := range v.AlertMetrics {
			if key == metric && i < len(v.Alerts) {
				add(metric, v.Alerts[i])
			}
		}
	}
	// Explicit interests still get a concise value even when that metric is normal.
	if len(v.Metrics) < len(DefaultMetrics) {
		for _, comparison := range v.Comparisons {
			if comparison.Metric != "temperature" && comparison.Metric != "rain" {
				add(comparison.Metric, comparison.Text)
			}
		}
		if has("pm25") && c.CurrentAir != nil && c.CurrentAir.PM25 != nil {
			add("pm25", fmt.Sprintf("此刻 PM2.5 约 %.0f μg/m³（实时值，不代表明天）。", *c.CurrentAir.PM25))
		}
		if has("rain") && c.Day.RainChance != nil && !seen["rain"] {
			add("rain", fmt.Sprintf("降水概率最高 %.0f%%，出门前留意是否需要带伞。", *c.Day.RainChance))
		}
	}
	if len(lines) == 0 && has("temperature") && c.PreviousDay != nil {
		add("temperature", "气温与前一天接近，按舒适度增减衣物就好。")
	}
	return lines
}
