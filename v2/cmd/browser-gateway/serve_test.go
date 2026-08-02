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
	"strings"
	"testing"
	"time"

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
		http.NotFoundHandler(), http.NotFoundHandler(), readiness,
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
		!strings.Contains(referenceResponse.Body.String(), `data-agentserver-reference-web="v2"`) ||
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
		http.NotFoundHandler(), http.NotFoundHandler(), &browserReadiness{},
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

func TestValidateBrowserOAuthAuthorityMatchesCoreLoginContract(t *testing.T) {
	scopes, err := validateBrowserOAuthAuthority("agentserver-api", "openid,runs:write,executors:write")
	if err != nil || len(scopes) != 3 || scopes[2] != "executors:write" {
		t.Fatalf("canonical browser OAuth authority = %q, %v", scopes, err)
	}
	for _, input := range []struct {
		audience string
		scopes   string
	}{
		{audience: "other-api", scopes: "openid,runs:write,executors:write"},
		{audience: "agentserver-api", scopes: "openid,runs:write"},
		{audience: "agentserver-api", scopes: "openid,executors:write,runs:write"},
		{audience: "agentserver-api", scopes: "openid,runs:write,executors:write,"},
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
