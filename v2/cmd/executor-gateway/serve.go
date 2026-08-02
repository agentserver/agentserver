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
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/agentserver/agentserver/v2/internal/executorgateway"
	"github.com/agentserver/agentserver/v2/internal/runcapability"
)

const (
	gatewayListenAddressEnvironment           = "AGENTSERVER_V2_EXECUTOR_GATEWAY_LISTEN_ADDR"
	gatewayPublicListenAddressEnvironment     = "AGENTSERVER_V2_EXECUTOR_GATEWAY_PUBLIC_LISTEN_ADDR"
	gatewayTLSCertificateEnvironment          = "AGENTSERVER_V2_EXECUTOR_GATEWAY_TLS_CERT_FILE"
	gatewayTLSKeyEnvironment                  = "AGENTSERVER_V2_EXECUTOR_GATEWAY_TLS_KEY_FILE"
	gatewayCoreURLEnvironment                 = "AGENTSERVER_V2_CORE_URL"
	gatewayCoreCAEnvironment                  = "AGENTSERVER_V2_CORE_CA_FILE"
	gatewayCoreClientCertificateEnvironment   = "AGENTSERVER_V2_CORE_CLIENT_CERT_FILE"
	gatewayCoreClientKeyEnvironment           = "AGENTSERVER_V2_CORE_CLIENT_KEY_FILE"
	gatewayCoreServerNameEnvironment          = "AGENTSERVER_V2_CORE_SERVER_NAME"
	gatewaySPIFFEIdentityEnvironment          = "AGENTSERVER_V2_EXECUTOR_GATEWAY_SPIFFE_ID"
	gatewayExecutorIDEnvironment              = "AGENTSERVER_V2_EXECUTOR_ID"
	gatewayCapabilityIssuerEnvironment        = "AGENTSERVER_V2_RUN_CAPABILITY_ISSUER"
	gatewayCapabilityKeyringEnvironment       = "AGENTSERVER_V2_RUN_CAPABILITY_KEYRING_FILE"
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
	maximumGatewayTLSFileBytes                = int64(1024 * 1024)
	gatewayStartupRecoveryTimeout             = 2 * time.Minute
)

var canonicalUUIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func serveGateway(ctx context.Context, getenv func(string) string, stdout io.Writer, mode gatewayServeMode) error {
	if ctx == nil || getenv == nil || stdout == nil {
		return errors.New("executor-gateway serve context, configuration source, and output are required")
	}
	if mode != gatewayServeProduction && mode != gatewayServeInsecureDevelopment {
		return errors.New("executor-gateway serve mode is invalid")
	}
	listenAddress, err := requiredGatewayConfiguration(getenv, gatewayListenAddressEnvironment)
	if err != nil {
		return err
	}
	if mode == gatewayServeInsecureDevelopment {
		if err := requireLoopbackAddress(listenAddress); err != nil {
			return err
		}
	} else if _, _, err := net.SplitHostPort(listenAddress); err != nil {
		return fmt.Errorf("parse production executor-gateway internal listen address: %w", err)
	}
	var publicListenAddress string
	if mode == gatewayServeProduction {
		publicListenAddress, err = requiredGatewayConfiguration(getenv, gatewayPublicListenAddressEnvironment)
		if err != nil {
			return err
		}
		if _, _, err := net.SplitHostPort(publicListenAddress); err != nil {
			return fmt.Errorf("parse production executor-gateway public listen address: %w", err)
		}
		if publicListenAddress == listenAddress {
			return errors.New("production executor-gateway public and internal listen addresses must be distinct")
		}
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
	if err := validateGatewayHTTPSOrigin(coreURL, gatewayCoreURLEnvironment); err != nil {
		return err
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
	coreServerName := strings.TrimSpace(getenv(gatewayCoreServerNameEnvironment))
	var gatewaySPIFFEIdentity string
	if mode == gatewayServeProduction {
		gatewaySPIFFEIdentity, err = requiredGatewayConfiguration(getenv, gatewaySPIFFEIdentityEnvironment)
		if err != nil {
			return err
		}
		if err := validateGatewaySPIFFEIdentity(gatewaySPIFFEIdentity); err != nil {
			return fmt.Errorf("%s: %w", gatewaySPIFFEIdentityEnvironment, err)
		}
		if coreServerName == "" {
			return fmt.Errorf("%s is required in production", gatewayCoreServerNameEnvironment)
		}
	}
	coreHTTPClient, err := newCoreHTTPClientWithIdentity(
		coreCAFile, coreClientCertificateFile, coreClientKeyFile,
		coreServerName, gatewaySPIFFEIdentity,
	)
	if err != nil {
		return err
	}
	defer coreHTTPClient.CloseIdleConnections()
	coreClient, err := executorgateway.NewCoreConnectionClient(coreURL, coreHTTPClient)
	if err != nil {
		return err
	}
	gatewayInstanceID, err := executorgateway.NewGatewayInstanceID()
	if err != nil {
		return err
	}
	var executorID string
	var executorAuthenticator executorgateway.ExecutorAuthenticator
	var mcpAuthenticator executorgateway.ExecutorMCPAuthenticator
	var identityHandler *executorgateway.ExecutorIdentityHandler
	switch mode {
	case gatewayServeInsecureDevelopment:
		executorID, err = requiredGatewayConfiguration(getenv, gatewayDevExecutorIDEnvironment)
		if err != nil {
			return err
		}
		if err := validateGatewayExecutorID(gatewayDevExecutorIDEnvironment, executorID); err != nil {
			return err
		}
		mcpAuthenticator, err = configuredDevMCPAuthenticator(getenv, executorID)
		if err != nil {
			return err
		}
		executorAuthenticator = devExecutorAuthenticator{executorID: executorID}
	case gatewayServeProduction:
		executorID, err = requiredGatewayConfiguration(getenv, gatewayExecutorIDEnvironment)
		if err != nil {
			return err
		}
		if err := validateGatewayExecutorID(gatewayExecutorIDEnvironment, executorID); err != nil {
			return err
		}
		capabilityIssuer, err := requiredGatewayConfiguration(getenv, gatewayCapabilityIssuerEnvironment)
		if err != nil {
			return err
		}
		capabilityKeyring, err := requiredGatewayConfiguration(getenv, gatewayCapabilityKeyringEnvironment)
		if err != nil {
			return err
		}
		if err := validateGatewayConfigPath(capabilityKeyring); err != nil {
			return fmt.Errorf("%s: %w", gatewayCapabilityKeyringEnvironment, err)
		}
		verifier, err := runcapability.LoadProductionVerifier(capabilityIssuer, capabilityKeyring)
		if err != nil {
			return fmt.Errorf("configure executor-gateway production run capability verifier: %w", err)
		}
		mcpAuthenticator, err = executorgateway.NewProductionExecutorMCPAuthenticator(verifier, coreClient, executorID, time.Now)
		if err != nil {
			return err
		}
		challengeConfig := executorgateway.DefaultExecutorChallengeConfig(gatewayInstanceID, executorID)
		challenges, err := executorgateway.NewExecutorChallengeAuthority(coreClient, challengeConfig)
		if err != nil {
			return err
		}
		executorAuthenticator, err = executorgateway.NewProductionExecutorAuthenticator(coreClient, challenges)
		if err != nil {
			return err
		}
		identityHandler, err = executorgateway.NewExecutorIdentityHandler(coreClient, challenges)
		if err != nil {
			return err
		}
	}
	agentxHandler, err := executorgateway.NewServer(
		executorAuthenticator,
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
	readiness := &gatewayReadiness{}
	var tlsConfig *tls.Config
	if mode == gatewayServeProduction {
		tlsConfig, err = productionGatewayTLSConfig(certificateFile, keyFile, gatewaySPIFFEIdentity)
	} else {
		tlsConfig, err = gatewayTLSConfig(certificateFile, keyFile)
	}
	if err != nil {
		return err
	}
	var startupRecovery *executorgateway.GatewayStartupRecoveryResult
	if mode == gatewayServeProduction {
		recoveryContext, cancelRecovery := context.WithTimeout(ctx, gatewayStartupRecoveryTimeout)
		recovered, recoveryErr := executorgateway.RecoverGatewayStartup(
			recoveryContext, coreClient, executorID, gatewayInstanceID, executionTransitions,
		)
		cancelRecovery()
		if recoveryErr != nil {
			return recoveryErr
		}
		startupRecovery = &recovered
	}
	internalListener, err := net.Listen("tcp", listenAddress)
	if err != nil {
		return fmt.Errorf("listen on executor-gateway internal address: %w", err)
	}
	defer internalListener.Close()
	internalHandler := gatewayRoutes(mcpHandler, agentxHandler, identityHandler, readiness)
	if mode == gatewayServeProduction {
		internalHandler = gatewayInternalRoutes(mcpHandler, readiness)
	}
	internalServer := &http.Server{
		Handler:           internalHandler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    32 * 1024,
		TLSConfig:         tlsConfig,
	}
	type serveEndpoint struct {
		server   *http.Server
		listener net.Listener
		useTLS   bool
	}
	endpoints := []serveEndpoint{{server: internalServer, listener: internalListener, useTLS: true}}
	var publicListener net.Listener
	if mode == gatewayServeProduction {
		publicListener, err = net.Listen("tcp", publicListenAddress)
		if err != nil {
			return fmt.Errorf("listen on executor-gateway public address: %w", err)
		}
		defer publicListener.Close()
		endpoints = append(endpoints, serveEndpoint{
			server: &http.Server{
				Handler:           gatewayPublicRoutes(agentxHandler, identityHandler, readiness),
				ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 * 1024,
			},
			listener: publicListener,
		})
	}
	var shutdownOnce sync.Once
	shutdown := func() {
		shutdownOnce.Do(func() {
			readiness.ready.Store(false)
			shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			shutdowns := []func(context.Context) error{agentxHandler.Shutdown, mcpHandler.Shutdown}
			for _, endpoint := range endpoints {
				shutdowns = append(shutdowns, endpoint.server.Shutdown)
			}
			completed := make(chan struct{}, len(shutdowns))
			for _, stop := range shutdowns {
				go func(stop func(context.Context) error) {
					_ = stop(shutdownContext)
					completed <- struct{}{}
				}(stop)
			}
			for range shutdowns {
				select {
				case <-completed:
				case <-shutdownContext.Done():
					return
				}
			}
		})
	}
	watchDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			shutdown()
		case <-watchDone:
		}
	}()
	authorityDescription := "production OAuth + Ed25519 machine proof"
	if mode == gatewayServeInsecureDevelopment {
		authorityDescription = "INSECURE DEV authentication"
	}
	recoveryDescription := "development startup recovery disabled"
	if startupRecovery != nil {
		recoveryDescription = fmt.Sprintf(
			"startup recovery generation %d, reconciled %d executions in %d passes",
			startupRecovery.FencedConnectionGeneration,
			startupRecovery.RecoveredExecutions,
			startupRecovery.Passes,
		)
	}
	readiness.ready.Store(true)
	if mode == gatewayServeProduction {
		fmt.Fprintf(stdout, "executor-gateway serve: %s; %s; single-replica process-local resume/challenges; public agentx HTTP on %s; internal MCP TLS on %s%s; gateway instance %s\n",
			authorityDescription, recoveryDescription, publicListener.Addr(), internalListener.Addr(), executorgateway.ExecutorMCPPath, gatewayInstanceID)
	} else {
		fmt.Fprintf(stdout, "executor-gateway serve: %s; %s; single-replica process-local resume/challenges; development TLS on %s; MCP endpoint %s; gateway instance %s\n",
			authorityDescription, recoveryDescription, internalListener.Addr(), executorgateway.ExecutorMCPPath, gatewayInstanceID)
	}
	serveErrors := make(chan error, len(endpoints))
	for _, endpoint := range endpoints {
		go func(endpoint serveEndpoint) {
			listener := endpoint.listener
			if endpoint.useTLS {
				listener = tls.NewListener(listener, tlsConfig)
			}
			serveErrors <- endpoint.server.Serve(listener)
		}(endpoint)
	}
	firstError := <-serveErrors
	shutdown()
	for remaining := 1; remaining < len(endpoints); remaining++ {
		<-serveErrors
	}
	close(watchDone)
	if errors.Is(firstError, http.ErrServerClosed) && ctx.Err() != nil {
		return nil
	}
	return firstError
}

type gatewayReadiness struct {
	ready atomic.Bool
}

func gatewayInternalRoutes(mcp http.Handler, readiness *gatewayReadiness) http.Handler {
	return gatewayRoutes(mcp, nil, nil, readiness)
}

func gatewayPublicRoutes(
	agentx http.Handler,
	identity *executorgateway.ExecutorIdentityHandler,
	readiness *gatewayReadiness,
) http.Handler {
	return gatewayRoutes(nil, agentx, identity, readiness)
}

func gatewayRoutes(
	mcp, agentx http.Handler,
	identity *executorgateway.ExecutorIdentityHandler,
	readiness *gatewayReadiness,
) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request == nil || request.URL == nil {
			writeGatewayHealth(response, http.StatusNotFound, `{"status":"not_found"}`)
			return
		}
		switch request.URL.Path {
		case executorgateway.ExecutorMCPPath:
			if mcp != nil {
				mcp.ServeHTTP(response, request)
				return
			}
		case executorgateway.AgentxConnectPath:
			if agentx != nil {
				agentx.ServeHTTP(response, request)
				return
			}
		case executorgateway.AgentxEnrollmentPath, executorgateway.AgentxChallengePath:
			if identity != nil {
				identity.ServeHTTP(response, request)
				return
			}
		case "/healthz":
			if exactGatewayHealthRequest(request) {
				writeGatewayHealth(response, http.StatusOK, `{"status":"ok"}`)
				return
			}
		case "/readyz":
			if exactGatewayHealthRequest(request) {
				if readiness == nil || !readiness.ready.Load() {
					writeGatewayHealth(response, http.StatusServiceUnavailable, `{"status":"not_ready"}`)
					return
				}
				writeGatewayHealth(response, http.StatusOK, `{"status":"ready"}`)
				return
			}
		}
		writeGatewayHealth(response, http.StatusNotFound, `{"status":"not_found"}`)
	})
}

func exactGatewayHealthRequest(request *http.Request) bool {
	return request.Method == http.MethodGet && request.URL.RawPath == "" && request.URL.RawQuery == "" && !request.URL.ForceQuery
}

func writeGatewayHealth(response http.ResponseWriter, status int, body string) {
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.WriteHeader(status)
	_, _ = io.WriteString(response, body+"\n")
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
	return newCoreHTTPClientWithIdentity(caFile, certificateFile, keyFile, serverName, "")
}

func newCoreHTTPClientWithIdentity(caFile, certificateFile, keyFile, serverName, expectedSPIFFEIdentity string) (*http.Client, error) {
	var certificate tls.Certificate
	var err error
	if expectedSPIFFEIdentity == "" {
		certificate, err = tls.LoadX509KeyPair(certificateFile, keyFile)
	} else {
		certificate, err = loadGatewayTLSIdentity(certificateFile, keyFile, expectedSPIFFEIdentity)
	}
	if err != nil {
		return nil, fmt.Errorf("load executor-gateway core client identity: %w", err)
	}
	var caPEM []byte
	if expectedSPIFFEIdentity == "" {
		caPEM, err = os.ReadFile(caFile)
	} else {
		caPEM, err = readStableGatewayFile("Core CA", caFile, maximumGatewayTLSFileBytes, false)
	}
	if err != nil {
		return nil, fmt.Errorf("read core server CA: %w", err)
	}
	rootCAs := x509.NewCertPool()
	if !rootCAs.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("core server CA file contains no certificates")
	}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          32,
		MaxIdleConnsPerHost:   32,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: time.Second,
		DisableCompression:    true,
		TLSClientConfig: &tls.Config{
			MinVersion:   tls.VersionTLS13,
			RootCAs:      rootCAs,
			Certificates: []tls.Certificate{certificate},
			ServerName:   serverName,
		},
	}
	return &http.Client{Transport: transport}, nil
}

func productionGatewayTLSConfig(certificateFile, keyFile, expectedSPIFFEIdentity string) (*tls.Config, error) {
	certificate, err := loadGatewayTLSIdentity(certificateFile, keyFile, expectedSPIFFEIdentity)
	if err != nil {
		return nil, fmt.Errorf("load executor-gateway production TLS identity: %w", err)
	}
	return &tls.Config{
		MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate},
		NextProtos: []string{"http/1.1"},
	}, nil
}

func loadGatewayTLSIdentity(certificatePath, keyPath, expectedSPIFFEIdentity string) (tls.Certificate, error) {
	if err := validateGatewaySPIFFEIdentity(expectedSPIFFEIdentity); err != nil {
		return tls.Certificate{}, err
	}
	certificateBytes, err := readStableGatewayFile("TLS certificate", certificatePath, maximumGatewayTLSFileBytes, false)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyBytes, err := readStableGatewayFile("TLS private key", keyPath, maximumGatewayTLSFileBytes, true)
	if err != nil {
		return tls.Certificate{}, err
	}
	defer clear(keyBytes)
	certificate, err := tls.X509KeyPair(certificateBytes, keyBytes)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("parse TLS key pair: %w", err)
	}
	if len(certificate.Certificate) == 0 {
		return tls.Certificate{}, errors.New("TLS certificate chain is empty")
	}
	leaf, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("parse TLS leaf certificate: %w", err)
	}
	if len(leaf.URIs) != 1 || leaf.URIs[0].String() != expectedSPIFFEIdentity {
		return tls.Certificate{}, errors.New("TLS leaf certificate does not contain the exact configured SPIFFE identity")
	}
	certificate.Leaf = leaf
	return certificate, nil
}

func readStableGatewayFile(label, path string, maximum int64, restricted bool) ([]byte, error) {
	if err := validateGatewayConfigPath(path); err != nil {
		return nil, fmt.Errorf("%s path: %w", label, err)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", label, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect %s: %w", label, err)
	}
	if !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > maximum || (restricted && info.Mode().Perm()&0o077 != 0) {
		return nil, fmt.Errorf("%s must be a bounded regular file with safe permissions", label)
	}
	raw, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil || int64(len(raw)) != info.Size() {
		clear(raw)
		return nil, fmt.Errorf("read stable %s", label)
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(info, after) || info.Size() != after.Size() || !info.ModTime().Equal(after.ModTime()) || info.Mode() != after.Mode() {
		clear(raw)
		return nil, fmt.Errorf("%s changed while it was being read", label)
	}
	return raw, nil
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

func validateGatewayHTTPSOrigin(raw, name string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" || parsed.RawPath != "" || parsed.Opaque != "" || parsed.ForceQuery ||
		(parsed.Path != "" && parsed.Path != "/") {
		return fmt.Errorf("%s must be an HTTPS origin without credentials, path, query, or fragment", name)
	}
	return nil
}

func validateGatewaySPIFFEIdentity(raw string) error {
	identity, err := url.Parse(raw)
	if err != nil || identity.Scheme != "spiffe" || identity.Host == "" || identity.User != nil || identity.Path == "" ||
		identity.RawPath != "" || identity.RawQuery != "" || identity.Fragment != "" || identity.Opaque != "" || identity.ForceQuery ||
		identity.String() != raw || len(raw) > 2048 || strings.ContainsAny(raw, "\x00\r\n") {
		return errors.New("value must be an exact bounded SPIFFE URI")
	}
	return nil
}

func validateGatewayConfigPath(path string) error {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsRune(path, 0) {
		return errors.New("path must be absolute and clean")
	}
	return nil
}

func validateGatewayExecutorID(name, value string) error {
	if value == "00000000-0000-0000-0000-000000000000" || !canonicalUUIDPattern.MatchString(value) {
		return fmt.Errorf("%s must be a non-zero canonical lowercase UUID", name)
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
