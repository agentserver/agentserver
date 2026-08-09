package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/agentserver/agentserver/v2/internal/egresscapability"
	"github.com/agentserver/agentserver/v2/internal/egressgateway"
	"github.com/agentserver/agentserver/v2/internal/httperrorlog"
)

const (
	egressPolicyPath      = egressgateway.PolicyPath
	egressShutdownTimeout = 5 * time.Second
)

type egressDependencies struct {
	ZTI            egressgateway.ZTIVerifier
	Authority      egressgateway.LiveAuthority // legacy development/migration path only
	Resolver       egressgateway.CredentialInjectionResolver
	ProviderPolicy egressgateway.ProviderEgressPolicy
	Audit          egressgateway.AuditSink
	Close          func()
}

// policyBootstrapDenyService exists only to let the TAE control plane verify
// the production HTTPS URL and v1 response contract before a policy has been
// published and security-approved. It has no credential, Core, policy, or ZTI
// dependency and therefore cannot produce an allow decision.
type policyBootstrapDenyService struct{}

func (policyBootstrapDenyService) Authorize(context.Context, egressgateway.OriginalRequest, string) egressgateway.Decision {
	return egressgateway.Decision{ReasonCode: "policy_bootstrap_inactive"}
}

type egressDependencyFactory func(context.Context, egressAuthorizerConfig, io.Writer, time.Time) (egressDependencies, error)

type egressReadiness struct {
	ready atomic.Bool
}

func serveEgressAuthorizer(
	ctx context.Context,
	getenv func(string) string,
	stdout, stderr io.Writer,
	mode egressAuthorizerServeMode,
) error {
	return serveEgressAuthorizerWithDependencies(ctx, getenv, stdout, stderr, mode, defaultEgressDependencies)
}

func serveEgressAuthorizerWithDependencies(
	ctx context.Context,
	getenv func(string) string,
	stdout, stderr io.Writer,
	mode egressAuthorizerServeMode,
	factory egressDependencyFactory,
) error {
	if ctx == nil || stdout == nil || stderr == nil || factory == nil {
		return errors.New("egress-authorizer context, output, and dependency factory are required")
	}
	config, err := loadEgressAuthorizerConfig(getenv, mode)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	var service egressgateway.DecisionService
	if config.policyBootstrap {
		service = policyBootstrapDenyService{}
	} else {
		dependencies, dependencyError := factory(ctx, config, stderr, now)
		if dependencyError != nil {
			return fmt.Errorf("configure egress dependencies: %w", dependencyError)
		}
		if dependencies.Close != nil {
			defer dependencies.Close()
		}
		placeholders, loadError := egresscapability.LoadVerifier(config.placeholderKeyring)
		if loadError != nil {
			return fmt.Errorf("configure egress placeholder verifier: %w", loadError)
		}
		if config.production {
			if dependencies.Resolver == nil || dependencies.ProviderPolicy == nil {
				return errors.New("production egress-authorizer requires the v2 Core workspace credential service and provider policy")
			}
			service, err = egressgateway.NewProviderService(egressgateway.ProviderServiceConfig{
				Placeholders: placeholders, ZTI: dependencies.ZTI, Resolver: dependencies.Resolver,
				Policy: dependencies.ProviderPolicy, Audit: dependencies.Audit, AllowedPSM: config.allowedTAEPSM, Now: time.Now,
			})
		} else {
			service, err = egressgateway.NewService(egressgateway.Config{
				Placeholders: placeholders, ZTI: dependencies.ZTI, Authority: dependencies.Authority,
				Audit: dependencies.Audit, AllowedPSM: config.allowedTAEPSM, Now: time.Now,
			})
		}
	}
	if err != nil {
		return err
	}
	policyHandler, err := egressgateway.NewHandler(service, config.decisionTimeout)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", config.listenAddress)
	if err != nil {
		return fmt.Errorf("listen on egress-authorizer address: %w", err)
	}
	defer listener.Close()
	if config.production {
		certificate, err := loadEgressTLSIdentity(config.tlsCertificate, config.tlsKey, config.spiffeIdentity)
		if err != nil {
			return fmt.Errorf("load egress-authorizer server TLS identity: %w", err)
		}
		listener = tls.NewListener(listener, &tls.Config{
			MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate},
			NextProtos: []string{"http/1.1"},
		})
	}
	readiness := &egressReadiness{}
	server := &http.Server{
		Handler:           egressRoutes(policyHandler, readiness),
		ReadHeaderTimeout: 450 * time.Millisecond,
		ReadTimeout:       time.Second,
		WriteTimeout:      500 * time.Millisecond,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    64 * 1024,
		ErrorLog:          httperrorlog.New(stderr),
	}
	readiness.ready.Store(true)
	scheme := "http"
	if config.production {
		scheme = "https"
	}
	fmt.Fprintf(stdout, "egress-authorizer serve: endpoint %s://%s%s; decision timeout %s\n", scheme, listener.Addr(), egressPolicyPath, config.decisionTimeout)
	return runEgressServer(ctx, server, listener, readiness)
}

func egressRoutes(policy http.Handler, readiness *egressReadiness) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request != nil && request.URL != nil && request.URL.Path == egressPolicyPath {
			policy.ServeHTTP(response, request)
			return
		}
		if request != nil && request.URL != nil && request.Method == http.MethodGet &&
			request.URL.RawQuery == "" && !request.URL.ForceQuery && request.URL.Fragment == "" && request.URL.RawPath == "" {
			switch request.URL.Path {
			case "/healthz":
				writeEgressStatus(response, http.StatusOK, `{"status":"ok"}`)
				return
			case "/readyz":
				if readiness == nil || !readiness.ready.Load() {
					writeEgressStatus(response, http.StatusServiceUnavailable, `{"status":"not_ready"}`)
					return
				}
				writeEgressStatus(response, http.StatusOK, `{"status":"ready"}`)
				return
			}
		}
		writeEgressStatus(response, http.StatusNotFound, `{"error":"not_found"}`)
	})
}

func writeEgressStatus(response http.ResponseWriter, status int, payload string) {
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.WriteHeader(status)
	_, _ = io.WriteString(response, payload+"\n")
}

func runEgressServer(ctx context.Context, server *http.Server, listener net.Listener, readiness *egressReadiness) error {
	if ctx == nil || server == nil || listener == nil || readiness == nil {
		return errors.New("complete egress-authorizer server runtime is required")
	}
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.Serve(listener) }()
	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-serverDone:
		if serveErr == nil {
			serveErr = errors.New("egress-authorizer HTTP server stopped unexpectedly")
		}
	}
	readiness.ready.Store(false)
	shutdownContext, cancel := context.WithTimeout(context.Background(), egressShutdownTimeout)
	defer cancel()
	shutdownErr := server.Shutdown(shutdownContext)
	if shutdownErr != nil {
		shutdownErr = errors.Join(shutdownErr, server.Close())
	}
	if serveErr == nil {
		serveErr = <-serverDone
	}
	if errors.Is(serveErr, http.ErrServerClosed) {
		serveErr = nil
	}
	return errors.Join(serveErr, shutdownErr)
}
