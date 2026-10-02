package dbop

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"tietie/backend/internal/memoryspace"
	"tietie/backend/internal/weather"
	"time"
)

func TestCountdownCalendarPersistenceCorrectionsDeletesAndPaging(t *testing.T) {
	db, path, now := setupCareDB(t)
	ctx := context.Background()
	b, _ := db.GetBindingBySessionID(ctx, "space")
	past, err := db.ApplyCountdown(ctx, "space", "birth", 1, b.CreatedAt, "", "生日", "1998-11-16", "auto", "birthday", now)
	if err != nil || past.Repeat != "annual" || past.NextDate != "2026-11-16" || past.DaysRemaining != 45 {
		t.Fatal(past, err)
	}
	// The mounted template page must preserve the semantic type, not only
	// the date and repeat rule, so later AI recall knows this is a birthday.
	index, err := db.GetMemoryRecord(ctx, MemoryID("space", CountdownMemoryPath(past.ID)), "space")
	if err != nil || index == nil {
		t.Fatal("countdown fact missing", err)
	}
	doc, err := db.GetMemoryRecord(ctx, MemoryID("space", index.CloudPath), "space")
	if err != nil || doc == nil {
		t.Fatal("countdown template page missing", err)
	}
	entries, err := memoryspace.Entries(doc.Content)
	if err != nil || len(entries) != 1 {
		t.Fatal("countdown page unreadable", err)
	}
	var fact struct{ Date, Kind, Repeat string }
	if err := json.Unmarshal(entries[0], &fact); err != nil || fact.Kind != "birthday" || fact.Date != past.Date || fact.Repeat != "annual" {
		t.Fatal("birthday type lost in long-term memory", fact, err)
	}
	future, err := db.ApplyCountdown(ctx, "space", "due", 2, b.CreatedAt, "", "证件到期", "2026-12-01", "auto", "deadline", now)
	if err != nil || future.Repeat != "once" || future.DaysRemaining != 60 {
		t.Fatal(future, err)
	}
	today := CalculateCountdown(Countdown{Date: "2000-10-02", Repeat: "annual"}, now)
	if today.DaysRemaining != 0 {
		t.Fatal(today)
	}
	midnight := CalculateCountdown(Countdown{Date: "2026-10-03", Repeat: "once"}, time.Date(2026, 10, 2, 23, 59, 0, 0, weather.Shanghai))
	if midnight.DaysRemaining != 1 {
		t.Fatal(midnight)
	}
	leap := CalculateCountdown(Countdown{Date: "2000-02-29", Repeat: "annual"}, now)
	if leap.NextDate != "2027-02-28" || !leap.LeapAdjusted {
		t.Fatal(leap)
	}
	expired := CalculateCountdown(*future, time.Date(2026, 12, 2, 12, 0, 0, 0, weather.Shanghai))
	if !expired.Expired || expired.NextDate != "2026-12-01" {
		t.Fatal("one-off rolled into another year", expired)
	}
	if _, err := db.ApplyCountdown(ctx, "space", "foreign", 900, b.CreatedAt, "", "坏日期", "2026-02-30", "auto", "other", now); err == nil {
		t.Fatal("invalid/foreign saved")
	}
	revised, err := db.ApplyCountdown(ctx, "space", "edit", 2, b.CreatedAt, past.ID, "生日", "1998-11-17", "annual", "birthday", now)
	if err != nil || revised.ID != past.ID {
		t.Fatal(revised, err)
	}
	_, _ = db.ApplyCountdown(ctx, "space", "birth", 1, b.CreatedAt, "", "生日", "1998-11-16", "auto", "birthday", now)
	stored, _, _ := db.ListCountdowns(ctx, "space", "", now)
	for _, row := range stored {
		if row.ID == past.ID && row.Date != "1998-11-17" {
			t.Fatal("replay overwrote newer correction", row)
		}
	}
	for i := 0; i < 60; i++ {
		if _, err := db.ApplyCountdown(ctx, "space", fmt.Sprint("more", i), 1, b.CreatedAt, "", fmt.Sprint("倒计时", i), "2027-01-01", "once", "other", now); err != nil {
			t.Fatal(err)
		}
	}
	page, next, err := db.ListCountdowns(ctx, "space", "", now)
	if err != nil || len(page) != 50 || next == "" {
		t.Fatal(len(page), next, err)
	}
	_, err = db.DeleteCountdown(ctx, "space", "remove", 2, b.CreatedAt, next)
	if err != nil {
		t.Fatal(err)
	}
	rest, _, err := db.ListCountdowns(ctx, "space", next, now)
	if err != nil || len(rest) != 12 {
		t.Fatal("deleted boundary broke paging", len(rest), err)
	}
	if _, err := db.DeleteCountdown(ctx, "space", "remove", 2, b.CreatedAt, next); err != nil {
		t.Fatal("delete replay failed", err)
	}
	memory, err := db.GetMemoryRecord(ctx, MemoryID("space", CountdownMemoryPath(next)), "space")
	if err != nil || memory.Operation != "delete" {
		t.Fatal(memory, err)
	}
	_ = db.Close()
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	page, _, err = reopened.ListCountdowns(ctx, "space", "", now)
	if err != nil || len(page) != 50 {
		t.Fatal("restart lost countdowns", err)
	}
}
