package harnessworker

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/checkpoint"
	"github.com/agentserver/agentserver/v2/internal/runmanifest"
)

func TestLoadCheckpointVerifiesAuthorityAndRestoresOneRollout(t *testing.T) {
	current, artifact, rollout := workerCheckpointFixture(t)
	reader := workerCheckpointPipe(t, artifact)
	codexHome := cleanCheckpointDirectory(t)
	staging := cleanCheckpointDirectory(t)
	restored, err := LoadCheckpoint(reader, current, codexHome, staging)
	if err != nil {
		t.Fatal(err)
	}
	if restored == nil || restored.Manifest.BrainThreadID != current.PreviousCheckpoint.ThreadID ||
		!filepath.IsAbs(restored.RolloutPath) {
		t.Fatalf("restored checkpoint = %+v", restored)
	}
	contents, err := os.ReadFile(restored.RolloutPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(contents, rollout) {
		t.Fatal("restored rollout bytes changed")
	}
	info, err := os.Lstat(restored.RolloutPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != checkpoint.RolloutMode {
		t.Fatalf("restored rollout mode = %v, err = %v", info.Mode(), err)
	}
	if _, err := reader.Stat(); err == nil {
		t.Fatal("LoadCheckpoint left the inherited pipe open")
	}
	entries, err := os.ReadDir(staging)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("checkpoint staging retained %d entries", len(entries))
	}
}

func TestLoadCheckpointRejectsObjectAndSourceAuthorityDrift(t *testing.T) {
	base, artifact, _ := workerCheckpointFixture(t)
	tests := []struct {
		name   string
		mutate func(*runmanifest.Manifest)
		want   string
	}{
		{name: "outer object digest", mutate: func(m *runmanifest.Manifest) { m.PreviousCheckpoint.Object.SHA256 = strings.Repeat("0", 64) }, want: "object digest"},
		{name: "source attempt generation", mutate: func(m *runmanifest.Manifest) { m.PreviousCheckpoint.RunAttemptGeneration++ }, want: "resume authority"},
		{name: "runtime digest", mutate: func(m *runmanifest.Manifest) { m.CodexRuntimeManifestDigest = strings.Repeat("f", 64) }, want: "resume authority"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current := base
			previous := *base.PreviousCheckpoint
			current.PreviousCheckpoint = &previous
			test.mutate(&current)
			reader := workerCheckpointPipe(t, artifact)
			codexHome := cleanCheckpointDirectory(t)
			_, err := LoadCheckpoint(reader, current, codexHome, cleanCheckpointDirectory(t))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("LoadCheckpoint() error = %v, want %q", err, test.want)
			}
			entries, readErr := os.ReadDir(codexHome)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if len(entries) != 0 {
				t.Fatalf("failed restore created %d CODEX_HOME entries", len(entries))
			}
		})
	}
}

func TestLoadCheckpointRejectsSymlinkDestinationAndUnexpectedPipe(t *testing.T) {
	current, artifact, _ := workerCheckpointFixture(t)
	codexHome := cleanCheckpointDirectory(t)
	outside := cleanCheckpointDirectory(t)
	if err := os.Symlink(outside, filepath.Join(codexHome, "sessions")); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	reader := workerCheckpointPipe(t, artifact)
	if _, err := LoadCheckpoint(reader, current, codexHome, cleanCheckpointDirectory(t)); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink restore error = %v", err)
	}
	outsideEntries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(outsideEntries) != 0 {
		t.Fatal("checkpoint restore followed a symlink outside CODEX_HOME")
	}

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	current.PreviousCheckpoint = nil
	if _, err := LoadCheckpoint(reader, current, codexHome, cleanCheckpointDirectory(t)); err == nil || !strings.Contains(err.Error(), "without signed") {
		t.Fatalf("unexpected checkpoint pipe error = %v", err)
	}
	if _, err := reader.Stat(); err == nil {
		t.Fatal("rejected unexpected checkpoint pipe remained open")
	}
}

func workerCheckpointFixture(t *testing.T) (runmanifest.Manifest, []byte, []byte) {
	t.Helper()
	rollout := []byte("{\"type\":\"session_meta\",\"payload\":{\"id\":\"thread-worker-checkpoint\"}}\n{\"type\":\"event_msg\",\"payload\":{\"type\":\"task_complete\"}}\n")
	rolloutDigest := sha256.Sum256(rollout)
	manifest := checkpoint.Manifest{
		ManifestVersion: checkpoint.CurrentManifestVersion, CanonicalizerVersion: checkpoint.Canonicalizer,
		CheckpointID: "61000000-0000-4000-8000-000000000006",
		WorkspaceID:  "62000000-0000-4000-8000-000000000006",
		SessionID:    "63000000-0000-4000-8000-000000000006",
		RunID:        "64000000-0000-4000-8000-000000000006",
		RunAttemptID: "65000000-0000-4000-8000-000000000006", RunAttemptGeneration: 4,
		BrainThreadID: "thread-worker-checkpoint", TerminalTurnID: "turn-worker-checkpoint",
		CodexRuntimeManifestDigest: strings.Repeat("a", 64), CheckpointAllowlistVersion: 1,
		CatalogDigest: strings.Repeat("b", 64),
		Files: []checkpoint.File{{
			Purpose: checkpoint.RolloutPurpose, FileType: checkpoint.RegularFileType,
			Path: "sessions/2026/07/31/rollout-worker-checkpoint.jsonl", Mode: checkpoint.RolloutMode,
			SizeBytes: int64(len(rollout)), SHA256: hex.EncodeToString(rolloutDigest[:]),
		}},
	}
	var artifact bytes.Buffer
	descriptor, err := checkpoint.WriteArtifact(&artifact, manifest, bytes.NewReader(rollout))
	if err != nil {
		t.Fatal(err)
	}
	current := runmanifest.Manifest{
		WorkspaceID: manifest.WorkspaceID, SessionID: manifest.SessionID,
		CodexRuntimeManifestDigest: manifest.CodexRuntimeManifestDigest,
		CheckpointAllowlistVersion: int(manifest.CheckpointAllowlistVersion),
		ExecutorMCP:                runmanifest.ExecutorMCP{CatalogDigest: manifest.CatalogDigest},
		PreviousCheckpoint: &runmanifest.PreviousCheckpoint{
			CheckpointID: manifest.CheckpointID, RunID: manifest.RunID, RunAttemptID: manifest.RunAttemptID,
			RunAttemptGeneration: manifest.RunAttemptGeneration, ThreadID: manifest.BrainThreadID, TurnID: manifest.TerminalTurnID,
			ManifestDigest: descriptor.ManifestDigest, CatalogDigest: manifest.CatalogDigest,
			CodexRuntimeManifestDigest: manifest.CodexRuntimeManifestDigest,
			CheckpointAllowlistVersion: manifest.CheckpointAllowlistVersion,
			Object: runmanifest.ObjectPointer{
				ObjectID: "66000000-0000-4000-8000-000000000006", SHA256: descriptor.SHA256,
				SizeBytes: descriptor.SizeBytes, MediaType: descriptor.MediaType,
			},
		},
	}
	return current, artifact.Bytes(), rollout
}

func workerCheckpointPipe(t *testing.T, contents []byte) *os.File {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, writeErr := io.Copy(writer, bytes.NewReader(contents))
		done <- errors.Join(writeErr, writer.Close())
	}()
	t.Cleanup(func() {
		_ = reader.Close()
		if err := <-done; err != nil && !errors.Is(err, os.ErrClosed) {
			t.Errorf("write checkpoint pipe: %v", err)
		}
	})
	return reader
}

func cleanCheckpointDirectory(t *testing.T) string {
	t.Helper()
	path := t.TempDir()
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(path)
}
