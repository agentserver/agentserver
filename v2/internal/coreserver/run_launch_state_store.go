package coreserver

import (
	"context"
	"encoding/hex"
	"errors"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/coredb"
)

type RunLaunchStateStore interface {
	ResolveRunLaunchState(context.Context, coredb.ResolveRunLaunchStateCommand) (coredb.ResolvedRunLaunchState, error)
}

type StateStoreRunLaunchStateQueries struct {
	Store RunLaunchStateStore
}

var _ RunLaunchStateQueries = StateStoreRunLaunchStateQueries{}

func (queries StateStoreRunLaunchStateQueries) ResolveRunLaunchState(ctx context.Context, request corecontract.ResolveRunLaunchStateRequest) (corecontract.ResolveRunLaunchStateResponse, error) {
	if queries.Store == nil {
		return corecontract.ResolveRunLaunchStateResponse{}, errors.New("nil core state store")
	}
	resolved, err := queries.Store.ResolveRunLaunchState(ctx, coredb.ResolveRunLaunchStateCommand{
		WorkspaceID: request.WorkspaceID, SessionID: request.SessionID, RunID: request.RunID,
		AttemptID: request.RunAttemptID, HolderID: request.HolderID, Generation: request.RunAttemptGeneration,
		ExpectedRunVersion: request.ExpectedRunVersion, ExpectedAttemptVersion: request.ExpectedRunAttemptVersion,
	})
	if err != nil {
		return corecontract.ResolveRunLaunchStateResponse{}, err
	}
	response := corecontract.ResolveRunLaunchStateResponse{
		WorkspaceID: resolved.WorkspaceID, SessionID: resolved.SessionID, RunID: resolved.RunID,
		RunAttemptID: resolved.AttemptID, HolderID: resolved.HolderID,
		RunAttemptGeneration: resolved.Generation, RunVersion: resolved.RunVersion,
		RunAttemptVersion: resolved.AttemptVersion,
		Prompt:            databaseRunLaunchObjectPointer(resolved.Prompt),
		ExecutorPolicy: corecontract.RunLaunchExecutorPolicyState{
			Version: resolved.ExecutorPolicy.Version, ContextDigest: hex.EncodeToString(resolved.ExecutorPolicy.ContextDigest[:]),
			AllowedTools: append([]string(nil), resolved.ExecutorPolicy.AllowedTools...),
		},
	}
	if resolved.LLMGateway != (coredb.RunLLMGatewayBinding{}) {
		response.LLMGateway = &corecontract.RunLaunchLLMGatewayState{
			GatewayID: resolved.LLMGateway.GatewayID, ConfigVersion: resolved.LLMGateway.ConfigVersion,
			GrantUserID: resolved.LLMGateway.GrantUserID, Model: resolved.LLMGateway.Model,
		}
	}
	if resolved.PreviousCheckpoint != nil {
		checkpoint := resolved.PreviousCheckpoint
		response.PreviousCheckpoint = &corecontract.RunLaunchCheckpointState{
			CheckpointID: checkpoint.ID, RunID: checkpoint.RunID,
			RunAttemptID: checkpoint.AttemptID, RunAttemptGeneration: checkpoint.AttemptGeneration,
			ThreadID: checkpoint.ThreadID, TurnID: checkpoint.TurnID,
			ManifestDigest:             hex.EncodeToString(checkpoint.ManifestDigest[:]),
			CatalogDigest:              hex.EncodeToString(checkpoint.CatalogDigest[:]),
			Catalog:                    contractBrainToolCatalog(checkpoint.Catalog),
			Object:                     databaseRunLaunchObjectPointer(checkpoint.Object),
			CodexRuntimeManifestDigest: hex.EncodeToString(checkpoint.CodexRuntimeManifestDigest[:]),
			CheckpointAllowlistVersion: checkpoint.CheckpointAllowlistVersion,
		}
	}
	return response, nil
}

func databaseRunLaunchObjectPointer(pointer coredb.ObjectPointer) corecontract.RunLaunchObjectPointer {
	return corecontract.RunLaunchObjectPointer{
		ObjectID: pointer.ObjectID, SHA256: hex.EncodeToString(pointer.SHA256[:]),
		SizeBytes: pointer.Size, MediaType: pointer.MediaType,
	}
}
