package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/llmproxy"
	"github.com/agentserver/agentserver/v2/internal/runcapability"
)

const llmProxyServeTestTimeout = 15 * time.Second

func TestServeLLMProxyProductionEndToEnd(t *testing.T) {
	pki := newLLMProxyTestPKI(t)
	identityURI := "spiffe://agentserver.test/ns/agentserver/sa/llmproxy"
	identity := pki.issue(t, "llmproxy", identityURI, true, true)
	coreIdentity := pki.issue(t, "core", "spiffe://agentserver.test/ns/agentserver/sa/core", true, false)
	upstreamIdentity := pki.issue(t, "upstream", "spiffe://agentserver.test/ns/agentserver/sa/upstream", true, false)

	now := time.Now().UTC()
	issuer := "https://core.agentserver.test"
	keyID := "llmproxy-production-test-key"
	capabilityPrivate := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x41}, ed25519.SeedSize))
	signer, err := runcapability.NewProductionSigner(issuer, keyID, capabilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	claims := runcapability.Claims{
		Version: runcapability.ProductionVersion, Issuer: issuer,
		CapabilityID: "99000000-0000-4000-8000-000000000001", Audience: runcapability.AudienceLLMProxy,
		WorkspaceID: "99000000-0000-4000-8000-000000000002", SessionID: "99000000-0000-4000-8000-000000000003",
		RunID: "99000000-0000-4000-8000-000000000004", RunAttemptID: "99000000-0000-4000-8000-000000000005",
		RunAttemptGeneration: 2, ActorID: "99000000-0000-4000-8000-000000000006", HolderID: "pool/production-holder",
		IssuedAtUnixMS: now.Add(-time.Minute).UnixMilli(), RunDeadlineUnixMS: now.Add(5 * time.Minute).UnixMilli(),
		ExpiresAtUnixMS: now.Add(6 * time.Minute).UnixMilli(), Model: "gpt-5.6-codex",
		Provider:     corecontract.WorkspaceLLMGatewayProvider,
		LLMGatewayID: "99000000-0000-4000-8000-000000000007", LLMGatewayVersion: 2,
		LLMGatewayGrantUserID: "99000000-0000-4000-8000-000000000006",
	}
	runToken, err := signer.Sign(claims)
	if err != nil {
		t.Fatal(err)
	}

	var coreCalls atomic.Int64
	coreServer := newLLMProxyTLSServer(t, coreIdentity, &tls.Config{
		MinVersion: tls.VersionTLS13, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pki.pool(t),
	}, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		coreCalls.Add(1)
		if request.Method != http.MethodPost || request.URL.Path != corecontract.AuthorizeLLMProxyRunCapabilityPath ||
			request.Header.Get("Authorization") != "Bearer "+runToken || request.TLS == nil || len(request.TLS.PeerCertificates) == 0 ||
			len(request.TLS.PeerCertificates[0].URIs) != 1 || request.TLS.PeerCertificates[0].URIs[0].String() != identityURI {
			http.Error(response, "invalid llmproxy Core request", http.StatusForbidden)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		response.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(response).Encode(corecontract.AuthorizeLLMProxyRunCapabilityResponse{
			CapabilityID: claims.CapabilityID, Audience: runcapability.AudienceLLMProxy,
			RunID: claims.RunID, RunAttemptID: claims.RunAttemptID, RunAttemptGeneration: claims.RunAttemptGeneration,
			RunVersion: 4, RunAttemptVersion: 5, AuthorizedAt: time.Now().UTC(),
			Model: claims.Model, Provider: claims.Provider,
			LLMGatewayID: claims.LLMGatewayID, LLMGatewayVersion: claims.LLMGatewayVersion,
			LLMGatewayGrantUserID: claims.LLMGatewayGrantUserID,
			ResponsesURL:          "https://gateway.example.com/v1/responses",
			UpstreamAuthorization: "Bearer upstream-secret", BearerExpiresAt: time.Now().UTC().Add(4 * time.Minute),
		})
	}))
	defer coreServer.Close()

	upstreamRequests := make(chan *http.Request, 1)
	upstreamBodies := make(chan string, 1)
	upstreamServer := newLLMProxyTLSServer(t, upstreamIdentity, &tls.Config{MinVersion: tls.VersionTLS13},
		http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			body, _ := io.ReadAll(request.Body)
			upstreamRequests <- request.Clone(context.Background())
			upstreamBodies <- string(body)
			response.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(response, "event: response.completed\ndata: {}\n\n")
		}))
	defer upstreamServer.Close()

	configuration := materializeLLMProxyServeConfiguration(
		t, pki, identity, issuer, keyID, capabilityPrivate.Public().(ed25519.PublicKey),
		identityURI, coreServer.URL,
	)
	localUpstreamURL, err := url.Parse(upstreamServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	upstreamClient := &http.Client{Transport: llmProxyRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		clone := request.Clone(request.Context())
		clonedURL := *request.URL
		clonedURL.Scheme = localUpstreamURL.Scheme
		clonedURL.Host = localUpstreamURL.Host
		clone.URL = &clonedURL
		return upstreamServer.Client().Transport.RoundTrip(clone)
	})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startup := make(chan string, 1)
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- serveLLMProxyWithUpstreamHTTPClient(
			ctx, func(name string) string { return configuration[name] }, channelWriter(startup), io.Discard, upstreamClient,
		)
	}()
	var line string
	select {
	case line = <-startup:
	case <-time.After(llmProxyServeTestTimeout):
		t.Fatal("llmproxy did not publish its production listener")
	}
	endpoint := strings.TrimSuffix(strings.TrimPrefix(line, "llmproxy serve: production TLS endpoint "),
		"; workspace gateway routes resolved live by Core\n")
	if !strings.HasSuffix(endpoint, llmproxy.ResponsesPath) {
		t.Fatalf("llmproxy startup line = %q", line)
	}

	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		MinVersion: tls.VersionTLS13, RootCAs: pki.pool(t),
	}}}
	defer client.CloseIdleConnections()
	body := `{"model":"gpt-5.6-codex","stream":true,"input":[]}`
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://"+endpoint, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+runToken)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	responseBody, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil || response.StatusCode != http.StatusOK ||
		string(responseBody) != "event: response.completed\ndata: {}\n\n" || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("llmproxy response = %d %q, read=%v close=%v", response.StatusCode, responseBody, readErr, closeErr)
	}
	select {
	case upstreamRequest := <-upstreamRequests:
		if upstreamRequest.Header.Get("Authorization") != "Bearer upstream-secret" ||
			upstreamRequest.Header.Get("Authorization") == "Bearer "+runToken || <-upstreamBodies != body {
			t.Fatalf("llmproxy upstream request headers = %v", upstreamRequest.Header)
		}
	case <-time.After(llmProxyServeTestTimeout):
		t.Fatal("llmproxy did not forward the authorized request")
	}
	if coreCalls.Load() != 1 {
		t.Fatalf("Core live authorization calls = %d", coreCalls.Load())
	}

	cancel()
	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(llmProxyServeTestTimeout):
		t.Fatal("llmproxy did not shut down within its bounded idle path")
	}
}

func TestLLMProxyRoutesExposeHealthAndReadinessWithoutPathRedirects(t *testing.T) {
	readiness := &llmProxyReadiness{}
	var modelCalls atomic.Int64
	handler := llmProxyRoutes(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		modelCalls.Add(1)
		response.WriteHeader(http.StatusNoContent)
	}), readiness)
	for _, test := range []struct {
		method string
		path   string
		ready  bool
		status int
	}{
		{method: http.MethodGet, path: "/healthz", status: http.StatusOK},
		{method: http.MethodGet, path: "/readyz", status: http.StatusServiceUnavailable},
		{method: http.MethodGet, path: "/readyz", ready: true, status: http.StatusOK},
		{method: http.MethodGet, path: "//healthz", status: http.StatusNotFound},
		{method: http.MethodGet, path: "/healthz?query=1", status: http.StatusNotFound},
		{method: http.MethodPost, path: llmproxy.ResponsesPath, status: http.StatusNoContent},
	} {
		readiness.ready.Store(test.ready)
		request := httptest.NewRequest(test.method, "https://llmproxy.test"+test.path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.status || response.Header().Get("Cache-Control") != "no-store" && test.status != http.StatusNoContent {
			t.Fatalf("%s %s ready=%v = %d headers %v", test.method, test.path, test.ready, response.Code, response.Header())
		}
	}
	if modelCalls.Load() != 1 {
		t.Fatalf("model route calls = %d", modelCalls.Load())
	}
}

type channelWriter chan<- string

func (writer channelWriter) Write(raw []byte) (int, error) {
	writer <- string(append([]byte(nil), raw...))
	return len(raw), nil
}

type llmProxyTestPKI struct {
	certificate *x509.Certificate
	privateKey  ed25519.PrivateKey
	caPEM       []byte
}

type issuedLLMProxyIdentity struct {
	certificate    tls.Certificate
	certificatePEM []byte
	privateKeyPEM  []byte
}

func newLLMProxyTestPKI(t *testing.T) llmProxyTestPKI {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "llmproxy-test-ca"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	raw, err := x509.CreateCertificate(rand.Reader, template, template, privateKey.Public(), privateKey)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(raw)
	if err != nil {
		t.Fatal(err)
	}
	return llmProxyTestPKI{certificate: certificate, privateKey: privateKey,
		caPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: raw})}
}

func (pki llmProxyTestPKI) issue(t *testing.T, name, identity string, server, client bool) issuedLLMProxyIdentity {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	identityURL, err := url.Parse(identity)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		t.Fatal(err)
	}
	extended := make([]x509.ExtKeyUsage, 0, 2)
	if server {
		extended = append(extended, x509.ExtKeyUsageServerAuth)
	}
	if client {
		extended = append(extended, x509.ExtKeyUsageClientAuth)
	}
	template := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: extended,
		URIs: []*url.URL{identityURL}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, DNSNames: []string{"localhost"},
	}
	raw, err := x509.CreateCertificate(rand.Reader, template, pki.certificate, privateKey.Public(), pki.privateKey)
	if err != nil {
		t.Fatal(err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: raw})
	privateKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})
	certificate, err := tls.X509KeyPair(certificatePEM, privateKeyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return issuedLLMProxyIdentity{certificate: certificate, certificatePEM: certificatePEM, privateKeyPEM: privateKeyPEM}
}

func (pki llmProxyTestPKI) pool(t *testing.T) *x509.CertPool {
	t.Helper()
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pki.caPEM) {
		t.Fatal("test CA was not accepted")
	}
	return pool
}

func newLLMProxyTLSServer(t *testing.T, identity issuedLLMProxyIdentity, extra *tls.Config, handler http.Handler) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	configuration := extra.Clone()
	configuration.Certificates = []tls.Certificate{identity.certificate}
	server.TLS = configuration
	server.StartTLS()
	return server
}

func materializeLLMProxyServeConfiguration(
	t *testing.T,
	pki llmProxyTestPKI,
	identity issuedLLMProxyIdentity,
	issuer string,
	keyID string,
	publicKey ed25519.PublicKey,
	identityURI string,
	coreURL string,
) map[string]string {
	t.Helper()
	root := t.TempDir()
	write := func(name string, contents []byte, mode os.FileMode) string {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, contents, mode); err != nil {
			t.Fatal(err)
		}
		return path
	}
	keyring, err := json.Marshal(runcapability.ProductionKeyringDocument{
		Version: runcapability.ProductionKeyringVersion,
		Keys: []runcapability.ProductionVerificationKeyDocument{{
			KeyID: keyID, Algorithm: runcapability.ProductionSignatureAlgorithm,
			PublicKey: base64.RawURLEncoding.EncodeToString(publicKey),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return map[string]string{
		llmProxyListenAddressEnvironment:     "127.0.0.1:0",
		llmProxyTLSCertificateEnvironment:    write("llmproxy.crt", identity.certificatePEM, 0o644),
		llmProxyTLSKeyEnvironment:            write("llmproxy.key", identity.privateKeyPEM, 0o600),
		llmProxySPIFFEIdentityEnvironment:    identityURI,
		llmProxyCoreURLEnvironment:           coreURL,
		llmProxyCoreCAEnvironment:            write("ca.pem", pki.caPEM, 0o644),
		llmProxyCapabilityIssuerEnvironment:  issuer,
		llmProxyCapabilityKeyringEnvironment: write("keyring.json", keyring, 0o644),
	}
}

type llmProxyRoundTripFunc func(*http.Request) (*http.Response, error)

func (function llmProxyRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}
