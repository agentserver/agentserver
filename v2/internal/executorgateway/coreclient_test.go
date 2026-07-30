package executorgateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/coredb"
	"github.com/agentserver/agentserver/v2/internal/coreserver"
)

func TestCoreConnectionClientRoundTrip(t *testing.T) {
	commands := &recordingCoreCommands{}
	handler, err := coreserver.NewExecutorConnectionHandler(allowCoreWorkload{}, commands)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := NewCoreConnectionClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}

	runtimeDigest := sha256.Sum256([]byte("runtime"))
	protocolDigest := sha256.Sum256([]byte("protocol"))
	codexDigest := sha256.Sum256([]byte("codex"))
	acquired, err := client.AcquireConnection(t.Context(), AcquireConnectionRequest{
		ExecutorID:               testExecutorID,
		ConnectionID:             testConnectionID(90),
		SessionID:                "70000000-0000-4000-8000-000000000090",
		GatewayInstanceID:        testGatewayInstanceID,
		AgentxVersion:            "0.1.0",
		RuntimeManifestSHA256:    runtimeDigest,
		ExecProtocolSourceSHA256: protocolDigest,
		LeaseTTL:                 45 * time.Second,
		Environments: []EnvironmentDeclaration{{
			ID:                  testEnvironmentID,
			Platform:            "linux-arm64",
			CodexRelease:        "0.146.0",
			CodexCommit:         strings.Repeat("a", 40),
			CodexSHA256:         codexDigest,
			OuterProfileVersion: "process-v1",
			ProcessMethods:      []string{"process/start", "process/read", "process/write", "process/terminate"},
		}},
	})
	if err != nil {
		t.Fatalf("AcquireConnection() error = %v", err)
	}
	if acquired.Generation != 7 || acquired.Status != "connecting" {
		t.Fatalf("acquired holder = %+v", acquired)
	}
	if commands.lastAcquire.ConnectionLeaseTTLMillis != 45_000 || commands.lastAcquire.RuntimeManifestSHA256 != hex.EncodeToString(runtimeDigest[:]) {
		t.Fatalf("acquire contract = %+v", commands.lastAcquire)
	}

	renewed, err := client.RenewConnection(t.Context(), acquired, 45*time.Second)
	if err != nil || renewed.Generation != acquired.Generation {
		t.Fatalf("RenewConnection() = %+v, %v", renewed, err)
	}
	activated, err := client.ActivateConnection(t.Context(), ActivateConnectionRequest{Holder: renewed, Environments: []EnvironmentDeclaration{{
		ID:                  testEnvironmentID,
		Platform:            "linux-arm64",
		CodexRelease:        "0.146.0",
		CodexCommit:         strings.Repeat("a", 40),
		CodexSHA256:         codexDigest,
		OuterProfileVersion: "process-v1",
		ProcessMethods:      []string{"process/start", "process/read", "process/write", "process/terminate"},
	}}})
	if err != nil || activated.Status != "online" {
		t.Fatalf("ActivateConnection() = %+v, %v", activated, err)
	}
	if err := client.FenceConnection(t.Context(), activated); err != nil {
		t.Fatalf("FenceConnection() error = %v", err)
	}
	if !commands.fenced {
		t.Fatal("core fence command was not delivered")
	}
}

func TestCoreConnectionClientMapsFencedAndRejectsRedirect(t *testing.T) {
	commands := &recordingCoreCommands{renewError: &coredb.StateError{
		Code:              coredb.ErrorConnectionFenced,
		Message:           "stale holder",
		CurrentGeneration: 9,
	}}
	handler, err := coreserver.NewExecutorConnectionHandler(allowCoreWorkload{}, commands)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := NewCoreConnectionClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.RenewConnection(t.Context(), testCoreHolder(), time.Minute)
	if !errors.Is(err, ErrConnectionFenced) {
		t.Fatalf("fenced core error = %v", err)
	}

	redirect := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.Redirect(response, request, server.URL, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(redirect.Close)
	redirectClient, err := NewCoreConnectionClient(redirect.URL, redirect.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = redirectClient.RenewConnection(t.Context(), testCoreHolder(), time.Minute)
	if err == nil || !strings.Contains(err.Error(), "HTTP 307") {
		t.Fatalf("redirect core error = %v", err)
	}
}

func TestCoreConnectionClientRejectsCleartextNonLoopback(t *testing.T) {
	if _, err := NewCoreConnectionClient("http://core.internal:8080", http.DefaultClient); err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("cleartext non-loopback error = %v", err)
	}
}

type allowCoreWorkload struct{}

func (allowCoreWorkload) AuthorizeWorkload(*http.Request, string) error { return nil }

type recordingCoreCommands struct {
	mu          sync.Mutex
	lastAcquire corecontract.AcquireExecutorConnectionRequest
	holder      corecontract.ConnectionHolder
	renewError  error
	fenced      bool
}

func (commands *recordingCoreCommands) AcquireExecutorConnection(_ context.Context, request corecontract.AcquireExecutorConnectionRequest) (corecontract.ConnectionHolder, error) {
	commands.mu.Lock()
	defer commands.mu.Unlock()
	commands.lastAcquire = request
	commands.holder = corecontract.ConnectionHolder{
		ExecutorID:        request.ExecutorID,
		ConnectionID:      request.ConnectionID,
		SessionID:         request.SessionID,
		GatewayInstanceID: request.GatewayInstanceID,
		Generation:        7,
		Status:            "connecting",
		ExpiresAt:         time.Now().Add(time.Minute),
	}
	return commands.holder, nil
}

func (commands *recordingCoreCommands) RenewExecutorConnection(_ context.Context, request corecontract.RenewExecutorConnectionRequest) (corecontract.ConnectionHolder, error) {
	commands.mu.Lock()
	defer commands.mu.Unlock()
	if commands.renewError != nil {
		return corecontract.ConnectionHolder{}, commands.renewError
	}
	commands.holder = request.Holder
	commands.holder.ExpiresAt = time.Now().Add(time.Minute)
	return commands.holder, nil
}

func (commands *recordingCoreCommands) ActivateExecutorConnection(_ context.Context, request corecontract.ActivateExecutorConnectionRequest) (corecontract.ConnectionHolder, error) {
	commands.mu.Lock()
	defer commands.mu.Unlock()
	commands.holder = request.Holder
	commands.holder.Status = "online"
	return commands.holder, nil
}

func (commands *recordingCoreCommands) FenceExecutorConnection(_ context.Context, request corecontract.FenceExecutorConnectionRequest) error {
	commands.mu.Lock()
	defer commands.mu.Unlock()
	commands.fenced = true
	commands.holder = request.Holder
	commands.holder.Status = "fenced"
	return nil
}

func testCoreHolder() ConnectionHolder {
	return ConnectionHolder{
		ExecutorID:        testExecutorID,
		ConnectionID:      testConnectionID(99),
		SessionID:         "70000000-0000-4000-8000-000000000099",
		GatewayInstanceID: testGatewayInstanceID,
		Generation:        7,
		Status:            "online",
		ExpiresAt:         time.Now().Add(time.Minute),
	}
}
