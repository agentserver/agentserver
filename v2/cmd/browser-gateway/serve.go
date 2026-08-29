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

	a2uiweb "github.com/agentserver/agentserver/v2/a2ui-web"
	"github.com/agentserver/agentserver/v2/internal/browsergateway"
	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

const (
	browserListenAddressEnvironment              = "AGENTSERVER_V2_BROWSER_GATEWAY_LISTEN_ADDR"
	browserTLSCertificateEnvironment             = "AGENTSERVER_V2_BROWSER_GATEWAY_TLS_CERT_FILE"
	browserTLSKeyEnvironment                     = "AGENTSERVER_V2_BROWSER_GATEWAY_TLS_KEY_FILE"
	browserCoreURLEnvironment                    = "AGENTSERVER_V2_CORE_URL"
	browserCoreCAEnvironment                     = "AGENTSERVER_V2_CORE_CA_FILE"
	browserCoreClientCertificateEnvironment      = "AGENTSERVER_V2_CORE_CLIENT_CERT_FILE"
	browserCoreClientKeyEnvironment              = "AGENTSERVER_V2_CORE_CLIENT_KEY_FILE"
	browserCoreServerNameEnvironment             = "AGENTSERVER_V2_CORE_SERVER_NAME"
	browserHydraPublicUpstreamEnvironment        = "AGENTSERVER_V2_HYDRA_PUBLIC_UPSTREAM"
	browserHydraCAEnvironment                    = "AGENTSERVER_V2_HYDRA_CA_FILE"
	browserHydraServerNameEnvironment            = "AGENTSERVER_V2_HYDRA_SERVER_NAME"
	browserDevelopmentOIDCUpstreamEnvironment    = "AGENTSERVER_V2_DEVELOPMENT_OIDC_AUTHORIZATION_UPSTREAM"
	browserOAuthClientIDEnvironment              = "AGENTSERVER_V2_BROWSER_OAUTH_CLIENT_ID"
	browserOAuthAudienceEnvironment              = "AGENTSERVER_V2_BROWSER_OAUTH_AUDIENCE"
	browserOAuthScopesEnvironment                = "AGENTSERVER_V2_BROWSER_OAUTH_SCOPES"
	browserOAuthAuthorizationEndpointEnvironment = "AGENTSERVER_V2_BROWSER_OAUTH_AUTHORIZATION_ENDPOINT"
	browserOAuthTokenEndpointEnvironment         = "AGENTSERVER_V2_BROWSER_OAUTH_TOKEN_ENDPOINT"
	browserFrontendOriginEnvironment             = "AGENTSERVER_V2_BROWSER_FRONTEND_ORIGIN"
	browserAPIOriginEnvironment                  = "AGENTSERVER_V2_BROWSER_API_ORIGIN"
)

const browserShutdownTimeout = 10 * time.Second

type browserReadiness struct {
	ready atomic.Bool
}

func serveBrowserGateway(ctx context.Context, getenv func(string) string, stdout io.Writer) error {
	listenAddress, err := requiredBrowserConfiguration(getenv, browserListenAddressEnvironment)
	if err != nil {
		return err
	}
	frontendOrigin, apiOrigin, splitPublicOrigins, err := browserPublicOrigins(getenv)
	if err != nil {
		return err
	}
	var certificateFile, keyFile string
	if !splitPublicOrigins {
		certificateFile, err = requiredBrowserConfiguration(getenv, browserTLSCertificateEnvironment)
		if err != nil {
			return err
		}
		keyFile, err = requiredBrowserConfiguration(getenv, browserTLSKeyEnvironment)
		if err != nil {
			return err
		}
	}
	coreURL, err := requiredBrowserConfiguration(getenv, browserCoreURLEnvironment)
	if err != nil {
		return err
	}
	if err := validateBrowserCoreURL(coreURL); err != nil {
		return err
	}
	coreCAFile, err := requiredBrowserConfiguration(getenv, browserCoreCAEnvironment)
	if err != nil {
		return err
	}
	coreClientCertificateFile, err := requiredBrowserConfiguration(getenv, browserCoreClientCertificateEnvironment)
	if err != nil {
		return err
	}
	coreClientKeyFile, err := requiredBrowserConfiguration(getenv, browserCoreClientKeyEnvironment)
	if err != nil {
		return err
	}
	browserOAuthClientID, err := requiredBrowserConfiguration(getenv, browserOAuthClientIDEnvironment)
	if err != nil {
		return err
	}
	browserOAuthAudience, err := requiredBrowserConfiguration(getenv, browserOAuthAudienceEnvironment)
	if err != nil {
		return err
	}
	browserOAuthScopes, err := requiredBrowserConfiguration(getenv, browserOAuthScopesEnvironment)
	if err != nil {
		return err
	}
	canonicalBrowserScopes, err := validateBrowserOAuthAuthority(browserOAuthAudience, browserOAuthScopes)
	if err != nil {
		return err
	}
	authorizationEndpoint := "/oauth2/auth"
	tokenEndpoint := "/oauth2/token"
	if splitPublicOrigins {
		authorizationEndpoint, err = requiredBrowserConfiguration(getenv, browserOAuthAuthorizationEndpointEnvironment)
		if err != nil {
			return err
		}
		tokenEndpoint, err = requiredBrowserConfiguration(getenv, browserOAuthTokenEndpointEnvironment)
		if err != nil {
			return err
		}
	}
	coreHTTPClient, err := newBrowserCoreHTTPClient(
		coreCAFile,
		coreClientCertificateFile,
		coreClientKeyFile,
		strings.TrimSpace(getenv(browserCoreServerNameEnvironment)),
	)
	if err != nil {
		return err
	}
	defer coreHTTPClient.CloseIdleConnections()
	backend, err := browsergateway.NewCoreRunBackend(coreURL, coreHTTPClient)
	if err != nil {
		return err
	}
	authConfig, err := browsergateway.NewBrowserAuthorizationConfigHandlerWithEndpoints(
		browserOAuthClientID, browserOAuthAudience, canonicalBrowserScopes, apiOrigin,
		authorizationEndpoint, tokenEndpoint,
	)
	if err != nil {
		return err
	}
	aguiHandler, err := browsergateway.NewAGUIHandler(backend, browsergateway.DefaultHandlerConfig())
	if err != nil {
		return err
	}
	sessionProxy, err := browsergateway.NewSessionResourceProxy(backend)
	if err != nil {
		return err
	}
	conversationAPI := browserConversationRoutes(aguiHandler.Routes(), sessionProxy.Routes())
	readiness := &browserReadiness{}
	referenceHandler := a2uiweb.Handler()
	if splitPublicOrigins {
		oauthURL, parseErr := url.Parse(tokenEndpoint)
		if parseErr != nil {
			return parseErr
		}
		referenceHandler, err = a2uiweb.HandlerForConnectionOrigins(apiOrigin, oauthURL.Scheme+"://"+oauthURL.Host)
		if err != nil {
			return err
		}
	}
	var handler http.Handler
	if splitPublicOrigins {
		handler = browserGatewaySplitRoutes(
			conversationAPI, authConfig, readiness, referenceHandler, frontendOrigin, apiOrigin,
		)
	} else {
		hydraPublicUpstream, err := requiredBrowserConfiguration(getenv, browserHydraPublicUpstreamEnvironment)
		if err != nil {
			return err
		}
		llmGatewayProxy, err := browsergateway.NewWorkspaceLLMGatewayProxy(coreURL, coreHTTPClient)
		if err != nil {
			return err
		}
		authProxy, err := browsergateway.NewAuthBridgeProxy(coreURL, coreHTTPClient)
		if err != nil {
			return err
		}
		hydraProxy, err := browsergateway.NewHydraPublicProxy(hydraPublicUpstream, frontendOrigin, &http.Client{Timeout: 10 * time.Second})
		if err != nil {
			return err
		}
		developmentOIDCHandler := http.NotFoundHandler()
		if upstream := strings.TrimSpace(getenv(browserDevelopmentOIDCUpstreamEnvironment)); upstream != "" {
			developmentOIDCProxy, err := browsergateway.NewDevelopmentOIDCAuthorizationProxy(
				upstream, &http.Client{Timeout: 10 * time.Second},
			)
			if err != nil {
				return err
			}
			developmentOIDCHandler = developmentOIDCProxy.Routes()
		}
		executorHandler, err := browsergateway.NewExecutorResourceHandler(backend, browsergateway.ExecutorResourceHandlerConfig{})
		if err != nil {
			return err
		}
		handler = browserGatewayRoutesWithReference(
			conversationAPI, executorHandler.Routes(), llmGatewayProxy.Routes(), authProxy.Routes(), authConfig,
			hydraProxy.Routes(), developmentOIDCHandler, readiness, referenceHandler,
		)
	}
	var tlsConfig *tls.Config
	if !splitPublicOrigins {
		tlsConfig, err = browserGatewayTLSConfig(certificateFile, keyFile)
		if err != nil {
			return err
		}
	}
	listener, err := net.Listen("tcp", listenAddress)
	if err != nil {
		return fmt.Errorf("listen on browser-gateway address: %w", err)
	}
	defer listener.Close()

	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    32 * 1024,
		TLSConfig:         tlsConfig,
	}
	serveContext, cancelServe := context.WithCancel(context.Background())
	defer cancelServe()
	go func() {
		select {
		case <-ctx.Done():
			readiness.ready.Store(false)
			shutdownContext, cancel := context.WithTimeout(context.Background(), browserShutdownTimeout)
			defer cancel()
			if err := server.Shutdown(shutdownContext); err != nil {
				_ = server.Close()
			}
		case <-serveContext.Done():
		}
	}()

	readiness.ready.Store(true)
	listenerDescription := "TLS"
	if splitPublicOrigins {
		listenerDescription = "HTTP behind the configured Istio TLS gateway"
	}
	fmt.Fprintf(
		stdout,
		"browser-gateway serve: listening with %s on %s; AG-UI endpoint /v2/workspaces/{workspaceId}/sessions/{sessionId}/agui; %s at /\n",
		listenerDescription, listener.Addr(), a2uiweb.AssetSummary(),
	)
	if splitPublicOrigins {
		err = server.Serve(listener)
	} else {
		err = server.Serve(tls.NewListener(listener, tlsConfig))
	}
	readiness.ready.Store(false)
	if errors.Is(err, http.ErrServerClosed) && ctx.Err() != nil {
		return nil
	}
	return err
}

func validateBrowserOAuthAuthority(audience, commaSeparatedScopes string) ([]string, error) {
	scopes := strings.Split(commaSeparatedScopes, ",")
	expected := corecontract.BrowserOAuthScopes()
	if audience != corecontract.BrowserOAuthAudience || !slices.Equal(scopes, expected) {
		return nil, fmt.Errorf(
			"browser OAuth authority must be audience %q and canonical scopes %q",
			corecontract.BrowserOAuthAudience, strings.Join(expected, ","),
		)
	}
	return scopes, nil
}

func browserGatewayRoutes(agui, executors, llmGateways, auth, authConfig, hydra, developmentOIDC http.Handler, readiness *browserReadiness) http.Handler {
	return browserGatewayRoutesWithReference(agui, executors, llmGateways, auth, authConfig, hydra, developmentOIDC, readiness, a2uiweb.Handler())
}

func browserGatewayRoutesWithReference(
	agui, executors, llmGateways, auth, authConfig, hydra, developmentOIDC http.Handler,
	readiness *browserReadiness,
	reference http.Handler,
) http.Handler {
	mux := http.NewServeMux()
	mountBrowserAPIRoutes(mux, agui, executors, llmGateways)
	mountBrowserFrontendRoutes(mux, auth, authConfig, hydra, developmentOIDC)
	mountBrowserHealthRoutes(mux, readiness)
	mux.Handle("/", reference)
	return mux
}

func mountBrowserAPIRoutes(mux *http.ServeMux, agui, executors, llmGateways http.Handler) {
	mux.Handle("POST "+corecontract.ExecutorManagementRoutePattern, executors)
	mux.Handle("POST "+corecontract.ExecutorEnrollmentTokenRoutePattern, executors)
	executorMethodGuard := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Allow", http.MethodPost)
		writeBrowserRouteError(response, http.StatusMethodNotAllowed, "method_not_allowed", "executor resource endpoints require POST")
	})
	mux.Handle(corecontract.ExecutorManagementRoutePattern, executorMethodGuard)
	mux.Handle(corecontract.ExecutorEnrollmentTokenRoutePattern, executorMethodGuard)
	mux.Handle(corecontract.LLMGatewayCollectionRoutePattern, llmGateways)
	mux.Handle(corecontract.LLMGatewayActionRoutePattern, llmGateways)
	mountBrowserConversationAPIRoutes(mux, agui)
}

func mountBrowserConversationAPIRoutes(mux *http.ServeMux, agui http.Handler) {
	mux.Handle("/v2/", agui)
}

func browserConversationRoutes(agui, sessions http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle(corecontract.UserSessionCollectionRoutePattern, sessions)
	mux.Handle(corecontract.UserSessionResourceRoutePattern, sessions)
	mux.Handle(corecontract.UserSessionPermissionModeRoutePattern, sessions)
	mux.Handle(corecontract.UserSessionWorkingDirectoryRoutePattern, sessions)
	mux.Handle(corecontract.UserSessionTranscriptRoutePattern, sessions)
	mux.Handle(corecontract.UserSessionTrajectoryRoutePattern, sessions)
	mux.Handle(corecontract.UserSessionArchiveRoutePattern, sessions)
	mux.Handle("/v2/", agui)
	return mux
}

func mountBrowserFrontendRoutes(mux *http.ServeMux, auth, authConfig, hydra, developmentOIDC http.Handler) {
	mux.Handle(corecontract.LLMGatewayOIDCCallbackPath, browsergateway.NewLLMGatewayCallbackHandler())
	mux.Handle("/auth/", auth)
	mux.Handle("GET /auth/config", authConfig)
	mux.Handle("GET /auth/idp/authorize", developmentOIDC)
	mux.Handle("/oauth2/", hydra)
}

func mountBrowserHealthRoutes(mux *http.ServeMux, readiness *browserReadiness) {
	mux.HandleFunc("GET /healthz", func(response http.ResponseWriter, _ *http.Request) {
		writeHealth(response, http.StatusOK, `{"status":"ok"}`)
	})
	mux.HandleFunc("GET /readyz", func(response http.ResponseWriter, _ *http.Request) {
		if readiness == nil || !readiness.ready.Load() {
			writeHealth(response, http.StatusServiceUnavailable, `{"status":"not_ready"}`)
			return
		}
		writeHealth(response, http.StatusOK, `{"status":"ready"}`)
	})
}

func browserPublicOrigins(getenv func(string) string) (frontend, api string, split bool, err error) {
	frontend = strings.TrimSpace(getenv(browserFrontendOriginEnvironment))
	api = strings.TrimSpace(getenv(browserAPIOriginEnvironment))
	if frontend == "" && api == "" {
		return "", "", false, nil
	}
	if frontend == "" || api == "" {
		return "", "", false, errors.New("browser frontend and API origins must either both be configured or both be absent")
	}
	for name, raw := range map[string]string{
		browserFrontendOriginEnvironment: frontend,
		browserAPIOriginEnvironment:      api,
	} {
		parsed, parseErr := url.Parse(raw)
		if parseErr != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil ||
			parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.String() != raw {
			return "", "", false, fmt.Errorf("%s must be an exact HTTPS origin", name)
		}
	}
	if frontend == api {
		return "", "", false, errors.New("browser frontend and API origins must be distinct in split-origin mode")
	}
	return frontend, api, true, nil
}

func browserGatewaySplitRoutes(
	agui, authConfig http.Handler,
	readiness *browserReadiness,
	reference http.Handler,
	frontendOrigin, apiOrigin string,
) http.Handler {
	frontendURL, _ := url.Parse(frontendOrigin)
	apiURL, _ := url.Parse(apiOrigin)
	frontend := http.NewServeMux()
	frontend.Handle("GET /auth/config", authConfig)
	mountBrowserHealthRoutes(frontend, readiness)
	frontend.Handle("/", reference)
	api := http.NewServeMux()
	mountBrowserConversationAPIRoutes(api, agui)
	apiHandler := browserCORSMiddleware(api, frontendOrigin)
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		host := canonicalRequestHostname(request)
		switch host {
		case frontendURL.Hostname():
			frontend.ServeHTTP(response, request)
		case apiURL.Hostname():
			apiHandler.ServeHTTP(response, request)
		default:
			writeBrowserRouteError(response, http.StatusNotFound, "not_found", "browser route is not exposed on this host")
		}
	})
}

func canonicalRequestHostname(request *http.Request) string {
	if request == nil {
		return ""
	}
	host := request.Host
	if parsed, _, err := net.SplitHostPort(host); err == nil {
		host = parsed
	}
	return strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
}

func browserCORSMiddleware(next http.Handler, allowedOrigin string) http.Handler {
	allowedHeaders := map[string]struct{}{
		"accept": {}, "authorization": {}, "content-type": {}, "idempotency-key": {},
	}
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		origins := request.Header.Values("Origin")
		if len(origins) > 1 || (len(origins) == 1 && origins[0] != allowedOrigin) {
			writeBrowserRouteError(response, http.StatusForbidden, "origin_not_allowed", "browser API origin is not allowed")
			return
		}
		if len(origins) == 1 {
			response.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
			response.Header().Add("Vary", "Origin")
		}
		if request.Method == http.MethodOptions {
			requestedMethod := request.Header.Get("Access-Control-Request-Method")
			if len(origins) != 1 || (requestedMethod != http.MethodGet && requestedMethod != http.MethodPost && requestedMethod != http.MethodPatch) {
				writeBrowserRouteError(response, http.StatusForbidden, "invalid_preflight", "browser API preflight is not allowed")
				return
			}
			for _, raw := range strings.Split(request.Header.Get("Access-Control-Request-Headers"), ",") {
				name := strings.ToLower(strings.TrimSpace(raw))
				if name == "" {
					continue
				}
				if _, found := allowedHeaders[name]; !found {
					writeBrowserRouteError(response, http.StatusForbidden, "invalid_preflight", "browser API preflight requested an unsupported header")
					return
				}
			}
			response.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH")
			response.Header().Set("Access-Control-Allow-Headers", "Accept, Authorization, Content-Type, Idempotency-Key")
			response.Header().Set("Access-Control-Max-Age", "600")
			response.Header().Add("Vary", "Access-Control-Request-Method")
			response.Header().Add("Vary", "Access-Control-Request-Headers")
			response.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(response, request)
	})
}

func writeBrowserRouteError(response http.ResponseWriter, status int, code, message string) {
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(status)
	_, _ = fmt.Fprintf(response, "{\"error\":{\"code\":%q,\"message\":%q}}\n", code, message)
}

func writeHealth(response http.ResponseWriter, status int, body string) {
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(status)
	_, _ = io.WriteString(response, body+"\n")
}

func validateBrowserCoreURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return fmt.Errorf("%s must be an HTTPS origin without credentials, path, query, or fragment", browserCoreURLEnvironment)
	}
	return nil
}

func newBrowserCoreHTTPClient(caFile, certificateFile, keyFile, serverName string) (*http.Client, error) {
	certificate, err := tls.LoadX509KeyPair(certificateFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load browser-gateway core client identity: %w", err)
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
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          32,
		MaxIdleConnsPerHost:   32,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: time.Second,
		TLSClientConfig: &tls.Config{
			MinVersion:   tls.VersionTLS13,
			RootCAs:      rootCAs,
			Certificates: []tls.Certificate{certificate},
			ServerName:   serverName,
		},
	}
	return &http.Client{Transport: transport}, nil
}

func newBrowserHydraHTTPClient(caFile, serverName string) (*http.Client, error) {
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
		MaxIdleConns:          32,
		MaxIdleConnsPerHost:   32,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: time.Second,
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS13,
			RootCAs:    rootCAs,
			ServerName: serverName,
		},
	}
	return &http.Client{Transport: transport, Timeout: 10 * time.Second}, nil
}

func browserGatewayTLSConfig(certificateFile, keyFile string) (*tls.Config, error) {
	certificate, err := tls.LoadX509KeyPair(certificateFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load browser-gateway TLS identity: %w", err)
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{certificate},
		NextProtos:   []string{"h2", "http/1.1"},
	}, nil
}

func requiredBrowserConfiguration(getenv func(string) string, name string) (string, error) {
	value := strings.TrimSpace(getenv(name))
	if value == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return value, nil
}
