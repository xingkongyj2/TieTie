package regions

import "testing"

func TestMatchNamesForWeatherFollowup(t *testing.T) {
	qianjiang := MatchNames("", "潜江", "")
	if len(qianjiang) != 1 || qianjiang[0].Province != "湖北省" || qianjiang[0].City != "潜江市" {
		t.Fatalf("潜江应唯一匹配湖北省潜江市，得到 %#v", qianjiang)
	}
	if _, err := ResolveNames("", "潜江", ""); err != nil {
		t.Fatalf("城市简称应能解析：%v", err)
	}
	if matches := MatchNames("", "新竹", ""); len(matches) != 2 {
		t.Fatalf("同名城市和县必须保留候选，得到 %#v", matches)
	}
	if _, err := ResolveNames("", "新竹", ""); err == nil {
		t.Fatal("有多个候选时不能自行选一个")
	}
}
