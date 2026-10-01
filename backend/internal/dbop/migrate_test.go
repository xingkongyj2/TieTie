package dbop

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestLegacyUserMigrationPreservesBinding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	legacy, err := gorm.Open(sqlite.Open("file:"+path), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		"CREATE TABLE users (id TEXT PRIMARY KEY, username TEXT NOT NULL, password_hash TEXT NOT NULL, code TEXT NOT NULL, created_at DATETIME)",
		"CREATE TABLE bindings (user_a TEXT, user_b TEXT, session_id TEXT NOT NULL, created_at DATETIME, PRIMARY KEY (user_a, user_b))",
		"INSERT INTO users (id, username, password_hash, code) VALUES ('usr_first', 'first', 'hash1', 'CODE0001'), ('usr_second', 'second', 'hash2', 'CODE0002')",
		"INSERT INTO bindings (user_a, user_b, session_id) VALUES ('usr_first', 'usr_second', 'sess_existing')",
	} {
		if err := legacy.Exec(query).Error; err != nil {
			t.Fatal(err)
		}
	}
	legacySQL, _ := legacy.DB()
	_ = legacySQL.Close()

	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	first, err := db.GetUserByUsername(ctx, "first")
	if err != nil || first == nil || first.ID != 1 || first.Password != "" {
		t.Fatalf("first user migration: %+v, %v", first, err)
	}
	if db.gdb.Migrator().HasColumn("users", "legacy_password_hash") {
		t.Fatal("legacy_password_hash remains in users")
	}
	if hash, err := db.GetLegacyPasswordHash(ctx, first.ID); err != nil || hash != "hash1" {
		t.Fatalf("legacy password migration: %q, %v", hash, err)
	}
	second, err := db.GetUserByUsername(ctx, "second")
	if err != nil || second == nil || second.ID != 2 {
		t.Fatalf("second user migration: %+v, %v", second, err)
	}
	binding, err := db.GetBindingByPair(ctx, second.ID, first.ID)
	if err != nil || binding == nil || binding.SessionID != "sess_existing" || binding.UserA != 1 || binding.UserB != 2 {
		t.Fatalf("binding migration: %+v, %v", binding, err)
	}
	newUser := &User{Username: "third", Password: "plain-text", Code: "CODE0003"}
	if err := db.CreateUser(ctx, newUser); err != nil || newUser.ID != 3 {
		t.Fatalf("autoincrement: %+v, %v", newUser, err)
	}
}

func TestExistingConflictingBindingsAreArchived(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conflict.db")
	old, err := gorm.Open(sqlite.Open("file:"+path), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		"CREATE TABLE users (id INTEGER PRIMARY KEY AUTOINCREMENT, username TEXT NOT NULL, password TEXT NOT NULL, legacy_password_hash TEXT, code TEXT NOT NULL, created_at DATETIME)",
		"CREATE TABLE bindings (user_a INTEGER, user_b INTEGER, session_id TEXT NOT NULL, created_at DATETIME, PRIMARY KEY (user_a, user_b))",
		"INSERT INTO users (id, username, password, legacy_password_hash, code) VALUES (1, 'first', '', 'hash1', 'CODE0001'), (2, 'second', 'plain', '', 'CODE0002'), (3, 'third', 'plain', '', 'CODE0003')",
		"INSERT INTO bindings (user_a, user_b, session_id, created_at) VALUES (1, 2, 'first-session', '2026-01-01'), (2, 3, 'later-session', '2026-02-01')",
	} {
		if err := old.Exec(query).Error; err != nil {
			t.Fatal(err)
		}
	}
	oldSQL, _ := old.DB()
	_ = oldSQL.Close()

	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if exists, err := sqliteColumnExists(db.gdb, "users", "legacy_password_hash"); err != nil || exists {
		t.Fatalf("obsolete column remains: %v, %v", exists, err)
	}
	if hash, err := db.GetLegacyPasswordHash(context.Background(), 1); err != nil || hash != "hash1" {
		t.Fatalf("legacy hash not preserved: %q, %v", hash, err)
	}
	if binding, err := db.GetLatestBindingByUser(context.Background(), 2); err != nil || binding == nil || binding.SessionID != "first-session" {
		t.Fatalf("first binding not retained: %+v, %v", binding, err)
	}
	if binding, err := db.GetLatestBindingByUser(context.Background(), 3); err != nil || binding != nil {
		t.Fatalf("conflicting binding remains: %+v, %v", binding, err)
	}
	var archived int64
	if err := db.gdb.Raw("SELECT COUNT(*) FROM archived_bindings WHERE session_id = 'later-session'").Scan(&archived).Error; err != nil || archived != 1 {
		t.Fatalf("conflicting binding not archived: %d, %v", archived, err)
	}
}
