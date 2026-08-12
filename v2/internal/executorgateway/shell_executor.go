package executorgateway

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/agentserver/agentserver/v2/internal/codexwire"
	"github.com/agentserver/agentserver/v2/internal/execprofile"
	"github.com/agentserver/agentserver/v2/internal/executionbackend"
)

const (
	defaultShellTerminalGrace  = 30 * time.Second
	maximumShellTerminalGrace  = 5 * time.Minute
	shellCoreFinalizationGrace = 10 * time.Second
	defaultShellMaxOutputBytes = 512 * 1024
	maximumShellMaxOutputBytes = 768 * 1024
	maximumShellOutputChunks   = 4096
)

type ShellV1OutputChunk struct {
	Sequence    uint64 `json:"sequence"`
	Stream      string `json:"stream"`
	ChunkBase64 string `json:"chunk_base64"`
}

type ShellV1Result struct {
	ProcessID      string               `json:"process_id"`
	Status         string               `json:"status"`
	Chunks         []ShellV1OutputChunk `json:"chunks"`
	NextSequence   uint64               `json:"next_sequence"`
	ExitCode       *int32               `json:"exit_code,omitempty"`
	SandboxDenied  bool                 `json:"sandbox_denied"`
	TimedOut       bool                 `json:"timed_out"`
	OutputComplete bool                 `json:"output_complete"`
}

type ShellExecutorConfig struct {
	Lifecycle                context.Context
	Now                      func() time.Time
	TerminalGrace            time.Duration
	MaxOutputBytes           int
	PolicyResolver           ExecutionPolicyResolver
	ApprovalGate             ExecutionApprovalGate
	BackendRouter            *executionbackend.Router
	ManagedEnvironmentIssuer ManagedProcessEnvironmentIssuer
	ManagedTargetFencer      ManagedTargetFencer
	Logger                   *slog.Logger
}

func DefaultShellExecutorConfig(lifecycle context.Context) ShellExecutorConfig {
	return ShellExecutorConfig{
		Lifecycle: lifecycle, Now: time.Now,
		TerminalGrace: defaultShellTerminalGrace, MaxOutputBytes: defaultShellMaxOutputBytes,
	}
}

type ShellExecuteRequest struct {
	Principal  ExecutorMCPPrincipal
	ToolCallID string
	Arguments  json.RawMessage
	Elicitor   ApprovalElicitor
}

// ShellExecutor owns one terminal-only shell vertical slice. Once core grants
// the process/start dispatch boundary, cleanup uses the executor lifecycle
// rather than the MCP request context so client cancellation cannot orphan a
// process before the frozen hard deadline and terminal grace expire.
type ShellExecutor struct {
	resolver    *EnvironmentResolver
	authority   ExecutionAuthority
	dispatcher  ProcessDispatcher
	identities  *ShellV1IdentityAllocator
	transitions *ExecutionTransitionAllocator
	config      ShellExecutorConfig
}

func NewShellExecutor(resolver *EnvironmentResolver, authority ExecutionAuthority, dispatcher ProcessDispatcher, identities *ShellV1IdentityAllocator, transitions *ExecutionTransitionAllocator, config ShellExecutorConfig) (*ShellExecutor, error) {
	if resolver == nil || authority == nil || dispatcher == nil || identities == nil || transitions == nil {
		return nil, errors.New("shell resolver, core authority, dispatcher, and allocators are required")
	}
	if config.Lifecycle == nil {
		return nil, errors.New("shell executor lifecycle context is required")
	}
	if config.Now == nil {
		return nil, errors.New("shell executor clock is required")
	}
	if config.PolicyResolver == nil || config.ApprovalGate == nil {
		return nil, errors.New("shell execution policy resolver and approval gate are required")
	}
	if config.TerminalGrace <= 0 || config.TerminalGrace > maximumShellTerminalGrace {
		return nil, fmt.Errorf("shell terminal grace must be positive and at most %s", maximumShellTerminalGrace)
	}
	if config.MaxOutputBytes < 1 || config.MaxOutputBytes > maximumShellMaxOutputBytes {
		return nil, fmt.Errorf("shell output bound must be between 1 and %d bytes", maximumShellMaxOutputBytes)
	}
	return &ShellExecutor{
		resolver: resolver, authority: authority, dispatcher: dispatcher,
		identities: identities, transitions: transitions, config: config,
	}, nil
}

func (executor *ShellExecutor) Execute(ctx context.Context, request ShellExecuteRequest) (ShellV1Result, error) {
	if ctx == nil {
		return ShellV1Result{}, errors.New("shell execution context is required")
	}
	if err := validateExecutorMCPPrincipal(request.Principal); err != nil {
		return ShellV1Result{}, err
	}
	var arguments ShellV1Arguments
	if err := decodeExactJSON(request.Arguments, &arguments); err != nil {
		return ShellV1Result{}, fmt.Errorf("decode shell arguments: %w", err)
	}
	if err := validateRegistryIdentity("shell environment ID", arguments.EnvironmentID); err != nil {
		return ShellV1Result{}, err
	}
	environment, err := executor.resolver.ResolveForPrincipal(ctx, request.Principal, arguments.EnvironmentID)
	if err != nil {
		return ShellV1Result{}, fmt.Errorf("resolve shell environment: %w", err)
	}
	if request.Principal.Production && environment.InsecureDev {
		return ShellV1Result{}, errors.New("production shell execution cannot target an insecure-development environment")
	}
	identities, err := executor.identities.Allocate()
	if err != nil {
		return ShellV1Result{}, fmt.Errorf("allocate shell identities: %w", err)
	}
	policy, err := executor.config.PolicyResolver.ResolveExecutionPolicy(ctx, ExecutionPolicyInput{
		Principal: request.Principal, ToolName: "shell", Arguments: append(json.RawMessage(nil), request.Arguments...), Environment: environment,
	})
	if err != nil {
		return ShellV1Result{}, fmt.Errorf("resolve shell execution policy: %w", err)
	}
	plan, err := MapShellV1(request.Arguments, request.Principal, request.ToolCallID, environment, policy, identities)
	if err != nil {
		return ShellV1Result{}, err
	}
	state, err := newShellExecutionState(executor.authority, executor.transitions, request.Principal, plan)
	if err != nil {
		return ShellV1Result{}, err
	}
	prepared, err := state.PrepareExecution(ctx)
	if err != nil {
		return ShellV1Result{}, err
	}
	switch prepared.Status {
	case "denied":
		return ShellV1Result{}, fmt.Errorf("%w: shell", ErrExecutionPolicyDenied)
	case "pending_approval":
		authorized, authorizeErr := executor.config.ApprovalGate.AuthorizeExecution(ctx, ApprovalGateRequest{
			Principal: request.Principal, Execution: prepared, ToolName: "shell",
			ToolCallID: request.ToolCallID, Elicitor: request.Elicitor,
		})
		if authorizeErr != nil {
			return ShellV1Result{}, authorizeErr
		}
		if err := state.AcceptAuthorizedExecution(authorized); err != nil {
			return ShellV1Result{}, err
		}
	case "approved":
	default:
		return ShellV1Result{}, fmt.Errorf("prepared shell execution has unsupported policy status %q", prepared.Status)
	}
	if err := state.PrepareOperations(ctx); err != nil {
		return ShellV1Result{}, err
	}
	begin, err := state.BeginStart(ctx)
	if err != nil {
		return ShellV1Result{}, err
	}
	if !begin.Began {
		return ShellV1Result{}, fmt.Errorf("core did not grant the one-shot shell process/start dispatch; operation status is %q", begin.Operation.Status)
	}
	if environment.Target.Kind == executionbackend.KindTAE {
		return executor.executeManaged(ctx, request, environment, identities, plan, state)
	}

	deadline := executor.config.Now().Add(time.Duration(plan.TimeoutMillis) * time.Millisecond)
	processDeadline := deadline.Add(executor.config.TerminalGrace)
	executionCtx, cancelExecution := context.WithDeadline(executor.config.Lifecycle, processDeadline.Add(shellCoreFinalizationGrace))
	defer cancelExecution()
	processCtx, cancelProcess := context.WithDeadline(executionCtx, processDeadline)
	defer cancelProcess()
	startExchange, dispatchErr := executor.dispatcher.DispatchProcess(processCtx, ProcessDispatchRequest{
		ExecutorID:                   environment.ExecutorID,
		ExpectedConnectionGeneration: environment.ConnectionGeneration,
		Context:                      plan.Start.Routing,
		Directives:                   &plan.Directives,
		RPC:                          plan.Start.RPC,
	})
	if startExchange == nil {
		result := newUnknownShellResult(plan.ProcessID)
		return executor.closeWithoutStartExchange(executionCtx, state, plan, result, dispatchErr)
	}
	forceStartUnknown := dispatchErr != nil

	coordinator, err := NewProcessTimeoutCoordinator(state, executor.dispatcher)
	if err != nil {
		return ShellV1Result{}, err
	}
	timeoutRequestID, err := json.Marshal(identities.TimeoutRPCRequestID)
	if err != nil {
		return ShellV1Result{}, err
	}
	timeoutResultCh := make(chan shellTimeoutOutcome, 1)
	go func() {
		result, runErr := coordinator.Run(processCtx, startExchange, ProcessTimeoutDispatchRequest{
			ExecutorID:                   environment.ExecutorID,
			ExpectedConnectionGeneration: environment.ConnectionGeneration,
			Context:                      plan.Timeout.Routing,
			Deadline:                     deadline,
			RPCRequestID:                 timeoutRequestID,
			Begin: BeginOperationDispatchRequest{
				OperationID: plan.Timeout.OperationID, ExecutionID: plan.Timeout.Routing.ExecutionID,
				RunID: request.Principal.Run.RunID, RunAttemptID: request.Principal.Run.RunAttemptID,
				HolderID: request.Principal.Run.HolderID, RunAttemptGeneration: request.Principal.Run.RunAttemptGeneration,
				ConnectionGeneration: environment.ConnectionGeneration,
				PolicyContext:        plan.PolicyContext, OperationPlan: plan.OperationPlan, Params: plan.Timeout.Params,
			},
		})
		timeoutResultCh <- shellTimeoutOutcome{result: result, err: runErr}
	}()
	evidenceCh := make(chan shellProcessEvidence, 1)
	go func() {
		evidenceCh <- collectShellProcessEvidence(processCtx, startExchange, plan.ProcessID, executor.config.MaxOutputBytes, executor.config.Now)
	}()

	startResponse, startResponseErr := startExchange.AwaitResponse(processCtx)
	startResponseSucceeded := false
	startResponseIsError := false
	startAcknowledged := false
	var orchestrationErrors []error
	if startResponseErr == nil && !forceStartUnknown {
		startResponseSucceeded, startResponseIsError, err = classifyShellProcessResponse(startResponse, execprofile.MethodProcessStart, plan.ProcessID)
		if err != nil {
			orchestrationErrors = append(orchestrationErrors, err)
			forceStartUnknown = true
		} else {
			if _, err := state.Acknowledge(executionCtx, plan.Start, startResponse); err != nil {
				orchestrationErrors = append(orchestrationErrors, err)
				forceStartUnknown = true
			} else {
				startAcknowledged = true
			}
		}
	} else if startResponseErr != nil {
		orchestrationErrors = append(orchestrationErrors, fmt.Errorf("await process/start response: %w", startResponseErr))
	}
	evidence := <-evidenceCh
	if evidence.err != nil {
		orchestrationErrors = append(orchestrationErrors, evidence.err)
	}

	startStatus := shellStartTerminalStatus(startAcknowledged, startResponseSucceeded, startResponseIsError, forceStartUnknown, evidence)
	startResultJSON, err := json.Marshal(shellOperationTerminalResult{
		Kind: ShellV1OperationProcessStart, ProcessID: plan.ProcessID, Status: startStatus,
		Acknowledged: startAcknowledged, ResponseError: startResponseIsError,
		ExitCode: evidence.exitCode, SandboxDenied: evidence.sandboxDenied,
		OutputComplete: evidence.outputComplete(), LastSequence: evidence.nextSequence - 1,
	})
	if err != nil {
		return ShellV1Result{}, err
	}
	if _, err := state.CompleteOperation(executionCtx, plan.Start, startStatus, startResultJSON); err != nil {
		return ShellV1Result{}, errors.Join(append(orchestrationErrors, err)...)
	}

	// A terminal observed before the frozen deadline tries the explicit Skip
	// path immediately. If timeout Begin already won, the local state observes
	// dispatching and leaves completion to the timeout branch.
	if !evidence.observedAt.IsZero() && evidence.observedAt.Before(deadline) {
		if _, err := state.SkipTimeoutIfPrepared(executionCtx, "process_terminal_before_deadline"); err != nil {
			orchestrationErrors = append(orchestrationErrors, err)
		}
	}
	timeoutOutcome := <-timeoutResultCh
	if timeoutOutcome.err != nil {
		orchestrationErrors = append(orchestrationErrors, timeoutOutcome.err)
	}
	timedOut := timeoutOutcome.result.Source != ""
	if timeoutOutcome.result.ProcessTerminalBeforeDeadline {
		if _, err := state.SkipTimeoutIfPrepared(executionCtx, "process_terminal_before_deadline"); err != nil {
			orchestrationErrors = append(orchestrationErrors, err)
		}
	}

	if timeoutOutcome.result.Begin.Began {
		timeoutStatus := "unknown"
		timeoutAcknowledged := false
		timeoutResponseError := false
		if timeoutOutcome.err == nil && timeoutOutcome.result.Terminate != nil {
			response, responseErr := timeoutOutcome.result.Terminate.AwaitResponse(processCtx)
			if responseErr != nil {
				orchestrationErrors = append(orchestrationErrors, fmt.Errorf("await process/terminate response: %w", responseErr))
			} else {
				var classifyErr error
				_, timeoutResponseError, classifyErr = classifyShellProcessResponse(response, execprofile.MethodProcessTerminate, plan.ProcessID)
				if classifyErr != nil {
					orchestrationErrors = append(orchestrationErrors, classifyErr)
				} else if _, acknowledgeErr := state.Acknowledge(executionCtx, plan.Timeout, response); acknowledgeErr != nil {
					orchestrationErrors = append(orchestrationErrors, acknowledgeErr)
				} else {
					timeoutAcknowledged = true
					if evidence.outputComplete() {
						// A reached hard deadline is a failed shell outcome even when
						// process/terminate itself was accepted successfully. Without
						// real exited+closed evidence it remains unknown.
						timeoutStatus = "failed"
					}
				}
			}
		}
		timeoutResultJSON, marshalErr := json.Marshal(shellOperationTerminalResult{
			Kind: ShellV1OperationTimeoutTerminate, ProcessID: plan.ProcessID, Status: timeoutStatus,
			Acknowledged: timeoutAcknowledged, ResponseError: timeoutResponseError,
			ExitCode: evidence.exitCode, SandboxDenied: evidence.sandboxDenied,
			OutputComplete: evidence.outputComplete(), LastSequence: evidence.nextSequence - 1,
		})
		if marshalErr != nil {
			return ShellV1Result{}, marshalErr
		}
		if _, completeErr := state.CompleteOperation(executionCtx, plan.Timeout, timeoutStatus, timeoutResultJSON); completeErr != nil {
			return ShellV1Result{}, errors.Join(append(orchestrationErrors, completeErr)...)
		}
	} else if state.OperationStatus(plan.Timeout.OperationID) == "prepared" {
		// Begin may have failed before committing while the process later became
		// terminal. Close the now-unnecessary optional operation explicitly; no
		// terminate is sent without Began=true.
		if _, err := state.SkipTimeoutIfPrepared(executionCtx, "process_terminal_without_timeout_dispatch"); err != nil {
			return ShellV1Result{}, errors.Join(append(orchestrationErrors, err)...)
		}
	}

	result := ShellV1Result{
		ProcessID: plan.ProcessID, Chunks: evidence.chunks, NextSequence: evidence.nextSequence,
		ExitCode: evidence.exitCode, SandboxDenied: evidence.sandboxDenied,
		TimedOut: timedOut, OutputComplete: evidence.outputComplete(),
	}
	result.Status = aggregateShellResultStatus(state.OperationStatus(plan.Start.OperationID), state.OperationStatus(plan.Timeout.OperationID))
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return ShellV1Result{}, err
	}
	if len(resultJSON) > 1024*1024 {
		return ShellV1Result{}, errors.New("shell terminal result exceeds the core canonical JSON bound")
	}
	if _, err := state.CompleteExecution(executionCtx, result.Status, resultJSON); err != nil {
		return ShellV1Result{}, errors.Join(append(orchestrationErrors, err)...)
	}
	// Once a canonical terminal result is committed, transient diagnostics do
	// not replace it with an MCP transport error. Unknown/failed status carries
	// the conservative outcome to the caller without leaking internal details.
	return result, nil
}

func (executor *ShellExecutor) executeManaged(
	_ context.Context,
	request ShellExecuteRequest,
	environment ResolvedEnvironment,
	identities ShellV1Identities,
	plan ShellV1Plan,
	state *shellExecutionState,
) (ShellV1Result, error) {
	if executor.config.BackendRouter == nil {
		result := newUnknownShellResult(plan.ProcessID)
		executionCtx, cancel := context.WithTimeout(executor.config.Lifecycle, shellCoreFinalizationGrace)
		defer cancel()
		closed, err := executor.closeWithoutStartExchange(executionCtx, state, plan, result,
			executionbackend.NewDispatchError(executionbackend.OutcomeNotSent, "backend_router_unavailable", errors.New("managed execution backend router is not configured")))
		executor.fenceManagedUnknown(executionCtx, request.Principal, environment.Target, "backend_router_unavailable")
		return closed, err
	}

	deadline := executor.config.Now().Add(time.Duration(plan.TimeoutMillis) * time.Millisecond)
	processDeadline := deadline.Add(executor.config.TerminalGrace)
	executionCtx, cancelExecution := context.WithDeadline(executor.config.Lifecycle, processDeadline.Add(shellCoreFinalizationGrace))
	defer cancelExecution()
	processCtx, cancelProcess := context.WithDeadline(executionCtx, processDeadline)
	defer cancelProcess()

	startOperation := backendOperationContext(request.Principal, plan.Start.Routing)
	environmentValues, err := injectManagedProcessEnvironment(processCtx, executor.config.ManagedEnvironmentIssuer,
		ManagedProcessEnvironmentRequest{
			Principal: request.Principal, Target: environment.Target, Operation: startOperation,
			ToolName: "shell", Executable: plan.Argv[0],
		}, plan.ExplicitEnvironment)
	if err != nil {
		result := newUnknownShellResult(plan.ProcessID)
		closed, closeErr := executor.closeWithoutStartExchange(executionCtx, state, plan, result, err)
		return closed, closeErr
	}
	startExchange, dispatchErr := executor.config.BackendRouter.StartProcess(processCtx, executionbackend.StartProcessRequest{
		Target: environment.Target, Operation: startOperation,
		RequestID: identities.StartRPCRequestID, ProcessID: plan.ProcessID,
		Executable: plan.Argv[0], Arguments: append([]string(nil), plan.Argv[1:]...),
		WorkingDirectory: plan.WorkingDirectory, WorkspaceRoot: plan.WorkspaceRoot,
		Platform: environment.Platform, Environment: environmentValues, TTY: plan.TTY,
		Timeout:          time.Duration(plan.TimeoutMillis) * time.Millisecond,
		OutputLimitBytes: int64(executor.config.MaxOutputBytes),
		DeadlineNotification: &executionbackend.DeadlineNotification{
			After:     time.Duration(plan.TimeoutMillis) * time.Millisecond,
			Operation: backendOperationContext(request.Principal, plan.Timeout.Routing),
			RequestID: identities.TimeoutRPCRequestID,
		},
	})
	if startExchange == nil {
		executor.logManagedDispatchFailure(request.Principal, environment.Target, startOperation, "start_dispatch", dispatchErr)
		result := newUnknownShellResult(plan.ProcessID)
		closed, closeErr := executor.closeWithoutStartExchange(executionCtx, state, plan, result, dispatchErr)
		executor.fenceManagedUnknown(executionCtx, request.Principal, environment.Target, "process_start_dispatch_unknown")
		return closed, closeErr
	}
	forceUnknown := dispatchErr != nil
	acknowledgement, ackErr := startExchange.AwaitAcknowledgement(processCtx)
	startAcknowledged := false
	var orchestrationErrors []error
	if ackErr != nil {
		executor.logManagedDispatchFailure(request.Principal, environment.Target, startOperation, "start_acknowledgement", ackErr)
		forceUnknown = true
		orchestrationErrors = append(orchestrationErrors, fmt.Errorf("await managed process acknowledgement: %w", ackErr))
	} else if acknowledgementJSON, marshalErr := marshalBackendAcknowledgement(identities.StartRPCRequestID, acknowledgement); marshalErr != nil {
		forceUnknown = true
		orchestrationErrors = append(orchestrationErrors, marshalErr)
	} else if _, acknowledgeErr := state.Acknowledge(executionCtx, plan.Start, acknowledgementJSON); acknowledgeErr != nil {
		forceUnknown = true
		orchestrationErrors = append(orchestrationErrors, acknowledgeErr)
	} else {
		startAcknowledged = true
	}

	evidenceCh := make(chan managedShellEvidence, 1)
	go func() {
		evidenceCh <- collectManagedShellEvidence(processCtx, startExchange, executor.config.MaxOutputBytes, executor.config.Now)
	}()

	timerWait := time.Until(deadline)
	if timerWait < 0 {
		timerWait = 0
	}
	timer := time.NewTimer(timerWait)
	defer timer.Stop()
	var evidence managedShellEvidence
	var timeoutBegan bool
	var timeoutStatus string
	var timeoutAcknowledged bool
	var timeoutResponseError bool
	timedOut := false
	beginTimeout := func() {
		timedOut = true
		begin, beginErr := state.BeginOperationDispatch(executionCtx, BeginOperationDispatchRequest{
			OperationID: plan.Timeout.OperationID, ExecutionID: plan.Timeout.Routing.ExecutionID,
			RunID: request.Principal.Run.RunID, RunAttemptID: request.Principal.Run.RunAttemptID,
			HolderID: request.Principal.Run.HolderID, RunAttemptGeneration: request.Principal.Run.RunAttemptGeneration,
			ConnectionGeneration: targetConnectionGeneration(environment.Target), Target: environment.Target,
			PolicyContext: plan.PolicyContext, OperationPlan: plan.OperationPlan, Params: plan.Timeout.Params,
		})
		if beginErr != nil {
			// The deadline has been reached, so a failed/ambiguous Begin must
			// never be projected as a successful process with a skipped timeout.
			forceUnknown = true
			orchestrationErrors = append(orchestrationErrors, beginErr)
			return
		}
		timeoutBegan = begin.Began
		if !timeoutBegan {
			// A non-begin result can describe an operation whose earlier Begin
			// already committed. Close a live operation conservatively as
			// unknown; a prepared/terminal result still forces the aggregate
			// away from success because this caller cannot prove termination
			// happened before the frozen deadline.
			switch state.OperationStatus(plan.Timeout.OperationID) {
			case "dispatching", "acknowledged":
				timeoutBegan = true
				timeoutStatus = "unknown"
				timeoutResponseError = true
			}
			forceUnknown = true
			return
		}
		timeoutStatus, timeoutAcknowledged, timeoutResponseError, err = executor.dispatchManagedTerminate(
			processCtx, executionCtx, request.Principal, environment.Target, identities.TimeoutRPCRequestID, plan, acknowledgement, state,
		)
		if err != nil {
			orchestrationErrors = append(orchestrationErrors, err)
		}
	}
	select {
	case evidence = <-evidenceCh:
		if evidence.err == nil && !evidence.observedAt.IsZero() && evidence.observedAt.Before(deadline) {
			if _, skipErr := state.SkipTimeoutIfPrepared(executionCtx, "process_terminal_before_deadline"); skipErr != nil {
				orchestrationErrors = append(orchestrationErrors, skipErr)
			}
		} else if !evidence.observedAt.IsZero() && !evidence.observedAt.Before(deadline) {
			// Selecting the terminal channel does not prove it won the deadline:
			// the terminal observation carries the authoritative local time. Route
			// an at/after-deadline terminal through the exact same timeout path.
			beginTimeout()
		}
	case <-timer.C:
		beginTimeout()
		evidence = <-evidenceCh
	}
	if evidence.err != nil {
		forceUnknown = true
		orchestrationErrors = append(orchestrationErrors, evidence.err)
	}

	startStatus := managedShellStartStatus(startAcknowledged, forceUnknown, evidence)
	startResultJSON, err := json.Marshal(shellOperationTerminalResult{
		Kind: ShellV1OperationProcessStart, ProcessID: plan.ProcessID, Status: startStatus,
		Acknowledged: startAcknowledged, ResponseError: evidence.responseError,
		ExitCode: evidence.exitCode, SandboxDenied: evidence.sandboxDenied,
		OutputComplete: evidence.outputComplete(), LastSequence: evidence.nextSequence - 1,
	})
	if err != nil {
		return ShellV1Result{}, err
	}
	if _, err := state.CompleteOperation(executionCtx, plan.Start, startStatus, startResultJSON); err != nil {
		return ShellV1Result{}, errors.Join(append(orchestrationErrors, err)...)
	}

	if timeoutBegan {
		if timeoutStatus == "" {
			timeoutStatus = "unknown"
		}
		// A confirmed deadline termination is intentionally a failed shell
		// operation, matching the existing agentx terminal contract.
		if timeoutStatus == "succeeded" {
			if evidence.outputComplete() {
				timeoutStatus = "failed"
			} else {
				timeoutStatus = "unknown"
			}
		}
		timeoutResultJSON, marshalErr := json.Marshal(shellOperationTerminalResult{
			Kind: ShellV1OperationTimeoutTerminate, ProcessID: plan.ProcessID, Status: timeoutStatus,
			Acknowledged: timeoutAcknowledged, ResponseError: timeoutResponseError,
			ExitCode: evidence.exitCode, SandboxDenied: evidence.sandboxDenied,
			OutputComplete: evidence.outputComplete(), LastSequence: evidence.nextSequence - 1,
		})
		if marshalErr != nil {
			return ShellV1Result{}, marshalErr
		}
		if _, completeErr := state.CompleteOperation(executionCtx, plan.Timeout, timeoutStatus, timeoutResultJSON); completeErr != nil {
			return ShellV1Result{}, errors.Join(append(orchestrationErrors, completeErr)...)
		}
	} else if state.OperationStatus(plan.Timeout.OperationID) == "prepared" {
		if _, skipErr := state.SkipTimeoutIfPrepared(executionCtx, "process_terminal_without_timeout_dispatch"); skipErr != nil {
			return ShellV1Result{}, errors.Join(append(orchestrationErrors, skipErr)...)
		}
	}

	result := ShellV1Result{
		ProcessID: plan.ProcessID, Chunks: evidence.chunks, NextSequence: evidence.nextSequence,
		ExitCode: evidence.exitCode, SandboxDenied: evidence.sandboxDenied,
		TimedOut: timedOut, OutputComplete: evidence.outputComplete(),
	}
	result.Status = aggregateShellResultStatus(state.OperationStatus(plan.Start.OperationID), state.OperationStatus(plan.Timeout.OperationID))
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return ShellV1Result{}, err
	}
	if len(resultJSON) > 1024*1024 {
		return ShellV1Result{}, errors.New("shell terminal result exceeds the core canonical JSON bound")
	}
	if _, err := state.CompleteExecution(executionCtx, result.Status, resultJSON); err != nil {
		return ShellV1Result{}, errors.Join(append(orchestrationErrors, err)...)
	}
	if result.Status == "unknown" || (timeoutBegan && timeoutStatus == "unknown") {
		executor.fenceManagedUnknown(executionCtx, request.Principal, environment.Target, "managed_shell_outcome_unknown")
	}
	return result, nil
}

func (executor *ShellExecutor) logManagedDispatchFailure(
	principal ExecutorMCPPrincipal,
	target executionbackend.Target,
	operation executionbackend.OperationContext,
	stage string,
	err error,
) {
	if executor == nil || executor.config.Logger == nil {
		return
	}
	var dispatchError *executionbackend.DispatchError
	if !errors.As(err, &dispatchError) || dispatchError == nil {
		return
	}
	executor.config.Logger.Error("managed shell dispatch failed",
		"workspace_id", principal.WorkspaceID,
		"run_id", operation.RunID,
		"run_attempt_id", operation.RunAttemptID,
		"execution_id", operation.ExecutionID,
		"operation_id", operation.OperationID,
		"target_id", target.ID,
		"target_generation", target.Generation,
		"dispatch_stage", stage,
		"dispatch_outcome", dispatchError.Outcome,
		"reason_code", dispatchError.Code,
		"provider_http_status", dispatchError.HTTPStatus,
		"provider_code", dispatchError.ProviderCode,
		"provider_request_id", dispatchError.ProviderRequestID,
		"request_written", dispatchError.RequestWritten,
	)
}

func (executor *ShellExecutor) dispatchManagedTerminate(
	processCtx context.Context,
	executionCtx context.Context,
	principal ExecutorMCPPrincipal,
	target executionbackend.Target,
	requestID string,
	plan ShellV1Plan,
	startAcknowledgement executionbackend.Acknowledgement,
	state *shellExecutionState,
) (string, bool, bool, error) {
	exchange, dispatchErr := executor.config.BackendRouter.SignalProcess(processCtx, executionbackend.SignalProcessRequest{
		Target: target, Operation: backendOperationContext(principal, plan.Timeout.Routing),
		RequestID: requestID, ProcessID: plan.ProcessID,
		ProviderHandle: startAcknowledgement.ProviderOperationID,
		Signal:         executionbackend.SignalTerminate, Reason: "shell deadline reached",
	})
	if exchange == nil {
		return "unknown", false, true, dispatchErr
	}
	acknowledgement, err := exchange.AwaitAcknowledgement(processCtx)
	if err != nil {
		return "unknown", false, true, errors.Join(dispatchErr, err)
	}
	ackJSON, err := marshalBackendAcknowledgement(requestID, acknowledgement)
	if err != nil {
		return "unknown", false, true, err
	}
	if _, err := state.Acknowledge(executionCtx, plan.Timeout, ackJSON); err != nil {
		return "unknown", false, true, err
	}
	terminal, err := exchange.AwaitTerminal(processCtx)
	if err != nil || dispatchErr != nil {
		return "unknown", true, true, errors.Join(dispatchErr, err)
	}
	switch terminal.Status {
	case executionbackend.TerminalSucceeded:
		return "succeeded", true, false, nil
	case executionbackend.TerminalFailed, executionbackend.TerminalCancelled:
		return "failed", true, true, nil
	default:
		return "unknown", true, true, nil
	}
}

func (executor *ShellExecutor) fenceManagedUnknown(ctx context.Context, principal ExecutorMCPPrincipal, target executionbackend.Target, reason string) {
	if executor.config.ManagedTargetFencer != nil {
		_ = executor.config.ManagedTargetFencer.FenceManagedTarget(ctx, principal, target, reason)
	}
}

type managedShellEvidence struct {
	chunks        []ShellV1OutputChunk
	nextSequence  uint64
	exitCode      *int32
	sandboxDenied bool
	responseError bool
	terminal      *executionbackend.TerminalResult
	overflow      bool
	observedAt    time.Time
	err           error
}

func (evidence managedShellEvidence) outputComplete() bool {
	return evidence.terminal != nil && evidence.terminal.OutputComplete && !evidence.overflow && evidence.err == nil
}

func collectManagedShellEvidence(ctx context.Context, exchange executionbackend.Exchange, maxOutputBytes int, now func() time.Time) managedShellEvidence {
	evidence := managedShellEvidence{chunks: []ShellV1OutputChunk{}, nextSequence: 1}
	retainedBytes := 0
	for {
		event, err := exchange.NextEvent(ctx)
		if errors.Is(err, io.EOF) {
			terminal, terminalErr := exchange.AwaitTerminal(ctx)
			if terminalErr != nil {
				evidence.err = fmt.Errorf("await managed process terminal: %w", terminalErr)
				return evidence
			}
			evidence.terminal = &terminal
			evidence.exitCode = terminal.ExitCode
			evidence.sandboxDenied = terminal.ReasonCode == "sandbox_denied"
			evidence.responseError = terminal.Status == executionbackend.TerminalFailed || terminal.Status == executionbackend.TerminalCancelled
			evidence.observedAt = now()
			return evidence
		}
		if err != nil {
			evidence.err = fmt.Errorf("collect managed process events: %w", err)
			return evidence
		}
		if event.Sequence != evidence.nextSequence {
			evidence.err = errors.New("managed process event sequence differs from the backend exchange")
			return evidence
		}
		evidence.nextSequence++
		stream := ""
		switch event.Kind {
		case executionbackend.EventStdout:
			stream = "stdout"
		case executionbackend.EventStderr:
			stream = "stderr"
		default:
			evidence.err = fmt.Errorf("managed process returned unsupported event kind %q", event.Kind)
			return evidence
		}
		encoded := base64.StdEncoding.EncodeToString(event.Data)
		projectedBytes := len(encoded) + len(stream) + 64
		if len(evidence.chunks) == maximumShellOutputChunks || projectedBytes > maxOutputBytes-retainedBytes {
			evidence.overflow = true
			continue
		}
		retainedBytes += projectedBytes
		evidence.chunks = append(evidence.chunks, ShellV1OutputChunk{
			Sequence: event.Sequence, Stream: stream, ChunkBase64: encoded,
		})
	}
}

func managedShellStartStatus(acknowledged, forceUnknown bool, evidence managedShellEvidence) string {
	if forceUnknown || !acknowledged || !evidence.outputComplete() {
		return "unknown"
	}
	switch evidence.terminal.Status {
	case executionbackend.TerminalSucceeded:
		if evidence.exitCode != nil && *evidence.exitCode == 0 && !evidence.sandboxDenied {
			return "succeeded"
		}
		return "failed"
	case executionbackend.TerminalFailed, executionbackend.TerminalCancelled:
		return "failed"
	default:
		return "unknown"
	}
}

func marshalBackendAcknowledgement(requestID string, acknowledgement executionbackend.Acknowledgement) (json.RawMessage, error) {
	if err := acknowledgement.Validate(); err != nil {
		return nil, fmt.Errorf("validate backend acknowledgement: %w", err)
	}
	return json.Marshal(struct {
		Version             string    `json:"version"`
		RequestID           string    `json:"requestId"`
		ProviderOperationID string    `json:"providerOperationId,omitempty"`
		ProviderRequestID   string    `json:"providerRequestId,omitempty"`
		AcceptedAt          time.Time `json:"acceptedAt"`
	}{
		Version: "execution-backend-ack-v1", RequestID: requestID,
		ProviderOperationID: acknowledgement.ProviderOperationID,
		ProviderRequestID:   acknowledgement.ProviderRequestID,
		AcceptedAt:          acknowledgement.AcceptedAt,
	})
}

func (executor *ShellExecutor) closeWithoutStartExchange(ctx context.Context, state *shellExecutionState, plan ShellV1Plan, result ShellV1Result, dispatchErr error) (ShellV1Result, error) {
	operationResult, err := json.Marshal(shellOperationTerminalResult{
		Kind: ShellV1OperationProcessStart, ProcessID: plan.ProcessID, Status: "unknown",
		OutputComplete: false,
	})
	if err != nil {
		return ShellV1Result{}, err
	}
	if _, err := state.CompleteOperation(ctx, plan.Start, "unknown", operationResult); err != nil {
		return ShellV1Result{}, errors.Join(dispatchErr, err)
	}
	if _, err := state.SkipTimeoutIfPrepared(ctx, "process_start_dispatch_failed"); err != nil {
		return ShellV1Result{}, errors.Join(dispatchErr, err)
	}
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return ShellV1Result{}, err
	}
	if _, err := state.CompleteExecution(ctx, "unknown", resultJSON); err != nil {
		return ShellV1Result{}, errors.Join(dispatchErr, err)
	}
	return result, nil
}

type shellTimeoutOutcome struct {
	result ProcessTimeoutDispatchResult
	err    error
}

type shellProcessEvidence struct {
	chunks        []ShellV1OutputChunk
	nextSequence  uint64
	exitCode      *int32
	sandboxDenied bool
	exited        bool
	closed        bool
	overflow      bool
	observedAt    time.Time
	err           error
}

func (evidence shellProcessEvidence) outputComplete() bool {
	return evidence.closed && !evidence.overflow && evidence.err == nil
}

func collectShellProcessEvidence(ctx context.Context, exchange *ProcessExchange, processID string, maxOutputBytes int, now func() time.Time) shellProcessEvidence {
	evidence := shellProcessEvidence{chunks: []ShellV1OutputChunk{}, nextSequence: 1}
	retainedBytes := 0
	for {
		raw, err := exchange.NextEvent(ctx)
		if errors.Is(err, io.EOF) {
			if evidence.closed {
				_, evidence.observedAt = processExchangeTerminal(exchange)
				if evidence.observedAt.IsZero() {
					evidence.observedAt = now()
				}
			}
			return evidence
		}
		if err != nil {
			evidence.err = fmt.Errorf("collect process events: %w", err)
			return evidence
		}
		message, err := codexwire.Parse(raw)
		if err != nil || message.Kind != codexwire.KindNotification {
			evidence.err = errors.New("retained process event is not a notification")
			return evidence
		}
		switch message.Method {
		case execprofile.NotificationProcessOutput:
			var params struct {
				ProcessID string `json:"processId"`
				Sequence  uint64 `json:"seq"`
				Stream    string `json:"stream"`
				Chunk     string `json:"chunk"`
			}
			if err := decodeExactJSON(message.Params, &params); err != nil || params.ProcessID != processID || params.Sequence != evidence.nextSequence {
				evidence.err = errors.New("retained process/output differs from the shell exchange")
				return evidence
			}
			evidence.nextSequence++
			projectedBytes := len(params.Chunk) + len(params.Stream) + 64
			if len(evidence.chunks) == maximumShellOutputChunks || projectedBytes > maxOutputBytes-retainedBytes {
				evidence.overflow = true
				continue
			}
			retainedBytes += projectedBytes
			evidence.chunks = append(evidence.chunks, ShellV1OutputChunk{Sequence: params.Sequence, Stream: params.Stream, ChunkBase64: params.Chunk})
		case execprofile.NotificationProcessExited:
			var params struct {
				ProcessID     string `json:"processId"`
				Sequence      uint64 `json:"seq"`
				ExitCode      int32  `json:"exitCode"`
				SandboxDenied *bool  `json:"sandboxDenied"`
			}
			if err := decodeExactJSON(message.Params, &params); err != nil || params.ProcessID != processID ||
				params.Sequence != evidence.nextSequence || params.SandboxDenied == nil || evidence.exited {
				evidence.err = errors.New("retained process/exited differs from the shell exchange")
				return evidence
			}
			evidence.nextSequence++
			exitCode := params.ExitCode
			evidence.exitCode = &exitCode
			evidence.sandboxDenied = *params.SandboxDenied
			evidence.exited = true
		case execprofile.NotificationProcessClosed:
			var params struct {
				ProcessID string `json:"processId"`
				Sequence  uint64 `json:"seq"`
			}
			if err := decodeExactJSON(message.Params, &params); err != nil || params.ProcessID != processID ||
				params.Sequence != evidence.nextSequence || !evidence.exited || evidence.closed {
				evidence.err = errors.New("retained process/closed differs from the shell exchange")
				return evidence
			}
			evidence.nextSequence++
			evidence.closed = true
		default:
			evidence.err = fmt.Errorf("retained unsupported process notification %q", message.Method)
			return evidence
		}
	}
}

func classifyShellProcessResponse(raw json.RawMessage, method, processID string) (succeeded bool, rpcError bool, err error) {
	message, err := codexwire.Parse(raw)
	if err != nil {
		return false, false, fmt.Errorf("parse %s response: %w", method, err)
	}
	switch message.Kind {
	case codexwire.KindError:
		return false, true, nil
	case codexwire.KindResponse:
		if method == execprofile.MethodProcessStart {
			var result struct {
				ProcessID string `json:"processId"`
			}
			if err := decodeExactJSON(message.Result, &result); err != nil || result.ProcessID != processID {
				return false, false, errors.New("process/start response does not match the shell process")
			}
		} else {
			var result struct{}
			if err := decodeExactJSON(message.Result, &result); err != nil {
				return false, false, errors.New("process/terminate response result is not empty")
			}
		}
		return true, false, nil
	default:
		return false, false, fmt.Errorf("%s did not return a response or error", method)
	}
}

func shellStartTerminalStatus(acknowledged, responseSucceeded, responseError, forceUnknown bool, evidence shellProcessEvidence) string {
	if forceUnknown || !acknowledged || evidence.err != nil || evidence.overflow {
		return "unknown"
	}
	if responseError {
		return "failed"
	}
	if !responseSucceeded || !evidence.outputComplete() || evidence.exitCode == nil {
		return "unknown"
	}
	if *evidence.exitCode == 0 && !evidence.sandboxDenied {
		return "succeeded"
	}
	return "failed"
}

func aggregateShellResultStatus(startStatus, timeoutStatus string) string {
	if startStatus == "unknown" || timeoutStatus == "unknown" {
		return "unknown"
	}
	if startStatus == "failed" || timeoutStatus == "failed" {
		return "failed"
	}
	if startStatus == "succeeded" && (timeoutStatus == "succeeded" || timeoutStatus == "skipped") {
		return "succeeded"
	}
	return "unknown"
}

func newUnknownShellResult(processID string) ShellV1Result {
	return ShellV1Result{
		ProcessID: processID, Status: "unknown", Chunks: []ShellV1OutputChunk{},
		NextSequence: 1, OutputComplete: false,
	}
}

type shellOperationTerminalResult struct {
	Kind           string `json:"kind"`
	ProcessID      string `json:"processId"`
	Status         string `json:"status"`
	Acknowledged   bool   `json:"acknowledged"`
	ResponseError  bool   `json:"responseError"`
	ExitCode       *int32 `json:"exitCode"`
	SandboxDenied  bool   `json:"sandboxDenied"`
	OutputComplete bool   `json:"outputComplete"`
	LastSequence   uint64 `json:"lastSequence"`
}
