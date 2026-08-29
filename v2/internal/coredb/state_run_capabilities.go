package coredb

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"

	"github.com/agentserver/agentserver/v2/internal/runmanifest"
	"github.com/jackc/pgx/v5"
)

func (s *StateStore) ResolveRunCapabilityIssuance(
	ctx context.Context,
	command ResolveRunCapabilityIssuanceCommand,
) (RunCapabilityIssuanceAuthority, error) {
	const operation = "ResolveRunCapabilityIssuance"
	if err := validateResolveRunCapabilityIssuance(command); err != nil {
		return RunCapabilityIssuanceAuthority{}, commandError(
			ErrorInvalidArgument, operation, "attempt", command.AttemptID, err.Error(),
		)
	}
	return withStateReadTransaction(ctx, s, operation, func(transaction pgx.Tx) (RunCapabilityIssuanceAuthority, error) {
		query := fmt.Sprintf(`
WITH authority_time AS MATERIALIZED (
    SELECT pg_catalog.clock_timestamp() AS now
)
SELECT r.actor_id::text, r.version, a.version, a.created_at,
       authority_time.now, s.latest_checkpoint_id::text,
       launch.llm_gateway_id::text, launch.llm_gateway_version,
       launch.llm_gateway_grant_user_id::text, launch.model
FROM authority_time
JOIN %s AS r
  ON r.id = $3 AND r.workspace_id = $1 AND r.session_id = $2
JOIN %s AS a
  ON a.id = $4 AND a.run_id = r.id AND a.generation = $6
JOIN %s AS s
  ON s.id = r.session_id AND s.workspace_id = r.workspace_id
 AND s.active_run_id = r.id
JOIN %s AS w
  ON w.id = r.workspace_id AND w.status = 'active'
JOIN %s AS wm
  ON wm.workspace_id = r.workspace_id AND wm.user_id = r.actor_id
 AND wm.role IN ('owner', 'developer')
JOIN %s AS sl
  ON sl.session_id = s.id AND sl.run_id = r.id
 AND sl.holder_id = $5 AND sl.generation = $6
 AND sl.expires_at > authority_time.now
JOIN %s AS al
  ON al.run_attempt_id = a.id
 AND al.holder_id = $5 AND al.generation = $6
 AND al.expires_at > authority_time.now
JOIN %s AS launch
  ON launch.run_id = r.id
 AND launch.workspace_id = r.workspace_id
 AND launch.session_id = r.session_id
WHERE r.status = 'starting'
  AND r.current_attempt_generation = $6
  AND r.version = $7
  AND a.status = 'leased'
  AND a.turn_started_at IS NULL
  AND a.holder_id = $5
  AND a.version = $8`,
			s.table("runs"), s.table("run_attempts"), s.table("sessions"),
			s.table("workspaces"), s.table("workspace_members"),
			s.table("session_leases"), s.table("attempt_leases"), s.table("run_launch_states"),
		)
		var authority RunCapabilityIssuanceAuthority
		var latestCheckpointID *string
		if err := transaction.QueryRow(
			ctx, query,
			command.WorkspaceID, command.SessionID, command.RunID, command.AttemptID,
			command.HolderID, command.Generation, command.ExpectedRunVersion,
			command.ExpectedAttemptVersion,
		).Scan(
			&authority.ActorID, &authority.RunVersion, &authority.AttemptVersion,
			&authority.AttemptCreatedAt, &authority.DatabaseTime, &latestCheckpointID,
			&authority.LLMGateway.GatewayID, &authority.LLMGateway.ConfigVersion,
			&authority.LLMGateway.GrantUserID, &authority.LLMGateway.Model,
		); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return RunCapabilityIssuanceAuthority{}, unavailableRunCapabilityAuthority(
					operation, command.AttemptID,
				)
			}
			return RunCapabilityIssuanceAuthority{}, databaseError(operation+" read live attempt authority", err)
		}
		if authority.LLMGateway != command.LLMGateway || authority.LLMGateway.GrantUserID != authority.ActorID {
			return RunCapabilityIssuanceAuthority{}, unavailableRunCapabilityAuthority(operation, command.AttemptID)
		}
		managedSandbox, err := s.readRunManagedSandboxBinding(ctx, transaction, operation, command.RunID)
		if err != nil || managedSandbox != command.ManagedSandbox {
			return RunCapabilityIssuanceAuthority{}, unavailableRunCapabilityAuthority(operation, command.AttemptID)
		}
		permissionMode, permissionModeVersion, permissionModeExplicit, err := s.readRunPermissionMode(ctx, transaction, operation, command.RunID)
		if err != nil || !permissionModeAuthorityMatches(permissionMode, permissionModeVersion, permissionModeExplicit, command.PermissionMode, command.PermissionModeVersion) {
			return RunCapabilityIssuanceAuthority{}, unavailableRunCapabilityAuthority(operation, command.AttemptID)
		}
		workspace, err := s.readRunWorkspaceBinding(ctx, transaction, operation, command.RunID)
		if err != nil || !workspaceBindingsEqual(workspace, command.Workspace) {
			return RunCapabilityIssuanceAuthority{}, unavailableRunCapabilityAuthority(operation, command.AttemptID)
		}
		catalogID, catalogDigest, err := s.readAuthorizedCapabilityCatalog(
			ctx, transaction, operation, command.RunID, command.SessionID, command.AttemptID,
			latestCheckpointID,
		)
		if err != nil {
			return RunCapabilityIssuanceAuthority{}, err
		}
		if catalogID != command.BrainToolCatalogID ||
			subtle.ConstantTimeCompare(catalogDigest[:], command.ToolCatalogDigest[:]) != 1 {
			return RunCapabilityIssuanceAuthority{}, unavailableRunCapabilityAuthority(operation, command.AttemptID)
		}
		authority.WorkspaceID = command.WorkspaceID
		authority.SessionID = command.SessionID
		authority.RunID = command.RunID
		authority.AttemptID = command.AttemptID
		authority.HolderID = command.HolderID
		authority.Generation = command.Generation
		authority.ExecutorID = command.ExecutorID
		authority.BrainToolCatalogID = catalogID
		authority.ToolCatalogDigest = catalogDigest
		authority.ManagedSandbox = managedSandbox
		authority.Workspace = workspace
		authority.PermissionMode = permissionMode
		authority.PermissionModeVersion = permissionModeVersion
		authority.PermissionModeExplicit = permissionModeExplicit
		return authority, nil
	})
}

func (s *StateStore) AuthorizeRunCapability(
	ctx context.Context,
	command AuthorizeRunCapabilityCommand,
) (AuthorizedRunCapability, error) {
	const operation = "AuthorizeRunCapability"
	if err := validateAuthorizeRunCapability(command); err != nil {
		return AuthorizedRunCapability{}, commandError(
			ErrorInvalidArgument, operation, "capability", command.CapabilityID, err.Error(),
		)
	}
	return withStateReadTransaction(ctx, s, operation, func(transaction pgx.Tx) (AuthorizedRunCapability, error) {
		query := fmt.Sprintf(`
WITH authority_time AS MATERIALIZED (
    SELECT pg_catalog.clock_timestamp() AS now
)
SELECT r.version, a.version, r.status, a.status,
       authority_time.now, s.latest_checkpoint_id::text
FROM authority_time
JOIN %s AS r
  ON r.id = $3 AND r.workspace_id = $1 AND r.session_id = $2
 AND r.actor_id = $5
JOIN %s AS a
  ON a.id = $4 AND a.run_id = r.id AND a.generation = $7
JOIN %s AS s
  ON s.id = r.session_id AND s.workspace_id = r.workspace_id
 AND s.active_run_id = r.id
JOIN %s AS w
  ON w.id = r.workspace_id AND w.status = 'active'
JOIN %s AS wm
  ON wm.workspace_id = r.workspace_id AND wm.user_id = r.actor_id
 AND wm.role IN ('owner', 'developer')
JOIN %s AS sl
  ON sl.session_id = s.id AND sl.run_id = r.id
 AND sl.holder_id = $6 AND sl.generation = $7
 AND sl.expires_at > authority_time.now
JOIN %s AS al
  ON al.run_attempt_id = a.id
 AND al.holder_id = $6 AND al.generation = $7
 AND al.expires_at > authority_time.now
WHERE r.current_attempt_generation = $7
  AND a.holder_id = $6
  AND (
      (r.status = 'starting' AND a.status = 'leased' AND a.turn_started_at IS NULL)
      OR
      (r.status = 'running' AND a.status = 'running' AND a.turn_started_at IS NOT NULL)
  )`,
			s.table("runs"), s.table("run_attempts"), s.table("sessions"),
			s.table("workspaces"), s.table("workspace_members"),
			s.table("session_leases"), s.table("attempt_leases"),
		)
		var result AuthorizedRunCapability
		var latestCheckpointID *string
		if err := transaction.QueryRow(
			ctx, query,
			command.WorkspaceID, command.SessionID, command.RunID, command.AttemptID,
			command.ActorID, command.HolderID, command.Generation,
		).Scan(
			&result.RunVersion, &result.AttemptVersion, &result.RunStatus,
			&result.AttemptStatus, &result.DatabaseTime, &latestCheckpointID,
		); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return AuthorizedRunCapability{}, unavailableRunCapabilityAuthority(operation, command.CapabilityID)
			}
			return AuthorizedRunCapability{}, databaseError(operation+" read live attempt authority", err)
		}

		if command.Audience == RunCapabilityAudienceExecutorMCP {
			versionsMatch := result.RunVersion == command.ExpectedRunVersion &&
				result.AttemptVersion == command.ExpectedAttemptVersion
			if result.RunStatus == RunStatusStarting {
				versionsMatch = result.RunVersion < maxSafeJSONInteger &&
					result.AttemptVersion < maxSafeJSONInteger &&
					result.RunVersion+1 == command.ExpectedRunVersion &&
					result.AttemptVersion+1 == command.ExpectedAttemptVersion
			}
			if !versionsMatch {
				return AuthorizedRunCapability{}, unavailableRunCapabilityAuthority(operation, command.CapabilityID)
			}
			managedSandbox, err := s.readRunManagedSandboxBinding(ctx, transaction, operation, command.RunID)
			if err != nil || managedSandbox != command.ManagedSandbox {
				return AuthorizedRunCapability{}, unavailableRunCapabilityAuthority(operation, command.CapabilityID)
			}
			workspace, err := s.readRunWorkspaceBinding(ctx, transaction, operation, command.RunID)
			if err != nil || !workspaceBindingsEqual(workspace, command.Workspace) {
				return AuthorizedRunCapability{}, unavailableRunCapabilityAuthority(operation, command.CapabilityID)
			}
			permissionMode, permissionModeVersion, permissionModeExplicit, err := s.readRunPermissionMode(ctx, transaction, operation, command.RunID)
			if err != nil || !permissionModeAuthorityMatches(permissionMode, permissionModeVersion, permissionModeExplicit, command.PermissionMode, command.PermissionModeVersion) {
				return AuthorizedRunCapability{}, unavailableRunCapabilityAuthority(operation, command.CapabilityID)
			}
			_, catalogDigest, err := s.readAuthorizedCapabilityCatalog(
				ctx, transaction, operation, command.RunID, command.SessionID, command.AttemptID,
				latestCheckpointID,
			)
			if err != nil {
				return AuthorizedRunCapability{}, err
			}
			if subtle.ConstantTimeCompare(catalogDigest[:], command.ToolCatalogDigest[:]) != 1 {
				return AuthorizedRunCapability{}, unavailableRunCapabilityAuthority(operation, command.CapabilityID)
			}
		} else {
			binding, err := s.readRunCapabilityLLMGatewayBinding(ctx, transaction, operation, command.RunID)
			if err != nil || binding != command.LLMGateway || binding.GrantUserID != command.ActorID {
				return AuthorizedRunCapability{}, unavailableRunCapabilityAuthority(operation, command.CapabilityID)
			}
			authority, err := s.readWorkspaceLLMGatewayLiveAuthority(
				ctx, transaction, operation, command.WorkspaceID, binding,
			)
			if err != nil {
				return AuthorizedRunCapability{}, err
			}
			result.LLMGateway = &authority
		}
		return result, nil
	})
}

func (s *StateStore) readRunCapabilityLLMGatewayBinding(
	ctx context.Context,
	transaction pgx.Tx,
	operation, runID string,
) (RunLLMGatewayBinding, error) {
	query := fmt.Sprintf(`
SELECT llm_gateway_id::text, llm_gateway_version,
       llm_gateway_grant_user_id::text, model
FROM %s
WHERE run_id = $1`, s.table("run_launch_states"))
	var binding RunLLMGatewayBinding
	if err := transaction.QueryRow(ctx, query, runID).Scan(
		&binding.GatewayID, &binding.ConfigVersion, &binding.GrantUserID, &binding.Model,
	); err != nil {
		return RunLLMGatewayBinding{}, err
	}
	if err := validateRunLLMGatewayBinding(binding); err != nil {
		return RunLLMGatewayBinding{}, databaseError(operation+" validate frozen LLM gateway binding", err)
	}
	return binding, nil
}

func (s *StateStore) readAuthorizedCapabilityCatalog(
	ctx context.Context,
	transaction pgx.Tx,
	operation, runID, sessionID, attemptID string,
	latestCheckpointID *string,
) (string, [sha256.Size]byte, error) {
	var query string
	var arguments []any
	if latestCheckpointID == nil {
		query = fmt.Sprintf(`
SELECT catalog.id::text, catalog.catalog_digest
FROM %s AS catalog
JOIN %s AS launch
  ON launch.run_id = $1
 AND catalog.policy_version = launch.executor_policy_version
 AND catalog.policy_context_digest = launch.executor_policy_context_digest
WHERE catalog.created_run_attempt_id = $3
  AND catalog.created_run_id = $1
  AND catalog.session_id = $2`, s.table("brain_tool_catalogs"), s.table("run_launch_states"))
		arguments = []any{runID, sessionID, attemptID}
	} else {
		query = fmt.Sprintf(`
SELECT catalog.id::text, catalog.catalog_digest
FROM %s AS checkpoint
JOIN %s AS catalog
  ON catalog.id = checkpoint.brain_tool_catalog_id
 AND catalog.session_id = checkpoint.session_id
 AND catalog.thread_id = checkpoint.thread_id
JOIN %s AS launch
  ON launch.run_id = $3
 AND catalog.policy_version = launch.executor_policy_version
 AND catalog.policy_context_digest = launch.executor_policy_context_digest
WHERE checkpoint.id = $1
  AND checkpoint.session_id = $2`,
			s.table("checkpoints"), s.table("brain_tool_catalogs"), s.table("run_launch_states"),
		)
		arguments = []any{*latestCheckpointID, sessionID, runID}
	}
	var catalogID string
	var rawDigest []byte
	if err := transaction.QueryRow(ctx, query, arguments...).Scan(&catalogID, &rawDigest); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", [sha256.Size]byte{}, unavailableRunCapabilityAuthority(operation, attemptID)
		}
		return "", [sha256.Size]byte{}, databaseError(operation+" read frozen catalog authority", err)
	}
	var digest [sha256.Size]byte
	if err := copyStoredSHA256(&digest, rawDigest); err != nil {
		return "", [sha256.Size]byte{}, databaseError(operation+" decode frozen catalog digest", err)
	}
	return catalogID, digest, nil
}

func validateResolveRunCapabilityIssuance(command ResolveRunCapabilityIssuanceCommand) error {
	for field, value := range map[string]string{
		"workspace_id": command.WorkspaceID, "session_id": command.SessionID,
		"run_id": command.RunID, "attempt_id": command.AttemptID,
		"executor_id": command.ExecutorID, "brain_tool_catalog_id": command.BrainToolCatalogID,
	} {
		if err := validateUUID(field, value); err != nil {
			return err
		}
	}
	if err := validateBoundedText("holder_id", command.HolderID, 256); err != nil {
		return err
	}
	if command.Generation < 1 || command.Generation > maxSafeJSONInteger ||
		command.ExpectedRunVersion < 1 || command.ExpectedRunVersion >= maxSafeJSONInteger ||
		command.ExpectedAttemptVersion < 1 || command.ExpectedAttemptVersion >= maxSafeJSONInteger {
		return errors.New("generation and expected versions must be positive safe integers with room for turn acceptance")
	}
	if command.ToolCatalogDigest == ([sha256.Size]byte{}) {
		return errors.New("tool_catalog_digest is required")
	}
	if err := validateRunLLMGatewayBinding(command.LLMGateway); err != nil {
		return err
	}
	if command.ManagedSandbox != (RunManagedSandboxBinding{}) {
		if err := validateRunManagedSandboxBinding(command.ManagedSandbox); err != nil {
			return err
		}
	}
	if command.Workspace != nil {
		if err := command.Workspace.Validate(); err != nil {
			return err
		}
	}
	if err := validatePermissionModeProjection(command.PermissionMode, command.PermissionModeVersion); err != nil {
		return err
	}
	return nil
}

func validateAuthorizeRunCapability(command AuthorizeRunCapabilityCommand) error {
	for field, value := range map[string]string{
		"capability_id": command.CapabilityID, "workspace_id": command.WorkspaceID,
		"session_id": command.SessionID, "run_id": command.RunID,
		"attempt_id": command.AttemptID, "actor_id": command.ActorID,
	} {
		if err := validateUUID(field, value); err != nil {
			return err
		}
	}
	if err := validateBoundedText("holder_id", command.HolderID, 256); err != nil {
		return err
	}
	if command.Generation < 1 || command.Generation > maxSafeJSONInteger {
		return errors.New("generation must be a positive safe integer")
	}
	switch command.Audience {
	case RunCapabilityAudienceExecutorMCP:
		if err := validateUUID("executor_id", command.ExecutorID); err != nil {
			return err
		}
		if command.ToolCatalogDigest == ([sha256.Size]byte{}) ||
			command.ExpectedRunVersion < 1 || command.ExpectedRunVersion > maxSafeJSONInteger ||
			command.ExpectedAttemptVersion < 1 || command.ExpectedAttemptVersion > maxSafeJSONInteger {
			return errors.New("executor capability catalog and expected versions are required")
		}
		if command.LLMGateway != (RunLLMGatewayBinding{}) {
			return errors.New("executor capability contains LLM gateway authority")
		}
		if command.ManagedSandbox != (RunManagedSandboxBinding{}) {
			if err := validateRunManagedSandboxBinding(command.ManagedSandbox); err != nil {
				return err
			}
		}
		if command.Workspace != nil {
			if err := command.Workspace.Validate(); err != nil {
				return err
			}
		}
		if err := validatePermissionModeProjection(command.PermissionMode, command.PermissionModeVersion); err != nil {
			return err
		}
	case RunCapabilityAudienceLLMProxy:
		if command.ExecutorID != "" || command.ToolCatalogDigest != ([sha256.Size]byte{}) ||
			command.ExpectedRunVersion != 0 || command.ExpectedAttemptVersion != 0 {
			return errors.New("llmproxy capability contains executor authority")
		}
		if err := validateRunLLMGatewayBinding(command.LLMGateway); err != nil {
			return err
		}
		if command.ManagedSandbox != (RunManagedSandboxBinding{}) {
			return errors.New("llmproxy capability contains managed sandbox authority")
		}
		if command.Workspace != nil {
			return errors.New("llmproxy capability contains workspace authority")
		}
		if command.PermissionMode != "" || command.PermissionModeVersion != 0 {
			return errors.New("llmproxy capability contains permission mode authority")
		}
	default:
		return errors.New("run capability audience is unsupported")
	}
	return nil
}

// validatePermissionModeProjection validates the optional request-side
// projection. Empty/zero is the explicit compatibility representation for a
// launch row created before session permission modes existed.
func validatePermissionModeProjection(mode runmanifest.CodexPermissionMode, version int64) error {
	if mode == "" {
		if version != 0 {
			return errors.New("permission mode version requires a mode")
		}
		return nil
	}
	effective, err := mode.Effective()
	if err != nil || effective != mode {
		if err == nil {
			err = errors.New("permission mode must be canonical")
		}
		return err
	}
	if version < 1 || version > maxSafeJSONInteger {
		return errors.New("permission mode version must be a positive JSON-safe integer")
	}
	return nil
}

func permissionModeAuthorityMatches(
	frozenMode runmanifest.CodexPermissionMode,
	frozenVersion int64,
	frozenExplicit bool,
	requestedMode runmanifest.CodexPermissionMode,
	requestedVersion int64,
) bool {
	if !frozenExplicit {
		return frozenMode == "" && frozenVersion == 0 && requestedMode == "" && requestedVersion == 0
	}
	return frozenMode != "" && frozenVersion >= 1 &&
		frozenMode == requestedMode && frozenVersion == requestedVersion
}

func unavailableRunCapabilityAuthority(operation, resourceID string) error {
	return commandError(
		ErrorForbidden, operation, "run_capability", resourceID,
		"live run capability authority is unavailable",
	)
}
