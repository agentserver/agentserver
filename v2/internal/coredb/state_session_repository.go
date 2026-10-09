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
	if request.BindingID == "" {
		return errors.New("repository credential binding is required")
	}
	_, err := withStateReadTransaction(ctx, s, "AuthorizeRepositoryCredential", func(tx pgx.Tx) (bool, error) {
		q := fmt.Sprintf(`SELECT 1 FROM %s w JOIN %s m ON m.workspace_id=w.id AND m.user_id=$2 AND m.role IN ('owner','developer') JOIN %s s ON s.id=$3 AND s.workspace_id=w.id AND s.creator_id=m.user_id AND s.status='active' JOIN %s r ON r.id=$4 AND r.workspace_id=w.id AND r.session_id=s.id AND r.status IN ('running','starting','queued') JOIN %s sr ON sr.session_id=s.id AND sr.workspace_id=w.id AND sr.binding->>'credentialBindingId'=$5 AND sr.binding->>'environmentId'=$6 JOIN %s b ON b.id=$5::uuid AND b.workspace_id=w.id AND b.kind='git' AND b.owner_scope='workspace' AND b.status='active'`, s.table("workspaces"), s.table("workspace_members"), s.table("sessions"), s.table("runs"), s.table("session_repositories"), s.table("workspace_credential_bindings"))
		var one int
		if err := tx.QueryRow(ctx, q, request.Operation.WorkspaceID, request.Operation.ActorID, request.Operation.SessionID, request.RunID, request.BindingID, request.EnvironmentID).Scan(&one); err != nil {
			return false, errors.New("repository credential is not authorized for this run")
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
