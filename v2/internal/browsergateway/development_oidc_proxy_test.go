package browsergateway

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestDevelopmentOIDCAuthorizationProxyForwardsQueryWithoutLoginCookie(t *testing.T) {
	requests := 0
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	jar.SetCookies(&url.URL{Scheme: "http", Host: "127.0.0.1:17447"}, []*http.Cookie{{Name: "ambient", Value: "must-not-cross"}})
	client := &http.Client{Jar: jar, Transport: browserRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		if request.Method != http.MethodGet || request.URL.String() != "http://127.0.0.1:17447/idp/authorize?client_id=agentserver-core&state=opaque" ||
			request.Header.Get("Cookie") != "" || request.Header.Get("Authorization") != "" {
			t.Fatalf("forwarded development IdP request = %s %s headers=%v", request.Method, request.URL, request.Header)
		}
		return &http.Response{
			StatusCode: http.StatusFound,
			Header: http.Header{
				"Location":   []string{"https://browser.example/auth/oidc/callback?code=fresh&state=opaque"},
				"Set-Cookie": []string{"fixture-cookie=must-not-cross"},
			},
			Body: io.NopCloser(strings.NewReader("")), Request: request,
		}, nil
	})}
	proxy, err := NewDevelopmentOIDCAuthorizationProxy("http://127.0.0.1:17447", client)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "https://browser.example/auth/idp/authorize?client_id=agentserver-core&state=opaque", nil)
	request.Header.Set("Cookie", "__Host-agentserver-oidc=browser-binding")
	response := httptest.NewRecorder()
	proxy.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusFound || response.Header().Get("Location") != "https://browser.example/auth/oidc/callback?code=fresh&state=opaque" ||
		response.Header().Get("Set-Cookie") != "" || requests != 1 {
		t.Fatalf("development IdP response = %d headers=%v requests=%d", response.Code, response.Header(), requests)
	}
}

func TestDevelopmentOIDCAuthorizationProxyRejectsBearerAndInvalidRedirect(t *testing.T) {
	requests := 0
	client := &http.Client{Transport: browserRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{
			StatusCode: http.StatusFound,
			Header:     http.Header{"Location": []string{"https://attacker.example/not-the-callback"}},
			Body:       io.NopCloser(strings.NewReader("")), Request: request,
		}, nil
	})}
	proxy, err := NewDevelopmentOIDCAuthorizationProxy("https://idp.internal", client)
	if err != nil {
		t.Fatal(err)
	}
	bearer := httptest.NewRequest(http.MethodGet, "https://browser.example/auth/idp/authorize?state=opaque", nil)
	bearer.Header.Set("Authorization", "Bearer must-not-cross")
	bearerResponse := httptest.NewRecorder()
	proxy.Routes().ServeHTTP(bearerResponse, bearer)
	if bearerResponse.Code != http.StatusBadRequest || requests != 0 {
		t.Fatalf("bearer response = %d requests=%d", bearerResponse.Code, requests)
	}
	invalid := httptest.NewRequest(http.MethodGet, "https://browser.example/auth/idp/authorize?state=opaque", nil)
	invalidResponse := httptest.NewRecorder()
	proxy.Routes().ServeHTTP(invalidResponse, invalid)
	if invalidResponse.Code != http.StatusBadGateway || invalidResponse.Header().Get("Location") != "" || requests != 1 {
		t.Fatalf("invalid redirect response = %d headers=%v requests=%d", invalidResponse.Code, invalidResponse.Header(), requests)
	}
	if _, err := NewDevelopmentOIDCAuthorizationProxy("http://idp.internal", http.DefaultClient); err == nil {
		t.Fatal("non-loopback cleartext development IdP upstream was accepted")
	}
}
