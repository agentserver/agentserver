package coredb

import "testing"

func TestPostgreSQLManagedNotSentFailure(t *testing.T) {
	for _, kind := range []string{DispatchTargetTAE, DispatchTargetKubernetes} {
		t.Run(kind, func(t *testing.T) {
			f := newManagedCredentialPostgresFixture(t, 940_000, kind)
			command := CompleteOperationCommand{OperationID: f.dispatch.Operation.ID, ExecutionID: f.dispatch.Execution.ID, RunID: f.running.Run.ID, AttemptID: f.running.Attempt.ID, Generation: f.running.Attempt.Generation, Target: f.sandbox.Target(), ExpectedExecutionVersion: f.dispatch.Execution.Version, ExpectedOperationVersion: f.dispatch.Operation.Version, TerminalStatus: OperationStatusFailed, ResultHash: executionTestHash(t, HashDomainOperationResult, 940_700), Record: stateTransitionRecord(940_710)}
			if _, err := f.store.CompleteOperation(t.Context(), command); !HasStateErrorCode(err, ErrorInvalidState) {
				t.Fatalf("unproven pre-ACK failure accepted: %v", err)
			}
			command.DispatchNotSent = true
			completed, err := f.store.CompleteOperation(t.Context(), command)
			if err != nil || completed.Operation.Status != OperationStatusFailed || completed.Operation.AcknowledgedAt != nil {
				t.Fatalf("not-sent failure: %+v %v", completed, err)
			}
			if retry, err := f.store.CompleteOperation(t.Context(), command); err != nil || retry.Changed {
				t.Fatalf("retry not idempotent: %v", err)
			}
			result, err := f.store.CompleteExecution(t.Context(), CompleteExecutionCommand{ExecutionID: completed.Execution.ID, RunID: f.running.Run.ID, AttemptID: f.running.Attempt.ID, Generation: f.running.Attempt.Generation, ExpectedExecutionVersion: completed.Execution.Version, TerminalStatus: ExecutionStatusFailed, ResultHash: executionTestHash(t, HashDomainExecutionResult, 940_720), Record: stateTransitionRecord(940_730)})
			if err != nil || result.Execution.Status != ExecutionStatusFailed {
				t.Fatalf("execution did not fail: %+v %v", result, err)
			}
		})
	}
}
