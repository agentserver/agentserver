package sandboxgatewayapp

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/agentserver/agentserver/v2/internal/httperrorlog"
	"github.com/agentserver/agentserver/v2/internal/sandboxcapability"
	"github.com/agentserver/agentserver/v2/internal/sandboxcontract"
	"github.com/agentserver/agentserver/v2/internal/sandboxgateway"
)

const shutdownTimeout = 30 * time.Second

type readiness struct {
	ready atomic.Bool
}

func Serve(ctx context.Context, config Config, provider sandboxgateway.Provider, stdout, stderr io.Writer) error {
	if ctx == nil || provider == nil || stdout == nil || stderr == nil {
		return errors.New("sandbox-gateway context, provider, and output are required")
	}
	verifier, err := sandboxcapability.LoadVerifier(config.CapabilityKeyring)
	if err != nil {
		return fmt.Errorf("configure sandbox capability verifier: %w", err)
	}
	authorizer, err := sandboxgateway.NewCapabilityAuthorizer(verifier, time.Now)
	if err != nil {
		return err
	}
	coreHTTPClient, err := CoreHTTPClient(config)
	if err != nil {
		return err
	}
	defer coreHTTPClient.CloseIdleConnections()
	coreClient, err := sandboxgateway.NewCoreClient(config.CoreURL, coreHTTPClient)
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewJSONHandler(stderr, nil))
	service, err := sandboxgateway.NewService(sandboxgateway.Config{
		Core: coreClient, Provider: provider, Limits: sandboxcontract.DefaultLimits(),
		ProviderRegion: config.ProviderRegion, ProviderPSM: config.ProviderPSM,
		IdleTTL: config.IdleTTL, EnsureTimeout: config.EnsureTimeout, EnsurePollInterval: config.EnsurePoll,
		Root: config.Root, Platform: config.Platform,
		WorkspaceAllowlist: config.WorkspaceAllowlist,
		Logger:             logger,
	})
	if err != nil {
		return err
	}
	handler, err := sandboxgateway.NewHandlerWithLogger(service, authorizer, 0, logger)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", config.ListenAddress)
	if err != nil {
		return fmt.Errorf("listen on sandbox-gateway address: %w", err)
	}
	defer listener.Close()
	tlsConfig, err := ServerTLSConfig(config)
	if err != nil {
		return err
	}
	listener = tls.NewListener(listener, tlsConfig)
	ready := &readiness{}
	server := &http.Server{
		Handler: healthRoutes(handler, ready), ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 * 1024, ErrorLog: httperrorlog.New(stderr),
	}
	initialContext, cancelInitial := context.WithTimeout(ctx, min(config.EnsureTimeout, 30*time.Second))
	_, err = service.ReconcileOnce(initialContext, config.ReconcileLimit)
	cancelInitial()
	if err != nil {
		return fmt.Errorf("initial managed sandbox reconcile: %w", err)
	}
	ready.ready.Store(true)
	fmt.Fprintf(stdout, "sandbox-gateway serve: provider tae; region %s; psm %s; endpoint https://%s; reconcile %s\n",
		config.ProviderRegion, config.ProviderPSM, listener.Addr(), config.ReconcileInterval)
	return run(ctx, service, server, listener, ready, logger, config.ReconcileInterval, config.ReconcileLimit)
}

func healthRoutes(handler http.Handler, ready *readiness) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet && request.URL != nil && request.URL.RawQuery == "" && !request.URL.ForceQuery &&
			request.URL.Fragment == "" && request.URL.RawPath == "" {
			switch request.URL.Path {
			case "/healthz":
				writeHealth(response, http.StatusOK, `{"status":"ok"}`)
				return
			case "/readyz":
				if ready == nil || !ready.ready.Load() {
					writeHealth(response, http.StatusServiceUnavailable, `{"status":"not_ready"}`)
					return
				}
				writeHealth(response, http.StatusOK, `{"status":"ready"}`)
				return
			}
		}
		handler.ServeHTTP(response, request)
	})
}

func writeHealth(response http.ResponseWriter, status int, body string) {
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.WriteHeader(status)
	_, _ = io.WriteString(response, body+"\n")
}

func run(ctx context.Context, service *sandboxgateway.Service, server *http.Server, listener net.Listener,
	ready *readiness, logger *slog.Logger, reconcileInterval time.Duration, reconcileLimit int,
) error {
	runContext, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	serverDone := make(chan error, 1)
	reconcileDone := make(chan struct{})
	go func() { serverDone <- server.Serve(listener) }()
	go func() {
		defer close(reconcileDone)
		ticker := time.NewTicker(reconcileInterval)
		defer ticker.Stop()
		for {
			select {
			case <-runContext.Done():
				return
			case <-ticker.C:
				report, err := service.ReconcileOnce(runContext, reconcileLimit)
				if err != nil {
					logger.Error("managed sandbox reconcile failed", "examined", report.Examined, "failed", report.Failed, "error", err)
				}
			}
		}
	}()
	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-serverDone:
		if serveErr == nil {
			serveErr = errors.New("sandbox-gateway HTTP server stopped unexpectedly")
		}
	}
	ready.ready.Store(false)
	cancelRun()
	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancelShutdown()
	shutdownErr := server.Shutdown(shutdownContext)
	if shutdownErr != nil {
		shutdownErr = errors.Join(shutdownErr, server.Close())
	}
	if serveErr == nil {
		serveErr = <-serverDone
	}
	<-reconcileDone
	if errors.Is(serveErr, http.ErrServerClosed) {
		serveErr = nil
	}
	return errors.Join(serveErr, shutdownErr)
}
