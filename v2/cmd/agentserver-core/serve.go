package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/coredb"
	"github.com/agentserver/agentserver/v2/internal/coreserver"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	coreListenAddressEnvironment       = "AGENTSERVER_V2_CORE_LISTEN_ADDR"
	coreTLSCertificateEnvironment      = "AGENTSERVER_V2_CORE_TLS_CERT_FILE"
	coreTLSKeyEnvironment              = "AGENTSERVER_V2_CORE_TLS_KEY_FILE"
	coreClientCAEnvironment            = "AGENTSERVER_V2_CORE_CLIENT_CA_FILE"
	coreGatewayIdentityEnvironment     = "AGENTSERVER_V2_EXECUTOR_GATEWAY_SPIFFE_ID"
	coreHarnessPoolIdentityEnvironment = "AGENTSERVER_V2_HARNESS_POOL_SPIFFE_ID"
)

func serveCore(ctx context.Context, getenv func(string) string, stdout io.Writer) error {
	databaseURL, err := requiredConfiguration(getenv, databaseURLEnvironment)
	if err != nil {
		return err
	}
	listenAddress, err := requiredConfiguration(getenv, coreListenAddressEnvironment)
	if err != nil {
		return err
	}
	certificateFile, err := requiredConfiguration(getenv, coreTLSCertificateEnvironment)
	if err != nil {
		return err
	}
	keyFile, err := requiredConfiguration(getenv, coreTLSKeyEnvironment)
	if err != nil {
		return err
	}
	clientCAFile, err := requiredConfiguration(getenv, coreClientCAEnvironment)
	if err != nil {
		return err
	}
	gatewayIdentity, err := requiredConfiguration(getenv, coreGatewayIdentityEnvironment)
	if err != nil {
		return err
	}
	harnessPoolIdentity, err := requiredConfiguration(getenv, coreHarnessPoolIdentityEnvironment)
	if err != nil {
		return err
	}
	if harnessPoolIdentity == gatewayIdentity {
		return errors.New("executor-gateway and harness-pool SPIFFE identities must be distinct")
	}

	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return errors.New("database URL is invalid")
	}
	poolConfig.MaxConns = 20
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return fmt.Errorf("open core database pool: %w", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping core database: %w", err)
	}

	authorizer, err := coreserver.NewSPIFFEWorkloadAuthorizer(gatewayIdentity)
	if err != nil {
		return err
	}
	harnessPoolAuthorizer, err := coreserver.NewSPIFFEWorkloadAuthorizer(harnessPoolIdentity)
	if err != nil {
		return err
	}
	store := coredb.NewStateStore(pool)
	connectionHandler, err := coreserver.NewExecutorConnectionHandler(authorizer, coreserver.StateStoreExecutorConnectionCommands{
		Store: store,
	})
	if err != nil {
		return err
	}
	environmentHandler, err := coreserver.NewExecutorEnvironmentHandler(authorizer, coreserver.StateStoreExecutorEnvironmentQueries{Store: store})
	if err != nil {
		return err
	}
	executionHandler, err := coreserver.NewExecutionHandler(authorizer, coreserver.StateStoreExecutionCommands{Store: store})
	if err != nil {
		return err
	}
	runAttemptHandler, err := coreserver.NewRunAttemptHandler(harnessPoolAuthorizer, coreserver.StateStoreRunAttemptCommands{Store: store})
	if err != nil {
		return err
	}
	runDispatchHandler, err := coreserver.NewRunDispatchHandler(harnessPoolAuthorizer, coreserver.StateStoreRunDispatchCommands{Store: store})
	if err != nil {
		return err
	}
	brainToolCatalogHandler, err := coreserver.NewBrainToolCatalogHandler(harnessPoolAuthorizer, coreserver.StateStoreBrainToolCatalogCommands{Store: store})
	if err != nil {
		return err
	}
	handler := http.NewServeMux()
	handler.Handle(corecontract.FreezeBrainToolCatalogPath, brainToolCatalogHandler)
	handler.Handle(corecontract.BrainToolCatalogPathPrefix, brainToolCatalogHandler)
	handler.Handle(corecontract.ClaimRunDispatchesPath, runDispatchHandler)
	handler.Handle(corecontract.RunDispatchPathPrefix, runDispatchHandler)
	handler.Handle(corecontract.ClaimRunAttemptPath, runAttemptHandler)
	handler.Handle(corecontract.RunAttemptPathPrefix, runAttemptHandler)
	handler.Handle(corecontract.ListExecutorEnvironmentsPath, environmentHandler)
	handler.Handle(corecontract.PrepareExecutionPath, executionHandler)
	handler.Handle(corecontract.ExecutionPathPrefix, executionHandler)
	handler.Handle("/", connectionHandler)
	tlsConfig, err := coreTLSConfig(certificateFile, keyFile, clientCAFile)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", listenAddress)
	if err != nil {
		return fmt.Errorf("listen on core address: %w", err)
	}
	defer listener.Close()

	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    32 * 1024,
		TLSConfig:         tlsConfig,
	}
	serveContext, cancelServe := context.WithCancel(context.Background())
	defer cancelServe()
	go func() {
		select {
		case <-ctx.Done():
			shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = server.Shutdown(shutdownContext)
		case <-serveContext.Done():
		}
	}()
	fmt.Fprintf(stdout, "agentserver-core serve: listening with mTLS on %s\n", listener.Addr())
	err = server.Serve(tls.NewListener(listener, tlsConfig))
	if errors.Is(err, http.ErrServerClosed) && ctx.Err() != nil {
		return nil
	}
	return err
}

func coreTLSConfig(certificateFile, keyFile, clientCAFile string) (*tls.Config, error) {
	certificate, err := tls.LoadX509KeyPair(certificateFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load core TLS identity: %w", err)
	}
	clientCAPEM, err := os.ReadFile(clientCAFile)
	if err != nil {
		return nil, fmt.Errorf("read core client CA: %w", err)
	}
	clientCAs := x509.NewCertPool()
	if !clientCAs.AppendCertsFromPEM(clientCAPEM) {
		return nil, errors.New("core client CA file contains no certificates")
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{certificate},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    clientCAs,
		NextProtos:   []string{"h2", "http/1.1"},
	}, nil
}

func requiredConfiguration(getenv func(string) string, name string) (string, error) {
	value := strings.TrimSpace(getenv(name))
	if value == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return value, nil
}
