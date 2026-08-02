package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	"github.com/agentserver/agentserver/v2/internal/llmproxy"
	"github.com/agentserver/agentserver/v2/internal/publichttps"
	"github.com/agentserver/agentserver/v2/internal/runcapability"
)

const (
	llmProxyShutdownTimeout = 30 * time.Second
	maximumLLMProxyTLSBytes = int64(1024 * 1024)
)

type llmProxyReadiness struct {
	ready atomic.Bool
}

func serveLLMProxy(ctx context.Context, getenv func(string) string, stdout io.Writer) error {
	return serveLLMProxyWithUpstreamHTTPClient(ctx, getenv, stdout, nil)
}

func serveLLMProxyWithUpstreamHTTPClient(
	ctx context.Context,
	getenv func(string) string,
	stdout io.Writer,
	upstreamHTTPClient *http.Client,
) error {
	if ctx == nil {
		return errors.New("llmproxy serve context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	config, err := loadLLMProxyConfig(getenv)
	if err != nil {
		return err
	}
	identity, err := loadLLMProxyIdentity(config.tlsCertificate, config.tlsKey, config.spiffeIdentity)
	if err != nil {
		return err
	}
	verifier, err := runcapability.LoadProductionVerifier(config.capabilityIssuer, config.capabilityKeyring)
	if err != nil {
		return fmt.Errorf("configure llmproxy production run capability verifier: %w", err)
	}
	coreHTTPClient, err := newLLMProxyCoreHTTPClient(config.coreCA, config.coreServerName, identity)
	if err != nil {
		return err
	}
	defer coreHTTPClient.CloseIdleConnections()
	coreClient, err := llmproxy.NewCoreClient(config.coreURL, coreHTTPClient)
	if err != nil {
		return err
	}
	authenticator, err := llmproxy.NewProductionAuthenticator(
		verifier, coreClient, time.Now,
	)
	if err != nil {
		return err
	}
	if upstreamHTTPClient == nil {
		upstreamHTTPClient, err = newLLMProxyUpstreamHTTPClient()
		if err != nil {
			return err
		}
	}
	defer upstreamHTTPClient.CloseIdleConnections()
	modelHandler, err := llmproxy.NewHandler(llmproxy.HandlerConfig{
		Authenticator: authenticator, HTTPClient: upstreamHTTPClient,
	})
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", config.listenAddress)
	if err != nil {
		return fmt.Errorf("listen on llmproxy address: %w", err)
	}
	defer listener.Close()
	readiness := &llmProxyReadiness{}
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{identity},
		NextProtos: []string{"h2", "http/1.1"},
	}
	server := &http.Server{
		Handler:           llmProxyRoutes(modelHandler, readiness),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    32 * 1024,
		TLSConfig:         tlsConfig,
		ErrorLog:          log.New(io.Discard, "", 0),
	}
	serveContext, cancelServe := context.WithCancel(context.Background())
	defer cancelServe()
	go func() {
		select {
		case <-ctx.Done():
			readiness.ready.Store(false)
			shutdownContext, cancel := context.WithTimeout(context.Background(), llmProxyShutdownTimeout)
			defer cancel()
			if err := server.Shutdown(shutdownContext); err != nil {
				_ = server.Close()
			}
		case <-serveContext.Done():
		}
	}()
	readiness.ready.Store(true)
	fmt.Fprintf(stdout, "llmproxy serve: production TLS endpoint %s%s; workspace gateway routes resolved live by Core\n",
		listener.Addr(), llmproxy.ResponsesPath)
	err = server.Serve(tls.NewListener(listener, tlsConfig))
	readiness.ready.Store(false)
	if errors.Is(err, http.ErrServerClosed) && ctx.Err() != nil {
		return nil
	}
	return err
}

func llmProxyRoutes(model http.Handler, readiness *llmProxyReadiness) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch {
		case request.URL.Path == llmproxy.ResponsesPath:
			model.ServeHTTP(response, request)
		case request.Method == http.MethodGet && request.URL.Path == "/healthz" && request.URL.RawPath == "" && request.URL.RawQuery == "":
			writeLLMProxyHealth(response, http.StatusOK, `{"status":"ok"}`)
		case request.Method == http.MethodGet && request.URL.Path == "/readyz" && request.URL.RawPath == "" && request.URL.RawQuery == "":
			if readiness == nil || !readiness.ready.Load() {
				writeLLMProxyHealth(response, http.StatusServiceUnavailable, `{"status":"not_ready"}`)
				return
			}
			writeLLMProxyHealth(response, http.StatusOK, `{"status":"ready"}`)
		default:
			writeLLMProxyHealth(response, http.StatusNotFound, `{"status":"not_found"}`)
		}
	})
}

func writeLLMProxyHealth(response http.ResponseWriter, status int, body string) {
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.WriteHeader(status)
	_, _ = io.WriteString(response, body+"\n")
}

func loadLLMProxyIdentity(certificatePath, keyPath, expectedIdentity string) (tls.Certificate, error) {
	certificateBytes, err := readLLMProxyFile("TLS certificate", certificatePath, maximumLLMProxyTLSBytes, false)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyBytes, err := readLLMProxyFile("TLS private key", keyPath, maximumLLMProxyTLSBytes, true)
	if err != nil {
		return tls.Certificate{}, err
	}
	defer clear(keyBytes)
	certificate, err := tls.X509KeyPair(certificateBytes, keyBytes)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("load llmproxy TLS identity: %w", err)
	}
	if len(certificate.Certificate) == 0 {
		return tls.Certificate{}, errors.New("llmproxy TLS certificate chain is empty")
	}
	leaf, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("parse llmproxy TLS leaf certificate: %w", err)
	}
	if len(leaf.URIs) != 1 || leaf.URIs[0].String() != expectedIdentity {
		return tls.Certificate{}, errors.New("llmproxy TLS leaf certificate does not contain the exact configured SPIFFE identity")
	}
	certificate.Leaf = leaf
	return certificate, nil
}

func readLLMProxyFile(label, path string, maximum int64, restricted bool) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open llmproxy %s: %w", label, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect llmproxy %s: %w", label, err)
	}
	if !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > maximum || (restricted && info.Mode().Perm()&0o077 != 0) {
		return nil, fmt.Errorf("llmproxy %s must be a bounded regular file with safe permissions", label)
	}
	raw, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil || int64(len(raw)) != info.Size() {
		clear(raw)
		return nil, fmt.Errorf("read stable llmproxy %s", label)
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(info, after) || info.Size() != after.Size() || !info.ModTime().Equal(after.ModTime()) {
		clear(raw)
		return nil, fmt.Errorf("llmproxy %s changed while it was being read", label)
	}
	return raw, nil
}

func newLLMProxyCoreHTTPClient(caPath, serverName string, identity tls.Certificate) (*http.Client, error) {
	roots, err := loadLLMProxyCertPool("Core CA", caPath)
	if err != nil {
		return nil, err
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
			MinVersion: tls.VersionTLS13, RootCAs: roots,
			Certificates: []tls.Certificate{identity}, ServerName: serverName,
		},
	}
	return &http.Client{Transport: transport}, nil
}

func newLLMProxyUpstreamHTTPClient() (*http.Client, error) {
	return publichttps.NewClient(publichttps.ClientConfig{
		NoOverallTimeout: true, ResponseHeaderTimeout: 60 * time.Second,
		MaxIdleConns: 64, MaxIdleConnsPerHost: 16,
	})
}

func loadLLMProxyCertPool(label, path string) (*x509.CertPool, error) {
	contents, err := readLLMProxyFile(label, path, maximumLLMProxyTLSBytes, false)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(contents) {
		return nil, fmt.Errorf("llmproxy %s contains no certificates", label)
	}
	return pool, nil
}
