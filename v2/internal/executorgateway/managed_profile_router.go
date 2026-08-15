package executorgateway

import (
	"context"
	"errors"

	"github.com/agentserver/agentserver/v2/internal/executionbackend"
	"github.com/agentserver/agentserver/v2/internal/managedsandboxprofile"
)

// TAEBackendRouter keeps execution on the sandbox-gateway selected by the
// Core-frozen environment binding. It deliberately routes by EnvironmentID,
// which is part of every validated execution target, rather than by a tool
// argument, request header, or provider-controlled sandbox ID.
type TAEBackendRouter struct {
	byEnvironment map[string]executionbackend.Backend
}

func NewTAEBackendRouter(backends map[string]executionbackend.Backend) (*TAEBackendRouter, error) {
	if len(backends) < 1 || len(backends) > len(managedsandboxprofile.Regions()) {
		return nil, errors.New("TAE backend router requires between one and four environments")
	}
	copy := make(map[string]executionbackend.Backend, len(backends))
	for environmentID, backend := range backends {
		if backend == nil || backend.Kind() != executionbackend.KindTAE {
			return nil, errors.New("TAE backend router requires only TAE backends")
		}
		if err := validateRegistryIdentity("TAE backend environment ID", environmentID); err != nil {
			return nil, err
		}
		copy[environmentID] = backend
	}
	return &TAEBackendRouter{byEnvironment: copy}, nil
}

func (*TAEBackendRouter) Kind() executionbackend.Kind { return executionbackend.KindTAE }

func (router *TAEBackendRouter) StartProcess(ctx context.Context, request executionbackend.StartProcessRequest) (executionbackend.Exchange, error) {
	backend, err := router.backend(request.Target)
	if err != nil {
		return nil, err
	}
	return backend.StartProcess(ctx, request)
}

func (router *TAEBackendRouter) SignalProcess(ctx context.Context, request executionbackend.SignalProcessRequest) (executionbackend.Exchange, error) {
	backend, err := router.backend(request.Target)
	if err != nil {
		return nil, err
	}
	return backend.SignalProcess(ctx, request)
}

func (router *TAEBackendRouter) ReadFile(ctx context.Context, request executionbackend.ReadFileRequest) (executionbackend.Exchange, error) {
	backend, err := router.backend(request.Target)
	if err != nil {
		return nil, err
	}
	return backend.ReadFile(ctx, request)
}

func (router *TAEBackendRouter) backend(target executionbackend.Target) (executionbackend.Backend, error) {
	if router == nil {
		return nil, executionbackend.NewDispatchError(executionbackend.OutcomeNotSent, "profile_router_unavailable", errors.New("TAE backend profile router is nil"))
	}
	if err := target.Validate(); err != nil {
		return nil, executionbackend.NewDispatchError(executionbackend.OutcomeNotSent, "invalid_target", err)
	}
	if target.Kind != executionbackend.KindTAE {
		return nil, executionbackend.NewDispatchError(executionbackend.OutcomeNotSent, "wrong_backend_kind", errors.New("TAE backend profile router requires a TAE target"))
	}
	backend := router.byEnvironment[target.EnvironmentID]
	if backend == nil {
		return nil, executionbackend.NewDispatchError(executionbackend.OutcomeNotSent, "profile_not_configured", errors.New("TAE target environment has no configured backend profile"))
	}
	return backend, nil
}

// ManagedTargetFencerRouter mirrors backend routing for lifecycle deletes.
// Both the signed principal profile and the resolved target environment must
// identify the same immutable binding before any request can leave the
// executor-gateway.
type ManagedTargetFencerRouter struct {
	byProfile map[string]ManagedTargetFencer
}

func NewManagedTargetFencerRouter(fencers map[string]ManagedTargetFencer) (*ManagedTargetFencerRouter, error) {
	if len(fencers) < 1 || len(fencers) > len(managedsandboxprofile.Regions()) {
		return nil, errors.New("managed target fencer router requires between one and four profiles")
	}
	copy := make(map[string]ManagedTargetFencer, len(fencers))
	for profileID, fencer := range fencers {
		if !managedsandboxprofile.ValidProfileID(profileID) || fencer == nil {
			return nil, errors.New("managed target fencer router contains an invalid profile")
		}
		copy[profileID] = fencer
	}
	return &ManagedTargetFencerRouter{byProfile: copy}, nil
}

func (router *ManagedTargetFencerRouter) FenceManagedTarget(
	ctx context.Context,
	principal ExecutorMCPPrincipal,
	target executionbackend.Target,
	reason string,
) error {
	if router == nil || principal.ManagedSandbox == nil {
		return errors.New("managed target fencer requires frozen sandbox authority")
	}
	authority := principal.ManagedSandbox
	if target.EnvironmentID != authority.EnvironmentID {
		return errors.New("managed target environment does not match frozen sandbox authority")
	}
	fencer := router.byProfile[authority.ProfileID]
	if fencer == nil {
		return errors.New("managed target fencer profile is not configured")
	}
	return fencer.FenceManagedTarget(ctx, principal, target, reason)
}

var _ executionbackend.Backend = (*TAEBackendRouter)(nil)
var _ ManagedTargetFencer = (*ManagedTargetFencerRouter)(nil)
