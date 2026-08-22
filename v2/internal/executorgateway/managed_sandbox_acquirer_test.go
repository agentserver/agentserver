package executorgateway

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/executorgateway/mcpcontract"
	"github.com/agentserver/agentserver/v2/internal/sandboxclient"
	"github.com/agentserver/agentserver/v2/internal/sandboxcontract"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestGatewayManagedSandboxSessionAcquirerEnsuresOnDemandAndReleases(t *testing.T) {
	client := &recordingManagedSandboxLifecycleClient{}
	acquirer, err := NewGatewayManagedSandboxSessionAcquirer(
		client,
		ManagedSandboxProvisioningSpec{
			Region: "i18n-tt", EnvironmentID: "a38f69c6-996b-4c8d-8e2a-e97ee69c4b10",
			SandboxTTL: time.Hour, ActivityTTL: 30 * time.Second,
		},
		func() (string, error) { return "90000000-0000-4000-8000-000000000009", nil },
		time.Now,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	principal := testExecutorMCPPrincipal("lazy-sandbox-capability")
	principal.ManagedSandbox = &ExecutorManagedSandboxAuthority{
		SettingVersion: 1, Region: "i18n-tt", EnvironmentID: "a38f69c6-996b-4c8d-8e2a-e97ee69c4b10",
	}
	lease, err := acquirer.Acquire(t.Context(), principal)
	if err != nil {
		t.Fatal(err)
	}
	ensure, renew, release := client.calls()
	if ensure != 1 || renew != 1 || release != 0 {
		t.Fatalf("initial ensure/renew/release calls = %d/%d/%d, want 1/1/0", ensure, renew, release)
	}
	if err := lease.Release(t.Context()); err != nil {
		t.Fatal(err)
	}
	ensure, renew, release = client.calls()
	if ensure != 1 || renew != 1 || release != 1 {
		t.Fatalf("final ensure/renew/release calls = %d/%d/%d, want 1/1/1", ensure, renew, release)
	}
}

func TestExecutorMCPSessionSharesOneLazySandboxLease(t *testing.T) {
	lease := &recordingManagedSandboxSessionLease{done: make(chan struct{})}
	acquirer := &recordingManagedSandboxSessionAcquirer{lease: lease}
	session := &executorMCPSession{principal: testExecutorMCPPrincipal("shared-lazy-sandbox")}

	const callers = 8
	var wait sync.WaitGroup
	wait.Add(callers)
	results := make(chan ManagedSandboxSessionLease, callers)
	for range callers {
		go func() {
			defer wait.Done()
			resolved, err := session.acquireManagedSandbox(t.Context(), acquirer)
			if err != nil {
				t.Errorf("acquire managed sandbox: %v", err)
				return
			}
			results <- resolved
		}()
	}
	wait.Wait()
	close(results)
	for resolved := range results {
		if resolved != lease {
			t.Fatal("concurrent caller received a different managed sandbox lease")
		}
	}
	if acquirer.callCount() != 1 {
		t.Fatalf("managed sandbox acquire calls = %d, want 1", acquirer.callCount())
	}
	if detached := session.detachManagedSandboxLease(); detached != lease {
		t.Fatal("session detached a different managed sandbox lease")
	}
}

func TestExecutorMCPAcquiresManagedSandboxOnlyOnFirstExecutorToolCall(t *testing.T) {
	registry := &recordingMCPEnvironmentRegistry{environments: []RegisteredEnvironment{
		testRegisteredEnvironment(testEnvironmentID, `{"kind":"local","root":"/workspace","displayName":"primary","defaultCwd":"."}`),
	}}
	resolver, err := NewEnvironmentResolver(registry)
	if err != nil {
		t.Fatal(err)
	}
	lease := &recordingManagedSandboxSessionLease{
		done: make(chan struct{}), released: make(chan struct{}),
	}
	acquirer := &recordingManagedSandboxSessionAcquirer{lease: lease}
	config := DefaultExecutorMCPConfig()
	config.ManagedSandboxAcquirer = acquirer
	sequence := 0
	config.IDGenerator = func() (string, error) {
		sequence++
		return fmtMCPTestSessionID(sequence), nil
	}
	handler, err := NewExecutorMCPHandler(
		testExecutorMCPAuthenticator{principals: map[string]ExecutorMCPPrincipal{
			testMCPBearerA: testExecutorMCPPrincipal("lazy-managed-capability"),
		}},
		resolver,
		config,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = handler.Shutdown(ctx)
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	session := connectRawMCPClient(t, server, testMCPBearerA)

	listed, err := session.ListTools(t.Context(), nil)
	if err != nil || len(listed.Tools) != 1 || listed.Tools[0].Name != mcpcontract.ToolListEnvironments {
		t.Fatalf("list tools before lazy acquisition = %+v, %v", listed, err)
	}
	if acquirer.callCount() != 0 {
		t.Fatalf("MCP initialize/tools-list acquired a managed sandbox %d time(s)", acquirer.callCount())
	}
	for call := 1; call <= 2; call++ {
		result, err := session.CallTool(t.Context(), &mcp.CallToolParams{
			Name: mcpcontract.ToolListEnvironments, Arguments: json.RawMessage(`{}`),
		})
		if err != nil || result == nil || result.IsError {
			t.Fatalf("list_environments call %d = %+v, %v", call, result, err)
		}
		if acquirer.callCount() != 1 {
			t.Fatalf("managed sandbox acquire calls after tool call %d = %d, want 1", call, acquirer.callCount())
		}
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-lease.released:
	case <-time.After(time.Second):
		t.Fatal("MCP session close did not asynchronously release managed sandbox activity")
	}
	if lease.releaseCallCount() != 1 {
		t.Fatalf("managed sandbox release calls = %d, want 1", lease.releaseCallCount())
	}
}

func TestReleaseManagedSandboxLeasesRunsConcurrently(t *testing.T) {
	started := make(chan struct{}, 2)
	unblock := make(chan struct{})
	leases := []ManagedSandboxSessionLease{
		newBlockingManagedSandboxSessionLease(started, unblock),
		newBlockingManagedSandboxSessionLease(started, unblock),
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- releaseManagedSandboxLeases(ctx, leases, nil)
	}()
	for range leases {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("managed sandbox shutdown releases were serialized")
		}
	}
	close(unblock)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

type recordingManagedSandboxLifecycleClient struct {
	mu           sync.Mutex
	ensureCalls  int
	renewCalls   int
	releaseCalls int
}

func (client *recordingManagedSandboxLifecycleClient) Ensure(
	_ context.Context,
	_ sandboxcontract.EnsureSandboxRequest,
	_ sandboxclient.TokenRequest,
) (sandboxcontract.EnsureSandboxResponse, error) {
	client.mu.Lock()
	client.ensureCalls++
	client.mu.Unlock()
	return sandboxcontract.EnsureSandboxResponse{Sandbox: testLazyManagedSandbox()}, nil
}

func (client *recordingManagedSandboxLifecycleClient) RenewActivity(
	_ context.Context,
	_ sandboxcontract.RenewSandboxActivityRequest,
	_ sandboxclient.TokenRequest,
) (sandboxcontract.SandboxResponse, error) {
	client.mu.Lock()
	client.renewCalls++
	client.mu.Unlock()
	return sandboxcontract.SandboxResponse{Sandbox: testLazyManagedSandbox()}, nil
}

func (client *recordingManagedSandboxLifecycleClient) ReleaseActivity(
	_ context.Context,
	_ sandboxcontract.ReleaseSandboxActivityRequest,
	_ sandboxclient.TokenRequest,
) (sandboxcontract.SandboxResponse, error) {
	client.mu.Lock()
	client.releaseCalls++
	client.mu.Unlock()
	return sandboxcontract.SandboxResponse{Sandbox: testLazyManagedSandbox(), Changed: true}, nil
}

func (client *recordingManagedSandboxLifecycleClient) calls() (int, int, int) {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.ensureCalls, client.renewCalls, client.releaseCalls
}

func testLazyManagedSandbox() sandboxcontract.Sandbox {
	return sandboxcontract.Sandbox{
		Profile: sandboxcontract.ProfileV1,
		Ref: sandboxcontract.SandboxRef{
			SandboxID: "65000000-0000-4000-8000-000000000006", TargetGeneration: 7,
		},
		State: sandboxcontract.SandboxReady, Root: "/workspace", ExpiresAt: time.Now().UTC().Add(time.Hour),
	}
}

type recordingManagedSandboxSessionAcquirer struct {
	mu    sync.Mutex
	calls int
	lease ManagedSandboxSessionLease
}

func (acquirer *recordingManagedSandboxSessionAcquirer) Acquire(context.Context, ExecutorMCPPrincipal) (ManagedSandboxSessionLease, error) {
	acquirer.mu.Lock()
	defer acquirer.mu.Unlock()
	acquirer.calls++
	return acquirer.lease, nil
}

func (acquirer *recordingManagedSandboxSessionAcquirer) callCount() int {
	acquirer.mu.Lock()
	defer acquirer.mu.Unlock()
	return acquirer.calls
}

type recordingManagedSandboxSessionLease struct {
	done     chan struct{}
	released chan struct{}
	once     sync.Once
	mu       sync.Mutex
	releases int
}

type blockingManagedSandboxSessionLease struct {
	started chan<- struct{}
	unblock <-chan struct{}
	done    chan struct{}
	once    sync.Once
}

func newBlockingManagedSandboxSessionLease(
	started chan<- struct{},
	unblock <-chan struct{},
) *blockingManagedSandboxSessionLease {
	return &blockingManagedSandboxSessionLease{
		started: started,
		unblock: unblock,
		done:    make(chan struct{}),
	}
}

func (lease *blockingManagedSandboxSessionLease) Done() <-chan struct{} { return lease.done }
func (*blockingManagedSandboxSessionLease) Err() error                  { return nil }
func (lease *blockingManagedSandboxSessionLease) Release(ctx context.Context) error {
	lease.started <- struct{}{}
	select {
	case <-lease.unblock:
		lease.once.Do(func() { close(lease.done) })
		return nil
	case <-ctx.Done():
		lease.once.Do(func() { close(lease.done) })
		return ctx.Err()
	}
}

func (lease *recordingManagedSandboxSessionLease) Done() <-chan struct{} { return lease.done }
func (*recordingManagedSandboxSessionLease) Err() error                  { return nil }
func (lease *recordingManagedSandboxSessionLease) Release(context.Context) error {
	lease.mu.Lock()
	lease.releases++
	lease.mu.Unlock()
	lease.once.Do(func() {
		close(lease.done)
		if lease.released != nil {
			close(lease.released)
		}
	})
	return nil
}

func (lease *recordingManagedSandboxSessionLease) releaseCallCount() int {
	lease.mu.Lock()
	defer lease.mu.Unlock()
	return lease.releases
}
