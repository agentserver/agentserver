package coredb

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

func resolveDispatchTarget(execution Execution, requested DispatchTarget, connectionGeneration int64) (DispatchTarget, error) {
	target := requested
	if target.Kind == "" {
		target = DispatchTarget{
			Kind: DispatchTargetAgentX, ID: execution.ExecutorID,
			Generation: connectionGeneration,
		}
	}
	if err := validateDispatchTarget(target, true); err != nil {
		return DispatchTarget{}, err
	}
	if target.Kind == DispatchTargetAgentX {
		if execution.ExecutorID == "" || target.ID != execution.ExecutorID {
			return DispatchTarget{}, errors.New("agentx dispatch target differs from executor_id")
		}
		if connectionGeneration != target.Generation {
			return DispatchTarget{}, errors.New("agentx connection_generation differs from target_generation")
		}
	} else if connectionGeneration != 0 {
		return DispatchTarget{}, errors.New("TAE dispatch target must not carry connection_generation")
	}
	if execution.Target.Kind != "" && (execution.Target.Kind != target.Kind || execution.Target.ID != target.ID) {
		return DispatchTarget{}, errors.New("dispatch target differs from the frozen execution target")
	}
	if execution.Target.Generation > 0 && execution.Target.Generation != target.Generation {
		return DispatchTarget{}, errors.New("dispatch target generation differs from the frozen execution generation")
	}
	return target, nil
}

func connectionGenerationForTarget(target DispatchTarget) any {
	if target.Kind == DispatchTargetAgentX {
		return target.Generation
	}
	return nil
}

func targetFencedError(operation, operationID string, current DispatchTarget) error {
	return &StateError{
		Code: ErrorConnectionFenced, Operation: operation,
		Resource: "operation", ResourceID: operationID,
		CurrentGeneration: current.Generation,
		Message: fmt.Sprintf(
			"dispatch target does not match frozen %s target generation",
			current.Kind,
		),
	}
}

func (s *StateStore) requireLiveDispatchTarget(
	ctx context.Context,
	transaction pgx.Tx,
	operation string,
	run Run,
	attempt RunAttempt,
	execution Execution,
	target DispatchTarget,
) error {
	switch target.Kind {
	case DispatchTargetAgentX:
		return s.requireLiveExecutorConnection(ctx, transaction, operation, execution, target.Generation)
	case DispatchTargetTAE:
		return s.requireLiveManagedSandbox(ctx, transaction, operation, run, attempt, execution, target)
	default:
		return commandError(ErrorInvalidArgument, operation, "execution", execution.ID, "unsupported dispatch target kind")
	}
}

func (s *StateStore) requireLiveManagedSandbox(
	ctx context.Context,
	transaction pgx.Tx,
	operation string,
	run Run,
	attempt RunAttempt,
	execution Execution,
	target DispatchTarget,
) error {
	query := fmt.Sprintf(`
SELECT sandbox.workspace_id::text,
       sandbox.session_id::text,
       sandbox.environment_id::text,
       sandbox.generation,
       sandbox.desired_state,
       sandbox.observed_state,
       sandbox.expires_at > pg_catalog.clock_timestamp(),
       EXISTS (
           SELECT 1
           FROM %s AS activity
           WHERE activity.sandbox_id = sandbox.id
             AND activity.target_generation = sandbox.generation
             AND activity.run_attempt_id = $2
             AND activity.run_attempt_generation = $3
             AND activity.released_at IS NULL
             AND activity.lease_expires_at > pg_catalog.clock_timestamp()
       )
FROM %s AS sandbox
WHERE sandbox.id = $1
FOR SHARE`, s.table("managed_sandbox_activities"), s.table("managed_sandboxes"))
	var workspaceID string
	var sessionID string
	var environmentID string
	var generation int64
	var desiredState string
	var observedState string
	var unexpired *bool
	var activityLive bool
	err := transaction.QueryRow(ctx, query, target.ID, attempt.ID, attempt.Generation).Scan(
		&workspaceID, &sessionID, &environmentID, &generation,
		&desiredState, &observedState, &unexpired, &activityLive,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return &StateError{
			Code: ErrorConnectionFenced, Operation: operation,
			Resource: "operation", ResourceID: execution.ID,
			Message: "managed sandbox does not exist at the dispatch target",
		}
	}
	if err != nil {
		return databaseError(operation+" read managed sandbox target", err)
	}
	if workspaceID != run.WorkspaceID || sessionID != run.SessionID || environmentID != execution.EnvID {
		return &StateError{
			Code: ErrorConnectionFenced, Operation: operation,
			Resource: "operation", ResourceID: execution.ID,
			CurrentGeneration: generation,
			Message:           "managed sandbox is outside the execution session authority",
		}
	}
	if generation != target.Generation || desiredState != "ready" || observedState != "ready" || unexpired == nil || !*unexpired || !activityLive {
		return &StateError{
			Code: ErrorConnectionFenced, Operation: operation,
			Resource: "operation", ResourceID: execution.ID,
			CurrentGeneration: generation,
			Message:           "managed sandbox is not a live ready dispatch generation",
		}
	}
	return nil
}
