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
	platformHydraPublicUpstreamEnvironment   = "AGENTSERVER_V2_HYDRA_PUBLIC_UPSTREAM"
	platformHydraCAEnvironment               = "AGENTSERVER_V2_HYDRA_CA_FILE"
	platformHydraServerNameEnvironment       = "AGENTSERVER_V2_HYDRA_SERVER_NAME"
	platformOAuthClientIDEnvironment         = "AGENTSERVER_V2_PLATFORM_OAUTH_CLIENT_ID"
	platformOAuthAudienceEnvironment         = "AGENTSERVER_V2_PLATFORM_OAUTH_AUDIENCE"
	platformOAuthScopesEnvironment           = "AGENTSERVER_V2_PLATFORM_OAUTH_SCOPES"
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
	hydraPublicUpstream, err := requiredPlatformConfiguration(getenv, platformHydraPublicUpstreamEnvironment)
	if err != nil {
		return err
	}
	hydraCAFile, err := requiredPlatformConfiguration(getenv, platformHydraCAEnvironment)
	if err != nil {
		return err
	}
	hydraServerName, err := requiredPlatformConfiguration(getenv, platformHydraServerNameEnvironment)
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
	llmGateways, err := browsergateway.NewWorkspaceLLMGatewayProxy(coreURL, coreClient)
	if err != nil {
		return err
	}
	authBridge, err := browsergateway.NewAuthBridgeProxy(coreURL, coreClient)
	if err != nil {
		return err
	}
	hydraClient, err := newPlatformHydraHTTPClient(hydraCAFile, hydraServerName)
	if err != nil {
		return err
	}
	defer hydraClient.CloseIdleConnections()
	hydra, err := browsergateway.NewHydraPublicProxy(hydraPublicUpstream, publicOrigin, hydraClient)
	if err != nil {
		return err
	}
	authConfig, err := platformgateway.NewAuthorizationConfigHandler(oauthClientID, oauthAudience, oauthScopes)
	if err != nil {
		return err
	}

	readiness := &platformReadiness{}
	handler, err := platformGatewayRoutes(
		executors.Routes(), llmGateways.Routes(), authBridge.Routes(), hydra.Routes(), authConfig,
		browsergateway.NewLLMGatewayCallbackHandler(), platformweb.Handler(), readiness, publicOrigin, browserOrigin,
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
	executors, llmGateways, authBridge, hydra, authConfig, llmCallback, web http.Handler,
	readiness *platformReadiness,
	publicOrigin, browserOrigin string,
) (http.Handler, error) {
	publicURL, err := url.Parse(publicOrigin)
	if err != nil {
		return nil, errors.New("Platform public origin is invalid")
	}
	browserURL, err := url.Parse(browserOrigin)
	if err != nil {
		return nil, errors.New("Browser public origin is invalid")
	}
	mux := http.NewServeMux()
	mux.Handle("POST "+corecontract.ExecutorManagementRoutePattern, executors)
	mux.Handle("POST "+corecontract.ExecutorEnrollmentTokenRoutePattern, executors)
	executorMethodGuard := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Allow", http.MethodPost)
		writePlatformError(response, http.StatusMethodNotAllowed, "method_not_allowed", "executor resource endpoints require POST")
	})
	mux.Handle(corecontract.ExecutorManagementRoutePattern, executorMethodGuard)
	mux.Handle(corecontract.ExecutorEnrollmentTokenRoutePattern, executorMethodGuard)
	mux.Handle(corecontract.LLMGatewayCollectionRoutePattern, llmGateways)
	mux.Handle(corecontract.LLMGatewayActionRoutePattern, llmGateways)
	mux.Handle(corecontract.LLMGatewayOIDCCallbackPath, llmCallback)
	mux.Handle("/auth/", authBridge)
	mux.Handle("GET /auth/config", authConfig)
	mux.Handle("/oauth2/", hydra)
	mux.HandleFunc("GET /healthz", func(response http.ResponseWriter, _ *http.Request) {
		writePlatformHealth(response, http.StatusOK, `{"status":"ok"}`)
	})
	mux.HandleFunc("GET /readyz", func(response http.ResponseWriter, _ *http.Request) {
		if readiness == nil || !readiness.ready.Load() {
			writePlatformHealth(response, http.StatusServiceUnavailable, `{"status":"not_ready"}`)
			return
		}
		writePlatformHealth(response, http.StatusOK, `{"status":"ready"}`)
	})
	mux.Handle("/", web)
	return platformPublicBoundary(mux, publicURL.Hostname(), publicOrigin, browserURL.String()), nil
}

func platformPublicBoundary(next http.Handler, hostname, publicOrigin, browserOrigin string) http.Handler {
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
		if origins[0] != browserOrigin || request.URL.Path != "/oauth2/token" {
			writePlatformError(response, http.StatusForbidden, "origin_not_allowed", "request origin is not allowed")
			return
		}
		response.Header().Set("Access-Control-Allow-Origin", browserOrigin)
		response.Header().Add("Vary", "Origin")
		if request.Method == http.MethodOptions {
			if request.Header.Get("Access-Control-Request-Method") != http.MethodPost {
				writePlatformError(response, http.StatusForbidden, "invalid_preflight", "token preflight method is not allowed")
				return
			}
			for _, raw := range strings.Split(request.Header.Get("Access-Control-Request-Headers"), ",") {
				name := strings.ToLower(strings.TrimSpace(raw))
				if name != "" && name != "accept" && name != "content-type" {
					writePlatformError(response, http.StatusForbidden, "invalid_preflight", "token preflight header is not allowed")
					return
				}
			}
			response.Header().Set("Access-Control-Allow-Methods", http.MethodPost)
			response.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type")
			response.Header().Set("Access-Control-Max-Age", "600")
			response.Header().Add("Vary", "Access-Control-Request-Method")
			response.Header().Add("Vary", "Access-Control-Request-Headers")
			response.WriteHeader(http.StatusNoContent)
			return
		}
		if request.Method != http.MethodPost {
			writePlatformError(response, http.StatusForbidden, "origin_not_allowed", "cross-origin token request must use POST")
			return
		}
		next.ServeHTTP(response, request)
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

func newPlatformHydraHTTPClient(caFile, serverName string) (*http.Client, error) {
	rootCAs, err := loadPlatformRootCAs(caFile, "Hydra")
	if err != nil {
		return nil, err
	}
	return newPlatformHTTPClient(&tls.Config{MinVersion: tls.VersionTLS13, RootCAs: rootCAs, ServerName: serverName}), nil
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
