package harnesspool

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const maximumControllerLeaseTTL = time.Hour

type ControllerCore interface {
	ClaimRunDispatches(context.Context, ClaimRunDispatchesRequest) ([]RunDispatch, error)
	CompleteRunDispatch(context.Context, CompleteRunDispatchRequest) (bool, error)
	ReleaseRunDispatch(context.Context, ReleaseRunDispatchRequest) (bool, error)
	ClaimRunAttempt(context.Context, ClaimRunAttemptRequest) (ClaimRunAttemptResult, error)
}

type RunAttemptClaimIdentityAllocator interface {
	AllocateRunAttemptClaim() (RunAttemptClaimIdentity, error)
}

type ControllerConfig struct {
	HolderID          string
	DispatchLockTTL   time.Duration
	AttemptLeaseTTL   time.Duration
	LongPollTimeout   time.Duration
	ContentionBackoff time.Duration
}

type ScheduledRunAttempt struct {
	Dispatch RunDispatch
	Claim    ClaimRunAttemptResult
}

// Controller implements the durable scheduling edge only. Job creation and
// worker supervision consume ScheduledRunAttempt in the next layer; this type
// never completes a dispatch merely because the database claim succeeded.
type Controller struct {
	core       ControllerCore
	identities RunAttemptClaimIdentityAllocator
	config     ControllerConfig
}

func NewController(core ControllerCore, identities RunAttemptClaimIdentityAllocator, config ControllerConfig) (*Controller, error) {
	if core == nil {
		return nil, errors.New("controller core client is required")
	}
	if identities == nil {
		return nil, errors.New("controller identity allocator is required")
	}
	if err := validateControllerConfig(config); err != nil {
		return nil, err
	}
	return &Controller{core: core, identities: identities, config: config}, nil
}

// ClaimNextRunAttempt long-polls one scheduler delivery and claims its exact
// run generation. A nil result means no work, expected contention, or a stale
// delivery that core safely completed.
func (controller *Controller) ClaimNextRunAttempt(ctx context.Context) (*ScheduledRunAttempt, error) {
	if ctx == nil {
		return nil, errors.New("controller context is required")
	}
	dispatches, err := controller.core.ClaimRunDispatches(ctx, ClaimRunDispatchesRequest{
		OwnerID: controller.config.HolderID, Limit: 1, LockTTL: controller.config.DispatchLockTTL,
		WaitTimeout: controller.config.LongPollTimeout,
	})
	if err != nil {
		return nil, err
	}
	if len(dispatches) == 0 {
		return nil, nil
	}
	if len(dispatches) != 1 {
		return nil, errors.New("core returned more than one dispatch to a single-item controller claim")
	}
	dispatch := dispatches[0]
	if dispatch.ClaimOwnerID != controller.config.HolderID || dispatch.ClaimGeneration < 1 || dispatch.RunDispatchID == "" || dispatch.RunID == "" || dispatch.CurrentRunVersion < 1 {
		return nil, errors.New("core returned a run dispatch with an invalid controller authority tuple")
	}
	if dispatch.CurrentRunStatus != "queued" && dispatch.CurrentRunStatus != "starting" {
		if _, err := controller.completeDispatch(ctx, dispatch); err != nil {
			return nil, fmt.Errorf("complete stale run dispatch: %w", err)
		}
		return nil, nil
	}

	identity, err := controller.identities.AllocateRunAttemptClaim()
	if err != nil {
		releaseErr := controller.releaseDispatch(ctx, dispatch)
		return nil, errors.Join(fmt.Errorf("allocate run-attempt claim identity: %w", err), releaseErr)
	}
	request := ClaimRunAttemptRequest{
		RunID: dispatch.RunID, RunAttemptID: identity.RunAttemptID, HolderID: controller.config.HolderID,
		ExpectedRunVersion: dispatch.CurrentRunVersion, LeaseTTL: controller.config.AttemptLeaseTTL, Record: identity.Record,
	}
	claim, err := controller.claimRunAttemptExactly(ctx, request)
	if err == nil {
		return &ScheduledRunAttempt{Dispatch: dispatch, Claim: claim}, nil
	}

	var commandError *CoreCommandError
	if errors.As(err, &commandError) {
		switch commandError.Code {
		case "lease_held", "version_conflict":
			if releaseErr := controller.releaseDispatch(ctx, dispatch); releaseErr != nil {
				return nil, errors.Join(err, releaseErr)
			}
			return nil, nil
		case "invalid_state":
			if _, completeErr := controller.completeDispatch(ctx, dispatch); completeErr == nil {
				return nil, nil
			} else {
				var completeCommandError *CoreCommandError
				if errors.As(completeErr, &completeCommandError) && completeCommandError.Code == "invalid_state" {
					if releaseErr := controller.releaseDispatch(ctx, dispatch); releaseErr != nil {
						return nil, errors.Join(err, completeErr, releaseErr)
					}
					return nil, nil
				}
				return nil, errors.Join(err, completeErr)
			}
		}
	}
	releaseErr := controller.releaseDispatch(ctx, dispatch)
	return nil, errors.Join(fmt.Errorf("claim run attempt: %w", err), releaseErr)
}

// CompleteAcceptedDispatch asks core to remove the recovery delivery. Core
// rejects this call while the run is still queued/starting, so callers cannot
// accidentally acknowledge work before the turn acceptance boundary.
func (controller *Controller) CompleteAcceptedDispatch(ctx context.Context, dispatch RunDispatch) error {
	if ctx == nil {
		return errors.New("controller context is required")
	}
	_, err := controller.completeDispatch(ctx, dispatch)
	return err
}

// ReleaseUnstartedDispatch makes a startup failure visible again after the
// configured backoff. The attempt lease remains the sole launch authority.
func (controller *Controller) ReleaseUnstartedDispatch(ctx context.Context, dispatch RunDispatch) error {
	if ctx == nil {
		return errors.New("controller context is required")
	}
	return controller.releaseDispatch(ctx, dispatch)
}

func (controller *Controller) claimRunAttemptExactly(ctx context.Context, request ClaimRunAttemptRequest) (ClaimRunAttemptResult, error) {
	result, err := controller.core.ClaimRunAttempt(ctx, request)
	if err == nil {
		return result, nil
	}
	var commandError *CoreCommandError
	if errors.As(err, &commandError) || ctx.Err() != nil {
		return ClaimRunAttemptResult{}, err
	}
	// The command is exactly idempotent by attempt/event/outbox identities.
	// One immediate retry resolves the common "commit succeeded, response was
	// lost" case without allocating a second attempt identity.
	return controller.core.ClaimRunAttempt(ctx, request)
}

func (controller *Controller) completeDispatch(ctx context.Context, dispatch RunDispatch) (bool, error) {
	return controller.core.CompleteRunDispatch(ctx, CompleteRunDispatchRequest{
		RunDispatchID: dispatch.RunDispatchID, RunID: dispatch.RunID,
		OwnerID: controller.config.HolderID, ClaimGeneration: dispatch.ClaimGeneration,
	})
}

func (controller *Controller) releaseDispatch(ctx context.Context, dispatch RunDispatch) error {
	_, err := controller.core.ReleaseRunDispatch(ctx, ReleaseRunDispatchRequest{
		RunDispatchID: dispatch.RunDispatchID, RunID: dispatch.RunID,
		OwnerID: controller.config.HolderID, ClaimGeneration: dispatch.ClaimGeneration,
		RetryAfter: controller.config.ContentionBackoff,
	})
	if err != nil {
		return fmt.Errorf("release run dispatch: %w", err)
	}
	return nil
}

func validateControllerConfig(config ControllerConfig) error {
	if !utf8.ValidString(config.HolderID) || strings.IndexByte(config.HolderID, 0) >= 0 || len(config.HolderID) < 1 || len(config.HolderID) > 256 {
		return errors.New("controller holder ID must contain between 1 and 256 valid UTF-8 bytes without NUL")
	}
	for field, duration := range map[string]time.Duration{
		"dispatch lock TTL": config.DispatchLockTTL,
		"attempt lease TTL": config.AttemptLeaseTTL,
	} {
		if duration < time.Millisecond || duration > maximumControllerLeaseTTL {
			return fmt.Errorf("controller %s must be between 1ms and %s", field, maximumControllerLeaseTTL)
		}
	}
	if config.LongPollTimeout < 0 || config.LongPollTimeout > 30*time.Second {
		return errors.New("controller long-poll timeout must be between zero and 30s")
	}
	if config.ContentionBackoff < 0 || config.ContentionBackoff > 24*time.Hour {
		return errors.New("controller contention backoff must be between zero and 24h")
	}
	return nil
}
