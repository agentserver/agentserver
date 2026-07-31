package coredb

import (
	"crypto/sha256"
	"fmt"
	"time"
)

type rowScanner interface {
	Scan(...any) error
}

func scanRun(scanner rowScanner) (Run, error) {
	var run Run
	var requestHash []byte
	err := scanner.Scan(
		&run.ID,
		&run.WorkspaceID,
		&run.SessionID,
		&run.ActorID,
		&run.Status,
		&requestHash,
		&run.IdempotencyKey,
		&run.CurrentAttemptGeneration,
		&run.NextEventSeq,
		&run.Version,
		&run.CreatedAt,
		&run.UpdatedAt,
	)
	if err != nil {
		return Run{}, err
	}
	if len(requestHash) != sha256.Size {
		return Run{}, fmt.Errorf("run %s has invalid %d-byte request hash", run.ID, len(requestHash))
	}
	copy(run.RequestHash[:], requestHash)
	return run, nil
}

func scanAttempt(scanner rowScanner) (RunAttempt, error) {
	var attempt RunAttempt
	var holderID *string
	var turnStartedAt *time.Time
	var terminalThreadID *string
	var terminalTurnID *string
	err := scanner.Scan(
		&attempt.ID,
		&attempt.RunID,
		&attempt.Generation,
		&attempt.Status,
		&turnStartedAt,
		&terminalThreadID,
		&terminalTurnID,
		&holderID,
		&attempt.Version,
		&attempt.CreatedAt,
		&attempt.UpdatedAt,
	)
	if err != nil {
		return RunAttempt{}, err
	}
	attempt.TurnStartedAt = turnStartedAt
	if terminalThreadID != nil {
		attempt.TerminalThreadID = *terminalThreadID
	}
	if terminalTurnID != nil {
		attempt.TerminalTurnID = *terminalTurnID
	}
	if holderID != nil {
		attempt.HolderID = *holderID
	}
	return attempt, nil
}

func scanLease(scanner rowScanner) (Lease, error) {
	var lease Lease
	err := scanner.Scan(
		&lease.HolderID,
		&lease.Generation,
		&lease.ExpiresAt,
		&lease.AcquiredAt,
		&lease.RenewedAt,
	)
	return lease, err
}

func runColumns(alias string) string {
	if alias != "" {
		alias += "."
	}
	return alias + "id::text, " +
		alias + "workspace_id::text, " +
		alias + "session_id::text, " +
		alias + "actor_id::text, " +
		alias + "status, " +
		alias + "request_hash, " +
		alias + "idempotency_key, " +
		alias + "current_attempt_generation, " +
		alias + "next_event_seq, " +
		alias + "version, " +
		alias + "created_at, " +
		alias + "updated_at"
}

func attemptColumns(alias string) string {
	if alias != "" {
		alias += "."
	}
	return alias + "id::text, " +
		alias + "run_id::text, " +
		alias + "generation, " +
		alias + "status, " +
		alias + "turn_started_at, " +
		alias + "terminal_thread_id, " +
		alias + "terminal_turn_id, " +
		alias + "holder_id, " +
		alias + "version, " +
		alias + "created_at, " +
		alias + "updated_at"
}
