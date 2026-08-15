package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/corecredentials"
	"github.com/agentserver/agentserver/v2/internal/coredb"
	"github.com/agentserver/agentserver/v2/internal/coreserver"
	"github.com/agentserver/agentserver/v2/internal/egresscapability"
	"github.com/agentserver/agentserver/v2/internal/egressgateway"
	"github.com/agentserver/agentserver/v2/internal/enrollmenttoken"
	"github.com/agentserver/agentserver/v2/internal/httperrorlog"
	"github.com/agentserver/agentserver/v2/internal/managedsandboxprofile"
	"github.com/agentserver/agentserver/v2/internal/objectruntime"
	"github.com/agentserver/agentserver/v2/internal/publichttps"
	"github.com/agentserver/agentserver/v2/internal/runcapability"
	"github.com/agentserver/agentserver/v2/internal/runcursor"
	"github.com/agentserver/agentserver/v2/internal/trajectorycursor"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	coreListenAddressEnvironment            = "AGENTSERVER_V2_CORE_LISTEN_ADDR"
	coreTLSCertificateEnvironment           = "AGENTSERVER_V2_CORE_TLS_CERT_FILE"
	coreTLSKeyEnvironment                   = "AGENTSERVER_V2_CORE_TLS_KEY_FILE"
	coreClientCAEnvironment                 = "AGENTSERVER_V2_CORE_CLIENT_CA_FILE"
	coreGatewayIdentityEnvironment          = "AGENTSERVER_V2_EXECUTOR_GATEWAY_SPIFFE_ID"
	coreHarnessPoolIdentityEnvironment      = "AGENTSERVER_V2_HARNESS_POOL_SPIFFE_ID"
	coreSandboxGatewayIdentityEnvironment   = "AGENTSERVER_V2_SANDBOX_GATEWAY_SPIFFE_ID"
	coreSandboxGatewayIdentitiesEnvironment = "AGENTSERVER_V2_SANDBOX_GATEWAY_SPIFFE_IDS"
	coreEgressAuthorizerIdentityEnvironment = "AGENTSERVER_V2_EGRESS_AUTHORIZER_SPIFFE_ID"
	coreBrowserIdentityEnvironment          = "AGENTSERVER_V2_BROWSER_GATEWAY_SPIFFE_ID"
	corePlatformIdentityEnvironment         = "AGENTSERVER_V2_PLATFORM_GATEWAY_SPIFFE_ID"
	coreHydraIntrospectionEnvironment       = "AGENTSERVER_V2_HYDRA_INTROSPECTION_URL"
	coreHydraAdminEnvironment               = "AGENTSERVER_V2_HYDRA_ADMIN_URL"
	coreHydraPublicOriginEnvironment        = "AGENTSERVER_V2_HYDRA_PUBLIC_ORIGIN"
	coreHydraIssuerEnvironment              = "AGENTSERVER_V2_HYDRA_ISSUER"
	coreHydraPlatformClientEnvironment      = "AGENTSERVER_V2_HYDRA_PLATFORM_CLIENT_ID"
	coreHydraBrowserClientEnvironment       = "AGENTSERVER_V2_HYDRA_BROWSER_CLIENT_ID"
	coreHydraCAEnvironment                  = "AGENTSERVER_V2_HYDRA_CA_FILE"
	coreHydraServerNameEnvironment          = "AGENTSERVER_V2_HYDRA_SERVER_NAME"
	coreHydraInsecureHTTPEnvironment        = "AGENTSERVER_V2_HYDRA_ALLOW_INSECURE_HTTP"
	coreExternalOIDCIssuerEnvironment       = "AGENTSERVER_V2_EXTERNAL_OIDC_ISSUER"
	coreExternalOIDCSubjectEnvironment      = "AGENTSERVER_V2_EXTERNAL_OIDC_SUBJECT"
	coreExternalOIDCClientEnvironment       = "AGENTSERVER_V2_EXTERNAL_OIDC_CLIENT_ID"
	coreExternalOIDCSecretEnvironment       = "AGENTSERVER_V2_EXTERNAL_OIDC_CLIENT_SECRET"
	coreExternalOIDCRedirectEnvironment     = "AGENTSERVER_V2_EXTERNAL_OIDC_REDIRECT_URL"
	coreExternalOIDCInsecureEnvironment     = "AGENTSERVER_V2_EXTERNAL_OIDC_ALLOW_INSECURE_HTTP"
	coreLoginTransactionKeyEnvironment      = "AGENTSERVER_V2_LOGIN_TRANSACTION_KEY"
	coreRunCursorKeyEnvironment             = "AGENTSERVER_V2_RUN_CURSOR_KEY"
	coreDevPromptObjectRootEnvironment      = "AGENTSERVER_V2_DEV_PROMPT_OBJECT_DIR"
	coreRunPolicyVersionEnvironment         = "AGENTSERVER_V2_RUN_POLICY_VERSION"
	coreRunAllowedToolsEnvironment          = "AGENTSERVER_V2_RUN_ALLOWED_TOOLS"
	coreLLMProxyIdentityEnvironment         = "AGENTSERVER_V2_LLMPROXY_SPIFFE_ID"
	coreCapabilityIssuerEnvironment         = "AGENTSERVER_V2_RUN_CAPABILITY_ISSUER"
	coreCapabilityKeyIDEnvironment          = "AGENTSERVER_V2_RUN_CAPABILITY_SIGNING_KEY_ID"
	coreCapabilityPrivateKeyEnvironment     = "AGENTSERVER_V2_RUN_CAPABILITY_SIGNING_KEY_FILE"
	coreCapabilityKeyringEnvironment        = "AGENTSERVER_V2_RUN_CAPABILITY_KEYRING_FILE"
	coreProductionExecutorEnvironment       = "AGENTSERVER_V2_EXECUTOR_ID"
	coreLLMGatewaySealingKeyringEnvironment = "AGENTSERVER_V2_LLM_GATEWAY_SEALING_KEYRING_FILE"
	coreLLMGatewayRedirectURLEnvironment    = "AGENTSERVER_V2_LLM_GATEWAY_REDIRECT_URL"
	coreMaxRunDurationEnvironment           = "AGENTSERVER_V2_MAX_RUN_DURATION"
	coreMaxApprovalTTLEnvironment           = "AGENTSERVER_V2_MAX_APPROVAL_TTL"
	coreCapabilityExpiryGraceEnvironment    = "AGENTSERVER_V2_RUN_CAPABILITY_EXPIRY_GRACE"
	coreEnrollmentKeyEnvironment            = "AGENTSERVER_V2_EXECUTOR_ENROLLMENT_TOKEN_KEY_FILE"
	coreEnrollmentTTLEnvironment            = "AGENTSERVER_V2_EXECUTOR_ENROLLMENT_TOKEN_TTL"
	coreManagedExecutorEnabledEnvironment   = "AGENTSERVER_V2_MANAGED_EXECUTOR_ENABLED"
	coreTAEWebhookRequiredEnvironment       = "AGENTSERVER_V2_TAE_POLICY_WEBHOOK_REQUIRED"
	coreEgressPlaceholderKeyringEnvironment = "AGENTSERVER_V2_EGRESS_PLACEHOLDER_KEYRING_FILE"
	coreCredentialSealingKeyringEnvironment = "AGENTSERVER_V2_CREDENTIAL_SEALING_KEYRING_FILE"
	coreManagedTAEPSMEnvironment            = "AGENTSERVER_V2_MANAGED_TAE_PSM"
	coreManagedSandboxProfilesEnvironment   = "AGENTSERVER_V2_MANAGED_SANDBOX_PROFILE_CATALOG"
	coreLarkDeviceAppIDEnvironment          = "AGENTSERVER_V2_LARK_DEVICE_APP_ID"
	coreLarkDeviceAppSecretEnvironment      = "AGENTSERVER_V2_LARK_DEVICE_APP_SECRET"
	coreLarkDeviceScopesEnvironment         = "AGENTSERVER_V2_LARK_DEVICE_SCOPES"
	coreByteCloudDeviceAPIEnvironment       = "AGENTSERVER_V2_BYTECLOUD_DEVICE_API_BASE_URL"
)

type coreProductionRunCapabilityConfig struct {
	signer   *runcapability.ProductionSigner
	verifier *runcapability.ProductionVerifier
	policy   coreserver.ProductionRunCapabilityPolicy
}

type coreProductionEnrollmentConfig struct {
	tokens *enrollmenttoken.Codec
	ttl    time.Duration
}

func workspaceCredentialControlPlaneEnabled(mode coreServeMode, managedExecutorEnabled bool) bool {
	return mode == coreServeProduction || managedExecutorEnabled
}

func configureCoreManagedSandboxProfiles(
	getenv func(string) string,
	managedExecutorEnabled bool,
) (*managedsandboxprofile.Catalog, error) {
	if getenv == nil {
		return nil, errors.New("managed sandbox profile configuration source is required")
	}
	raw := strings.TrimSpace(getenv(coreManagedSandboxProfilesEnvironment))
	if !managedExecutorEnabled {
		if raw != "" {
			return nil, errors.New("managed sandbox profile catalog requires the managed executor")
		}
		return nil, nil
	}
	if raw == "" {
		return nil, fmt.Errorf("%s is required with the managed executor", coreManagedSandboxProfilesEnvironment)
	}
	catalog, err := managedsandboxprofile.ParseCatalog([]byte(raw))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", coreManagedSandboxProfilesEnvironment, err)
	}
	return catalog, nil
}

func serveCore(ctx context.Context, getenv func(string) string, stdout, stderr io.Writer, mode coreServeMode) error {
	if mode != coreServeProduction && mode != coreServeInsecureDevelopment {
		return errors.New("Core serve mode is invalid")
	}
	logger := slog.New(slog.NewJSONHandler(stderr, nil))
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
	managedExecutorEnabled, err := strictOptionalBoolean(getenv(coreManagedExecutorEnabledEnvironment), coreManagedExecutorEnabledEnvironment)
	if err != nil {
		return err
	}
	if mode == coreServeProduction && strings.TrimSpace(getenv(coreManagedExecutorEnabledEnvironment)) == "" {
		return fmt.Errorf("%s is required in production", coreManagedExecutorEnabledEnvironment)
	}
	webhookRequired := false
	if managedExecutorEnabled {
		webhookRequired, err = strictOptionalBoolean(getenv(coreTAEWebhookRequiredEnvironment), coreTAEWebhookRequiredEnvironment)
		if err != nil {
			return err
		}
		if strings.TrimSpace(getenv(coreTAEWebhookRequiredEnvironment)) == "" {
			return fmt.Errorf("%s is required with the managed executor", coreTAEWebhookRequiredEnvironment)
		}
	} else if strings.TrimSpace(getenv(coreTAEWebhookRequiredEnvironment)) != "" {
		return errors.New("TAE webhook profile requires the managed executor")
	}
	if managedExecutorEnabled && !webhookRequired {
		for _, name := range []string{coreEgressAuthorizerIdentityEnvironment, coreEgressPlaceholderKeyringEnvironment} {
			if strings.TrimSpace(getenv(name)) != "" {
				return fmt.Errorf("%s must be unset for a direct TAE Sandbox profile", name)
			}
		}
	}
	managedTAEPSM := ""
	if managedExecutorEnabled {
		managedTAEPSM, err = requiredConfiguration(getenv, coreManagedTAEPSMEnvironment)
		if err != nil {
			return err
		}
		if len(managedTAEPSM) > 256 || strings.ContainsAny(managedTAEPSM, "\x00\r\n") {
			return fmt.Errorf("%s is invalid", coreManagedTAEPSMEnvironment)
		}
	} else if strings.TrimSpace(getenv(coreManagedTAEPSMEnvironment)) != "" {
		return errors.New("managed TAE PSM requires the managed executor")
	}
	var sandboxGatewayIdentities []string
	if managedExecutorEnabled {
		sandboxGatewayIdentities, err = loadSandboxGatewayIdentities(getenv)
		if err != nil {
			return err
		}
		if slices.Contains(sandboxGatewayIdentities, gatewayIdentity) ||
			slices.Contains(sandboxGatewayIdentities, harnessPoolIdentity) {
			return errors.New("sandbox-gateway must have a distinct production SPIFFE identity")
		}
	} else if strings.TrimSpace(getenv(coreSandboxGatewayIdentityEnvironment)) != "" ||
		strings.TrimSpace(getenv(coreSandboxGatewayIdentitiesEnvironment)) != "" {
		return errors.New("sandbox-gateway identity requires the managed executor")
	}
	browserIdentity, err := requiredConfiguration(getenv, coreBrowserIdentityEnvironment)
	if err != nil {
		return err
	}
	if browserIdentity == gatewayIdentity || browserIdentity == harnessPoolIdentity ||
		(managedExecutorEnabled && slices.Contains(sandboxGatewayIdentities, browserIdentity)) {
		if mode == coreServeProduction {
			return errors.New("browser-gateway, sandbox-gateway, executor-gateway, and harness-pool SPIFFE identities must be distinct")
		}
		return errors.New("browser-gateway, executor-gateway, and harness-pool SPIFFE identities must be distinct")
	}
	platformIdentity := browserIdentity
	if mode == coreServeProduction {
		platformIdentity, err = requiredConfiguration(getenv, corePlatformIdentityEnvironment)
		if err != nil {
			return err
		}
		identities := []string{gatewayIdentity, harnessPoolIdentity, browserIdentity}
		if managedExecutorEnabled {
			identities = append(identities, sandboxGatewayIdentities...)
		}
		if slices.Contains(identities, platformIdentity) {
			return errors.New("platform-gateway, browser-gateway, sandbox-gateway, executor-gateway, and harness-pool SPIFFE identities must be distinct")
		}
	}
	var egressAuthorizerIdentity string
	if webhookRequired && mode != coreServeProduction {
		egressAuthorizerIdentity, err = requiredConfiguration(getenv, coreEgressAuthorizerIdentityEnvironment)
		if err != nil {
			return err
		}
	}
	var llmproxyIdentity string
	if mode == coreServeProduction {
		llmproxyIdentity, err = requiredConfiguration(getenv, coreLLMProxyIdentityEnvironment)
		if err != nil {
			return err
		}
		identities := []string{gatewayIdentity, harnessPoolIdentity, browserIdentity, platformIdentity}
		if managedExecutorEnabled {
			identities = append(identities, sandboxGatewayIdentities...)
		}
		if slices.Contains(identities, llmproxyIdentity) {
			return errors.New("llmproxy, platform-gateway, browser-gateway, sandbox-gateway, executor-gateway, and harness-pool SPIFFE identities must be distinct")
		}
		if webhookRequired {
			egressAuthorizerIdentity, err = requiredConfiguration(getenv, coreEgressAuthorizerIdentityEnvironment)
			if err != nil {
				return err
			}
			identities := []string{gatewayIdentity, harnessPoolIdentity, browserIdentity, platformIdentity, llmproxyIdentity}
			identities = append(identities, sandboxGatewayIdentities...)
			if slices.Contains(identities, egressAuthorizerIdentity) {
				return errors.New("egress-authorizer must have a distinct production SPIFFE identity")
			}
		}
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
	hydraIssuer, err := requiredConfiguration(getenv, coreHydraIssuerEnvironment)
	if err != nil {
		return err
	}
	hydraPlatformClientID, err := requiredConfiguration(getenv, coreHydraPlatformClientEnvironment)
	if err != nil {
		return err
	}
	if hydraPlatformClientID != corecontract.PlatformOAuthClientID {
		return fmt.Errorf("%s must be exactly %q", coreHydraPlatformClientEnvironment, corecontract.PlatformOAuthClientID)
	}
	hydraBrowserClientID, err := requiredConfiguration(getenv, coreHydraBrowserClientEnvironment)
	if err != nil {
		return err
	}
	if hydraBrowserClientID != corecontract.BrowserOAuthClientID {
		return fmt.Errorf("%s must be exactly %q", coreHydraBrowserClientEnvironment, corecontract.BrowserOAuthClientID)
	}
	allowInsecureHydra, err := strictOptionalBoolean(getenv(coreHydraInsecureHTTPEnvironment), coreHydraInsecureHTTPEnvironment)
	if err != nil {
		return err
	}
	var hydraCAFile, hydraServerName string
	if mode == coreServeProduction {
		if allowInsecureHydra {
			return errors.New("production Core forbids cleartext Hydra access")
		}
		hydraCAFile, err = requiredConfiguration(getenv, coreHydraCAEnvironment)
		if err != nil {
			return err
		}
		hydraServerName, err = requiredConfiguration(getenv, coreHydraServerNameEnvironment)
		if err != nil {
			return err
		}
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
	productionCapabilities, err := configureCoreProductionRunCapabilities(getenv, mode)
	if err != nil {
		return err
	}
	productionEnrollment, err := configureCoreProductionEnrollment(getenv, mode, productionCapabilities)
	if err != nil {
		return err
	}
	promptStore, promptReader, objectStoreDescription, err := configureCorePromptStore(ctx, getenv, mode)
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
	trajectoryCursorCodec, err := trajectorycursor.NewCodec(cursorKey)
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
	var sandboxGatewayAuthorizer coreserver.WorkloadAuthorizer
	if managedExecutorEnabled {
		sandboxGatewayAuthorizer, err = coreserver.NewSPIFFEWorkloadAuthorizer(sandboxGatewayIdentities...)
		if err != nil {
			return err
		}
	}
	browserAuthorizer, err := coreserver.NewSPIFFEWorkloadAuthorizer(browserIdentity)
	if err != nil {
		return err
	}
	platformAuthorizer, err := coreserver.NewSPIFFEWorkloadAuthorizer(platformIdentity)
	if err != nil {
		return err
	}
	var llmproxyAuthorizer coreserver.WorkloadAuthorizer
	var egressAuthorizer coreserver.WorkloadAuthorizer
	if productionCapabilities != nil {
		llmproxyAuthorizer, err = coreserver.NewSPIFFEWorkloadAuthorizer(llmproxyIdentity)
		if err != nil {
			return err
		}
	}
	if webhookRequired {
		egressAuthorizer, err = coreserver.NewSPIFFEWorkloadAuthorizer(egressAuthorizerIdentity)
		if err != nil {
			return err
		}
	}
	hydraHTTPClient := &http.Client{Timeout: 5 * time.Second}
	if mode == coreServeProduction {
		hydraHTTPClient, err = newCoreHydraHTTPClient(hydraCAFile, hydraServerName)
		if err != nil {
			return err
		}
		defer hydraHTTPClient.CloseIdleConnections()
	}
	hydraIntrospector, err := coreserver.NewHydraUserIntrospector(hydraEndpoint, hydraHTTPClient, allowInsecureHydra)
	if err != nil {
		return err
	}
	hydraAdmin, err := coreserver.NewHydraAdminClient(hydraAdminOrigin, hydraHTTPClient, allowInsecureHydra)
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
	browserUserAuthorizer, err := coreserver.NewIntrospectedUserAuthorizer(coreserver.IntrospectedUserAuthorizerConfig{
		Introspector: hydraIntrospector, ExpectedIssuer: hydraIssuer,
		ExpectedClientID: corecontract.BrowserOAuthClientID, ExpectedAudience: corecontract.BrowserOAuthAudience,
		ExpectedAuthority: corecontract.UserOAuthBrowserAuthority, AllowedScopes: corecontract.BrowserOAuthScopes(),
		ActionPermissions: corecontract.BrowserOAuthActionPermissions(),
	})
	if err != nil {
		return err
	}
	platformUserAuthorizer, err := coreserver.NewIntrospectedUserAuthorizer(coreserver.IntrospectedUserAuthorizerConfig{
		Introspector: hydraIntrospector, ExpectedIssuer: hydraIssuer,
		ExpectedClientID: corecontract.PlatformOAuthClientID, ExpectedAudience: corecontract.PlatformOAuthAudience,
		ExpectedAuthority: corecontract.UserOAuthPlatformAuthority, AllowedScopes: corecontract.PlatformOAuthScopes(),
		ActionPermissions: corecontract.PlatformOAuthActionPermissions(),
	})
	if err != nil {
		return err
	}
	store := coredb.NewStateStore(pool)
	var workspaceCredentialHandler *coreserver.WorkspaceCredentialHandler
	var workspaceCredentialAuthorizationHandler *coreserver.WorkspaceCredentialAuthorizationHandler
	var egressCredentialHandler *coreserver.EgressCredentialHandler
	var executionCredentialHandler *coreserver.ExecutionCredentialHandler
	var credentialRegistry *corecredentials.ProviderRegistry
	var credentialSealer *corecredentials.Keyring
	// Workspace credential configuration is a production control-plane feature:
	// owners must be able to authorize providers before managed execution is
	// activated. The managed-executor flag remains the kill switch for runtime
	// resolution and sandbox authority only.
	if workspaceCredentialControlPlaneEnabled(mode, managedExecutorEnabled) {
		sealingKeyringFile, sealingErr := requiredConfiguration(getenv, coreCredentialSealingKeyringEnvironment)
		if sealingErr != nil {
			return sealingErr
		}
		sealer, loadErr := corecredentials.LoadKeyring(sealingKeyringFile)
		if loadErr != nil {
			return fmt.Errorf("configure v2 workspace credential sealing keyring: %w", loadErr)
		}
		credentialSealer = sealer
		publicProviderHTTPClient, clientErr := publichttps.NewClient(publichttps.ClientConfig{
			Timeout: 25 * time.Second, ResponseHeaderTimeout: 15 * time.Second,
			MaxIdleConns: 32, MaxIdleConnsPerHost: 8,
		})
		if clientErr != nil {
			return fmt.Errorf("configure v2 credential provider HTTPS: %w", clientErr)
		}
		defer publicProviderHTTPClient.CloseIdleConnections()
		byteCloudDeviceAPI, byteCloudConfigErr := configureCoreByteCloudDeviceAPI(getenv, mode)
		if byteCloudConfigErr != nil {
			return byteCloudConfigErr
		}
		byteCloudProviderHTTPClient := newCoreByteCloudDeviceHTTPClient()
		defer byteCloudProviderHTTPClient.CloseIdleConnections()
		registryConfig := corecredentials.DefaultRegistryConfig{
			ByteCloudDeviceFlow: &corecredentials.ByteCloudDeviceFlowConfig{
				APIBaseURL: byteCloudDeviceAPI,
				HTTPClient: byteCloudProviderHTTPClient, Now: time.Now,
			},
		}
		larkAppID, larkAppSecret, larkConfigErr := configureCoreLarkDeviceApplication(
			getenv, mode == coreServeProduction,
		)
		if larkConfigErr != nil {
			return larkConfigErr
		}
		if larkAppID != "" {
			registryConfig.LarkDeviceFlow = &corecredentials.LarkDeviceFlowConfig{
				AppID: larkAppID, AppSecret: larkAppSecret,
				Scopes:     strings.TrimSpace(getenv(coreLarkDeviceScopesEnvironment)),
				HTTPClient: publicProviderHTTPClient, Now: time.Now,
			}
		}
		registry, registryErr := corecredentials.NewConfiguredRegistry(registryConfig)
		if registryErr != nil {
			return fmt.Errorf("configure v2 workspace credential providers: %w", registryErr)
		}
		credentialRegistry = registry
		credentialCommands := coreserver.StateStoreWorkspaceCredentialCommands{
			Store: store, Registry: registry, Sealer: sealer, Now: time.Now, Logger: logger,
		}
		workspaceCredentialHandler, err = coreserver.NewWorkspaceCredentialHandler(platformAuthorizer, platformUserAuthorizer, credentialCommands)
		if err != nil {
			return err
		}
		workspaceCredentialAuthorizationHandler, err = coreserver.NewWorkspaceCredentialAuthorizationHandler(platformAuthorizer, platformUserAuthorizer, credentialCommands)
		if err != nil {
			return err
		}
	}
	if managedExecutorEnabled {
		placeholderKeyringFile, keyringErr := requiredConfiguration(getenv, coreEgressPlaceholderKeyringEnvironment)
		if keyringErr != nil && webhookRequired {
			return keyringErr
		}
		var placeholderVerifier *egresscapability.Verifier
		var capabilityVerifier corecredentials.PlaceholderVerifier
		if webhookRequired {
			var loadErr error
			placeholderVerifier, loadErr = egresscapability.LoadVerifier(placeholderKeyringFile)
			if loadErr != nil {
				return fmt.Errorf("configure v2 egress placeholder verifier: %w", loadErr)
			}
			capabilityVerifier, loadErr = egressgateway.NewCapabilityPlaceholderVerifier(placeholderVerifier)
			if loadErr != nil {
				return loadErr
			}
		}
		credentialRefresher, refreshErr := coreserver.NewWorkspaceCredentialRefresher(
			store, credentialRegistry, credentialSealer, time.Now,
		)
		if refreshErr != nil {
			return fmt.Errorf("configure v2 workspace credential refresh: %w", refreshErr)
		}
		egressCredentialService, serviceErr := coreserver.NewEgressCredentialService(coreserver.EgressCredentialServiceConfig{
			Store: store, Registry: credentialRegistry, Sealer: credentialSealer, Placeholders: capabilityVerifier,
			ProcessProofs: placeholderVerifier, ProcessEnvironmentTAEPSM: managedTAEPSM, Now: time.Now,
			CredentialRefresher: credentialRefresher,
		})
		if serviceErr != nil {
			return fmt.Errorf("configure v2 egress credential resolver: %w", serviceErr)
		}
		if webhookRequired {
			egressCredentialHandler, err = coreserver.NewEgressCredentialHandler(egressAuthorizer, egressCredentialService)
		}
		if err == nil {
			executionCredentialHandler, err = coreserver.NewExecutionCredentialHandler(authorizer, egressCredentialService)
		}
		if err != nil {
			return err
		}
	}
	managedSandboxProfiles, err := configureCoreManagedSandboxProfiles(getenv, managedExecutorEnabled)
	if err != nil {
		return err
	}
	availableManagedSandboxRegions := []string(nil)
	if managedSandboxProfiles != nil {
		for _, binding := range managedSandboxProfiles.Bindings() {
			availableManagedSandboxRegions = append(availableManagedSandboxRegions, binding.Region)
		}
	}
	platformResourceHandler, err := coreserver.NewPlatformResourceHandler(
		platformAuthorizer,
		platformUserAuthorizer,
		coreserver.StateStorePlatformResourceCommands{
			Store: store, AvailableManagedSandboxRegions: availableManagedSandboxRegions,
		},
	)
	if err != nil {
		return err
	}
	var llmGatewayService *coreserver.WorkspaceLLMGatewayService
	if productionCapabilities != nil {
		sealingKeyring, err := requiredConfiguration(getenv, coreLLMGatewaySealingKeyringEnvironment)
		if err != nil {
			return err
		}
		redirectURL, err := requiredConfiguration(getenv, coreLLMGatewayRedirectURLEnvironment)
		if err != nil {
			return err
		}
		sealer, err := coreserver.LoadLLMGatewayGrantSealer(sealingKeyring)
		if err != nil {
			return fmt.Errorf("configure workspace LLM gateway token sealing: %w", err)
		}
		gatewayHTTPClient, err := publichttps.NewClient(publichttps.ClientConfig{
			Timeout: 30 * time.Second, ResponseHeaderTimeout: 20 * time.Second,
			MaxIdleConns: 32, MaxIdleConnsPerHost: 8,
		})
		if err != nil {
			return fmt.Errorf("configure workspace LLM gateway outbound HTTPS: %w", err)
		}
		defer gatewayHTTPClient.CloseIdleConnections()
		providers, err := coreserver.NewDiscoveredWorkspaceLLMGatewayOIDCFactory(gatewayHTTPClient)
		if err != nil {
			return fmt.Errorf("configure workspace LLM gateway OIDC discovery: %w", err)
		}
		llmGatewayService, err = coreserver.NewWorkspaceLLMGatewayService(coreserver.WorkspaceLLMGatewayServiceConfig{
			Store: store, Sealer: sealer, Providers: providers, RedirectURL: redirectURL, Logger: logger,
		})
		if err != nil {
			return fmt.Errorf("configure workspace LLM gateway service: %w", err)
		}
	}
	// Managed TAE credentials use the provider-neutral egress credential
	// resolver above. The legacy Lark grant/refresh HTTP surface is deliberately
	// not mounted in the v2 production Core, so a deployment never needs a
	// workspace token, client secret, or install-time grant Secret.
	var userExecutorHandler *coreserver.UserExecutorManagementHandler
	var internalExecutorIdentityHandler *coreserver.InternalExecutorIdentityHandler
	if productionEnrollment != nil {
		executorEnrollmentService, err := coreserver.NewExecutorEnrollmentService(coreserver.ExecutorEnrollmentServiceConfig{
			Store: store, Tokens: productionEnrollment.tokens, Hydra: hydraAdmin, TokenTTL: productionEnrollment.ttl,
		})
		if err != nil {
			return fmt.Errorf("configure executor enrollment service: %w", err)
		}
		executorOAuthAuthorizer, err := coreserver.NewExecutorOAuthAuthorizer(coreserver.ExecutorOAuthAuthorizerConfig{
			Introspector: hydraIntrospector, Store: store, Hydra: hydraAdmin, ExpectedIssuer: hydraIssuer,
		})
		if err != nil {
			return fmt.Errorf("configure executor OAuth authorizer: %w", err)
		}
		userExecutorHandler, err = coreserver.NewUserExecutorManagementHandler(platformAuthorizer, platformUserAuthorizer, executorEnrollmentService)
		if err != nil {
			return err
		}
		internalExecutorIdentityHandler, err = coreserver.NewInternalExecutorIdentityHandler(authorizer, executorEnrollmentService, executorOAuthAuthorizer)
		if err != nil {
			return err
		}
	}
	var runCapabilityHandler *coreserver.RunCapabilityHandler
	if productionCapabilities != nil {
		runCapabilityService, err := coreserver.NewProductionRunCapabilityService(coreserver.ProductionRunCapabilityServiceConfig{
			Store: store, Signer: productionCapabilities.signer,
			Verifier: productionCapabilities.verifier, Policy: productionCapabilities.policy,
			LLMGatewayResolver: llmGatewayService, Logger: logger,
		})
		if err != nil {
			return fmt.Errorf("configure production run capability authority: %w", err)
		}
		runCapabilityHandler, err = coreserver.NewRunCapabilityHandler(
			harnessPoolAuthorizer, authorizer, llmproxyAuthorizer, runCapabilityService,
		)
		if err != nil {
			return err
		}
	}
	loginBridge, err := coreserver.NewLoginBridge(coreserver.LoginBridgeConfig{
		Store: store, Hydra: hydraAdmin, IdentityProvider: externalOIDC, Sealer: loginSealer,
		OAuthProfiles: []coreserver.LoginBridgeOAuthProfile{
			{Authority: corecontract.UserOAuthPlatformAuthority, ClientID: hydraPlatformClientID, Scopes: corecontract.PlatformOAuthScopes(), Audience: []string{corecontract.PlatformOAuthAudience}},
			{Authority: corecontract.UserOAuthBrowserAuthority, ClientID: hydraBrowserClientID, Scopes: corecontract.BrowserOAuthScopes(), Audience: []string{corecontract.BrowserOAuthAudience}},
		},
		HydraPublicOrigin: hydraPublicOrigin,
	})
	if err != nil {
		return err
	}
	loginBridgeHandler, err := coreserver.NewLoginBridgeHandler(platformAuthorizer, loginBridge)
	if err != nil {
		return err
	}
	userRunService, err := coreserver.NewUserRunService(coreserver.UserRunServiceConfig{
		Store: store, Prompts: promptStore, Policies: policyResolver, CursorCodec: cursorCodec,
		LLMGateways: func() coreserver.UserRunLLMGatewayResolver {
			if productionCapabilities != nil {
				return store
			}
			return nil
		}(),
		ManagedSandboxSettings: func() coreserver.UserRunManagedSandboxSettingSource {
			if managedSandboxProfiles != nil {
				return store
			}
			return nil
		}(),
		ManagedSandboxProfiles: managedSandboxProfiles,
	})
	if err != nil {
		return err
	}
	userRunHandler, err := coreserver.NewUserRunHandler(browserAuthorizer, browserUserAuthorizer, userRunService)
	if err != nil {
		return err
	}
	userSessionHandler, err := coreserver.NewUserSessionHandler(
		browserAuthorizer,
		browserUserAuthorizer,
		coreserver.StateStoreUserSessionCommands{Store: store, Prompts: promptReader, TrajectoryCursors: trajectoryCursorCodec},
	)
	if err != nil {
		return err
	}
	var workspaceLLMGatewayHandler *coreserver.WorkspaceLLMGatewayHandler
	if llmGatewayService != nil {
		workspaceLLMGatewayHandler, err = coreserver.NewWorkspaceLLMGatewayHandler(
			platformAuthorizer, platformUserAuthorizer, llmGatewayService,
		)
		if err != nil {
			return err
		}
	}
	userApprovalService, err := coreserver.NewUserApprovalService(coreserver.UserApprovalServiceConfig{Store: store})
	if err != nil {
		return err
	}
	userApprovalHandler, err := coreserver.NewUserApprovalHandler(browserAuthorizer, browserUserAuthorizer, userApprovalService)
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
	var managedSandboxHandler *coreserver.ManagedSandboxHandler
	if managedExecutorEnabled {
		managedSandboxHandler, err = coreserver.NewManagedSandboxHandler(
			sandboxGatewayAuthorizer,
			coreserver.StateStoreManagedSandboxCommands{Store: store},
		)
		if err != nil {
			return err
		}
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
	mountCorePlatformResourceRoutes(handler, platformResourceHandler)
	if workspaceCredentialHandler != nil {
		credentialRoutes := workspaceCredentialHandler.Routes()
		handler.Handle(corecontract.WorkspaceCredentialProviderSchemasPath, credentialRoutes)
		handler.Handle(corecontract.WorkspaceCredentialCollectionRoutePattern, credentialRoutes)
		handler.Handle(corecontract.WorkspaceCredentialResourceRoutePattern, credentialRoutes)
	}
	if workspaceCredentialAuthorizationHandler != nil {
		authorizationRoutes := workspaceCredentialAuthorizationHandler.Routes()
		handler.Handle(corecontract.WorkspaceCredentialAuthorizationCollectionRoutePattern, authorizationRoutes)
		handler.Handle(corecontract.WorkspaceCredentialAuthorizationResourceRoutePattern, authorizationRoutes)
	}
	mountCoreUserSessionRoutes(handler, userSessionHandler)
	handler.Handle("/v2/", userRunHandler.Routes())
	mountCoreWorkspaceLLMGatewayRoutes(handler, workspaceLLMGatewayHandler)
	handler.Handle(corecontract.DecideUserApprovalRoutePattern, userApprovalHandler)
	mountCoreExecutorIdentityRoutes(handler, userExecutorHandler, internalExecutorIdentityHandler)
	handler.Handle(corecontract.FreezeBrainToolCatalogPath, brainToolCatalogHandler)
	handler.Handle(corecontract.BrainToolCatalogPathPrefix, brainToolCatalogHandler)
	handler.Handle(corecontract.ClaimRunDispatchesPath, runDispatchHandler)
	handler.Handle(corecontract.RunDispatchPathPrefix, runDispatchHandler)
	handler.Handle(corecontract.ClaimRunAttemptPath, runAttemptHandler)
	handler.Handle(corecontract.RunAttemptPathPrefix, runAttemptHandler)
	handler.Handle(corecontract.ResolveRunLaunchStatePath, runLaunchStateHandler)
	mountCoreCredentialRoutes(handler, egressCredentialHandler, executionCredentialHandler)
	handler.Handle(corecontract.ListExecutorEnvironmentsPath, environmentHandler)
	handler.Handle(corecontract.PrepareExecutionPath, executionHandler)
	handler.Handle(corecontract.ExecutionPathPrefix, executionHandler)
	if managedSandboxHandler != nil {
		handler.Handle(corecontract.ReserveManagedSandboxPath, managedSandboxHandler)
		handler.Handle(corecontract.ListManagedSandboxesForReconcilePath, managedSandboxHandler)
		handler.Handle(corecontract.AuthorizeManagedSandboxOperationPath, managedSandboxHandler)
		handler.Handle(corecontract.ManagedSandboxPathPrefix, managedSandboxHandler)
	}
	handler.Handle(corecontract.CreateApprovalPath, approvalHandler)
	handler.Handle(corecontract.ApprovalActionRoutePattern, approvalActionHandler)
	handler.Handle(corecontract.ApprovalPathPrefix, approvalHandler)
	mountCoreRunCapabilityRoutes(handler, runCapabilityHandler)
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
		ErrorLog:          httperrorlog.New(stderr),
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

func mountCorePlatformResourceRoutes(mux *http.ServeMux, handler *coreserver.PlatformResourceHandler) {
	if mux == nil || handler == nil {
		return
	}
	routes := handler.Routes()
	mux.Handle(corecontract.WorkspaceCollectionRoutePattern, routes)
	mux.Handle(corecontract.WorkspaceResourceRoutePattern, routes)
	mux.Handle(corecontract.WorkspaceArchiveRoutePattern, routes)
	mux.Handle(corecontract.WorkspaceManagedSandboxRoutePattern, routes)
	mux.Handle(corecontract.WorkspaceMembersCollectionPattern, routes)
	mux.Handle(corecontract.WorkspaceMemberResourceRoutePattern, routes)
}

func mountCoreUserSessionRoutes(mux *http.ServeMux, handler *coreserver.UserSessionHandler) {
	if mux == nil || handler == nil {
		return
	}
	routes := handler.Routes()
	mux.Handle(corecontract.UserSessionCollectionRoutePattern, routes)
	mux.Handle(corecontract.UserSessionResourceRoutePattern, routes)
	mux.Handle(corecontract.UserSessionTranscriptRoutePattern, routes)
	mux.Handle(corecontract.UserSessionTrajectoryRoutePattern, routes)
	mux.Handle(corecontract.UserSessionArchiveRoutePattern, routes)
}

func mountCoreRunCapabilityRoutes(mux *http.ServeMux, handler http.Handler) {
	if mux == nil || handler == nil {
		return
	}
	mux.Handle(corecontract.IssueRunCapabilitiesPath, handler)
	mux.Handle(corecontract.AuthorizeExecutorRunCapabilityPath, handler)
	mux.Handle(corecontract.AuthorizeLLMProxyRunCapabilityPath, handler)
}

func mountCoreWorkspaceLLMGatewayRoutes(mux *http.ServeMux, handler http.Handler) {
	if mux == nil || handler == nil {
		return
	}
	mux.Handle(corecontract.LLMGatewayCollectionRoutePattern, handler)
	mux.Handle(corecontract.LLMGatewayActionRoutePattern, handler)
}

func mountCoreExecutorIdentityRoutes(mux *http.ServeMux, users, internal http.Handler) {
	if mux == nil {
		return
	}
	if users != nil {
		mux.Handle("GET "+corecontract.ExecutorManagementRoutePattern, users)
		mux.Handle("POST "+corecontract.ExecutorManagementRoutePattern, users)
		mux.Handle("POST "+corecontract.ExecutorEnrollmentTokenRoutePattern, users)
		mux.Handle("DELETE "+corecontract.ExecutorEnrollmentTokenRoutePattern, users)
	}
	if internal != nil {
		mux.Handle(corecontract.CompleteExecutorEnrollmentPath, internal)
		mux.Handle(corecontract.AuthorizeExecutorConnectionPath, internal)
	}
}

func configureCorePromptStore(
	ctx context.Context,
	getenv func(string) string,
	mode coreServeMode,
) (coreserver.UserPromptStore, coreserver.UserPromptReader, string, error) {
	switch mode {
	case coreServeProduction:
		config, err := objectruntime.ParseEnvironment(getenv)
		if err != nil {
			return nil, nil, "", fmt.Errorf("configure production object routing: %w", err)
		}
		objects, err := objectruntime.Open(ctx, config)
		if err != nil {
			return nil, nil, "", err
		}
		prompts, err := coreserver.NewEncryptedUserPromptStore(objects)
		if err != nil {
			return nil, nil, "", err
		}
		return prompts, prompts, "explicit plaintext S3 object store", nil
	case coreServeInsecureDevelopment:
		promptObjectRoot, err := requiredConfiguration(getenv, coreDevPromptObjectRootEnvironment)
		if err != nil {
			return nil, nil, "", err
		}
		prompts, err := coreserver.NewLocalUserPromptStore(promptObjectRoot)
		if err != nil {
			return nil, nil, "", fmt.Errorf("configure insecure-development prompt object store: %w", err)
		}
		return prompts, prompts, "INSECURE DEV plaintext object store", nil
	default:
		return nil, nil, "", errors.New("Core serve mode is invalid")
	}
}

func configureCoreProductionRunCapabilities(
	getenv func(string) string,
	mode coreServeMode,
) (*coreProductionRunCapabilityConfig, error) {
	switch mode {
	case coreServeInsecureDevelopment:
		return nil, nil
	case coreServeProduction:
	default:
		return nil, errors.New("Core serve mode is invalid")
	}
	issuer, err := requiredConfiguration(getenv, coreCapabilityIssuerEnvironment)
	if err != nil {
		return nil, err
	}
	keyID, err := requiredConfiguration(getenv, coreCapabilityKeyIDEnvironment)
	if err != nil {
		return nil, err
	}
	privateKeyFile, err := requiredConfiguration(getenv, coreCapabilityPrivateKeyEnvironment)
	if err != nil {
		return nil, err
	}
	keyringFile, err := requiredConfiguration(getenv, coreCapabilityKeyringEnvironment)
	if err != nil {
		return nil, err
	}
	signer, err := runcapability.LoadProductionSigner(issuer, keyID, privateKeyFile)
	if err != nil {
		return nil, fmt.Errorf("configure production run capability signer: %w", err)
	}
	verifier, err := runcapability.LoadProductionVerifier(issuer, keyringFile)
	if err != nil {
		return nil, fmt.Errorf("configure production run capability verifier: %w", err)
	}
	if verifier.Issuer() != signer.Issuer() || !slices.Contains(verifier.KeyIDs(), signer.KeyID()) {
		return nil, errors.New("production run capability keyring must contain the active signing key for the configured issuer")
	}
	executorID, err := requiredConfiguration(getenv, coreProductionExecutorEnvironment)
	if err != nil {
		return nil, err
	}
	maxRunDuration, err := requiredCoreDuration(getenv, coreMaxRunDurationEnvironment)
	if err != nil {
		return nil, err
	}
	maxApprovalTTL, err := requiredCoreDuration(getenv, coreMaxApprovalTTLEnvironment)
	if err != nil {
		return nil, err
	}
	expiryGrace, err := requiredCoreDuration(getenv, coreCapabilityExpiryGraceEnvironment)
	if err != nil {
		return nil, err
	}
	policy := coreserver.ProductionRunCapabilityPolicy{
		ExecutorID:     executorID,
		MaxRunDuration: maxRunDuration, MaxApprovalTTL: maxApprovalTTL, ExpiryGrace: expiryGrace,
	}
	if err := coreserver.ValidateProductionRunCapabilityPolicy(policy); err != nil {
		return nil, fmt.Errorf("configure production run capability policy: %w", err)
	}
	return &coreProductionRunCapabilityConfig{signer: signer, verifier: verifier, policy: policy}, nil
}

func configureCoreProductionEnrollment(
	getenv func(string) string,
	mode coreServeMode,
	capabilities *coreProductionRunCapabilityConfig,
) (*coreProductionEnrollmentConfig, error) {
	switch mode {
	case coreServeInsecureDevelopment:
		return nil, nil
	case coreServeProduction:
	default:
		return nil, errors.New("Core serve mode is invalid")
	}
	if capabilities == nil || capabilities.signer == nil || capabilities.signer.Issuer() == "" {
		return nil, errors.New("production executor enrollment requires the configured Core capability issuer")
	}
	keyFile, err := requiredConfiguration(getenv, coreEnrollmentKeyEnvironment)
	if err != nil {
		return nil, err
	}
	codec, err := enrollmenttoken.LoadCodec(capabilities.signer.Issuer(), keyFile)
	if err != nil {
		return nil, fmt.Errorf("configure executor enrollment token authority: %w", err)
	}
	ttl, err := requiredCoreDuration(getenv, coreEnrollmentTTLEnvironment)
	if err != nil {
		return nil, err
	}
	if ttl <= 0 || ttl > enrollmenttoken.MaximumTTL || ttl%time.Millisecond != 0 {
		return nil, fmt.Errorf("%s must be a positive whole-millisecond duration no greater than %s", coreEnrollmentTTLEnvironment, enrollmenttoken.MaximumTTL)
	}
	return &coreProductionEnrollmentConfig{tokens: codec, ttl: ttl}, nil
}

func requiredCoreDuration(getenv func(string) string, name string) (time.Duration, error) {
	raw, err := requiredConfiguration(getenv, name)
	if err != nil {
		return 0, err
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be a Go duration: %w", name, err)
	}
	return value, nil
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

func newCoreHydraHTTPClient(caFile, serverName string) (*http.Client, error) {
	caPEM, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("read Hydra server CA: %w", err)
	}
	rootCAs := x509.NewCertPool()
	if !rootCAs.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("Hydra server CA file contains no certificates")
	}
	transport := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          16,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 5 * time.Second,
		ExpectContinueTimeout: time.Second,
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS13,
			RootCAs:    rootCAs,
			ServerName: serverName,
		},
	}
	return &http.Client{Transport: transport, Timeout: 5 * time.Second}, nil
}

// newCoreByteCloudDeviceHTTPClient is intentionally separate from the
// public-provider client. The fixed i18n-tt production gateway can resolve to
// ByteCloud-internal addresses, while user-configurable public providers must
// continue to reject all private DNS answers. The deployment pins the only
// accepted origin before this transport is constructed.
func newCoreByteCloudDeviceHTTPClient() *http.Client {
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          16,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
		ExpectContinueTimeout: time.Second,
		DisableCompression:    true,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	}
	return &http.Client{
		Transport: transport,
		Timeout:   25 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("ByteCloud provider redirects are forbidden")
		},
	}
}

func requiredConfiguration(getenv func(string) string, name string) (string, error) {
	value := strings.TrimSpace(getenv(name))
	if value == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return value, nil
}

func loadSandboxGatewayIdentities(getenv func(string) string) ([]string, error) {
	legacy := strings.TrimSpace(getenv(coreSandboxGatewayIdentityEnvironment))
	raw := strings.TrimSpace(getenv(coreSandboxGatewayIdentitiesEnvironment))
	if legacy != "" && raw != "" {
		return nil, fmt.Errorf(
			"%s and %s are mutually exclusive",
			coreSandboxGatewayIdentityEnvironment, coreSandboxGatewayIdentitiesEnvironment,
		)
	}
	if raw == "" {
		if legacy == "" {
			return nil, fmt.Errorf("%s is required", coreSandboxGatewayIdentitiesEnvironment)
		}
		return []string{legacy}, nil
	}
	if len(raw) > 16*1024 {
		return nil, fmt.Errorf("%s is too large", coreSandboxGatewayIdentitiesEnvironment)
	}
	var identities []string
	if err := json.Unmarshal([]byte(raw), &identities); err != nil {
		return nil, fmt.Errorf("decode %s: %w", coreSandboxGatewayIdentitiesEnvironment, err)
	}
	if len(identities) < 1 || len(identities) > len(managedsandboxprofile.Regions()) {
		return nil, fmt.Errorf("%s must contain between one and four identities", coreSandboxGatewayIdentitiesEnvironment)
	}
	seen := make(map[string]struct{}, len(identities))
	for _, identity := range identities {
		if identity == "" || strings.TrimSpace(identity) != identity {
			return nil, fmt.Errorf("%s contains an invalid identity", coreSandboxGatewayIdentitiesEnvironment)
		}
		if _, duplicate := seen[identity]; duplicate {
			return nil, fmt.Errorf("%s contains a duplicate identity", coreSandboxGatewayIdentitiesEnvironment)
		}
		seen[identity] = struct{}{}
	}
	return identities, nil
}

func configureCoreLarkDeviceApplication(getenv func(string) string, required bool) (string, string, error) {
	appID := strings.TrimSpace(getenv(coreLarkDeviceAppIDEnvironment))
	appSecret := strings.TrimSpace(getenv(coreLarkDeviceAppSecretEnvironment))
	if (appID == "") != (appSecret == "") {
		return "", "", errors.New("Lark device-flow app ID and app secret must be configured together")
	}
	if required && appID == "" {
		return "", "", fmt.Errorf(
			"%s and %s are required for production workspace credential device flow",
			coreLarkDeviceAppIDEnvironment, coreLarkDeviceAppSecretEnvironment,
		)
	}
	return appID, appSecret, nil
}

func configureCoreByteCloudDeviceAPI(getenv func(string) string, mode coreServeMode) (string, error) {
	origin := strings.TrimSpace(getenv(coreByteCloudDeviceAPIEnvironment))
	if origin == "" {
		origin = corecredentials.DefaultByteCloudDeviceAPIBaseURL
	}
	if mode == coreServeProduction && origin != corecredentials.DefaultByteCloudDeviceAPIBaseURL {
		return "", fmt.Errorf(
			"%s must be the pinned i18n-tt production origin %s",
			coreByteCloudDeviceAPIEnvironment, corecredentials.DefaultByteCloudDeviceAPIBaseURL,
		)
	}
	return origin, nil
}
