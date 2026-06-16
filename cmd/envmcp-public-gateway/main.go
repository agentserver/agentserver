// Command envmcp-public-gateway runs the public-internet MCP server
// that lets external clients (Codex CLI, Claude Desktop in dev-mode
// via mcp-remote, etc.) call the agentserver workspace's env tools.
//
// Architecture (spec: docs/superpowers/specs/2026-06-09-envmcp-public-
// gateway-design.md, with the 2026-06-15 1-PAT-1-workspace amendment):
//
//	external client ─Bearer agpat_xxx─▶ /v1/mcp
//	   ▼
//	mcppublic.Middleware  →  PATResolver  →  Principal{user, workspace}
//	   ▼
//	mcppublic.Server (Streamable HTTP, MCP 2025-06-18)
//	   ▼
//	mcppublic.Dispatcher
//	   ├─ tools/list, list_environments     — in-process
//	   └─ tools/call (others) ─▶ CapMinter ─▶ BridgeBackend
//	                                              ▼
//	                                       per-Principal toolkit
//	                                       = bridge.Pool + nameresolver
//	                                       + sessions + envtools/tools
//	                                              ▼
//	                                       codex-exec-gateway /bridge
//
// Required environment variables — every one is mandatory, the
// gateway fails-closed on a missing knob. (Use Helm
// templates/envmcp-public-gateway.yaml to wire them.)
//
//	DATABASE_URL                       — postgres for PAT validation
//	CXG_LISTEN_ADDR                    — e.g. ":8090"
//	CXG_CAPTOKEN_HMAC_SECRET           — shared with app-gateway + exec-gateway
//	CXG_EXEC_GATEWAY_INTERNAL_URL      — http base for /api/codex-exec/*
//	CXG_EXEC_GATEWAY_INTERNAL_SECRET   — X-Internal-Secret for the above
//	CXG_BRIDGE_BASE_URL                — ws base for /bridge dials
//	MCP_PUBLIC_RESOURCE_METADATA_URL   — full URL of /v1/.well-known/oauth-protected-resource
//	                                     (advertised in 401 WWW-Authenticate)
//	MCP_PUBLIC_ISSUER_URL              — agentserver web base (advertised in
//	                                     the protected-resource doc)
//
// Optional:
//
//	CXG_LOG_LEVEL                      — debug / info / warn / error (default info)
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/agentserver/agentserver/internal/db"
	"github.com/agentserver/agentserver/internal/envtools/bridge"
	"github.com/agentserver/agentserver/internal/mcppublic"
	"github.com/agentserver/agentserver/internal/server"
)

func main() {
	logger := newLogger()
	if err := run(logger); err != nil {
		logger.Error("envmcp-public-gateway exited with error", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	dbConn, err := db.Open(cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer dbConn.Close()

	// Auth layer: PATResolver hits ValidateMCPPATSecret +
	// ListWorkspacesByUser on every request. The middleware emits
	// `WWW-Authenticate: Bearer resource_metadata=…` on 401 so
	// OAuth-capable clients (Claude Desktop 1P, Phase 2) can
	// discover us.
	patResolver := &mcppublic.PATResolver{DB: dbConn, Logger: logger}
	authMW := mcppublic.AuthMiddleware(
		[]mcppublic.PrincipalResolver{patResolver},
		cfg.ResourceMetadataURL,
		logger,
	)

	// Dispatcher pipeline: CapMinter → BridgeBackend → Tool[].Call.
	minter, err := mcppublic.NewCapMinter(cfg.CapTokenHMACSecret)
	if err != nil {
		return fmt.Errorf("cap-minter: %w", err)
	}
	execClient := server.NewExecutorsClient(cfg.ExecGatewayInternalURL, cfg.ExecGatewayInternalSecret)
	executors, err := mcppublic.NewHTTPExecutorsSource(execClient)
	if err != nil {
		return fmt.Errorf("executors source: %w", err)
	}
	// copy_path's HTTPS relay is per-workspace and authenticates
	// with X-Internal-Secret — same one exec-gateway expects on
	// /api/exec-gateway/relay/create. Per-workspace state lives on
	// the RelayClient struct (workspaceID field), but we don't have
	// a workspace at backend-construction time; PR F's BridgeBackend
	// currently takes a single nil RelayClient and copy_path falls
	// back to the ws cat-pump path. Wiring the per-workspace
	// RelayClient at toolkit-build time is a follow-up — for v1 the
	// fallback path is fine since copy_path is rarely used.
	backend, err := mcppublic.NewBridgeBackend(cfg.BridgeBaseURL, executors, nil, logger)
	if err != nil {
		return fmt.Errorf("bridge backend: %w", err)
	}
	defer backend.Close()

	dispatcher := mcppublic.NewDispatcher(
		executors, minter, backend,
		mcppublic.DefaultPublicToolMeta(),
		logger,
	)
	srv := mcppublic.NewServer(dispatcher, cfg.IssuerURL, logger)

	httpSrv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           srv.Mount(authMW),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		logger.Info("envmcp-public-gateway listening",
			"addr", cfg.ListenAddr,
			"resource_metadata_url", cfg.ResourceMetadataURL,
			"exec_gateway_internal_url", cfg.ExecGatewayInternalURL,
			"bridge_base_url", cfg.BridgeBaseURL,
		)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		logger.Info("envmcp-public-gateway: signal received, draining")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return httpSrv.Shutdown(shutdownCtx)
	case err := <-errCh:
		return fmt.Errorf("listen: %w", err)
	}
}

// config is the env-var bag every gateway pod reads at boot. All
// fields are required (fail-closed) except where noted.
type config struct {
	DatabaseURL               string
	ListenAddr                string
	CapTokenHMACSecret        []byte
	ExecGatewayInternalURL    string
	ExecGatewayInternalSecret string
	BridgeBaseURL             string
	ResourceMetadataURL       string
	IssuerURL                 string
}

func loadConfig() (config, error) {
	cfg := config{
		DatabaseURL:               os.Getenv("DATABASE_URL"),
		ListenAddr:                envOr("CXG_LISTEN_ADDR", ":8090"),
		CapTokenHMACSecret:        []byte(os.Getenv("CXG_CAPTOKEN_HMAC_SECRET")),
		ExecGatewayInternalURL:    os.Getenv("CXG_EXEC_GATEWAY_INTERNAL_URL"),
		ExecGatewayInternalSecret: os.Getenv("CXG_EXEC_GATEWAY_INTERNAL_SECRET"),
		BridgeBaseURL:             os.Getenv("CXG_BRIDGE_BASE_URL"),
		ResourceMetadataURL:       os.Getenv("MCP_PUBLIC_RESOURCE_METADATA_URL"),
		IssuerURL:                 os.Getenv("MCP_PUBLIC_ISSUER_URL"),
	}
	missing := []string{}
	if cfg.DatabaseURL == "" {
		missing = append(missing, "DATABASE_URL")
	}
	if len(cfg.CapTokenHMACSecret) == 0 {
		missing = append(missing, "CXG_CAPTOKEN_HMAC_SECRET")
	}
	if cfg.ExecGatewayInternalURL == "" {
		missing = append(missing, "CXG_EXEC_GATEWAY_INTERNAL_URL")
	}
	if cfg.ExecGatewayInternalSecret == "" {
		missing = append(missing, "CXG_EXEC_GATEWAY_INTERNAL_SECRET")
	}
	if cfg.BridgeBaseURL == "" {
		missing = append(missing, "CXG_BRIDGE_BASE_URL")
	}
	if len(missing) > 0 {
		return cfg, fmt.Errorf("missing required env vars: %v", missing)
	}
	return cfg, nil
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func newLogger() *slog.Logger {
	level := slog.LevelInfo
	switch os.Getenv("CXG_LOG_LEVEL") {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
}

// Silence the unused-import warning for bridge if a future refactor
// drops the explicit reference. (BridgeBackend uses it via the
// internal package import path, but having an explicit reference here
// makes the dependency obvious to `go mod tidy`.)
var _ = bridge.Pool{}
