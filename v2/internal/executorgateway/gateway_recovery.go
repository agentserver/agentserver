package executorgateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

const GatewayRecoveryBatchSize = corecontract.MaxGatewayRecoveryRecords

type RecoverGatewayRequest struct {
	ExecutorID        string
	GatewayInstanceID string
	Records           []ExecutionTransitionRecord
}

type RecoverGatewayResult struct {
	FencedConnectionGeneration int64
	ConnectionFenced           bool
	RecoveredExecutions        int
	Remaining                  bool
}

type GatewayRecoveryAuthority interface {
	RecoverExecutorGateway(context.Context, RecoverGatewayRequest) (RecoverGatewayResult, error)
}

type GatewayStartupRecoveryResult struct {
	FencedConnectionGeneration int64
	ConnectionFenced           bool
	RecoveredExecutions        int
	Passes                     int
}

// RecoverGatewayStartup drains the bounded Core recovery lane before the
// production listener or readiness is published. A failed or ambiguous Core
// call is deliberately not retried in-process: the gateway exits, and a fresh
// process uses a fresh producer instance and transition identities.
func RecoverGatewayStartup(
	ctx context.Context,
	authority GatewayRecoveryAuthority,
	executorID, gatewayInstanceID string,
	transitions *ExecutionTransitionAllocator,
) (GatewayStartupRecoveryResult, error) {
	if ctx == nil || authority == nil || transitions == nil {
		return GatewayStartupRecoveryResult{}, errors.New("gateway recovery context, authority, and transition allocator are required")
	}
	if err := validateRegistryIdentity("gateway recovery executor ID", executorID); err != nil {
		return GatewayStartupRecoveryResult{}, err
	}
	if err := validateRegistryIdentity("gateway recovery instance ID", gatewayInstanceID); err != nil {
		return GatewayStartupRecoveryResult{}, err
	}
	if executorID == gatewayInstanceID {
		return GatewayStartupRecoveryResult{}, errors.New("gateway recovery executor and instance identities must be distinct")
	}

	summary := GatewayStartupRecoveryResult{FencedConnectionGeneration: -1}
	zeroProgressPasses := 0
	for {
		records := make([]ExecutionTransitionRecord, GatewayRecoveryBatchSize)
		for index := range records {
			record, err := transitions.Allocate()
			if err != nil {
				return GatewayStartupRecoveryResult{}, fmt.Errorf("allocate gateway recovery transition %d: %w", index, err)
			}
			records[index] = record
		}
		result, err := authority.RecoverExecutorGateway(ctx, RecoverGatewayRequest{
			ExecutorID: executorID, GatewayInstanceID: gatewayInstanceID, Records: records,
		})
		if err != nil {
			return GatewayStartupRecoveryResult{}, fmt.Errorf("recover executor-gateway startup state: %w", err)
		}
		if result.FencedConnectionGeneration < 0 || result.RecoveredExecutions < 0 || result.RecoveredExecutions > len(records) {
			return GatewayStartupRecoveryResult{}, errors.New("Core returned an invalid gateway recovery bound")
		}
		if summary.FencedConnectionGeneration >= 0 && summary.FencedConnectionGeneration != result.FencedConnectionGeneration {
			return GatewayStartupRecoveryResult{}, errors.New("Core changed the fenced connection generation during gateway recovery")
		}
		summary.FencedConnectionGeneration = result.FencedConnectionGeneration
		summary.ConnectionFenced = summary.ConnectionFenced || result.ConnectionFenced
		summary.RecoveredExecutions += result.RecoveredExecutions
		summary.Passes++
		if !result.Remaining {
			return summary, nil
		}
		if result.RecoveredExecutions == 0 {
			zeroProgressPasses++
			if zeroProgressPasses >= 2 {
				return GatewayStartupRecoveryResult{}, errors.New("Core gateway recovery made no progress in two consecutive passes")
			}
		} else {
			zeroProgressPasses = 0
		}
	}
}

func (client *CoreConnectionClient) RecoverExecutorGateway(ctx context.Context, request RecoverGatewayRequest) (RecoverGatewayResult, error) {
	if client == nil {
		return RecoverGatewayResult{}, errors.New("core connection client is required")
	}
	records := make([]corecontract.TransitionRecord, len(request.Records))
	for index, record := range request.Records {
		records[index] = contractExecutionTransitionRecord(record)
	}
	command := corecontract.RecoverExecutorGatewayRequest{
		GatewayInstanceID: request.GatewayInstanceID,
		Records:           records,
	}
	var response corecontract.RecoverExecutorGatewayResponse
	if err := client.post(
		ctx, corecontract.RecoverExecutorGatewayPath(request.ExecutorID), command, &response, http.StatusOK,
	); err != nil {
		return RecoverGatewayResult{}, err
	}
	return RecoverGatewayResult{
		FencedConnectionGeneration: response.FencedConnectionGeneration,
		ConnectionFenced:           response.ConnectionFenced,
		RecoveredExecutions:        response.RecoveredExecutions,
		Remaining:                  response.Remaining,
	}, nil
}
