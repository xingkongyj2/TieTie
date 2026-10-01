package scheduler

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestWorkerBoundsBatchAndParallelism(t *testing.T) {
	now := time.Now()
	started, release, done := make(chan struct{}, 8), make(chan struct{}), make(chan int)
	var active, peak, completed atomic.Int32
	worker := Worker[int]{Options: Options{BatchSize: 8, Concurrency: 3},
		Claim: func(_ context.Context, at time.Time, limit int) ([]int, error) {
			if limit != 8 || !at.Equal(now) {
				t.Errorf("claim must use configured batch and supplied clock")
			}
			return []int{1, 2, 3, 4, 5, 6, 7, 8}, nil
		},
		Work: func(ctx context.Context, job int) error {
			if _, hasDeadline := ctx.Deadline(); !hasDeadline {
				t.Error("job needs a deadline")
			}
			n := active.Add(1)
			for previous := peak.Load(); n > previous; previous = peak.Load() {
				if peak.CompareAndSwap(previous, n) {
					break
				}
			}
			started <- struct{}{}
			<-release
			active.Add(-1)
			completed.Add(1)
			return nil
		},
	}
	go func() { done <- worker.RunOnce(context.Background(), now) }()
	for i := 0; i < 3; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("independent jobs did not start concurrently")
		}
	}
	if peak.Load() != 3 {
		t.Fatalf("wanted three workers, got %d", peak.Load())
	}
	close(release)
	if count := <-done; count != 8 || completed.Load() != 8 || peak.Load() > 3 {
		t.Fatalf("batch=%d completed=%d peak=%d", count, completed.Load(), peak.Load())
	}
}

func TestWorkerStopsWithoutWaitingForPollingInterval(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	claimed, done := make(chan struct{}, 1), make(chan struct{})
	worker := Worker[int]{Options: Options{PollInterval: time.Hour}, Claim: func(context.Context, time.Time, int) ([]int, error) { claimed <- struct{}{}; return nil, nil }}
	go func() { worker.Run(ctx); close(done) }()
	<-claimed
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown blocked on poll timer")
	}
}
