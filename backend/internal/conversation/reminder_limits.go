package conversation

// ReminderTitleMaxRunes bounds the short, user-facing reminder card title.
// Detailed schedules, context and explanations belong in memory rather than
// making the title spill across the reminder card.
const ReminderTitleMaxRunes = 80
