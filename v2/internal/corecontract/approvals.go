package corecontract

import "time"

const (
	CreateApprovalPath         = "/internal/v2/approvals:create"
	ApprovalPathPrefix         = "/internal/v2/approvals/"
	ApprovalActionRoutePattern = "POST /internal/v2/approvals/{approvalAction}"

	DecideUserApprovalRoutePattern = "POST /v2/workspaces/{workspaceId}/approvals/{approvalAction}"
)

type ApprovalState struct {
	ApprovalID           string              `json:"approvalId"`
	ExecutionID          string              `json:"executionId"`
	RunID                string              `json:"runId"`
	RunAttemptID         string              `json:"runAttemptId"`
	RunAttemptGeneration int64               `json:"runAttemptGeneration"`
	Nonce                string              `json:"nonce"`
	RequesterID          string              `json:"requesterId"`
	ApproverID           string              `json:"approverId,omitempty"`
	Decision             string              `json:"decision,omitempty"`
	ContextDigest        CanonicalJSONDigest `json:"contextDigest"`
	Status               string              `json:"status"`
	ExpiresAt            time.Time           `json:"expiresAt"`
	DecidedAt            *time.Time          `json:"decidedAt,omitempty"`
	ConsumedAt           *time.Time          `json:"consumedAt,omitempty"`
	Version              int64               `json:"version"`
	CreatedAt            time.Time           `json:"createdAt"`
	UpdatedAt            time.Time           `json:"updatedAt"`
}

type CreateApprovalRequest struct {
	ApprovalID               string           `json:"approvalId"`
	ExecutionID              string           `json:"executionId"`
	RunID                    string           `json:"runId"`
	RunAttemptID             string           `json:"runAttemptId"`
	HolderID                 string           `json:"holderId"`
	RunAttemptGeneration     int64            `json:"runAttemptGeneration"`
	ExpectedExecutionVersion int64            `json:"expectedExecutionVersion"`
	Nonce                    string           `json:"nonce"`
	RequesterID              string           `json:"requesterId"`
	ExpiresAt                time.Time        `json:"expiresAt"`
	Record                   TransitionRecord `json:"record"`
}

type CreateApprovalResponse struct {
	Execution ExecutionState `json:"execution"`
	Approval  ApprovalState  `json:"approval"`
	Created   bool           `json:"created"`
}

type ApprovalTerminalRequest struct {
	ApprovalID              string              `json:"approvalId"`
	Nonce                   string              `json:"nonce"`
	ContextDigest           CanonicalJSONDigest `json:"contextDigest"`
	ExpectedApprovalVersion int64               `json:"expectedApprovalVersion"`
	Record                  TransitionRecord    `json:"record"`
}

type ApprovalTerminalResponse struct {
	Execution ExecutionState `json:"execution"`
	Approval  ApprovalState  `json:"approval"`
	Changed   bool           `json:"changed"`
}

type ConsumeApprovalRequest struct {
	ApprovalID               string              `json:"approvalId"`
	ExecutionID              string              `json:"executionId"`
	RunID                    string              `json:"runId"`
	RunAttemptID             string              `json:"runAttemptId"`
	HolderID                 string              `json:"holderId"`
	RunAttemptGeneration     int64               `json:"runAttemptGeneration"`
	Nonce                    string              `json:"nonce"`
	ContextDigest            CanonicalJSONDigest `json:"contextDigest"`
	ExpectedApprovalVersion  int64               `json:"expectedApprovalVersion"`
	ExpectedExecutionVersion int64               `json:"expectedExecutionVersion"`
	Record                   TransitionRecord    `json:"record"`
}

type ConsumeApprovalResponse struct {
	Execution ExecutionState `json:"execution"`
	Approval  ApprovalState  `json:"approval"`
	Consumed  bool           `json:"consumed"`
}

// ObserveApprovalRequest is the harness-pool's bounded long-poll capability.
// It repeats the complete approval and live attempt scope so an approval ID
// alone is never a read capability. Record is consumed only when database
// time causes the observation to atomically expire the approval.
type ObserveApprovalRequest struct {
	ApprovalID           string              `json:"approvalId"`
	ExecutionID          string              `json:"executionId"`
	RunID                string              `json:"runId"`
	RunAttemptID         string              `json:"runAttemptId"`
	HolderID             string              `json:"holderId"`
	RunAttemptGeneration int64               `json:"runAttemptGeneration"`
	Nonce                string              `json:"nonce"`
	ContextDigest        CanonicalJSONDigest `json:"contextDigest"`
	AfterApprovalVersion int64               `json:"afterApprovalVersion"`
	WaitMillis           int64               `json:"waitMs"`
	Record               TransitionRecord    `json:"record"`
}

type ObserveApprovalResponse struct {
	ExecutionID      string        `json:"executionId"`
	ExecutionStatus  string        `json:"executionStatus"`
	ExecutionVersion int64         `json:"executionVersion"`
	Approval         ApprovalState `json:"approval"`
	OutcomeAvailable bool          `json:"outcomeAvailable"`
}

type DecideUserApprovalRequest struct {
	Decision                string              `json:"decision"`
	Nonce                   string              `json:"nonce"`
	ContextDigest           CanonicalJSONDigest `json:"contextDigest"`
	ExpectedApprovalVersion int64               `json:"expectedApprovalVersion"`
}

type DecideUserApprovalResponse struct {
	WorkspaceID      string        `json:"workspaceId"`
	ExecutionID      string        `json:"executionId"`
	ExecutionStatus  string        `json:"executionStatus"`
	ExecutionVersion int64         `json:"executionVersion"`
	Approval         ApprovalState `json:"approval"`
	Changed          bool          `json:"changed"`
}

func ExpireApprovalPath(approvalID string) string {
	return ApprovalPathPrefix + approvalID + ":expire"
}

func CancelApprovalPath(approvalID string) string {
	return ApprovalPathPrefix + approvalID + ":cancel"
}

func ConsumeApprovalPath(approvalID string) string {
	return ApprovalPathPrefix + approvalID + ":consume"
}

func ObserveApprovalPath(approvalID string) string {
	return ApprovalPathPrefix + approvalID + ":observe"
}

func DecideUserApprovalPath(workspaceID, approvalID string) string {
	return "/v2/workspaces/" + workspaceID + "/approvals/" + approvalID + ":decide"
}
