package dbop

import (
	"context"
	"strings"
	"testing"

	"tietie/backend/internal/memoryspace"
)

func TestProfileCorrectionIsAtomicAndSurvivesRestart(t *testing.T) {
	db, path := openReminderTestDB(t)
	ctx := context.Background()
	for _, user := range []User{{ID: 1, Username: "一", Code: "3001"}, {ID: 2, Username: "二", Code: "3002"}} {
		if err := db.CreateUser(ctx, &user); err != nil {
			t.Fatal(err)
		}
	}
	profile := UserProfile{UserID: 1, Gender: "female", Birthday: "1998-11-16", Hobbies: []string{"画画"}, Bio: "爱看展", Avatar: "/avatars/cream-cat.png"}
	if _, err := db.SaveUserProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	binding, err := db.GetLatestBindingByUser(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SavePrivateChannel(ctx, PrivateChannel{SessionID: "private", SpaceID: "space", OwnerID: 1, BindingCreatedAt: binding.CreatedAt}); err != nil {
		t.Fatal(err)
	}
	if err := db.gdb.Exec("CREATE TRIGGER fail_profile_memory BEFORE INSERT ON memory_records BEGIN SELECT RAISE(ABORT, 'memory unavailable'); END").Error; err != nil {
		t.Fatal(err)
	}
	profile.Birthday, profile.Hobbies = "", []string{}
	if _, err := db.SaveUserProfile(ctx, profile); err == nil {
		t.Fatal("memory failure ignored")
	}
	got, err := db.GetUserProfile(ctx, 1)
	if err != nil || got.Birthday != "1998-11-16" || len(got.Hobbies) != 1 {
		t.Fatal("profile committed without correction memory", got, err)
	}
	if err := db.gdb.Exec("DROP TRIGGER fail_profile_memory").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveUserProfile(ctx, profile); err != nil {
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
	got, err = reopened.GetUserProfile(ctx, 1)
	if err != nil || got.Birthday != "" || len(got.Hobbies) != 0 || got.Bio != "爱看展" {
		t.Fatal("profile clear lost on restart", got, err)
	}
	for _, session := range []string{"space", "private"} {
		key := MemoryID(session, memoryspace.FactPath("profile", "self", 1, "account_profile"))
		record, err := reopened.GetMemoryRecord(ctx, key, session)
		if err != nil || record == nil || record.State != "pending" || record.OwnerID != 1 || strings.Contains(record.Content, "画画") {
			t.Fatal("current profile outbox missing or stale", record, err)
		}
	}
}
