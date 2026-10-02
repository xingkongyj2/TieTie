package weather

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"tietie/backend/internal/regions"
)

//go:embed care-policy.txt
var CarePolicy string

//go:embed city-climate.json
var climateJSON []byte

type CityClimate struct {
	Province, City, Zone, Basis, Characteristics, Source string
	Priorities                                           []string
}

var climateCatalog struct {
	Cities map[string]CityClimate `json:"cities"`
}

func init() {
	if err := json.Unmarshal(climateJSON, &climateCatalog); err != nil {
		panic(err)
	}
}
func Climate(region regions.Location) CityClimate {
	return climateCatalog.Cities[region.CodeSystem+":"+region.CityCode]
}

// Local context ranks advice; a city's reputation never creates a forecast.
func LocalAdvice(region regions.Location, card Card, f Forecast) string {
	profile := Climate(region)
	peakHumidity := 0.0
	lowHumidity := 101.0
	lowVisibility := 1e9
	for _, h := range f.Hours {
		if len(h.Time) < 10 || h.Time[:10] != card.Day.Date {
			continue
		}
		if h.Humidity != nil {
			if *h.Humidity > peakHumidity {
				peakHumidity = *h.Humidity
			}
			if *h.Humidity < lowHumidity {
				lowHumidity = *h.Humidity
			}
		}
		if h.Visibility != nil && *h.Visibility < lowVisibility {
			lowVisibility = *h.Visibility
		}
	}
	rain := card.Day.RainChance != nil && *card.Day.RainChance >= 40
	prefix := region.City + "这次预报"
	switch profile.Zone {
	case "yangtze", "basin", "south", "southwest":
		if peakHumidity >= 80 && card.Day.Min < 15 {
			return prefix + "偏冷且湿度高，选不易吸潮的外层，袜子和鞋子保持干爽。"
		}
		if peakHumidity >= 70 && card.Day.Max >= 28 {
			return prefix + "偏热、湿度也高，宽松透气的衣物更舒服，少叠穿。"
		}
		if rain {
			return prefix + "有降雨，轻便防水外层搭防滑鞋；比厚重吸水的鞋服更好打理。"
		}
		if lowVisibility < 1000 {
			return prefix + "能见度偏低，早班或通勤可以多留一点路上时间。"
		}
	case "northwest", "north":
		if lowHumidity <= 30 {
			return prefix + "空气较干，包里备水，外套按风速选择防风面料。"
		}
		if card.Day.Max-card.Day.Min >= 8 {
			return prefix + fmt.Sprintf("昼夜温差约 %.0f°C，分层穿搭比单件厚衣更方便。", card.Day.Max-card.Day.Min)
		}
	case "plateau":
		if card.Day.UV != nil && *card.Day.UV >= 6 {
			return prefix + "紫外线较强，即使气温低，也建议备帽子和防晒衣。"
		}
		if card.Day.Min < 5 {
			return prefix + "早晚偏冷，轻羽绒或防风外层更合适，别只按午后温度穿。"
		}
	case "northeast":
		if card.Day.Min <= 0 {
			return prefix + "最低温接近或低于零度，保暖内搭与防风外层一起备好；有雨雪时留意防滑。"
		}
	}
	return ""
}
