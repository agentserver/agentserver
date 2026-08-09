package harnessworker

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/agentserver/agentserver/v2/internal/checkpoint"
	"github.com/agentserver/agentserver/v2/internal/runmanifest"
)

// RestoredCheckpoint is the only native-resume state exposed to the worker
// runner. RolloutPath is a newly created regular file beneath the fresh
// CODEX_HOME; the outer artifact and staging file have already been removed.
type RestoredCheckpoint struct {
	Manifest    checkpoint.Manifest
	RolloutPath string
}

// LoadCheckpoint consumes and closes the optional inherited checkpoint pipe.
// current must be the already-verified signed run manifest. The complete
// outer object is independently size/hash verified in staging before its
// embedded manifest is authorized or any rollout path is created.
func LoadCheckpoint(checkpointPipe *os.File, current runmanifest.Manifest, codexHome, stagingRoot string) (*RestoredCheckpoint, error) {
	previous := current.PreviousCheckpoint
	if previous == nil {
		if checkpointPipe == nil {
			return nil, nil
		}
		closeErr := checkpointPipe.Close()
		return nil, errors.Join(errors.New("worker received checkpoint bytes without signed checkpoint authority"), closeErr)
	}
	if checkpointPipe == nil {
		return nil, errors.New("worker checkpoint pipe is required by the signed run manifest")
	}
	info, err := checkpointPipe.Stat()
	if err != nil {
		_ = checkpointPipe.Close()
		return nil, fmt.Errorf("inspect worker checkpoint pipe: %w", err)
	}
	if info.Mode()&os.ModeNamedPipe == 0 {
		_ = checkpointPipe.Close()
		return nil, errors.New("worker checkpoint descriptor must be a pipe")
	}
	if previous.Object.MediaType != checkpoint.ArtifactMediaType {
		_ = checkpointPipe.Close()
		return nil, fmt.Errorf("worker checkpoint media type must be %q", checkpoint.ArtifactMediaType)
	}
	if previous.Object.SizeBytes < 1 || previous.Object.SizeBytes > checkpoint.MaximumArtifactBytes {
		_ = checkpointPipe.Close()
		return nil, fmt.Errorf("worker checkpoint object size must be between 1 and %d bytes", checkpoint.MaximumArtifactBytes)
	}
	if err := validateCheckpointRoot("CODEX_HOME", codexHome); err != nil {
		_ = checkpointPipe.Close()
		return nil, err
	}
	if err := validateCheckpointRoot("checkpoint staging", stagingRoot); err != nil {
		_ = checkpointPipe.Close()
		return nil, err
	}

	staged, err := os.CreateTemp(stagingRoot, ".checkpoint-object-*")
	if err != nil {
		_ = checkpointPipe.Close()
		return nil, fmt.Errorf("create worker checkpoint staging file: %w", err)
	}
	stagedPath := staged.Name()
	defer os.Remove(stagedPath)
	defer staged.Close()
	if err := copyVerifiedCheckpointObject(staged, checkpointPipe, previous.Object); err != nil {
		return nil, err
	}
	if _, err := staged.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("rewind worker checkpoint staging file: %w", err)
	}

	var restored RestoredCheckpoint
	err = checkpoint.ReadArtifact(staged, previous.Object.SizeBytes, func(manifest checkpoint.Manifest, canonical []byte, rollout io.Reader) error {
		packSetDigest := ""
		if current.ToolPack != nil {
			packSetDigest = current.ToolPack.PackSetDigest
		}
		authority := checkpoint.ResumeAuthority{
			ManifestDigest: previous.ManifestDigest, CheckpointID: previous.CheckpointID,
			WorkspaceID: current.WorkspaceID, SessionID: current.SessionID,
			RunID: previous.RunID, RunAttemptID: previous.RunAttemptID,
			RunAttemptGeneration: previous.RunAttemptGeneration,
			BrainThreadID:        previous.ThreadID, TerminalTurnID: previous.TurnID,
			CodexRuntimeManifestDigest: current.CodexRuntimeManifestDigest,
			CheckpointAllowlistVersion: int64(current.CheckpointAllowlistVersion),
			CatalogDigest:              current.ExecutorMCP.CatalogDigest,
			PackSetDigest:              packSetDigest,
		}
		if err := checkpoint.VerifyResume(manifest, canonical, authority); err != nil {
			return err
		}
		path, err := restoreCheckpointRollout(codexHome, manifest.Files[0], rollout)
		if err != nil {
			return err
		}
		restored = RestoredCheckpoint{Manifest: manifest, RolloutPath: path}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("restore worker checkpoint: %w", err)
	}
	return &restored, nil
}

func copyVerifiedCheckpointObject(destination *os.File, source *os.File, pointer runmanifest.ObjectPointer) error {
	want, err := hex.DecodeString(pointer.SHA256)
	if err != nil || len(want) != sha256.Size || hex.EncodeToString(want) != pointer.SHA256 {
		_ = source.Close()
		return errors.New("worker checkpoint object digest must be canonical lowercase SHA-256")
	}
	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(destination, hasher), io.LimitReader(source, pointer.SizeBytes+1))
	sourceCloseErr := source.Close()
	if copyErr != nil || sourceCloseErr != nil {
		return errors.Join(errors.New("read worker checkpoint object"), copyErr, sourceCloseErr)
	}
	if written != pointer.SizeBytes {
		return errors.New("worker checkpoint bytes do not match the signed object size")
	}
	if subtle.ConstantTimeCompare(hasher.Sum(nil), want) != 1 {
		return errors.New("worker checkpoint bytes do not match the signed object digest")
	}
	return nil
}

func validateCheckpointRoot(label, root string) error {
	if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return fmt.Errorf("worker %s must be an absolute clean path", label)
	}
	info, err := os.Lstat(root)
	if err != nil {
		return fmt.Errorf("inspect worker %s: %w", label, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("worker %s must be a non-symlink directory", label)
	}
	return nil
}

func restoreCheckpointRollout(root string, entry checkpoint.File, source io.Reader) (string, error) {
	if err := entry.Validate(1); err != nil {
		return "", err
	}
	parts := strings.Split(entry.Path, "/")
	current := root
	createdDirectories := make([]string, 0, len(parts)-1)
	cleanupDirectories := func() {
		for index := len(createdDirectories) - 1; index >= 0; index-- {
			_ = os.Remove(createdDirectories[index])
		}
	}
	for _, part := range parts[:len(parts)-1] {
		current = filepath.Join(current, filepath.FromSlash(part))
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(current, 0o700); err != nil {
				cleanupDirectories()
				return "", fmt.Errorf("create checkpoint rollout directory: %w", err)
			}
			createdDirectories = append(createdDirectories, current)
			continue
		}
		if err != nil {
			cleanupDirectories()
			return "", fmt.Errorf("inspect checkpoint rollout directory: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			cleanupDirectories()
			return "", errors.New("checkpoint rollout path contains a symlink or non-directory component")
		}
	}
	destination := filepath.Join(current, filepath.FromSlash(parts[len(parts)-1]))
	relative, err := filepath.Rel(root, destination)
	if err != nil || relative == "." || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		cleanupDirectories()
		return "", errors.New("checkpoint rollout path escapes CODEX_HOME")
	}
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, checkpoint.RolloutMode)
	if err != nil {
		cleanupDirectories()
		return "", fmt.Errorf("create checkpoint rollout file: %w", err)
	}
	success := false
	defer func() {
		if !success {
			_ = file.Close()
			_ = os.Remove(destination)
			cleanupDirectories()
		}
	}()
	if _, err := checkpoint.CopyVerifiedRollout(file, source, entry); err != nil {
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("close checkpoint rollout file: %w", err)
	}
	info, err := os.Lstat(destination)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() != entry.SizeBytes {
		return "", errors.New("restored checkpoint rollout is not the exact regular file")
	}
	success = true
	return destination, nil
}
