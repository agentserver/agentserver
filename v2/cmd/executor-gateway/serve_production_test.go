//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
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
	"github.com/agentserver/agentserver/v2/internal/executorgateway"
	"github.com/agentserver/agentserver/v2/internal/runcapability"
	"nhooyr.io/websocket"
)

const executorGatewayServeTestTimeout = 15 * time.Second

func TestReadStableGatewayFileAcceptsRestrictedKubernetesSecretProjection(t *testing.T) {
	root := t.TempDir()
	versionDirectory := filepath.Join(root, "..2026_08_02_00_00_00")
	if err := os.Mkdir(versionDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	contents := []byte("projected-private-key")
	target := filepath.Join(versionDirectory, "tls.key")
	if err := os.WriteFile(target, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Base(versionDirectory), filepath.Join(root, "..data")); err != nil {
		t.Fatal(err)
	}
	projection := filepath.Join(root, "tls.key")
	if err := os.Symlink(filepath.Join("..data", "tls.key"), projection); err != nil {
		t.Fatal(err)
	}
	read, err := readStableGatewayFile("projected TLS private key", projection, 1024, true)
	if err != nil || !bytes.Equal(read, contents) {
		t.Fatalf("read projected Secret = %q, %v", read, err)
	}
	if err := os.Chmod(target, 0o440); err != nil {
		t.Fatal(err)
	}
	if read, err := readStableGatewayFile("projected TLS private key", projection, 1024, true); err != nil || !bytes.Equal(read, contents) {
		t.Fatalf("read group-readable projected Secret = %q, %v", read, err)
	}
	if err := os.Chmod(target, 0o444); err != nil {
		t.Fatal(err)
	}
	if _, err := readStableGatewayFile("projected TLS private key", projection, 1024, true); err == nil {
		t.Fatal("other-readable projected private key was accepted")
	}
}

func TestServeExecutorGatewayProductionMachineIdentityEndToEnd(t *testing.T) {
	pki := newExecutorGatewayTestPKI(t)
	const gatewayIdentityURI = "spiffe://agentserver.test/ns/agentserver/sa/executor-gateway"
	gatewayIdentity := pki.issue(t, "executor-gateway", gatewayIdentityURI, true, true)
	coreIdentity := pki.issue(t, "core", "spiffe://agentserver.test/ns/agentserver/sa/core", true, false)

	now := time.Now().UTC().Truncate(time.Second)
	machinePublicKey, machinePrivateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	machineDigest := sha256.Sum256(machinePublicKey)
	const (
		executorID      = "9a000000-0000-4000-8000-000000000001"
		workspaceID     = "9a000000-0000-4000-8000-000000000002"
		oauthBearer     = "opaque-production-executor-oauth-token"
		enrollmentToken = "asv2enr1.claims.mac"
	)
	enrollmentResult := corecontract.CompleteExecutorEnrollmentResponse{
		Executor: corecontract.ExecutorResourceState{
			ExecutorID: executorID, WorkspaceID: workspaceID, Status: "offline", Version: 2,
			CreatedAt: now.Add(-time.Hour), UpdatedAt: now,
		},
		OAuthClientID: "agentserver-executor-" + executorID,
		Audience:      "executor-gateway", Scope: "executor:connect",
	}
	authorizationResult := corecontract.AuthorizeExecutorConnectionResponse{
		ExecutorID: executorID, WorkspaceID: workspaceID,
		OAuthClientID:           "agentserver-executor-" + executorID,
		MachinePublicKeyEd25519: base64.RawURLEncoding.EncodeToString(machinePublicKey),
		MachineKeySHA256:        hex.EncodeToString(machineDigest[:]), ExecutorVersion: 2,
		TokenExpiresAt: now.Add(5 * time.Minute), AuthorizedAt: now.Add(-time.Hour),
	}
	var completeCalls atomic.Int64
	var authorizeCalls atomic.Int64
	var recoveryCalls atomic.Int64
	var unavailable atomic.Bool
	var revoked atomic.Bool
	coreServer := newExecutorGatewayTLSServer(t, pki, coreIdentity, gatewayIdentityURI, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.TLS == nil || len(request.TLS.PeerCertificates) == 0 || len(request.TLS.PeerCertificates[0].URIs) != 1 ||
			request.TLS.PeerCertificates[0].URIs[0].String() != gatewayIdentityURI {
			writeExecutorGatewayTestCoreJSON(response, http.StatusForbidden, corecontract.ErrorResponse{Code: "forbidden", Message: "wrong workload"})
			return
		}
		switch request.URL.Path {
		case corecontract.RecoverExecutorGatewayPath(executorID):
			recoveryCalls.Add(1)
			var command corecontract.RecoverExecutorGatewayRequest
			if request.Method != http.MethodPost || request.Header.Get("Content-Type") != "application/json" ||
				json.NewDecoder(io.LimitReader(request.Body, 512*1024+1)).Decode(&command) != nil ||
				command.GatewayInstanceID == "" || len(command.Records) != corecontract.MaxGatewayRecoveryRecords {
				writeExecutorGatewayTestCoreJSON(response, http.StatusBadRequest, corecontract.ErrorResponse{Code: "invalid_argument", Message: "invalid gateway recovery"})
				return
			}
			for index, record := range command.Records {
				if record.ProducerInstanceID != command.GatewayInstanceID || record.ProducerSeq != int64(index+1) {
					writeExecutorGatewayTestCoreJSON(response, http.StatusBadRequest, corecontract.ErrorResponse{Code: "invalid_argument", Message: "invalid gateway recovery records"})
					return
				}
			}
			writeExecutorGatewayTestCoreJSON(response, http.StatusOK, corecontract.RecoverExecutorGatewayResponse{
				FencedConnectionGeneration: 0, RecoveredExecutions: 0, Remaining: false,
			})
		case corecontract.CompleteExecutorEnrollmentPath:
			completeCalls.Add(1)
			if request.Method != http.MethodPost || request.Header.Get("Authorization") != "Bearer "+enrollmentToken ||
				request.Header.Get("Content-Type") != "application/json" ||
				request.Header.Get(corecontract.ExpectedExecutorIDHeader) != executorID {
				writeExecutorGatewayTestCoreJSON(response, http.StatusBadRequest, corecontract.ErrorResponse{Code: "invalid_argument", Message: "invalid enrollment relay"})
				return
			}
			var command corecontract.CompleteExecutorEnrollmentRequest
			if err := json.NewDecoder(io.LimitReader(request.Body, 512*1024+1)).Decode(&command); err != nil || command.AgentxVersion != "1.0.0" {
				writeExecutorGatewayTestCoreJSON(response, http.StatusBadRequest, corecontract.ErrorResponse{Code: "invalid_argument", Message: "invalid enrollment body"})
				return
			}
			writeExecutorGatewayTestCoreJSON(response, http.StatusOK, enrollmentResult)
		case corecontract.AuthorizeExecutorConnectionPath:
			authorizeCalls.Add(1)
			body, readErr := io.ReadAll(io.LimitReader(request.Body, 2))
			if request.Method != http.MethodPost || request.Header.Get("Authorization") != "Bearer "+oauthBearer ||
				request.Header.Get("Content-Type") != "" || readErr != nil || len(body) != 0 {
				writeExecutorGatewayTestCoreJSON(response, http.StatusBadRequest, corecontract.ErrorResponse{Code: "invalid_argument", Message: "invalid live authorization"})
				return
			}
			if unavailable.Load() {
				writeExecutorGatewayTestCoreJSON(response, http.StatusServiceUnavailable, corecontract.ErrorResponse{Code: "unavailable", Message: "Core unavailable"})
				return
			}
			if revoked.Load() {
				writeExecutorGatewayTestCoreJSON(response, http.StatusForbidden, corecontract.ErrorResponse{Code: "forbidden", Message: "executor revoked"})
				return
			}
			writeExecutorGatewayTestCoreJSON(response, http.StatusOK, authorizationResult)
		default:
			writeExecutorGatewayTestCoreJSON(response, http.StatusNotFound, corecontract.ErrorResponse{Code: "not_found", Message: "not found"})
		}
	}))
	defer coreServer.Close()

	capabilityPublicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	configuration := materializeExecutorGatewayProductionConfiguration(
		t, pki, gatewayIdentity, gatewayIdentityURI, executorID, coreServer.URL, capabilityPublicKey,
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startup := make(chan string, 1)
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- serveGateway(ctx, func(name string) string { return configuration[name] }, executorGatewayChannelWriter(startup), gatewayServeProduction)
	}()
	var startupLine string
	select {
	case startupLine = <-startup:
	case err := <-serveDone:
		t.Fatalf("executor-gateway exited before publishing its production listeners: %v", err)
	case <-time.After(executorGatewayServeTestTimeout):
		t.Fatal("executor-gateway did not publish its production listener")
	}
	address := executorGatewayAddressFromStartup(t, startupLine)
	internalAddress := executorGatewayInternalAddressFromStartup(t, startupLine)
	client := &http.Client{}
	defer client.CloseIdleConnections()
	baseURL := "http://" + address
	publicMCPResponse, err := client.Get(baseURL + executorgateway.ExecutorMCPPath)
	if err != nil {
		t.Fatal(err)
	}
	_ = publicMCPResponse.Body.Close()
	if publicMCPResponse.StatusCode != http.StatusNotFound {
		t.Fatalf("public /mcp status = %d", publicMCPResponse.StatusCode)
	}
	internalClient := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		MinVersion: tls.VersionTLS13,
		RootCAs:    pki.pool(t),
		ServerName: "localhost",
	}}}
	defer internalClient.CloseIdleConnections()
	internalMCPResponse, err := internalClient.Post("https://"+internalAddress+executorgateway.ExecutorMCPPath, "application/json", bytes.NewReader([]byte(`{}`)))
	if err != nil {
		t.Fatal(err)
	}
	_ = internalMCPResponse.Body.Close()
	if internalMCPResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("internal /mcp status = %d", internalMCPResponse.StatusCode)
	}
	internalAgentxResponse, err := internalClient.Get("https://" + internalAddress + executorgateway.AgentxConnectPath)
	if err != nil {
		t.Fatal(err)
	}
	_ = internalAgentxResponse.Body.Close()
	if internalAgentxResponse.StatusCode != http.StatusNotFound {
		t.Fatalf("internal agentx status = %d", internalAgentxResponse.StatusCode)
	}

	enrollmentCommand := corecontract.CompleteExecutorEnrollmentRequest{
		MachinePublicKeyEd25519: base64.RawURLEncoding.EncodeToString(machinePublicKey),
		MachineProofEd25519:     strings.Repeat("a", 86),
		OAuthPublicKeyP256X:     strings.Repeat("b", 43), OAuthPublicKeyP256Y: strings.Repeat("c", 43),
		OAuthProofES256: strings.Repeat("d", 86), AgentxVersion: "1.0.0",
		RuntimeManifestSHA256: strings.Repeat("1", 64), ExecProtocolSourceSHA256: strings.Repeat("2", 64),
		Environments: []corecontract.ExecutorEnrollmentEnvironment{},
	}
	rawEnrollment, err := json.Marshal(enrollmentCommand)
	if err != nil {
		t.Fatal(err)
	}
	enrollmentRequest, err := http.NewRequestWithContext(t.Context(), http.MethodPost, baseURL+executorgateway.AgentxEnrollmentPath, bytes.NewReader(rawEnrollment))
	if err != nil {
		t.Fatal(err)
	}
	enrollmentRequest.Header.Set("Content-Type", "application/json")
	enrollmentRequest.Header.Set("Authorization", "Bearer "+enrollmentToken)
	enrollmentResponse, err := client.Do(enrollmentRequest)
	if err != nil {
		t.Fatal(err)
	}
	enrollmentBody, readErr := io.ReadAll(enrollmentResponse.Body)
	closeErr := enrollmentResponse.Body.Close()
	if readErr != nil || closeErr != nil || enrollmentResponse.StatusCode != http.StatusOK ||
		enrollmentResponse.Header.Get("Cache-Control") != "no-store" || bytes.Contains(enrollmentBody, []byte(enrollmentToken)) || completeCalls.Load() != 1 {
		t.Fatalf("production enrollment = %d %q, read=%v close=%v calls=%d", enrollmentResponse.StatusCode, enrollmentBody, readErr, closeErr, completeCalls.Load())
	}

	invalidChallenge := issueExecutorGatewayChallenge(t, client, baseURL, oauthBearer)
	invalidHeaders := executorGatewayProofHeaders(invalidChallenge, oauthBearer,
		base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x01}, ed25519.SignatureSize)))
	assertExecutorGatewayUpgradeFailure(t, client, baseURL, invalidHeaders, http.StatusUnauthorized)
	validTranscript, err := executorgateway.ExecutorWSSProofTranscript(invalidChallenge, oauthBearer)
	if err != nil {
		t.Fatal(err)
	}
	replayHeaders := executorGatewayProofHeaders(invalidChallenge, oauthBearer,
		base64.RawURLEncoding.EncodeToString(ed25519.Sign(machinePrivateKey, validTranscript)))
	assertExecutorGatewayUpgradeFailure(t, client, baseURL, replayHeaders, http.StatusUnauthorized)

	retryChallenge := issueExecutorGatewayChallenge(t, client, baseURL, oauthBearer)
	retryTranscript, err := executorgateway.ExecutorWSSProofTranscript(retryChallenge, oauthBearer)
	if err != nil {
		t.Fatal(err)
	}
	retryHeaders := executorGatewayProofHeaders(retryChallenge, oauthBearer,
		base64.RawURLEncoding.EncodeToString(ed25519.Sign(machinePrivateKey, retryTranscript)))
	unavailable.Store(true)
	assertExecutorGatewayUpgradeFailure(t, client, baseURL, retryHeaders, http.StatusServiceUnavailable)
	unavailable.Store(false)
	connection, response, err := websocket.Dial(t.Context(), "ws://"+address+executorgateway.AgentxConnectPath, &websocket.DialOptions{
		HTTPClient: client, HTTPHeader: retryHeaders, CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil || response == nil || response.StatusCode != http.StatusSwitchingProtocols || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("production signed WSS upgrade = response %+v, error %v", response, err)
	}
	_ = connection.Close(websocket.StatusNormalClosure, "production identity accepted")
	assertExecutorGatewayUpgradeFailure(t, client, baseURL, retryHeaders, http.StatusUnauthorized)

	revokedChallenge := issueExecutorGatewayChallenge(t, client, baseURL, oauthBearer)
	revokedTranscript, err := executorgateway.ExecutorWSSProofTranscript(revokedChallenge, oauthBearer)
	if err != nil {
		t.Fatal(err)
	}
	revokedHeaders := executorGatewayProofHeaders(revokedChallenge, oauthBearer,
		base64.RawURLEncoding.EncodeToString(ed25519.Sign(machinePrivateKey, revokedTranscript)))
	revoked.Store(true)
	assertExecutorGatewayUpgradeFailure(t, client, baseURL, revokedHeaders, http.StatusUnauthorized)
	if got := authorizeCalls.Load(); got != 9 {
		t.Fatalf("Core live authorization calls = %d, want 9", got)
	}
	if recoveryCalls.Load() != 1 {
		t.Fatalf("Core gateway startup recovery calls = %d, want 1", recoveryCalls.Load())
	}

	cancel()
	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(executorGatewayServeTestTimeout):
		t.Fatal("executor-gateway did not shut down within its bounded path")
	}
}

func issueExecutorGatewayChallenge(t *testing.T, client *http.Client, baseURL, bearer string) executorgateway.ExecutorChallengeResponse {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, baseURL+executorgateway.AgentxChallengePath, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+bearer)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var challenge executorgateway.ExecutorChallengeResponse
	if response.StatusCode != http.StatusCreated || response.Header.Get("Cache-Control") != "no-store" || json.NewDecoder(response.Body).Decode(&challenge) != nil {
		t.Fatalf("challenge response = %d headers %v", response.StatusCode, response.Header)
	}
	return challenge
}

func executorGatewayProofHeaders(challenge executorgateway.ExecutorChallengeResponse, bearer, proof string) http.Header {
	header := http.Header{}
	header.Set("Authorization", "Bearer "+bearer)
	header.Set(executorgateway.AgentxChallengeIDHeader, challenge.ChallengeID)
	header.Set(executorgateway.AgentxMachineProofHeader, proof)
	return header
}

func assertExecutorGatewayUpgradeFailure(t *testing.T, client *http.Client, baseURL string, header http.Header, wantStatus int) {
	t.Helper()
	connection, response, err := websocket.Dial(t.Context(), "ws://"+strings.TrimPrefix(baseURL, "http://")+executorgateway.AgentxConnectPath, &websocket.DialOptions{
		HTTPClient: client, HTTPHeader: header, CompressionMode: websocket.CompressionDisabled,
	})
	if err == nil || connection != nil || response == nil || response.StatusCode != wantStatus ||
		response.Header.Get("Content-Type") != "application/json" || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("WSS rejection = connection %+v, response %+v, error %v, want HTTP %d", connection, response, err, wantStatus)
	}
	body, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr != nil || bytes.Contains(body, []byte("opaque-production-executor-oauth-token")) {
		t.Fatalf("WSS rejection body = %q, error %v", body, readErr)
	}
}

type executorGatewayChannelWriter chan<- string

func (writer executorGatewayChannelWriter) Write(raw []byte) (int, error) {
	writer <- string(append([]byte(nil), raw...))
	return len(raw), nil
}

type executorGatewayTestPKI struct {
	certificate *x509.Certificate
	privateKey  ed25519.PrivateKey
	caPEM       []byte
}

type issuedExecutorGatewayIdentity struct {
	certificate    tls.Certificate
	certificatePEM []byte
	privateKeyPEM  []byte
}

func newExecutorGatewayTestPKI(t *testing.T) executorGatewayTestPKI {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "executor-gateway-test-ca"},
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
	return executorGatewayTestPKI{
		certificate: certificate, privateKey: privateKey,
		caPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: raw}),
	}
}

func (pki executorGatewayTestPKI) issue(t *testing.T, name, identity string, server, client bool) issuedExecutorGatewayIdentity {
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
	return issuedExecutorGatewayIdentity{certificate: certificate, certificatePEM: certificatePEM, privateKeyPEM: privateKeyPEM}
}

func (pki executorGatewayTestPKI) pool(t *testing.T) *x509.CertPool {
	t.Helper()
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pki.caPEM) {
		t.Fatal("executor-gateway test CA was not accepted")
	}
	return pool
}

func newExecutorGatewayTLSServer(
	t *testing.T,
	pki executorGatewayTestPKI,
	identity issuedExecutorGatewayIdentity,
	expectedClientIdentity string,
	handler http.Handler,
) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{
		MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{identity.certificate},
		ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pki.pool(t),
		VerifyConnection: func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) == 0 || len(state.PeerCertificates[0].URIs) != 1 ||
				state.PeerCertificates[0].URIs[0].String() != expectedClientIdentity {
				return io.ErrUnexpectedEOF
			}
			return nil
		},
	}
	server.StartTLS()
	return server
}

func materializeExecutorGatewayProductionConfiguration(
	t *testing.T,
	pki executorGatewayTestPKI,
	identity issuedExecutorGatewayIdentity,
	identityURI string,
	executorID string,
	coreURL string,
	capabilityPublicKey ed25519.PublicKey,
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
			KeyID: "executor-gateway-production-test-key", Algorithm: runcapability.ProductionSignatureAlgorithm,
			PublicKey: base64.RawURLEncoding.EncodeToString(capabilityPublicKey),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	certificatePath := write("executor-gateway.crt", identity.certificatePEM, 0o644)
	keyPath := write("executor-gateway.key", identity.privateKeyPEM, 0o600)
	return map[string]string{
		gatewayListenAddressEnvironment:          "127.0.0.1:0",
		gatewayPublicListenAddressEnvironment:    "localhost:0",
		gatewayTLSCertificateEnvironment:         certificatePath,
		gatewayTLSKeyEnvironment:                 keyPath,
		gatewayCoreURLEnvironment:                coreURL,
		gatewayCoreCAEnvironment:                 write("ca.pem", pki.caPEM, 0o644),
		gatewayCoreClientCertificateEnvironment:  certificatePath,
		gatewayCoreClientKeyEnvironment:          keyPath,
		gatewayCoreServerNameEnvironment:         "localhost",
		gatewaySPIFFEIdentityEnvironment:         identityURI,
		gatewayExecutorIDEnvironment:             executorID,
		gatewayCapabilityIssuerEnvironment:       "https://core.agentserver.test",
		gatewayCapabilityKeyringEnvironment:      write("run-capability-keyring.json", keyring, 0o644),
		gatewayExecutionPolicyVersionEnvironment: "production-test-v1",
		gatewayShellPolicyDecisionEnvironment:    "deny",
		gatewayReadPolicyDecisionEnvironment:     "deny",
	}
}

func executorGatewayAddressFromStartup(t *testing.T, line string) string {
	t.Helper()
	const prefix = "public agentx HTTP on "
	start := strings.Index(line, prefix)
	if start < 0 {
		t.Fatalf("executor-gateway startup line = %q", line)
	}
	rest := line[start+len(prefix):]
	end := strings.Index(rest, "; internal MCP TLS on ")
	if end < 0 {
		t.Fatalf("executor-gateway startup line = %q", line)
	}
	address := rest[:end]
	if _, _, err := net.SplitHostPort(address); err != nil {
		t.Fatalf("executor-gateway startup address %q: %v", address, err)
	}
	return address
}

func executorGatewayInternalAddressFromStartup(t *testing.T, line string) string {
	t.Helper()
	const prefix = "; internal MCP TLS on "
	start := strings.Index(line, prefix)
	if start < 0 {
		t.Fatalf("executor-gateway startup line = %q", line)
	}
	rest := line[start+len(prefix):]
	end := strings.Index(rest, executorgateway.ExecutorMCPPath+";")
	if end < 0 {
		t.Fatalf("executor-gateway startup line = %q", line)
	}
	address := rest[:end]
	if _, _, err := net.SplitHostPort(address); err != nil {
		t.Fatalf("executor-gateway internal startup address %q: %v", address, err)
	}
	return address
}

func writeExecutorGatewayTestCoreJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}
