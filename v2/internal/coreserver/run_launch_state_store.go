package coreserver

import (
	"context"
	"encoding/hex"
	"errors"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/coredb"
	"github.com/agentserver/agentserver/v2/internal/runmanifest"
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
	if resolved.PermissionModeExplicit {
		modeValue, err := runmanifest.CodexPermissionMode(resolved.PermissionMode).Effective()
		if err != nil || resolved.PermissionMode == "" || resolved.PermissionModeVersion < 1 || resolved.PermissionModeVersion > 1<<53-1 {
			if err == nil {
				err = errors.New("permission mode authority is invalid")
			}
			return corecontract.ResolveRunLaunchStateResponse{}, err
		}
		mode := string(modeValue)
		version := resolved.PermissionModeVersion
		response.PermissionMode = &mode
		response.PermissionModeVersion = &version
	} else if resolved.PermissionMode != "" || resolved.PermissionModeVersion != 0 {
		return corecontract.ResolveRunLaunchStateResponse{}, errors.New("permission mode authority is set without an explicit marker")
	}
	if resolved.LLMGateway != (coredb.RunLLMGatewayBinding{}) {
		response.LLMGateway = &corecontract.RunLaunchLLMGatewayState{
			GatewayID: resolved.LLMGateway.GatewayID, ConfigVersion: resolved.LLMGateway.ConfigVersion,
			GrantUserID: resolved.LLMGateway.GrantUserID, Model: resolved.LLMGateway.Model,
		}
	}
	if resolved.LarkEgress != (coredb.RunLarkEgressBinding{}) {
		response.LarkEgress = &corecontract.RunLaunchLarkEgressState{
			GrantID: resolved.LarkEgress.GrantID, GrantVersion: resolved.LarkEgress.GrantVersion,
			GrantUserID:  resolved.LarkEgress.GrantUserID,
			PolicySHA256: hex.EncodeToString(resolved.LarkEgress.PolicySHA256[:]),
		}
	}
	if resolved.ManagedSandbox != (coredb.RunManagedSandboxBinding{}) {
		binding := resolved.ManagedSandbox
		response.ManagedSandbox = &corecontract.RunLaunchManagedSandboxState{
			SettingVersion: binding.SettingVersion, Region: binding.Region,
			EnvironmentID: binding.EnvironmentID,
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
