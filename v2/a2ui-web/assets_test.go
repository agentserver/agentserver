package a2uiweb

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestHandlerServesClosedReferenceAssetsWithSecurityHeaders(t *testing.T) {
	tests := []struct {
		path        string
		contentType string
		marker      string
	}{
		{path: "/", contentType: "text/html; charset=utf-8", marker: `data-agentserver-reference-web="v2"`},
		{path: "/index.html", contentType: "text/html; charset=utf-8", marker: "/reference/app.js"},
		{path: "/reference/app.js", contentType: "text/javascript; charset=utf-8", marker: "streamRun"},
		{path: "/reference/protocol.js", contentType: "text/javascript; charset=utf-8", marker: "SSEDecoder"},
		{path: "/reference/auth.js", contentType: "text/javascript; charset=utf-8", marker: "createAuthorizationTransaction"},
		{path: "/reference/llm-gateways.js", contentType: "text/javascript; charset=utf-8", marker: "createBrowserBinding"},
		{path: "/reference/styles.css", contentType: "text/css; charset=utf-8", marker: ".app-shell"},
	}
	handler := Handler()
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "https://gateway.test"+test.path, nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusOK || response.Header().Get("Content-Type") != test.contentType ||
				!strings.Contains(response.Body.String(), test.marker) {
				t.Fatalf("GET %s = %d %q headers=%v", test.path, response.Code, response.Body.String(), response.Header())
			}
			if response.Header().Get("Content-Security-Policy") != contentSecurityPolicy ||
				response.Header().Get("Cross-Origin-Opener-Policy") != "same-origin-allow-popups" ||
				response.Header().Get("Cache-Control") != "no-store" ||
				response.Header().Get("Referrer-Policy") != "no-referrer" ||
				response.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatalf("GET %s security headers = %v", test.path, response.Header())
			}
			if response.Header().Get("Content-Length") != strconv.Itoa(response.Body.Len()) {
				t.Fatalf("GET %s Content-Length = %q body=%d", test.path, response.Header().Get("Content-Length"), response.Body.Len())
			}
		})
	}
}

func TestHandlerForAPIOriginAddsOneExactCSPConnectionAuthority(t *testing.T) {
	handler, err := HandlerForAPIOrigin("https://browser-gateway.byted.bps.dev")
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "https://agent.byted.bps.dev/", nil))
	if response.Code != http.StatusOK || response.Header().Get("Content-Security-Policy") !=
		contentSecurityPolicy+" https://browser-gateway.byted.bps.dev" {
		t.Fatalf("cross-origin CSP response = %d headers=%v", response.Code, response.Header())
	}
	for _, invalid := range []string{"", "http://browser-gateway.byted.bps.dev", "https://browser-gateway.byted.bps.dev/path"} {
		if _, err := HandlerForAPIOrigin(invalid); err == nil {
			t.Fatalf("invalid API origin %q was accepted", invalid)
		}
	}
}

func TestHandlerSupportsHeadAndRejectsFallbacksAndWrites(t *testing.T) {
	handler := Handler()
	head := httptest.NewRecorder()
	handler.ServeHTTP(head, httptest.NewRequest(http.MethodHead, "https://gateway.test/reference/app.js", nil))
	if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("Content-Length") == "" {
		t.Fatalf("HEAD asset = %d %q headers=%v", head.Code, head.Body.String(), head.Header())
	}

	notFound := httptest.NewRecorder()
	handler.ServeHTTP(notFound, httptest.NewRequest(http.MethodGet, "https://gateway.test/unknown", nil))
	if notFound.Code != http.StatusNotFound {
		t.Fatalf("unknown asset status = %d", notFound.Code)
	}

	write := httptest.NewRecorder()
	handler.ServeHTTP(write, httptest.NewRequest(http.MethodPost, "https://gateway.test/", strings.NewReader("ignored")))
	if write.Code != http.StatusMethodNotAllowed || write.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("POST reference web = %d headers=%v", write.Code, write.Header())
	}
}
