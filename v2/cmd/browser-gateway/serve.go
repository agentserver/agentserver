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
	"strings"
	"sync/atomic"
	"time"

	a2uiweb "github.com/agentserver/agentserver/v2/a2ui-web"
	"github.com/agentserver/agentserver/v2/internal/browsergateway"
)

const (
	browserListenAddressEnvironment         = "AGENTSERVER_V2_BROWSER_GATEWAY_LISTEN_ADDR"
	browserTLSCertificateEnvironment        = "AGENTSERVER_V2_BROWSER_GATEWAY_TLS_CERT_FILE"
	browserTLSKeyEnvironment                = "AGENTSERVER_V2_BROWSER_GATEWAY_TLS_KEY_FILE"
	browserCoreURLEnvironment               = "AGENTSERVER_V2_CORE_URL"
	browserCoreCAEnvironment                = "AGENTSERVER_V2_CORE_CA_FILE"
	browserCoreClientCertificateEnvironment = "AGENTSERVER_V2_CORE_CLIENT_CERT_FILE"
	browserCoreClientKeyEnvironment         = "AGENTSERVER_V2_CORE_CLIENT_KEY_FILE"
	browserCoreServerNameEnvironment        = "AGENTSERVER_V2_CORE_SERVER_NAME"
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
	certificateFile, err := requiredBrowserConfiguration(getenv, browserTLSCertificateEnvironment)
	if err != nil {
		return err
	}
	keyFile, err := requiredBrowserConfiguration(getenv, browserTLSKeyEnvironment)
	if err != nil {
		return err
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
	aguiHandler, err := browsergateway.NewAGUIHandler(backend, browsergateway.DefaultHandlerConfig())
	if err != nil {
		return err
	}
	readiness := &browserReadiness{}
	handler := browserGatewayRoutes(aguiHandler.Routes(), readiness)
	tlsConfig, err := browserGatewayTLSConfig(certificateFile, keyFile)
	if err != nil {
		return err
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
	fmt.Fprintf(
		stdout,
		"browser-gateway serve: listening with TLS on %s; AG-UI endpoint /v2/workspaces/{workspaceId}/sessions/{sessionId}/agui; %s at /\n",
		listener.Addr(), a2uiweb.AssetSummary(),
	)
	err = server.Serve(tls.NewListener(listener, tlsConfig))
	readiness.ready.Store(false)
	if errors.Is(err, http.ErrServerClosed) && ctx.Err() != nil {
		return nil
	}
	return err
}

func browserGatewayRoutes(agui http.Handler, readiness *browserReadiness) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/v2/", agui)
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
	mux.Handle("/", a2uiweb.Handler())
	return mux
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
