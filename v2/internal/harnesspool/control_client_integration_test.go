package harnesspool

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/codexwire"
	"github.com/agentserver/agentserver/v2/internal/harnesscontrol"
	"github.com/agentserver/agentserver/v2/internal/harnessworker"
	"github.com/agentserver/agentserver/v2/internal/runevent"
	"github.com/agentserver/agentserver/v2/internal/runmanifest"
)

func TestWorkerControlClientRunsAuthenticatedLifecycleAndInterrupt(t *testing.T) {
	lifecycle := &recordingAttemptLifecycle{}
	fixture := newWorkerControlIntegrationFixture(t, lifecycle)
	client := fixture.newClient(t, fixture.control.Capability(), fixture.prepared.Manifest, fixture.prepared.SignedManifest)
	startWorkerControlClient(t, client)

	ctx := testContext(t)
	if err := client.SendThreadReady(ctx, "thread-worker-control", false); err != nil {
		t.Fatal(err)
	}
	if err := client.SendTurnAccepted(ctx, "thread-worker-control", "turn-worker-control"); err != nil {
		t.Fatal(err)
	}
	interrupt := harnesscontrol.InterruptCommand{
		Kind: harnesscontrol.CommandKindInterrupt, Reason: "cancelled", GraceMillis: 1_000,
		Message: "cancel the active attempt",
	}
	if err := fixture.control.SendInterrupt(ctx, interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case received := <-client.Interrupts():
		if received != interrupt {
			t.Fatalf("interrupt = %+v, want %+v", received, interrupt)
		}
	case <-ctx.Done():
		t.Fatal("worker did not receive interrupt")
	}
	waitForControlJournalFrames(t, fixture.control, 0)

	wantTerminal := harnesscontrol.TurnTerminalEvent{
		Kind: harnesscontrol.EventKindTurnTerminal, ThreadID: "thread-worker-control",
		TurnID: "turn-worker-control", Status: "completed", RolloutLocator: testCompletedRolloutLocator,
	}
	if err := client.SendTurnTerminal(ctx, wantTerminal); err != nil {
		t.Fatal(err)
	}
	terminal, err := fixture.control.WaitTerminal(ctx)
	if err != nil || terminal != wantTerminal {
		t.Fatalf("WaitTerminal() = %+v, %v", terminal, err)
	}
	if err := client.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	threads, turns := lifecycle.snapshot()
	if len(threads) != 1 || threads[0] != "thread-worker-control" ||
		len(turns) != 1 || turns[0] != [2]string{"thread-worker-control", "turn-worker-control"} {
		t.Fatalf("lifecycle calls = threads %q turns %q", threads, turns)
	}
}

func TestWorkerControlClientResumesAckLostAfterLifecycleAuthorityOnce(t *testing.T) {
	lifecycle := &resumeBlockingAttemptLifecycle{
		started: make(chan struct{}), release: make(chan struct{}),
	}
	t.Cleanup(lifecycle.unblock)
	fixture := newWorkerControlIntegrationFixture(t, lifecycle)
	client := fixture.newClient(t, fixture.control.Capability(), fixture.prepared.Manifest, fixture.prepared.SignedManifest)
	startWorkerControlClient(t, client)

	ctx := testContext(t)
	threadResult := make(chan error, 1)
	go func() {
		threadResult <- client.SendThreadReady(ctx, "thread-resumed-control", false)
	}()
	select {
	case <-lifecycle.started:
	case <-ctx.Done():
		t.Fatal("thread lifecycle authority was not entered")
	}
	closeActiveControlConnection(t, fixture.control)
	lifecycle.unblock()
	select {
	case err := <-threadResult:
		if err != nil {
			t.Fatalf("SendThreadReady() after resume = %v", err)
		}
	case <-ctx.Done():
		t.Fatal("worker did not reconcile the committed thread event after resume")
	}
	if calls := lifecycle.threadCallCount(); calls != 1 {
		t.Fatalf("ThreadStarted calls = %d, want exactly 1", calls)
	}

	if err := client.SendTurnAccepted(ctx, "thread-resumed-control", "turn-resumed-control"); err != nil {
		t.Fatal(err)
	}
	if err := client.SendTurnTerminal(ctx, harnesscontrol.TurnTerminalEvent{
		Kind: harnesscontrol.EventKindTurnTerminal, ThreadID: "thread-resumed-control",
		TurnID: "turn-resumed-control", Status: "completed", RolloutLocator: testCompletedRolloutLocator,
	}); err != nil {
		t.Fatal(err)
	}
	if err := client.Wait(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerControlClientPipelinesRuntimeEventsThroughPoolAndCoreBeforeTerminal(t *testing.T) {
	fixture := newWorkerControlIntegrationFixture(t, &recordingAttemptLifecycle{})
	core := newRuntimeAppendCore(fixture.prepared)
	authority := &attemptLifecycleAuthority{
		ctx: t.Context(), scheduler: &poolTestScheduler{}, core: core,
		identities: &runtimeSequenceIdentityAllocator{}, prepared: fixture.prepared,
	}
	fixture.control.runtime.lifecycle = authority
	client := fixture.newClient(t, fixture.control.Capability(), fixture.prepared.Manifest, fixture.prepared.SignedManifest)
	startWorkerControlClient(t, client)

	ctx := testContext(t)
	threadID, turnID, callID := "thread-runtime-1", "turn-runtime-1", "call-runtime-1"
	if err := client.SendThreadReady(ctx, threadID, false); err != nil {
		t.Fatal(err)
	}
	if err := client.SendTurnAccepted(ctx, threadID, turnID); err != nil {
		t.Fatal(err)
	}
	start := codexwire.Message{
		Kind: codexwire.KindNotification, Method: "item/started",
		Params: []byte(`{
			"threadId":"thread-runtime-1","turnId":"turn-runtime-1","startedAtMs":1,
			"item":{"type":"dynamicToolCall","id":"call-runtime-1","namespace":"executor","tool":"read_file","arguments":{"environment_id":"91000000-0000-4000-8000-000000000091","path":"README.md"},"status":"inProgress","contentItems":null,"success":null}
		}`),
	}
	if err := client.SendAppServerNotification(ctx, start); err != nil {
		t.Fatal(err)
	}
	if err := client.SendExecutorMCPProgress(ctx, harnessworker.ProgressEvent{
		RunID: fixture.prepared.Manifest.RunID, CallID: callID,
		RunAttemptGeneration: fixture.prepared.Manifest.RunAttemptGeneration,
		Progress:             1, Total: 2, Message: "reading",
	}); err != nil {
		t.Fatal(err)
	}
	completed := codexwire.Message{
		Kind: codexwire.KindNotification, Method: "item/completed",
		Params: []byte(`{
			"threadId":"thread-runtime-1","turnId":"turn-runtime-1","completedAtMs":2,
			"item":{"type":"dynamicToolCall","id":"call-runtime-1","namespace":"executor","tool":"read_file","arguments":{"environment_id":"91000000-0000-4000-8000-000000000091","path":"README.md"},"status":"completed","contentItems":[{"type":"inputText","text":"file contents"}],"success":true}
		}`),
	}
	if err := client.SendAppServerNotification(ctx, completed); err != nil {
		t.Fatal(err)
	}
	if err := client.SendAppServerNotification(ctx, codexwire.Message{
		Kind: codexwire.KindNotification, Method: "turn/completed",
		Params: []byte(`{"threadId":"thread-runtime-1","turn":{"id":"turn-runtime-1","status":"completed"}}`),
	}); err != nil {
		t.Fatal(err)
	}
	terminal := harnesscontrol.TurnTerminalEvent{
		Kind: harnesscontrol.EventKindTurnTerminal, ThreadID: threadID, TurnID: turnID, Status: "completed",
		RolloutLocator: testCompletedRolloutLocator,
	}
	if err := client.SendTurnTerminal(ctx, terminal); err != nil {
		t.Fatal(err)
	}
	if err := client.Wait(ctx); err != nil {
		t.Fatal(err)
	}

	requests := core.appendSnapshot()
	var kinds []string
	for _, request := range requests {
		for _, event := range request.Events {
			kinds = append(kinds, event.Kind)
		}
	}
	want := []string{
		runevent.KindToolCallStarted, runevent.KindToolCallArguments,
		runevent.KindToolCallProgress,
		runevent.KindToolCallCompleted, runevent.KindToolCallResult,
	}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatalf("worker→pool→core canonical event kinds = %v, want %v", kinds, want)
	}
	if snapshot, found := fixture.control.Snapshot(); !found || snapshot.ReceivedThrough != 7 {
		t.Fatalf("terminal cumulative receive cursor = found %v snapshot %+v", found, snapshot)
	}
}

func TestWorkerControlClientAcceptsInterruptedTerminalAfterClosingUnfinishedToolProjection(t *testing.T) {
	fixture := newWorkerControlIntegrationFixture(t, &recordingAttemptLifecycle{})
	core := newRuntimeAppendCore(fixture.prepared)
	authority := &attemptLifecycleAuthority{
		ctx: t.Context(), scheduler: &poolTestScheduler{}, core: core,
		identities: &runtimeSequenceIdentityAllocator{}, prepared: fixture.prepared,
	}
	fixture.control.runtime.lifecycle = authority
	client := fixture.newClient(t, fixture.control.Capability(), fixture.prepared.Manifest, fixture.prepared.SignedManifest)
	startWorkerControlClient(t, client)

	ctx := testContext(t)
	threadID, turnID, callID := "thread-runtime-1", "turn-runtime-1", "call-runtime-interrupted"
	if err := client.SendThreadReady(ctx, threadID, false); err != nil {
		t.Fatal(err)
	}
	if err := client.SendTurnAccepted(ctx, threadID, turnID); err != nil {
		t.Fatal(err)
	}
	if err := client.SendAppServerNotification(ctx, codexwire.Message{
		Kind: codexwire.KindNotification, Method: "item/started",
		Params: []byte(`{
			"threadId":"thread-runtime-1","turnId":"turn-runtime-1","startedAtMs":1,
			"item":{"type":"dynamicToolCall","id":"call-runtime-interrupted","namespace":"executor","tool":"read_file","arguments":{"environment_id":"91000000-0000-4000-8000-000000000091","path":"README.md"},"status":"inProgress","contentItems":null,"success":null}
		}`),
	}); err != nil {
		t.Fatal(err)
	}
	if err := client.SendAppServerNotification(ctx, codexwire.Message{
		Kind: codexwire.KindNotification, Method: "turn/completed",
		Params: []byte(`{"threadId":"thread-runtime-1","turn":{"id":"turn-runtime-1","status":"interrupted"}}`),
	}); err != nil {
		t.Fatal(err)
	}
	terminal := harnesscontrol.TurnTerminalEvent{
		Kind: harnesscontrol.EventKindTurnTerminal, ThreadID: threadID, TurnID: turnID,
		Status: "interrupted", ErrorCode: "turn_interrupted",
		ErrorMessage: "stock app-server confirmed that the turn was interrupted",
	}
	if err := client.SendTurnTerminal(ctx, terminal); err != nil {
		t.Fatal(err)
	}
	accepted, err := fixture.control.WaitTerminal(ctx)
	if err != nil || accepted != terminal {
		t.Fatalf("WaitTerminal() = %+v, %v", accepted, err)
	}
	if err := client.Wait(ctx); err != nil {
		t.Fatal(err)
	}

	requests := core.appendSnapshot()
	var events []AttemptEvent
	for _, request := range requests {
		events = append(events, request.Events...)
	}
	var kinds []string
	for _, event := range events {
		kinds = append(kinds, event.Kind)
	}
	want := []string{
		runevent.KindToolCallStarted, runevent.KindToolCallArguments,
		runevent.KindToolCallCompleted, runevent.KindToolCallResult,
	}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatalf("interrupted canonical event kinds = %v, want %v", kinds, want)
	}
	var result runevent.ToolCallResultPayload
	if err := json.Unmarshal(events[len(events)-1].Payload, &result); err != nil {
		t.Fatal(err)
	}
	if result.ToolCallID != callID || result.Presentation != nil || !strings.Contains(result.Content, "stock turn was interrupted") {
		t.Fatalf("interrupted canonical tool result = %+v", result)
	}
}

func TestWorkerControlClientRoutesCanonicalApprovalOutcome(t *testing.T) {
	lifecycle := newBlockingControlApprovalLifecycle()
	fixture := newWorkerControlIntegrationFixture(t, lifecycle)
	client := fixture.newClient(t, fixture.control.Capability(), fixture.prepared.Manifest, fixture.prepared.SignedManifest)
	startWorkerControlClient(t, client)

	ctx := testContext(t)
	if err := client.SendThreadReady(ctx, "thread-worker-approval", false); err != nil {
		t.Fatal(err)
	}
	if err := client.SendTurnAccepted(ctx, "thread-worker-approval", "turn-worker-approval"); err != nil {
		t.Fatal(err)
	}
	request := workerApprovalRequest(fixture, "a4", "call-worker-approval")
	result := make(chan struct {
		decision harnessworker.ElicitationDecision
		err      error
	}, 1)
	go func() {
		decision, err := client.AwaitApproval(ctx, request)
		result <- struct {
			decision harnessworker.ElicitationDecision
			err      error
		}{decision: decision, err: err}
	}()

	select {
	case observed := <-lifecycle.requests:
		if observed.ApprovalID != request.ApprovalID || observed.CallID != request.CallID ||
			observed.ExecutionID != request.ExecutionID || observed.ContextHash != request.ContextHash {
			t.Fatalf("worker approval projection = %+v, want request %+v", observed, request)
		}
	case <-ctx.Done():
		t.Fatal("worker approval did not reach pool observation")
	}
	select {
	case completed := <-result:
		t.Fatalf("worker approval returned before canonical decision: %+v", completed)
	case <-time.After(20 * time.Millisecond):
	}
	lifecycle.releaseAll()

	select {
	case completed := <-result:
		if completed.err != nil || completed.decision.Action != harnessworker.ApprovalAccept {
			t.Fatalf("worker approval decision = %+v, %v", completed.decision, completed.err)
		}
		want := map[string]any{
			"approvalId": request.ApprovalID, "executionId": request.ExecutionID,
			"runId": request.RunID, "runAttemptId": fixture.prepared.Manifest.RunAttemptID,
			"runAttemptGeneration": request.RunAttemptGeneration,
			"nonce":                request.Nonce, "contextHash": request.ContextHash,
			"status": "approved", "approvalVersion": int64(2),
		}
		if !reflect.DeepEqual(completed.decision.Content, want) {
			t.Fatalf("canonical MCP approval evidence = %#v, want %#v", completed.decision.Content, want)
		}
	case <-ctx.Done():
		t.Fatal("worker approval did not return canonical outcome")
	}
	waitForControlJournalFrames(t, fixture.control, 0)

	terminal := harnesscontrol.TurnTerminalEvent{
		Kind: harnesscontrol.EventKindTurnTerminal, ThreadID: "thread-worker-approval",
		TurnID: "turn-worker-approval", Status: "completed", RolloutLocator: testCompletedRolloutLocator,
	}
	if err := client.SendTurnTerminal(ctx, terminal); err != nil {
		t.Fatal(err)
	}
	if err := client.Wait(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerControlClientResumesJournaledApprovalOutcomeAfterPendingDisconnect(t *testing.T) {
	lifecycle := newBlockingControlApprovalLifecycle()
	fixture := newWorkerControlIntegrationFixture(t, lifecycle)
	reconnectGate := newSecondControlHandshakeGate(t, fixture.httpClient)
	config := harnessworker.DefaultWorkerControlClientConfig(
		fixture.prepared.Manifest, fixture.prepared.SignedManifest,
		fixture.control.Capability(), testHarnessWorkerID, fixture.httpClient,
	)
	config.HandshakeTimeout = 2 * time.Second
	config.WriteTimeout = 2 * time.Second
	config.ReconnectBackoff = 200 * time.Millisecond
	client, err := harnessworker.NewWorkerControlClient(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close(errors.New("pending approval resume test complete")) })
	t.Cleanup(reconnectGate.release)
	startWorkerControlClient(t, client)

	ctx := testContext(t)
	const threadID = "thread-worker-approval-resume"
	const turnID = "turn-worker-approval-resume"
	if err := client.SendThreadReady(ctx, threadID, false); err != nil {
		t.Fatal(err)
	}
	if err := client.SendTurnAccepted(ctx, threadID, turnID); err != nil {
		t.Fatal(err)
	}
	request := workerApprovalRequest(fixture, "d4", "call-worker-approval-resume")
	type approvalResult struct {
		decision harnessworker.ElicitationDecision
		err      error
	}
	result := make(chan approvalResult, 1)
	go func() {
		decision, err := client.AwaitApproval(ctx, request)
		result <- approvalResult{decision: decision, err: err}
	}()
	select {
	case observed := <-lifecycle.requests:
		if observed.ApprovalID != request.ApprovalID || observed.CallID != request.CallID {
			t.Fatalf("observed approval = %+v, want %+v", observed, request)
		}
	case <-ctx.Done():
		t.Fatal("pending approval did not reach pool observation")
	}

	closeActiveControlConnection(t, fixture.control)
	select {
	case <-reconnectGate.reconnectStarted:
	case <-ctx.Done():
		t.Fatal("worker did not start the gated resume handshake")
	}
	waitForControlState(t, fixture.control, harnesscontrol.SessionDisconnected)
	lifecycle.releaseAll()
	waitForControlJournalFrames(t, fixture.control, 1)
	select {
	case completed := <-result:
		t.Fatalf("approval outcome crossed a disconnected transport: %+v", completed)
	case <-time.After(20 * time.Millisecond):
	}

	reconnectGate.release()
	select {
	case completed := <-result:
		if completed.err != nil || completed.decision.Action != harnessworker.ApprovalAccept {
			t.Fatalf("resumed approval decision = %+v, %v", completed.decision, completed.err)
		}
		if completed.decision.Content["approvalId"] != request.ApprovalID ||
			completed.decision.Content["approvalVersion"] != int64(2) {
			t.Fatalf("resumed canonical approval evidence = %#v", completed.decision.Content)
		}
	case <-ctx.Done():
		t.Fatal("worker did not receive journaled approval outcome after resume")
	}
	waitForControlJournalFrames(t, fixture.control, 0)

	terminal := harnesscontrol.TurnTerminalEvent{
		Kind: harnesscontrol.EventKindTurnTerminal, ThreadID: threadID, TurnID: turnID,
		Status: "completed", RolloutLocator: testCompletedRolloutLocator,
	}
	if err := client.SendTurnTerminal(ctx, terminal); err != nil {
		t.Fatal(err)
	}
	if accepted, err := fixture.control.WaitTerminal(ctx); err != nil || accepted != terminal {
		t.Fatalf("WaitTerminal() = %+v, %v", accepted, err)
	}
	if err := client.Wait(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerControlClientBoundsPendingApprovals(t *testing.T) {
	lifecycle := newBlockingControlApprovalLifecycle()
	fixture := newWorkerControlIntegrationFixture(t, lifecycle)
	config := harnessworker.DefaultWorkerControlClientConfig(
		fixture.prepared.Manifest, fixture.prepared.SignedManifest,
		fixture.control.Capability(), testHarnessWorkerID, fixture.httpClient,
	)
	config.MaxPendingApprovals = 1
	config.HandshakeTimeout = 2 * time.Second
	config.WriteTimeout = 2 * time.Second
	config.ReconnectBackoff = 5 * time.Millisecond
	client, err := harnessworker.NewWorkerControlClient(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close(errors.New("pending approval test complete")) })
	startWorkerControlClient(t, client)

	ctx := testContext(t)
	if err := client.SendThreadReady(ctx, "thread-worker-approval-limit", false); err != nil {
		t.Fatal(err)
	}
	if err := client.SendTurnAccepted(ctx, "thread-worker-approval-limit", "turn-worker-approval-limit"); err != nil {
		t.Fatal(err)
	}
	firstDone := make(chan struct{})
	go func() {
		_, _ = client.AwaitApproval(ctx, workerApprovalRequest(fixture, "b4", "call-worker-limit-1"))
		close(firstDone)
	}()
	select {
	case <-lifecycle.requests:
	case <-ctx.Done():
		t.Fatal("first pending approval did not reach pool")
	}
	_, err = client.AwaitApproval(ctx, workerApprovalRequest(fixture, "c4", "call-worker-limit-2"))
	if err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("second pending approval error = %v", err)
	}
	lifecycle.releaseAll()
	select {
	case <-firstDone:
	case <-ctx.Done():
		t.Fatal("first pending approval did not finish")
	}
}

func TestWorkerControlClientRejectsWrongSPIFFEAndBearer(t *testing.T) {
	t.Run("wrong server SPIFFE URI", func(t *testing.T) {
		fixture := newWorkerControlIntegrationFixture(t, &recordingAttemptLifecycle{})
		manifest := fixture.prepared.Manifest
		manifest.ControllerCallback.TLSIdentity = "spiffe://agentserver.local/ns/agentserver/sa/not-the-pool"
		signed := signWorkerControlManifest(t, manifest)
		client := fixture.newClient(t, fixture.control.Capability(), manifest, signed)
		err := client.Start(testContext(t))
		if err == nil || !strings.Contains(err.Error(), "wrong SPIFFE URI SAN") {
			t.Fatalf("Start() error = %v, want SPIFFE rejection", err)
		}
	})

	t.Run("wrong bearer", func(t *testing.T) {
		fixture := newWorkerControlIntegrationFixture(t, &recordingAttemptLifecycle{})
		client := fixture.newClient(
			t, fixedControlCapability(8), fixture.prepared.Manifest, fixture.prepared.SignedManifest,
		)
		err := client.Start(testContext(t))
		if err == nil || !strings.Contains(err.Error(), "HTTP 401") {
			t.Fatalf("Start() error = %v, want HTTP 401", err)
		}
	})

	t.Run("redirect without bearer forwarding", func(t *testing.T) {
		authorization := make(chan string, 1)
		redirectServer := httptest.NewUnstartedServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			authorization <- request.Header.Get("Authorization")
			http.Redirect(response, request, "https://redirect-target.invalid/control", http.StatusTemporaryRedirect)
		}))
		prepared := poolTestPreparedLaunch(t)
		prepared.Manifest.ControllerCallback.Endpoint = "https://" + redirectServer.Listener.Addr().String() + HarnessControlPath
		prepared.SignedManifest = signWorkerControlManifest(t, prepared.Manifest)
		serverCertificate, serverLeaf := newSelfSignedControlServerCertificate(
			t, prepared.Manifest.ControllerCallback.TLSIdentity, redirectServer.Listener.Addr(),
		)
		redirectServer.TLS = &tls.Config{
			MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{serverCertificate},
		}
		redirectServer.StartTLS()
		t.Cleanup(redirectServer.Close)

		serverRoots := x509.NewCertPool()
		serverRoots.AddCert(serverLeaf)
		httpClient := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS13, RootCAs: serverRoots,
		}}}
		t.Cleanup(httpClient.CloseIdleConnections)
		capability := fixedControlCapability(7)
		config := harnessworker.DefaultWorkerControlClientConfig(
			prepared.Manifest, prepared.SignedManifest, capability, testHarnessWorkerID, httpClient,
		)
		config.HandshakeTimeout = 2 * time.Second
		client, err := harnessworker.NewWorkerControlClient(config)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { client.Close(errors.New("redirect test complete")) })
		err = client.Start(testContext(t))
		if err == nil || !strings.Contains(err.Error(), "redirects are forbidden") {
			t.Fatalf("Start() error = %v, want redirect rejection", err)
		}
		if strings.Contains(err.Error(), capability) {
			t.Fatal("redirect error exposed the control capability")
		}
		select {
		case header := <-authorization:
			if header != "Bearer "+capability {
				t.Fatalf("Authorization = %q", header)
			}
		case <-time.After(time.Second):
			t.Fatal("redirect origin did not receive the WebSocket request")
		}
	})
}

type workerControlIntegrationFixture struct {
	prepared   PreparedRunLaunch
	control    *AttemptControl
	http       *httptest.Server
	httpClient *http.Client
}

type secondControlHandshakeGate struct {
	dial             func(context.Context, string, string) (net.Conn, error)
	reconnectStarted chan struct{}
	releaseReconnect chan struct{}

	mu          sync.Mutex
	dialCount   int
	startedOnce sync.Once
	releaseOnce sync.Once
}

func newSecondControlHandshakeGate(t *testing.T, client *http.Client) *secondControlHandshakeGate {
	t.Helper()
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport == nil {
		t.Fatal("worker control test client must use an explicit *http.Transport")
	}
	dial := transport.DialContext
	if dial == nil {
		dial = (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	}
	gate := &secondControlHandshakeGate{
		dial: dial, reconnectStarted: make(chan struct{}), releaseReconnect: make(chan struct{}),
	}
	transport.DialContext = gate.dialContext
	return gate
}

func (gate *secondControlHandshakeGate) dialContext(
	ctx context.Context,
	network string,
	address string,
) (net.Conn, error) {
	gate.mu.Lock()
	gate.dialCount++
	dialCount := gate.dialCount
	gate.mu.Unlock()
	if dialCount > 1 {
		gate.startedOnce.Do(func() { close(gate.reconnectStarted) })
		select {
		case <-gate.releaseReconnect:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return gate.dial(ctx, network, address)
}

func (gate *secondControlHandshakeGate) release() {
	gate.releaseOnce.Do(func() { close(gate.releaseReconnect) })
}

func newWorkerControlIntegrationFixture(t *testing.T, lifecycle AttemptLifecycle) *workerControlIntegrationFixture {
	t.Helper()
	testServer := httptest.NewUnstartedServer(nil)
	prepared := poolTestPreparedLaunch(t)
	prepared.Manifest.ControllerCallback.Endpoint = "https://" + testServer.Listener.Addr().String() + HarnessControlPath
	prepared.SignedManifest = signWorkerControlManifest(t, prepared.Manifest)

	config := testControlServerConfig(prepared)
	server, err := NewControlServer(config)
	if err != nil {
		t.Fatal(err)
	}
	control, err := server.RegisterAttempt(prepared, lifecycle)
	if err != nil {
		t.Fatal(err)
	}

	serverCertificate, serverLeaf := newSelfSignedControlServerCertificate(
		t, prepared.Manifest.ControllerCallback.TLSIdentity, testServer.Listener.Addr(),
	)
	workerCertificate, workerLeaf := newSelfSignedWorkerCertificate(t, testWorkerTLSIdentity)
	clientCAs := x509.NewCertPool()
	clientCAs.AddCert(workerLeaf)
	testServer.Config.Handler = server
	testServer.TLS = &tls.Config{
		MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{serverCertificate},
		ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: clientCAs,
	}
	testServer.StartTLS()

	serverRoots := x509.NewCertPool()
	serverRoots.AddCert(serverLeaf)
	httpClient := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		MinVersion: tls.VersionTLS13, RootCAs: serverRoots,
		Certificates: []tls.Certificate{workerCertificate},
	}}}
	fixture := &workerControlIntegrationFixture{
		prepared: prepared, control: control, http: testServer, httpClient: httpClient,
	}
	t.Cleanup(func() {
		control.Close(errors.New("worker control integration test complete"))
		testServer.Close()
		httpClient.CloseIdleConnections()
	})
	return fixture
}

func (fixture *workerControlIntegrationFixture) newClient(
	t *testing.T,
	capability string,
	manifest runmanifest.Manifest,
	signed runmanifest.SignedManifest,
) *harnessworker.WorkerControlClient {
	t.Helper()
	config := harnessworker.DefaultWorkerControlClientConfig(
		manifest, signed, capability, testHarnessWorkerID, fixture.httpClient,
	)
	config.HandshakeTimeout = 2 * time.Second
	config.WriteTimeout = 2 * time.Second
	config.ReconnectBackoff = 5 * time.Millisecond
	client, err := harnessworker.NewWorkerControlClient(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close(errors.New("worker control integration test complete")) })
	return client
}

func startWorkerControlClient(t *testing.T, client *harnessworker.WorkerControlClient) {
	t.Helper()
	if err := client.Start(testContext(t)); err != nil {
		t.Fatal(err)
	}
}

func workerApprovalRequest(
	fixture *workerControlIntegrationFixture,
	prefix string,
	callID string,
) harnessworker.ElicitationRequest {
	manifest := fixture.prepared.Manifest
	return harnessworker.ElicitationRequest{
		RunID: manifest.RunID, CallID: callID,
		RunAttemptGeneration: manifest.RunAttemptGeneration,
		ToolCatalogDigest:    manifest.ExecutorMCP.CatalogDigest,
		ExecutionID:          prefix + "000000-0000-4000-8000-000000000041",
		ApprovalID:           prefix + "000000-0000-4000-8000-000000000042",
		Nonce:                prefix + "000000-0000-4000-8000-000000000043",
		ApprovalVersion:      1, ContextHash: strings.Repeat("a", 64),
		ExpiresAt: time.Now().Add(10 * time.Second).UTC(),
		Message:   "approve deterministic tool operation", RequestedSchema: []byte(`{"type":"object"}`),
	}
}

func signWorkerControlManifest(t *testing.T, manifest runmanifest.Manifest) runmanifest.SignedManifest {
	t.Helper()
	seed := sha256.Sum256([]byte("worker-control-integration-signing-key"))
	signed, err := runmanifest.Sign(manifest, "worker-control-test-key", ed25519.NewKeyFromSeed(seed[:]))
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func newSelfSignedControlServerCertificate(t *testing.T, identity string, address net.Addr) (tls.Certificate, *x509.Certificate) {
	t.Helper()
	parsedIdentity, err := url.Parse(identity)
	if err != nil {
		t.Fatal(err)
	}
	host, _, err := net.SplitHostPort(address.String())
	if err != nil {
		t.Fatal(err)
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "harness-pool-test"},
		NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true, IsCA: true, URIs: []*url.URL{parsedIdentity},
	}
	if ip := net.ParseIP(host); ip != nil {
		template.IPAddresses = []net.IP{ip}
	} else {
		template.DNSNames = []string{host}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, publicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: privateKey, Leaf: leaf}, leaf
}

func closeActiveControlConnection(t *testing.T, control *AttemptControl) {
	t.Helper()
	control.runtime.mu.Lock()
	connection := control.runtime.connection
	control.runtime.mu.Unlock()
	if connection == nil {
		t.Fatal("attempt has no active control connection")
	}
	connection.closeNow()
}

type resumeBlockingAttemptLifecycle struct {
	started     chan struct{}
	release     chan struct{}
	startedOnce sync.Once
	releaseOnce sync.Once
	mu          sync.Mutex
	threadCalls int
}

func (lifecycle *resumeBlockingAttemptLifecycle) ThreadStarted(string) error {
	lifecycle.mu.Lock()
	lifecycle.threadCalls++
	lifecycle.mu.Unlock()
	lifecycle.startedOnce.Do(func() { close(lifecycle.started) })
	<-lifecycle.release
	return nil
}

func (*resumeBlockingAttemptLifecycle) TurnAccepted(string, string) error { return nil }

func (lifecycle *resumeBlockingAttemptLifecycle) unblock() {
	lifecycle.releaseOnce.Do(func() { close(lifecycle.release) })
}

func (lifecycle *resumeBlockingAttemptLifecycle) threadCallCount() int {
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	return lifecycle.threadCalls
}
