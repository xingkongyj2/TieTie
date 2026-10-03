// Package scheduler executes only due jobs returned by a persistent queue.
// It never scans users or allocates one timer/goroutine for every reminder.
package scheduler

import (
	"context"
	"sync"
	"time"
)

type Options struct {
	PollInterval time.Duration
	BatchSize    int
	Concurrency  int
	JobTimeout   time.Duration
}

func (options Options) Normalized() Options {
	if options.PollInterval <= 0 {
		options.PollInterval = time.Second
	}
	if options.BatchSize <= 0 || options.BatchSize > 512 {
		options.BatchSize = 64
	}
	if options.Concurrency <= 0 || options.Concurrency > 32 {
		options.Concurrency = 4
	}
	if options.JobTimeout <= 0 {
		options.JobTimeout = time.Minute
	}
	return options
}

// Claim must atomically reserve at most limit jobs with runAt <= now. Work must
// persist its outcome, including uncertain network results, before returning.
type Worker[T any] struct {
	Options Options
	Claim   func(context.Context, time.Time, int) ([]T, error)
	Work    func(context.Context, T) error
	OnError func(error)
}

// maxIdleBackoff 是空转时轮询间隔最多放宽到几倍。
const maxIdleBackoff = 2

func (worker Worker[T]) Run(ctx context.Context) {
	options := worker.Options.Normalized()
	idle := 0
	for ctx.Err() == nil {
		count := worker.RunOnce(ctx, time.Now())
		if count == options.BatchSize {
			continue
		} // Drain a due backlog promptly.
		if count > 0 {
			idle = 0
		} else if idle < maxIdleBackoff {
			idle++
		}
		// 连续空转就逐步放宽间隔：远端数据库一次往返要上百毫秒，多条队列按固定秒数
		// 轮询会把链路占满，真正到点的任务反而排在后面。最坏延迟 = 间隔 × 2^上限。
		timer := time.NewTimer(options.PollInterval << idle)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// RunOnce is also a deterministic entry point for queue and concurrency tests.
func (worker Worker[T]) RunOnce(ctx context.Context, now time.Time) int {
	options := worker.Options.Normalized()
	jobs, err := worker.Claim(ctx, now, options.BatchSize)
	if err != nil {
		worker.report(err)
		return 0
	}
	if len(jobs) == 0 {
		return 0
	}
	channel := make(chan T)
	var group sync.WaitGroup
	for index := 0; index < min(options.Concurrency, len(jobs)); index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for job := range channel {
				jobCtx, cancel := context.WithTimeout(ctx, options.JobTimeout)
				err := worker.Work(jobCtx, job)
				cancel()
				if err != nil {
					worker.report(err)
				}
			}
		}()
	}
	// Work receives cancelled jobs too so it can release unstarted reservations.
	for _, job := range jobs {
		channel <- job
	}
	close(channel)
	group.Wait()
	return len(jobs)
}

func (worker Worker[T]) report(err error) {
	if worker.OnError != nil {
		worker.OnError(err)
	}
}
