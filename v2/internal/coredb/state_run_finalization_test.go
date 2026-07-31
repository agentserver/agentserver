package coredb

import (
	"crypto/sha256"
	"strings"
	"testing"

	checkpointartifact "github.com/agentserver/agentserver/v2/internal/checkpoint"
)

func TestValidateRunFinalizationCommands(t *testing.T) {
	begin := validBeginRunFinalizationCommand()
	if err := validateBeginRunFinalization(begin); err != nil {
		t.Fatal(err)
	}
	commit := validCommitCheckpointCommand()
	if err := validateCommitCheckpointAndTerminalRun(commit); err != nil {
		t.Fatal(err)
	}

	begin.Generation = maxSafeJSONInteger + 1
	if err := validateBeginRunFinalization(begin); err == nil || !strings.Contains(err.Error(), "safe integers") {
		t.Fatalf("unsafe generation validation error = %v", err)
	}
	commit = validCommitCheckpointCommand()
	commit.Object.MediaType = "application/octet-stream"
	if err := validateCommitCheckpointAndTerminalRun(commit); err == nil || !strings.Contains(err.Error(), "artifact v1") {
		t.Fatalf("wrong checkpoint media type validation error = %v", err)
	}
	commit = validCommitCheckpointCommand()
	commit.Object.Size = checkpointartifact.MaximumArtifactBytes + 1
	if err := validateCommitCheckpointAndTerminalRun(commit); err == nil || !strings.Contains(err.Error(), "artifact v1") {
		t.Fatalf("oversized checkpoint validation error = %v", err)
	}
}

func TestCheckpointCommitFingerprintIncludesEveryResumeAuthorityField(t *testing.T) {
	command := validCommitCheckpointCommand()
	run := Run{ID: command.RunID, WorkspaceID: testFinalizationWorkspaceID, SessionID: testFinalizationSessionID}
	attempt := RunAttempt{ID: command.AttemptID, Generation: command.Generation}
	checkpoint := Checkpoint{
		ID: command.CheckpointID, WorkspaceID: run.WorkspaceID, SessionID: run.SessionID,
		RunID: run.ID, AttemptID: attempt.ID, AttemptGeneration: attempt.Generation,
		BrainToolCatalogID: command.BrainToolCatalogID, ThreadID: command.ThreadID, TurnID: command.TurnID,
		ManifestDigest: command.ManifestDigest, CatalogDigest: command.CatalogDigest, Object: command.Object,
		CodexRuntimeManifestDigest: command.CodexRuntimeManifestDigest,
		CheckpointAllowlistVersion: command.CheckpointAllowlistVersion,
	}
	if !checkpointMatchesCommit(checkpoint, run, attempt, command) {
		t.Fatal("exact checkpoint fingerprint did not match")
	}
	changed := command
	changed.CodexRuntimeManifestDigest[0] ^= 0xff
	if checkpointMatchesCommit(checkpoint, run, attempt, changed) {
		t.Fatal("changed runtime manifest digest matched committed checkpoint")
	}
	changed = command
	changed.Object.ObjectID = "99000000-0000-4000-8000-000000000099"
	if checkpointMatchesCommit(checkpoint, run, attempt, changed) {
		t.Fatal("changed checkpoint object identity matched committed checkpoint")
	}
}

const (
	testFinalizationWorkspaceID = "91000000-0000-4000-8000-000000000001"
	testFinalizationSessionID   = "92000000-0000-4000-8000-000000000001"
)

func validBeginRunFinalizationCommand() BeginRunFinalizationCommand {
	return BeginRunFinalizationCommand{
		RunID: "93000000-0000-4000-8000-000000000001", AttemptID: "94000000-0000-4000-8000-000000000001",
		HolderID: "pool-holder", Generation: 2, ExpectedRunVersion: 3, ExpectedAttemptVersion: 2,
		ThreadID: "thread-1", TurnID: "turn-1",
		Record: TransitionRecord{
			EventID: "95000000-0000-4000-8000-000000000001", ProducerInstanceID: "96000000-0000-4000-8000-000000000001",
			ProducerSeq: 4, OutboxID: "97000000-0000-4000-8000-000000000001",
		},
	}
}

func validCommitCheckpointCommand() CommitCheckpointAndTerminalRunCommand {
	begin := validBeginRunFinalizationCommand()
	return CommitCheckpointAndTerminalRunCommand{
		RunID: begin.RunID, AttemptID: begin.AttemptID, HolderID: begin.HolderID, Generation: begin.Generation,
		ExpectedRunVersion: 4, ExpectedAttemptVersion: 3,
		CheckpointID:       "98000000-0000-4000-8000-000000000001",
		BrainToolCatalogID: "99000000-0000-4000-8000-000000000001",
		ThreadID:           begin.ThreadID, TurnID: begin.TurnID,
		ManifestDigest: sha256.Sum256([]byte("manifest")), CatalogDigest: sha256.Sum256([]byte("catalog")),
		Object: ObjectPointer{
			ObjectID: "9a000000-0000-4000-8000-000000000001", SHA256: sha256.Sum256([]byte("object")),
			Size: 1024, MediaType: checkpointartifact.ArtifactMediaType,
		},
		CodexRuntimeManifestDigest: sha256.Sum256([]byte("runtime")), CheckpointAllowlistVersion: 1,
		Record: TransitionRecord{
			EventID: "9b000000-0000-4000-8000-000000000001", ProducerInstanceID: "9c000000-0000-4000-8000-000000000001",
			ProducerSeq: 5, OutboxID: "9d000000-0000-4000-8000-000000000001",
		},
	}
}
