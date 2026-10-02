package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"tietie/backend/internal/auth"
	"tietie/backend/internal/dbop"
	"tietie/backend/internal/qoder"
	"tietie/backend/internal/weather"
	"time"
)

func (s *Server) weatherProvider() weather.Provider {
	s.weatherOnce.Do(func() {
		if s.Weather == nil {
			cfg := weather.QWeatherConfig{}
			if s.Cfg != nil {
				cfg = weather.QWeatherConfig{Host: s.Cfg.QWeatherHost, APIKey: s.Cfg.QWeatherKey, KeyID: s.Cfg.QWeatherKeyID, DeveloperID: s.Cfg.QWeatherDeveloperID, ProjectID: s.Cfg.QWeatherProjectID, PrivateKeyFile: s.Cfg.QWeatherPrivateKeyFile}
			}
			s.Weather = weather.NewQWeather(cfg)
		}
	})
	return s.Weather
}
func (s *Server) handleCareSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "PUT" {
		writeMethodNotAllowed(w)
		return
	}
	session := r.PathValue("id")
	if err := s.ensureBoundSession(r.Context(), session); err != nil {
		writeError(w, err)
		return
	}
	if r.Method == "PUT" {
		var body struct {
			Mode    string `json:"mode"`
			Enabled bool   `json:"enabled"`
			Time    string `json:"time"`
		}
		if err := decodeJSONBody(r, &body, 1024); err != nil {
			writeError(w, err)
			return
		}
		if (body.Mode != "morning" && body.Mode != "night") || !dbop.ValidCareClock(body.Time) {
			writeError(w, qoder.NewApiError(400, "invalid_care", "请选择模式和有效的提醒时间。"))
			return
		}
		err := s.DB.SaveCareMode(r.Context(), session, auth.UserIDFrom(r.Context()), body.Mode, body.Time, body.Enabled, time.Now())
		if errors.Is(err, dbop.ErrCareRegion) {
			if noticeErr := s.DB.NotifyCareRegionMissing(r.Context(), session, auth.UserIDFrom(r.Context()), body.Mode, time.Now()); noticeErr != nil {
				writeError(w, noticeErr)
				return
			}
			writeError(w, qoder.NewApiError(409, "care_region_required", "暂未开启，已在群里说明双方地区填写情况。"))
			return
		}
		if err != nil {
			if errors.Is(err, weather.ErrLocation) {
				writeError(w, qoder.NewApiError(400, "care_unavailable", err.Error()))
			} else {
				writeError(w, err)
			}
			return
		}
	}
	modes, err := s.DB.GetCareModes(r.Context(), session)
	if err != nil {
		writeError(w, err)
		return
	}
	members, _, err := s.DB.CareMembers(r.Context(), session)
	if err != nil {
		writeError(w, err)
		return
	}
	reports, err := s.DB.ListCareReports(r.Context(), session, 2)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"modes": modes, "members": members, "reports": reports})
}
func (s *Server) handleCarePreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		writeMethodNotAllowed(w)
		return
	}
	session := r.PathValue("id")
	if err := s.ensureBoundSession(r.Context(), session); err != nil {
		writeError(w, err)
		return
	}
	kind := r.URL.Query().Get("mode")
	if kind != "morning" && kind != "night" {
		writeError(w, qoder.NewApiError(400, "invalid_care", "请选择早安或晚安模式。"))
		return
	}
	members, b, err := s.DB.CareMembers(r.Context(), session)
	if err != nil {
		writeError(w, err)
		return
	}
	for _, m := range members {
		if m.Region.CityCode == "" {
			writeError(w, qoder.NewApiError(409, "care_region_required", "双方都需要先填写地区，才能查看天气关怀。"))
			return
		}
	}
	cards, err := s.buildCareCards(r.Context(), dbop.CareMode{SessionID: session, Mode: kind, BindingCreatedAt: b.CreatedAt}, members, time.Now())
	if err != nil {
		writeError(w, qoder.NewApiError(503, "weather_unavailable", err.Error()))
		return
	}
	writeJSON(w, 200, map[string]any{"cards": cards})
}
func (s *Server) buildCareCards(ctx context.Context, job dbop.CareMode, members []dbop.CareMember, now time.Time) ([]weather.Card, error) {
	cards := []weather.Card{}
	groups := map[string]int{}
	for _, member := range members {
		if member.Region.CityCode == "" {
			return nil, dbop.ErrCareRegion
		}
	}
	for _, member := range members {
		if member.Region.CityCode == "" {
			return nil, dbop.ErrCareRegion
		}
		key := member.Region.CodeSystem + ":" + member.Region.CityCode + ":" + member.Region.DistrictCode
		if index, ok := groups[key]; ok {
			cards[index].RecipientIDs = append(cards[index].RecipientIDs, member.UserID)
			cards[index].RecipientNames = append(cards[index].RecipientNames, member.Name)
			metrics := []string{}
			if member.PreferenceKey != "" {
				metrics = strings.Split(member.PreferenceKey, ",")
			}
			view := weather.Select(cards[index], []int64{member.UserID}, []string{member.Name}, metrics)
			merged := false
			for i := range cards[index].Views {
				if strings.Join(cards[index].Views[i].Metrics, ",") == strings.Join(view.Metrics, ",") {
					cards[index].Views[i].RecipientIDs = append(cards[index].Views[i].RecipientIDs, member.UserID)
					cards[index].Views[i].RecipientNames = append(cards[index].Views[i].RecipientNames, member.Name)
					merged = true
					break
				}
			}
			if !merged {
				cards[index].Views = append(cards[index].Views, view)
			}
			continue
		}
		forecast, err := s.weatherProvider().Forecast(ctx, member.Region)
		if err != nil {
			return nil, err
		}
		reports, reportErr := s.DB.ListWeatherCareReports(ctx, job.SessionID, 8)
		if reportErr != nil {
			return nil, reportErr
		}
		dayTime := now.In(weather.Shanghai)
		if job.Mode == "night" {
			dayTime = dayTime.AddDate(0, 0, 1)
		}
		previous := dayTime.AddDate(0, 0, -1).Format("2006-01-02")
		hasPrevious := false
		for _, day := range forecast.Days {
			if day.Date == previous {
				hasPrevious = true
			}
		}
		if !hasPrevious {
			for _, report := range reports {
				for _, old := range report.Cards {
					if old.Region == member.Region && old.Source == forecast.Source && old.Day.Date == previous {
						forecast.Days = append(forecast.Days, old.Day)
						hasPrevious = true
						break
					}
				}
				if hasPrevious {
					break
				}
			}
		}
		card, err := weather.Analyze(forecast, member.Region, job.Mode, now)
		if err != nil {
			return nil, err
		}
		if card.CurrentAir != nil && card.CurrentAir.PM25 != nil {
			found := false
			for _, report := range reports {
				for _, old := range report.Cards {
					if old.Region != card.Region || old.Source != card.Source || old.CurrentAir == nil || old.CurrentAir.PM25 == nil {
						continue
					}
					deltaTime := card.CurrentAir.RetrievedAt.Sub(old.CurrentAir.RetrievedAt)
					if deltaTime >= 22*time.Hour && deltaTime <= 26*time.Hour {
						delta := *card.CurrentAir.PM25 - *old.CurrentAir.PM25
						if delta >= .5 {
							card.AirComparison = fmt.Sprintf("比前一天相近时段的实时 PM2.5 增加约 %.0f μg/m³。", delta)
						} else if delta <= -.5 {
							card.AirComparison = fmt.Sprintf("比前一天相近时段的实时 PM2.5 减少约 %.0f μg/m³。", -delta)
						} else {
							card.AirComparison = "与前一天相近时段的实时 PM2.5 接近。"
						}
						found = true
						break
					}
				}
				if found {
					break
				}
			}
		}
		card.RecipientIDs = []int64{member.UserID}
		card.RecipientNames = []string{member.Name}
		metrics := []string{}
		if member.PreferenceKey != "" {
			metrics = strings.Split(member.PreferenceKey, ",")
		}
		card.Views = []weather.View{weather.Select(card, card.RecipientIDs, card.RecipientNames, metrics)}
		groups[key] = len(cards)
		cards = append(cards, card)
	}
	if job.Mode == "morning" {
		rows, err := s.DB.CareTodayReminders(ctx, job.SessionID, job.BindingCreatedAt, now)
		if err != nil {
			return nil, err
		}
		for index := range cards {
			for _, reminder := range rows {
				included := false
				for _, id := range reminder.RecipientIDs {
					for _, target := range cards[index].RecipientIDs {
						if id == target {
							included = true
						}
					}
				}
				if !included {
					continue
				}
				if len(cards[index].Reminders) >= 20 {
					cards[index].MoreReminders++
					continue
				}
				cards[index].Reminders = append(cards[index].Reminders, weather.Reminder{Title: reminder.Title, Time: reminder.DueAt.In(weather.Shanghai).Format("15:04"), RecipientIDs: reminder.RecipientIDs})
			}
		}
	}
	return cards, nil
}
func careText(cards []weather.Card) string {
	lines := []string{}
	for _, card := range cards {
		names := []string{}
		for _, name := range card.RecipientNames {
			names = append(names, "@"+name)
		}
		day := "今天"
		greeting := "早安 ☀️"
		if card.Mode == "night" {
			day = "明天"
			greeting = "晚安 🌙"
		}
		location := card.Region.City
		if card.Region.District != "" {
			location += " · " + card.Region.District
		}
		text := fmt.Sprintf("%s %s\n%s%s，%s，%.0f–%.0f°C。", strings.Join(names, " "), greeting, day, location, card.Description, card.Day.Min, card.Day.Max)
		if card.Mode == "morning" {
			if len(card.Reminders) == 0 {
				text += "\n今天没有待提醒事项，按自己的节奏来。"
			}
			for _, reminder := range card.Reminders {
				text += "\n" + reminder.Time + " · " + reminder.Title
			}
		} else {
			for _, view := range card.Views {
				if len(card.Views) > 1 {
					text += "\n@" + strings.Join(view.RecipientNames, " @")
				}
				for _, takeaway := range view.Summary {
					text += "\n" + takeaway
				}
				if view.Clothing != "" {
					text += "\n穿搭：" + view.Clothing + " " + view.LocalAdvice
				}
			}
		}
		lines = append(lines, text)
	}
	return strings.Join(lines, "\n\n")
}
func (s *Server) runCareMode(ctx context.Context, job dbop.CareMode) error {
	now := time.Now()
	members, b, err := s.DB.CareMembers(ctx, job.SessionID)
	if err != nil || b == nil || !b.CreatedAt.Equal(job.BindingCreatedAt) {
		return nil
	}
	cards, err := s.buildCareCards(ctx, job, members, now)
	if err == nil {
		err = s.DB.CompleteCareReport(ctx, job, members, dbop.CareReport{Text: careText(cards), Cards: cards}, now)
	}
	if err != nil {
		state := "retrying"
		message := "天气暂时没查到，稍后重试。"
		if errors.Is(err, dbop.ErrCareRegion) {
			state = "region_missing"
			message = "地区尚未补全，提醒已暂停；请双方填写地区。"
		}
		saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if e := s.DB.FailCareMode(saveCtx, job, message, state, now); e != nil {
			return e
		}
		if errors.Is(err, dbop.ErrCareStale) || errors.Is(err, dbop.ErrCareRegion) {
			return nil
		}
		return err
	}
	return nil
}
func (s *Server) appendCareHistory(ctx context.Context, session string, result *qoder.MessagesResult, after string) error {
	reports, err := s.DB.ListCareReports(ctx, session, 100, after)
	if err != nil {
		return err
	}
	for _, report := range reports {
		ids := []int64{}
		for _, card := range report.Cards {
			ids = append(ids, card.RecipientIDs...)
		}
		source := "reminder"
		if report.Mode == "region_notice" {
			source = "chat"
		}
		if report.Mode == "region_notice" || report.Mode == "anniversary" {
			members, _, err := s.DB.CareMembers(ctx, session)
			if err != nil {
				return err
			}
			for _, member := range members {
				ids = append(ids, member.UserID)
			}
		}
		result.Messages = append(result.Messages, qoder.PublicMessage{ID: report.ID, Sender: "ai", Source: source, Kind: "text", Text: report.Text, RecipientIDs: ids, WeatherCards: report.Cards, CreatedAt: report.CreatedAt.Format(time.RFC3339Nano), Time: report.CreatedAt.In(weather.Shanghai).Format("15:04")})
	}
	sort.SliceStable(result.Messages, func(i, j int) bool {
		a, _ := time.Parse(time.RFC3339Nano, result.Messages[i].CreatedAt)
		b, _ := time.Parse(time.RFC3339Nano, result.Messages[j].CreatedAt)
		return a.Before(b)
	})
	return nil
}
