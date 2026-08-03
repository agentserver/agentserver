package platformweb

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPlatformAssetsAreClosedAndHardened(t *testing.T) {
	handler := Handler()
	request := httptest.NewRequest(http.MethodGet, "https://agent.example/", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `data-agentserver-platform-web="v2"`) ||
		response.Header().Get("Content-Security-Policy") == "" || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("platform index = %d headers=%v body=%q", response.Code, response.Header(), response.Body.String())
	}
	unknown := httptest.NewRecorder()
	handler.ServeHTTP(unknown, httptest.NewRequest(http.MethodGet, "https://agent.example/missing", nil))
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("unknown platform asset = %d", unknown.Code)
	}
}
