package supervisor

import (
	"context"
	"log/slog"
	"time"
)

// ActivityProbe returns the most recent activity timestamp for the
// given subprocess key as observed by an external client (e.g. the
// broker connection pool's ws frame clock). Return the zero time when
// there is no signal; the reaper falls back to supervisor.lastActive.
type ActivityProbe func(Key) time.Time

// IdleReaper periodically scans the Supervisor and shuts down entries
// idle for longer than idleAfter. When a Probe is configured, the
// reaper uses max(supervisor.lastActive, probe(key)) so that activity
// only visible to external clients (e.g. broker.Pool's ws frame clock)
// keeps the subprocess alive even when nothing touches the supervisor.
//
// Background: broker.Turn caches its loopback ws in broker.Pool and
// never re-enters supervisor.EnsureSubprocess after the first dial,
// so supervisor.lastActive freezes at the first-dial timestamp. A
// 14-minute turn was reaped at the 30-minute mark because the
// supervisor saw it as silent. The probe lets the broker-side clock
// participate in the reap decision without coupling the two packages.
type IdleReaper struct {
	sup       *Supervisor
	interval  time.Duration
	idleAfter time.Duration
	probe     ActivityProbe
	logger    *slog.Logger
}

func NewIdleReaper(sup *Supervisor, interval, idleAfter time.Duration, logger *slog.Logger) *IdleReaper {
	if logger == nil {
		logger = slog.Default()
	}
	return &IdleReaper{sup: sup, interval: interval, idleAfter: idleAfter, logger: logger}
}

// SetProbe installs an ActivityProbe. Must be called before Run.
func (r *IdleReaper) SetProbe(probe ActivityProbe) { r.probe = probe }

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
				effective := last
				if r.probe != nil {
					if probed := r.probe(key); probed.After(effective) {
						effective = probed
					}
				}
				if now.Sub(effective) >= r.idleAfter {
					if err := r.sup.Shutdown(ctx, key); err != nil {
						r.logger.Error("idle reap: subprocess shutdown failed (CODEX_HOME may not be saved to S3)", "key", key, "err", err)
					}
				}
			}
		}
	}
}
