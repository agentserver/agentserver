package executorgateway

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/agentserver/agentserver/v2/internal/executionbackend"
	"github.com/agentserver/agentserver/v2/internal/sandboxcontract"
)

// managedAttemptLeases is the single-replica executor gateway's activity owner.
// Core keys an activity by sandbox + attempt, NOT by MCP transport session.
// Multiple MCP clients (including the repository bootstrap client) therefore
// need references to one keepalive, not independent renew/release loops.
// This is process-local just like MCP sessions; it is not a distributed lock.
type managedAttemptLeases struct {
	base    ManagedSandboxSessionAcquirer
	logger  *slog.Logger
	mu      sync.Mutex
	entries map[managedAttemptKey]*managedAttemptEntry
}

type managedAttemptKey struct {
	workspaceID, sessionID, runID, attemptID string
	generation                               int64
}

func managedLeaseKey(p ExecutorMCPPrincipal) managedAttemptKey {
	return managedAttemptKey{p.WorkspaceID, p.SessionID, p.Run.RunID, p.Run.RunAttemptID, p.Run.RunAttemptGeneration}
}

type managedAttemptEntry struct {
	principal ExecutorMCPPrincipal
	lease     ManagedSandboxSessionLease
	refs      int
	// A non-nil busy channel serializes acquire/release/fence without holding
	// the pool lock across I/O. A new acquisition cannot overtake an old release.
	busy        chan struct{}
	done        chan struct{}
	doneOnce    sync.Once
	err         error
	fenceFailed bool
}

func newManagedAttemptLeases(base ManagedSandboxSessionAcquirer) *managedAttemptLeases {
	return &managedAttemptLeases{base: base, entries: make(map[managedAttemptKey]*managedAttemptEntry)}
}

// An acquisition may have acquired activity before repository setup failed.
// A delayed, unconfirmed cleanup from that path must not revoke a retry.
type managedAcquireCleanupError struct{ cause error }

func (*managedAcquireCleanupError) Error() string {
	return "managed sandbox acquisition cleanup could not be confirmed"
}
func (err *managedAcquireCleanupError) Unwrap() error { return err.cause }

func (pool *managedAttemptLeases) Acquire(ctx context.Context, p ExecutorMCPPrincipal) (ManagedSandboxSessionLease, error) {
	key := managedLeaseKey(p)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		pool.mu.Lock()
		entry := pool.entries[key]
		if entry == nil {
			entry = &managedAttemptEntry{principal: p, busy: make(chan struct{}), done: make(chan struct{})}
			pool.entries[key] = entry
			pool.mu.Unlock()
			lease, err := pool.base.Acquire(ctx, p)
			pool.mu.Lock()
			close(entry.busy)
			entry.busy = nil
			if err != nil {
				var cleanupError *managedAcquireCleanupError
				if errors.As(err, &cleanupError) {
					pool.blockUntilExpiryLocked(key, entry)
				} else {
					delete(pool.entries, key)
				}
				pool.mu.Unlock()
				return nil, err
			}
			entry.lease, entry.refs = lease, 1
			pool.mu.Unlock()
			go func() {
				select {
				case <-lease.Done():
					entry.doneOnce.Do(func() { close(entry.done) })
				case <-entry.done:
				}
			}()
			return &managedAttemptReference{pool: pool, key: key, entry: entry}, nil
		}
		// Do not accidentally merge different capabilities, holders, repository
		// bindings or deadlines just because their attempt UUIDs match.
		if !equalExecutorMCPPrincipals(entry.principal, p) {
			pool.mu.Unlock()
			return nil, errors.New("managed sandbox attempt authority differs from its existing lease")
		}
		if entry.busy != nil {
			busy := entry.busy
			pool.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-busy:
				continue
			}
		}
		if entry.err != nil {
			err := entry.err
			pool.mu.Unlock()
			return nil, err
		}
		select {
		case <-entry.lease.Done():
			pool.beginRetireLocked(entry, errors.New("managed sandbox activity ended"))
			pool.mu.Unlock()
			go pool.finishRetire(key, entry, false)
			continue
		default:
			entry.refs++
			pool.mu.Unlock()
			return &managedAttemptReference{pool: pool, key: key, entry: entry}, nil
		}
	}
}

func (pool *managedAttemptLeases) beginRetireLocked(entry *managedAttemptEntry, cause error) {
	entry.busy = make(chan struct{})
	entry.err = cause
	entry.doneOnce.Do(func() { close(entry.done) })
}

func (pool *managedAttemptLeases) finishRetire(key managedAttemptKey, entry *managedAttemptEntry, fenced bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err := entry.lease.Release(ctx)
	pool.mu.Lock()
	defer pool.mu.Unlock()
	// A successful fence has already revoked the exact target generation;
	// its provider may no longer exist when ReleaseActivity checks it.
	if !entry.fenceFailed && (err == nil || fenced) {
		if pool.entries[key] == entry {
			delete(pool.entries, key)
		}
	} else {
		// Do not let a new lease race a possibly in-flight remote release. The
		// failed attempt remains closed until its signed authority expires.
		pool.blockUntilExpiryLocked(key, entry)
	}
	close(entry.busy)
	entry.busy = nil
	return err
}

func (pool *managedAttemptLeases) blockUntilExpiryLocked(key managedAttemptKey, entry *managedAttemptEntry) {
	entry.err = errors.New("managed sandbox activity cleanup could not be confirmed")
	time.AfterFunc(time.Until(entry.principal.CapabilityExpiresAt), func() {
		pool.mu.Lock()
		defer pool.mu.Unlock()
		if pool.entries[key] == entry {
			delete(pool.entries, key)
		}
	})
}

type managedAttemptReference struct {
	pool     *managedAttemptLeases
	key      managedAttemptKey
	entry    *managedAttemptEntry
	once     sync.Once
	err      error
	released chan struct{}
}

func (ref *managedAttemptReference) Done() <-chan struct{} { return ref.entry.done }
func (ref *managedAttemptReference) Err() error {
	ref.pool.mu.Lock()
	err := ref.entry.err
	ref.pool.mu.Unlock()
	if err != nil {
		return err
	}
	return ref.entry.lease.Err()
}
func (ref *managedAttemptReference) RepositoryContext() *sandboxcontract.RepositoryContext {
	if lease, ok := ref.entry.lease.(repositoryContextLease); ok {
		return lease.RepositoryContext()
	}
	return nil
}
func (ref *managedAttemptReference) Release(ctx context.Context) error {
	if ctx == nil {
		return errors.New("managed sandbox release context is required")
	}
	ref.once.Do(func() {
		ref.released = make(chan struct{})
		ref.pool.mu.Lock()
		entry := ref.entry
		if ref.pool.entries[ref.key] != entry || entry.busy != nil || entry.err != nil {
			ref.pool.mu.Unlock()
			close(ref.released)
			return // An old reference must never release a replacement lease.
		}
		entry.refs--
		if entry.refs != 0 {
			ref.pool.mu.Unlock()
			close(ref.released)
			return
		}
		ref.pool.beginRetireLocked(entry, errors.New("last MCP session released managed sandbox activity"))
		ref.pool.mu.Unlock()
		go func() {
			ref.err = ref.pool.finishRetire(ref.key, entry, false)
			close(ref.released)
		}()
	})
	select {
	case <-ref.released:
		return ref.err
	case <-ctx.Done():
		// The bounded cleanup continues and keeps the acquisition barrier in
		// place even if this MCP close or shutdown stops waiting for it.
		return ctx.Err()
	}
}

type attemptLeaseFencer struct {
	pool *managedAttemptLeases
	base ManagedTargetFencer
}

func (fencer *attemptLeaseFencer) FenceManagedTarget(ctx context.Context, p ExecutorMCPPrincipal, target executionbackend.Target, reason string) error {
	key := managedLeaseKey(p)
	fencer.pool.mu.Lock()
	entry := fencer.pool.entries[key]
	if entry != nil && entry.busy == nil && entry.err == nil {
		lease, ok := entry.lease.(interface {
			Target() executionbackend.Target
		})
		if !ok || lease.Target() != target {
			entry = nil
		} else {
			fencer.pool.beginRetireLocked(entry, errors.New("managed sandbox target was fenced"))
		}
	} else {
		entry = nil
	}
	fencer.pool.mu.Unlock()
	err := fencer.base.FenceManagedTarget(ctx, p, target, reason)
	if entry != nil {
		fencer.pool.mu.Lock()
		entry.fenceFailed = err != nil
		fencer.pool.mu.Unlock()
		_ = fencer.pool.finishRetire(key, entry, err == nil)
	}
	if fencer.pool.logger != nil {
		// Identity, fixed reason code and outcomes only; no command, output,
		// bearer or provider error text crosses into the diagnostic record.
		fencer.pool.logger.WarnContext(ctx, "managed sandbox target fenced",
			"workspace_id", p.WorkspaceID, "session_id", p.SessionID,
			"run_id", p.Run.RunID, "run_attempt_id", p.Run.RunAttemptID,
			"target_id", target.ID, "target_generation", target.Generation,
			"reason_code", reason, "succeeded", err == nil,
			"shared_lease_invalidated", entry != nil,
		)
	}
	return err
}
