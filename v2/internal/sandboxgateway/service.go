package sandboxgateway

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/executionbackend"
	"github.com/agentserver/agentserver/v2/internal/sandboxcontract"
)

type IDGenerator func() (string, error)

type Config struct {
	Core               Core
	Provider           Provider
	Limits             sandboxcontract.Limits
	ProviderRegion     string
	ProviderPSM        string
	IdleTTL            time.Duration
	EnsureTimeout      time.Duration
	EnsurePollInterval time.Duration
	Root               string
	Platform           string
	WorkspaceAllowlist []string
	IDGenerator        IDGenerator
	Now                func() time.Time
	Logger             *slog.Logger
}

type Service struct {
	core               Core
	provider           Provider
	limits             sandboxcontract.Limits
	providerRegion     string
	providerPSM        string
	idleTTL            time.Duration
	ensureTimeout      time.Duration
	ensurePollInterval time.Duration
	root               string
	platform           string
	workspaceAllowlist map[string]struct{}
	idGenerator        IDGenerator
	now                func() time.Time
	logger             *slog.Logger
}

type Error struct {
	HTTPStatus int
	Code       string
	Message    string
	Outcome    executionbackend.DispatchOutcome
	Cause      error
}

func (serviceError *Error) Error() string {
	if serviceError == nil {
		return "<nil>"
	}
	message := serviceError.Message
	if message == "" {
		message = serviceError.Code
	}
	if serviceError.Cause != nil {
		return message + ": " + serviceError.Cause.Error()
	}
	return message
}

func (serviceError *Error) Unwrap() error {
	if serviceError == nil {
		return nil
	}
	return serviceError.Cause
}

func NewService(config Config) (*Service, error) {
	if config.Core == nil || config.Provider == nil {
		return nil, errors.New("sandbox gateway core and provider are required")
	}
	if err := config.Limits.Validate(); err != nil {
		return nil, fmt.Errorf("sandbox gateway limits: %w", err)
	}
	if config.ProviderRegion == "" || len(config.ProviderRegion) > 128 || config.ProviderPSM == "" || len(config.ProviderPSM) > 256 {
		return nil, errors.New("sandbox gateway provider region or PSM is invalid")
	}
	if config.IdleTTL <= 0 || config.IdleTTL > time.Duration(config.Limits.MaxSandboxTTLSeconds)*time.Second || config.IdleTTL%time.Second != 0 {
		return nil, errors.New("sandbox gateway idle TTL must be whole seconds within sandbox TTL limits")
	}
	if config.EnsureTimeout <= 0 || config.EnsureTimeout > time.Minute {
		return nil, errors.New("sandbox gateway ensure timeout must be positive and at most one minute")
	}
	if config.EnsurePollInterval <= 0 || config.EnsurePollInterval > config.EnsureTimeout {
		return nil, errors.New("sandbox gateway ensure poll interval is invalid")
	}
	if config.Root == "" {
		config.Root = "/workspace"
	}
	if config.Platform == "" {
		config.Platform = "linux-amd64"
	}
	if config.IDGenerator == nil {
		config.IDGenerator = randomUUID
	}
	if config.Now == nil {
		config.Now = func() time.Time { return time.Now().UTC() }
	}
	var workspaceAllowlist map[string]struct{}
	if config.WorkspaceAllowlist != nil {
		if len(config.WorkspaceAllowlist) < 1 || len(config.WorkspaceAllowlist) > 64 {
			return nil, errors.New("sandbox gateway workspace allowlist must contain between 1 and 64 entries")
		}
		workspaceAllowlist = make(map[string]struct{}, len(config.WorkspaceAllowlist))
		for _, workspaceID := range config.WorkspaceAllowlist {
			if workspaceID == "" || len(workspaceID) > 128 {
				return nil, errors.New("sandbox gateway workspace allowlist contains an invalid identifier")
			}
			if _, duplicate := workspaceAllowlist[workspaceID]; duplicate {
				return nil, errors.New("sandbox gateway workspace allowlist contains a duplicate")
			}
			workspaceAllowlist[workspaceID] = struct{}{}
		}
	}
	return &Service{
		core: config.Core, provider: config.Provider, limits: config.Limits,
		providerRegion: config.ProviderRegion, providerPSM: config.ProviderPSM,
		idleTTL: config.IdleTTL, ensureTimeout: config.EnsureTimeout,
		ensurePollInterval: config.EnsurePollInterval, root: config.Root,
		platform: config.Platform, workspaceAllowlist: workspaceAllowlist,
		idGenerator: config.IDGenerator, now: config.Now, logger: config.Logger,
	}, nil
}

func (service *Service) EnsureSandbox(ctx context.Context, principal Principal, request sandboxcontract.EnsureSandboxRequest) (sandboxcontract.EnsureSandboxResponse, error) {
	startedAt := time.Now()
	if err := request.Validate(service.limits); err != nil {
		return sandboxcontract.EnsureSandboxResponse{}, invalidRequest(err)
	}
	if !service.workspaceAllowed(request.Session.WorkspaceID) {
		return sandboxcontract.EnsureSandboxResponse{}, forbidden(errors.New("workspace is not enabled for managed execution"))
	}
	if err := bindSessionPrincipal(principal, ActionEnsure, request.Session); err != nil {
		return sandboxcontract.EnsureSandboxResponse{}, forbidden(err)
	}
	service.logEnsure("info", "begin", request.RequestID, principal, corecontract.ManagedSandboxState{}, ProviderSandbox{}, 0, startedAt)
	sandboxID, err := service.idGenerator()
	if err != nil {
		return sandboxcontract.EnsureSandboxResponse{}, unavailable("identity_generation_failed", err)
	}
	createKey, err := service.idGenerator()
	if err != nil {
		return sandboxcontract.EnsureSandboxResponse{}, unavailable("identity_generation_failed", err)
	}
	reserved, err := service.core.ReserveManagedSandbox(ctx, corecontract.ReserveManagedSandboxRequest{
		SandboxID: sandboxID, WorkspaceID: request.Session.WorkspaceID,
		SessionID: request.Session.SessionID, EnvironmentID: request.Session.EnvironmentID,
		ProviderRegion: service.providerRegion, ProviderPSM: service.providerPSM,
		ProviderSessionRef: "", CreateIdempotencyKey: createKey,
		RequestedTTLSeconds: request.RequestedTTLSeconds, IdleTTLSeconds: int64(service.idleTTL / time.Second),
	})
	if err != nil {
		service.logEnsure("error", "reserve_failed", request.RequestID, principal, corecontract.ManagedSandboxState{}, ProviderSandbox{}, 0, startedAt)
		return sandboxcontract.EnsureSandboxResponse{}, coreServiceError("reserve_failed", err)
	}
	service.logEnsure("info", "reserved", request.RequestID, principal, reserved.Sandbox, ProviderSandbox{}, 0, startedAt,
		"reservation_created", reserved.Created)
	providerSandbox, state, polls, err := service.ensureManagedSandbox(ctx, reserved.Sandbox, func(stage string, observed corecontract.ManagedSandboxState, provider ProviderSandbox, poll int) {
		service.logEnsure("info", stage, request.RequestID, principal, observed, provider, poll, startedAt)
	})
	if err != nil {
		service.logEnsure("error", safeServiceErrorCode(err, "ensure_failed"), request.RequestID, principal, state, providerSandbox, polls, startedAt)
		return sandboxcontract.EnsureSandboxResponse{}, err
	}
	service.logEnsure("info", "ready", request.RequestID, principal, state, providerSandbox, polls, startedAt)
	return sandboxcontract.EnsureSandboxResponse{Sandbox: service.contractSandbox(state, providerSandbox)}, nil
}

func (service *Service) ensureManagedSandbox(
	ctx context.Context,
	state corecontract.ManagedSandboxState,
	logTransition func(string, corecontract.ManagedSandboxState, ProviderSandbox, int),
) (ProviderSandbox, corecontract.ManagedSandboxState, int, error) {
	deadline := service.now().Add(service.ensureTimeout)
	polls := 0
	lastLoggedCoreState := ""
	lastLoggedProviderState := ProviderSandboxState("")
	lastLoggedProviderRef := ""
	lastLoggedProviderStatusClass := ""
	lastLoggedExecutionReady := false
	var lastProvider ProviderSandbox
	logObservation := func(stage string, provider ProviderSandbox) {
		if logTransition == nil {
			return
		}
		if stage == "poll" && state.ObservedState == lastLoggedCoreState && provider.State == lastLoggedProviderState &&
			provider.SessionRef == lastLoggedProviderRef && provider.ProviderStatusClass == lastLoggedProviderStatusClass &&
			provider.ExecutionReady == lastLoggedExecutionReady {
			return
		}
		logTransition(stage, state, provider, polls)
		lastLoggedCoreState, lastLoggedProviderState, lastLoggedProviderRef = state.ObservedState, provider.State, provider.SessionRef
		lastLoggedProviderStatusClass, lastLoggedExecutionReady = provider.ProviderStatusClass, provider.ExecutionReady
	}
	for {
		switch state.ObservedState {
		case "ready":
			providerSandbox, err := service.provider.GetSandbox(ctx, state.ProviderSessionRef)
			polls++
			if err == nil {
				lastProvider = providerSandbox
				providerSandbox, observed, ready, convergeErr := service.convergeProviderSandbox(ctx, state, providerSandbox)
				if convergeErr != nil {
					return providerSandbox, state, polls, convergeErr
				}
				state = observed
				logObservation("poll", providerSandbox)
				if ready {
					return providerSandbox, observed, polls, nil
				}
				break
			}
			if errors.Is(err, ErrProviderSandboxNotFound) {
				_, _ = service.observeProviderProblem(ctx, state, "failed", "provider_session_missing", state.ProviderSessionRef)
				return ProviderSandbox{}, state, polls, unavailable("provider_session_missing", err)
			}
			return ProviderSandbox{}, state, polls, unavailable("provider_get_failed", err)
		case "reserved":
			begun, err := service.core.BeginManagedSandboxCreate(ctx, corecontract.BeginManagedSandboxCreateRequest{
				SandboxID: state.SandboxID, Generation: state.Generation, ExpectedVersion: state.Version,
			})
			if err != nil {
				return ProviderSandbox{}, state, polls, coreServiceError("begin_create_failed", err)
			}
			state = begun.Sandbox
			logObservation("create_started", ProviderSandbox{})
			if begun.Changed {
				providerSandbox, observed, ready, createErr := service.createProviderSandbox(ctx, state)
				polls++
				lastProvider = providerSandbox
				if createErr != nil {
					return providerSandbox, observed, polls, createErr
				}
				state = observed
				logObservation("provider_created", providerSandbox)
				if ready {
					return providerSandbox, observed, polls, nil
				}
			}
		case "creating", "unknown":
			providerSandbox, err := service.findOrGetProviderSandbox(ctx, state)
			polls++
			if err == nil {
				lastProvider = providerSandbox
				providerSandbox, observed, ready, convergeErr := service.convergeProviderSandbox(ctx, state, providerSandbox)
				if convergeErr != nil {
					return providerSandbox, state, polls, convergeErr
				}
				state = observed
				logObservation("poll", providerSandbox)
				if ready {
					return providerSandbox, observed, polls, nil
				}
				break
			}
			if !errors.Is(err, ErrProviderSandboxNotFound) {
				return ProviderSandbox{}, state, polls, unavailable("provider_get_failed", err)
			}
		case "failed":
			return ProviderSandbox{}, state, polls, conflict("sandbox_failed", errors.New("managed sandbox generation is failed and must be replaced"))
		case "deleting", "deleted":
			return ProviderSandbox{}, state, polls, conflict("sandbox_deleting", errors.New("managed sandbox generation is being deleted"))
		default:
			return ProviderSandbox{}, state, polls, unavailable("invalid_core_state", fmt.Errorf("unsupported managed sandbox state %q", state.ObservedState))
		}
		if !service.now().Before(deadline) {
			failed, markedFailed, failErr := service.failProviderReadyTimeout(ctx, state)
			if failErr != nil {
				return lastProvider, state, polls, coreServiceError("observe_provider_ready_timeout_failed", failErr)
			}
			state = failed
			if !markedFailed && state.ObservedState == "ready" {
				// A concurrent ensure won the generation fence at the deadline.
				// Re-enter once so the ready provider observation is returned rather
				// than converting a successful allocation into a timeout.
				continue
			}
			if !markedFailed {
				logObservation("ensure_timeout", lastProvider)
				return lastProvider, state, polls, unavailable("ensure_timeout", errors.New("managed sandbox readiness deadline elapsed without an exact provider session"))
			}
			logObservation("provider_ready_timeout", lastProvider)
			return lastProvider, state, polls, unavailable("provider_ready_timeout", errors.New("managed sandbox did not become executable before deadline"))
		}
		if err := waitContext(ctx, service.ensurePollInterval); err != nil {
			return ProviderSandbox{}, state, polls, unavailable("ensure_cancelled", err)
		}
		current, err := service.core.GetManagedSandbox(ctx, state.SandboxID, state.Generation)
		if err != nil {
			return ProviderSandbox{}, state, polls, coreServiceError("get_failed", err)
		}
		state = current.Sandbox
	}
}

func (service *Service) createProviderSandbox(ctx context.Context, state corecontract.ManagedSandboxState) (ProviderSandbox, corecontract.ManagedSandboxState, bool, error) {
	providerSandbox, err := service.provider.CreateSandbox(ctx, CreateSandboxRequest{
		SessionRef: state.ProviderSessionRef, SandboxID: state.SandboxID, IdempotencyKey: state.CreateIdempotencyKey,
		WorkspaceID: state.WorkspaceID, SessionID: state.SessionID, EnvironmentID: state.EnvironmentID,
		Region: state.ProviderRegion, PSM: state.ProviderPSM,
		TTL: time.Duration(state.RequestedTTLSeconds) * time.Second,
	})
	if err != nil {
		var providerError *ProviderError
		if errors.As(err, &providerError) && providerError.Ambiguous {
			observed, getErr := service.findOrGetProviderSandbox(ctx, state)
			if getErr == nil {
				return service.convergeProviderSandbox(ctx, state, observed)
			}
			// Preserve the recovery lookup result rather than only the original
			// create error. In particular, provider_search_incomplete is the
			// operator-visible proof that no cleanup decision may be made from
			// this observation. A later reconcile must repeat the exact search
			// and may mutate only after that search proves absence or bounded
			// duplicate ambiguity.
			code := safeProviderCode(providerError.Code, "provider_create_unknown")
			var lookupError *ProviderError
			if errors.As(getErr, &lookupError) {
				code = safeProviderCode(lookupError.Code, code)
			}
			if observedProblem, observeErr := service.observeProviderProblem(ctx, state, "unknown", code, state.ProviderSessionRef); observeErr == nil {
				state = observedProblem.Sandbox
			}
			return ProviderSandbox{}, state, false, unavailable("provider_create_unknown", err)
		}
		_, _ = service.observeProviderProblem(ctx, state, "failed", safeProviderErrorCode(err, "provider_create_failed"), state.ProviderSessionRef)
		return ProviderSandbox{}, state, false, unavailable("provider_create_failed", err)
	}
	converged, observed, ready, convergeErr := service.convergeProviderSandbox(ctx, state, providerSandbox)
	if convergeErr != nil {
		// A syntactically invalid provider response (for example an empty
		// session reference) must not leave a desired-ready row in creating
		// forever. Record a terminal provider observation so the reconciler can
		// perform the identity-bounded cleanup. Core transport/version errors
		// are deliberately left untouched: they are not evidence that the
		// provider resource itself failed.
		if providerObservationFailureCode(convergeErr) != "" {
			code := providerObservationFailureCode(convergeErr)
			if failed, observeErr := service.observeProviderProblem(ctx, state, "failed", code, state.ProviderSessionRef); observeErr == nil {
				observed = failed.Sandbox
			}
		}
	}
	return converged, observed, ready, convergeErr
}

// convergeProviderSandbox records the provider's lifecycle observation under
// the Core generation/version fence. A provider may return before its session
// is ready; that is a normal asynchronous create and callers should poll when
// ready is false. Terminal provider loss is recorded as failed so the delete
// reconciler can clean up the Core generation and permit a future generation.
func (service *Service) convergeProviderSandbox(ctx context.Context, state corecontract.ManagedSandboxState, providerSandbox ProviderSandbox) (ProviderSandbox, corecontract.ManagedSandboxState, bool, error) {
	if err := validateProviderSandbox(providerSandbox); err != nil {
		return ProviderSandbox{}, state, false, unavailable("invalid_provider_state", err)
	}
	if state.ProviderSessionRef != "" && providerSandbox.SessionRef != state.ProviderSessionRef {
		return ProviderSandbox{}, state, false, unavailable("provider_identity_mismatch", errors.New("provider returned a different session reference"))
	}
	if providerSandbox.State == ProviderSandboxReady {
		if !providerSandbox.ExpiresAt.After(service.now()) {
			observed, err := service.observeProviderProblem(ctx, state, "failed", "provider_expired", providerSandbox.SessionRef)
			if err != nil {
				return ProviderSandbox{}, state, false, coreServiceError("observe_provider_expired_failed", err)
			}
			return providerSandbox, observed.Sandbox, false, nil
		}
		ready, observed, err := service.convergeReady(ctx, state, providerSandbox)
		return ready, observed, true, err
	}

	code := "provider_state_unknown"
	observedState := "unknown"
	switch providerSandbox.State {
	case ProviderSandboxCreating:
		// A ready generation must not regress to creating. Preserve the
		// generation fence and force an operator-visible unknown state instead.
		if state.ObservedState != "ready" {
			observedState = "creating"
			code = ""
		} else {
			code = "provider_state_regressed"
		}
	case ProviderSandboxDeleting:
		observedState = "failed"
		code = "provider_deleting_unexpected"
	case ProviderSandboxDeleted:
		observedState = "failed"
		code = "provider_deleted"
	case ProviderSandboxFailed:
		observedState = "failed"
		code = "provider_failed"
	case ProviderSandboxUnknown:
		code = "provider_state_unknown"
	default:
		code = "provider_state_unknown"
	}
	if observedState == "creating" {
		observed, err := service.core.ObserveManagedSandbox(ctx, corecontract.ObserveManagedSandboxRequest{
			SandboxID: state.SandboxID, Generation: state.Generation, ExpectedVersion: state.Version,
			ObservedState: "creating", ProviderSessionRef: providerSandbox.SessionRef,
		})
		if err != nil {
			current, getErr := service.core.GetManagedSandbox(ctx, state.SandboxID, state.Generation)
			if getErr == nil && current.Sandbox.ObservedState == "creating" && current.Sandbox.ProviderSessionRef == providerSandbox.SessionRef {
				return providerSandbox, current.Sandbox, false, nil
			}
			return ProviderSandbox{}, state, false, coreServiceError("observe_provider_creating_failed", err)
		}
		return providerSandbox, observed.Sandbox, false, nil
	}
	observed, err := service.observeProviderProblem(ctx, state, observedState, code, providerSandbox.SessionRef)
	if err != nil {
		return ProviderSandbox{}, state, false, coreServiceError("observe_provider_state_failed", err)
	}
	return providerSandbox, observed.Sandbox, false, nil
}

func (service *Service) convergeReady(ctx context.Context, state corecontract.ManagedSandboxState, providerSandbox ProviderSandbox) (ProviderSandbox, corecontract.ManagedSandboxState, error) {
	if err := validateProviderSandbox(providerSandbox); err != nil {
		return ProviderSandbox{}, state, unavailable("invalid_provider_state", err)
	}
	if state.ProviderSessionRef != "" && providerSandbox.SessionRef != state.ProviderSessionRef {
		return ProviderSandbox{}, state, unavailable("provider_identity_mismatch", errors.New("provider returned a different session reference"))
	}
	if providerSandbox.State != ProviderSandboxReady {
		return ProviderSandbox{}, state, unavailable("provider_not_ready", fmt.Errorf("provider state is %q", providerSandbox.State))
	}
	if !providerSandbox.ExpiresAt.After(service.now()) {
		return ProviderSandbox{}, state, unavailable("provider_expired", errors.New("provider sandbox expiry is not in the future"))
	}
	if state.ObservedState == "ready" && state.ProviderSessionRef == providerSandbox.SessionRef &&
		state.ExpiresAt != nil && state.ExpiresAt.Equal(providerSandbox.ExpiresAt) {
		return providerSandbox, state, nil
	}
	observed, err := service.core.ObserveManagedSandbox(ctx, corecontract.ObserveManagedSandboxRequest{
		SandboxID: state.SandboxID, Generation: state.Generation, ExpectedVersion: state.Version,
		ObservedState: "ready", ProviderSessionRef: providerSandbox.SessionRef, ExpiresAt: &providerSandbox.ExpiresAt,
	})
	if err != nil {
		current, getErr := service.core.GetManagedSandbox(ctx, state.SandboxID, state.Generation)
		if getErr == nil && current.Sandbox.ObservedState == "ready" &&
			current.Sandbox.ProviderSessionRef == providerSandbox.SessionRef {
			return providerSandbox, current.Sandbox, nil
		}
		return ProviderSandbox{}, state, coreServiceError("observe_ready_failed", err)
	}
	return providerSandbox, observed.Sandbox, nil
}

func (service *Service) findOrGetProviderSandbox(ctx context.Context, state corecontract.ManagedSandboxState) (ProviderSandbox, error) {
	if state.ProviderSessionRef != "" {
		return service.provider.GetSandbox(ctx, state.ProviderSessionRef)
	}
	return service.provider.FindSandbox(ctx, service.providerFindRequest(state))
}

func (service *Service) providerFindRequest(state corecontract.ManagedSandboxState) FindSandboxRequest {
	return FindSandboxRequest{
		SandboxID: state.SandboxID, IdempotencyKey: state.CreateIdempotencyKey,
		WorkspaceID: state.WorkspaceID, SessionID: state.SessionID,
		EnvironmentID: state.EnvironmentID, Region: state.ProviderRegion,
		PSM: state.ProviderPSM,
	}
}

func (service *Service) deleteProviderSandbox(ctx context.Context, state corecontract.ManagedSandboxState) error {
	return service.provider.DeleteSandbox(ctx, DeleteSandboxProviderRequest{
		SessionRef: state.ProviderSessionRef,
		Identity:   service.providerFindRequest(state),
	})
}

func (service *Service) GetSandbox(ctx context.Context, principal Principal, request sandboxcontract.GetSandboxRequest) (sandboxcontract.SandboxResponse, error) {
	if err := request.Validate(service.limits); err != nil {
		return sandboxcontract.SandboxResponse{}, invalidRequest(err)
	}
	if !service.workspaceAllowed(request.Session.WorkspaceID) {
		return sandboxcontract.SandboxResponse{}, forbidden(errors.New("workspace is not enabled for managed execution"))
	}
	if err := bindSessionPrincipal(principal, ActionGet, request.Session); err != nil {
		return sandboxcontract.SandboxResponse{}, forbidden(err)
	}
	if err := bindLifecycleRef(principal, request.Ref); err != nil {
		return sandboxcontract.SandboxResponse{}, forbidden(err)
	}
	state, err := service.core.GetManagedSandbox(ctx, request.Ref.SandboxID, request.Ref.TargetGeneration)
	if err != nil {
		return sandboxcontract.SandboxResponse{}, coreServiceError("get_failed", err)
	}
	if err := matchSessionState(request.Session, state.Sandbox); err != nil {
		return sandboxcontract.SandboxResponse{}, forbidden(err)
	}
	providerSandbox, err := service.provider.GetSandbox(ctx, state.Sandbox.ProviderSessionRef)
	if err != nil {
		if errors.Is(err, ErrProviderSandboxNotFound) {
			_, _ = service.observeProviderProblem(ctx, state.Sandbox, "failed", "provider_session_missing", state.Sandbox.ProviderSessionRef)
			return sandboxcontract.SandboxResponse{}, unavailable("provider_session_missing", err)
		}
		return sandboxcontract.SandboxResponse{}, unavailable("provider_get_failed", err)
	}
	providerSandbox, converged, ready, err := service.convergeProviderSandbox(ctx, state.Sandbox, providerSandbox)
	if err != nil {
		return sandboxcontract.SandboxResponse{}, err
	}
	if !ready {
		return sandboxcontract.SandboxResponse{}, unavailable("provider_not_ready", errors.New("provider sandbox is not ready"))
	}
	return sandboxcontract.SandboxResponse{Sandbox: service.contractSandbox(converged, providerSandbox)}, nil
}

func (service *Service) RenewSandboxActivity(ctx context.Context, principal Principal, request sandboxcontract.RenewSandboxActivityRequest) (sandboxcontract.SandboxResponse, error) {
	if err := request.Validate(service.limits); err != nil {
		return sandboxcontract.SandboxResponse{}, invalidRequest(err)
	}
	if !service.workspaceAllowed(request.Session.WorkspaceID) {
		return sandboxcontract.SandboxResponse{}, forbidden(errors.New("workspace is not enabled for managed execution"))
	}
	if err := bindSessionPrincipal(principal, ActionRenewActivity, request.Session); err != nil {
		return sandboxcontract.SandboxResponse{}, forbidden(err)
	}
	if err := bindLifecycleRef(principal, request.Ref); err != nil {
		return sandboxcontract.SandboxResponse{}, forbidden(err)
	}
	if request.RunAttemptID != principal.RunAttemptID || request.RunAttemptGeneration != principal.RunAttemptGeneration {
		return sandboxcontract.SandboxResponse{}, forbidden(errors.New("activity request differs from capability attempt"))
	}
	renewed, err := service.core.RenewManagedSandboxActivity(ctx, corecontract.RenewManagedSandboxActivityRequest{
		SandboxID: request.Ref.SandboxID, Generation: request.Ref.TargetGeneration,
		RunID: principal.RunID, RunAttemptID: principal.RunAttemptID,
		RunAttemptGeneration: principal.RunAttemptGeneration, HolderID: principal.HolderID,
		ActivityTTLMillis: request.ActivityTTLSeconds * 1000,
	})
	if err != nil {
		return sandboxcontract.SandboxResponse{}, coreServiceError("renew_activity_failed", err)
	}
	if err := matchSessionState(request.Session, renewed.Sandbox); err != nil {
		return sandboxcontract.SandboxResponse{}, forbidden(err)
	}
	providerSandbox, err := service.provider.GetSandbox(ctx, renewed.Sandbox.ProviderSessionRef)
	if err != nil {
		if errors.Is(err, ErrProviderSandboxNotFound) {
			_, _ = service.observeProviderProblem(ctx, renewed.Sandbox, "failed", "provider_session_missing", renewed.Sandbox.ProviderSessionRef)
			return sandboxcontract.SandboxResponse{}, unavailable("provider_session_missing", err)
		}
		return sandboxcontract.SandboxResponse{}, unavailable("provider_get_failed", err)
	}
	providerSandbox, converged, ready, err := service.convergeProviderSandbox(ctx, renewed.Sandbox, providerSandbox)
	if err != nil {
		return sandboxcontract.SandboxResponse{}, err
	}
	if !ready {
		return sandboxcontract.SandboxResponse{}, unavailable("provider_not_ready", errors.New("provider sandbox is not ready"))
	}
	return sandboxcontract.SandboxResponse{Sandbox: service.contractSandbox(converged, providerSandbox), Changed: renewed.Changed}, nil
}

func (service *Service) ReleaseSandboxActivity(ctx context.Context, principal Principal, request sandboxcontract.ReleaseSandboxActivityRequest) (sandboxcontract.SandboxResponse, error) {
	if err := request.Validate(service.limits); err != nil {
		return sandboxcontract.SandboxResponse{}, invalidRequest(err)
	}
	if !service.workspaceAllowed(request.Session.WorkspaceID) {
		return sandboxcontract.SandboxResponse{}, forbidden(errors.New("workspace is not enabled for managed execution"))
	}
	if err := bindSessionPrincipal(principal, ActionReleaseActivity, request.Session); err != nil {
		return sandboxcontract.SandboxResponse{}, forbidden(err)
	}
	if err := bindLifecycleRef(principal, request.Ref); err != nil {
		return sandboxcontract.SandboxResponse{}, forbidden(err)
	}
	if request.RunAttemptID != principal.RunAttemptID || request.RunAttemptGeneration != principal.RunAttemptGeneration {
		return sandboxcontract.SandboxResponse{}, forbidden(errors.New("activity request differs from capability attempt"))
	}
	released, err := service.core.ReleaseManagedSandboxActivity(ctx, corecontract.ReleaseManagedSandboxActivityRequest{
		SandboxID: request.Ref.SandboxID, Generation: request.Ref.TargetGeneration,
		RunID: principal.RunID, RunAttemptID: principal.RunAttemptID,
		RunAttemptGeneration: principal.RunAttemptGeneration, HolderID: principal.HolderID,
		IdleTTLMillis: service.idleTTL.Milliseconds(),
	})
	if err != nil {
		return sandboxcontract.SandboxResponse{}, coreServiceError("release_activity_failed", err)
	}
	if err := matchSessionState(request.Session, released.Sandbox); err != nil {
		return sandboxcontract.SandboxResponse{}, forbidden(err)
	}
	providerSandbox, err := service.provider.GetSandbox(ctx, released.Sandbox.ProviderSessionRef)
	if err != nil {
		if errors.Is(err, ErrProviderSandboxNotFound) {
			_, _ = service.observeProviderProblem(ctx, released.Sandbox, "failed", "provider_session_missing", released.Sandbox.ProviderSessionRef)
			return sandboxcontract.SandboxResponse{}, unavailable("provider_session_missing", err)
		}
		return sandboxcontract.SandboxResponse{}, unavailable("provider_get_failed", err)
	}
	providerSandbox, converged, ready, err := service.convergeProviderSandbox(ctx, released.Sandbox, providerSandbox)
	if err != nil {
		return sandboxcontract.SandboxResponse{}, err
	}
	if !ready {
		return sandboxcontract.SandboxResponse{}, unavailable("provider_not_ready", errors.New("provider sandbox is not ready"))
	}
	return sandboxcontract.SandboxResponse{Sandbox: service.contractSandbox(converged, providerSandbox), Changed: released.Changed}, nil
}

func (service *Service) SetSandboxTimeout(ctx context.Context, principal Principal, request sandboxcontract.SetSandboxTimeoutRequest) (sandboxcontract.SandboxResponse, error) {
	if err := request.Validate(service.limits); err != nil {
		return sandboxcontract.SandboxResponse{}, invalidRequest(err)
	}
	if !service.workspaceAllowed(request.Session.WorkspaceID) {
		return sandboxcontract.SandboxResponse{}, forbidden(errors.New("workspace is not enabled for managed execution"))
	}
	if err := bindSessionPrincipal(principal, ActionSetTimeout, request.Session); err != nil {
		return sandboxcontract.SandboxResponse{}, forbidden(err)
	}
	if err := bindLifecycleRef(principal, request.Ref); err != nil {
		return sandboxcontract.SandboxResponse{}, forbidden(err)
	}
	current, err := service.core.GetManagedSandbox(ctx, request.Ref.SandboxID, request.Ref.TargetGeneration)
	if err != nil {
		return sandboxcontract.SandboxResponse{}, coreServiceError("get_failed", err)
	}
	if err := matchReadySessionState(request.Session, current.Sandbox, service.now()); err != nil {
		return sandboxcontract.SandboxResponse{}, conflict("sandbox_not_ready", err)
	}
	providerSandbox, err := service.provider.SetSandboxTimeout(ctx, SetSandboxTimeoutProviderRequest{
		SessionRef: current.Sandbox.ProviderSessionRef, TTL: time.Duration(request.TTLSeconds) * time.Second,
	})
	if err != nil {
		return sandboxcontract.SandboxResponse{}, unavailable("provider_timeout_update_failed", err)
	}
	providerSandbox, converged, ready, err := service.convergeProviderSandbox(ctx, current.Sandbox, providerSandbox)
	if err != nil {
		return sandboxcontract.SandboxResponse{}, err
	}
	if !ready {
		return sandboxcontract.SandboxResponse{}, unavailable("provider_not_ready", errors.New("provider sandbox is not ready after timeout update"))
	}
	return sandboxcontract.SandboxResponse{Sandbox: service.contractSandbox(converged, providerSandbox), Changed: true}, nil
}

func (service *Service) DeleteSandbox(ctx context.Context, principal Principal, request sandboxcontract.DeleteSandboxRequest) (sandboxcontract.SandboxResponse, error) {
	if err := request.Validate(service.limits); err != nil {
		return sandboxcontract.SandboxResponse{}, invalidRequest(err)
	}
	if !service.workspaceAllowed(request.Session.WorkspaceID) {
		return sandboxcontract.SandboxResponse{}, forbidden(errors.New("workspace is not enabled for managed execution"))
	}
	if err := bindSessionPrincipal(principal, ActionDelete, request.Session); err != nil {
		return sandboxcontract.SandboxResponse{}, forbidden(err)
	}
	if err := bindLifecycleRef(principal, request.Ref); err != nil {
		return sandboxcontract.SandboxResponse{}, forbidden(err)
	}
	current, err := service.core.GetManagedSandbox(ctx, request.Ref.SandboxID, request.Ref.TargetGeneration)
	if err != nil {
		return sandboxcontract.SandboxResponse{}, coreServiceError("get_failed", err)
	}
	if err := matchSessionState(request.Session, current.Sandbox); err != nil {
		return sandboxcontract.SandboxResponse{}, forbidden(err)
	}
	deleting, err := service.core.BeginManagedSandboxDelete(ctx, corecontract.BeginManagedSandboxDeleteRequest{
		SandboxID: request.Ref.SandboxID, Generation: request.Ref.TargetGeneration,
		ExpectedVersion: current.Sandbox.Version, Reason: request.Reason,
	})
	if err != nil {
		return sandboxcontract.SandboxResponse{}, coreServiceError("begin_delete_failed", err)
	}
	if deleting.Sandbox.ObservedState == "deleted" {
		return sandboxcontract.SandboxResponse{Sandbox: service.contractSandbox(deleting.Sandbox, ProviderSandbox{}), Changed: deleting.Changed}, nil
	}
	err = service.deleteProviderSandbox(ctx, deleting.Sandbox)
	if err != nil && !errors.Is(err, ErrProviderSandboxNotFound) {
		_ = service.observeProblem(ctx, deleting.Sandbox, "unknown", safeProviderErrorCode(err, "provider_delete_unknown"))
		return sandboxcontract.SandboxResponse{}, unavailable("provider_delete_failed", err)
	}
	deleted, err := service.core.ObserveManagedSandbox(ctx, corecontract.ObserveManagedSandboxRequest{
		SandboxID: deleting.Sandbox.SandboxID, Generation: deleting.Sandbox.Generation,
		ExpectedVersion: deleting.Sandbox.Version, ObservedState: "deleted",
	})
	if err != nil {
		current, getErr := service.core.GetManagedSandbox(ctx, deleting.Sandbox.SandboxID, deleting.Sandbox.Generation)
		if getErr == nil && current.Sandbox.ObservedState == "deleted" && current.Sandbox.DesiredState == "deleted" {
			return sandboxcontract.SandboxResponse{Sandbox: service.contractSandbox(current.Sandbox, ProviderSandbox{}), Changed: true}, nil
		}
		return sandboxcontract.SandboxResponse{}, coreServiceError("observe_deleted_failed", err)
	}
	return sandboxcontract.SandboxResponse{Sandbox: service.contractSandbox(deleted.Sandbox, ProviderSandbox{}), Changed: true}, nil
}

func (service *Service) RunCommand(ctx context.Context, principal Principal, request sandboxcontract.RunCommandRequest) (executionbackend.Exchange, error) {
	if err := request.Validate(service.limits); err != nil {
		return nil, dispatchRequestError(err)
	}
	if err := bindOperationPrincipal(principal, ActionRunCommand, request.Identity, request.Ref); err != nil {
		return nil, forbidden(err)
	}
	state, err := service.authorizeOperation(ctx, request.Identity, request.Ref, corecontract.ManagedSandboxActionRunCommand)
	if err != nil {
		return nil, err
	}
	backendRequest := executionbackend.StartProcessRequest{
		Target: request.Ref.Target(request.Identity.Session.EnvironmentID), Operation: request.Identity.BackendContext(),
		RequestID: request.RequestID, ProcessID: request.ProcessID,
		Executable: request.Executable, Arguments: append([]string(nil), request.Arguments...),
		WorkingDirectory: request.WorkingDirectory, WorkspaceRoot: service.root,
		Platform: service.platform, Environment: cloneEnvironment(request.Environment),
		Timeout:          time.Duration(request.TimeoutMillis) * time.Millisecond,
		OutputLimitBytes: request.OutputLimitBytes,
	}
	exchange, err := service.provider.StartProcess(ctx, StartProcessProviderRequest{SessionRef: state.ProviderSessionRef, Request: backendRequest})
	if err != nil {
		return nil, err
	}
	return validateExchangeIdentity(exchange, backendRequest.Target, backendRequest.Operation)
}

func (service *Service) SignalCommand(ctx context.Context, principal Principal, request sandboxcontract.SignalCommandRequest) (executionbackend.Exchange, error) {
	if err := request.Validate(service.limits); err != nil {
		return nil, dispatchRequestError(err)
	}
	if err := bindOperationPrincipal(principal, ActionSignalCommand, request.Identity, request.Ref); err != nil {
		return nil, forbidden(err)
	}
	state, err := service.authorizeOperation(ctx, request.Identity, request.Ref, corecontract.ManagedSandboxActionSignalCommand)
	if err != nil {
		return nil, err
	}
	backendRequest := executionbackend.SignalProcessRequest{
		Target: request.Ref.Target(request.Identity.Session.EnvironmentID), Operation: request.Identity.BackendContext(),
		RequestID: request.RequestID, ProcessID: request.ProcessID, ProviderHandle: request.ProviderHandle,
		Signal: request.Signal, Reason: request.Reason,
	}
	exchange, err := service.provider.SignalProcess(ctx, SignalProcessProviderRequest{SessionRef: state.ProviderSessionRef, Request: backendRequest})
	if err != nil {
		return nil, err
	}
	return validateExchangeIdentity(exchange, backendRequest.Target, backendRequest.Operation)
}

func (service *Service) ReadFile(ctx context.Context, principal Principal, request sandboxcontract.ReadFileRequest) (executionbackend.Exchange, error) {
	if err := request.Validate(service.limits); err != nil {
		return nil, dispatchRequestError(err)
	}
	if err := bindOperationPrincipal(principal, ActionReadFile, request.Identity, request.Ref); err != nil {
		return nil, forbidden(err)
	}
	state, err := service.authorizeOperation(ctx, request.Identity, request.Ref, corecontract.ManagedSandboxActionReadFile)
	if err != nil {
		return nil, err
	}
	backendRequest := executionbackend.ReadFileRequest{
		Target: request.Ref.Target(request.Identity.Session.EnvironmentID), Operation: request.Identity.BackendContext(),
		RequestID: request.RequestID, Path: request.Path, Offset: request.Offset, Limit: request.Limit,
	}
	exchange, err := service.provider.ReadFile(ctx, ReadFileProviderRequest{SessionRef: state.ProviderSessionRef, Request: backendRequest})
	if err != nil {
		return nil, err
	}
	return validateExchangeIdentity(exchange, backendRequest.Target, backendRequest.Operation)
}

func (service *Service) authorizeOperation(ctx context.Context, identity sandboxcontract.OperationIdentity, ref sandboxcontract.SandboxRef, action string) (corecontract.ManagedSandboxState, error) {
	if !service.workspaceAllowed(identity.Session.WorkspaceID) {
		return corecontract.ManagedSandboxState{}, executionbackend.NewDispatchError(
			executionbackend.OutcomeNotSent, "workspace_not_allowed", errors.New("workspace is not enabled for managed execution"),
		)
	}
	authorized, err := service.core.AuthorizeManagedSandboxOperation(ctx, corecontract.AuthorizeManagedSandboxOperationRequest{
		WorkspaceID: identity.Session.WorkspaceID, SessionID: identity.Session.SessionID,
		RunID: identity.RunID, RunAttemptID: identity.RunAttemptID,
		RunAttemptGeneration: identity.RunAttemptGeneration,
		ExecutionID:          identity.ExecutionID, OperationID: identity.OperationID,
		MutationKey: identity.MutationKey, SandboxID: ref.SandboxID,
		TargetGeneration: ref.TargetGeneration, EnvironmentID: identity.Session.EnvironmentID,
		Action: action,
	})
	if err != nil {
		return corecontract.ManagedSandboxState{}, dispatchUnknown("core_authorization_failed", err)
	}
	if authorized.SandboxID != ref.SandboxID || authorized.TargetGeneration != ref.TargetGeneration ||
		authorized.OperationID != identity.OperationID || authorized.AuthorizedAt.IsZero() {
		return corecontract.ManagedSandboxState{}, dispatchUnknown("invalid_core_authorization", errors.New("core authorization response identity mismatch"))
	}
	current, err := service.core.GetManagedSandbox(ctx, ref.SandboxID, ref.TargetGeneration)
	if err != nil {
		return corecontract.ManagedSandboxState{}, dispatchUnknown("core_target_read_failed", err)
	}
	if err := matchReadySessionState(identity.Session, current.Sandbox, service.now()); err != nil {
		return corecontract.ManagedSandboxState{}, dispatchUnknown("target_fenced", err)
	}
	return current.Sandbox, nil
}

func (service *Service) workspaceAllowed(workspaceID string) bool {
	if service == nil || workspaceID == "" {
		return false
	}
	if service.workspaceAllowlist == nil {
		return true
	}
	_, allowed := service.workspaceAllowlist[workspaceID]
	return allowed
}

func (service *Service) observeProblem(ctx context.Context, state corecontract.ManagedSandboxState, observedState, code string) error {
	_, err := service.observeProviderProblem(ctx, state, observedState, code, "")
	return err
}

func (service *Service) observeProviderProblem(ctx context.Context, state corecontract.ManagedSandboxState, observedState, code, providerSessionRef string) (corecontract.ManagedSandboxMutationResponse, error) {
	digest := sha256.Sum256([]byte(code))
	return service.core.ObserveManagedSandbox(ctx, corecontract.ObserveManagedSandboxRequest{
		SandboxID: state.SandboxID, Generation: state.Generation, ExpectedVersion: state.Version,
		ObservedState: observedState, ProviderSessionRef: providerSessionRef,
		ErrorCode: code, ErrorSHA256: hex.EncodeToString(digest[:]),
	})
}

func (service *Service) failProviderReadyTimeout(ctx context.Context, state corecontract.ManagedSandboxState) (corecontract.ManagedSandboxState, bool, error) {
	// A caller deadline alone is not provider failure evidence: cancellation
	// exits through ensure_cancelled. This function is reached only after the
	// service-owned readiness deadline and requires an exact, durably observed
	// provider reference before marking the generation for bounded cleanup.
	if state.ProviderSessionRef == "" {
		return state, false, nil
	}
	observeContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), min(service.ensureTimeout, 5*time.Second))
	defer cancel()
	observed, err := service.observeProviderProblem(observeContext, state, "failed", "provider_ready_timeout", state.ProviderSessionRef)
	if err == nil {
		return observed.Sandbox, true, nil
	}
	current, getErr := service.core.GetManagedSandbox(observeContext, state.SandboxID, state.Generation)
	if getErr == nil && current.Sandbox.ObservedState == "failed" &&
		current.Sandbox.ProviderSessionRef == state.ProviderSessionRef && current.Sandbox.LastErrorCode == "provider_ready_timeout" {
		return current.Sandbox, true, nil
	}
	if getErr == nil && current.Sandbox.ObservedState == "ready" && current.Sandbox.ProviderSessionRef == state.ProviderSessionRef {
		return current.Sandbox, false, nil
	}
	return state, false, err
}

func (service *Service) readinessExpired(state corecontract.ManagedSandboxState) bool {
	startedAt := state.CreatedAt
	if startedAt.IsZero() {
		startedAt = state.UpdatedAt
	}
	return !startedAt.IsZero() && !startedAt.Add(service.ensureTimeout).After(service.now())
}

func (service *Service) logEnsure(
	level, stage, requestID string,
	principal Principal,
	state corecontract.ManagedSandboxState,
	provider ProviderSandbox,
	polls int,
	startedAt time.Time,
	extra ...any,
) {
	if service == nil || service.logger == nil {
		return
	}
	attributes := []any{
		"stage", stage,
		"request_id", requestID,
		"workspace_id", principal.WorkspaceID,
		"session_id", principal.SessionID,
		"environment_id", principal.EnvironmentID,
		"run_id", principal.RunID,
		"run_attempt_id", principal.RunAttemptID,
		"run_attempt_generation", principal.RunAttemptGeneration,
		"sandbox_id", state.SandboxID,
		"sandbox_generation", state.Generation,
		"core_state", safeCoreState(state.ObservedState),
		"provider_session_ref", firstNonEmpty(provider.SessionRef, state.ProviderSessionRef),
		"provider_state", safeProviderState(provider.State),
		"provider_status_class", safeProviderStatusClass(provider.ProviderStatusClass),
		"provider_execution_ready", provider.ExecutionReady,
		"provider_request_id", provider.RequestID,
		"poll_count", polls,
		"elapsed_ms", time.Since(startedAt).Milliseconds(),
	}
	attributes = append(attributes, extra...)
	if level == "error" {
		service.logger.Error("managed sandbox ensure", attributes...)
		return
	}
	service.logger.Info("managed sandbox ensure", attributes...)
}

func safeCoreState(state string) string {
	switch state {
	case "reserved", "creating", "ready", "deleting", "deleted", "failed", "unknown":
		return state
	default:
		return "other"
	}
}

func safeProviderState(state ProviderSandboxState) string {
	switch state {
	case ProviderSandboxReady, ProviderSandboxCreating, ProviderSandboxDeleting,
		ProviderSandboxDeleted, ProviderSandboxFailed, ProviderSandboxUnknown:
		return string(state)
	default:
		return "other"
	}
}

func safeProviderStatusClass(status string) string {
	switch status {
	case "creating", "ready", "deleting", "deleted", "failed", "other":
		return status
	default:
		return "other"
	}
}

func safeServiceErrorCode(err error, fallback string) string {
	var serviceError *Error
	if errors.As(err, &serviceError) && serviceError != nil {
		return safeProviderCode(serviceError.Code, fallback)
	}
	return fallback
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func (service *Service) contractSandbox(state corecontract.ManagedSandboxState, providerSandbox ProviderSandbox) sandboxcontract.Sandbox {
	result := sandboxcontract.Sandbox{
		Profile: sandboxcontract.ProfileV1,
		Ref:     sandboxcontract.SandboxRef{SandboxID: state.SandboxID, TargetGeneration: state.Generation},
		State:   sandboxcontract.SandboxState(state.ObservedState),
	}
	if state.ObservedState == "ready" {
		result.Root = providerSandbox.Root
		result.ExpiresAt = providerSandbox.ExpiresAt
	}
	if result.Root == "" && state.ObservedState == "ready" {
		result.Root = service.root
	}
	if result.ExpiresAt.IsZero() && state.ExpiresAt != nil {
		result.ExpiresAt = *state.ExpiresAt
	}
	return result
}

func matchSessionState(identity sandboxcontract.SessionIdentity, state corecontract.ManagedSandboxState) error {
	if state.WorkspaceID != identity.WorkspaceID || state.SessionID != identity.SessionID || state.EnvironmentID != identity.EnvironmentID {
		return errors.New("managed sandbox is outside the requested session")
	}
	return nil
}

func matchReadySessionState(identity sandboxcontract.SessionIdentity, state corecontract.ManagedSandboxState, now time.Time) error {
	if err := matchSessionState(identity, state); err != nil {
		return err
	}
	if state.DesiredState != "ready" || state.ObservedState != "ready" || state.ProviderSessionRef == "" ||
		state.ExpiresAt == nil || !state.ExpiresAt.After(now.UTC()) {
		return errors.New("managed sandbox target is not live and ready")
	}
	return nil
}

func bindLifecycleRef(principal Principal, ref sandboxcontract.SandboxRef) error {
	if principal.SandboxID != "" && principal.SandboxID != ref.SandboxID {
		return errors.New("lifecycle capability sandbox ID mismatch")
	}
	if principal.TargetGeneration > 0 && principal.TargetGeneration != ref.TargetGeneration {
		return errors.New("lifecycle capability generation mismatch")
	}
	return nil
}

func validateExchangeIdentity(exchange executionbackend.Exchange, target executionbackend.Target, operation executionbackend.OperationContext) (executionbackend.Exchange, error) {
	if exchange == nil {
		return nil, dispatchUnknown("nil_provider_exchange", errors.New("provider returned a nil exchange"))
	}
	if exchange.Target() != target || exchange.Operation() != operation {
		return nil, dispatchUnknown("provider_exchange_identity_mismatch", errors.New("provider exchange changed operation target identity"))
	}
	return exchange, nil
}

func cloneEnvironment(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func waitContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func randomUUID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16]), nil
}

func safeProviderCode(code, fallback string) string {
	if code == "" || len(code) > 64 {
		return fallback
	}
	for index, character := range code {
		if (index == 0 && (character < 'a' || character > 'z')) ||
			(index > 0 && !((character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '_')) {
			return fallback
		}
	}
	return code
}

func safeProviderErrorCode(err error, fallback string) string {
	var providerError *ProviderError
	if errors.As(err, &providerError) {
		return safeProviderCode(providerError.Code, fallback)
	}
	var dispatchError *executionbackend.DispatchError
	if errors.As(err, &dispatchError) {
		return safeProviderCode(dispatchError.Code, fallback)
	}
	return fallback
}

// providerObservationFailureCode returns a safe, provider-state-specific
// error code only for failures that prove the provider returned an invalid
// lifecycle observation. Core transport/version failures must not be turned
// into a provider failure because doing so could trigger an unsafe delete.
func providerObservationFailureCode(err error) string {
	var serviceError *Error
	if !errors.As(err, &serviceError) || serviceError == nil {
		return ""
	}
	switch serviceError.Code {
	case "invalid_provider_state", "provider_identity_mismatch":
		return safeProviderCode(serviceError.Code, "provider_observation_invalid")
	default:
		return ""
	}
}

func invalidRequest(err error) error {
	return &Error{HTTPStatus: http.StatusBadRequest, Code: "invalid_argument", Message: "sandbox request is invalid", Outcome: executionbackend.OutcomeNotSent, Cause: err}
}

func forbidden(err error) error {
	return &Error{HTTPStatus: http.StatusForbidden, Code: "forbidden", Message: "sandbox capability is not authorized", Outcome: executionbackend.OutcomeNotSent, Cause: err}
}

func conflict(code string, err error) error {
	return &Error{HTTPStatus: http.StatusConflict, Code: code, Message: "sandbox state conflicts with the request", Outcome: executionbackend.OutcomeNotSent, Cause: err}
}

func unavailable(code string, err error) error {
	return &Error{HTTPStatus: http.StatusServiceUnavailable, Code: code, Message: "sandbox provider is temporarily unavailable", Outcome: executionbackend.OutcomeUnknown, Cause: err}
}

func coreServiceError(code string, err error) error {
	status := http.StatusServiceUnavailable
	var coreError *CoreError
	if errors.As(err, &coreError) {
		switch coreError.HTTPStatus {
		case http.StatusBadRequest:
			status = http.StatusBadRequest
		case http.StatusForbidden:
			status = http.StatusForbidden
		case http.StatusNotFound:
			status = http.StatusNotFound
		case http.StatusConflict:
			status = http.StatusConflict
		}
	}
	return &Error{HTTPStatus: status, Code: code, Message: "core rejected sandbox authority", Outcome: executionbackend.OutcomeNotSent, Cause: err}
}

func dispatchRequestError(err error) error {
	return executionbackend.NewDispatchError(executionbackend.OutcomeNotSent, "invalid_request", err)
}

func dispatchUnknown(code string, err error) error {
	return executionbackend.NewDispatchError(executionbackend.OutcomeUnknown, code, err)
}
