package harnesspool

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/agentserver/agentserver/v2/internal/checkpoint"
	"github.com/agentserver/agentserver/v2/internal/objectstore"
	"github.com/agentserver/agentserver/v2/internal/runmanifest"
)

const localDevelopmentPromptMediaType = "text/plain; charset=utf-8"

// LocalDevelopmentObjectStore is a shared-filesystem adapter for explicit
// insecure-development deployments. It understands the immutable prompt
// objects written by agentserver-core's developer store and writes checkpoint
// artifacts with create-if-absent semantics. It is deliberately not an
// encrypted or multi-pod production object store.
type LocalDevelopmentObjectStore struct {
	root string
}

func NewLocalDevelopmentObjectStore(root string) (*LocalDevelopmentObjectStore, error) {
	if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return nil, errors.New("local development object root must be a clean absolute path")
	}
	info, err := os.Lstat(root)
	if err != nil {
		return nil, fmt.Errorf("inspect local development object root: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("local development object root must be a direct directory")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("local development object root must not be accessible by group or other")
	}
	return &LocalDevelopmentObjectStore{root: root}, nil
}

func (store *LocalDevelopmentObjectStore) OpenRunObject(ctx context.Context, request AttemptObjectRequest) (io.ReadCloser, error) {
	if ctx == nil {
		return nil, errors.New("local development object read context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if store == nil || store.root == "" {
		return nil, errors.New("local development object store is required")
	}
	if err := validateUUIDIdentity("local run object workspace", request.WorkspaceID); err != nil {
		return nil, err
	}
	path, err := store.runObjectPath(request.Kind, request.Pointer)
	if err != nil {
		return nil, err
	}
	file, err := openImmutableLocalObject(path, request.Pointer.SizeBytes)
	if err != nil {
		return nil, err
	}
	return &localDevelopmentObjectReader{ctx: ctx, file: file}, nil
}

func (store *LocalDevelopmentObjectStore) PutCheckpointObject(
	ctx context.Context,
	request CheckpointObjectWriteRequest,
	source io.Reader,
) (_ EventObjectPointer, returnErr error) {
	if ctx == nil {
		return EventObjectPointer{}, errors.New("local checkpoint object write context is required")
	}
	if err := ctx.Err(); err != nil {
		return EventObjectPointer{}, err
	}
	if store == nil || store.root == "" {
		return EventObjectPointer{}, errors.New("local development object store is required")
	}
	if source == nil {
		return EventObjectPointer{}, errors.New("local checkpoint object source is required")
	}
	if err := validateUUIDIdentity("local checkpoint object workspace", request.WorkspaceID); err != nil {
		return EventObjectPointer{}, err
	}
	pointer := request.Object
	if err := validateLocalCheckpointPointer(pointer); err != nil {
		return EventObjectPointer{}, err
	}
	finalPath := filepath.Join(store.root, pointer.ObjectID+".checkpoint")
	if exists, err := localCheckpointObjectMatches(finalPath, pointer); err != nil {
		return EventObjectPointer{}, err
	} else if exists {
		if err := syncLocalObjectDirectory(store.root); err != nil {
			return EventObjectPointer{}, err
		}
		return pointer, nil
	}

	temporary, err := os.CreateTemp(store.root, ".checkpoint-object-*")
	if err != nil {
		return EventObjectPointer{}, fmt.Errorf("create local checkpoint object staging: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		if err := os.Remove(temporaryPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			returnErr = errors.Join(returnErr, fmt.Errorf("remove local checkpoint object staging: %w", err))
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return EventObjectPointer{}, fmt.Errorf("restrict local checkpoint object staging: %w", err)
	}

	hasher := sha256.New()
	contextSource := &contextReader{ctx: ctx, reader: source}
	written, copyErr := io.CopyN(io.MultiWriter(temporary, hasher), contextSource, pointer.Size)
	var extra [1]byte
	extraBytes, extraErr := contextSource.Read(extra[:])
	syncErr := temporary.Sync()
	closeErr := temporary.Close()
	if copyErr != nil || written != pointer.Size {
		return EventObjectPointer{}, errors.Join(
			errors.New("local checkpoint object source ended before its declared size"), copyErr,
			wrapCheckpointError("sync local checkpoint staging", syncErr),
			wrapCheckpointError("close local checkpoint staging", closeErr),
		)
	}
	if extraBytes != 0 || !errors.Is(extraErr, io.EOF) {
		return EventObjectPointer{}, errors.Join(
			errors.New("local checkpoint object source exceeded its declared size"), extraErr,
			wrapCheckpointError("sync local checkpoint staging", syncErr),
			wrapCheckpointError("close local checkpoint staging", closeErr),
		)
	}
	if subtle.ConstantTimeCompare(hasher.Sum(nil), pointer.SHA256[:]) != 1 {
		return EventObjectPointer{}, errors.Join(
			errors.New("local checkpoint object source does not match its declared digest"),
			wrapCheckpointError("sync local checkpoint staging", syncErr),
			wrapCheckpointError("close local checkpoint staging", closeErr),
		)
	}
	if syncErr != nil || closeErr != nil {
		return EventObjectPointer{}, errors.Join(
			wrapCheckpointError("sync local checkpoint staging", syncErr),
			wrapCheckpointError("close local checkpoint staging", closeErr),
		)
	}
	if err := ctx.Err(); err != nil {
		return EventObjectPointer{}, err
	}
	if err := os.Link(temporaryPath, finalPath); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return EventObjectPointer{}, fmt.Errorf("commit immutable local checkpoint object: %w", err)
		}
		if exists, verifyErr := localCheckpointObjectMatches(finalPath, pointer); verifyErr != nil {
			return EventObjectPointer{}, verifyErr
		} else if !exists {
			return EventObjectPointer{}, errors.New("concurrent local checkpoint object does not match the requested pointer")
		}
	}
	if err := syncLocalObjectDirectory(store.root); err != nil {
		return EventObjectPointer{}, err
	}
	return pointer, nil
}

func (store *LocalDevelopmentObjectStore) runObjectPath(kind objectstore.Kind, pointer runmanifest.ObjectPointer) (string, error) {
	if err := validateUUIDIdentity("local run object ID", pointer.ObjectID); err != nil {
		return "", err
	}
	if pointer.SizeBytes < 1 {
		return "", errors.New("local run object size must be positive")
	}
	if _, err := decodeClientSHA256(pointer.SHA256); err != nil {
		return "", errors.New("local run object digest must be canonical SHA-256")
	}
	var suffix string
	switch kind {
	case objectstore.KindUserPrompt:
		if pointer.MediaType != localDevelopmentPromptMediaType {
			return "", errors.New("local development prompt object has an invalid media type")
		}
		suffix = ".prompt"
	case objectstore.KindCheckpoint:
		if pointer.MediaType != checkpoint.ArtifactMediaType {
			return "", errors.New("local development checkpoint object has an invalid media type")
		}
		if pointer.SizeBytes > checkpoint.MaximumArtifactBytes {
			return "", errors.New("local checkpoint object exceeds the artifact size bound")
		}
		suffix = ".checkpoint"
	default:
		return "", errors.New("local development object store does not support this object kind")
	}
	return filepath.Join(store.root, pointer.ObjectID+suffix), nil
}

func validateLocalCheckpointPointer(pointer EventObjectPointer) error {
	if err := validateUUIDIdentity("local checkpoint object ID", pointer.ObjectID); err != nil {
		return err
	}
	if pointer.Size < 1 || pointer.Size > checkpoint.MaximumArtifactBytes {
		return errors.New("local checkpoint object size is outside the artifact bound")
	}
	if pointer.MediaType != checkpoint.ArtifactMediaType {
		return errors.New("local checkpoint object media type is not artifact v1")
	}
	return nil
}

func openImmutableLocalObject(path string, expectedSize int64) (*os.File, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect immutable local run object: %w", err)
	}
	if !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 || before.Mode().Perm() != 0o600 || before.Size() != expectedSize {
		return nil, errors.New("immutable local run object has an unsafe type, mode, or size")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open immutable local run object: %w", err)
	}
	opened, openedErr := file.Stat()
	after, afterErr := os.Lstat(path)
	if openedErr != nil || afterErr != nil || !opened.Mode().IsRegular() || !after.Mode().IsRegular() ||
		after.Mode()&os.ModeSymlink != 0 || opened.Mode().Perm() != 0o600 || after.Mode().Perm() != 0o600 ||
		opened.Size() != expectedSize || after.Size() != expectedSize || !os.SameFile(before, opened) || !os.SameFile(opened, after) {
		_ = file.Close()
		return nil, errors.New("immutable local run object identity changed while opening")
	}
	return file, nil
}

func localCheckpointObjectMatches(path string, pointer EventObjectPointer) (bool, error) {
	file, err := openImmutableLocalObject(path, pointer.Size)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		var pathError *os.PathError
		if errors.As(err, &pathError) && errors.Is(pathError.Err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	hasher := sha256.New()
	written, readErr := io.Copy(hasher, file)
	closeErr := file.Close()
	if readErr != nil || closeErr != nil {
		return false, errors.Join(
			wrapCheckpointError("verify immutable local checkpoint object", readErr),
			wrapCheckpointError("close immutable local checkpoint object", closeErr),
		)
	}
	return written == pointer.Size && subtle.ConstantTimeCompare(hasher.Sum(nil), pointer.SHA256[:]) == 1, nil
}

func syncLocalObjectDirectory(root string) error {
	directory, err := os.Open(root)
	if err != nil {
		return fmt.Errorf("open local object directory for sync: %w", err)
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	return errors.Join(
		wrapCheckpointError("sync local object directory", syncErr),
		wrapCheckpointError("close local object directory", closeErr),
	)
}

type localDevelopmentObjectReader struct {
	ctx  context.Context
	file *os.File
}

func (reader *localDevelopmentObjectReader) Read(destination []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.file.Read(destination)
}

func (reader *localDevelopmentObjectReader) Close() error {
	if reader == nil || reader.file == nil {
		return nil
	}
	return reader.file.Close()
}
