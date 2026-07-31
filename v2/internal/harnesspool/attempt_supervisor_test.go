package harnesspool

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/harnessbootstrap"
	"github.com/agentserver/agentserver/v2/internal/harnesscontrol"
)

const testCompletedRolloutLocator = "sessions/2026/07/31/rollout-pool-test.jsonl"

func TestControlAttemptSupervisorWaitsForCompletedTerminalAndStoppedWorkload(t *testing.T) {
	prepared := poolTestPreparedLaunch(t)
	server := newSupervisorTestControlServer(t, prepared)
	lifecycle := &recordingAttemptLifecycle{}
	workload := newSupervisorTestWorkload()
	launcher := attemptWorkloadLauncherFunc(func(_ context.Context, launch AttemptWorkloadLaunch) (AttemptWorkload, error) {
		if launch.Prepared.Manifest.RunAttemptID != prepared.Manifest.RunAttemptID || validateControlCapability(launch.ControlCapability) != nil ||
			launch.RuntimeCapabilities != supervisorTestRuntimeCapabilities() {
			return nil, errors.New("launcher received invalid attempt authority")
		}
		runtime := currentAttemptRuntime(t, server, prepared.Manifest.RunAttemptID)
		runtime.markReady()
		if err := runtime.processEvent(harnesscontrol.Event{
			Kind: harnesscontrol.EventKindThreadReady,
			ThreadReady: &harnesscontrol.ThreadReadyEvent{
				Kind: harnesscontrol.EventKindThreadReady, ThreadID: "thread-supervisor", Resumed: false,
			},
		}); err != nil {
			return nil, err
		}
		if err := runtime.processEvent(harnesscontrol.Event{
			Kind: harnesscontrol.EventKindTurnAccepted,
			TurnAccepted: &harnesscontrol.TurnAcceptedEvent{
				Kind: harnesscontrol.EventKindTurnAccepted, ThreadID: "thread-supervisor", TurnID: "turn-supervisor",
			},
		}); err != nil {
			return nil, err
		}
		if err := runtime.processEvent(harnesscontrol.Event{
			Kind: harnesscontrol.EventKindTurnTerminal,
			TurnTerminal: &harnesscontrol.TurnTerminalEvent{
				Kind: harnesscontrol.EventKindTurnTerminal, ThreadID: "thread-supervisor",
				TurnID: "turn-supervisor", Status: "completed", RolloutLocator: testCompletedRolloutLocator,
			},
		}); err != nil {
			return nil, err
		}
		time.AfterFunc(10*time.Millisecond, func() { workload.finish(nil) })
		return workload, nil
	})
	supervisor := newSupervisorForTest(t, server, launcher)
	if err := supervisor.Supervise(t.Context(), prepared, lifecycle); err != nil {
		t.Fatal(err)
	}
	if workload.stopCount() != 0 {
		t.Fatalf("clean completed workload was forcibly stopped %d times", workload.stopCount())
	}
	if workload.cleanupCount() != 1 {
		t.Fatalf("completed workload cleanup calls = %d, want 1", workload.cleanupCount())
	}
	threads, turns := lifecycle.snapshot()
	if !reflect.DeepEqual(threads, []string{"thread-supervisor"}) ||
		!reflect.DeepEqual(turns, [][2]string{{"thread-supervisor", "turn-supervisor"}}) {
		t.Fatalf("lifecycle calls = threads %q turns %q", threads, turns)
	}
	server.mu.Lock()
	registrations := len(server.byAttempt)
	server.mu.Unlock()
	if registrations != 0 {
		t.Fatalf("supervisor leaked %d control registrations", registrations)
	}
}

func TestControlAttemptSupervisorPreservesFailedTerminalClassification(t *testing.T) {
	prepared := poolTestPreparedLaunch(t)
	server := newSupervisorTestControlServer(t, prepared)
	workload := newSupervisorTestWorkload()
	launcher := attemptWorkloadLauncherFunc(func(_ context.Context, _ AttemptWorkloadLaunch) (AttemptWorkload, error) {
		runtime := currentAttemptRuntime(t, server, prepared.Manifest.RunAttemptID)
		runtime.markReady()
		if err := runtime.processEvent(harnesscontrol.Event{
			Kind: harnesscontrol.EventKindThreadReady,
			ThreadReady: &harnesscontrol.ThreadReadyEvent{
				Kind: harnesscontrol.EventKindThreadReady, ThreadID: "thread-failed", Resumed: false,
			},
		}); err != nil {
			return nil, err
		}
		if err := runtime.processEvent(harnesscontrol.Event{
			Kind: harnesscontrol.EventKindTurnAccepted,
			TurnAccepted: &harnesscontrol.TurnAcceptedEvent{
				Kind: harnesscontrol.EventKindTurnAccepted, ThreadID: "thread-failed", TurnID: "turn-failed",
			},
		}); err != nil {
			return nil, err
		}
		if err := runtime.processEvent(harnesscontrol.Event{
			Kind: harnesscontrol.EventKindTurnTerminal,
			TurnTerminal: &harnesscontrol.TurnTerminalEvent{
				Kind: harnesscontrol.EventKindTurnTerminal, ThreadID: "thread-failed", TurnID: "turn-failed",
				Status: "failed", ErrorCode: "model_error", ErrorMessage: "model request failed",
			},
		}); err != nil {
			return nil, err
		}
		time.AfterFunc(10*time.Millisecond, func() { workload.finish(nil) })
		return workload, nil
	})
	supervisor := newSupervisorForTest(t, server, launcher)
	err := supervisor.Supervise(t.Context(), prepared, &recordingAttemptLifecycle{})
	var terminal *AttemptTerminalError
	if !errors.As(err, &terminal) || terminal.Status != "failed" || terminal.Code != "model_error" {
		t.Fatalf("failed terminal error = %#v (%v)", terminal, err)
	}
	if workload.cleanupCount() != 1 {
		t.Fatalf("failed workload cleanup calls = %d, want 1", workload.cleanupCount())
	}
}

func TestControlAttemptSupervisorStopsWorkloadWhenAuthorityContextIsCancelled(t *testing.T) {
	prepared := poolTestPreparedLaunch(t)
	server := newSupervisorTestControlServer(t, prepared)
	workload := newSupervisorTestWorkload()
	launched := make(chan struct{})
	launcher := attemptWorkloadLauncherFunc(func(_ context.Context, _ AttemptWorkloadLaunch) (AttemptWorkload, error) {
		runtime := currentAttemptRuntime(t, server, prepared.Manifest.RunAttemptID)
		runtime.markReady()
		close(launched)
		return workload, nil
	})
	supervisor := newSupervisorForTest(t, server, launcher)
	ctx, cancel := context.WithCancelCause(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- supervisor.Supervise(ctx, prepared, &recordingAttemptLifecycle{})
	}()
	select {
	case <-launched:
	case <-time.After(time.Second):
		t.Fatal("workload was not launched")
	}
	cause := errors.New("renew run-attempt leases: lease authority lost")
	cancel(cause)
	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), cause.Error()) {
			t.Fatalf("cancelled supervision error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled supervisor did not return promptly")
	}
	if workload.stopCount() != 1 {
		t.Fatalf("cancelled workload stop calls = %d", workload.stopCount())
	}
	if workload.cleanupCount() != 1 {
		t.Fatalf("cancelled workload cleanup calls = %d, want 1", workload.cleanupCount())
	}
	command := supervisor.interruptCommand(cause)
	if command.Reason != "lease_lost" || command.GraceMillis != 1_000 {
		t.Fatalf("lease-loss interrupt = %+v", command)
	}
}

func TestControlAttemptSupervisorRejectsWorkloadExitBeforeControl(t *testing.T) {
	prepared := poolTestPreparedLaunch(t)
	server := newSupervisorTestControlServer(t, prepared)
	workload := newSupervisorTestWorkload()
	workload.finish(errors.New("container exited 1"))
	workload.cleanupErr = errors.New("synthetic runtime cleanup failure")
	supervisor := newSupervisorForTest(t, server, attemptWorkloadLauncherFunc(
		func(context.Context, AttemptWorkloadLaunch) (AttemptWorkload, error) { return workload, nil },
	))
	err := supervisor.Supervise(t.Context(), prepared, &recordingAttemptLifecycle{})
	if !errors.Is(err, ErrAttemptStoppedBeforeTerminal) || !strings.Contains(err.Error(), "container exited 1") ||
		!strings.Contains(err.Error(), "synthetic runtime cleanup failure") {
		t.Fatalf("pre-control workload exit error = %v", err)
	}
	if workload.cleanupCount() != 1 {
		t.Fatalf("pre-control workload cleanup calls = %d, want 1", workload.cleanupCount())
	}
}

func TestControlAttemptSupervisorFailsBeforeForkWhenCapabilityIssuanceFails(t *testing.T) {
	prepared := poolTestPreparedLaunch(t)
	server := newSupervisorTestControlServer(t, prepared)
	launches := 0
	launcher := attemptWorkloadLauncherFunc(func(context.Context, AttemptWorkloadLaunch) (AttemptWorkload, error) {
		launches++
		return nil, errors.New("must not launch")
	})
	capabilityErr := errors.New("synthetic capability issuer unavailable")
	supervisor, err := NewControlAttemptSupervisor(
		server,
		launcher,
		attemptRuntimeCapabilitySourceFunc(func(context.Context, PreparedRunLaunch) (harnessbootstrap.RuntimeCapabilities, error) {
			return harnessbootstrap.RuntimeCapabilities{}, capabilityErr
		}),
		ControlAttemptSupervisorConfig{StartupTimeout: time.Second, StopTimeout: time.Second, InterruptGrace: time.Second},
	)
	if err != nil {
		t.Fatal(err)
	}
	err = supervisor.Supervise(t.Context(), prepared, &recordingAttemptLifecycle{})
	if !errors.Is(err, capabilityErr) || launches != 0 {
		t.Fatalf("capability failure = %v, launches=%d", err, launches)
	}
	server.mu.Lock()
	registrations := len(server.byAttempt)
	server.mu.Unlock()
	if registrations != 0 {
		t.Fatalf("capability failure leaked %d control registrations", registrations)
	}
}

func newSupervisorTestControlServer(t *testing.T, prepared PreparedRunLaunch) *ControlServer {
	t.Helper()
	server, err := NewControlServer(testControlServerConfig(prepared))
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func newSupervisorForTest(t *testing.T, server *ControlServer, launcher AttemptWorkloadLauncher) *ControlAttemptSupervisor {
	t.Helper()
	supervisor, err := NewControlAttemptSupervisor(server, launcher, staticSupervisorCapabilities{}, ControlAttemptSupervisorConfig{
		StartupTimeout: time.Second, StopTimeout: time.Second, InterruptGrace: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	return supervisor
}

type staticSupervisorCapabilities struct{}

func (staticSupervisorCapabilities) IssueAttemptRuntimeCapabilities(context.Context, PreparedRunLaunch) (harnessbootstrap.RuntimeCapabilities, error) {
	return supervisorTestRuntimeCapabilities(), nil
}

func supervisorTestRuntimeCapabilities() harnessbootstrap.RuntimeCapabilities {
	return harnessbootstrap.RuntimeCapabilities{
		ExecutorMCP: "supervisor-executor-capability",
		LLMProxy:    "supervisor-llmproxy-capability",
	}
}

func currentAttemptRuntime(t *testing.T, server *ControlServer, attemptID string) *attemptControlRuntime {
	t.Helper()
	server.mu.Lock()
	defer server.mu.Unlock()
	runtime := server.byAttempt[attemptID]
	if runtime == nil {
		t.Fatalf("attempt %s is not registered", attemptID)
	}
	return runtime
}

type attemptWorkloadLauncherFunc func(context.Context, AttemptWorkloadLaunch) (AttemptWorkload, error)

func (launcher attemptWorkloadLauncherFunc) Launch(ctx context.Context, launch AttemptWorkloadLaunch) (AttemptWorkload, error) {
	return launcher(ctx, launch)
}

type attemptRuntimeCapabilitySourceFunc func(context.Context, PreparedRunLaunch) (harnessbootstrap.RuntimeCapabilities, error)

func (source attemptRuntimeCapabilitySourceFunc) IssueAttemptRuntimeCapabilities(ctx context.Context, prepared PreparedRunLaunch) (harnessbootstrap.RuntimeCapabilities, error) {
	return source(ctx, prepared)
}

type supervisorTestWorkload struct {
	done       chan error
	finishMu   sync.Once
	stateMu    sync.Mutex
	stops      int
	cleanups   int
	cleanupErr error
}

func newSupervisorTestWorkload() *supervisorTestWorkload {
	return &supervisorTestWorkload{done: make(chan error, 1)}
}

func (workload *supervisorTestWorkload) Wait(ctx context.Context) error {
	select {
	case err := <-workload.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (workload *supervisorTestWorkload) Stop(context.Context) error {
	workload.stateMu.Lock()
	workload.stops++
	workload.stateMu.Unlock()
	workload.finish(nil)
	return nil
}

func (*supervisorTestWorkload) OpenCheckpointRollout(context.Context, string) (AttemptCheckpointRollout, error) {
	return AttemptCheckpointRollout{}, errors.New("test workload has no checkpoint rollout")
}

func (workload *supervisorTestWorkload) Cleanup(context.Context) error {
	workload.stateMu.Lock()
	defer workload.stateMu.Unlock()
	workload.cleanups++
	return workload.cleanupErr
}

func (workload *supervisorTestWorkload) finish(err error) {
	workload.finishMu.Do(func() { workload.done <- err })
}

func (workload *supervisorTestWorkload) stopCount() int {
	workload.stateMu.Lock()
	defer workload.stateMu.Unlock()
	return workload.stops
}

func (workload *supervisorTestWorkload) cleanupCount() int {
	workload.stateMu.Lock()
	defer workload.stateMu.Unlock()
	return workload.cleanups
}
