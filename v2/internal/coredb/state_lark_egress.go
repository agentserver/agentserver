package coredb

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const managedLarkCredentialMinimumLifetime = 5 * time.Second

func validateRunLarkEgressBinding(binding RunLarkEgressBinding) error {
	if err := validateUUID("lark_grant.grant_id", binding.GrantID); err != nil {
		return err
	}
	if err := validateUUID("lark_grant.grant_user_id", binding.GrantUserID); err != nil {
		return err
	}
	if binding.GrantVersion < 1 || binding.GrantVersion > maxSafeJSONInteger {
		return errors.New("lark_grant.grant_version must be a positive safe integer")
	}
	if isZeroDigest(binding.PolicySHA256) {
		return errors.New("lark_grant.policy_sha256 is required")
	}
	return nil
}

// ResolveUserRunLarkEgressBinding freezes an active per-user grant when one is
// present. Lark remains an optional pack: absence returns the zero binding and
// later lark-cli dispatch fails closed instead of making unrelated runs fail.
func (s *StateStore) ResolveUserRunLarkEgressBinding(
	ctx context.Context,
	command ResolveUserRunLarkEgressBindingCommand,
) (RunLarkEgressBinding, error) {
	const operation = "ResolveUserRunLarkEgressBinding"
	for field, value := range map[string]string{
		"workspace_id": command.WorkspaceID, "session_id": command.SessionID, "actor_id": command.ActorID,
	} {
		if err := validateUUID(field, value); err != nil {
			return RunLarkEgressBinding{}, commandError(ErrorInvalidArgument, operation, "workspace", command.WorkspaceID, err.Error())
		}
	}
	if err := validateBoundedText("idempotency_key", command.IdempotencyKey, 256); err != nil {
		return RunLarkEgressBinding{}, commandError(ErrorInvalidArgument, operation, "workspace", command.WorkspaceID, err.Error())
	}
	return withStateReadTransaction(ctx, s, operation, func(transaction pgx.Tx) (RunLarkEgressBinding, error) {
		membershipQuery := fmt.Sprintf(`
SELECT 1
FROM %s AS workspace
JOIN %s AS member
  ON member.workspace_id = workspace.id
 AND member.user_id = $2
 AND member.role IN ('owner', 'developer')
JOIN %s AS session
  ON session.id = $3
 AND session.workspace_id = workspace.id
 AND session.creator_id = member.user_id
 AND session.status = 'active'
WHERE workspace.id = $1 AND workspace.status = 'active'`,
			s.table("workspaces"), s.table("workspace_members"), s.table("sessions"),
		)
		var marker int
		if err := transaction.QueryRow(ctx, membershipQuery, command.WorkspaceID, command.ActorID, command.SessionID).Scan(&marker); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return RunLarkEgressBinding{}, commandError(ErrorForbidden, operation, "workspace", command.WorkspaceID, "active session membership does not permit Lark authority")
			}
			return RunLarkEgressBinding{}, databaseError(operation+" authorize membership", err)
		}

		existingQuery := fmt.Sprintf(`
SELECT launch.lark_grant_id::text, launch.lark_grant_version,
       launch.lark_grant_user_id::text, launch.lark_policy_sha256
FROM %s AS run
JOIN %s AS launch ON launch.run_id = run.id
WHERE run.workspace_id = $1 AND run.session_id = $2
  AND run.actor_id = $3 AND run.idempotency_key = $4`,
			s.table("runs"), s.table("run_launch_states"),
		)
		var grantID, grantUserID *string
		var grantVersion *int64
		var policyBytes []byte
		if err := transaction.QueryRow(
			ctx, existingQuery, command.WorkspaceID, command.SessionID, command.ActorID, command.IdempotencyKey,
		).Scan(&grantID, &grantVersion, &grantUserID, &policyBytes); err == nil {
			return decodeOptionalRunLarkEgressBinding(operation, grantID, grantVersion, grantUserID, policyBytes)
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return RunLarkEgressBinding{}, databaseError(operation+" read existing binding", err)
		}

		query := fmt.Sprintf(`
SELECT grant_state.id::text, grant_state.authority_version,
       grant_state.user_id::text, grant_state.policy_sha256
FROM %s AS grant_state
WHERE grant_state.workspace_id = $1
  AND grant_state.user_id = $2
  AND grant_state.pack_id = $3
  AND grant_state.status = $4
  AND grant_state.access_expires_at > pg_catalog.clock_timestamp() + INTERVAL '5 seconds'`,
			s.table("workspace_lark_grants"),
		)
		var binding RunLarkEgressBinding
		if err := transaction.QueryRow(
			ctx, query, command.WorkspaceID, command.ActorID, LarkReadOnlyPackID, LarkGrantStatusActive,
		).Scan(&binding.GrantID, &binding.GrantVersion, &binding.GrantUserID, &policyBytes); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return RunLarkEgressBinding{}, nil
			}
			return RunLarkEgressBinding{}, databaseError(operation+" resolve active grant", err)
		}
		if err := copyStoredSHA256(&binding.PolicySHA256, policyBytes); err != nil {
			return RunLarkEgressBinding{}, databaseError(operation+" decode policy digest", err)
		}
		if err := validateRunLarkEgressBinding(binding); err != nil {
			return RunLarkEgressBinding{}, databaseError(operation+" validate active grant", err)
		}
		return binding, nil
	})
}

func decodeOptionalRunLarkEgressBinding(
	operation string,
	grantID *string,
	grantVersion *int64,
	grantUserID *string,
	policyBytes []byte,
) (RunLarkEgressBinding, error) {
	if grantID == nil && grantVersion == nil && grantUserID == nil && policyBytes == nil {
		return RunLarkEgressBinding{}, nil
	}
	if grantID == nil || grantVersion == nil || grantUserID == nil || policyBytes == nil {
		return RunLarkEgressBinding{}, databaseError(operation+" decode existing Lark binding", errors.New("stored binding is incomplete"))
	}
	binding := RunLarkEgressBinding{GrantID: *grantID, GrantVersion: *grantVersion, GrantUserID: *grantUserID}
	if err := copyStoredSHA256(&binding.PolicySHA256, policyBytes); err != nil {
		return RunLarkEgressBinding{}, databaseError(operation+" decode existing Lark policy digest", err)
	}
	if err := validateRunLarkEgressBinding(binding); err != nil {
		return RunLarkEgressBinding{}, databaseError(operation+" validate existing Lark binding", err)
	}
	return binding, nil
}

func (s *StateStore) requireCreateRunLarkEgress(ctx context.Context, transaction pgx.Tx, command CreateRunCommand) error {
	query := fmt.Sprintf(`
SELECT 1
FROM %s AS grant_state
JOIN %s AS member
  ON member.workspace_id = grant_state.workspace_id
 AND member.user_id = grant_state.user_id
 AND member.role IN ('owner', 'developer')
WHERE grant_state.id = $1
  AND grant_state.workspace_id = $2
  AND grant_state.user_id = $3
  AND grant_state.authority_version = $4
  AND grant_state.policy_sha256 = $5
  AND grant_state.pack_id = $6
  AND grant_state.status = $7
  AND grant_state.access_expires_at > pg_catalog.clock_timestamp() + INTERVAL '5 seconds'
FOR SHARE OF grant_state, member`, s.table("workspace_lark_grants"), s.table("workspace_members"))
	var marker int
	if err := transaction.QueryRow(
		ctx, query, command.LarkEgress.GrantID, command.WorkspaceID,
		command.ActorID, command.LarkEgress.GrantVersion, command.LarkEgress.PolicySHA256[:],
		LarkReadOnlyPackID, LarkGrantStatusActive,
	).Scan(&marker); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return commandError(ErrorForbidden, "CreateRun", "lark_grant", command.LarkEgress.GrantID, "frozen Lark grant is no longer active")
		}
		return databaseError("CreateRun authorize Lark grant", err)
	}
	return nil
}

// ResolveManagedLarkEgressAuthority is used by executor-gateway immediately
// after Core grants the one-shot process_start dispatch and before a
// placeholder is signed. It intentionally omits credential material.
func (s *StateStore) ResolveManagedLarkEgressAuthority(ctx context.Context, query ManagedLarkAuthorityQuery) (ManagedLarkAuthority, error) {
	return s.readManagedLarkAuthority(ctx, query, false)
}

// AuthorizeManagedLarkEgress is the Policy Webhook live check. It accepts an
// acknowledged process but never a terminal operation and returns only the
// still-sealed credential envelope.
func (s *StateStore) AuthorizeManagedLarkEgress(ctx context.Context, query ManagedLarkAuthorityQuery) (ManagedLarkAuthority, error) {
	return s.readManagedLarkAuthority(ctx, query, true)
}

func (s *StateStore) readManagedLarkAuthority(ctx context.Context, query ManagedLarkAuthorityQuery, live bool) (ManagedLarkAuthority, error) {
	operation := "ResolveManagedLarkEgressAuthority"
	if live {
		operation = "AuthorizeManagedLarkEgress"
	}
	if err := validateManagedLarkAuthorityQuery(query, live); err != nil {
		return ManagedLarkAuthority{}, commandError(ErrorInvalidArgument, operation, "operation", query.OperationID, err.Error())
	}
	return withStateReadTransaction(ctx, s, operation, func(transaction pgx.Tx) (ManagedLarkAuthority, error) {
		operationStatuses := "('dispatching')"
		frozenPredicate := ""
		sandboxPredicate := ""
		if live {
			operationStatuses = "('dispatching', 'acknowledged')"
			frozenPredicate = `
 AND launch.lark_grant_id = $12
 AND launch.lark_grant_version = $13
 AND launch.lark_policy_sha256 = $14`
			sandboxPredicate = `
 AND sandbox.provider_psm = $15`
		}
		statement := fmt.Sprintf(`
WITH authority_time AS MATERIALIZED (
    SELECT pg_catalog.clock_timestamp() AS now
)
SELECT grant_state.id::text,
       grant_state.authority_version,
       grant_state.user_id::text,
       grant_state.policy_sha256,
       grant_state.credential_version,
       grant_state.sealed_token_set,
       grant_state.access_expires_at,
       authority_time.now
FROM authority_time
JOIN %s AS workspace
  ON workspace.id = $1
 AND workspace.status = 'active'
JOIN %s AS member
  ON member.workspace_id = workspace.id
 AND member.user_id = $3
 AND member.role IN ('owner', 'developer')
JOIN %s AS session
  ON session.id = $2
 AND session.workspace_id = workspace.id
 AND session.creator_id = member.user_id
 AND session.status = 'active'
JOIN %s AS run
  ON run.id = $5
 AND run.workspace_id = workspace.id
 AND run.session_id = session.id
 AND run.actor_id = member.user_id
 AND run.status = 'running'
 AND run.current_attempt_generation = $7
 AND session.active_run_id = run.id
JOIN %s AS attempt
  ON attempt.id = $6
 AND attempt.run_id = run.id
 AND attempt.generation = $7
 AND attempt.status = 'running'
 AND attempt.turn_started_at IS NOT NULL
JOIN %s AS session_lease
  ON session_lease.session_id = session.id
 AND session_lease.run_id = run.id
 AND session_lease.holder_id = attempt.holder_id
 AND session_lease.generation = attempt.generation
 AND session_lease.expires_at > authority_time.now
JOIN %s AS attempt_lease
  ON attempt_lease.run_attempt_id = attempt.id
 AND attempt_lease.holder_id = attempt.holder_id
 AND attempt_lease.generation = attempt.generation
 AND attempt_lease.expires_at > authority_time.now
JOIN %s AS launch
  ON launch.run_id = run.id
 AND launch.workspace_id = run.workspace_id
 AND launch.session_id = run.session_id
 AND launch.lark_grant_user_id = run.actor_id%s
JOIN %s AS execution
  ON execution.id = $8
 AND execution.run_id = run.id
 AND execution.run_attempt_id = attempt.id
 AND execution.run_attempt_generation = attempt.generation
 AND execution.env_id = $4
 AND execution.tool_name = 'shell'
 AND execution.target_kind = 'tae'
 AND execution.target_id = $10
 AND execution.target_generation = $11
 AND execution.status IN ('dispatching', 'running')
JOIN %s AS operation
  ON operation.id = $9
 AND operation.execution_id = execution.id
 AND operation.kind = 'process_start'
 AND operation.status IN %s
 AND operation.target_kind = 'tae'
 AND operation.target_id = execution.target_id
 AND operation.target_generation = execution.target_generation
JOIN %s AS sandbox
  ON sandbox.id = execution.target_id
 AND sandbox.generation = execution.target_generation
 AND sandbox.workspace_id = run.workspace_id
 AND sandbox.session_id = run.session_id
 AND sandbox.environment_id = execution.env_id
 AND sandbox.desired_state = 'ready'
 AND sandbox.observed_state = 'ready'
 AND sandbox.expires_at > authority_time.now%s
JOIN %s AS activity
  ON activity.sandbox_id = sandbox.id
 AND activity.target_generation = sandbox.generation
 AND activity.run_attempt_id = attempt.id
 AND activity.run_attempt_generation = attempt.generation
 AND activity.released_at IS NULL
 AND activity.lease_expires_at > authority_time.now
JOIN %s AS grant_state
  ON grant_state.id = launch.lark_grant_id
 AND grant_state.workspace_id = run.workspace_id
 AND grant_state.user_id = run.actor_id
 AND grant_state.authority_version = launch.lark_grant_version
 AND grant_state.policy_sha256 = launch.lark_policy_sha256
 AND grant_state.pack_id = 'lark-readonly@v1'
 AND grant_state.status = 'active'
 AND grant_state.access_expires_at > authority_time.now + INTERVAL '5 seconds'`,
			s.table("workspaces"), s.table("workspace_members"), s.table("sessions"),
			s.table("runs"), s.table("run_attempts"), s.table("session_leases"),
			s.table("attempt_leases"), s.table("run_launch_states"), frozenPredicate,
			s.table("executions"), s.table("execution_operations"), operationStatuses,
			s.table("managed_sandboxes"), sandboxPredicate, s.table("managed_sandbox_activities"),
			s.table("workspace_lark_grants"),
		)
		arguments := []any{
			query.WorkspaceID, query.SessionID, query.ActorID, query.EnvironmentID,
			query.RunID, query.AttemptID, query.AttemptGeneration,
			query.ExecutionID, query.OperationID, query.SandboxID, query.TargetGeneration,
		}
		if live {
			arguments = append(arguments, query.GrantID, query.GrantVersion, query.PolicySHA256[:], query.TAEPSM)
		}
		var authority ManagedLarkAuthority
		var policyBytes []byte
		if err := transaction.QueryRow(ctx, statement, arguments...).Scan(
			&authority.Binding.GrantID, &authority.Binding.GrantVersion,
			&authority.Binding.GrantUserID, &policyBytes,
			&authority.CredentialVersion, &authority.SealedTokenSet,
			&authority.AccessExpiresAt, &authority.AuthorizedAt,
		); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ManagedLarkAuthority{}, commandError(ErrorLeaseLost, operation, "operation", query.OperationID, "managed Lark operation no longer has live authority")
			}
			return ManagedLarkAuthority{}, databaseError(operation+" read live authority", err)
		}
		authority.WorkspaceID = query.WorkspaceID
		if err := copyStoredSHA256(&authority.Binding.PolicySHA256, policyBytes); err != nil {
			return ManagedLarkAuthority{}, databaseError(operation+" decode policy digest", err)
		}
		if err := validateRunLarkEgressBinding(authority.Binding); err != nil || authority.CredentialVersion < 1 ||
			len(authority.SealedTokenSet) < 29 || len(authority.SealedTokenSet) > 262144 ||
			!authority.AccessExpiresAt.After(authority.AuthorizedAt.Add(managedLarkCredentialMinimumLifetime)) {
			return ManagedLarkAuthority{}, databaseError(operation+" validate stored authority", errors.New("stored managed Lark authority is invalid"))
		}
		authority.SealedTokenSet = append([]byte(nil), authority.SealedTokenSet...)
		return authority, nil
	})
}

func validateManagedLarkAuthorityQuery(query ManagedLarkAuthorityQuery, live bool) error {
	for _, identity := range []struct{ name, value string }{
		{"workspace_id", query.WorkspaceID}, {"session_id", query.SessionID},
		{"actor_id", query.ActorID}, {"environment_id", query.EnvironmentID},
		{"run_id", query.RunID}, {"attempt_id", query.AttemptID},
		{"execution_id", query.ExecutionID}, {"operation_id", query.OperationID},
		{"sandbox_id", query.SandboxID},
	} {
		if err := validateUUID(identity.name, identity.value); err != nil {
			return err
		}
	}
	if query.AttemptGeneration < 1 || query.AttemptGeneration > maxSafeJSONInteger ||
		query.TargetGeneration < 1 || query.TargetGeneration > maxSafeJSONInteger {
		return errors.New("attempt and target generations must be positive safe integers")
	}
	if !live {
		if query.GrantID != "" || query.GrantVersion != 0 || !isZeroDigest(query.PolicySHA256) || query.TAEPSM != "" {
			return errors.New("placeholder authority resolution must not supply a caller-selected grant or PSM")
		}
		return nil
	}
	if err := validateRunLarkEgressBinding(RunLarkEgressBinding{
		GrantID: query.GrantID, GrantVersion: query.GrantVersion,
		GrantUserID: query.ActorID, PolicySHA256: query.PolicySHA256,
	}); err != nil {
		return err
	}
	return validateBoundedText("tae_psm", query.TAEPSM, 256)
}

func (s *StateStore) CreateWorkspaceLarkGrant(ctx context.Context, command CreateWorkspaceLarkGrantCommand) (WorkspaceLarkGrant, error) {
	const operation = "CreateWorkspaceLarkGrant"
	if err := validateCreateWorkspaceLarkGrant(command); err != nil {
		return WorkspaceLarkGrant{}, commandError(ErrorInvalidArgument, operation, "lark_grant", command.ID, err.Error())
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (WorkspaceLarkGrant, error) {
		query := fmt.Sprintf(`
INSERT INTO %s
    (id, workspace_id, user_id, pack_id, policy_sha256, status,
     sealed_token_set, access_expires_at, refresh_expires_at, next_refresh_at)
SELECT $1, member.workspace_id, member.user_id, $4, $5, $6, $7, $8, $9, $10
FROM %s AS member
JOIN %s AS workspace ON workspace.id = member.workspace_id
WHERE member.workspace_id = $2
  AND member.user_id = $3
  AND member.role IN ('owner', 'developer')
  AND workspace.status = 'active'
RETURNING %s`, s.table("workspace_lark_grants"), s.table("workspace_members"), s.table("workspaces"), workspaceLarkGrantColumns(""))
		grant, err := scanWorkspaceLarkGrant(transaction.QueryRow(
			ctx, query, command.ID, command.WorkspaceID, command.UserID,
			LarkReadOnlyPackID, command.PolicySHA256[:], LarkGrantStatusActive,
			command.SealedTokenSet, command.AccessExpiresAt, command.RefreshExpiresAt, command.NextRefreshAt,
		))
		if err == nil {
			return grant, nil
		}
		var postgresError *pgconn.PgError
		if pgxErrorAs(err, &postgresError) && postgresError.Code == "23505" {
			return WorkspaceLarkGrant{}, commandError(ErrorConflict, operation, "lark_grant", command.ID, "Lark grant identity or workspace user binding already exists")
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return WorkspaceLarkGrant{}, commandError(ErrorForbidden, operation, "workspace", command.WorkspaceID, "active owner or developer membership is required")
		}
		return WorkspaceLarkGrant{}, databaseError(operation+" insert", err)
	})
}

func (s *StateStore) UpdateWorkspaceLarkGrantCredential(
	ctx context.Context,
	command UpdateWorkspaceLarkGrantCredentialCommand,
) (WorkspaceLarkGrant, error) {
	const operation = "UpdateWorkspaceLarkGrantCredential"
	if err := validateUpdateWorkspaceLarkGrantCredential(command); err != nil {
		return WorkspaceLarkGrant{}, commandError(ErrorInvalidArgument, operation, "lark_grant", command.GrantID, err.Error())
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (WorkspaceLarkGrant, error) {
		query := fmt.Sprintf(`
UPDATE %s
SET sealed_token_set = $1,
    access_expires_at = $2,
    refresh_expires_at = $3,
    next_refresh_at = $4,
    credential_version = credential_version + 1,
    last_refreshed_at = pg_catalog.clock_timestamp(),
    last_refresh_error_code = NULL,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $5
  AND authority_version = $6
  AND credential_version = $7
  AND status = $8
  AND refresh_lock_owner IS NULL
RETURNING %s`, s.table("workspace_lark_grants"), workspaceLarkGrantColumns(""))
		grant, err := scanWorkspaceLarkGrant(transaction.QueryRow(
			ctx, query, command.SealedTokenSet, command.AccessExpiresAt, command.RefreshExpiresAt, command.NextRefreshAt,
			command.GrantID, command.ExpectedAuthorityVersion, command.ExpectedCredentialVersion,
			LarkGrantStatusActive,
		))
		if errors.Is(err, pgx.ErrNoRows) {
			return WorkspaceLarkGrant{}, versionConflict(operation, "lark_grant", command.GrantID, command.ExpectedCredentialVersion)
		}
		if err != nil {
			return WorkspaceLarkGrant{}, databaseError(operation+" update", err)
		}
		return grant, nil
	})
}

func validateCreateWorkspaceLarkGrant(command CreateWorkspaceLarkGrantCommand) error {
	for field, value := range map[string]string{
		"grant_id": command.ID, "workspace_id": command.WorkspaceID, "user_id": command.UserID,
	} {
		if err := validateUUID(field, value); err != nil {
			return err
		}
	}
	return validateWorkspaceLarkCredential(command.PolicySHA256, command.SealedTokenSet, command.AccessExpiresAt, command.RefreshExpiresAt, command.NextRefreshAt)
}

func validateUpdateWorkspaceLarkGrantCredential(command UpdateWorkspaceLarkGrantCredentialCommand) error {
	if err := validateUUID("grant_id", command.GrantID); err != nil {
		return err
	}
	if command.ExpectedAuthorityVersion < 1 || command.ExpectedAuthorityVersion > maxSafeJSONInteger ||
		command.ExpectedCredentialVersion < 1 || command.ExpectedCredentialVersion > maxSafeJSONInteger {
		return errors.New("expected Lark grant versions must be positive safe integers")
	}
	return validateWorkspaceLarkCredential([32]byte{1}, command.SealedTokenSet, command.AccessExpiresAt, command.RefreshExpiresAt, command.NextRefreshAt)
}

func validateWorkspaceLarkCredential(
	policy [32]byte,
	sealed []byte,
	accessExpiry time.Time,
	refreshExpiry *time.Time,
	nextRefresh time.Time,
) error {
	if isZeroDigest(policy) || len(sealed) < 29 || len(sealed) > 262144 || accessExpiry.IsZero() || nextRefresh.IsZero() {
		return errors.New("Lark policy, sealed token set, access expiry, and next refresh time are required")
	}
	if refreshExpiry != nil && (refreshExpiry.IsZero() || !refreshExpiry.After(accessExpiry)) {
		return errors.New("Lark refresh expiry must follow access expiry")
	}
	if !nextRefresh.Before(accessExpiry) {
		return errors.New("Lark next refresh time must precede access expiry")
	}
	return nil
}

func workspaceLarkGrantColumns(alias string) string {
	prefix := ""
	if alias != "" {
		prefix = alias + "."
	}
	return fmt.Sprintf(`%sid::text, %sworkspace_id::text, %suser_id::text,
       %spack_id, %spolicy_sha256, %sstatus, %ssealed_token_set,
       %saccess_expires_at, %srefresh_expires_at,
       %sauthority_version, %scredential_version, %slast_refreshed_at,
       %srevoked_at, %snext_refresh_at, %srefresh_lock_owner,
       %srefresh_lock_until, %srefresh_dispatched_at, %srefresh_attempts,
       %slast_refresh_error_code, %screated_at, %supdated_at`,
		prefix, prefix, prefix, prefix, prefix, prefix, prefix, prefix,
		prefix, prefix, prefix, prefix, prefix, prefix, prefix, prefix,
		prefix, prefix, prefix, prefix, prefix,
	)
}

func scanWorkspaceLarkGrant(row pgx.Row) (WorkspaceLarkGrant, error) {
	var grant WorkspaceLarkGrant
	var policy []byte
	err := row.Scan(
		&grant.ID, &grant.WorkspaceID, &grant.UserID, &grant.PackID,
		&policy, &grant.Status, &grant.SealedTokenSet,
		&grant.AccessExpiresAt, &grant.RefreshExpiresAt,
		&grant.AuthorityVersion, &grant.CredentialVersion, &grant.LastRefreshedAt,
		&grant.RevokedAt, &grant.NextRefreshAt, &grant.RefreshLockOwner,
		&grant.RefreshLockUntil, &grant.RefreshDispatchedAt, &grant.RefreshAttempts,
		&grant.LastRefreshErrorCode, &grant.CreatedAt, &grant.UpdatedAt,
	)
	if err != nil {
		return WorkspaceLarkGrant{}, err
	}
	if err := copyStoredSHA256(&grant.PolicySHA256, policy); err != nil {
		return WorkspaceLarkGrant{}, err
	}
	grant.SealedTokenSet = append([]byte(nil), grant.SealedTokenSet...)
	return grant, nil
}

func (s *StateStore) RecordManagedEgressAuditEvent(ctx context.Context, event ManagedEgressAuditEvent) error {
	const operation = "RecordManagedEgressAuditEvent"
	if err := validateManagedEgressAuditEvent(event); err != nil {
		return commandError(ErrorInvalidArgument, operation, "egress_audit_event", event.ID, err.Error())
	}
	_, err := withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (bool, error) {
		insert := fmt.Sprintf(`
INSERT INTO %s
    (id, decided_at, capability_id, workspace_id, session_id, run_id,
     run_attempt_id, run_attempt_generation, execution_id, operation_id,
     sandbox_id, target_generation, grant_id, grant_version, tae_psm,
     request_host, request_path, request_method, decision, reason_code)
VALUES
    ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
     $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)
ON CONFLICT (id) DO NOTHING`, s.table("managed_egress_audit_events"))
		arguments := managedEgressAuditArguments(event)
		result, err := transaction.Exec(ctx, insert, arguments...)
		if err != nil {
			return false, databaseError(operation+" insert event", err)
		}
		if result.RowsAffected() == 0 {
			match := fmt.Sprintf(`
SELECT 1
FROM %s
WHERE id = $1 AND decided_at = $2
  AND capability_id IS NOT DISTINCT FROM $3
  AND workspace_id IS NOT DISTINCT FROM $4
  AND session_id IS NOT DISTINCT FROM $5
  AND run_id IS NOT DISTINCT FROM $6
  AND run_attempt_id IS NOT DISTINCT FROM $7
  AND run_attempt_generation IS NOT DISTINCT FROM $8
  AND execution_id IS NOT DISTINCT FROM $9
  AND operation_id IS NOT DISTINCT FROM $10
  AND sandbox_id IS NOT DISTINCT FROM $11
  AND target_generation IS NOT DISTINCT FROM $12
  AND grant_id IS NOT DISTINCT FROM $13
  AND grant_version IS NOT DISTINCT FROM $14
  AND tae_psm IS NOT DISTINCT FROM $15
  AND request_host = $16 AND request_path = $17 AND request_method = $18
  AND decision = $19 AND reason_code = $20`, s.table("managed_egress_audit_events"))
			var marker int
			if err := transaction.QueryRow(ctx, match, arguments...).Scan(&marker); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return false, commandError(ErrorIdempotencyConflict, operation, "egress_audit_event", event.ID, "audit event identity is already bound to a different decision")
				}
				return false, databaseError(operation+" verify retry", err)
			}
		}
		outbox := fmt.Sprintf(`
INSERT INTO %s (audit_event_id)
VALUES ($1)
ON CONFLICT (audit_event_id) DO NOTHING`, s.table("managed_egress_audit_outbox"))
		if _, err := transaction.Exec(ctx, outbox, event.ID); err != nil {
			return false, databaseError(operation+" insert outbox", err)
		}
		return result.RowsAffected() == 1, nil
	})
	return err
}

func validateManagedEgressAuditEvent(event ManagedEgressAuditEvent) error {
	if err := validateUUID("audit_event_id", event.ID); err != nil {
		return err
	}
	if event.DecidedAt.IsZero() {
		return errors.New("audit decided_at is required")
	}
	for name, value := range map[string]string{
		"capability_id": event.CapabilityID, "workspace_id": event.WorkspaceID,
		"session_id": event.SessionID, "run_id": event.RunID,
		"run_attempt_id": event.RunAttemptID, "execution_id": event.ExecutionID,
		"operation_id": event.OperationID, "sandbox_id": event.SandboxID,
		"grant_id": event.GrantID,
	} {
		if value != "" {
			if err := validateBoundedText(name, value, 2048); err != nil {
				return err
			}
		}
	}
	if event.TAEPSM != "" {
		if err := validateBoundedText("tae_psm", event.TAEPSM, 256); err != nil {
			return err
		}
	}
	for name, value := range map[string]int64{
		"run_attempt_generation": event.RunAttemptGeneration,
		"target_generation":      event.TargetGeneration, "grant_version": event.GrantVersion,
	} {
		if value < 0 || value > maxSafeJSONInteger {
			return fmt.Errorf("%s must be zero or a positive safe integer", name)
		}
	}
	if event.Decision != "allow" && event.Decision != "deny" {
		return errors.New("audit decision must be allow or deny")
	}
	if err := validateBoundedText("reason_code", event.ReasonCode, 128); err != nil {
		return err
	}
	for name, value := range map[string]string{
		"request_host": event.RequestHost, "request_path": event.RequestPath,
		"request_method": event.RequestMethod,
	} {
		maximum := 65536
		if name == "request_method" {
			maximum = 256
		}
		if len(value) > maximum || strings.ContainsRune(value, '\x00') {
			return fmt.Errorf("%s is outside audit bounds", name)
		}
	}
	return nil
}

func managedEgressAuditArguments(event ManagedEgressAuditEvent) []any {
	return []any{
		event.ID, event.DecidedAt, nullableLarkText(event.CapabilityID),
		nullableLarkText(event.WorkspaceID), nullableLarkText(event.SessionID), nullableLarkText(event.RunID),
		nullableLarkText(event.RunAttemptID), nullablePositiveInt64(event.RunAttemptGeneration),
		nullableLarkText(event.ExecutionID), nullableLarkText(event.OperationID),
		nullableLarkText(event.SandboxID), nullablePositiveInt64(event.TargetGeneration),
		nullableLarkText(event.GrantID), nullablePositiveInt64(event.GrantVersion), nullableLarkText(event.TAEPSM),
		event.RequestHost, event.RequestPath, event.RequestMethod, event.Decision, event.ReasonCode,
	}
}

func nullableLarkText(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func larkBindingsEqual(left, right RunLarkEgressBinding) bool {
	return left.GrantID == right.GrantID && left.GrantVersion == right.GrantVersion &&
		left.GrantUserID == right.GrantUserID && bytes.Equal(left.PolicySHA256[:], right.PolicySHA256[:])
}
