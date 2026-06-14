package codexexecgateway

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/agentserver/agentserver/internal/codexexecgateway/audit"
	"github.com/agentserver/agentserver/internal/codexexecgateway/handlers"
	"github.com/agentserver/agentserver/internal/codexexecgateway/relay"
	sdkpkg "github.com/agentserver/agentserver/internal/codexexecgateway/sdk"
	"github.com/agentserver/agentserver/internal/envtools/bridge"
	"github.com/agentserver/agentserver/internal/envtools/processes"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// Server bundles the chi router with its dependencies.
// Server wires the routes for codex-exec-gateway. Production must
// always be constructed with a real *Store; tests that exercise only
// auth-rejection paths may use newServerNoStoreForTesting.
type Server struct {
	config        Config
	store         *Store
	registry      *ConnRegistry
	revoked       *RevokedSet
	relayRegistry *relay.Registry // nil if PublicHTTPSBaseURL unset (dev/disabled)
	sdkServer     *sdkpkg.Server  // nil if AgentserverInternalURL unset (dev/disabled)
	sdkSessions   *processes.Manager
	logger        *slog.Logger
	// recorder is the audit Recorder shared by the bridge pumps, the SDK
	// REST surface, and the relay endpoints. Always non-nil: NewServer
	// assigns audit.NewNoopRecorder() when Config.Audit.Enabled is false,
	// so callsites can call hook methods unconditionally.
	recorder audit.Recorder
}

// NewServer is the production constructor. Refuses a nil store so a
// misconfigured deploy can't silently bypass the /bridge ownership
// check (which falls back to "skip + warn" when store is nil for the
// sake of test wiring).
func NewServer(cfg Config, store *Store) (*Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	if store == nil {
		return nil, fmt.Errorf("codexexecgateway: store is required")
	}
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))
	var relayReg *relay.Registry
	if cfg.PublicHTTPSBaseURL != "" {
		relayReg = relay.NewRegistry(cfg.RelayMaxPerWorkspace, cfg.RelayDefaultTTL, logger)
	}

	registry := NewConnRegistry()

	// Build the SDK REST surface.  Enabled when AgentserverInternalURL is
	// set; disabled (nil sdkServer) in dev/test environments where the
	// agentserver validate-proxy-token endpoint is not available.
	var sdkSrv *sdkpkg.Server
	var sdkSessions *processes.Manager
	if cfg.AgentserverInternalURL != "" {
		sdkSessions = processes.NewManager(30 * time.Minute)
		sdkSessions.Run() // starts the idle-session GC goroutine

		sdkAuth := sdkpkg.NewProxyTokenAuth(
			cfg.AgentserverInternalURL,
			cfg.AgentserverInternalSecret,
			5*time.Minute,
			30*time.Second,
		)

		// Per-workspace bridge.Pool, name resolver, and tool registry
		// are built lazily inside sdkpkg.Server.wsCtxFor — we just hand
		// it the inputs it needs (ws base URL, cap-token secret, and a
		// RelayClient factory for copy_path).
		bridgeBaseURL := cfg.PublicWSBaseURL
		if bridgeBaseURL == "" && cfg.SelfHTTPBaseURL != "" {
			bridgeBaseURL = strings.Replace(cfg.SelfHTTPBaseURL, "https://", "wss://", 1)
			bridgeBaseURL = strings.Replace(bridgeBaseURL, "http://", "ws://", 1)
		}

		// RelayFactory: copy_path needs a workspace-scoped RelayClient,
		// authenticated with the same cap-token used by the bridge.
		// nil when PublicHTTPSBaseURL is unset (relay disabled in dev) —
		// wsCtxFor handles that by simply not registering copy_path.
		var relayFactory sdkpkg.RelayClientFactory
		if cfg.PublicHTTPSBaseURL != "" {
			relayFactory = func(workspaceID, capToken string) *bridge.RelayClient {
				return bridge.NewRelayClient(cfg.PublicHTTPSBaseURL, cfg.InternalSharedSecret, capToken, logger)
			}
		}

		sdkSrv = &sdkpkg.Server{
			Auth:             sdkAuth,
			Sessions:         sdkSessions,
			Registry:         sdkConnectedAdapter{store: store, registry: registry},
			ExecGatewayWSURL: bridgeBaseURL + "/bridge",
			CapTokenSecret:   cfg.CapTokenHMACSecret,
			RelayFactory:     relayFactory,
			Logger:           logger,
		}
		logger.Info("sdk REST surface enabled", "agentserver_url", cfg.AgentserverInternalURL)
	} else {
		logger.Warn("sdk REST surface disabled: CXG_AGENTSERVER_INTERNAL_URL not set")
	}

	// Audit recorder. NewRecorder returns a noop when cfg.Audit.Enabled
	// is false, so production behavior is unchanged until the env var is
	// flipped. Always non-nil so the hook callsites in bridge/inbound
	// don't need a nil check.
	rec, err := audit.NewRecorder(cfg.Audit)
	if err != nil {
		return nil, fmt.Errorf("audit recorder: %w", err)
	}
	// Share the same Recorder with the SDK REST surface so handler-level
	// CallStart/CallEnd records and envmcp bridge frame records land in
	// the same WAL. handleBridge in bridge.go recognises the sdk-pool
	// turn_id marker and suppresses the per-frame side to avoid double
	// recording (see captoken.go).
	if sdkSrv != nil {
		sdkSrv.Recorder = rec
	}

	return &Server{
		config:        cfg,
		store:         store,
		registry:      registry,
		revoked:       NewRevokedSet(10000),
		relayRegistry: relayReg,
		sdkServer:     sdkSrv,
		sdkSessions:   sdkSessions,
		logger:        logger,
		recorder:      rec,
	}, nil
}

// Stop releases background goroutines (SDK session GC, audit uploader).
// Call from main's signal handler after http.Server.Shutdown returns.
// Flushes the audit WAL with a 10s ceiling — uploader Run() exits when
// its context is cancelled, so the bound is mostly on Sync() of the
// open WAL segment.
func (s *Server) Stop() {
	if s.sdkSessions != nil {
		s.sdkSessions.Stop()
	}
	if s.recorder != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = s.recorder.Close(ctx)
		cancel()
	}
}

// newServerNoStoreForTesting constructs a Server with a nil store. ONLY
// for tests in this package that exercise routes which fail before
// reaching the store. The /bridge handler logs an explicit warning and
// skips the workspace-ownership check when store is nil.
func newServerNoStoreForTesting(cfg Config) (*Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))
	var relayReg *relay.Registry
	if cfg.PublicHTTPSBaseURL != "" {
		relayReg = relay.NewRegistry(cfg.RelayMaxPerWorkspace, cfg.RelayDefaultTTL, logger)
	}
	return &Server{
		config:        cfg,
		store:         nil,
		registry:      NewConnRegistry(),
		revoked:       NewRevokedSet(10000),
		relayRegistry: relayReg,
		logger:        logger,
		recorder:      audit.NewNoopRecorder(),
	}, nil
}

// verifyCapToken is the CapTokenVerifier closure handlers.RequireCapToken
// uses to authenticate /api/exec-gateway/connected (and any future
// per-workspace endpoint moved off the shared-secret model). Closes
// over the HMAC secret + the in-memory revoked set so the middleware
// stays in the handlers package without taking on a parent-package
// import.
//
// Revocation check matches /bridge's order (verify → check revoked) so
// a revoke-turn call kills both new bridge dials and new list refreshes
// from the same turn within the cap-token's TTL window.
func (s *Server) verifyCapToken(token string) (handlers.CapTokenClaims, error) {
	payload, err := VerifyCapabilityToken(token, s.config.CapTokenHMACSecret)
	if err != nil {
		return handlers.CapTokenClaims{}, err
	}
	if s.revoked.Contains(payload.TurnID) {
		return handlers.CapTokenClaims{}, fmt.Errorf("turn revoked: %s", payload.TurnID)
	}
	return handlers.CapTokenClaims{
		WorkspaceID: payload.WorkspaceID,
		UserID:      payload.UserID,
		TurnID:      payload.TurnID,
	}, nil
}

func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	r.Get("/codex-exec/{exe_id}", s.handleInbound)
	r.Get("/bridge/{exe_id}", s.handleBridge)

	// HTTP relay public endpoints — ticket Bearer is auth; no other
	// middleware. Registered even when relayRegistry is nil so the
	// handlers can return a clear 404 to misconfigured callers.
	r.Put("/relay/{ticket}", s.handleRelayPut)
	r.Get("/relay/{ticket}", s.handleRelayGet)

	// Upstream codex `exec-server --remote` compat. Two paths because
	// codex renamed the endpoint in 0.133 (executor → environment); the
	// handler treats them identically.
	cloudRegister := handlers.CloudRegister(s.store, s.config.PublicWSBaseURL,
		handlers.AgentserverValidator{
			BaseURL:        s.config.AgentserverInternalURL,
			InternalSecret: s.config.AgentserverInternalSecret,
		},
		s.config.AgentserverInternalSecret)
	r.Post("/cloud/executor/{exe_id}/register", cloudRegister)
	r.Post("/cloud/environment/{env_id}/register", cloudRegister)

	// *Store satisfies handlers.Store, handlers.BindingStore, and
	// handlers.InternalConnectedStore directly — no adapter needed because
	// all three interfaces now use execmodel types, which *Store also uses
	// (via the type aliases in models.go).
	r.Route("/api/codex-exec", func(r chi.Router) {
		r.Use(handlers.RequireAgentserverSecret(s.config.AgentserverInternalSecret))
		r.Post("/register", handlers.Register(s.store))
		// Used by agentserver to clean up an orphaned executor after a
		// register-then-bind failure (v0.54.2). CASCADE on
		// workspace_executors handles any leftover binding rows.
		r.Delete("/executors/{exe_id}", handlers.DeleteExecutor(s.store))
		r.Route("/workspaces/{wid}/executors", func(r chi.Router) {
			r.Post("/", handlers.PostBinding(s.store))
			r.Get("/", handlers.ListBinding(s.store, func() map[string]struct{} {
				ids := s.registry.ConnectedIDs()
				set := make(map[string]struct{}, len(ids))
				for _, id := range ids {
					set[id] = struct{}{}
				}
				return set
			}))
			r.Delete("/{exe_id}", handlers.DeleteBinding(s.store))
		})
	})

	r.Route("/api/exec-gateway", func(r chi.Router) {
		// /connected is called by env-mcp directly (post the
		// 2026-06-14 loopback removal); authenticates via the
		// workspace cap-token, which carries workspace_id in its
		// HMAC-signed payload — no query-string forgery surface.
		r.With(handlers.RequireCapToken(s.verifyCapToken, s.logger)).
			Get("/connected", handlers.Connected(s.store, s.registry))

		// Admin-style endpoints stay on the cluster-shared bearer:
		// revoke-turn is called by codex-app-gateway when a turn is
		// cancelled, relay/create is called by env-mcp's copy_path
		// HTTPS relay (both legitimately need cluster-level auth, not
		// a per-workspace token).
		r.Group(func(r chi.Router) {
			r.Use(handlers.RequireSharedSecret(s.config.InternalSharedSecret))
			r.Post("/revoke-turn", handlers.RevokeTurn(s.revoked))
			r.Post("/relay/create", s.handleRelayCreate)
		})
	})

	// SDK REST surface (/api/connectors/*). Mounted last so SDK routes don't
	// shadow any existing paths.
	if s.sdkServer != nil {
		s.sdkServer.Mount(r)
	}

	return r
}

// (real ConnRegistry lives in registry.go; real RevokedSet in revocation.go)
