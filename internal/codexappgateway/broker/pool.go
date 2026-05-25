package broker

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// WSURLResolver is the per-workspace loopback ws URL provider. In
// production this calls into supervisor.EnsureSubprocess to spawn /
// reuse the codex subprocess and returns its handle.LastActiveAt() so
// frames flowing through the Conn feed the supervisor's idle clock.
// Tests may return a nil clock — the Conn will allocate a detached one.
type WSURLResolver func(ctx context.Context, workspaceID string) (wsURL string, clock *atomic.Int64, err error)

// Pool caches one *Conn per workspace id. Connections whose ws has been
// silent (no frames in either direction) for longer than idleTTL are
// reaped and closed. Safe for concurrent use.
//
// Idleness is measured from Conn.lastActiveAt, which the conn itself
// updates on every successful ws read/write. This means a long-running
// Turn that keeps streaming item/completed frames stays alive past
// idleTTL — the reaper only kills conns that are actually silent.
type Pool struct {
	resolver WSURLResolver
	idleTTL  time.Duration

	mu        sync.Mutex
	entries   map[string]*poolEntry
	stop      chan struct{}
	closeOnce sync.Once
}

type poolEntry struct {
	mu   sync.Mutex // single-flight Dial per workspace
	conn *Conn
}

// NewPool starts a background reaper goroutine. Caller must Close().
func NewPool(resolver WSURLResolver, idleTTL time.Duration) *Pool {
	p := &Pool{
		resolver: resolver,
		idleTTL:  idleTTL,
		entries:  make(map[string]*poolEntry),
		stop:     make(chan struct{}),
	}
	go p.reaper()
	return p
}

// Get returns a live *Conn for workspaceID, dialing if necessary.
// Concurrent Get calls for the same workspace share one Conn.
func (p *Pool) Get(ctx context.Context, workspaceID string) (*Conn, error) {
	if workspaceID == "" {
		return nil, errors.New("workspaceID required")
	}
	p.mu.Lock()
	e, ok := p.entries[workspaceID]
	if !ok {
		e = &poolEntry{}
		p.entries[workspaceID] = e
	}
	p.mu.Unlock()

	e.mu.Lock()
	defer e.mu.Unlock()

	if e.conn != nil && !e.connClosed() {
		// Bump activity on Get so a conn that's been idle ≥ idleTTL but not
		// yet swept by the reaper isn't reaped in the microsecond window
		// between Get returning and the caller's first frame. The first
		// frame would bump it anyway, but the window is enough for the
		// reaper tick to land in between and surface the exact bug this
		// file fixes.
		e.conn.lastActiveAt.Store(time.Now().UnixNano())
		return e.conn, nil
	}
	// (Re)dial.
	wsURL, clock, err := p.resolver(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("resolve loopback url: %w", err)
	}
	conn, err := DialWithClock(ctx, wsURL, clock)
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}
	e.conn = conn
	return conn, nil
}

func (e *poolEntry) connClosed() bool {
	if e.conn == nil {
		return true
	}
	// closeErr non-nil → reader has exited; the ws is dead.
	if v := e.conn.closeErr.Load(); v != nil {
		if h, _ := v.(*errHolder); h != nil && h.err != nil {
			return true
		}
	}
	return false
}

// Close stops the reaper and closes all live connections.
// It is safe to call Close more than once; subsequent calls are no-ops.
func (p *Pool) Close() {
	p.closeOnce.Do(func() {
		close(p.stop)
		p.mu.Lock()
		defer p.mu.Unlock()
		for _, e := range p.entries {
			e.mu.Lock()
			if e.conn != nil {
				e.conn.Close()
				e.conn = nil
			}
			e.mu.Unlock()
		}
		p.entries = map[string]*poolEntry{}
	})
}

func (p *Pool) reaper() {
	interval := p.idleTTL / 4
	if interval < 50*time.Millisecond {
		interval = 50 * time.Millisecond
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-p.stop:
			return
		case <-tick.C:
			p.reapOnce()
		}
	}
}

func (p *Pool) reapOnce() {
	cutoffNanos := time.Now().Add(-p.idleTTL).UnixNano()
	p.mu.Lock()
	keys := make([]string, 0, len(p.entries))
	for k := range p.entries {
		keys = append(keys, k)
	}
	p.mu.Unlock()
	for _, k := range keys {
		p.mu.Lock()
		e := p.entries[k]
		p.mu.Unlock()
		if e == nil {
			continue
		}
		e.mu.Lock()
		stale := e.conn != nil && e.conn.lastActiveAt.Load() < cutoffNanos
		if stale {
			e.conn.Close()
			e.conn = nil
		}
		e.mu.Unlock()
	}
}
