package executorgateway

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/executionbackend"
)

const coreCanonicalizerRFC8785V1 = "rfc8785-v1"

type ExecutionTransitionRecord struct {
	EventID            string
	ProducerInstanceID string
	ProducerSeq        int64
	OutboxID           string
}

type CanonicalDigest struct {
	Domain               string
	CanonicalizerVersion string
	SHA256               [32]byte
}

type ExecutionState struct {
	ExecutionID          string
	RunID                string
	RunAttemptID         string
	RunAttemptGeneration int64
	AppServerToolCallID  string
	ExecutorID           string
	EnvironmentID        string
	Target               executionbackend.Target
	ToolName             string
	ToolVersion          string
	MapperVersion        string
	PolicyVersion        string
	PolicyDecision       string
	OperationCount       int
	ArgumentsDigest      CanonicalDigest
	ToolSchemaDigest     CanonicalDigest
	OperationPlanDigest  CanonicalDigest
	PolicyContextDigest  CanonicalDigest
	Status               string
	DispatchedAt         *time.Time
	TerminalResultDigest *CanonicalDigest
	TerminalAt           *time.Time
	Version              int64
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type ExecutionOperationState struct {
	OperationID           string
	ExecutionID           string
	Ordinal               int
	Kind                  string
	EffectClass           string
	MutationKey           string
	ParamsDigest          CanonicalDigest
	Status                string
	ConnectionGeneration  int64
	Target                executionbackend.Target
	AcknowledgementDigest *CanonicalDigest
	TerminalResultDigest  *CanonicalDigest
	DispatchedAt          *time.Time
	AcknowledgedAt        *time.Time
	TerminalAt            *time.Time
	Version               int64
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

type PrepareExecutionRequest struct {
	ExecutionID               string
	RunID                     string
	RunAttemptID              string
	HolderID                  string
	RunAttemptGeneration      int64
	ExpectedRunVersion        int64
	ExpectedRunAttemptVersion int64
	AppServerToolCallID       string
	ExecutorID                string
	EnvironmentID             string
	Target                    executionbackend.Target
	ToolName                  string
	ToolVersion               string
	MapperVersion             string
	PolicyVersion             string
	OperationCount            int
	Arguments                 json.RawMessage
	ToolSchema                json.RawMessage
	OperationPlan             json.RawMessage
	PolicyContext             json.RawMessage
	PolicyDecision            string
	Record                    ExecutionTransitionRecord
}

type PrepareExecutionResult struct {
	Execution ExecutionState
	Created   bool
}

type PrepareOperationRequest struct {
	OperationID              string
	ExecutionID              string
	RunID                    string
	RunAttemptID             string
	HolderID                 string
	RunAttemptGeneration     int64
	ExpectedExecutionVersion int64
	Ordinal                  int
	Kind                     string
	EffectClass              string
	MutationKey              string
	Params                   json.RawMessage
	Record                   ExecutionTransitionRecord
}

type PrepareOperationResult struct {
	Execution ExecutionState
	Operation ExecutionOperationState
	Created   bool
}

type BeginOperationDispatchRequest struct {
	OperationID              string
	ExecutionID              string
	RunID                    string
	RunAttemptID             string
	HolderID                 string
	RunAttemptGeneration     int64
	ConnectionGeneration     int64
	Target                   executionbackend.Target
	ExpectedExecutionVersion int64
	ExpectedOperationVersion int64
	PolicyContext            json.RawMessage
	OperationPlan            json.RawMessage
	Params                   json.RawMessage
	Record                   ExecutionTransitionRecord
}

type BeginOperationDispatchResult struct {
	Execution ExecutionState
	Operation ExecutionOperationState
	Began     bool
}

type AcknowledgeOperationRequest struct {
	OperationID              string
	ExecutionID              string
	RunID                    string
	RunAttemptID             string
	RunAttemptGeneration     int64
	ConnectionGeneration     int64
	Target                   executionbackend.Target
	ExpectedExecutionVersion int64
	ExpectedOperationVersion int64
	Acknowledgement          json.RawMessage
	Record                   ExecutionTransitionRecord
}

type AcknowledgeOperationResult struct {
	Execution ExecutionState
	Operation ExecutionOperationState
	Changed   bool
}

type CompleteOperationRequest struct {
	OperationID              string
	ExecutionID              string
	RunID                    string
	RunAttemptID             string
	RunAttemptGeneration     int64
	ConnectionGeneration     int64
	Target                   executionbackend.Target
	ExpectedExecutionVersion int64
	ExpectedOperationVersion int64
	TerminalStatus           string
	Result                   json.RawMessage
	Record                   ExecutionTransitionRecord
}

type CompleteOperationResult struct {
	Execution ExecutionState
	Operation ExecutionOperationState
	Changed   bool
}

type SkipOperationRequest struct {
	OperationID              string
	ExecutionID              string
	RunID                    string
	RunAttemptID             string
	HolderID                 string
	RunAttemptGeneration     int64
	ExpectedExecutionVersion int64
	ExpectedOperationVersion int64
	Result                   json.RawMessage
	Record                   ExecutionTransitionRecord
}

type SkipOperationResult struct {
	Execution ExecutionState
	Operation ExecutionOperationState
	Changed   bool
}

type CompleteExecutionRequest struct {
	ExecutionID              string
	RunID                    string
	RunAttemptID             string
	RunAttemptGeneration     int64
	ExpectedExecutionVersion int64
	TerminalStatus           string
	Result                   json.RawMessage
	Record                   ExecutionTransitionRecord
}

type CompleteExecutionResult struct {
	Execution ExecutionState
	Changed   bool
}

type ExecutionAuthority interface {
	PrepareExecution(context.Context, PrepareExecutionRequest) (PrepareExecutionResult, error)
	PrepareOperation(context.Context, PrepareOperationRequest) (PrepareOperationResult, error)
	BeginOperationDispatch(context.Context, BeginOperationDispatchRequest) (BeginOperationDispatchResult, error)
	AcknowledgeOperation(context.Context, AcknowledgeOperationRequest) (AcknowledgeOperationResult, error)
	CompleteOperation(context.Context, CompleteOperationRequest) (CompleteOperationResult, error)
	SkipOperation(context.Context, SkipOperationRequest) (SkipOperationResult, error)
	CompleteExecution(context.Context, CompleteExecutionRequest) (CompleteExecutionResult, error)
}

var _ ExecutionAuthority = (*CoreConnectionClient)(nil)

func (client *CoreConnectionClient) PrepareExecution(ctx context.Context, request PrepareExecutionRequest) (PrepareExecutionResult, error) {
	contractRequest := corecontract.PrepareExecutionRequest{
		ExecutionID:               request.ExecutionID,
		RunID:                     request.RunID,
		RunAttemptID:              request.RunAttemptID,
		HolderID:                  request.HolderID,
		RunAttemptGeneration:      request.RunAttemptGeneration,
		ExpectedRunVersion:        request.ExpectedRunVersion,
		ExpectedRunAttemptVersion: request.ExpectedRunAttemptVersion,
		AppServerToolCallID:       request.AppServerToolCallID,
		ExecutorID:                request.ExecutorID,
		EnvironmentID:             request.EnvironmentID,
		TargetKind:                string(request.Target.Kind),
		TargetID:                  request.Target.ID,
		TargetGeneration:          request.Target.Generation,
		ToolName:                  request.ToolName,
		ToolVersion:               request.ToolVersion,
		MapperVersion:             request.MapperVersion,
		PolicyVersion:             request.PolicyVersion,
		OperationCount:            request.OperationCount,
		Arguments:                 copyCoreJSON(request.Arguments),
		ToolSchema:                copyCoreJSON(request.ToolSchema),
		OperationPlan:             copyCoreJSON(request.OperationPlan),
		PolicyContext:             copyCoreJSON(request.PolicyContext),
		PolicyDecision:            request.PolicyDecision,
		Record:                    contractExecutionTransitionRecord(request.Record),
	}
	var response corecontract.PrepareExecutionResponse
	if err := client.post(ctx, corecontract.PrepareExecutionPath, contractRequest, &response, http.StatusOK); err != nil {
		return PrepareExecutionResult{}, err
	}
	execution, err := gatewayExecutionState(response.Execution)
	if err != nil {
		return PrepareExecutionResult{}, fmt.Errorf("validate core PrepareExecution response: %w", err)
	}
	if execution.RunID != request.RunID || execution.RunAttemptID != request.RunAttemptID || execution.AppServerToolCallID != request.AppServerToolCallID {
		return PrepareExecutionResult{}, errors.New("core PrepareExecution response does not match the requested tool-call identity")
	}
	return PrepareExecutionResult{Execution: execution, Created: response.Created}, nil
}

func (client *CoreConnectionClient) PrepareOperation(ctx context.Context, request PrepareOperationRequest) (PrepareOperationResult, error) {
	contractRequest := corecontract.PrepareOperationRequest{
		OperationID:              request.OperationID,
		ExecutionID:              request.ExecutionID,
		RunID:                    request.RunID,
		RunAttemptID:             request.RunAttemptID,
		HolderID:                 request.HolderID,
		RunAttemptGeneration:     request.RunAttemptGeneration,
		ExpectedExecutionVersion: request.ExpectedExecutionVersion,
		Ordinal:                  request.Ordinal,
		Kind:                     request.Kind,
		EffectClass:              request.EffectClass,
		MutationKey:              request.MutationKey,
		Params:                   copyCoreJSON(request.Params),
		Record:                   contractExecutionTransitionRecord(request.Record),
	}
	var response corecontract.PrepareOperationResponse
	if err := client.post(ctx, corecontract.PrepareOperationPath(request.ExecutionID), contractRequest, &response, http.StatusOK); err != nil {
		return PrepareOperationResult{}, err
	}
	execution, operation, err := gatewayExecutionAndOperation(response.Execution, response.Operation)
	if err != nil {
		return PrepareOperationResult{}, fmt.Errorf("validate core PrepareOperation response: %w", err)
	}
	if operation.OperationID != request.OperationID || operation.ExecutionID != request.ExecutionID {
		return PrepareOperationResult{}, errors.New("core PrepareOperation response does not match the requested operation identity")
	}
	return PrepareOperationResult{Execution: execution, Operation: operation, Created: response.Created}, nil
}

func (client *CoreConnectionClient) BeginOperationDispatch(ctx context.Context, request BeginOperationDispatchRequest) (BeginOperationDispatchResult, error) {
	contractRequest := corecontract.BeginOperationDispatchRequest{
		OperationID:              request.OperationID,
		ExecutionID:              request.ExecutionID,
		RunID:                    request.RunID,
		RunAttemptID:             request.RunAttemptID,
		HolderID:                 request.HolderID,
		RunAttemptGeneration:     request.RunAttemptGeneration,
		ConnectionGeneration:     request.ConnectionGeneration,
		TargetKind:               string(request.Target.Kind),
		TargetID:                 request.Target.ID,
		TargetGeneration:         request.Target.Generation,
		ExpectedExecutionVersion: request.ExpectedExecutionVersion,
		ExpectedOperationVersion: request.ExpectedOperationVersion,
		PolicyContext:            copyCoreJSON(request.PolicyContext),
		OperationPlan:            copyCoreJSON(request.OperationPlan),
		Params:                   copyCoreJSON(request.Params),
		Record:                   contractExecutionTransitionRecord(request.Record),
	}
	var response corecontract.BeginOperationDispatchResponse
	path := corecontract.BeginOperationDispatchPath(request.ExecutionID, request.OperationID)
	if err := client.post(ctx, path, contractRequest, &response, http.StatusOK); err != nil {
		return BeginOperationDispatchResult{}, err
	}
	execution, operation, err := gatewayExecutionAndOperation(response.Execution, response.Operation)
	if err != nil {
		return BeginOperationDispatchResult{}, fmt.Errorf("validate core BeginOperationDispatch response: %w", err)
	}
	if operation.OperationID != request.OperationID || operation.ExecutionID != request.ExecutionID {
		return BeginOperationDispatchResult{}, errors.New("core BeginOperationDispatch response does not match the requested operation identity")
	}
	if response.Began && operation.Status != "dispatching" {
		return BeginOperationDispatchResult{}, errors.New("core granted dispatch without returning a dispatching operation")
	}
	if response.Began && request.Target.Kind == "" && operation.ConnectionGeneration != request.ConnectionGeneration {
		return BeginOperationDispatchResult{}, errors.New("core granted agentx dispatch without returning the matching connection generation")
	}
	if response.Began && request.Target.Kind != "" && operation.Target != request.Target {
		return BeginOperationDispatchResult{}, errors.New("core granted dispatch without returning the matching target generation")
	}
	return BeginOperationDispatchResult{Execution: execution, Operation: operation, Began: response.Began}, nil
}

func (client *CoreConnectionClient) AcknowledgeOperation(ctx context.Context, request AcknowledgeOperationRequest) (AcknowledgeOperationResult, error) {
	contractRequest := corecontract.AcknowledgeOperationRequest{
		OperationID:              request.OperationID,
		ExecutionID:              request.ExecutionID,
		RunID:                    request.RunID,
		RunAttemptID:             request.RunAttemptID,
		RunAttemptGeneration:     request.RunAttemptGeneration,
		ConnectionGeneration:     request.ConnectionGeneration,
		TargetKind:               string(request.Target.Kind),
		TargetID:                 request.Target.ID,
		TargetGeneration:         request.Target.Generation,
		ExpectedExecutionVersion: request.ExpectedExecutionVersion,
		ExpectedOperationVersion: request.ExpectedOperationVersion,
		Acknowledgement:          copyCoreJSON(request.Acknowledgement),
		Record:                   contractExecutionTransitionRecord(request.Record),
	}
	var response corecontract.AcknowledgeOperationResponse
	path := corecontract.AcknowledgeOperationPath(request.ExecutionID, request.OperationID)
	if err := client.post(ctx, path, contractRequest, &response, http.StatusOK); err != nil {
		return AcknowledgeOperationResult{}, err
	}
	execution, operation, err := gatewayExecutionAndOperation(response.Execution, response.Operation)
	if err != nil {
		return AcknowledgeOperationResult{}, fmt.Errorf("validate core AcknowledgeOperation response: %w", err)
	}
	if operation.OperationID != request.OperationID || operation.ExecutionID != request.ExecutionID {
		return AcknowledgeOperationResult{}, errors.New("core AcknowledgeOperation response does not match the requested operation identity")
	}
	return AcknowledgeOperationResult{Execution: execution, Operation: operation, Changed: response.Changed}, nil
}

func (client *CoreConnectionClient) CompleteOperation(ctx context.Context, request CompleteOperationRequest) (CompleteOperationResult, error) {
	contractRequest := corecontract.CompleteOperationRequest{
		OperationID:              request.OperationID,
		ExecutionID:              request.ExecutionID,
		RunID:                    request.RunID,
		RunAttemptID:             request.RunAttemptID,
		RunAttemptGeneration:     request.RunAttemptGeneration,
		ConnectionGeneration:     request.ConnectionGeneration,
		TargetKind:               string(request.Target.Kind),
		TargetID:                 request.Target.ID,
		TargetGeneration:         request.Target.Generation,
		ExpectedExecutionVersion: request.ExpectedExecutionVersion,
		ExpectedOperationVersion: request.ExpectedOperationVersion,
		TerminalStatus:           request.TerminalStatus,
		Result:                   copyCoreJSON(request.Result),
		Record:                   contractExecutionTransitionRecord(request.Record),
	}
	var response corecontract.CompleteOperationResponse
	path := corecontract.CompleteOperationPath(request.ExecutionID, request.OperationID)
	if err := client.post(ctx, path, contractRequest, &response, http.StatusOK); err != nil {
		return CompleteOperationResult{}, err
	}
	execution, operation, err := gatewayExecutionAndOperation(response.Execution, response.Operation)
	if err != nil {
		return CompleteOperationResult{}, fmt.Errorf("validate core CompleteOperation response: %w", err)
	}
	if operation.OperationID != request.OperationID || operation.ExecutionID != request.ExecutionID {
		return CompleteOperationResult{}, errors.New("core CompleteOperation response does not match the requested operation identity")
	}
	return CompleteOperationResult{Execution: execution, Operation: operation, Changed: response.Changed}, nil
}

func (client *CoreConnectionClient) SkipOperation(ctx context.Context, request SkipOperationRequest) (SkipOperationResult, error) {
	contractRequest := corecontract.SkipOperationRequest{
		OperationID:              request.OperationID,
		ExecutionID:              request.ExecutionID,
		RunID:                    request.RunID,
		RunAttemptID:             request.RunAttemptID,
		HolderID:                 request.HolderID,
		RunAttemptGeneration:     request.RunAttemptGeneration,
		ExpectedExecutionVersion: request.ExpectedExecutionVersion,
		ExpectedOperationVersion: request.ExpectedOperationVersion,
		Result:                   copyCoreJSON(request.Result),
		Record:                   contractExecutionTransitionRecord(request.Record),
	}
	var response corecontract.SkipOperationResponse
	path := corecontract.SkipOperationPath(request.ExecutionID, request.OperationID)
	if err := client.post(ctx, path, contractRequest, &response, http.StatusOK); err != nil {
		return SkipOperationResult{}, err
	}
	execution, operation, err := gatewayExecutionAndOperation(response.Execution, response.Operation)
	if err != nil {
		return SkipOperationResult{}, fmt.Errorf("validate core SkipOperation response: %w", err)
	}
	if operation.OperationID != request.OperationID || operation.ExecutionID != request.ExecutionID {
		return SkipOperationResult{}, errors.New("core SkipOperation response does not match the requested operation identity")
	}
	if operation.Status != "skipped" || operation.ConnectionGeneration != 0 || operation.DispatchedAt != nil ||
		operation.AcknowledgementDigest != nil || operation.AcknowledgedAt != nil || operation.TerminalResultDigest == nil || operation.TerminalAt == nil {
		return SkipOperationResult{}, errors.New("core SkipOperation response is not a non-dispatched terminal operation")
	}
	return SkipOperationResult{Execution: execution, Operation: operation, Changed: response.Changed}, nil
}

func (client *CoreConnectionClient) CompleteExecution(ctx context.Context, request CompleteExecutionRequest) (CompleteExecutionResult, error) {
	contractRequest := corecontract.CompleteExecutionRequest{
		ExecutionID:              request.ExecutionID,
		RunID:                    request.RunID,
		RunAttemptID:             request.RunAttemptID,
		RunAttemptGeneration:     request.RunAttemptGeneration,
		ExpectedExecutionVersion: request.ExpectedExecutionVersion,
		TerminalStatus:           request.TerminalStatus,
		Result:                   copyCoreJSON(request.Result),
		Record:                   contractExecutionTransitionRecord(request.Record),
	}
	var response corecontract.CompleteExecutionResponse
	if err := client.post(ctx, corecontract.CompleteExecutionPath(request.ExecutionID), contractRequest, &response, http.StatusOK); err != nil {
		return CompleteExecutionResult{}, err
	}
	execution, err := gatewayExecutionState(response.Execution)
	if err != nil {
		return CompleteExecutionResult{}, fmt.Errorf("validate core CompleteExecution response: %w", err)
	}
	if execution.ExecutionID != request.ExecutionID {
		return CompleteExecutionResult{}, errors.New("core CompleteExecution response does not match the requested execution identity")
	}
	return CompleteExecutionResult{Execution: execution, Changed: response.Changed}, nil
}

func contractExecutionTransitionRecord(record ExecutionTransitionRecord) corecontract.TransitionRecord {
	return corecontract.TransitionRecord{
		EventID:            record.EventID,
		ProducerInstanceID: record.ProducerInstanceID,
		ProducerSeq:        record.ProducerSeq,
		OutboxID:           record.OutboxID,
	}
}

func copyCoreJSON(raw json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), raw...)
}

func gatewayExecutionAndOperation(executionContract corecontract.ExecutionState, operationContract corecontract.ExecutionOperationState) (ExecutionState, ExecutionOperationState, error) {
	execution, err := gatewayExecutionState(executionContract)
	if err != nil {
		return ExecutionState{}, ExecutionOperationState{}, err
	}
	operation, err := gatewayExecutionOperationState(operationContract)
	if err != nil {
		return ExecutionState{}, ExecutionOperationState{}, err
	}
	if operation.ExecutionID != execution.ExecutionID {
		return ExecutionState{}, ExecutionOperationState{}, errors.New("operation belongs to a different execution")
	}
	if operation.Target.Kind == "" {
		operation.Target = execution.Target
	} else {
		operation.Target.EnvironmentID = execution.EnvironmentID
		if execution.Target.Kind != "" && (operation.Target.Kind != execution.Target.Kind || operation.Target.ID != execution.Target.ID ||
			(execution.Target.Generation > 0 && operation.Target.Generation > 0 && operation.Target.Generation != execution.Target.Generation)) {
			return ExecutionState{}, ExecutionOperationState{}, errors.New("operation dispatch target differs from execution target")
		}
	}
	return execution, operation, nil
}

func gatewayExecutionState(source corecontract.ExecutionState) (ExecutionState, error) {
	argumentsDigest, err := gatewayCanonicalDigest(source.ArgumentsDigest, "execution-arguments")
	if err != nil {
		return ExecutionState{}, fmt.Errorf("arguments digest: %w", err)
	}
	toolSchemaDigest, err := gatewayCanonicalDigest(source.ToolSchemaDigest, "tool-schema")
	if err != nil {
		return ExecutionState{}, fmt.Errorf("tool schema digest: %w", err)
	}
	operationPlanDigest, err := gatewayCanonicalDigest(source.OperationPlanDigest, "operation-plan")
	if err != nil {
		return ExecutionState{}, fmt.Errorf("operation plan digest: %w", err)
	}
	policyContextDigest, err := gatewayCanonicalDigest(source.PolicyContextDigest, "policy-context")
	if err != nil {
		return ExecutionState{}, fmt.Errorf("policy context digest: %w", err)
	}
	terminalResultDigest, err := gatewayOptionalCanonicalDigest(source.TerminalResultDigest, "execution-result")
	if err != nil {
		return ExecutionState{}, fmt.Errorf("terminal result digest: %w", err)
	}
	if source.ExecutionID == "" || source.RunID == "" || source.RunAttemptID == "" || source.EnvironmentID == "" {
		return ExecutionState{}, errors.New("required execution identity is empty")
	}
	target, err := gatewayExecutionTarget(source.TargetKind, source.TargetID, source.TargetGeneration, source.EnvironmentID, source.ExecutorID, 0)
	if err != nil {
		return ExecutionState{}, fmt.Errorf("dispatch target: %w", err)
	}
	if source.RunAttemptGeneration < 1 || source.OperationCount < 1 || source.OperationCount > 256 || source.Version < 1 {
		return ExecutionState{}, errors.New("execution generation, operation count, or version is invalid")
	}
	if !validCoreExecutionStatus(source.Status) {
		return ExecutionState{}, fmt.Errorf("unsupported execution status %q", source.Status)
	}
	return ExecutionState{
		ExecutionID:          source.ExecutionID,
		RunID:                source.RunID,
		RunAttemptID:         source.RunAttemptID,
		RunAttemptGeneration: source.RunAttemptGeneration,
		AppServerToolCallID:  source.AppServerToolCallID,
		ExecutorID:           source.ExecutorID,
		EnvironmentID:        source.EnvironmentID,
		Target:               target,
		ToolName:             source.ToolName,
		ToolVersion:          source.ToolVersion,
		MapperVersion:        source.MapperVersion,
		PolicyVersion:        source.PolicyVersion,
		PolicyDecision:       source.PolicyDecision,
		OperationCount:       source.OperationCount,
		ArgumentsDigest:      argumentsDigest,
		ToolSchemaDigest:     toolSchemaDigest,
		OperationPlanDigest:  operationPlanDigest,
		PolicyContextDigest:  policyContextDigest,
		Status:               source.Status,
		DispatchedAt:         source.DispatchedAt,
		TerminalResultDigest: terminalResultDigest,
		TerminalAt:           source.TerminalAt,
		Version:              source.Version,
		CreatedAt:            source.CreatedAt,
		UpdatedAt:            source.UpdatedAt,
	}, nil
}

func gatewayExecutionOperationState(source corecontract.ExecutionOperationState) (ExecutionOperationState, error) {
	paramsDigest, err := gatewayCanonicalDigest(source.ParamsDigest, "operation-params")
	if err != nil {
		return ExecutionOperationState{}, fmt.Errorf("params digest: %w", err)
	}
	acknowledgementDigest, err := gatewayOptionalCanonicalDigest(source.AcknowledgementDigest, "operation-ack")
	if err != nil {
		return ExecutionOperationState{}, fmt.Errorf("acknowledgement digest: %w", err)
	}
	terminalResultDigest, err := gatewayOptionalCanonicalDigest(source.TerminalResultDigest, "operation-result")
	if err != nil {
		return ExecutionOperationState{}, fmt.Errorf("terminal result digest: %w", err)
	}
	if source.OperationID == "" || source.ExecutionID == "" || source.MutationKey == "" {
		return ExecutionOperationState{}, errors.New("required operation identity is empty")
	}
	if source.Ordinal < 1 || source.Ordinal > 256 || source.Version < 1 || source.ConnectionGeneration < 0 {
		return ExecutionOperationState{}, errors.New("operation ordinal, version, or connection generation is invalid")
	}
	if !validCoreOperationStatus(source.Status) {
		return ExecutionOperationState{}, fmt.Errorf("unsupported operation status %q", source.Status)
	}
	target, err := gatewayExecutionTarget(source.TargetKind, source.TargetID, source.TargetGeneration, "operation-environment", "", source.ConnectionGeneration)
	if err != nil {
		return ExecutionOperationState{}, fmt.Errorf("dispatch target: %w", err)
	}
	if source.Status == "skipped" && (source.ConnectionGeneration != 0 || source.DispatchedAt != nil ||
		source.AcknowledgementDigest != nil || source.AcknowledgedAt != nil || source.TerminalResultDigest == nil || source.TerminalAt == nil) {
		return ExecutionOperationState{}, errors.New("skipped operation crossed the dispatch boundary or lacks terminal evidence")
	}
	return ExecutionOperationState{
		OperationID:           source.OperationID,
		ExecutionID:           source.ExecutionID,
		Ordinal:               source.Ordinal,
		Kind:                  source.Kind,
		EffectClass:           source.EffectClass,
		MutationKey:           source.MutationKey,
		ParamsDigest:          paramsDigest,
		Status:                source.Status,
		ConnectionGeneration:  source.ConnectionGeneration,
		Target:                target,
		AcknowledgementDigest: acknowledgementDigest,
		TerminalResultDigest:  terminalResultDigest,
		DispatchedAt:          source.DispatchedAt,
		AcknowledgedAt:        source.AcknowledgedAt,
		TerminalAt:            source.TerminalAt,
		Version:               source.Version,
		CreatedAt:             source.CreatedAt,
		UpdatedAt:             source.UpdatedAt,
	}, nil
}

func gatewayExecutionTarget(kindText, targetID string, generation int64, environmentID, legacyExecutorID string, legacyConnectionGeneration int64) (executionbackend.Target, error) {
	kind := executionbackend.Kind(kindText)
	if kind == "" {
		if legacyExecutorID == "" {
			return executionbackend.Target{}, nil
		}
		kind = executionbackend.KindAgentX
		targetID = legacyExecutorID
		generation = legacyConnectionGeneration
	}
	if err := kind.Validate(); err != nil {
		return executionbackend.Target{}, err
	}
	if targetID == "" || generation < 0 {
		return executionbackend.Target{}, errors.New("target ID is empty or generation is negative")
	}
	if kind == executionbackend.KindAgentX {
		if legacyExecutorID != "" && targetID != legacyExecutorID {
			return executionbackend.Target{}, errors.New("agentx target differs from executor projection")
		}
		if legacyConnectionGeneration > 0 && generation != legacyConnectionGeneration {
			return executionbackend.Target{}, errors.New("agentx target generation differs from connection projection")
		}
	} else if legacyConnectionGeneration != 0 {
		return executionbackend.Target{}, errors.New("TAE target carries an agentx connection projection")
	}
	return executionbackend.Target{Kind: kind, ID: targetID, Generation: generation, EnvironmentID: environmentID}, nil
}

func gatewayCanonicalDigest(source corecontract.CanonicalJSONDigest, expectedDomain string) (CanonicalDigest, error) {
	if source.Domain != expectedDomain {
		return CanonicalDigest{}, fmt.Errorf("domain is %q, want %q", source.Domain, expectedDomain)
	}
	if source.CanonicalizerVersion != coreCanonicalizerRFC8785V1 {
		return CanonicalDigest{}, fmt.Errorf("canonicalizer is %q", source.CanonicalizerVersion)
	}
	raw, err := hex.DecodeString(source.SHA256)
	if err != nil || len(raw) != 32 {
		return CanonicalDigest{}, errors.New("SHA-256 is not 32 lowercase hexadecimal bytes")
	}
	if hex.EncodeToString(raw) != source.SHA256 {
		return CanonicalDigest{}, errors.New("SHA-256 is not canonical lowercase hexadecimal")
	}
	var digest [32]byte
	copy(digest[:], raw)
	return CanonicalDigest{Domain: source.Domain, CanonicalizerVersion: source.CanonicalizerVersion, SHA256: digest}, nil
}

func gatewayOptionalCanonicalDigest(source *corecontract.CanonicalJSONDigest, expectedDomain string) (*CanonicalDigest, error) {
	if source == nil {
		return nil, nil
	}
	digest, err := gatewayCanonicalDigest(*source, expectedDomain)
	if err != nil {
		return nil, err
	}
	return &digest, nil
}

func validCoreExecutionStatus(status string) bool {
	switch status {
	case "created", "pending_approval", "approved", "denied", "expired", "dispatching", "running", "cancelling", "succeeded", "failed", "cancelled", "unknown":
		return true
	default:
		return false
	}
}

func validCoreOperationStatus(status string) bool {
	switch status {
	case "prepared", "dispatching", "acknowledged", "succeeded", "failed", "cancelled", "unknown", "skipped":
		return true
	default:
		return false
	}
}
