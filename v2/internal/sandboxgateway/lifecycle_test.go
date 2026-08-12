package sandboxgateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/executionbackend"
	"github.com/agentserver/agentserver/v2/internal/sandboxcontract"
	"github.com/agentserver/agentserver/v2/internal/sandboxgateway"
)

// scriptedLifecycleProvider is intentionally small: lifecycle tests must be
// able to model provider observations independently from the fake provider's
// happy-path in-memory session store. Data-plane methods are not exercised by
// these tests and fail closed if a test accidentally reaches them.
type scriptedLifecycleProvider struct {
	mu sync.Mutex

	createResult sandboxgateway.ProviderSandbox
	createErr    error
	createCalls  int

	getResults []sandboxgateway.ProviderSandbox
	getErrors  []error
	getCalls   int

	findResult sandboxgateway.ProviderSandbox
	findErr    error

	deleteErrors []error
	deleteCalls  []sandboxgateway.DeleteSandboxProviderRequest
}

type deadlineReadyCore struct {
	*fakeCore
	readyResult sandboxgateway.ProviderSandbox
}

func (core *deadlineReadyCore) ObserveManagedSandbox(ctx context.Context, request corecontract.ObserveManagedSandboxRequest) (corecontract.ManagedSandboxMutationResponse, error) {
	if request.ObservedState == "failed" && request.ErrorCode == "provider_ready_timeout" {
		core.mu.Lock()
		if core.state != nil {
			core.state.ObservedState = "ready"
			core.state.ProviderSessionRef = core.readyResult.SessionRef
			core.state.ExpiresAt = ptrTime(core.readyResult.ExpiresAt)
			core.bump()
		}
		core.mu.Unlock()
		return corecontract.ManagedSandboxMutationResponse{}, errors.New("sandbox version conflict")
	}
	return core.fakeCore.ObserveManagedSandbox(ctx, request)
}

func (provider *scriptedLifecycleProvider) CreateSandbox(context.Context, sandboxgateway.CreateSandboxRequest) (sandboxgateway.ProviderSandbox, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	provider.createCalls++
	return provider.createResult, provider.createErr
}

func (provider *scriptedLifecycleProvider) FindSandbox(context.Context, sandboxgateway.FindSandboxRequest) (sandboxgateway.ProviderSandbox, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return provider.findResult, provider.findErr
}

func (provider *scriptedLifecycleProvider) GetSandbox(context.Context, string) (sandboxgateway.ProviderSandbox, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	provider.getCalls++
	if len(provider.getResults) == 0 {
		if len(provider.getErrors) != 0 {
			return sandboxgateway.ProviderSandbox{}, provider.getErrors[len(provider.getErrors)-1]
		}
		return sandboxgateway.ProviderSandbox{}, sandboxgateway.ErrProviderSandboxNotFound
	}
	index := provider.getCalls - 1
	if index >= len(provider.getResults) {
		index = len(provider.getResults) - 1
	}
	if len(provider.getErrors) != 0 {
		errorIndex := provider.getCalls - 1
		if errorIndex >= len(provider.getErrors) {
			errorIndex = len(provider.getErrors) - 1
		}
		if provider.getErrors[errorIndex] != nil {
			return sandboxgateway.ProviderSandbox{}, provider.getErrors[errorIndex]
		}
	}
	return provider.getResults[index], nil
}

func (provider *scriptedLifecycleProvider) SetSandboxTimeout(context.Context, sandboxgateway.SetSandboxTimeoutProviderRequest) (sandboxgateway.ProviderSandbox, error) {
	return sandboxgateway.ProviderSandbox{}, errors.New("timeout update is not part of lifecycle test")
}

func (provider *scriptedLifecycleProvider) DeleteSandbox(_ context.Context, request sandboxgateway.DeleteSandboxProviderRequest) error {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	provider.deleteCalls = append(provider.deleteCalls, request)
	if len(provider.deleteErrors) == 0 {
		return nil
	}
	index := len(provider.deleteCalls) - 1
	if index >= len(provider.deleteErrors) {
		index = len(provider.deleteErrors) - 1
	}
	return provider.deleteErrors[index]
}

func (provider *scriptedLifecycleProvider) StartProcess(context.Context, sandboxgateway.StartProcessProviderRequest) (executionbackend.Exchange, error) {
	return nil, errors.New("process start is not part of lifecycle test")
}

func (provider *scriptedLifecycleProvider) SignalProcess(context.Context, sandboxgateway.SignalProcessProviderRequest) (executionbackend.Exchange, error) {
	return nil, errors.New("process signal is not part of lifecycle test")
}

func (provider *scriptedLifecycleProvider) ReadFile(context.Context, sandboxgateway.ReadFileProviderRequest) (executionbackend.Exchange, error) {
	return nil, errors.New("file read is not part of lifecycle test")
}

func TestManagedSandboxEnsureAdoptsAsyncCreatingSessionThenConvergesReady(t *testing.T) {
	now := time.Date(2026, 8, 6, 20, 0, 0, 0, time.UTC)
	core := newFakeCore(now)
	provider := &scriptedLifecycleProvider{
		createResult: sandboxgateway.ProviderSandbox{SessionRef: "tae-async-1", State: sandboxgateway.ProviderSandboxCreating},
		getResults: []sandboxgateway.ProviderSandbox{
			{SessionRef: "tae-async-1", State: sandboxgateway.ProviderSandboxCreating},
			{SessionRef: "tae-async-1", State: sandboxgateway.ProviderSandboxReady, Root: "/workspace", ExpiresAt: now.Add(time.Hour)},
		},
	}
	service := newLifecycleService(t, core, provider, now)

	response, err := service.EnsureSandbox(t.Context(), lifecyclePrincipal(), lifecycleEnsureRequest())
	if err != nil {
		t.Fatalf("EnsureSandbox() error = %v", err)
	}
	if response.Sandbox.State != sandboxcontract.SandboxReady || response.Sandbox.Root != "/workspace" {
		t.Fatalf("EnsureSandbox() response = %+v", response.Sandbox)
	}
	core.mu.Lock()
	state := *core.state
	core.mu.Unlock()
	if state.ObservedState != "ready" || state.ProviderSessionRef != "tae-async-1" {
		t.Fatalf("Core state = %+v", state)
	}
	provider.mu.Lock()
	createCalls, getCalls := provider.createCalls, provider.getCalls
	provider.mu.Unlock()
	if createCalls != 1 || getCalls < 2 {
		t.Fatalf("provider calls create/get = %d/%d, want 1/at least 2", createCalls, getCalls)
	}
}

func TestManagedSandboxReconcileProviderTerminalStatesFailsAndDeletes(t *testing.T) {
	cases := map[string]sandboxgateway.ProviderSandboxState{
		"expired-ready": sandboxgateway.ProviderSandboxReady,
		"deleted":       sandboxgateway.ProviderSandboxDeleted,
		"failed":        sandboxgateway.ProviderSandboxFailed,
	}
	for name, terminal := range cases {
		t.Run(name, func(t *testing.T) {
			now := time.Date(2026, 8, 6, 20, 0, 0, 0, time.UTC)
			core := newFakeCore(now)
			providerState := sandboxgateway.ProviderSandbox{SessionRef: "tae-terminal-1", State: terminal}
			if terminal == sandboxgateway.ProviderSandboxReady {
				providerState.Root = "/workspace"
				providerState.ExpiresAt = now.Add(-time.Minute)
			}
			provider := &scriptedLifecycleProvider{
				createResult: sandboxgateway.ProviderSandbox{
					SessionRef: "tae-terminal-1", State: sandboxgateway.ProviderSandboxReady,
					Root: "/workspace", ExpiresAt: now.Add(time.Hour),
				},
				getResults: []sandboxgateway.ProviderSandbox{providerState},
			}
			service := newLifecycleService(t, core, provider, now)
			if _, err := service.EnsureSandbox(t.Context(), lifecyclePrincipal(), lifecycleEnsureRequest()); err != nil {
				t.Fatalf("initial EnsureSandbox() error = %v", err)
			}
			core.mu.Lock()
			// Reconciliation only sees rows selected by Core. The provider state
			// is changed after the initial ready observation to model a session
			// expiring or disappearing between two control-plane polls.
			core.state.UpdatedAt = now
			core.state.ExpiresAt = ptrTime(now.Add(time.Hour))
			core.state.Version++
			core.mu.Unlock()
			provider.mu.Lock()
			provider.getResults = []sandboxgateway.ProviderSandbox{providerState}
			provider.mu.Unlock()

			// For the terminal cases above, seed a ready Core row explicitly when
			// the initial scripted observation was not ready.
			core.mu.Lock()
			core.state.ObservedState = "ready"
			core.state.ProviderSessionRef = "tae-terminal-1"
			core.state.ExpiresAt = ptrTime(now.Add(time.Hour))
			core.state.Version++
			core.mu.Unlock()

			report, err := service.ReconcileOnce(t.Context(), 10)
			if err != nil {
				t.Fatalf("ReconcileOnce() error = %v (report %+v)", err, report)
			}
			core.mu.Lock()
			final := *core.state
			core.mu.Unlock()
			if final.ObservedState != "deleted" || final.DesiredState != "deleted" {
				t.Fatalf("final Core state = %+v", final)
			}
			provider.mu.Lock()
			deleteCalls := len(provider.deleteCalls)
			provider.mu.Unlock()
			if deleteCalls != 1 {
				t.Fatalf("provider delete calls = %d, want one", deleteCalls)
			}
		})
	}
}

func TestManagedSandboxReconcileRetriesPartialDeleteWithoutMarkingDeleted(t *testing.T) {
	now := time.Date(2026, 8, 6, 20, 0, 0, 0, time.UTC)
	core := newFakeCore(now)
	provider := &scriptedLifecycleProvider{
		createResult: sandboxgateway.ProviderSandbox{
			SessionRef: "tae-ready-1", State: sandboxgateway.ProviderSandboxReady,
			Root: "/workspace", ExpiresAt: now.Add(time.Hour),
		},
		getResults:   []sandboxgateway.ProviderSandbox{{SessionRef: "tae-ready-1", State: sandboxgateway.ProviderSandboxReady, Root: "/workspace", ExpiresAt: now.Add(-time.Minute)}},
		deleteErrors: []error{&sandboxgateway.ProviderError{Code: "provider_delete_partial", Ambiguous: true, Cause: errors.New("one candidate remained")}, nil},
	}
	service := newLifecycleService(t, core, provider, now)
	if _, err := service.EnsureSandbox(t.Context(), lifecyclePrincipal(), lifecycleEnsureRequest()); err != nil {
		t.Fatalf("initial EnsureSandbox() error = %v", err)
	}
	core.mu.Lock()
	core.state.ObservedState = "ready"
	core.state.ProviderSessionRef = "tae-ready-1"
	core.state.ExpiresAt = ptrTime(now.Add(-time.Minute))
	core.state.Version++
	core.mu.Unlock()

	first, firstErr := service.ReconcileOnce(t.Context(), 10)
	if firstErr == nil || first.Failed != 1 {
		t.Fatalf("first reconcile report/error = %+v/%v, want one failed row", first, firstErr)
	}
	core.mu.Lock()
	intermediate := *core.state
	core.mu.Unlock()
	if intermediate.ObservedState == "deleted" || intermediate.DesiredState != "deleted" {
		t.Fatalf("partial delete advanced Core too far: %+v", intermediate)
	}

	second, secondErr := service.ReconcileOnce(t.Context(), 10)
	if secondErr != nil || second.Failed != 0 {
		t.Fatalf("second reconcile report/error = %+v/%v", second, secondErr)
	}
	core.mu.Lock()
	final := *core.state
	core.mu.Unlock()
	if final.ObservedState != "deleted" {
		t.Fatalf("retry final Core state = %+v", final)
	}
	provider.mu.Lock()
	deleteCalls := len(provider.deleteCalls)
	provider.mu.Unlock()
	if deleteCalls != 2 {
		t.Fatalf("provider delete calls = %d, want two", deleteCalls)
	}
}

func TestManagedSandboxAmbiguousCreateIsUnknownAndDuplicateRecoveryDeletesExactMatches(t *testing.T) {
	now := time.Date(2026, 8, 6, 20, 0, 0, 0, time.UTC)
	core := newFakeCore(now)
	provider := &scriptedLifecycleProvider{
		createErr: &sandboxgateway.ProviderError{Code: "provider_create_ambiguous", Ambiguous: true, Cause: errors.New("create response was lost")},
		findErr:   &sandboxgateway.ProviderError{Code: "provider_create_ambiguous", Ambiguous: true, Cause: errors.New("two exact sessions exist")},
	}
	service := newLifecycleService(t, core, provider, now)
	if _, err := service.EnsureSandbox(t.Context(), lifecyclePrincipal(), lifecycleEnsureRequest()); err == nil {
		t.Fatal("ambiguous EnsureSandbox() unexpectedly succeeded")
	}
	core.mu.Lock()
	unknown := *core.state
	core.mu.Unlock()
	if unknown.ObservedState != "unknown" || unknown.LastErrorCode != "provider_create_ambiguous" {
		t.Fatalf("ambiguous create Core state = %+v", unknown)
	}

	// A duplicate exact-match result is safe to clean only after the provider
	// has completed its bounded exact search. The scripted provider models that
	// search by switching from FindSandbox ambiguity to a delete implementation.
	provider.mu.Lock()
	provider.findErr = &sandboxgateway.ProviderError{Code: "provider_create_ambiguous", Ambiguous: true, Cause: errors.New("two exact sessions exist")}
	provider.deleteErrors = []error{nil}
	provider.mu.Unlock()
	// Make the unknown observation stale so the reconciler is allowed to take
	// the failed -> deleting -> deleted recovery path.
	core.mu.Lock()
	core.state.UpdatedAt = now.Add(-time.Minute)
	core.state.Version++
	core.mu.Unlock()

	report, reconcileErr := service.ReconcileOnce(t.Context(), 10)
	if reconcileErr != nil {
		t.Fatalf("duplicate recovery reconcile error = %v (report %+v)", reconcileErr, report)
	}
	core.mu.Lock()
	final := *core.state
	core.mu.Unlock()
	if final.ObservedState != "deleted" || final.DesiredState != "deleted" {
		t.Fatalf("duplicate recovery Core state = %+v", final)
	}
	provider.mu.Lock()
	deleteCalls := len(provider.deleteCalls)
	provider.mu.Unlock()
	if deleteCalls != 1 {
		t.Fatalf("duplicate recovery delete calls = %d, want one exact recovery call", deleteCalls)
	}
}

func TestManagedSandboxIncompleteCreateSearchRemainsFailClosedPastTTL(t *testing.T) {
	now := time.Date(2026, 8, 6, 20, 0, 0, 0, time.UTC)
	core := newFakeCore(now)
	provider := &scriptedLifecycleProvider{
		createErr: &sandboxgateway.ProviderError{
			Code: "provider_create_ambiguous", Ambiguous: true,
			Cause: errors.New("create response was lost"),
		},
		findErr: &sandboxgateway.ProviderError{
			Code: "provider_search_incomplete", Ambiguous: true,
			Cause: errors.New("bounded exact search did not enumerate every match"),
		},
	}
	service := newLifecycleService(t, core, provider, now)
	if _, err := service.EnsureSandbox(t.Context(), lifecyclePrincipal(), lifecycleEnsureRequest()); err == nil {
		t.Fatal("ambiguous create with an incomplete recovery search unexpectedly succeeded")
	}
	core.mu.Lock()
	unknown := *core.state
	// Expire both the stale observation window and the requested sandbox TTL.
	// The current provider result is still incomplete, so neither fact is
	// sufficient authority to delete anything.
	core.state.CreatedAt = now.Add(-time.Hour)
	core.state.UpdatedAt = now.Add(-time.Hour)
	core.state.Version++
	core.mu.Unlock()
	if unknown.ObservedState != "unknown" || unknown.LastErrorCode != "provider_search_incomplete" {
		t.Fatalf("incomplete recovery observation = %+v", unknown)
	}

	report, reconcileErr := service.ReconcileOnce(t.Context(), 10)
	if reconcileErr == nil || report.Failed != 1 {
		t.Fatalf("incomplete recovery reconcile = %+v/%v, want one retryable failure", report, reconcileErr)
	}
	core.mu.Lock()
	stillUnknown := *core.state
	core.mu.Unlock()
	provider.mu.Lock()
	deleteCalls := len(provider.deleteCalls)
	provider.mu.Unlock()
	if stillUnknown.ObservedState != "unknown" || stillUnknown.DesiredState != "ready" || deleteCalls != 0 {
		t.Fatalf("incomplete recovery mutated state/provider: state=%+v deleteCalls=%d", stillUnknown, deleteCalls)
	}

	// Once a subsequent complete exact search proves there is no live match,
	// the already elapsed TTL permits the normal failed/delete convergence.
	provider.mu.Lock()
	provider.findErr = sandboxgateway.ErrProviderSandboxNotFound
	provider.mu.Unlock()
	report, reconcileErr = service.ReconcileOnce(t.Context(), 10)
	if reconcileErr != nil || report.Failed != 0 {
		t.Fatalf("complete absence recovery reconcile = %+v/%v", report, reconcileErr)
	}
	core.mu.Lock()
	deleted := *core.state
	core.mu.Unlock()
	provider.mu.Lock()
	deleteCalls = len(provider.deleteCalls)
	provider.mu.Unlock()
	if deleted.ObservedState != "deleted" || deleted.DesiredState != "deleted" || deleteCalls != 1 {
		t.Fatalf("complete absence recovery = state=%+v deleteCalls=%d", deleted, deleteCalls)
	}
}

func TestManagedSandboxInvalidProviderReferenceIsFailedForReconciliation(t *testing.T) {
	now := time.Date(2026, 8, 6, 20, 0, 0, 0, time.UTC)
	core := newFakeCore(now)
	provider := &scriptedLifecycleProvider{
		createResult: sandboxgateway.ProviderSandbox{State: sandboxgateway.ProviderSandboxCreating},
	}
	service := newLifecycleService(t, core, provider, now)
	if _, err := service.EnsureSandbox(t.Context(), lifecyclePrincipal(), lifecycleEnsureRequest()); err == nil {
		t.Fatal("invalid provider reference unexpectedly succeeded")
	}
	core.mu.Lock()
	state := *core.state
	core.mu.Unlock()
	if state.ObservedState != "failed" {
		t.Fatalf("invalid provider reference left state in %q: %+v", state.ObservedState, state)
	}
}

func TestManagedSandboxEnsureTimeoutFailsExactCreatingGenerationForCleanup(t *testing.T) {
	now := time.Date(2026, 8, 6, 20, 0, 0, 0, time.UTC)
	core := newFakeCore(now)
	provider := &scriptedLifecycleProvider{
		createResult: sandboxgateway.ProviderSandbox{
			SessionRef: "tae-never-ready-1", State: sandboxgateway.ProviderSandboxCreating,
			ProviderStatusClass: "ready", ExecutionReady: false,
		},
		getResults: []sandboxgateway.ProviderSandbox{{
			SessionRef: "tae-never-ready-1", State: sandboxgateway.ProviderSandboxCreating,
			ProviderStatusClass: "ready", ExecutionReady: false,
		}},
	}
	clockNow := now
	service := newLifecycleServiceWithClock(t, core, provider, func() time.Time {
		clockNow = clockNow.Add(100 * time.Millisecond)
		return clockNow
	}, nil)

	_, err := service.EnsureSandbox(t.Context(), lifecyclePrincipal(), lifecycleEnsureRequest())
	var serviceError *sandboxgateway.Error
	if !errors.As(err, &serviceError) || serviceError.Code != "provider_ready_timeout" {
		t.Fatalf("EnsureSandbox() error = %#v", err)
	}
	core.mu.Lock()
	failed := *core.state
	core.mu.Unlock()
	if failed.ObservedState != "failed" || failed.LastErrorCode != "provider_ready_timeout" || failed.ProviderSessionRef != "tae-never-ready-1" {
		t.Fatalf("timed out Core state = %+v", failed)
	}

	report, reconcileErr := service.ReconcileOnce(t.Context(), 10)
	if reconcileErr != nil || report.Failed != 0 {
		t.Fatalf("cleanup reconcile = %+v/%v", report, reconcileErr)
	}
	core.mu.Lock()
	deleted := *core.state
	core.mu.Unlock()
	provider.mu.Lock()
	deleteCalls := append([]sandboxgateway.DeleteSandboxProviderRequest(nil), provider.deleteCalls...)
	provider.mu.Unlock()
	if deleted.ObservedState != "deleted" || deleted.DesiredState != "deleted" || len(deleteCalls) != 1 ||
		deleteCalls[0].SessionRef != "tae-never-ready-1" {
		t.Fatalf("cleanup result state=%+v calls=%+v", deleted, deleteCalls)
	}
}

func TestManagedSandboxEnsureDeadlineDoesNotFailConcurrentReadyGeneration(t *testing.T) {
	now := time.Date(2026, 8, 6, 20, 0, 0, 0, time.UTC)
	baseCore := newFakeCore(now)
	ready := sandboxgateway.ProviderSandbox{
		SessionRef: "tae-concurrent-ready-1", State: sandboxgateway.ProviderSandboxReady,
		Root: "/workspace", ExpiresAt: now.Add(time.Hour), ProviderStatusClass: "ready", ExecutionReady: true,
	}
	core := &deadlineReadyCore{fakeCore: baseCore, readyResult: ready}
	provider := &scriptedLifecycleProvider{
		createResult: sandboxgateway.ProviderSandbox{
			SessionRef: ready.SessionRef, State: sandboxgateway.ProviderSandboxCreating,
			ProviderStatusClass: "ready", ExecutionReady: false,
		},
		getResults: []sandboxgateway.ProviderSandbox{ready},
	}
	clockNow := now
	service, err := sandboxgateway.NewService(sandboxgateway.Config{
		Core: core, Provider: provider, Limits: sandboxcontract.DefaultLimits(),
		ProviderRegion: "sg", ProviderPSM: "toutiao.tae.sandbox", IdleTTL: time.Minute,
		EnsureTimeout: 250 * time.Millisecond, EnsurePollInterval: time.Millisecond,
		IDGenerator: (&sequenceIDs{values: []string{testSandboxID, testCreateKey}}).Next,
		Now:         func() time.Time { clockNow = clockNow.Add(100 * time.Millisecond); return clockNow },
	})
	if err != nil {
		t.Fatal(err)
	}

	response, ensureErr := service.EnsureSandbox(t.Context(), lifecyclePrincipal(), lifecycleEnsureRequest())
	if ensureErr != nil || response.Sandbox.State != sandboxcontract.SandboxReady || response.Sandbox.Ref.SandboxID != testSandboxID {
		t.Fatalf("concurrent ready EnsureSandbox() = %+v/%v", response, ensureErr)
	}
	baseCore.mu.Lock()
	final := *baseCore.state
	baseCore.mu.Unlock()
	if final.ObservedState != "ready" || final.LastErrorCode == "provider_ready_timeout" {
		t.Fatalf("concurrent ready Core state = %+v", final)
	}
}

func TestManagedSandboxEnsureLogsCorrelatedLifecycleWithoutPayloads(t *testing.T) {
	now := time.Date(2026, 8, 6, 20, 0, 0, 0, time.UTC)
	core := newFakeCore(now)
	provider := &scriptedLifecycleProvider{createResult: sandboxgateway.ProviderSandbox{
		SessionRef: "tae-logged-1", State: sandboxgateway.ProviderSandboxReady,
		Root: "/workspace", ExpiresAt: now.Add(time.Hour), ProviderStatusClass: "creating", ExecutionReady: true,
		RequestID: "provider-log-1",
	}}
	var output bytes.Buffer
	service := newLifecycleServiceWithLogger(t, core, provider, now, slog.New(slog.NewJSONHandler(&output, nil)))
	if _, err := service.EnsureSandbox(t.Context(), lifecyclePrincipal(), lifecycleEnsureRequest()); err != nil {
		t.Fatal(err)
	}
	var records []map[string]any
	decoder := json.NewDecoder(bytes.NewReader(output.Bytes()))
	for {
		var record map[string]any
		if err := decoder.Decode(&record); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		records = append(records, record)
	}
	if len(records) < 4 {
		t.Fatalf("log records = %s", output.String())
	}
	final := records[len(records)-1]
	for key, want := range map[string]any{
		"stage": "ready", "request_id": "lifecycle-ensure", "workspace_id": testWorkspaceID,
		"run_id": testRunID, "run_attempt_id": testAttemptID, "sandbox_id": testSandboxID,
		"provider_session_ref": "tae-logged-1", "provider_status_class": "creating",
		"provider_execution_ready": true, "provider_request_id": "provider-log-1",
	} {
		if final[key] != want {
			t.Fatalf("final log %q = %#v, want %#v; logs=%s", key, final[key], want, output.String())
		}
	}
	for _, forbidden := range []string{"\"arguments\"", "\"environment\"", "authorization", "token", "secret", "\"executable\""} {
		if bytes.Contains(bytes.ToLower(output.Bytes()), []byte(forbidden)) {
			t.Fatalf("logs contain forbidden payload field %q: %s", forbidden, output.String())
		}
	}
}

func newLifecycleService(t *testing.T, core *fakeCore, provider sandboxgateway.Provider, now time.Time) *sandboxgateway.Service {
	return newLifecycleServiceWithLogger(t, core, provider, now, nil)
}

func newLifecycleServiceWithLogger(t *testing.T, core *fakeCore, provider sandboxgateway.Provider, now time.Time, logger *slog.Logger) *sandboxgateway.Service {
	return newLifecycleServiceWithClock(t, core, provider, func() time.Time { return now }, logger)
}

func newLifecycleServiceWithClock(t *testing.T, core *fakeCore, provider sandboxgateway.Provider, now func() time.Time, logger *slog.Logger) *sandboxgateway.Service {
	t.Helper()
	service, err := sandboxgateway.NewService(sandboxgateway.Config{
		Core: core, Provider: provider, Limits: sandboxcontract.DefaultLimits(),
		ProviderRegion: "sg", ProviderPSM: "toutiao.tae.sandbox", IdleTTL: time.Minute,
		EnsureTimeout: 250 * time.Millisecond, EnsurePollInterval: time.Millisecond,
		IDGenerator: (&sequenceIDs{values: []string{testSandboxID, testCreateKey}}).Next,
		Now:         now, Logger: logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func lifecyclePrincipal() sandboxgateway.Principal {
	return sandboxgateway.Principal{
		Audience:    sandboxgateway.AudienceLifecycle,
		WorkspaceID: testWorkspaceID, SessionID: testSessionID, EnvironmentID: testEnvironmentID,
		RunID: testRunID, RunAttemptID: testAttemptID, RunAttemptGeneration: 1, HolderID: "lifecycle-test-holder",
	}
}

func lifecycleEnsureRequest() sandboxcontract.EnsureSandboxRequest {
	return sandboxcontract.EnsureSandboxRequest{
		Profile: sandboxcontract.ProfileV1, RequestID: "lifecycle-ensure",
		Session:             sandboxcontract.SessionIdentity{WorkspaceID: testWorkspaceID, SessionID: testSessionID, EnvironmentID: testEnvironmentID},
		RequestedTTLSeconds: 600, RuntimeProfileDigest: repeatHex("1"), PackSetDigest: repeatHex("2"),
	}
}

func ptrTime(value time.Time) *time.Time { return &value }

var _ sandboxgateway.Provider = (*scriptedLifecycleProvider)(nil)
