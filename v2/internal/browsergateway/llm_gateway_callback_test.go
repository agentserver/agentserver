package browsergateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLLMGatewayCallbackIsStaticNoStorePostMessageBridge(t *testing.T) {
	handler := NewLLMGatewayCallbackHandler()
	request := httptest.NewRequest(http.MethodGet, "https://agent.example/auth/llm-gateway/callback?code=code-1&state=state-1", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" ||
		response.Header().Get("Referrer-Policy") != "no-referrer" ||
		!strings.Contains(response.Header().Get("Content-Security-Policy"), "script-src 'sha256-") ||
		!strings.Contains(response.Body.String(), "opener.postMessage(payload, targetOrigin)") ||
		!strings.Contains(response.Body.String(), "new window.BroadcastChannel(callbackChannelName)") ||
		!strings.Contains(response.Body.String(), "channel.postMessage(payload)") ||
		!strings.Contains(response.Body.String(), "agentserver-v2.llm-gateway-oidc-callback.v1") ||
		!strings.Contains(response.Body.String(), "history.replaceState") ||
		strings.Contains(response.Body.String(), "state-1") || strings.Contains(response.Body.String(), "code-1") {
		t.Fatalf("callback = %d headers=%v body=%q", response.Code, response.Header(), response.Body.String())
	}

	invalid := httptest.NewRequest(http.MethodPost, "https://agent.example/auth/llm-gateway/callback", strings.NewReader("secret"))
	invalid.Header.Set("Authorization", "Bearer must-not-reach-callback")
	invalidResponse := httptest.NewRecorder()
	handler.ServeHTTP(invalidResponse, invalid)
	if invalidResponse.Code != http.StatusBadRequest || invalidResponse.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("invalid callback = %d headers=%v", invalidResponse.Code, invalidResponse.Header())
	}
}
