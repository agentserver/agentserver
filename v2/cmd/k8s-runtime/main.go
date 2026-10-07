package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"github.com/agentserver/agentserver/v2/internal/k8sruntime"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "agentserver-k8s-runtime:", err)
		os.Exit(1)
	}
}
func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	gateway := os.Getenv("AGENTSERVER_SANDBOX_GATEWAY_IDENTITY")
	app, err := k8sruntime.New(k8sruntime.Config{PodUID: os.Getenv("AGENTSERVER_SANDBOX_POD_UID"), GatewayIdentity: gateway, Workspace: "/workspace", Bwrap: "/usr/local/bin/bwrap"})
	if err != nil {
		return err
	}
	defer app.Close()
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	err = app.Probe(probeCtx)
	cancel()
	if err != nil {
		return err
	}
	cert, err := tls.LoadX509KeyPair("/var/run/agentserver/runtime-tls/tls.crt", "/var/run/agentserver/runtime-tls/tls.key")
	if err != nil {
		return errors.New("runtime TLS identity unavailable")
	}
	ca, err := os.ReadFile("/var/run/agentserver/runtime-tls/ca.crt")
	if err != nil {
		return errors.New("runtime CA unavailable")
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		return errors.New("invalid runtime CA")
	}
	server := &http.Server{Addr: ":8443", Handler: app, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 * 1024, TLSConfig: &tls.Config{
		MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}, ClientCAs: pool, ClientAuth: tls.VerifyClientCertIfGiven,
	}}
	finished := make(chan error, 1)
	go func() { finished <- server.ListenAndServeTLS("", "") }()
	select {
	case err := <-finished:
		return err
	case <-ctx.Done():
	}
	app.Close()
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdown); err != nil {
		_ = server.Close()
		return err
	}
	if err := <-finished; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
