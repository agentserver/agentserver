package harnesspool

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	"github.com/agentserver/agentserver/v2/internal/checkpoint"
	"github.com/agentserver/agentserver/v2/internal/objectstore"
)

// EncryptedRunObjectStore adapts the provider-neutral encrypted object
// protocol to the pool's prompt/checkpoint source and checkpoint sink. The
// concrete S3 and KMS clients remain outside the worker process boundary.
type EncryptedRunObjectStore struct {
	objects encryptedRunObjectProtocol
}

type encryptedRunObjectProtocol interface {
	Put(context.Context, objectstore.Scope, io.Reader) error
	Open(context.Context, objectstore.Scope) (io.ReadCloser, error)
}

func NewEncryptedRunObjectStore(objects objectstore.Protocol) (*EncryptedRunObjectStore, error) {
	if objects == nil {
		return nil, errors.New("pool encrypted object store is required")
	}
	return &EncryptedRunObjectStore{objects: objects}, nil
}

func (store *EncryptedRunObjectStore) OpenRunObject(
	ctx context.Context,
	request AttemptObjectRequest,
) (io.ReadCloser, error) {
	if ctx == nil {
		return nil, errors.New("pool encrypted object read context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if store == nil || store.objects == nil {
		return nil, errors.New("pool encrypted object store is not initialized")
	}
	scope, err := encryptedRunObjectScope(request)
	if err != nil {
		return nil, err
	}
	reader, err := store.objects.Open(ctx, scope)
	if err != nil {
		return nil, fmt.Errorf("open pool encrypted run object: %w", err)
	}
	return reader, nil
}

func (store *EncryptedRunObjectStore) PutCheckpointObject(
	ctx context.Context,
	request CheckpointObjectWriteRequest,
	source io.Reader,
) (EventObjectPointer, error) {
	if ctx == nil {
		return EventObjectPointer{}, errors.New("pool encrypted checkpoint write context is required")
	}
	if err := ctx.Err(); err != nil {
		return EventObjectPointer{}, err
	}
	if store == nil || store.objects == nil {
		return EventObjectPointer{}, errors.New("pool encrypted object store is not initialized")
	}
	if source == nil {
		return EventObjectPointer{}, errors.New("pool encrypted checkpoint source is required")
	}
	if err := validateUUIDIdentity("pool encrypted checkpoint workspace", request.WorkspaceID); err != nil {
		return EventObjectPointer{}, err
	}
	if err := validateUUIDIdentity("pool encrypted checkpoint object ID", request.Object.ObjectID); err != nil {
		return EventObjectPointer{}, err
	}
	if request.Object.MediaType != checkpoint.ArtifactMediaType || request.Object.Size < 1 || request.Object.Size > checkpoint.MaximumArtifactBytes {
		return EventObjectPointer{}, errors.New("pool encrypted checkpoint pointer is outside the artifact profile")
	}
	scope := objectstore.Scope{
		WorkspaceID: request.WorkspaceID, Kind: objectstore.KindCheckpoint,
		Descriptor: objectstore.Descriptor{
			ObjectID: request.Object.ObjectID, SHA256: request.Object.SHA256,
			Size: request.Object.Size, MediaType: request.Object.MediaType,
		},
	}
	if err := store.objects.Put(ctx, scope, source); err != nil {
		return EventObjectPointer{}, fmt.Errorf("put pool encrypted checkpoint object: %w", err)
	}
	return request.Object, nil
}

func encryptedRunObjectScope(request AttemptObjectRequest) (objectstore.Scope, error) {
	if err := validateUUIDIdentity("pool encrypted run object workspace", request.WorkspaceID); err != nil {
		return objectstore.Scope{}, err
	}
	if err := validateUUIDIdentity("pool encrypted run object ID", request.Pointer.ObjectID); err != nil {
		return objectstore.Scope{}, err
	}
	if request.Pointer.SizeBytes < 1 {
		return objectstore.Scope{}, errors.New("pool encrypted run object size must be positive")
	}
	if request.Kind != objectstore.KindUserPrompt && request.Kind != objectstore.KindCheckpoint {
		return objectstore.Scope{}, errors.New("pool encrypted run object kind is unsupported")
	}
	if request.Kind == objectstore.KindUserPrompt && request.Pointer.MediaType != localDevelopmentPromptMediaType {
		return objectstore.Scope{}, errors.New("pool encrypted prompt object media type is invalid")
	}
	if request.Kind == objectstore.KindCheckpoint &&
		(request.Pointer.MediaType != checkpoint.ArtifactMediaType || request.Pointer.SizeBytes > checkpoint.MaximumArtifactBytes) {
		return objectstore.Scope{}, errors.New("pool encrypted checkpoint object is outside the artifact profile")
	}
	digest, err := hex.DecodeString(request.Pointer.SHA256)
	if err != nil || len(digest) != 32 || hex.EncodeToString(digest) != request.Pointer.SHA256 {
		return objectstore.Scope{}, errors.New("pool encrypted run object digest is not canonical SHA-256")
	}
	var canonicalDigest [32]byte
	copy(canonicalDigest[:], digest)
	return objectstore.Scope{
		WorkspaceID: request.WorkspaceID, Kind: request.Kind,
		Descriptor: objectstore.Descriptor{
			ObjectID: request.Pointer.ObjectID, SHA256: canonicalDigest,
			Size: request.Pointer.SizeBytes, MediaType: request.Pointer.MediaType,
		},
	}, nil
}
