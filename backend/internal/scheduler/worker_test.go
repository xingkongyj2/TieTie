package scheduler

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestWakeClaimsNewJobBeforeIdlePoll(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	wake := make(chan struct{}, 1)
	firstClaim := make(chan struct{})
	worked := make(chan struct{})
	var claims atomic.Int32
	worker := Worker[int]{
		Options: Options{PollInterval: 2 * time.Second, BatchSize: 1},
		Wake:    wake,
		Claim: func(context.Context, time.Time, int) ([]int, error) {
			if claims.Add(1) == 1 {
				close(firstClaim)
				return nil, nil
			}
			return []int{1}, nil
		},
		Work: func(context.Context, int) error {
			close(worked)
			cancel()
			return nil
		},
	}
	stopped := make(chan struct{})
	go func() { worker.Run(ctx); close(stopped) }()
	select {
	case <-firstClaim:
	case <-ctx.Done():
		t.Fatal("worker did not make its initial claim")
	}
	wake <- struct{}{}
	select {
	case <-worked:
	case <-ctx.Done():
		t.Fatal("wake did not claim the new job before the idle poll")
	}
	<-stopped
}
