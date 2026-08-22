package harnessworker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/agentserver/agentserver/v2/internal/checkpoint"
	"github.com/agentserver/agentserver/v2/internal/codexwire"
	"github.com/agentserver/agentserver/v2/internal/harnesscontrol"
	"github.com/agentserver/agentserver/v2/internal/runmanifest"
	"github.com/agentserver/agentserver/v2/internal/safediagnostic"
)

const (
	defaultWorkerPendingCalls = 64
	workerMaxEventBytes       = 1024 * 1024
)

var (
	ErrWorkerRunDurationExceeded    = errors.New("signed worker run duration exceeded")
	ErrAppServerShutdownUnconfirmed = errors.New("stock app-server shutdown was not confirmed")
)

// AppServerNotificationHandler consumes validated stock app-server
// notifications. It is synchronous and must return only after the notification
// has crossed the caller's bounded delivery boundary. RunOneShotWorker keeps
// draining after the first handler failure while it interrupts the turn.
type AppServerNotificationHandler func(context.Context, codexwire.Message) error

// PreparedWorkerRuntime is an attempt-local staging boundary. CheckpointRoots
// returns worker-readable fresh directories. Finalize moves only the verified
// rollout, writes deployment-owned Codex configuration, and returns the
// app-identity runtime used by stock app-server.
type PreparedWorkerRuntime interface {
	CheckpointRoots() (codexHome, stagingRoot string)
	Finalize(context.Context, runmanifest.Manifest, *RestoredCheckpoint) (PreparedAppServerRuntime, error)
	Close() error
}

// WorkerRuntimePreparer creates one fresh runtime after the signed manifest is
// verified and before any control, MCP, or app-server goroutine is started.
type WorkerRuntimePreparer interface {
	Prepare(context.Context, runmanifest.Manifest) (PreparedWorkerRuntime, error)
}

type PreparedAppServerRuntime struct {
	ProcessConfig AppServerProcessConfig
	ThreadCWD     string
	RolloutPath   string
}

// OneShotWorkerConfig contains only inherited one-shot authority, deployment
// facts, and explicit bounded sinks. Endpoints, model selection, catalog, and
// timeouts still come exclusively from the verified run manifest.
type OneShotWorkerConfig struct {
	BootstrapPipe  *os.File
	PromptPipe     *os.File
	CheckpointPipe *os.File

	VerificationKeyring *runmanifest.VerificationKeyring
	RuntimePreparer     WorkerRuntimePreparer
	ControlHTTPClient   *http.Client
	ExecutorHTTPClient  *http.Client
	BaseInstructions    string
	Logger              *slog.Logger

	WorkerInstanceIDGenerator WorkerInstanceIDGenerator
	ProgressHandler           ProgressHandler
	NotificationHandler       AppServerNotificationHandler
	ClientInfo                AppServerClientInfo
}

type oneShotWorkerControl interface {
	AppServerLifecycleSink
	Start(context.Context) error
	Interrupts() <-chan harnesscontrol.InterruptCommand
	AwaitApproval(context.Context, ElicitationRequest) (ElicitationDecision, error)
	SendAppServerNotification(context.Context, codexwire.Message) error
	SendExecutorMCPProgress(context.Context, ProgressEvent) error
	SendTurnTerminal(context.Context, harnesscontrol.TurnTerminalEvent) error
	Wait(context.Context) error
	Close(error)
}

type workerRuntimeEventSink interface {
	SendAppServerNotification(context.Context, codexwire.Message) error
	SendExecutorMCPProgress(context.Context, ProgressEvent) error
}

type oneShotWorkerMCP interface {
	DynamicToolCaller
	Catalog() *Catalog
	Close() error
}

type oneShotWorkerProcess interface {
	AppServerTransport
	CloseStdin() error
	Wait(context.Context) error
}

type appServerStderrSource interface {
	Stderr() (contents []byte, truncated bool)
}

type oneShotWorkerRunner interface {
	ConsumeEvents(func(codexwire.Message)) error
	Run(context.Context, AppServerRunRequest) (AppServerRunResult, error)
}

type oneShotWorkerDependencies struct {
	newControl   func(WorkerControlClientConfig) (oneShotWorkerControl, error)
	connectMCP   func(context.Context, MCPClientConfig) (oneShotWorkerMCP, error)
	startProcess func(context.Context, AppServerProcessConfig) (oneShotWorkerProcess, error)
	newRunner    func(AppServerTransport, *DynamicBridge, AppServerRunnerOptions) (oneShotWorkerRunner, error)
}

func defaultOneShotWorkerDependencies() oneShotWorkerDependencies {
	return oneShotWorkerDependencies{
		newControl: func(config WorkerControlClientConfig) (oneShotWorkerControl, error) {
			return NewWorkerControlClient(config)
		},
		connectMCP: func(ctx context.Context, config MCPClientConfig) (oneShotWorkerMCP, error) {
			return ConnectMCP(ctx, config)
		},
		startProcess: func(ctx context.Context, config AppServerProcessConfig) (oneShotWorkerProcess, error) {
			return StartAppServerProcess(ctx, config)
		},
		newRunner: func(peer AppServerTransport, bridge *DynamicBridge, options AppServerRunnerOptions) (oneShotWorkerRunner, error) {
			return NewAppServerRunner(peer, bridge, options)
		},
	}
}

// RunOneShotWorker consumes the inherited launch inputs and owns exactly one
// stock app-server turn. A successfully acknowledged terminal is the return
// boundary even when that terminal is interrupted or failed; the holder owns
// the durable classification. Errors are reserved for attempts whose terminal
// could not be authoritatively delivered.
func RunOneShotWorker(ctx context.Context, config OneShotWorkerConfig) error {
	return runOneShotWorker(ctx, config, defaultOneShotWorkerDependencies())
}

func runOneShotWorker(ctx context.Context, config OneShotWorkerConfig, dependencies oneShotWorkerDependencies) error {
	if err := validateOneShotWorkerConfig(ctx, config, dependencies); err != nil {
		closeErr := closeUnconsumedWorkerInputs(config.BootstrapPipe, config.PromptPipe, config.CheckpointPipe)
		return errors.Join(err, closeErr)
	}

	bootstrap, err := LoadBootstrap(config.BootstrapPipe, config.VerificationKeyring, config.WorkerInstanceIDGenerator)
	config.BootstrapPipe = nil
	if err != nil {
		return errors.Join(err, closeUnconsumedWorkerInputs(config.PromptPipe, config.CheckpointPipe))
	}
	baseInstructions, err := authorizedBaseInstructions(bootstrap.Manifest, config.BaseInstructions)
	if err != nil {
		return errors.Join(err, closeUnconsumedWorkerInputs(config.PromptPipe, config.CheckpointPipe))
	}
	runDuration := time.Duration(bootstrap.Manifest.Limits.MaxRunDurationMS) * time.Millisecond
	timeoutCtx, cancelTimeout := context.WithTimeoutCause(ctx, runDuration, ErrWorkerRunDurationExceeded)
	defer cancelTimeout()
	runCtx, cancelRun := context.WithCancelCause(timeoutCtx)
	defer cancelRun(nil)

	prompt, err := LoadPrompt(config.PromptPipe, bootstrap.Manifest.Prompt)
	config.PromptPipe = nil
	if err != nil {
		return errors.Join(err, closeUnconsumedWorkerInputs(config.CheckpointPipe))
	}

	runtime, err := config.RuntimePreparer.Prepare(runCtx, bootstrap.Manifest)
	if err != nil {
		return errors.Join(fmt.Errorf("prepare worker runtime: %w", err), closeUnconsumedWorkerInputs(config.CheckpointPipe))
	}
	if runtime == nil {
		return errors.Join(errors.New("worker runtime preparer returned nil"), closeUnconsumedWorkerInputs(config.CheckpointPipe))
	}
	runtimeClosed := false
	closeRuntime := func() error {
		if runtimeClosed {
			return nil
		}
		runtimeClosed = true
		return runtime.Close()
	}
	defer closeRuntime()

	checkpointHome, checkpointStaging := runtime.CheckpointRoots()
	restored, err := LoadCheckpoint(config.CheckpointPipe, bootstrap.Manifest, checkpointHome, checkpointStaging)
	config.CheckpointPipe = nil
	if err != nil {
		return fmt.Errorf("load worker checkpoint: %w", err)
	}
	appRuntime, err := runtime.Finalize(runCtx, bootstrap.Manifest, restored)
	if err != nil {
		return fmt.Errorf("finalize worker runtime: %w", err)
	}
	if err := validatePreparedAppServerRuntime(appRuntime, bootstrap.Manifest, restored); err != nil {
		return err
	}

	controlConfig := DefaultWorkerControlClientConfig(
		bootstrap.Manifest,
		bootstrap.SignedManifest,
		bootstrap.ControlCapability,
		bootstrap.WorkerInstanceID,
		config.ControlHTTPClient,
	)
	control, err := dependencies.newControl(controlConfig)
	if err != nil {
		return fmt.Errorf("create worker control client: %w", err)
	}
	if control == nil {
		return errors.New("worker control factory returned nil")
	}
	// The control stream is the cleanup evidence path for a cancelled turn, so
	// it must outlive runCtx. In particular, a holder interrupt cancels runCtx
	// to drive turn/interrupt and MCP cleanup, then the worker still has to send
	// turn_terminal(interrupted) and wait for its cumulative ACK. The stream is
	// bounded by explicit close below rather than by the turn context.
	controlLifetimeCtx, cancelControlLifetime := context.WithCancelCause(context.Background())
	controlClosed := false
	closeControl := func(cause error) {
		if !controlClosed {
			controlClosed = true
			if cause == nil {
				cause = context.Canceled
			}
			cancelControlLifetime(cause)
			control.Close(cause)
		}
	}
	defer closeControl(errors.New("one-shot worker ended"))
	if err := control.Start(controlLifetimeCtx); err != nil {
		return fmt.Errorf("start worker control client: %w", err)
	}
	runtimeEvents := newWorkerRuntimeEventForwarder(control, config.NotificationHandler, config.ProgressHandler)

	watchCtx, stopWatchers := context.WithCancel(context.Background())
	defer stopWatchers()
	go watchWorkerInterrupts(watchCtx, control.Interrupts(), cancelRun)
	go watchWorkerControl(watchCtx, control, cancelRun)

	limits := DefaultLimits()
	mcp, err := dependencies.connectMCP(runCtx, MCPClientConfig{
		Endpoint:              bootstrap.Manifest.ExecutorMCP.Endpoint,
		TLSIdentity:           bootstrap.Manifest.ExecutorMCP.TLSIdentity,
		BearerToken:           bootstrap.ExecutorMCPCapability,
		HTTPClient:            config.ExecutorHTTPClient,
		Namespace:             bootstrap.Manifest.ExecutorMCP.Namespace,
		NamespaceDescription:  bootstrap.Manifest.ExecutorMCP.NamespaceDescription,
		ExpectedCatalogDigest: bootstrap.Manifest.ExecutorMCP.CatalogDigest,
		ExpectedCatalog:       bootstrap.Manifest.ExecutorMCP.CanonicalCatalog,
		Limits:                limits,
		ElicitationHandler:    control.AwaitApproval,
		ProgressHandler:       runtimeEvents.HandleProgress,
		CloseGrace:            time.Duration(bootstrap.Manifest.Limits.MCPTransportGraceMS) * time.Millisecond,
		ApprovalOutcomeGrace:  time.Duration(bootstrap.Manifest.Limits.MCPTransportGraceMS) * time.Millisecond,
	})
	if err != nil {
		return fmt.Errorf("connect worker executor MCP: %w", err)
	}
	if mcp == nil || mcp.Catalog() == nil {
		if mcp != nil {
			_ = mcp.Close()
		}
		return errors.New("worker MCP factory returned no verified catalog")
	}
	mcpClosed := false
	closeMCP := func() error {
		if mcpClosed {
			return nil
		}
		mcpClosed = true
		return mcp.Close()
	}
	defer closeMCP()

	bridge, err := NewDynamicBridge(mcp, defaultWorkerPendingCalls, limits.MaxArgumentBytes)
	if err != nil {
		return err
	}
	processConfig := appRuntime.ProcessConfig
	processConfig.Environment.ModelCapability = bootstrap.LLMProxyCapability
	process, err := dependencies.startProcess(runCtx, processConfig)
	if err != nil {
		return fmt.Errorf("start stock app-server: %w", err)
	}
	if process == nil {
		return errors.New("app-server process factory returned nil")
	}

	lifecycle := &recordingWorkerLifecycle{sink: control}
	runnerOptions := workerRunnerOptions(bootstrap.Manifest, lifecycle)
	runner, err := dependencies.newRunner(process, bridge, runnerOptions)
	if err != nil {
		bridge.Close(err)
		return errors.Join(err, closeWorkerProcess(process, bootstrap.Manifest.Limits.WorkerCallbackGraceMS))
	}
	if runner == nil {
		bridge.Close(errors.New("app-server runner factory returned nil"))
		return errors.Join(errors.New("app-server runner factory returned nil"), closeWorkerProcess(process, bootstrap.Manifest.Limits.WorkerCallbackGraceMS))
	}

	eventErr := make(chan error, 1)
	go consumeAppServerNotifications(runCtx, runner.ConsumeEvents, runtimeEvents.HandleNotification, cancelRun, eventErr)
	request := appServerRequest(bootstrap.Manifest, prompt, baseInstructions, mcp.Catalog(), appRuntime, restored, config.ClientInfo)
	result, runnerErr := runner.Run(runCtx, request)
	notificationErr := <-eventErr

	closeStdinErr := process.CloseStdin()
	waitCtx, cancelWait := context.WithTimeout(
		context.Background(),
		time.Duration(bootstrap.Manifest.Limits.WorkerCallbackGraceMS)*time.Millisecond,
	)
	waitErr := process.Wait(waitCtx)
	cancelWait()
	appServerStderr, appServerStderrTruncated := appServerProcessStderr(process)
	mcpErr := closeMCP()
	runtimeErr := closeRuntime()
	cleanupFailures := workerCleanupFailures{
		runner: runnerErr, notification: notificationErr, closeStdin: closeStdinErr,
		processWait: waitErr, mcp: mcpErr, runtime: runtimeErr,
	}
	cleanupErr := cleanupFailures.joined()
	if errors.Is(waitErr, context.DeadlineExceeded) || errors.Is(waitErr, context.Canceled) {
		// A terminal event is not a substitute for process cleanup. Returning
		// closes the worker control connection and lets the holder kill and
		// verify the entire attempt process group; it must classify the already
		// accepted turn as interrupted rather than accepting a fabricated worker
		// terminal while app-server may still be alive.
		cleanupErr = errors.Join(ErrAppServerShutdownUnconfirmed, cleanupErr)
		logWorkerFailureDiagnostics(
			context.WithoutCancel(ctx), config.Logger, bootstrap.Manifest,
			"process_shutdown", "", "", result, cleanupFailures,
			context.Cause(runCtx), appServerStderr, appServerStderrTruncated,
			harnesscontrol.TurnTerminalEvent{Status: "failed", ErrorCode: "app_server_shutdown_unconfirmed"},
			ErrAppServerShutdownUnconfirmed,
		)
		closeControl(cleanupErr)
		return cleanupErr
	}

	threadID, turnID, accepted := lifecycle.Accepted()
	if !accepted {
		if cleanupErr == nil {
			cleanupErr = errors.New("app-server ended before authoritative turn acceptance")
		}
		logWorkerFailureDiagnostics(
			context.WithoutCancel(ctx), config.Logger, bootstrap.Manifest,
			"before_turn_acceptance", threadID, turnID, result, cleanupFailures,
			context.Cause(runCtx), appServerStderr, appServerStderrTruncated,
			harnesscontrol.TurnTerminalEvent{Status: "failed", ErrorCode: "turn_not_accepted"},
			cleanupErr,
		)
		closeControl(cleanupErr)
		return cleanupErr
	}
	terminal := classifyWorkerTerminal(
		threadID, turnID, result, cleanupErr, context.Cause(runCtx),
		appServerStderr, appServerStderrTruncated,
	)
	if terminal.ErrorCode == "worker_runtime_failed" {
		terminal.ErrorMessage = workerRuntimeFailureMessage(
			cleanupFailures, context.Cause(runCtx), result.Terminal.Turn.Status,
			appServerStderr, appServerStderrTruncated,
		)
	}
	var terminalCause error
	if terminal.Status == "completed" {
		locator, err := completedRolloutLocator(appRuntime, result)
		if err != nil {
			terminal.Status = "interrupted"
			terminal.ErrorCode = "checkpoint_locator_invalid"
			terminal.ErrorMessage = "the completed turn did not yield an authorized rollout locator"
			terminalCause = err
		} else {
			terminal.RolloutLocator = locator
		}
	}
	if terminal.Status == "failed" || cleanupErr != nil || terminalCause != nil {
		logWorkerFailureDiagnostics(
			context.WithoutCancel(ctx), config.Logger, bootstrap.Manifest,
			"turn_terminal", threadID, turnID, result, cleanupFailures,
			context.Cause(runCtx), appServerStderr, appServerStderrTruncated,
			terminal, terminalCause,
		)
	}
	terminalCtx, cancelTerminal := context.WithTimeout(
		context.Background(),
		time.Duration(bootstrap.Manifest.Limits.WorkerCallbackGraceMS)*time.Millisecond,
	)
	defer cancelTerminal()
	if err := control.SendTurnTerminal(terminalCtx, terminal); err != nil {
		closeControl(err)
		return errors.Join(cleanupErr, fmt.Errorf("send worker terminal: %w", err))
	}
	if err := control.Wait(terminalCtx); err != nil {
		closeControl(err)
		return errors.Join(cleanupErr, fmt.Errorf("finish worker control terminal: %w", err))
	}
	stopWatchers()
	cancelControlLifetime(errors.New("worker terminal was acknowledged"))
	controlClosed = true
	return nil
}

func completedRolloutLocator(runtime PreparedAppServerRuntime, result AppServerRunResult) (string, error) {
	codexHome := runtime.ProcessConfig.Environment.CodexHome
	rolloutPath := result.Thread.Thread.Path
	if err := validateAbsolutePath("configured app-server Codex home", codexHome); err != nil {
		return "", err
	}
	if filepath.Clean(codexHome) != codexHome {
		return "", errors.New("configured app-server Codex home is not clean")
	}
	if result.Initialize.CodexHome != codexHome {
		return "", errors.New("app-server initialize Codex home changed the prepared runtime boundary")
	}
	if err := validateAbsolutePath("completed thread rollout path", rolloutPath); err != nil {
		return "", err
	}
	if filepath.Clean(rolloutPath) != rolloutPath {
		return "", errors.New("completed thread rollout path is not clean")
	}
	relative, err := filepath.Rel(codexHome, rolloutPath)
	if err != nil {
		return "", fmt.Errorf("resolve completed rollout locator: %w", err)
	}
	locator := filepath.ToSlash(relative)
	if err := checkpoint.ValidateRolloutLocator(locator); err != nil {
		return "", err
	}
	if filepath.Clean(filepath.Join(codexHome, filepath.FromSlash(locator))) != rolloutPath {
		return "", errors.New("completed rollout locator does not round-trip beneath Codex home")
	}
	return locator, nil
}

func validateOneShotWorkerConfig(ctx context.Context, config OneShotWorkerConfig, dependencies oneShotWorkerDependencies) error {
	if ctx == nil {
		return errors.New("one-shot worker context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if config.BootstrapPipe == nil || config.PromptPipe == nil {
		return errors.New("one-shot worker bootstrap and prompt pipes are required")
	}
	if config.VerificationKeyring == nil {
		return errors.New("one-shot worker verification keyring is required")
	}
	if config.RuntimePreparer == nil {
		return errors.New("one-shot worker runtime preparer is required")
	}
	if config.Logger == nil {
		return errors.New("one-shot worker diagnostic logger is required")
	}
	if config.ControlHTTPClient == nil || config.ExecutorHTTPClient == nil {
		return errors.New("one-shot worker control and executor HTTP clients are required")
	}
	if config.NotificationHandler == nil {
		return errors.New("one-shot worker notification handler is required")
	}
	if config.BaseInstructions != "" {
		if len(config.BaseInstructions) > MaximumPromptBytes || !utf8.ValidString(config.BaseInstructions) || strings.ContainsRune(config.BaseInstructions, 0) {
			return errors.New("one-shot worker base instructions are invalid or too large")
		}
	}
	for label, value := range map[string]string{
		"client name": config.ClientInfo.Name, "client title": config.ClientInfo.Title,
		"client version": config.ClientInfo.Version,
	} {
		if err := validateNameText("one-shot worker "+label, value, maxIdentityBytes); err != nil {
			return err
		}
	}
	if dependencies.newControl == nil || dependencies.connectMCP == nil ||
		dependencies.startProcess == nil || dependencies.newRunner == nil {
		return errors.New("one-shot worker dependencies are incomplete")
	}
	return nil
}

func authorizedBaseInstructions(manifest runmanifest.Manifest, instructions string) (string, error) {
	if manifest.ToolPack == nil {
		if instructions != "" {
			return "", errors.New("worker deployment supplied base instructions without signed tool-pack authority")
		}
		return "", nil
	}
	digest := sha256.Sum256([]byte(instructions))
	if instructions == "" || hex.EncodeToString(digest[:]) != manifest.ToolPack.SkillSHA256 {
		return "", errors.New("worker base instructions do not match signed tool-pack skill authority")
	}
	return instructions, nil
}

func validatePreparedAppServerRuntime(runtime PreparedAppServerRuntime, manifest runmanifest.Manifest, restored *RestoredCheckpoint) error {
	if err := validateAbsolutePath("prepared app-server thread cwd", runtime.ThreadCWD); err != nil {
		return err
	}
	if (restored == nil) != (runtime.RolloutPath == "") {
		return errors.New("prepared app-server rollout path does not match checkpoint resume mode")
	}
	if runtime.RolloutPath != "" {
		if err := validateAbsolutePath("prepared app-server rollout path", runtime.RolloutPath); err != nil {
			return err
		}
	}
	if runtime.ProcessConfig.Environment.ModelCapability != "" {
		return errors.New("prepared app-server runtime must not contain a model capability")
	}
	if manifest.PreviousCheckpoint != nil && restored == nil {
		return errors.New("signed checkpoint authority was not restored")
	}
	return nil
}

func workerRunnerOptions(manifest runmanifest.Manifest, lifecycle AppServerLifecycleSink) AppServerRunnerOptions {
	maximumEventBytes := min(int(manifest.Limits.MaxEventBufferBytes), workerMaxEventBytes)
	options := DefaultAppServerRunnerOptions()
	options.EventBuffer = maxConfiguredAppServerEvents
	options.MaxEventBytes = maximumEventBytes
	options.MaxEventBufferBytes = int(manifest.Limits.MaxEventBufferBytes)
	options.InterruptGrace = time.Duration(manifest.Limits.WorkerCallbackGraceMS) * time.Millisecond
	options.MaxPromptTextBytes = MaximumPromptBytes
	options.LifecycleSink = lifecycle
	return options
}

func appServerRequest(
	manifest runmanifest.Manifest,
	prompt string,
	baseInstructions string,
	catalog *Catalog,
	runtime PreparedAppServerRuntime,
	restored *RestoredCheckpoint,
	clientInfo AppServerClientInfo,
) AppServerRunRequest {
	request := AppServerRunRequest{
		RunID: manifest.RunID, RunAttemptGeneration: manifest.RunAttemptGeneration,
		PermissionMode: manifest.PermissionMode,
		ClientInfo:     clientInfo, Catalog: catalog, UserText: prompt,
	}
	if restored == nil {
		request.Start = &AppServerThreadStart{
			Model: manifest.Model.Model, CWD: runtime.ThreadCWD, BaseInstructions: baseInstructions,
			PermissionMode: manifest.PermissionMode,
		}
		return request
	}
	request.Resume = &AppServerThreadResume{
		ThreadID: restored.Manifest.BrainThreadID, RolloutPath: runtime.RolloutPath,
		CWD: runtime.ThreadCWD, CheckpointCatalogDigest: restored.Manifest.CatalogDigest,
	}
	return request
}

type recordingWorkerLifecycle struct {
	sink     AppServerLifecycleSink
	mu       sync.Mutex
	threadID string
	turnID   string
}

func (lifecycle *recordingWorkerLifecycle) SendThreadReady(ctx context.Context, threadID string, resumed bool) error {
	if err := lifecycle.sink.SendThreadReady(ctx, threadID, resumed); err != nil {
		return err
	}
	lifecycle.mu.Lock()
	lifecycle.threadID = threadID
	lifecycle.mu.Unlock()
	return nil
}

func (lifecycle *recordingWorkerLifecycle) SendTurnAccepted(ctx context.Context, threadID, turnID string) error {
	if err := lifecycle.sink.SendTurnAccepted(ctx, threadID, turnID); err != nil {
		return err
	}
	lifecycle.mu.Lock()
	lifecycle.threadID = threadID
	lifecycle.turnID = turnID
	lifecycle.mu.Unlock()
	return nil
}

func (lifecycle *recordingWorkerLifecycle) Accepted() (threadID, turnID string, accepted bool) {
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	return lifecycle.threadID, lifecycle.turnID, lifecycle.threadID != "" && lifecycle.turnID != ""
}

type workerInterruptError struct {
	reason string
}

func (err *workerInterruptError) Error() string {
	if err == nil {
		return "worker interrupted"
	}
	return "worker interrupted by holder: " + err.reason
}

func watchWorkerInterrupts(ctx context.Context, commands <-chan harnesscontrol.InterruptCommand, cancel context.CancelCauseFunc) {
	select {
	case command, ok := <-commands:
		if ok {
			cancel(&workerInterruptError{reason: command.Reason})
		}
	case <-ctx.Done():
	}
}

func watchWorkerControl(ctx context.Context, control oneShotWorkerControl, cancel context.CancelCauseFunc) {
	err := control.Wait(ctx)
	if err != nil && ctx.Err() == nil {
		cancel(fmt.Errorf("worker control session failed: %w", err))
	}
}

func consumeAppServerNotifications(
	ctx context.Context,
	consumeEvents func(func(codexwire.Message)) error,
	handler AppServerNotificationHandler,
	cancel context.CancelCauseFunc,
	result chan<- error,
) {
	var first error
	consumeErr := consumeEvents(func(event codexwire.Message) {
		if first != nil {
			return
		}
		if err := handler(ctx, event); err != nil {
			first = fmt.Errorf("deliver app-server notification: %w", err)
			cancel(first)
		}
	})
	result <- errors.Join(first, consumeErr)
}

// workerRuntimeEventForwarder preserves the causal edge between a dynamic
// tool's item/started notification and MCP progress. Runner notifications and
// MCP callbacks are delivered by different goroutines, so progress waits
// until the matching start frame has been journaled on control.
type workerRuntimeEventForwarder struct {
	control      workerRuntimeEventSink
	notification AppServerNotificationHandler
	progress     ProgressHandler

	mu      sync.Mutex
	started map[string]chan struct{}
}

func newWorkerRuntimeEventForwarder(
	control workerRuntimeEventSink,
	notification AppServerNotificationHandler,
	progress ProgressHandler,
) *workerRuntimeEventForwarder {
	return &workerRuntimeEventForwarder{
		control: control, notification: notification, progress: progress,
		started: make(map[string]chan struct{}),
	}
}

func (forwarder *workerRuntimeEventForwarder) HandleNotification(ctx context.Context, message codexwire.Message) error {
	if shouldForwardAppServerNotification(message.Method) {
		if err := forwarder.control.SendAppServerNotification(ctx, message); err != nil {
			return err
		}
		if callID := dynamicToolStartCallID(message); callID != "" {
			forwarder.markDynamicToolStarted(callID)
		}
	}
	if forwarder.notification != nil {
		return forwarder.notification(ctx, message)
	}
	return nil
}

func (forwarder *workerRuntimeEventForwarder) HandleProgress(ctx context.Context, event ProgressEvent) error {
	started := forwarder.dynamicToolStarted(event.CallID)
	select {
	case <-started:
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := forwarder.control.SendExecutorMCPProgress(ctx, event); err != nil {
		return err
	}
	if forwarder.progress != nil {
		return forwarder.progress(ctx, event)
	}
	return nil
}

func (forwarder *workerRuntimeEventForwarder) dynamicToolStarted(callID string) <-chan struct{} {
	forwarder.mu.Lock()
	defer forwarder.mu.Unlock()
	if started := forwarder.started[callID]; started != nil {
		return started
	}
	started := make(chan struct{})
	forwarder.started[callID] = started
	return started
}

func (forwarder *workerRuntimeEventForwarder) markDynamicToolStarted(callID string) {
	forwarder.mu.Lock()
	defer forwarder.mu.Unlock()
	started := forwarder.started[callID]
	if started == nil {
		started = make(chan struct{})
		forwarder.started[callID] = started
	}
	select {
	case <-started:
	default:
		close(started)
	}
}

func shouldForwardAppServerNotification(method string) bool {
	switch method {
	case "item/started", "item/completed", "item/agentMessage/delta",
		"item/reasoning/summaryTextDelta", "item/reasoning/summaryPartAdded",
		"item/reasoning/textDelta", "turn/completed":
		return true
	default:
		return false
	}
}

func dynamicToolStartCallID(message codexwire.Message) string {
	if message.Kind != codexwire.KindNotification || message.Method != "item/started" {
		return ""
	}
	var params struct {
		Item struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		} `json:"item"`
	}
	if err := json.Unmarshal(message.Params, &params); err != nil || params.Item.Type != "dynamicToolCall" {
		return ""
	}
	return params.Item.ID
}

func classifyWorkerTerminal(
	threadID, turnID string,
	result AppServerRunResult,
	cleanupErr error,
	runCause error,
	appServerStderr []byte,
	appServerStderrTruncated bool,
) harnesscontrol.TurnTerminalEvent {
	event := harnesscontrol.TurnTerminalEvent{
		Kind: harnesscontrol.EventKindTurnTerminal, ThreadID: threadID, TurnID: turnID,
	}
	if cleanupErr == nil && result.Terminal.ThreadID == threadID && result.Terminal.Turn.ID == turnID {
		switch result.Terminal.Turn.Status {
		case "completed":
			event.Status = "completed"
			return event
		case "interrupted":
			event.Status = "interrupted"
			event.ErrorCode = "turn_interrupted"
			event.ErrorMessage = "stock app-server confirmed that the turn was interrupted"
			return event
		case "failed":
			event.Status = "failed"
			event.ErrorCode = "turn_failed"
			event.ErrorMessage = stockTurnFailureMessage(
				result.Terminal.Turn.Error, appServerStderr, appServerStderrTruncated,
			)
			return event
		}
	}
	var interrupt *workerInterruptError
	switch {
	case errors.As(runCause, &interrupt):
		event.Status = "interrupted"
		event.ErrorCode = "interrupt_" + interrupt.reason
		event.ErrorMessage = "the holder interrupted the active turn"
	case errors.Is(runCause, ErrWorkerRunDurationExceeded), errors.Is(runCause, context.DeadlineExceeded):
		event.Status = "interrupted"
		event.ErrorCode = "run_duration_exceeded"
		event.ErrorMessage = "the signed maximum run duration expired"
	case errors.Is(runCause, context.Canceled):
		event.Status = "interrupted"
		event.ErrorCode = "worker_shutdown"
		event.ErrorMessage = "the worker was asked to shut down"
	default:
		event.Status = "failed"
		event.ErrorCode = "worker_runtime_failed"
		event.ErrorMessage = "the worker could not complete bounded runtime cleanup"
	}
	return event
}

type workerCleanupFailures struct {
	runner       error
	notification error
	closeStdin   error
	processWait  error
	mcp          error
	runtime      error
}

func (failures workerCleanupFailures) joined() error {
	return errors.Join(
		failures.runner, failures.notification, failures.closeStdin,
		failures.processWait, failures.mcp, failures.runtime,
	)
}

// workerRuntimeFailureMessage is the bounded, user-visible control summary.
// logWorkerFailureDiagnostics emits the corresponding redacted details to the
// internal worker log before this summary is acknowledged by the holder.
func workerRuntimeFailureMessage(
	failures workerCleanupFailures,
	runCause error,
	terminalStatus string,
	stderr []byte,
	stderrTruncated bool,
) string {
	type stagedFailure struct {
		stage string
		err   error
	}
	staged := []stagedFailure{
		{stage: "runner", err: failures.runner},
		{stage: "notification", err: failures.notification},
		{stage: "close_stdin", err: failures.closeStdin},
		{stage: "process_wait", err: failures.processWait},
		{stage: "mcp", err: failures.mcp},
		{stage: "runtime", err: failures.runtime},
		{stage: "run_cause", err: runCause},
	}
	stages := make([]string, 0, len(staged))
	details := make([]string, 0, len(staged)+3)
	var classificationText []byte
	primaryMessage := ""
	primaryStage := ""
	for _, failure := range staged {
		if failure.err == nil {
			continue
		}
		stages = append(stages, failure.stage)
		raw := []byte(failure.err.Error())
		classificationText = append(classificationText, raw...)
		classificationText = append(classificationText, '\n')
		if primaryMessage == "" {
			primaryMessage = safeWorkerDiagnostic(raw, 2048)
			primaryStage = failure.stage
		}
		if digest := diagnosticFingerprint(raw); digest != "" {
			details = append(details, failure.stage+"_error_sha256="+digest)
		}
		clear(raw)
	}
	if len(stages) == 0 {
		stages = append(stages, "unknown")
	}
	switch terminalStatus {
	case "completed", "failed", "interrupted":
	default:
		terminalStatus = "missing"
	}
	details = append([]string{
		"category=" + classifyStockTurnFailure(classificationText, stderr),
		"stages=" + strings.Join(stages, ","),
		"terminal_status=" + terminalStatus,
	}, details...)
	if primaryMessage == "" {
		primaryMessage = safeWorkerDiagnostic(stderr, 2048)
		primaryStage = "stderr"
	}
	if primaryMessage != "" {
		details = append(details, "message_stage="+primaryStage, "message="+strconv.Quote(primaryMessage))
	}
	clear(classificationText)
	if digest := diagnosticFingerprint(stderr); digest != "" {
		details = append(details, "stderr_sha256="+digest)
	}
	if stderrTruncated {
		details = append(details, "stderr_truncated=true")
	}
	return "the worker could not complete bounded runtime cleanup; " + strings.Join(details, " ")
}

func appServerProcessStderr(process oneShotWorkerProcess) ([]byte, bool) {
	source, ok := process.(appServerStderrSource)
	if !ok {
		return nil, false
	}
	contents, truncated := source.Stderr()
	return append([]byte(nil), contents...), truncated
}

// stockTurnFailureMessage is the bounded, creator-visible durable summary.
// It preserves one useful upstream diagnostic after shared credential
// redaction; fingerprints remain as correlation aids, not as replacements for
// the error itself.
func stockTurnFailureMessage(turnError json.RawMessage, stderr []byte, stderrTruncated bool) string {
	const prefix = "stock app-server confirmed that the turn failed"
	category := classifyStockTurnFailure(turnError, stderr)
	details := "category=" + category
	if message := stockTurnDiagnostic(turnError, stderr); message != "" {
		details += " message=" + strconv.Quote(message)
	}
	if digest := diagnosticFingerprint(turnError); digest != "" {
		details += " turn_error_sha256=" + digest
	}
	if digest := diagnosticFingerprint(stderr); digest != "" {
		details += " stderr_sha256=" + digest
	}
	if stderrTruncated {
		details += " stderr_truncated=true"
	}
	return prefix + "; " + details
}

func stockTurnDiagnostic(turnError json.RawMessage, stderr []byte) string {
	var fields map[string]json.RawMessage
	if json.Unmarshal(turnError, &fields) == nil {
		for _, name := range []string{"message", "error", "codexErrorInfo"} {
			var value string
			if raw := fields[name]; len(raw) != 0 && json.Unmarshal(raw, &value) == nil && strings.TrimSpace(value) != "" {
				return safeWorkerDiagnostic([]byte(value), 2048)
			}
		}
		if raw := fields["error"]; len(raw) != 0 {
			var nested map[string]json.RawMessage
			if json.Unmarshal(raw, &nested) == nil {
				var value string
				if json.Unmarshal(nested["message"], &value) == nil && strings.TrimSpace(value) != "" {
					return safeWorkerDiagnostic([]byte(value), 2048)
				}
			}
		}
	}
	if value := safeWorkerDiagnostic(turnError, 2048); value != "" && value != "null" && value != "{}" {
		return value
	}
	return safeWorkerDiagnostic(stderr, 2048)
}

func safeWorkerDiagnostic(raw []byte, maximumBytes int) string {
	return strings.TrimSpace(safediagnostic.Sanitize(raw, maximumBytes).Value)
}

func classifyStockTurnFailure(turnError, stderr []byte) string {
	contents := strings.ToLower(string(turnError) + "\n" + string(stderr))
	switch {
	case containsAny(contents,
		"serveroverloaded", "server overloaded", "selected model is at capacity",
		"model is at capacity"):
		return "model_overloaded"
	case containsAny(contents,
		"unknownissuer", "unknown issuer", "certificate verify", "certificate_verify",
		"invalid peer certificate", "self signed certificate", "unable to get local issuer"):
		return "tls_trust_failure"
	case strings.Contains(contents, "bad record mac"):
		return "tls_record_failure"
	case containsAny(contents, "tls handshake", "handshake failure", "certificate"):
		return "tls_failure"
	case containsAny(contents, "status code: 401", "status 401", "unauthorized"):
		return "model_unauthorized"
	case containsAny(contents, "status code: 403", "status 403", "forbidden"):
		return "model_forbidden"
	case containsAny(contents, "status code: 429", "status 429", "rate limit"):
		return "model_rate_limited"
	case containsAny(contents, "connection refused", "connection reset", "error sending request", "dns error"):
		return "model_transport_failure"
	case containsAny(contents, "timed out", "timeout"):
		return "model_timeout"
	default:
		return "unclassified"
	}
}

func containsAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}

func diagnosticFingerprint(contents []byte) string {
	trimmed := strings.TrimSpace(string(contents))
	if trimmed == "" || trimmed == "null" || trimmed == "{}" {
		return ""
	}
	digest := sha256.Sum256(contents)
	return fmt.Sprintf("%x", digest[:8])
}

func closeWorkerProcess(process oneShotWorkerProcess, graceMillis int64) error {
	if process == nil {
		return nil
	}
	closeErr := process.CloseStdin()
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(graceMillis)*time.Millisecond)
	defer cancel()
	return errors.Join(closeErr, process.Wait(ctx))
}

func closeUnconsumedWorkerInputs(files ...*os.File) error {
	var result error
	for _, file := range files {
		if file != nil {
			result = errors.Join(result, file.Close())
		}
	}
	return result
}
