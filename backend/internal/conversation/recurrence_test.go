package conversation

import (
	"reflect"
	"testing"
	"time"
)

func TestSelectedDatesReminderRequiresFirstActualDate(t *testing.T) {
	due := time.Date(2026, 10, 7, 8, 0, 0, 0, ShanghaiLocation())
	for _, test := range []struct {
		name  string
		rule  Recurrence
		valid bool
	}{
		{"one selected date", Recurrence{Type: "dates", Dates: []string{"2026-10-07"}}, true},
		{"multiple dates in any order", Recurrence{Type: "dates", Dates: []string{"2026-10-11", "2026-10-07", "2026-10-09"}}, true},
		{"missing dates", Recurrence{Type: "dates"}, false},
		{"missing first due date", Recurrence{Type: "dates", Dates: []string{"2026-10-08", "2026-10-09"}}, false},
		{"date before first due date", Recurrence{Type: "dates", Dates: []string{"2026-10-06", "2026-10-07"}}, false},
		{"duplicate date", Recurrence{Type: "dates", Dates: []string{"2026-10-07", "2026-10-07"}}, false},
		{"invalid calendar date", Recurrence{Type: "dates", Dates: []string{"2026-10-07", "2026-02-30"}}, false},
		{"unexpected interval", Recurrence{Type: "dates", Dates: []string{"2026-10-07"}, IntervalDays: 1}, false},
		{"unexpected weekday", Recurrence{Type: "dates", Dates: []string{"2026-10-07"}, Weekdays: []int{3}}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateRecurrence(&test.rule, due.UTC()); (err == nil) != test.valid {
				t.Fatalf("selected dates validity=%t, error=%v", test.valid, err)
			}
		})
	}
}

func TestSelectedDatesReminderAdvancesOneFiniteSeriesInShanghai(t *testing.T) {
	// This UTC timestamp falls on the following Shanghai calendar day. Selected
	// dates share its Shanghai clock, even when the stored queue is in UTC.
	anchor := time.Date(2026, 10, 6, 23, 30, 15, 0, time.UTC)
	rule := &Recurrence{Type: "dates", Dates: []string{"2026-10-11", "2026-10-09", "2026-10-07", "2026-10-08"}}
	originalDates := append([]string(nil), rule.Dates...)
	previous := anchor
	for _, date := range []string{"2026-10-08", "2026-10-09", "2026-10-11"} {
		next, ok := NextRecurrence(rule, anchor, previous, previous.Add(time.Minute))
		if !ok || next.In(ShanghaiLocation()).Format(time.RFC3339) != date+"T07:30:15+08:00" {
			t.Fatalf("next occurrence for %s = %s, exists=%t", date, next, ok)
		}
		previous = next
	}
	if next, ok := NextRecurrence(rule, anchor, previous, previous); ok || !next.IsZero() {
		t.Fatalf("finite selected dates must stop after the last date: next=%s exists=%t", next, ok)
	}
	if !reflect.DeepEqual(rule.Dates, originalDates) {
		t.Fatal("advancing a series must preserve its original selected dates")
	}
}

func TestSelectedDatesReminderSkipsMissedDatesWithoutInventingAnotherDate(t *testing.T) {
	anchor := time.Date(2026, 10, 7, 8, 0, 0, 0, ShanghaiLocation())
	rule := &Recurrence{Type: "dates", Dates: []string{"2026-10-07", "2026-10-08", "2026-10-09", "2026-10-11"}}
	now := time.Date(2026, 10, 9, 9, 0, 0, 0, ShanghaiLocation())
	next, ok := NextRecurrence(rule, anchor, anchor, now)
	want := time.Date(2026, 10, 11, 8, 0, 0, 0, ShanghaiLocation())
	if !ok || !next.Equal(want) {
		t.Fatalf("missed dates must advance to the next selected date: next=%s exists=%t", next, ok)
	}
	if next, ok := NextRecurrence(rule, anchor, anchor, want.Add(time.Minute)); ok || !next.IsZero() {
		t.Fatalf("no future selected dates must end the series: next=%s exists=%t", next, ok)
	}
}
