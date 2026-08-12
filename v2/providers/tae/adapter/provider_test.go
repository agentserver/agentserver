package adapter

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/executionbackend"
	"github.com/agentserver/agentserver/v2/internal/larkegresspolicy"
	"github.com/agentserver/agentserver/v2/internal/managedruntime"
	"github.com/agentserver/agentserver/v2/internal/sandboxgateway"
	"github.com/agentserver/agentserver/v2/internal/taepolicy"
)

var testNow = time.Date(2026, 8, 6, 20, 0, 0, 0, time.UTC)

const testTAEImage = "aliyun-sin-hub.byted.org/agentserver/tae-sandbox:sha256-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type fakeControlPlane struct {
	create func(context.Context, CreateInput) (ControlSession, error)
	get    func(context.Context, string) (ControlSession, error)
	search func(context.Context, SearchInput) (SearchResult, error)
	update func(context.Context, string, time.Duration) error
	delete func(context.Context, string) error
}

func (control *fakeControlPlane) Create(ctx context.Context, input CreateInput) (ControlSession, error) {
	return control.create(ctx, input)
}

func (control *fakeControlPlane) Get(ctx context.Context, sessionID string) (ControlSession, error) {
	return control.get(ctx, sessionID)
}

func (control *fakeControlPlane) Search(ctx context.Context, input SearchInput) (SearchResult, error) {
	return control.search(ctx, input)
}

func (control *fakeControlPlane) UpdateTTL(ctx context.Context, sessionID string, ttl time.Duration) error {
	return control.update(ctx, sessionID, ttl)
}

func (control *fakeControlPlane) Delete(ctx context.Context, sessionID string) error {
	return control.delete(ctx, sessionID)
}

type fakeDataPlane struct {
	start    func(context.Context, string, StartProcessInput) (EventStream, error)
	connect  func(context.Context, string, int) (EventStream, error)
	signal   func(context.Context, string, int, int) (string, error)
	stat     func(context.Context, string, string) (FileInfo, string, error)
	download func(context.Context, string, string) (Download, error)
}

func (data *fakeDataPlane) StartProcess(ctx context.Context, sessionID string, input StartProcessInput) (EventStream, error) {
	return data.start(ctx, sessionID, input)
}

func (data *fakeDataPlane) ConnectProcess(ctx context.Context, sessionID string, pid int) (EventStream, error) {
	return data.connect(ctx, sessionID, pid)
}

func (data *fakeDataPlane) SignalProcess(ctx context.Context, sessionID string, pid, signal int) (string, error) {
	return data.signal(ctx, sessionID, pid, signal)
}

func (data *fakeDataPlane) Stat(ctx context.Context, sessionID, path string) (FileInfo, string, error) {
	return data.stat(ctx, sessionID, path)
}

func (data *fakeDataPlane) Download(ctx context.Context, sessionID, path string) (Download, error) {
	return data.download(ctx, sessionID, path)
}

type scriptedStream struct {
	mu        sync.Mutex
	events    []StreamEvent
	finalErr  error
	requestID string
	closed    bool
}

func (stream *scriptedStream) Next(ctx context.Context) (StreamEvent, error) {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	select {
	case <-ctx.Done():
		return StreamEvent{}, ctx.Err()
	default:
	}
	if len(stream.events) == 0 {
		if stream.finalErr == nil {
			return StreamEvent{}, io.EOF
		}
		return StreamEvent{}, stream.finalErr
	}
	event := stream.events[0]
	stream.events = stream.events[1:]
	return event, nil
}

func (stream *scriptedStream) RequestID() string { return stream.requestID }

func (stream *scriptedStream) Close() error {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	stream.closed = true
	return nil
}

func (stream *scriptedStream) IsClosed() bool {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	return stream.closed
}

func TestProviderLifecycleUsesProviderAssignedIdentityAndCompleteMetadata(t *testing.T) {
	var captured CreateInput
	control := defaultFakeControl()
	control.create = func(_ context.Context, input CreateInput) (ControlSession, error) {
		captured = input
		return readyControlSession("tae-session-1", input.Metadata), nil
	}
	provider := newTestProvider(t, control, defaultFakeData())
	request := validCreateRequest()
	created, err := provider.CreateSandbox(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if created.SessionRef != "tae-session-1" || created.State != sandboxgateway.ProviderSandboxReady || created.Root != "/workspace" {
		t.Fatalf("created sandbox = %+v", created)
	}
	if request.SessionRef != "" {
		t.Fatal("test must exercise provider-assigned identity")
	}
	want := provider.createMetadata(request.SandboxID, request.IdempotencyKey, request.WorkspaceID, request.SessionID,
		request.EnvironmentID, request.RuntimeProfileSHA256, request.PackSetSHA256)
	if !metadataContainsIdentity(captured.Metadata, want) || len(captured.Metadata) != 8 ||
		captured.Metadata[MetadataTAEPolicySHA256] != provider.policy.BindingSHA256 {
		t.Fatalf("create metadata = %#v, want %#v", captured.Metadata, want)
	}

	control.search = func(_ context.Context, input SearchInput) (SearchResult, error) {
		if input.Limit != 2 || !metadataContainsIdentity(input.Metadata, want) || len(input.Metadata) != 8 {
			t.Fatalf("search input = %+v", input)
		}
		return SearchResult{Sessions: []ControlSession{readyControlSession("tae-session-1", want)}, Total: 1}, nil
	}
	found, err := provider.FindSandbox(t.Context(), validFindRequest())
	if err != nil || found.SessionRef != created.SessionRef {
		t.Fatalf("FindSandbox() = %+v, %v", found, err)
	}
}

func TestProviderAcceptsTAEAddedMetadataWhileRequiringCompleteIdentity(t *testing.T) {
	control := defaultFakeControl()
	var deleted string
	control.create = func(_ context.Context, input CreateInput) (ControlSession, error) {
		metadata := cloneStrings(input.Metadata)
		metadata["tae_provider_field"] = "provider-owned"
		return readyControlSession("tae-session-with-provider-metadata", metadata), nil
	}
	control.get = func(_ context.Context, sessionID string) (ControlSession, error) {
		metadata := providerIdentityMetadataForTest(t)
		metadata["tae_provider_field"] = "provider-owned"
		return readyControlSession(sessionID, metadata), nil
	}
	control.delete = func(_ context.Context, sessionID string) error {
		deleted = sessionID
		return nil
	}
	provider := newTestProvider(t, control, defaultFakeData())
	created, err := provider.CreateSandbox(t.Context(), validCreateRequest())
	if err != nil {
		t.Fatal(err)
	}
	if created.SessionRef != "tae-session-with-provider-metadata" || created.State != sandboxgateway.ProviderSandboxReady {
		t.Fatalf("CreateSandbox() = %+v", created)
	}

	control.search = func(_ context.Context, input SearchInput) (SearchResult, error) {
		metadata := cloneStrings(input.Metadata)
		metadata["tae_provider_field"] = "provider-owned"
		return SearchResult{Sessions: []ControlSession{
			readyControlSession("tae-session-with-provider-metadata", metadata),
		}, Total: 1}, nil
	}
	found, err := provider.FindSandbox(t.Context(), validFindRequest())
	if err != nil {
		t.Fatal(err)
	}
	if found.SessionRef != created.SessionRef || found.State != sandboxgateway.ProviderSandboxReady {
		t.Fatalf("FindSandbox() = %+v", found)
	}
	if err := provider.DeleteSandbox(t.Context(), sandboxgateway.DeleteSandboxProviderRequest{
		SessionRef: created.SessionRef,
		Identity:   validFindRequest(),
	}); err != nil {
		t.Fatal(err)
	}
	if deleted != created.SessionRef {
		t.Fatalf("deleted session = %q, want %q", deleted, created.SessionRef)
	}
}

func TestProviderRejectsSessionWithoutExactPolicyBindingMetadata(t *testing.T) {
	control := defaultFakeControl()
	control.create = func(_ context.Context, input CreateInput) (ControlSession, error) {
		metadata := cloneStrings(input.Metadata)
		delete(metadata, MetadataTAEPolicySHA256)
		return readyControlSession("tae-session-drifted", metadata), nil
	}
	provider := newTestProvider(t, control, defaultFakeData())
	_, err := provider.CreateSandbox(t.Context(), validCreateRequest())
	var providerError *sandboxgateway.ProviderError
	if !errors.As(err, &providerError) || providerError.Code != "provider_metadata_mismatch" || !providerError.Ambiguous {
		t.Fatalf("CreateSandbox() error = %#v", err)
	}
}

func TestProviderRejectsSessionWithConflictingIdentityAndProviderMetadata(t *testing.T) {
	control := defaultFakeControl()
	control.create = func(_ context.Context, input CreateInput) (ControlSession, error) {
		metadata := cloneStrings(input.Metadata)
		metadata[MetadataSessionID] = "different-session"
		metadata["tae_provider_field"] = "provider-owned"
		return readyControlSession("tae-session-conflicting", metadata), nil
	}
	provider := newTestProvider(t, control, defaultFakeData())
	_, err := provider.CreateSandbox(t.Context(), validCreateRequest())
	var providerError *sandboxgateway.ProviderError
	if !errors.As(err, &providerError) || providerError.Code != "provider_metadata_mismatch" || !providerError.Ambiguous {
		t.Fatalf("CreateSandbox() error = %#v", err)
	}
}

func TestProviderRejectsReadySessionWithDifferentRuntimeCommand(t *testing.T) {
	control := defaultFakeControl()
	control.create = func(_ context.Context, input CreateInput) (ControlSession, error) {
		session := readyControlSession("tae-session-drifted", input.Metadata)
		session.Command = "/opt/tiger/run.sh"
		return session, nil
	}
	provider := newTestProvider(t, control, defaultFakeData())
	_, err := provider.CreateSandbox(t.Context(), validCreateRequest())
	var providerError *sandboxgateway.ProviderError
	if !errors.As(err, &providerError) || providerError.Code != "runtime_command_mismatch" {
		t.Fatalf("CreateSandbox() error = %#v", err)
	}
}

func TestProviderAcceptsReadySessionWhenTAEOmitsRuntimeCommand(t *testing.T) {
	control := defaultFakeControl()
	control.create = func(_ context.Context, input CreateInput) (ControlSession, error) {
		session := readyControlSession("tae-session-unreported-command", input.Metadata)
		session.Command = ""
		return session, nil
	}
	provider := newTestProvider(t, control, defaultFakeData())
	created, err := provider.CreateSandbox(t.Context(), validCreateRequest())
	if err != nil {
		t.Fatal(err)
	}
	if created.State != sandboxgateway.ProviderSandboxReady || created.Root != "/workspace" {
		t.Fatalf("created sandbox = %+v", created)
	}
}

func TestProviderTerminalReadinessUsesSandboxdWithTerminalStatesTakingPrecedence(t *testing.T) {
	for name, testCase := range map[string]struct {
		status          string
		deleted         bool
		sandboxdEnabled bool
		wantState       sandboxgateway.ProviderSandboxState
		wantClass       string
	}{
		"empty status with sandboxd":          {sandboxdEnabled: true, wantState: sandboxgateway.ProviderSandboxReady, wantClass: "creating"},
		"starting with sandboxd":              {status: "starting", sandboxdEnabled: true, wantState: sandboxgateway.ProviderSandboxReady, wantClass: "creating"},
		"running without sandboxd":            {status: "running", wantState: sandboxgateway.ProviderSandboxCreating, wantClass: "ready"},
		"unknown without sandboxd":            {status: "provider-new-state", wantState: sandboxgateway.ProviderSandboxUnknown, wantClass: "other"},
		"unknown with sandboxd stays unknown": {status: "provider-new-state", sandboxdEnabled: true, wantState: sandboxgateway.ProviderSandboxUnknown, wantClass: "other"},
		"failed takes precedence":             {status: "failed", sandboxdEnabled: true, wantState: sandboxgateway.ProviderSandboxFailed, wantClass: "failed"},
		"deleting takes precedence":           {status: "deleting", sandboxdEnabled: true, wantState: sandboxgateway.ProviderSandboxDeleting, wantClass: "deleting"},
		"explicit deletion takes precedence":  {status: "running", deleted: true, sandboxdEnabled: true, wantState: sandboxgateway.ProviderSandboxDeleted, wantClass: "deleted"},
	} {
		t.Run(name, func(t *testing.T) {
			provider := newTestProvider(t, defaultFakeControl(), defaultFakeData())
			got, err := provider.providerSandbox(ControlSession{
				ID: "tae-readiness-1", Status: testCase.status, Deleted: testCase.deleted,
				SandboxdEnabled: testCase.sandboxdEnabled, ExpiresAt: testNow.Add(time.Hour),
			})
			if err != nil {
				t.Fatal(err)
			}
			if got.State != testCase.wantState || got.ProviderStatusClass != testCase.wantClass || got.ExecutionReady != testCase.sandboxdEnabled {
				t.Fatalf("provider sandbox = %+v, want state %q class %q execution-ready %v", got, testCase.wantState, testCase.wantClass, testCase.sandboxdEnabled)
			}
			if got.State == sandboxgateway.ProviderSandboxReady && got.Root != "/workspace" {
				t.Fatalf("ready provider root = %q", got.Root)
			}
		})
	}
}

func TestRuntimeCommandConflictsOnlyOnReportedDifferentValue(t *testing.T) {
	for name, testCase := range map[string]struct {
		command string
		want    bool
	}{
		"unreported": {command: "", want: false},
		"exact":      {command: managedruntime.ExecutablePath, want: false},
		"different":  {command: "/opt/tiger/run.sh", want: true},
	} {
		t.Run(name, func(t *testing.T) {
			if got := RuntimeCommandConflicts(testCase.command); got != testCase.want {
				t.Fatalf("RuntimeCommandConflicts(%q) = %v, want %v", testCase.command, got, testCase.want)
			}
		})
	}
}

func TestNewProviderRejectsMissingOrMismatchedPolicyBinding(t *testing.T) {
	base := Config{
		Control: defaultFakeControl(), Data: defaultFakeData(), Region: "sg", PSM: "psm.agentserver.tae",
		Root: "/workspace",
	}
	if _, err := NewProvider(base); err == nil {
		t.Fatal("provider accepted a missing TAE policy binding")
	}
	base.Policy = validProviderPolicy()
	base.Policy.BindingSHA256 = strings.Repeat("f", 64)
	if _, err := NewProvider(base); err == nil {
		t.Fatal("provider accepted a drifted TAE policy binding digest")
	}
}

func TestProviderFindRejectsAmbiguousExactMatches(t *testing.T) {
	control := defaultFakeControl()
	control.search = func(_ context.Context, input SearchInput) (SearchResult, error) {
		return SearchResult{Sessions: []ControlSession{
			readyControlSession("tae-session-1", input.Metadata),
			readyControlSession("tae-session-2", input.Metadata),
		}, Total: 2}, nil
	}
	provider := newTestProvider(t, control, defaultFakeData())
	_, err := provider.FindSandbox(t.Context(), validFindRequest())
	var providerError *sandboxgateway.ProviderError
	if !errors.As(err, &providerError) || !providerError.Ambiguous || providerError.Code != "provider_create_ambiguous" {
		t.Fatalf("FindSandbox() error = %#v", err)
	}
}

func TestProviderDeleteRecoversAllExactMatchesWhenCreateResponseWasLost(t *testing.T) {
	control := defaultFakeControl()
	var deleted []string
	control.search = func(_ context.Context, input SearchInput) (SearchResult, error) {
		if input.Limit != maxDeleteSearchMatches {
			t.Fatalf("delete recovery search limit = %d", input.Limit)
		}
		return SearchResult{Sessions: []ControlSession{
			readyControlSession("tae-session-1", input.Metadata),
			readyControlSession("tae-session-2", input.Metadata),
		}, Total: 2}, nil
	}
	control.delete = func(_ context.Context, sessionID string) error {
		deleted = append(deleted, sessionID)
		return nil
	}
	provider := newTestProvider(t, control, defaultFakeData())
	if err := provider.DeleteSandbox(t.Context(), sandboxgateway.DeleteSandboxProviderRequest{Identity: validFindRequest()}); err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 2 || deleted[0] != "tae-session-1" || deleted[1] != "tae-session-2" {
		t.Fatalf("deleted sessions = %v", deleted)
	}
}

func TestProviderDeleteDoesNotMutateAnIncompleteRecoverySearch(t *testing.T) {
	control := defaultFakeControl()
	deleteCalls := 0
	control.search = func(_ context.Context, input SearchInput) (SearchResult, error) {
		return SearchResult{Sessions: []ControlSession{readyControlSession("tae-session-1", input.Metadata)}, Total: 2}, nil
	}
	control.delete = func(context.Context, string) error {
		deleteCalls++
		return nil
	}
	provider := newTestProvider(t, control, defaultFakeData())
	err := provider.DeleteSandbox(t.Context(), sandboxgateway.DeleteSandboxProviderRequest{Identity: validFindRequest()})
	var providerError *sandboxgateway.ProviderError
	if !errors.As(err, &providerError) || !providerError.Ambiguous || providerError.Code != "provider_delete_search_incomplete" {
		t.Fatalf("DeleteSandbox() error = %#v", err)
	}
	if deleteCalls != 0 {
		t.Fatalf("delete calls = %d, want zero", deleteCalls)
	}
}

func TestProviderDeleteReferencedSessionRequiresExactIdentity(t *testing.T) {
	control := defaultFakeControl()
	control.get = func(_ context.Context, sessionID string) (ControlSession, error) {
		return readyControlSession(sessionID, map[string]string{"wrong": "metadata"}), nil
	}
	deleteCalls := 0
	control.delete = func(context.Context, string) error {
		deleteCalls++
		return nil
	}
	provider := newTestProvider(t, control, defaultFakeData())
	err := provider.DeleteSandbox(t.Context(), sandboxgateway.DeleteSandboxProviderRequest{
		SessionRef: "tae-session-1", Identity: validFindRequest(),
	})
	var providerError *sandboxgateway.ProviderError
	if !errors.As(err, &providerError) || providerError.Code != "provider_identity_mismatch" {
		t.Fatalf("DeleteSandbox() error = %#v", err)
	}
	if deleteCalls != 0 {
		t.Fatalf("delete calls = %d, want zero", deleteCalls)
	}
}

func TestProviderProcessExchangeAcknowledgesOnlyProcessStart(t *testing.T) {
	stream := &scriptedStream{requestID: "tae-log-1", events: []StreamEvent{
		{Name: "process.start", Data: map[string]any{"pid": 42}},
		{Name: "process.data", Data: map[string]any{"stdout": "hello\n", "stderr": "warning\n"}},
		{Name: "process.exit", Data: map[string]any{"exit_code": 0}},
	}}
	data := defaultFakeData()
	data.start = func(_ context.Context, sessionID string, input StartProcessInput) (EventStream, error) {
		if sessionID != "tae-session-1" || input.Executable != "lark-cli" || input.Environment["LARK_TOKEN"] != "placeholder-only" {
			t.Fatalf("start input = %q %+v", sessionID, input)
		}
		return stream, nil
	}
	provider := newTestProvider(t, defaultFakeControl(), data)
	exchange, err := provider.StartProcess(t.Context(), sandboxgateway.StartProcessProviderRequest{
		SessionRef: "tae-session-1", Request: validStartRequest(1024),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	ack, err := exchange.AwaitAcknowledgement(ctx)
	if err != nil || ack.ProviderOperationID != "tae-pid:42" || ack.ProviderRequestID != "tae-log-1" {
		t.Fatalf("ack = %+v, %v", ack, err)
	}
	stdout, err := exchange.NextEvent(ctx)
	if err != nil || stdout.Sequence != 1 || stdout.Kind != executionbackend.EventStdout || string(stdout.Data) != "hello\n" {
		t.Fatalf("stdout = %+v, %v", stdout, err)
	}
	stderr, err := exchange.NextEvent(ctx)
	if err != nil || stderr.Sequence != 2 || stderr.Kind != executionbackend.EventStderr || string(stderr.Data) != "warning\n" {
		t.Fatalf("stderr = %+v, %v", stderr, err)
	}
	if _, err := exchange.NextEvent(ctx); !errors.Is(err, io.EOF) {
		t.Fatalf("NextEvent() error = %v, want EOF", err)
	}
	terminal, err := exchange.AwaitTerminal(ctx)
	if err != nil || terminal.Status != executionbackend.TerminalSucceeded || terminal.ExitCode == nil || *terminal.ExitCode != 0 || !terminal.OutputComplete {
		t.Fatalf("terminal = %+v, %v", terminal, err)
	}
}

func TestProviderOutputLimitKillsProcess(t *testing.T) {
	stream := &scriptedStream{events: []StreamEvent{
		{Name: "process.start", Data: map[string]any{"pid": 99}},
		{Name: "process.data", Data: map[string]any{"stdout": "abcdef"}},
	}}
	var signalPID, signalNumber int
	data := defaultFakeData()
	data.start = func(context.Context, string, StartProcessInput) (EventStream, error) { return stream, nil }
	data.signal = func(_ context.Context, _ string, pid, signal int) (string, error) {
		signalPID, signalNumber = pid, signal
		return "kill-log", nil
	}
	provider := newTestProvider(t, defaultFakeControl(), data)
	exchange, err := provider.StartProcess(t.Context(), sandboxgateway.StartProcessProviderRequest{
		SessionRef: "tae-session-1", Request: validStartRequest(3),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err := exchange.AwaitAcknowledgement(ctx); err != nil {
		t.Fatal(err)
	}
	event, err := exchange.NextEvent(ctx)
	if err != nil || string(event.Data) != "abc" {
		t.Fatalf("bounded event = %+v, %v", event, err)
	}
	terminal, err := exchange.AwaitTerminal(ctx)
	if err != nil || terminal.Status != executionbackend.TerminalFailed || terminal.ReasonCode != "output_limit_exceeded" || terminal.OutputComplete {
		t.Fatalf("terminal = %+v, %v", terminal, err)
	}
	if signalPID != 99 || signalNumber != 9 {
		t.Fatalf("signal = pid %d signal %d", signalPID, signalNumber)
	}
}

func TestProviderRejectsMalformedProcessData(t *testing.T) {
	stream := &scriptedStream{events: []StreamEvent{
		{Name: "process.start", Data: map[string]any{"pid": 101}},
		{Name: "process.data", Data: map[string]any{"stdout": 42}},
	}}
	data := defaultFakeData()
	data.start = func(context.Context, string, StartProcessInput) (EventStream, error) { return stream, nil }
	provider := newTestProvider(t, defaultFakeControl(), data)
	exchange, err := provider.StartProcess(t.Context(), sandboxgateway.StartProcessProviderRequest{
		SessionRef: "tae-session-1", Request: validStartRequest(1024),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err := exchange.AwaitAcknowledgement(ctx); err != nil {
		t.Fatal(err)
	}
	terminal, err := exchange.AwaitTerminal(ctx)
	if err != nil || terminal.Status != executionbackend.TerminalUnknown ||
		terminal.ReasonCode != "provider_data_invalid" || terminal.OutputComplete {
		t.Fatalf("malformed data terminal = %+v, %v", terminal, err)
	}
}

func TestProviderReconnectsButDoesNotClaimCompleteOutput(t *testing.T) {
	initial := &scriptedStream{events: []StreamEvent{
		{Name: "process.start", Data: map[string]any{"pid": 7}},
		{Name: "process.data", Data: map[string]any{"stdout": "before\n"}},
	}}
	reconnected := &scriptedStream{events: []StreamEvent{
		{Name: "process.data", Data: map[string]any{"stdout": "after\n"}},
		{Name: "process.exit", Data: map[string]any{"exit_code": 0}},
	}}
	connectCalls := 0
	data := defaultFakeData()
	data.start = func(context.Context, string, StartProcessInput) (EventStream, error) { return initial, nil }
	data.connect = func(_ context.Context, _ string, pid int) (EventStream, error) {
		connectCalls++
		if pid != 7 {
			t.Fatalf("connect PID = %d", pid)
		}
		return reconnected, nil
	}
	provider := newTestProvider(t, defaultFakeControl(), data)
	exchange, err := provider.StartProcess(t.Context(), sandboxgateway.StartProcessProviderRequest{
		SessionRef: "tae-session-1", Request: validStartRequest(1024),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err := exchange.AwaitAcknowledgement(ctx); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := exchange.NextEvent(ctx); err != nil {
			t.Fatal(err)
		}
	}
	terminal, err := exchange.AwaitTerminal(ctx)
	if err != nil || terminal.Status != executionbackend.TerminalSucceeded || terminal.OutputComplete || terminal.ReasonCode != "stream_reconnected" {
		t.Fatalf("terminal = %+v, %v", terminal, err)
	}
	if connectCalls != 1 || !reconnected.IsClosed() {
		t.Fatalf("connect calls = %d, reconnected closed = %v", connectCalls, reconnected.IsClosed())
	}
}

func TestProviderReadFileUsesBoundedRegularFileRange(t *testing.T) {
	data := defaultFakeData()
	data.stat = func(_ context.Context, sessionID, path string) (FileInfo, string, error) {
		if sessionID != "tae-session-1" || path != "/workspace/result.txt" {
			t.Fatalf("stat = %q %q", sessionID, path)
		}
		return FileInfo{Type: "file", Size: 6}, "stat-log", nil
	}
	data.download = func(context.Context, string, string) (Download, error) {
		return Download{Body: io.NopCloser(strings.NewReader("abcdef")), ContentLength: 6, RequestID: "download-log"}, nil
	}
	provider := newTestProvider(t, defaultFakeControl(), data)
	request := validReadRequest()
	request.Offset, request.Limit = 2, 3
	exchange, err := provider.ReadFile(t.Context(), sandboxgateway.ReadFileProviderRequest{SessionRef: "tae-session-1", Request: request})
	if err != nil {
		t.Fatal(err)
	}
	ack, err := exchange.AwaitAcknowledgement(t.Context())
	if err != nil || ack.ProviderRequestID != "download-log" {
		t.Fatalf("ack = %+v, %v", ack, err)
	}
	event, err := exchange.NextEvent(t.Context())
	if err != nil || event.Kind != executionbackend.EventFileBytes || string(event.Data) != "cde" {
		t.Fatalf("event = %+v, %v", event, err)
	}
}

func TestProviderReadFileRejectsSymlinkBeforeDownload(t *testing.T) {
	data := defaultFakeData()
	data.stat = func(context.Context, string, string) (FileInfo, string, error) {
		return FileInfo{Type: "file", Size: 3, SymlinkTarget: "/etc/passwd"}, "stat-log", nil
	}
	data.download = func(context.Context, string, string) (Download, error) {
		t.Fatal("download must not be called for a symlink")
		return Download{}, nil
	}
	provider := newTestProvider(t, defaultFakeControl(), data)
	_, err := provider.ReadFile(t.Context(), sandboxgateway.ReadFileProviderRequest{SessionRef: "tae-session-1", Request: validReadRequest()})
	if executionbackend.OutcomeOf(err) != executionbackend.OutcomeRejected {
		t.Fatalf("ReadFile() error = %v", err)
	}
}

func TestProviderStartFailurePreservesSafeDispatchMetadata(t *testing.T) {
	data := defaultFakeData()
	data.start = func(context.Context, string, StartProcessInput) (EventStream, error) {
		return nil, &RequestError{
			WroteRequest: true, StatusCode: 403, Code: "forbidden",
			ProviderCode: "PermissionDenied", RequestID: "provider-log-start-1",
			Cause: errors.New("TAE returned a non-success response"),
		}
	}
	provider := newTestProvider(t, defaultFakeControl(), data)
	_, err := provider.StartProcess(t.Context(), sandboxgateway.StartProcessProviderRequest{
		SessionRef: "tae-session-1", Request: validStartRequest(1024),
	})
	var dispatchError *executionbackend.DispatchError
	if !errors.As(err, &dispatchError) || dispatchError.Outcome != executionbackend.OutcomeRejected ||
		dispatchError.Code != "forbidden" || dispatchError.ProviderCode != "PermissionDenied" ||
		dispatchError.ProviderRequestID != "provider-log-start-1" || dispatchError.HTTPStatus != 403 ||
		dispatchError.RequestWritten == nil || !*dispatchError.RequestWritten {
		t.Fatalf("StartProcess() dispatch error = %#v", err)
	}
}

func TestProviderPreAcknowledgementStreamFailurePreservesRequestID(t *testing.T) {
	streamError := &RequestError{
		WroteRequest: true, Code: "stream_lost", RequestID: "provider-stream-log-1",
		Cause: errors.New("TAE SSE stream read failed"),
	}
	stream := &scriptedStream{finalErr: streamError, requestID: "provider-stream-log-1"}
	data := defaultFakeData()
	data.start = func(context.Context, string, StartProcessInput) (EventStream, error) { return stream, nil }
	provider := newTestProvider(t, defaultFakeControl(), data)
	exchange, err := provider.StartProcess(t.Context(), sandboxgateway.StartProcessProviderRequest{
		SessionRef: "tae-session-1", Request: validStartRequest(1024),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = exchange.AwaitAcknowledgement(t.Context())
	var dispatchError *executionbackend.DispatchError
	if !errors.As(err, &dispatchError) || dispatchError.Outcome != executionbackend.OutcomeUnknown ||
		dispatchError.Code != "provider_stream_lost" || dispatchError.ProviderRequestID != "provider-stream-log-1" ||
		dispatchError.RequestWritten == nil || !*dispatchError.RequestWritten {
		t.Fatalf("AwaitAcknowledgement() dispatch error = %#v", err)
	}
}

func newTestProvider(t *testing.T, control ControlPlane, data DataPlane) *Provider {
	t.Helper()
	provider, err := NewProvider(Config{
		Control: control, Data: data, Region: "sg", PSM: "psm.agentserver.tae", Root: "/workspace",
		Now: func() time.Time { return testNow }, ReconnectDelay: 10 * time.Millisecond, Policy: validProviderPolicy(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func validProviderPolicy() taepolicy.Binding {
	policy := taepolicy.Binding{
		Version: taepolicy.BindingVersion, Region: "sg", SandboxPSM: "psm.agentserver.tae",
		Revision: "lark-readonly-v1", PolicySHA256: larkegresspolicy.SHA256Hex(),
		PublicHost: taepolicy.PublicHost, PublicAccess: taepolicy.PublicAccessWhitelist, PublicWebhookRequired: true,
		WebhookMode: "psm", WebhookPSM: "agentserver.egress-authorizer", WebhookPath: taepolicy.WebhookPath,
		Published: true, Approved: true, EvidenceRef: "tae-change/sg-2026-08-06",
	}
	policy.BindingSHA256 = policy.DigestHex()
	return policy
}

func defaultFakeControl() *fakeControlPlane {
	return &fakeControlPlane{
		create: func(_ context.Context, input CreateInput) (ControlSession, error) {
			return readyControlSession("tae-session-1", input.Metadata), nil
		},
		get: func(_ context.Context, sessionID string) (ControlSession, error) {
			return readyControlSession(sessionID, nil), nil
		},
		search: func(context.Context, SearchInput) (SearchResult, error) { return SearchResult{}, nil },
		update: func(context.Context, string, time.Duration) error { return nil },
		delete: func(context.Context, string) error { return nil },
	}
}

func defaultFakeData() *fakeDataPlane {
	return &fakeDataPlane{
		start: func(context.Context, string, StartProcessInput) (EventStream, error) {
			return nil, &RequestError{Code: "provider_unavailable", Cause: errors.New("unconfigured fake start")}
		},
		connect: func(context.Context, string, int) (EventStream, error) {
			return nil, &RequestError{Code: "provider_unavailable", Cause: errors.New("unconfigured fake connect")}
		},
		signal: func(context.Context, string, int, int) (string, error) { return "signal-log", nil },
		stat: func(context.Context, string, string) (FileInfo, string, error) {
			return FileInfo{}, "", &RequestError{Code: "provider_unavailable", Cause: errors.New("unconfigured fake stat")}
		},
		download: func(context.Context, string, string) (Download, error) {
			return Download{}, &RequestError{Code: "provider_unavailable", Cause: errors.New("unconfigured fake download")}
		},
	}
}

func readyControlSession(id string, metadata map[string]string) ControlSession {
	return ControlSession{
		ID: id, Status: "running", ExpiresAt: testNow.Add(time.Hour), SandboxdEnabled: true,
		Command: managedruntime.ExecutablePath, Metadata: cloneStrings(metadata),
	}
}

func validCreateRequest() sandboxgateway.CreateSandboxRequest {
	return sandboxgateway.CreateSandboxRequest{
		SandboxID: "sandbox-1", IdempotencyKey: "create-1", WorkspaceID: "workspace-1",
		SessionID: "session-1", EnvironmentID: "environment-1", Region: "sg", PSM: "psm.agentserver.tae",
		RuntimeProfileSHA256: strings.Repeat("a", 64), PackSetSHA256: strings.Repeat("b", 64), TTL: time.Hour,
	}
}

func validFindRequest() sandboxgateway.FindSandboxRequest {
	create := validCreateRequest()
	return sandboxgateway.FindSandboxRequest{
		SandboxID: create.SandboxID, IdempotencyKey: create.IdempotencyKey, WorkspaceID: create.WorkspaceID,
		SessionID: create.SessionID, EnvironmentID: create.EnvironmentID, Region: create.Region, PSM: create.PSM,
		RuntimeProfileSHA256: create.RuntimeProfileSHA256, PackSetSHA256: create.PackSetSHA256,
	}
}

func providerIdentityMetadataForTest(t *testing.T) map[string]string {
	t.Helper()
	provider := newTestProvider(t, defaultFakeControl(), defaultFakeData())
	request := validCreateRequest()
	return provider.createMetadata(request.SandboxID, request.IdempotencyKey, request.WorkspaceID, request.SessionID,
		request.EnvironmentID, request.RuntimeProfileSHA256, request.PackSetSHA256)
}

func validStartRequest(outputLimit int64) executionbackend.StartProcessRequest {
	return executionbackend.StartProcessRequest{
		Target: validTarget(), Operation: validOperation(), RequestID: "request-1", ProcessID: "process-1",
		Executable: "lark-cli", Arguments: []string{"doc", "get", "document-1"},
		WorkingDirectory: "/workspace", WorkspaceRoot: "/workspace", Platform: "linux-amd64",
		Environment: map[string]string{"LARK_TOKEN": "placeholder-only"}, Timeout: time.Second,
		OutputLimitBytes: outputLimit,
	}
}

func validReadRequest() executionbackend.ReadFileRequest {
	return executionbackend.ReadFileRequest{
		Target: validTarget(), Operation: validOperation(), RequestID: "request-read-1",
		Path: "/workspace/result.txt", Limit: 6,
	}
}

func validTarget() executionbackend.Target {
	return executionbackend.Target{Kind: executionbackend.KindTAE, ID: "sandbox-1", Generation: 1, EnvironmentID: "environment-1"}
}

func validOperation() executionbackend.OperationContext {
	return executionbackend.OperationContext{
		WorkspaceID: "workspace-1", SessionID: "session-1", RunID: "run-1", RunAttemptID: "attempt-1",
		RunAttemptGeneration: 1, ExecutionID: "execution-1", OperationID: "operation-1", MutationKey: "mutation-1",
	}
}

var _ ControlPlane = (*fakeControlPlane)(nil)
var _ DataPlane = (*fakeDataPlane)(nil)
var _ EventStream = (*scriptedStream)(nil)
