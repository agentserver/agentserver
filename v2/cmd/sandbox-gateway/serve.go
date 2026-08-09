package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/agentserver/agentserver/v2/internal/httperrorlog"
	"github.com/agentserver/agentserver/v2/internal/sandboxcapability"
	"github.com/agentserver/agentserver/v2/internal/sandboxcontract"
	"github.com/agentserver/agentserver/v2/internal/sandboxgateway"
	"github.com/agentserver/agentserver/v2/internal/sandboxgateway/fakeprovider"
)

const sandboxGatewayShutdownTimeout = 30 * time.Second

type sandboxGatewayReadiness struct {
	ready atomic.Bool
}

type sandboxProviderFactory func(sandboxGatewayConfig) (sandboxgateway.Provider, error)

func serveSandboxGateway(
	ctx context.Context,
	getenv func(string) string,
	stdout, stderr io.Writer,
	mode sandboxGatewayServeMode,
) error {
	return serveSandboxGatewayWithProvider(ctx, getenv, stdout, stderr, mode, defaultSandboxProvider)
}

func serveSandboxGatewayWithProvider(
	ctx context.Context,
	getenv func(string) string,
	stdout, stderr io.Writer,
	mode sandboxGatewayServeMode,
	providerFactory sandboxProviderFactory,
) error {
	if ctx == nil || stdout == nil || stderr == nil || providerFactory == nil {
		return errors.New("sandbox-gateway context, output, and provider factory are required")
	}
	config, err := loadSandboxGatewayConfig(getenv, mode)
	if err != nil {
		return err
	}
	verifier, err := sandboxcapability.LoadVerifier(config.capabilityKeyring)
	if err != nil {
		return fmt.Errorf("configure sandbox capability verifier: %w", err)
	}
	authorizer, err := sandboxgateway.NewCapabilityAuthorizer(verifier, time.Now)
	if err != nil {
		return err
	}
	coreHTTPClient, err := newSandboxGatewayCoreHTTPClient(config)
	if err != nil {
		return err
	}
	defer coreHTTPClient.CloseIdleConnections()
	coreClient, err := sandboxgateway.NewCoreClient(config.coreURL, coreHTTPClient)
	if err != nil {
		return err
	}
	provider, err := providerFactory(config)
	if err != nil {
		return fmt.Errorf("configure sandbox provider: %w", err)
	}
	service, err := sandboxgateway.NewService(sandboxgateway.Config{
		Core: coreClient, Provider: provider, Limits: sandboxcontract.DefaultLimits(),
		ProviderRegion: config.providerRegion, ProviderPSM: config.providerPSM,
		IdleTTL: config.idleTTL, EnsureTimeout: config.ensureTimeout,
		EnsurePollInterval: config.ensurePoll, Root: config.root, Platform: config.platform,
	})
	if err != nil {
		return err
	}
	handler, err := sandboxgateway.NewHandler(service, authorizer, 0)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", config.listenAddress)
	if err != nil {
		return fmt.Errorf("listen on sandbox-gateway address: %w", err)
	}
	defer listener.Close()
	if config.production {
		tlsConfig, err := newSandboxGatewayServerTLSConfig(config)
		if err != nil {
			return err
		}
		listener = tls.NewListener(listener, tlsConfig)
	}
	readiness := &sandboxGatewayReadiness{}
	server := &http.Server{
		Handler:           sandboxGatewayRoutes(handler, readiness),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    32 * 1024,
		ErrorLog:          httperrorlog.New(stderr),
	}
	logger := slog.New(slog.NewJSONHandler(stderr, nil))
	initialContext, cancelInitial := context.WithTimeout(ctx, min(config.ensureTimeout, 30*time.Second))
	_, err = service.ReconcileOnce(initialContext, config.reconcileLimit)
	cancelInitial()
	if err != nil {
		return fmt.Errorf("initial managed sandbox reconcile: %w", err)
	}
	readiness.ready.Store(true)
	scheme := "http"
	if config.production {
		scheme = "https"
	}
	fmt.Fprintf(stdout, "sandbox-gateway serve: provider %s; region %s; psm %s; endpoint %s://%s; reconcile %s\n",
		config.providerMode, config.providerRegion, config.providerPSM, scheme, listener.Addr(), config.reconcileInterval)
	return runSandboxGatewayServices(ctx, service, server, listener, readiness, logger, config.reconcileInterval, config.reconcileLimit)
}

func defaultSandboxProvider(config sandboxGatewayConfig) (sandboxgateway.Provider, error) {
	if config.providerMode == "fake" && !config.production {
		return fakeprovider.New(time.Now, nil), nil
	}
	return nil, errors.New("production TAE provider is not linked into the provider-neutral main module; build the providers/tae sandbox-gateway binary")
}

func sandboxGatewayRoutes(handler http.Handler, readiness *sandboxGatewayReadiness) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL != nil && request.URL.RawQuery == "" && !request.URL.ForceQuery && request.URL.Fragment == "" && request.URL.RawPath == "" && request.Method == http.MethodGet {
			switch request.URL.Path {
			case "/healthz":
				writeSandboxGatewayHealth(response, http.StatusOK, `{"status":"ok"}`)
				return
			case "/readyz":
				if readiness == nil || !readiness.ready.Load() {
					writeSandboxGatewayHealth(response, http.StatusServiceUnavailable, `{"status":"not_ready"}`)
					return
				}
				writeSandboxGatewayHealth(response, http.StatusOK, `{"status":"ready"}`)
				return
			}
		}
		handler.ServeHTTP(response, request)
	})
}

func writeSandboxGatewayHealth(response http.ResponseWriter, status int, body string) {
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.WriteHeader(status)
	_, _ = io.WriteString(response, body+"\n")
}

func runSandboxGatewayServices(
	ctx context.Context,
	service *sandboxgateway.Service,
	server *http.Server,
	listener net.Listener,
	readiness *sandboxGatewayReadiness,
	logger *slog.Logger,
	reconcileInterval time.Duration,
	reconcileLimit int,
) error {
	if ctx == nil || service == nil || server == nil || listener == nil || readiness == nil || logger == nil {
		return errors.New("complete sandbox-gateway service runtime is required")
	}
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
	readiness.ready.Store(false)
	cancelRun()
	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), sandboxGatewayShutdownTimeout)
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

func newSandboxGatewayCoreHTTPClient(config sandboxGatewayConfig) (*http.Client, error) {
	parsed, err := url.Parse(config.coreURL)
	if err != nil {
		return nil, err
	}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          16,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 35 * time.Second,
		ExpectContinueTimeout: time.Second,
		DisableCompression:    true,
	}
	if parsed.Scheme == "https" {
		certificate, err := loadSandboxGatewayCertificate(config.coreCertificate, config.coreKey, config.spiffeIdentity)
		if err != nil {
			return nil, fmt.Errorf("load sandbox-gateway Core client identity: %w", err)
		}
		rootCAs, err := loadSandboxGatewayCertPool("Core server CA", config.coreCA)
		if err != nil {
			return nil, err
		}
		transport.TLSClientConfig = &tls.Config{
			MinVersion: tls.VersionTLS13,
			RootCAs:    rootCAs, Certificates: []tls.Certificate{certificate}, ServerName: config.coreServerName,
		}
	}
	return &http.Client{Transport: transport}, nil
}
