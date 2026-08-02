package executorgateway

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"nhooyr.io/websocket"
)

const (
	testMachineExecutorID = "a1000000-0000-4000-8000-000000000001"
	testMachineWorkspace  = "a1000000-0000-4000-8000-000000000002"
	testMachineGatewayID  = "a1000000-0000-4000-8000-000000000003"
)

func TestProductionExecutorChallengeAuthenticatesBeforeUpgradeAndConsumesOnce(t *testing.T) {
	now := time.Date(2026, 8, 2, 10, 11, 12, 345_000_000, time.UTC)
	publicKey, privateKey, err := ed25519.GenerateKey(bytes.NewReader(bytes.Repeat([]byte{0x31}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	core := &recordingExecutorIdentityCore{machine: testMachineAuthority(now, publicKey)}
	config := DefaultExecutorChallengeConfig(testMachineGatewayID, testMachineExecutorID)
	config.Now = func() time.Time { return now }
	config.Entropy = bytes.NewReader(bytes.Repeat([]byte{0x51}, 32))
	config.IDGenerator = func() (string, error) { return "a1000000-0000-4000-8000-000000000004", nil }
	challenges, err := NewExecutorChallengeAuthority(core, config)
	if err != nil {
		t.Fatal(err)
	}
	const bearer = "opaque-executor-oauth-token"
	challenge, err := challenges.Issue(t.Context(), bearer)
	if err != nil {
		t.Fatal(err)
	}
	if challenge.Version != ExecutorWSSProofVersion || challenge.ExecutorID != testMachineExecutorID ||
		challenge.GatewayInstanceID != testMachineGatewayID || challenge.Target != AgentxConnectPath ||
		challenge.Challenge != base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x51}, 32)) ||
		challenge.MachineKeySHA256 != hex.EncodeToString(core.machine.MachineKeySHA256[:]) ||
		!challenge.ExpiresAt.Equal(now.Add(MaximumExecutorChallengeTTL)) || challenges.Outstanding() != 1 {
		t.Fatalf("challenge = %+v, outstanding %d", challenge, challenges.Outstanding())
	}
	transcript, err := ExecutorWSSProofTranscript(challenge, bearer)
	if err != nil {
		t.Fatal(err)
	}
	signature := base64.RawURLEncoding.EncodeToString(ed25519.Sign(privateKey, transcript))
	authenticator, err := NewProductionExecutorAuthenticator(core, challenges)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "https://gateway.example"+AgentxConnectPath, nil)
	request.Header.Set("Authorization", "Bearer "+bearer)
	request.Header.Set(AgentxChallengeIDHeader, challenge.ChallengeID)
	request.Header.Set(AgentxMachineProofHeader, signature)
	identity, err := authenticator.AuthenticateExecutor(request)
	if err != nil {
		t.Fatal(err)
	}
	if identity.ExecutorID != testMachineExecutorID || challenges.Outstanding() != 0 || core.authorizeCalls != 2 {
		t.Fatalf("identity = %+v, outstanding %d, authorize calls %d", identity, challenges.Outstanding(), core.authorizeCalls)
	}
	if _, err := authenticator.AuthenticateExecutor(request); err == nil || core.authorizeCalls != 3 {
		t.Fatalf("consumed challenge replay error = %v, authorize calls %d", err, core.authorizeCalls)
	}
}

func TestProductionExecutorProofGatesActualWebSocketUpgrade(t *testing.T) {
	now := time.Date(2026, 8, 2, 10, 11, 12, 0, time.UTC)
	publicKey, privateKey, err := ed25519.GenerateKey(bytes.NewReader(bytes.Repeat([]byte{0x38}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	core := &recordingExecutorIdentityCore{machine: testMachineAuthority(now, publicKey)}
	ids := []string{
		"a1100000-0000-4000-8000-000000000001",
		"a1100000-0000-4000-8000-000000000002",
	}
	challengeConfig := DefaultExecutorChallengeConfig(testMachineGatewayID, testMachineExecutorID)
	challengeConfig.Now = func() time.Time { return now }
	challengeConfig.Entropy = bytes.NewReader(bytes.Repeat([]byte{0x52}, 64))
	challengeConfig.IDGenerator = func() (string, error) {
		id := ids[0]
		ids = ids[1:]
		return id, nil
	}
	challenges, err := NewExecutorChallengeAuthority(core, challengeConfig)
	if err != nil {
		t.Fatal(err)
	}
	authenticator, err := NewProductionExecutorAuthenticator(core, challenges)
	if err != nil {
		t.Fatal(err)
	}
	serverConfig := DefaultServerConfig(testMachineGatewayID)
	server, err := NewServer(authenticator, &fakeConnectionAuthority{}, serverConfig)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	const bearer = "opaque-executor-oauth-token"
	valid, err := challenges.Issue(t.Context(), bearer)
	if err != nil {
		t.Fatal(err)
	}
	transcript, err := ExecutorWSSProofTranscript(valid, bearer)
	if err != nil {
		t.Fatal(err)
	}
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+bearer)
	headers.Set(AgentxChallengeIDHeader, valid.ChallengeID)
	headers.Set(AgentxMachineProofHeader, base64.RawURLEncoding.EncodeToString(ed25519.Sign(privateKey, transcript)))
	connection, response, err := websocket.Dial(t.Context(), httpServer.URL+AgentxConnectPath, &websocket.DialOptions{HTTPHeader: headers})
	if err != nil || response == nil || response.StatusCode != http.StatusSwitchingProtocols || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("valid proof WebSocket upgrade = response %+v, error %v", response, err)
	}
	_ = connection.Close(websocket.StatusNormalClosure, "proof accepted")

	invalid, err := challenges.Issue(t.Context(), bearer)
	if err != nil {
		t.Fatal(err)
	}
	headers.Set(AgentxChallengeIDHeader, invalid.ChallengeID)
	headers.Set(AgentxMachineProofHeader, base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x01}, ed25519.SignatureSize)))
	connection, response, err = websocket.Dial(t.Context(), httpServer.URL+AgentxConnectPath, &websocket.DialOptions{HTTPHeader: headers})
	if err == nil || connection != nil || response == nil || response.StatusCode != http.StatusUnauthorized ||
		response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("invalid proof WebSocket upgrade = connection %+v, response %+v, error %v", connection, response, err)
	}
	_ = response.Body.Close()
}

func TestProductionExecutorCoreOutageIsServiceUnavailableAndDoesNotConsumeChallenge(t *testing.T) {
	now := time.Date(2026, 8, 2, 10, 11, 12, 0, time.UTC)
	publicKey, privateKey, err := ed25519.GenerateKey(bytes.NewReader(bytes.Repeat([]byte{0x39}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	core := &recordingExecutorIdentityCore{machine: testMachineAuthority(now, publicKey)}
	config := DefaultExecutorChallengeConfig(testMachineGatewayID, testMachineExecutorID)
	config.Now = func() time.Time { return now }
	config.Entropy = bytes.NewReader(bytes.Repeat([]byte{0x53}, 32))
	config.IDGenerator = func() (string, error) { return "a1200000-0000-4000-8000-000000000001", nil }
	challenges, err := NewExecutorChallengeAuthority(core, config)
	if err != nil {
		t.Fatal(err)
	}
	authenticator, err := NewProductionExecutorAuthenticator(core, challenges)
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(authenticator, &fakeConnectionAuthority{}, DefaultServerConfig(testMachineGatewayID))
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	const bearer = "opaque-executor-oauth-token"
	challenge, err := challenges.Issue(t.Context(), bearer)
	if err != nil {
		t.Fatal(err)
	}
	transcript, err := ExecutorWSSProofTranscript(challenge, bearer)
	if err != nil {
		t.Fatal(err)
	}
	header := http.Header{}
	header.Set("Authorization", "Bearer "+bearer)
	header.Set(AgentxChallengeIDHeader, challenge.ChallengeID)
	header.Set(AgentxMachineProofHeader, base64.RawURLEncoding.EncodeToString(ed25519.Sign(privateKey, transcript)))

	core.mu.Lock()
	core.authorizeErr = errors.New("Core transport contains secret-token")
	core.mu.Unlock()
	connection, response, err := websocket.Dial(t.Context(), httpServer.URL+AgentxConnectPath, &websocket.DialOptions{HTTPHeader: header})
	if err == nil || connection != nil || response == nil || response.StatusCode != http.StatusServiceUnavailable ||
		response.Header.Get("Content-Type") != "application/json" || response.Header.Get("WWW-Authenticate") != "" ||
		challenges.Outstanding() != 1 {
		t.Fatalf("Core outage upgrade = connection %+v, response %+v, error %v, outstanding %d", connection, response, err, challenges.Outstanding())
	}
	body, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr != nil || bytes.Contains(body, []byte("secret-token")) {
		t.Fatalf("Core outage body = %q, error %v", body, readErr)
	}

	core.mu.Lock()
	core.authorizeErr = &CoreCommandError{HTTPStatus: http.StatusForbidden, Code: "forbidden", Message: "secret-token"}
	core.mu.Unlock()
	connection, response, err = websocket.Dial(t.Context(), httpServer.URL+AgentxConnectPath, &websocket.DialOptions{HTTPHeader: header})
	if err == nil || connection != nil || response == nil || response.StatusCode != http.StatusUnauthorized ||
		response.Header.Get("WWW-Authenticate") == "" || challenges.Outstanding() != 1 {
		t.Fatalf("Core rejection upgrade = connection %+v, response %+v, error %v, outstanding %d", connection, response, err, challenges.Outstanding())
	}
	_ = response.Body.Close()

	core.mu.Lock()
	core.authorizeErr = nil
	core.mu.Unlock()
	connection, response, err = websocket.Dial(t.Context(), httpServer.URL+AgentxConnectPath, &websocket.DialOptions{HTTPHeader: header})
	if err != nil || response == nil || response.StatusCode != http.StatusSwitchingProtocols || challenges.Outstanding() != 0 {
		t.Fatalf("recovered Core upgrade = response %+v, error %v, outstanding %d", response, err, challenges.Outstanding())
	}
	_ = connection.Close(websocket.StatusNormalClosure, "proof accepted after retry")
}

func TestExecutorChallengeInvalidProofAndAuthorityDriftAreConsumed(t *testing.T) {
	now := time.Date(2026, 8, 2, 10, 11, 12, 0, time.UTC)
	publicKey, _, err := ed25519.GenerateKey(bytes.NewReader(bytes.Repeat([]byte{0x32}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	core := &recordingExecutorIdentityCore{machine: testMachineAuthority(now, publicKey)}
	ids := []string{
		"a2000000-0000-4000-8000-000000000001",
		"a2000000-0000-4000-8000-000000000002",
	}
	config := DefaultExecutorChallengeConfig(testMachineGatewayID, testMachineExecutorID)
	config.Now = func() time.Time { return now }
	config.Entropy = bytes.NewReader(bytes.Repeat([]byte{0x61}, 64))
	config.IDGenerator = func() (string, error) {
		id := ids[0]
		ids = ids[1:]
		return id, nil
	}
	challenges, err := NewExecutorChallengeAuthority(core, config)
	if err != nil {
		t.Fatal(err)
	}
	authenticator, err := NewProductionExecutorAuthenticator(core, challenges)
	if err != nil {
		t.Fatal(err)
	}
	const bearer = "opaque-executor-oauth-token"

	invalidProof, err := challenges.Issue(t.Context(), bearer)
	if err != nil {
		t.Fatal(err)
	}
	request := machineProofRequest(invalidProof, bearer, base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x01}, ed25519.SignatureSize)))
	if _, err := authenticator.AuthenticateExecutor(request); err == nil || challenges.Outstanding() != 0 {
		t.Fatalf("invalid proof error = %v, outstanding %d", err, challenges.Outstanding())
	}

	drift, err := challenges.Issue(t.Context(), bearer)
	if err != nil {
		t.Fatal(err)
	}
	_, differentPrivateKey, err := ed25519.GenerateKey(bytes.NewReader(bytes.Repeat([]byte{0x72}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	transcript, err := ExecutorWSSProofTranscript(drift, bearer)
	if err != nil {
		t.Fatal(err)
	}
	request = machineProofRequest(drift, bearer, base64.RawURLEncoding.EncodeToString(ed25519.Sign(differentPrivateKey, transcript)))
	core.mu.Lock()
	core.machine.ExecutorVersion++
	core.mu.Unlock()
	if _, err := authenticator.AuthenticateExecutor(request); err == nil || challenges.Outstanding() != 0 {
		t.Fatalf("authority drift error = %v, outstanding %d", err, challenges.Outstanding())
	}
}

func TestExecutorChallengeCapacityExpiryAndTokenDeadline(t *testing.T) {
	now := time.Date(2026, 8, 2, 10, 11, 12, 0, time.UTC)
	publicKey, _, err := ed25519.GenerateKey(bytes.NewReader(bytes.Repeat([]byte{0x33}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	core := &recordingExecutorIdentityCore{machine: testMachineAuthority(now, publicKey)}
	core.machine.TokenExpiresAt = now.Add(5 * time.Second)
	ids := []string{
		"a3000000-0000-4000-8000-000000000001",
		"a3000000-0000-4000-8000-000000000002",
		"a3000000-0000-4000-8000-000000000003",
	}
	config := DefaultExecutorChallengeConfig(testMachineGatewayID, testMachineExecutorID)
	config.MaximumOutstanding = 1
	config.Now = func() time.Time { return now }
	config.Entropy = bytes.NewReader(bytes.Repeat([]byte{0x73}, 96))
	config.IDGenerator = func() (string, error) {
		id := ids[0]
		ids = ids[1:]
		return id, nil
	}
	challenges, err := NewExecutorChallengeAuthority(core, config)
	if err != nil {
		t.Fatal(err)
	}
	first, err := challenges.Issue(t.Context(), "token-a")
	if err != nil || !first.ExpiresAt.Equal(now.Add(5*time.Second)) {
		t.Fatalf("first challenge = %+v, error %v", first, err)
	}
	if _, err := challenges.Issue(t.Context(), "token-a"); err == nil {
		t.Fatal("challenge capacity overflow was accepted")
	}
	now = now.Add(5 * time.Second)
	core.mu.Lock()
	core.machine.TokenExpiresAt = now.Add(5 * time.Minute)
	core.mu.Unlock()
	if _, err := challenges.Issue(t.Context(), "token-a"); err != nil {
		t.Fatalf("expired challenge did not free capacity: %v", err)
	}
	if challenges.Outstanding() != 1 {
		t.Fatalf("outstanding challenges = %d", challenges.Outstanding())
	}
}

func TestExecutorWSSProofTranscriptHasStableGoldenDigest(t *testing.T) {
	response := ExecutorChallengeResponse{
		Version:           ExecutorWSSProofVersion,
		ChallengeID:       "a4000000-0000-4000-8000-000000000001",
		Challenge:         base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x41}, 32)),
		ExecutorID:        "a4000000-0000-4000-8000-000000000002",
		MachineKeySHA256:  strings.Repeat("ab", 32),
		GatewayInstanceID: "a4000000-0000-4000-8000-000000000003",
		Target:            AgentxConnectPath,
		IssuedAt:          time.UnixMilli(1_800_000_000_123).UTC(),
		ExpiresAt:         time.UnixMilli(1_800_000_030_123).UTC(),
	}
	transcript, err := ExecutorWSSProofTranscript(response, "golden-oauth-bearer")
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(transcript)
	const want = "6399fd9b16c2b8a75590083505bb08492a32d4a8b5548d9d74b560c344d484f4"
	if got := hex.EncodeToString(digest[:]); got != want {
		t.Fatalf("transcript SHA-256 = %s", got)
	}
}

func TestExecutorChallengeUsesCanonicalMillisecondTimestamps(t *testing.T) {
	now := time.Date(2026, 8, 2, 10, 11, 12, 345_678_901, time.FixedZone("offset", 8*60*60))
	publicKey, _, err := ed25519.GenerateKey(bytes.NewReader(bytes.Repeat([]byte{0x42}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	core := &recordingExecutorIdentityCore{machine: testMachineAuthority(now, publicKey)}
	config := DefaultExecutorChallengeConfig(testMachineGatewayID, testMachineExecutorID)
	config.Now = func() time.Time { return now }
	config.Entropy = bytes.NewReader(bytes.Repeat([]byte{0x54}, 32))
	config.IDGenerator = func() (string, error) { return "a4100000-0000-4000-8000-000000000001", nil }
	challenges, err := NewExecutorChallengeAuthority(core, config)
	if err != nil {
		t.Fatal(err)
	}
	response, err := challenges.Issue(t.Context(), "opaque-token")
	if err != nil {
		t.Fatal(err)
	}
	wantIssuedAt := now.UTC().Truncate(time.Millisecond)
	if !response.IssuedAt.Equal(wantIssuedAt) || response.IssuedAt.Location() != time.UTC ||
		response.IssuedAt.Nanosecond()%int(time.Millisecond) != 0 || response.ExpiresAt.Nanosecond()%int(time.Millisecond) != 0 {
		t.Fatalf("challenge timestamps = %s / %s, want whole-millisecond UTC", response.IssuedAt, response.ExpiresAt)
	}
	response.IssuedAt = response.IssuedAt.Add(time.Microsecond)
	if _, err := ExecutorWSSProofTranscript(response, "opaque-token"); err == nil {
		t.Fatal("sub-millisecond challenge timestamp was accepted")
	}
}

func TestExecutorIdentityHandlerRelaysEnrollmentAndIssuesChallenge(t *testing.T) {
	now := time.Date(2026, 8, 2, 10, 11, 12, 0, time.UTC)
	publicKey, _, err := ed25519.GenerateKey(bytes.NewReader(bytes.Repeat([]byte{0x34}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	core := &recordingExecutorIdentityCore{
		machine: testMachineAuthority(now, publicKey),
		enrollment: corecontract.CompleteExecutorEnrollmentResponse{
			Executor: corecontract.ExecutorResourceState{
				ExecutorID: testMachineExecutorID, WorkspaceID: testMachineWorkspace, Status: "offline", Version: 2,
				CreatedAt: now.Add(-time.Hour), UpdatedAt: now,
			},
			OAuthClientID: "agentserver-executor-" + testMachineExecutorID,
			Audience:      "executor-gateway", Scope: "executor:connect",
		},
	}
	config := DefaultExecutorChallengeConfig(testMachineGatewayID, testMachineExecutorID)
	config.Now = func() time.Time { return now }
	config.Entropy = bytes.NewReader(bytes.Repeat([]byte{0x75}, 32))
	config.IDGenerator = func() (string, error) { return "a5000000-0000-4000-8000-000000000001", nil }
	challenges, err := NewExecutorChallengeAuthority(core, config)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewExecutorIdentityHandler(core, challenges)
	if err != nil {
		t.Fatal(err)
	}

	command := testEnrollmentCommand()
	body, err := json.Marshal(command)
	if err != nil {
		t.Fatal(err)
	}
	enrollmentRequest := httptest.NewRequest(http.MethodPost, "https://gateway.example"+AgentxEnrollmentPath, bytes.NewReader(body))
	enrollmentRequest.Header.Set("Content-Type", "application/json")
	enrollmentRequest.Header.Set("Authorization", "Bearer asv2enr1.claims.mac")
	enrollmentResponse := httptest.NewRecorder()
	handler.ServeHTTP(enrollmentResponse, enrollmentRequest)
	if enrollmentResponse.Code != http.StatusOK || enrollmentResponse.Header().Get("Cache-Control") != "no-store" ||
		enrollmentResponse.Header().Get("X-Content-Type-Options") != "nosniff" || core.completeCalls != 1 ||
		core.enrollmentBearer != "asv2enr1.claims.mac" || core.expectedExecutor != testMachineExecutorID ||
		core.enrollmentRequest.AgentxVersion != command.AgentxVersion {
		t.Fatalf("enrollment response = %d %s, core = %+v", enrollmentResponse.Code, enrollmentResponse.Body, core)
	}

	challengeRequest := httptest.NewRequest(http.MethodPost, "https://gateway.example"+AgentxChallengePath, nil)
	challengeRequest.Header.Set("Authorization", "Bearer opaque-token")
	challengeResponse := httptest.NewRecorder()
	handler.ServeHTTP(challengeResponse, challengeRequest)
	if challengeResponse.Code != http.StatusCreated || challengeResponse.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("challenge response = %d %s", challengeResponse.Code, challengeResponse.Body)
	}

	duplicate := httptest.NewRequest(http.MethodPost, "https://gateway.example"+AgentxEnrollmentPath,
		strings.NewReader(`{"machinePublicKeyEd25519":"a","machinePublicKeyEd25519":"b"}`))
	duplicate.Header.Set("Content-Type", "application/json")
	duplicate.Header.Set("Authorization", "Bearer asv2enr1.claims.mac")
	duplicateResponse := httptest.NewRecorder()
	handler.ServeHTTP(duplicateResponse, duplicate)
	if duplicateResponse.Code != http.StatusBadRequest || core.completeCalls != 1 {
		t.Fatalf("duplicate enrollment response = %d %s, calls %d", duplicateResponse.Code, duplicateResponse.Body, core.completeCalls)
	}

	withBody := httptest.NewRequest(http.MethodPost, "https://gateway.example"+AgentxChallengePath, strings.NewReader("{}"))
	withBody.Header.Set("Authorization", "Bearer opaque-token")
	withBodyResponse := httptest.NewRecorder()
	handler.ServeHTTP(withBodyResponse, withBody)
	if withBodyResponse.Code != http.StatusBadRequest {
		t.Fatalf("challenge body response = %d %s", withBodyResponse.Code, withBodyResponse.Body)
	}
}

func TestExecutorIdentityHandlerRejectsEnrollmentForAnotherDeployment(t *testing.T) {
	now := time.Date(2026, 8, 2, 10, 11, 12, 0, time.UTC)
	publicKey, _, err := ed25519.GenerateKey(bytes.NewReader(bytes.Repeat([]byte{0x43}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	otherExecutorID := "a5100000-0000-4000-8000-000000000099"
	core := &recordingExecutorIdentityCore{
		machine: testMachineAuthority(now, publicKey),
		enrollment: corecontract.CompleteExecutorEnrollmentResponse{
			Executor: corecontract.ExecutorResourceState{
				ExecutorID: otherExecutorID, WorkspaceID: testMachineWorkspace, Status: "offline", Version: 2,
				CreatedAt: now.Add(-time.Hour), UpdatedAt: now,
			},
			OAuthClientID: "agentserver-executor-" + otherExecutorID,
			Audience:      "executor-gateway", Scope: "executor:connect",
		},
	}
	challenges, err := NewExecutorChallengeAuthority(core, DefaultExecutorChallengeConfig(testMachineGatewayID, testMachineExecutorID))
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewExecutorIdentityHandler(core, challenges)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(testEnrollmentCommand())
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "https://gateway.example"+AgentxEnrollmentPath, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer asv2enr1.claims.mac")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadGateway || core.completeCalls != 1 || strings.Contains(response.Body.String(), otherExecutorID) {
		t.Fatalf("foreign enrollment response = %d %s, Core calls %d", response.Code, response.Body, core.completeCalls)
	}
}

func machineProofRequest(challenge ExecutorChallengeResponse, bearer, signature string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, "https://gateway.example"+AgentxConnectPath, nil)
	request.Header.Set("Authorization", "Bearer "+bearer)
	request.Header.Set(AgentxChallengeIDHeader, challenge.ChallengeID)
	request.Header.Set(AgentxMachineProofHeader, signature)
	return request
}

func testMachineAuthority(now time.Time, publicKey ed25519.PublicKey) ExecutorMachineAuthority {
	digest := sha256.Sum256(publicKey)
	return ExecutorMachineAuthority{
		ExecutorID: testMachineExecutorID, WorkspaceID: testMachineWorkspace,
		OAuthClientID:           "agentserver-executor-" + testMachineExecutorID,
		MachinePublicKeyEd25519: append(ed25519.PublicKey(nil), publicKey...), MachineKeySHA256: digest,
		ExecutorVersion: 2, TokenExpiresAt: now.Add(5 * time.Minute), AuthorizedAt: now.Add(-time.Hour),
	}
}

func testEnrollmentCommand() corecontract.CompleteExecutorEnrollmentRequest {
	return corecontract.CompleteExecutorEnrollmentRequest{
		MachinePublicKeyEd25519: strings.Repeat("a", 43), MachineProofEd25519: strings.Repeat("b", 86),
		OAuthPublicKeyP256X: strings.Repeat("c", 43), OAuthPublicKeyP256Y: strings.Repeat("d", 43),
		OAuthProofES256: strings.Repeat("e", 86), AgentxVersion: "1.0.0",
		RuntimeManifestSHA256: strings.Repeat("1", 64), ExecProtocolSourceSHA256: strings.Repeat("2", 64),
		Environments: []corecontract.ExecutorEnrollmentEnvironment{},
	}
}

type recordingExecutorIdentityCore struct {
	mu sync.Mutex

	machine       ExecutorMachineAuthority
	enrollment    corecontract.CompleteExecutorEnrollmentResponse
	authorizeErr  error
	enrollmentErr error

	authorizeCalls    int
	completeCalls     int
	enrollmentBearer  string
	expectedExecutor  string
	enrollmentRequest corecontract.CompleteExecutorEnrollmentRequest
}

func (core *recordingExecutorIdentityCore) AuthorizeExecutorConnection(context.Context, string) (ExecutorMachineAuthority, error) {
	core.mu.Lock()
	defer core.mu.Unlock()
	core.authorizeCalls++
	if core.authorizeErr != nil {
		return ExecutorMachineAuthority{}, core.authorizeErr
	}
	result := core.machine
	result.MachinePublicKeyEd25519 = append(ed25519.PublicKey(nil), core.machine.MachinePublicKeyEd25519...)
	return result, nil
}

func (core *recordingExecutorIdentityCore) CompleteExecutorEnrollment(
	_ context.Context,
	bearer, expectedExecutorID string,
	request corecontract.CompleteExecutorEnrollmentRequest,
) (corecontract.CompleteExecutorEnrollmentResponse, error) {
	core.mu.Lock()
	defer core.mu.Unlock()
	core.completeCalls++
	core.enrollmentBearer = bearer
	core.expectedExecutor = expectedExecutorID
	core.enrollmentRequest = request
	if core.enrollmentErr != nil {
		return corecontract.CompleteExecutorEnrollmentResponse{}, core.enrollmentErr
	}
	return core.enrollment, nil
}

var _ ExecutorIdentityCore = (*recordingExecutorIdentityCore)(nil)

func TestExecutorIdentityHandlerMapsCoreDenialWithoutReflectingBearer(t *testing.T) {
	now := time.Now().UTC()
	publicKey, _, err := ed25519.GenerateKey(bytes.NewReader(bytes.Repeat([]byte{0x35}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	core := &recordingExecutorIdentityCore{
		machine:      testMachineAuthority(now, publicKey),
		authorizeErr: &CoreCommandError{HTTPStatus: http.StatusForbidden, Code: "secret-token", Message: "secret-token"},
	}
	config := DefaultExecutorChallengeConfig(testMachineGatewayID, testMachineExecutorID)
	challenges, err := NewExecutorChallengeAuthority(core, config)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewExecutorIdentityHandler(core, challenges)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "https://gateway.example"+AgentxChallengePath, nil)
	request.Header.Set("Authorization", "Bearer secret-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || strings.Contains(response.Body.String(), "secret-token") ||
		!strings.Contains(response.Body.String(), `"code":"forbidden"`) {
		t.Fatalf("Core denial response = %d %s", response.Code, response.Body)
	}
}

func TestExecutorChallengeRejectsInvalidConfiguration(t *testing.T) {
	core := &recordingExecutorIdentityCore{}
	valid := DefaultExecutorChallengeConfig(testMachineGatewayID, testMachineExecutorID)
	for name, mutate := range map[string]func(*ExecutorChallengeConfig){
		"ttl":      func(value *ExecutorChallengeConfig) { value.TTL = MaximumExecutorChallengeTTL + time.Millisecond },
		"target":   func(value *ExecutorChallengeConfig) { value.Target += "?other" },
		"capacity": func(value *ExecutorChallengeConfig) { value.MaximumOutstanding = 0 },
		"entropy":  func(value *ExecutorChallengeConfig) { value.Entropy = nil },
		"executor": func(value *ExecutorChallengeConfig) { value.ExpectedExecutorID = "" },
	} {
		t.Run(name, func(t *testing.T) {
			config := valid
			mutate(&config)
			if _, err := NewExecutorChallengeAuthority(core, config); err == nil {
				t.Fatal("invalid challenge configuration was accepted")
			}
		})
	}
	if _, err := NewProductionExecutorAuthenticator(nil, nil); err == nil {
		t.Fatal("nil production authenticator dependencies were accepted")
	}
	if _, err := NewExecutorIdentityHandler(nil, nil); err == nil {
		t.Fatal("nil identity handler dependencies were accepted")
	}
}

func TestExecutorChallengeCoreFailureDoesNotCreateAuthority(t *testing.T) {
	core := &recordingExecutorIdentityCore{authorizeErr: errors.New("unavailable")}
	config := DefaultExecutorChallengeConfig(testMachineGatewayID, testMachineExecutorID)
	challenges, err := NewExecutorChallengeAuthority(core, config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := challenges.Issue(t.Context(), "opaque-token"); err == nil || challenges.Outstanding() != 0 {
		t.Fatalf("Core failure issue error = %v, outstanding %d", err, challenges.Outstanding())
	}
}

func TestCoreConnectionClientRelaysEnrollmentAndLiveAuthorizesMachine(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	publicKey, _, err := ed25519.GenerateKey(bytes.NewReader(bytes.Repeat([]byte{0x36}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	machineDigest := sha256.Sum256(publicKey)
	command := testEnrollmentCommand()
	enrollmentResult := corecontract.CompleteExecutorEnrollmentResponse{
		Executor: corecontract.ExecutorResourceState{
			ExecutorID: testMachineExecutorID, WorkspaceID: testMachineWorkspace, Status: "offline", Version: 2,
			CreatedAt: now.Add(-time.Hour), UpdatedAt: now,
		},
		OAuthClientID: "agentserver-executor-" + testMachineExecutorID,
		Audience:      "executor-gateway", Scope: "executor:connect",
	}
	authorizeResult := corecontract.AuthorizeExecutorConnectionResponse{
		ExecutorID: testMachineExecutorID, WorkspaceID: testMachineWorkspace,
		OAuthClientID:           "agentserver-executor-" + testMachineExecutorID,
		MachinePublicKeyEd25519: base64.RawURLEncoding.EncodeToString(publicKey),
		MachineKeySHA256:        hex.EncodeToString(machineDigest[:]), ExecutorVersion: 2,
		TokenExpiresAt: now.Add(5 * time.Minute), AuthorizedAt: now.Add(-time.Hour),
	}
	var mu sync.Mutex
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		mu.Lock()
		paths = append(paths, request.URL.Path)
		mu.Unlock()
		response.Header().Set("Cache-Control", "no-store")
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case corecontract.CompleteExecutorEnrollmentPath:
			if request.Method != http.MethodPost || request.Header.Get("Authorization") != "Bearer asv2enr1.claims.mac" ||
				request.Header.Get("Content-Type") != "application/json" ||
				request.Header.Get(corecontract.ExpectedExecutorIDHeader) != testMachineExecutorID {
				t.Errorf("enrollment HTTP request = %s headers %+v", request.Method, request.Header)
			}
			var got corecontract.CompleteExecutorEnrollmentRequest
			if err := json.NewDecoder(request.Body).Decode(&got); err != nil || got.AgentxVersion != command.AgentxVersion {
				t.Errorf("decode enrollment request = %+v, %v", got, err)
			}
			_ = json.NewEncoder(response).Encode(enrollmentResult)
		case corecontract.AuthorizeExecutorConnectionPath:
			if request.Method != http.MethodPost || request.Header.Get("Authorization") != "Bearer opaque-token" ||
				request.ContentLength != 0 || request.Header.Get("Content-Type") != "" {
				t.Errorf("authorization HTTP request = %s content-length %d headers %+v", request.Method, request.ContentLength, request.Header)
			}
			_ = json.NewEncoder(response).Encode(authorizeResult)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	client, err := NewCoreConnectionClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	completed, err := client.CompleteExecutorEnrollment(t.Context(), "asv2enr1.claims.mac", testMachineExecutorID, command)
	if err != nil || completed.OAuthClientID != enrollmentResult.OAuthClientID {
		t.Fatalf("complete enrollment = %+v, %v", completed, err)
	}
	machine, err := client.AuthorizeExecutorConnection(t.Context(), "opaque-token")
	if err != nil {
		t.Fatal(err)
	}
	if machine.ExecutorID != testMachineExecutorID || machine.MachineKeySHA256 != machineDigest ||
		!bytes.Equal(machine.MachinePublicKeyEd25519, publicKey) || !machine.TokenExpiresAt.Equal(authorizeResult.TokenExpiresAt) {
		t.Fatalf("machine authority = %+v", machine)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(paths) != 2 || paths[0] != corecontract.CompleteExecutorEnrollmentPath || paths[1] != corecontract.AuthorizeExecutorConnectionPath {
		t.Fatalf("Core identity paths = %v", paths)
	}
}

func TestCoreConnectionClientMachineAuthorizationRejectsCacheableDuplicateAndDriftedResponses(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	publicKey, _, err := ed25519.GenerateKey(bytes.NewReader(bytes.Repeat([]byte{0x37}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(publicKey)
	valid := corecontract.AuthorizeExecutorConnectionResponse{
		ExecutorID: testMachineExecutorID, WorkspaceID: testMachineWorkspace,
		OAuthClientID:           "agentserver-executor-" + testMachineExecutorID,
		MachinePublicKeyEd25519: base64.RawURLEncoding.EncodeToString(publicKey),
		MachineKeySHA256:        hex.EncodeToString(digest[:]), ExecutorVersion: 2,
		TokenExpiresAt: now.Add(5 * time.Minute), AuthorizedAt: now.Add(-time.Hour),
	}
	validBody, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	duplicateBody := bytes.Replace(validBody, []byte(`{"executorId":`), []byte(`{"executorId":"`+testMachineExecutorID+`","executorId":`), 1)
	for _, test := range []struct {
		name  string
		cache string
		body  []byte
	}{
		{name: "cacheable", body: validBody},
		{name: "duplicate", cache: "no-store", body: duplicateBody},
		{name: "fingerprint drift", cache: "no-store", body: bytes.Replace(validBody, []byte(valid.MachineKeySHA256), []byte(strings.Repeat("0", 64)), 1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				if test.cache != "" {
					response.Header().Set("Cache-Control", test.cache)
				}
				response.Header().Set("Content-Type", "application/json")
				_, _ = response.Write(test.body)
			}))
			defer server.Close()
			client, err := NewCoreConnectionClient(server.URL, server.Client())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.AuthorizeExecutorConnection(t.Context(), "opaque-token"); err == nil {
				t.Fatal("invalid Core machine authorization response was accepted")
			}
		})
	}
}
