package sandboxgateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/executionbackend"
	"github.com/agentserver/agentserver/v2/internal/sandboxcontract"
	"github.com/agentserver/agentserver/v2/internal/sandboxgateway"
	"github.com/agentserver/agentserver/v2/internal/sandboxgateway/fakeprovider"
)

const (
	testWorkspaceID   = "10000000-0000-4000-8000-000000000001"
	testSessionID     = "20000000-0000-4000-8000-000000000001"
	testEnvironmentID = "30000000-0000-4000-8000-000000000001"
	testRunID         = "40000000-0000-4000-8000-000000000001"
	testAttemptID     = "50000000-0000-4000-8000-000000000001"
	testExecutionID   = "60000000-0000-4000-8000-000000000001"
	testOperationID   = "70000000-0000-4000-8000-000000000001"
	testMutationKey   = "80000000-0000-4000-8000-000000000001"
	testSandboxID     = "90000000-0000-4000-8000-000000000001"
	testCreateKey     = "a0000000-0000-4000-8000-000000000001"
)

func TestFakeProviderLarkCLIGoldenPathThroughHTTPHandler(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	core := newFakeCore(now)
	provider := fakeprovider.New(func() time.Time { return now }, nil)
	ids := &sequenceIDs{values: []string{
		testSandboxID, testCreateKey,
		"90000000-0000-4000-8000-000000000002", "a0000000-0000-4000-8000-000000000002",
	}}
	service, err := sandboxgateway.NewService(sandboxgateway.Config{
		Core: core, Provider: provider, Limits: sandboxcontract.DefaultLimits(),
		ProviderRegion: "sg", ProviderPSM: "toutiao.tae.sandbox",
		IdleTTL: 2 * time.Minute, EnsureTimeout: time.Second,
		EnsurePollInterval: time.Millisecond, Root: "/workspace", Platform: "linux-amd64",
		IDGenerator: ids.Next, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	authorizer := &testAuthorizer{lifecycle: sandboxgateway.Principal{
		Audience:    sandboxgateway.AudienceLifecycle,
		WorkspaceID: testWorkspaceID, SessionID: testSessionID, EnvironmentID: testEnvironmentID,
		RunID: testRunID, RunAttemptID: testAttemptID, RunAttemptGeneration: 1, HolderID: "pool-holder-1",
	}}
	handler, err := sandboxgateway.NewHandler(service, authorizer, 0)
	if err != nil {
		t.Fatal(err)
	}

	ensureRequest := sandboxcontract.EnsureSandboxRequest{
		Profile: sandboxcontract.ProfileV1, RequestID: "ensure-request-1",
		Session:             sandboxcontract.SessionIdentity{WorkspaceID: testWorkspaceID, SessionID: testSessionID, EnvironmentID: testEnvironmentID},
		RequestedTTLSeconds: 600, RuntimeProfileDigest: repeatHex("1"), PackSetDigest: repeatHex("2"),
	}
	ensure := serveJSON(t, handler, http.MethodPost, sandboxcontract.EnsureSandboxPath, ensureRequest)
	if ensure.Code != http.StatusOK {
		t.Fatalf("ensure status = %d body = %s", ensure.Code, ensure.Body.String())
	}
	var ensured sandboxcontract.EnsureSandboxResponse
	decodeResponse(t, ensure.Body.Bytes(), &ensured)
	if err := ensured.Validate(); err != nil {
		t.Fatalf("ensure response validation: %v", err)
	}
	if ensured.Sandbox.Ref.SandboxID != testSandboxID || ensured.Sandbox.Ref.TargetGeneration != 1 ||
		ensured.Sandbox.State != sandboxcontract.SandboxReady || provider.SessionCount() != 1 {
		t.Fatalf("ensured sandbox = %+v, provider sessions = %d", ensured.Sandbox, provider.SessionCount())
	}
	// A second ensure proposes different candidate identities, but Core returns
	// the active session-scoped generation and provider create remains one-shot.
	repeatEnsure := serveJSON(t, handler, http.MethodPost, sandboxcontract.EnsureSandboxPath, ensureRequest)
	if repeatEnsure.Code != http.StatusOK {
		t.Fatalf("repeat ensure status = %d body = %s", repeatEnsure.Code, repeatEnsure.Body.String())
	}
	var repeated sandboxcontract.EnsureSandboxResponse
	decodeResponse(t, repeatEnsure.Body.Bytes(), &repeated)
	if repeated.Sandbox.Ref != ensured.Sandbox.Ref || provider.SessionCount() != 1 {
		t.Fatalf("repeat ensure = %+v, provider sessions = %d", repeated, provider.SessionCount())
	}

	authorizer.mu.Lock()
	authorizer.lifecycle.SandboxID = testSandboxID
	authorizer.lifecycle.TargetGeneration = 1
	authorizer.mu.Unlock()
	renewPath, err := sandboxcontract.RenewSandboxActivityPath(testSandboxID)
	if err != nil {
		t.Fatal(err)
	}
	renew := serveJSON(t, handler, http.MethodPost, renewPath, sandboxcontract.RenewSandboxActivityRequest{
		Profile: sandboxcontract.ProfileV1, RequestID: "renew-request-1", Session: ensureRequest.Session,
		Ref: ensured.Sandbox.Ref, RunAttemptID: testAttemptID, RunAttemptGeneration: 1, ActivityTTLSeconds: 120,
	})
	if renew.Code != http.StatusOK {
		t.Fatalf("renew status = %d body = %s", renew.Code, renew.Body.String())
	}

	identity := sandboxcontract.OperationIdentity{
		Session: ensureRequest.Session, RunID: testRunID, RunAttemptID: testAttemptID,
		RunAttemptGeneration: 1, ExecutionID: testExecutionID,
		OperationID: testOperationID, MutationKey: testMutationKey,
	}
	core.allow = corecontract.AuthorizeManagedSandboxOperationRequest{
		WorkspaceID: testWorkspaceID, SessionID: testSessionID, RunID: testRunID,
		RunAttemptID: testAttemptID, RunAttemptGeneration: 1,
		ExecutionID: testExecutionID, OperationID: testOperationID, MutationKey: testMutationKey,
		SandboxID: testSandboxID, TargetGeneration: 1, EnvironmentID: testEnvironmentID,
		Action: corecontract.ManagedSandboxActionRunCommand,
	}
	authorizer.mu.Lock()
	authorizer.backend = sandboxgateway.Principal{
		Audience:    sandboxgateway.AudienceBackend,
		WorkspaceID: testWorkspaceID, SessionID: testSessionID, EnvironmentID: testEnvironmentID,
		RunID: testRunID, RunAttemptID: testAttemptID, RunAttemptGeneration: 1,
		ExecutionID: testExecutionID, OperationID: testOperationID, MutationKey: testMutationKey,
		SandboxID: testSandboxID, TargetGeneration: 1,
	}
	authorizer.mu.Unlock()
	runPath, err := sandboxcontract.RunCommandPath(testSandboxID)
	if err != nil {
		t.Fatal(err)
	}
	run := serveJSON(t, handler, http.MethodPost, runPath, sandboxcontract.RunCommandRequest{
		Profile: sandboxcontract.ProfileV1, RequestID: "run-command-request-1",
		Identity: identity, Ref: ensured.Sandbox.Ref, ProcessID: "lark-process-1",
		Executable: "lark-cli", Arguments: []string{"doc", "get", "fake-token"},
		WorkingDirectory: "/workspace", Environment: map[string]string{"LARK_AUTHORIZATION": "Bearer placeholder-not-a-real-token"},
		TimeoutMillis: 30_000, OutputLimitBytes: 64 * 1024,
	})
	if run.Code != http.StatusOK || run.Header().Get("Content-Type") != "application/x-ndjson" {
		t.Fatalf("run status = %d content-type = %q body = %s", run.Code, run.Header().Get("Content-Type"), run.Body.String())
	}
	frames := decodeFrames(t, run.Body.Bytes())
	if len(frames) != 3 || frames[0].Type != sandboxcontract.OperationFrameAcknowledgement ||
		frames[1].Type != sandboxcontract.OperationFrameEvent || frames[2].Type != sandboxcontract.OperationFrameTerminal {
		t.Fatalf("operation frames = %+v", frames)
	}
	if !bytes.Contains(frames[1].Event.Data, []byte(`"source":"fake-lark"`)) ||
		frames[2].Terminal.Status != "succeeded" || !frames[2].Terminal.OutputComplete {
		t.Fatalf("lark output frame = %+v terminal = %+v", frames[1], frames[2])
	}
	if core.authorizeCalls != 1 {
		t.Fatalf("Core authorize calls = %d, want one", core.authorizeCalls)
	}

	// A body identity that differs from the single-operation capability is
	// denied before a second Core introspection or provider call.
	badRequest := sandboxcontract.RunCommandRequest{
		Profile: sandboxcontract.ProfileV1, RequestID: "run-command-request-2",
		Identity: identity, Ref: ensured.Sandbox.Ref, ProcessID: "lark-process-2",
		Executable: "lark-cli", Arguments: []string{"doc", "get"}, WorkingDirectory: "/workspace",
		TimeoutMillis: 30_000, OutputLimitBytes: 1024,
	}
	badRequest.Identity.MutationKey = "80000000-0000-4000-8000-000000000002"
	denied := serveJSON(t, handler, http.MethodPost, runPath, badRequest)
	if denied.Code != http.StatusForbidden || core.authorizeCalls != 1 {
		t.Fatalf("mismatched capability status = %d, authorize calls = %d, body = %s", denied.Code, core.authorizeCalls, denied.Body.String())
	}
}

func TestHandlerRejectsUnknownJSONBeforeService(t *testing.T) {
	now := time.Now().UTC()
	core := newFakeCore(now)
	provider := fakeprovider.New(func() time.Time { return now }, nil)
	service, err := sandboxgateway.NewService(sandboxgateway.Config{
		Core: core, Provider: provider, Limits: sandboxcontract.DefaultLimits(),
		ProviderRegion: "sg", ProviderPSM: "toutiao.tae.sandbox", IdleTTL: time.Minute,
		EnsureTimeout: time.Second, EnsurePollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := sandboxgateway.NewHandler(service, &testAuthorizer{lifecycle: sandboxgateway.Principal{
		Audience:    sandboxgateway.AudienceLifecycle,
		WorkspaceID: testWorkspaceID, SessionID: testSessionID, EnvironmentID: testEnvironmentID,
	}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"profile":"e2b-semantic-subset/v1","requestId":"request-1","session":{"workspaceId":"10000000-0000-4000-8000-000000000001","sessionId":"20000000-0000-4000-8000-000000000001","environmentId":"30000000-0000-4000-8000-000000000001"},"requestedTtlSeconds":600,"runtimeProfileDigest":"` + repeatHex("1") + `","packSetDigest":"` + repeatHex("2") + `","future":true}`)
	request := httptest.NewRequest(http.MethodPost, sandboxcontract.EnsureSandboxPath, bytes.NewReader(raw))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || provider.SessionCount() != 0 {
		t.Fatalf("unknown JSON status = %d sessions = %d body = %s", response.Code, provider.SessionCount(), response.Body.String())
	}
}

func TestWorkspaceAllowlistDeniesBeforeCoreOrProviderCalls(t *testing.T) {
	now := time.Now().UTC()
	core := newFakeCore(now)
	provider := fakeprovider.New(func() time.Time { return now }, nil)
	service, err := sandboxgateway.NewService(sandboxgateway.Config{
		Core: core, Provider: provider, Limits: sandboxcontract.DefaultLimits(),
		ProviderRegion: "sg", ProviderPSM: "toutiao.tae.sandbox", IdleTTL: time.Minute,
		EnsureTimeout: time.Second, EnsurePollInterval: time.Millisecond,
		WorkspaceAllowlist: []string{"10000000-0000-4000-8000-000000000099"},
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := sandboxgateway.NewHandler(service, &testAuthorizer{lifecycle: sandboxgateway.Principal{
		Audience: sandboxgateway.AudienceLifecycle, WorkspaceID: testWorkspaceID,
		SessionID: testSessionID, EnvironmentID: testEnvironmentID,
	}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	response := serveJSON(t, handler, http.MethodPost, sandboxcontract.EnsureSandboxPath, sandboxcontract.EnsureSandboxRequest{
		Profile: sandboxcontract.ProfileV1, RequestID: "allowlist-denial",
		Session: sandboxcontract.SessionIdentity{
			WorkspaceID: testWorkspaceID, SessionID: testSessionID, EnvironmentID: testEnvironmentID,
		},
		RequestedTTLSeconds: 600, RuntimeProfileDigest: repeatHex("1"), PackSetDigest: repeatHex("2"),
	})
	if response.Code != http.StatusForbidden || core.reserveCalls != 0 || core.authorizeCalls != 0 || provider.SessionCount() != 0 {
		t.Fatalf(
			"allowlist denial status/core reserve/core authorize/provider = %d/%d/%d/%d body=%s",
			response.Code, core.reserveCalls, core.authorizeCalls, provider.SessionCount(), response.Body.String(),
		)
	}
}

func TestHandlerReturnsAndLogsOnlySafeProviderDispatchMetadata(t *testing.T) {
	const (
		secretArgument = "secret-command-argument"
		secretToken    = "secret-lark-token"
		secretCause    = "secret-provider-response-body"
	)
	now := time.Now().UTC()
	core := newFakeCore(now)
	provider := &dispatchFailureProvider{err: func() error {
		dispatchError := executionbackend.NewDispatchError(
			executionbackend.OutcomeRejected, "forbidden", errors.New(secretCause),
		)
		dispatchError.ProviderRequestID = "provider-log-1"
		dispatchError.ProviderCode = "PermissionDenied"
		dispatchError.HTTPStatus = http.StatusForbidden
		requestWritten := true
		dispatchError.RequestWritten = &requestWritten
		return dispatchError
	}()}
	service, err := sandboxgateway.NewService(sandboxgateway.Config{
		Core: core, Provider: provider, Limits: sandboxcontract.DefaultLimits(),
		ProviderRegion: "sg", ProviderPSM: "toutiao.tae.sandbox", IdleTTL: time.Minute,
		EnsureTimeout: time.Second, EnsurePollInterval: time.Millisecond,
		Root: "/workspace", Platform: "linux-amd64",
	})
	if err != nil {
		t.Fatal(err)
	}
	expiresAt := now.Add(time.Hour)
	core.state = &corecontract.ManagedSandboxState{
		SandboxID: testSandboxID, WorkspaceID: testWorkspaceID, SessionID: testSessionID,
		EnvironmentID: testEnvironmentID, ProviderKind: "tae", Generation: 1,
		DesiredState: "ready", ObservedState: "ready", ProviderRegion: "sg",
		ProviderPSM: "toutiao.tae.sandbox", ProviderSessionRef: "tae-session-1",
		ExpiresAt: &expiresAt, Version: 1, CreatedAt: now, UpdatedAt: now,
	}
	core.activityLive = true
	identity := sandboxcontract.OperationIdentity{
		Session: sandboxcontract.SessionIdentity{WorkspaceID: testWorkspaceID, SessionID: testSessionID, EnvironmentID: testEnvironmentID},
		RunID:   testRunID, RunAttemptID: testAttemptID, RunAttemptGeneration: 1,
		ExecutionID: testExecutionID, OperationID: testOperationID, MutationKey: testMutationKey,
	}
	core.allow = corecontract.AuthorizeManagedSandboxOperationRequest{
		WorkspaceID: testWorkspaceID, SessionID: testSessionID, RunID: testRunID,
		RunAttemptID: testAttemptID, RunAttemptGeneration: 1,
		ExecutionID: testExecutionID, OperationID: testOperationID, MutationKey: testMutationKey,
		SandboxID: testSandboxID, TargetGeneration: 1, EnvironmentID: testEnvironmentID,
		Action: corecontract.ManagedSandboxActionRunCommand,
	}
	authorizer := &testAuthorizer{backend: sandboxgateway.Principal{
		Audience: sandboxgateway.AudienceBackend, WorkspaceID: testWorkspaceID, SessionID: testSessionID,
		EnvironmentID: testEnvironmentID, RunID: testRunID, RunAttemptID: testAttemptID,
		RunAttemptGeneration: 1, ExecutionID: testExecutionID, OperationID: testOperationID,
		MutationKey: testMutationKey, SandboxID: testSandboxID, TargetGeneration: 1,
	}}
	var logs bytes.Buffer
	handler, err := sandboxgateway.NewHandlerWithLogger(service, authorizer, 0, slog.New(slog.NewJSONHandler(&logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	path, err := sandboxcontract.RunCommandPath(testSandboxID)
	if err != nil {
		t.Fatal(err)
	}
	response := serveJSON(t, handler, http.MethodPost, path, sandboxcontract.RunCommandRequest{
		Profile: sandboxcontract.ProfileV1, RequestID: "request-provider-failure", Identity: identity,
		Ref: sandboxcontract.SandboxRef{SandboxID: testSandboxID, TargetGeneration: 1}, ProcessID: "process-provider-failure",
		Executable: "lark-cli", Arguments: []string{secretArgument}, WorkingDirectory: "/workspace",
		Environment: map[string]string{"LARK_ACCESS_TOKEN": secretToken}, TimeoutMillis: 30_000, OutputLimitBytes: 1024,
	})
	if response.Code != http.StatusConflict {
		t.Fatalf("dispatch failure status = %d body = %s", response.Code, response.Body.String())
	}
	var contractError sandboxcontract.ErrorResponse
	decodeResponse(t, response.Body.Bytes(), &contractError)
	if contractError.Code != "forbidden" || contractError.Outcome != string(executionbackend.OutcomeRejected) ||
		contractError.ProviderRequestID != "provider-log-1" || contractError.ProviderCode != "PermissionDenied" ||
		contractError.ProviderHTTPStatus != http.StatusForbidden || contractError.RequestWritten == nil || !*contractError.RequestWritten {
		t.Fatalf("dispatch error response = %+v", contractError)
	}
	combined := response.Body.String() + logs.String()
	for _, forbidden := range []string{secretArgument, secretToken, secretCause, "Authorization"} {
		if strings.Contains(combined, forbidden) {
			t.Fatalf("dispatch diagnostics leaked %q: %s", forbidden, combined)
		}
	}
	for _, wanted := range []string{"sandbox provider dispatch failed", "provider-log-1", "PermissionDenied", `"provider_http_status":403`, `"request_written":true`} {
		if !strings.Contains(logs.String(), wanted) {
			t.Fatalf("dispatch log %q does not contain %q", logs.String(), wanted)
		}
	}
}

type dispatchFailureProvider struct {
	err error
}

func (*dispatchFailureProvider) CreateSandbox(context.Context, sandboxgateway.CreateSandboxRequest) (sandboxgateway.ProviderSandbox, error) {
	return sandboxgateway.ProviderSandbox{}, errors.New("unexpected create")
}

func (*dispatchFailureProvider) FindSandbox(context.Context, sandboxgateway.FindSandboxRequest) (sandboxgateway.ProviderSandbox, error) {
	return sandboxgateway.ProviderSandbox{}, errors.New("unexpected find")
}

func (*dispatchFailureProvider) GetSandbox(context.Context, string) (sandboxgateway.ProviderSandbox, error) {
	return sandboxgateway.ProviderSandbox{}, errors.New("unexpected get")
}

func (*dispatchFailureProvider) SetSandboxTimeout(context.Context, sandboxgateway.SetSandboxTimeoutProviderRequest) (sandboxgateway.ProviderSandbox, error) {
	return sandboxgateway.ProviderSandbox{}, errors.New("unexpected set timeout")
}

func (*dispatchFailureProvider) DeleteSandbox(context.Context, sandboxgateway.DeleteSandboxProviderRequest) error {
	return errors.New("unexpected delete")
}

func (provider *dispatchFailureProvider) StartProcess(context.Context, sandboxgateway.StartProcessProviderRequest) (executionbackend.Exchange, error) {
	return nil, provider.err
}

func (provider *dispatchFailureProvider) SignalProcess(context.Context, sandboxgateway.SignalProcessProviderRequest) (executionbackend.Exchange, error) {
	return nil, provider.err
}

func (provider *dispatchFailureProvider) ReadFile(context.Context, sandboxgateway.ReadFileProviderRequest) (executionbackend.Exchange, error) {
	return nil, provider.err
}

type sequenceIDs struct {
	mu     sync.Mutex
	values []string
	next   int
}

func (ids *sequenceIDs) Next() (string, error) {
	ids.mu.Lock()
	defer ids.mu.Unlock()
	if ids.next >= len(ids.values) {
		return "", errors.New("test ID sequence exhausted")
	}
	value := ids.values[ids.next]
	ids.next++
	return value, nil
}

type testAuthorizer struct {
	mu        sync.Mutex
	lifecycle sandboxgateway.Principal
	backend   sandboxgateway.Principal
	err       error
}

func (authorizer *testAuthorizer) Authorize(_ *http.Request, action string) (sandboxgateway.Principal, error) {
	authorizer.mu.Lock()
	defer authorizer.mu.Unlock()
	if authorizer.err != nil {
		return sandboxgateway.Principal{}, authorizer.err
	}
	switch action {
	case sandboxgateway.ActionRunCommand, sandboxgateway.ActionSignalCommand, sandboxgateway.ActionReadFile:
		return authorizer.backend, nil
	default:
		return authorizer.lifecycle, nil
	}
}

type fakeCore struct {
	mu             sync.Mutex
	now            time.Time
	state          *corecontract.ManagedSandboxState
	activityLive   bool
	allow          corecontract.AuthorizeManagedSandboxOperationRequest
	authorizeCalls int
	reserveCalls   int
}

func newFakeCore(now time.Time) *fakeCore { return &fakeCore{now: now} }

func (core *fakeCore) ReserveManagedSandbox(_ context.Context, request corecontract.ReserveManagedSandboxRequest) (corecontract.ReserveManagedSandboxResponse, error) {
	core.mu.Lock()
	defer core.mu.Unlock()
	core.reserveCalls++
	if core.state != nil && core.state.ObservedState != "deleted" {
		return corecontract.ReserveManagedSandboxResponse{Sandbox: *core.state, Created: false}, nil
	}
	generation := int64(1)
	if core.state != nil {
		generation = core.state.Generation + 1
	}
	state := corecontract.ManagedSandboxState{
		SandboxID: request.SandboxID, WorkspaceID: request.WorkspaceID,
		SessionID: request.SessionID, EnvironmentID: request.EnvironmentID,
		ProviderKind: "tae", Generation: generation, DesiredState: "ready", ObservedState: "reserved",
		ProviderRegion: request.ProviderRegion, ProviderPSM: request.ProviderPSM,
		ProviderSessionRef: request.ProviderSessionRef, CreateIdempotencyKey: request.CreateIdempotencyKey,
		RuntimeProfileSHA256: request.RuntimeProfileSHA256, PackSetSHA256: request.PackSetSHA256,
		RequestedTTLSeconds: request.RequestedTTLSeconds, IdleTTLSeconds: request.IdleTTLSeconds,
		Version: 1, CreatedAt: core.now, UpdatedAt: core.now,
	}
	core.state = &state
	return corecontract.ReserveManagedSandboxResponse{Sandbox: state, Created: true}, nil
}

func (core *fakeCore) GetManagedSandbox(_ context.Context, sandboxID string, generation int64) (corecontract.GetManagedSandboxResponse, error) {
	core.mu.Lock()
	defer core.mu.Unlock()
	if core.state == nil || core.state.SandboxID != sandboxID || core.state.Generation != generation {
		return corecontract.GetManagedSandboxResponse{}, errors.New("sandbox not found")
	}
	return corecontract.GetManagedSandboxResponse{Sandbox: *core.state}, nil
}

func (core *fakeCore) BeginManagedSandboxCreate(_ context.Context, request corecontract.BeginManagedSandboxCreateRequest) (corecontract.ManagedSandboxMutationResponse, error) {
	core.mu.Lock()
	defer core.mu.Unlock()
	if err := core.matchVersion(request.SandboxID, request.Generation, request.ExpectedVersion); err != nil {
		if core.state != nil && (core.state.ObservedState == "creating" || core.state.ObservedState == "ready") {
			return corecontract.ManagedSandboxMutationResponse{Sandbox: *core.state, Changed: false}, nil
		}
		return corecontract.ManagedSandboxMutationResponse{}, err
	}
	core.state.ObservedState = "creating"
	core.bump()
	return corecontract.ManagedSandboxMutationResponse{Sandbox: *core.state, Changed: true}, nil
}

func (core *fakeCore) ObserveManagedSandbox(_ context.Context, request corecontract.ObserveManagedSandboxRequest) (corecontract.ManagedSandboxMutationResponse, error) {
	core.mu.Lock()
	defer core.mu.Unlock()
	if core.state != nil && core.state.ObservedState == request.ObservedState &&
		(request.ProviderSessionRef == "" || core.state.ProviderSessionRef == request.ProviderSessionRef) {
		return corecontract.ManagedSandboxMutationResponse{Sandbox: *core.state, Changed: false}, nil
	}
	if err := core.matchVersion(request.SandboxID, request.Generation, request.ExpectedVersion); err != nil {
		return corecontract.ManagedSandboxMutationResponse{}, err
	}
	core.state.ObservedState = request.ObservedState
	if request.ProviderSessionRef != "" {
		core.state.ProviderSessionRef = request.ProviderSessionRef
	}
	if request.ExpiresAt != nil {
		core.state.ExpiresAt = request.ExpiresAt
	}
	core.state.LastErrorCode = request.ErrorCode
	core.state.LastErrorSHA256 = request.ErrorSHA256
	if request.ObservedState == "deleted" {
		core.state.DesiredState = "deleted"
		deleted := core.now
		core.state.DeletedAt = &deleted
	}
	core.bump()
	return corecontract.ManagedSandboxMutationResponse{Sandbox: *core.state, Changed: true}, nil
}

func (core *fakeCore) RenewManagedSandboxActivity(_ context.Context, request corecontract.RenewManagedSandboxActivityRequest) (corecontract.ManagedSandboxMutationResponse, error) {
	core.mu.Lock()
	defer core.mu.Unlock()
	if core.state == nil || request.SandboxID != core.state.SandboxID || request.Generation != core.state.Generation || core.state.ObservedState != "ready" {
		return corecontract.ManagedSandboxMutationResponse{}, errors.New("sandbox not ready")
	}
	core.activityLive = true
	return corecontract.ManagedSandboxMutationResponse{Sandbox: *core.state, Changed: true}, nil
}

func (core *fakeCore) ReleaseManagedSandboxActivity(_ context.Context, request corecontract.ReleaseManagedSandboxActivityRequest) (corecontract.ManagedSandboxMutationResponse, error) {
	core.mu.Lock()
	defer core.mu.Unlock()
	changed := core.activityLive
	core.activityLive = false
	return corecontract.ManagedSandboxMutationResponse{Sandbox: *core.state, Changed: changed}, nil
}

func (core *fakeCore) BeginManagedSandboxDelete(_ context.Context, request corecontract.BeginManagedSandboxDeleteRequest) (corecontract.ManagedSandboxMutationResponse, error) {
	core.mu.Lock()
	defer core.mu.Unlock()
	if err := core.matchVersion(request.SandboxID, request.Generation, request.ExpectedVersion); err != nil {
		return corecontract.ManagedSandboxMutationResponse{}, err
	}
	core.state.DesiredState = "deleted"
	core.state.ObservedState = "deleting"
	core.activityLive = false
	core.bump()
	return corecontract.ManagedSandboxMutationResponse{Sandbox: *core.state, Changed: true}, nil
}

func (core *fakeCore) ListManagedSandboxesForReconcile(context.Context, corecontract.ListManagedSandboxesForReconcileRequest) (corecontract.ListManagedSandboxesForReconcileResponse, error) {
	core.mu.Lock()
	defer core.mu.Unlock()
	if core.state == nil {
		return corecontract.ListManagedSandboxesForReconcileResponse{Sandboxes: []corecontract.ManagedSandboxState{}}, nil
	}
	return corecontract.ListManagedSandboxesForReconcileResponse{Sandboxes: []corecontract.ManagedSandboxState{*core.state}}, nil
}

func (core *fakeCore) AuthorizeManagedSandboxOperation(_ context.Context, request corecontract.AuthorizeManagedSandboxOperationRequest) (corecontract.AuthorizeManagedSandboxOperationResponse, error) {
	core.mu.Lock()
	defer core.mu.Unlock()
	core.authorizeCalls++
	if !core.activityLive || !reflect.DeepEqual(request, core.allow) {
		return corecontract.AuthorizeManagedSandboxOperationResponse{}, errors.New("operation not authorized")
	}
	return corecontract.AuthorizeManagedSandboxOperationResponse{
		SandboxID: request.SandboxID, TargetGeneration: request.TargetGeneration,
		OperationID: request.OperationID, OperationKind: "process_start", AuthorizedAt: core.now,
	}, nil
}

func (core *fakeCore) matchVersion(sandboxID string, generation, version int64) error {
	if core.state == nil || core.state.SandboxID != sandboxID || core.state.Generation != generation || core.state.Version != version {
		return errors.New("sandbox version conflict")
	}
	return nil
}

func (core *fakeCore) bump() {
	core.state.Version++
	core.state.UpdatedAt = core.now
}

func serveJSON(t *testing.T, handler http.Handler, method, path string, value any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(raw))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func decodeResponse(t *testing.T, raw []byte, destination any) {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		t.Fatal(err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		t.Fatalf("response contains trailing JSON: %v", err)
	}
}

func decodeFrames(t *testing.T, raw []byte) []sandboxcontract.OperationFrame {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var frames []sandboxcontract.OperationFrame
	for {
		var frame sandboxcontract.OperationFrame
		if err := decoder.Decode(&frame); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		if err := frame.Validate(); err != nil {
			t.Fatalf("invalid operation frame: %v", err)
		}
		frames = append(frames, frame)
	}
	return frames
}

func repeatHex(character string) string {
	return character + character + character + character + character + character + character + character +
		character + character + character + character + character + character + character + character +
		character + character + character + character + character + character + character + character +
		character + character + character + character + character + character + character + character +
		character + character + character + character + character + character + character + character +
		character + character + character + character + character + character + character + character +
		character + character + character + character + character + character + character + character +
		character + character + character + character + character + character + character + character
}

var _ sandboxgateway.Core = (*fakeCore)(nil)
var _ sandboxgateway.Authorizer = (*testAuthorizer)(nil)
