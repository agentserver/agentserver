package coredb

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// workspaceBindingEnvironment is the database projection needed to turn a
// session's logical environment ID into run authority. The executor row is
// locked before the environment row so this helper follows the same lock
// order as connection acquire/renew/recovery and executor revocation.
type workspaceBindingEnvironment struct {
	ID             string
	ExecutorID     string
	ExecutorStatus string
	BackendKind    string
	Version        int64
	RootDescriptor []byte
	Status         string
}

// readWorkspaceBindingEnvironment reads one workspace-scoped environment
// using the canonical executor -> environment lock order. The first lookup
// only discovers the executor ID; the locked reads below re-check both
// workspace ownership and the environment relationship before returning.
func (s *StateStore) readWorkspaceBindingEnvironment(
	ctx context.Context,
	transaction pgx.Tx,
	operation, workspaceID, environmentID string,
) (workspaceBindingEnvironment, error) {
	lookup := fmt.Sprintf(`
SELECT executor_id::text
FROM %s
WHERE id = $1`, s.table("executor_environments"))
	var executorID string
	if err := transaction.QueryRow(ctx, lookup, environmentID).Scan(&executorID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return workspaceBindingEnvironment{}, commandError(ErrorNotFound, operation, "environment", environmentID, "environment is not registered in this workspace")
		}
		return workspaceBindingEnvironment{}, databaseError(operation+" find environment executor", err)
	}

	// All executor connection mutations lock this row first. A shared lock
	// prevents an executor revoke or generation mutation from racing the
	// workspace binding decision while allowing unrelated readers to proceed.
	executorQuery := fmt.Sprintf(`
SELECT status
FROM %s
WHERE id = $1 AND workspace_id = $2
FOR SHARE`, s.table("executors"))
	var executorStatus string
	if err := transaction.QueryRow(ctx, executorQuery, executorID, workspaceID).Scan(&executorStatus); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return workspaceBindingEnvironment{}, commandError(ErrorNotFound, operation, "environment", environmentID, "environment is not registered in this workspace")
		}
		return workspaceBindingEnvironment{}, databaseError(operation+" lock environment executor", err)
	}

	environmentQuery := fmt.Sprintf(`
	SELECT id::text, executor_id::text, backend_kind, version, root_descriptor::text, status
FROM %s
WHERE id = $1 AND executor_id = $2
FOR SHARE`, s.table("executor_environments"))
	var environment workspaceBindingEnvironment
	if err := transaction.QueryRow(ctx, environmentQuery, environmentID, executorID).Scan(
		&environment.ID, &environment.ExecutorID, &environment.BackendKind,
		&environment.Version, &environment.RootDescriptor, &environment.Status,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return workspaceBindingEnvironment{}, commandError(ErrorNotFound, operation, "environment", environmentID, "environment is not registered in this workspace")
		}
		return workspaceBindingEnvironment{}, databaseError(operation+" lock environment", err)
	}
	switch environment.BackendKind {
	case DispatchTargetAgentX:
		// AgentX projects the run-frozen filesystem authority into the pinned
		// Codex exec-server sandbox. It is the only backend currently qualified
		// to carry a user-selected working tree.
	case DispatchTargetTAE:
		// The documented TAE Terminal process API has no per-process
		// filesystem-access field. Keep the fixed managed-CLI environment
		// available through its existing unbound profile, but do not let a
		// session turn that profile into generic workspace authority.
		return workspaceBindingEnvironment{}, commandError(
			ErrorInvalidState, operation, "environment", environmentID,
			"TAE environments do not currently enforce session workspace filesystem authority",
		)
	default:
		return workspaceBindingEnvironment{}, databaseError(operation+" validate environment backend", fmt.Errorf("unsupported backend kind %q", environment.BackendKind))
	}
	environment.ExecutorStatus = executorStatus
	return environment, nil
}
