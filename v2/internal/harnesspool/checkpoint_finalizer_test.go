package harnesspool

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/checkpoint"
	"github.com/agentserver/agentserver/v2/internal/harnesscontrol"
	"github.com/agentserver/agentserver/v2/internal/runmanifest"
)

const checkpointFinalizerRolloutLocator = "sessions/2026/07/31/rollout-finalizer.jsonl"

func TestCheckpointFinalizerCommitsFreshAndResumedArtifactsBeforeCleanup(t *testing.T) {
	for _, test := range []struct {
		name     string
		prepared func(*testing.T) PreparedRunLaunch
	}{
		{name: "fresh", prepared: poolTestPreparedLaunch},
		{name: "resume", prepared: checkpointResumePreparedLaunch},
		{name: "managed", prepared: checkpointManagedPreparedLaunch},
	} {
		t.Run(test.name, func(t *testing.T) {
			prepared := test.prepared(t)
			threadID := prepared.FrozenCatalog.ThreadID
			if threadID == "" {
				threadID = "thread-checkpoint-finalizer"
			}
			terminal := checkpointFinalizerTerminal(threadID)
			rollout := []byte("{\"type\":\"session_meta\"}\n{\"type\":\"response_item\",\"payload\":\"ok\"}\n")
			order := []string{}
			core := &checkpointFinalizerTestCore{prepared: prepared, order: &order}
			workload := &checkpointFinalizerTestWorkload{
				rollout: rollout, expectedSize: int64(len(rollout)), stopped: true, order: &order,
			}
			objects := &checkpointFinalizerTestObjects{order: &order, inspectPermissions: true}
			identities := newCheckpointFinalizerTestIdentities()
			stagingRoot := secureCheckpointStagingRoot(t)
			finalizer := newCheckpointFinalizerForTest(t, core, identities, objects, stagingRoot)

			committed, err := finalizer.FinalizeCompletedAttempt(t.Context(), prepared, workload, terminal)
			if err != nil {
				t.Fatal(err)
			}
			if err := workload.Cleanup(t.Context()); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(order, []string{"begin", "open", "upload", "commit", "cleanup"}) {
				t.Fatalf("finalization order = %q", order)
			}
			if len(core.beginRequests) != 1 || len(core.commitRequests) != 1 || len(objects.payloads) != 1 {
				t.Fatalf("begin/commit/upload calls = %d/%d/%d", len(core.beginRequests), len(core.commitRequests), len(objects.payloads))
			}
			if len(objects.workspaces) != 1 || objects.workspaces[0] != prepared.Manifest.WorkspaceID {
				t.Fatalf("checkpoint object workspace = %q, want %q", objects.workspaces, prepared.Manifest.WorkspaceID)
			}
			claim := prepared.Scheduled.Claim
			begin := core.beginRequests[0]
			if begin.ExpectedRunVersion != claim.Run.Version+1 || begin.ExpectedRunAttemptVersion != claim.RunAttempt.Version+1 ||
				begin.ThreadID != terminal.ThreadID || begin.TurnID != terminal.TurnID {
				t.Fatalf("begin request = %+v", begin)
			}
			commit := core.commitRequests[0]
			if commit.ExpectedRunVersion != begin.ExpectedRunVersion+1 || commit.ExpectedRunAttemptVersion != begin.ExpectedRunAttemptVersion+1 ||
				commit.Checkpoint.BrainToolCatalogID != prepared.FrozenCatalog.CatalogID ||
				commit.Checkpoint.CatalogDigest != prepared.FrozenCatalog.CatalogDigest || committed.Checkpoint != core.lastCommit.Checkpoint {
				t.Fatalf("commit request/result = %+v / %+v", commit, committed)
			}
			assertCheckpointArtifactForTest(t, objects.payloads[0], prepared, terminal, commit)
			if identities.transitionCalls != 2 || identities.checkpointCalls != 1 {
				t.Fatalf("identity allocations = transitions %d checkpoint %d", identities.transitionCalls, identities.checkpointCalls)
			}
			if objects.stagedDirectory == "" || objects.stagedArtifact == "" {
				t.Fatal("object sink did not inspect pool staging")
			}
			if _, err := os.Stat(objects.stagedDirectory); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("staging directory survived finalization: %v", err)
			}
			assertCheckpointStagingEmpty(t, stagingRoot)
		})
	}
}

func TestCheckpointFinalizerRejectsMalformedOrDriftingRolloutBeforeUpload(t *testing.T) {
	valid := []byte("{\"type\":\"session_meta\"}\n")
	for _, test := range []struct {
		name         string
		rollout      []byte
		expectedSize int64
	}{
		{name: "malformed JSONL", rollout: []byte("not-json\n"), expectedSize: int64(len("not-json\n"))},
		{name: "short size", rollout: valid, expectedSize: int64(len(valid) + 1)},
		{name: "long size", rollout: append(append([]byte(nil), valid...), []byte("{}\n")...), expectedSize: int64(len(valid))},
	} {
		t.Run(test.name, func(t *testing.T) {
			prepared := poolTestPreparedLaunch(t)
			core := &checkpointFinalizerTestCore{prepared: prepared}
			workload := &checkpointFinalizerTestWorkload{rollout: test.rollout, expectedSize: test.expectedSize, stopped: true}
			objects := &checkpointFinalizerTestObjects{}
			stagingRoot := secureCheckpointStagingRoot(t)
			finalizer := newCheckpointFinalizerForTest(t, core, newCheckpointFinalizerTestIdentities(), objects, stagingRoot)

			if _, err := finalizer.FinalizeCompletedAttempt(t.Context(), prepared, workload, checkpointFinalizerTerminal("thread-rollout-validation")); err == nil {
				t.Fatal("FinalizeCompletedAttempt() unexpectedly succeeded")
			}
			if len(objects.payloads) != 0 || len(core.commitRequests) != 0 {
				t.Fatalf("invalid rollout reached upload/commit: %d/%d", len(objects.payloads), len(core.commitRequests))
			}
			assertCheckpointStagingEmpty(t, stagingRoot)
		})
	}
}

func TestCheckpointFinalizerRejectsObjectPointerDriftBeforeCommit(t *testing.T) {
	prepared := poolTestPreparedLaunch(t)
	rollout := []byte("{\"type\":\"session_meta\"}\n")
	core := &checkpointFinalizerTestCore{prepared: prepared}
	objects := &checkpointFinalizerTestObjects{mutatePointer: func(pointer EventObjectPointer) EventObjectPointer {
		pointer.ObjectID = "79000000-0000-4000-8000-000000000099"
		return pointer
	}}
	finalizer := newCheckpointFinalizerForTest(
		t, core, newCheckpointFinalizerTestIdentities(), objects, secureCheckpointStagingRoot(t),
	)
	workload := &checkpointFinalizerTestWorkload{rollout: rollout, expectedSize: int64(len(rollout)), stopped: true}

	_, err := finalizer.FinalizeCompletedAttempt(t.Context(), prepared, workload, checkpointFinalizerTerminal("thread-object-drift"))
	if err == nil || !strings.Contains(err.Error(), "pointer different") || len(core.commitRequests) != 0 {
		t.Fatalf("pointer drift error/commits = %v/%d", err, len(core.commitRequests))
	}
}

func TestCheckpointFinalizerRetriesAmbiguousBoundariesWithExactIdentity(t *testing.T) {
	ambiguous := errors.New("synthetic response lost")
	for _, operation := range []string{"begin", "upload", "commit"} {
		t.Run(operation, func(t *testing.T) {
			prepared := poolTestPreparedLaunch(t)
			rollout := []byte("{\"type\":\"session_meta\"}\n")
			core := &checkpointFinalizerTestCore{prepared: prepared}
			objects := &checkpointFinalizerTestObjects{}
			switch operation {
			case "begin":
				core.beginErrors = []error{ambiguous}
			case "upload":
				objects.errors = []error{ambiguous}
			case "commit":
				core.commitErrors = []error{ambiguous}
			}
			identities := newCheckpointFinalizerTestIdentities()
			finalizer := newCheckpointFinalizerForTest(t, core, identities, objects, secureCheckpointStagingRoot(t))
			workload := &checkpointFinalizerTestWorkload{rollout: rollout, expectedSize: int64(len(rollout)), stopped: true}

			if _, err := finalizer.FinalizeCompletedAttempt(t.Context(), prepared, workload, checkpointFinalizerTerminal("thread-exact-retry")); err != nil {
				t.Fatal(err)
			}
			if identities.transitionCalls != 2 || identities.checkpointCalls != 1 {
				t.Fatalf("retry reallocated identities: transitions %d checkpoint %d", identities.transitionCalls, identities.checkpointCalls)
			}
			switch operation {
			case "begin":
				if len(core.beginRequests) != 2 || !reflect.DeepEqual(core.beginRequests[0], core.beginRequests[1]) {
					t.Fatalf("begin retries changed request: %+v", core.beginRequests)
				}
			case "upload":
				if len(objects.pointers) != 2 || objects.pointers[0] != objects.pointers[1] || !bytes.Equal(objects.payloads[0], objects.payloads[1]) {
					t.Fatalf("upload retries changed pointer or bytes: %+v", objects.pointers)
				}
			case "commit":
				if len(core.commitRequests) != 2 || !reflect.DeepEqual(core.commitRequests[0], core.commitRequests[1]) {
					t.Fatalf("commit retries changed request: %+v", core.commitRequests)
				}
			}
		})
	}
}

func TestCheckpointFinalizerRetainsAttemptAfterRepeatedAmbiguity(t *testing.T) {
	ambiguousOne := errors.New("synthetic response lost one")
	ambiguousTwo := errors.New("synthetic response lost two")
	for _, operation := range []string{"begin", "upload", "commit"} {
		t.Run(operation, func(t *testing.T) {
			prepared := poolTestPreparedLaunch(t)
			rollout := []byte("{\"type\":\"session_meta\"}\n")
			core := &checkpointFinalizerTestCore{prepared: prepared}
			objects := &checkpointFinalizerTestObjects{}
			switch operation {
			case "begin":
				core.beginErrors = []error{ambiguousOne, ambiguousTwo}
			case "upload":
				objects.errors = []error{ambiguousOne, ambiguousTwo}
			case "commit":
				core.commitErrors = []error{ambiguousOne, ambiguousTwo}
			}
			stagingRoot := secureCheckpointStagingRoot(t)
			finalizer := newCheckpointFinalizerForTest(t, core, newCheckpointFinalizerTestIdentities(), objects, stagingRoot)
			workload := &checkpointFinalizerTestWorkload{rollout: rollout, expectedSize: int64(len(rollout)), stopped: true}

			_, err := finalizer.FinalizeCompletedAttempt(t.Context(), prepared, workload, checkpointFinalizerTerminal("thread-retention"))
			if err == nil || !RequiresAttemptRuntimeRetention(err) || !strings.Contains(err.Error(), operation) {
				t.Fatalf("repeated %s ambiguity error = %v", operation, err)
			}
			assertCheckpointStagingEmpty(t, stagingRoot)
		})
	}
}

func TestCheckpointFinalizerRejectsCommittedCheckpointFingerprintDrift(t *testing.T) {
	mutations := map[string]func(*CommitCheckpointResult){
		"catalog ID": func(result *CommitCheckpointResult) {
			result.Checkpoint.BrainToolCatalogID = "79000000-0000-4000-8000-000000000091"
		},
		"manifest digest": func(result *CommitCheckpointResult) {
			result.Checkpoint.ManifestDigest = sha256.Sum256([]byte("changed manifest"))
		},
		"catalog digest": func(result *CommitCheckpointResult) {
			result.Checkpoint.CatalogDigest = sha256.Sum256([]byte("changed catalog"))
		},
		"runtime digest": func(result *CommitCheckpointResult) {
			result.Checkpoint.CodexRuntimeManifestDigest = sha256.Sum256([]byte("changed runtime"))
		},
		"allowlist": func(result *CommitCheckpointResult) {
			result.Checkpoint.CheckpointAllowlistVersion++
		},
		"source generation": func(result *CommitCheckpointResult) {
			result.Checkpoint.RunAttemptGeneration++
		},
		"workspace": func(result *CommitCheckpointResult) {
			result.Checkpoint.WorkspaceID = "79000000-0000-4000-8000-000000000092"
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			prepared := checkpointManagedPreparedLaunch(t)
			rollout := []byte("{\"type\":\"session_meta\"}\n")
			core := &checkpointFinalizerTestCore{prepared: prepared, commitMutate: mutate}
			finalizer := newCheckpointFinalizerForTest(
				t, core, newCheckpointFinalizerTestIdentities(), &checkpointFinalizerTestObjects{}, secureCheckpointStagingRoot(t),
			)
			workload := &checkpointFinalizerTestWorkload{rollout: rollout, expectedSize: int64(len(rollout)), stopped: true}
			if _, err := finalizer.FinalizeCompletedAttempt(t.Context(), prepared, workload, checkpointFinalizerTerminal("thread-result-drift")); err == nil ||
				!strings.Contains(err.Error(), "checkpoint commit result") {
				t.Fatalf("fingerprint drift error = %v", err)
			}
		})
	}
}

func newCheckpointFinalizerForTest(
	t *testing.T,
	core CheckpointFinalizationCore,
	identities CheckpointFinalizationIdentityAllocator,
	objects CheckpointObjectSink,
	stagingRoot string,
) *CheckpointFinalizer {
	t.Helper()
	finalizer, err := NewCheckpointFinalizer(core, identities, objects, CheckpointFinalizerConfig{StagingRoot: stagingRoot})
	if err != nil {
		t.Fatal(err)
	}
	return finalizer
}

func secureCheckpointStagingRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	return root
}

func assertCheckpointStagingEmpty(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("checkpoint staging root retained %d entries", len(entries))
	}
}

func assertCheckpointArtifactForTest(
	t *testing.T,
	artifact []byte,
	prepared PreparedRunLaunch,
	terminal harnesscontrol.TurnTerminalEvent,
	commit CommitCheckpointRequest,
) {
	t.Helper()
	objectDigest := sha256.Sum256(artifact)
	if commit.Checkpoint.Object.SHA256 != objectDigest || commit.Checkpoint.Object.Size != int64(len(artifact)) ||
		commit.Checkpoint.Object.MediaType != checkpoint.ArtifactMediaType {
		t.Fatalf("committed object pointer = %+v", commit.Checkpoint.Object)
	}
	var gotManifest checkpoint.Manifest
	var canonical []byte
	var gotRollout []byte
	err := checkpoint.ReadArtifact(bytes.NewReader(artifact), int64(len(artifact)), func(manifest checkpoint.Manifest, raw []byte, rollout io.Reader) error {
		gotManifest = manifest
		canonical = append([]byte(nil), raw...)
		var err error
		gotRollout, err = io.ReadAll(rollout)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	manifestDigest, err := checkpoint.Digest(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(commit.Checkpoint.ManifestDigest[:]) != manifestDigest ||
		gotManifest.CheckpointID != commit.Checkpoint.CheckpointID || gotManifest.WorkspaceID != prepared.Manifest.WorkspaceID ||
		gotManifest.SessionID != prepared.Manifest.SessionID || gotManifest.RunID != prepared.Manifest.RunID ||
		gotManifest.RunAttemptID != prepared.Manifest.RunAttemptID || gotManifest.RunAttemptGeneration != prepared.Manifest.RunAttemptGeneration ||
		gotManifest.BrainThreadID != terminal.ThreadID || gotManifest.TerminalTurnID != terminal.TurnID ||
		gotManifest.CatalogDigest != prepared.Manifest.ExecutorMCP.CatalogDigest || len(gotRollout) == 0 {
		t.Fatalf("checkpoint manifest/rollout = %+v / %q", gotManifest, gotRollout)
	}
}

func checkpointFinalizerTerminal(threadID string) harnesscontrol.TurnTerminalEvent {
	return harnesscontrol.TurnTerminalEvent{
		Kind: harnesscontrol.EventKindTurnTerminal, ThreadID: threadID, TurnID: "turn-checkpoint-finalizer",
		Status: "completed", RolloutLocator: checkpointFinalizerRolloutLocator,
	}
}

func checkpointResumePreparedLaunch(t *testing.T) PreparedRunLaunch {
	t.Helper()
	inputs := testRunLaunchInputs()
	proposal, err := BuildExecutorCatalog(inputs.ExecutorCatalogPolicy)
	if err != nil {
		t.Fatal(err)
	}
	catalog := resolverCheckpointCatalog(proposal, "thread-checkpoint-resume")
	inputs.PreviousCheckpoint = &runmanifest.PreviousCheckpoint{
		CheckpointID: "47000000-0000-4000-8000-000000000004",
		RunID:        "4c000000-0000-4000-8000-000000000004", RunAttemptID: "4d000000-0000-4000-8000-000000000004",
		RunAttemptGeneration: 2, ThreadID: catalog.ThreadID, TurnID: "turn-previous",
		ManifestDigest: strings.Repeat("d", 64), CatalogDigest: proposal.Catalog.Digest(),
		CodexRuntimeManifestDigest: inputs.CodexRuntimeManifestDigest,
		CheckpointAllowlistVersion: int64(inputs.CheckpointAllowlistVersion),
		Object: runmanifest.ObjectPointer{
			ObjectID: "48000000-0000-4000-8000-000000000004", SHA256: strings.Repeat("e", 64),
			SizeBytes: 1024, MediaType: checkpoint.ArtifactMediaType,
		},
	}
	inputs.PreviousBrainToolCatalog = &catalog
	preparer := newTestLaunchPreparer(
		t, &recordingLaunchCore{}, &fixedCatalogAllocator{id: "79000000-0000-4000-8000-000000000093"},
		&fixedLaunchResolver{inputs: inputs},
	)
	prepared, err := preparer.Prepare(t.Context(), ScheduledRunAttempt{
		Dispatch: testControllerDispatch("starting"), Claim: testControllerClaim(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return prepared
}

func checkpointManagedPreparedLaunch(t *testing.T) PreparedRunLaunch {
	t.Helper()
	inputs := testRunLaunchInputs()
	spec := poolTestManagedSandboxSpec()
	inputs.ManagedSandbox = &spec
	preparer := newTestLaunchPreparer(
		t, &recordingLaunchCore{}, &fixedCatalogAllocator{id: "79000000-0000-4000-8000-000000000094"},
		&fixedLaunchResolver{inputs: inputs},
	)
	prepared, err := preparer.Prepare(t.Context(), ScheduledRunAttempt{
		Dispatch: testControllerDispatch("starting"), Claim: testControllerClaim(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return prepared
}

type checkpointFinalizerTestIdentities struct {
	records         []TransitionRecord
	checkpoint      CheckpointIdentity
	transitionCalls int
	checkpointCalls int
}

func newCheckpointFinalizerTestIdentities() *checkpointFinalizerTestIdentities {
	return &checkpointFinalizerTestIdentities{
		records: []TransitionRecord{
			{EventID: "71000000-0000-4000-8000-000000000071", ProducerInstanceID: "72000000-0000-4000-8000-000000000070", ProducerSeq: 71, OutboxID: "73000000-0000-4000-8000-000000000071"},
			{EventID: "71000000-0000-4000-8000-000000000072", ProducerInstanceID: "72000000-0000-4000-8000-000000000070", ProducerSeq: 72, OutboxID: "73000000-0000-4000-8000-000000000072"},
		},
		checkpoint: CheckpointIdentity{
			CheckpointID: "74000000-0000-4000-8000-000000000070",
			ObjectID:     "75000000-0000-4000-8000-000000000070",
		},
	}
}

func (identities *checkpointFinalizerTestIdentities) AllocateTransitionRecord() (TransitionRecord, error) {
	identities.transitionCalls++
	if len(identities.records) == 0 {
		return TransitionRecord{}, errors.New("no test transition identity")
	}
	record := identities.records[0]
	identities.records = identities.records[1:]
	return record, nil
}

func (identities *checkpointFinalizerTestIdentities) AllocateCheckpointIdentity() (CheckpointIdentity, error) {
	identities.checkpointCalls++
	return identities.checkpoint, nil
}

type checkpointFinalizerTestCore struct {
	prepared       PreparedRunLaunch
	order          *[]string
	beginErrors    []error
	commitErrors   []error
	commitMutate   func(*CommitCheckpointResult)
	beginRequests  []BeginRunFinalizationRequest
	commitRequests []CommitCheckpointRequest
	lastCommit     CommitCheckpointResult
}

func (core *checkpointFinalizerTestCore) BeginRunFinalization(_ context.Context, request BeginRunFinalizationRequest) (BeginRunFinalizationResult, error) {
	core.beginRequests = append(core.beginRequests, request)
	checkpointFinalizerAppendOrder(core.order, "begin")
	if err := checkpointFinalizerPopError(&core.beginErrors); err != nil {
		return BeginRunFinalizationResult{}, err
	}
	now := time.Date(2026, 7, 31, 18, 0, 0, 0, time.UTC)
	run := core.prepared.Scheduled.Claim.Run
	run.Status = "finalizing"
	run.Version = request.ExpectedRunVersion + 1
	run.UpdatedAt = now
	attempt := core.prepared.Scheduled.Claim.RunAttempt
	attempt.Status = "finalizing"
	attempt.Version = request.ExpectedRunAttemptVersion + 1
	attempt.TerminalThreadID = request.ThreadID
	attempt.TerminalTurnID = request.TurnID
	attempt.TurnStartedAt = &now
	attempt.UpdatedAt = now
	return BeginRunFinalizationResult{Run: run, RunAttempt: attempt, Changed: true}, nil
}

func (core *checkpointFinalizerTestCore) CommitCheckpoint(_ context.Context, request CommitCheckpointRequest) (CommitCheckpointResult, error) {
	core.commitRequests = append(core.commitRequests, request)
	checkpointFinalizerAppendOrder(core.order, "commit")
	if err := checkpointFinalizerPopError(&core.commitErrors); err != nil {
		return CommitCheckpointResult{}, err
	}
	now := time.Date(2026, 7, 31, 18, 1, 0, 0, time.UTC)
	run := core.prepared.Scheduled.Claim.Run
	run.Status = "completed"
	run.Version = request.ExpectedRunVersion + 1
	run.UpdatedAt = now
	attempt := core.prepared.Scheduled.Claim.RunAttempt
	attempt.Status = "succeeded"
	attempt.Version = request.ExpectedRunAttemptVersion + 1
	attempt.TerminalThreadID = request.Checkpoint.ThreadID
	attempt.TerminalTurnID = request.Checkpoint.TurnID
	attempt.TurnStartedAt = &now
	attempt.UpdatedAt = now
	checkpointResult := CommittedCheckpoint{
		CheckpointID: request.Checkpoint.CheckpointID,
		WorkspaceID:  core.prepared.Manifest.WorkspaceID, SessionID: core.prepared.Manifest.SessionID,
		RunID: request.RunID, RunAttemptID: request.RunAttemptID, RunAttemptGeneration: request.RunAttemptGeneration,
		BrainToolCatalogID: request.Checkpoint.BrainToolCatalogID,
		ThreadID:           request.Checkpoint.ThreadID, TurnID: request.Checkpoint.TurnID,
		ManifestDigest: request.Checkpoint.ManifestDigest, CatalogDigest: request.Checkpoint.CatalogDigest,
		Object: request.Checkpoint.Object, CodexRuntimeManifestDigest: request.Checkpoint.CodexRuntimeManifestDigest,
		CheckpointAllowlistVersion: request.Checkpoint.CheckpointAllowlistVersion,
		CreatedAt:                  now,
	}
	result := CommitCheckpointResult{
		Run: run, RunAttempt: attempt, Checkpoint: checkpointResult, SessionVersion: 2, Created: true,
	}
	if core.commitMutate != nil {
		core.commitMutate(&result)
	}
	core.lastCommit = result
	return result, nil
}

type checkpointFinalizerTestObjects struct {
	order              *[]string
	errors             []error
	mutatePointer      func(EventObjectPointer) EventObjectPointer
	inspectPermissions bool
	workspaces         []string
	pointers           []EventObjectPointer
	payloads           [][]byte
	stagedDirectory    string
	stagedArtifact     string
}

func (objects *checkpointFinalizerTestObjects) PutCheckpointObject(
	_ context.Context,
	request CheckpointObjectWriteRequest,
	source io.Reader,
) (EventObjectPointer, error) {
	checkpointFinalizerAppendOrder(objects.order, "upload")
	objects.workspaces = append(objects.workspaces, request.WorkspaceID)
	pointer := request.Object
	objects.pointers = append(objects.pointers, pointer)
	if objects.inspectPermissions {
		objects.inspectStagingPermissions(source)
	}
	payload, readErr := io.ReadAll(source)
	objects.payloads = append(objects.payloads, payload)
	if readErr != nil {
		return EventObjectPointer{}, readErr
	}
	if err := checkpointFinalizerPopError(&objects.errors); err != nil {
		return EventObjectPointer{}, err
	}
	if objects.mutatePointer != nil {
		return objects.mutatePointer(pointer), nil
	}
	return pointer, nil
}

func (objects *checkpointFinalizerTestObjects) inspectStagingPermissions(source io.Reader) {
	wrapped, ok := source.(*contextReader)
	if !ok {
		return
	}
	file, ok := wrapped.reader.(*os.File)
	if !ok {
		return
	}
	objects.stagedArtifact = file.Name()
	objects.stagedDirectory = filepath.Dir(file.Name())
	directory, err := os.Stat(objects.stagedDirectory)
	if err != nil || directory.Mode().Perm() != 0o700 {
		objects.errors = append([]error{errors.New("checkpoint staging directory is not mode 0700")}, objects.errors...)
		return
	}
	entries, err := os.ReadDir(objects.stagedDirectory)
	if err != nil || len(entries) != 2 {
		objects.errors = append([]error{errors.New("checkpoint staging does not contain exactly rollout and artifact")}, objects.errors...)
		return
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			objects.errors = append([]error{errors.New("checkpoint staging file is not a mode 0600 regular file")}, objects.errors...)
			return
		}
	}
}

type checkpointFinalizerTestWorkload struct {
	rollout      []byte
	expectedSize int64
	stopped      bool
	openErr      error
	cleanupErr   error
	order        *[]string
	openCalls    int
	cleanupCalls int
}

func (*checkpointFinalizerTestWorkload) Wait(context.Context) error { return nil }

func (*checkpointFinalizerTestWorkload) Stop(context.Context) error { return nil }

func (workload *checkpointFinalizerTestWorkload) OpenCheckpointRollout(_ context.Context, locator string) (AttemptCheckpointRollout, error) {
	workload.openCalls++
	checkpointFinalizerAppendOrder(workload.order, "open")
	if !workload.stopped {
		return AttemptCheckpointRollout{}, errors.New("attempt process tree is still live")
	}
	if locator != checkpointFinalizerRolloutLocator {
		return AttemptCheckpointRollout{}, errors.New("unexpected checkpoint rollout locator")
	}
	if workload.openErr != nil {
		return AttemptCheckpointRollout{}, workload.openErr
	}
	return AttemptCheckpointRollout{
		Reader: io.NopCloser(bytes.NewReader(workload.rollout)), SizeBytes: workload.expectedSize,
	}, nil
}

func (workload *checkpointFinalizerTestWorkload) Cleanup(context.Context) error {
	workload.cleanupCalls++
	checkpointFinalizerAppendOrder(workload.order, "cleanup")
	return workload.cleanupErr
}

func checkpointFinalizerAppendOrder(order *[]string, value string) {
	if order != nil {
		*order = append(*order, value)
	}
}

func checkpointFinalizerPopError(values *[]error) error {
	if len(*values) == 0 {
		return nil
	}
	err := (*values)[0]
	*values = (*values)[1:]
	return err
}
