package supervisor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/agentserver/agentserver/internal/codexappgateway/codexhome"
)

// Key identifies one workspace's codex app-server subprocess. The
// codex-app-server process internally manages multiple threads via its
// own JSON-RPC protocol; the gateway does not see thread IDs.
type Key struct {
	WorkspaceID string
}

// SupervisorConfig holds the static dependencies.
type SupervisorConfig struct {
	CodexBin string
	HomeMgr  *codexhome.Manager
	Store    codexhome.ObjectStore
	ExtraEnv []string     // forwarded to every spawned subprocess
	Logger   *slog.Logger // defaults to slog.Default() if nil
}

// Supervisor owns the in-memory (workspace, thread) → subprocess map.
type Supervisor struct {
	cfg    SupervisorConfig
	logger *slog.Logger

	mu       sync.Mutex
	children map[Key]*entry
}

type entry struct {
	handle    *ChildHandle
	codexHome string
	// lastActiveAt is the single source of truth for "is this subprocess
	// idle". Stored as a pointer so external clients (broker.Conn via
	// ChildHandle.LastActiveAt) can bump it on every ws frame without
	// going through Touch — making frame flow on either the TUI proxy
	// path (which calls Touch) or the broker.Turn path (which writes
	// directly through the pointer) feed into the same clock the
	// IdleReaper reads.
	lastActiveAt *atomic.Int64
}

// SpawnConfig is what a ConfigBuilder returns: the per-thread CODEX_HOME
// config.toml input plus per-spawn process env vars (e.g. a workspace-
// scoped LLM API key fetched from agentserver at spawn time). The env
// list is concatenated with SupervisorConfig.ExtraEnv when launching.
type SpawnConfig struct {
	Config codexhome.ConfigInput
	Env    []string
}

// ConfigBuilder produces a fresh SpawnConfig at spawn time. Allowed
// to hit the network; errors propagate.
//
// Pre the 2026-06-14 loopback removal this took a per-spawn loopback
// token argument the builder embedded in config.toml's env block.
// That token authorised env-mcp against the app-gateway's loopback
// /internal/{connected,scheduled-tasks/*} handlers. With those
// handlers gone, env-mcp authenticates downstream calls with its
// workspace cap-token (set by the builder via CXG_WORKSPACE_TOKEN)
// and the supervisor no longer needs to mint or track anything.
type ConfigBuilder func() (SpawnConfig, error)

func NewSupervisor(cfg SupervisorConfig) *Supervisor {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Supervisor{cfg: cfg, logger: logger, children: map[Key]*entry{}}
}

// EnsureSubprocess returns a live ChildHandle for key, spawning one if
// necessary. Concurrent EnsureSubprocess calls for the same key see
// the same handle (one-spawn-per-key invariant; loser of the race
// discards their spawn). If a cached entry's subprocess has crashed,
// it is evicted and a fresh subprocess is spawned.
func (s *Supervisor) EnsureSubprocess(ctx context.Context, key Key, build ConfigBuilder) (*ChildHandle, error) {
	s.mu.Lock()
	if e, ok := s.children[key]; ok {
		if e.handle.IsAlive() {
			e.lastActiveAt.Store(time.Now().UnixNano())
			s.mu.Unlock()
			return e.handle, nil
		}
		// Subprocess crashed since the entry was last seen. Drop it; we'll
		// respawn below. Try to upload its CODEX_HOME state first so the
		// freshly-spawned successor can resume from where the dead one
		// left off (sqlite WAL may still be flushable).
		deadHome := e.codexHome
		delete(s.children, key)
		s.mu.Unlock()
		backend := codexhome.NewS3Backend(s.cfg.Store, key.WorkspaceID)
		// Best-effort: ignore upload error here — the dead-process cleanup
		// path can't usefully retry, and we'd rather respawn than block.
		if err := backend.Upload(ctx, deadHome); err != nil {
			s.logger.Warn("dead-subprocess CODEX_HOME upload failed", "key", key, "err", err)
		}
		if err := s.cfg.HomeMgr.RemoveTmpDir(deadHome); err != nil {
			s.logger.Warn("dead-subprocess tmpdir cleanup failed", "key", key, "err", err)
		}
		// Fall through to the spawn path below.
	} else {
		s.mu.Unlock()
	}

	spawnCfg, err := build()
	if err != nil {
		return nil, fmt.Errorf("config builder: %w", err)
	}
	codexHome, err := s.cfg.HomeMgr.NewTmpDir(key.WorkspaceID)
	if err != nil {
		return nil, fmt.Errorf("new tmpdir: %w", err)
	}
	backend := codexhome.NewS3Backend(s.cfg.Store, key.WorkspaceID)
	if err := backend.Download(ctx, codexHome); err != nil && !errors.Is(err, codexhome.ErrObjectNotFound) {
		_ = s.cfg.HomeMgr.RemoveTmpDir(codexHome)
		return nil, fmt.Errorf("S3 download: %w", err)
	}
	if err := s.cfg.HomeMgr.WriteConfig(codexHome, spawnCfg.Config); err != nil {
		_ = s.cfg.HomeMgr.RemoveTmpDir(codexHome)
		return nil, fmt.Errorf("write config: %w", err)
	}

	// Static SupervisorConfig.ExtraEnv first, per-spawn env last so the
	// per-spawn values win on duplicate keys (e.g. a workspace-scoped
	// CODEX_API_KEY overriding a static fallback).
	extraEnv := append(append([]string{}, s.cfg.ExtraEnv...), spawnCfg.Env...)
	handle, err := spawnCodexAppServer(ctx, s.cfg.CodexBin, codexHome, extraEnv)
	if err != nil {
		_ = s.cfg.HomeMgr.RemoveTmpDir(codexHome)
		return nil, fmt.Errorf("spawn: %w", err)
	}

	s.mu.Lock()
	if e, ok := s.children[key]; ok {
		// Lost the race; discard our spawn and return theirs.
		e.lastActiveAt.Store(time.Now().UnixNano())
		winner := e.handle
		s.mu.Unlock()
		// Clean up our discarded spawn out-of-band so the lock isn't held
		// during the SIGTERM→SIGKILL window.
		go func() {
			if err := handle.Stop(context.Background()); err != nil {
				s.logger.Warn("race-loser subprocess stop failed", "key", key, "err", err)
			}
			if err := s.cfg.HomeMgr.RemoveTmpDir(codexHome); err != nil {
				s.logger.Warn("race-loser tmpdir cleanup failed", "key", key, "err", err)
			}
		}()
		return winner, nil
	}
	clock := &atomic.Int64{}
	clock.Store(time.Now().UnixNano())
	handle.lastActiveAt = clock
	s.children[key] = &entry{
		handle:       handle,
		codexHome:    codexHome,
		lastActiveAt: clock,
	}
	s.mu.Unlock()
	return handle, nil
}

// Touch bumps the last-active timestamp for a key. Callers must invoke
// it on every proxied frame (see proxy.RunProxy's onFrame callback) so
// the IdleReaper sees fresh activity for the duration of an active
// session, not just at connect/disconnect. The broker.Conn path bumps
// the same atomic directly via ChildHandle.LastActiveAt instead of
// calling Touch, so frame flow on either path feeds one clock.
func (s *Supervisor) Touch(key Key) {
	s.mu.Lock()
	clock := func() *atomic.Int64 {
		if e, ok := s.children[key]; ok {
			return e.lastActiveAt
		}
		return nil
	}()
	s.mu.Unlock()
	if clock != nil {
		clock.Store(time.Now().UnixNano())
	}
}

// Shutdown terminates the subprocess for key, uploads its CODEX_HOME to
// S3, and drops the in-memory entry. Safe on missing keys.
func (s *Supervisor) Shutdown(ctx context.Context, key Key) error {
	s.mu.Lock()
	e, ok := s.children[key]
	if !ok {
		s.mu.Unlock()
		return nil
	}
	delete(s.children, key)
	s.mu.Unlock()

	// Continue uploading even if Stop errors — flushed sqlite is still useful.
	if err := e.handle.Stop(ctx); err != nil {
		s.logger.Warn("subprocess stop failed", "key", key, "err", err)
	}
	// Always reclaim disk before returning, even if S3 upload fails.
	// (S3 upload failure is transient; leaking the tmpdir would compound on
	// long-running pods with intermittent S3 connectivity.)
	defer func() {
		if err := s.cfg.HomeMgr.RemoveTmpDir(e.codexHome); err != nil {
			s.logger.Warn("tmpdir cleanup failed", "key", key, "err", err)
		}
	}()
	backend := codexhome.NewS3Backend(s.cfg.Store, key.WorkspaceID)
	if err := backend.Upload(ctx, e.codexHome); err != nil {
		return fmt.Errorf("S3 upload: %w", err)
	}
	return nil
}

// ShutdownAll shuts down every active subprocess.
func (s *Supervisor) ShutdownAll(ctx context.Context) {
	s.mu.Lock()
	keys := make([]Key, 0, len(s.children))
	for k := range s.children {
		keys = append(keys, k)
	}
	s.mu.Unlock()
	for _, k := range keys {
		if err := s.Shutdown(ctx, k); err != nil {
			s.logger.Error("ShutdownAll: subprocess shutdown failed (CODEX_HOME may not be saved to S3)", "key", k, "err", err)
		}
	}
}

// snapshot returns the keys + last-active times. Used by the reaper.
func (s *Supervisor) snapshot() map[Key]time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[Key]time.Time, len(s.children))
	for k, e := range s.children {
		out[k] = time.Unix(0, e.lastActiveAt.Load())
	}
	return out
}
