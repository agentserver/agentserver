package coredb

import "time"

const (
	MaxExecutionOperations = 256

	ExecutionStatusCreated         = "created"
	ExecutionStatusPendingApproval = "pending_approval"
	ExecutionStatusApproved        = "approved"
	ExecutionStatusDenied          = "denied"
	ExecutionStatusExpired         = "expired"
	ExecutionStatusDispatching     = "dispatching"
	ExecutionStatusRunning         = "running"
	ExecutionStatusCancelling      = "cancelling"
	ExecutionStatusSucceeded       = "succeeded"
	ExecutionStatusFailed          = "failed"
	ExecutionStatusCancelled       = "cancelled"
	ExecutionStatusUnknown         = "unknown"

	OperationStatusPrepared     = "prepared"
	OperationStatusDispatching  = "dispatching"
	OperationStatusAcknowledged = "acknowledged"
	OperationStatusSucceeded    = "succeeded"
	OperationStatusFailed       = "failed"
	OperationStatusCancelled    = "cancelled"
	OperationStatusUnknown      = "unknown"
	OperationStatusSkipped      = "skipped"

	OperationKindTimeoutTerminate = "timeout_terminate"

	PolicyDecisionAllow = "allow"
	PolicyDecisionAsk   = "ask"
	PolicyDecisionDeny  = "deny"

	OperationEffectRead     = "read"
	OperationEffectMutation = "mutation"
)

type Execution struct {
	ID                   string
	RunID                string
	RunAttemptID         string
	RunAttemptGeneration int64
	AppServerToolCallID  string
	ExecutorID           string
	EnvID                string
	ToolName             string
	ToolVersion          string
	MapperVersion        string
	PolicyVersion        string
	PolicyDecision       string
	OperationCount       int
	ArgumentsHash        CanonicalJSONHash
	ToolSchemaHash       CanonicalJSONHash
	OperationPlanHash    CanonicalJSONHash
	PolicyContextHash    CanonicalJSONHash
	Status               string
	DispatchedAt         *time.Time
	TerminalResultHash   *CanonicalJSONHash
	TerminalAt           *time.Time
	Version              int64
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type ExecutionOperation struct {
	ID                   string
	ExecutionID          string
	Ordinal              int
	Kind                 string
	EffectClass          string
	MutationKey          string
	ParamsHash           CanonicalJSONHash
	Status               string
	ConnectionGeneration int64
	AcknowledgementHash  *CanonicalJSONHash
	TerminalResultHash   *CanonicalJSONHash
	DispatchedAt         *time.Time
	AcknowledgedAt       *time.Time
	TerminalAt           *time.Time
	Version              int64
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type PrepareExecutionCommand struct {
	ExecutionID            string
	RunID                  string
	AttemptID              string
	HolderID               string
	Generation             int64
	ExpectedRunVersion     int64
	ExpectedAttemptVersion int64
	AppServerToolCallID    string
	ExecutorID             string
	EnvID                  string
	ToolName               string
	ToolVersion            string
	MapperVersion          string
	PolicyVersion          string
	OperationCount         int
	ArgumentsHash          CanonicalJSONHash
	ToolSchemaHash         CanonicalJSONHash
	OperationPlanHash      CanonicalJSONHash
	PolicyContextHash      CanonicalJSONHash
	PolicyDecision         string
	Record                 TransitionRecord
}

type PrepareExecutionResult struct {
	Execution Execution
	Created   bool
}

type PrepareOperationCommand struct {
	OperationID              string
	ExecutionID              string
	RunID                    string
	AttemptID                string
	HolderID                 string
	Generation               int64
	ExpectedExecutionVersion int64
	Ordinal                  int
	Kind                     string
	EffectClass              string
	MutationKey              string
	ParamsHash               CanonicalJSONHash
	Record                   TransitionRecord
}

type PrepareOperationResult struct {
	Execution Execution
	Operation ExecutionOperation
	Created   bool
}

type BeginOperationDispatchCommand struct {
	OperationID              string
	ExecutionID              string
	RunID                    string
	AttemptID                string
	HolderID                 string
	Generation               int64
	ConnectionGeneration     int64
	ExpectedExecutionVersion int64
	ExpectedOperationVersion int64
	PolicyContextHash        CanonicalJSONHash
	OperationPlanHash        CanonicalJSONHash
	ParamsHash               CanonicalJSONHash
	Record                   TransitionRecord
}

type BeginOperationDispatchResult struct {
	Execution Execution
	Operation ExecutionOperation
	// Began is the one-shot permission to perform the external send. It is true
	// only for the transaction that changed prepared to dispatching. An exact
	// retry after commit always returns false and must reconcile or mark unknown.
	Began bool
}

type AcknowledgeOperationCommand struct {
	OperationID              string
	ExecutionID              string
	RunID                    string
	AttemptID                string
	Generation               int64
	ConnectionGeneration     int64
	ExpectedExecutionVersion int64
	ExpectedOperationVersion int64
	AcknowledgementHash      CanonicalJSONHash
	Record                   TransitionRecord
}

type AcknowledgeOperationResult struct {
	Execution Execution
	Operation ExecutionOperation
	Changed   bool
}

type CompleteOperationCommand struct {
	OperationID              string
	ExecutionID              string
	RunID                    string
	AttemptID                string
	Generation               int64
	ConnectionGeneration     int64
	ExpectedExecutionVersion int64
	ExpectedOperationVersion int64
	TerminalStatus           string
	ResultHash               CanonicalJSONHash
	Record                   TransitionRecord
}

type CompleteOperationResult struct {
	Execution Execution
	Operation ExecutionOperation
	Changed   bool
}

type SkipOperationCommand struct {
	OperationID              string
	ExecutionID              string
	RunID                    string
	AttemptID                string
	HolderID                 string
	Generation               int64
	ExpectedExecutionVersion int64
	ExpectedOperationVersion int64
	ResultHash               CanonicalJSONHash
	Record                   TransitionRecord
}

type SkipOperationResult struct {
	Execution Execution
	Operation ExecutionOperation
	Changed   bool
}

type CompleteExecutionCommand struct {
	ExecutionID              string
	RunID                    string
	AttemptID                string
	Generation               int64
	ExpectedExecutionVersion int64
	TerminalStatus           string
	ResultHash               CanonicalJSONHash
	Record                   TransitionRecord
}

type CompleteExecutionResult struct {
	Execution Execution
	Changed   bool
}
