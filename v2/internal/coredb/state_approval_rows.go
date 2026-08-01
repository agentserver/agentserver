package coredb

import (
	"fmt"
	"time"
)

func scanApproval(scanner rowScanner) (Approval, error) {
	var approval Approval
	var approverID *string
	var decision *string
	var canonicalizer string
	var contextHash []byte
	var decidedAt *time.Time
	var consumedAt *time.Time
	if err := scanner.Scan(
		&approval.ID,
		&approval.ExecutionID,
		&approval.RunID,
		&approval.RunAttemptID,
		&approval.RunAttemptGeneration,
		&approval.Nonce,
		&approval.RequesterID,
		&approverID,
		&decision,
		&canonicalizer,
		&contextHash,
		&approval.Status,
		&approval.ExpiresAt,
		&decidedAt,
		&consumedAt,
		&approval.Version,
		&approval.CreatedAt,
		&approval.UpdatedAt,
	); err != nil {
		return Approval{}, err
	}
	if approverID != nil {
		approval.ApproverID = *approverID
	}
	if decision != nil {
		approval.Decision = *decision
	}
	approval.DecidedAt = decidedAt
	approval.ConsumedAt = consumedAt
	var err error
	approval.ContextHash, err = storedCanonicalHash(HashDomainApprovalContext, contextHash, canonicalizer)
	if err != nil {
		return Approval{}, fmt.Errorf("approval %s context: %w", approval.ID, err)
	}
	return approval, nil
}

func approvalColumns(alias string) string {
	if alias != "" {
		alias += "."
	}
	return alias + "id::text, " +
		alias + "execution_id::text, " +
		alias + "run_id::text, " +
		alias + "run_attempt_id::text, " +
		alias + "run_attempt_generation, " +
		alias + "nonce::text, " +
		alias + "requester_id, " +
		alias + "approver_id::text, " +
		alias + "decision, " +
		alias + "canonicalizer_version, " +
		alias + "context_hash, " +
		alias + "status, " +
		alias + "expires_at, " +
		alias + "decided_at, " +
		alias + "consumed_at, " +
		alias + "version, " +
		alias + "created_at, " +
		alias + "updated_at"
}
