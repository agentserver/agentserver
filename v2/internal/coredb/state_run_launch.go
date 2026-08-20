package coredb

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	checkpointartifact "github.com/agentserver/agentserver/v2/internal/checkpoint"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	MaxRunLaunchAllowedTools = 64
	maxRunObjectBytes        = int64(1 << 40)
	maxSafeJSONInteger       = int64(1<<53 - 1)
)

func (s *StateStore) ResolveRunLaunchState(ctx context.Context, command ResolveRunLaunchStateCommand) (ResolvedRunLaunchState, error) {
	const operation = "ResolveRunLaunchState"
	if err := validateResolveRunLaunchState(command); err != nil {
		return ResolvedRunLaunchState{}, commandError(ErrorInvalidArgument, operation, "attempt", command.AttemptID, err.Error())
	}

	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (ResolvedRunLaunchState, error) {
		run, attempt, err := s.lockRunAttempt(ctx, transaction, operation, command.RunID, command.AttemptID)
		if err != nil {
			return ResolvedRunLaunchState{}, err
		}
		if run.WorkspaceID != command.WorkspaceID || run.SessionID != command.SessionID {
			return ResolvedRunLaunchState{}, commandError(ErrorConflict, operation, "run", run.ID, "run does not belong to the requested workspace and session")
		}
		if run.Version != command.ExpectedRunVersion {
			return ResolvedRunLaunchState{}, versionConflict(operation, "run", run.ID, run.Version)
		}
		if attempt.Version != command.ExpectedAttemptVersion {
			return ResolvedRunLaunchState{}, versionConflict(operation, "attempt", attempt.ID, attempt.Version)
		}
		if err := s.requireLiveCatalogPreparationContext(ctx, transaction, operation, run, attempt, command.HolderID, command.Generation); err != nil {
			return ResolvedRunLaunchState{}, err
		}

		prompt, policy, llmGateway, larkEgress, err := s.readRunLaunchInput(ctx, transaction, operation, run.ID)
		if err != nil {
			return ResolvedRunLaunchState{}, err
		}
		managedSandbox, err := s.readRunManagedSandboxBinding(ctx, transaction, operation, run.ID)
		if err != nil {
			return ResolvedRunLaunchState{}, err
		}
		checkpoint, err := s.readLatestCheckpoint(ctx, transaction, operation, run.SessionID)
		if err != nil {
			return ResolvedRunLaunchState{}, err
		}
		return ResolvedRunLaunchState{
			WorkspaceID: run.WorkspaceID, SessionID: run.SessionID, RunID: run.ID,
			AttemptID: attempt.ID, HolderID: attempt.HolderID, Generation: attempt.Generation,
			RunVersion: run.Version, AttemptVersion: attempt.Version,
			Prompt: prompt, PreviousCheckpoint: checkpoint, ExecutorPolicy: policy,
			LLMGateway: llmGateway, LarkEgress: larkEgress, ManagedSandbox: managedSandbox,
		}, nil
	})
}

func validateResolveRunLaunchState(command ResolveRunLaunchStateCommand) error {
	for _, identifier := range []struct {
		field string
		value string
	}{
		{"workspace_id", command.WorkspaceID},
		{"session_id", command.SessionID},
		{"run_id", command.RunID},
		{"attempt_id", command.AttemptID},
	} {
		if err := validateUUID(identifier.field, identifier.value); err != nil {
			return err
		}
	}
	if err := validateBoundedText("holder_id", command.HolderID, 256); err != nil {
		return err
	}
	if command.Generation < 1 || command.Generation > maxSafeJSONInteger {
		return errors.New("generation must be a positive safe integer")
	}
	if command.ExpectedRunVersion < 1 || command.ExpectedAttemptVersion < 1 {
		return errors.New("expected versions must be positive")
	}
	return nil
}

func normalizeRunExecutorPolicy(policy RunExecutorPolicy) (RunExecutorPolicy, error) {
	normalized := policy
	normalized.AllowedTools = append([]string(nil), policy.AllowedTools...)
	sort.Strings(normalized.AllowedTools)
	if err := validateRunExecutorPolicy(normalized); err != nil {
		return RunExecutorPolicy{}, err
	}
	return normalized, nil
}

func validateRunExecutorPolicy(policy RunExecutorPolicy) error {
	if err := validateBoundedText("executor_policy.version", policy.Version, 128); err != nil {
		return err
	}
	if policy.ContextDigest == ([sha256.Size]byte{}) {
		return errors.New("executor_policy.context_digest is required")
	}
	if len(policy.AllowedTools) > MaxRunLaunchAllowedTools {
		return fmt.Errorf("executor_policy.allowed_tools cannot contain more than %d entries", MaxRunLaunchAllowedTools)
	}
	for index, tool := range policy.AllowedTools {
		if err := validateBoundedText(fmt.Sprintf("executor_policy.allowed_tools[%d]", index), tool, 128); err != nil {
			return err
		}
		if index > 0 && policy.AllowedTools[index-1] >= tool {
			return errors.New("executor_policy.allowed_tools must be sorted and unique")
		}
	}
	return nil
}

func validateRunObjectPointer(field string, pointer ObjectPointer) error {
	if err := validateUUID(field+".object_id", pointer.ObjectID); err != nil {
		return err
	}
	if pointer.Size < 1 || pointer.Size > maxRunObjectBytes {
		return fmt.Errorf("%s.size must be between 1 and %d", field, maxRunObjectBytes)
	}
	if err := validateBoundedText(field+".media_type", pointer.MediaType, 255); err != nil {
		return err
	}
	if strings.ContainsAny(pointer.MediaType, "\r\n") {
		return fmt.Errorf("%s.media_type must not contain a line break", field)
	}
	return nil
}

func (s *StateStore) insertRunLaunchInput(ctx context.Context, transaction pgx.Tx, command CreateRunCommand) error {
	var gatewayID, grantUserID, model any
	var gatewayVersion any
	if command.LLMGateway != (RunLLMGatewayBinding{}) {
		if err := validateRunLLMGatewayBinding(command.LLMGateway); err != nil {
			return commandError(ErrorInvalidArgument, "CreateRun", "llm_gateway", command.LLMGateway.GatewayID, err.Error())
		}
		gatewayID = command.LLMGateway.GatewayID
		gatewayVersion = command.LLMGateway.ConfigVersion
		grantUserID = command.LLMGateway.GrantUserID
		model = command.LLMGateway.Model
	}
	var larkGrantID, larkGrantUserID, larkPolicySHA256 any
	var larkGrantVersion any
	if command.LarkEgress != (RunLarkEgressBinding{}) {
		if err := validateRunLarkEgressBinding(command.LarkEgress); err != nil {
			return commandError(ErrorInvalidArgument, "CreateRun", "lark_grant", command.LarkEgress.GrantID, err.Error())
		}
		larkGrantID = command.LarkEgress.GrantID
		larkGrantVersion = command.LarkEgress.GrantVersion
		larkGrantUserID = command.LarkEgress.GrantUserID
		larkPolicySHA256 = command.LarkEgress.PolicySHA256[:]
	}
	var managedSettingVersion, managedRegion, managedEnvironmentID any
	if command.ManagedSandbox != (RunManagedSandboxBinding{}) {
		managedSettingVersion = command.ManagedSandbox.SettingVersion
		managedRegion = command.ManagedSandbox.Region
		managedEnvironmentID = command.ManagedSandbox.EnvironmentID
	}
	query := fmt.Sprintf(`
INSERT INTO %s
    (run_id, workspace_id, session_id,
     prompt_object_id, prompt_sha256, prompt_size, prompt_media_type,
     executor_policy_version, executor_policy_context_digest,
     llm_gateway_id, llm_gateway_version, llm_gateway_grant_user_id, model,
	 lark_grant_id, lark_grant_version, lark_grant_user_id, lark_policy_sha256,
	 managed_sandbox_setting_version, managed_sandbox_region,
	 managed_sandbox_environment_id)
VALUES
	($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13,
	 $14, $15, $16, $17, $18, $19, $20)`, s.table("run_launch_states"))
	if _, err := transaction.Exec(ctx, query,
		command.RunID,
		command.WorkspaceID,
		command.SessionID,
		command.Prompt.ObjectID,
		command.Prompt.SHA256[:],
		command.Prompt.Size,
		command.Prompt.MediaType,
		command.ExecutorPolicy.Version,
		command.ExecutorPolicy.ContextDigest[:],
		gatewayID,
		gatewayVersion,
		grantUserID,
		model,
		larkGrantID,
		larkGrantVersion,
		larkGrantUserID,
		larkPolicySHA256,
		managedSettingVersion,
		managedRegion,
		managedEnvironmentID,
	); err != nil {
		var postgresError *pgconn.PgError
		if pgxErrorAs(err, &postgresError) && postgresError.Code == "23505" {
			return commandError(ErrorConflict, "CreateRun", "run_launch_state", command.RunID, "run launch authority already exists")
		}
		return databaseError("CreateRun insert run launch state", err)
	}

	toolQuery := fmt.Sprintf(`
INSERT INTO %s (run_id, ordinal, tool_name)
VALUES ($1, $2, $3)`, s.table("run_launch_allowed_tools"))
	for index, tool := range command.ExecutorPolicy.AllowedTools {
		if _, err := transaction.Exec(ctx, toolQuery, command.RunID, index+1, tool); err != nil {
			return databaseError("CreateRun insert allowed tool", err)
		}
	}
	return nil
}

func (s *StateStore) readRunManagedSandboxBinding(
	ctx context.Context,
	transaction pgx.Tx,
	operation, runID string,
) (RunManagedSandboxBinding, error) {
	query := fmt.Sprintf(`
SELECT managed_sandbox_setting_version, managed_sandbox_region,
	   managed_sandbox_environment_id::text
FROM %s
WHERE run_id = $1`, s.table("run_launch_states"))
	var settingVersion *int64
	var region, environmentID *string
	if err := transaction.QueryRow(ctx, query, runID).Scan(
		&settingVersion, &region, &environmentID,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return RunManagedSandboxBinding{}, commandError(ErrorInvalidState, operation, "run", runID, "run has no immutable launch authority")
		}
		return RunManagedSandboxBinding{}, databaseError(operation+" read managed sandbox binding", err)
	}
	if settingVersion == nil && region == nil && environmentID == nil {
		return RunManagedSandboxBinding{}, nil
	}
	if settingVersion == nil || region == nil || environmentID == nil {
		return RunManagedSandboxBinding{}, databaseError(operation+" decode managed sandbox binding", errors.New("stored binding is incomplete"))
	}
	binding := RunManagedSandboxBinding{
		SettingVersion: *settingVersion, Region: *region, EnvironmentID: *environmentID,
	}
	if err := validateRunManagedSandboxBinding(binding); err != nil {
		return RunManagedSandboxBinding{}, databaseError(operation+" validate managed sandbox binding", err)
	}
	return binding, nil
}

func (s *StateStore) readRunLaunchInput(ctx context.Context, transaction pgx.Tx, operation, runID string) (ObjectPointer, RunExecutorPolicy, RunLLMGatewayBinding, RunLarkEgressBinding, error) {
	query := fmt.Sprintf(`
SELECT prompt_object_id::text, prompt_sha256, prompt_size, prompt_media_type,
       executor_policy_version, executor_policy_context_digest,
       llm_gateway_id::text, llm_gateway_version,
       llm_gateway_grant_user_id::text, model,
       lark_grant_id::text, lark_grant_version,
       lark_grant_user_id::text, lark_policy_sha256
FROM %s
WHERE run_id = $1`, s.table("run_launch_states"))
	var prompt ObjectPointer
	var promptDigest []byte
	var policy RunExecutorPolicy
	var policyDigest []byte
	var gatewayID, grantUserID, model *string
	var gatewayVersion *int64
	var larkGrantID, larkGrantUserID *string
	var larkGrantVersion *int64
	var larkPolicySHA256 []byte
	if err := transaction.QueryRow(ctx, query, runID).Scan(
		&prompt.ObjectID,
		&promptDigest,
		&prompt.Size,
		&prompt.MediaType,
		&policy.Version,
		&policyDigest,
		&gatewayID,
		&gatewayVersion,
		&grantUserID,
		&model,
		&larkGrantID,
		&larkGrantVersion,
		&larkGrantUserID,
		&larkPolicySHA256,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ObjectPointer{}, RunExecutorPolicy{}, RunLLMGatewayBinding{}, RunLarkEgressBinding{}, commandError(ErrorInvalidState, operation, "run", runID, "run has no immutable launch authority")
		}
		return ObjectPointer{}, RunExecutorPolicy{}, RunLLMGatewayBinding{}, RunLarkEgressBinding{}, databaseError(operation+" read run launch state", err)
	}
	if err := copyStoredSHA256(&prompt.SHA256, promptDigest); err != nil {
		return ObjectPointer{}, RunExecutorPolicy{}, RunLLMGatewayBinding{}, RunLarkEgressBinding{}, databaseError(operation+" decode prompt digest", err)
	}
	if err := copyStoredSHA256(&policy.ContextDigest, policyDigest); err != nil {
		return ObjectPointer{}, RunExecutorPolicy{}, RunLLMGatewayBinding{}, RunLarkEgressBinding{}, databaseError(operation+" decode policy digest", err)
	}
	var llmGateway RunLLMGatewayBinding
	if gatewayID != nil || gatewayVersion != nil || grantUserID != nil || model != nil {
		if gatewayID == nil || gatewayVersion == nil || grantUserID == nil || model == nil {
			return ObjectPointer{}, RunExecutorPolicy{}, RunLLMGatewayBinding{}, RunLarkEgressBinding{}, databaseError(operation+" decode LLM gateway binding", errors.New("stored binding is incomplete"))
		}
		llmGateway = RunLLMGatewayBinding{GatewayID: *gatewayID, ConfigVersion: *gatewayVersion, GrantUserID: *grantUserID, Model: *model}
		if err := validateRunLLMGatewayBinding(llmGateway); err != nil {
			return ObjectPointer{}, RunExecutorPolicy{}, RunLLMGatewayBinding{}, RunLarkEgressBinding{}, databaseError(operation+" validate LLM gateway binding", err)
		}
	}
	var larkEgress RunLarkEgressBinding
	if larkGrantID != nil || larkGrantVersion != nil || larkGrantUserID != nil || larkPolicySHA256 != nil {
		if larkGrantID == nil || larkGrantVersion == nil || larkGrantUserID == nil || larkPolicySHA256 == nil {
			return ObjectPointer{}, RunExecutorPolicy{}, RunLLMGatewayBinding{}, RunLarkEgressBinding{}, databaseError(operation+" decode Lark egress binding", errors.New("stored binding is incomplete"))
		}
		larkEgress = RunLarkEgressBinding{
			GrantID: *larkGrantID, GrantVersion: *larkGrantVersion, GrantUserID: *larkGrantUserID,
		}
		if err := copyStoredSHA256(&larkEgress.PolicySHA256, larkPolicySHA256); err != nil {
			return ObjectPointer{}, RunExecutorPolicy{}, RunLLMGatewayBinding{}, RunLarkEgressBinding{}, databaseError(operation+" decode Lark policy digest", err)
		}
		if err := validateRunLarkEgressBinding(larkEgress); err != nil {
			return ObjectPointer{}, RunExecutorPolicy{}, RunLLMGatewayBinding{}, RunLarkEgressBinding{}, databaseError(operation+" validate Lark egress binding", err)
		}
	}

	toolQuery := fmt.Sprintf(`
SELECT tool_name
FROM %s
WHERE run_id = $1
ORDER BY ordinal`, s.table("run_launch_allowed_tools"))
	rows, err := transaction.Query(ctx, toolQuery, runID)
	if err != nil {
		return ObjectPointer{}, RunExecutorPolicy{}, RunLLMGatewayBinding{}, RunLarkEgressBinding{}, databaseError(operation+" read allowed tools", err)
	}
	defer rows.Close()
	for rows.Next() {
		var tool string
		if err := rows.Scan(&tool); err != nil {
			return ObjectPointer{}, RunExecutorPolicy{}, RunLLMGatewayBinding{}, RunLarkEgressBinding{}, databaseError(operation+" scan allowed tool", err)
		}
		policy.AllowedTools = append(policy.AllowedTools, tool)
	}
	if err := rows.Err(); err != nil {
		return ObjectPointer{}, RunExecutorPolicy{}, RunLLMGatewayBinding{}, RunLarkEgressBinding{}, databaseError(operation+" finish allowed tools", err)
	}
	if err := validateRunObjectPointer("prompt", prompt); err != nil {
		return ObjectPointer{}, RunExecutorPolicy{}, RunLLMGatewayBinding{}, RunLarkEgressBinding{}, databaseError(operation+" validate stored prompt", err)
	}
	if err := validateRunExecutorPolicy(policy); err != nil {
		return ObjectPointer{}, RunExecutorPolicy{}, RunLLMGatewayBinding{}, RunLarkEgressBinding{}, databaseError(operation+" validate stored executor policy", err)
	}
	return prompt, policy, llmGateway, larkEgress, nil
}

func (s *StateStore) readLatestCheckpoint(ctx context.Context, transaction pgx.Tx, operation, sessionID string) (*Checkpoint, error) {
	latestQuery := fmt.Sprintf("SELECT latest_checkpoint_id::text FROM %s WHERE id = $1", s.table("sessions"))
	var checkpointID *string
	if err := transaction.QueryRow(ctx, latestQuery, sessionID).Scan(&checkpointID); err != nil {
		return nil, databaseError(operation+" read latest checkpoint identity", err)
	}
	if checkpointID == nil {
		return nil, nil
	}

	query := fmt.Sprintf(`
SELECT c.id::text, c.workspace_id::text, c.session_id::text,
       c.run_id::text, c.run_attempt_id::text, c.attempt_generation,
       c.brain_tool_catalog_id::text, c.thread_id, c.turn_id,
       c.manifest_digest, b.catalog_digest,
       c.object_id::text, c.object_sha256, c.object_size, c.object_media_type,
       c.codex_runtime_manifest_digest, c.checkpoint_allowlist_version,
       c.created_at, b.session_id::text, b.thread_id
FROM %s AS c
JOIN %s AS b ON b.id = c.brain_tool_catalog_id
WHERE c.id = $1 AND c.session_id = $2`, s.table("checkpoints"), s.table("brain_tool_catalogs"))
	var checkpoint Checkpoint
	var manifestDigest, catalogDigest, objectDigest, runtimeDigest []byte
	var catalogSessionID string
	var catalogThreadID *string
	if err := transaction.QueryRow(ctx, query, *checkpointID, sessionID).Scan(
		&checkpoint.ID,
		&checkpoint.WorkspaceID,
		&checkpoint.SessionID,
		&checkpoint.RunID,
		&checkpoint.AttemptID,
		&checkpoint.AttemptGeneration,
		&checkpoint.BrainToolCatalogID,
		&checkpoint.ThreadID,
		&checkpoint.TurnID,
		&manifestDigest,
		&catalogDigest,
		&checkpoint.Object.ObjectID,
		&objectDigest,
		&checkpoint.Object.Size,
		&checkpoint.Object.MediaType,
		&runtimeDigest,
		&checkpoint.CheckpointAllowlistVersion,
		&checkpoint.CreatedAt,
		&catalogSessionID,
		&catalogThreadID,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, databaseError(operation+" resolve latest checkpoint", errors.New("session latest checkpoint does not resolve to a committed checkpoint and catalog"))
		}
		return nil, databaseError(operation+" read latest checkpoint", err)
	}
	for destination, source := range map[*[sha256.Size]byte][]byte{
		&checkpoint.ManifestDigest:             manifestDigest,
		&checkpoint.CatalogDigest:              catalogDigest,
		&checkpoint.Object.SHA256:              objectDigest,
		&checkpoint.CodexRuntimeManifestDigest: runtimeDigest,
	} {
		if err := copyStoredSHA256(destination, source); err != nil {
			return nil, databaseError(operation+" decode latest checkpoint digest", err)
		}
	}
	if catalogSessionID != checkpoint.SessionID || catalogThreadID == nil || *catalogThreadID != checkpoint.ThreadID {
		return nil, databaseError(operation+" validate latest checkpoint catalog", errors.New("checkpoint catalog does not match its session and thread"))
	}
	catalogQuery := fmt.Sprintf(`
SELECT %s
FROM %s AS b
WHERE b.id = $1`, brainToolCatalogColumns("b"), s.table("brain_tool_catalogs"))
	catalog, err := scanBrainToolCatalog(transaction.QueryRow(ctx, catalogQuery, checkpoint.BrainToolCatalogID))
	if err != nil {
		return nil, databaseError(operation+" read latest checkpoint catalog authority", err)
	}
	if catalog.SessionID != checkpoint.SessionID || catalog.ThreadID != checkpoint.ThreadID || catalog.CatalogDigest != checkpoint.CatalogDigest {
		return nil, databaseError(operation+" validate latest checkpoint catalog authority", errors.New("checkpoint catalog authority fingerprint is inconsistent"))
	}
	checkpoint.Catalog = catalog
	if err := validateStoredCheckpoint(checkpoint); err != nil {
		return nil, databaseError(operation+" validate latest checkpoint", err)
	}
	return &checkpoint, nil
}

func validateStoredCheckpoint(checkpoint Checkpoint) error {
	for _, identifier := range []struct {
		field string
		value string
	}{
		{"checkpoint.id", checkpoint.ID},
		{"checkpoint.workspace_id", checkpoint.WorkspaceID},
		{"checkpoint.session_id", checkpoint.SessionID},
		{"checkpoint.run_id", checkpoint.RunID},
		{"checkpoint.attempt_id", checkpoint.AttemptID},
		{"checkpoint.brain_tool_catalog_id", checkpoint.BrainToolCatalogID},
	} {
		if err := validateUUID(identifier.field, identifier.value); err != nil {
			return err
		}
	}
	if checkpoint.AttemptGeneration < 1 || checkpoint.AttemptGeneration > maxSafeJSONInteger {
		return errors.New("checkpoint.attempt_generation must be a positive safe integer")
	}
	if err := validateBoundedText("checkpoint.thread_id", checkpoint.ThreadID, 256); err != nil {
		return err
	}
	if err := validateBoundedText("checkpoint.turn_id", checkpoint.TurnID, 256); err != nil {
		return err
	}
	if err := validateRunObjectPointer("checkpoint.object", checkpoint.Object); err != nil {
		return err
	}
	if checkpoint.Object.Size > checkpointartifact.MaximumArtifactBytes ||
		checkpoint.Object.MediaType != checkpointartifact.ArtifactMediaType {
		return errors.New("checkpoint.object does not use the bounded checkpoint artifact v1 profile")
	}
	if checkpoint.CheckpointAllowlistVersion < 1 || checkpoint.CheckpointAllowlistVersion > maxSafeJSONInteger {
		return errors.New("checkpoint.checkpoint_allowlist_version must be a positive safe integer")
	}
	if checkpoint.CreatedAt.IsZero() {
		return errors.New("checkpoint.created_at is required")
	}
	return nil
}

func copyStoredSHA256(destination *[sha256.Size]byte, source []byte) error {
	if len(source) != sha256.Size {
		return fmt.Errorf("stored SHA-256 has %d bytes", len(source))
	}
	copy(destination[:], source)
	return nil
}

func runLaunchInputMatches(prompt ObjectPointer, policy RunExecutorPolicy, llmGateway RunLLMGatewayBinding, larkEgress RunLarkEgressBinding, managedSandbox RunManagedSandboxBinding, command CreateRunCommand) bool {
	return prompt.ObjectID == command.Prompt.ObjectID &&
		subtle.ConstantTimeCompare(prompt.SHA256[:], command.Prompt.SHA256[:]) == 1 &&
		prompt.Size == command.Prompt.Size &&
		prompt.MediaType == command.Prompt.MediaType &&
		policy.Version == command.ExecutorPolicy.Version &&
		subtle.ConstantTimeCompare(policy.ContextDigest[:], command.ExecutorPolicy.ContextDigest[:]) == 1 &&
		slices.Equal(policy.AllowedTools, command.ExecutorPolicy.AllowedTools) &&
		llmGateway == command.LLMGateway && larkEgress == command.LarkEgress && managedSandbox == command.ManagedSandbox
}
