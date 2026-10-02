package conversation

import (
	"encoding/json"
	"testing"
)

func TestWeatherQueryControlSchema(t *testing.T) {
	base := Action{Type: "query_weather", Key: "weather", WeatherWhen: "now", RecipientIDs: []int64{1, 2}}
	good := []Action{base, {Type: "query_weather", Key: "city", WeatherWhen: "tomorrow", Region: &RegionUpdate{Province: "湖北", City: "潜江"}}, {Type: "query_weather", Key: "default"}}
	for _, action := range good {
		body, _ := json.Marshal(map[string]any{"protocol": "tietie.control", "version": 2, "requestId": "turn_1", "actions": []Action{action}})
		out := ParseAssistant(string(body))
		if out.ProtocolError != "" || len(out.Actions) != 1 {
			t.Fatal(string(body), out)
		}
	}
	mutations := []func(*Action){func(a *Action) { a.Storage = "database_and_memory" }, func(a *Action) { a.WeatherWhen = "year" }, func(a *Action) { a.TargetUserID = 1 }, func(a *Action) { a.RecipientIDs = []int64{1, 1} }, func(a *Action) { a.Region = &RegionUpdate{Clear: true} }, func(a *Action) { a.Region = &RegionUpdate{Province: "湖北", City: "无此城"} }, func(a *Action) { a.Title = "hidden task" }, func(a *Action) { a.Date = "2026-10-02" }}
	for _, mutate := range mutations {
		a := base
		mutate(&a)
		if ValidateV2Action(a) == nil {
			t.Fatal("invalid query accepted", a)
		}
	}
}
