package executorgateway

import (
	"context"
	"errors"
	"testing"
)

func TestRecoverGatewayStartupDrainsAllBatchesBeforeReturning(t *testing.T) {
	const gatewayID = "71000000-0000-4000-8000-000000000001"
	allocator, err := NewExecutionTransitionAllocator(gatewayID, deterministicIDGenerator())
	if err != nil {
		t.Fatal(err)
	}
	authority := &scriptedGatewayRecoveryAuthority{results: []RecoverGatewayResult{
		{FencedConnectionGeneration: 7, ConnectionFenced: true, RecoveredExecutions: GatewayRecoveryBatchSize, Remaining: true},
		{FencedConnectionGeneration: 7, RecoveredExecutions: 1, Remaining: false},
	}}
	summary, err := RecoverGatewayStartup(
		t.Context(), authority, "72000000-0000-4000-8000-000000000001", gatewayID, allocator,
	)
	if err != nil {
		t.Fatal(err)
	}
	if summary.FencedConnectionGeneration != 7 || !summary.ConnectionFenced ||
		summary.RecoveredExecutions != GatewayRecoveryBatchSize+1 || summary.Passes != 2 {
		t.Fatalf("gateway recovery summary = %+v", summary)
	}
	if len(authority.requests) != 2 {
		t.Fatalf("gateway recovery requests = %d, want 2", len(authority.requests))
	}
	for pass, request := range authority.requests {
		if request.ExecutorID != "72000000-0000-4000-8000-000000000001" || request.GatewayInstanceID != gatewayID || len(request.Records) != GatewayRecoveryBatchSize {
			t.Fatalf("gateway recovery request %d = %+v", pass, request)
		}
		for index, record := range request.Records {
			wantSequence := int64(pass*GatewayRecoveryBatchSize + index + 1)
			if record.ProducerInstanceID != gatewayID || record.ProducerSeq != wantSequence {
				t.Fatalf("gateway recovery record %d/%d = %+v, want sequence %d", pass, index, record, wantSequence)
			}
		}
	}
}

func TestRecoverGatewayStartupRejectsGenerationDriftAndNoProgress(t *testing.T) {
	const gatewayID = "73000000-0000-4000-8000-000000000001"
	tests := []struct {
		name    string
		results []RecoverGatewayResult
		want    string
	}{
		{
			name: "generation drift",
			results: []RecoverGatewayResult{
				{FencedConnectionGeneration: 2, RecoveredExecutions: 1, Remaining: true},
				{FencedConnectionGeneration: 3},
			},
			want: "changed the fenced connection generation",
		},
		{
			name: "no progress",
			results: []RecoverGatewayResult{
				{FencedConnectionGeneration: 2, Remaining: true},
				{FencedConnectionGeneration: 2, Remaining: true},
			},
			want: "no progress",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			allocator, err := NewExecutionTransitionAllocator(gatewayID, deterministicIDGenerator())
			if err != nil {
				t.Fatal(err)
			}
			_, err = RecoverGatewayStartup(
				t.Context(), &scriptedGatewayRecoveryAuthority{results: test.results},
				"74000000-0000-4000-8000-000000000001", gatewayID, allocator,
			)
			if err == nil || !containsRecoveryError(err, test.want) {
				t.Fatalf("RecoverGatewayStartup() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestRecoverGatewayStartupDoesNotRetryAmbiguousCoreCall(t *testing.T) {
	const gatewayID = "75000000-0000-4000-8000-000000000001"
	allocator, err := NewExecutionTransitionAllocator(gatewayID, deterministicIDGenerator())
	if err != nil {
		t.Fatal(err)
	}
	authority := &scriptedGatewayRecoveryAuthority{err: errors.New("response lost after commit")}
	if _, err := RecoverGatewayStartup(
		t.Context(), authority, "76000000-0000-4000-8000-000000000001", gatewayID, allocator,
	); err == nil {
		t.Fatal("ambiguous Core recovery result was retried or accepted")
	}
	if len(authority.requests) != 1 {
		t.Fatalf("ambiguous Core recovery calls = %d, want 1", len(authority.requests))
	}
}

type scriptedGatewayRecoveryAuthority struct {
	requests []RecoverGatewayRequest
	results  []RecoverGatewayResult
	err      error
}

func (authority *scriptedGatewayRecoveryAuthority) RecoverExecutorGateway(_ context.Context, request RecoverGatewayRequest) (RecoverGatewayResult, error) {
	authority.requests = append(authority.requests, request)
	if authority.err != nil {
		return RecoverGatewayResult{}, authority.err
	}
	if len(authority.results) == 0 {
		return RecoverGatewayResult{}, errors.New("unexpected gateway recovery call")
	}
	result := authority.results[0]
	authority.results = authority.results[1:]
	return result, nil
}

func containsRecoveryError(err error, want string) bool {
	for err != nil {
		if len(err.Error()) >= len(want) {
			for index := 0; index+len(want) <= len(err.Error()); index++ {
				if err.Error()[index:index+len(want)] == want {
					return true
				}
			}
		}
		err = errors.Unwrap(err)
	}
	return false
}
