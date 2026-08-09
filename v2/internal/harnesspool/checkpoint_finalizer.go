package harnesspool

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"

	"github.com/agentserver/agentserver/v2/internal/checkpoint"
	"github.com/agentserver/agentserver/v2/internal/harnesscontrol"
)

type CheckpointFinalizationCore interface {
	BeginRunFinalization(context.Context, BeginRunFinalizationRequest) (BeginRunFinalizationResult, error)
	CommitCheckpoint(context.Context, CommitCheckpointRequest) (CommitCheckpointResult, error)
}

type CheckpointFinalizationIdentityAllocator interface {
	AllocateTransitionRecord() (TransitionRecord, error)
	AllocateCheckpointIdentity() (CheckpointIdentity, error)
}

// CheckpointObjectSink stores an immutable object under the supplied complete
// pointer. It must either return that exact pointer or an error; an exact retry
// with a freshly rewound reader must not create a second object.
type CheckpointObjectWriteRequest struct {
	WorkspaceID string
	Object      EventObjectPointer
}

type CheckpointObjectSink interface {
	PutCheckpointObject(context.Context, CheckpointObjectWriteRequest, io.Reader) (EventObjectPointer, error)
}

type CheckpointFinalizerConfig struct {
	StagingRoot string
}

// AttemptRuntimeRetentionError marks an ambiguous external commit boundary.
// The supervisor must not delete the attempt runtime when this error is in the
// returned chain because a retry may still need the original rollout bytes.
type AttemptRuntimeRetentionError struct {
	Operation string
	Err       error
}

func (err *AttemptRuntimeRetentionError) Error() string {
	if err == nil {
		return "<nil>"
	}
	return fmt.Sprintf("retain attempt runtime after ambiguous %s: %v", err.Operation, err.Err)
}

func (err *AttemptRuntimeRetentionError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Err
}

func RequiresAttemptRuntimeRetention(err error) bool {
	var retention *AttemptRuntimeRetentionError
	return errors.As(err, &retention)
}

type CheckpointFinalizer struct {
	core       CheckpointFinalizationCore
	identities CheckpointFinalizationIdentityAllocator
	objects    CheckpointObjectSink
	staging    string
}

func NewCheckpointFinalizer(
	core CheckpointFinalizationCore,
	identities CheckpointFinalizationIdentityAllocator,
	objects CheckpointObjectSink,
	config CheckpointFinalizerConfig,
) (*CheckpointFinalizer, error) {
	if core == nil {
		return nil, errors.New("checkpoint finalization core client is required")
	}
	if identities == nil {
		return nil, errors.New("checkpoint finalization identity allocator is required")
	}
	if objects == nil {
		return nil, errors.New("checkpoint object sink is required")
	}
	staging, err := validateCheckpointStagingRoot(config.StagingRoot)
	if err != nil {
		return nil, err
	}
	return &CheckpointFinalizer{core: core, identities: identities, objects: objects, staging: staging}, nil
}

func (finalizer *CheckpointFinalizer) FinalizeCompletedAttempt(
	ctx context.Context,
	prepared PreparedRunLaunch,
	workload AttemptWorkload,
	terminal harnesscontrol.TurnTerminalEvent,
) (_ CommitCheckpointResult, returnErr error) {
	if ctx == nil {
		return CommitCheckpointResult{}, errors.New("checkpoint finalization context is required")
	}
	if finalizer == nil || finalizer.core == nil || finalizer.identities == nil || finalizer.objects == nil {
		return CommitCheckpointResult{}, errors.New("checkpoint finalizer is required")
	}
	if workload == nil {
		return CommitCheckpointResult{}, errors.New("checkpoint finalization workload is required")
	}
	if err := validateCompletedFinalizationInput(prepared, terminal); err != nil {
		return CommitCheckpointResult{}, err
	}

	beginRecord, err := finalizer.identities.AllocateTransitionRecord()
	if err != nil {
		return CommitCheckpointResult{}, fmt.Errorf("allocate begin-finalization transition identity: %w", err)
	}
	checkpointIdentity, err := finalizer.identities.AllocateCheckpointIdentity()
	if err != nil {
		return CommitCheckpointResult{}, fmt.Errorf("allocate checkpoint identity: %w", err)
	}
	commitRecord, err := finalizer.identities.AllocateTransitionRecord()
	if err != nil {
		return CommitCheckpointResult{}, fmt.Errorf("allocate checkpoint-commit transition identity: %w", err)
	}

	claim := prepared.Scheduled.Claim
	beginRequest := BeginRunFinalizationRequest{
		RunID: claim.Run.RunID, RunAttemptID: claim.RunAttempt.RunAttemptID,
		HolderID: claim.RunAttempt.HolderID, RunAttemptGeneration: claim.RunAttempt.Generation,
		ExpectedRunVersion: claim.Run.Version + 1, ExpectedRunAttemptVersion: claim.RunAttempt.Version + 1,
		ThreadID: terminal.ThreadID, TurnID: terminal.TurnID, Record: beginRecord,
	}
	begin, err := finalizer.beginExactly(ctx, beginRequest)
	if err != nil {
		return CommitCheckpointResult{}, err
	}
	if err := validateBeginFinalizationResult(prepared, beginRequest, begin); err != nil {
		return CommitCheckpointResult{}, err
	}

	stagingDir, err := os.MkdirTemp(finalizer.staging, "attempt-checkpoint-")
	if err != nil {
		return CommitCheckpointResult{}, fmt.Errorf("create checkpoint staging directory: %w", err)
	}
	if err := os.Chmod(stagingDir, 0o700); err != nil {
		_ = os.RemoveAll(stagingDir)
		return CommitCheckpointResult{}, fmt.Errorf("restrict checkpoint staging directory: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(stagingDir); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("remove checkpoint staging directory: %w", err))
		}
	}()

	rolloutFile, err := createCheckpointStagingFile(stagingDir, "rollout-*.jsonl")
	if err != nil {
		return CommitCheckpointResult{}, err
	}
	rolloutPath := rolloutFile.Name()
	rollout, err := workload.OpenCheckpointRollout(ctx, terminal.RolloutLocator)
	if err != nil {
		_ = rolloutFile.Close()
		return CommitCheckpointResult{}, fmt.Errorf("open completed attempt rollout: %w", err)
	}
	if rollout.Reader == nil {
		_ = rolloutFile.Close()
		return CommitCheckpointResult{}, errors.New("attempt workload returned a nil checkpoint rollout reader")
	}
	descriptor, stageErr := checkpoint.StageRollout(rolloutFile, &contextReader{ctx: ctx, reader: rollout.Reader}, rollout.SizeBytes)
	closeSourceErr := rollout.Reader.Close()
	syncErr := rolloutFile.Sync()
	closeStageErr := rolloutFile.Close()
	if stageErr != nil || closeSourceErr != nil || syncErr != nil || closeStageErr != nil {
		return CommitCheckpointResult{}, errors.Join(
			wrapCheckpointError("stage completed attempt rollout", stageErr),
			wrapCheckpointError("close completed attempt rollout", closeSourceErr),
			wrapCheckpointError("sync checkpoint rollout staging", syncErr),
			wrapCheckpointError("close checkpoint rollout staging", closeStageErr),
		)
	}

	var packSetDigest *[32]byte
	packSetDigestText := ""
	if prepared.Manifest.ToolPack != nil {
		decoded, err := decodeClientSHA256(prepared.Manifest.ToolPack.PackSetDigest)
		if err != nil {
			return CommitCheckpointResult{}, fmt.Errorf("decode prepared pack-set digest: %w", err)
		}
		packSetDigest = &decoded
		packSetDigestText = prepared.Manifest.ToolPack.PackSetDigest
	}
	manifest := checkpoint.Manifest{
		ManifestVersion: checkpoint.CurrentManifestVersion, CanonicalizerVersion: checkpoint.Canonicalizer,
		CheckpointID: checkpointIdentity.CheckpointID,
		WorkspaceID:  prepared.Manifest.WorkspaceID, SessionID: prepared.Manifest.SessionID,
		RunID: prepared.Manifest.RunID, RunAttemptID: prepared.Manifest.RunAttemptID,
		RunAttemptGeneration: prepared.Manifest.RunAttemptGeneration,
		BrainThreadID:        terminal.ThreadID, TerminalTurnID: terminal.TurnID,
		CodexRuntimeManifestDigest: prepared.Manifest.CodexRuntimeManifestDigest,
		CheckpointAllowlistVersion: int64(prepared.Manifest.CheckpointAllowlistVersion),
		CatalogDigest:              prepared.Manifest.ExecutorMCP.CatalogDigest,
		PackSetDigest:              packSetDigestText,
		Files: []checkpoint.File{{
			Purpose: checkpoint.RolloutPurpose, FileType: checkpoint.RegularFileType,
			Path: terminal.RolloutLocator, Mode: checkpoint.RolloutMode,
			SizeBytes: descriptor.SizeBytes, SHA256: descriptor.SHA256,
		}},
	}

	artifactFile, err := createCheckpointStagingFile(stagingDir, "artifact-*.checkpoint")
	if err != nil {
		return CommitCheckpointResult{}, err
	}
	artifactPath := artifactFile.Name()
	rolloutStage, err := os.Open(rolloutPath)
	if err != nil {
		_ = artifactFile.Close()
		return CommitCheckpointResult{}, fmt.Errorf("reopen staged checkpoint rollout: %w", err)
	}
	artifactDescriptor, writeErr := checkpoint.WriteArtifact(artifactFile, manifest, rolloutStage)
	closeRolloutErr := rolloutStage.Close()
	syncErr = artifactFile.Sync()
	closeArtifactErr := artifactFile.Close()
	if writeErr != nil || closeRolloutErr != nil || syncErr != nil || closeArtifactErr != nil {
		return CommitCheckpointResult{}, errors.Join(
			wrapCheckpointError("write checkpoint artifact", writeErr),
			wrapCheckpointError("close staged checkpoint rollout", closeRolloutErr),
			wrapCheckpointError("sync checkpoint artifact staging", syncErr),
			wrapCheckpointError("close checkpoint artifact staging", closeArtifactErr),
		)
	}

	artifactDigest, err := decodeClientSHA256(artifactDescriptor.SHA256)
	if err != nil {
		return CommitCheckpointResult{}, fmt.Errorf("decode generated checkpoint object digest: %w", err)
	}
	expectedObject := EventObjectPointer{
		ObjectID: checkpointIdentity.ObjectID, SHA256: artifactDigest,
		Size: artifactDescriptor.SizeBytes, MediaType: artifactDescriptor.MediaType,
	}
	storedObject, err := finalizer.putObjectExactly(ctx, prepared.Manifest.WorkspaceID, artifactPath, expectedObject)
	if err != nil {
		return CommitCheckpointResult{}, err
	}
	if storedObject != expectedObject {
		return CommitCheckpointResult{}, errors.New("checkpoint object sink returned a pointer different from the generated artifact")
	}

	manifestDigest, err := decodeClientSHA256(artifactDescriptor.ManifestDigest)
	if err != nil {
		return CommitCheckpointResult{}, fmt.Errorf("decode generated checkpoint manifest digest: %w", err)
	}
	runtimeDigest, err := decodeClientSHA256(prepared.Manifest.CodexRuntimeManifestDigest)
	if err != nil {
		return CommitCheckpointResult{}, fmt.Errorf("decode prepared runtime manifest digest: %w", err)
	}
	commitRequest := CommitCheckpointRequest{
		RunID: begin.Run.RunID, RunAttemptID: begin.RunAttempt.RunAttemptID,
		HolderID: begin.RunAttempt.HolderID, RunAttemptGeneration: begin.RunAttempt.Generation,
		ExpectedRunVersion: begin.Run.Version, ExpectedRunAttemptVersion: begin.RunAttempt.Version,
		Checkpoint: CheckpointCommit{
			CheckpointID: checkpointIdentity.CheckpointID, BrainToolCatalogID: prepared.FrozenCatalog.CatalogID,
			ThreadID: terminal.ThreadID, TurnID: terminal.TurnID,
			ManifestDigest: manifestDigest, CatalogDigest: prepared.FrozenCatalog.CatalogDigest,
			Object: storedObject, CodexRuntimeManifestDigest: runtimeDigest,
			CheckpointAllowlistVersion: int64(prepared.Manifest.CheckpointAllowlistVersion),
			PackSetDigest:              packSetDigest,
		},
		Record: commitRecord,
	}
	committed, err := finalizer.commitExactly(ctx, commitRequest)
	if err != nil {
		return CommitCheckpointResult{}, err
	}
	if err := validateCommitCheckpointResult(prepared, commitRequest, committed); err != nil {
		return CommitCheckpointResult{}, err
	}
	return committed, nil
}

func (finalizer *CheckpointFinalizer) beginExactly(ctx context.Context, request BeginRunFinalizationRequest) (BeginRunFinalizationResult, error) {
	result, err := finalizer.core.BeginRunFinalization(ctx, request)
	if err == nil {
		return result, nil
	}
	if isDefiniteCoreCommand(err) {
		return BeginRunFinalizationResult{}, fmt.Errorf("begin run finalization: %w", err)
	}
	result, retryErr := finalizer.core.BeginRunFinalization(ctx, request)
	if retryErr == nil {
		return result, nil
	}
	if isDefiniteCoreCommand(retryErr) {
		return BeginRunFinalizationResult{}, fmt.Errorf("retry begin run finalization: %w", retryErr)
	}
	return BeginRunFinalizationResult{}, &AttemptRuntimeRetentionError{Operation: "begin run finalization", Err: errors.Join(err, retryErr)}
}

func (finalizer *CheckpointFinalizer) putObjectExactly(
	ctx context.Context,
	workspaceID, artifactPath string,
	expected EventObjectPointer,
) (EventObjectPointer, error) {
	put := func() (EventObjectPointer, error) {
		artifact, err := os.Open(artifactPath)
		if err != nil {
			return EventObjectPointer{}, fmt.Errorf("open staged checkpoint artifact: %w", err)
		}
		stored, putErr := finalizer.objects.PutCheckpointObject(ctx, CheckpointObjectWriteRequest{
			WorkspaceID: workspaceID, Object: expected,
		}, &contextReader{ctx: ctx, reader: artifact})
		closeErr := artifact.Close()
		return stored, errors.Join(putErr, wrapCheckpointError("close staged checkpoint artifact", closeErr))
	}
	stored, err := put()
	if err == nil {
		return stored, nil
	}
	stored, retryErr := put()
	if retryErr == nil {
		return stored, nil
	}
	return EventObjectPointer{}, &AttemptRuntimeRetentionError{Operation: "checkpoint object upload", Err: errors.Join(err, retryErr)}
}

func (finalizer *CheckpointFinalizer) commitExactly(ctx context.Context, request CommitCheckpointRequest) (CommitCheckpointResult, error) {
	result, err := finalizer.core.CommitCheckpoint(ctx, request)
	if err == nil {
		return result, nil
	}
	if isDefiniteCoreCommand(err) {
		return CommitCheckpointResult{}, fmt.Errorf("commit checkpoint and terminal run: %w", err)
	}
	result, retryErr := finalizer.core.CommitCheckpoint(ctx, request)
	if retryErr == nil {
		return result, nil
	}
	if isDefiniteCoreCommand(retryErr) {
		return CommitCheckpointResult{}, fmt.Errorf("retry checkpoint commit: %w", retryErr)
	}
	return CommitCheckpointResult{}, &AttemptRuntimeRetentionError{Operation: "checkpoint commit", Err: errors.Join(err, retryErr)}
}

func validateCheckpointStagingRoot(root string) (string, error) {
	if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return "", errors.New("checkpoint staging root must be a clean absolute path")
	}
	info, err := os.Lstat(root)
	if err != nil {
		return "", fmt.Errorf("inspect checkpoint staging root: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", errors.New("checkpoint staging root must be a direct directory")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return "", errors.New("checkpoint staging root must not be accessible by group or other")
	}
	return root, nil
}

func validateCompletedFinalizationInput(prepared PreparedRunLaunch, terminal harnesscontrol.TurnTerminalEvent) error {
	if err := validatePreparedSupervisionInput(prepared.Scheduled, prepared); err != nil {
		return fmt.Errorf("validate completed prepared launch: %w", err)
	}
	if terminal.Status != "completed" || terminal.RolloutLocator == "" ||
		!validClientProtocolText(terminal.ThreadID, 256) || !validClientProtocolText(terminal.TurnID, 256) {
		return errors.New("checkpoint finalization requires a completed terminal with thread, turn, and rollout locator")
	}
	if err := checkpoint.ValidateRolloutLocator(terminal.RolloutLocator); err != nil {
		return err
	}
	claim := prepared.Scheduled.Claim
	if claim.Run.Version > math.MaxInt64-3 || claim.RunAttempt.Version > math.MaxInt64-3 {
		return errors.New("attempt finalization versions would overflow")
	}
	if prepared.FrozenCatalog.ThreadID != "" && prepared.FrozenCatalog.ThreadID != terminal.ThreadID {
		return errors.New("completed terminal thread differs from the prepared resume catalog")
	}
	return nil
}

func validateBeginFinalizationResult(prepared PreparedRunLaunch, request BeginRunFinalizationRequest, result BeginRunFinalizationResult) error {
	if result.Run.RunID != request.RunID || result.RunAttempt.RunID != request.RunID ||
		result.Run.WorkspaceID != prepared.Manifest.WorkspaceID || result.Run.SessionID != prepared.Manifest.SessionID ||
		result.RunAttempt.RunAttemptID != request.RunAttemptID || result.Run.CurrentAttemptGeneration != request.RunAttemptGeneration ||
		result.RunAttempt.Generation != request.RunAttemptGeneration || result.RunAttempt.HolderID != request.HolderID ||
		result.RunAttempt.TerminalThreadID != request.ThreadID || result.RunAttempt.TerminalTurnID != request.TurnID ||
		result.Run.Status != "finalizing" || result.RunAttempt.Status != "finalizing" ||
		result.RunAttempt.TurnStartedAt == nil || result.Run.Version != request.ExpectedRunVersion+1 ||
		result.RunAttempt.Version != request.ExpectedRunAttemptVersion+1 {
		return errors.New("begin-finalization result does not preserve the completed attempt authority")
	}
	return nil
}

func validateCommitCheckpointResult(prepared PreparedRunLaunch, request CommitCheckpointRequest, result CommitCheckpointResult) error {
	if result.Run.RunID != request.RunID || result.Run.Status != "completed" ||
		result.Run.WorkspaceID != prepared.Manifest.WorkspaceID || result.Run.SessionID != prepared.Manifest.SessionID ||
		result.Run.CurrentAttemptGeneration != request.RunAttemptGeneration || result.Run.Version != request.ExpectedRunVersion+1 ||
		result.RunAttempt.RunAttemptID != request.RunAttemptID || result.RunAttempt.RunID != request.RunID ||
		result.RunAttempt.Status != "succeeded" || result.RunAttempt.Generation != request.RunAttemptGeneration ||
		result.RunAttempt.HolderID != request.HolderID || result.RunAttempt.TerminalThreadID != request.Checkpoint.ThreadID ||
		result.RunAttempt.TerminalTurnID != request.Checkpoint.TurnID || result.RunAttempt.TurnStartedAt == nil ||
		result.RunAttempt.Version != request.ExpectedRunAttemptVersion+1 ||
		result.Checkpoint.WorkspaceID != prepared.Manifest.WorkspaceID || result.Checkpoint.SessionID != prepared.Manifest.SessionID ||
		!committedCheckpointMatchesRequest(result.Checkpoint, request) || result.Checkpoint.CreatedAt.IsZero() || result.SessionVersion < 1 {
		return errors.New("checkpoint commit result does not preserve the terminal checkpoint authority")
	}
	return nil
}

func createCheckpointStagingFile(directory, pattern string) (*os.File, error) {
	file, err := os.CreateTemp(directory, pattern)
	if err != nil {
		return nil, fmt.Errorf("create checkpoint staging file: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("restrict checkpoint staging file: %w", err)
	}
	return file, nil
}

func isDefiniteCoreCommand(err error) bool {
	var commandError *CoreCommandError
	return errors.As(err, &commandError)
}

func wrapCheckpointError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", operation, err)
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader *contextReader) Read(destination []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(destination)
}
