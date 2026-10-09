package coredb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/workspacerepository"
	"github.com/jackc/pgx/v5"
)

func (s *StateStore) AuthorizeRepositoryCredential(ctx context.Context, request corecontract.ResolveRepositoryCredentialRequest) error {
	const op = "AuthorizeRepositoryCredential"
	o := request.Operation
	for _, id := range []string{o.WorkspaceID, o.ActorID, o.SessionID, o.EnvironmentID, o.RunID, o.RunAttemptID, o.SandboxID, request.BindingID} {
		if validateUUID("repository_scope", id) != nil {
			return commandError(ErrorInvalidArgument, op, "repository", "", "repository credential scope is invalid")
		}
	}
	if request.EnvironmentID != o.EnvironmentID || request.RunID != o.RunID || request.RunAttemptID != o.RunAttemptID || o.RunAttemptGeneration < 1 || o.TargetGeneration < 1 || request.HolderID == "" || len(request.HolderID) > 256 || o.ExecutionID != "" || o.OperationID != "" {
		return commandError(ErrorInvalidArgument, op, "repository", "", "repository credential scope is invalid")
	}
	_, err := withStateReadTransaction(ctx, s, "AuthorizeRepositoryCredential", func(tx pgx.Tx) (bool, error) {
		q := fmt.Sprintf(`WITH clock AS MATERIALIZED (SELECT pg_catalog.clock_timestamp() AS now)
SELECT 1 FROM clock
JOIN %s w ON w.id=$1 AND w.status='active'
JOIN %s m ON m.workspace_id=w.id AND m.user_id=$2 AND m.role IN ('owner','developer')
JOIN %s u ON u.id=m.user_id AND u.status='active'
JOIN %s s ON s.id=$3 AND s.workspace_id=w.id AND s.creator_id=m.user_id AND s.status='active'
JOIN %s r ON r.id=$4 AND r.workspace_id=w.id AND r.session_id=s.id AND r.actor_id=m.user_id AND r.status IN ('running','starting') AND s.active_run_id=r.id AND r.current_attempt_generation=$8
JOIN %s a ON a.id=$7 AND a.run_id=r.id AND a.generation=$8 AND a.holder_id=$9
 AND ((r.status='starting' AND a.status='leased' AND a.turn_started_at IS NULL) OR (r.status='running' AND a.status='running' AND a.turn_started_at IS NOT NULL))
JOIN %s sl ON sl.session_id=s.id AND sl.run_id=r.id AND sl.holder_id=a.holder_id AND sl.generation=a.generation AND sl.expires_at>clock.now
JOIN %s al ON al.run_attempt_id=a.id AND al.holder_id=a.holder_id AND al.generation=a.generation AND al.expires_at>clock.now
JOIN %s launch ON launch.run_id=r.id AND launch.workspace_id=w.id AND launch.session_id=s.id
 AND launch.repository_binding->'source'->>'credentialBindingId'=$5
 AND launch.repository_binding->>'environmentId'=$6
 AND launch.repository_binding->>'checkoutId'=s.id::text
 AND launch.workspace_environment_id=$6::uuid
JOIN %s sandbox ON sandbox.id=$10 AND sandbox.generation=$11 AND sandbox.workspace_id=w.id AND sandbox.session_id=s.id AND sandbox.environment_id=$6::uuid AND sandbox.provider_kind='k8s' AND sandbox.desired_state='ready' AND sandbox.observed_state='ready' AND sandbox.expires_at>clock.now
JOIN %s activity ON activity.sandbox_id=sandbox.id AND activity.target_generation=sandbox.generation AND activity.run_attempt_id=a.id AND activity.run_attempt_generation=a.generation AND activity.released_at IS NULL AND activity.lease_expires_at>clock.now
JOIN %s b ON b.id=$5::uuid AND b.workspace_id=w.id AND b.kind='git' AND b.auth_type='https-token' AND b.owner_scope='workspace' AND b.status='active'
 AND (b.access_expires_at IS NULL OR b.access_expires_at>clock.now)
 AND ($12::bigint=0 OR b.authority_version=$12) AND ($13::bigint=0 OR b.credential_version=$13)`,
			s.table("workspaces"), s.table("workspace_members"), s.table("users"), s.table("sessions"), s.table("runs"), s.table("run_attempts"), s.table("session_leases"), s.table("attempt_leases"), s.table("run_launch_states"), s.table("managed_sandboxes"), s.table("managed_sandbox_activities"), s.table("workspace_credential_bindings"))
		var one int
		if err := tx.QueryRow(ctx, q, o.WorkspaceID, o.ActorID, o.SessionID, o.RunID, request.BindingID, o.EnvironmentID, o.RunAttemptID, o.RunAttemptGeneration, request.HolderID, o.SandboxID, o.TargetGeneration, request.ExpectedAuthorityVersion, request.ExpectedCredentialVersion).Scan(&one); err != nil {
			if !errors.Is(err, pgx.ErrNoRows) {
				return false, databaseError(op, err)
			}
			return false, commandError(ErrorForbidden, op, "repository", request.BindingID, "repository credential is not authorized for this live run")
		}
		return true, nil
	})
	return err
}

func decodeRepositoryBinding(raw []byte) (*workspacerepository.Binding, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var binding workspacerepository.Binding
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&binding); err != nil {
		return nil, err
	}
	var extra any
	if !errors.Is(d.Decode(&extra), io.EOF) {
		return nil, errors.New("invalid repository binding document")
	}
	if err := binding.Validate(); err != nil {
		return nil, err
	}
	return &binding, nil
}

func (s *StateStore) RecordRepositoryCredentialUse(ctx context.Context, r corecontract.ResolveRepositoryCredentialRequest, eventID, decision string) error {
	if validateUUID("event_id", eventID) != nil || (decision != "allow" && decision != "deny") {
		return errors.New("invalid repository audit event")
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = withStateTransaction(ctx, s, "RecordRepositoryCredentialUse", func(tx pgx.Tx) (bool, error) {
		q := fmt.Sprintf(`INSERT INTO %s (event_id,scope,authority_version,credential_version,decision) VALUES ($1,$2,$3,$4,$5)`, s.table("repository_credential_use_events"))
		_, err := tx.Exec(ctx, q, eventID, raw, r.ExpectedAuthorityVersion, r.ExpectedCredentialVersion, decision)
		return err == nil, err
	})
	return err
}

func (s *StateStore) readSessionRepository(ctx context.Context, tx pgx.Tx, op, workspaceID, sessionID string) (*workspacerepository.Binding, error) {
	var raw []byte
	query := fmt.Sprintf(`SELECT binding FROM %s WHERE workspace_id=$1 AND session_id=$2`, s.table("session_repositories"))
	if err := tx.QueryRow(ctx, query, workspaceID, sessionID).Scan(&raw); errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	} else if err != nil {
		return nil, databaseError(op+" read session repository", err)
	}
	binding, err := decodeRepositoryBinding(raw)
	if err != nil || binding == nil || binding.CheckoutID != sessionID {
		return nil, databaseError(op+" validate session repository", errors.New("stored repository binding is invalid"))
	}
	return binding, nil
}

// inheritSessionRepository runs exactly once inside the session-create
// transaction. It never changes a pre-existing session after a default update.
func (s *StateStore) inheritSessionRepository(ctx context.Context, tx pgx.Tx, op, workspaceID, sessionID string) error {
	setting, err := s.readWorkspaceRepository(ctx, tx, op, workspaceID, false)
	if err != nil || setting.Source == nil {
		return err
	}
	managed, err := s.readWorkspaceManagedSandboxSetting(ctx, tx, op, workspaceID, false)
	if err != nil {
		return err
	}
	if managed.Region != "cn" && managed.Region != "sg" {
		return commandError(ErrorInvalidState, op, "workspace", workspaceID, "repository sessions require a CN or SG Kubernetes sandbox")
	}
	environmentID := s.managedProfilesByRegion[managed.Region]
	if environmentID == "" {
		return commandError(ErrorInvalidState, op, "workspace", workspaceID, "repository sandbox region is not installed")
	}
	binding := workspacerepository.Binding{CheckoutID: sessionID, Source: *setting.Source, SourceVersion: setting.Version, Region: managed.Region, EnvironmentID: environmentID, ManagedSettingVersion: managed.Version}
	environment, err := s.readWorkspaceBindingEnvironment(ctx, tx, op, workspaceID, environmentID, &binding)
	if err != nil {
		return err
	}
	if environment.Status == ExecutorEnvironmentStatusDisabled || environment.ExecutorStatus == ExecutorStatusRevoked {
		return commandError(ErrorInvalidState, op, "environment", environmentID, "repository environment is disabled or revoked")
	}
	raw, err := json.Marshal(binding)
	if err != nil {
		return err
	}
	query := fmt.Sprintf(`INSERT INTO %s (session_id,workspace_id,binding) VALUES ($1,$2,$3)`, s.table("session_repositories"))
	if _, err := tx.Exec(ctx, query, sessionID, workspaceID, raw); err != nil {
		return databaseError(op+" bind session repository", err)
	}
	update := fmt.Sprintf(`UPDATE %s SET working_environment_id=$2,working_directory=$3 WHERE id=$1`, s.table("sessions"))
	if _, err := tx.Exec(ctx, update, sessionID, environmentID, binding.Source.WorkingDirectory); err != nil {
		return databaseError(op+" inherit repository directory", err)
	}
	return nil
}

func (s *StateStore) GetUserSessionRepository(ctx context.Context, workspaceID, sessionID, actorID string) (*workspacerepository.Binding, error) {
	const op = "GetUserSessionRepository"
	if err := validateUserSessionScope(workspaceID, sessionID, actorID); err != nil {
		return nil, commandError(ErrorInvalidArgument, op, "session", sessionID, err.Error())
	}
	return withStateReadTransaction(ctx, s, op, func(tx pgx.Tx) (*workspacerepository.Binding, error) {
		if _, err := s.readUserSession(ctx, tx, op, workspaceID, sessionID, actorID, false); err != nil {
			return nil, err
		}
		return s.readSessionRepository(ctx, tx, op, workspaceID, sessionID)
	})
}
