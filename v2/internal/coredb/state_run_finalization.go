package coredb

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	checkpointartifact "github.com/agentserver/agentserver/v2/internal/checkpoint"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// BeginRunFinalization freezes the completed stock turn identity before any
// checkpoint bytes are opened or uploaded. The caller must already have
// stopped the complete attempt process group; PostgreSQL deliberately does
// not try to infer that host-local process fact.
func (s *StateStore) BeginRunFinalization(ctx context.Context, command BeginRunFinalizationCommand) (BeginRunFinalizationResult, error) {
	const operation = "BeginRunFinalization"
	if err := validateBeginRunFinalization(command); err != nil {
		return BeginRunFinalizationResult{}, commandError(ErrorInvalidArgument, operation, "attempt", command.AttemptID, err.Error())
	}

	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (BeginRunFinalizationResult, error) {
		run, attempt, err := s.lockRunAttempt(ctx, transaction, operation, command.RunID, command.AttemptID)
		if err != nil {
			return BeginRunFinalizationResult{}, err
		}
		if err := requireFinalizationAuthority(operation, run, attempt, command.HolderID, command.Generation); err != nil {
			return BeginRunFinalizationResult{}, err
		}

		if attempt.TerminalThreadID != "" || attempt.TerminalTurnID != "" {
			if attempt.TerminalThreadID != command.ThreadID || attempt.TerminalTurnID != command.TurnID {
				return BeginRunFinalizationResult{}, commandError(ErrorIdempotencyConflict, operation, "attempt", attempt.ID, "attempt is already bound to a different terminal thread or turn")
			}
			if (run.Status == RunStatusFinalizing && attempt.Status == AttemptStatusFinalizing) ||
				(run.Status == RunStatusCompleted && attempt.Status == AttemptStatusSucceeded) {
				return BeginRunFinalizationResult{Run: run, Attempt: attempt, Changed: false}, nil
			}
			return BeginRunFinalizationResult{}, commandError(ErrorInvalidState, operation, "attempt", attempt.ID, "terminal identity is present outside a finalizing or succeeded attempt")
		}

		if run.Version != command.ExpectedRunVersion {
			return BeginRunFinalizationResult{}, versionConflict(operation, "run", run.ID, run.Version)
		}
		if attempt.Version != command.ExpectedAttemptVersion {
			return BeginRunFinalizationResult{}, versionConflict(operation, "attempt", attempt.ID, attempt.Version)
		}
		if run.Status != RunStatusRunning || attempt.Status != AttemptStatusRunning || attempt.TurnStartedAt == nil {
			return BeginRunFinalizationResult{}, commandError(ErrorInvalidState, operation, "attempt", attempt.ID, "only a running accepted turn can begin finalization")
		}
		if err := s.requireActiveFinalizationContext(ctx, transaction, operation, run, attempt, command.HolderID, command.Generation); err != nil {
			return BeginRunFinalizationResult{}, err
		}
		if err := s.requireTerminalCatalogThread(ctx, transaction, operation, run, attempt, command.ThreadID); err != nil {
			return BeginRunFinalizationResult{}, err
		}
		if err := s.requireTerminalExecutions(ctx, transaction, operation, attempt.ID); err != nil {
			return BeginRunFinalizationResult{}, err
		}

		updateAttempt := fmt.Sprintf(`
UPDATE %s
SET status = $1,
    terminal_thread_id = $2,
    terminal_turn_id = $3,
    version = version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $4 AND version = $5
RETURNING %s`, s.table("run_attempts"), attemptColumns(""))
		updatedAttempt, err := scanAttempt(transaction.QueryRow(ctx, updateAttempt,
			AttemptStatusFinalizing,
			command.ThreadID,
			command.TurnID,
			attempt.ID,
			attempt.Version,
		))
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return BeginRunFinalizationResult{}, versionConflict(operation, "attempt", attempt.ID, attempt.Version)
			}
			return BeginRunFinalizationResult{}, databaseError(operation+" update attempt", err)
		}

		updateRun := fmt.Sprintf(`
UPDATE %s
SET status = $1,
    next_event_seq = next_event_seq + 1,
    version = version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $2 AND version = $3
RETURNING %s`, s.table("runs"), runColumns(""))
		updatedRun, err := scanRun(transaction.QueryRow(ctx, updateRun, RunStatusFinalizing, run.ID, run.Version))
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return BeginRunFinalizationResult{}, versionConflict(operation, "run", run.ID, run.Version)
			}
			return BeginRunFinalizationResult{}, databaseError(operation+" update run", err)
		}

		payload, err := marshalTransitionPayload(struct {
			RunID             string `json:"runId"`
			RunAttemptID      string `json:"runAttemptId"`
			AttemptGeneration int64  `json:"runAttemptGeneration"`
			ThreadID          string `json:"threadId"`
			TurnID            string `json:"turnId"`
		}{run.ID, attempt.ID, attempt.Generation, command.ThreadID, command.TurnID})
		if err != nil {
			return BeginRunFinalizationResult{}, commandError(ErrorInvalidArgument, operation, "attempt", attempt.ID, err.Error())
		}
		if err := s.insertTransitionEvent(ctx, transaction, run.ID, run.NextEventSeq, &attempt.ID, &attempt.Generation, command.Record, EventSourceSystem, "run.finalizing", payload); err != nil {
			return BeginRunFinalizationResult{}, err
		}
		if err := s.insertOutbox(ctx, transaction, command.Record.OutboxID, "run.finalizing", run.ID, payload); err != nil {
			return BeginRunFinalizationResult{}, err
		}
		return BeginRunFinalizationResult{Run: updatedRun, Attempt: updatedAttempt, Changed: true}, nil
	})
}

// CommitCheckpointAndTerminalRun makes the uploaded checkpoint reachable in
// the same transaction that makes the run terminal. A successful response is
// therefore the only boundary after which attempt runtime cleanup is safe.
func (s *StateStore) CommitCheckpointAndTerminalRun(ctx context.Context, command CommitCheckpointAndTerminalRunCommand) (CommitCheckpointAndTerminalRunResult, error) {
	const operation = "CommitCheckpointAndTerminalRun"
	if err := validateCommitCheckpointAndTerminalRun(command); err != nil {
		return CommitCheckpointAndTerminalRunResult{}, commandError(ErrorInvalidArgument, operation, "attempt", command.AttemptID, err.Error())
	}

	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (CommitCheckpointAndTerminalRunResult, error) {
		run, attempt, err := s.lockRunAttempt(ctx, transaction, operation, command.RunID, command.AttemptID)
		if err != nil {
			return CommitCheckpointAndTerminalRunResult{}, err
		}
		if err := requireFinalizationAuthority(operation, run, attempt, command.HolderID, command.Generation); err != nil {
			return CommitCheckpointAndTerminalRunResult{}, err
		}

		existing, found, err := s.readCommittedCheckpointForRun(ctx, transaction, operation, run.ID)
		if err != nil {
			return CommitCheckpointAndTerminalRunResult{}, err
		}
		if found {
			return s.exactCheckpointCommitRetry(ctx, transaction, operation, run, attempt, existing, command)
		}

		if run.Version != command.ExpectedRunVersion {
			return CommitCheckpointAndTerminalRunResult{}, versionConflict(operation, "run", run.ID, run.Version)
		}
		if attempt.Version != command.ExpectedAttemptVersion {
			return CommitCheckpointAndTerminalRunResult{}, versionConflict(operation, "attempt", attempt.ID, attempt.Version)
		}
		if run.Status != RunStatusFinalizing || attempt.Status != AttemptStatusFinalizing ||
			attempt.TerminalThreadID != command.ThreadID || attempt.TerminalTurnID != command.TurnID {
			return CommitCheckpointAndTerminalRunResult{}, commandError(ErrorInvalidState, operation, "attempt", attempt.ID, "run attempt is not finalizing the requested terminal thread and turn")
		}

		sessionVersion, err := s.lockActiveFinalizationSession(ctx, transaction, operation, run)
		if err != nil {
			return CommitCheckpointAndTerminalRunResult{}, err
		}
		if err := s.requireLiveLeases(ctx, transaction, run, attempt, command.HolderID, command.Generation); err != nil {
			return CommitCheckpointAndTerminalRunResult{}, err
		}
		if err := s.requireTerminalExecutions(ctx, transaction, operation, attempt.ID); err != nil {
			return CommitCheckpointAndTerminalRunResult{}, err
		}
		catalog, err := s.lockCheckpointCatalog(ctx, transaction, operation, command.BrainToolCatalogID)
		if err != nil {
			return CommitCheckpointAndTerminalRunResult{}, err
		}
		if catalog.WorkspaceID != run.WorkspaceID || catalog.SessionID != run.SessionID ||
			catalog.CreatedRunID != run.ID || catalog.CreatedRunAttemptID != attempt.ID ||
			catalog.CreatedAttemptGeneration != attempt.Generation || catalog.CreatedHolderID != attempt.HolderID ||
			catalog.ThreadID != command.ThreadID || catalog.CatalogDigest != command.CatalogDigest {
			return CommitCheckpointAndTerminalRunResult{}, commandError(ErrorConflict, operation, "brain_tool_catalog", catalog.ID, "catalog does not match the finalizing attempt, thread, and digest")
		}

		checkpoint, err := s.insertCommittedCheckpoint(ctx, transaction, operation, run, attempt, catalog, command)
		if err != nil {
			return CommitCheckpointAndTerminalRunResult{}, err
		}

		updateAttempt := fmt.Sprintf(`
UPDATE %s
SET status = $1,
    version = version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $2 AND version = $3
RETURNING %s`, s.table("run_attempts"), attemptColumns(""))
		updatedAttempt, err := scanAttempt(transaction.QueryRow(ctx, updateAttempt, AttemptStatusSucceeded, attempt.ID, attempt.Version))
		if err != nil {
			return CommitCheckpointAndTerminalRunResult{}, databaseError(operation+" update attempt", err)
		}

		updateRun := fmt.Sprintf(`
UPDATE %s
SET status = $1,
    next_event_seq = next_event_seq + 1,
    version = version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $2 AND version = $3
RETURNING %s`, s.table("runs"), runColumns(""))
		updatedRun, err := scanRun(transaction.QueryRow(ctx, updateRun, RunStatusCompleted, run.ID, run.Version))
		if err != nil {
			return CommitCheckpointAndTerminalRunResult{}, databaseError(operation+" update run", err)
		}

		payload, err := marshalTransitionPayload(struct{}{})
		if err != nil {
			return CommitCheckpointAndTerminalRunResult{}, commandError(ErrorInvalidArgument, operation, "run", run.ID, err.Error())
		}
		if err := s.insertTransitionEvent(ctx, transaction, run.ID, run.NextEventSeq, &attempt.ID, &attempt.Generation, command.Record, EventSourceSystem, "run.completed", payload); err != nil {
			return CommitCheckpointAndTerminalRunResult{}, err
		}
		if err := s.insertOutbox(ctx, transaction, command.Record.OutboxID, "run.completed", run.ID, payload); err != nil {
			return CommitCheckpointAndTerminalRunResult{}, err
		}

		updateSession := fmt.Sprintf(`
UPDATE %s
SET active_run_id = NULL,
    latest_checkpoint_id = $1,
    version = version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $2 AND active_run_id = $3 AND version = $4
RETURNING version`, s.table("sessions"))
		var updatedSessionVersion int64
		if err := transaction.QueryRow(ctx, updateSession, checkpoint.ID, run.SessionID, run.ID, sessionVersion).Scan(&updatedSessionVersion); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return CommitCheckpointAndTerminalRunResult{}, commandError(ErrorVersionConflict, operation, "session", run.SessionID, "session changed while committing checkpoint")
			}
			return CommitCheckpointAndTerminalRunResult{}, databaseError(operation+" update session", err)
		}

		if err := s.deleteFinalizedLeases(ctx, transaction, operation, run, attempt, command.HolderID, command.Generation); err != nil {
			return CommitCheckpointAndTerminalRunResult{}, err
		}
		return CommitCheckpointAndTerminalRunResult{
			Run: updatedRun, Attempt: updatedAttempt, Checkpoint: checkpoint,
			SessionVersion: updatedSessionVersion, Created: true,
		}, nil
	})
}

func validateBeginRunFinalization(command BeginRunFinalizationCommand) error {
	if err := validateFinalizationIdentity(command.RunID, command.AttemptID, command.HolderID, command.Generation, command.ExpectedRunVersion, command.ExpectedAttemptVersion, command.ThreadID, command.TurnID); err != nil {
		return err
	}
	return validateTransitionRecord(command.Record)
}

func validateCommitCheckpointAndTerminalRun(command CommitCheckpointAndTerminalRunCommand) error {
	if err := validateFinalizationIdentity(command.RunID, command.AttemptID, command.HolderID, command.Generation, command.ExpectedRunVersion, command.ExpectedAttemptVersion, command.ThreadID, command.TurnID); err != nil {
		return err
	}
	if err := validateUUID("checkpoint_id", command.CheckpointID); err != nil {
		return err
	}
	if err := validateUUID("brain_tool_catalog_id", command.BrainToolCatalogID); err != nil {
		return err
	}
	if err := validateRunObjectPointer("checkpoint.object", command.Object); err != nil {
		return err
	}
	if command.Object.Size > checkpointartifact.MaximumArtifactBytes || command.Object.MediaType != checkpointartifact.ArtifactMediaType {
		return errors.New("checkpoint.object does not use the bounded checkpoint artifact v1 profile")
	}
	if command.CheckpointAllowlistVersion < 1 || command.CheckpointAllowlistVersion > maxSafeJSONInteger {
		return errors.New("checkpoint_allowlist_version must be a positive safe integer")
	}
	return validateTransitionRecord(command.Record)
}

func validateFinalizationIdentity(runID, attemptID, holderID string, generation, expectedRunVersion, expectedAttemptVersion int64, threadID, turnID string) error {
	if err := validateUUID("run_id", runID); err != nil {
		return err
	}
	if err := validateUUID("attempt_id", attemptID); err != nil {
		return err
	}
	if err := validateBoundedText("holder_id", holderID, 256); err != nil {
		return err
	}
	if generation < 1 || generation > maxSafeJSONInteger ||
		expectedRunVersion < 1 || expectedRunVersion > maxSafeJSONInteger ||
		expectedAttemptVersion < 1 || expectedAttemptVersion > maxSafeJSONInteger {
		return errors.New("generation and expected versions must be positive safe integers")
	}
	if err := validateBoundedText("thread_id", threadID, 256); err != nil {
		return err
	}
	return validateBoundedText("turn_id", turnID, 256)
}

func requireFinalizationAuthority(operation string, run Run, attempt RunAttempt, holderID string, generation int64) error {
	if run.CurrentAttemptGeneration != generation || attempt.Generation != generation {
		return fencedAttemptError(operation, attempt.ID, run.CurrentAttemptGeneration, "attempt generation was fenced")
	}
	if attempt.HolderID != holderID {
		return fencedAttemptError(operation, attempt.ID, run.CurrentAttemptGeneration, "attempt holder was fenced")
	}
	return nil
}

func (s *StateStore) requireActiveFinalizationContext(ctx context.Context, transaction pgx.Tx, operation string, run Run, attempt RunAttempt, holderID string, generation int64) error {
	if _, err := s.lockActiveFinalizationSession(ctx, transaction, operation, run); err != nil {
		return err
	}
	return s.requireLiveLeases(ctx, transaction, run, attempt, holderID, generation)
}

func (s *StateStore) lockActiveFinalizationSession(ctx context.Context, transaction pgx.Tx, operation string, run Run) (int64, error) {
	query := fmt.Sprintf(`
SELECT active_run_id::text, version
FROM %s
WHERE id = $1
FOR UPDATE`, s.table("sessions"))
	var activeRunID *string
	var version int64
	if err := transaction.QueryRow(ctx, query, run.SessionID).Scan(&activeRunID, &version); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, commandError(ErrorNotFound, operation, "session", run.SessionID, "session does not exist")
		}
		return 0, databaseError(operation+" lock session", err)
	}
	if activeRunID == nil || *activeRunID != run.ID {
		return 0, commandError(ErrorInvalidState, operation, "run", run.ID, "run is not the session active run")
	}
	return version, nil
}

func (s *StateStore) requireTerminalCatalogThread(ctx context.Context, transaction pgx.Tx, operation string, run Run, attempt RunAttempt, threadID string) error {
	query := fmt.Sprintf(`
SELECT workspace_id::text, session_id::text, created_run_id::text,
       created_attempt_generation, created_holder_id, thread_id
FROM %s
WHERE created_run_attempt_id = $1
FOR SHARE`, s.table("brain_tool_catalogs"))
	var workspaceID, sessionID, createdRunID, createdHolderID string
	var generation int64
	var catalogThreadID *string
	if err := transaction.QueryRow(ctx, query, attempt.ID).Scan(
		&workspaceID, &sessionID, &createdRunID, &generation, &createdHolderID, &catalogThreadID,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return commandError(ErrorNotFound, operation, "brain_tool_catalog", attempt.ID, "attempt has no frozen brain tool catalog")
		}
		return databaseError(operation+" lock terminal catalog", err)
	}
	if workspaceID != run.WorkspaceID || sessionID != run.SessionID || createdRunID != run.ID ||
		generation != attempt.Generation || createdHolderID != attempt.HolderID || catalogThreadID == nil || *catalogThreadID != threadID {
		return commandError(ErrorConflict, operation, "brain_tool_catalog", attempt.ID, "frozen catalog is not bound to the terminal attempt thread")
	}
	return nil
}

func (s *StateStore) requireTerminalExecutions(ctx context.Context, transaction pgx.Tx, operation, attemptID string) error {
	query := fmt.Sprintf(`
SELECT pg_catalog.count(*)
FROM %s
WHERE run_attempt_id = $1 AND terminal_at IS NULL`, s.table("executions"))
	var live int64
	if err := transaction.QueryRow(ctx, query, attemptID).Scan(&live); err != nil {
		return databaseError(operation+" inspect attempt executions", err)
	}
	if live != 0 {
		return commandError(ErrorInvalidState, operation, "attempt", attemptID, "attempt still has non-terminal executions")
	}
	return nil
}

func (s *StateStore) lockCheckpointCatalog(ctx context.Context, transaction pgx.Tx, operation, catalogID string) (BrainToolCatalog, error) {
	query := fmt.Sprintf(`
SELECT %s
FROM %s AS c
WHERE c.id = $1
FOR SHARE`, brainToolCatalogColumns("c"), s.table("brain_tool_catalogs"))
	catalog, err := scanBrainToolCatalog(transaction.QueryRow(ctx, query, catalogID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return BrainToolCatalog{}, commandError(ErrorNotFound, operation, "brain_tool_catalog", catalogID, "catalog does not exist")
		}
		return BrainToolCatalog{}, databaseError(operation+" lock checkpoint catalog", err)
	}
	return catalog, nil
}

func (s *StateStore) insertCommittedCheckpoint(ctx context.Context, transaction pgx.Tx, operation string, run Run, attempt RunAttempt, catalog BrainToolCatalog, command CommitCheckpointAndTerminalRunCommand) (Checkpoint, error) {
	query := fmt.Sprintf(`
INSERT INTO %s
    (id, workspace_id, session_id, run_id, run_attempt_id, attempt_generation,
     brain_tool_catalog_id, thread_id, turn_id, manifest_digest,
     object_id, object_sha256, object_size, object_media_type,
     codex_runtime_manifest_digest, checkpoint_allowlist_version)
VALUES
    ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
     $11, $12, $13, $14, $15, $16)
RETURNING created_at`, s.table("checkpoints"))
	checkpoint := Checkpoint{
		ID: command.CheckpointID, WorkspaceID: run.WorkspaceID, SessionID: run.SessionID,
		RunID: run.ID, AttemptID: attempt.ID, AttemptGeneration: attempt.Generation,
		BrainToolCatalogID: catalog.ID, ThreadID: command.ThreadID, TurnID: command.TurnID,
		ManifestDigest: command.ManifestDigest, CatalogDigest: catalog.CatalogDigest, Object: command.Object,
		CodexRuntimeManifestDigest: command.CodexRuntimeManifestDigest,
		CheckpointAllowlistVersion: command.CheckpointAllowlistVersion,
	}
	if err := transaction.QueryRow(ctx, query,
		checkpoint.ID,
		checkpoint.WorkspaceID,
		checkpoint.SessionID,
		checkpoint.RunID,
		checkpoint.AttemptID,
		checkpoint.AttemptGeneration,
		checkpoint.BrainToolCatalogID,
		checkpoint.ThreadID,
		checkpoint.TurnID,
		checkpoint.ManifestDigest[:],
		checkpoint.Object.ObjectID,
		checkpoint.Object.SHA256[:],
		checkpoint.Object.Size,
		checkpoint.Object.MediaType,
		checkpoint.CodexRuntimeManifestDigest[:],
		checkpoint.CheckpointAllowlistVersion,
	).Scan(&checkpoint.CreatedAt); err != nil {
		var postgresError *pgconn.PgError
		if pgxErrorAs(err, &postgresError) && postgresError.Code == "23505" {
			return Checkpoint{}, commandError(ErrorConflict, operation, "checkpoint", checkpoint.ID, "checkpoint identity or run is already in use")
		}
		return Checkpoint{}, databaseError(operation+" insert checkpoint", err)
	}
	if err := validateStoredCheckpoint(checkpoint); err != nil {
		return Checkpoint{}, databaseError(operation+" validate inserted checkpoint", err)
	}
	return checkpoint, nil
}

func (s *StateStore) readCommittedCheckpointForRun(ctx context.Context, transaction pgx.Tx, operation, runID string) (Checkpoint, bool, error) {
	query := fmt.Sprintf(`
SELECT c.id::text, c.workspace_id::text, c.session_id::text,
       c.run_id::text, c.run_attempt_id::text, c.attempt_generation,
       c.brain_tool_catalog_id::text, c.thread_id, c.turn_id,
       c.manifest_digest, b.catalog_digest,
       c.object_id::text, c.object_sha256, c.object_size, c.object_media_type,
       c.codex_runtime_manifest_digest, c.checkpoint_allowlist_version,
       c.created_at
FROM %s AS c
JOIN %s AS b ON b.id = c.brain_tool_catalog_id
WHERE c.run_id = $1
FOR UPDATE OF c`, s.table("checkpoints"), s.table("brain_tool_catalogs"))
	var checkpoint Checkpoint
	var manifestDigest, catalogDigest, objectDigest, runtimeDigest []byte
	err := transaction.QueryRow(ctx, query, runID).Scan(
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
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Checkpoint{}, false, nil
	}
	if err != nil {
		return Checkpoint{}, false, databaseError(operation+" read committed checkpoint", err)
	}
	for destination, source := range map[*[sha256.Size]byte][]byte{
		&checkpoint.ManifestDigest:             manifestDigest,
		&checkpoint.CatalogDigest:              catalogDigest,
		&checkpoint.Object.SHA256:              objectDigest,
		&checkpoint.CodexRuntimeManifestDigest: runtimeDigest,
	} {
		if err := copyStoredSHA256(destination, source); err != nil {
			return Checkpoint{}, false, databaseError(operation+" decode committed checkpoint digest", err)
		}
	}
	if err := validateStoredCheckpoint(checkpoint); err != nil {
		return Checkpoint{}, false, databaseError(operation+" validate committed checkpoint", err)
	}
	return checkpoint, true, nil
}

func (s *StateStore) exactCheckpointCommitRetry(ctx context.Context, transaction pgx.Tx, operation string, run Run, attempt RunAttempt, checkpoint Checkpoint, command CommitCheckpointAndTerminalRunCommand) (CommitCheckpointAndTerminalRunResult, error) {
	if run.Status != RunStatusCompleted || attempt.Status != AttemptStatusSucceeded ||
		attempt.TerminalThreadID != command.ThreadID || attempt.TerminalTurnID != command.TurnID ||
		!checkpointMatchesCommit(checkpoint, run, attempt, command) {
		return CommitCheckpointAndTerminalRunResult{}, commandError(ErrorIdempotencyConflict, operation, "checkpoint", checkpoint.ID, "run already has a different committed checkpoint or terminal identity")
	}
	query := fmt.Sprintf(`
SELECT active_run_id::text, latest_checkpoint_id::text, version
FROM %s
WHERE id = $1
FOR UPDATE`, s.table("sessions"))
	var activeRunID, latestCheckpointID *string
	var sessionVersion int64
	if err := transaction.QueryRow(ctx, query, run.SessionID).Scan(&activeRunID, &latestCheckpointID, &sessionVersion); err != nil {
		return CommitCheckpointAndTerminalRunResult{}, databaseError(operation+" verify committed session", err)
	}
	if activeRunID != nil || latestCheckpointID == nil || *latestCheckpointID != checkpoint.ID {
		return CommitCheckpointAndTerminalRunResult{}, databaseError(operation+" verify committed session", errors.New("completed run checkpoint is not the session resume pointer"))
	}
	return CommitCheckpointAndTerminalRunResult{
		Run: run, Attempt: attempt, Checkpoint: checkpoint,
		SessionVersion: sessionVersion, Created: false,
	}, nil
}

func checkpointMatchesCommit(checkpoint Checkpoint, run Run, attempt RunAttempt, command CommitCheckpointAndTerminalRunCommand) bool {
	return checkpoint.ID == command.CheckpointID &&
		checkpoint.WorkspaceID == run.WorkspaceID &&
		checkpoint.SessionID == run.SessionID &&
		checkpoint.RunID == run.ID &&
		checkpoint.AttemptID == attempt.ID &&
		checkpoint.AttemptGeneration == attempt.Generation &&
		checkpoint.BrainToolCatalogID == command.BrainToolCatalogID &&
		checkpoint.ThreadID == command.ThreadID &&
		checkpoint.TurnID == command.TurnID &&
		checkpoint.ManifestDigest == command.ManifestDigest &&
		checkpoint.CatalogDigest == command.CatalogDigest &&
		checkpoint.Object == command.Object &&
		checkpoint.CodexRuntimeManifestDigest == command.CodexRuntimeManifestDigest &&
		checkpoint.CheckpointAllowlistVersion == command.CheckpointAllowlistVersion
}

func (s *StateStore) deleteFinalizedLeases(ctx context.Context, transaction pgx.Tx, operation string, run Run, attempt RunAttempt, holderID string, generation int64) error {
	attemptQuery := fmt.Sprintf(`
DELETE FROM %s
WHERE run_attempt_id = $1 AND holder_id = $2 AND generation = $3`, s.table("attempt_leases"))
	result, err := transaction.Exec(ctx, attemptQuery, attempt.ID, holderID, generation)
	if err != nil {
		return databaseError(operation+" delete attempt lease", err)
	}
	if result.RowsAffected() != 1 {
		return commandError(ErrorLeaseLost, operation, "attempt", attempt.ID, "attempt lease disappeared while committing checkpoint")
	}

	sessionQuery := fmt.Sprintf(`
DELETE FROM %s
WHERE session_id = $1 AND run_id = $2 AND holder_id = $3 AND generation = $4`, s.table("session_leases"))
	result, err = transaction.Exec(ctx, sessionQuery, run.SessionID, run.ID, holderID, generation)
	if err != nil {
		return databaseError(operation+" delete session lease", err)
	}
	if result.RowsAffected() != 1 {
		return commandError(ErrorLeaseLost, operation, "session", run.SessionID, "session lease disappeared while committing checkpoint")
	}
	return nil
}
