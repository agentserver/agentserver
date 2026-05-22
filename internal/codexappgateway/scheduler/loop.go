// internal/codexappgateway/scheduler/loop.go
package scheduler

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"
)

// Config holds the runtime parameters for the scheduler Loop.
type Config struct {
	AgentserverBase string
	InternalSecret  string
	ImbridgeBase    string
	ImbridgeSecret  string
	CodexBin        string
	PodID           string
	PID             int
	TickInterval    time.Duration
	LeaseSeconds    int
	Concurrency     int
}

// Loop polls agentserver-main for due tasks and dispatches them concurrently.
// Multi-replica safe: the lease uses FOR UPDATE SKIP LOCKED.
type Loop struct {
	cfg        Config
	agent      *AgentserverClient
	dispatcher *Dispatcher
	logger     *slog.Logger
	inflight   atomic.Int32
}

// New constructs a Loop, wiring together AgentserverClient, Spawner, and
// Broadcaster from cfg. Defaults are applied for zero-valued numeric fields.
func New(cfg Config, logger *slog.Logger) *Loop {
	agent := NewAgentserverClient(cfg.AgentserverBase, cfg.InternalSecret, cfg.PodID, cfg.PID)
	disp := NewDispatcher(
		agent,
		NewSpawner(cfg.CodexBin, nil),
		NewBroadcaster(cfg.ImbridgeBase, cfg.ImbridgeSecret),
	)
	if cfg.TickInterval <= 0 {
		cfg.TickInterval = 15 * time.Second
	}
	if cfg.LeaseSeconds <= 0 {
		cfg.LeaseSeconds = 30 * 60
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 4
	}
	return &Loop{cfg: cfg, agent: agent, dispatcher: disp, logger: logger}
}

// Run blocks until ctx is cancelled, ticking the lease+dispatch loop each
// TickInterval. Each due task is dispatched in its own goroutine, bounded by
// Concurrency.
func (l *Loop) Run(ctx context.Context) {
	l.logger.Info("scheduler loop start",
		"tick", l.cfg.TickInterval,
		"lease_s", l.cfg.LeaseSeconds,
		"concurrency", l.cfg.Concurrency,
	)
	t := time.NewTicker(l.cfg.TickInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			l.tick(ctx)
		}
	}
}

// tick leases up to (Concurrency - inflight) tasks and dispatches each in a
// goroutine. The atomic inflight counter is decremented when each goroutine
// completes.
func (l *Loop) tick(ctx context.Context) {
	free := int(int32(l.cfg.Concurrency) - l.inflight.Load())
	if free <= 0 {
		return
	}
	batch, err := l.agent.LeaseDue(ctx, LeaseRequest{
		Limit:        free,
		LeaseSeconds: l.cfg.LeaseSeconds,
	})
	if err != nil {
		l.logger.Warn("lease failed", "err", err)
		return
	}
	for _, t := range batch {
		l.inflight.Add(1)
		go func(t Task) {
			defer l.inflight.Add(-1)
			if err := l.dispatcher.Fire(ctx, t); err != nil {
				l.logger.Warn("dispatcher.Fire failed", "task_id", t.ID, "err", err)
			}
		}(t)
	}
}
