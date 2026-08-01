package coreserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"

	"github.com/agentserver/agentserver/v2/internal/coredb"
	"github.com/agentserver/agentserver/v2/internal/objectstore"
)

// EncryptedUserPromptStore persists user prompts through the provider-neutral
// encrypted object protocol. The returned Core pointer always describes the
// plaintext and remains stable across an exact idempotent retry.
type EncryptedUserPromptStore struct {
	objects encryptedPromptObjectProtocol
}

type encryptedPromptObjectProtocol interface {
	Put(context.Context, objectstore.Scope, io.Reader) error
}

func NewEncryptedUserPromptStore(objects *objectstore.Store) (*EncryptedUserPromptStore, error) {
	if objects == nil {
		return nil, errors.New("Core encrypted prompt object store is required")
	}
	return &EncryptedUserPromptStore{objects: objects}, nil
}

func (store *EncryptedUserPromptStore) PutUserPrompt(
	ctx context.Context,
	request UserPromptWriteRequest,
) (coredb.ObjectPointer, error) {
	if ctx == nil {
		return coredb.ObjectPointer{}, errors.New("encrypted user prompt context is required")
	}
	if err := ctx.Err(); err != nil {
		return coredb.ObjectPointer{}, err
	}
	if store == nil || store.objects == nil {
		return coredb.ObjectPointer{}, errors.New("Core encrypted prompt object store is not initialized")
	}
	if len(request.Prompt) < 1 || len(request.Prompt) > maxUserRunPromptBytes {
		return coredb.ObjectPointer{}, errors.New("encrypted user prompt is outside the run prompt size bound")
	}
	pointer := userPromptObjectPointer(request)
	scope := objectstore.Scope{
		WorkspaceID: request.WorkspaceID, Kind: objectstore.KindUserPrompt,
		Descriptor: objectstore.Descriptor{
			ObjectID: pointer.ObjectID, SHA256: pointer.SHA256,
			Size: pointer.Size, MediaType: pointer.MediaType,
		},
	}
	if err := store.objects.Put(ctx, scope, bytes.NewReader([]byte(request.Prompt))); err != nil {
		if errors.Is(err, objectstore.ErrObjectConflict) {
			return coredb.ObjectPointer{}, publicRunStateError(
				coredb.ErrorIdempotencyConflict, "PutUserPrompt", "object", pointer.ObjectID,
				"idempotency key already names different prompt bytes",
			)
		}
		return coredb.ObjectPointer{}, fmt.Errorf("put encrypted user prompt object: %w", err)
	}
	return pointer, nil
}

func userPromptObjectPointer(request UserPromptWriteRequest) coredb.ObjectPointer {
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
	contents := []byte(request.Prompt)
	return coredb.ObjectPointer{
		ObjectID: formatPromptUUID(objectRaw), SHA256: sha256.Sum256(contents),
		Size: int64(len(contents)), MediaType: "text/plain; charset=utf-8",
	}
}
