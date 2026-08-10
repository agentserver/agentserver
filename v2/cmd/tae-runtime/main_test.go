package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/managedruntime"
)

func TestRuntimePortUsesFaaSContract(t *testing.T) {
	for name, value := range map[string]string{
		"zero": "0", "leading zero": "08080", "negative": "-1", "too large": "65536", "space": "8080 ", "text": "http",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := runtimePort(func(string) string { return value }); err == nil {
				t.Fatalf("runtimePort(%q) succeeded", value)
			}
		})
	}
	if port, err := runtimePort(func(string) string { return "" }); err != nil || port != managedruntime.DefaultPort {
		t.Fatalf("default runtime port = %d, %v", port, err)
	}
	if port, err := runtimePort(func(string) string { return "9002" }); err != nil || port != 9002 {
		t.Fatalf("configured runtime port = %d, %v", port, err)
	}
}

func TestRuntimeHandlerMatchesTerminalPing(t *testing.T) {
	handler := runtimeHandler()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/ping", nil))
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/json" ||
		response.Body.String() != `"pong"` {
		t.Fatalf("GET ping response = status:%d type:%q body:%q",
			response.Code, response.Header().Get("Content-Type"), response.Body.String())
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodHead, "/v1/ping", nil))
	if response.Code != http.StatusOK || response.Body.Len() != 0 {
		t.Fatalf("HEAD ping response = status:%d body:%q", response.Code, response.Body.String())
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/ping", strings.NewReader("ignored")))
	if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("POST ping response = status:%d allow:%q", response.Code, response.Header().Get("Allow"))
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/process/start", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("runtime unexpectedly implements SandboxD: status=%d", response.Code)
	}
}

func TestServeStopsOnContextCancellation(t *testing.T) {
	listener := newBlockingListener()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- serve(ctx, listener, &http.Server{Handler: runtimeHandler()})
	}()
	select {
	case <-listener.accepting:
	case <-time.After(time.Second):
		t.Fatal("runtime server did not begin accepting connections")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("runtime server did not stop after cancellation")
	}
}

type blockingListener struct {
	accepting chan struct{}
	closed    chan struct{}
	startOnce sync.Once
	closeOnce sync.Once
}

func newBlockingListener() *blockingListener {
	return &blockingListener{accepting: make(chan struct{}), closed: make(chan struct{})}
}

func (listener *blockingListener) Accept() (net.Conn, error) {
	listener.startOnce.Do(func() { close(listener.accepting) })
	<-listener.closed
	return nil, net.ErrClosed
}

func (listener *blockingListener) Close() error {
	listener.closeOnce.Do(func() { close(listener.closed) })
	return nil
}

func (listener *blockingListener) Addr() net.Addr {
	return blockingListenerAddress("[::]:8080")
}

type blockingListenerAddress string

func (address blockingListenerAddress) Network() string { return "tcp6" }
func (address blockingListenerAddress) String() string  { return string(address) }
