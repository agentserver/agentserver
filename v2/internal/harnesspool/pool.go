package harnesspool

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/agentserver/agentserver/v2/internal/harnesscontrol"
	"github.com/agentserver/agentserver/v2/internal/runmanifest"
)

const maximumPoolConcurrency = 1024

type RunAttemptScheduler interface {
	ClaimNextRunAttempt(context.Context) (*ScheduledRunAttempt, error)
	CompleteAcceptedDispatch(context.Context, RunDispatch) error
	ReleaseUnstartedDispatch(context.Context, RunDispatch) error
	AttemptLeaseTTL() time.Duration
}

type RunAttemptPreparer interface {
	Prepare(context.Context, ScheduledRunAttempt) (PreparedRunLaunch, error)
}

type AttemptSupervisionCore interface {
	RenewRunAttempt(context.Context, RenewRunAttemptRequest) (RenewRunAttemptResult, error)
	InterruptRunAttempt(context.Context, InterruptRunAttemptRequest) (InterruptRunAttemptResult, error)
	AbandonRunAttempt(context.Context, AbandonRunAttemptRequest) (AbandonRunAttemptResult, error)
	BindBrainThreadCatalog(context.Context, BindBrainThreadCatalogRequest) (BindBrainThreadCatalogResult, error)
	MarkTurnAccepted(context.Context, MarkTurnAcceptedRequest) (MarkTurnAcceptedResult, error)
}

type AttemptEventCore interface {
	AppendAttemptEvents(context.Context, AppendAttemptEventsRequest) (AppendAttemptEventsResult, error)
}

type TransitionIdentityAllocator interface {
	AllocateTransitionRecord() (TransitionRecord, error)
}

// AttemptLifecycle is the synchronous authority boundary exposed to one
// per-attempt runtime. A runtime must not send turn/start until ThreadStarted
// succeeds, and must not continue the accepted turn until TurnAccepted
// succeeds. Implementations never run a model or execute a tool.
type AttemptLifecycle interface {
	ThreadStarted(threadID string) error
	TurnAccepted(threadID, turnID string) error
}

type AttemptRuntimeLifecycle interface {
	RuntimeEvent(context.Context, AttemptRuntimeEvent) error
}

// AttemptRuntimeEvent binds one decoded runtime payload to its immutable
// worker->pool control sequence. The sequence is used only to retain an exact
// pending core append across same-process transport resume; canonical producer
// identities are allocated by the pool.
type AttemptRuntimeEvent struct {
	ControlSequence uint64
	Event           harnesscontrol.Event
}

// AttemptSupervisor owns the local worker-process/control-stream
// implementation. It blocks for the lifetime of exactly one attempt, returns
// when that process tree is fully stopped, and must return promptly when ctx
// is cancelled.
type AttemptSupervisor interface {
	Supervise(context.Context, PreparedRunLaunch, AttemptLifecycle) error
}

type PoolFailureStage string

const (
	PoolFailureClaim     PoolFailureStage = "claim"
	PoolFailurePrepare   PoolFailureStage = "prepare"
	PoolFailureSupervise PoolFailureStage = "supervise"
	PoolFailureCleanup   PoolFailureStage = "cleanup"
)

type PoolFailure struct {
	Stage        PoolFailureStage
	RunID        string
	RunAttemptID string
	Err          error
}

type PoolReporter interface {
	ReportPoolFailure(PoolFailure)
}

type PoolConfig struct {
	MaxConcurrentAttempts int
	LeaseRenewInterval    time.Duration
	IdleBackoff           time.Duration
	FailureBackoff        time.Duration
	CleanupTimeout        time.Duration
}

type Pool struct {
	scheduler  RunAttemptScheduler
	preparer   RunAttemptPreparer
	core       AttemptSupervisionCore
	identities TransitionIdentityAllocator
	supervisor AttemptSupervisor
	reporter   PoolReporter
	config     PoolConfig
}

func NewPool(
	scheduler RunAttemptScheduler,
	preparer RunAttemptPreparer,
	core AttemptSupervisionCore,
	identities TransitionIdentityAllocator,
	supervisor AttemptSupervisor,
	reporter PoolReporter,
	config PoolConfig,
) (*Pool, error) {
	if scheduler == nil {
		return nil, errors.New("run-attempt scheduler is required")
	}
	if preparer == nil {
		return nil, errors.New("run-attempt preparer is required")
	}
	if core == nil {
		return nil, errors.New("attempt supervision core client is required")
	}
	if identities == nil {
		return nil, errors.New("transition identity allocator is required")
	}
	if supervisor == nil {
		return nil, errors.New("attempt supervisor is required")
	}
	if reporter == nil {
		return nil, errors.New("pool failure reporter is required")
	}
	if err := validatePoolConfig(config, scheduler.AttemptLeaseTTL()); err != nil {
		return nil, err
	}
	return &Pool{
		scheduler: scheduler, preparer: preparer, core: core, identities: identities,
		supervisor: supervisor, reporter: reporter, config: config,
	}, nil
}

// Run continuously fills bounded attempt slots. Per-attempt failures are
// reported and isolated; process shutdown is driven only by ctx. The durable
// dispatch and database leases remain the recovery source of truth.
func (pool *Pool) Run(ctx context.Context) error {
	if ctx == nil {
		return errors.New("pool context is required")
	}
	if pool == nil || pool.scheduler == nil {
		return errors.New("pool is required")
	}
	slots := make(chan struct{}, pool.config.MaxConcurrentAttempts)
	var attempts sync.WaitGroup
	for {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			attempts.Wait()
			return nil
		}

		scheduled, err := pool.scheduler.ClaimNextRunAttempt(ctx)
		if err != nil {
			<-slots
			if ctx.Err() != nil {
				attempts.Wait()
				return nil
			}
			pool.report(PoolFailureClaim, nil, err)
			if !waitPoolBackoff(ctx, pool.config.FailureBackoff) {
				attempts.Wait()
				return nil
			}
			continue
		}
		if scheduled == nil {
			<-slots
			if !waitPoolBackoff(ctx, pool.config.IdleBackoff) {
				attempts.Wait()
				return nil
			}
			continue
		}

		attempts.Add(1)
		go func(scheduled ScheduledRunAttempt) {
			defer attempts.Done()
			defer func() { <-slots }()
			stage, err := pool.processAttempt(ctx, scheduled)
			if err != nil && ctx.Err() == nil {
				pool.report(stage, &scheduled, err)
			}
		}(*scheduled)
	}
}

func (pool *Pool) processAttempt(parent context.Context, scheduled ScheduledRunAttempt) (PoolFailureStage, error) {
	if err := validateScheduledLaunchAuthority(scheduled); err != nil {
		return PoolFailurePrepare, pool.releaseAfterStartupFailure(parent, scheduled, err)
	}
	attemptCtx, cancelAttempt := context.WithCancelCause(parent)
	// Keep the holder's lifecycle-command path separate from the turn runtime.
	// Explicit cancellation stops attemptCtx immediately, but control still has
	// to append already-observed runtime facts and acknowledge an interrupted
	// terminal while the exact holder keeps both leases alive.
	leaseCtx, stopLease := context.WithCancel(parent)
	defer stopLease()
	state := newAttemptAuthorityState(scheduled.Claim)
	leaseDone := make(chan error, 1)
	go func() {
		leaseDone <- pool.keepAttemptLease(leaseCtx, cancelAttempt, scheduled, state)
	}()

	prepared, err := pool.preparer.Prepare(attemptCtx, scheduled)
	if err != nil {
		cancelAttempt(err)
		stopLease()
		leaseErr := <-leaseDone
		failure := errors.Join(fmt.Errorf("prepare run launch: %w", err), leaseErr)
		if parent.Err() == nil && leaseErr == nil {
			abandoned, abandonErr := pool.abandonStoppedPreTurnAttempt(scheduled)
			if abandonErr != nil {
				return PoolFailureCleanup, pool.releaseAfterStartupFailure(parent, scheduled, errors.Join(failure, abandonErr))
			}
			if abandoned.Disposition == "cancelled" {
				return PoolFailureCleanup, pool.completeStoppedDispatch(scheduled.Dispatch)
			}
		}
		return PoolFailurePrepare, pool.releaseAfterStartupFailure(parent, scheduled, failure)
	}
	if err := validatePreparedSupervisionInput(scheduled, prepared); err != nil {
		cancelAttempt(err)
		stopLease()
		leaseErr := <-leaseDone
		failure := errors.Join(fmt.Errorf("validate prepared run launch: %w", err), leaseErr)
		if parent.Err() == nil && leaseErr == nil {
			abandoned, abandonErr := pool.abandonStoppedPreTurnAttempt(scheduled)
			if abandonErr != nil {
				return PoolFailureCleanup, pool.releaseAfterStartupFailure(parent, scheduled, errors.Join(failure, abandonErr))
			}
			if abandoned.Disposition == "cancelled" {
				return PoolFailureCleanup, pool.completeStoppedDispatch(scheduled.Dispatch)
			}
		}
		return PoolFailurePrepare, pool.releaseAfterStartupFailure(parent, scheduled, failure)
	}

	authority := &attemptLifecycleAuthority{
		ctx: leaseCtx, scheduler: pool.scheduler, core: pool.core, identities: pool.identities,
		prepared: prepared,
	}
	supervisionErr := pool.supervisor.Supervise(attemptCtx, prepared, authority)
	accepted := authority.accepted()
	if accepted && supervisionErr != nil && state.snapshot().Run.Status != "cancelling" && parent.Err() == nil {
		// A cancel can win while an accepted attempt is crossing finalization,
		// causing that command to fail before the normal lease tick observes
		// the new run version. One exact renewal resolves that common race.
		_, _ = pool.observeAttemptAuthority(parent, scheduled, state)
	}
	cancelAttempt(errors.New("attempt supervisor returned"))
	cleanupErr := authority.dispatchCleanupError()
	stopLease()
	leaseErr := <-leaseDone
	latest := state.snapshot()
	if !accepted {
		failure := errors.Join(supervisionErr, leaseErr, cleanupErr)
		if failure == nil {
			failure = errors.New("attempt supervisor stopped before turn acceptance")
		}
		if parent.Err() == nil && leaseErr == nil {
			abandoned, abandonErr := pool.abandonStoppedPreTurnAttempt(scheduled)
			if abandonErr != nil {
				return PoolFailureCleanup, pool.releaseAfterStartupFailure(parent, scheduled, errors.Join(failure, abandonErr))
			}
			if abandoned.Disposition == "cancelled" {
				return PoolFailureCleanup, errors.Join(cleanupErr, pool.completeStoppedDispatch(scheduled.Dispatch))
			}
		}
		return PoolFailureSupervise, pool.releaseAfterStartupFailure(parent, scheduled, failure)
	}
	if latest.Run.Status == "cancelling" {
		cancelErr := pool.finishCancelledAttempt(scheduled, authority.accepted(), latest, supervisionErr)
		if cancelErr != nil {
			return PoolFailureCleanup, errors.Join(supervisionErr, leaseErr, cleanupErr, cancelErr)
		}
		return PoolFailureCleanup, cleanupErr
	}
	if cleanupErr != nil && supervisionErr == nil && leaseErr == nil {
		return PoolFailureCleanup, cleanupErr
	}
	return PoolFailureSupervise, errors.Join(supervisionErr, leaseErr, cleanupErr)
}

func (pool *Pool) abandonStoppedPreTurnAttempt(scheduled ScheduledRunAttempt) (AbandonRunAttemptResult, error) {
	record, err := pool.identities.AllocateTransitionRecord()
	if err != nil {
		return AbandonRunAttemptResult{}, fmt.Errorf("allocate attempt-abandoned transition identity: %w", err)
	}
	claim := scheduled.Claim
	request := AbandonRunAttemptRequest{
		RunID: claim.Run.RunID, RunAttemptID: claim.RunAttempt.RunAttemptID,
		HolderID: claim.RunAttempt.HolderID, RunAttemptGeneration: claim.RunAttempt.Generation,
		Reason: "startup_failed", Record: record,
	}
	commandTimeout := pool.config.CleanupTimeout
	if leaseWindow := pool.scheduler.AttemptLeaseTTL() / 2; commandTimeout > leaseWindow {
		commandTimeout = leaseWindow
	}
	cleanupCtx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	result, err := pool.core.AbandonRunAttempt(cleanupCtx, request)
	if err != nil && ambiguousPoolCommand(err, cleanupCtx) {
		result, err = pool.core.AbandonRunAttempt(cleanupCtx, request)
	}
	if err != nil {
		return AbandonRunAttemptResult{}, fmt.Errorf("reconcile stopped pre-turn attempt: %w", err)
	}
	if result.Run.RunID != request.RunID || result.RunAttempt.RunAttemptID != request.RunAttemptID ||
		result.RunAttempt.Generation != request.RunAttemptGeneration || result.RunAttempt.HolderID != request.HolderID ||
		!validAbandonRunAttemptResult(result) {
		return AbandonRunAttemptResult{}, errors.New("core returned an invalid stopped pre-turn attempt result")
	}
	return result, nil
}

func (pool *Pool) completeStoppedDispatch(dispatch RunDispatch) error {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), pool.config.CleanupTimeout)
	defer cancel()
	err := pool.scheduler.CompleteAcceptedDispatch(cleanupCtx, dispatch)
	if err != nil && ambiguousPoolCommand(err, cleanupCtx) {
		err = pool.scheduler.CompleteAcceptedDispatch(cleanupCtx, dispatch)
	}
	if err != nil {
		return fmt.Errorf("complete terminal run dispatch: %w", err)
	}
	return nil
}

func (pool *Pool) keepAttemptLease(
	ctx context.Context,
	cancel context.CancelCauseFunc,
	scheduled ScheduledRunAttempt,
	state *attemptAuthorityState,
) error {
	ticker := time.NewTicker(pool.config.LeaseRenewInterval)
	defer ticker.Stop()
	claim := scheduled.Claim
	request := RenewRunAttemptRequest{
		SessionID: claim.Run.SessionID, RunID: claim.Run.RunID,
		RunAttemptID: claim.RunAttempt.RunAttemptID, HolderID: claim.RunAttempt.HolderID,
		RunAttemptGeneration: claim.RunAttempt.Generation, LeaseTTL: pool.scheduler.AttemptLeaseTTL(),
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			result, err := pool.renewAttemptExactly(ctx, request)
			if err != nil {
				failure := fmt.Errorf("renew run-attempt leases: %w", err)
				cancel(failure)
				return failure
			}
			state.observe(result)
			if result.Run.Status == "cancelling" {
				cancel(errors.New("explicit run cancellation requested"))
			}
		}
	}
}

type attemptAuthoritySnapshot struct {
	Run        Run
	RunAttempt RunAttempt
}

type attemptAuthorityState struct {
	mu            sync.Mutex
	snapshotValue attemptAuthoritySnapshot
}

func newAttemptAuthorityState(claim ClaimRunAttemptResult) *attemptAuthorityState {
	return &attemptAuthorityState{snapshotValue: attemptAuthoritySnapshot{Run: claim.Run, RunAttempt: claim.RunAttempt}}
}

func (state *attemptAuthorityState) observe(result RenewRunAttemptResult) {
	state.mu.Lock()
	state.snapshotValue = attemptAuthoritySnapshot{Run: result.Run, RunAttempt: result.RunAttempt}
	state.mu.Unlock()
}

func (state *attemptAuthorityState) snapshot() attemptAuthoritySnapshot {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.snapshotValue
}

func (pool *Pool) observeAttemptAuthority(ctx context.Context, scheduled ScheduledRunAttempt, state *attemptAuthorityState) (RenewRunAttemptResult, error) {
	claim := scheduled.Claim
	result, err := pool.renewAttemptExactly(ctx, RenewRunAttemptRequest{
		SessionID: claim.Run.SessionID, RunID: claim.Run.RunID,
		RunAttemptID: claim.RunAttempt.RunAttemptID, HolderID: claim.RunAttempt.HolderID,
		RunAttemptGeneration: claim.RunAttempt.Generation, LeaseTTL: pool.scheduler.AttemptLeaseTTL(),
	})
	if err == nil {
		state.observe(result)
	}
	return result, err
}

func (pool *Pool) finishCancelledAttempt(
	scheduled ScheduledRunAttempt,
	turnAccepted bool,
	latest attemptAuthoritySnapshot,
	supervisionErr error,
) error {
	if turnAccepted && latest.RunAttempt.Status != "finalizing" {
		var terminal *AttemptTerminalError
		if !errors.As(supervisionErr, &terminal) || terminal.Status != "interrupted" {
			return errors.New("cancelling accepted attempt did not confirm an interrupted stock turn")
		}
	}
	record, err := pool.identities.AllocateTransitionRecord()
	if err != nil {
		return fmt.Errorf("allocate run-cancelled transition identity: %w", err)
	}
	claim := scheduled.Claim
	request := InterruptRunAttemptRequest{
		RunID: claim.Run.RunID, RunAttemptID: claim.RunAttempt.RunAttemptID,
		HolderID: claim.RunAttempt.HolderID, RunAttemptGeneration: claim.RunAttempt.Generation,
		ExpectedRunVersion: latest.Run.Version, ExpectedRunAttemptVersion: latest.RunAttempt.Version,
		Reason: "cancelled", Record: record,
	}
	commandTimeout := pool.config.CleanupTimeout
	if leaseWindow := pool.scheduler.AttemptLeaseTTL() / 2; commandTimeout > leaseWindow {
		commandTimeout = leaseWindow
	}
	cleanupCtx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	result, err := pool.core.InterruptRunAttempt(cleanupCtx, request)
	if err != nil && ambiguousPoolCommand(err, cleanupCtx) {
		result, err = pool.core.InterruptRunAttempt(cleanupCtx, request)
	}
	if err != nil {
		return fmt.Errorf("commit cancelled run after attempt cleanup: %w", err)
	}
	if result.Run.RunID != request.RunID || result.Run.Status != "cancelled" ||
		result.RunAttempt.RunAttemptID != request.RunAttemptID || result.RunAttempt.Status != "interrupted" ||
		result.RunAttempt.Generation != request.RunAttemptGeneration || result.RunAttempt.HolderID != request.HolderID {
		return errors.New("core returned an invalid cancelled attempt result")
	}
	return nil
}

func (pool *Pool) renewAttemptExactly(ctx context.Context, request RenewRunAttemptRequest) (RenewRunAttemptResult, error) {
	result, err := pool.core.RenewRunAttempt(ctx, request)
	if err == nil {
		return result, nil
	}
	if !ambiguousPoolCommand(err, ctx) {
		return RenewRunAttemptResult{}, err
	}
	return pool.core.RenewRunAttempt(ctx, request)
}

func (pool *Pool) releaseAfterStartupFailure(parent context.Context, scheduled ScheduledRunAttempt, failure error) error {
	if parent.Err() != nil {
		return failure
	}
	cleanupCtx, cancel := context.WithTimeout(parent, pool.config.CleanupTimeout)
	defer cancel()
	err := pool.scheduler.ReleaseUnstartedDispatch(cleanupCtx, scheduled.Dispatch)
	if err != nil && ambiguousPoolCommand(err, cleanupCtx) {
		err = pool.scheduler.ReleaseUnstartedDispatch(cleanupCtx, scheduled.Dispatch)
	}
	if err != nil {
		return errors.Join(failure, fmt.Errorf("release unstarted dispatch: %w", err))
	}
	return failure
}

func (pool *Pool) report(stage PoolFailureStage, scheduled *ScheduledRunAttempt, err error) {
	if err == nil {
		return
	}
	failure := PoolFailure{Stage: stage, Err: err}
	if scheduled != nil {
		failure.RunID = scheduled.Claim.Run.RunID
		failure.RunAttemptID = scheduled.Claim.RunAttempt.RunAttemptID
	}
	pool.reporter.ReportPoolFailure(failure)
}

func validatePoolConfig(config PoolConfig, leaseTTL time.Duration) error {
	if config.MaxConcurrentAttempts < 1 || config.MaxConcurrentAttempts > maximumPoolConcurrency {
		return fmt.Errorf("pool max concurrent attempts must be between 1 and %d", maximumPoolConcurrency)
	}
	if leaseTTL < time.Millisecond || leaseTTL > maximumControllerLeaseTTL {
		return errors.New("pool scheduler returned an invalid attempt lease TTL")
	}
	if config.LeaseRenewInterval < time.Millisecond || config.LeaseRenewInterval > leaseTTL/2 {
		return errors.New("pool lease renew interval must be at least 1ms and no greater than half the attempt lease TTL")
	}
	for field, duration := range map[string]time.Duration{
		"idle backoff": config.IdleBackoff, "failure backoff": config.FailureBackoff,
		"cleanup timeout": config.CleanupTimeout,
	} {
		if duration < time.Millisecond || duration > time.Hour {
			return fmt.Errorf("pool %s must be between 1ms and 1h", field)
		}
	}
	return nil
}

func validatePreparedSupervisionInput(scheduled ScheduledRunAttempt, prepared PreparedRunLaunch) error {
	if err := validateScheduledLaunchAuthority(prepared.Scheduled); err != nil {
		return err
	}
	claim := scheduled.Claim
	if prepared.Scheduled != scheduled {
		return errors.New("prepared launch does not preserve its scheduled attempt authority")
	}
	if err := prepared.Manifest.Validate(); err != nil {
		return err
	}
	if prepared.Manifest.WorkspaceID != claim.Run.WorkspaceID || prepared.Manifest.SessionID != claim.Run.SessionID ||
		prepared.Manifest.RunID != claim.Run.RunID || prepared.Manifest.RunAttemptID != claim.RunAttempt.RunAttemptID ||
		prepared.Manifest.RunAttemptGeneration != claim.RunAttempt.Generation || prepared.Manifest.HolderID != claim.RunAttempt.HolderID ||
		prepared.Manifest.ExecutorMCP.CatalogID != prepared.FrozenCatalog.CatalogID ||
		prepared.Manifest.ExecutorMCP.CatalogDigest != fmt.Sprintf("%x", prepared.FrozenCatalog.CatalogDigest) {
		return errors.New("prepared manifest does not match its attempt and frozen catalog authority")
	}
	if prepared.FrozenCatalog.WorkspaceID != claim.Run.WorkspaceID || prepared.FrozenCatalog.SessionID != claim.Run.SessionID {
		return errors.New("prepared frozen catalog does not belong to the scheduled workspace and session")
	}
	if prepared.Manifest.PreviousCheckpoint != nil && prepared.FrozenCatalog.ThreadID != prepared.Manifest.PreviousCheckpoint.ThreadID {
		return errors.New("prepared resume catalog does not match the checkpoint thread")
	}
	canonical, err := runmanifest.CanonicalBytes(prepared.Manifest)
	if err != nil {
		return err
	}
	if !validClientProtocolText(prepared.SignedManifest.KeyID, 256) ||
		prepared.SignedManifest.Algorithm != runmanifest.SignatureAlgorithm ||
		!bytes.Equal(prepared.SignedManifest.Manifest, canonical) {
		return errors.New("signed run manifest envelope does not contain the exact prepared manifest")
	}
	signature, err := base64.RawURLEncoding.DecodeString(prepared.SignedManifest.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize ||
		base64.RawURLEncoding.EncodeToString(signature) != prepared.SignedManifest.Signature {
		return errors.New("signed run manifest envelope contains a non-canonical signature")
	}
	return nil
}

func ambiguousPoolCommand(err error, ctx context.Context) bool {
	if err == nil || ctx.Err() != nil {
		return false
	}
	var commandError *CoreCommandError
	return !errors.As(err, &commandError)
}

func waitPoolBackoff(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

type attemptLifecycleAuthority struct {
	ctx        context.Context
	scheduler  RunAttemptScheduler
	core       AttemptSupervisionCore
	identities TransitionIdentityAllocator
	prepared   PreparedRunLaunch

	mu                sync.Mutex
	threadID          string
	turnID            string
	turnRecord        *TransitionRecord
	turnWasAccepted   bool
	dispatchCompleted bool
	dispatchErr       error

	runtimeMu      sync.Mutex
	runtimeCursor  uint64
	runtimeMapper  *runtimeEventMapper
	pendingRuntime *pendingRuntimeAppend
}

func (authority *attemptLifecycleAuthority) ThreadStarted(threadID string) error {
	if !validClientProtocolText(threadID, 256) {
		return errors.New("worker thread ID must contain between 1 and 256 valid UTF-8 bytes without NUL")
	}
	authority.mu.Lock()
	defer authority.mu.Unlock()
	if err := context.Cause(authority.ctx); err != nil {
		return err
	}
	if authority.threadID != "" {
		if authority.threadID != threadID {
			return errors.New("worker changed thread identity within one attempt")
		}
		return nil
	}
	catalog := authority.prepared.FrozenCatalog
	if catalog.ThreadID != "" {
		if catalog.ThreadID != threadID {
			return errors.New("resumed worker thread does not match the checkpoint catalog authority")
		}
		authority.threadID = threadID
		return nil
	}
	claim := authority.prepared.Scheduled.Claim
	request := BindBrainThreadCatalogRequest{
		CatalogID: catalog.CatalogID, RunID: claim.Run.RunID, RunAttemptID: claim.RunAttempt.RunAttemptID,
		HolderID: claim.RunAttempt.HolderID, RunAttemptGeneration: claim.RunAttempt.Generation,
		ExpectedRunVersion: claim.Run.Version, ExpectedRunAttemptVersion: claim.RunAttempt.Version,
		ExpectedCatalogVersion: catalog.Version, ThreadID: threadID,
	}
	bound, err := authority.core.BindBrainThreadCatalog(authority.ctx, request)
	if err != nil && ambiguousPoolCommand(err, authority.ctx) {
		bound, err = authority.core.BindBrainThreadCatalog(authority.ctx, request)
	}
	if err != nil {
		return fmt.Errorf("bind brain thread catalog: %w", err)
	}
	if !boundBrainCatalogMatches(catalog, bound, request) {
		return errors.New("bound brain thread catalog response changed immutable catalog authority")
	}
	authority.prepared.FrozenCatalog = bound.Catalog
	authority.threadID = threadID
	return nil
}

func (authority *attemptLifecycleAuthority) TurnAccepted(threadID, turnID string) error {
	if !validClientProtocolText(threadID, 256) || !validClientProtocolText(turnID, 256) {
		return errors.New("worker thread and turn IDs must contain between 1 and 256 valid UTF-8 bytes without NUL")
	}
	authority.mu.Lock()
	defer authority.mu.Unlock()
	if err := context.Cause(authority.ctx); err != nil {
		return err
	}
	if authority.threadID == "" {
		return errors.New("worker reported turn acceptance before thread catalog binding")
	}
	if authority.threadID != threadID {
		return errors.New("accepted turn thread does not match the bound catalog thread")
	}
	if authority.turnWasAccepted {
		if authority.turnID != turnID {
			return errors.New("worker changed accepted turn identity within one attempt")
		}
		authority.completeDispatchLocked()
		return nil
	}
	if authority.turnRecord == nil {
		record, err := authority.identities.AllocateTransitionRecord()
		if err != nil {
			return fmt.Errorf("allocate turn-accepted transition identity: %w", err)
		}
		authority.turnRecord = &record
	}
	claim := authority.prepared.Scheduled.Claim
	request := MarkTurnAcceptedRequest{
		RunID: claim.Run.RunID, RunAttemptID: claim.RunAttempt.RunAttemptID,
		HolderID: claim.RunAttempt.HolderID, RunAttemptGeneration: claim.RunAttempt.Generation,
		ExpectedRunVersion: claim.Run.Version, ExpectedRunAttemptVersion: claim.RunAttempt.Version,
		Record: *authority.turnRecord,
	}
	accepted, err := authority.core.MarkTurnAccepted(authority.ctx, request)
	if err != nil && ambiguousPoolCommand(err, authority.ctx) {
		accepted, err = authority.core.MarkTurnAccepted(authority.ctx, request)
	}
	if err != nil {
		return fmt.Errorf("mark turn accepted: %w", err)
	}
	if !acceptedTurnMatches(claim, accepted) {
		return errors.New("turn-accepted response does not match the scheduled attempt transition")
	}
	authority.prepared.Scheduled.Claim.Run = accepted.Run
	authority.prepared.Scheduled.Claim.RunAttempt = accepted.RunAttempt
	authority.turnID = turnID
	authority.turnWasAccepted = true
	authority.completeDispatchLocked()
	return nil
}

func (authority *attemptLifecycleAuthority) completeDispatchLocked() {
	if authority.dispatchCompleted {
		return
	}
	err := authority.scheduler.CompleteAcceptedDispatch(authority.ctx, authority.prepared.Scheduled.Dispatch)
	if err != nil && ambiguousPoolCommand(err, authority.ctx) {
		err = authority.scheduler.CompleteAcceptedDispatch(authority.ctx, authority.prepared.Scheduled.Dispatch)
	}
	if err != nil {
		// Delivery cleanup is not part of turn authority. Once core committed
		// turn.accepted, another consumer can safely complete the durable row.
		authority.dispatchErr = fmt.Errorf("complete accepted run dispatch: %w", err)
		return
	}
	authority.dispatchCompleted = true
	authority.dispatchErr = nil
}

func (authority *attemptLifecycleAuthority) accepted() bool {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	return authority.turnWasAccepted
}

func (authority *attemptLifecycleAuthority) dispatchCleanupError() error {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	return authority.dispatchErr
}

func boundBrainCatalogMatches(before BrainToolCatalog, result BindBrainThreadCatalogResult, request BindBrainThreadCatalogRequest) bool {
	after := result.Catalog
	return after.CatalogID == before.CatalogID && after.WorkspaceID == before.WorkspaceID &&
		after.SessionID == before.SessionID && after.CreatedRunID == before.CreatedRunID &&
		after.CreatedRunAttemptID == before.CreatedRunAttemptID &&
		after.CreatedAttemptGeneration == before.CreatedAttemptGeneration &&
		after.CreatedHolderID == before.CreatedHolderID && after.CreatedRunVersion == before.CreatedRunVersion &&
		after.CreatedAttemptVersion == before.CreatedAttemptVersion && after.ThreadID == request.ThreadID &&
		after.ContractVersion == before.ContractVersion && after.CanonicalizerVersion == before.CanonicalizerVersion &&
		bytes.Equal(after.CanonicalCatalog, before.CanonicalCatalog) && after.CatalogDigest == before.CatalogDigest &&
		after.PolicyVersion == before.PolicyVersion && after.PolicyContextDigest == before.PolicyContextDigest &&
		after.Version == request.ExpectedCatalogVersion+1 && after.CreatedAt.Equal(before.CreatedAt) &&
		!after.UpdatedAt.IsZero()
}

func acceptedTurnMatches(before ClaimRunAttemptResult, result MarkTurnAcceptedResult) bool {
	run := result.Run
	attempt := result.RunAttempt
	return run.RunID == before.Run.RunID && run.WorkspaceID == before.Run.WorkspaceID &&
		run.SessionID == before.Run.SessionID && run.Status == "running" &&
		run.CurrentAttemptGeneration == before.Run.CurrentAttemptGeneration && run.Version == before.Run.Version+1 &&
		attempt.RunAttemptID == before.RunAttempt.RunAttemptID && attempt.RunID == before.Run.RunID &&
		attempt.Generation == before.RunAttempt.Generation && attempt.Status == "running" &&
		attempt.HolderID == before.RunAttempt.HolderID && attempt.Version == before.RunAttempt.Version+1 &&
		attempt.TurnStartedAt != nil
}
