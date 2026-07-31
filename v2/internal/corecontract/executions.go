package corecontract

import (
	"encoding/json"
	"time"
)

const (
	PrepareExecutionPath = "/internal/v2/executions:prepare"
	ExecutionPathPrefix  = "/internal/v2/executions/"
)

// TransitionRecord supplies the immutable event and outbox identities that
// core commits atomically with one domain state transition.
type TransitionRecord struct {
	EventID            string `json:"eventId"`
	ProducerInstanceID string `json:"producerInstanceId"`
	ProducerSeq        int64  `json:"producerSeq"`
	OutboxID           string `json:"outboxId"`
}

// CanonicalJSONDigest is a read-only projection of a digest computed by core.
// Command requests carry the JSON value itself, never a caller-supplied digest.
type CanonicalJSONDigest struct {
	Domain               string `json:"domain"`
	CanonicalizerVersion string `json:"canonicalizerVersion"`
	SHA256               string `json:"sha256"`
}

type ExecutionState struct {
	ExecutionID          string               `json:"executionId"`
	RunID                string               `json:"runId"`
	RunAttemptID         string               `json:"runAttemptId"`
	RunAttemptGeneration int64                `json:"runAttemptGeneration"`
	AppServerToolCallID  string               `json:"appServerToolCallId"`
	ExecutorID           string               `json:"executorId"`
	EnvironmentID        string               `json:"environmentId"`
	ToolName             string               `json:"toolName"`
	ToolVersion          string               `json:"toolVersion"`
	MapperVersion        string               `json:"mapperVersion"`
	PolicyVersion        string               `json:"policyVersion"`
	PolicyDecision       string               `json:"policyDecision"`
	OperationCount       int                  `json:"operationCount"`
	ArgumentsDigest      CanonicalJSONDigest  `json:"argumentsDigest"`
	ToolSchemaDigest     CanonicalJSONDigest  `json:"toolSchemaDigest"`
	OperationPlanDigest  CanonicalJSONDigest  `json:"operationPlanDigest"`
	PolicyContextDigest  CanonicalJSONDigest  `json:"policyContextDigest"`
	Status               string               `json:"status"`
	DispatchedAt         *time.Time           `json:"dispatchedAt,omitempty"`
	TerminalResultDigest *CanonicalJSONDigest `json:"terminalResultDigest,omitempty"`
	TerminalAt           *time.Time           `json:"terminalAt,omitempty"`
	Version              int64                `json:"version"`
	CreatedAt            time.Time            `json:"createdAt"`
	UpdatedAt            time.Time            `json:"updatedAt"`
}

type ExecutionOperationState struct {
	OperationID           string               `json:"operationId"`
	ExecutionID           string               `json:"executionId"`
	Ordinal               int                  `json:"ordinal"`
	Kind                  string               `json:"kind"`
	EffectClass           string               `json:"effectClass"`
	MutationKey           string               `json:"mutationKey"`
	ParamsDigest          CanonicalJSONDigest  `json:"paramsDigest"`
	Status                string               `json:"status"`
	ConnectionGeneration  int64                `json:"connectionGeneration,omitempty"`
	AcknowledgementDigest *CanonicalJSONDigest `json:"acknowledgementDigest,omitempty"`
	TerminalResultDigest  *CanonicalJSONDigest `json:"terminalResultDigest,omitempty"`
	DispatchedAt          *time.Time           `json:"dispatchedAt,omitempty"`
	AcknowledgedAt        *time.Time           `json:"acknowledgedAt,omitempty"`
	TerminalAt            *time.Time           `json:"terminalAt,omitempty"`
	Version               int64                `json:"version"`
	CreatedAt             time.Time            `json:"createdAt"`
	UpdatedAt             time.Time            `json:"updatedAt"`
}

type PrepareExecutionRequest struct {
	ExecutionID               string           `json:"executionId"`
	RunID                     string           `json:"runId"`
	RunAttemptID              string           `json:"runAttemptId"`
	HolderID                  string           `json:"holderId"`
	RunAttemptGeneration      int64            `json:"runAttemptGeneration"`
	ExpectedRunVersion        int64            `json:"expectedRunVersion"`
	ExpectedRunAttemptVersion int64            `json:"expectedRunAttemptVersion"`
	AppServerToolCallID       string           `json:"appServerToolCallId"`
	ExecutorID                string           `json:"executorId"`
	EnvironmentID             string           `json:"environmentId"`
	ToolName                  string           `json:"toolName"`
	ToolVersion               string           `json:"toolVersion"`
	MapperVersion             string           `json:"mapperVersion"`
	PolicyVersion             string           `json:"policyVersion"`
	OperationCount            int              `json:"operationCount"`
	Arguments                 json.RawMessage  `json:"arguments"`
	ToolSchema                json.RawMessage  `json:"toolSchema"`
	OperationPlan             json.RawMessage  `json:"operationPlan"`
	PolicyContext             json.RawMessage  `json:"policyContext"`
	PolicyDecision            string           `json:"policyDecision"`
	Record                    TransitionRecord `json:"record"`
}

type PrepareExecutionResponse struct {
	Execution ExecutionState `json:"execution"`
	Created   bool           `json:"created"`
}

type PrepareOperationRequest struct {
	OperationID              string           `json:"operationId"`
	ExecutionID              string           `json:"executionId"`
	RunID                    string           `json:"runId"`
	RunAttemptID             string           `json:"runAttemptId"`
	HolderID                 string           `json:"holderId"`
	RunAttemptGeneration     int64            `json:"runAttemptGeneration"`
	ExpectedExecutionVersion int64            `json:"expectedExecutionVersion"`
	Ordinal                  int              `json:"ordinal"`
	Kind                     string           `json:"kind"`
	EffectClass              string           `json:"effectClass"`
	MutationKey              string           `json:"mutationKey"`
	Params                   json.RawMessage  `json:"params"`
	Record                   TransitionRecord `json:"record"`
}

type PrepareOperationResponse struct {
	Execution ExecutionState          `json:"execution"`
	Operation ExecutionOperationState `json:"operation"`
	Created   bool                    `json:"created"`
}

type BeginOperationDispatchRequest struct {
	OperationID              string           `json:"operationId"`
	ExecutionID              string           `json:"executionId"`
	RunID                    string           `json:"runId"`
	RunAttemptID             string           `json:"runAttemptId"`
	HolderID                 string           `json:"holderId"`
	RunAttemptGeneration     int64            `json:"runAttemptGeneration"`
	ConnectionGeneration     int64            `json:"connectionGeneration"`
	ExpectedExecutionVersion int64            `json:"expectedExecutionVersion"`
	ExpectedOperationVersion int64            `json:"expectedOperationVersion"`
	PolicyContext            json.RawMessage  `json:"policyContext"`
	OperationPlan            json.RawMessage  `json:"operationPlan"`
	Params                   json.RawMessage  `json:"params"`
	Record                   TransitionRecord `json:"record"`
}

type BeginOperationDispatchResponse struct {
	Execution ExecutionState          `json:"execution"`
	Operation ExecutionOperationState `json:"operation"`
	Began     bool                    `json:"began"`
}

type AcknowledgeOperationRequest struct {
	OperationID              string           `json:"operationId"`
	ExecutionID              string           `json:"executionId"`
	RunID                    string           `json:"runId"`
	RunAttemptID             string           `json:"runAttemptId"`
	RunAttemptGeneration     int64            `json:"runAttemptGeneration"`
	ConnectionGeneration     int64            `json:"connectionGeneration"`
	ExpectedExecutionVersion int64            `json:"expectedExecutionVersion"`
	ExpectedOperationVersion int64            `json:"expectedOperationVersion"`
	Acknowledgement          json.RawMessage  `json:"acknowledgement"`
	Record                   TransitionRecord `json:"record"`
}

type AcknowledgeOperationResponse struct {
	Execution ExecutionState          `json:"execution"`
	Operation ExecutionOperationState `json:"operation"`
	Changed   bool                    `json:"changed"`
}

type CompleteOperationRequest struct {
	OperationID              string           `json:"operationId"`
	ExecutionID              string           `json:"executionId"`
	RunID                    string           `json:"runId"`
	RunAttemptID             string           `json:"runAttemptId"`
	RunAttemptGeneration     int64            `json:"runAttemptGeneration"`
	ConnectionGeneration     int64            `json:"connectionGeneration"`
	ExpectedExecutionVersion int64            `json:"expectedExecutionVersion"`
	ExpectedOperationVersion int64            `json:"expectedOperationVersion"`
	TerminalStatus           string           `json:"terminalStatus"`
	Result                   json.RawMessage  `json:"result"`
	Record                   TransitionRecord `json:"record"`
}

type CompleteOperationResponse struct {
	Execution ExecutionState          `json:"execution"`
	Operation ExecutionOperationState `json:"operation"`
	Changed   bool                    `json:"changed"`
}

type SkipOperationRequest struct {
	OperationID              string           `json:"operationId"`
	ExecutionID              string           `json:"executionId"`
	RunID                    string           `json:"runId"`
	RunAttemptID             string           `json:"runAttemptId"`
	HolderID                 string           `json:"holderId"`
	RunAttemptGeneration     int64            `json:"runAttemptGeneration"`
	ExpectedExecutionVersion int64            `json:"expectedExecutionVersion"`
	ExpectedOperationVersion int64            `json:"expectedOperationVersion"`
	Result                   json.RawMessage  `json:"result"`
	Record                   TransitionRecord `json:"record"`
}

type SkipOperationResponse struct {
	Execution ExecutionState          `json:"execution"`
	Operation ExecutionOperationState `json:"operation"`
	Changed   bool                    `json:"changed"`
}

type CompleteExecutionRequest struct {
	ExecutionID              string           `json:"executionId"`
	RunID                    string           `json:"runId"`
	RunAttemptID             string           `json:"runAttemptId"`
	RunAttemptGeneration     int64            `json:"runAttemptGeneration"`
	ExpectedExecutionVersion int64            `json:"expectedExecutionVersion"`
	TerminalStatus           string           `json:"terminalStatus"`
	Result                   json.RawMessage  `json:"result"`
	Record                   TransitionRecord `json:"record"`
}

type CompleteExecutionResponse struct {
	Execution ExecutionState `json:"execution"`
	Changed   bool           `json:"changed"`
}

func PrepareOperationPath(executionID string) string {
	return ExecutionPathPrefix + executionID + "/operations:prepare"
}

func BeginOperationDispatchPath(executionID, operationID string) string {
	return executionOperationActionPath(executionID, operationID, "begin-dispatch")
}

func AcknowledgeOperationPath(executionID, operationID string) string {
	return executionOperationActionPath(executionID, operationID, "acknowledge")
}

func CompleteOperationPath(executionID, operationID string) string {
	return executionOperationActionPath(executionID, operationID, "complete")
}

func SkipOperationPath(executionID, operationID string) string {
	return executionOperationActionPath(executionID, operationID, "skip")
}

func CompleteExecutionPath(executionID string) string {
	return ExecutionPathPrefix + executionID + ":complete"
}

func executionOperationActionPath(executionID, operationID, action string) string {
	return ExecutionPathPrefix + executionID + "/operations/" + operationID + ":" + action
}
