package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/browsergateway"
	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

func TestServeBrowserGatewayRequiresConfigurationBeforeListening(t *testing.T) {
	err := serveBrowserGateway(t.Context(), func(string) string { return "" }, os.Stdout)
	if err == nil || !strings.Contains(err.Error(), browserListenAddressEnvironment+" is required") {
		t.Fatalf("serveBrowserGateway() error = %v", err)
	}
}

func TestBrowserGatewayHealthAndReadiness(t *testing.T) {
	readiness := &browserReadiness{}
	handler := browserGatewayRoutes(
		http.NotFoundHandler(), http.NotFoundHandler(), http.NotFoundHandler(), http.NotFoundHandler(),
		http.NotFoundHandler(), http.NotFoundHandler(), http.NotFoundHandler(), readiness,
	)
	assertBrowserHealth(t, handler, "/healthz", http.StatusOK, `{"status":"ok"}`)
	assertBrowserHealth(t, handler, "/readyz", http.StatusServiceUnavailable, `{"status":"not_ready"}`)
	readiness.ready.Store(true)
	assertBrowserHealth(t, handler, "/readyz", http.StatusOK, `{"status":"ready"}`)
	request := httptest.NewRequest(http.MethodPost, "https://gateway.test/healthz", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /healthz status = %d", response.Code)
	}
	referenceRequest := httptest.NewRequest(http.MethodGet, "https://gateway.test/", nil)
	referenceResponse := httptest.NewRecorder()
	handler.ServeHTTP(referenceResponse, referenceRequest)
	if referenceResponse.Code != http.StatusOK ||
		!strings.Contains(referenceResponse.Body.String(), `data-agentserver-browser-web="v2"`) ||
		referenceResponse.Header().Get("Content-Security-Policy") == "" {
		t.Fatalf("GET / reference web = %d %q headers=%v", referenceResponse.Code, referenceResponse.Body.String(), referenceResponse.Header())
	}
}

func TestBrowserGatewayExecutorRoutesAreClosedBeforeAGUIFallback(t *testing.T) {
	executorCalls := 0
	aguiCalls := 0
	executors := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		executorCalls++
		response.WriteHeader(http.StatusCreated)
	})
	agui := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		aguiCalls++
		response.WriteHeader(http.StatusAccepted)
	})
	handler := browserGatewayRoutes(
		agui, executors, http.NotFoundHandler(), http.NotFoundHandler(),
		http.NotFoundHandler(), http.NotFoundHandler(), http.NotFoundHandler(), &browserReadiness{},
	)
	workspaceID := "71000000-0000-4000-8000-000000000002"
	executorID := "71000000-0000-4000-8000-000000000003"
	for _, path := range []string{
		corecontract.CreateExecutorResourcePath(workspaceID),
		corecontract.IssueExecutorEnrollmentTokenPath(workspaceID, executorID),
	} {
		request := httptest.NewRequest(http.MethodPost, "https://gateway.test"+path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusCreated {
			t.Fatalf("POST %s status = %d", path, response.Code)
		}

		request = httptest.NewRequest(http.MethodGet, "https://gateway.test"+path, nil)
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != http.MethodPost ||
			response.Header().Get("Cache-Control") != "no-store" || !strings.Contains(response.Body.String(), "method_not_allowed") {
			t.Fatalf("GET %s response = %d headers=%v body=%q", path, response.Code, response.Header(), response.Body.String())
		}
	}

	aguiRequest := httptest.NewRequest(http.MethodPost, "https://gateway.test/v2/workspaces/"+workspaceID+"/sessions/71000000-0000-4000-8000-000000000004/agui", nil)
	aguiResponse := httptest.NewRecorder()
	handler.ServeHTTP(aguiResponse, aguiRequest)
	if aguiResponse.Code != http.StatusAccepted || executorCalls != 2 || aguiCalls != 1 {
		t.Fatalf("route calls/status = executor:%d agui:%d status:%d", executorCalls, aguiCalls, aguiResponse.Code)
	}
}

func TestBrowserConversationRoutesDispatchSessionsBeforeAGUIFallback(t *testing.T) {
	sessionCalls, aguiCalls := 0, 0
	sessions := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		sessionCalls++
		response.WriteHeader(http.StatusOK)
	})
	agui := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		aguiCalls++
		response.WriteHeader(http.StatusAccepted)
	})
	handler := browserConversationRoutes(agui, sessions)
	workspaceID := "40000000-0000-4000-8000-000000000004"
	sessionID := "50000000-0000-4000-8000-000000000005"

	sessionResponse := httptest.NewRecorder()
	handler.ServeHTTP(sessionResponse, httptest.NewRequest(http.MethodGet, corecontract.UserSessionsPath(workspaceID), nil))
	permissionModeResponse := httptest.NewRecorder()
	handler.ServeHTTP(permissionModeResponse, httptest.NewRequest(http.MethodPatch, corecontract.UserSessionPermissionModePath(workspaceID, sessionID), nil))
	workingDirectoryResponse := httptest.NewRecorder()
	handler.ServeHTTP(workingDirectoryResponse, httptest.NewRequest(http.MethodPatch, corecontract.UserSessionWorkingDirectoryPath(workspaceID, sessionID), nil))
	aguiResponse := httptest.NewRecorder()
	handler.ServeHTTP(aguiResponse, httptest.NewRequest(http.MethodPost, "/v2/workspaces/"+workspaceID+"/sessions/"+sessionID+"/agui", nil))
	if sessionResponse.Code != http.StatusOK || permissionModeResponse.Code != http.StatusOK || workingDirectoryResponse.Code != http.StatusOK || aguiResponse.Code != http.StatusAccepted || sessionCalls != 3 || aguiCalls != 1 {
		t.Fatalf("conversation routing = session:%d/%d permission-mode:%d working-directory:%d agui:%d/%d", sessionResponse.Code, sessionCalls, permissionModeResponse.Code, workingDirectoryResponse.Code, aguiResponse.Code, aguiCalls)
	}
}

func TestBrowserGatewaySplitOriginsRestrictHostsAndApplyExactCORS(t *testing.T) {
	apiCalls := 0
	agui := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		apiCalls++
		response.WriteHeader(http.StatusAccepted)
	})
	aguiRoutes := http.NewServeMux()
	aguiRoutes.Handle(browsergateway.AGUIRoutePattern, agui)
	handler := browserGatewaySplitRoutes(
		aguiRoutes, http.NotFoundHandler(), &browserReadiness{}, http.NotFoundHandler(),
		"https://browser.byted.bps.dev", "https://browser-gateway.byted.bps.dev",
	)
	aguiPath := "/v2/workspaces/40000000-0000-4000-8000-000000000004/sessions/50000000-0000-4000-8000-000000000005/agui"

	frontendAPI := httptest.NewRequest(http.MethodPost, "http://browser.byted.bps.dev/v2/test", nil)
	frontendAPI.Host = "browser.byted.bps.dev"
	frontendAPIResponse := httptest.NewRecorder()
	handler.ServeHTTP(frontendAPIResponse, frontendAPI)
	if frontendAPIResponse.Code != http.StatusNotFound || apiCalls != 0 {
		t.Fatalf("frontend /v2 response = %d, API calls %d", frontendAPIResponse.Code, apiCalls)
	}

	preflight := httptest.NewRequest(http.MethodOptions, "http://browser-gateway.byted.bps.dev"+aguiPath, nil)
	preflight.Host = "browser-gateway.byted.bps.dev"
	preflight.Header.Set("Origin", "https://browser.byted.bps.dev")
	preflight.Header.Set("Access-Control-Request-Method", http.MethodPost)
	preflight.Header.Set("Access-Control-Request-Headers", "authorization, content-type, idempotency-key")
	preflightResponse := httptest.NewRecorder()
	handler.ServeHTTP(preflightResponse, preflight)
	if preflightResponse.Code != http.StatusNoContent ||
		preflightResponse.Header().Get("Access-Control-Allow-Origin") != "https://browser.byted.bps.dev" ||
		preflightResponse.Header().Get("Access-Control-Allow-Credentials") != "" || apiCalls != 0 {
		t.Fatalf("preflight = %d headers=%v API calls=%d", preflightResponse.Code, preflightResponse.Header(), apiCalls)
	}
	patchPreflight := httptest.NewRequest(http.MethodOptions, "http://browser-gateway.byted.bps.dev"+corecontract.UserSessionPath(
		"40000000-0000-4000-8000-000000000004", "50000000-0000-4000-8000-000000000005",
	), nil)
	patchPreflight.Host = "browser-gateway.byted.bps.dev"
	patchPreflight.Header.Set("Origin", "https://browser.byted.bps.dev")
	patchPreflight.Header.Set("Access-Control-Request-Method", http.MethodPatch)
	patchPreflight.Header.Set("Access-Control-Request-Headers", "authorization, content-type")
	patchPreflightResponse := httptest.NewRecorder()
	handler.ServeHTTP(patchPreflightResponse, patchPreflight)
	if patchPreflightResponse.Code != http.StatusNoContent || !strings.Contains(patchPreflightResponse.Header().Get("Access-Control-Allow-Methods"), "PATCH") {
		t.Fatalf("PATCH preflight = %d headers=%v", patchPreflightResponse.Code, patchPreflightResponse.Header())
	}

	request := httptest.NewRequest(http.MethodPost, "http://browser-gateway.byted.bps.dev"+aguiPath, nil)
	request.Host = "browser-gateway.byted.bps.dev"
	request.Header.Set("Origin", "https://browser.byted.bps.dev")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || response.Header().Get("Access-Control-Allow-Origin") != "https://browser.byted.bps.dev" || apiCalls != 1 {
		t.Fatalf("cross-origin API = %d headers=%v calls=%d", response.Code, response.Header(), apiCalls)
	}

	attacker := httptest.NewRequest(http.MethodPost, "http://browser-gateway.byted.bps.dev"+aguiPath, nil)
	attacker.Host = "browser-gateway.byted.bps.dev"
	attacker.Header.Set("Origin", "https://attacker.example")
	attackerResponse := httptest.NewRecorder()
	handler.ServeHTTP(attackerResponse, attacker)
	if attackerResponse.Code != http.StatusForbidden || apiCalls != 1 {
		t.Fatalf("attacker origin = %d calls=%d", attackerResponse.Code, apiCalls)
	}

	for _, route := range []struct {
		host string
		path string
	}{
		{host: "browser.byted.bps.dev", path: "/oauth2/auth"},
		{host: "browser.byted.bps.dev", path: "/auth/hydra/login"},
		{host: "browser.byted.bps.dev", path: "/v2/workspaces/40000000-0000-4000-8000-000000000004/executors"},
		{host: "browser-gateway.byted.bps.dev", path: "/v2/workspaces/40000000-0000-4000-8000-000000000004/llm-gateways"},
	} {
		closed := httptest.NewRequest(http.MethodGet, "http://"+route.host+route.path, nil)
		closed.Host = route.host
		closedResponse := httptest.NewRecorder()
		handler.ServeHTTP(closedResponse, closed)
		if closedResponse.Code != http.StatusNotFound || apiCalls != 1 {
			t.Fatalf("split Browser exposed %s%s: status=%d calls=%d", route.host, route.path, closedResponse.Code, apiCalls)
		}
	}
}

func TestBrowserPublicOriginsRequireExactDistinctPair(t *testing.T) {
	values := map[string]string{
		browserFrontendOriginEnvironment: "https://browser.byted.bps.dev",
		browserAPIOriginEnvironment:      "https://browser-gateway.byted.bps.dev",
	}
	frontend, api, split, err := browserPublicOrigins(func(name string) string { return values[name] })
	if err != nil || !split || frontend != values[browserFrontendOriginEnvironment] || api != values[browserAPIOriginEnvironment] {
		t.Fatalf("browser origins = %q %q %v %v", frontend, api, split, err)
	}
	delete(values, browserAPIOriginEnvironment)
	if _, _, _, err := browserPublicOrigins(func(name string) string { return values[name] }); err == nil {
		t.Fatal("partial split-origin configuration was accepted")
	}
}

func TestValidateBrowserOAuthAuthorityMatchesCoreLoginContract(t *testing.T) {
	expected := corecontract.BrowserOAuthScopes()
	scopes, err := validateBrowserOAuthAuthority(corecontract.BrowserOAuthAudience, strings.Join(expected, ","))
	if err != nil || !slices.Equal(scopes, expected) {
		t.Fatalf("canonical browser OAuth authority = %q, %v", scopes, err)
	}
	for _, input := range []struct {
		audience string
		scopes   string
	}{
		{audience: "other-api", scopes: strings.Join(expected, ",")},
		{audience: corecontract.BrowserOAuthAudience, scopes: "openid,runs:create"},
		{audience: corecontract.BrowserOAuthAudience, scopes: strings.Join(append([]string{expected[1]}, expected[0], expected[2]), ",")},
		{audience: corecontract.BrowserOAuthAudience, scopes: strings.Join(expected, ",") + ","},
	} {
		if _, err := validateBrowserOAuthAuthority(input.audience, input.scopes); err == nil {
			t.Fatalf("non-canonical browser OAuth authority was accepted: %+v", input)
		}
	}
}

func TestValidateBrowserCoreURLRequiresHTTPSOrigin(t *testing.T) {
	for _, valid := range []string{"https://core.internal", "https://core.internal:8443/"} {
		if err := validateBrowserCoreURL(valid); err != nil {
			t.Fatalf("validateBrowserCoreURL(%q) = %v", valid, err)
		}
	}
	for _, invalid := range []string{
		"http://core.internal", "https://user@core.internal", "https://core.internal/v2",
		"https://core.internal?x=1", "https://core.internal#fragment", "https://:443", "core.internal",
	} {
		if err := validateBrowserCoreURL(invalid); err == nil {
			t.Fatalf("invalid core URL %q was accepted", invalid)
		}
	}
}

func TestBrowserGatewayTLSAndCoreMTLSConfiguration(t *testing.T) {
	certificateFile, keyFile := writeBrowserTestCertificate(t)
	serverTLS, err := browserGatewayTLSConfig(certificateFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	if serverTLS.MinVersion != tls.VersionTLS13 || len(serverTLS.Certificates) != 1 {
		t.Fatalf("browser TLS config = %+v", serverTLS)
	}
	client, err := newBrowserCoreHTTPClient(certificateFile, certificateFile, keyFile, "core.internal")
	if err != nil {
		t.Fatal(err)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport.TLSClientConfig == nil || transport.TLSClientConfig.MinVersion != tls.VersionTLS13 ||
		len(transport.TLSClientConfig.Certificates) != 1 || transport.TLSClientConfig.RootCAs == nil ||
		transport.TLSClientConfig.ServerName != "core.internal" || !transport.ForceAttemptHTTP2 {
		t.Fatalf("core mTLS transport = %#v", client.Transport)
	}
	hydraClient, err := newBrowserHydraHTTPClient(certificateFile, "hydra.agentserver.internal")
	if err != nil {
		t.Fatal(err)
	}
	hydraTransport, ok := hydraClient.Transport.(*http.Transport)
	if !ok || hydraTransport.TLSClientConfig == nil || hydraTransport.TLSClientConfig.MinVersion != tls.VersionTLS13 ||
		hydraTransport.TLSClientConfig.RootCAs == nil || len(hydraTransport.TLSClientConfig.Certificates) != 0 ||
		hydraTransport.TLSClientConfig.ServerName != "hydra.agentserver.internal" || !hydraTransport.ForceAttemptHTTP2 {
		t.Fatalf("Hydra TLS transport = %#v", hydraClient.Transport)
	}
}

func assertBrowserHealth(t *testing.T, handler http.Handler, path string, status int, body string) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "https://gateway.test"+path, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != status || strings.TrimSpace(response.Body.String()) != body || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("GET %s = %d %q, headers %+v", path, response.Code, response.Body.String(), response.Header())
	}
}

func writeBrowserTestCertificate(t *testing.T) (string, string) {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "core.internal"},
		DNSNames:              []string{"core.internal"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	certificateFile := filepath.Join(directory, "identity.crt")
	keyFile := filepath.Join(directory, "identity.key")
	if err := os.WriteFile(certificateFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certificateFile, keyFile
}
