package conversation

import (
	"fmt"
	"slices"
	"time"
)

// Recurrence describes future occurrences of one reminder series. DueAt is the
// first actual occurrence and supplies the time of day in Asia/Shanghai.
// Weekdays use ISO numbering (Monday=1); dates are finite calendar dates.
type Recurrence struct {
	Type         string   `json:"type"`
	IntervalDays int      `json:"intervalDays,omitempty"`
	Weekdays     []int    `json:"weekdays,omitempty"`
	Dates        []string `json:"dates,omitempty"`
}

var shanghai = time.FixedZone("Asia/Shanghai", 8*60*60)

func ShanghaiLocation() *time.Location { return shanghai }

func ValidateRecurrence(rule *Recurrence, dueAt time.Time) error {
	if rule == nil {
		return nil
	}
	if dueAt.IsZero() {
		return fmt.Errorf("recurrence requires dueAt")
	}
	local := dueAt.In(shanghai)
	switch rule.Type {
	case "daily", "weekly", "monthly", "yearly":
		if rule.IntervalDays != 0 || len(rule.Weekdays) != 0 || len(rule.Dates) != 0 {
			return fmt.Errorf("unexpected recurrence options")
		}
	case "interval":
		if rule.IntervalDays < 1 || rule.IntervalDays > 3650 || len(rule.Weekdays) != 0 || len(rule.Dates) != 0 {
			return fmt.Errorf("invalid interval recurrence")
		}
	case "weekdays":
		if rule.IntervalDays != 0 || len(rule.Dates) != 0 || len(rule.Weekdays) == 0 || len(rule.Weekdays) > 7 {
			return fmt.Errorf("invalid weekday recurrence")
		}
		seen := map[int]bool{}
		for _, day := range rule.Weekdays {
			if day < 1 || day > 7 || seen[day] {
				return fmt.Errorf("invalid weekday recurrence")
			}
			seen[day] = true
		}
		if !seen[isoWeekday(local)] {
			return fmt.Errorf("dueAt must match recurrence weekdays")
		}
	case "dates":
		if rule.IntervalDays != 0 || len(rule.Weekdays) != 0 || len(rule.Dates) == 0 || len(rule.Dates) > 100 {
			return fmt.Errorf("invalid selected dates")
		}
		first := local.Format("2006-01-02")
		seen := map[string]bool{}
		for _, date := range rule.Dates {
			parsed, err := time.ParseInLocation("2006-01-02", date, shanghai)
			if err != nil || parsed.Format("2006-01-02") != date || date < first || seen[date] {
				return fmt.Errorf("invalid selected dates")
			}
			seen[date] = true
		}
		if !seen[first] {
			return fmt.Errorf("dueAt must be first selected date")
		}
	default:
		return fmt.Errorf("invalid recurrence type")
	}
	return nil
}

func isoWeekday(t time.Time) int {
	if t.Weekday() == time.Sunday {
		return 7
	}
	return int(t.Weekday())
}

// NextRecurrence returns the first scheduled occurrence strictly after both
// the prior due time and the current clock. Missed days are skipped after an
// outage, while the original Shanghai wall clock remains unchanged.
func NextRecurrence(rule *Recurrence, anchor, previous, now time.Time) (time.Time, bool) {
	if ValidateRecurrence(rule, anchor) != nil || rule == nil || previous.Before(anchor) {
		return time.Time{}, false
	}
	a := anchor.In(shanghai)
	cutoff := previous.In(shanghai)
	if now.After(previous) {
		cutoff = now.In(shanghai)
	}
	makeDay := func(year int, month time.Month, day int) time.Time {
		return time.Date(year, month, day, a.Hour(), a.Minute(), a.Second(), a.Nanosecond(), shanghai)
	}
	switch rule.Type {
	case "daily", "interval", "weekly":
		days := 1
		if rule.Type == "interval" {
			days = rule.IntervalDays
		} else if rule.Type == "weekly" {
			days = 7
		}
		elapsed := int(cutoff.Sub(a) / (24 * time.Hour))
		step := elapsed/days + 1
		candidate := a.AddDate(0, 0, step*days)
		if !candidate.After(cutoff) {
			candidate = candidate.AddDate(0, 0, days)
		}
		return candidate.UTC(), true
	case "weekdays":
		for n := 0; n <= 7; n++ {
			day := cutoff.AddDate(0, 0, n)
			if !slices.Contains(rule.Weekdays, isoWeekday(day)) {
				continue
			}
			candidate := makeDay(day.Year(), day.Month(), day.Day())
			if candidate.After(cutoff) {
				return candidate.UTC(), true
			}
		}
	case "monthly":
		year, month := cutoff.Year(), cutoff.Month()
		for n := 0; n <= 12; n++ {
			monthStart := time.Date(year, month, 1, 0, 0, 0, 0, shanghai).AddDate(0, n, 0)
			last := time.Date(monthStart.Year(), monthStart.Month()+1, 0, 0, 0, 0, 0, shanghai).Day()
			candidate := makeDay(monthStart.Year(), monthStart.Month(), min(a.Day(), last))
			if candidate.After(cutoff) {
				return candidate.UTC(), true
			}
		}
	case "yearly":
		for year := cutoff.Year(); year <= cutoff.Year()+2; year++ {
			last := time.Date(year, a.Month()+1, 0, 0, 0, 0, 0, shanghai).Day()
			candidate := makeDay(year, a.Month(), min(a.Day(), last))
			if candidate.After(cutoff) {
				return candidate.UTC(), true
			}
		}
	case "dates":
		var best time.Time
		for _, date := range rule.Dates {
			day, _ := time.ParseInLocation("2006-01-02", date, shanghai)
			candidate := makeDay(day.Year(), day.Month(), day.Day())
			if candidate.After(cutoff) && (best.IsZero() || candidate.Before(best)) {
				best = candidate
			}
		}
		if !best.IsZero() {
			return best.UTC(), true
		}
	}
	return time.Time{}, false
}
