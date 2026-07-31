package harnesspool

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
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
		TurnID: "turn-worker-control", Status: "completed",
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
		TurnID: "turn-resumed-control", Status: "completed",
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
