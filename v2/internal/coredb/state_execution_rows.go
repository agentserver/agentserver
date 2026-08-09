package coredb

import (
	"fmt"
	"time"
)

func scanExecution(scanner rowScanner) (Execution, error) {
	var execution Execution
	var canonicalizer string
	var argumentsHash []byte
	var toolSchemaHash []byte
	var operationPlanHash []byte
	var policyContextHash []byte
	var terminalResultHash []byte
	var executorID *string
	var targetKind *string
	var targetID *string
	var targetGeneration *int64
	var dispatchedAt *time.Time
	var terminalAt *time.Time
	err := scanner.Scan(
		&execution.ID,
		&execution.RunID,
		&execution.RunAttemptID,
		&execution.RunAttemptGeneration,
		&execution.AppServerToolCallID,
		&executorID,
		&execution.EnvID,
		&targetKind,
		&targetID,
		&targetGeneration,
		&execution.ToolName,
		&execution.ToolVersion,
		&execution.MapperVersion,
		&execution.PolicyVersion,
		&execution.PolicyDecision,
		&execution.OperationCount,
		&canonicalizer,
		&argumentsHash,
		&toolSchemaHash,
		&operationPlanHash,
		&policyContextHash,
		&execution.Status,
		&dispatchedAt,
		&terminalResultHash,
		&terminalAt,
		&execution.Version,
		&execution.CreatedAt,
		&execution.UpdatedAt,
	)
	if err != nil {
		return Execution{}, err
	}
	if executorID != nil {
		execution.ExecutorID = *executorID
	}
	if targetKind != nil {
		execution.Target.Kind = *targetKind
	}
	if targetID != nil {
		execution.Target.ID = *targetID
	}
	if targetGeneration != nil {
		execution.Target.Generation = *targetGeneration
	}
	execution.DispatchedAt = dispatchedAt
	execution.TerminalAt = terminalAt
	if execution.ArgumentsHash, err = storedCanonicalHash(HashDomainExecutionArguments, argumentsHash, canonicalizer); err != nil {
		return Execution{}, fmt.Errorf("execution %s arguments: %w", execution.ID, err)
	}
	if execution.ToolSchemaHash, err = storedCanonicalHash(HashDomainToolSchema, toolSchemaHash, canonicalizer); err != nil {
		return Execution{}, fmt.Errorf("execution %s tool schema: %w", execution.ID, err)
	}
	if execution.OperationPlanHash, err = storedCanonicalHash(HashDomainOperationPlan, operationPlanHash, canonicalizer); err != nil {
		return Execution{}, fmt.Errorf("execution %s operation plan: %w", execution.ID, err)
	}
	if execution.PolicyContextHash, err = storedCanonicalHash(HashDomainPolicyContext, policyContextHash, canonicalizer); err != nil {
		return Execution{}, fmt.Errorf("execution %s policy context: %w", execution.ID, err)
	}
	if terminalResultHash != nil {
		hash, hashErr := storedCanonicalHash(HashDomainExecutionResult, terminalResultHash, canonicalizer)
		if hashErr != nil {
			return Execution{}, fmt.Errorf("execution %s terminal result: %w", execution.ID, hashErr)
		}
		execution.TerminalResultHash = &hash
	}
	return execution, nil
}

func scanExecutionOperation(scanner rowScanner) (ExecutionOperation, error) {
	var operation ExecutionOperation
	var canonicalizer string
	var paramsHash []byte
	var acknowledgementHash []byte
	var terminalResultHash []byte
	var connectionGeneration *int64
	var targetKind *string
	var targetID *string
	var targetGeneration *int64
	var dispatchedAt *time.Time
	var acknowledgedAt *time.Time
	var terminalAt *time.Time
	err := scanner.Scan(
		&operation.ID,
		&operation.ExecutionID,
		&operation.Ordinal,
		&operation.Kind,
		&operation.EffectClass,
		&operation.MutationKey,
		&canonicalizer,
		&paramsHash,
		&operation.Status,
		&connectionGeneration,
		&targetKind,
		&targetID,
		&targetGeneration,
		&acknowledgementHash,
		&terminalResultHash,
		&dispatchedAt,
		&acknowledgedAt,
		&terminalAt,
		&operation.Version,
		&operation.CreatedAt,
		&operation.UpdatedAt,
	)
	if err != nil {
		return ExecutionOperation{}, err
	}
	if connectionGeneration != nil {
		operation.ConnectionGeneration = *connectionGeneration
	}
	if targetKind != nil {
		operation.Target.Kind = *targetKind
	}
	if targetID != nil {
		operation.Target.ID = *targetID
	}
	if targetGeneration != nil {
		operation.Target.Generation = *targetGeneration
	}
	operation.DispatchedAt = dispatchedAt
	operation.AcknowledgedAt = acknowledgedAt
	operation.TerminalAt = terminalAt
	if operation.ParamsHash, err = storedCanonicalHash(HashDomainOperationParams, paramsHash, canonicalizer); err != nil {
		return ExecutionOperation{}, fmt.Errorf("operation %s params: %w", operation.ID, err)
	}
	if acknowledgementHash != nil {
		hash, hashErr := storedCanonicalHash(HashDomainOperationAck, acknowledgementHash, canonicalizer)
		if hashErr != nil {
			return ExecutionOperation{}, fmt.Errorf("operation %s acknowledgement: %w", operation.ID, hashErr)
		}
		operation.AcknowledgementHash = &hash
	}
	if terminalResultHash != nil {
		hash, hashErr := storedCanonicalHash(HashDomainOperationResult, terminalResultHash, canonicalizer)
		if hashErr != nil {
			return ExecutionOperation{}, fmt.Errorf("operation %s terminal result: %w", operation.ID, hashErr)
		}
		operation.TerminalResultHash = &hash
	}
	return operation, nil
}

func executionColumns(alias string) string {
	if alias != "" {
		alias += "."
	}
	return alias + "id::text, " +
		alias + "run_id::text, " +
		alias + "run_attempt_id::text, " +
		alias + "run_attempt_generation, " +
		alias + "app_server_tool_call_id, " +
		alias + "executor_id::text, " +
		alias + "env_id::text, " +
		alias + "target_kind, " +
		alias + "target_id::text, " +
		alias + "target_generation, " +
		alias + "tool_name, " +
		alias + "tool_version, " +
		alias + "mapper_version, " +
		alias + "policy_version, " +
		alias + "policy_decision, " +
		alias + "operation_count, " +
		alias + "canonicalizer_version, " +
		alias + "arguments_hash, " +
		alias + "tool_schema_hash, " +
		alias + "operation_plan_hash, " +
		alias + "policy_context_hash, " +
		alias + "status, " +
		alias + "dispatched_at, " +
		alias + "terminal_result_hash, " +
		alias + "terminal_at, " +
		alias + "version, " +
		alias + "created_at, " +
		alias + "updated_at"
}

func executionOperationColumns(alias string) string {
	if alias != "" {
		alias += "."
	}
	return alias + "id::text, " +
		alias + "execution_id::text, " +
		alias + "ordinal, " +
		alias + "kind, " +
		alias + "effect_class, " +
		alias + "mutation_key::text, " +
		alias + "canonicalizer_version, " +
		alias + "params_hash, " +
		alias + "status, " +
		alias + "connection_generation, " +
		alias + "target_kind, " +
		alias + "target_id::text, " +
		alias + "target_generation, " +
		alias + "acknowledgement_hash, " +
		alias + "terminal_result_hash, " +
		alias + "dispatched_at, " +
		alias + "acknowledged_at, " +
		alias + "terminal_at, " +
		alias + "version, " +
		alias + "created_at, " +
		alias + "updated_at"
}
