package regions

import "testing"

func TestCatalogHasUsableProvinceAndCityPairs(t *testing.T) {
	if len(catalog.Provinces) != 34 {
		t.Fatal("incomplete province options")
	}
	seen := map[string]bool{}
	for _, province := range catalog.Provinces {
		if len(province.Cities) == 0 {
			t.Fatal("province has no selectable city", province)
		}
		for _, city := range province.Cities {
			if seen[city.Code] {
				t.Fatal("duplicate city code", city.Code)
			}
			seen[city.Code] = true
			location, err := Resolve(province.Code, city.Code)
			if err != nil || location.City != city.Name || location.Province != province.Name || location.CodeSystem != province.CodeSystem {
				t.Fatal(location, err)
			}
			for _, district := range city.Districts {
				location, err := Resolve(province.Code, city.Code, district.Code)
				if err != nil || location.District != district.Name || location.DistrictCode != district.Code {
					t.Fatal(location, err)
				}
			}
		}
	}
	for _, pair := range [][2]string{{"110000", "110000"}, {"420000", "429004"}, {"410000", "419001"}, {"460000", "469030"}, {"650000", "659013"}, {"810000", "810000"}, {"820000", "820000"}, {"830000", "830100"}} {
		if _, err := Resolve(pair[0], pair[1]); err != nil {
			t.Fatal(pair, err)
		}
	}
	for _, pair := range [][2]string{{"420000", "330100"}, {"420000", ""}, {"", "420100"}, {"not-real", "no"}} {
		if _, err := Resolve(pair[0], pair[1]); err == nil {
			t.Fatal("invalid pair accepted", pair)
		}
	}
	if _, err := Resolve("420000", "420100", "330106"); err == nil {
		t.Fatal("district from another city accepted")
	}
}
