package dbop

import (
	"context"
	"testing"
	"time"
)

func TestImpressionRejectsOldGenerationAndRecoversPendingWork(t *testing.T) {
	db, _ := openReminderTestDB(t)
	ctx := context.Background()
	if _, err := db.EnsureImpression(ctx, "space", 2, "old", false); err != nil {
		t.Fatal(err)
	}
	jobs, err := db.ClaimImpressions(ctx, time.Now().Add(time.Second), 4)
	if err != nil || len(jobs) != 1 {
		t.Fatal(jobs, err)
	}
	if _, err := db.EnsureImpression(ctx, "space", 2, "new", false); err != nil {
		t.Fatal(err)
	}
	if err := db.FinishImpression(ctx, jobs[0], "old summary", ""); err != nil {
		t.Fatal(err)
	}
	row, _ := db.GetImpression(ctx, "space", 2)
	if row.Status != "pending" || row.Summary != "" || row.SourceHash != "new" {
		t.Fatal(row)
	}
	jobs, _ = db.ClaimImpressions(ctx, time.Now().Add(time.Second), 4)
	if err := db.RecoverImpressions(ctx); err != nil {
		t.Fatal(err)
	}
	jobs, _ = db.ClaimImpressions(ctx, time.Now().Add(time.Second), 4)
	if len(jobs) != 1 {
		t.Fatal(jobs)
	}
	if err := db.FinishImpression(ctx, jobs[0], "", "try again"); err != nil {
		t.Fatal(err)
	}
	row, _ = db.EnsureImpression(ctx, "space", 2, "new", false)
	if row.Status != "failed" {
		t.Fatal("every poll retries failed inference")
	}
	row, _ = db.EnsureImpression(ctx, "space", 2, "new", true)
	if row.Status != "pending" {
		t.Fatal("explicit retry did not enqueue")
	}
}
