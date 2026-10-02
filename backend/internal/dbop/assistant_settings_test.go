package dbop

import (
	"context"
	"strings"
	"testing"

	"tietie/backend/internal/memoryspace"
)

func TestSpeakingStyleChannelInitializationIsolationAndAtomicCorrection(t *testing.T) {
	db, path := openReminderTestDB(t)
	ctx := context.Background()
	if err := db.SaveAssistantStyle(ctx, "space", 1, "concise"); err != nil {
		t.Fatal(err)
	}
	binding, err := db.GetBindingBySessionID(ctx, "space")
	if err != nil || binding == nil {
		t.Fatal(binding, err)
	}
	channel := PrivateChannel{SessionID: "private", SpaceID: "space", OwnerID: 1, BindingCreatedAt: binding.CreatedAt}
	if err := db.CreateInitializedPrivateChannel(ctx, channel, SpaceMemoryStore{SessionID: "private", StoreID: "private-store"}); err != nil {
		t.Fatal(err)
	}
	style, err := db.GetAssistantStyle(ctx, "private")
	if err != nil || style.Tone != "concise" {
		t.Fatal("private initialization lost style", style, err)
	}
	if err := db.SetBehaviorInstructions(ctx, "private", binding.CreatedAt, "private protocol"); err != nil {
		t.Fatal(err)
	}
	if err := db.gdb.Exec("CREATE TRIGGER fail_style BEFORE INSERT ON memory_records WHEN NEW.session_id='private' BEGIN SELECT RAISE(ABORT, 'memory unavailable'); END").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.SaveAssistantStyle(ctx, "space", 2, "playful"); err == nil {
		t.Fatal("partial style save committed")
	}
	style, _ = db.GetAssistantStyle(ctx, "space")
	if style.Tone != "concise" {
		t.Fatal("transaction rollback lost shared style", style)
	}
	if err := db.gdb.Exec("DROP TRIGGER fail_style").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.SaveAssistantStyle(ctx, "space", 2, "playful"); err != nil {
		t.Fatal(err)
	}
	if err := db.SetBehaviorInstructions(ctx, "private", binding.CreatedAt, "new private protocol"); err != nil {
		t.Fatal(err)
	}
	root, err := db.GetMemoryRecord(ctx, MemoryID("private", memoryspace.BehaviorPath), "private")
	if err != nil || !strings.Contains(root.Content, `"tone":"playful"`) || !strings.Contains(root.Content, "new private protocol") {
		t.Fatal("protocol overwrote style or style overwrote protocol", root, err)
	}
	if err := db.SaveAssistantStyle(ctx, "space", 99, "warm"); err == nil {
		t.Fatal("foreign member changed style")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	style, err = reopened.GetAssistantStyle(ctx, "private")
	if err != nil || style.Tone != "playful" {
		t.Fatal("restart lost style", style, err)
	}
	style, err = reopened.GetAssistantStyle(ctx, "different-conversation")
	if err != nil || style.Tone != "warm" {
		t.Fatal("style leaked to another conversation", style, err)
	}
}
