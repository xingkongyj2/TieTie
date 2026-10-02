package dbop

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"tietie/backend/internal/memoryspace"
)

func TestAnniversarySavePinCorrectionAndRestart(t *testing.T) {
	db, path := openReminderTestDB(t)
	ctx := context.Background()
	first, err := db.ApplyAnniversary(ctx, "space", "first", 1, "", "第一次一起旅行", "2025-05-24", "first_meet")
	if err != nil {
		t.Fatal(err)
	}
	second, err := db.ApplyAnniversary(ctx, "space", "second", 2, "", "下次旅行", "2027-01-01", "other")
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []string{"first", "duplicate"} {
		got, err := db.ApplyAnniversary(ctx, "space", request, 1, "", first.Title, first.Date, first.Kind)
		if err != nil || got.ID != first.ID {
			t.Fatal("duplicate anniversary", got, err)
		}
	}
	if featured, err := db.FeaturedAnniversary(ctx, "space"); err != nil || featured != nil {
		t.Fatal("a date was implicitly pinned", featured, err)
	}
	if _, err := db.PinAnniversary(ctx, "space", first.ID, 1, true); err != nil {
		t.Fatal(err)
	}
	secondMemory := MemoryID("space", AnniversaryMemoryPath(second.ID))
	if err := db.gdb.Exec(fmt.Sprintf("CREATE TRIGGER fail_anniversary_pin BEFORE INSERT ON memory_records WHEN NEW.id='%s' BEGIN SELECT RAISE(ABORT,'pin memory unavailable'); END", secondMemory)).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := db.PinAnniversary(ctx, "space", second.ID, 2, true); err == nil {
		t.Fatal("pin committed without memory")
	}
	featured, _ := db.FeaturedAnniversary(ctx, "space")
	if featured == nil || featured.ID != first.ID {
		t.Fatal("partial transaction removed existing pin", featured)
	}
	if err := db.gdb.Exec("DROP TRIGGER fail_anniversary_pin").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := db.PinAnniversary(ctx, "space", second.ID, 2, true); err != nil {
		t.Fatal(err)
	}
	rows, _, err := db.ListAnniversaries(ctx, "space", "", 50)
	if err != nil || len(rows) != 2 {
		t.Fatal(rows, err)
	}
	pinned := 0
	for _, row := range rows {
		if row.Pinned {
			pinned++
		}
	}
	if pinned != 1 {
		t.Fatal("multiple pins", pinned)
	}
	firstMemory, _ := db.GetMemoryRecord(ctx, MemoryID("space", AnniversaryMemoryPath(first.ID)), "space")
	if !strings.Contains(firstMemory.Content, `"pinned":false`) {
		t.Fatal("old pin survived in memory", firstMemory.Content)
	}
	if _, err := db.ApplyAnniversary(ctx, "space", "correct", 2, second.ID, "下次旅行", "2027-02-03", "other"); err != nil {
		t.Fatal(err)
	}
	featured, _ = db.FeaturedAnniversary(ctx, "space")
	if featured.ID != second.ID || featured.Date != "2027-02-03" {
		t.Fatal("correction lost pin", featured)
	}
	old, err := db.GetMemoryRevision(ctx, "space", secondMemory, 1)
	if err != nil || old == nil || !strings.Contains(old.Content, "2027-01-01") {
		t.Fatal("original anniversary history lost", old, err)
	}
	if _, err := db.CreateBinding(ctx, 3, 4, "other"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ApplyAnniversary(ctx, "other", "forge", 3, second.ID, "foreign", "2025-01-01", "other"); err == nil {
		t.Fatal("another space updated anniversary")
	}
	if _, err := db.PinAnniversary(ctx, "space", first.ID, 3, true); err == nil {
		t.Fatal("outsider pinned anniversary")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	featured, err = reopened.FeaturedAnniversary(ctx, "space")
	if err != nil || featured == nil || featured.ID != second.ID || featured.Date != "2027-02-03" {
		t.Fatal("restart lost anniversary", featured, err)
	}
	if _, err := reopened.PinAnniversary(ctx, "space", second.ID, 1, false); err != nil {
		t.Fatal(err)
	}
	if featured, err := reopened.FeaturedAnniversary(ctx, "space"); err != nil || featured != nil {
		t.Fatal("cancel pin did not restore space default", featured, err)
	}
	var docs []MemoryRecord
	reopened.gdb.Where("kind IN ('template','projection') AND operation='upsert'").Find(&docs)
	for _, doc := range docs {
		if err := memoryspace.ValidateDocument(doc.Path, doc.Content); err != nil {
			t.Fatal("new memory schema created", doc.Path, err)
		}
	}
}

func TestAnniversaryPaginationDatesAndPrivateIsolation(t *testing.T) {
	db, _ := openReminderTestDB(t)
	ctx := context.Background()
	for _, date := range []string{"2025-02-29", "2026-13-01", "10-02", "2026-2-01"} {
		if _, err := db.ApplyAnniversary(ctx, "space", date, 1, "", "日期", date, "other"); err == nil {
			t.Fatal("invalid date accepted", date)
		}
	}
	for i := 0; i < 52; i++ {
		if _, err := db.ApplyAnniversary(ctx, "space", fmt.Sprintf("date-%d", i), 1, "", fmt.Sprintf("记录%d", i), "2024-02-29", "other"); err != nil {
			t.Fatal(err)
		}
	}
	first, cursor, err := db.ListAnniversaries(ctx, "space", "", 50)
	if err != nil || len(first) != 50 || cursor == "" {
		t.Fatal(len(first), cursor, err)
	}
	last, next, err := db.ListAnniversaries(ctx, "space", cursor, 50)
	if err != nil || len(last) != 2 || next != "" {
		t.Fatal(len(last), next, err)
	}
	if first[0].Title != "记录51" || last[1].Title != "记录0" {
		t.Fatal("newly added anniversaries must appear first", first[0].Title, last[1].Title)
	}
	seen := map[string]bool{}
	for _, row := range append(first, last...) {
		if seen[row.ID] {
			t.Fatal("pagination duplicated date")
		}
		seen[row.ID] = true
	}
	if _, err := db.PinAnniversary(ctx, "space", last[1].ID, 1, true); err != nil {
		t.Fatal(err)
	}
	newest, _, err := db.ListAnniversaries(ctx, "space", "", 1)
	if err != nil || len(newest) != 1 || newest[0].ID != first[0].ID {
		t.Fatal("pinning must not change creation order", newest, err)
	}
	binding, _ := db.GetBindingBySessionID(ctx, "space")
	if err := db.SavePrivateChannel(ctx, PrivateChannel{SessionID: "private", SpaceID: "space", OwnerID: 1, BindingCreatedAt: binding.CreatedAt}); err != nil {
		t.Fatal(err)
	}
	private, err := db.ApplyAnniversary(ctx, "private", "private-date", 1, "", "私密日期", "2026-10-01", "other")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.ListAnniversaries(ctx, "space", private.ID, 50); err == nil {
		t.Fatal("private cursor admitted")
	}
	rows, _, err := db.ListAnniversaries(ctx, "private", "", 50)
	if err != nil || len(rows) != 1 || rows[0].ID != private.ID {
		t.Fatal("private anniversary not isolated", rows, err)
	}
}
