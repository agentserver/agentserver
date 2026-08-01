package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/harnesspool"
	"github.com/agentserver/agentserver/v2/internal/objectruntime"
)

const harnessPoolServeIntegrationTimeout = 15 * time.Second

func TestServeHarnessPoolStartsPollingAndShutsDownCleanly(t *testing.T) {
	configuration := validHarnessPoolConfiguration(t)
	fixture := newHarnessPoolServeTLSFixture(t, configuration)
	coreRequested := make(chan struct{})
	var requestOnce sync.Once
	var coreRequests atomic.Int64
	coreServer, coreURL := startHarnessPoolTestCore(t, fixture, func(response http.ResponseWriter, request *http.Request) {
		coreRequests.Add(1)
		requestOnce.Do(func() { close(coreRequested) })
		if request.URL.Path != "/internal/v2/run-dispatches:claim" || request.Method != http.MethodPost {
			http.Error(response, "unexpected command", http.StatusNotFound)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(response, `{"runDispatches":[]}`)
	})
	defer coreServer()
	configuration[poolCoreURLEnvironment] = coreURL
	prepareHarnessPoolServeFiles(t, configuration, fixture)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- serveHarnessPool(
			ctx, func(name string) string { return configuration[name] }, &stdout, &stderr,
			harnessPoolServeInsecureDevelopment,
		)
	}()
	select {
	case <-coreRequested:
	case <-time.After(harnessPoolServeIntegrationTimeout):
		t.Fatal("harness-pool did not start its durable Core claim loop")
	}
	if coreRequests.Load() < 1 {
		t.Fatal("harness-pool Core claim loop made no requests")
	}

	match := regexp.MustCompile(`control (https://[^;]+);`).FindStringSubmatch(stdout.String())
	if len(match) != 2 || !strings.HasSuffix(match[1], harnesspool.HarnessControlPath) {
		t.Fatalf("harness-pool startup output = %q", stdout.String())
	}
	controlURL, err := url.Parse(match[1])
	if err != nil {
		t.Fatal(err)
	}
	controlURL.Path = "/readyz"
	client := fixture.anonymousHTTPClient(t)
	response, err := client.Get(controlURL.String())
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil || response.StatusCode != http.StatusOK || string(body) != "{\"status\":\"ready\"}\n" {
		t.Fatalf("ready response = %d %q, read=%v close=%v", response.StatusCode, body, readErr, closeErr)
	}
	controlURL.Path = harnesspool.HarnessControlPath
	response, err = client.Get(controlURL.String())
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated control status = %d", response.StatusCode)
	}

	cancel()
	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(harnessPoolServeIntegrationTimeout):
		t.Fatal("harness-pool did not shut down within its bounded idle path")
	}
	if stderr.Len() != 0 {
		t.Fatalf("harness-pool stderr = %q", stderr.String())
	}
}

func TestHarnessPoolObjectStoreModesAreSeparated(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	development, description, err := configureHarnessPoolObjectStore(
		t.Context(), func(string) string { return "" }, harnessPoolServeInsecureDevelopment, root,
	)
	if err != nil || development == nil || !strings.Contains(description, "INSECURE DEV") {
		t.Fatalf("development object store = %T, %q, %v", development, description, err)
	}

	_, _, err = configureHarnessPoolObjectStore(
		t.Context(), func(string) string { return "" }, harnessPoolServeProduction, "",
	)
	if err == nil || !strings.Contains(err.Error(), objectruntime.ObjectPrefixEnvironment+" is required") {
		t.Fatalf("production object store missing routing error = %v", err)
	}
	_, _, err = configureHarnessPoolObjectStore(
		t.Context(), func(string) string { return "" }, harnessPoolServeMode(255), "",
	)
	if err == nil || !strings.Contains(err.Error(), "mode") {
		t.Fatalf("invalid object store mode error = %v", err)
	}
}

func TestServeHarnessPoolRejectsProductionUntilCapabilityIssuerExists(t *testing.T) {
	err := serveHarnessPool(
		t.Context(), func(string) string { return "" }, io.Discard, io.Discard, harnessPoolServeProduction,
	)
	if err == nil || !strings.Contains(err.Error(), "capability issuance") {
		t.Fatalf("production harness-pool error = %v", err)
	}
}

func TestHarnessPoolRoutesExposeHealthAndReadiness(t *testing.T) {
	readiness := &harnessPoolReadiness{}
	handler := harnessPoolRoutes(http.NotFoundHandler(), readiness)
	for _, test := range []struct {
		path   string
		ready  bool
		status int
		body   string
	}{
		{path: "/healthz", status: http.StatusOK, body: "{\"status\":\"ok\"}\n"},
		{path: "/readyz", status: http.StatusServiceUnavailable, body: "{\"status\":\"not_ready\"}\n"},
		{path: "/readyz", ready: true, status: http.StatusOK, body: "{\"status\":\"ready\"}\n"},
	} {
		readiness.ready.Store(test.ready)
		request := httptest.NewRequest(http.MethodGet, "https://pool.test"+test.path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.status || response.Body.String() != test.body || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("GET %s ready=%v = %d %q", test.path, test.ready, response.Code, response.Body.String())
		}
	}
}

func TestHarnessPoolFailureReporterKeepsOneBoundedLogLine(t *testing.T) {
	var output bytes.Buffer
	reporter := &harnessPoolFailureReporter{writer: &output}
	reporter.ReportPoolFailure(harnesspool.PoolFailure{
		Stage: harnesspool.PoolFailureSupervise, RunID: "run-id", RunAttemptID: "attempt-id",
		Err: errors.New("first line\nsecond line"),
	})
	if strings.Count(output.String(), "\n") != 1 || !strings.Contains(output.String(), "first line second line") {
		t.Fatalf("failure log = %q", output.String())
	}
	output.Reset()
	reporter.ReportPoolFailure(harnesspool.PoolFailure{
		Stage: harnesspool.PoolFailureClaim,
		Err:   errors.New(strings.Repeat("界", maximumHarnessPoolFailureLogBytes)),
	})
	if output.Len() > maximumHarnessPoolFailureLogBytes+256 || !strings.Contains(output.String(), "...(truncated)") {
		t.Fatalf("bounded failure log length = %d", output.Len())
	}
}

func TestHarnessPoolCallbackEndpointPreservesConfiguredLoopbackHost(t *testing.T) {
	endpoint, err := harnessPoolCallbackEndpoint("localhost:0", testNetworkAddress("127.0.0.1:43127"))
	if err != nil {
		t.Fatal(err)
	}
	if endpoint != "https://localhost:43127"+harnesspool.HarnessControlPath {
		t.Fatalf("callback endpoint = %q", endpoint)
	}
}

type harnessPoolServeTLSFixture struct {
	caCertificate     *x509.Certificate
	caPrivateKey      ed25519.PrivateKey
	caPEM             []byte
	poolIdentity      string
	workerIdentity    string
	poolCertificate   tls.Certificate
	workerCertificate tls.Certificate
}

func newHarnessPoolServeTLSFixture(t *testing.T, configuration map[string]string) harnessPoolServeTLSFixture {
	t.Helper()
	_, caKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "harness-pool-test-ca"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), IsCA: true,
		BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, caKey.Public(), caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCertificate, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	fixture := harnessPoolServeTLSFixture{
		caCertificate: caCertificate, caPrivateKey: caKey,
		caPEM:          pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}),
		poolIdentity:   configuration[poolTLSIdentityEnvironment],
		workerIdentity: configuration[poolWorkerTLSIdentityEnvironment],
	}
	fixture.poolCertificate = fixture.issue(t, 2, fixture.poolIdentity, true, true)
	fixture.workerCertificate = fixture.issue(t, 3, fixture.workerIdentity, false, true)
	return fixture
}

func (fixture harnessPoolServeTLSFixture) issue(t *testing.T, serial int64, identity string, server, client bool) tls.Certificate {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	identityURL, err := url.Parse(identity)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: identity},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, URIs: []*url.URL{identityURL},
	}
	if server {
		template.ExtKeyUsage = append(template.ExtKeyUsage, x509.ExtKeyUsageServerAuth)
		template.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
	}
	if client {
		template.ExtKeyUsage = append(template.ExtKeyUsage, x509.ExtKeyUsageClientAuth)
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, template, fixture.caCertificate, publicKey, fixture.caPrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}),
	)
	if err != nil {
		t.Fatal(err)
	}
	return certificate
}

func (fixture harnessPoolServeTLSFixture) workerHTTPClient(t *testing.T) *http.Client {
	t.Helper()
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(fixture.caPEM) {
		t.Fatal("test CA was not parsed")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{
		MinVersion: tls.VersionTLS13, RootCAs: roots,
		Certificates: []tls.Certificate{fixture.workerCertificate},
	}}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport, Timeout: 5 * time.Second}
}

func (fixture harnessPoolServeTLSFixture) anonymousHTTPClient(t *testing.T) *http.Client {
	t.Helper()
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(fixture.caPEM) {
		t.Fatal("test CA was not parsed")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots}}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport, Timeout: 5 * time.Second}
}

func startHarnessPoolTestCore(
	t *testing.T,
	fixture harnessPoolServeTLSFixture,
	handler http.HandlerFunc,
) (func(), string) {
	t.Helper()
	coreCertificate := fixture.issue(t, 4, "spiffe://agentserver.test/ns/agentserver/sa/core", true, false)
	clientCAs := x509.NewCertPool()
	if !clientCAs.AppendCertsFromPEM(fixture.caPEM) {
		t.Fatal("test client CA was not parsed")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	authenticatedHandler := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.TLS == nil || len(request.TLS.VerifiedChains) == 0 || len(request.TLS.VerifiedChains[0]) == 0 ||
			len(request.TLS.VerifiedChains[0][0].URIs) != 1 || request.TLS.VerifiedChains[0][0].URIs[0].String() != fixture.poolIdentity {
			http.Error(response, "wrong harness-pool test identity", http.StatusUnauthorized)
			return
		}
		handler(response, request)
	})
	server := &http.Server{Handler: authenticatedHandler, TLSConfig: &tls.Config{
		MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{coreCertificate},
		ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: clientCAs,
	}}
	done := make(chan error, 1)
	go func() { done <- server.Serve(tls.NewListener(listener, server.TLSConfig)) }()
	stop := func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownContext)
		if err := <-done; err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("test Core server = %v", err)
		}
	}
	return stop, "https://" + listener.Addr().String()
}

func prepareHarnessPoolServeFiles(t *testing.T, configuration map[string]string, fixture harnessPoolServeTLSFixture) {
	t.Helper()
	writeTLSKeyPairForTest(t, configuration[poolTLSCertificateEnvironment], configuration[poolTLSKeyEnvironment], fixture.poolCertificate)
	for _, path := range []string{configuration[poolCoreCAEnvironment], configuration[poolWorkerClientCAEnvironment]} {
		if err := os.WriteFile(path, fixture.caPEM, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(configuration[poolManifestSigningKeyEnvironment], bytesRepeat(0x71, ed25519.SeedSize), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		configuration[poolDevObjectRootEnvironment], configuration[poolRuntimeRootEnvironment],
		configuration[poolCheckpointStagingRootEnvironment],
	} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
}

func writeTLSKeyPairForTest(t *testing.T, certificatePath, keyPath string, certificate tls.Certificate) {
	t.Helper()
	if len(certificate.Certificate) == 0 {
		t.Fatal("test certificate chain is empty")
	}
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Certificate[0]})
	privateKey, ok := certificate.PrivateKey.(ed25519.PrivateKey)
	if !ok {
		t.Fatal("test TLS private key is not Ed25519")
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certificatePath, certificatePEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
}

type testNetworkAddress string

func (address testNetworkAddress) Network() string { return "tcp" }
func (address testNetworkAddress) String() string  { return string(address) }
