// Package weather keeps dated forecasts distinct from separately labeled current air data.
package weather

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"tietie/backend/internal/regions"
	"time"
)

var Shanghai = time.FixedZone("Asia/Shanghai", 8*3600)
var ErrLocation = errors.New("这个地区暂时无法取得可靠天气坐标，请选择可查询的城市")

type Point struct {
	Name      string  `json:"name"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

//go:embed coordinates.json
var coordinateJSON []byte
var coordinates map[string]Point

func init() {
	if err := json.Unmarshal(coordinateJSON, &coordinates); err != nil {
		panic(err)
	}
}

type Hour struct {
	Humidity    *float64 `json:"humidity"`
	UV          *float64 `json:"uv"`
	Time        string   `json:"time"`
	Temperature *float64 `json:"temperature"`
	FeelsLike   *float64 `json:"feelsLike"`
	RainChance  *float64 `json:"rainChance"`
	Wind        *float64 `json:"wind"`
	Visibility  *float64 `json:"visibility"`
	PM25        *float64 `json:"pm25"`
	AQI         *float64 `json:"aqi"`
	Code        *float64 `json:"code"`
}
type Day struct {
	Description string   `json:"description,omitempty"`
	Date        string   `json:"date"`
	Min         float64  `json:"min"`
	Max         float64  `json:"max"`
	Code        int      `json:"code"`
	RainChance  *float64 `json:"rainChance"`
	Wind        *float64 `json:"wind"`
	UV          *float64 `json:"uv"`
}
type Reminder struct {
	Title        string  `json:"title"`
	Time         string  `json:"time"`
	RecipientIDs []int64 `json:"recipientIds"`
}
type Card struct {
	QueryNotice    string           `json:"queryNotice,omitempty"`
	CurrentWeather *WeatherSnapshot `json:"currentWeather,omitempty"`
	PreviousDay    *Day             `json:"previousDay,omitempty"`
	CurrentAir     *AirSnapshot     `json:"currentAir,omitempty"`
	AirComparison  string           `json:"airComparison,omitempty"`
	AQILabel       string           `json:"aqiLabel"`
	Attributions   []string         `json:"attributions,omitempty"`
	Views          []View           `json:"views,omitempty"`
	Comparisons    []Comparison     `json:"comparisons,omitempty"`
	AlertMetrics   []string         `json:"-"`
	LocalAdvice    string           `json:"localAdvice,omitempty"`
	Mode           string           `json:"mode"`
	Region         regions.Location `json:"region"`
	Precision      string           `json:"precision"`
	RecipientIDs   []int64          `json:"recipientIds"`
	RecipientNames []string         `json:"recipientNames"`
	Day            Day              `json:"day"`
	Description    string           `json:"description"`
	Comparison     string           `json:"comparison"`
	Alerts         []string         `json:"alerts"`
	Clothing       string           `json:"clothing"`
	Hours          []Hour           `json:"hours"`
	Reminders      []Reminder       `json:"reminders"`
	MoreReminders  int              `json:"moreReminders"`
	AirAvailable   bool             `json:"airAvailable"`
	GeneratedAt    time.Time        `json:"generatedAt"`
	Source         string           `json:"source"`
}
type AirSnapshot struct {
	RetrievedAt time.Time `json:"retrievedAt"`
	PM25        *float64  `json:"pm25"`
	AQI         *float64  `json:"aqi"`
	AQILabel    string    `json:"aqiLabel"`
}
type WeatherSnapshot struct {
	RetrievedAt time.Time `json:"retrievedAt"`
	Temperature *float64  `json:"temperature"`
	FeelsLike   *float64  `json:"feelsLike"`
	Humidity    *float64  `json:"humidity"`
	Wind        *float64  `json:"wind"`
	Visibility  *float64  `json:"visibility"`
	Description string    `json:"description"`
	Code        int       `json:"code"`
}
type Forecast struct {
	CurrentWeather *WeatherSnapshot
	CurrentAir     *AirSnapshot
	Source         string
	AQILabel       string
	Attributions   []string
	Days           []Day
	Hours          []Hour
	AirAvailable   bool
	Precision      string
	FetchedAt      time.Time
}
type Provider interface {
	Forecast(context.Context, regions.Location) (Forecast, error)
}

// Manual queries bypass the scheduled forecast cache.
type FreshProvider interface {
	ForecastFresh(context.Context, regions.Location) (Forecast, error)
}

func Locate(region regions.Location) (Point, string, error) {
	if region.DistrictCode != "" {
		if p, ok := coordinates[region.DistrictCode]; ok && p.Name == region.District {
			return toWGS(p), "district", nil
		}
	}
	if p, ok := coordinates[region.CityCode]; ok && p.Name == region.City {
		return toWGS(p), "city", nil
	}
	return Point{}, "", ErrLocation
}

// GCJ-02 to approximately WGS-84, adequate for weather forecast grid centroids.
func toWGS(p Point) Point {
	if p.Longitude < 72.004 || p.Longitude > 137.8347 || p.Latitude < .8293 || p.Latitude > 55.8271 {
		return p
	}
	x, y := p.Longitude-105, p.Latitude-35
	lat := -100 + 2*x + 3*y + .2*y*y + .1*x*y + .2*math.Sqrt(math.Abs(x))
	lon := 300 + x + 2*y + .1*x*x + .1*x*y + .1*math.Sqrt(math.Abs(x))
	for _, target := range []*float64{&lat, &lon} {
		*target += (20*math.Sin(6*x*math.Pi) + 20*math.Sin(2*x*math.Pi)) * 2 / 3
	}
	lat += (20*math.Sin(y*math.Pi)+40*math.Sin(y/3*math.Pi))*2/3 + (160*math.Sin(y/12*math.Pi)+320*math.Sin(y*math.Pi/30))*2/3
	lon += (20*math.Sin(x*math.Pi)+40*math.Sin(x/3*math.Pi))*2/3 + (150*math.Sin(x/12*math.Pi)+300*math.Sin(x/30*math.Pi))*2/3
	rad := p.Latitude / 180 * math.Pi
	magic := 1 - .00669342162296594323*math.Sin(rad)*math.Sin(rad)
	lat = lat * 180 / ((6378245 * (1 - .00669342162296594323)) / (magic * math.Sqrt(magic)) * math.Pi)
	lon = lon * 180 / (6378245 / math.Sqrt(magic) * math.Cos(rad) * math.Pi)
	p.Latitude -= lat
	p.Longitude -= lon
	return p
}

func Description(code int) string {
	switch {
	case code == 0:
		return "晴"
	case code == 1 || code == 2:
		return "晴间多云"
	case code == 3:
		return "阴"
	case code == 45 || code == 48:
		return "雾"
	case code >= 51 && code <= 57:
		return "毛毛雨"
	case code >= 61 && code <= 67:
		return "雨"
	case code >= 71 && code <= 77:
		return "雪"
	case code >= 80 && code <= 82:
		return "阵雨"
	case code >= 85 && code <= 86:
		return "阵雪"
	case code >= 95:
		return "雷雨"
	}
	return "天气状况待确认"
}
func Analyze(f Forecast, region regions.Location, mode string, now time.Time) (Card, error) {
	dayTime := now.In(Shanghai)
	if mode == "night" || mode == "query_tomorrow" {
		dayTime = dayTime.AddDate(0, 0, 1)
	}
	date := dayTime.Format("2006-01-02")
	c := Card{CurrentWeather: f.CurrentWeather, Mode: mode, Region: region, Precision: f.Precision, GeneratedAt: f.FetchedAt, Source: f.Source, AQILabel: f.AQILabel, Attributions: f.Attributions, Hours: []Hour{}, Alerts: []string{}, Reminders: []Reminder{}}
	if c.Source == "" {
		c.Source = "和风天气"
	}
	if c.AQILabel == "" {
		c.AQILabel = "中国 AQI"
	}
	alert := func(metric, text string) {
		c.Alerts = append(c.Alerts, text)
		c.AlertMetrics = append(c.AlertMetrics, metric)
	}
	found := false
	for _, d := range f.Days {
		if d.Date == date {
			c.Day = d
			found = true
		}
	}
	if !found || now.Sub(f.FetchedAt) > time.Hour || f.FetchedAt.After(now.Add(5*time.Minute)) || c.Day.Min > c.Day.Max || c.Day.Min < -100 || c.Day.Max > 70 {
		return c, errors.New("预报已过期或缺少目标日期")
	}
	c.Description = Description(c.Day.Code)
	if c.Day.Description != "" {
		c.Description = c.Day.Description
	}
	previous := dayTime.AddDate(0, 0, -1).Format("2006-01-02")
	for _, d := range f.Days {
		if d.Date == previous {
			previousDay := d
			c.PreviousDay = &previousDay
			delta := c.Day.Max - d.Max
			switch {
			case delta >= 3:
				c.Comparison = fmt.Sprintf("最高温比前一天升高 %.0f°C", delta)
			case delta <= -3:
				c.Comparison = fmt.Sprintf("最高温比前一天降低 %.0f°C", -delta)
			default:
				c.Comparison = "最高温与前一天接近"
			}
			lowDelta := c.Day.Min - d.Min
			if lowDelta <= -3 {
				c.Comparison += fmt.Sprintf("，最低温降低 %.0f°C", -lowDelta)
			} else if lowDelta >= 3 {
				c.Comparison += fmt.Sprintf("，最低温升高 %.0f°C", lowDelta)
			}
		}
	}
	minVisibility := math.Inf(1)
	maxPM, maxAQI := 0.0, 0.0
	hasPM, hasAQI := false, false
	previousPM := 0.0
	previousHasPM := false
	fog := c.Day.Code == 45 || c.Day.Code == 48
	for _, h := range f.Hours {
		if strings.HasPrefix(h.Time, previous+"T") && h.PM25 != nil {
			previousHasPM = true
			previousPM = math.Max(previousPM, *h.PM25)
		}
		if !strings.HasPrefix(h.Time, date+"T") {
			continue
		}
		if h.Visibility != nil && *h.Visibility < minVisibility {
			minVisibility = *h.Visibility
		}
		if h.Code != nil && (*h.Code == 45 || *h.Code == 48) {
			fog = true
		}
		if h.PM25 != nil {
			hasPM = true
			maxPM = math.Max(maxPM, *h.PM25)
		}
		if h.AQI != nil {
			hasAQI = true
			maxAQI = math.Max(maxAQI, *h.AQI)
		}
		if strings.HasSuffix(h.Time, "T07:00") || strings.HasSuffix(h.Time, "T12:00") || strings.HasSuffix(h.Time, "T18:00") || strings.HasSuffix(h.Time, "T22:00") {
			c.Hours = append(c.Hours, h)
		}
	}
	c.AirAvailable = hasPM || hasAQI
	if c.Day.Max-c.Day.Min >= 8 {
		alert("temperature", fmt.Sprintf("昼夜温差 %.0f°C，早晚加一件可脱穿的外套。", c.Day.Max-c.Day.Min))
	}
	if c.Day.RainChance != nil && *c.Day.RainChance >= 40 {
		alert("rain", fmt.Sprintf("降水概率最高 %.0f%%，出门带伞，鞋子选防滑好走的。", *c.Day.RainChance))
	}
	if fog {
		alert("fog", "预报有雾，早出门留足路上的时间。")
	}
	if minVisibility < 1000 {
		alert("visibility", fmt.Sprintf("能见度最低约 %.0f 米，出行留意路况。", minVisibility))
	}
	if hasPM && maxPM >= 35 {
		alert("pm25", fmt.Sprintf("PM2.5 小时预报最高约 %.0f μg/m³，空气较浑浊，户外活动可选空气好一些的时段。", maxPM))
	}
	if hasPM && previousHasPM && maxPM-previousPM >= 10 {
		alert("pm25", "PM2.5 小时预报峰值比前一天升高，出门前留意当地实时空气质量。")
	} else if hasPM && previousHasPM && previousPM-maxPM >= 10 {
		alert("pm25", "PM2.5 小时预报峰值比前一天回落，颗粒物预报有所改善。")
	}
	if maxAQI > 100 {
		alert("aqi", fmt.Sprintf("%s 小时预报最高约 %.0f，留意空气质量，减少长时间高强度户外活动。", c.AQILabel, maxAQI))
	}
	if c.Day.Wind != nil && *c.Day.Wind >= 35 {
		alert("wind", "风较大，外套选防风面料，留意帽子和随身物品。")
	}
	if c.Day.UV != nil && *c.Day.UV >= 6 {
		alert("uv", "白天紫外线较强，帽子、防晒衣可以一起备好。")
	}
	switch {
	case c.Day.Min < 5:
		c.Clothing = "保暖内搭配羽绒服或厚外套，长裤和围巾也一起备好。"
	case c.Day.Min < 12:
		c.Clothing = "长袖内搭配针织衫或夹克，搭长裤，早晚注意保暖。"
	case c.Day.Min < 18:
		c.Clothing = "长袖衬衫或薄针织搭轻外套，白天暖了可以脱下外层。"
	case c.Day.Max >= 30:
		c.Clothing = "选透气的棉麻短袖或轻薄衬衫，搭宽松下装，外出备遮阳。"
	default:
		c.Clothing = "轻薄衬衫或短袖搭舒适下装，包里放件薄外套更从容。"
	}
	if len(c.Alerts) == 0 {
		alert("general", "天气变化不大，按自己的舒适度穿，出门前再看一眼实时天气。")
	}
	if c.Day.Min >= 12 && c.Day.Max < 30 {
		c.Clothing += " 休闲可搭轻夹克和运动鞋，通勤可选衬衫与直筒裤，按喜欢的风格来。"
	}
	c.CurrentAir = f.CurrentAir
	if c.CurrentAir != nil {
		c.AirAvailable = c.AirAvailable || c.CurrentAir.PM25 != nil || c.CurrentAir.AQI != nil
	}
	c.Comparisons = comparisons(f, c.Day)
	c.LocalAdvice = LocalAdvice(region, c, f)
	return c, nil
}
