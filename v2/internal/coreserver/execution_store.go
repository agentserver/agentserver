package coreserver

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/coredb"
	"github.com/google/jsonschema-go/jsonschema"
)

type ExecutionStateStore interface {
	PrepareExecution(context.Context, coredb.PrepareExecutionCommand) (coredb.PrepareExecutionResult, error)
	PrepareOperation(context.Context, coredb.PrepareOperationCommand) (coredb.PrepareOperationResult, error)
	BeginOperationDispatch(context.Context, coredb.BeginOperationDispatchCommand) (coredb.BeginOperationDispatchResult, error)
	AcknowledgeOperation(context.Context, coredb.AcknowledgeOperationCommand) (coredb.AcknowledgeOperationResult, error)
	CompleteOperation(context.Context, coredb.CompleteOperationCommand) (coredb.CompleteOperationResult, error)
	SkipOperation(context.Context, coredb.SkipOperationCommand) (coredb.SkipOperationResult, error)
	CompleteExecution(context.Context, coredb.CompleteExecutionCommand) (coredb.CompleteExecutionResult, error)
}

type StateStoreExecutionCommands struct {
	Store ExecutionStateStore
}

var _ ExecutionCommands = StateStoreExecutionCommands{}

func (commands StateStoreExecutionCommands) PrepareExecution(ctx context.Context, request corecontract.PrepareExecutionRequest) (corecontract.PrepareExecutionResponse, error) {
	if commands.Store == nil {
		return corecontract.PrepareExecutionResponse{}, errors.New("nil core state store")
	}
	argumentsHash, toolSchemaHash, operationPlanHash, policyContextHash, err := prepareExecutionHashes(request)
	if err != nil {
		return corecontract.PrepareExecutionResponse{}, executionCommandConversionError("PrepareExecution", "execution", request.ExecutionID, err)
	}
	result, err := commands.Store.PrepareExecution(ctx, coredb.PrepareExecutionCommand{
		ExecutionID:            request.ExecutionID,
		RunID:                  request.RunID,
		AttemptID:              request.RunAttemptID,
		HolderID:               request.HolderID,
		Generation:             request.RunAttemptGeneration,
		ExpectedRunVersion:     request.ExpectedRunVersion,
		ExpectedAttemptVersion: request.ExpectedRunAttemptVersion,
		AppServerToolCallID:    request.AppServerToolCallID,
		ExecutorID:             request.ExecutorID,
		EnvID:                  request.EnvironmentID,
		Target: coredb.DispatchTarget{
			Kind: request.TargetKind, ID: request.TargetID, Generation: request.TargetGeneration,
		},
		ToolName:          request.ToolName,
		ToolVersion:       request.ToolVersion,
		MapperVersion:     request.MapperVersion,
		PolicyVersion:     request.PolicyVersion,
		OperationCount:    request.OperationCount,
		ArgumentsHash:     argumentsHash,
		ToolSchemaHash:    toolSchemaHash,
		OperationPlanHash: operationPlanHash,
		PolicyContextHash: policyContextHash,
		PolicyDecision:    request.PolicyDecision,
		Record:            databaseTransitionRecord(request.Record),
	})
	if err != nil {
		return corecontract.PrepareExecutionResponse{}, err
	}
	return corecontract.PrepareExecutionResponse{Execution: contractExecution(result.Execution), Created: result.Created}, nil
}

func (commands StateStoreExecutionCommands) PrepareOperation(ctx context.Context, request corecontract.PrepareOperationRequest) (corecontract.PrepareOperationResponse, error) {
	if commands.Store == nil {
		return corecontract.PrepareOperationResponse{}, errors.New("nil core state store")
	}
	paramsHash, err := hashCanonicalJSONObject(coredb.HashDomainOperationParams, request.Params)
	if err != nil {
		return corecontract.PrepareOperationResponse{}, executionCommandConversionError("PrepareOperation", "operation", request.OperationID, fmt.Errorf("params: %w", err))
	}
	result, err := commands.Store.PrepareOperation(ctx, coredb.PrepareOperationCommand{
		OperationID:              request.OperationID,
		ExecutionID:              request.ExecutionID,
		RunID:                    request.RunID,
		AttemptID:                request.RunAttemptID,
		HolderID:                 request.HolderID,
		Generation:               request.RunAttemptGeneration,
		ExpectedExecutionVersion: request.ExpectedExecutionVersion,
		Ordinal:                  request.Ordinal,
		Kind:                     request.Kind,
		EffectClass:              request.EffectClass,
		MutationKey:              request.MutationKey,
		ParamsHash:               paramsHash,
		Record:                   databaseTransitionRecord(request.Record),
	})
	if err != nil {
		return corecontract.PrepareOperationResponse{}, err
	}
	return corecontract.PrepareOperationResponse{
		Execution: contractExecution(result.Execution),
		Operation: contractExecutionOperation(result.Operation),
		Created:   result.Created,
	}, nil
}

func (commands StateStoreExecutionCommands) BeginOperationDispatch(ctx context.Context, request corecontract.BeginOperationDispatchRequest) (corecontract.BeginOperationDispatchResponse, error) {
	if commands.Store == nil {
		return corecontract.BeginOperationDispatchResponse{}, errors.New("nil core state store")
	}
	policyContextHash, err := hashCanonicalJSONObject(coredb.HashDomainPolicyContext, request.PolicyContext)
	if err != nil {
		return corecontract.BeginOperationDispatchResponse{}, executionCommandConversionError("BeginOperationDispatch", "operation", request.OperationID, fmt.Errorf("policy_context: %w", err))
	}
	operationPlanHash, err := hashCanonicalJSONObject(coredb.HashDomainOperationPlan, request.OperationPlan)
	if err != nil {
		return corecontract.BeginOperationDispatchResponse{}, executionCommandConversionError("BeginOperationDispatch", "operation", request.OperationID, fmt.Errorf("operation_plan: %w", err))
	}
	paramsHash, err := hashCanonicalJSONObject(coredb.HashDomainOperationParams, request.Params)
	if err != nil {
		return corecontract.BeginOperationDispatchResponse{}, executionCommandConversionError("BeginOperationDispatch", "operation", request.OperationID, fmt.Errorf("params: %w", err))
	}
	result, err := commands.Store.BeginOperationDispatch(ctx, coredb.BeginOperationDispatchCommand{
		OperationID:          request.OperationID,
		ExecutionID:          request.ExecutionID,
		RunID:                request.RunID,
		AttemptID:            request.RunAttemptID,
		HolderID:             request.HolderID,
		Generation:           request.RunAttemptGeneration,
		ConnectionGeneration: request.ConnectionGeneration,
		Target: coredb.DispatchTarget{
			Kind: request.TargetKind, ID: request.TargetID, Generation: request.TargetGeneration,
		},
		ExpectedExecutionVersion: request.ExpectedExecutionVersion,
		ExpectedOperationVersion: request.ExpectedOperationVersion,
		PolicyContextHash:        policyContextHash,
		OperationPlanHash:        operationPlanHash,
		ParamsHash:               paramsHash,
		Record:                   databaseTransitionRecord(request.Record),
	})
	if err != nil {
		return corecontract.BeginOperationDispatchResponse{}, err
	}
	return corecontract.BeginOperationDispatchResponse{
		Execution: contractExecution(result.Execution),
		Operation: contractExecutionOperation(result.Operation),
		Began:     result.Began,
	}, nil
}

func (commands StateStoreExecutionCommands) AcknowledgeOperation(ctx context.Context, request corecontract.AcknowledgeOperationRequest) (corecontract.AcknowledgeOperationResponse, error) {
	if commands.Store == nil {
		return corecontract.AcknowledgeOperationResponse{}, errors.New("nil core state store")
	}
	acknowledgementHash, err := hashCanonicalJSONObject(coredb.HashDomainOperationAck, request.Acknowledgement)
	if err != nil {
		return corecontract.AcknowledgeOperationResponse{}, executionCommandConversionError("AcknowledgeOperation", "operation", request.OperationID, fmt.Errorf("acknowledgement: %w", err))
	}
	result, err := commands.Store.AcknowledgeOperation(ctx, coredb.AcknowledgeOperationCommand{
		OperationID:          request.OperationID,
		ExecutionID:          request.ExecutionID,
		RunID:                request.RunID,
		AttemptID:            request.RunAttemptID,
		Generation:           request.RunAttemptGeneration,
		ConnectionGeneration: request.ConnectionGeneration,
		Target: coredb.DispatchTarget{
			Kind: request.TargetKind, ID: request.TargetID, Generation: request.TargetGeneration,
		},
		ExpectedExecutionVersion: request.ExpectedExecutionVersion,
		ExpectedOperationVersion: request.ExpectedOperationVersion,
		AcknowledgementHash:      acknowledgementHash,
		Record:                   databaseTransitionRecord(request.Record),
	})
	if err != nil {
		return corecontract.AcknowledgeOperationResponse{}, err
	}
	return corecontract.AcknowledgeOperationResponse{
		Execution: contractExecution(result.Execution),
		Operation: contractExecutionOperation(result.Operation),
		Changed:   result.Changed,
	}, nil
}

func (commands StateStoreExecutionCommands) CompleteOperation(ctx context.Context, request corecontract.CompleteOperationRequest) (corecontract.CompleteOperationResponse, error) {
	if commands.Store == nil {
		return corecontract.CompleteOperationResponse{}, errors.New("nil core state store")
	}
	resultHash, err := hashCanonicalJSONObject(coredb.HashDomainOperationResult, request.Result)
	if err != nil {
		return corecontract.CompleteOperationResponse{}, executionCommandConversionError("CompleteOperation", "operation", request.OperationID, fmt.Errorf("result: %w", err))
	}
	result, err := commands.Store.CompleteOperation(ctx, coredb.CompleteOperationCommand{
		OperationID:          request.OperationID,
		ExecutionID:          request.ExecutionID,
		RunID:                request.RunID,
		AttemptID:            request.RunAttemptID,
		Generation:           request.RunAttemptGeneration,
		ConnectionGeneration: request.ConnectionGeneration,
		Target: coredb.DispatchTarget{
			Kind: request.TargetKind, ID: request.TargetID, Generation: request.TargetGeneration,
		},
		ExpectedExecutionVersion: request.ExpectedExecutionVersion,
		ExpectedOperationVersion: request.ExpectedOperationVersion,
		TerminalStatus:           request.TerminalStatus,
		ResultHash:               resultHash,
		Record:                   databaseTransitionRecord(request.Record),
	})
	if err != nil {
		return corecontract.CompleteOperationResponse{}, err
	}
	return corecontract.CompleteOperationResponse{
		Execution: contractExecution(result.Execution),
		Operation: contractExecutionOperation(result.Operation),
		Changed:   result.Changed,
	}, nil
}

func (commands StateStoreExecutionCommands) SkipOperation(ctx context.Context, request corecontract.SkipOperationRequest) (corecontract.SkipOperationResponse, error) {
	if commands.Store == nil {
		return corecontract.SkipOperationResponse{}, errors.New("nil core state store")
	}
	resultHash, err := hashCanonicalJSONObject(coredb.HashDomainOperationResult, request.Result)
	if err != nil {
		return corecontract.SkipOperationResponse{}, executionCommandConversionError("SkipOperation", "operation", request.OperationID, fmt.Errorf("result: %w", err))
	}
	result, err := commands.Store.SkipOperation(ctx, coredb.SkipOperationCommand{
		OperationID:              request.OperationID,
		ExecutionID:              request.ExecutionID,
		RunID:                    request.RunID,
		AttemptID:                request.RunAttemptID,
		HolderID:                 request.HolderID,
		Generation:               request.RunAttemptGeneration,
		ExpectedExecutionVersion: request.ExpectedExecutionVersion,
		ExpectedOperationVersion: request.ExpectedOperationVersion,
		ResultHash:               resultHash,
		Record:                   databaseTransitionRecord(request.Record),
	})
	if err != nil {
		return corecontract.SkipOperationResponse{}, err
	}
	return corecontract.SkipOperationResponse{
		Execution: contractExecution(result.Execution),
		Operation: contractExecutionOperation(result.Operation),
		Changed:   result.Changed,
	}, nil
}

func (commands StateStoreExecutionCommands) CompleteExecution(ctx context.Context, request corecontract.CompleteExecutionRequest) (corecontract.CompleteExecutionResponse, error) {
	if commands.Store == nil {
		return corecontract.CompleteExecutionResponse{}, errors.New("nil core state store")
	}
	resultHash, err := hashCanonicalJSONObject(coredb.HashDomainExecutionResult, request.Result)
	if err != nil {
		return corecontract.CompleteExecutionResponse{}, executionCommandConversionError("CompleteExecution", "execution", request.ExecutionID, fmt.Errorf("result: %w", err))
	}
	result, err := commands.Store.CompleteExecution(ctx, coredb.CompleteExecutionCommand{
		ExecutionID:              request.ExecutionID,
		RunID:                    request.RunID,
		AttemptID:                request.RunAttemptID,
		Generation:               request.RunAttemptGeneration,
		ExpectedExecutionVersion: request.ExpectedExecutionVersion,
		TerminalStatus:           request.TerminalStatus,
		ResultHash:               resultHash,
		Record:                   databaseTransitionRecord(request.Record),
	})
	if err != nil {
		return corecontract.CompleteExecutionResponse{}, err
	}
	return corecontract.CompleteExecutionResponse{Execution: contractExecution(result.Execution), Changed: result.Changed}, nil
}

func prepareExecutionHashes(request corecontract.PrepareExecutionRequest) (
	argumentsHash coredb.CanonicalJSONHash,
	toolSchemaHash coredb.CanonicalJSONHash,
	operationPlanHash coredb.CanonicalJSONHash,
	policyContextHash coredb.CanonicalJSONHash,
	err error,
) {
	var resolvedSchema *jsonschema.Resolved
	_, toolSchemaHash, err = coredb.ValidateAndHashCanonicalJSON(
		coredb.HashDomainToolSchema,
		request.ToolSchema,
		func(value any) error {
			resolved, resolveErr := resolveToolInputSchema(value)
			if resolveErr == nil {
				resolvedSchema = resolved
			}
			return resolveErr
		},
	)
	if err != nil {
		return argumentsHash, toolSchemaHash, operationPlanHash, policyContextHash, fmt.Errorf("tool_schema: %w", err)
	}
	_, argumentsHash, err = coredb.ValidateAndHashCanonicalJSON(
		coredb.HashDomainExecutionArguments,
		request.Arguments,
		func(value any) error {
			if _, ok := value.(map[string]any); !ok {
				return errors.New("tool arguments must be a JSON object")
			}
			return resolvedSchema.Validate(value)
		},
	)
	if err != nil {
		return argumentsHash, toolSchemaHash, operationPlanHash, policyContextHash, fmt.Errorf("arguments: %w", err)
	}
	operationPlanHash, err = hashCanonicalJSONObject(coredb.HashDomainOperationPlan, request.OperationPlan)
	if err != nil {
		return argumentsHash, toolSchemaHash, operationPlanHash, policyContextHash, fmt.Errorf("operation_plan: %w", err)
	}
	policyContextHash, err = hashCanonicalJSONObject(coredb.HashDomainPolicyContext, request.PolicyContext)
	if err != nil {
		return argumentsHash, toolSchemaHash, operationPlanHash, policyContextHash, fmt.Errorf("policy_context: %w", err)
	}
	return argumentsHash, toolSchemaHash, operationPlanHash, policyContextHash, nil
}

func hashCanonicalJSONObject(domain coredb.CanonicalHashDomain, raw json.RawMessage) (coredb.CanonicalJSONHash, error) {
	_, hash, err := coredb.ValidateAndHashCanonicalJSON(domain, raw, func(value any) error {
		if _, ok := value.(map[string]any); !ok {
			return errors.New("value must be a JSON object")
		}
		return nil
	})
	return hash, err
}

func resolveToolInputSchema(value any) (*jsonschema.Resolved, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("tool input schema root must be an object")
	}
	if rootType, ok := object["type"].(string); !ok || rootType != "object" {
		return nil, errors.New("tool input schema must declare root type object")
	}
	if err := validateExecutionSchemaKeywords(object, "$", 0); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode tool input schema: %w", err)
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, fmt.Errorf("decode tool input schema: %w", err)
	}
	resolved, err := schema.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
	if err != nil {
		return nil, fmt.Errorf("resolve tool input schema: %w", err)
	}
	return resolved, nil
}

var supportedExecutionSchemaKeywords = map[string]struct{}{
	"$id": {}, "$schema": {}, "$ref": {}, "$comment": {}, "$defs": {}, "definitions": {},
	"dependencies": {}, "$anchor": {}, "$dynamicAnchor": {}, "$dynamicRef": {}, "$vocabulary": {},
	"title": {}, "description": {}, "default": {}, "deprecated": {}, "readOnly": {}, "writeOnly": {}, "examples": {},
	"type": {}, "enum": {}, "const": {}, "multipleOf": {}, "minimum": {}, "maximum": {},
	"exclusiveMinimum": {}, "exclusiveMaximum": {}, "minLength": {}, "maxLength": {}, "pattern": {},
	"prefixItems": {}, "items": {}, "minItems": {}, "maxItems": {}, "additionalItems": {}, "uniqueItems": {},
	"contains": {}, "minContains": {}, "maxContains": {}, "unevaluatedItems": {},
	"minProperties": {}, "maxProperties": {}, "required": {}, "dependentRequired": {}, "properties": {},
	"patternProperties": {}, "additionalProperties": {}, "propertyNames": {}, "unevaluatedProperties": {},
	"allOf": {}, "anyOf": {}, "oneOf": {}, "not": {}, "if": {}, "then": {}, "else": {}, "dependentSchemas": {},
	"contentEncoding": {}, "contentMediaType": {}, "contentSchema": {}, "format": {},
}

func validateExecutionSchemaKeywords(schema map[string]any, path string, depth int) error {
	if depth > 64 {
		return errors.New("tool input schema nesting exceeds 64")
	}
	for keyword := range schema {
		if _, ok := supportedExecutionSchemaKeywords[keyword]; !ok {
			return fmt.Errorf("unsupported tool input schema keyword %q at %s", keyword, path)
		}
	}
	for _, keyword := range []string{"$defs", "definitions", "properties", "patternProperties", "dependentSchemas"} {
		if entries, ok := schema[keyword].(map[string]any); ok {
			for name, child := range entries {
				if err := validateExecutionSchemaValue(child, path+"/"+keyword+"/"+name, depth+1); err != nil {
					return err
				}
			}
		}
	}
	if entries, ok := schema["dependencies"].(map[string]any); ok {
		for name, child := range entries {
			if _, isArray := child.([]any); isArray {
				continue
			}
			if err := validateExecutionSchemaValue(child, path+"/dependencies/"+name, depth+1); err != nil {
				return err
			}
		}
	}
	for _, keyword := range []string{
		"items", "additionalItems", "contains", "unevaluatedItems", "additionalProperties", "propertyNames",
		"unevaluatedProperties", "not", "if", "then", "else", "contentSchema",
	} {
		if child, exists := schema[keyword]; exists {
			if err := validateExecutionSchemaValue(child, path+"/"+keyword, depth+1); err != nil {
				return err
			}
		}
	}
	for _, keyword := range []string{"prefixItems", "allOf", "anyOf", "oneOf"} {
		if children, ok := schema[keyword].([]any); ok {
			for index, child := range children {
				if err := validateExecutionSchemaValue(child, fmt.Sprintf("%s/%s/%d", path, keyword, index), depth+1); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func validateExecutionSchemaValue(value any, path string, depth int) error {
	if _, ok := value.(bool); ok {
		return nil
	}
	object, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("tool input schema at %s must be an object or boolean", path)
	}
	return validateExecutionSchemaKeywords(object, path, depth)
}

func databaseTransitionRecord(record corecontract.TransitionRecord) coredb.TransitionRecord {
	return coredb.TransitionRecord{
		EventID:            record.EventID,
		ProducerInstanceID: record.ProducerInstanceID,
		ProducerSeq:        record.ProducerSeq,
		OutboxID:           record.OutboxID,
	}
}

func contractExecution(execution coredb.Execution) corecontract.ExecutionState {
	return corecontract.ExecutionState{
		ExecutionID:          execution.ID,
		RunID:                execution.RunID,
		RunAttemptID:         execution.RunAttemptID,
		RunAttemptGeneration: execution.RunAttemptGeneration,
		AppServerToolCallID:  execution.AppServerToolCallID,
		ExecutorID:           execution.ExecutorID,
		EnvironmentID:        execution.EnvID,
		TargetKind:           execution.Target.Kind,
		TargetID:             execution.Target.ID,
		TargetGeneration:     execution.Target.Generation,
		ToolName:             execution.ToolName,
		ToolVersion:          execution.ToolVersion,
		MapperVersion:        execution.MapperVersion,
		PolicyVersion:        execution.PolicyVersion,
		PolicyDecision:       execution.PolicyDecision,
		OperationCount:       execution.OperationCount,
		ArgumentsDigest:      contractCanonicalJSONDigest(execution.ArgumentsHash),
		ToolSchemaDigest:     contractCanonicalJSONDigest(execution.ToolSchemaHash),
		OperationPlanDigest:  contractCanonicalJSONDigest(execution.OperationPlanHash),
		PolicyContextDigest:  contractCanonicalJSONDigest(execution.PolicyContextHash),
		Status:               execution.Status,
		DispatchedAt:         execution.DispatchedAt,
		TerminalResultDigest: contractOptionalCanonicalJSONDigest(execution.TerminalResultHash),
		TerminalAt:           execution.TerminalAt,
		Version:              execution.Version,
		CreatedAt:            execution.CreatedAt,
		UpdatedAt:            execution.UpdatedAt,
	}
}

func contractExecutionOperation(operation coredb.ExecutionOperation) corecontract.ExecutionOperationState {
	return corecontract.ExecutionOperationState{
		OperationID:           operation.ID,
		ExecutionID:           operation.ExecutionID,
		Ordinal:               operation.Ordinal,
		Kind:                  operation.Kind,
		EffectClass:           operation.EffectClass,
		MutationKey:           operation.MutationKey,
		ParamsDigest:          contractCanonicalJSONDigest(operation.ParamsHash),
		Status:                operation.Status,
		ConnectionGeneration:  operation.ConnectionGeneration,
		TargetKind:            operation.Target.Kind,
		TargetID:              operation.Target.ID,
		TargetGeneration:      operation.Target.Generation,
		AcknowledgementDigest: contractOptionalCanonicalJSONDigest(operation.AcknowledgementHash),
		TerminalResultDigest:  contractOptionalCanonicalJSONDigest(operation.TerminalResultHash),
		DispatchedAt:          operation.DispatchedAt,
		AcknowledgedAt:        operation.AcknowledgedAt,
		TerminalAt:            operation.TerminalAt,
		Version:               operation.Version,
		CreatedAt:             operation.CreatedAt,
		UpdatedAt:             operation.UpdatedAt,
	}
}

func contractCanonicalJSONDigest(hash coredb.CanonicalJSONHash) corecontract.CanonicalJSONDigest {
	digest := hash.SHA256()
	return corecontract.CanonicalJSONDigest{
		Domain:               string(hash.Domain()),
		CanonicalizerVersion: hash.CanonicalizerVersion(),
		SHA256:               hex.EncodeToString(digest[:]),
	}
}

func contractOptionalCanonicalJSONDigest(hash *coredb.CanonicalJSONHash) *corecontract.CanonicalJSONDigest {
	if hash == nil {
		return nil
	}
	value := contractCanonicalJSONDigest(*hash)
	return &value
}

func executionCommandConversionError(operation, resource, resourceID string, err error) error {
	return &coredb.StateError{
		Code:       coredb.ErrorInvalidArgument,
		Operation:  operation,
		Resource:   resource,
		ResourceID: resourceID,
		Message:    fmt.Sprintf("invalid internal command: %v", err),
	}
}
