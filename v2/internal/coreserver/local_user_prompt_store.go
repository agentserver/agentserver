package coreserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/agentserver/agentserver/v2/internal/coredb"
)

// LocalUserPromptStore is an explicit developer-mode object backend. It gives
// CreateRun real durable/idempotent object pointers without pretending that a
// pod-local plaintext directory is the production encrypted object store.
type LocalUserPromptStore struct {
	root string
}

func NewLocalUserPromptStore(root string) (*LocalUserPromptStore, error) {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return nil, errors.New("local prompt object root must be an absolute clean path")
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("stat local prompt object root: %w", err)
	}
	if !info.IsDir() {
		return nil, errors.New("local prompt object root is not a directory")
	}
	return &LocalUserPromptStore{root: root}, nil
}

func (store *LocalUserPromptStore) PutUserPrompt(ctx context.Context, request UserPromptWriteRequest) (_ coredb.ObjectPointer, returnErr error) {
	if store == nil || store.root == "" {
		return coredb.ObjectPointer{}, errors.New("local prompt object store is not initialized")
	}
	if err := ctx.Err(); err != nil {
		return coredb.ObjectPointer{}, err
	}
	identityHash := sha256.New()
	_, _ = io.WriteString(identityHash, "agentserver-v2/user-prompt-object/v1\x00")
	for _, value := range []string{request.WorkspaceID, request.ActorID, request.SessionID, request.IdempotencyKey} {
		_, _ = io.WriteString(identityHash, value)
		_, _ = identityHash.Write([]byte{0})
	}
	identityDigest := identityHash.Sum(nil)
	var objectRaw [16]byte
	copy(objectRaw[:], identityDigest[:16])
	objectRaw[6] = (objectRaw[6] & 0x0f) | 0x50
	objectRaw[8] = (objectRaw[8] & 0x3f) | 0x80
	objectID := formatPromptUUID(objectRaw)
	contents := []byte(request.Prompt)
	digest := sha256.Sum256(contents)
	pointer := coredb.ObjectPointer{
		ObjectID: objectID, SHA256: digest, Size: int64(len(contents)), MediaType: "text/plain; charset=utf-8",
	}
	finalPath := filepath.Join(store.root, objectID+".prompt")
	if matches, exists, err := promptFileMatches(finalPath, contents); err != nil {
		return coredb.ObjectPointer{}, err
	} else if exists {
		if !matches {
			return coredb.ObjectPointer{}, publicRunStateError(coredb.ErrorIdempotencyConflict, "PutUserPrompt", "object", objectID, "idempotency key already names different prompt bytes")
		}
		return pointer, nil
	}

	temporary, err := os.CreateTemp(store.root, ".user-prompt-*")
	if err != nil {
		return coredb.ObjectPointer{}, fmt.Errorf("create temporary prompt object: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		if err := os.Remove(temporaryPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			returnErr = errors.Join(returnErr, fmt.Errorf("remove temporary prompt object: %w", err))
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return coredb.ObjectPointer{}, fmt.Errorf("restrict temporary prompt object: %w", err)
	}
	if _, err := temporary.Write(contents); err != nil {
		temporary.Close()
		return coredb.ObjectPointer{}, fmt.Errorf("write temporary prompt object: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return coredb.ObjectPointer{}, fmt.Errorf("sync temporary prompt object: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return coredb.ObjectPointer{}, fmt.Errorf("close temporary prompt object: %w", err)
	}
	if err := os.Link(temporaryPath, finalPath); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return coredb.ObjectPointer{}, fmt.Errorf("commit immutable prompt object: %w", err)
		}
		matches, _, verifyErr := promptFileMatches(finalPath, contents)
		if verifyErr != nil {
			return coredb.ObjectPointer{}, verifyErr
		}
		if !matches {
			return coredb.ObjectPointer{}, publicRunStateError(coredb.ErrorIdempotencyConflict, "PutUserPrompt", "object", objectID, "concurrent idempotency key names different prompt bytes")
		}
	}
	return pointer, nil
}

func promptFileMatches(path string, want []byte) (matches, exists bool, err error) {
	linkInfo, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, false, nil
	}
	if err != nil {
		return false, false, fmt.Errorf("lstat immutable prompt object: %w", err)
	}
	if !linkInfo.Mode().IsRegular() || linkInfo.Mode()&0o077 != 0 {
		return false, true, errors.New("immutable prompt object has unsafe type or permissions")
	}
	file, err := os.Open(path)
	if err != nil {
		return false, false, fmt.Errorf("open immutable prompt object: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return false, true, fmt.Errorf("stat immutable prompt object: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&0o077 != 0 || info.Size() > maxUserRunPromptBytes {
		return false, true, errors.New("immutable prompt object has unsafe type, permissions, or size")
	}
	contents, err := io.ReadAll(io.LimitReader(file, int64(maxUserRunPromptBytes)+1))
	if err != nil {
		return false, true, fmt.Errorf("read immutable prompt object: %w", err)
	}
	return bytes.Equal(contents, want), true, nil
}

func formatPromptUUID(raw [16]byte) string {
	var encoded [36]byte
	hex.Encode(encoded[0:8], raw[0:4])
	encoded[8] = '-'
	hex.Encode(encoded[9:13], raw[4:6])
	encoded[13] = '-'
	hex.Encode(encoded[14:18], raw[6:8])
	encoded[18] = '-'
	hex.Encode(encoded[19:23], raw[8:10])
	encoded[23] = '-'
	hex.Encode(encoded[24:36], raw[10:16])
	return string(encoded[:])
}
