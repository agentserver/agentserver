package productiondeploy

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/corecredentials"
	"github.com/agentserver/agentserver/v2/internal/taeimage"
)

type deploymentInput struct {
	namespace      string
	platform       string
	component      string
	replicas       int
	image          string
	serviceAccount string
	command        []any
	args           []any
	environment    []any
	initContainers []any
	volumes        []any
	volumeMounts   []any
	ports          []any
	probePort      uint16
	hostAliases    map[string]string
	resources      ContainerResourcesDocument
	uid            uint32
	gid            uint32
	fsGroup        uint32
	capabilities   []string
	strategy       string
	configHash     string
	termination    int
}

func renderRuntime(context renderContext) ([]kubeObject, error) {
	core, err := renderCoreDeployment(context)
	if err != nil {
		return nil, err
	}
	platform, err := renderPlatformDeployment(context)
	if err != nil {
		return nil, err
	}
	browser, err := renderBrowserDeployment(context)
	if err != nil {
		return nil, err
	}
	executor, err := renderExecutorDeployment(context)
	if err != nil {
		return nil, err
	}
	harness, err := renderHarnessDeployment(context)
	if err != nil {
		return nil, err
	}
	llmproxy, err := renderLLMProxyDeployment(context)
	if err != nil {
		return nil, err
	}
	hydra := renderHydraDeployment(context)
	config := context.config
	items := []kubeObject{
		core,
		platform,
		browser,
		executor,
		harness,
		llmproxy,
		hydra,
		podDisruptionBudget(config, coreComponent, config.Document.Replicas.Core),
		podDisruptionBudget(config, platformComponent, config.Document.Replicas.PlatformGateway),
		podDisruptionBudget(config, browserComponent, config.Document.Replicas.BrowserGateway),
		podDisruptionBudget(config, harnessComponent, config.Document.Replicas.HarnessPool),
		podDisruptionBudget(config, llmproxyComponent, config.Document.Replicas.LLMProxy),
		podDisruptionBudget(config, hydraComponent, config.Document.Replicas.Hydra),
	}
	if managedExecutionActive(config.Document.Managed) {
		sandbox, err := renderSandboxDeployment(context)
		if err != nil {
			return nil, err
		}
		items = append(items,
			sandbox,
			podDisruptionBudget(config, sandboxComponent, config.Document.Replicas.SandboxGateway),
		)
		if managedEgressAuthorizerEnabled(config.Document.Managed) {
			egress, err := renderEgressAuthorizerDeployment(context)
			if err != nil {
				return nil, err
			}
			items = append(items, egress,
				podDisruptionBudget(config, egressComponent, config.Document.Replicas.EgressAuthorizer))
		}
	} else if managedEgressAuthorizerEnabled(config.Document.Managed) {
		egress, err := renderEgressAuthorizerDeployment(context)
		if err != nil {
			return nil, err
		}
		items = append(items,
			egress,
			podDisruptionBudget(config, egressComponent, config.Document.Replicas.EgressAuthorizer),
		)
	}
	return items, nil
}

func renderHydraDeployment(context renderContext) kubeObject {
	document := context.config.Document
	service := document.Services.Hydra
	labels := componentLabels(hydraComponent)
	materialPath := "/var/run/agentserver/hydra"
	container := kubeObject{
		"name": hydraComponent, "image": document.Images.Hydra, "imagePullPolicy": "IfNotPresent",
		"command": []any{"/usr/bin/hydra"}, "args": []any{"serve", "all", "--sqa-opt-out"},
		"env": []any{
			secretEnvironment("DSN", document.Secrets.Hydra, "database-url"),
			secretEnvironment("SECRETS_SYSTEM", document.Secrets.Hydra, "system-secret"),
			secretEnvironment("SECRETS_COOKIE", document.Secrets.Hydra, "cookie-secret"),
			valueEnvironment("URLS_SELF_ISSUER", document.OAuth.Hydra.Issuer),
			valueEnvironment("URLS_SELF_PUBLIC", document.OAuth.Hydra.PublicOrigin),
			valueEnvironment("URLS_LOGIN", document.OAuth.Hydra.PublicOrigin+"/auth/hydra/login"),
			valueEnvironment("URLS_CONSENT", document.OAuth.Hydra.PublicOrigin+"/auth/hydra/consent"),
			valueEnvironment("SERVE_PUBLIC_PORT", strconv.Itoa(int(service.PublicPort))),
			valueEnvironment("SERVE_ADMIN_PORT", strconv.Itoa(int(service.AdminPort))),
			valueEnvironment("SERVE_PUBLIC_BASE_URL", document.OAuth.Hydra.PublicOrigin),
			valueEnvironment("SERVE_PUBLIC_TLS_ENABLED", "false"),
			valueEnvironment("SERVE_PUBLIC_CORS_ENABLED", "true"),
			valueEnvironment("SERVE_PUBLIC_CORS_ALLOWED_ORIGINS", strings.Join([]string{
				"https://" + document.Ingress.FrontendHostname,
				"https://" + document.Ingress.BrowserFrontendHostname,
			}, ",")),
			valueEnvironment("SERVE_PUBLIC_CORS_ALLOWED_METHODS", "POST,OPTIONS"),
			valueEnvironment("SERVE_PUBLIC_CORS_ALLOWED_HEADERS", "Accept,Content-Type"),
			valueEnvironment("SERVE_PUBLIC_CORS_ALLOW_CREDENTIALS", "false"),
			valueEnvironment("SERVE_ADMIN_TLS_ENABLED", "true"),
			valueEnvironment("SERVE_ADMIN_TLS_CERT_PATH", materialPath+"/tls.crt"),
			valueEnvironment("SERVE_ADMIN_TLS_KEY_PATH", materialPath+"/tls.key"),
			valueEnvironment("SERVE_COOKIES_SECURE", "true"),
			valueEnvironment("SERVE_COOKIES_SAME_SITE_MODE", "Lax"),
			valueEnvironment("OIDC_DYNAMIC_CLIENT_REGISTRATION_ENABLED", "false"),
			valueEnvironment("OIDC_SUBJECT_IDENTIFIERS_SUPPORTED_TYPES", "public"),
			valueEnvironment("OAUTH2_PKCE_ENFORCED_FOR_PUBLIC_CLIENTS", "true"),
			valueEnvironment("OAUTH2_EXPOSE_INTERNAL_ERRORS", "false"),
			valueEnvironment("OAUTH2_GRANT_REFRESH_TOKEN_ROTATION_GRACE_PERIOD", "0s"),
			valueEnvironment("STRATEGIES_ACCESS_TOKEN", "opaque"),
			valueEnvironment("TTL_ACCESS_TOKEN", "1h"),
			valueEnvironment("TTL_ID_TOKEN", "1h"),
			valueEnvironment("TTL_AUTH_CODE", "10m"),
			valueEnvironment("LOG_LEVEL", "info"),
			valueEnvironment("LOG_FORMAT", "json"),
		},
		"ports": []any{
			kubeObject{"name": "http-public", "containerPort": int(service.PublicPort), "protocol": "TCP"},
			kubeObject{"name": "https-admin", "containerPort": int(service.AdminPort), "protocol": "TCP"},
		},
		"resources":       resources(document.Resources.Hydra),
		"securityContext": runtimeSecurityContext(HydraUID, HydraGID),
		"volumeMounts": []any{
			kubeObject{"name": "hydra-material", "mountPath": materialPath, "readOnly": true},
			kubeObject{"name": "scratch", "mountPath": "/tmp"},
		},
		"startupProbe":   hydraHealthProbe("/health/ready", service.AdminPort, 30),
		"readinessProbe": hydraHealthProbe("/health/ready", service.AdminPort, 6),
		"livenessProbe":  hydraHealthProbe("/health/alive", service.AdminPort, 6),
	}
	return kubeObject{
		"apiVersion": "apps/v1", "kind": "Deployment",
		"metadata": metadata(hydraComponent, document.Namespace, labels, map[string]string{"reloader.stakater.com/auto": "true"}),
		"spec": kubeObject{
			"replicas": document.Replicas.Hydra, "revisionHistoryLimit": 3, "progressDeadlineSeconds": 600,
			"strategy": kubeObject{"type": "RollingUpdate", "rollingUpdate": kubeObject{"maxUnavailable": 0, "maxSurge": 1}},
			"selector": kubeObject{"matchLabels": selectorLabels(hydraComponent)},
			"template": kubeObject{
				"metadata": kubeObject{"labels": labels, "annotations": kubeObject{
					"agentserver.dev/config-sha256":                  context.documentHash,
					"cluster-autoscaler.kubernetes.io/safe-to-evict": "false",
				}},
				"spec": kubeObject{
					"serviceAccountName": hydraComponent, "automountServiceAccountToken": false,
					"enableServiceLinks": false, "terminationGracePeriodSeconds": 30,
					"securityContext": kubeObject{
						"fsGroup": int64(HydraGID), "fsGroupChangePolicy": "OnRootMismatch",
						"seccompProfile": kubeObject{"type": "RuntimeDefault"},
					},
					"nodeSelector": productionNodeSelector(document.Platform),
					"containers":   []any{container},
					"volumes": []any{
						hydraMaterialVolume(document.Secrets.Hydra, true),
						emptyDirVolume("scratch", "Memory", document.Resources.ScratchTmpfs),
					},
					"topologySpreadConstraints": []any{kubeObject{
						"maxSkew": 1, "topologyKey": "kubernetes.io/hostname", "whenUnsatisfiable": "ScheduleAnyway",
						"labelSelector": kubeObject{"matchLabels": selectorLabels(hydraComponent)},
					}},
				},
			},
		},
	}
}

func hydraMaterialVolume(secretName string, includeServerIdentity bool) kubeObject {
	keys := []string{"ca.crt"}
	if includeServerIdentity {
		keys = append(keys, "tls.crt", "tls.key")
	}
	items := make([]any, len(keys))
	for index, key := range keys {
		items[index] = kubeObject{"key": key, "path": key, "mode": 288}
	}
	return kubeObject{"name": "hydra-material", "secret": kubeObject{
		"secretName": secretName, "defaultMode": 288, "items": items,
	}}
}

func hydraHealthProbe(path string, port uint16, failures int) kubeObject {
	return kubeObject{
		"httpGet":        kubeObject{"scheme": "HTTPS", "path": path, "port": int(port)},
		"timeoutSeconds": 3, "periodSeconds": 5, "failureThreshold": failures,
	}
}

func renderCoreDeployment(context renderContext) (kubeObject, error) {
	config := context.config
	document := config.Document
	materialProfile := materialProfileCore
	if managedExecutionActive(document.Managed) {
		materialProfile = materialProfileCoreManaged
		if managedEgressAuthorizerEnabled(document.Managed) {
			materialProfile = materialProfileCoreManagedWebhook
		}
	}
	material, err := secretMaterialVolume("material", document.Secrets.Core, materialProfile, groupReadableSecretMode)
	if err != nil {
		return nil, err
	}
	materialMounts, err := secretMaterialMounts("material", "/var/run/agentserver/material", materialProfile)
	if err != nil {
		return nil, err
	}
	environment := []any{
		secretEnvironment("AGENTSERVER_V2_DATABASE_URL", document.Secrets.Core, "database-url"),
		valueEnvironment("AGENTSERVER_V2_CORE_LISTEN_ADDR", listenAddress(document.Services.Core.Port)),
		valueEnvironment("AGENTSERVER_V2_CORE_TLS_CERT_FILE", serviceMaterialPath("tls.crt")),
		valueEnvironment("AGENTSERVER_V2_CORE_TLS_KEY_FILE", serviceMaterialPath("tls.key")),
		valueEnvironment("AGENTSERVER_V2_CORE_CLIENT_CA_FILE", serviceMaterialPath("ca.crt")),
		valueEnvironment("AGENTSERVER_V2_EXECUTOR_GATEWAY_SPIFFE_ID", spiffeIdentity(config, executorComponent)),
		valueEnvironment("AGENTSERVER_V2_HARNESS_POOL_SPIFFE_ID", spiffeIdentity(config, harnessComponent)),
		valueEnvironment("AGENTSERVER_V2_BROWSER_GATEWAY_SPIFFE_ID", spiffeIdentity(config, browserComponent)),
		valueEnvironment("AGENTSERVER_V2_PLATFORM_GATEWAY_SPIFFE_ID", spiffeIdentity(config, platformComponent)),
		valueEnvironment("AGENTSERVER_V2_LLMPROXY_SPIFFE_ID", spiffeIdentity(config, llmproxyComponent)),
		valueEnvironment("AGENTSERVER_V2_HYDRA_INTROSPECTION_URL", document.OAuth.Hydra.IntrospectionURL),
		valueEnvironment("AGENTSERVER_V2_HYDRA_ADMIN_URL", document.OAuth.Hydra.AdminURL),
		valueEnvironment("AGENTSERVER_V2_HYDRA_PUBLIC_ORIGIN", document.OAuth.Hydra.PublicOrigin),
		valueEnvironment("AGENTSERVER_V2_HYDRA_ISSUER", document.OAuth.Hydra.Issuer),
		valueEnvironment("AGENTSERVER_V2_HYDRA_PLATFORM_CLIENT_ID", document.OAuth.Hydra.PlatformClientID),
		valueEnvironment("AGENTSERVER_V2_HYDRA_BROWSER_CLIENT_ID", document.OAuth.Hydra.BrowserClientID),
		valueEnvironment("AGENTSERVER_V2_HYDRA_CA_FILE", serviceMaterialPath("ca.crt")),
		valueEnvironment("AGENTSERVER_V2_HYDRA_SERVER_NAME", HydraInternalHost),
		secretEnvironment("AGENTSERVER_V2_EXTERNAL_OIDC_ISSUER", document.Secrets.Core, "external-oidc-issuer"),
		secretEnvironment("AGENTSERVER_V2_EXTERNAL_OIDC_CLIENT_ID", document.Secrets.Core, "external-oidc-client-id"),
		secretEnvironment("AGENTSERVER_V2_EXTERNAL_OIDC_CLIENT_SECRET", document.Secrets.Core, "external-oidc-client-secret"),
		valueEnvironment("AGENTSERVER_V2_EXTERNAL_OIDC_REDIRECT_URL", document.OAuth.ExternalOIDC.RedirectURL),
		secretEnvironment("AGENTSERVER_V2_LOGIN_TRANSACTION_KEY", document.Secrets.Core, "login-transaction-key"),
		secretEnvironment("AGENTSERVER_V2_RUN_CURSOR_KEY", document.Secrets.Core, "run-cursor-key"),
		valueEnvironment("AGENTSERVER_V2_RUN_POLICY_VERSION", document.Runtime.RunPolicyVersion),
		valueEnvironment("AGENTSERVER_V2_RUN_ALLOWED_TOOLS", strings.Join(document.Runtime.AllowedTools, ",")),
		valueEnvironment("AGENTSERVER_V2_RUN_CAPABILITY_ISSUER", document.Runtime.CapabilityIssuer),
		valueEnvironment("AGENTSERVER_V2_RUN_CAPABILITY_SIGNING_KEY_ID", document.Runtime.CapabilitySigningKeyID),
		valueEnvironment("AGENTSERVER_V2_RUN_CAPABILITY_SIGNING_KEY_FILE", serviceMaterialPath("run-capability.key")),
		valueEnvironment("AGENTSERVER_V2_RUN_CAPABILITY_KEYRING_FILE", serviceMaterialPath("run-capability-keyring.json")),
		valueEnvironment("AGENTSERVER_V2_EXECUTOR_ID", document.Bootstrap.ExecutorID),
		valueEnvironment("AGENTSERVER_V2_LLM_GATEWAY_SEALING_KEYRING_FILE", serviceMaterialPath("llm-gateway-sealing-keyring.json")),
		valueEnvironment("AGENTSERVER_V2_LLM_GATEWAY_REDIRECT_URL", "https://"+document.Ingress.FrontendHostname+corecontract.LLMGatewayOIDCCallbackPath),
		valueEnvironment("AGENTSERVER_V2_MAX_RUN_DURATION", document.Runtime.MaxRunDuration),
		valueEnvironment("AGENTSERVER_V2_MAX_APPROVAL_TTL", document.Runtime.MaxApprovalTTL),
		valueEnvironment("AGENTSERVER_V2_RUN_CAPABILITY_EXPIRY_GRACE", document.Runtime.CapabilityExpiryGrace),
		valueEnvironment("AGENTSERVER_V2_EXECUTOR_ENROLLMENT_TOKEN_KEY_FILE", serviceMaterialPath("executor-enrollment.key")),
		valueEnvironment("AGENTSERVER_V2_EXECUTOR_ENROLLMENT_TOKEN_TTL", document.Runtime.EnrollmentTokenTTL),
		valueEnvironment("AGENTSERVER_V2_MANAGED_EXECUTOR_ENABLED", strconv.FormatBool(managedExecutionActive(document.Managed))),
		valueEnvironment("AGENTSERVER_V2_CREDENTIAL_SEALING_KEYRING_FILE", serviceMaterialPath("credential-sealing-keyring.json")),
		secretEnvironment("AGENTSERVER_V2_LARK_DEVICE_APP_ID", document.Secrets.Core, "lark-device-app-id"),
		secretEnvironment("AGENTSERVER_V2_LARK_DEVICE_APP_SECRET", document.Secrets.Core, "lark-device-app-secret"),
		valueEnvironment("AGENTSERVER_V2_LARK_DEVICE_SCOPES", corecredentials.DefaultManagedLarkScopes),
		valueEnvironment("AGENTSERVER_V2_BYTECLOUD_DEVICE_API_BASE_URL", corecredentials.DefaultByteCloudDeviceAPIBaseURL),
	}
	if managedExecutionActive(document.Managed) {
		environment = append(environment,
			valueEnvironment("AGENTSERVER_V2_SANDBOX_GATEWAY_SPIFFE_ID", spiffeIdentity(config, sandboxComponent)),
			valueEnvironment("AGENTSERVER_V2_MANAGED_TAE_PSM", document.Managed.TAE.PSM),
			valueEnvironment("AGENTSERVER_V2_TAE_POLICY_WEBHOOK_REQUIRED", strconv.FormatBool(document.Managed.TAE.Policy.PublicWebhookRequired)),
		)
		if managedEgressAuthorizerEnabled(document.Managed) {
			environment = append(environment,
				valueEnvironment("AGENTSERVER_V2_EGRESS_AUTHORIZER_SPIFFE_ID", spiffeIdentity(config, egressComponent)),
				valueEnvironment("AGENTSERVER_V2_EGRESS_PLACEHOLDER_KEYRING_FILE", serviceMaterialPath("egress-placeholder-keyring.json")),
			)
		}
	}
	environment = append(environment, objectStoreEnvironment(document)...)
	return deployment(deploymentInput{
		namespace: document.Namespace, platform: document.Platform, component: coreComponent, replicas: document.Replicas.Core,
		image: document.Images.Service, serviceAccount: coreComponent,
		command: []any{"/usr/local/bin/agentserver-core"}, args: []any{"serve"},
		environment:  environment,
		volumes:      []any{material, emptyDirVolume("scratch", "Memory", document.Resources.ScratchTmpfs)},
		volumeMounts: append(materialMounts, kubeObject{"name": "scratch", "mountPath": "/tmp"}),
		resources:    document.Resources.Core, uid: ServiceUID, gid: ServiceGID, fsGroup: ServiceGID,
		hostAliases: map[string]string{HydraInternalHost: document.Services.Hydra.ClusterIP},
		strategy:    "RollingUpdate", configHash: context.documentHash, termination: 20,
	}), nil
}

func renderPlatformDeployment(context renderContext) (kubeObject, error) {
	config := context.config
	document := config.Document
	material, err := secretMaterialVolume("material", document.Secrets.PlatformGateway, materialProfilePlatformGateway, groupReadableSecretMode)
	if err != nil {
		return nil, err
	}
	materialMounts, err := secretMaterialMounts("material", "/var/run/agentserver/material", materialProfilePlatformGateway)
	if err != nil {
		return nil, err
	}
	environment := []any{
		valueEnvironment("AGENTSERVER_V2_PLATFORM_GATEWAY_LISTEN_ADDR", listenAddress(document.Services.PlatformGateway.Port)),
		valueEnvironment("AGENTSERVER_V2_PLATFORM_PUBLIC_ORIGIN", "https://"+document.Ingress.FrontendHostname),
		valueEnvironment("AGENTSERVER_V2_BROWSER_FRONTEND_ORIGIN", "https://"+document.Ingress.BrowserFrontendHostname),
		valueEnvironment("AGENTSERVER_V2_CORE_URL", internalOrigin(CoreInternalHost, document.Services.Core.Port)),
		valueEnvironment("AGENTSERVER_V2_CORE_CA_FILE", serviceMaterialPath("ca.crt")),
		valueEnvironment("AGENTSERVER_V2_CORE_CLIENT_CERT_FILE", serviceMaterialPath("tls.crt")),
		valueEnvironment("AGENTSERVER_V2_CORE_CLIENT_KEY_FILE", serviceMaterialPath("tls.key")),
		valueEnvironment("AGENTSERVER_V2_CORE_SERVER_NAME", CoreInternalHost),
		valueEnvironment("AGENTSERVER_V2_PLATFORM_OAUTH_CLIENT_ID", document.OAuth.Hydra.PlatformClientID),
		valueEnvironment("AGENTSERVER_V2_PLATFORM_OAUTH_AUDIENCE", PlatformOAuthAudience()),
		valueEnvironment("AGENTSERVER_V2_PLATFORM_OAUTH_SCOPES", strings.Join(PlatformOAuthScopes(), ",")),
		valueEnvironment("AGENTSERVER_V2_PLATFORM_OAUTH_AUTHORIZATION_ENDPOINT", document.OAuth.Hydra.PublicOrigin+"/oauth2/auth"),
		valueEnvironment("AGENTSERVER_V2_PLATFORM_OAUTH_TOKEN_ENDPOINT", document.OAuth.Hydra.PublicOrigin+"/oauth2/token"),
	}
	return deployment(deploymentInput{
		namespace: document.Namespace, platform: document.Platform, component: platformComponent, replicas: document.Replicas.PlatformGateway,
		image: document.Images.Service, serviceAccount: platformComponent,
		command: []any{"/usr/local/bin/platform-gateway"}, args: []any{"serve"}, environment: environment,
		volumes:      []any{material, emptyDirVolume("scratch", "Memory", document.Resources.ScratchTmpfs)},
		volumeMounts: append(materialMounts, kubeObject{"name": "scratch", "mountPath": "/tmp"}),
		ports:        []any{kubeObject{"name": "http", "containerPort": int(document.Services.PlatformGateway.Port), "protocol": "TCP"}},
		probePort:    document.Services.PlatformGateway.Port,
		hostAliases:  map[string]string{CoreInternalHost: document.Services.Core.ClusterIP},
		resources:    document.Resources.PlatformGateway, uid: ServiceUID, gid: ServiceGID, fsGroup: ServiceGID,
		strategy: "RollingUpdate", configHash: context.documentHash, termination: 20,
	}), nil
}

func renderBrowserDeployment(context renderContext) (kubeObject, error) {
	config := context.config
	document := config.Document
	material, err := secretMaterialVolume("material", document.Secrets.BrowserGateway, materialProfileBrowserGateway, groupReadableSecretMode)
	if err != nil {
		return nil, err
	}
	materialMounts, err := secretMaterialMounts("material", "/var/run/agentserver/material", materialProfileBrowserGateway)
	if err != nil {
		return nil, err
	}
	environment := []any{
		valueEnvironment("AGENTSERVER_V2_BROWSER_GATEWAY_LISTEN_ADDR", listenAddress(document.Services.BrowserGateway.Port)),
		valueEnvironment("AGENTSERVER_V2_BROWSER_FRONTEND_ORIGIN", "https://"+document.Ingress.BrowserFrontendHostname),
		valueEnvironment("AGENTSERVER_V2_BROWSER_API_ORIGIN", "https://"+document.Ingress.BrowserHostname),
		valueEnvironment("AGENTSERVER_V2_CORE_URL", internalOrigin(CoreInternalHost, document.Services.Core.Port)),
		valueEnvironment("AGENTSERVER_V2_CORE_CA_FILE", serviceMaterialPath("ca.crt")),
		valueEnvironment("AGENTSERVER_V2_CORE_CLIENT_CERT_FILE", serviceMaterialPath("tls.crt")),
		valueEnvironment("AGENTSERVER_V2_CORE_CLIENT_KEY_FILE", serviceMaterialPath("tls.key")),
		valueEnvironment("AGENTSERVER_V2_CORE_SERVER_NAME", CoreInternalHost),
		valueEnvironment("AGENTSERVER_V2_BROWSER_OAUTH_CLIENT_ID", document.OAuth.Hydra.BrowserClientID),
		valueEnvironment("AGENTSERVER_V2_BROWSER_OAUTH_AUDIENCE", BrowserOAuthAudience()),
		valueEnvironment("AGENTSERVER_V2_BROWSER_OAUTH_SCOPES", strings.Join(BrowserOAuthScopes(), ",")),
		valueEnvironment("AGENTSERVER_V2_BROWSER_OAUTH_AUTHORIZATION_ENDPOINT", document.OAuth.Hydra.PublicOrigin+"/oauth2/auth"),
		valueEnvironment("AGENTSERVER_V2_BROWSER_OAUTH_TOKEN_ENDPOINT", document.OAuth.Hydra.PublicOrigin+"/oauth2/token"),
	}
	return deployment(deploymentInput{
		namespace: document.Namespace, platform: document.Platform, component: browserComponent, replicas: document.Replicas.BrowserGateway,
		image: document.Images.Service, serviceAccount: browserComponent,
		command: []any{"/usr/local/bin/browser-gateway"}, args: []any{"serve"}, environment: environment,
		volumes:      []any{material, emptyDirVolume("scratch", "Memory", document.Resources.ScratchTmpfs)},
		volumeMounts: append(materialMounts, kubeObject{"name": "scratch", "mountPath": "/tmp"}),
		ports:        []any{kubeObject{"name": "http", "containerPort": int(document.Services.BrowserGateway.Port), "protocol": "TCP"}},
		probePort:    document.Services.BrowserGateway.Port,
		hostAliases:  map[string]string{CoreInternalHost: document.Services.Core.ClusterIP},
		resources:    document.Resources.BrowserGateway, uid: ServiceUID, gid: ServiceGID, fsGroup: ServiceGID,
		strategy: "RollingUpdate", configHash: context.documentHash, termination: 20,
	}), nil
}

func renderExecutorDeployment(context renderContext) (kubeObject, error) {
	config := context.config
	document := config.Document
	materialProfile := materialProfileExecutorGateway
	if managedExecutionActive(document.Managed) {
		materialProfile = materialProfileExecutorManaged
		if managedEgressAuthorizerEnabled(document.Managed) {
			materialProfile = materialProfileExecutorWebhook
		}
	}
	material, err := secretMaterialVolume("material", document.Secrets.ExecutorGateway, materialProfile, groupReadableSecretMode)
	if err != nil {
		return nil, err
	}
	materialMounts, err := secretMaterialMounts("material", "/var/run/agentserver/material", materialProfile)
	if err != nil {
		return nil, err
	}
	environment := []any{
		valueEnvironment("AGENTSERVER_V2_EXECUTOR_GATEWAY_LISTEN_ADDR", listenAddress(document.Services.ExecutorGateway.InternalPort)),
		valueEnvironment("AGENTSERVER_V2_EXECUTOR_GATEWAY_PUBLIC_LISTEN_ADDR", listenAddress(document.Services.ExecutorGateway.PublicPort)),
		valueEnvironment("AGENTSERVER_V2_EXECUTOR_GATEWAY_TLS_CERT_FILE", serviceMaterialPath("tls.crt")),
		valueEnvironment("AGENTSERVER_V2_EXECUTOR_GATEWAY_TLS_KEY_FILE", serviceMaterialPath("tls.key")),
		valueEnvironment("AGENTSERVER_V2_CORE_URL", internalOrigin(CoreInternalHost, document.Services.Core.Port)),
		valueEnvironment("AGENTSERVER_V2_CORE_CA_FILE", serviceMaterialPath("ca.crt")),
		valueEnvironment("AGENTSERVER_V2_CORE_CLIENT_CERT_FILE", serviceMaterialPath("tls.crt")),
		valueEnvironment("AGENTSERVER_V2_CORE_CLIENT_KEY_FILE", serviceMaterialPath("tls.key")),
		valueEnvironment("AGENTSERVER_V2_CORE_SERVER_NAME", CoreInternalHost),
		valueEnvironment("AGENTSERVER_V2_EXECUTOR_GATEWAY_SPIFFE_ID", spiffeIdentity(config, executorComponent)),
		valueEnvironment("AGENTSERVER_V2_EXECUTOR_ID", document.Bootstrap.ExecutorID),
		valueEnvironment("AGENTSERVER_V2_RUN_CAPABILITY_ISSUER", document.Runtime.CapabilityIssuer),
		valueEnvironment("AGENTSERVER_V2_RUN_CAPABILITY_KEYRING_FILE", serviceMaterialPath("run-capability-keyring.json")),
		valueEnvironment("AGENTSERVER_V2_EXECUTION_POLICY_VERSION", document.Runtime.ExecutionPolicyVersion),
		valueEnvironment("AGENTSERVER_V2_SHELL_POLICY_DECISION", document.Runtime.ShellPolicyDecision),
		valueEnvironment("AGENTSERVER_V2_READ_FILE_POLICY_DECISION", document.Runtime.ReadFilePolicyDecision),
	}
	hosts := map[string]string{CoreInternalHost: document.Services.Core.ClusterIP}
	if managedExecutionActive(document.Managed) {
		issuer := spiffeIdentity(config, executorComponent)
		environment = append(environment,
			valueEnvironment("AGENTSERVER_V2_SANDBOX_GATEWAY_URL", internalOrigin(SandboxInternalHost, document.Services.SandboxGateway.Port)),
			valueEnvironment("AGENTSERVER_V2_SANDBOX_GATEWAY_CA_FILE", serviceMaterialPath("ca.crt")),
			valueEnvironment("AGENTSERVER_V2_SANDBOX_GATEWAY_SERVER_NAME", SandboxInternalHost),
			valueEnvironment("AGENTSERVER_V2_SANDBOX_BACKEND_CAPABILITY_ISSUER", issuer),
			valueEnvironment("AGENTSERVER_V2_SANDBOX_BACKEND_CAPABILITY_KEY_ID", ProductionSandboxBackendKeyID),
			valueEnvironment("AGENTSERVER_V2_SANDBOX_BACKEND_CAPABILITY_SIGNING_KEY_FILE", serviceMaterialPath("sandbox-backend-capability.key")),
			valueEnvironment("AGENTSERVER_V2_SANDBOX_FENCER_CAPABILITY_ISSUER", issuer),
			valueEnvironment("AGENTSERVER_V2_SANDBOX_FENCER_CAPABILITY_KEY_ID", ProductionSandboxFencerKeyID),
			valueEnvironment("AGENTSERVER_V2_SANDBOX_FENCER_CAPABILITY_SIGNING_KEY_FILE", serviceMaterialPath("sandbox-fencer-capability.key")),
			valueEnvironment("AGENTSERVER_V2_MANAGED_TAE_PSM", document.Managed.TAE.PSM),
			valueEnvironment("AGENTSERVER_V2_TAE_POLICY_WEBHOOK_REQUIRED", strconv.FormatBool(document.Managed.TAE.Policy.PublicWebhookRequired)),
		)
		if managedEgressAuthorizerEnabled(document.Managed) {
			environment = append(environment,
				valueEnvironment("AGENTSERVER_V2_EGRESS_PLACEHOLDER_ISSUER", issuer),
				valueEnvironment("AGENTSERVER_V2_EGRESS_PLACEHOLDER_KEY_ID", ProductionEgressPlaceholderKeyID),
				valueEnvironment("AGENTSERVER_V2_EGRESS_PLACEHOLDER_SIGNING_KEY_FILE", serviceMaterialPath("egress-placeholder.key")),
			)
		}
		hosts[SandboxInternalHost] = document.Services.SandboxGateway.ClusterIP
	}
	return deployment(deploymentInput{
		namespace: document.Namespace, platform: document.Platform, component: executorComponent, replicas: 1,
		image: document.Images.Service, serviceAccount: executorComponent,
		command: []any{"/usr/local/bin/executor-gateway"}, args: []any{"serve"}, environment: environment,
		volumes:      []any{material, emptyDirVolume("scratch", "Memory", document.Resources.ScratchTmpfs)},
		volumeMounts: append(materialMounts, kubeObject{"name": "scratch", "mountPath": "/tmp"}),
		ports: []any{
			kubeObject{"name": "http-agentx", "containerPort": int(document.Services.ExecutorGateway.PublicPort), "protocol": "TCP"},
			kubeObject{"name": "https-mcp", "containerPort": int(document.Services.ExecutorGateway.InternalPort), "protocol": "TCP"},
		},
		probePort:   document.Services.ExecutorGateway.PublicPort,
		hostAliases: hosts,
		resources:   document.Resources.ExecutorGateway, uid: ServiceUID, gid: ServiceGID, fsGroup: ServiceGID,
		strategy: "Recreate", configHash: context.documentHash, termination: 30,
	}), nil
}

func renderHarnessDeployment(context renderContext) (kubeObject, error) {
	config := context.config
	document := config.Document
	poolProfile := materialProfileHarnessPool
	if managedExecutionActive(document.Managed) {
		poolProfile = materialProfileHarnessPoolManaged
	}
	poolMaterial, err := secretMaterialVolume("pool-material", document.Secrets.HarnessPool, poolProfile, groupReadableSecretMode)
	if err != nil {
		return nil, err
	}
	workerMaterial, err := secretMaterialVolume("worker-material", document.Secrets.HarnessWorker, materialProfileHarnessWorker, workerReadableSecretMode)
	if err != nil {
		return nil, err
	}
	poolMaterialMounts, err := secretMaterialMounts("pool-material", "/var/run/agentserver/pool", poolProfile)
	if err != nil {
		return nil, err
	}
	workerMaterialMounts, err := secretMaterialMounts("worker-material", "/var/run/agentserver/worker", materialProfileHarnessWorker)
	if err != nil {
		return nil, err
	}
	environment := []any{
		valueEnvironment("AGENTSERVER_V2_HARNESS_POOL_LISTEN_ADDR", "127.0.0.1:"+strconv.Itoa(HarnessControlPort)),
		valueEnvironment("AGENTSERVER_V2_HARNESS_POOL_TLS_CERT_FILE", poolMaterialPath("tls.crt")),
		valueEnvironment("AGENTSERVER_V2_HARNESS_POOL_TLS_KEY_FILE", poolMaterialPath("tls.key")),
		valueEnvironment("AGENTSERVER_V2_HARNESS_POOL_WORKER_CA_FILE", poolMaterialPath("ca.crt")),
		valueEnvironment("AGENTSERVER_V2_HARNESS_POOL_SPIFFE_ID", spiffeIdentity(config, harnessComponent)),
		valueEnvironment("AGENTSERVER_V2_HARNESS_WORKER_SPIFFE_ID", workerSPIFFEIdentity(config)),
		valueEnvironment("AGENTSERVER_V2_CORE_URL", internalOrigin(CoreInternalHost, document.Services.Core.Port)),
		valueEnvironment("AGENTSERVER_V2_CORE_CA_FILE", poolMaterialPath("ca.crt")),
		valueEnvironment("AGENTSERVER_V2_CORE_SERVER_NAME", CoreInternalHost),
		valueEnvironment("AGENTSERVER_V2_EXECUTOR_ID", document.Bootstrap.ExecutorID),
		valueEnvironment("AGENTSERVER_V2_HARNESS_RUNTIME_DIR", "/var/lib/agentserver/runtime"),
		valueEnvironment("AGENTSERVER_V2_CHECKPOINT_STAGING_DIR", "/var/lib/agentserver/checkpoint"),
		valueEnvironment("AGENTSERVER_V2_HARNESS_WORKER_BIN", "/usr/local/bin/harness-worker"),
		valueEnvironment("AGENTSERVER_V2_HARNESS_WORKER_CONFIG_FILE", "/etc/agentserver/worker-deployment.json"),
		valueEnvironment("AGENTSERVER_V2_RUN_MANIFEST_SIGNING_KEY_ID", document.Runtime.ManifestSigningKeyID),
		valueEnvironment("AGENTSERVER_V2_RUN_MANIFEST_SIGNING_KEY_FILE", poolMaterialPath("run-manifest.key")),
		valueEnvironment("AGENTSERVER_V2_CODEX_RUNTIME_MANIFEST_SHA256", document.Runtime.RuntimeManifestSHA256),
		valueEnvironment("AGENTSERVER_V2_CHECKPOINT_ALLOWLIST_VERSION", strconv.Itoa(document.Runtime.CheckpointAllowlistVersion)),
		valueEnvironment("AGENTSERVER_V2_HARNESS_WORKER_SERVICE_ACCOUNT", harnessComponent),
		valueEnvironment("AGENTSERVER_V2_HARNESS_PRIVILEGED_FORK", "true"),
		valueEnvironment("AGENTSERVER_V2_HARNESS_WORKER_UID", strconv.FormatUint(uint64(WorkerUID), 10)),
		valueEnvironment("AGENTSERVER_V2_HARNESS_WORKER_GID", strconv.FormatUint(uint64(WorkerGID), 10)),
		valueEnvironment("AGENTSERVER_V2_HARNESS_APP_UID", strconv.FormatUint(uint64(AppUID), 10)),
		valueEnvironment("AGENTSERVER_V2_HARNESS_APP_GID", strconv.FormatUint(uint64(AppGID), 10)),
		valueEnvironment("AGENTSERVER_V2_EXECUTOR_MCP_ENDPOINT", internalOrigin(ExecutorInternalHost, document.Services.ExecutorGateway.InternalPort)+"/mcp"),
		valueEnvironment("AGENTSERVER_V2_EXECUTOR_GATEWAY_SPIFFE_ID", spiffeIdentity(config, executorComponent)),
		// Stock Codex treats this as an API base URL and appends /responses.
		// The workspace-configured third-party URL is the separate exact
		// /v1/responses authority resolved inside llmproxy by Core.
		valueEnvironment("AGENTSERVER_V2_LLMPROXY_ENDPOINT", internalOrigin(LLMProxyInternalHost, document.Services.LLMProxy.Port)+"/v1"),
		valueEnvironment("AGENTSERVER_V2_LLMPROXY_SPIFFE_ID", spiffeIdentity(config, llmproxyComponent)),
		valueEnvironment("AGENTSERVER_V2_HARNESS_MAX_CONCURRENT_ATTEMPTS", strconv.Itoa(document.Runtime.MaxConcurrentAttempts)),
		valueEnvironment("AGENTSERVER_V2_MAX_RUN_DURATION", document.Runtime.MaxRunDuration),
		valueEnvironment("AGENTSERVER_V2_MAX_APPROVAL_TTL", document.Runtime.MaxApprovalTTL),
	}
	if managedExecutionActive(document.Managed) {
		environment = append(environment,
			valueEnvironment("AGENTSERVER_V2_MANAGED_ENVIRONMENT_ID", document.Managed.Environment.EnvironmentID),
			valueEnvironment("AGENTSERVER_V2_MANAGED_RUNTIME_PROFILE_SHA256", document.Managed.Environment.RuntimeProfileSHA256),
			valueEnvironment("AGENTSERVER_V2_MANAGED_PACK_SET_SHA256", document.Managed.Environment.PackSetSHA256),
			valueEnvironment("AGENTSERVER_V2_MANAGED_SANDBOX_TTL", document.Managed.Environment.SandboxTTL),
			valueEnvironment("AGENTSERVER_V2_MANAGED_ACTIVITY_TTL", document.Managed.Environment.ActivityTTL),
			valueEnvironment("AGENTSERVER_V2_SANDBOX_GATEWAY_URL", internalOrigin(SandboxInternalHost, document.Services.SandboxGateway.Port)),
			valueEnvironment("AGENTSERVER_V2_SANDBOX_GATEWAY_CA_FILE", poolMaterialPath("ca.crt")),
			valueEnvironment("AGENTSERVER_V2_SANDBOX_GATEWAY_SERVER_NAME", SandboxInternalHost),
			valueEnvironment("AGENTSERVER_V2_SANDBOX_LIFECYCLE_CAPABILITY_ISSUER", spiffeIdentity(config, harnessComponent)),
			valueEnvironment("AGENTSERVER_V2_SANDBOX_LIFECYCLE_CAPABILITY_KEY_ID", ProductionSandboxLifecycleKeyID),
			valueEnvironment("AGENTSERVER_V2_SANDBOX_LIFECYCLE_CAPABILITY_SIGNING_KEY_FILE", poolMaterialPath("sandbox-lifecycle-capability.key")),
		)
		if managedLarkEnabled(document.Managed) {
			environment = append(environment, valueEnvironment("AGENTSERVER_V2_MANAGED_LARK_SKILL_SHA256", document.Managed.Lark.SkillSHA256))
		}
	}
	environment = append(environment, objectStoreEnvironment(document)...)
	configVolume := configMapVolume("harness-config", context.harnessConfigName, map[string]string{
		"worker-deployment.json": "worker-deployment.json",
		"network-guard.json":     "network-guard.json",
	})
	hosts := map[string]string{
		CoreInternalHost:     document.Services.Core.ClusterIP,
		ExecutorInternalHost: document.Services.ExecutorGateway.ClusterIP,
		LLMProxyInternalHost: document.Services.LLMProxy.ClusterIP,
	}
	if managedExecutionActive(document.Managed) {
		hosts[SandboxInternalHost] = document.Services.SandboxGateway.ClusterIP
	}
	return deployment(deploymentInput{
		namespace: document.Namespace, platform: document.Platform, component: harnessComponent, replicas: document.Replicas.HarnessPool,
		image: document.Images.Harness, serviceAccount: harnessComponent,
		command: []any{"/usr/local/bin/harness-pool"}, args: []any{"serve"}, environment: environment,
		initContainers: []any{
			prepareHarnessDirectoriesInitContainer(document.Images.Harness),
			networkGuardInitContainer(document.Images.Harness),
		},
		volumes: []any{
			poolMaterial, workerMaterial, configVolume,
			emptyDirVolume("runtime", "Memory", document.Resources.RuntimeTmpfs),
			emptyDirVolume("checkpoint", "Memory", document.Resources.CheckpointTmpfs),
			emptyDirVolume("scratch", "Memory", document.Resources.ScratchTmpfs),
		},
		volumeMounts: append(append(poolMaterialMounts, workerMaterialMounts...),
			kubeObject{"name": "harness-config", "mountPath": "/etc/agentserver/worker-deployment.json", "subPath": "worker-deployment.json", "readOnly": true},
			kubeObject{"name": "runtime", "mountPath": "/var/lib/agentserver/runtime"},
			kubeObject{"name": "checkpoint", "mountPath": "/var/lib/agentserver/checkpoint"},
			kubeObject{"name": "scratch", "mountPath": "/tmp"},
		),
		hostAliases: hosts,
		// The pool is fixed platform code and must retain these effective
		// capabilities across runtimes that discard added capabilities for a
		// non-root container entrypoint. Workers and app processes still switch
		// to their fixed non-root credentials before executing harness or Codex.
		resources: document.Resources.HarnessPool, uid: 0, gid: 0, fsGroup: PoolGID,
		capabilities: []string{"CHOWN", "SETUID", "SETGID", "DAC_OVERRIDE"},
		strategy:     "RollingUpdate", configHash: context.harnessDeploymentHash, termination: 45,
	}), nil
}

func renderSandboxDeployment(context renderContext) (kubeObject, error) {
	config := context.config
	document := config.Document
	taeSandboxImage, err := taeimage.ContentTagForRepository(ProductionTAEManagedSandboxImage, document.Images.ManagedSandbox)
	if err != nil {
		return nil, err
	}
	material, err := secretMaterialVolume("material", document.Secrets.SandboxGateway, materialProfileSandboxGateway, groupReadableSecretMode)
	if err != nil {
		return nil, err
	}
	mounts, err := secretMaterialMounts("material", "/var/run/agentserver/material", materialProfileSandboxGateway)
	if err != nil {
		return nil, err
	}
	environment := []any{
		valueEnvironment("AGENTSERVER_V2_SANDBOX_GATEWAY_LISTEN_ADDR", listenAddress(document.Services.SandboxGateway.Port)),
		valueEnvironment("AGENTSERVER_V2_SANDBOX_GATEWAY_TLS_CERT_FILE", serviceMaterialPath("tls.crt")),
		valueEnvironment("AGENTSERVER_V2_SANDBOX_GATEWAY_TLS_KEY_FILE", serviceMaterialPath("tls.key")),
		valueEnvironment("AGENTSERVER_V2_SANDBOX_GATEWAY_CLIENT_CA_FILE", serviceMaterialPath("ca.crt")),
		valueEnvironment("AGENTSERVER_V2_SANDBOX_GATEWAY_SPIFFE_ID", spiffeIdentity(config, sandboxComponent)),
		valueEnvironment("AGENTSERVER_V2_EXECUTOR_GATEWAY_SPIFFE_ID", spiffeIdentity(config, executorComponent)),
		valueEnvironment("AGENTSERVER_V2_HARNESS_POOL_SPIFFE_ID", spiffeIdentity(config, harnessComponent)),
		valueEnvironment("AGENTSERVER_V2_CORE_URL", internalOrigin(CoreInternalHost, document.Services.Core.Port)),
		valueEnvironment("AGENTSERVER_V2_CORE_CA_FILE", serviceMaterialPath("ca.crt")),
		valueEnvironment("AGENTSERVER_V2_CORE_CLIENT_CERT_FILE", serviceMaterialPath("tls.crt")),
		valueEnvironment("AGENTSERVER_V2_CORE_CLIENT_KEY_FILE", serviceMaterialPath("tls.key")),
		valueEnvironment("AGENTSERVER_V2_CORE_SERVER_NAME", CoreInternalHost),
		valueEnvironment("AGENTSERVER_V2_SANDBOX_CAPABILITY_KEYRING_FILE", serviceMaterialPath("sandbox-capability-keyring.json")),
		valueEnvironment("AGENTSERVER_V2_SANDBOX_PROVIDER", "tae"),
		valueEnvironment("AGENTSERVER_V2_TAE_REGION", document.Managed.TAE.Region),
		valueEnvironment("AGENTSERVER_V2_TAE_PSM", document.Managed.TAE.PSM),
		valueEnvironment("AGENTSERVER_V2_TAE_SANDBOX_IMAGE", taeSandboxImage),
		valueEnvironment("AGENTSERVER_V2_TAE_SANDBOX_ID", document.Managed.TAE.SandboxID),
		valueEnvironment("AGENTSERVER_V2_TAE_SANDBOX_REVISION_ID", document.Managed.TAE.RevisionID),
		valueEnvironment("AGENTSERVER_V2_TAE_AUTH_MODE", "bytecloud-app-aksk-v1"),
		valueEnvironment("AGENTSERVER_V2_TAE_BYTECLOUD_SITE", "i18n-tt"),
		valueEnvironment("AGENTSERVER_V2_TAE_BYTECLOUD_JWT_ENDPOINT", ProductionByteCloudJWTEndpoint),
		valueEnvironment("AGENTSERVER_V2_TAE_PROXY_URL", ProductionTAEProxyURL),
		valueEnvironment("AGENTSERVER_V2_TAE_BYTECLOUD_ACCESS_KEY_ID_FILE", serviceMaterialPath("bytecloud-access-key-id")),
		valueEnvironment("AGENTSERVER_V2_TAE_BYTECLOUD_SECRET_ACCESS_KEY_FILE", serviceMaterialPath("bytecloud-secret-access-key")),
		valueEnvironment("AGENTSERVER_V2_TAE_BYTECLOUD_JWT_TIMEOUT", "5s"),
		valueEnvironment("AGENTSERVER_V2_MANAGED_IDLE_TTL", document.Managed.Environment.IdleTTL),
		valueEnvironment("AGENTSERVER_V2_MANAGED_SANDBOX_ROOT", document.Managed.Environment.Root.Path),
		valueEnvironment("AGENTSERVER_V2_MANAGED_SANDBOX_PLATFORM", document.Platform),
		valueEnvironment("AGENTSERVER_V2_MANAGED_WORKSPACE_ALLOWLIST", strings.Join(document.Managed.WorkspaceAllowlist, ",")),
		valueEnvironment("AGENTSERVER_V2_SANDBOX_ENSURE_TIMEOUT", "45s"),
		valueEnvironment("AGENTSERVER_V2_SANDBOX_ENSURE_POLL_INTERVAL", "250ms"),
		valueEnvironment("AGENTSERVER_V2_SANDBOX_RECONCILE_INTERVAL", "30s"),
		valueEnvironment("AGENTSERVER_V2_SANDBOX_RECONCILE_LIMIT", "100"),
	}
	environment = append(environment, managedTAEPolicyEnvironment(document.Managed.TAE)...)
	return deployment(deploymentInput{
		namespace: document.Namespace, platform: document.Platform, component: sandboxComponent,
		replicas: document.Replicas.SandboxGateway, image: document.Images.Service, serviceAccount: sandboxComponent,
		command: []any{"/usr/local/bin/sandbox-gateway"}, args: []any{"serve"}, environment: environment,
		volumes:      []any{material, emptyDirVolume("scratch", "Memory", document.Resources.ScratchTmpfs)},
		volumeMounts: append(mounts, kubeObject{"name": "scratch", "mountPath": "/tmp"}),
		hostAliases:  map[string]string{CoreInternalHost: document.Services.Core.ClusterIP},
		resources:    document.Resources.SandboxGateway, uid: ServiceUID, gid: ServiceGID, fsGroup: ServiceGID,
		strategy: "RollingUpdate", configHash: context.documentHash, termination: 45,
	}), nil
}

func managedTAEPolicyEnvironment(tae ManagedTAEDocument) []any {
	policy := tae.Policy
	environment := []any{
		valueEnvironment("AGENTSERVER_V2_TAE_POLICY_REVISION", policy.Revision),
		valueEnvironment("AGENTSERVER_V2_TAE_POLICY_SHA256", policy.PolicySHA256),
		valueEnvironment("AGENTSERVER_V2_TAE_POLICY_BINDING_SHA256", policy.BindingSHA256),
		valueEnvironment("AGENTSERVER_V2_TAE_POLICY_HOST", policy.PublicHost),
		valueEnvironment("AGENTSERVER_V2_TAE_POLICY_ACCESS", policy.PublicAccess),
		valueEnvironment("AGENTSERVER_V2_TAE_POLICY_WEBHOOK_REQUIRED", strconv.FormatBool(policy.PublicWebhookRequired)),
		valueEnvironment("AGENTSERVER_V2_TAE_POLICY_PUBLISHED", strconv.FormatBool(policy.Published)),
		valueEnvironment("AGENTSERVER_V2_TAE_POLICY_APPROVED", strconv.FormatBool(policy.Approved)),
		valueEnvironment("AGENTSERVER_V2_TAE_POLICY_EVIDENCE_REF", policy.EvidenceRef),
		valueEnvironment("AGENTSERVER_V2_TAE_NETWORK_BINDING_SHA256", tae.NetworkEvidence.BindingSHA256),
		valueEnvironment("AGENTSERVER_V2_TAE_NETWORK_REPORT_SHA256", tae.NetworkEvidence.ReportSHA256),
		valueEnvironment("AGENTSERVER_V2_TAE_NETWORK_EVIDENCE_REF", tae.NetworkEvidence.EvidenceRef),
	}
	if policy.PublicWebhookRequired {
		environment = append(environment,
			valueEnvironment("AGENTSERVER_V2_TAE_WEBHOOK_MODE", policy.WebhookMode),
			valueEnvironment("AGENTSERVER_V2_TAE_WEBHOOK_PATH", policy.WebhookPath),
		)
		if policy.WebhookMode == "psm" {
			environment = append(environment, valueEnvironment("AGENTSERVER_V2_TAE_WEBHOOK_PSM", policy.WebhookPSM))
		} else {
			environment = append(environment, valueEnvironment("AGENTSERVER_V2_TAE_WEBHOOK_URL", policy.WebhookURL))
		}
	}
	return environment
}

func renderEgressAuthorizerDeployment(context renderContext) (kubeObject, error) {
	config := context.config
	document := config.Document
	bootstrap := managedPolicyBootstrap(document.Managed)
	materialProfile := materialProfileEgressAuthorizer
	if bootstrap {
		materialProfile = materialProfileEgressBootstrap
	}
	material, err := secretMaterialVolume("material", document.Secrets.EgressAuthorizer, materialProfile, groupReadableSecretMode)
	if err != nil {
		return nil, err
	}
	mounts, err := secretMaterialMounts("material", "/var/run/agentserver/material", materialProfile)
	if err != nil {
		return nil, err
	}
	environment := []any{
		valueEnvironment("AGENTSERVER_V2_EGRESS_AUTHORIZER_LISTEN_ADDR", listenAddress(document.Services.EgressAuthorizer.Port)),
		valueEnvironment("AGENTSERVER_V2_EGRESS_AUTHORIZER_TLS_CERT_FILE", serviceMaterialPath("tls.crt")),
		valueEnvironment("AGENTSERVER_V2_EGRESS_AUTHORIZER_TLS_KEY_FILE", serviceMaterialPath("tls.key")),
		valueEnvironment("AGENTSERVER_V2_EGRESS_AUTHORIZER_SPIFFE_ID", spiffeIdentity(config, egressComponent)),
	}
	args := []any{"serve", "--policy-bootstrap"}
	hosts := map[string]string(nil)
	if !bootstrap {
		environment = append(environment,
			valueEnvironment("AGENTSERVER_V2_CORE_URL", internalOrigin(CoreInternalHost, document.Services.Core.Port)),
			valueEnvironment("AGENTSERVER_V2_CORE_CA_FILE", serviceMaterialPath("ca.crt")),
			valueEnvironment("AGENTSERVER_V2_CORE_CLIENT_CERT_FILE", serviceMaterialPath("tls.crt")),
			valueEnvironment("AGENTSERVER_V2_CORE_CLIENT_KEY_FILE", serviceMaterialPath("tls.key")),
			valueEnvironment("AGENTSERVER_V2_CORE_SERVER_NAME", CoreInternalHost),
			valueEnvironment("AGENTSERVER_V2_EGRESS_PLACEHOLDER_KEYRING_FILE", serviceMaterialPath("egress-placeholder-keyring.json")),
			valueEnvironment("AGENTSERVER_V2_EGRESS_ALLOWED_TAE_PSM", document.Managed.TAE.PSM),
			valueEnvironment("AGENTSERVER_V2_EGRESS_DECISION_TIMEOUT", "350ms"),
		)
		environment = append(environment, managedTAEPolicyEnvironment(document.Managed.TAE)...)
		args = []any{"serve"}
		hosts = map[string]string{CoreInternalHost: document.Services.Core.ClusterIP}
	}
	return deployment(deploymentInput{
		namespace: document.Namespace, platform: document.Platform, component: egressComponent,
		replicas: document.Replicas.EgressAuthorizer, image: document.Images.Service, serviceAccount: egressComponent,
		command: []any{"/usr/local/bin/egress-authorizer"}, args: args, environment: environment,
		volumes:      []any{material, emptyDirVolume("scratch", "Memory", document.Resources.ScratchTmpfs)},
		volumeMounts: append(mounts, kubeObject{"name": "scratch", "mountPath": "/tmp"}),
		hostAliases:  hosts,
		resources:    document.Resources.EgressAuthorizer, uid: ServiceUID, gid: ServiceGID, fsGroup: ServiceGID,
		strategy: "RollingUpdate", configHash: context.documentHash, termination: 20,
	}), nil
}

func renderLLMProxyDeployment(context renderContext) (kubeObject, error) {
	config := context.config
	document := config.Document
	material, err := secretMaterialVolume("material", document.Secrets.LLMProxy, materialProfileLLMProxy, groupReadableSecretMode)
	if err != nil {
		return nil, err
	}
	materialMounts, err := secretMaterialMounts("material", "/var/run/agentserver/material", materialProfileLLMProxy)
	if err != nil {
		return nil, err
	}
	environment := []any{
		valueEnvironment("AGENTSERVER_V2_LLMPROXY_LISTEN_ADDR", listenAddress(document.Services.LLMProxy.Port)),
		valueEnvironment("AGENTSERVER_V2_LLMPROXY_TLS_CERT_FILE", serviceMaterialPath("tls.crt")),
		valueEnvironment("AGENTSERVER_V2_LLMPROXY_TLS_KEY_FILE", serviceMaterialPath("tls.key")),
		valueEnvironment("AGENTSERVER_V2_LLMPROXY_SPIFFE_ID", spiffeIdentity(config, llmproxyComponent)),
		valueEnvironment("AGENTSERVER_V2_CORE_URL", internalOrigin(CoreInternalHost, document.Services.Core.Port)),
		valueEnvironment("AGENTSERVER_V2_CORE_CA_FILE", serviceMaterialPath("ca.crt")),
		valueEnvironment("AGENTSERVER_V2_CORE_SERVER_NAME", CoreInternalHost),
		valueEnvironment("AGENTSERVER_V2_RUN_CAPABILITY_ISSUER", document.Runtime.CapabilityIssuer),
		valueEnvironment("AGENTSERVER_V2_RUN_CAPABILITY_KEYRING_FILE", serviceMaterialPath("run-capability-keyring.json")),
	}
	return deployment(deploymentInput{
		namespace: document.Namespace, platform: document.Platform, component: llmproxyComponent, replicas: document.Replicas.LLMProxy,
		image: document.Images.Service, serviceAccount: llmproxyComponent,
		command: []any{"/usr/local/bin/llmproxy"}, args: []any{"serve"}, environment: environment,
		volumes:      []any{material, emptyDirVolume("scratch", "Memory", document.Resources.ScratchTmpfs)},
		volumeMounts: append(materialMounts, kubeObject{"name": "scratch", "mountPath": "/tmp"}),
		hostAliases:  map[string]string{CoreInternalHost: document.Services.Core.ClusterIP},
		resources:    document.Resources.LLMProxy, uid: ServiceUID, gid: ServiceGID, fsGroup: ServiceGID,
		strategy: "RollingUpdate", configHash: context.documentHash, termination: 30,
	}), nil
}

func deployment(input deploymentInput) kubeObject {
	labels := componentLabels(input.component)
	annotations := map[string]string{
		"agentserver.dev/config-sha256":                  input.configHash,
		"cluster-autoscaler.kubernetes.io/safe-to-evict": "false",
	}
	podSecurity := kubeObject{"seccompProfile": kubeObject{"type": "RuntimeDefault"}}
	if input.fsGroup != 0 {
		podSecurity["fsGroup"] = int64(input.fsGroup)
		podSecurity["fsGroupChangePolicy"] = "OnRootMismatch"
	}
	strategy := kubeObject{"type": input.strategy}
	if input.strategy == "RollingUpdate" {
		strategy["rollingUpdate"] = kubeObject{"maxUnavailable": 0, "maxSurge": 1}
	}
	ports := input.ports
	if len(ports) == 0 {
		ports = []any{kubeObject{"name": "https", "containerPort": HarnessControlPort, "protocol": "TCP"}}
	}
	probePort := input.probePort
	if probePort == 0 {
		probePort = HarnessControlPort
	}
	container := kubeObject{
		"name": input.component, "image": input.image, "imagePullPolicy": "IfNotPresent",
		"command": input.command, "args": input.args, "env": input.environment,
		"ports":           ports,
		"resources":       resources(input.resources),
		"securityContext": runtimeSecurityContext(input.uid, input.gid, input.capabilities...),
		"volumeMounts":    input.volumeMounts,
		"startupProbe":    execProbe("127.0.0.1:" + strconv.Itoa(int(probePort))),
		"readinessProbe":  execProbe("127.0.0.1:" + strconv.Itoa(int(probePort))),
		"livenessProbe":   execProbe("127.0.0.1:" + strconv.Itoa(int(probePort))),
	}
	podSpec := kubeObject{
		"serviceAccountName": input.serviceAccount, "automountServiceAccountToken": false,
		"enableServiceLinks": false, "terminationGracePeriodSeconds": input.termination,
		"securityContext": podSecurity, "nodeSelector": productionNodeSelector(input.platform), "containers": []any{container},
		"volumes": input.volumes,
		"topologySpreadConstraints": []any{kubeObject{
			"maxSkew": 1, "topologyKey": "kubernetes.io/hostname", "whenUnsatisfiable": "ScheduleAnyway",
			"labelSelector": kubeObject{"matchLabels": selectorLabels(input.component)},
		}},
	}
	if len(input.initContainers) != 0 {
		podSpec["initContainers"] = input.initContainers
	}
	if len(input.hostAliases) != 0 {
		podSpec["hostAliases"] = hostAliases(input.hostAliases)
	}
	return kubeObject{
		"apiVersion": "apps/v1", "kind": "Deployment",
		"metadata": metadata(input.component, input.namespace, componentLabels(input.component), map[string]string{
			"reloader.stakater.com/auto": "true",
		}),
		"spec": kubeObject{
			"replicas": input.replicas, "revisionHistoryLimit": 3, "progressDeadlineSeconds": 600,
			"strategy": strategy, "selector": kubeObject{"matchLabels": selectorLabels(input.component)},
			"template": kubeObject{"metadata": kubeObject{"labels": labels, "annotations": annotations}, "spec": podSpec},
		},
	}
}

func podDisruptionBudget(config LoadedConfig, component string, replicas int) kubeObject {
	minAvailable := 1
	if replicas > 2 {
		minAvailable = replicas - 1
	}
	return kubeObject{
		"apiVersion": "policy/v1", "kind": "PodDisruptionBudget",
		"metadata": metadata(component, config.Document.Namespace, componentLabels(component), nil),
		"spec": kubeObject{
			"minAvailable": minAvailable,
			"selector":     kubeObject{"matchLabels": selectorLabels(component)},
		},
	}
}

func objectStoreEnvironment(document ConfigDocument) []any {
	values := []any{
		valueEnvironment("AGENTSERVER_V2_OBJECT_PREFIX", document.Objects.Prefix),
		valueEnvironment("AGENTSERVER_V2_S3_BUCKET", document.Objects.S3Bucket),
		valueEnvironment("AGENTSERVER_V2_S3_REGION", document.Objects.S3Region),
		valueEnvironment("AGENTSERVER_V2_S3_USE_PATH_STYLE", strconv.FormatBool(document.Objects.S3UsePathStyle)),
		secretEnvironment("AGENTSERVER_V2_S3_ACCESS_KEY_ID", document.Secrets.ObjectStore, "access-key-id"),
		secretEnvironment("AGENTSERVER_V2_S3_SECRET_ACCESS_KEY", document.Secrets.ObjectStore, "secret-access-key"),
	}
	if document.Objects.S3Endpoint != "" {
		values = append(values, valueEnvironment("AGENTSERVER_V2_S3_ENDPOINT", document.Objects.S3Endpoint))
	}
	return values
}

func spiffeIdentity(config LoadedConfig, component string) string {
	return fmt.Sprintf("spiffe://%s/ns/%s/sa/%s", config.Document.TrustDomain, config.Document.Namespace, component)
}

func workerSPIFFEIdentity(config LoadedConfig) string {
	return fmt.Sprintf("spiffe://%s/ns/%s/workload/harness-worker", config.Document.TrustDomain, config.Document.Namespace)
}

func listenAddress(port uint16) string { return "0.0.0.0:" + strconv.Itoa(int(port)) }

func internalOrigin(host string, port uint16) string {
	return "https://" + host + ":" + strconv.Itoa(int(port))
}
