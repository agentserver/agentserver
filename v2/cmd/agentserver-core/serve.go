package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
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
	"github.com/agentserver/agentserver/v2/internal/objectruntime"
	"github.com/agentserver/agentserver/v2/internal/runcursor"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	coreListenAddressEnvironment        = "AGENTSERVER_V2_CORE_LISTEN_ADDR"
	coreTLSCertificateEnvironment       = "AGENTSERVER_V2_CORE_TLS_CERT_FILE"
	coreTLSKeyEnvironment               = "AGENTSERVER_V2_CORE_TLS_KEY_FILE"
	coreClientCAEnvironment             = "AGENTSERVER_V2_CORE_CLIENT_CA_FILE"
	coreGatewayIdentityEnvironment      = "AGENTSERVER_V2_EXECUTOR_GATEWAY_SPIFFE_ID"
	coreHarnessPoolIdentityEnvironment  = "AGENTSERVER_V2_HARNESS_POOL_SPIFFE_ID"
	coreBrowserIdentityEnvironment      = "AGENTSERVER_V2_BROWSER_GATEWAY_SPIFFE_ID"
	coreHydraIntrospectionEnvironment   = "AGENTSERVER_V2_HYDRA_INTROSPECTION_URL"
	coreHydraAdminEnvironment           = "AGENTSERVER_V2_HYDRA_ADMIN_URL"
	coreHydraPublicOriginEnvironment    = "AGENTSERVER_V2_HYDRA_PUBLIC_ORIGIN"
	coreHydraBrowserClientEnvironment   = "AGENTSERVER_V2_HYDRA_BROWSER_CLIENT_ID"
	coreHydraInsecureHTTPEnvironment    = "AGENTSERVER_V2_HYDRA_ALLOW_INSECURE_HTTP"
	coreExternalOIDCIssuerEnvironment   = "AGENTSERVER_V2_EXTERNAL_OIDC_ISSUER"
	coreExternalOIDCClientEnvironment   = "AGENTSERVER_V2_EXTERNAL_OIDC_CLIENT_ID"
	coreExternalOIDCSecretEnvironment   = "AGENTSERVER_V2_EXTERNAL_OIDC_CLIENT_SECRET"
	coreExternalOIDCRedirectEnvironment = "AGENTSERVER_V2_EXTERNAL_OIDC_REDIRECT_URL"
	coreExternalOIDCInsecureEnvironment = "AGENTSERVER_V2_EXTERNAL_OIDC_ALLOW_INSECURE_HTTP"
	coreLoginTransactionKeyEnvironment  = "AGENTSERVER_V2_LOGIN_TRANSACTION_KEY"
	coreRunCursorKeyEnvironment         = "AGENTSERVER_V2_RUN_CURSOR_KEY"
	coreDevPromptObjectRootEnvironment  = "AGENTSERVER_V2_DEV_PROMPT_OBJECT_DIR"
	coreRunPolicyVersionEnvironment     = "AGENTSERVER_V2_RUN_POLICY_VERSION"
	coreRunAllowedToolsEnvironment      = "AGENTSERVER_V2_RUN_ALLOWED_TOOLS"
)

func serveCore(ctx context.Context, getenv func(string) string, stdout io.Writer, mode coreServeMode) error {
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
	browserIdentity, err := requiredConfiguration(getenv, coreBrowserIdentityEnvironment)
	if err != nil {
		return err
	}
	if browserIdentity == gatewayIdentity || browserIdentity == harnessPoolIdentity {
		return errors.New("browser-gateway, executor-gateway, and harness-pool SPIFFE identities must be distinct")
	}
	hydraEndpoint, err := requiredConfiguration(getenv, coreHydraIntrospectionEnvironment)
	if err != nil {
		return err
	}
	hydraAdminOrigin, err := requiredConfiguration(getenv, coreHydraAdminEnvironment)
	if err != nil {
		return err
	}
	hydraPublicOrigin, err := requiredConfiguration(getenv, coreHydraPublicOriginEnvironment)
	if err != nil {
		return err
	}
	hydraBrowserClientID, err := requiredConfiguration(getenv, coreHydraBrowserClientEnvironment)
	if err != nil {
		return err
	}
	allowInsecureHydra, err := strictOptionalBoolean(getenv(coreHydraInsecureHTTPEnvironment), coreHydraInsecureHTTPEnvironment)
	if err != nil {
		return err
	}
	externalOIDCIssuer, err := requiredConfiguration(getenv, coreExternalOIDCIssuerEnvironment)
	if err != nil {
		return err
	}
	externalOIDCClientID, err := requiredConfiguration(getenv, coreExternalOIDCClientEnvironment)
	if err != nil {
		return err
	}
	externalOIDCClientSecret, err := requiredConfiguration(getenv, coreExternalOIDCSecretEnvironment)
	if err != nil {
		return err
	}
	externalOIDCRedirectURL, err := requiredConfiguration(getenv, coreExternalOIDCRedirectEnvironment)
	if err != nil {
		return err
	}
	allowInsecureExternalOIDC, err := strictOptionalBoolean(getenv(coreExternalOIDCInsecureEnvironment), coreExternalOIDCInsecureEnvironment)
	if err != nil {
		return err
	}
	loginTransactionKeyEncoded, err := requiredConfiguration(getenv, coreLoginTransactionKeyEnvironment)
	if err != nil {
		return err
	}
	loginTransactionKey, err := decodeLoginTransactionKey(loginTransactionKeyEncoded)
	if err != nil {
		return err
	}
	defer clear(loginTransactionKey)
	cursorKeyEncoded, err := requiredConfiguration(getenv, coreRunCursorKeyEnvironment)
	if err != nil {
		return err
	}
	cursorKey, err := decodeRunCursorKey(cursorKeyEncoded)
	if err != nil {
		return err
	}
	promptStore, objectStoreDescription, err := configureCorePromptStore(ctx, getenv, mode)
	if err != nil {
		return err
	}
	policyVersion, err := requiredConfiguration(getenv, coreRunPolicyVersionEnvironment)
	if err != nil {
		return err
	}
	policyResolver, err := coreserver.NewStaticUserRunPolicyResolver(policyVersion, commaSeparatedTools(getenv(coreRunAllowedToolsEnvironment)))
	if err != nil {
		return fmt.Errorf("configure user run policy: %w", err)
	}
	cursorCodec, err := runcursor.NewCodec(cursorKey)
	if err != nil {
		return err
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
	browserAuthorizer, err := coreserver.NewSPIFFEWorkloadAuthorizer(browserIdentity)
	if err != nil {
		return err
	}
	hydraIntrospector, err := coreserver.NewHydraUserIntrospector(hydraEndpoint, &http.Client{Timeout: 5 * time.Second}, allowInsecureHydra)
	if err != nil {
		return err
	}
	hydraAdmin, err := coreserver.NewHydraAdminClient(hydraAdminOrigin, &http.Client{Timeout: 5 * time.Second}, allowInsecureHydra)
	if err != nil {
		return err
	}
	externalOIDC, err := coreserver.NewDiscoveredExternalOIDCProvider(
		ctx, externalOIDCIssuer, externalOIDCClientID, externalOIDCClientSecret,
		externalOIDCRedirectURL, &http.Client{Timeout: 5 * time.Second}, allowInsecureExternalOIDC,
	)
	if err != nil {
		return err
	}
	loginSealer, err := coreserver.NewLoginTransactionSealer(loginTransactionKey)
	if err != nil {
		return err
	}
	userAuthorizer, err := coreserver.NewIntrospectedUserAuthorizer(coreserver.IntrospectedUserAuthorizerConfig{
		Introspector: hydraIntrospector, ExpectedAudience: "agentserver-api",
		ActionScopes: map[string]string{
			"runs.create": "runs:write", "runs.cancel": "runs:write", "runs.events.read": "runs:write",
			"approvals.decide": "runs:write",
		},
	})
	if err != nil {
		return err
	}
	store := coredb.NewStateStore(pool)
	loginBridge, err := coreserver.NewLoginBridge(coreserver.LoginBridgeConfig{
		Store: store, Hydra: hydraAdmin, IdentityProvider: externalOIDC, Sealer: loginSealer,
		HydraBrowserClientID: hydraBrowserClientID, HydraPublicOrigin: hydraPublicOrigin,
	})
	if err != nil {
		return err
	}
	loginBridgeHandler, err := coreserver.NewLoginBridgeHandler(browserAuthorizer, loginBridge)
	if err != nil {
		return err
	}
	userRunService, err := coreserver.NewUserRunService(coreserver.UserRunServiceConfig{
		Store: store, Prompts: promptStore, Policies: policyResolver, CursorCodec: cursorCodec,
	})
	if err != nil {
		return err
	}
	userRunHandler, err := coreserver.NewUserRunHandler(browserAuthorizer, userAuthorizer, userRunService)
	if err != nil {
		return err
	}
	userApprovalService, err := coreserver.NewUserApprovalService(coreserver.UserApprovalServiceConfig{Store: store})
	if err != nil {
		return err
	}
	userApprovalHandler, err := coreserver.NewUserApprovalHandler(browserAuthorizer, userAuthorizer, userApprovalService)
	if err != nil {
		return err
	}
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
	approvalHandler, err := coreserver.NewApprovalHandler(authorizer, coreserver.StateStoreApprovalCommands{Store: store})
	if err != nil {
		return err
	}
	approvalObservationHandler, err := coreserver.NewApprovalObservationHandler(
		harnessPoolAuthorizer,
		coreserver.StateStoreApprovalCommands{Store: store},
	)
	if err != nil {
		return err
	}
	approvalActionHandler, err := coreserver.NewInternalApprovalActionHandler(approvalHandler, approvalObservationHandler)
	if err != nil {
		return err
	}
	runAttemptHandler, err := coreserver.NewRunAttemptHandler(harnessPoolAuthorizer, coreserver.StateStoreRunAttemptCommands{Store: store})
	if err != nil {
		return err
	}
	runLaunchStateHandler, err := coreserver.NewRunLaunchStateHandler(harnessPoolAuthorizer, coreserver.StateStoreRunLaunchStateQueries{Store: store})
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
	handler.Handle("/internal/v2/auth/", loginBridgeHandler.Routes())
	handler.Handle("/v2/", userRunHandler.Routes())
	handler.Handle(corecontract.DecideUserApprovalRoutePattern, userApprovalHandler)
	handler.Handle(corecontract.FreezeBrainToolCatalogPath, brainToolCatalogHandler)
	handler.Handle(corecontract.BrainToolCatalogPathPrefix, brainToolCatalogHandler)
	handler.Handle(corecontract.ClaimRunDispatchesPath, runDispatchHandler)
	handler.Handle(corecontract.RunDispatchPathPrefix, runDispatchHandler)
	handler.Handle(corecontract.ClaimRunAttemptPath, runAttemptHandler)
	handler.Handle(corecontract.RunAttemptPathPrefix, runAttemptHandler)
	handler.Handle(corecontract.ResolveRunLaunchStatePath, runLaunchStateHandler)
	handler.Handle(corecontract.ListExecutorEnvironmentsPath, environmentHandler)
	handler.Handle(corecontract.PrepareExecutionPath, executionHandler)
	handler.Handle(corecontract.ExecutionPathPrefix, executionHandler)
	handler.Handle(corecontract.CreateApprovalPath, approvalHandler)
	handler.Handle(corecontract.ApprovalActionRoutePattern, approvalActionHandler)
	handler.Handle(corecontract.ApprovalPathPrefix, approvalHandler)
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
		WriteTimeout:      40 * time.Second,
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
	fmt.Fprintf(stdout, "agentserver-core serve: listening with mTLS on %s; %s\n", listener.Addr(), objectStoreDescription)
	err = server.Serve(tls.NewListener(listener, tlsConfig))
	if errors.Is(err, http.ErrServerClosed) && ctx.Err() != nil {
		return nil
	}
	return err
}

func configureCorePromptStore(
	ctx context.Context,
	getenv func(string) string,
	mode coreServeMode,
) (coreserver.UserPromptStore, string, error) {
	switch mode {
	case coreServeProduction:
		config, err := objectruntime.ParseEnvironment(getenv)
		if err != nil {
			return nil, "", fmt.Errorf("configure production object routing: %w", err)
		}
		objects, err := objectruntime.Open(ctx, config)
		if err != nil {
			return nil, "", err
		}
		prompts, err := coreserver.NewEncryptedUserPromptStore(objects)
		if err != nil {
			return nil, "", err
		}
		return prompts, "encrypted S3/KMS object store", nil
	case coreServeInsecureDevelopment:
		promptObjectRoot, err := requiredConfiguration(getenv, coreDevPromptObjectRootEnvironment)
		if err != nil {
			return nil, "", err
		}
		prompts, err := coreserver.NewLocalUserPromptStore(promptObjectRoot)
		if err != nil {
			return nil, "", fmt.Errorf("configure insecure-development prompt object store: %w", err)
		}
		return prompts, "INSECURE DEV plaintext object store", nil
	default:
		return nil, "", errors.New("Core serve mode is invalid")
	}
}

func decodeRunCursorKey(encoded string) ([]byte, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(decoded) != 32 || base64.RawURLEncoding.EncodeToString(decoded) != encoded {
		return nil, fmt.Errorf("%s must be a canonical unpadded base64url 256-bit key", coreRunCursorKeyEnvironment)
	}
	return decoded, nil
}

func decodeLoginTransactionKey(encoded string) ([]byte, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(decoded) != 32 || base64.RawURLEncoding.EncodeToString(decoded) != encoded {
		return nil, fmt.Errorf("%s must be a canonical unpadded base64url 256-bit key", coreLoginTransactionKeyEnvironment)
	}
	return decoded, nil
}

func strictOptionalBoolean(value, name string) (bool, error) {
	switch strings.TrimSpace(value) {
	case "", "false":
		return false, nil
	case "true":
		return true, nil
	default:
		return false, fmt.Errorf("%s must be true or false", name)
	}
}

func commaSeparatedTools(value string) []string {
	if value == "" {
		return nil
	}
	return strings.Split(value, ",")
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
