package browsergateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

func TestAuthBridgeProxyForwardsOnlyCookieAndExactQueryWithoutFollowingRedirect(t *testing.T) {
	requests := 0
	client := &http.Client{Transport: browserRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		if request.Method != http.MethodGet || request.URL.Scheme != "https" || request.URL.Host != "core.internal" ||
			request.URL.Path != corecontract.HydraLoginBridgePath || request.URL.RawQuery != "login_challenge=challenge-1" ||
			request.Header.Get("Cookie") != "__Host-agentserver-oidc=binding" || request.Header.Get("Authorization") != "" {
			t.Fatalf("forwarded auth request = %s %s headers=%v", request.Method, request.URL.String(), request.Header)
		}
		return &http.Response{
			StatusCode: http.StatusFound,
			Header: http.Header{
				"Location":     []string{"https://idp.example/authorize?state=opaque"},
				"Set-Cookie":   []string{"__Host-agentserver-oidc=new-binding; Path=/; Secure; HttpOnly; SameSite=Lax"},
				"Content-Type": []string{"text/html; charset=utf-8"},
			},
			Body: io.NopCloser(strings.NewReader("redirect")), Request: request,
		}, nil
	})}
	proxy, err := NewAuthBridgeProxy("https://core.internal", client)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "https://browser.example/auth/hydra/login?login_challenge=challenge-1", nil)
	request.Header.Set("Cookie", "__Host-agentserver-oidc=binding")
	response := httptest.NewRecorder()
	proxy.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusFound || response.Header().Get("Location") != "https://idp.example/authorize?state=opaque" ||
		!strings.Contains(response.Header().Get("Set-Cookie"), "new-binding") || requests != 1 || response.Body.String() != "redirect" {
		t.Fatalf("proxied response = %d %q headers=%v requests=%d", response.Code, response.Body.String(), response.Header(), requests)
	}
}

func TestAuthBridgeProxyRejectsBearerAndInvalidUpstreamRedirect(t *testing.T) {
	requests := 0
	client := &http.Client{Transport: browserRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{
			StatusCode: http.StatusFound,
			Header:     http.Header{"Location": []string{"http://cleartext.example/stolen"}},
			Body:       io.NopCloser(strings.NewReader("redirect")), Request: request,
		}, nil
	})}
	proxy, err := NewAuthBridgeProxy("https://core.internal", client)
	if err != nil {
		t.Fatal(err)
	}
	bearer := httptest.NewRequest(http.MethodGet, "https://browser.example/auth/hydra/consent?consent_challenge=x", nil)
	bearer.Header.Set("Authorization", "Bearer must-not-cross")
	bearerResponse := httptest.NewRecorder()
	proxy.Routes().ServeHTTP(bearerResponse, bearer)
	if bearerResponse.Code != http.StatusBadRequest || requests != 0 {
		t.Fatalf("bearer request = %d upstream=%d", bearerResponse.Code, requests)
	}
	malformed := httptest.NewRequest(http.MethodGet, "https://browser.example/auth/hydra/consent?consent_challenge=x", nil)
	malformed.URL.RawQuery = "consent_challenge=x&discard=%zz"
	malformedResponse := httptest.NewRecorder()
	proxy.Routes().ServeHTTP(malformedResponse, malformed)
	if malformedResponse.Code != http.StatusBadRequest || requests != 0 {
		t.Fatalf("malformed query request = %d upstream=%d", malformedResponse.Code, requests)
	}

	invalid := httptest.NewRequest(http.MethodGet, "https://browser.example/auth/oidc/callback?state=x&code=y", nil)
	invalidResponse := httptest.NewRecorder()
	proxy.Routes().ServeHTTP(invalidResponse, invalid)
	if invalidResponse.Code != http.StatusBadGateway || invalidResponse.Header().Get("Location") != "" || requests != 1 {
		t.Fatalf("invalid redirect = %d headers=%v upstream=%d", invalidResponse.Code, invalidResponse.Header(), requests)
	}
}
