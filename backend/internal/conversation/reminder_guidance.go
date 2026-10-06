package conversation

import "time"

// ReminderGuidance supplies authoritative facts for the two existing AI turns.
// It never creates an operation or substitutes for the model's final response.
func ReminderGuidance(e EnvelopeV2) EnvelopeV2 {
	e.ReminderRequest, e.ReplyInstructions = nil, ""
	if e.ReplyMode == SilentReply {
		return e
	}
	if e.Kind == "user_message" && e.Actor.Kind == "member" && !e.HasAttachments {
		ctx := Context{Now: e.CurrentTime, AuthorID: e.Actor.UserID, Members: e.Members}
		if request, ok := ParseSingleReminder(e.Text, ctx); ok && request.DueAt.After(e.CurrentTime) {
			e.ReminderRequest = &request
		}
		return e
	}
	if e.Kind != "action_result" || e.Actor.Kind != "system" || e.ReplyTo == nil || len(e.Results) != 1 {
		return e
	}
	result := e.Results[0]
	reminder := result.Reminder
	if result.Type != "create_reminder" || result.Status != "succeeded" || result.DatabaseStatus != "saved" || result.MemoryStatus != "synced" ||
		reminder == nil || reminder.ID == "" || reminder.ID != result.ReminderID || reminder.Title == "" || reminder.DueAt.IsZero() || reminder.Recurrence != nil || reminder.Status != "scheduled" || len(reminder.RecipientIDs) == 0 || len(reminder.RecipientIDs) > 2 || reminder.CreatedBy != e.ReplyTo.ID {
		return e
	}
	members := make(map[int64]bool, len(e.Members))
	for _, member := range e.Members {
		if member.ID > 0 {
			members[member.ID] = true
		}
	}
	if !members[e.ReplyTo.ID] {
		return e
	}
	seen := make(map[int64]bool, len(reminder.RecipientIDs))
	for _, id := range reminder.RecipientIDs {
		if !members[id] || seen[id] {
			return e
		}
		seen[id] = true
	}
	// Preserve the durable execution result while presenting its exact timestamp
	// in the application's timezone, eliminating another AI timezone calculation.
	copyReminder := *reminder
	copyReminder.DueAt = reminder.DueAt.In(time.FixedZone("Asia/Shanghai", 8*60*60))
	result.Reminder = &copyReminder
	e.Results = []ActionResult{result}
	return e
}
