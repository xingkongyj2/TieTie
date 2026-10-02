package weather

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

var DefaultMetrics = []string{"temperature", "feels_like", "rain", "wind", "humidity", "visibility", "fog", "pm25", "aqi", "uv", "clothing"}
var metricNames = map[string]string{"temperature": "温度", "feels_like": "体感", "rain": "降雨", "wind": "风速", "humidity": "湿度", "visibility": "能见度", "fog": "雾", "pm25": "PM2.5", "aqi": "空气质量", "uv": "紫外线", "clothing": "穿搭"}

func NormalizeMetrics(metrics []string) ([]string, error) {
	if len(metrics) == 0 {
		return append([]string{}, DefaultMetrics...), nil
	}
	if len(metrics) > len(DefaultMetrics) {
		return nil, errors.New("天气指标过多")
	}
	seen := map[string]bool{}
	for _, key := range metrics {
		if _, ok := metricNames[key]; !ok {
			return nil, errors.New("不支持的天气指标")
		}
		seen[key] = true
	}
	out := []string{}
	for _, key := range DefaultMetrics {
		if seen[key] {
			out = append(out, key)
		}
	}
	return out, nil
}
func MetricLabels(metrics []string) []string {
	out := []string{}
	for _, key := range metrics {
		out = append(out, metricNames[key])
	}
	return out
}

type Comparison struct {
	Metric string `json:"metric"`
	Text   string `json:"text"`
}
type View struct {
	Summary        []string     `json:"summary"`
	AlertMetrics   []string     `json:"-"`
	RecipientIDs   []int64      `json:"recipientIds"`
	RecipientNames []string     `json:"recipientNames"`
	Metrics        []string     `json:"metrics"`
	Comparisons    []Comparison `json:"comparisons"`
	Alerts         []string     `json:"alerts"`
	Clothing       string       `json:"clothing,omitempty"`
	LocalAdvice    string       `json:"localAdvice,omitempty"`
	PreferenceHint string       `json:"preferenceHint"`
}

func Select(c Card, ids []int64, names, metrics []string) View {
	normalized, _ := NormalizeMetrics(metrics)
	v := View{RecipientIDs: ids, RecipientNames: names, Metrics: normalized, Comparisons: []Comparison{}, Alerts: []string{}}
	has := func(key string) bool {
		for _, k := range normalized {
			if k == key {
				return true
			}
		}
		return false
	}
	for _, entry := range c.Comparisons {
		if has(entry.Metric) {
			v.Comparisons = append(v.Comparisons, entry)
		}
	}
	for i, text := range c.Alerts {
		if i < len(c.AlertMetrics) && (has(c.AlertMetrics[i]) || c.AlertMetrics[i] == "general") {
			v.Alerts = append(v.Alerts, text)
			v.AlertMetrics = append(v.AlertMetrics, c.AlertMetrics[i])
		}
	}
	if c.CurrentAir != nil && c.CurrentAir.PM25 != nil && *c.CurrentAir.PM25 >= 35 && has("pm25") {
		v.Alerts = append(v.Alerts, fmt.Sprintf("此刻 PM2.5 约 %.0f μg/m³，出门前留意当地空气质量，户外活动可选空气更好的时段。", *c.CurrentAir.PM25))
		v.AlertMetrics = append(v.AlertMetrics, "pm25")
	}
	if c.CurrentAir != nil && c.CurrentAir.AQI != nil && *c.CurrentAir.AQI > 100 && has("aqi") {
		v.Alerts = append(v.Alerts, fmt.Sprintf("此刻 %s 约 %.0f，减少长时间高强度户外活动。", c.CurrentAir.AQILabel, *c.CurrentAir.AQI))
		v.AlertMetrics = append(v.AlertMetrics, "aqi")
	}
	if has("clothing") {
		v.Clothing, v.LocalAdvice = c.Clothing, c.LocalAdvice
	}
	if len(normalized) == len(DefaultMetrics) {
		v.PreferenceHint = "想精简一点，可以告诉我：‘以后只看温度和降雨’，我就按你关注的指标提醒。"
	} else {
		v.PreferenceHint = "按你关注的指标：" + strings.Join(MetricLabels(normalized), "、") + "。想调整，直接告诉我就好。"
	}
	v.Summary = Brief(c, v)
	return v
}
func comparisons(f Forecast, day Day) []Comparison {
	result := []Comparison{}
	date, _ := time.Parse("2006-01-02", day.Date)
	prev := date.AddDate(0, 0, -1).Format("2006-01-02")
	line := func(key, label string, now float64, old *float64, unit string) {
		text := fmt.Sprintf("%s %.0f%s", label, now, unit)
		if old != nil {
			diffUnit := unit
			if key == "rain" || key == "humidity" {
				diffUnit = " 个百分点"
			}
			delta := now - *old
			if math.Abs(delta) < .5 {
				text += "，与前一天接近"
			} else if delta > 0 {
				text += fmt.Sprintf("，比前一天增加 %.0f%s", delta, diffUnit)
			} else {
				text += fmt.Sprintf("，比前一天减少 %.0f%s", -delta, diffUnit)
			}
		}
		result = append(result, Comparison{Metric: key, Text: text})
	}
	var before *Day
	for i := range f.Days {
		if f.Days[i].Date == prev {
			before = &f.Days[i]
		}
	}
	var prevMax, prevMin, prevRange, prevRain, prevWind, prevUV *float64
	if before != nil {
		prevMax = &before.Max
		prevMin = &before.Min
		r := before.Max - before.Min
		prevRange = &r
		prevRain, prevWind, prevUV = before.RainChance, before.Wind, before.UV
	}
	line("temperature", "最高温", day.Max, prevMax, "°C")
	line("temperature", "最低温", day.Min, prevMin, "°C")
	line("temperature", "昼夜温差", day.Max-day.Min, prevRange, "°C")
	if day.RainChance != nil {
		line("rain", "降水概率上限", *day.RainChance, prevRain, "%")
	}
	if day.Wind != nil {
		line("wind", "风速上限", *day.Wind, prevWind, " km/h")
	}
	if day.UV != nil {
		line("uv", "紫外线指数上限", *day.UV, prevUV, "")
	}
	for _, metric := range []string{"feels_like", "humidity", "visibility", "pm25", "aqi"} {
		value := func(h Hour) *float64 {
			switch metric {
			case "feels_like":
				return h.FeelsLike
			case "humidity":
				return h.Humidity
			case "visibility":
				return h.Visibility
			case "pm25":
				return h.PM25
			default:
				return h.AQI
			}
		}
		peak := func(date string) (*float64, string) {
			var out *float64
			hours := []string{}
			for _, h := range f.Hours {
				if !strings.HasPrefix(h.Time, date+"T") {
					continue
				}
				p := value(h)
				if p != nil {
					hours = append(hours, strings.TrimPrefix(h.Time, date+"T"))
				}
				if p != nil && (out == nil || metric == "visibility" && *p < *out || metric != "visibility" && *p > *out) {
					v := *p
					out = &v
				}
			}
			sort.Strings(hours)
			return out, strings.Join(hours, ",")
		}
		current, currentHours := peak(day.Date)
		old, previousHours := peak(prev)
		// Unequal forecast coverage is not evidence of a day-over-day change.
		if currentHours != previousHours {
			old = nil
		}
		if current == nil {
			continue
		}
		label, unit := "预报时段"+metricNames[metric]+"最高值", ""
		switch metric {
		case "feels_like":
			unit = "°C"
		case "humidity":
			unit = "%"
		case "visibility":
			label = "预报时段最低能见度"
			unit = " m"
		case "pm25":
			unit = " μg/m³"
		case "aqi":
			label = "预报时段" + f.AQILabel + "最高值"
		}
		line(metric, label, *current, old, unit)
	}
	return result
}
