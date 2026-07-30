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
		{name: "unknown wins", executionStatus: ExecutionStatusRunning, operations: []string{OperationStatusFailed, OperationStatusUnknown}, want: ExecutionStatusUnknown},
		{name: "failed leaves later operation unsent", executionStatus: ExecutionStatusRunning, operations: []string{OperationStatusFailed, OperationStatusPrepared}, want: ExecutionStatusFailed},
		{name: "cancel leaves later operation unsent", executionStatus: ExecutionStatusCancelling, operations: []string{OperationStatusSucceeded, OperationStatusPrepared}, want: ExecutionStatusCancelled},
		{name: "prepared is not terminal", executionStatus: ExecutionStatusRunning, operations: []string{OperationStatusSucceeded, OperationStatusPrepared}, wantError: true},
		{name: "acknowledged is still live", executionStatus: ExecutionStatusRunning, operations: []string{OperationStatusAcknowledged}, wantError: true},
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
