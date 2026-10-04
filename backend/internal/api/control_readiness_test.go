package api

import (
	"testing"
	"time"

	"tietie/backend/internal/qoder"
)

func TestControlReadinessRetryKeepsToolWaitBackoff(t *testing.T) {
	for _, test := range []struct {
		name    string
		history *qoder.MessagesResult
		want    time.Duration
	}{
		{name: "running before idle event", history: &qoder.MessagesResult{Session: &qoder.PublicSession{Status: "running"}}, want: time.Second},
		{name: "idle awaiting an answer", history: &qoder.MessagesResult{Session: &qoder.PublicSession{Status: "idle"}, Messages: []qoder.PublicMessage{{Kind: "ask", Answered: false}}}, want: 15 * time.Second},
		{name: "unknown session", history: &qoder.MessagesResult{Session: &qoder.PublicSession{Status: "unknown"}}, want: 15 * time.Second},
		{name: "archived session", history: &qoder.MessagesResult{Session: &qoder.PublicSession{Status: "archived"}}, want: 15 * time.Second},
		{name: "missing snapshot", history: &qoder.MessagesResult{}, want: 15 * time.Second},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := controlReadinessRetryDelay(test.history); got != test.want {
				t.Fatalf("retry delay = %v, want %v", got, test.want)
			}
			if conversationIdle(test.history) {
				t.Fatal("a not-ready conversation must not execute control actions")
			}
		})
	}
}
