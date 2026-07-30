package coredb

import "testing"

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
