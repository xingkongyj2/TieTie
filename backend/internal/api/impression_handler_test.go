package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tietie/backend/internal/auth"
	"tietie/backend/internal/config"
	"tietie/backend/internal/dbop"
	"tietie/backend/internal/qoder"
)

// Unconfigured operations intentionally fail: cache-hit tests must never read
// sources, queue another analysis, or reach a remote generator.
type impressionTestStore struct {
	impressionStore
	binding func(context.Context, string) (*dbop.Binding, error)
	daily   func(context.Context, string, int64, time.Time) (*dbop.Impression, error)
	ensure  func(context.Context, string, int64, bool, time.Time) (*dbop.Impression, error)
	apply   func(context.Context, string, dbop.MemoryRecord) (*dbop.MemoryRecord, error)
	sources func(context.Context, string, int64) (dbop.ImpressionSources, error)
	record  func(context.Context, string, string) (*dbop.MemoryRecord, error)
	finish  func(context.Context, dbop.Impression, string, string) error
	cached  func(context.Context, dbop.Impression, dbop.Impression) error
}

func (s impressionTestStore) GetBindingBySessionID(ctx context.Context, session string) (*dbop.Binding, error) {
	return s.binding(ctx, session)
}
func (s impressionTestStore) GetDailyImpression(ctx context.Context, session string, target int64, now time.Time) (*dbop.Impression, error) {
	return s.daily(ctx, session, target, now)
}
func (s impressionTestStore) EnsureDailyImpression(ctx context.Context, session string, target int64, retry bool, now time.Time) (*dbop.Impression, error) {
	return s.ensure(ctx, session, target, retry, now)
}
func (s impressionTestStore) ApplyMemoryAction(ctx context.Context, request string, record dbop.MemoryRecord) (*dbop.MemoryRecord, error) {
	return s.apply(ctx, request, record)
}
func (s impressionTestStore) ImpressionSources(ctx context.Context, session string, target int64) (dbop.ImpressionSources, error) {
	return s.sources(ctx, session, target)
}
func (s impressionTestStore) GetMemoryRecord(ctx context.Context, id, session string) (*dbop.MemoryRecord, error) {
	return s.record(ctx, id, session)
}
func (s impressionTestStore) FinishImpression(ctx context.Context, job dbop.Impression, summary, problem string) error {
	return s.finish(ctx, job, summary, problem)
}
func (s impressionTestStore) FinishCachedImpression(ctx context.Context, job, cached dbop.Impression) error {
	return s.cached(ctx, job, cached)
}

func impressionBinding(context.Context, string) (*dbop.Binding, error) {
	return &dbop.Binding{SessionID: "sess_shared", UserA: 8, UserB: 9, CreatedAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}, nil
}

func impressionRequest(method, query, body string, owner int64) *http.Request {
	r := httptest.NewRequest(method, "/api/qoder/sessions/sess_shared/partner-impression"+query, strings.NewReader(body))
	r.SetPathValue("id", "sess_shared")
	return r.WithContext(auth.ContextWithClaims(r.Context(), &auth.Claims{UserID: owner}))
}

func TestPartnerImpressionReturnsTodaysCacheBeforeSourcesOrRetry(t *testing.T) {
	generated := time.Now().UTC()
	cached := &dbop.Impression{SessionID: "sess_shared", TargetID: 9, Summary: "TA喜欢骑车。", Status: "ready", GeneratedAt: &generated}
	reads := 0
	store := impressionTestStore{binding: impressionBinding, daily: func(_ context.Context, session string, target int64, now time.Time) (*dbop.Impression, error) {
		reads++
		if session != "sess_shared" || target != 9 || now.IsZero() {
			t.Fatal("cache must be scoped to the authenticated partner and current day")
		}
		return cached, nil
	}}
	s := &Server{} // No Qoder dependency is needed to serve a completed profile.
	for _, query := range []string{"", "?retry=true&targetId=123"} {
		w := httptest.NewRecorder()
		s.servePartnerImpression(w, impressionRequest(http.MethodGet, query, "", 8), store, nil)
		var response struct{ Impression dbop.Impression }
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || w.Code != http.StatusOK || response.Impression.Summary != cached.Summary || response.Impression.TargetID != 9 || !response.Impression.GeneratedAt.Equal(generated) {
			t.Fatalf("cached response changed: %d %s (%v)", w.Code, w.Body, err)
		}
	}
	if reads != 2 {
		t.Fatalf("cache reads=%d", reads)
	}
}

func TestPartnerImpressionMissUsesDailyQueueWithoutReadingSources(t *testing.T) {
	for _, retry := range []bool{false, true} {
		store := impressionTestStore{binding: impressionBinding,
			daily: func(context.Context, string, int64, time.Time) (*dbop.Impression, error) { return nil, nil },
			ensure: func(_ context.Context, session string, target int64, gotRetry bool, now time.Time) (*dbop.Impression, error) {
				if session != "sess_shared" || target != 9 || gotRetry != retry || now.IsZero() {
					t.Fatal("daily queue received incorrect scope or retry flag")
				}
				return &dbop.Impression{TargetID: target, Status: "pending"}, nil
			},
		}
		query := ""
		if retry {
			query = "?retry=true"
		}
		w := httptest.NewRecorder()
		(&Server{}).servePartnerImpression(w, impressionRequest(http.MethodGet, query, "", 8), store, nil)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"status":"pending"`) {
			t.Fatalf("queue response: %d %s", w.Code, w.Body)
		}
	}
}

func TestPartnerImpressionSupplementPersistsFactButKeepsTodaysSummary(t *testing.T) {
	for _, syncFails := range []bool{false, true} {
		stored, synced, ensured := false, false, false
		store := impressionTestStore{binding: impressionBinding,
			apply: func(_ context.Context, request string, record dbop.MemoryRecord) (*dbop.MemoryRecord, error) {
				var fact map[string]any
				if err := json.Unmarshal([]byte(record.PendingContent), &fact); err != nil {
					t.Fatal(err)
				}
				if request != "observation/sess_shared/8/request_123" || record.SessionID != "sess_shared" || record.OwnerID != 9 || record.SourceUserID != 8 || fact["sourceType"] != "role_supplement" || fact["confirmation"] != "已确认" || fact["content"] != "TA喜欢骑车" {
					t.Fatalf("supplement lost its real ownership or confirmation: %#v", record)
				}
				stored = true
				return &record, nil
			},
			ensure: func(_ context.Context, _ string, _ int64, retry bool, _ time.Time) (*dbop.Impression, error) {
				if !stored || !synced || !retry {
					t.Fatal("daily queue must follow persistence and preserve retry policy")
				}
				ensured = true
				return &dbop.Impression{TargetID: 9, Status: "ready", Summary: "今天已保存的画像"}, nil
			},
		}
		syncMemory := func(_ context.Context, _ dbop.MemoryRecord) error {
			synced = true
			if syncFails {
				return errors.New("cloud sync unavailable")
			}
			return nil
		}
		w := httptest.NewRecorder()
		(&Server{}).servePartnerImpression(w, impressionRequest(http.MethodPost, "?retry=true", `{"text":" TA喜欢骑车 ","requestId":"request_123"}`, 8), store, syncMemory)
		status := "synced"
		if syncFails {
			status = "pending"
		}
		if w.Code != http.StatusOK || !ensured || !strings.Contains(w.Body.String(), `"summary":"今天已保存的画像"`) || !strings.Contains(w.Body.String(), `"memoryStatus":"`+status+`"`) {
			t.Fatalf("supplement should preserve cached summary: %d %s", w.Code, w.Body)
		}
	}
}

func TestPartnerImpressionRejectsUnauthorizedOrExitedSpaceBeforeCache(t *testing.T) {
	w := httptest.NewRecorder()
	(&Server{}).handlePartnerImpression(w, impressionRequest(http.MethodGet, "", "", 0))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated response=%d", w.Code)
	}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		for _, binding := range []*dbop.Binding{nil, {SessionID: "sess_shared", UserA: 10, UserB: 11}} {
			store := impressionTestStore{binding: func(context.Context, string) (*dbop.Binding, error) { return binding, nil }}
			w := httptest.NewRecorder()
			(&Server{}).servePartnerImpression(w, impressionRequest(method, "?retry=true", `{"text":"事实","requestId":"request_123"}`, 8), store, nil)
			if w.Code != http.StatusForbidden {
				t.Fatalf("inaccessible space response=%d", w.Code)
			}
		}
	}
}

func TestImpressionWorkerSettlesDailyCacheWithoutGenerationOrTimestampRefresh(t *testing.T) {
	generated := time.Now().UTC().Add(-time.Hour)
	job := dbop.Impression{SessionID: "sess_shared", TargetID: 9, SourceHash: "lease-token", Status: "generating"}
	cached := dbop.Impression{SessionID: job.SessionID, TargetID: job.TargetID, Summary: "今日画像", Status: "ready", GeneratedAt: &generated}
	finished := false
	store := impressionTestStore{binding: impressionBinding,
		daily: func(context.Context, string, int64, time.Time) (*dbop.Impression, error) { return &cached, nil },
		cached: func(_ context.Context, gotJob, gotCache dbop.Impression) error {
			finished = true
			if gotJob.SourceHash != job.SourceHash || gotCache.Summary != cached.Summary || !gotCache.GeneratedAt.Equal(generated) {
				t.Fatal("cache completion must retain its original analysis timestamp")
			}
			return nil
		},
	}
	if err := (&Server{}).runImpressionWithStore(context.Background(), job, store); err != nil || !finished {
		t.Fatalf("cached worker result: %v, finished=%v", err, finished)
	}
}

func TestImpressionWorkerWithoutEvidenceStillPersistsDailySuccess(t *testing.T) {
	finished := false
	store := impressionTestStore{binding: impressionBinding,
		daily: func(context.Context, string, int64, time.Time) (*dbop.Impression, error) { return nil, nil },
		sources: func(context.Context, string, int64) (dbop.ImpressionSources, error) {
			return dbop.ImpressionSources{}, nil
		},
		finish: func(_ context.Context, _ dbop.Impression, summary, problem string) error {
			finished = true
			if summary == "" || problem != "" {
				t.Fatal("honest insufficient-evidence result must be persisted as a successful daily result")
			}
			return nil
		},
	}
	if err := (&Server{}).runImpressionWithStore(context.Background(), dbop.Impression{SessionID: "sess_shared", TargetID: 9}, store); err != nil || !finished {
		t.Fatalf("empty evidence result: %v, finished=%v", err, finished)
	}
}

func TestImpressionWorkerGeneratesOneSnapshotAndRechecksBinding(t *testing.T) {
	for _, state := range []string{"active", "exited", "rebound", "invalid-output"} {
		t.Run(state, func(t *testing.T) {
			var requests []string
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests = append(requests, r.Method+" "+r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				switch r.Method + " " + r.URL.Path {
				case "POST /sessions":
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["resources"] != nil || body["title"] != "TieTie impression generation" {
						t.Error("generation must use an isolated session without mounted stores")
					}
					_, _ = w.Write([]byte(`{"id":"sess_generated","status":"idle"}`))
				case "POST /sessions/sess_generated/events":
					var body struct {
						Events []qoder.Event `json:"events"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Events) != 1 {
						t.Error("missing generation prompt")
					} else {
						prompt := eventText(body.Events[0])
						if !strings.Contains(prompt, impressionInstructions) || !strings.Contains(prompt, `"viewerUserId":8`) || !strings.Contains(prompt, `"targetUserId":9`) || !strings.Contains(prompt, "TA喜欢骑车") || !strings.Contains(prompt, `"confirmation":"已确认"`) {
							t.Error("generation lost fact-only instructions or authenticated target")
						}
					}
					_, _ = w.Write([]byte(`{"data":[]}`))
				case "GET /sessions/sess_generated/events":
					summary := `{"summary":" TA喜欢骑车🚴。 "}`
					if state == "invalid-output" {
						summary = "not json"
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"data": []qoder.Event{{ID: "evt_summary", Type: "agent.message", Content: []qoder.ContentBlock{{Type: "text", Text: summary}}}}})
				case "GET /sessions/sess_generated":
					_, _ = w.Write([]byte(`{"id":"sess_generated","status":"idle"}`))
				case "DELETE /sessions/sess_generated":
					w.WriteHeader(http.StatusNoContent)
				default:
					t.Errorf("unexpected remote request %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer upstream.Close()
			bindingReads, sourceReads, finishes := 0, 0, 0
			store := impressionTestStore{
				binding: func(ctx context.Context, session string) (*dbop.Binding, error) {
					bindingReads++
					binding, _ := impressionBinding(ctx, session)
					if bindingReads > 1 {
						if state == "exited" {
							return nil, nil
						}
						if state == "rebound" {
							binding.CreatedAt = binding.CreatedAt.Add(time.Hour)
						}
					}
					return binding, nil
				},
				daily: func(context.Context, string, int64, time.Time) (*dbop.Impression, error) { return nil, nil },
				sources: func(context.Context, string, int64) (dbop.ImpressionSources, error) {
					sourceReads++
					return dbop.ImpressionSources{Memories: []dbop.MemoryRecord{{ID: "fact-1"}}}, nil
				},
				record: func(context.Context, string, string) (*dbop.MemoryRecord, error) {
					return &dbop.MemoryRecord{ID: "fact-1", Path: "profile/observations/legacy.json", PendingContent: `{"content":"TA喜欢骑车","confirmation":"旧状态"}`}, nil
				},
				finish: func(_ context.Context, job dbop.Impression, summary, problem string) error {
					finishes++
					if job.SourceHash != "lease-token" {
						t.Error("finish must keep the claimed job CAS token")
					}
					if state == "active" {
						if summary != "TA喜欢骑车🚴。" || problem != "" {
							t.Error("fresh generated result did not reach the success persistence path")
						}
					} else if summary != "" || problem == "" {
						t.Error("invalid output or inactive binding must never publish a profile")
					}
					return nil
				},
			}
			s := &Server{Cfg: &config.Config{AgentID: "agent-test", EnvironmentID: "env-test"}, Qoder: &qoder.Client{BaseURL: upstream.URL, Token: "test-token", HC: upstream.Client()}}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			err := s.runImpressionWithStore(ctx, dbop.Impression{SessionID: "sess_shared", TargetID: 9, SourceHash: "lease-token"}, store)
			if (state == "invalid-output") != (err != nil) || sourceReads != 1 || finishes != 1 {
				t.Fatalf("worker err=%v, snapshot reads=%d, finishes=%d", err, sourceReads, finishes)
			}
			if len(requests) != 5 || requests[len(requests)-1] != "DELETE /sessions/sess_generated" {
				t.Fatalf("isolated generation was not cleaned up: %v", requests)
			}
		})
	}
}
