package weather

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"tietie/backend/internal/regions"
	"time"
)

type QWeatherConfig struct{ Host, APIKey, KeyID, DeveloperID, ProjectID, PrivateKeyFile string }
type QWeather struct {
	Config QWeatherConfig
	HTTP   *http.Client
	mu     sync.Mutex
	cache  map[string]Forecast
}

func NewQWeather(cfg QWeatherConfig) *QWeather {
	return &QWeather{Config: cfg, HTTP: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}, cache: map[string]Forecast{}}
}
func (c *QWeather) authorize(r *http.Request) error {
	cfg := c.Config
	if cfg.PrivateKeyFile != "" {
		if cfg.KeyID == "" || cfg.DeveloperID == "" || cfg.ProjectID == "" {
			return errors.New("和风天气 JWT 配置缺少项目、开发者或凭据 ID")
		}
		body, err := os.ReadFile(cfg.PrivateKeyFile)
		if err != nil {
			return errors.New("和风天气签名私钥文件不可读取")
		}
		block, _ := pem.Decode(body)
		if block == nil {
			return errors.New("和风天气签名私钥格式无效")
		}
		parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return errors.New("和风天气签名私钥格式无效")
		}
		key, ok := parsed.(ed25519.PrivateKey)
		if !ok {
			return errors.New("和风天气需要 Ed25519 私钥")
		}
		header, _ := json.Marshal(map[string]any{"alg": "EdDSA", "kid": cfg.KeyID})
		payload, _ := json.Marshal(map[string]any{"iss": cfg.DeveloperID, "sub": cfg.ProjectID, "iat": time.Now().Unix() - 30, "exp": time.Now().Unix() + 1200})
		enc := base64.RawURLEncoding.EncodeToString
		data := enc(header) + "." + enc(payload)
		r.Header.Set("Authorization", "Bearer "+data+"."+enc(ed25519.Sign(key, []byte(data))))
	} else if cfg.APIKey != "" {
		r.Header.Set("X-QW-Api-Key", cfg.APIKey)
	} else {
		return errors.New("和风天气尚未配置鉴权凭据")
	}
	return nil
}
func (c *QWeather) get(ctx context.Context, path string, params url.Values, out any) error {
	host := strings.TrimSpace(c.Config.Host)
	if host == "" {
		return errors.New("和风天气尚未配置 API Host")
	}
	if !strings.Contains(host, "://") {
		host = "https://" + host
	}
	base, err := url.Parse(host)
	if err != nil || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Path != "" && base.Path != "/" || base.Fragment != "" {
		return errors.New("和风天气 API Host 配置无效")
	}
	// Local HTTP is used only by isolated fixture tests; public credentials require TLS.
	if base.Scheme != "https" && !(base.Scheme == "http" && (base.Hostname() == "127.0.0.1" || base.Hostname() == "localhost")) {
		return errors.New("和风天气 API Host 必须使用 HTTPS")
	}
	base.Path = path
	base.RawQuery = params.Encode()
	req, err := http.NewRequestWithContext(ctx, "GET", base.String(), nil)
	if err != nil {
		return errors.New("无法创建天气请求")
	}
	if err = c.authorize(req); err != nil {
		return err
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("和风天气请求未完成")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("和风天气接口返回状态 %d，请检查配额和权限", res.StatusCode)
	}
	if err = json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(out); err != nil {
		return errors.New("和风天气返回无效数据")
	}
	return nil
}

type qwValue struct {
	Value *float64 `json:"value"`
	Unit  string   `json:"unit"`
}
type qwCondition struct{ Code, Text string }
type qwPeriod struct {
	Condition     qwCondition
	Wind          struct{ Speed qwValue }
	Precipitation struct{ Probability *float64 }
	Humidity      *float64
}
type qwMetadata struct{ Attributions []string }

func scaled(v *float64, factor float64) *float64 {
	if v == nil {
		return nil
	}
	out := *v * factor
	return &out
}
func qwCode(code string) int {
	n, _ := strconv.Atoi(code)
	switch {
	case n == 100 || n == 150:
		return 0
	case n == 101 || n == 102 || n == 103 || n == 151 || n == 152 || n == 153:
		return 2
	case n == 104:
		return 3
	case n == 300 || n == 301:
		return 80
	case n >= 302 && n <= 304:
		return 95
	case n == 309:
		return 51
	case n >= 305 && n <= 399:
		return 63
	case n >= 400 && n <= 499:
		return 73
	case n == 500 || n == 501 || n == 509 || n == 510 || n == 514 || n == 515:
		return 45
	}
	return -1
}
func localHour(t string) string {
	parsed, err := time.Parse(time.RFC3339, t)
	if err != nil {
		parsed, err = time.Parse("2006-01-02T15:04Z07:00", t)
	}
	if err != nil {
		return ""
	}
	return parsed.In(Shanghai).Format("2006-01-02T15:04")
}

func (c *QWeather) Forecast(ctx context.Context, region regions.Location) (Forecast, error) {
	return c.forecast(ctx, region, false)
}
func (c *QWeather) ForecastFresh(ctx context.Context, region regions.Location) (Forecast, error) {
	return c.forecast(ctx, region, true)
}
func (c *QWeather) forecast(ctx context.Context, region regions.Location, fresh bool) (Forecast, error) {
	point, precision, err := Locate(region)
	if err != nil {
		return Forecast{}, err
	}
	coords := fmt.Sprintf("%.2f/%.2f", point.Latitude, point.Longitude)
	c.mu.Lock()
	cached, ok := c.cache[coords]
	c.mu.Unlock()
	if !fresh && ok && time.Since(cached.FetchedAt) < 30*time.Minute {
		cached.Precision = precision
		return copyForecast(cached), nil
	}
	var daily struct {
		Metadata qwMetadata
		Days     []struct {
			ForecastStartTime              string
			TemperatureMax, TemperatureMin qwValue
			UVIndexMax                     *float64
			Daytime, Nighttime             qwPeriod
		}
	}
	if err = c.get(ctx, "/weather/v1/daily/"+coords, url.Values{"days": {"3"}, "lang": {"zh"}}, &daily); err != nil {
		return Forecast{}, err
	}
	var hourly struct {
		Metadata qwMetadata
		Hours    []struct {
			ForecastTime                       string
			Condition                          qwCondition
			Temperature, FeelsLike, Visibility qwValue
			Humidity, UVIndex                  *float64
			Wind                               struct{ Speed qwValue }
			Precipitation                      struct{ Probability *float64 }
		}
	}
	if err = c.get(ctx, "/weather/v1/hourly/"+coords, url.Values{"hours": {"48"}, "lang": {"zh"}}, &hourly); err != nil {
		return Forecast{}, err
	}
	f := Forecast{Source: "和风天气", AQILabel: "中国 AQI", Precision: precision, FetchedAt: time.Now().UTC(), Attributions: append(daily.Metadata.Attributions, hourly.Metadata.Attributions...)}
	for _, d := range daily.Days {
		t := localHour(d.ForecastStartTime)
		if len(t) < 10 || d.TemperatureMin.Value == nil || d.TemperatureMax.Value == nil {
			continue
		}
		rain := d.Daytime.Precipitation.Probability
		if night := d.Nighttime.Precipitation.Probability; night != nil && (rain == nil || *night > *rain) {
			rain = night
		}
		wind := d.Daytime.Wind.Speed.Value
		if night := d.Nighttime.Wind.Speed.Value; night != nil && (wind == nil || *night > *wind) {
			wind = night
		}
		f.Days = append(f.Days, Day{Date: t[:10], Min: *d.TemperatureMin.Value, Max: *d.TemperatureMax.Value, Code: qwCode(d.Daytime.Condition.Code), Description: d.Daytime.Condition.Text, RainChance: scaled(rain, 100), Wind: scaled(wind, 3.6), UV: d.UVIndexMax})
	}
	for _, h := range hourly.Hours {
		t := localHour(h.ForecastTime)
		if t == "" {
			continue
		}
		code := float64(qwCode(h.Condition.Code))
		f.Hours = append(f.Hours, Hour{Time: t, Temperature: h.Temperature.Value, FeelsLike: h.FeelsLike.Value, Visibility: h.Visibility.Value, Humidity: scaled(h.Humidity, 100), UV: h.UVIndex, RainChance: scaled(h.Precipitation.Probability, 100), Wind: scaled(h.Wind.Speed.Value, 3.6), Code: &code})
	}
	if len(f.Days) == 0 || len(f.Hours) == 0 {
		return Forecast{}, errors.New("和风天气缺少有效日期或逐小时预报")
	}
	var air struct {
		Metadata qwMetadata
		Hours    []struct {
			ForecastTime string
			Indexes      []struct {
				Code string
				AQI  *float64
			}
			Pollutants []struct {
				Code          string
				Concentration qwValue
			}
		}
	}
	if err = c.get(ctx, "/airquality/v1/hourly/"+coords, url.Values{"lang": {"zh"}}, &air); err == nil {
		index := map[string]int{}
		for i, h := range f.Hours {
			index[h.Time] = i
		}
		for _, h := range air.Hours {
			if i, ok := index[localHour(h.ForecastTime)]; ok {
				for _, a := range h.Indexes {
					if a.Code == "cn-mee" {
						f.Hours[i].AQI = a.AQI
						f.AirAvailable = f.AirAvailable || a.AQI != nil
					}
				}
				for _, p := range h.Pollutants {
					if p.Code == "pm2p5" && (p.Concentration.Unit == "μg/m3" || p.Concentration.Unit == "μg/m³") {
						f.Hours[i].PM25 = p.Concentration.Value
						f.AirAvailable = f.AirAvailable || p.Concentration.Value != nil
					}
				}
			}
		}
		f.Attributions = append(f.Attributions, air.Metadata.Attributions...)
	}
	var current struct {
		Metadata qwMetadata
		Indexes  []struct {
			Code string
			AQI  *float64
		}
		Pollutants []struct {
			Code          string
			Concentration qwValue
		}
	}
	if err = c.get(ctx, "/airquality/v1/current/"+coords, url.Values{"lang": {"zh"}}, &current); err == nil {
		snapshot := &AirSnapshot{RetrievedAt: time.Now().UTC(), AQILabel: "中国 AQI"}
		for _, index := range current.Indexes {
			if index.Code == "cn-mee" {
				snapshot.AQI = index.AQI
			}
		}
		for _, p := range current.Pollutants {
			if p.Code == "pm2p5" && (p.Concentration.Unit == "μg/m³" || p.Concentration.Unit == "μg/m3") {
				snapshot.PM25 = p.Concentration.Value
			}
		}
		if snapshot.PM25 != nil || snapshot.AQI != nil {
			f.CurrentAir = snapshot
			f.AirAvailable = true
		}
		f.Attributions = append(f.Attributions, current.Metadata.Attributions...)
	}
	if fresh {
		var currentWeather struct {
			Metadata                           qwMetadata
			Condition                          qwCondition
			Temperature, FeelsLike, Visibility qwValue
			Humidity                           *float64
			Wind                               struct{ Speed qwValue }
		}
		if err := c.get(ctx, "/weather/v1/current/"+coords, url.Values{"lang": {"zh"}}, &currentWeather); err == nil && currentWeather.Temperature.Value != nil && *currentWeather.Temperature.Value >= -100 && *currentWeather.Temperature.Value <= 70 {
			f.CurrentWeather = &WeatherSnapshot{RetrievedAt: time.Now().UTC(), Temperature: currentWeather.Temperature.Value, FeelsLike: currentWeather.FeelsLike.Value, Humidity: scaled(currentWeather.Humidity, 100), Wind: scaled(currentWeather.Wind.Speed.Value, 3.6), Visibility: currentWeather.Visibility.Value, Description: currentWeather.Condition.Text, Code: qwCode(currentWeather.Condition.Code)}
			f.Attributions = append(f.Attributions, currentWeather.Metadata.Attributions...)
		}
	}
	seen := map[string]bool{}
	attrs := []string{}
	for _, a := range f.Attributions {
		if !seen[a] {
			seen[a] = true
			attrs = append(attrs, a)
		}
	}
	f.Attributions = attrs
	c.mu.Lock()
	if len(c.cache) >= 1024 {
		c.cache = map[string]Forecast{}
	}
	c.cache[coords] = f
	c.mu.Unlock()
	return copyForecast(f), nil
}

// Consumers may add historical days; cached data must stay immutable across jobs.
func copyForecast(f Forecast) Forecast {
	copyNumber := func(p *float64) *float64 {
		if p == nil {
			return nil
		}
		v := *p
		return &v
	}
	f.Days = append([]Day(nil), f.Days...)
	for i := range f.Days {
		d := &f.Days[i]
		d.RainChance = copyNumber(d.RainChance)
		d.Wind = copyNumber(d.Wind)
		d.UV = copyNumber(d.UV)
	}
	f.Hours = append([]Hour(nil), f.Hours...)
	for i := range f.Hours {
		h := &f.Hours[i]
		h.Temperature = copyNumber(h.Temperature)
		h.FeelsLike = copyNumber(h.FeelsLike)
		h.RainChance = copyNumber(h.RainChance)
		h.Wind = copyNumber(h.Wind)
		h.Humidity = copyNumber(h.Humidity)
		h.Visibility = copyNumber(h.Visibility)
		h.PM25 = copyNumber(h.PM25)
		h.AQI = copyNumber(h.AQI)
		h.Code = copyNumber(h.Code)
		h.UV = copyNumber(h.UV)
	}
	f.Attributions = append([]string(nil), f.Attributions...)
	if f.CurrentWeather != nil {
		current := *f.CurrentWeather
		current.Temperature, current.FeelsLike = copyNumber(current.Temperature), copyNumber(current.FeelsLike)
		current.Humidity, current.Wind, current.Visibility = copyNumber(current.Humidity), copyNumber(current.Wind), copyNumber(current.Visibility)
		f.CurrentWeather = &current
	}
	if f.CurrentAir != nil {
		a := *f.CurrentAir
		a.PM25 = copyNumber(a.PM25)
		a.AQI = copyNumber(a.AQI)
		f.CurrentAir = &a
	}
	return f
}
