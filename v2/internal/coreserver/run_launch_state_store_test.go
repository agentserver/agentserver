package coreserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/coredb"
	"github.com/agentserver/agentserver/v2/internal/workspaceauthority"
)

func TestStateStoreRunLaunchStateQueriesMapAuthorityProjection(t *testing.T) {
	store := &recordingRunLaunchStateStore{}
	queries := StateStoreRunLaunchStateQueries{Store: store}
	request := corecontract.ResolveRunLaunchStateRequest{
		WorkspaceID: "40000000-0000-4000-8000-000000000004", SessionID: "43000000-0000-4000-8000-000000000004",
		RunID: testRunID, RunAttemptID: testRunAttemptID, HolderID: "pool-holder", RunAttemptGeneration: 3,
		ExpectedRunVersion: 2, ExpectedRunAttemptVersion: 1,
	}
	response, err := queries.ResolveRunLaunchState(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if store.command.AttemptID != request.RunAttemptID || store.command.ExpectedAttemptVersion != 1 ||
		response.Prompt.SHA256 != hex.EncodeToString(store.promptDigest[:]) ||
		response.ExecutorPolicy.ContextDigest != hex.EncodeToString(store.policyDigest[:]) ||
		response.PreviousCheckpoint == nil ||
		response.PreviousCheckpoint.CatalogDigest != hex.EncodeToString(store.catalogDigest[:]) ||
		response.PreviousCheckpoint.Catalog.CatalogID != "49000000-0000-4000-8000-000000000004" ||
		response.PreviousCheckpoint.RunID != "4a000000-0000-4000-8000-000000000004" ||
		response.PreviousCheckpoint.RunAttemptID != "4b000000-0000-4000-8000-000000000004" ||
		response.PreviousCheckpoint.RunAttemptGeneration != 2 || response.PreviousCheckpoint.TurnID != "turn-previous" ||
		response.PreviousCheckpoint.CheckpointAllowlistVersion != 7 || response.Workspace == nil ||
		response.Workspace.EnvironmentID != "50000000-0000-0000-0000-000000000005" || response.Workspace.EnvironmentVersion != 2 ||
		response.Workspace.WorkingDirectory != "rtm-aihub" || response.Workspace.WorkingDirectoryVersion != 3 ||
		response.Workspace.RootSHA256 != hex.EncodeToString(store.workspaceDigest[:]) {
		t.Fatalf("store command/response = %+v / %+v", store.command, response)
	}
	response.ExecutorPolicy.AllowedTools[0] = "mutated"
	if store.allowedTools[0] != "read_file" {
		t.Fatal("transport response aliases store policy")
	}
}

type recordingRunLaunchStateStore struct {
	command         coredb.ResolveRunLaunchStateCommand
	promptDigest    [32]byte
	policyDigest    [32]byte
	catalogDigest   [32]byte
	allowedTools    []string
	workspaceDigest [32]byte
}

func (store *recordingRunLaunchStateStore) ResolveRunLaunchState(_ context.Context, command coredb.ResolveRunLaunchStateCommand) (coredb.ResolvedRunLaunchState, error) {
	store.command = command
	store.promptDigest = sha256.Sum256([]byte("prompt"))
	store.policyDigest = sha256.Sum256([]byte("policy"))
	store.catalogDigest = sha256.Sum256([]byte("catalog"))
	store.allowedTools = []string{"read_file", "shell"}
	manifestDigest := sha256.Sum256([]byte("manifest"))
	objectDigest := sha256.Sum256([]byte("checkpoint-object"))
	runtimeDigest := sha256.Sum256([]byte("runtime"))
	store.workspaceDigest = sha256.Sum256([]byte("workspace-root"))
	return coredb.ResolvedRunLaunchState{
		WorkspaceID: command.WorkspaceID, SessionID: command.SessionID, RunID: command.RunID,
		AttemptID: command.AttemptID, HolderID: command.HolderID, Generation: command.Generation,
		RunVersion: command.ExpectedRunVersion, AttemptVersion: command.ExpectedAttemptVersion,
		Prompt: coredb.ObjectPointer{
			ObjectID: "46000000-0000-4000-8000-000000000004", SHA256: store.promptDigest,
			Size: 128, MediaType: "application/json",
		},
		PreviousCheckpoint: &coredb.Checkpoint{
			ID:    "47000000-0000-4000-8000-000000000004",
			RunID: "4a000000-0000-4000-8000-000000000004", AttemptID: "4b000000-0000-4000-8000-000000000004",
			AttemptGeneration: 2, ThreadID: "thread-previous", TurnID: "turn-previous",
			ManifestDigest: manifestDigest, CatalogDigest: store.catalogDigest,
			Object: coredb.ObjectPointer{
				ObjectID: "48000000-0000-4000-8000-000000000004", SHA256: objectDigest,
				Size: 1024, MediaType: "application/vnd.agentserver.codex-checkpoint.v1",
			},
			CodexRuntimeManifestDigest: runtimeDigest, CheckpointAllowlistVersion: 7,
			Catalog: coredb.BrainToolCatalog{
				ID: "49000000-0000-4000-8000-000000000004", CatalogDigest: store.catalogDigest,
			},
			CreatedAt: time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC),
		},
		ExecutorPolicy: coredb.RunExecutorPolicy{
			Version: "executor-policy/1", ContextDigest: store.policyDigest,
			AllowedTools: store.allowedTools,
		},
		Workspace: &workspaceauthority.Binding{
			EnvironmentID: "50000000-0000-0000-0000-000000000005", EnvironmentVersion: 2,
			RootSHA256: store.workspaceDigest, WorkingDirectory: "rtm-aihub", WorkingDirectoryVersion: 3,
		},
	}, nil
}
