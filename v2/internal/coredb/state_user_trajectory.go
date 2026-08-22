package coredb

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	maxUserTrajectoryRuns           = 32
	maxUserTrajectoryEvents         = 16 * 1024
	maxUserTrajectoryAttempts       = 1024
	maxUserTrajectoryExecutions     = 4096
	maxUserTrajectoryOperations     = 8192
	maxUserTrajectorySandboxes      = 512
	maxUserTrajectoryActivities     = 2048
	maxUserTrajectoryCredentialUses = 4096
	maxUserTrajectoryCheckpoints    = 1024
)

type UserSessionTrajectoryRunPosition struct {
	RunID        string
	RunCreatedAt time.Time
}

type ReadUserSessionTrajectoryQuery struct {
	WorkspaceID string
	SessionID   string
	ActorID     string
	Before      *UserSessionTrajectoryRunPosition
}

type UserSessionTrajectoryEvent struct {
	RunID string
	Event RunEvent
}

type UserSessionTrajectorySandboxActivity struct {
	SandboxID            string
	TargetGeneration     int64
	RunID                string
	RunAttemptID         string
	RunAttemptGeneration int64
	CreatedAt            time.Time
	UpdatedAt            time.Time
	ReleasedAt           *time.Time
}

type UserSessionTrajectoryCredentialUse struct {
	Event              WorkspaceCredentialUseEvent
	BindingDisplayName string
}

type UserSessionTrajectoryCheckpoint struct {
	ID                   string
	RunID                string
	RunAttemptID         string
	RunAttemptGeneration int64
	CreatedAt            time.Time
}

type ReadUserSessionTrajectoryResult struct {
	Session                UserSession
	Runs                   []Run
	PromptPointers         map[string]ObjectPointer
	ManagedSandboxBindings map[string]RunManagedSandboxBinding
	Attempts               []RunAttempt
	Events                 []UserSessionTrajectoryEvent
	Executions             []Execution
	Operations             []ExecutionOperation
	Sandboxes              []ManagedSandbox
	Activities             []UserSessionTrajectorySandboxActivity
	CredentialUses         []UserSessionTrajectoryCredentialUse
	Checkpoints            []UserSessionTrajectoryCheckpoint
	HasOlderRuns           bool
	Truncated              bool
}

// ReadUserSessionTrajectory returns a bounded repeatable-read source for the
// server-side trajectory projector. The same creator/membership predicate as
// transcript reads is rechecked in this transaction. No sealed credential or
// process environment value is selected by any query below.
func (s *StateStore) ReadUserSessionTrajectory(
	ctx context.Context,
	query ReadUserSessionTrajectoryQuery,
) (ReadUserSessionTrajectoryResult, error) {
	const operation = "ReadUserSessionTrajectory"
	if err := validateUserSessionScope(query.WorkspaceID, query.SessionID, query.ActorID); err != nil {
		return ReadUserSessionTrajectoryResult{}, commandError(ErrorInvalidArgument, operation, "session", query.SessionID, err.Error())
	}
	if query.Before != nil {
		if err := validateUUID("before.run_id", query.Before.RunID); err != nil || query.Before.RunCreatedAt.IsZero() {
			if err == nil {
				err = errors.New("before run timestamp is required")
			}
			return ReadUserSessionTrajectoryResult{}, commandError(ErrorInvalidArgument, operation, "session", query.SessionID, err.Error())
		}
	}
	return withStateReadTransaction(ctx, s, operation, func(transaction pgx.Tx) (ReadUserSessionTrajectoryResult, error) {
		session, err := s.readUserSession(ctx, transaction, operation, query.WorkspaceID, query.SessionID, query.ActorID, false)
		if err != nil {
			return ReadUserSessionTrajectoryResult{}, err
		}
		result := ReadUserSessionTrajectoryResult{Session: session}
		result.Runs, result.HasOlderRuns, err = s.readUserTrajectoryRuns(ctx, transaction, query)
		if err != nil || len(result.Runs) == 0 {
			return result, err
		}
		runClause, arguments := trajectoryRunClause(result.Runs)
		if result.PromptPointers, err = s.readUserTrajectoryPromptPointers(ctx, transaction, runClause, arguments); err != nil {
			return ReadUserSessionTrajectoryResult{}, err
		}
		if result.ManagedSandboxBindings, err = s.readUserTrajectoryManagedSandboxBindings(ctx, transaction, runClause, arguments); err != nil {
			return ReadUserSessionTrajectoryResult{}, err
		}
		var truncated bool
		if result.Attempts, truncated, err = s.readUserTrajectoryAttempts(ctx, transaction, runClause, arguments); err != nil {
			return ReadUserSessionTrajectoryResult{}, err
		}
		result.Truncated = result.Truncated || truncated
		if result.Events, truncated, err = s.readUserTrajectoryEvents(ctx, transaction, runClause, arguments); err != nil {
			return ReadUserSessionTrajectoryResult{}, err
		}
		result.Truncated = result.Truncated || truncated
		if result.Executions, truncated, err = s.readUserTrajectoryExecutions(ctx, transaction, runClause, arguments); err != nil {
			return ReadUserSessionTrajectoryResult{}, err
		}
		result.Truncated = result.Truncated || truncated
		if result.Operations, truncated, err = s.readUserTrajectoryOperations(ctx, transaction, runClause, arguments); err != nil {
			return ReadUserSessionTrajectoryResult{}, err
		}
		result.Truncated = result.Truncated || truncated
		if result.Sandboxes, truncated, err = s.readUserTrajectorySandboxes(ctx, transaction, query.WorkspaceID, query.SessionID); err != nil {
			return ReadUserSessionTrajectoryResult{}, err
		}
		result.Truncated = result.Truncated || truncated
		if result.Activities, truncated, err = s.readUserTrajectoryActivities(ctx, transaction, runClause, arguments); err != nil {
			return ReadUserSessionTrajectoryResult{}, err
		}
		result.Truncated = result.Truncated || truncated
		if result.CredentialUses, truncated, err = s.readUserTrajectoryCredentialUses(ctx, transaction, runClause, arguments); err != nil {
			return ReadUserSessionTrajectoryResult{}, err
		}
		result.Truncated = result.Truncated || truncated
		if result.Checkpoints, truncated, err = s.readUserTrajectoryCheckpoints(ctx, transaction, runClause, arguments); err != nil {
			return ReadUserSessionTrajectoryResult{}, err
		}
		result.Truncated = result.Truncated || truncated
		return result, nil
	})
}

func (s *StateStore) readUserTrajectoryManagedSandboxBindings(
	ctx context.Context,
	transaction pgx.Tx,
	runClause string,
	arguments []any,
) (map[string]RunManagedSandboxBinding, error) {
	statement := fmt.Sprintf(`
SELECT launch.run_id::text, launch.managed_sandbox_setting_version,
	   launch.managed_sandbox_region,
	   launch.managed_sandbox_environment_id::text
FROM %s AS launch
WHERE launch.run_id IN (%s)`, s.table("run_launch_states"), runClause)
	rows, err := transaction.Query(ctx, statement, arguments...)
	if err != nil {
		return nil, databaseError("ReadUserSessionTrajectory query managed sandbox bindings", err)
	}
	defer rows.Close()
	result := make(map[string]RunManagedSandboxBinding, len(arguments))
	for rows.Next() {
		var runID string
		var settingVersion *int64
		var region, environmentID *string
		if err := rows.Scan(&runID, &settingVersion, &region, &environmentID); err != nil {
			return nil, databaseError("ReadUserSessionTrajectory scan managed sandbox binding", err)
		}
		if settingVersion == nil && region == nil && environmentID == nil {
			result[runID] = RunManagedSandboxBinding{}
			continue
		}
		if settingVersion == nil || region == nil || environmentID == nil {
			return nil, databaseError("ReadUserSessionTrajectory decode managed sandbox binding", errors.New("stored binding is incomplete"))
		}
		binding := RunManagedSandboxBinding{
			SettingVersion: *settingVersion,
			Region:         *region,
			EnvironmentID:  *environmentID,
		}
		if err := validateRunManagedSandboxBinding(binding); err != nil {
			return nil, databaseError("ReadUserSessionTrajectory validate managed sandbox binding", err)
		}
		result[runID] = binding
	}
	if err := rows.Err(); err != nil {
		return nil, databaseError("ReadUserSessionTrajectory iterate managed sandbox bindings", err)
	}
	if len(result) != len(arguments) {
		return nil, databaseError("ReadUserSessionTrajectory validate managed sandbox bindings", errors.New("selected run has no immutable launch authority"))
	}
	return result, nil
}

func (s *StateStore) readUserTrajectoryPromptPointers(
	ctx context.Context,
	transaction pgx.Tx,
	runClause string,
	arguments []any,
) (map[string]ObjectPointer, error) {
	statement := fmt.Sprintf(`
SELECT launch.run_id::text, launch.prompt_object_id::text,
       launch.prompt_sha256, launch.prompt_size, launch.prompt_media_type
FROM %s AS launch
WHERE launch.run_id IN (%s)`, s.table("run_launch_states"), runClause)
	rows, err := transaction.Query(ctx, statement, arguments...)
	if err != nil {
		return nil, databaseError("ReadUserSessionTrajectory query prompt pointers", err)
	}
	defer rows.Close()
	result := make(map[string]ObjectPointer, len(arguments))
	for rows.Next() {
		var runID string
		var pointer ObjectPointer
		var digest []byte
		if err := rows.Scan(
			&runID, &pointer.ObjectID, &digest, &pointer.Size, &pointer.MediaType,
		); err != nil {
			return nil, databaseError("ReadUserSessionTrajectory scan prompt pointer", err)
		}
		if err := copyStoredSHA256(&pointer.SHA256, digest); err != nil {
			return nil, databaseError("ReadUserSessionTrajectory decode prompt digest", err)
		}
		if err := validateRunObjectPointer("prompt", pointer); err != nil || pointer.MediaType != userPromptTranscriptMediaType {
			if err == nil {
				err = errors.New("stored prompt media type is unsupported")
			}
			return nil, databaseError("ReadUserSessionTrajectory validate prompt authority", err)
		}
		result[runID] = pointer
	}
	if err := rows.Err(); err != nil {
		return nil, databaseError("ReadUserSessionTrajectory iterate prompt pointers", err)
	}
	if len(result) != len(arguments) {
		return nil, databaseError("ReadUserSessionTrajectory validate prompt pointers", errors.New("selected run has no immutable prompt authority"))
	}
	return result, nil
}

func (s *StateStore) readUserTrajectoryRuns(
	ctx context.Context,
	transaction pgx.Tx,
	query ReadUserSessionTrajectoryQuery,
) ([]Run, bool, error) {
	predicate := ""
	arguments := []any{query.WorkspaceID, query.SessionID, query.ActorID}
	if query.Before != nil {
		predicate = " AND (run.created_at, run.id) <= ($4, $5::uuid)"
		arguments = append(arguments, query.Before.RunCreatedAt.UTC(), query.Before.RunID)
	}
	statement := fmt.Sprintf(`
SELECT %s
FROM %s AS run
WHERE run.workspace_id = $1
  AND run.session_id = $2
  AND run.actor_id = $3%s
ORDER BY run.created_at DESC, run.id DESC
LIMIT %d`, runColumns("run"), s.table("runs"), predicate, maxUserTrajectoryRuns+1)
	rows, err := transaction.Query(ctx, statement, arguments...)
	if err != nil {
		return nil, false, databaseError("ReadUserSessionTrajectory query runs", err)
	}
	defer rows.Close()
	runs := make([]Run, 0, maxUserTrajectoryRuns+1)
	for rows.Next() {
		run, scanErr := scanRun(rows)
		if scanErr != nil {
			return nil, false, databaseError("ReadUserSessionTrajectory scan run", scanErr)
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, false, databaseError("ReadUserSessionTrajectory iterate runs", err)
	}
	hasOlder := len(runs) > maxUserTrajectoryRuns
	if hasOlder {
		runs = runs[:maxUserTrajectoryRuns]
	}
	return runs, hasOlder, nil
}

func trajectoryRunClause(runs []Run) (string, []any) {
	placeholders := make([]string, len(runs))
	arguments := make([]any, len(runs))
	for index, run := range runs {
		placeholders[index] = fmt.Sprintf("$%d", index+1)
		arguments[index] = run.ID
	}
	return strings.Join(placeholders, ", "), arguments
}

func (s *StateStore) readUserTrajectoryAttempts(ctx context.Context, transaction pgx.Tx, runClause string, arguments []any) ([]RunAttempt, bool, error) {
	statement := fmt.Sprintf(`
SELECT %s
FROM %s AS attempt
JOIN %s AS run ON run.id = attempt.run_id
WHERE attempt.run_id IN (%s)
ORDER BY run.created_at DESC, run.id DESC, attempt.generation DESC
LIMIT %d`, attemptColumns("attempt"), s.table("run_attempts"), s.table("runs"), runClause, maxUserTrajectoryAttempts+1)
	rows, err := transaction.Query(ctx, statement, arguments...)
	if err != nil {
		return nil, false, databaseError("ReadUserSessionTrajectory query attempts", err)
	}
	defer rows.Close()
	result := make([]RunAttempt, 0)
	for rows.Next() {
		value, scanErr := scanAttempt(rows)
		if scanErr != nil {
			return nil, false, databaseError("ReadUserSessionTrajectory scan attempt", scanErr)
		}
		result = append(result, value)
	}
	if err := rows.Err(); err != nil {
		return nil, false, databaseError("ReadUserSessionTrajectory iterate attempts", err)
	}
	truncated := len(result) > maxUserTrajectoryAttempts
	if truncated {
		result = result[:maxUserTrajectoryAttempts]
	}
	return result, truncated, nil
}

func (s *StateStore) readUserTrajectoryEvents(ctx context.Context, transaction pgx.Tx, runClause string, arguments []any) ([]UserSessionTrajectoryEvent, bool, error) {
	statement := fmt.Sprintf(`
SELECT event.run_id::text,
       event.event_id::text, event.seq,
       event.run_attempt_id::text, event.run_attempt_generation,
       event.producer_instance_id::text, event.producer_seq,
       event.source, event.kind, event.schema_version,
       event.payload, event.object_id::text, event.object_sha256,
       event.object_size, event.object_media_type, event.created_at
FROM %s AS event
JOIN %s AS run ON run.id = event.run_id
WHERE event.run_id IN (%s)
ORDER BY run.created_at DESC, run.id DESC, event.seq DESC
LIMIT %d`, s.table("run_events"), s.table("runs"), runClause, maxUserTrajectoryEvents+1)
	rows, err := transaction.Query(ctx, statement, arguments...)
	if err != nil {
		return nil, false, databaseError("ReadUserSessionTrajectory query events", err)
	}
	defer rows.Close()
	result := make([]UserSessionTrajectoryEvent, 0)
	for rows.Next() {
		value, scanErr := scanUserSessionTranscriptEvent(rows)
		if scanErr != nil {
			return nil, false, databaseError("ReadUserSessionTrajectory scan event", scanErr)
		}
		result = append(result, UserSessionTrajectoryEvent(value))
	}
	if err := rows.Err(); err != nil {
		return nil, false, databaseError("ReadUserSessionTrajectory iterate events", err)
	}
	truncated := len(result) > maxUserTrajectoryEvents
	if truncated {
		result = result[:maxUserTrajectoryEvents]
	}
	reverseTrajectoryEvents(result)
	return result, truncated, nil
}

func (s *StateStore) readUserTrajectoryExecutions(ctx context.Context, transaction pgx.Tx, runClause string, arguments []any) ([]Execution, bool, error) {
	statement := fmt.Sprintf(`
SELECT %s
FROM %s AS execution
JOIN %s AS run ON run.id = execution.run_id
WHERE execution.run_id IN (%s)
ORDER BY run.created_at DESC, run.id DESC, execution.created_at DESC, execution.id DESC
LIMIT %d`, executionColumns("execution"), s.table("executions"), s.table("runs"), runClause, maxUserTrajectoryExecutions+1)
	rows, err := transaction.Query(ctx, statement, arguments...)
	if err != nil {
		return nil, false, databaseError("ReadUserSessionTrajectory query executions", err)
	}
	defer rows.Close()
	result := make([]Execution, 0)
	for rows.Next() {
		value, scanErr := scanExecution(rows)
		if scanErr != nil {
			return nil, false, databaseError("ReadUserSessionTrajectory scan execution", scanErr)
		}
		result = append(result, value)
	}
	if err := rows.Err(); err != nil {
		return nil, false, databaseError("ReadUserSessionTrajectory iterate executions", err)
	}
	truncated := len(result) > maxUserTrajectoryExecutions
	if truncated {
		result = result[:maxUserTrajectoryExecutions]
	}
	return result, truncated, nil
}

func (s *StateStore) readUserTrajectoryOperations(ctx context.Context, transaction pgx.Tx, runClause string, arguments []any) ([]ExecutionOperation, bool, error) {
	statement := fmt.Sprintf(`
SELECT %s
FROM %s AS operation
JOIN %s AS execution ON execution.id = operation.execution_id
JOIN %s AS run ON run.id = execution.run_id
WHERE execution.run_id IN (%s)
ORDER BY run.created_at DESC, run.id DESC, operation.created_at DESC, operation.id DESC
LIMIT %d`, executionOperationColumns("operation"), s.table("execution_operations"), s.table("executions"), s.table("runs"), runClause, maxUserTrajectoryOperations+1)
	rows, err := transaction.Query(ctx, statement, arguments...)
	if err != nil {
		return nil, false, databaseError("ReadUserSessionTrajectory query operations", err)
	}
	defer rows.Close()
	result := make([]ExecutionOperation, 0)
	for rows.Next() {
		value, scanErr := scanExecutionOperation(rows)
		if scanErr != nil {
			return nil, false, databaseError("ReadUserSessionTrajectory scan operation", scanErr)
		}
		result = append(result, value)
	}
	if err := rows.Err(); err != nil {
		return nil, false, databaseError("ReadUserSessionTrajectory iterate operations", err)
	}
	truncated := len(result) > maxUserTrajectoryOperations
	if truncated {
		result = result[:maxUserTrajectoryOperations]
	}
	return result, truncated, nil
}

func (s *StateStore) readUserTrajectorySandboxes(ctx context.Context, transaction pgx.Tx, workspaceID, sessionID string) ([]ManagedSandbox, bool, error) {
	statement := fmt.Sprintf(`
SELECT %s
FROM %s AS sandbox
WHERE sandbox.workspace_id = $1 AND sandbox.session_id = $2
ORDER BY sandbox.created_at DESC, sandbox.id DESC
LIMIT %d`, managedSandboxColumns("sandbox"), s.table("managed_sandboxes"), maxUserTrajectorySandboxes+1)
	rows, err := transaction.Query(ctx, statement, workspaceID, sessionID)
	if err != nil {
		return nil, false, databaseError("ReadUserSessionTrajectory query sandboxes", err)
	}
	defer rows.Close()
	result := make([]ManagedSandbox, 0)
	for rows.Next() {
		value, scanErr := scanManagedSandbox(rows)
		if scanErr != nil {
			return nil, false, databaseError("ReadUserSessionTrajectory scan sandbox", scanErr)
		}
		result = append(result, value)
	}
	if err := rows.Err(); err != nil {
		return nil, false, databaseError("ReadUserSessionTrajectory iterate sandboxes", err)
	}
	truncated := len(result) > maxUserTrajectorySandboxes
	if truncated {
		result = result[:maxUserTrajectorySandboxes]
	}
	return result, truncated, nil
}

func (s *StateStore) readUserTrajectoryActivities(ctx context.Context, transaction pgx.Tx, runClause string, arguments []any) ([]UserSessionTrajectorySandboxActivity, bool, error) {
	statement := fmt.Sprintf(`
SELECT activity.sandbox_id::text, activity.target_generation,
       attempt.run_id::text, activity.run_attempt_id::text,
       activity.run_attempt_generation, activity.created_at,
       activity.updated_at, activity.released_at
FROM %s AS activity
JOIN %s AS attempt
  ON attempt.id = activity.run_attempt_id
 AND attempt.generation = activity.run_attempt_generation
JOIN %s AS run ON run.id = attempt.run_id
WHERE attempt.run_id IN (%s)
ORDER BY run.created_at DESC, run.id DESC, activity.created_at DESC, activity.sandbox_id DESC
LIMIT %d`, s.table("managed_sandbox_activities"), s.table("run_attempts"), s.table("runs"), runClause, maxUserTrajectoryActivities+1)
	rows, err := transaction.Query(ctx, statement, arguments...)
	if err != nil {
		return nil, false, databaseError("ReadUserSessionTrajectory query sandbox activities", err)
	}
	defer rows.Close()
	result := make([]UserSessionTrajectorySandboxActivity, 0)
	for rows.Next() {
		var value UserSessionTrajectorySandboxActivity
		if err := rows.Scan(
			&value.SandboxID, &value.TargetGeneration, &value.RunID,
			&value.RunAttemptID, &value.RunAttemptGeneration,
			&value.CreatedAt, &value.UpdatedAt, &value.ReleasedAt,
		); err != nil {
			return nil, false, databaseError("ReadUserSessionTrajectory scan sandbox activity", err)
		}
		result = append(result, value)
	}
	if err := rows.Err(); err != nil {
		return nil, false, databaseError("ReadUserSessionTrajectory iterate sandbox activities", err)
	}
	truncated := len(result) > maxUserTrajectoryActivities
	if truncated {
		result = result[:maxUserTrajectoryActivities]
	}
	return result, truncated, nil
}

func (s *StateStore) readUserTrajectoryCredentialUses(ctx context.Context, transaction pgx.Tx, runClause string, arguments []any) ([]UserSessionTrajectoryCredentialUse, bool, error) {
	statement := fmt.Sprintf(`
SELECT credential_use.event_id::text, credential_use.created_at, credential_use.stage,
       COALESCE(credential_use.capability_id, ''), COALESCE(credential_use.workspace_id::text, ''),
       COALESCE(credential_use.session_id::text, ''), COALESCE(credential_use.actor_id::text, ''),
       COALESCE(credential_use.environment_id::text, ''), COALESCE(credential_use.run_id::text, ''),
       COALESCE(credential_use.run_attempt_id::text, ''), COALESCE(credential_use.run_attempt_generation, 0),
       COALESCE(credential_use.execution_id::text, ''), COALESCE(credential_use.operation_id::text, ''),
       COALESCE(credential_use.sandbox_id::text, ''), COALESCE(credential_use.target_generation, 0),
       COALESCE(credential_use.provider_kind, ''), COALESCE(credential_use.binding_id::text, ''),
       COALESCE(credential_use.authority_version, 0), COALESCE(credential_use.credential_version, 0),
       COALESCE(credential_use.tae_psm, ''), COALESCE(credential_use.request_host, ''),
       COALESCE(credential_use.request_path, ''), COALESCE(credential_use.request_method, ''),
       credential_use.decision, credential_use.reason_code, COALESCE(binding.display_name, '')
FROM %s AS credential_use
JOIN %s AS run ON run.id = credential_use.run_id
LEFT JOIN %s AS binding ON binding.id = credential_use.binding_id
WHERE credential_use.run_id IN (%s)
ORDER BY run.created_at DESC, run.id DESC, credential_use.created_at DESC, credential_use.event_id DESC
LIMIT %d`, s.table("workspace_credential_use_events"), s.table("runs"), s.table("workspace_credential_bindings"), runClause, maxUserTrajectoryCredentialUses+1)
	rows, err := transaction.Query(ctx, statement, arguments...)
	if err != nil {
		return nil, false, databaseError("ReadUserSessionTrajectory query credential uses", err)
	}
	defer rows.Close()
	result := make([]UserSessionTrajectoryCredentialUse, 0)
	for rows.Next() {
		var value UserSessionTrajectoryCredentialUse
		event := &value.Event
		if err := rows.Scan(
			&event.EventID, &event.At, &event.Stage, &event.CapabilityID,
			&event.WorkspaceID, &event.SessionID, &event.ActorID, &event.EnvironmentID,
			&event.RunID, &event.RunAttemptID, &event.RunAttemptGeneration,
			&event.ExecutionID, &event.OperationID, &event.SandboxID,
			&event.TargetGeneration, &event.ProviderKind, &event.BindingID,
			&event.AuthorityVersion, &event.CredentialVersion, &event.TAEPSM,
			&event.Host, &event.Path, &event.Method, &event.Decision,
			&event.ReasonCode, &value.BindingDisplayName,
		); err != nil {
			return nil, false, databaseError("ReadUserSessionTrajectory scan credential use", err)
		}
		result = append(result, value)
	}
	if err := rows.Err(); err != nil {
		return nil, false, databaseError("ReadUserSessionTrajectory iterate credential uses", err)
	}
	truncated := len(result) > maxUserTrajectoryCredentialUses
	if truncated {
		result = result[:maxUserTrajectoryCredentialUses]
	}
	return result, truncated, nil
}

func (s *StateStore) readUserTrajectoryCheckpoints(ctx context.Context, transaction pgx.Tx, runClause string, arguments []any) ([]UserSessionTrajectoryCheckpoint, bool, error) {
	statement := fmt.Sprintf(`
SELECT checkpoint.id::text, checkpoint.run_id::text,
       checkpoint.run_attempt_id::text, checkpoint.attempt_generation,
       checkpoint.created_at
FROM %s AS checkpoint
JOIN %s AS run ON run.id = checkpoint.run_id
WHERE checkpoint.run_id IN (%s)
ORDER BY run.created_at DESC, run.id DESC, checkpoint.created_at DESC, checkpoint.id DESC
LIMIT %d`, s.table("checkpoints"), s.table("runs"), runClause, maxUserTrajectoryCheckpoints+1)
	rows, err := transaction.Query(ctx, statement, arguments...)
	if err != nil {
		return nil, false, databaseError("ReadUserSessionTrajectory query checkpoints", err)
	}
	defer rows.Close()
	result := make([]UserSessionTrajectoryCheckpoint, 0)
	for rows.Next() {
		var value UserSessionTrajectoryCheckpoint
		if err := rows.Scan(&value.ID, &value.RunID, &value.RunAttemptID, &value.RunAttemptGeneration, &value.CreatedAt); err != nil {
			return nil, false, databaseError("ReadUserSessionTrajectory scan checkpoint", err)
		}
		result = append(result, value)
	}
	if err := rows.Err(); err != nil {
		return nil, false, databaseError("ReadUserSessionTrajectory iterate checkpoints", err)
	}
	truncated := len(result) > maxUserTrajectoryCheckpoints
	if truncated {
		result = result[:maxUserTrajectoryCheckpoints]
	}
	return result, truncated, nil
}

func reverseTrajectoryEvents(values []UserSessionTrajectoryEvent) {
	for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
		values[left], values[right] = values[right], values[left]
	}
}
