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
	BindBrainThreadCatalog(context.Context, BindBrainThreadCatalogRequest) (BindBrainThreadCatalogResult, error)
	MarkTurnAccepted(context.Context, MarkTurnAcceptedRequest) (MarkTurnAcceptedResult, error)
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
	leaseDone := make(chan error, 1)
	go func() {
		leaseDone <- pool.keepAttemptLease(attemptCtx, cancelAttempt, scheduled)
	}()

	prepared, err := pool.preparer.Prepare(attemptCtx, scheduled)
	if err != nil {
		cancelAttempt(err)
		leaseErr := <-leaseDone
		failure := errors.Join(fmt.Errorf("prepare run launch: %w", err), leaseErr)
		return PoolFailurePrepare, pool.releaseAfterStartupFailure(parent, scheduled, failure)
	}
	if err := validatePreparedSupervisionInput(scheduled, prepared); err != nil {
		cancelAttempt(err)
		leaseErr := <-leaseDone
		failure := errors.Join(fmt.Errorf("validate prepared run launch: %w", err), leaseErr)
		return PoolFailurePrepare, pool.releaseAfterStartupFailure(parent, scheduled, failure)
	}

	authority := &attemptLifecycleAuthority{
		ctx: attemptCtx, scheduler: pool.scheduler, core: pool.core, identities: pool.identities,
		prepared: prepared,
	}
	supervisionErr := pool.supervisor.Supervise(attemptCtx, prepared, authority)
	cancelAttempt(errors.New("attempt supervisor returned"))
	leaseErr := <-leaseDone
	cleanupErr := authority.dispatchCleanupError()
	if !authority.accepted() {
		failure := errors.Join(supervisionErr, leaseErr, cleanupErr)
		if failure == nil {
			failure = errors.New("attempt supervisor stopped before turn acceptance")
		}
		return PoolFailureSupervise, pool.releaseAfterStartupFailure(parent, scheduled, failure)
	}
	if cleanupErr != nil && supervisionErr == nil && leaseErr == nil {
		return PoolFailureCleanup, cleanupErr
	}
	return PoolFailureSupervise, errors.Join(supervisionErr, leaseErr, cleanupErr)
}

func (pool *Pool) keepAttemptLease(ctx context.Context, cancel context.CancelCauseFunc, scheduled ScheduledRunAttempt) error {
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
			if _, err := pool.renewAttemptExactly(ctx, request); err != nil {
				failure := fmt.Errorf("renew run-attempt leases: %w", err)
				cancel(failure)
				return failure
			}
		}
	}
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
