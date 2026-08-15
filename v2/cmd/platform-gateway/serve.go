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
	"net/url"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/agentserver/agentserver/v2/internal/browsergateway"
	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/platformgateway"
	platformweb "github.com/agentserver/agentserver/v2/platform-web"
)

const (
	platformListenAddressEnvironment         = "AGENTSERVER_V2_PLATFORM_GATEWAY_LISTEN_ADDR"
	platformPublicOriginEnvironment          = "AGENTSERVER_V2_PLATFORM_PUBLIC_ORIGIN"
	platformBrowserOriginEnvironment         = "AGENTSERVER_V2_BROWSER_FRONTEND_ORIGIN"
	platformCoreURLEnvironment               = "AGENTSERVER_V2_CORE_URL"
	platformCoreCAEnvironment                = "AGENTSERVER_V2_CORE_CA_FILE"
	platformCoreClientCertificateEnvironment = "AGENTSERVER_V2_CORE_CLIENT_CERT_FILE"
	platformCoreClientKeyEnvironment         = "AGENTSERVER_V2_CORE_CLIENT_KEY_FILE"
	platformCoreServerNameEnvironment        = "AGENTSERVER_V2_CORE_SERVER_NAME"
	platformOAuthClientIDEnvironment         = "AGENTSERVER_V2_PLATFORM_OAUTH_CLIENT_ID"
	platformOAuthAudienceEnvironment         = "AGENTSERVER_V2_PLATFORM_OAUTH_AUDIENCE"
	platformOAuthScopesEnvironment           = "AGENTSERVER_V2_PLATFORM_OAUTH_SCOPES"
	platformOAuthAuthorizationEnvironment    = "AGENTSERVER_V2_PLATFORM_OAUTH_AUTHORIZATION_ENDPOINT"
	platformOAuthTokenEnvironment            = "AGENTSERVER_V2_PLATFORM_OAUTH_TOKEN_ENDPOINT"
)

const platformShutdownTimeout = 10 * time.Second

type platformReadiness struct{ ready atomic.Bool }

func servePlatformGateway(ctx context.Context, getenv func(string) string, stdout io.Writer) error {
	listenAddress, err := requiredPlatformConfiguration(getenv, platformListenAddressEnvironment)
	if err != nil {
		return err
	}
	publicOrigin, err := requiredExactHTTPSOrigin(getenv, platformPublicOriginEnvironment)
	if err != nil {
		return err
	}
	browserOrigin, err := requiredExactHTTPSOrigin(getenv, platformBrowserOriginEnvironment)
	if err != nil {
		return err
	}
	if publicOrigin == browserOrigin {
		return errors.New("Platform and Browser public origins must be distinct")
	}
	coreURL, err := requiredPlatformConfiguration(getenv, platformCoreURLEnvironment)
	if err != nil {
		return err
	}
	if err := validatePlatformCoreURL(coreURL); err != nil {
		return err
	}
	coreCAFile, err := requiredPlatformConfiguration(getenv, platformCoreCAEnvironment)
	if err != nil {
		return err
	}
	coreClientCertificateFile, err := requiredPlatformConfiguration(getenv, platformCoreClientCertificateEnvironment)
	if err != nil {
		return err
	}
	coreClientKeyFile, err := requiredPlatformConfiguration(getenv, platformCoreClientKeyEnvironment)
	if err != nil {
		return err
	}
	coreServerName, err := requiredPlatformConfiguration(getenv, platformCoreServerNameEnvironment)
	if err != nil {
		return err
	}
	oauthClientID, err := requiredPlatformConfiguration(getenv, platformOAuthClientIDEnvironment)
	if err != nil {
		return err
	}
	oauthAudience, err := requiredPlatformConfiguration(getenv, platformOAuthAudienceEnvironment)
	if err != nil {
		return err
	}
	oauthScopeText, err := requiredPlatformConfiguration(getenv, platformOAuthScopesEnvironment)
	if err != nil {
		return err
	}
	oauthScopes, err := validatePlatformOAuthAuthority(oauthClientID, oauthAudience, oauthScopeText)
	if err != nil {
		return err
	}
	authorizationEndpoint, err := requiredPlatformConfiguration(getenv, platformOAuthAuthorizationEnvironment)
	if err != nil {
		return err
	}
	tokenEndpoint, err := requiredPlatformConfiguration(getenv, platformOAuthTokenEnvironment)
	if err != nil {
		return err
	}

	coreClient, err := newPlatformCoreHTTPClient(coreCAFile, coreClientCertificateFile, coreClientKeyFile, coreServerName)
	if err != nil {
		return err
	}
	defer coreClient.CloseIdleConnections()
	backend, err := browsergateway.NewCoreRunBackend(coreURL, coreClient)
	if err != nil {
		return err
	}
	executors, err := browsergateway.NewExecutorResourceHandler(backend, browsergateway.ExecutorResourceHandlerConfig{})
	if err != nil {
		return err
	}
	resources, err := platformgateway.NewResourceProxy(coreURL, coreClient)
	if err != nil {
		return err
	}
	credentials, err := platformgateway.NewWorkspaceCredentialRoutes(coreURL, coreClient)
	if err != nil {
		return err
	}
	llmGateways, err := browsergateway.NewWorkspaceLLMGatewayProxy(coreURL, coreClient)
	if err != nil {
		return err
	}
	authBridge, err := browsergateway.NewAuthBridgeProxy(coreURL, coreClient)
	if err != nil {
		return err
	}
	authConfig, err := platformgateway.NewAuthorizationConfigHandlerWithEndpoints(
		oauthClientID, oauthAudience, oauthScopes, authorizationEndpoint, tokenEndpoint,
	)
	if err != nil {
		return err
	}
	oauthURL, _ := url.Parse(tokenEndpoint)
	oauthOrigin := oauthURL.Scheme + "://" + oauthURL.Host
	web, err := platformweb.HandlerForOAuthOrigin(oauthOrigin)
	if err != nil {
		return err
	}

	readiness := &platformReadiness{}
	handler, err := platformGatewayRoutes(
		resources.Routes(), credentials.Routes(), executors.Routes(), llmGateways.Routes(), authBridge.Routes(), authConfig,
		browsergateway.NewLLMGatewayCallbackHandler(), web, readiness, publicOrigin, oauthOrigin,
	)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", listenAddress)
	if err != nil {
		return fmt.Errorf("listen on platform-gateway address: %w", err)
	}
	defer listener.Close()
	server := &http.Server{
		Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second,
		IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 * 1024,
	}
	serveContext, cancelServe := context.WithCancel(context.Background())
	defer cancelServe()
	go func() {
		select {
		case <-ctx.Done():
			readiness.ready.Store(false)
			shutdownContext, cancel := context.WithTimeout(context.Background(), platformShutdownTimeout)
			defer cancel()
			if err := server.Shutdown(shutdownContext); err != nil {
				_ = server.Close()
			}
		case <-serveContext.Done():
		}
	}()
	readiness.ready.Store(true)
	fmt.Fprintf(stdout, "platform-gateway serve: listening with HTTP behind Istio TLS on %s; %s at /\n", listener.Addr(), platformweb.AssetSummary())
	err = server.Serve(listener)
	readiness.ready.Store(false)
	if errors.Is(err, http.ErrServerClosed) && ctx.Err() != nil {
		return nil
	}
	return err
}

func validatePlatformOAuthAuthority(clientID, audience, commaSeparatedScopes string) ([]string, error) {
	scopes := strings.Split(commaSeparatedScopes, ",")
	expected := corecontract.PlatformOAuthScopes()
	if clientID != corecontract.PlatformOAuthClientID || audience != corecontract.PlatformOAuthAudience || !slices.Equal(scopes, expected) {
		return nil, fmt.Errorf(
			"platform OAuth authority must be client %q, audience %q, and canonical scopes %q",
			corecontract.PlatformOAuthClientID, corecontract.PlatformOAuthAudience, strings.Join(expected, ","),
		)
	}
	return scopes, nil
}

func platformGatewayRoutes(
	resources, credentials, executors, llmGateways, authBridge, authConfig, llmCallback, web http.Handler,
	readiness *platformReadiness,
	publicOrigin, authOrigin string,
) (http.Handler, error) {
	publicURL, err := url.Parse(publicOrigin)
	if err != nil {
		return nil, errors.New("Platform public origin is invalid")
	}
	authURL, err := url.Parse(authOrigin)
	if err != nil || authURL.Hostname() == "" || authURL.Hostname() == publicURL.Hostname() {
		return nil, errors.New("Authentication public origin is invalid or not distinct from Platform")
	}
	platformMux := http.NewServeMux()
	platformMux.Handle(corecontract.WorkspaceCollectionRoutePattern, resources)
	platformMux.Handle(corecontract.WorkspaceResourceRoutePattern, resources)
	platformMux.Handle(corecontract.WorkspaceArchiveRoutePattern, resources)
	platformMux.Handle(corecontract.WorkspaceManagedSandboxRoutePattern, resources)
	platformMux.Handle(corecontract.WorkspaceMembersCollectionPattern, resources)
	platformMux.Handle(corecontract.WorkspaceMemberResourceRoutePattern, resources)
	platformMux.Handle(corecontract.WorkspaceCredentialProviderSchemasPath, credentials)
	platformMux.Handle(corecontract.WorkspaceCredentialCollectionRoutePattern, credentials)
	platformMux.Handle(corecontract.WorkspaceCredentialResourceRoutePattern, credentials)
	platformMux.Handle(corecontract.WorkspaceCredentialAuthorizationCollectionRoutePattern, credentials)
	platformMux.Handle(corecontract.WorkspaceCredentialAuthorizationResourceRoutePattern, credentials)
	platformMux.Handle("GET "+corecontract.ExecutorManagementRoutePattern, executors)
	platformMux.Handle("POST "+corecontract.ExecutorManagementRoutePattern, executors)
	platformMux.Handle("POST "+corecontract.ExecutorEnrollmentTokenRoutePattern, executors)
	platformMux.Handle("DELETE "+corecontract.ExecutorEnrollmentTokenRoutePattern, executors)
	executorMethodGuard := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Allow", "GET, POST, DELETE")
		writePlatformError(response, http.StatusMethodNotAllowed, "method_not_allowed", "executor resource method is not allowed")
	})
	platformMux.Handle(corecontract.ExecutorManagementRoutePattern, executorMethodGuard)
	platformMux.Handle(corecontract.ExecutorEnrollmentTokenRoutePattern, executorMethodGuard)
	platformMux.Handle(corecontract.LLMGatewayCollectionRoutePattern, llmGateways)
	platformMux.Handle(corecontract.LLMGatewayActionRoutePattern, llmGateways)
	platformMux.Handle(corecontract.LLMGatewayOIDCCallbackPath, llmCallback)
	platformMux.Handle("GET /auth/config", authConfig)
	platformMux.HandleFunc("GET /healthz", func(response http.ResponseWriter, _ *http.Request) {
		writePlatformHealth(response, http.StatusOK, `{"status":"ok"}`)
	})
	platformMux.HandleFunc("GET /readyz", func(response http.ResponseWriter, _ *http.Request) {
		if readiness == nil || !readiness.ready.Load() {
			writePlatformHealth(response, http.StatusServiceUnavailable, `{"status":"not_ready"}`)
			return
		}
		writePlatformHealth(response, http.StatusOK, `{"status":"ready"}`)
	})
	for _, path := range []string{
		corecontract.PublicHydraLoginPath, corecontract.PublicHydraConsentPath, corecontract.PublicOIDCCallbackPath,
	} {
		platformMux.Handle(path, http.NotFoundHandler())
	}
	platformMux.Handle("/", web)

	authMux := http.NewServeMux()
	for _, path := range []string{
		corecontract.PublicHydraLoginPath, corecontract.PublicHydraConsentPath, corecontract.PublicOIDCCallbackPath,
	} {
		authMux.Handle("GET "+path, authBridge)
	}
	platformHandler := platformPublicBoundary(platformMux, publicURL.Hostname(), publicOrigin)
	authHandler := platformPublicBoundary(authMux, authURL.Hostname(), authOrigin)
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch canonicalPlatformHostname(request.Host) {
		case publicURL.Hostname():
			platformHandler.ServeHTTP(response, request)
		case authURL.Hostname():
			authHandler.ServeHTTP(response, request)
		default:
			writePlatformError(response, http.StatusNotFound, "not_found", "route is not exposed on this host")
		}
	}), nil
}

func platformPublicBoundary(next http.Handler, hostname, publicOrigin string) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if canonicalPlatformHostname(request.Host) != hostname {
			writePlatformError(response, http.StatusNotFound, "not_found", "platform route is not exposed on this host")
			return
		}
		origins := request.Header.Values("Origin")
		if len(origins) > 1 {
			writePlatformError(response, http.StatusForbidden, "origin_not_allowed", "request origin is not allowed")
			return
		}
		if len(origins) == 0 || origins[0] == publicOrigin {
			next.ServeHTTP(response, request)
			return
		}
		writePlatformError(response, http.StatusForbidden, "origin_not_allowed", "request origin is not allowed")
	})
}

func requiredExactHTTPSOrigin(getenv func(string) string, name string) (string, error) {
	raw, err := requiredPlatformConfiguration(getenv, name)
	if err != nil {
		return "", err
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil ||
		parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.String() != raw {
		return "", fmt.Errorf("%s must be an exact HTTPS origin", name)
	}
	return raw, nil
}

func validatePlatformCoreURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return fmt.Errorf("%s must be an HTTPS origin without credentials, path, query, or fragment", platformCoreURLEnvironment)
	}
	return nil
}

func newPlatformCoreHTTPClient(caFile, certificateFile, keyFile, serverName string) (*http.Client, error) {
	certificate, err := tls.LoadX509KeyPair(certificateFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load platform-gateway Core client identity: %w", err)
	}
	rootCAs, err := loadPlatformRootCAs(caFile, "Core")
	if err != nil {
		return nil, err
	}
	return newPlatformHTTPClient(&tls.Config{
		MinVersion: tls.VersionTLS13, RootCAs: rootCAs, Certificates: []tls.Certificate{certificate}, ServerName: serverName,
	}), nil
}

func loadPlatformRootCAs(path, authority string) (*x509.CertPool, error) {
	caPEM, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s server CA: %w", authority, err)
	}
	rootCAs := x509.NewCertPool()
	if !rootCAs.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("%s server CA file contains no certificates", authority)
	}
	return rootCAs, nil
}

func newPlatformHTTPClient(tlsConfig *tls.Config) *http.Client {
	transport := &http.Transport{
		DialContext:       (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2: true, MaxIdleConns: 32, MaxIdleConnsPerHost: 32, IdleConnTimeout: 60 * time.Second,
		TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 30 * time.Second, ExpectContinueTimeout: time.Second,
		TLSClientConfig: tlsConfig,
	}
	return &http.Client{Transport: transport, Timeout: 30 * time.Second}
}

func canonicalPlatformHostname(host string) string {
	if parsed, _, err := net.SplitHostPort(host); err == nil {
		host = parsed
	}
	return strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
}

func writePlatformError(response http.ResponseWriter, status int, code, message string) {
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(status)
	_, _ = fmt.Fprintf(response, "{\"error\":{\"code\":%q,\"message\":%q}}\n", code, message)
}

func writePlatformHealth(response http.ResponseWriter, status int, body string) {
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(status)
	_, _ = io.WriteString(response, body+"\n")
}

func requiredPlatformConfiguration(getenv func(string) string, name string) (string, error) {
	value := strings.TrimSpace(getenv(name))
	if value == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return value, nil
}
