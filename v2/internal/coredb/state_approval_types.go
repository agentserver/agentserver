package coredb

import "time"

const (
	MaxApprovalTTL = 24 * time.Hour

	ApprovalStatusPending   = "pending"
	ApprovalStatusApproved  = "approved"
	ApprovalStatusDenied    = "denied"
	ApprovalStatusExpired   = "expired"
	ApprovalStatusCancelled = "cancelled"
	ApprovalStatusConsumed  = "consumed"

	ApprovalDecisionApprove = "approve"
	ApprovalDecisionDeny    = "deny"
)

// Approval is the durable authority for one policy=ask execution. An
// approved decision is not dispatch authority: the exact live gateway must
// still consume it before the execution enters approved.
type Approval struct {
	ID                   string
	ExecutionID          string
	RunID                string
	RunAttemptID         string
	RunAttemptGeneration int64
	Nonce                string
	RequesterID          string
	ApproverID           string
	Decision             string
	ContextHash          CanonicalJSONHash
	Status               string
	ExpiresAt            time.Time
	DecidedAt            *time.Time
	ConsumedAt           *time.Time
	Version              int64
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type CreateApprovalCommand struct {
	ApprovalID               string
	ExecutionID              string
	RunID                    string
	AttemptID                string
	HolderID                 string
	Generation               int64
	ExpectedExecutionVersion int64
	Nonce                    string
	RequesterID              string
	ExpiresAt                time.Time
	Record                   TransitionRecord
}

type CreateApprovalResult struct {
	Execution Execution
	Approval  Approval
	Created   bool
}

type DecideApprovalCommand struct {
	ApprovalID              string
	WorkspaceID             string
	ActorID                 string
	Nonce                   string
	ExpectedContextHash     [32]byte
	ExpectedApprovalVersion int64
	Decision                string
	Record                  TransitionRecord
}

type DecideApprovalResult struct {
	Execution Execution
	Approval  Approval
	Changed   bool
}

type ExpireApprovalCommand struct {
	ApprovalID              string
	Nonce                   string
	ExpectedContextHash     [32]byte
	ExpectedApprovalVersion int64
	Record                  TransitionRecord
}

type ExpireApprovalResult struct {
	Execution Execution
	Approval  Approval
	Changed   bool
}

type CancelApprovalCommand struct {
	ApprovalID              string
	Nonce                   string
	ExpectedContextHash     [32]byte
	ExpectedApprovalVersion int64
	Record                  TransitionRecord
}

type CancelApprovalResult struct {
	Execution Execution
	Approval  Approval
	Changed   bool
}

type ConsumeApprovalCommand struct {
	ApprovalID               string
	ExecutionID              string
	RunID                    string
	AttemptID                string
	HolderID                 string
	Generation               int64
	Nonce                    string
	ExpectedContextHash      [32]byte
	ExpectedApprovalVersion  int64
	ExpectedExecutionVersion int64
	Record                   TransitionRecord
}

type ConsumeApprovalResult struct {
	Execution Execution
	Approval  Approval
	Consumed  bool
}

type ObserveApprovalCommand struct {
	ApprovalID           string
	ExecutionID          string
	RunID                string
	AttemptID            string
	HolderID             string
	Generation           int64
	Nonce                string
	ExpectedContextHash  [32]byte
	AfterApprovalVersion int64
	Record               TransitionRecord
}

type ObserveApprovalResult struct {
	Execution Execution
	Approval  Approval
	Changed   bool
}
