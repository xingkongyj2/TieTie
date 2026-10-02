package dbop

import (
	"context"
	"errors"
	"strings"
	"testing"
	"tietie/backend/internal/memoryspace"
	"tietie/backend/internal/regions"
	"tietie/backend/internal/weather"
	"time"
)

func TestEitherMemberCanCorrectRegionsAndMetricsWithDurableMemory(t *testing.T) {
	db, path, now := setupCareDB(t)
	ctx := context.Background()
	b, _ := db.GetBindingBySessionID(ctx, "space")
	old, _ := db.GetUserProfile(ctx, 2)
	old.Bio = "喜欢看展"
	old.Birthday = "1998-11-16"
	old.Hobbies = []string{"骑车"}
	_, _ = db.SaveUserProfile(ctx, *old)
	region, err := regions.ResolveNames("浙江", "杭州", "西湖")
	if err != nil {
		t.Fatal(err)
	}
	row, err := db.ApplyProfileRegion(ctx, "space", "move", 1, 2, b.CreatedAt, region)
	if err != nil || row.Region.City != "杭州市" || row.Bio != "喜欢看展" || row.Birthday != old.Birthday || row.RegionSourceUserID != 1 {
		t.Fatal(row, err)
	}
	self, _ := db.GetUserProfile(ctx, 1)
	if self.Region.City != "武汉市" {
		t.Fatal("partner update changed actor", self)
	}
	memory, err := db.GetMemoryRecord(ctx, MemoryID("space", memoryspace.FactPath("profile", "self", 2, "account_profile")), "space")
	if err != nil || !strings.Contains(memory.PendingContent, "西湖区") {
		t.Fatal(memory, err)
	}
	_, _ = db.ApplyProfileRegion(ctx, "space", "clear", 2, 2, b.CreatedAt, regions.Location{})
	_, _ = db.ApplyProfileRegion(ctx, "space", "move", 1, 2, b.CreatedAt, region)
	row, _ = db.GetUserProfile(ctx, 2)
	if row.Region.CityCode != "" {
		t.Fatal("old retry restored outdated region", row)
	}
	if _, err := db.ApplyProfileRegion(ctx, "space", "foreign", 99, 2, b.CreatedAt, region); !errors.Is(err, ErrReminderForbidden) {
		t.Fatal("foreign actor", err)
	}
	if _, err := db.ApplyProfileRegion(ctx, "space", "wrongtarget", 1, 99, b.CreatedAt, region); !errors.Is(err, ErrReminderForbidden) {
		t.Fatal("foreign target", err)
	}
	if _, err := db.ApplyProfileRegion(ctx, "space", "stale", 1, 2, b.CreatedAt.Add(time.Hour), region); !errors.Is(err, ErrReminderForbidden) {
		t.Fatal("stale binding", err)
	}
	pref, err := db.ApplyCarePreference(ctx, "space", "focus", 1, 2, b.CreatedAt, []string{"rain", "temperature"})
	if err != nil || strings.Join(pref.Metrics, ",") != "temperature,rain" {
		t.Fatal(pref, err)
	}
	_, _ = db.ApplyCarePreference(ctx, "space", "onlypm", 2, 2, b.CreatedAt, []string{"pm25"})
	_, _ = db.ApplyCarePreference(ctx, "space", "focus", 1, 2, b.CreatedAt, []string{"rain", "temperature"})
	metrics, _ := db.GetCarePreference(ctx, "space", 2)
	if strings.Join(metrics, ",") != "pm25" {
		t.Fatal("retry overwrote preferences", metrics)
	}
	_ = db.Close()
	db2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	metrics, err = db2.GetCarePreference(ctx, "space", 2)
	if err != nil || strings.Join(metrics, ",") != "pm25" {
		t.Fatal(metrics, err)
	}
	_, err = db2.ApplyCarePreference(ctx, "space", "default", 1, 2, b.CreatedAt, nil)
	if err != nil {
		t.Fatal(err)
	}
	metrics, _ = db2.GetCarePreference(ctx, "space", 2)
	if len(metrics) != len(weather.DefaultMetrics) {
		t.Fatal(metrics)
	}
	if err := db2.NotifyCareRegionMissing(ctx, "space", 1, "morning", now); err != nil {
		t.Fatal(err)
	}
	_ = db2.NotifyCareRegionMissing(ctx, "space", 2, "morning", now.Add(time.Second))
	reports, _ := db2.ListCareReports(ctx, "space", 10)
	if len(reports) != 1 || !strings.Contains(reports[0].Text, "@一 已填写") || !strings.Contains(reports[0].Text, "@二 还没有填写") {
		t.Fatal(reports)
	}
}
