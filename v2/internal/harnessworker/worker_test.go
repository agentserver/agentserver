package harnessworker

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/codexwire"
	"github.com/agentserver/agentserver/v2/internal/harnessbootstrap"
	"github.com/agentserver/agentserver/v2/internal/harnesscontrol"
	"github.com/agentserver/agentserver/v2/internal/runmanifest"
)

func TestOneShotWorkerAssemblesVerifiedRuntimeAndReportsAfterCleanup(t *testing.T) {
	fixture := newOneShotWorkerFixture(t)
	fixture.runner.notifications = []codexwire.Message{{
		Kind: codexwire.KindNotification, Method: "item/completed", Params: json.RawMessage(`{"item":{"type":"agentMessage"}}`),
	}}
	var notifications []string
	fixture.config.NotificationHandler = func(_ context.Context, message codexwire.Message) error {
		notifications = append(notifications, message.Method)
		fixture.order.add("notification")
		return nil
	}

	if err := runOneShotWorker(t.Context(), fixture.config, fixture.dependencies()); err != nil {
		t.Fatal(err)
	}
	if len(notifications) != 1 || notifications[0] != "item/completed" {
		t.Fatalf("notifications = %v", notifications)
	}
	if fixture.mcpConfig.Endpoint != fixture.manifest.ExecutorMCP.Endpoint ||
		fixture.mcpConfig.TLSIdentity != fixture.manifest.ExecutorMCP.TLSIdentity ||
		fixture.mcpConfig.BearerToken != oneShotExecutorCapability ||
		fixture.mcpConfig.ExpectedCatalogDigest != fixture.catalog.Digest() {
		t.Fatalf("MCP config did not come from verified authority: %+v", fixture.mcpConfig)
	}
	if fixture.processConfig.Environment.ModelCapability != oneShotLLMCapability {
		t.Fatalf("app-server model capability = %q", fixture.processConfig.Environment.ModelCapability)
	}
	if strings.Contains(strings.Join(fixture.processConfig.EnvironmentStringsForTest(), "\n"), oneShotExecutorCapability) {
		t.Fatal("executor capability crossed into app-server environment")
	}
	terminal := fixture.control.terminalSnapshot()
	if terminal.Status != "completed" || terminal.ThreadID != oneShotThreadID || terminal.TurnID != oneShotTurnID {
		t.Fatalf("terminal = %+v", terminal)
	}
	fixture.order.requireBefore(t, "process_close_stdin", "process_wait", "mcp_close", "runtime_close", "control_terminal")
	if got := fixture.runner.request.UserText; got != oneShotPrompt {
		t.Fatalf("runner prompt = %q", got)
	}
	if fixture.runner.request.Start == nil || fixture.runner.request.Start.Model != fixture.manifest.Model.Model ||
		fixture.runner.request.Start.CWD != fixture.runtime.threadCWD {
		t.Fatalf("runner request = %+v", fixture.runner.request)
	}
}

func TestOneShotWorkerTurnsHolderInterruptIntoOneBoundedAppServerInterrupt(t *testing.T) {
	fixture := newOneShotWorkerFixture(t)
	accepted := make(chan struct{})
	fixture.runner.accepted = accepted
	fixture.runner.waitForCancellation = true
	go func() {
		<-accepted
		fixture.control.interrupts <- harnesscontrol.InterruptCommand{
			Kind: harnesscontrol.CommandKindInterrupt, Reason: "cancelled", GraceMillis: 100,
			Message: "cancel the test turn",
		}
	}()

	if err := runOneShotWorker(t.Context(), fixture.config, fixture.dependencies()); err != nil {
		t.Fatal(err)
	}
	terminal := fixture.control.terminalSnapshot()
	if terminal.Status != "interrupted" || terminal.ErrorCode != "interrupt_cancelled" {
		t.Fatalf("interrupt terminal = %+v", terminal)
	}
	if fixture.runner.cancellationCount != 1 {
		t.Fatalf("runner cancellation count = %d", fixture.runner.cancellationCount)
	}
}

func TestOneShotWorkerDoesNotInventTerminalBeforeTurnAcceptance(t *testing.T) {
	fixture := newOneShotWorkerFixture(t)
	fixture.runner.failBeforeAcceptance = errors.New("synthetic pre-turn failure")
	err := runOneShotWorker(t.Context(), fixture.config, fixture.dependencies())
	if err == nil || !strings.Contains(err.Error(), "synthetic pre-turn failure") {
		t.Fatalf("pre-turn worker error = %v", err)
	}
	if terminal := fixture.control.terminalSnapshot(); terminal.Kind != "" {
		t.Fatalf("worker invented pre-turn terminal: %+v", terminal)
	}
}

func TestOneShotWorkerDoesNotReportTerminalBeforeChildShutdownIsConfirmed(t *testing.T) {
	fixture := newOneShotWorkerFixture(t)
	fixture.process.waitErr = context.DeadlineExceeded
	err := runOneShotWorker(t.Context(), fixture.config, fixture.dependencies())
	if !errors.Is(err, ErrAppServerShutdownUnconfirmed) {
		t.Fatalf("unconfirmed child shutdown error = %v", err)
	}
	if terminal := fixture.control.terminalSnapshot(); terminal.Kind != "" {
		t.Fatalf("worker reported terminal before child shutdown: %+v", terminal)
	}
	fixture.order.requireBefore(t, "process_close_stdin", "process_wait", "mcp_close", "runtime_close")
}

const (
	oneShotPrompt             = "perform the deterministic test turn"
	oneShotExecutorCapability = "executor-capability-one-shot"
	oneShotLLMCapability      = "llmproxy-capability-one-shot"
	oneShotThreadID           = "thread-one-shot"
	oneShotTurnID             = "turn-one-shot"
)

type oneShotWorkerFixture struct {
	config        OneShotWorkerConfig
	manifest      runmanifest.Manifest
	catalog       *Catalog
	runtime       *fakePreparedWorkerRuntime
	control       *fakeOneShotWorkerControl
	mcp           *fakeOneShotWorkerMCP
	process       *fakeOneShotWorkerProcess
	runner        *fakeOneShotWorkerRunner
	order         *workerOrder
	mcpConfig     MCPClientConfig
	processConfig AppServerProcessConfig
}

func newOneShotWorkerFixture(t *testing.T) *oneShotWorkerFixture {
	t.Helper()
	catalog := runnerTestCatalog(t)
	manifest, signed, keyring := oneShotSignedManifest(t, catalog, []byte(oneShotPrompt))
	bootstrap, err := harnessbootstrap.Encode(harnessbootstrap.Envelope{
		Version: harnessbootstrap.CurrentVersion, SignedManifest: signed,
		ControlCapability: base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)),
		RuntimeCapabilities: harnessbootstrap.RuntimeCapabilities{
			ExecutorMCP: oneShotExecutorCapability, LLMProxy: oneShotLLMCapability,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	order := &workerOrder{}
	runtime := newFakePreparedWorkerRuntime(t, order)
	control := newFakeOneShotWorkerControl(order)
	mcp := &fakeOneShotWorkerMCP{catalog: catalog, order: order}
	process := &fakeOneShotWorkerProcess{order: order}
	runner := newFakeOneShotWorkerRunner(order)
	fixture := &oneShotWorkerFixture{
		manifest: manifest, catalog: catalog, runtime: runtime, control: control,
		mcp: mcp, process: process, runner: runner, order: order,
	}
	fixture.config = OneShotWorkerConfig{
		BootstrapPipe: pipeWithWorkerBytes(t, bootstrap), PromptPipe: pipeWithWorkerBytes(t, []byte(oneShotPrompt)),
		VerificationKeyring: keyring, RuntimePreparer: fakeWorkerRuntimePreparer{runtime: runtime},
		ControlHTTPClient: &http.Client{}, ExecutorHTTPClient: &http.Client{},
		WorkerInstanceIDGenerator: func() (string, error) {
			return "71000000-0000-4000-8000-000000000007", nil
		},
		ElicitationHandler: func(context.Context, ElicitationRequest) (ElicitationDecision, error) {
			return ElicitationDecision{Action: ApprovalDecline}, nil
		},
		NotificationHandler: func(context.Context, codexwire.Message) error { return nil },
		ClientInfo:          AppServerClientInfo{Name: "agentserver-harness-worker", Title: "Agentserver Harness Worker", Version: "v2-test"},
	}
	return fixture
}

func (fixture *oneShotWorkerFixture) dependencies() oneShotWorkerDependencies {
	return oneShotWorkerDependencies{
		newControl: func(config WorkerControlClientConfig) (oneShotWorkerControl, error) {
			fixture.order.add("control_create")
			if config.Manifest.RunID != fixture.manifest.RunID || config.ControlCapability == "" {
				return nil, errors.New("wrong control authority")
			}
			return fixture.control, nil
		},
		connectMCP: func(_ context.Context, config MCPClientConfig) (oneShotWorkerMCP, error) {
			fixture.order.add("mcp_connect")
			fixture.mcpConfig = config
			return fixture.mcp, nil
		},
		startProcess: func(_ context.Context, config AppServerProcessConfig) (oneShotWorkerProcess, error) {
			fixture.order.add("process_start")
			fixture.processConfig = config
			return fixture.process, nil
		},
		newRunner: func(_ AppServerTransport, _ *DynamicBridge, options AppServerRunnerOptions) (oneShotWorkerRunner, error) {
			fixture.order.add("runner_create")
			fixture.runner.lifecycle = options.LifecycleSink
			return fixture.runner, nil
		},
	}
}

func oneShotSignedManifest(t *testing.T, catalog *Catalog, prompt []byte) (runmanifest.Manifest, runmanifest.SignedManifest, *runmanifest.VerificationKeyring) {
	t.Helper()
	promptDigest := sha256.Sum256(prompt)
	executorMCP, err := runmanifest.ExecutorMCPFromCatalog(
		"https://executor-gateway.agentserver.test/mcp",
		"spiffe://agentserver.test/ns/agentserver/sa/executor-gateway",
		"executor-mcp", "executor-tools/1", "72000000-0000-4000-8000-000000000007", catalog,
	)
	if err != nil {
		t.Fatal(err)
	}
	manifest := runmanifest.Manifest{
		ManifestVersion: runmanifest.CurrentVersion, CanonicalizerVersion: runmanifest.Canonicalizer,
		WorkspaceID: "73000000-0000-4000-8000-000000000007", SessionID: "74000000-0000-4000-8000-000000000007",
		RunID: "75000000-0000-4000-8000-000000000007", RunAttemptID: "76000000-0000-4000-8000-000000000007",
		RunAttemptGeneration: 3, HolderID: "pool-holder-one-shot",
		Prompt: runmanifest.ObjectPointer{
			ObjectID: "77000000-0000-4000-8000-000000000007", SHA256: base64ToHex(promptDigest[:]),
			SizeBytes: int64(len(prompt)), MediaType: PromptMediaType,
		},
		CodexRuntimeManifestDigest: strings.Repeat("a", 64),
		Model: runmanifest.ModelRoute{
			Model: "gpt-5", Provider: "llmproxy", Endpoint: "https://llmproxy.agentserver.test/v1",
			TLSIdentity: "spiffe://agentserver.test/ns/agentserver/sa/llmproxy", Audience: "llmproxy",
		},
		ExecutorMCP:    executorMCP,
		ExecutorPolicy: runmanifest.ExecutorPolicy{Version: "executor-policy/1", ContextDigest: strings.Repeat("b", 64)},
		Limits: runmanifest.RunLimits{
			MaxRunDurationMS: 60_000, MaxApprovalTTLMS: 10_000, GatewayActiveExecutionTimeoutMS: 30_000,
			MCPTransportGraceMS: 100, WorkerCallbackGraceMS: 500,
			MaxEventBufferBytes: 2 * 1024 * 1024, MaxControlBufferBytes: 2 * 1024 * 1024,
		},
		CheckpointAllowlistVersion: 1, WorkerImageDigest: strings.Repeat("c", 64),
		ExpectedServiceAccount: "harness-worker",
		ControllerCallback: runmanifest.ControllerCallback{
			Endpoint:    "https://harness-pool.agentserver.test/internal/v2/harness/control",
			TLSIdentity: "spiffe://agentserver.test/ns/agentserver/sa/harness-pool",
			Audience:    "harness-pool-control", HolderID: "pool-holder-one-shot",
		},
	}
	seed := sha256.Sum256([]byte("one-shot-worker-signing-key"))
	privateKey := ed25519.NewKeyFromSeed(seed[:])
	signed, err := runmanifest.Sign(manifest, "one-shot-worker-key", privateKey)
	if err != nil {
		t.Fatal(err)
	}
	document, err := json.Marshal(runmanifest.VerificationKeyringDocument{
		Version: runmanifest.VerificationKeyringVersion,
		Keys: []runmanifest.VerificationKeyDocument{{
			KeyID: "one-shot-worker-key", Algorithm: runmanifest.SignatureAlgorithm,
			PublicKey: base64.RawURLEncoding.EncodeToString(privateKey.Public().(ed25519.PublicKey)),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	keyring, err := runmanifest.ParseVerificationKeyring(document)
	if err != nil {
		t.Fatal(err)
	}
	return manifest, signed, keyring
}

func base64ToHex(value []byte) string {
	const digits = "0123456789abcdef"
	encoded := make([]byte, len(value)*2)
	for index, item := range value {
		encoded[index*2] = digits[item>>4]
		encoded[index*2+1] = digits[item&15]
	}
	return string(encoded)
}

func pipeWithWorkerBytes(t *testing.T, contents []byte) *os.File {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, writeErr := io.Copy(writer, bytes.NewReader(contents))
		done <- errors.Join(writeErr, writer.Close())
	}()
	t.Cleanup(func() {
		_ = reader.Close()
		if err := <-done; err != nil && !errors.Is(err, os.ErrClosed) {
			t.Errorf("write worker input pipe: %v", err)
		}
	})
	return reader
}

type fakeWorkerRuntimePreparer struct{ runtime PreparedWorkerRuntime }

func (preparer fakeWorkerRuntimePreparer) Prepare(context.Context, runmanifest.Manifest) (PreparedWorkerRuntime, error) {
	return preparer.runtime, nil
}

type fakePreparedWorkerRuntime struct {
	restoreHome string
	stagingRoot string
	threadCWD   string
	order       *workerOrder
	mu          sync.Mutex
	closed      bool
}

func newFakePreparedWorkerRuntime(t *testing.T, order *workerOrder) *fakePreparedWorkerRuntime {
	t.Helper()
	root := t.TempDir()
	restoreHome := filepath.Join(root, "restore-home")
	stagingRoot := filepath.Join(root, "staging")
	threadCWD := filepath.Join(root, "cwd")
	for _, directory := range []string{restoreHome, stagingRoot, threadCWD} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return &fakePreparedWorkerRuntime{restoreHome: restoreHome, stagingRoot: stagingRoot, threadCWD: threadCWD, order: order}
}

func (runtime *fakePreparedWorkerRuntime) CheckpointRoots() (string, string) {
	return runtime.restoreHome, runtime.stagingRoot
}

func (runtime *fakePreparedWorkerRuntime) Finalize(context.Context, runmanifest.Manifest, *RestoredCheckpoint) (PreparedAppServerRuntime, error) {
	runtime.order.add("runtime_finalize")
	return PreparedAppServerRuntime{
		ProcessConfig: AppServerProcessConfig{Environment: AppServerRuntimeEnvironment{}},
		ThreadCWD:     runtime.threadCWD,
	}, nil
}

func (runtime *fakePreparedWorkerRuntime) Close() error {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if !runtime.closed {
		runtime.closed = true
		runtime.order.add("runtime_close")
	}
	return nil
}

type fakeOneShotWorkerControl struct {
	order      *workerOrder
	interrupts chan harnesscontrol.InterruptCommand
	done       chan struct{}
	doneOnce   sync.Once
	mu         sync.Mutex
	threadID   string
	turnID     string
	terminal   harnesscontrol.TurnTerminalEvent
}

func newFakeOneShotWorkerControl(order *workerOrder) *fakeOneShotWorkerControl {
	return &fakeOneShotWorkerControl{order: order, interrupts: make(chan harnesscontrol.InterruptCommand, 1), done: make(chan struct{})}
}

func (control *fakeOneShotWorkerControl) Start(context.Context) error {
	control.order.add("control_start")
	return nil
}

func (control *fakeOneShotWorkerControl) Interrupts() <-chan harnesscontrol.InterruptCommand {
	return control.interrupts
}

func (control *fakeOneShotWorkerControl) SendThreadReady(_ context.Context, threadID string, _ bool) error {
	control.mu.Lock()
	defer control.mu.Unlock()
	control.threadID = threadID
	return nil
}

func (control *fakeOneShotWorkerControl) SendTurnAccepted(_ context.Context, threadID, turnID string) error {
	control.mu.Lock()
	defer control.mu.Unlock()
	if threadID != control.threadID {
		return errors.New("fake control thread changed")
	}
	control.turnID = turnID
	return nil
}

func (control *fakeOneShotWorkerControl) SendTurnTerminal(_ context.Context, event harnesscontrol.TurnTerminalEvent) error {
	control.mu.Lock()
	if event.ThreadID != control.threadID || event.TurnID != control.turnID {
		control.mu.Unlock()
		return errors.New("fake control terminal identity changed")
	}
	control.terminal = event
	control.mu.Unlock()
	control.order.add("control_terminal")
	control.doneOnce.Do(func() { close(control.done) })
	return nil
}

func (control *fakeOneShotWorkerControl) Wait(ctx context.Context) error {
	select {
	case <-control.done:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func (control *fakeOneShotWorkerControl) Close(error) {
	control.doneOnce.Do(func() { close(control.done) })
}

func (control *fakeOneShotWorkerControl) terminalSnapshot() harnesscontrol.TurnTerminalEvent {
	control.mu.Lock()
	defer control.mu.Unlock()
	return control.terminal
}

type fakeOneShotWorkerMCP struct {
	catalog   *Catalog
	order     *workerOrder
	closeOnce sync.Once
}

func (mcp *fakeOneShotWorkerMCP) Catalog() *Catalog { return mcp.catalog }

func (*fakeOneShotWorkerMCP) CallDynamicTool(context.Context, DynamicCall) (DynamicToolResult, error) {
	return DynamicToolResult{}, errors.New("unexpected fake MCP call")
}

func (mcp *fakeOneShotWorkerMCP) Close() error {
	mcp.closeOnce.Do(func() { mcp.order.add("mcp_close") })
	return nil
}

type fakeOneShotWorkerProcess struct {
	order     *workerOrder
	closeOnce sync.Once
	waitOnce  sync.Once
	waitErr   error
}

func (*fakeOneShotWorkerProcess) Send(any) error { return errors.New("unexpected fake process send") }

func (*fakeOneShotWorkerProcess) Receive(context.Context) (codexwire.Message, error) {
	return codexwire.Message{}, errors.New("unexpected fake process receive")
}

func (process *fakeOneShotWorkerProcess) CloseStdin() error {
	process.closeOnce.Do(func() { process.order.add("process_close_stdin") })
	return nil
}

func (process *fakeOneShotWorkerProcess) Wait(context.Context) error {
	process.waitOnce.Do(func() { process.order.add("process_wait") })
	return process.waitErr
}

type fakeOneShotWorkerRunner struct {
	order                *workerOrder
	events               chan codexwire.Message
	lifecycle            AppServerLifecycleSink
	notifications        []codexwire.Message
	accepted             chan struct{}
	waitForCancellation  bool
	failBeforeAcceptance error
	request              AppServerRunRequest
	cancellationCount    int
}

func newFakeOneShotWorkerRunner(order *workerOrder) *fakeOneShotWorkerRunner {
	return &fakeOneShotWorkerRunner{order: order, events: make(chan codexwire.Message, 8)}
}

func (runner *fakeOneShotWorkerRunner) Events() <-chan codexwire.Message { return runner.events }

func (runner *fakeOneShotWorkerRunner) Run(ctx context.Context, request AppServerRunRequest) (AppServerRunResult, error) {
	runner.request = request
	runner.order.add("runner_run")
	defer close(runner.events)
	if runner.failBeforeAcceptance != nil {
		return AppServerRunResult{}, runner.failBeforeAcceptance
	}
	if err := runner.lifecycle.SendThreadReady(ctx, oneShotThreadID, false); err != nil {
		return AppServerRunResult{}, err
	}
	if err := runner.lifecycle.SendTurnAccepted(ctx, oneShotThreadID, oneShotTurnID); err != nil {
		return AppServerRunResult{}, err
	}
	if runner.accepted != nil {
		close(runner.accepted)
	}
	for _, notification := range runner.notifications {
		runner.events <- notification
	}
	terminalStatus := "completed"
	var runErr error
	if runner.waitForCancellation {
		<-ctx.Done()
		runner.cancellationCount++
		terminalStatus = "interrupted"
		runErr = context.Cause(ctx)
	}
	return AppServerRunResult{
		Thread:   AppServerThreadResult{Thread: AppServerThread{ID: oneShotThreadID}},
		Turn:     AppServerTurn{ID: oneShotTurnID, Status: "inProgress"},
		Terminal: AppServerTerminal{ThreadID: oneShotThreadID, Turn: AppServerTurn{ID: oneShotTurnID, Status: terminalStatus}},
	}, runErr
}

type workerOrder struct {
	mu    sync.Mutex
	items []string
}

func (order *workerOrder) add(item string) {
	order.mu.Lock()
	order.items = append(order.items, item)
	order.mu.Unlock()
}

func (order *workerOrder) requireBefore(t *testing.T, items ...string) {
	t.Helper()
	order.mu.Lock()
	got := append([]string(nil), order.items...)
	order.mu.Unlock()
	position := -1
	for _, item := range items {
		found := -1
		for index := position + 1; index < len(got); index++ {
			if got[index] == item {
				found = index
				break
			}
		}
		if found < 0 {
			t.Fatalf("order %v does not contain %q after position %d", got, item, position)
		}
		position = found
	}
}

// EnvironmentStringsForTest intentionally mirrors only the capability field;
// production child environment validation remains in AppServerProcess.
func (config AppServerProcessConfig) EnvironmentStringsForTest() []string {
	return []string{AppServerModelCapabilityEnvironment + "=" + config.Environment.ModelCapability}
}
