package dbop

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestBindingAllowsOnlyOnePartner(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "binding.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	for _, u := range []*User{
		{Username: "first", Password: "secret", Code: "CODE0001"},
		{Username: "second", Password: "secret", Code: "CODE0002"},
		{Username: "third", Password: "secret", Code: "CODE0003"},
	} {
		if err := db.CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	if created, err := db.CreateBinding(ctx, 1, 2, "session-first"); err != nil || !created {
		t.Fatalf("initial binding: %v, %v", created, err)
	}
	if created, err := db.CreateBinding(ctx, 2, 1, "session-duplicate"); err != nil || created {
		t.Fatalf("same pair should reuse binding: %v, %v", created, err)
	}
	if created, err := db.CreateBinding(ctx, 2, 3, "session-conflict"); !errors.Is(err, ErrAlreadyBound) || created {
		t.Fatalf("new partner should be rejected: %v, %v", created, err)
	}
	if binding, err := db.GetLatestBindingByUser(ctx, 3); err != nil || binding != nil {
		t.Fatalf("third user should remain unbound: %+v, %v", binding, err)
	}
}
