package dbop

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestAnniversaryDeletionAtomicIdempotentAndKeepsHistory(t *testing.T) {
	db, path := openReminderTestDB(t)
	ctx := context.Background()
	keep, _ := db.ApplyAnniversary(ctx, "space", "keep", 1, "", "旅行", "2025-01-01", "other")
	row, err := db.ApplyAnniversary(ctx, "space", "wedding", 1, "", "结婚的日子", "2026-05-01", "wedding")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.PinAnniversary(ctx, "space", row.ID, 1, true); err != nil {
		t.Fatal(err)
	}
	key := MemoryID("space", AnniversaryMemoryPath(row.ID))
	if err := db.gdb.Exec(fmt.Sprintf("CREATE TRIGGER fail_anniversary_delete BEFORE INSERT ON memory_records WHEN NEW.id='%s' BEGIN SELECT RAISE(ABORT,'outbox unavailable'); END", key)).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := db.DeleteAnniversary(ctx, "space", "delete-wedding", 2, row.ID); err == nil {
		t.Fatal("deletion committed without durable outbox")
	}
	if featured, _ := db.FeaturedAnniversary(ctx, "space"); featured == nil || featured.ID != row.ID {
		t.Fatal("failed deletion removed the database pin")
	}
	if ids, cursor, _, err := db.AnniversaryDeletionChanges(ctx, "space", "0"); err != nil || len(ids) != 0 || cursor != "0" {
		t.Fatal("failed deletion leaked a receipt", ids, cursor, err)
	}
	db.gdb.Exec("DROP TRIGGER fail_anniversary_delete")
	deleted, err := db.DeleteAnniversary(ctx, "space", "delete-wedding", 2, row.ID)
	if err != nil || deleted.Title != row.Title {
		t.Fatal(deleted, err)
	}
	rows, _, _ := db.ListAnniversaries(ctx, "space", "", 50)
	if len(rows) != 1 || rows[0].ID != keep.ID {
		t.Fatal("database card was not removed", rows)
	}
	if featured, _ := db.FeaturedAnniversary(ctx, "space"); featured != nil {
		t.Fatal("deleted pin remained featured")
	}
	memory, _ := db.GetMemoryRecord(ctx, key, "space")
	if memory == nil || memory.Operation != "delete" || memory.State != "pending" {
		t.Fatal("cloud deletion was not queued", memory)
	}
	old, _ := db.GetMemoryRevision(ctx, "space", key, 1)
	if old == nil || !strings.Contains(old.Content, row.Date) {
		t.Fatal("deletion lost historical memory", old)
	}
	projection, err := db.FactProjection(ctx, *memory)
	if err != nil || projection == nil || strings.Contains(projection.Content, row.Title) || !strings.Contains(projection.Content, keep.Title) {
		t.Fatal("deletion removed unrelated memory or left the current date", projection, err)
	}
	if _, err := db.DeleteAnniversary(ctx, "space", "delete-wedding", 2, row.ID); err != nil {
		t.Fatal("deletion replay failed", err)
	}
	again, _ := db.GetMemoryRecord(ctx, key, "space")
	if again.Revision != memory.Revision {
		t.Fatal("replay repeated memory deletion")
	}
	ids, cursor, reset, err := db.AnniversaryDeletionChanges(ctx, "space", "0")
	if err != nil || reset || len(ids) != 1 || ids[0] != row.ID || cursor != "1" {
		t.Fatal("client deletion delta missing", ids, cursor, reset, err)
	}
	if _, err := db.DeleteAnniversary(ctx, "space", "outsider", 3, keep.ID); err == nil {
		t.Fatal("outsider deleted another member's date")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err := reopened.DeleteAnniversary(ctx, "space", "delete-wedding", 2, row.ID); err != nil {
		t.Fatal("restart lost deletion receipt", err)
	}
	if _, err := reopened.ApplyAnniversary(ctx, "space", "recreate", 1, "", row.Title, row.Date, row.Kind); err != nil {
		t.Fatal("explicit re-add was blocked by deleted history", err)
	}
}

func TestLegacyMemoryDeletionReconcilesAnniversaryAndKeepsCursor(t *testing.T) {
	db, path := openReminderTestDB(t)
	ctx := context.Background()
	older, _ := db.ApplyAnniversary(ctx, "space", "older", 1, "", "其他日期", "2025-01-01", "other")
	legacy, _ := db.ApplyAnniversary(ctx, "space", "legacy", 1, "", "结婚的日子", "2026-05-01", "wedding")
	memory, _ := db.GetMemoryRecord(ctx, MemoryID("space", AnniversaryMemoryPath(legacy.ID)), "space")
	memory.Operation, memory.PendingContent = "delete", ""
	if _, err := db.ApplyMemoryAction(ctx, "legacy-forget", *memory); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	rows, _, err := reopened.ListAnniversaries(ctx, "space", "", 50)
	if err != nil || len(rows) != 1 || rows[0].ID != older.ID {
		t.Fatal("legacy cancelled card survived restart", rows, err)
	}
	rows, _, err = reopened.ListAnniversaries(ctx, "space", legacy.ID, 50)
	if err != nil || len(rows) != 1 || rows[0].ID != older.ID {
		t.Fatal("deleted boundary blocked older pages", rows, err)
	}
	ids, _, _, _ := reopened.AnniversaryDeletionChanges(ctx, "space", "0")
	if len(ids) != 1 || ids[0] != legacy.ID {
		t.Fatal("legacy repair did not invalidate cached pages", ids)
	}
}
