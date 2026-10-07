package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/agentserver/agentserver/v2/internal/executionbackend"
	"github.com/agentserver/agentserver/v2/internal/sandboxgatewayapp"
	"github.com/agentserver/agentserver/v2/providers/kubernetes/adapter"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "k8s-sandbox-gateway:", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	config, err := sandboxgatewayapp.LoadProductionConfig(os.Getenv)
	if err != nil {
		return err
	}
	if config.ProviderKind != executionbackend.KindKubernetes {
		return errors.New("this binary requires k8s provider")
	}
	kubeconfig, err := rest.InClusterConfig()
	if err != nil {
		return errors.New("in-cluster Kubernetes identity required")
	}
	kubeconfig.Timeout = 15 * time.Second
	kubeconfig.QPS = 10
	kubeconfig.Burst = 20
	kube, err := dynamic.NewForConfig(kubeconfig)
	if err != nil {
		return errors.New("configure Kubernetes client failed")
	}
	cert, err := tls.LoadX509KeyPair(os.Getenv("AGENTSERVER_V2_RUNTIME_CLIENT_CERT_FILE"), os.Getenv("AGENTSERVER_V2_RUNTIME_CLIENT_KEY_FILE"))
	if err != nil {
		return errors.New("runtime client identity unavailable")
	}
	ca, err := os.ReadFile(os.Getenv("AGENTSERVER_V2_RUNTIME_CA_FILE"))
	if err != nil {
		return errors.New("runtime server CA unavailable")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return errors.New("invalid runtime CA")
	}
	serverName := os.Getenv("AGENTSERVER_V2_RUNTIME_SERVER_NAME")
	if serverName == "" {
		return errors.New("runtime server name required")
	}
	transport := &http.Transport{Proxy: nil, ForceAttemptHTTP2: true, DisableCompression: true,
		DialContext:         (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 15 * time.Second, IdleConnTimeout: 30 * time.Second,
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, Certificates: []tls.Certificate{cert}, ServerName: serverName},
	}
	defer transport.CloseIdleConnections()
	runtime, err := adapter.NewHTTPRuntimeClient(&http.Client{Transport: transport})
	if err != nil {
		return err
	}
	provider, err := adapter.New(kube, runtime, adapter.Config{Namespace: os.Getenv("AGENTSERVER_V2_SANDBOX_NAMESPACE"), Pool: os.Getenv("AGENTSERVER_V2_SANDBOX_POOL"), Region: config.ProviderRegion, Scope: config.ProviderPSM, ClusterDomain: os.Getenv("AGENTSERVER_V2_CLUSTER_DOMAIN"), RuntimePort: 8443})
	if err != nil {
		return err
	}
	return sandboxgatewayapp.Serve(ctx, config, provider, os.Stdout, os.Stderr)
}
