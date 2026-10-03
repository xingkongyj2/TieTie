package api

import (
	"testing"

	"tietie/backend/internal/dbop"
)

func TestCachedPublicMessageKeepsViewerIdentity(t *testing.T) {
	binding := &dbop.Binding{UserA: 11, UserB: 22}
	row := dbop.Message{ID: "evt_test", Sender: "user", UserID: 22, Source: "chat", Text: "你好", CloudCreatedAt: "2026-10-03T08:00:00Z"}
	self, ok := cachedPublicMessage(row, binding, 22)
	if !ok || self.Sender != "self" || self.UserID != 22 {
		t.Fatalf("owner's message not mapped to self: %#v, %v", self, ok)
	}
	partner, ok := cachedPublicMessage(row, binding, 11)
	if !ok || partner.Sender != "partner" || partner.UserID != 22 {
		t.Fatalf("other member's message not mapped to partner: %#v, %v", partner, ok)
	}
	row.UserID = 33
	if _, ok := cachedPublicMessage(row, binding, 11); ok {
		t.Fatal("message from a nonmember must stay hidden")
	}
	row.UserID = 22
	row.Source = "internal"
	if _, ok := cachedPublicMessage(row, binding, 11); ok {
		t.Fatal("internal messages must stay hidden")
	}
}
