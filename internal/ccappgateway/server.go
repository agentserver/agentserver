package ccappgateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/agentserver/agentserver/internal/ccappgateway/auth"
	"github.com/agentserver/agentserver/internal/ccappgateway/runner"
	"github.com/agentserver/agentserver/internal/ccappgateway/workspace"
)

// s3ProbeKey is the S3 key used by readyz to verify S3 reachability.
// A Get on this key is expected to return ErrObjectNotFound (proving auth
// works) or hit a non-NotFound error (indicating S3 is unreachable).
const s3ProbeKey = "cc-app-gateway/__readyz__/probe"

// Server is the HTTP server for cc-app-gateway.
type Server struct {
	cfg     ServeConfig
	handler *TurnHandler
	wstoken *WSTokenClient
	http    *http.Server
	router  chi.Router

	// Store is the S3-backed ObjectStore used for session persistence.
	// Exported for test inspection and injection via NewServerWithRunnerAndStore.
	Store workspace.ObjectStore

	// SessionLocks provides per-(workspaceID, sessionID) serialization.
	// Key = workspaceID + "/" + sessionID; value = *sync.Mutex.
	SessionLocks sync.Map

	// TeardownWG tracks in-flight backgrounded Teardown goroutines.
	// TurnHandler calls Add(1) before launching; goroutine calls Done().
	// Shutdown waits for this to drain after http.Shutdown returns.
	TeardownWG sync.WaitGroup
}

// AcquireSessionLock returns the per-(workspaceID, sessionID) mutex, already
// locked. Caller MUST Unlock it (typically at the end of the Teardown goroutine
// after the S3 Put completes).
func (s *Server) AcquireSessionLock(workspaceID, sessionID string) *sync.Mutex {
	key := workspaceID + "/" + sessionID
	actual, _ := s.SessionLocks.LoadOrStore(key, &sync.Mutex{})
	mu := actual.(*sync.Mutex)
	mu.Lock()
	return mu
}

// NewServer wires config, wstoken client, turn handler, chi router with the
// real runner and a real S3 client. Fails fast if S3 client init fails.
func NewServer(cfg ServeConfig) (*Server, error) {
	ctx := context.Background()
	store, err := NewS3Client(ctx, S3Config{
		Endpoint:  cfg.S3Endpoint,
		Region:    cfg.S3Region,
		Bucket:    cfg.S3Bucket,
		PathStyle: cfg.S3PathStyle,
	})
	if err != nil {
		return nil, fmt.Errorf("server: s3 client init: %w", err)
	}
	return newServerInternal(cfg, runner.Run, store), nil
}

// NewServerWithRunner is like NewServer but injects a custom RunnerFunc.
// Uses a noopStore (no real S3). Used by tests that don't exercise S3.
func NewServerWithRunner(cfg ServeConfig, runFn RunnerFunc) (*Server, error) {
	return NewServerWithRunnerAndStore(cfg, runFn, &noopStore{})
}

// NewServerWithRunnerAndStore is like NewServerWithRunner but also injects a
// custom ObjectStore. Used by tests that exercise S3-dependent logic (readyz,
// Setup/Teardown).
func NewServerWithRunnerAndStore(cfg ServeConfig, runFn RunnerFunc, store workspace.ObjectStore) (*Server, error) {
	return newServerInternal(cfg, runFn, store), nil
}

// newServerInternal is the single wiring point for all constructors.
func newServerInternal(cfg ServeConfig, runFn RunnerFunc, store workspace.ObjectStore) *Server {
	wstoken := NewWSTokenClient(cfg.AgentserverInternalURL, cfg.InternalSecret)

	s := &Server{
		cfg:     cfg,
		wstoken: wstoken,
		Store:   store,
	}

	s.handler = &TurnHandler{
		Cfg:     cfg,
		WSToken: wstoken,
		Runner:  runFn,
		TmpRoot: cfg.TmpRoot,
		Store:   store, // used by ServeHTTP for workspace.Setup/Teardown S3 round-trip
		Server:  s,     // used by ServeHTTP for AcquireSessionLock + TeardownWG drain
	}

	s.router = s.buildRoutes()
	s.http = &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: s.router,
	}
	return s
}

// noopStore is used by NewServerWithRunner — tests that don't exercise
// Setup/Teardown don't need a real S3 client. Get returns ErrObjectNotFound
// (so Setup always treats the session as fresh), Put/Delete are no-ops.
type noopStore struct{}

func (noopStore) Get(_ context.Context, _ string) ([]byte, error) {
	return nil, workspace.ErrObjectNotFound
}
func (noopStore) Put(_ context.Context, _ string, _ []byte) error { return nil }
func (noopStore) Delete(_ context.Context, _ string) error        { return nil }

// Routes returns the chi router (exposed for in-process tests without a listener).
func (s *Server) Routes() chi.Router {
	return s.router
}

// Start runs the HTTP server. Blocks until ctx is cancelled or the listener fails.
// Returns http.ErrServerClosed if the server was gracefully shut down.
func (s *Server) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", s.cfg.ListenAddr, err)
	}
	// Update Addr to the actual bound address (useful when ListenAddr was ":0").
	s.http.Addr = ln.Addr().String()

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.http.Serve(ln)
	}()

	select {
	case <-ctx.Done():
		// Caller drives shutdown with its own deadline context.
		// Return nil so the caller's shutdown logic can proceed.
		return nil
	case err := <-errCh:
		return err
	}
}

// Shutdown drains in-flight HTTP requests within the timeout context, then
// waits for backgrounded Teardown goroutines to complete (using the same
// deadline). Logs a warning if Teardowns outlive the context.
func (s *Server) Shutdown(ctx context.Context) error {
	httpErr := s.http.Shutdown(ctx)

	// Wait for in-flight Teardown goroutines with the same deadline.
	done := make(chan struct{})
	go func() {
		s.TeardownWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		log.Printf("[cc-app-gateway] shutdown deadline reached with pending teardowns")
	}
	return httpErr
}

// buildRoutes constructs the chi router with all routes.
func (s *Server) buildRoutes() chi.Router {
	r := chi.NewRouter()

	r.Get("/healthz", s.handleHealthz)
	r.Get("/readyz", s.handleReadyz)

	authMiddleware := auth.Either(
		auth.InternalSecretMiddleware(s.cfg.InternalSecret),
		auth.BearerMiddleware(),
	)
	r.With(authMiddleware).Post("/api/turns", s.handler.ServeHTTP)

	return r
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "ok")
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	var failures []string

	// Check 1: claude binary exists as a file.
	if info, err := os.Stat(s.cfg.ClaudeBin); err != nil || info.IsDir() {
		failures = append(failures, fmt.Sprintf("claude binary not found at %s", s.cfg.ClaudeBin))
	}

	// Check 2: agentserver /healthz is reachable.
	if err := pingAgentserver(s.cfg.AgentserverInternalURL); err != nil {
		failures = append(failures, fmt.Sprintf("agentserver healthz: %v", err))
	}

	// Check 3: S3 reachability via probe key.
	// ErrObjectNotFound is a SUCCESS (proves auth/network works).
	// Any other error means S3 is unreachable.
	probeCtx, probeCancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer probeCancel()
	_, err := s.Store.Get(probeCtx, s3ProbeKey)
	if err != nil && !errors.Is(err, workspace.ErrObjectNotFound) {
		failures = append(failures, "s3 unreachable: "+err.Error())
	}

	if len(failures) > 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck
			"status":   "not ready",
			"failures": failures,
		})
		return
	}

	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "ok")
}

// pingAgentserver does a GET /healthz on the agentserver URL with a 2s timeout.
// Returns nil if the server responds (any HTTP status), or an error if the
// connection fails or times out.
func pingAgentserver(baseURL string) error {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(baseURL + "/healthz")
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}
