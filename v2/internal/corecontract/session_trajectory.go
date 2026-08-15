package corecontract

import "time"

const UserSessionTrajectoryRoutePattern = "/v2/workspaces/{workspaceId}/sessions/{sessionId}/trajectory"

func UserSessionTrajectoryPath(workspaceID, sessionID string) string {
	return UserSessionPath(workspaceID, sessionID) + "/trajectory"
}

type UserSessionTrajectoryDetail struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type UserSessionTrajectoryFailure struct {
	Code        string `json:"code"`
	Category    string `json:"category"`
	Message     string `json:"message"`
	Component   string `json:"component"`
	Phase       string `json:"phase"`
	Retryable   bool   `json:"retryable"`
	Fingerprint string `json:"fingerprint,omitempty"`
}

// UserSessionTrajectoryRecord is a bounded, already-redacted lifecycle
// projection. Browser clients never receive the raw canonical event payload.
type UserSessionTrajectoryRecord struct {
	ID                   string                        `json:"id"`
	ParentID             string                        `json:"parentId,omitempty"`
	Kind                 string                        `json:"kind"`
	Status               string                        `json:"status"`
	Title                string                        `json:"title"`
	Summary              string                        `json:"summary"`
	RunID                string                        `json:"runId"`
	RunAttemptID         string                        `json:"runAttemptId,omitempty"`
	RunAttemptGeneration int64                         `json:"runAttemptGeneration,omitempty"`
	ToolCallID           string                        `json:"toolCallId,omitempty"`
	ExecutionID          string                        `json:"executionId,omitempty"`
	OperationID          string                        `json:"operationId,omitempty"`
	SandboxID            string                        `json:"sandboxId,omitempty"`
	TargetGeneration     int64                         `json:"targetGeneration,omitempty"`
	StartedAt            time.Time                     `json:"startedAt"`
	CompletedAt          *time.Time                    `json:"completedAt,omitempty"`
	DurationMillis       *int64                        `json:"durationMillis,omitempty"`
	Input                string                        `json:"input,omitempty"`
	Output               string                        `json:"output,omitempty"`
	InputTruncated       bool                          `json:"inputTruncated,omitempty"`
	OutputTruncated      bool                          `json:"outputTruncated,omitempty"`
	Details              []UserSessionTrajectoryDetail `json:"details"`
	Failure              *UserSessionTrajectoryFailure `json:"failure,omitempty"`
}

type GetUserSessionTrajectoryResponse struct {
	SchemaVersion int                           `json:"schemaVersion"`
	WorkspaceID   string                        `json:"workspaceId"`
	SessionID     string                        `json:"sessionId"`
	ActiveRunID   string                        `json:"activeRunId,omitempty"`
	Records       []UserSessionTrajectoryRecord `json:"records"`
	NextBefore    string                        `json:"nextBefore,omitempty"`
	HasMore       bool                          `json:"hasMore"`
	Truncated     bool                          `json:"truncated"`
	ReadAt        time.Time                     `json:"readAt"`
}
