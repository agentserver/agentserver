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
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/agentserver/agentserver/v2/internal/executorgateway"
	"github.com/agentserver/agentserver/v2/internal/runcapability"
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
	gatewayDevActorIDEnvironment              = "AGENTSERVER_V2_DEV_ACTOR_ID"
	gatewayDevRunIDEnvironment                = "AGENTSERVER_V2_DEV_RUN_ID"
	gatewayDevRunAttemptIDEnvironment         = "AGENTSERVER_V2_DEV_RUN_ATTEMPT_ID"
	gatewayDevRunAttemptGenerationEnvironment = "AGENTSERVER_V2_DEV_RUN_ATTEMPT_GENERATION"
	gatewayDevRunHolderIDEnvironment          = "AGENTSERVER_V2_DEV_RUN_HOLDER_ID"
	gatewayDevRunVersionEnvironment           = "AGENTSERVER_V2_DEV_RUN_VERSION"
	gatewayDevRunAttemptVersionEnvironment    = "AGENTSERVER_V2_DEV_RUN_ATTEMPT_VERSION"
	gatewayDevMaxApprovalTTLEnvironment       = "AGENTSERVER_V2_DEV_MAX_APPROVAL_TTL_MS"
	gatewayDevToolCatalogDigestEnvironment    = "AGENTSERVER_V2_DEV_TOOL_CATALOG_DIGEST"
	gatewayDevMCPBearerEnvironment            = "AGENTSERVER_V2_DEV_MCP_BEARER_TOKEN"
	gatewayDevRunCapabilityKeyEnvironment     = "AGENTSERVER_V2_DEV_RUN_CAPABILITY_KEY"
	gatewayExecutionPolicyVersionEnvironment  = "AGENTSERVER_V2_EXECUTION_POLICY_VERSION"
	gatewayShellPolicyDecisionEnvironment     = "AGENTSERVER_V2_SHELL_POLICY_DECISION"
	gatewayReadPolicyDecisionEnvironment      = "AGENTSERVER_V2_READ_FILE_POLICY_DECISION"
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
	mcpAuthenticator, err := configuredDevMCPAuthenticator(getenv, devExecutorID)
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
	policyVersion, err := requiredGatewayConfiguration(getenv, gatewayExecutionPolicyVersionEnvironment)
	if err != nil {
		return err
	}
	shellPolicyDecision, err := requiredGatewayConfiguration(getenv, gatewayShellPolicyDecisionEnvironment)
	if err != nil {
		return err
	}
	readPolicyDecision, err := requiredGatewayConfiguration(getenv, gatewayReadPolicyDecisionEnvironment)
	if err != nil {
		return err
	}
	policyResolver, err := executorgateway.NewStaticExecutionPolicyResolver(policyVersion, map[string]string{
		"shell": shellPolicyDecision, "read_file": readPolicyDecision,
	})
	if err != nil {
		return err
	}
	approvalGate, err := executorgateway.NewDefaultCoreApprovalGate(coreClient, executionTransitions)
	if err != nil {
		return err
	}
	shellConfig := executorgateway.DefaultShellExecutorConfig(ctx)
	shellConfig.PolicyResolver = policyResolver
	shellConfig.ApprovalGate = approvalGate
	shellExecutor, err := executorgateway.NewShellExecutor(
		environmentResolver,
		coreClient,
		agentxHandler,
		shellIdentities,
		executionTransitions,
		shellConfig,
	)
	if err != nil {
		return err
	}
	readFileConfig := executorgateway.DefaultReadFileExecutorConfig(ctx)
	readFileConfig.PolicyResolver = policyResolver
	readFileConfig.ApprovalGate = approvalGate
	readFileExecutor, err := executorgateway.NewReadFileExecutor(
		environmentResolver,
		coreClient,
		agentxHandler,
		readFileIdentities,
		executionTransitions,
		readFileConfig,
	)
	if err != nil {
		return err
	}
	mcpConfig := executorgateway.DefaultExecutorMCPConfig()
	mcpConfig.Logger = slog.Default()
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

type devRunCapabilityAuthenticator struct {
	codec      *runcapability.DevelopmentCodec
	executorID string
	now        func() time.Time
}

func configuredDevMCPAuthenticator(getenv func(string) string, executorID string) (executorgateway.ExecutorMCPAuthenticator, error) {
	encodedKey := strings.TrimSpace(getenv(gatewayDevRunCapabilityKeyEnvironment))
	if encodedKey != "" {
		codec, err := runcapability.NewDevelopmentCodecFromBase64Key(encodedKey)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", gatewayDevRunCapabilityKeyEnvironment, err)
		}
		return newDevRunCapabilityAuthenticator(codec, executorID, time.Now)
	}

	workspaceID, err := requiredGatewayConfiguration(getenv, gatewayDevWorkspaceIDEnvironment)
	if err != nil {
		return nil, err
	}
	if workspaceID == "00000000-0000-0000-0000-000000000000" || !canonicalUUIDPattern.MatchString(workspaceID) {
		return nil, errors.New("AGENTSERVER_V2_DEV_WORKSPACE_ID must be a non-zero canonical lowercase UUID")
	}
	actorID, err := requiredGatewayConfiguration(getenv, gatewayDevActorIDEnvironment)
	if err != nil {
		return nil, err
	}
	if actorID == "00000000-0000-0000-0000-000000000000" || !canonicalUUIDPattern.MatchString(actorID) {
		return nil, errors.New("AGENTSERVER_V2_DEV_ACTOR_ID must be a non-zero canonical lowercase UUID")
	}
	runID, err := requiredGatewayConfiguration(getenv, gatewayDevRunIDEnvironment)
	if err != nil {
		return nil, err
	}
	if runID == "00000000-0000-0000-0000-000000000000" || !canonicalUUIDPattern.MatchString(runID) {
		return nil, errors.New("AGENTSERVER_V2_DEV_RUN_ID must be a non-zero canonical lowercase UUID")
	}
	runAttemptID, err := requiredGatewayConfiguration(getenv, gatewayDevRunAttemptIDEnvironment)
	if err != nil {
		return nil, err
	}
	if runAttemptID == "00000000-0000-0000-0000-000000000000" || !canonicalUUIDPattern.MatchString(runAttemptID) {
		return nil, errors.New("AGENTSERVER_V2_DEV_RUN_ATTEMPT_ID must be a non-zero canonical lowercase UUID")
	}
	runAttemptGeneration, err := requiredPositiveGatewayInt64(getenv, gatewayDevRunAttemptGenerationEnvironment)
	if err != nil {
		return nil, err
	}
	holderID, err := requiredGatewayConfiguration(getenv, gatewayDevRunHolderIDEnvironment)
	if err != nil {
		return nil, err
	}
	if len(holderID) > 256 {
		return nil, errors.New("AGENTSERVER_V2_DEV_RUN_HOLDER_ID must contain at most 256 bytes")
	}
	runVersion, err := requiredPositiveGatewayInt64(getenv, gatewayDevRunVersionEnvironment)
	if err != nil {
		return nil, err
	}
	runAttemptVersion, err := requiredPositiveGatewayInt64(getenv, gatewayDevRunAttemptVersionEnvironment)
	if err != nil {
		return nil, err
	}
	maxApprovalTTLMillis, err := requiredPositiveGatewayInt64(getenv, gatewayDevMaxApprovalTTLEnvironment)
	if err != nil {
		return nil, err
	}
	if maxApprovalTTLMillis > int64(24*time.Hour/time.Millisecond) {
		return nil, fmt.Errorf("%s must be at most 86400000", gatewayDevMaxApprovalTTLEnvironment)
	}
	toolCatalogDigest, err := requiredGatewayConfiguration(getenv, gatewayDevToolCatalogDigestEnvironment)
	if err != nil {
		return nil, err
	}
	if len(toolCatalogDigest) != 64 {
		return nil, errors.New("AGENTSERVER_V2_DEV_TOOL_CATALOG_DIGEST must be 64 lowercase hexadecimal characters")
	}
	for _, character := range []byte(toolCatalogDigest) {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return nil, errors.New("AGENTSERVER_V2_DEV_TOOL_CATALOG_DIGEST must be 64 lowercase hexadecimal characters")
		}
	}
	bearer, err := requiredGatewayConfiguration(getenv, gatewayDevMCPBearerEnvironment)
	if err != nil {
		return nil, err
	}
	return newDevMCPAuthenticator(bearer, workspaceID, actorID, executorID, toolCatalogDigest, time.Duration(maxApprovalTTLMillis)*time.Millisecond, executorgateway.ExecutorMCPRunContext{
		RunID: runID, RunAttemptID: runAttemptID, RunAttemptGeneration: runAttemptGeneration,
		HolderID: holderID, ExpectedRunVersion: runVersion, ExpectedRunAttemptVersion: runAttemptVersion,
	})
}

func newDevRunCapabilityAuthenticator(
	codec *runcapability.DevelopmentCodec,
	executorID string,
	now func() time.Time,
) (devRunCapabilityAuthenticator, error) {
	if codec == nil {
		return devRunCapabilityAuthenticator{}, errors.New("development run capability codec is required")
	}
	if executorID == "00000000-0000-0000-0000-000000000000" || !canonicalUUIDPattern.MatchString(executorID) {
		return devRunCapabilityAuthenticator{}, errors.New("development run capability executor ID must be a non-zero canonical lowercase UUID")
	}
	if now == nil {
		return devRunCapabilityAuthenticator{}, errors.New("development run capability clock is required")
	}
	return devRunCapabilityAuthenticator{codec: codec, executorID: executorID, now: now}, nil
}

func newDevMCPAuthenticator(bearer, workspaceID, actorID, executorID, toolCatalogDigest string, maxApprovalTTL time.Duration, run executorgateway.ExecutorMCPRunContext) (devMCPAuthenticator, error) {
	if len(bearer) < 32 || len(bearer) > maximumDevMCPBearerBytes {
		return devMCPAuthenticator{}, fmt.Errorf("%s must contain between 32 and %d bytes", gatewayDevMCPBearerEnvironment, maximumDevMCPBearerBytes)
	}
	for _, character := range []byte(bearer) {
		if character <= ' ' || character >= 0x7f {
			return devMCPAuthenticator{}, fmt.Errorf("%s contains an invalid byte", gatewayDevMCPBearerEnvironment)
		}
	}
	if actorID == "00000000-0000-0000-0000-000000000000" || !canonicalUUIDPattern.MatchString(actorID) {
		return devMCPAuthenticator{}, errors.New("development MCP actor ID must be a non-zero canonical lowercase UUID")
	}
	if maxApprovalTTL <= 0 || maxApprovalTTL > 24*time.Hour {
		return devMCPAuthenticator{}, errors.New("development MCP maximum approval TTL must be positive and at most 24 hours")
	}
	digest := sha256.Sum256([]byte(bearer))
	// The static bearer exists only for the explicit insecure-development
	// fallback. Keep it bounded even though it is not minted per attempt.
	runDeadline := time.Now().UTC().Add(24 * time.Hour)
	return devMCPAuthenticator{
		authorization: []byte("Bearer " + bearer),
		principal: executorgateway.ExecutorMCPPrincipal{
			CapabilityID:        "insecure-dev:" + hex.EncodeToString(digest[:]),
			WorkspaceID:         workspaceID,
			ActorID:             actorID,
			ExecutorID:          executorID,
			ToolCatalogDigest:   toolCatalogDigest,
			MaxApprovalTTL:      maxApprovalTTL,
			RunDeadline:         runDeadline,
			CapabilityExpiresAt: runDeadline,
			Run:                 run,
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

func (authenticator devRunCapabilityAuthenticator) AuthenticateExecutorMCP(request *http.Request) (executorgateway.ExecutorMCPPrincipal, error) {
	if request == nil || authenticator.codec == nil || authenticator.now == nil {
		return executorgateway.ExecutorMCPPrincipal{}, errors.New("development MCP run capability authenticator is unavailable")
	}
	values := request.Header.Values("Authorization")
	if len(values) != 1 || strings.Contains(values[0], ",") || !strings.HasPrefix(values[0], "Bearer ") {
		return executorgateway.ExecutorMCPPrincipal{}, errors.New("development MCP run capability is missing")
	}
	token := strings.TrimPrefix(values[0], "Bearer ")
	if token == "" || strings.TrimSpace(token) != token {
		return executorgateway.ExecutorMCPPrincipal{}, errors.New("development MCP run capability framing is invalid")
	}
	claims, err := authenticator.codec.Verify(token, runcapability.AudienceExecutorMCP, authenticator.now())
	if err != nil {
		return executorgateway.ExecutorMCPPrincipal{}, fmt.Errorf("verify development MCP run capability: %w", err)
	}
	if claims.ExecutorID != authenticator.executorID {
		return executorgateway.ExecutorMCPPrincipal{}, errors.New("development MCP run capability belongs to another executor")
	}
	return executorgateway.ExecutorMCPPrincipal{
		CapabilityID: "insecure-dev:" + claims.CapabilityID,
		WorkspaceID:  claims.WorkspaceID, ActorID: claims.ActorID, ExecutorID: claims.ExecutorID,
		ToolCatalogDigest:   claims.ToolCatalogDigest,
		MaxApprovalTTL:      time.Duration(claims.MaxApprovalTTLMillis) * time.Millisecond,
		RunDeadline:         time.UnixMilli(claims.RunDeadlineUnixMS).UTC(),
		CapabilityExpiresAt: time.UnixMilli(claims.ExpiresAtUnixMS).UTC(),
		Run: executorgateway.ExecutorMCPRunContext{
			RunID: claims.RunID, RunAttemptID: claims.RunAttemptID,
			RunAttemptGeneration: claims.RunAttemptGeneration, HolderID: claims.HolderID,
			ExpectedRunVersion:        claims.ExpectedRunVersion,
			ExpectedRunAttemptVersion: claims.ExpectedRunAttemptVersion,
		},
	}, nil
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
