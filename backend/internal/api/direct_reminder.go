package api

import (
	"context"
	"fmt"
	"tietie/backend/internal/conversation"
	"tietie/backend/internal/dbop"
	"time"
)

// The key belongs to the authenticated input, independent of cloud event IDs
// or the assistant's wording. History replay and background sync share it.
func (s *Server) saveDirectReminder(ctx context.Context, space conversation.Context, text string) (*dbop.Reminder, error) {
	parsed, ok := conversation.ParseSingleReminder(text, space)
	if !ok || !parsed.DueAt.After(space.Now) {
		return nil, nil
	}
	key := fmt.Sprintf("%d/%s/direct", space.AuthorID, space.Now.Format(time.RFC3339Nano))
	reminder, _, err := s.DB.ApplyReminderAction(ctx, space.SessionID, "direct_"+key, 0, dbop.ReminderAction{
		Type: "create", Title: parsed.Title, DueAt: parsed.DueAt, RecipientIDs: parsed.RecipientIDs, CreatedBy: space.AuthorID, RequestKey: key})
	return reminder, err
}
