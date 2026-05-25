package supervisor

import (
	"context"
	"log/slog"
	"time"
)

// IdleReaper periodically scans the Supervisor and shuts down entries
// idle for longer than idleAfter. "Idle" means snapshot()'s clock has
// not advanced for at least idleAfter — and that clock is shared with
// broker.Conn (via ChildHandle.LastActiveAt), so frame flow on either
// the TUI proxy path or the broker.Turn path keeps the subprocess
// alive without any per-path bookkeeping.
type IdleReaper struct {
	sup       *Supervisor
	interval  time.Duration
	idleAfter time.Duration
	logger    *slog.Logger
}

func NewIdleReaper(sup *Supervisor, interval, idleAfter time.Duration, logger *slog.Logger) *IdleReaper {
	if logger == nil {
		logger = slog.Default()
	}
	return &IdleReaper{sup: sup, interval: interval, idleAfter: idleAfter, logger: logger}
}

// Run blocks until ctx is done, ticking every interval and shutting
// down idle entries.
func (r *IdleReaper) Run(ctx context.Context) {
	t := time.NewTicker(r.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			for key, last := range r.sup.snapshot() {
				if now.Sub(last) >= r.idleAfter {
					if err := r.sup.Shutdown(ctx, key); err != nil {
						r.logger.Error("idle reap: subprocess shutdown failed (CODEX_HOME may not be saved to S3)", "key", key, "err", err)
					}
				}
			}
		}
	}
}
