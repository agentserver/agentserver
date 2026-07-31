package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/agentserver/agentserver/v2/internal/executorgateway"
)

const (
	gatewayListenAddressEnvironment           = "AGENTSERVER_V2_EXECUTOR_GATEWAY_LISTEN_ADDR"
	gatewayTLSCertificateEnvironment          = "AGENTSERVER_V2_EXECUTOR_GATEWAY_TLS_CERT_FILE"
	gatewayTLSKeyEnvironment                  = "AGENTSERVER_V2_EXECUTOR_GATEWAY_TLS_KEY_FILE"
	gatewayCoreURLEnvironment                 = "AGENTSERVER_V2_CORE_URL"
	gatewayCoreCAEnvironment                  = "AGENTSERVER_V2_CORE_CA_FILE"
	gatewayCoreClientCertificateEnvironment   = "AGENTSERVER_V2_CORE_CLIENT_CERT_FILE"
	gatewayCoreClientKeyEnvironment           = "AGENTSERVER_V2_CORE_CLIENT_KEY_FILE"
	gatewayCoreServerNameEnvironment          = "AGENTSERVER_V2_CORE_SERVER_NAME"
	gatewayDevExecutorIDEnvironment           = "AGENTSERVER_V2_DEV_EXECUTOR_ID"
	gatewayDevWorkspaceIDEnvironment          = "AGENTSERVER_V2_DEV_WORKSPACE_ID"
	gatewayDevRunIDEnvironment                = "AGENTSERVER_V2_DEV_RUN_ID"
	gatewayDevRunAttemptIDEnvironment         = "AGENTSERVER_V2_DEV_RUN_ATTEMPT_ID"
	gatewayDevRunAttemptGenerationEnvironment = "AGENTSERVER_V2_DEV_RUN_ATTEMPT_GENERATION"
	gatewayDevRunHolderIDEnvironment          = "AGENTSERVER_V2_DEV_RUN_HOLDER_ID"
	gatewayDevRunVersionEnvironment           = "AGENTSERVER_V2_DEV_RUN_VERSION"
	gatewayDevRunAttemptVersionEnvironment    = "AGENTSERVER_V2_DEV_RUN_ATTEMPT_VERSION"
	gatewayDevToolCatalogDigestEnvironment    = "AGENTSERVER_V2_DEV_TOOL_CATALOG_DIGEST"
	gatewayDevMCPBearerEnvironment            = "AGENTSERVER_V2_DEV_MCP_BEARER_TOKEN"
	gatewayDevExecutorHeader                  = "X-Agentserver-Dev-Executor-Id"
	maximumDevMCPBearerBytes                  = 16 * 1024
)

var canonicalUUIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func serveGateway(ctx context.Context, getenv func(string) string, stdout io.Writer) error {
	listenAddress, err := requiredGatewayConfiguration(getenv, gatewayListenAddressEnvironment)
	if err != nil {
		return err
	}
	if err := requireLoopbackAddress(listenAddress); err != nil {
		return err
	}
	certificateFile, err := requiredGatewayConfiguration(getenv, gatewayTLSCertificateEnvironment)
	if err != nil {
		return err
	}
	keyFile, err := requiredGatewayConfiguration(getenv, gatewayTLSKeyEnvironment)
	if err != nil {
		return err
	}
	coreURL, err := requiredGatewayConfiguration(getenv, gatewayCoreURLEnvironment)
	if err != nil {
		return err
	}
	parsedCoreURL, err := url.Parse(coreURL)
	if err != nil || parsedCoreURL.Scheme != "https" {
		return errors.New("AGENTSERVER_V2_CORE_URL must be an HTTPS origin")
	}
	coreCAFile, err := requiredGatewayConfiguration(getenv, gatewayCoreCAEnvironment)
	if err != nil {
		return err
	}
	coreClientCertificateFile, err := requiredGatewayConfiguration(getenv, gatewayCoreClientCertificateEnvironment)
	if err != nil {
		return err
	}
	coreClientKeyFile, err := requiredGatewayConfiguration(getenv, gatewayCoreClientKeyEnvironment)
	if err != nil {
		return err
	}
	devExecutorID, err := requiredGatewayConfiguration(getenv, gatewayDevExecutorIDEnvironment)
	if err != nil {
		return err
	}
	if devExecutorID == "00000000-0000-0000-0000-000000000000" || !canonicalUUIDPattern.MatchString(devExecutorID) {
		return errors.New("AGENTSERVER_V2_DEV_EXECUTOR_ID must be a non-zero canonical lowercase UUID")
	}
	devWorkspaceID, err := requiredGatewayConfiguration(getenv, gatewayDevWorkspaceIDEnvironment)
	if err != nil {
		return err
	}
	if devWorkspaceID == "00000000-0000-0000-0000-000000000000" || !canonicalUUIDPattern.MatchString(devWorkspaceID) {
		return errors.New("AGENTSERVER_V2_DEV_WORKSPACE_ID must be a non-zero canonical lowercase UUID")
	}
	devRunID, err := requiredGatewayConfiguration(getenv, gatewayDevRunIDEnvironment)
	if err != nil {
		return err
	}
	if devRunID == "00000000-0000-0000-0000-000000000000" || !canonicalUUIDPattern.MatchString(devRunID) {
		return errors.New("AGENTSERVER_V2_DEV_RUN_ID must be a non-zero canonical lowercase UUID")
	}
	devRunAttemptID, err := requiredGatewayConfiguration(getenv, gatewayDevRunAttemptIDEnvironment)
	if err != nil {
		return err
	}
	if devRunAttemptID == "00000000-0000-0000-0000-000000000000" || !canonicalUUIDPattern.MatchString(devRunAttemptID) {
		return errors.New("AGENTSERVER_V2_DEV_RUN_ATTEMPT_ID must be a non-zero canonical lowercase UUID")
	}
	devRunAttemptGeneration, err := requiredPositiveGatewayInt64(getenv, gatewayDevRunAttemptGenerationEnvironment)
	if err != nil {
		return err
	}
	devRunHolderID, err := requiredGatewayConfiguration(getenv, gatewayDevRunHolderIDEnvironment)
	if err != nil {
		return err
	}
	if len(devRunHolderID) > 256 {
		return errors.New("AGENTSERVER_V2_DEV_RUN_HOLDER_ID must contain at most 256 bytes")
	}
	devRunVersion, err := requiredPositiveGatewayInt64(getenv, gatewayDevRunVersionEnvironment)
	if err != nil {
		return err
	}
	devRunAttemptVersion, err := requiredPositiveGatewayInt64(getenv, gatewayDevRunAttemptVersionEnvironment)
	if err != nil {
		return err
	}
	devToolCatalogDigest, err := requiredGatewayConfiguration(getenv, gatewayDevToolCatalogDigestEnvironment)
	if err != nil {
		return err
	}
	if len(devToolCatalogDigest) != 64 {
		return errors.New("AGENTSERVER_V2_DEV_TOOL_CATALOG_DIGEST must be 64 lowercase hexadecimal characters")
	}
	for _, character := range []byte(devToolCatalogDigest) {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return errors.New("AGENTSERVER_V2_DEV_TOOL_CATALOG_DIGEST must be 64 lowercase hexadecimal characters")
		}
	}
	devMCPBearer, err := requiredGatewayConfiguration(getenv, gatewayDevMCPBearerEnvironment)
	if err != nil {
		return err
	}
	mcpAuthenticator, err := newDevMCPAuthenticator(devMCPBearer, devWorkspaceID, devExecutorID, devToolCatalogDigest, executorgateway.ExecutorMCPRunContext{
		RunID:                     devRunID,
		RunAttemptID:              devRunAttemptID,
		RunAttemptGeneration:      devRunAttemptGeneration,
		HolderID:                  devRunHolderID,
		ExpectedRunVersion:        devRunVersion,
		ExpectedRunAttemptVersion: devRunAttemptVersion,
	})
	if err != nil {
		return err
	}

	coreHTTPClient, err := newCoreHTTPClient(
		coreCAFile,
		coreClientCertificateFile,
		coreClientKeyFile,
		strings.TrimSpace(getenv(gatewayCoreServerNameEnvironment)),
	)
	if err != nil {
		return err
	}
	coreClient, err := executorgateway.NewCoreConnectionClient(coreURL, coreHTTPClient)
	if err != nil {
		return err
	}
	gatewayInstanceID, err := executorgateway.NewGatewayInstanceID()
	if err != nil {
		return err
	}
	agentxHandler, err := executorgateway.NewServer(
		devExecutorAuthenticator{executorID: devExecutorID},
		coreClient,
		executorgateway.DefaultServerConfig(gatewayInstanceID),
	)
	if err != nil {
		return err
	}
	environmentResolver, err := executorgateway.NewEnvironmentResolver(coreClient)
	if err != nil {
		return err
	}
	shellIdentities, err := executorgateway.NewDefaultShellV1IdentityAllocator()
	if err != nil {
		return err
	}
	readFileIdentities, err := executorgateway.NewDefaultReadFileV1IdentityAllocator()
	if err != nil {
		return err
	}
	executionTransitions, err := executorgateway.NewDefaultExecutionTransitionAllocator(gatewayInstanceID)
	if err != nil {
		return err
	}
	shellExecutor, err := executorgateway.NewShellExecutor(
		environmentResolver,
		coreClient,
		agentxHandler,
		shellIdentities,
		executionTransitions,
		executorgateway.DefaultShellExecutorConfig(ctx),
	)
	if err != nil {
		return err
	}
	readFileExecutor, err := executorgateway.NewReadFileExecutor(
		environmentResolver,
		coreClient,
		agentxHandler,
		readFileIdentities,
		executionTransitions,
		executorgateway.DefaultReadFileExecutorConfig(ctx),
	)
	if err != nil {
		return err
	}
	mcpConfig := executorgateway.DefaultExecutorMCPConfig()
	mcpConfig.ShellExecutor = shellExecutor
	mcpConfig.ReadFileExecutor = readFileExecutor
	mcpHandler, err := executorgateway.NewExecutorMCPHandler(
		mcpAuthenticator,
		environmentResolver,
		mcpConfig,
	)
	if err != nil {
		return err
	}
	handler := http.NewServeMux()
	handler.Handle(executorgateway.ExecutorMCPPath, mcpHandler)
	handler.Handle("/", agentxHandler)
	tlsConfig, err := gatewayTLSConfig(certificateFile, keyFile)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", listenAddress)
	if err != nil {
		return fmt.Errorf("listen on executor-gateway address: %w", err)
	}
	defer listener.Close()

	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
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
			shutdowns := []func(context.Context) error{
				server.Shutdown,
				agentxHandler.Shutdown,
				mcpHandler.Shutdown,
			}
			completed := make(chan struct{}, len(shutdowns))
			for _, shutdown := range shutdowns {
				go func() {
					_ = shutdown(shutdownContext)
					completed <- struct{}{}
				}()
			}
			for range shutdowns {
				select {
				case <-completed:
				case <-shutdownContext.Done():
					return
				}
			}
		case <-serveContext.Done():
		}
	}()
	fmt.Fprintf(stdout, "executor-gateway serve: INSECURE DEV authentication; listening on %s; MCP endpoint %s; gateway instance %s\n", listener.Addr(), executorgateway.ExecutorMCPPath, gatewayInstanceID)
	err = server.Serve(tls.NewListener(listener, tlsConfig))
	if errors.Is(err, http.ErrServerClosed) && ctx.Err() != nil {
		return nil
	}
	return err
}

type devExecutorAuthenticator struct {
	executorID string
}

type devMCPAuthenticator struct {
	authorization []byte
	principal     executorgateway.ExecutorMCPPrincipal
}

func newDevMCPAuthenticator(bearer, workspaceID, executorID, toolCatalogDigest string, run executorgateway.ExecutorMCPRunContext) (devMCPAuthenticator, error) {
	if len(bearer) < 32 || len(bearer) > maximumDevMCPBearerBytes {
		return devMCPAuthenticator{}, fmt.Errorf("%s must contain between 32 and %d bytes", gatewayDevMCPBearerEnvironment, maximumDevMCPBearerBytes)
	}
	for _, character := range []byte(bearer) {
		if character <= ' ' || character >= 0x7f {
			return devMCPAuthenticator{}, fmt.Errorf("%s contains an invalid byte", gatewayDevMCPBearerEnvironment)
		}
	}
	digest := sha256.Sum256([]byte(bearer))
	return devMCPAuthenticator{
		authorization: []byte("Bearer " + bearer),
		principal: executorgateway.ExecutorMCPPrincipal{
			CapabilityID:      "insecure-dev:" + hex.EncodeToString(digest[:]),
			WorkspaceID:       workspaceID,
			ExecutorID:        executorID,
			ToolCatalogDigest: toolCatalogDigest,
			Run:               run,
		},
	}, nil
}

func (authenticator devMCPAuthenticator) AuthenticateExecutorMCP(request *http.Request) (executorgateway.ExecutorMCPPrincipal, error) {
	values := request.Header.Values("Authorization")
	if len(values) != 1 || subtle.ConstantTimeCompare([]byte(values[0]), authenticator.authorization) != 1 {
		return executorgateway.ExecutorMCPPrincipal{}, errors.New("development MCP bearer is missing or different")
	}
	return authenticator.principal, nil
}

func (authenticator devExecutorAuthenticator) AuthenticateExecutor(request *http.Request) (executorgateway.ExecutorIdentity, error) {
	if request.Header.Get(gatewayDevExecutorHeader) != authenticator.executorID {
		return executorgateway.ExecutorIdentity{}, errors.New("development executor identity header is missing or different")
	}
	return executorgateway.ExecutorIdentity{ExecutorID: authenticator.executorID}, nil
}

func newCoreHTTPClient(caFile, certificateFile, keyFile, serverName string) (*http.Client, error) {
	certificate, err := tls.LoadX509KeyPair(certificateFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load executor-gateway core client identity: %w", err)
	}
	caPEM, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("read core server CA: %w", err)
	}
	rootCAs := x509.NewCertPool()
	if !rootCAs.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("core server CA file contains no certificates")
	}
	transport := &http.Transport{
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          16,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		TLSClientConfig: &tls.Config{
			MinVersion:   tls.VersionTLS13,
			RootCAs:      rootCAs,
			Certificates: []tls.Certificate{certificate},
			ServerName:   serverName,
		},
	}
	return &http.Client{Transport: transport}, nil
}

func gatewayTLSConfig(certificateFile, keyFile string) (*tls.Config, error) {
	certificate, err := tls.LoadX509KeyPair(certificateFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load executor-gateway TLS identity: %w", err)
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{certificate},
		NextProtos:   []string{"http/1.1"},
	}, nil
}

func requireLoopbackAddress(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("parse insecure-dev listen address: %w", err)
	}
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("insecure-dev executor-gateway must bind an explicit loopback address")
	}
	return nil
}

func requiredGatewayConfiguration(getenv func(string) string, name string) (string, error) {
	value := strings.TrimSpace(getenv(name))
	if value == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return value, nil
}

func requiredPositiveGatewayInt64(getenv func(string) string, name string) (int64, error) {
	value, err := requiredGatewayConfiguration(getenv, name)
	if err != nil {
		return 0, err
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 1 {
		return 0, fmt.Errorf("%s must be a positive base-10 integer", name)
	}
	return parsed, nil
}
