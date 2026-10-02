// Package regions supplies the same offline province/city catalog to validation
// and the picker. Names and code systems always come from the catalog.
package regions

import (
	_ "embed"
	"encoding/json"
	"errors"
	"strings"
)

type City struct {
	Code      string     `json:"code"`
	Name      string     `json:"name"`
	Districts []District `json:"districts"`
}

type District struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

type Province struct {
	Code       string `json:"code"`
	Name       string `json:"name"`
	CodeSystem string `json:"codeSystem"`
	Cities     []City `json:"cities"`
}

type Location struct {
	ProvinceCode string `json:"provinceCode"`
	Province     string `json:"province"`
	CityCode     string `json:"cityCode" gorm:"index:idx_user_profile_region_city"`
	City         string `json:"city"`
	DistrictCode string `json:"districtCode" gorm:"index:idx_user_profile_region_district"`
	District     string `json:"district"`
	CodeSystem   string `json:"codeSystem"`
}

//go:embed china.json
var catalogJSON []byte

var catalog struct {
	Version   string     `json:"version"`
	Provinces []Province `json:"provinces"`
}

func init() {
	if err := json.Unmarshal(catalogJSON, &catalog); err != nil {
		panic(err)
	}
}

func Catalog() any { return catalog }

// ResolveNames accepts colloquial names but never picks one of several matches.
func ResolveNames(province, city, district string) (Location, error) {
	province, city, district = strings.TrimSpace(province), strings.TrimSpace(city), strings.TrimSpace(district)
	if city == "" {
		return Location{}, errors.New("请补充城市名称")
	}
	alias := func(a, b string) bool {
		if a == b {
			return true
		}
		for _, suffix := range []string{"特别行政区", "壮族自治区", "回族自治区", "维吾尔自治区", "自治区", "省", "市", "区", "县"} {
			if strings.TrimSuffix(b, suffix) == a {
				return true
			}
		}
		return false
	}
	matches := []Location{}
	for _, p := range catalog.Provinces {
		if province != "" && !alias(province, p.Name) {
			continue
		}
		for _, c := range p.Cities {
			if !alias(city, c.Name) {
				continue
			}
			if district == "" {
				loc, _ := Resolve(p.Code, c.Code)
				matches = append(matches, loc)
				continue
			}
			for _, d := range c.Districts {
				if alias(district, d.Name) {
					loc, _ := Resolve(p.Code, c.Code, d.Code)
					matches = append(matches, loc)
				}
			}
		}
	}
	if len(matches) != 1 {
		return Location{}, errors.New("地区名称不明确或省市区不匹配，请补充准确的城市和区县")
	}
	return matches[0], nil
}

// An empty pair clears the region; partial or unrelated pairs are invalid.
func Resolve(provinceCode, cityCode string, districtCodes ...string) (Location, error) {
	districtCode := ""
	if len(districtCodes) > 0 {
		districtCode = districtCodes[0]
	}
	if provinceCode == "" && cityCode == "" && districtCode == "" {
		return Location{}, nil
	}
	for _, province := range catalog.Provinces {
		if province.Code != provinceCode {
			continue
		}
		for _, city := range province.Cities {
			if city.Code == cityCode {
				location := Location{ProvinceCode: province.Code, Province: province.Name, CityCode: city.Code, City: city.Name, CodeSystem: province.CodeSystem}
				if districtCode == "" {
					return location, nil
				}
				for _, district := range city.Districts {
					if district.Code == districtCode {
						location.DistrictCode, location.District = district.Code, district.Name
						return location, nil
					}
				}
				return Location{}, errors.New("invalid city/district pair")
			}
		}
	}
	return Location{}, errors.New("invalid province/city pair")
}
