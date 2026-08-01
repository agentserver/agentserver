package harnesspool

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/checkpoint"
	"github.com/agentserver/agentserver/v2/internal/objectstore"
	"github.com/agentserver/agentserver/v2/internal/runmanifest"
)

const encryptedObjectTestWorkspaceID = "90000000-0000-4000-8000-000000000009"

func TestEncryptedRunObjectStoreConvertsPromptAndCheckpointAuthority(t *testing.T) {
	for _, test := range []struct {
		name      string
		kind      objectstore.Kind
		objectID  string
		mediaType string
	}{
		{name: "prompt", kind: objectstore.KindUserPrompt, objectID: "91000000-0000-4000-8000-000000000001", mediaType: localDevelopmentPromptMediaType},
		{name: "checkpoint", kind: objectstore.KindCheckpoint, objectID: "92000000-0000-4000-8000-000000000002", mediaType: checkpoint.ArtifactMediaType},
	} {
		t.Run(test.name, func(t *testing.T) {
			contents := []byte("authenticated " + test.name + " bytes")
			digest := sha256.Sum256(contents)
			protocol := &recordingEncryptedRunObjectProtocol{opened: contents}
			store := &EncryptedRunObjectStore{objects: protocol}
			request := AttemptObjectRequest{
				WorkspaceID: encryptedObjectTestWorkspaceID, Kind: test.kind,
				Pointer: runmanifest.ObjectPointer{
					ObjectID: test.objectID, SHA256: hex.EncodeToString(digest[:]),
					SizeBytes: int64(len(contents)), MediaType: test.mediaType,
				},
			}

			reader, err := store.OpenRunObject(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			opened, readErr := io.ReadAll(reader)
			closeErr := reader.Close()
			if readErr != nil || closeErr != nil || !bytes.Equal(opened, contents) {
				t.Fatalf("opened object = %q, read %v, close %v", opened, readErr, closeErr)
			}
			want := objectstore.Scope{
				WorkspaceID: request.WorkspaceID, Kind: test.kind,
				Descriptor: objectstore.Descriptor{
					ObjectID: test.objectID, SHA256: digest,
					Size: int64(len(contents)), MediaType: test.mediaType,
				},
			}
			if protocol.openCalls != 1 || protocol.openScope != want {
				t.Fatalf("Open scope = calls %d, %+v; want %+v", protocol.openCalls, protocol.openScope, want)
			}
		})
	}
}

func TestEncryptedRunObjectStorePutsCheckpointUnderExactAuthority(t *testing.T) {
	contents := []byte("deterministic checkpoint artifact")
	pointer := EventObjectPointer{
		ObjectID: "93000000-0000-4000-8000-000000000003", SHA256: sha256.Sum256(contents),
		Size: int64(len(contents)), MediaType: checkpoint.ArtifactMediaType,
	}
	protocol := &recordingEncryptedRunObjectProtocol{}
	store := &EncryptedRunObjectStore{objects: protocol}
	request := CheckpointObjectWriteRequest{WorkspaceID: encryptedObjectTestWorkspaceID, Object: pointer}

	stored, err := store.PutCheckpointObject(t.Context(), request, bytes.NewReader(contents))
	if err != nil || stored != pointer {
		t.Fatalf("PutCheckpointObject() = %+v, %v; want exact pointer", stored, err)
	}
	want := objectstore.Scope{
		WorkspaceID: encryptedObjectTestWorkspaceID, Kind: objectstore.KindCheckpoint,
		Descriptor: objectstore.Descriptor{
			ObjectID: pointer.ObjectID, SHA256: pointer.SHA256,
			Size: pointer.Size, MediaType: pointer.MediaType,
		},
	}
	if protocol.putCalls != 1 || protocol.putScope != want || !bytes.Equal(protocol.putSource, contents) {
		t.Fatalf("Put scope = calls %d, %+v, source %q; want %+v", protocol.putCalls, protocol.putScope, protocol.putSource, want)
	}

	backendFailure := errors.New("object backend unavailable")
	protocol.putErr = backendFailure
	if stored, err := store.PutCheckpointObject(t.Context(), request, bytes.NewReader(contents)); stored != (EventObjectPointer{}) || !errors.Is(err, backendFailure) {
		t.Fatalf("failed PutCheckpointObject() = %+v, %v", stored, err)
	}
}

func TestEncryptedRunObjectStoreRejectsMalformedReadAuthorityBeforeOpen(t *testing.T) {
	contents := []byte("prompt")
	digest := sha256.Sum256(contents)
	valid := AttemptObjectRequest{
		WorkspaceID: encryptedObjectTestWorkspaceID, Kind: objectstore.KindUserPrompt,
		Pointer: runmanifest.ObjectPointer{
			ObjectID: "94000000-0000-4000-8000-000000000004", SHA256: hex.EncodeToString(digest[:]),
			SizeBytes: int64(len(contents)), MediaType: localDevelopmentPromptMediaType,
		},
	}
	tests := []struct {
		name   string
		mutate func(*AttemptObjectRequest)
	}{
		{name: "workspace", mutate: func(request *AttemptObjectRequest) { request.WorkspaceID = "not-a-workspace" }},
		{name: "object ID", mutate: func(request *AttemptObjectRequest) { request.Pointer.ObjectID = "not-an-object" }},
		{name: "size", mutate: func(request *AttemptObjectRequest) { request.Pointer.SizeBytes = 0 }},
		{name: "kind", mutate: func(request *AttemptObjectRequest) { request.Kind = "future-kind" }},
		{name: "prompt media", mutate: func(request *AttemptObjectRequest) { request.Pointer.MediaType = "application/octet-stream" }},
		{name: "checkpoint media", mutate: func(request *AttemptObjectRequest) {
			request.Kind = objectstore.KindCheckpoint
			request.Pointer.MediaType = localDevelopmentPromptMediaType
		}},
		{name: "checkpoint size", mutate: func(request *AttemptObjectRequest) {
			request.Kind = objectstore.KindCheckpoint
			request.Pointer.MediaType = checkpoint.ArtifactMediaType
			request.Pointer.SizeBytes = checkpoint.MaximumArtifactBytes + 1
		}},
		{name: "digest syntax", mutate: func(request *AttemptObjectRequest) { request.Pointer.SHA256 = "not-a-digest" }},
		{name: "digest case", mutate: func(request *AttemptObjectRequest) { request.Pointer.SHA256 = strings.ToUpper(request.Pointer.SHA256) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := valid
			test.mutate(&request)
			protocol := &recordingEncryptedRunObjectProtocol{}
			store := &EncryptedRunObjectStore{objects: protocol}
			if _, err := store.OpenRunObject(t.Context(), request); err == nil {
				t.Fatal("malformed read authority was accepted")
			}
			if protocol.openCalls != 0 {
				t.Fatalf("malformed read reached object protocol %d time(s)", protocol.openCalls)
			}
		})
	}
}

func TestEncryptedRunObjectStoreRejectsMalformedWriteAuthorityBeforePut(t *testing.T) {
	contents := []byte("checkpoint")
	valid := CheckpointObjectWriteRequest{
		WorkspaceID: encryptedObjectTestWorkspaceID,
		Object: EventObjectPointer{
			ObjectID: "95000000-0000-4000-8000-000000000005", SHA256: sha256.Sum256(contents),
			Size: int64(len(contents)), MediaType: checkpoint.ArtifactMediaType,
		},
	}
	tests := []struct {
		name   string
		mutate func(*CheckpointObjectWriteRequest)
	}{
		{name: "workspace", mutate: func(request *CheckpointObjectWriteRequest) { request.WorkspaceID = "not-a-workspace" }},
		{name: "object ID", mutate: func(request *CheckpointObjectWriteRequest) { request.Object.ObjectID = "not-an-object" }},
		{name: "size", mutate: func(request *CheckpointObjectWriteRequest) { request.Object.Size = 0 }},
		{name: "oversize", mutate: func(request *CheckpointObjectWriteRequest) { request.Object.Size = checkpoint.MaximumArtifactBytes + 1 }},
		{name: "media", mutate: func(request *CheckpointObjectWriteRequest) { request.Object.MediaType = "application/octet-stream" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := valid
			test.mutate(&request)
			protocol := &recordingEncryptedRunObjectProtocol{}
			store := &EncryptedRunObjectStore{objects: protocol}
			if _, err := store.PutCheckpointObject(t.Context(), request, bytes.NewReader(contents)); err == nil {
				t.Fatal("malformed write authority was accepted")
			}
			if protocol.putCalls != 0 {
				t.Fatalf("malformed write reached object protocol %d time(s)", protocol.putCalls)
			}
		})
	}
}

func TestEncryptedRunObjectStoreRejectsInvalidCallState(t *testing.T) {
	if _, err := NewEncryptedRunObjectStore(nil); err == nil {
		t.Fatal("nil encrypted object store was accepted")
	}
	contents := []byte("prompt")
	digest := sha256.Sum256(contents)
	read := AttemptObjectRequest{
		WorkspaceID: encryptedObjectTestWorkspaceID, Kind: objectstore.KindUserPrompt,
		Pointer: runmanifest.ObjectPointer{
			ObjectID: "96000000-0000-4000-8000-000000000006", SHA256: hex.EncodeToString(digest[:]),
			SizeBytes: int64(len(contents)), MediaType: localDevelopmentPromptMediaType,
		},
	}
	write := CheckpointObjectWriteRequest{
		WorkspaceID: encryptedObjectTestWorkspaceID,
		Object: EventObjectPointer{
			ObjectID: "97000000-0000-4000-8000-000000000007", SHA256: digest,
			Size: int64(len(contents)), MediaType: checkpoint.ArtifactMediaType,
		},
	}
	store := &EncryptedRunObjectStore{objects: &recordingEncryptedRunObjectProtocol{opened: contents}}
	if _, err := store.OpenRunObject(nil, read); err == nil {
		t.Fatal("nil read context was accepted")
	}
	if _, err := store.PutCheckpointObject(nil, write, bytes.NewReader(contents)); err == nil {
		t.Fatal("nil write context was accepted")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.OpenRunObject(cancelled, read); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled read context = %v", err)
	}
	if _, err := store.PutCheckpointObject(cancelled, write, bytes.NewReader(contents)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled write context = %v", err)
	}
	if _, err := store.PutCheckpointObject(t.Context(), write, nil); err == nil {
		t.Fatal("nil checkpoint source was accepted")
	}
	var nilStore *EncryptedRunObjectStore
	if _, err := nilStore.OpenRunObject(t.Context(), read); err == nil {
		t.Fatal("nil encrypted read store was accepted")
	}
	if _, err := nilStore.PutCheckpointObject(t.Context(), write, bytes.NewReader(contents)); err == nil {
		t.Fatal("nil encrypted write store was accepted")
	}
}

type recordingEncryptedRunObjectProtocol struct {
	openCalls int
	openScope objectstore.Scope
	opened    []byte
	openErr   error
	putCalls  int
	putScope  objectstore.Scope
	putSource []byte
	putErr    error
}

func (protocol *recordingEncryptedRunObjectProtocol) Open(
	_ context.Context,
	scope objectstore.Scope,
) (io.ReadCloser, error) {
	protocol.openCalls++
	protocol.openScope = scope
	if protocol.openErr != nil {
		return nil, protocol.openErr
	}
	return io.NopCloser(bytes.NewReader(protocol.opened)), nil
}

func (protocol *recordingEncryptedRunObjectProtocol) Put(
	_ context.Context,
	scope objectstore.Scope,
	source io.Reader,
) error {
	protocol.putCalls++
	protocol.putScope = scope
	protocol.putSource, _ = io.ReadAll(source)
	return protocol.putErr
}
