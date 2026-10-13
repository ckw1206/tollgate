// Package worker runs periodic jobs in-process.
package worker

import (
	"context"
	"log/slog"
	"time"
)

// Every runs fn now and then every d until ctx is cancelled.
func Every(ctx context.Context, name string, d time.Duration, fn func(context.Context) error) {
	go func() {
		run := func() {
			if err := fn(ctx); err != nil && ctx.Err() == nil {
				slog.Error("job failed", "job", name, "err", err)
			}
		}
		run()
		t := time.NewTicker(d)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				run()
			}
		}
	}()
}
