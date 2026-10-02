package dbop

import (
	"context"
	"errors"
	"strings"
	"testing"
	"tietie/backend/internal/regions"
	"tietie/backend/internal/weather"
	"time"
)

func setupCareDB(t *testing.T) (*DB, string, time.Time) {
	db, path := openReminderTestDB(t)
	ctx := context.Background()
	for _, u := range []User{{ID: 1, Username: "一", Code: "5011"}, {ID: 2, Username: "二", Code: "5012"}} {
		if err := db.CreateUser(ctx, &u); err != nil {
			t.Fatal(err)
		}
	}
	region, _ := regions.Resolve("420000", "420100", "420111")
	for _, id := range []int64{1, 2} {
		if _, err := db.SaveUserProfile(ctx, UserProfile{UserID: id, Region: region, Hobbies: []string{}}); err != nil {
			t.Fatal(err)
		}
	}
	return db, path, time.Date(2026, 10, 2, 7, 0, 0, 0, weather.Shanghai)
}
func TestCareModeBlocksMissingRegionAndPersistsDailyQueueAndMemory(t *testing.T) {
	db, path, now := setupCareDB(t)
	ctx := context.Background()
	if _, err := db.SaveUserProfile(ctx, UserProfile{UserID: 2, Hobbies: []string{}}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveCareMode(ctx, "space", 1, "morning", "08:00", true, now); !errors.Is(err, ErrCareRegion) {
		t.Fatal("enabled without partner region", err)
	}
	region, _ := regions.Resolve("420000", "420100", "420111")
	_, _ = db.SaveUserProfile(ctx, UserProfile{UserID: 2, Region: region, Hobbies: []string{}})
	if err := db.SaveCareMode(ctx, "space", 1, "morning", "08:00", true, now); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveCareMode(ctx, "space", 1, "night", "21:00", false, now); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveCareMode(ctx, "space", 999, "night", "21:00", true, now); err == nil {
		t.Fatal("outsider changed mode")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	early, err := db.ClaimCareModes(ctx, now, 8)
	if err != nil || len(early) != 0 {
		t.Fatal(early, err)
	}
	due := now.Add(time.Hour)
	jobs, err := db.ClaimCareModes(ctx, due, 8)
	if err != nil || len(jobs) != 1 {
		t.Fatal(jobs, err)
	}
	job := jobs[0]
	duplicate, _ := db.ClaimCareModes(ctx, due, 8)
	if len(duplicate) != 0 {
		t.Fatal("lease double claimed")
	}
	members, _, _ := db.CareMembers(ctx, "space")
	if err := db.CompleteCareReport(ctx, job, members, CareReport{Text: "测试早安", Cards: []weather.Card{{Mode: "morning", Region: region}}}, due); err != nil {
		t.Fatal(err)
	}
	reports, _ := db.ListCareReports(ctx, "space", 10)
	if len(reports) != 1 {
		t.Fatal(reports)
	}
	if err := db.CompleteCareReport(ctx, job, members, CareReport{Text: "重复"}, due); !errors.Is(err, ErrCareStale) {
		t.Fatal("duplicate completion accepted", err)
	}
	modes, _ := db.GetCareModes(ctx, "space")
	if modes[0].NextDue.In(weather.Shanghai).Format("2006-01-02 15:04") != "2026-10-03 08:00" {
		t.Fatal(modes)
	}
	var facts []MemoryRecord
	if err := db.gdb.Where("session_id=? AND category=?", "space", "realtime").Find(&facts).Error; err != nil || len(facts) != 1 || facts[0].ExpiresAt == nil || !strings.Contains(facts[0].Content, "测试早安") {
		t.Fatal(facts, err)
	}
	for _, pair := range []struct {
		now   string
		clock string
		want  string
	}{{"2026-10-02T23:59:00+08:00", "08:00", "2026-10-03 08:00"}, {"2026-10-02T08:00:00+08:00", "08:00", "2026-10-03 08:00"}} {
		v, _ := time.Parse(time.RFC3339, pair.now)
		if got := NextCareTime(v, pair.clock).In(weather.Shanghai).Format("2006-01-02 15:04"); got != pair.want {
			t.Fatal(got)
		}
	}
}
func TestCareCommitRejectsRegionChangesDisableAndUnbind(t *testing.T) {
	for _, change := range []string{"region", "disable", "unbind"} {
		t.Run(change, func(t *testing.T) {
			db, _, now := setupCareDB(t)
			ctx := context.Background()
			if err := db.SaveCareMode(ctx, "space", 1, "morning", "08:00", true, now); err != nil {
				t.Fatal(err)
			}
			due := now.Add(time.Hour)
			jobs, _ := db.ClaimCareModes(ctx, due, 8)
			job := jobs[0]
			members, _, _ := db.CareMembers(ctx, "space")
			switch change {
			case "region":
				region, _ := regions.Resolve("330000", "330100", "330106")
				_, _ = db.SaveUserProfile(ctx, UserProfile{UserID: 2, Region: region})
			case "disable":
				_ = db.SaveCareMode(ctx, "space", 1, "morning", "08:00", false, due)
			case "unbind":
				_, _ = db.Unbind(ctx, 1)
			}
			if err := db.CompleteCareReport(ctx, job, members, CareReport{Text: "不应发送"}, due); !errors.Is(err, ErrCareStale) {
				t.Fatal(err)
			}
			var count int64
			db.gdb.Model(&CareReport{}).Count(&count)
			if count != 0 {
				t.Fatal("stale forecast sent")
			}
		})
	}
}
func TestCareReportAndMemoryAreAtomicAndLeaseRecovers(t *testing.T) {
	db, _, now := setupCareDB(t)
	ctx := context.Background()
	_ = db.SaveCareMode(ctx, "space", 1, "morning", "08:00", true, now)
	due := now.Add(time.Hour)
	jobs, _ := db.ClaimCareModes(ctx, due, 8)
	job := jobs[0]
	members, _, _ := db.CareMembers(ctx, "space")
	db.gdb.Exec("CREATE TRIGGER fail_care_memory BEFORE INSERT ON memory_records BEGIN SELECT RAISE(ABORT, 'memory unavailable'); END")
	if err := db.CompleteCareReport(ctx, job, members, CareReport{Text: "rollback"}, due); err == nil {
		t.Fatal("memory failure ignored")
	}
	var count int64
	db.gdb.Model(&CareReport{}).Count(&count)
	if count != 0 {
		t.Fatal("report committed without memory")
	}
	db.gdb.Exec("DROP TRIGGER fail_care_memory")
	recovered, _ := db.ClaimCareModes(ctx, due.Add(4*time.Minute), 8)
	if len(recovered) != 1 || recovered[0].Token == job.Token {
		t.Fatal("lease not recovered", recovered)
	}
	if err := db.CompleteCareReport(ctx, job, members, CareReport{Text: "stale worker"}, due); !errors.Is(err, ErrCareStale) {
		t.Fatal("old worker committed", err)
	}
	skipped, _ := db.ClaimCareModes(ctx, due.Add(24*time.Hour), 8)
	if len(skipped) != 0 {
		t.Fatal("outdated greeting replayed", skipped)
	}
}

func TestCareHistoryCursorIsIncrementalAndSpaceIsolated(t *testing.T) {
	db, _, now := setupCareDB(t)
	ctx := context.Background()
	_, b, _ := db.CareMembers(ctx, "space")
	for _, row := range []CareReport{{ID: "evt_care_a", SessionID: "space", CreatedAt: now, BindingCreatedAt: b.CreatedAt}, {ID: "evt_care_b", SessionID: "space", CreatedAt: now, BindingCreatedAt: b.CreatedAt}, {ID: "evt_care_c", SessionID: "space", CreatedAt: now.Add(time.Minute), BindingCreatedAt: b.CreatedAt}, {ID: "evt_foreign", SessionID: "other", CreatedAt: now.Add(time.Hour), BindingCreatedAt: b.CreatedAt}} {
		if err := db.gdb.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	rows, err := db.ListCareReports(ctx, "space", 100, "evt_care_a")
	if err != nil || len(rows) != 2 || rows[0].ID != "evt_care_b" || rows[1].ID != "evt_care_c" {
		t.Fatal(rows, err)
	}
	rows, err = db.ListCareReports(ctx, "space", 100, "evt_care_c")
	if err != nil || len(rows) != 0 {
		t.Fatal("already read reports repeated", rows, err)
	}
	rows, err = db.ListCareReports(ctx, "space", 100, "evt_foreign")
	if err != nil || len(rows) != 3 {
		t.Fatal("foreign cursor affected own history", rows, err)
	}
}
