package dbop

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/gorm/schema"
)

var impressionTestNow = time.Date(2026, 10, 7, 8, 30, 0, 0, time.UTC)

func impressionUserRows(user User) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "ai_profile", "ai_profile_analyzed_at", "ai_profile_session_id"}).AddRow(user.ID, user.AIProfile, user.AIProfileAnalyzedAt, user.AIProfileSessionID)
}

func impressionRows(row *Impression) *sqlmock.Rows {
	rows := sqlmock.NewRows([]string{"session_id", "target_id", "source_hash", "summary", "status", "error", "run_at", "generated_at", "updated_at"})
	if row != nil {
		rows.AddRow(row.SessionID, row.TargetID, row.SourceHash, row.Summary, row.Status, row.Error, row.RunAt, row.GeneratedAt, row.UpdatedAt)
	}
	return rows
}

func expectImpressionUserLock(mock sqlmock.Sqlmock, user User) {
	mock.ExpectQuery("SELECT .* FROM `users` .*FOR UPDATE").WithArgs(user.ID, 1).WillReturnRows(impressionUserRows(user))
}

func expectImpressionLock(mock sqlmock.Sqlmock, session string, target int64, row *Impression) {
	mock.ExpectQuery("SELECT .* FROM `impressions` .*FOR UPDATE").WithArgs(session, target, 1).WillReturnRows(impressionRows(row))
}

type impressionTokenArg struct{}

func (impressionTokenArg) Match(value driver.Value) bool {
	text, ok := value.(string)
	return ok && regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(text)
}

type impressionUTCArg struct{ start, end time.Time }

func (arg impressionUTCArg) Match(value driver.Value) bool {
	instant, ok := value.(time.Time)
	return ok && instant.Location() == time.UTC && instant.Nanosecond()%1000 == 0 && !instant.Before(arg.start) && instant.Before(arg.end)
}

func checkImpressionExpectations(t *testing.T, mock sqlmock.Sqlmock) {
	t.Helper()
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAIProfileCacheFieldsArePrivateAndDoNotChangeAccountCreation(t *testing.T) {
	parsed, err := schema.Parse(&User{}, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"AIProfile", "AIProfileAnalyzedAt", "AIProfileSessionID"} {
		field := parsed.LookUpField(name)
		if field == nil || field.Creatable || !field.Updatable || !field.Readable {
			t.Fatalf("%s must be read/update-only", name)
		}
	}
	body, err := json.Marshal(User{ID: 7, AIProfile: "private portrait", AIProfileAnalyzedAt: &impressionTestNow, AIProfileSessionID: "private-space"})
	if err != nil || strings.Contains(string(body), "private") || strings.Contains(string(body), "aiProfile") {
		t.Fatalf("account JSON must not disclose portrait cache: %s %v", body, err)
	}
}

func TestDailyImpressionUsesBeijingNaturalDayAndSourceSpace(t *testing.T) {
	beforeMidnight := time.Date(2026, 10, 7, 15, 59, 59, 0, time.UTC)
	for _, tc := range []struct {
		name    string
		now     time.Time
		session string
		want    bool
	}{
		{"same Beijing day across UTC hours", beforeMidnight, "space", true},
		{"next Beijing day", beforeMidnight.Add(time.Second), "space", false},
		{"new space cannot read former partner context", beforeMidnight, "new-space", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock := mockWechatDB(t)
			generated := time.Date(2026, 10, 6, 16, 0, 0, 0, time.UTC)
			mock.ExpectQuery("SELECT .* FROM `users`").WithArgs(int64(7), 1).WillReturnRows(impressionUserRows(User{ID: 7, AIProfile: "today portrait", AIProfileAnalyzedAt: &generated, AIProfileSessionID: "space"}))
			row, err := db.GetDailyImpression(context.Background(), tc.session, 7, tc.now)
			if err != nil || (row != nil) != tc.want {
				t.Fatalf("row=%#v error=%v", row, err)
			}
			if row != nil && (row.Status != "ready" || row.Summary != "today portrait" || !row.GeneratedAt.Equal(generated)) {
				t.Fatalf("cache changed its successful analysis: %#v", row)
			}
			checkImpressionExpectations(t, mock)
		})
	}
}

func TestEnsureDailyImpressionReturnsTodayCacheWithoutInspectingSourcesOrJobs(t *testing.T) {
	db, mock := mockWechatDB(t)
	generated := impressionTestNow.Add(-time.Hour)
	mock.ExpectBegin()
	expectImpressionUserLock(mock, User{ID: 7, AIProfile: "already analyzed", AIProfileAnalyzedAt: &generated, AIProfileSessionID: "space"})
	mock.ExpectCommit()
	row, err := db.EnsureDailyImpression(context.Background(), "space", 7, true, impressionTestNow)
	if err != nil || row == nil || row.Status != "ready" || row.Summary != "already analyzed" || !row.GeneratedAt.Equal(generated) {
		t.Fatalf("row=%#v error=%v", row, err)
	}
	checkImpressionExpectations(t, mock)
}

func TestEnsureDailyImpressionCreatesOnlyOnePendingJob(t *testing.T) {
	db, mock := mockWechatDB(t)
	mock.ExpectBegin()
	expectImpressionUserLock(mock, User{ID: 7})
	expectImpressionLock(mock, "space", 7, nil)
	mock.ExpectExec("INSERT INTO `impressions`").WithArgs("space", int64(7), impressionTokenArg{}, "", "pending", "", impressionTestNow, nil, impressionTestNow).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	first, err := db.EnsureDailyImpression(context.Background(), "space", 7, false, impressionTestNow)
	if err != nil || first == nil || first.Status != "pending" || first.NextAnalysisAt != nil {
		t.Fatalf("row=%#v error=%v", first, err)
	}
	mock.ExpectBegin()
	expectImpressionUserLock(mock, User{ID: 7})
	expectImpressionLock(mock, "space", 7, first)
	mock.ExpectCommit()
	again, err := db.EnsureDailyImpression(context.Background(), "space", 7, true, impressionTestNow.Add(time.Minute))
	if err != nil || again == nil || again.SourceHash != first.SourceHash || !again.RunAt.Equal(first.RunAt) {
		t.Fatalf("repeat request replaced a pending job: %#v %v", again, err)
	}
	checkImpressionExpectations(t, mock)
}

func TestEnsureDailyImpressionQueuesNewDayAndRetainsYesterdaySummary(t *testing.T) {
	db, mock := mockWechatDB(t)
	yesterday := impressionTestNow.Add(-24 * time.Hour)
	old := &Impression{SessionID: "space", TargetID: 7, SourceHash: "yesterday-job", Summary: "yesterday portrait", Status: "ready", GeneratedAt: &yesterday}
	mock.ExpectBegin()
	expectImpressionUserLock(mock, User{ID: 7, AIProfile: old.Summary, AIProfileAnalyzedAt: &yesterday, AIProfileSessionID: "space"})
	expectImpressionLock(mock, "space", 7, old)
	mock.ExpectExec("UPDATE `impressions`").WithArgs("", yesterday, impressionTestNow, impressionTokenArg{}, "pending", old.Summary, impressionTestNow, "space", int64(7)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	row, err := db.EnsureDailyImpression(context.Background(), "space", 7, false, impressionTestNow)
	if err != nil || row == nil || row.Status != "pending" || row.Summary != old.Summary || row.SourceHash == old.SourceHash || !row.GeneratedAt.Equal(yesterday) {
		t.Fatalf("new day must queue a fresh job while retaining its prior summary: %#v %v", row, err)
	}
	checkImpressionExpectations(t, mock)
}

func TestConcurrentEnsureRequestsReuseGeneratingJobAcrossMidnight(t *testing.T) {
	db, mock := mockWechatDB(t)
	mock.MatchExpectationsInOrder(false)
	job := &Impression{SessionID: "space", TargetID: 7, SourceHash: "in-flight-job", Status: "generating", RunAt: impressionTestNow.Add(-24 * time.Hour)}
	for range 2 {
		mock.ExpectBegin()
		expectImpressionUserLock(mock, User{ID: 7})
		expectImpressionLock(mock, "space", 7, job)
		mock.ExpectCommit()
	}
	var group sync.WaitGroup
	group.Add(2)
	for range 2 {
		go func() {
			defer group.Done()
			row, err := db.EnsureDailyImpression(context.Background(), "space", 7, true, impressionTestNow)
			if err != nil || row == nil || row.Status != "generating" || row.SourceHash != job.SourceHash || !row.RunAt.Equal(job.RunAt) {
				t.Errorf("in-flight job must survive repeat requests and midnight: %#v %v", row, err)
			}
		}()
	}
	group.Wait()
	checkImpressionExpectations(t, mock)
}

func TestEnsureDailyImpressionMigratesTodaysExistingSuccess(t *testing.T) {
	db, mock := mockWechatDB(t)
	generated := impressionTestNow.Add(-time.Hour)
	old := &Impression{SessionID: "space", TargetID: 7, Summary: "existing portrait", Status: "ready", GeneratedAt: &generated}
	mock.ExpectBegin()
	expectImpressionUserLock(mock, User{ID: 7})
	expectImpressionLock(mock, "space", 7, old)
	mock.ExpectExec("UPDATE `users`").WithArgs(old.Summary, generated, "space", int64(7)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	row, err := db.EnsureDailyImpression(context.Background(), "space", 7, true, impressionTestNow)
	if err != nil || row == nil || row.Status != "ready" || !row.GeneratedAt.Equal(generated) {
		t.Fatalf("existing daily success must migrate without a new job: %#v %v", row, err)
	}
	checkImpressionExpectations(t, mock)
}

func TestEnsureDailyImpressionRetriesOnlyExplicitFailures(t *testing.T) {
	for _, retry := range []bool{false, true} {
		t.Run(map[bool]string{false: "keep failed", true: "explicit retry"}[retry], func(t *testing.T) {
			db, mock := mockWechatDB(t)
			old := &Impression{SessionID: "space", TargetID: 7, SourceHash: "failed-job", Status: "failed", Error: "generation failed", UpdatedAt: impressionTestNow}
			mock.ExpectBegin()
			expectImpressionUserLock(mock, User{ID: 7})
			expectImpressionLock(mock, "space", 7, old)
			if retry {
				mock.ExpectExec("UPDATE `impressions`").WithArgs("", nil, impressionTestNow, impressionTokenArg{}, "pending", "", impressionTestNow, "space", int64(7)).WillReturnResult(sqlmock.NewResult(0, 1))
			}
			mock.ExpectCommit()
			row, err := db.EnsureDailyImpression(context.Background(), "space", 7, retry, impressionTestNow)
			if err != nil || row == nil || (row.Status == "pending") != retry || retry && row.SourceHash == old.SourceHash {
				t.Fatalf("row=%#v error=%v", row, err)
			}
			checkImpressionExpectations(t, mock)
		})
	}
}

func TestYesterdayFailureAutomaticallyQueuesTodaysFirstAnalysis(t *testing.T) {
	db, mock := mockWechatDB(t)
	yesterday := impressionTestNow.Add(-24 * time.Hour)
	old := &Impression{SessionID: "space", TargetID: 7, SourceHash: "failed-job", Summary: "previous successful portrait", Status: "failed", Error: "generation failed", GeneratedAt: &yesterday, UpdatedAt: yesterday}
	mock.ExpectBegin()
	expectImpressionUserLock(mock, User{ID: 7})
	expectImpressionLock(mock, "space", 7, old)
	mock.ExpectExec("UPDATE `impressions`").WithArgs("", yesterday, impressionTestNow, impressionTokenArg{}, "pending", old.Summary, impressionTestNow, "space", int64(7)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	row, err := db.EnsureDailyImpression(context.Background(), "space", 7, false, impressionTestNow)
	if err != nil || row == nil || row.Status != "pending" || row.Summary != old.Summary || row.SourceHash == old.SourceHash || row.Error != "" {
		t.Fatalf("a new day must not remain stuck on yesterday's failure: %#v %v", row, err)
	}
	checkImpressionExpectations(t, mock)
}

func TestNewSpaceWaitsUntilNextBeijingDayWithoutExposingOldSummary(t *testing.T) {
	db, mock := mockWechatDB(t)
	generated := impressionTestNow.Add(-time.Hour)
	_, tomorrow := impressionDay(impressionTestNow)
	mock.ExpectBegin()
	expectImpressionUserLock(mock, User{ID: 7, AIProfile: "former partner context", AIProfileAnalyzedAt: &generated, AIProfileSessionID: "old-space"})
	expectImpressionLock(mock, "new-space", 7, nil)
	mock.ExpectExec("INSERT INTO `impressions`").WithArgs("new-space", int64(7), impressionTokenArg{}, "", "pending", "", tomorrow, nil, impressionTestNow).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	row, err := db.EnsureDailyImpression(context.Background(), "new-space", 7, true, impressionTestNow)
	if err != nil || row == nil || row.Summary != "" || row.GeneratedAt != nil || row.NextAnalysisAt == nil || !row.NextAnalysisAt.Equal(tomorrow) || !row.RunAt.Equal(tomorrow) {
		t.Fatalf("new space must defer without former context: %#v %v", row, err)
	}
	checkImpressionExpectations(t, mock)
}

func impressionGeneratingJob() Impression {
	return Impression{SessionID: "space", TargetID: 7, SourceHash: "current-job", Status: "generating", RunAt: time.Now().UTC().Add(time.Minute).Truncate(time.Microsecond)}
}

func TestFinishImpressionCommitsPortraitAndTaskTogetherAfterCurrentLeaseCAS(t *testing.T) {
	for _, matched := range []bool{true, false} {
		t.Run(map[bool]string{true: "current job", false: "stale job"}[matched], func(t *testing.T) {
			db, mock := mockWechatDB(t)
			job := impressionGeneratingJob()
			start, end := impressionDay(time.Now())
			instant := impressionUTCArg{start, end}
			mock.ExpectBegin()
			expectImpressionUserLock(mock, User{ID: 7})
			affected := int64(0)
			if matched {
				affected = 1
			}
			mock.ExpectExec("UPDATE `impressions` .*source_hash=\\? AND status='generating' AND run_at=\\?").WithArgs("", instant, "ready", "new portrait", instant, job.SessionID, job.TargetID, job.SourceHash, job.RunAt).WillReturnResult(sqlmock.NewResult(0, affected))
			if matched {
				mock.ExpectExec("UPDATE `users`").WithArgs("new portrait", instant, job.SessionID, job.TargetID).WillReturnResult(sqlmock.NewResult(0, 1))
			}
			mock.ExpectCommit()
			if err := db.FinishImpression(context.Background(), job, "new portrait", ""); err != nil {
				t.Fatal(err)
			}
			checkImpressionExpectations(t, mock)
		})
	}
}

func TestFinishFailureKeepsExistingSuccessAndPreviousSummary(t *testing.T) {
	db, mock := mockWechatDB(t)
	job := impressionGeneratingJob()
	yesterday := time.Now().UTC().Add(-24 * time.Hour)
	mock.ExpectBegin()
	expectImpressionUserLock(mock, User{ID: 7, AIProfile: "previous portrait", AIProfileAnalyzedAt: &yesterday, AIProfileSessionID: "space"})
	mock.ExpectExec("UPDATE `impressions` .*source_hash=\\? AND status='generating' AND run_at=\\?").WithArgs("generation failed", "failed", sqlmock.AnyArg(), job.SessionID, job.TargetID, job.SourceHash, job.RunAt).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if err := db.FinishImpression(context.Background(), job, "", "generation failed"); err != nil {
		t.Fatal(err)
	}
	checkImpressionExpectations(t, mock)
}

func TestFinishCannotOverwriteTodaysSuccessfulPortraitOrAnalysisTime(t *testing.T) {
	db, mock := mockWechatDB(t)
	job := impressionGeneratingJob()
	start, _ := impressionDay(time.Now())
	generated := start.Add(time.Minute)
	mock.ExpectBegin()
	expectImpressionUserLock(mock, User{ID: 7, AIProfile: "first success", AIProfileAnalyzedAt: &generated, AIProfileSessionID: "space"})
	mock.ExpectExec("UPDATE `impressions` .*source_hash=\\? AND status='generating' AND run_at=\\?").WithArgs("", generated, "ready", "first success", sqlmock.AnyArg(), job.SessionID, job.TargetID, job.SourceHash, job.RunAt).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if err := db.FinishImpression(context.Background(), job, "must not overwrite", ""); err != nil {
		t.Fatal(err)
	}
	checkImpressionExpectations(t, mock)
}

func TestFinishingNewSpaceCannotPublishOverTodaysOldSpaceSuccess(t *testing.T) {
	db, mock := mockWechatDB(t)
	job := impressionGeneratingJob()
	start, tomorrow := impressionDay(time.Now())
	generated := start.Add(time.Minute)
	mock.ExpectBegin()
	expectImpressionUserLock(mock, User{ID: 7, AIProfile: "former partner context", AIProfileAnalyzedAt: &generated, AIProfileSessionID: "old-space"})
	mock.ExpectExec("UPDATE `impressions` .*source_hash=\\? AND status='generating' AND run_at=\\?").WithArgs("", nil, tomorrow, impressionTokenArg{}, "pending", "", sqlmock.AnyArg(), job.SessionID, job.TargetID, job.SourceHash, job.RunAt).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if err := db.FinishImpression(context.Background(), job, "cannot publish a second success", ""); err != nil {
		t.Fatal(err)
	}
	checkImpressionExpectations(t, mock)
}

func TestFinishCacheWriteFailureRollsBackSuccessfulTask(t *testing.T) {
	db, mock := mockWechatDB(t)
	job := impressionGeneratingJob()
	want := errors.New("cache write failed")
	mock.ExpectBegin()
	expectImpressionUserLock(mock, User{ID: 7})
	mock.ExpectExec("UPDATE `impressions`").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE `users`").WillReturnError(want)
	mock.ExpectRollback()
	if err := db.FinishImpression(context.Background(), job, "new portrait", ""); !errors.Is(err, want) {
		t.Fatalf("expected atomic rollback, got %v", err)
	}
	checkImpressionExpectations(t, mock)
}

func TestFinishingCachedJobDoesNotExtendYesterdayIntoToday(t *testing.T) {
	db, mock := mockWechatDB(t)
	job := impressionGeneratingJob()
	start, _ := impressionDay(time.Now())
	yesterday := start.Add(-time.Second)
	cached := Impression{SessionID: job.SessionID, TargetID: job.TargetID, Summary: "cached before midnight", Status: "ready", GeneratedAt: &yesterday}
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `impressions` .*source_hash=\\? AND status='generating' AND run_at=\\?").WithArgs("", yesterday, "ready", cached.Summary, sqlmock.AnyArg(), job.SessionID, job.TargetID, job.SourceHash, job.RunAt).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if err := db.FinishCachedImpression(context.Background(), job, cached); err != nil {
		t.Fatal(err)
	}
	checkImpressionExpectations(t, mock)
}

func TestFinishingCachedJobRejectsAnotherSpacesPortrait(t *testing.T) {
	db, mock := mockWechatDB(t)
	job := impressionGeneratingJob()
	generated := time.Now().UTC()
	cached := Impression{SessionID: "old-space", TargetID: job.TargetID, Summary: "former partner context", Status: "ready", GeneratedAt: &generated}
	if err := db.FinishCachedImpression(context.Background(), job, cached); err == nil {
		t.Fatal("cached completion must reject another space without any database access")
	}
	checkImpressionExpectations(t, mock)
}
