package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/agentserver/agentserver/v2/internal/managedruntime"
)

const (
	shutdownTimeout = 5 * time.Second
)

func main() {
	os.Exit(run(os.Getenv, os.Stderr))
}

func run(getenv func(string) string, stderr io.Writer) int {
	port, err := runtimePort(getenv)
	if err != nil {
		fmt.Fprintf(stderr, "agentserver-tae-runtime: %v\n", err)
		return 2
	}
	listener, err := net.Listen("tcp6", net.JoinHostPort("::", strconv.Itoa(port)))
	if err != nil {
		fmt.Fprintf(stderr, "agentserver-tae-runtime: listen: %v\n", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	server := &http.Server{
		Handler:           runtimeHandler(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    16 * 1024,
	}
	if err := serve(ctx, listener, server); err != nil {
		fmt.Fprintf(stderr, "agentserver-tae-runtime: serve: %v\n", err)
		return 1
	}
	return 0
}

func runtimePort(getenv func(string) string) (int, error) {
	if getenv == nil {
		return 0, errors.New("environment reader is required")
	}
	raw := getenv(managedruntime.PortEnvironment)
	if raw == "" {
		return managedruntime.DefaultPort, nil
	}
	parsed, err := strconv.ParseUint(raw, 10, 16)
	if err != nil || parsed == 0 || strconv.FormatUint(parsed, 10) != raw {
		return 0, fmt.Errorf("%s must be a canonical integer between 1 and 65535", managedruntime.PortEnvironment)
	}
	return int(parsed), nil
}

func runtimeHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/ping", func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			response.Header().Set("Allow", "GET, HEAD")
			http.Error(response, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		response.Header().Set("Cache-Control", "no-store")
		response.Header().Set("Content-Type", "application/json")
		response.WriteHeader(http.StatusOK)
		if request.Method == http.MethodGet {
			_, _ = io.WriteString(response, `"pong"`)
		}
	})
	return mux
}

func serve(ctx context.Context, listener net.Listener, server *http.Server) error {
	if ctx == nil || listener == nil || server == nil {
		return errors.New("context, listener, and HTTP server are required")
	}
	result := make(chan error, 1)
	go func() {
		result <- server.Serve(listener)
	}()
	select {
	case err := <-result:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		shutdownErr := server.Shutdown(shutdownContext)
		serveErr := <-result
		if errors.Is(serveErr, http.ErrServerClosed) {
			serveErr = nil
		}
		return errors.Join(shutdownErr, serveErr)
	}
}
