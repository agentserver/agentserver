package sandboxgateway

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

type ReconcileReport struct {
	Examined  int
	Converged int
	Unchanged int
	Failed    int
}

// ReconcileOnce converges only this service's configured region/PSM shard.
// It does not create a provider resource: creates remain owned by the
// one-shot reserved -> creating transition in EnsureSandbox.
func (service *Service) ReconcileOnce(ctx context.Context, limit int) (ReconcileReport, error) {
	if limit < 1 || limit > 1000 {
		return ReconcileReport{}, errors.New("reconcile limit must be between 1 and 1000")
	}
	candidates, err := service.core.ListManagedSandboxesForReconcile(ctx, corecontract.ListManagedSandboxesForReconcileRequest{Limit: limit})
	if err != nil {
		return ReconcileReport{}, coreServiceError("list_reconcile_failed", err)
	}
	report := ReconcileReport{}
	var reconcileErrors []error
	for _, state := range candidates.Sandboxes {
		if state.ProviderKind != "tae" || state.ProviderRegion != service.providerRegion || state.ProviderPSM != service.providerPSM {
			continue
		}
		if !service.workspaceAllowed(state.WorkspaceID) {
			continue
		}
		report.Examined++
		changed, reconcileErr := service.reconcileSandbox(ctx, state)
		if reconcileErr != nil {
			report.Failed++
			reconcileErrors = append(reconcileErrors, fmt.Errorf("sandbox %s generation %d: %w", state.SandboxID, state.Generation, reconcileErr))
			continue
		}
		if changed {
			report.Converged++
		} else {
			report.Unchanged++
		}
	}
	return report, errors.Join(reconcileErrors...)
}

func (service *Service) reconcileSandbox(ctx context.Context, state corecontract.ManagedSandboxState) (bool, error) {
	if state.DesiredState == "deleted" || state.ObservedState == "deleting" ||
		state.ObservedState == "failed" || service.sandboxExpiredOrIdle(state) {
		return service.reconcileDelete(ctx, state)
	}
	switch state.ObservedState {
	case "reserved":
		return false, nil
	case "creating", "unknown", "ready":
		providerSandbox, err := service.findOrGetProviderSandbox(ctx, state)
		if errors.Is(err, ErrProviderSandboxNotFound) {
			switch {
			case state.ObservedState == "ready":
				observed, observeErr := service.observeProviderProblem(ctx, state, "failed", "provider_session_missing", state.ProviderSessionRef)
				if observeErr != nil {
					return false, observeErr
				}
				_, deleteErr := service.reconcileDelete(ctx, observed.Sandbox)
				return true, deleteErr
			case state.ObservedState == "creating" && service.observationStale(state):
				observeErr := service.observeProblem(ctx, state, "unknown", "provider_create_not_observed")
				return observeErr == nil, observeErr
			case state.ObservedState == "unknown" &&
				((state.ProviderSessionRef != "" && service.observationStale(state)) || service.createTTLElapsed(state)):
				observed, observeErr := service.observeProviderProblem(ctx, state, "failed", "provider_create_absent", state.ProviderSessionRef)
				if observeErr != nil {
					return false, observeErr
				}
				_, deleteErr := service.reconcileDelete(ctx, observed.Sandbox)
				return true, deleteErr
			}
			return false, nil
		}
		if err != nil {
			// FindSandbox is deliberately ambiguous when a lost create response
			// left more than one exact live match. That result is safe to hand to
			// the recovery delete path because the provider has already completed
			// an exact, bounded enumeration. Other ambiguous/unknown provider
			// errors (especially incomplete searches) remain fail-closed and are
			// retried without mutation.
			if providerDuplicateAmbiguity(err) {
				observed, observeErr := service.observeProviderProblem(ctx, state, "failed", "provider_create_ambiguous", state.ProviderSessionRef)
				if observeErr != nil {
					return false, observeErr
				}
				_, deleteErr := service.reconcileDelete(ctx, observed.Sandbox)
				return true, deleteErr
			}
			return false, err
		}
		_, converged, ready, err := service.convergeProviderSandbox(ctx, state, providerSandbox)
		if err != nil {
			if code := providerObservationFailureCode(err); code != "" {
				observed, observeErr := service.observeProviderProblem(ctx, state, "failed", code, state.ProviderSessionRef)
				if observeErr != nil {
					return false, observeErr
				}
				_, deleteErr := service.reconcileDelete(ctx, observed.Sandbox)
				return true, deleteErr
			}
			return false, err
		}
		changed := converged.Version != state.Version
		if !ready && converged.ObservedState == "failed" {
			deleted, deleteErr := service.reconcileDelete(ctx, converged)
			return changed || deleted, deleteErr
		}
		return changed, nil
	case "deleted":
		return false, nil
	default:
		return false, fmt.Errorf("unsupported observed state %q", state.ObservedState)
	}
}

func providerDuplicateAmbiguity(err error) bool {
	var providerError *ProviderError
	if !errors.As(err, &providerError) || providerError == nil || !providerError.Ambiguous {
		return false
	}
	switch providerError.Code {
	case "provider_create_ambiguous", "idempotency_ambiguous":
		return true
	default:
		return false
	}
}

func (service *Service) reconcileDelete(ctx context.Context, state corecontract.ManagedSandboxState) (bool, error) {
	changed := false
	if state.DesiredState != "deleted" || (state.ObservedState != "deleting" && state.ObservedState != "deleted") {
		begun, err := service.core.BeginManagedSandboxDelete(ctx, corecontract.BeginManagedSandboxDeleteRequest{
			SandboxID: state.SandboxID, Generation: state.Generation,
			ExpectedVersion: state.Version, Reason: service.reconcileDeleteReason(state),
		})
		if err != nil {
			current, getErr := service.core.GetManagedSandbox(ctx, state.SandboxID, state.Generation)
			if getErr != nil || current.Sandbox.DesiredState != "deleted" {
				return false, err
			}
			state = current.Sandbox
		} else {
			state = begun.Sandbox
			changed = begun.Changed
		}
	}
	if state.ObservedState == "deleted" {
		return changed, nil
	}
	err := service.deleteProviderSandbox(ctx, state)
	if err != nil && !errors.Is(err, ErrProviderSandboxNotFound) {
		return changed, err
	}
	observed, err := service.core.ObserveManagedSandbox(ctx, corecontract.ObserveManagedSandboxRequest{
		SandboxID: state.SandboxID, Generation: state.Generation,
		ExpectedVersion: state.Version, ObservedState: "deleted",
	})
	if err != nil {
		current, getErr := service.core.GetManagedSandbox(ctx, state.SandboxID, state.Generation)
		if getErr == nil && current.Sandbox.ObservedState == "deleted" {
			return true, nil
		}
		return changed, err
	}
	return changed || observed.Changed, nil
}

func (service *Service) sandboxExpiredOrIdle(state corecontract.ManagedSandboxState) bool {
	now := service.now()
	return state.ObservedState == "ready" &&
		((state.ExpiresAt != nil && !state.ExpiresAt.After(now)) ||
			(state.IdleExpiresAt != nil && !state.IdleExpiresAt.After(now)))
}

func (service *Service) observationStale(state corecontract.ManagedSandboxState) bool {
	return !state.UpdatedAt.IsZero() && service.now().Sub(state.UpdatedAt) >= service.ensureTimeout
}

func (service *Service) createTTLElapsed(state corecontract.ManagedSandboxState) bool {
	if state.CreatedAt.IsZero() || state.RequestedTTLSeconds < 1 {
		return false
	}
	return !state.CreatedAt.Add(time.Duration(state.RequestedTTLSeconds) * time.Second).After(service.now())
}

func (service *Service) reconcileDeleteReason(state corecontract.ManagedSandboxState) string {
	now := service.now()
	switch {
	case state.DesiredState == "deleted":
		return "desired state is deleted"
	case state.ExpiresAt != nil && !state.ExpiresAt.After(now):
		return "provider TTL expired"
	case state.IdleExpiresAt != nil && !state.IdleExpiresAt.After(now):
		return "managed sandbox idle TTL expired"
	case state.ObservedState == "failed":
		return "failed managed sandbox cleanup"
	default:
		return "managed sandbox reconciliation cleanup"
	}
}
