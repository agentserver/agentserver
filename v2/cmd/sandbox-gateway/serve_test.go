package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSandboxGatewayRoutesExposeExactHealthSurface(t *testing.T) {
	forwarded := 0
	backend := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		forwarded++
		response.WriteHeader(http.StatusTeapot)
	})
	readiness := &sandboxGatewayReadiness{}
	handler := sandboxGatewayRoutes(backend, readiness)
	for _, test := range []struct {
		path   string
		status int
	}{
		{path: "/healthz", status: http.StatusOK},
		{path: "/readyz", status: http.StatusServiceUnavailable},
		{path: "/healthz?query=1", status: http.StatusTeapot},
	} {
		request := httptest.NewRequest(http.MethodGet, "http://sandbox.test"+test.path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.status || response.Header().Get("Location") != "" || response.Header().Get("Cache-Control") != "no-store" && test.status != http.StatusTeapot {
			t.Fatalf("GET %s = %d headers=%v", test.path, response.Code, response.Header())
		}
	}
	if forwarded != 1 {
		t.Fatalf("forwarded calls = %d", forwarded)
	}
	readiness.ready.Store(true)
	request := httptest.NewRequest(http.MethodGet, "http://sandbox.test/readyz", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"ready"`) {
		t.Fatalf("ready response = %d %s", response.Code, response.Body.String())
	}
}
