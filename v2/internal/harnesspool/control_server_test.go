package harnesspool

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/harnesscontrol"
	"nhooyr.io/websocket"
)

const (
	testControlPoolInstanceID = "81000000-0000-4000-8000-000000000081"
	testHarnessControlID      = "82000000-0000-4000-8000-000000000082"
	testHarnessWorkerID       = "83000000-0000-4000-8000-000000000083"
)

func TestControlServerMapsAuthenticatedWorkerLifecycleInOrder(t *testing.T) {
	lifecycle := &recordingAttemptLifecycle{}
	fixture := newControlServerFixture(t, lifecycle)
	connection := fixture.dial(t, fixture.control.Capability())
	hello := fixture.hello(nil)
	writeControlValue(t, connection, fixture.config.WireLimits, hello)
	welcome := readControlMessage(t, connection, fixture.config.WireLimits)
	if welcome.Welcome == nil || welcome.Welcome.ResumeStatus != "fresh" ||
		welcome.Welcome.PoolInstanceID != testControlPoolInstanceID ||
		welcome.Welcome.ControlSessionID != testHarnessControlID {
		t.Fatalf("fresh welcome = %+v", welcome)
	}
	if err := fixture.control.WaitConnected(testContext(t)); err != nil {
		t.Fatal(err)
	}
	worker := fixture.workerSession(t, hello, *welcome.Welcome)

	sendWorkerEvent(t, connection, worker, harnesscontrol.ThreadReadyEvent{
		Kind: harnesscontrol.EventKindThreadReady, ThreadID: "thread-control-1", Resumed: false,
	})
	sendWorkerEvent(t, connection, worker, harnesscontrol.TurnAcceptedEvent{
		Kind: harnesscontrol.EventKindTurnAccepted, ThreadID: "thread-control-1", TurnID: "turn-control-1",
	})
	sendWorkerEvent(t, connection, worker, harnesscontrol.TurnTerminalEvent{
		Kind: harnesscontrol.EventKindTurnTerminal, ThreadID: "thread-control-1", TurnID: "turn-control-1",
		Status: "completed",
	})
	terminal, err := fixture.control.WaitTerminal(testContext(t))
	if err != nil || terminal.Status != "completed" || terminal.ThreadID != "thread-control-1" {
		t.Fatalf("WaitTerminal() = %+v, %v", terminal, err)
	}
	threads, turns := lifecycle.snapshot()
	if !reflect.DeepEqual(threads, []string{"thread-control-1"}) ||
		!reflect.DeepEqual(turns, [][2]string{{"thread-control-1", "turn-control-1"}}) {
		t.Fatalf("lifecycle calls = threads %q turns %q", threads, turns)
	}
}

func TestControlServerResumesOnlyItsInMemoryJournalAndReplaysExactCommand(t *testing.T) {
	fixture := newControlServerFixture(t, &recordingAttemptLifecycle{})
	firstConnection := fixture.dial(t, fixture.control.Capability())
	hello := fixture.hello(nil)
	writeControlValue(t, firstConnection, fixture.config.WireLimits, hello)
	welcomeMessage := readControlMessage(t, firstConnection, fixture.config.WireLimits)
	if welcomeMessage.Welcome == nil || welcomeMessage.Welcome.ResumeStatus != "fresh" {
		t.Fatalf("fresh welcome = %+v", welcomeMessage)
	}
	worker := fixture.workerSession(t, hello, *welcomeMessage.Welcome)
	if err := fixture.control.WaitConnected(testContext(t)); err != nil {
		t.Fatal(err)
	}
	interrupt := harnesscontrol.InterruptCommand{
		Kind: harnesscontrol.CommandKindInterrupt, Reason: "lease_lost", GraceMillis: 1_000,
		Message: "attempt lease was fenced",
	}
	if err := fixture.control.SendInterrupt(testContext(t), interrupt); err != nil {
		t.Fatal(err)
	}
	original := readControlMessage(t, firstConnection, fixture.config.WireLimits)
	if original.Frame == nil || original.Frame.Type != harnesscontrol.MessageTypeCommand {
		t.Fatalf("interrupt frame = %+v", original)
	}
	// Simulate loss after the bytes reached the worker transport but before the
	// worker committed them to its receive cursor.
	firstConnection.CloseNow()
	waitForControlState(t, fixture.control, harnesscontrol.SessionDisconnected)

	workerSnapshot := worker.Snapshot()
	resumeHello := fixture.hello(&harnesscontrol.ResumeCursor{
		PoolInstanceID: testControlPoolInstanceID, ControlSessionID: testHarnessControlID,
		RunAttemptGeneration: fixture.prepared.Manifest.RunAttemptGeneration,
		WorkerSentThrough:    workerSnapshot.SentThrough, WorkerReceivedThrough: workerSnapshot.ReceivedThrough,
	})
	secondConnection := fixture.dial(t, fixture.control.Capability())
	writeControlValue(t, secondConnection, fixture.config.WireLimits, resumeHello)
	resumed := readControlMessage(t, secondConnection, fixture.config.WireLimits)
	if resumed.Welcome == nil || resumed.Welcome.ResumeStatus != "resumed" || resumed.Welcome.PoolSentThrough != 1 {
		t.Fatalf("resumed welcome = %+v", resumed)
	}
	replayed := readControlMessage(t, secondConnection, fixture.config.WireLimits)
	if replayed.Frame == nil || !reflect.DeepEqual(*replayed.Frame, *original.Frame) {
		t.Fatalf("replayed frame = %+v, want exact %+v", replayed.Frame, original.Frame)
	}
	received, err := worker.Receive(*replayed.Frame)
	if err != nil || !received.Deliver {
		t.Fatalf("worker Receive(replay) = %+v, %v", received, err)
	}
	ack, err := worker.AckFrame()
	if err != nil {
		t.Fatal(err)
	}
	writeControlValue(t, secondConnection, fixture.config.WireLimits, ack)
	waitForControlJournalFrames(t, fixture.control, 0)
	secondConnection.CloseNow()
}

func TestControlServerRejectsOutOfOrderLifecycleWithoutCallingAuthority(t *testing.T) {
	lifecycle := &recordingAttemptLifecycle{}
	fixture := newControlServerFixture(t, lifecycle)
	connection := fixture.dial(t, fixture.control.Capability())
	hello := fixture.hello(nil)
	writeControlValue(t, connection, fixture.config.WireLimits, hello)
	welcome := readControlMessage(t, connection, fixture.config.WireLimits)
	if welcome.Welcome == nil {
		t.Fatalf("welcome = %+v", welcome)
	}
	worker := fixture.workerSession(t, hello, *welcome.Welcome)
	frame, err := worker.Send(harnesscontrol.Payload{
		Type: harnesscontrol.MessageTypeEvent,
		Payload: mustControlPayload(t, harnesscontrol.TurnAcceptedEvent{
			Kind: harnesscontrol.EventKindTurnAccepted, ThreadID: "thread-control-1", TurnID: "turn-control-1",
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	writeControlValue(t, connection, fixture.config.WireLimits, frame)
	failure := readControlMessage(t, connection, fixture.config.WireLimits)
	if failure.SessionError == nil || failure.SessionError.Code != harnesscontrol.ErrorAttemptMismatch || !failure.SessionError.Terminal {
		t.Fatalf("out-of-order failure = %+v", failure)
	}
	if _, err := fixture.control.WaitTerminal(testContext(t)); err == nil {
		t.Fatal("out-of-order lifecycle did not fail attempt control")
	}
	threads, turns := lifecycle.snapshot()
	if len(threads) != 0 || len(turns) != 0 {
		t.Fatalf("out-of-order event reached lifecycle: threads=%q turns=%q", threads, turns)
	}
}

func TestControlServerDoesNotPiggybackAckBeforeLifecycleAuthorityCommits(t *testing.T) {
	lifecycle := &blockingAttemptLifecycle{started: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(lifecycle.unblock)
	fixture := newControlServerFixture(t, lifecycle)
	connection := fixture.dial(t, fixture.control.Capability())
	hello := fixture.hello(nil)
	writeControlValue(t, connection, fixture.config.WireLimits, hello)
	welcome := readControlMessage(t, connection, fixture.config.WireLimits)
	if welcome.Welcome == nil {
		t.Fatalf("welcome = %+v", welcome)
	}
	worker := fixture.workerSession(t, hello, *welcome.Welcome)
	frame, err := worker.Send(harnesscontrol.Payload{
		Type: harnesscontrol.MessageTypeEvent,
		Payload: mustControlPayload(t, harnesscontrol.ThreadReadyEvent{
			Kind: harnesscontrol.EventKindThreadReady, ThreadID: "thread-blocked", Resumed: false,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	writeControlValue(t, connection, fixture.config.WireLimits, frame)
	select {
	case <-lifecycle.started:
	case <-time.After(time.Second):
		t.Fatal("thread lifecycle authority was not called")
	}
	interruptResult := make(chan error, 1)
	go func() {
		interruptResult <- fixture.control.SendInterrupt(context.Background(), harnesscontrol.InterruptCommand{
			Kind: harnesscontrol.CommandKindInterrupt, Reason: "cancelled", GraceMillis: 1_000,
			Message: "cancel after bind",
		})
	}()
	select {
	case err := <-interruptResult:
		t.Fatalf("interrupt bypassed pending lifecycle authority: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	lifecycle.unblock()
	ack := readControlMessage(t, connection, fixture.config.WireLimits)
	if ack.Ack == nil || ack.Ack.Ack != 1 {
		t.Fatalf("post-authority ACK = %+v", ack)
	}
	if err := worker.ReceiveAck(*ack.Ack); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-interruptResult:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("interrupt did not proceed after lifecycle authority committed")
	}
	command := readControlMessage(t, connection, fixture.config.WireLimits)
	if command.Frame == nil || command.Frame.Type != harnesscontrol.MessageTypeCommand || command.Frame.Ack != 1 {
		t.Fatalf("post-authority interrupt = %+v", command)
	}
}

func TestControlServerDoesNotAckRuntimeFrameBeforeCoreAppendCommits(t *testing.T) {
	fixture := newControlServerFixture(t, &recordingAttemptLifecycle{})
	core := newRuntimeAppendCore(fixture.prepared)
	core.appendStarted = make(chan struct{})
	core.appendRelease = make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(core.appendRelease) }) }
	t.Cleanup(release)
	authority := &attemptLifecycleAuthority{
		ctx: t.Context(), scheduler: &poolTestScheduler{}, core: core,
		identities: &runtimeSequenceIdentityAllocator{}, prepared: fixture.prepared,
	}
	fixture.control.runtime.lifecycle = authority

	connection := fixture.dial(t, fixture.control.Capability())
	hello := fixture.hello(nil)
	writeControlValue(t, connection, fixture.config.WireLimits, hello)
	welcome := readControlMessage(t, connection, fixture.config.WireLimits)
	if welcome.Welcome == nil {
		t.Fatalf("welcome = %+v", welcome)
	}
	worker := fixture.workerSession(t, hello, *welcome.Welcome)
	sendWorkerEvent(t, connection, worker, harnesscontrol.ThreadReadyEvent{
		Kind: harnesscontrol.EventKindThreadReady, ThreadID: "thread-runtime-1", Resumed: false,
	})
	sendWorkerEvent(t, connection, worker, harnesscontrol.TurnAcceptedEvent{
		Kind: harnesscontrol.EventKindTurnAccepted, ThreadID: "thread-runtime-1", TurnID: "turn-runtime-1",
	})

	params := mustControlPayload(t, map[string]any{
		"threadId": "thread-runtime-1", "turnId": "turn-runtime-1",
		"item": map[string]any{"type": "agentMessage", "id": "message-1", "text": ""},
	})
	frame, err := worker.Send(harnesscontrol.Payload{
		Type: harnesscontrol.MessageTypeEvent,
		Payload: mustControlPayload(t, harnesscontrol.AppServerNotificationEvent{
			Kind: harnesscontrol.EventKindAppServerNotification, Method: "item/started", Params: params,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	writeControlValue(t, connection, fixture.config.WireLimits, frame)
	select {
	case <-core.appendStarted:
	case <-time.After(time.Second):
		t.Fatal("runtime event did not reach the core append boundary")
	}
	if snapshot, found := fixture.control.Snapshot(); !found || snapshot.ReceivedThrough != 2 {
		t.Fatalf("pending core append advanced control receive cursor: found=%v snapshot=%+v", found, snapshot)
	}

	interruptResult := make(chan error, 1)
	go func() {
		interruptResult <- fixture.control.SendInterrupt(context.Background(), harnesscontrol.InterruptCommand{
			Kind: harnesscontrol.CommandKindInterrupt, Reason: "cancelled", GraceMillis: 1_000,
			Message: "cancel after runtime append",
		})
	}()
	select {
	case err := <-interruptResult:
		t.Fatalf("interrupt bypassed pending core append: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	release()
	ack := readControlMessage(t, connection, fixture.config.WireLimits)
	if ack.Ack == nil || ack.Ack.Ack != frame.SessionSeq {
		t.Fatalf("post-core runtime ACK = %+v", ack)
	}
	if err := worker.ReceiveAck(*ack.Ack); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-interruptResult:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("interrupt did not proceed after runtime append committed")
	}
	command := readControlMessage(t, connection, fixture.config.WireLimits)
	if command.Frame == nil || command.Frame.Ack != frame.SessionSeq {
		t.Fatalf("post-runtime command = %+v", command)
	}
}

func TestControlServerRequiresAttemptProfileAndOneRegistration(t *testing.T) {
	prepared := poolTestPreparedLaunch(t)
	config := testControlServerConfig(prepared)
	server, err := NewControlServer(config)
	if err != nil {
		t.Fatal(err)
	}
	control, err := server.RegisterAttempt(prepared, &recordingAttemptLifecycle{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { control.Close(errors.New("test complete")) })
	if _, err := server.RegisterAttempt(prepared, &recordingAttemptLifecycle{}); err == nil || !strings.Contains(err.Error(), "already") {
		t.Fatalf("duplicate registration error = %v", err)
	}

	wrongConfig := config
	wrongConfig.HolderID = "different-holder"
	wrongServer, err := NewControlServer(wrongConfig)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wrongServer.RegisterAttempt(prepared, &recordingAttemptLifecycle{}); err == nil || !strings.Contains(err.Error(), "deployment profile") {
		t.Fatalf("wrong-holder registration error = %v", err)
	}
}

type controlServerFixture struct {
	prepared   PreparedRunLaunch
	config     ControlServerConfig
	server     *ControlServer
	control    *AttemptControl
	http       *httptest.Server
	httpClient *http.Client
}

func newControlServerFixture(t *testing.T, lifecycle AttemptLifecycle) *controlServerFixture {
	t.Helper()
	prepared := poolTestPreparedLaunch(t)
	config := testControlServerConfig(prepared)
	server, err := NewControlServer(config)
	if err != nil {
		t.Fatal(err)
	}
	control, err := server.RegisterAttempt(prepared, lifecycle)
	if err != nil {
		t.Fatal(err)
	}
	testServer := httptest.NewUnstartedServer(server)
	workerCertificate, workerLeaf := newSelfSignedWorkerCertificate(t, testWorkerTLSIdentity)
	clientCAs := x509.NewCertPool()
	clientCAs.AddCert(workerLeaf)
	testServer.TLS = &tls.Config{
		MinVersion: tls.VersionTLS13, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: clientCAs,
	}
	testServer.StartTLS()
	httpClient := testServer.Client()
	transport, ok := httpClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("httptest transport = %T", httpClient.Transport)
	}
	transport = transport.Clone()
	transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	transport.TLSClientConfig.Certificates = []tls.Certificate{workerCertificate}
	httpClient.Transport = transport
	fixture := &controlServerFixture{
		prepared: prepared, config: config, server: server, control: control,
		http: testServer, httpClient: httpClient,
	}
	t.Cleanup(func() {
		control.Close(errors.New("test complete"))
		testServer.Close()
	})
	return fixture
}

func testControlServerConfig(prepared PreparedRunLaunch) ControlServerConfig {
	manifest := prepared.Manifest
	config := DefaultControlServerConfig(
		testControlPoolInstanceID, manifest.HolderID,
		manifest.ControllerCallback.Endpoint, manifest.ControllerCallback.TLSIdentity,
		manifest.ControllerCallback.Audience, manifest.ExpectedServiceAccount, testWorkerTLSIdentity,
	)
	config.IDGenerator = func() (string, error) { return testHarnessControlID, nil }
	capability := fixedControlCapability(7)
	config.CapabilityGenerator = func() (string, error) { return capability, nil }
	config.HandshakeTimeout = 2 * time.Second
	config.WriteTimeout = 2 * time.Second
	return config
}

func (fixture *controlServerFixture) dial(t *testing.T, capability string) *websocket.Conn {
	t.Helper()
	endpoint := "wss" + strings.TrimPrefix(fixture.http.URL, "https")
	parsedCallback, err := url.Parse(fixture.config.CallbackEndpoint)
	if err != nil {
		t.Fatal(err)
	}
	header := make(http.Header)
	header.Set("Authorization", "Bearer "+capability)
	ctx := testContext(t)
	connection, response, err := websocket.Dial(ctx, endpoint+parsedCallback.Path, &websocket.DialOptions{
		HTTPClient: fixture.httpClient, HTTPHeader: header,
	})
	if err != nil {
		if response != nil {
			t.Fatalf("dial control WebSocket: %v (HTTP %s)", err, response.Status)
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.CloseNow() })
	return connection
}

func (fixture *controlServerFixture) hello(resume *harnesscontrol.ResumeCursor) harnesscontrol.Hello {
	manifest := fixture.prepared.Manifest
	return harnesscontrol.Hello{
		Type: harnesscontrol.MessageTypeHello, ProtocolVersions: []string{harnesscontrol.CurrentProtocolVersion},
		WorkerInstanceID: testHarnessWorkerID, WorkspaceID: manifest.WorkspaceID, SessionID: manifest.SessionID,
		RunID: manifest.RunID, RunAttemptID: manifest.RunAttemptID,
		RunAttemptGeneration: manifest.RunAttemptGeneration, HolderID: manifest.HolderID,
		ManifestDigest: fixture.control.runtime.expected.ManifestDigest, Resume: resume,
	}
}

func (fixture *controlServerFixture) workerSession(t *testing.T, hello harnesscontrol.Hello, welcome harnesscontrol.Welcome) *harnesscontrol.Session {
	t.Helper()
	binding, err := harnesscontrol.BindingFromHello(hello)
	if err != nil {
		t.Fatal(err)
	}
	session, err := harnesscontrol.NewSession(harnesscontrol.SessionConfig{
		Role: harnesscontrol.RoleWorker, PoolInstanceID: welcome.PoolInstanceID,
		ControlSessionID: welcome.ControlSessionID, Attempt: binding,
		WireLimits: fixture.config.WireLimits, MaxUnackedFrames: fixture.config.MaxUnackedFrames,
		MaxJournalBytes:         int(fixture.prepared.Manifest.Limits.MaxControlBufferBytes),
		MaxReceiveHistoryFrames: fixture.config.MaxReceiveHistoryFrames,
		ResumeWindow:            time.Duration(harnesscontrol.ResumeWindowMillis) * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	return session
}

type recordingAttemptLifecycle struct {
	mu      sync.Mutex
	threads []string
	turns   [][2]string
	err     error
}

type blockingAttemptLifecycle struct {
	started     chan struct{}
	release     chan struct{}
	startOnce   sync.Once
	releaseOnce sync.Once
}

func (lifecycle *blockingAttemptLifecycle) ThreadStarted(string) error {
	lifecycle.startOnce.Do(func() { close(lifecycle.started) })
	<-lifecycle.release
	return nil
}

func (*blockingAttemptLifecycle) TurnAccepted(string, string) error { return nil }

func (lifecycle *blockingAttemptLifecycle) unblock() {
	lifecycle.releaseOnce.Do(func() { close(lifecycle.release) })
}

func (lifecycle *recordingAttemptLifecycle) ThreadStarted(threadID string) error {
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	if lifecycle.err != nil {
		return lifecycle.err
	}
	lifecycle.threads = append(lifecycle.threads, threadID)
	return nil
}

func (lifecycle *recordingAttemptLifecycle) TurnAccepted(threadID, turnID string) error {
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	if lifecycle.err != nil {
		return lifecycle.err
	}
	lifecycle.turns = append(lifecycle.turns, [2]string{threadID, turnID})
	return nil
}

func (lifecycle *recordingAttemptLifecycle) snapshot() ([]string, [][2]string) {
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	return append([]string(nil), lifecycle.threads...), append([][2]string(nil), lifecycle.turns...)
}

func sendWorkerEvent(t *testing.T, connection *websocket.Conn, worker *harnesscontrol.Session, event any) {
	t.Helper()
	frame, err := worker.Send(harnesscontrol.Payload{
		Type: harnesscontrol.MessageTypeEvent, Payload: mustControlPayload(t, event),
	})
	if err != nil {
		t.Fatal(err)
	}
	writeControlValue(t, connection, testControlWireLimits(worker), frame)
	ackMessage := readControlMessage(t, connection, testControlWireLimits(worker))
	if ackMessage.Ack == nil {
		t.Fatalf("event ACK = %+v", ackMessage)
	}
	if err := worker.ReceiveAck(*ackMessage.Ack); err != nil {
		t.Fatal(err)
	}
}

// Session intentionally keeps its limits private. All server fixtures use the
// same negotiated limits, so this helper returns that fixed test contract.
func testControlWireLimits(_ *harnesscontrol.Session) harnesscontrol.Limits {
	return harnesscontrol.Limits{MaxFrameBytes: 1024 * 1024, MaxJSONValues: 65_536, MaxJSONDepth: 128}
}

func mustControlPayload(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func writeControlValue(t *testing.T, connection *websocket.Conn, limits harnesscontrol.Limits, value any) {
	t.Helper()
	raw, err := harnesscontrol.Encode(value, limits)
	if err != nil {
		t.Fatal(err)
	}
	if err := connection.Write(testContext(t), websocket.MessageText, raw); err != nil {
		t.Fatal(err)
	}
}

func readControlMessage(t *testing.T, connection *websocket.Conn, limits harnesscontrol.Limits) harnesscontrol.Message {
	t.Helper()
	messageType, raw, err := connection.Read(testContext(t))
	if err != nil {
		t.Fatal(err)
	}
	if messageType != websocket.MessageText {
		t.Fatalf("control message type = %v", messageType)
	}
	message, err := harnesscontrol.Decode(raw, limits)
	if err != nil {
		t.Fatal(err)
	}
	return message
}

func waitForControlState(t *testing.T, control *AttemptControl, want harnesscontrol.SessionState) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if snapshot, found := control.Snapshot(); found && snapshot.State == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	snapshot, _ := control.Snapshot()
	t.Fatalf("control session state = %s, want %s", snapshot.State, want)
}

func waitForControlJournalFrames(t *testing.T, control *AttemptControl, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if snapshot, found := control.Snapshot(); found && snapshot.JournalFrames == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	snapshot, _ := control.Snapshot()
	t.Fatalf("control journal frames = %d, want %d", snapshot.JournalFrames, want)
}

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func newSelfSignedWorkerCertificate(t *testing.T, identity string) (tls.Certificate, *x509.Certificate) {
	t.Helper()
	parsedIdentity, err := url.Parse(identity)
	if err != nil {
		t.Fatal(err)
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "harness-worker-test"},
		NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true, IsCA: true, URIs: []*url.URL{parsedIdentity},
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
