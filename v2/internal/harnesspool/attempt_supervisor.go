package harnesspool

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/agentserver/agentserver/v2/internal/harnesscontrol"
)

var ErrAttemptStoppedBeforeTerminal = errors.New("attempt workload stopped before a turn terminal event")

type AttemptWorkloadLaunch struct {
	Prepared          PreparedRunLaunch
	ControlCapability string
}

// AttemptWorkload is one already-created per-attempt process boundary. Wait
// returns only after the full process tree is stopped. Both methods must honor
// ctx so holder shutdown cannot block forever on a broken runtime.
type AttemptWorkload interface {
	Wait(context.Context) error
	Stop(context.Context) error
}

type AttemptWorkloadLauncher interface {
	Launch(context.Context, AttemptWorkloadLaunch) (AttemptWorkload, error)
}

type ControlAttemptSupervisorConfig struct {
	StartupTimeout time.Duration
	StopTimeout    time.Duration
	InterruptGrace time.Duration
}

func DefaultControlAttemptSupervisorConfig() ControlAttemptSupervisorConfig {
	return ControlAttemptSupervisorConfig{
		StartupTimeout: 2 * time.Minute,
		StopTimeout:    30 * time.Second,
		InterruptGrace: 10 * time.Second,
	}
}

// AttemptTerminalError preserves the worker's bounded terminal classification
// without pretending that transport success is a durable core finalization.
type AttemptTerminalError struct {
	Status  string
	Code    string
	Message string
}

func (err *AttemptTerminalError) Error() string {
	if err == nil {
		return "<nil>"
	}
	if err.Code == "" {
		return fmt.Sprintf("attempt turn ended with status %s", err.Status)
	}
	return fmt.Sprintf("attempt turn ended with status %s (%s): %s", err.Status, err.Code, err.Message)
}

// ControlAttemptSupervisor composes one registered control capability with
// one launched workload. It deliberately does not implement checkpoint/core
// finalization; a completed terminal only means this runtime stopped cleanly.
type ControlAttemptSupervisor struct {
	controls *ControlServer
	launcher AttemptWorkloadLauncher
	config   ControlAttemptSupervisorConfig
}

func NewControlAttemptSupervisor(
	controls *ControlServer,
	launcher AttemptWorkloadLauncher,
	config ControlAttemptSupervisorConfig,
) (*ControlAttemptSupervisor, error) {
	if controls == nil {
		return nil, errors.New("harness control server is required")
	}
	if launcher == nil {
		return nil, errors.New("attempt workload launcher is required")
	}
	if config.StartupTimeout < time.Millisecond || config.StartupTimeout > time.Hour {
		return nil, errors.New("attempt startup timeout must be between 1ms and 1h")
	}
	if config.StopTimeout < time.Millisecond || config.StopTimeout > time.Hour {
		return nil, errors.New("attempt stop timeout must be between 1ms and 1h")
	}
	if config.InterruptGrace < time.Millisecond || config.InterruptGrace > 5*time.Minute {
		return nil, errors.New("attempt interrupt grace must be between 1ms and 5m")
	}
	return &ControlAttemptSupervisor{controls: controls, launcher: launcher, config: config}, nil
}

func (supervisor *ControlAttemptSupervisor) Supervise(
	ctx context.Context,
	prepared PreparedRunLaunch,
	lifecycle AttemptLifecycle,
) error {
	if ctx == nil {
		return errors.New("attempt supervision context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	control, err := supervisor.controls.RegisterAttempt(prepared, lifecycle)
	if err != nil {
		return fmt.Errorf("register attempt control: %w", err)
	}
	defer control.Close(errors.New("attempt supervision ended"))

	workload, err := supervisor.launcher.Launch(ctx, AttemptWorkloadLaunch{
		Prepared: prepared, ControlCapability: control.Capability(),
	})
	if err != nil {
		return fmt.Errorf("launch attempt workload: %w", err)
	}
	if workload == nil {
		return errors.New("attempt workload launcher returned a nil workload")
	}
	waitContext, cancelWait := context.WithCancel(context.Background())
	defer cancelWait()
	workloadDone := make(chan error, 1)
	go func() { workloadDone <- workload.Wait(waitContext) }()
	connected := make(chan error, 1)
	go func() { connected <- control.WaitConnected(waitContext) }()
	terminal := make(chan terminalWaitResult, 1)
	go func() {
		value, err := control.WaitTerminal(waitContext)
		terminal <- terminalWaitResult{event: value, err: err}
	}()

	startupTimer := time.NewTimer(supervisor.config.StartupTimeout)
	defer startupTimer.Stop()
	select {
	case <-ctx.Done():
		return supervisor.stopAttempt(control, workload, workloadDone, false, context.Cause(ctx))
	case err := <-connected:
		if err != nil {
			return supervisor.stopAttempt(control, workload, workloadDone, false, fmt.Errorf("establish worker control: %w", err))
		}
	case err := <-workloadDone:
		if terminalErr, finished := terminalAfterWorkloadExit(control, err); finished {
			return terminalErr
		}
		return errors.Join(ErrAttemptStoppedBeforeTerminal, err)
	case <-startupTimer.C:
		return supervisor.stopAttempt(
			control, workload, workloadDone, false,
			fmt.Errorf("worker did not establish control within %s", supervisor.config.StartupTimeout),
		)
	}

	select {
	case <-ctx.Done():
		return supervisor.stopAttempt(control, workload, workloadDone, true, context.Cause(ctx))
	case result := <-terminal:
		if result.err != nil {
			return supervisor.stopAttempt(control, workload, workloadDone, false, fmt.Errorf("worker control failed: %w", result.err))
		}
		return supervisor.finishTerminal(ctx, control, workload, workloadDone, result.event)
	case err := <-workloadDone:
		if terminalErr, finished := terminalAfterWorkloadExit(control, err); finished {
			return terminalErr
		}
		return errors.Join(ErrAttemptStoppedBeforeTerminal, err)
	}
}

type terminalWaitResult struct {
	event harnesscontrol.TurnTerminalEvent
	err   error
}

func (supervisor *ControlAttemptSupervisor) finishTerminal(
	ctx context.Context,
	control *AttemptControl,
	workload AttemptWorkload,
	workloadDone <-chan error,
	terminal harnesscontrol.TurnTerminalEvent,
) error {
	timer := time.NewTimer(supervisor.config.StopTimeout)
	defer timer.Stop()
	select {
	case err := <-workloadDone:
		return terminalResultError(terminal, err)
	case <-ctx.Done():
		return supervisor.stopAttempt(control, workload, workloadDone, false, context.Cause(ctx))
	case <-timer.C:
		cause := fmt.Errorf("attempt workload did not stop within %s after turn terminal", supervisor.config.StopTimeout)
		return supervisor.stopAttempt(control, workload, workloadDone, false, errors.Join(terminalResultError(terminal, nil), cause))
	}
}

func (supervisor *ControlAttemptSupervisor) stopAttempt(
	control *AttemptControl,
	workload AttemptWorkload,
	workloadDone <-chan error,
	interrupt bool,
	cause error,
) error {
	if cause == nil {
		cause = errors.New("attempt supervision cancelled")
	}
	var interruptErr error
	if interrupt {
		cleanupContext, cancel := context.WithTimeout(context.Background(), supervisor.config.StopTimeout)
		interruptErr = control.SendInterrupt(cleanupContext, supervisor.interruptCommand(cause))
		cancel()
		if interruptErr == nil || errors.Is(interruptErr, ErrControlWriteAmbiguous) {
			graceTimer := time.NewTimer(supervisor.config.InterruptGrace)
			select {
			case waitErr := <-workloadDone:
				graceTimer.Stop()
				return errors.Join(cause, interruptErr, waitErr)
			case <-graceTimer.C:
			}
		}
	}
	cleanupContext, cancelCleanup := context.WithTimeout(context.Background(), supervisor.config.StopTimeout)
	defer cancelCleanup()
	stopResult := make(chan error, 1)
	go func() { stopResult <- workload.Stop(cleanupContext) }()
	var stopErr error
	select {
	case stopErr = <-stopResult:
	case <-cleanupContext.Done():
		stopErr = fmt.Errorf("stop attempt workload: %w", cleanupContext.Err())
	}
	select {
	case waitErr := <-workloadDone:
		return errors.Join(cause, interruptErr, stopErr, waitErr)
	case <-cleanupContext.Done():
		return errors.Join(cause, interruptErr, stopErr, fmt.Errorf("wait for stopped attempt workload: %w", cleanupContext.Err()))
	}
}

func (supervisor *ControlAttemptSupervisor) interruptCommand(cause error) harnesscontrol.InterruptCommand {
	reason := "cancelled"
	message := "attempt execution was cancelled"
	lower := strings.ToLower(cause.Error())
	switch {
	case strings.Contains(lower, "lease"):
		reason = "lease_lost"
		message = "attempt lease authority was lost"
	case strings.Contains(lower, "fenc"), strings.Contains(lower, "stale generation"):
		reason = "fenced"
		message = "attempt generation was fenced"
	case errors.Is(cause, context.Canceled):
		reason = "shutdown"
		message = "harness pool is shutting down"
	}
	graceMillis := (supervisor.config.InterruptGrace + time.Millisecond - 1) / time.Millisecond
	return harnesscontrol.InterruptCommand{
		Kind: harnesscontrol.CommandKindInterrupt, Reason: reason,
		GraceMillis: int64(graceMillis), Message: message,
	}
}

func terminalResultError(terminal harnesscontrol.TurnTerminalEvent, workloadErr error) error {
	if terminal.Status == "completed" {
		return workloadErr
	}
	return errors.Join(&AttemptTerminalError{
		Status: terminal.Status, Code: terminal.ErrorCode, Message: terminal.ErrorMessage,
	}, workloadErr)
}

func terminalAfterWorkloadExit(control *AttemptControl, workloadErr error) (error, bool) {
	select {
	case <-control.runtime.done:
		control.runtime.mu.Lock()
		outcome := control.runtime.outcome
		control.runtime.mu.Unlock()
		if outcome.terminal != nil {
			return terminalResultError(*outcome.terminal, workloadErr), true
		}
		return errors.Join(ErrAttemptStoppedBeforeTerminal, workloadErr, outcome.err), true
	default:
		return nil, false
	}
}
