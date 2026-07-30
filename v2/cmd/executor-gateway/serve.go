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
	"regexp"
	"strings"
	"time"

	"github.com/agentserver/agentserver/v2/internal/executorgateway"
)

const (
	gatewayListenAddressEnvironment         = "AGENTSERVER_V2_EXECUTOR_GATEWAY_LISTEN_ADDR"
	gatewayTLSCertificateEnvironment        = "AGENTSERVER_V2_EXECUTOR_GATEWAY_TLS_CERT_FILE"
	gatewayTLSKeyEnvironment                = "AGENTSERVER_V2_EXECUTOR_GATEWAY_TLS_KEY_FILE"
	gatewayCoreURLEnvironment               = "AGENTSERVER_V2_CORE_URL"
	gatewayCoreCAEnvironment                = "AGENTSERVER_V2_CORE_CA_FILE"
	gatewayCoreClientCertificateEnvironment = "AGENTSERVER_V2_CORE_CLIENT_CERT_FILE"
	gatewayCoreClientKeyEnvironment         = "AGENTSERVER_V2_CORE_CLIENT_KEY_FILE"
	gatewayCoreServerNameEnvironment        = "AGENTSERVER_V2_CORE_SERVER_NAME"
	gatewayDevExecutorIDEnvironment         = "AGENTSERVER_V2_DEV_EXECUTOR_ID"
	gatewayDevExecutorHeader                = "X-Agentserver-Dev-Executor-Id"
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
	handler, err := executorgateway.NewServer(
		devExecutorAuthenticator{executorID: devExecutorID},
		coreClient,
		executorgateway.DefaultServerConfig(gatewayInstanceID),
	)
	if err != nil {
		return err
	}
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
			httpShutdown := make(chan error, 1)
			go func() {
				httpShutdown <- server.Shutdown(shutdownContext)
			}()
			_ = handler.Shutdown(shutdownContext)
			<-httpShutdown
		case <-serveContext.Done():
		}
	}()
	fmt.Fprintf(stdout, "executor-gateway serve: INSECURE DEV authentication; listening on %s; gateway instance %s\n", listener.Addr(), gatewayInstanceID)
	err = server.Serve(tls.NewListener(listener, tlsConfig))
	if errors.Is(err, http.ErrServerClosed) && ctx.Err() != nil {
		return nil
	}
	return err
}

type devExecutorAuthenticator struct {
	executorID string
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
