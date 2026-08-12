package coredb

import "testing"

func TestNullableUUIDPreservesOptionalExecutorIdentity(t *testing.T) {
	if got := nullableUUID(""); got != nil {
		t.Fatalf("nullableUUID(empty) = %#v, want nil", got)
	}
	executorID := stateTestUUID(80_000)
	if got := nullableUUID(executorID); got != executorID {
		t.Fatalf("nullableUUID(valid) = %#v, want %q", got, executorID)
	}
}

func TestAggregateExecutionStatusUsesFailClosedPrecedence(t *testing.T) {
	tests := []struct {
		name            string
		executionStatus string
		operations      []string
		want            string
		wantError       bool
	}{
		{name: "all succeeded", executionStatus: ExecutionStatusRunning, operations: []string{OperationStatusSucceeded, OperationStatusSucceeded}, want: ExecutionStatusSucceeded},
		{name: "skipped is neutral", executionStatus: ExecutionStatusRunning, operations: []string{OperationStatusSucceeded, OperationStatusSkipped}, want: ExecutionStatusSucceeded},
		{name: "unknown wins", executionStatus: ExecutionStatusRunning, operations: []string{OperationStatusFailed, OperationStatusUnknown}, want: ExecutionStatusUnknown},
		{name: "unknown with skipped", executionStatus: ExecutionStatusRunning, operations: []string{OperationStatusUnknown, OperationStatusSkipped}, want: ExecutionStatusUnknown},
		{name: "failed with skipped", executionStatus: ExecutionStatusRunning, operations: []string{OperationStatusFailed, OperationStatusSkipped}, want: ExecutionStatusFailed},
		{name: "cancelled with skipped", executionStatus: ExecutionStatusCancelling, operations: []string{OperationStatusCancelled, OperationStatusSkipped}, want: ExecutionStatusCancelled},
		{name: "failed cannot hide prepared", executionStatus: ExecutionStatusRunning, operations: []string{OperationStatusFailed, OperationStatusPrepared}, wantError: true},
		{name: "cancelling cannot hide prepared", executionStatus: ExecutionStatusCancelling, operations: []string{OperationStatusSucceeded, OperationStatusPrepared}, wantError: true},
		{name: "prepared is not terminal", executionStatus: ExecutionStatusRunning, operations: []string{OperationStatusSucceeded, OperationStatusPrepared}, wantError: true},
		{name: "acknowledged is still live", executionStatus: ExecutionStatusRunning, operations: []string{OperationStatusAcknowledged}, wantError: true},
		{name: "only skipped is not success", executionStatus: ExecutionStatusRunning, operations: []string{OperationStatusSkipped}, wantError: true},
		{name: "unsupported status", executionStatus: ExecutionStatusRunning, operations: []string{"future"}, wantError: true},
		{name: "no operations", executionStatus: ExecutionStatusDispatching, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := aggregateExecutionStatus(test.executionStatus, test.operations)
			if test.wantError {
				if err == nil {
					t.Fatalf("aggregateExecutionStatus() = %q, want error", got)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("aggregateExecutionStatus() = %q, %v; want %q", got, err, test.want)
			}
		})
	}
}

func TestValidatePreparedOperationSkip(t *testing.T) {
	execution := Execution{Status: ExecutionStatusRunning, OperationCount: 2}
	process := ExecutionOperation{Ordinal: 1, Kind: "process_start", Status: OperationStatusSucceeded}
	timeout := ExecutionOperation{Ordinal: 2, Kind: OperationKindTimeoutTerminate, Status: OperationStatusPrepared}
	tests := []struct {
		name       string
		execution  Execution
		target     ExecutionOperation
		operations []ExecutionOperation
		wantError  bool
	}{
		{name: "terminal process makes timeout unnecessary", execution: execution, target: timeout, operations: []ExecutionOperation{process, timeout}},
		{name: "unknown process still preserves unknown aggregate", execution: execution, target: timeout, operations: []ExecutionOperation{{Ordinal: 1, Status: OperationStatusUnknown}, timeout}},
		{name: "execution not dispatched", execution: Execution{Status: ExecutionStatusApproved, OperationCount: 2}, target: timeout, operations: []ExecutionOperation{process, timeout}, wantError: true},
		{name: "target already dispatching", execution: execution, target: ExecutionOperation{Ordinal: 2, Kind: OperationKindTimeoutTerminate, Status: OperationStatusDispatching}, operations: []ExecutionOperation{process, timeout}, wantError: true},
		{name: "wrong kind", execution: execution, target: ExecutionOperation{Ordinal: 2, Kind: "process_start", Status: OperationStatusPrepared}, operations: []ExecutionOperation{process, timeout}, wantError: true},
		{name: "not trailing", execution: Execution{Status: ExecutionStatusRunning, OperationCount: 3}, target: timeout, operations: []ExecutionOperation{process, timeout, {Ordinal: 3, Status: OperationStatusPrepared}}, wantError: true},
		{name: "no preceding operation", execution: Execution{Status: ExecutionStatusRunning, OperationCount: 1}, target: ExecutionOperation{Ordinal: 1, Kind: OperationKindTimeoutTerminate, Status: OperationStatusPrepared}, operations: []ExecutionOperation{{Ordinal: 1, Kind: OperationKindTimeoutTerminate, Status: OperationStatusPrepared}}, wantError: true},
		{name: "preceding operation still live", execution: execution, target: timeout, operations: []ExecutionOperation{{Ordinal: 1, Status: OperationStatusAcknowledged}, timeout}, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validatePreparedOperationSkip(test.execution, test.target, test.operations)
			if (err != nil) != test.wantError {
				t.Fatalf("validatePreparedOperationSkip() error = %v, wantError = %t", err, test.wantError)
			}
		})
	}
}

func TestPlanGatewayExecutionRecoveryIsFailClosed(t *testing.T) {
	execution := Execution{ID: stateTestUUID(81_000), Status: ExecutionStatusRunning, OperationCount: 2}
	process := ExecutionOperation{
		ID: stateTestUUID(81_001), Ordinal: 1, Kind: "process_start",
		Status: OperationStatusDispatching, ConnectionGeneration: 7,
	}
	timeout := ExecutionOperation{
		ID: stateTestUUID(81_002), Ordinal: 2, Kind: OperationKindTimeoutTerminate,
		Status: OperationStatusPrepared,
	}
	statuses, changes, err := planGatewayExecutionRecovery(execution, []ExecutionOperation{process, timeout}, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 2 || statuses[0] != OperationStatusUnknown || statuses[1] != OperationStatusSkipped ||
		len(changes) != 2 || changes[0].FromStatus != OperationStatusDispatching || changes[0].ToStatus != OperationStatusUnknown ||
		changes[1].FromStatus != OperationStatusPrepared || changes[1].ToStatus != OperationStatusSkipped {
		t.Fatalf("gateway recovery plan = statuses %v, changes %+v", statuses, changes)
	}
	if aggregate, err := aggregateExecutionStatus(execution.Status, statuses); err != nil || aggregate != ExecutionStatusUnknown {
		t.Fatalf("gateway recovery aggregate = %q, %v", aggregate, err)
	}

	terminal := process
	terminal.Status = OperationStatusSucceeded
	statuses, changes, err = planGatewayExecutionRecovery(execution, []ExecutionOperation{terminal, timeout}, 7)
	if err != nil || len(changes) != 1 || statuses[0] != OperationStatusSucceeded || statuses[1] != OperationStatusSkipped {
		t.Fatalf("terminal-only gateway recovery plan = statuses %v, changes %+v, error %v", statuses, changes, err)
	}
	if aggregate, err := aggregateExecutionStatus(execution.Status, statuses); err != nil || aggregate != ExecutionStatusSucceeded {
		t.Fatalf("terminal-only gateway recovery aggregate = %q, %v", aggregate, err)
	}

	required := timeout
	required.Kind = "required_followup"
	if _, _, err := planGatewayExecutionRecovery(execution, []ExecutionOperation{process, required}, 7); err == nil {
		t.Fatal("gateway recovery fabricated a terminal outcome for an unsent required operation")
	}
	future := process
	future.ConnectionGeneration = 8
	if _, _, err := planGatewayExecutionRecovery(execution, []ExecutionOperation{future, timeout}, 7); err == nil {
		t.Fatal("gateway recovery consumed an operation from an unfenced generation")
	}
}

func TestGatewayExecutionRecoveryFitsCanonicalEventBoundaryAtMaximumPlan(t *testing.T) {
	execution := Execution{
		ID: stateTestUUID(82_000), ExecutorID: stateTestUUID(82_001),
		Status: ExecutionStatusRunning, OperationCount: MaxExecutionOperations,
	}
	operations := make([]ExecutionOperation, MaxExecutionOperations)
	for index := range operations {
		operations[index] = ExecutionOperation{
			ID: stateTestUUID(82_100 + index), Ordinal: index + 1,
			Kind: "required_operation", Status: OperationStatusAcknowledged,
			ConnectionGeneration: 7,
		}
	}
	statuses, changes, err := planGatewayExecutionRecovery(execution, operations, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != MaxExecutionOperations || len(changes) != MaxExecutionOperations {
		t.Fatalf("maximum recovery plan = %d statuses, %d changes", len(statuses), len(changes))
	}
	for index, status := range statuses {
		if status != OperationStatusUnknown || changes[index].ToStatus != OperationStatusUnknown {
			t.Fatalf("maximum recovery operation %d = status %q, change %+v", index, status, changes[index])
		}
	}

	states := make([]gatewayRecoveryOperationState, len(operations))
	for index, operation := range operations {
		states[index] = gatewayRecoveryOperationState{
			OperationID: operation.ID, Ordinal: operation.Ordinal,
			Status: statuses[index], ConnectionGeneration: operation.ConnectionGeneration,
		}
	}
	if evidence, _, err := gatewayExecutionRecoveryEvidence(
		execution, ExecutionStatusUnknown, states, stateTestUUID(82_500), 7,
	); err != nil || len(evidence) > MaxInlineEventPayloadBytes {
		t.Fatalf("maximum recovery evidence = %d bytes, %v", len(evidence), err)
	}
	payload, err := gatewayExecutionRecoveryEventPayload(
		Run{ID: stateTestUUID(82_501)},
		RunAttempt{ID: stateTestUUID(82_502), Generation: 3},
		execution,
		ExecutionStatusUnknown,
		changes,
		stateTestUUID(82_500),
		7,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(payload) > MaxInlineEventPayloadBytes {
		t.Fatalf("maximum recovery event = %d bytes, limit %d", len(payload), MaxInlineEventPayloadBytes)
	}
}
