package ccappgateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/agentserver/agentserver/internal/ccappgateway/auth"
	"github.com/agentserver/agentserver/internal/ccappgateway/runner"
)

// Server is the HTTP server for cc-app-gateway.
type Server struct {
	cfg     ServeConfig
	handler *TurnHandler
	wstoken *WSTokenClient
	http    *http.Server
	router  chi.Router
}

// NewServer wires config, wstoken client, turn handler, chi router with the real runner.
func NewServer(cfg ServeConfig) (*Server, error) {
	return NewServerWithRunner(cfg, runner.Run)
}

// NewServerWithRunner is like NewServer but injects a custom RunnerFunc. Used by tests.
func NewServerWithRunner(cfg ServeConfig, runFn RunnerFunc) (*Server, error) {
	wstoken := NewWSTokenClient(cfg.AgentserverInternalURL, cfg.InternalSecret)

	handler := &TurnHandler{
		Cfg:     cfg,
		WSToken: wstoken,
		Runner:  runFn,
		TmpRoot: cfg.TmpRoot,
	}

	s := &Server{
		cfg:     cfg,
		handler: handler,
		wstoken: wstoken,
	}
	s.router = s.buildRoutes()
	s.http = &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: s.router,
	}
	return s, nil
}

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
		return s.http.Shutdown(context.Background())
	case err := <-errCh:
		return err
	}
}

// Shutdown drains in-flight requests within the timeout context.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.http.Shutdown(ctx)
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
