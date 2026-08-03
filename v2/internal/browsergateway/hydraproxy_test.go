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

func TestHydraPublicProxyForwardsAuthorizationAndTokenWithoutBrowserAuthority(t *testing.T) {
	requests := 0
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	jar.SetCookies(&url.URL{Scheme: "http", Host: "127.0.0.1:17447"}, []*http.Cookie{{Name: "ambient", Value: "must-not-cross"}})
	client := &http.Client{Jar: jar, Transport: browserRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		if request.URL.Scheme != "http" || request.URL.Host != "127.0.0.1:17447" || request.Host != "browser.example" ||
			request.Header.Get("X-Forwarded-Host") != "browser.example" || request.Header.Get("X-Forwarded-Proto") != "https" ||
			request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" {
			t.Fatalf("forwarded Hydra request = %s %s headers=%v", request.Method, request.URL.String(), request.Header)
		}
		switch requests {
		case 1:
			if request.Method != http.MethodGet || request.URL.Path != "/oauth2/auth" || request.URL.RawQuery != "client_id=agentserver-web" {
				t.Fatalf("authorization upstream = %s %s", request.Method, request.URL.String())
			}
			return &http.Response{
				StatusCode: http.StatusFound,
				Header: http.Header{
					"Location":     []string{"https://browser.example/auth/hydra/login?login_challenge=opaque"},
					"Content-Type": []string{"text/html; charset=utf-8"},
					"Set-Cookie":   []string{"hydra_csrf=must-not-cross; Secure; HttpOnly"},
				},
				Body: io.NopCloser(strings.NewReader("<html>Hydra redirect</html>")), Request: request,
			}, nil
		case 2:
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatal(err)
			}
			if request.Method != http.MethodPost || request.URL.Path != "/oauth2/token" || request.URL.RawQuery != "" ||
				request.Header.Get("Content-Type") != "application/x-www-form-urlencoded" || string(body) != "code=opaque" {
				t.Fatalf("token upstream = %s %s headers=%v body=%q", request.Method, request.URL.String(), request.Header, body)
			}
			return &http.Response{
				StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}},
				Body: io.NopCloser(strings.NewReader(`{"access_token":"fresh"}`)), Request: request,
			}, nil
		default:
			t.Fatalf("unexpected upstream request %d", requests)
			return nil, nil
		}
	})}
	proxy, err := NewHydraPublicProxy("http://127.0.0.1:17447", "https://browser.example", client)
	if err != nil {
		t.Fatal(err)
	}

	authorize := httptest.NewRequest(http.MethodGet, "https://browser.example/oauth2/auth?client_id=agentserver-web", nil)
	authorize.Header.Set("Cookie", "__Host-agentserver-oidc=stale-browser-binding")
	authorizeResponse := httptest.NewRecorder()
	proxy.Routes().ServeHTTP(authorizeResponse, authorize)
	if authorizeResponse.Code != http.StatusFound ||
		authorizeResponse.Header().Get("Location") != "https://browser.example/auth/hydra/login?login_challenge=opaque" ||
		authorizeResponse.Body.Len() != 0 || authorizeResponse.Header().Get("Content-Length") != "0" ||
		authorizeResponse.Header().Get("Content-Type") != "" || authorizeResponse.Header().Get("Set-Cookie") != "" {
		t.Fatalf("authorization response = %d headers=%v", authorizeResponse.Code, authorizeResponse.Header())
	}

	token := httptest.NewRequest(http.MethodPost, "https://browser.example/oauth2/token", strings.NewReader("code=opaque"))
	token.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	token.Header.Set("Cookie", "__Host-agentserver-oidc=stale-browser-binding")
	tokenResponse := httptest.NewRecorder()
	proxy.Routes().ServeHTTP(tokenResponse, token)
	if tokenResponse.Code != http.StatusOK || tokenResponse.Body.String() != `{"access_token":"fresh"}` || requests != 2 {
		t.Fatalf("token response = %d %q, requests=%d", tokenResponse.Code, tokenResponse.Body.String(), requests)
	}
}

func TestHydraPublicProxyRejectsBearerAndNonLoopbackCleartext(t *testing.T) {
	if _, err := NewHydraPublicProxy("http://hydra.internal:4444", "https://browser.example", http.DefaultClient); err == nil {
		t.Fatal("non-loopback cleartext Hydra upstream was accepted")
	}
	if _, err := NewHydraPublicProxy("https://hydra.internal", "http://browser.example", http.DefaultClient); err == nil {
		t.Fatal("non-loopback cleartext Hydra public origin was accepted")
	}
	requests := 0
	proxy, err := NewHydraPublicProxy("https://hydra.internal", "https://browser.example", &http.Client{Transport: browserRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		return nil, io.EOF
	})})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "https://browser.example/oauth2/auth?client_id=x", nil)
	request.Header.Set("Authorization", "must-not-cross")
	response := httptest.NewRecorder()
	proxy.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("Authorization request status = %d", response.Code)
	}
	if requests != 0 {
		t.Fatalf("rejected requests reached upstream %d time(s)", requests)
	}
}
