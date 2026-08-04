package coreserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/coredb"
	"github.com/agentserver/agentserver/v2/internal/objectstore"
)

func TestEncryptedUserPromptStoreBindsPlaintextPointerToWorkspace(t *testing.T) {
	objects := &recordingEncryptedPromptProtocol{}
	store := &EncryptedUserPromptStore{objects: objects}
	request := UserPromptWriteRequest{
		WorkspaceID: userRunWorkspaceID, SessionID: userRunSessionID, ActorID: userRunActorID,
		IdempotencyKey: "request-1", Prompt: "run the deterministic task",
	}

	first, err := store.PutUserPrompt(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.PutUserPrompt(t.Context(), request)
	if err != nil || second != first {
		t.Fatalf("exact retry = %+v, %v; want %+v", second, err, first)
	}
	if first != userPromptObjectPointer(request) {
		t.Fatalf("prompt pointer = %+v, want stable derived pointer", first)
	}
	wantScope := objectstore.Scope{
		WorkspaceID: request.WorkspaceID,
		Kind:        objectstore.KindUserPrompt,
		Descriptor: objectstore.Descriptor{
			ObjectID: first.ObjectID, SHA256: first.SHA256,
			Size: first.Size, MediaType: first.MediaType,
		},
	}
	if objects.calls != 2 || objects.scope != wantScope || !bytes.Equal(objects.plaintext, []byte(request.Prompt)) {
		t.Fatalf("encrypted prompt call = calls %d, scope %+v, plaintext %q", objects.calls, objects.scope, objects.plaintext)
	}
}

func TestEncryptedUserPromptStoreMapsOnlyObjectConflictToPublicIdempotencyConflict(t *testing.T) {
	backendFailure := errors.New("KMS unavailable")
	request := UserPromptWriteRequest{
		WorkspaceID: userRunWorkspaceID, SessionID: userRunSessionID, ActorID: userRunActorID,
		IdempotencyKey: "request-1", Prompt: "hello",
	}

	store := &EncryptedUserPromptStore{objects: &recordingEncryptedPromptProtocol{
		err: errors.Join(errors.New("existing immutable object differs"), objectstore.ErrObjectConflict),
	}}
	if _, err := store.PutUserPrompt(t.Context(), request); !coredb.HasStateErrorCode(err, coredb.ErrorIdempotencyConflict) {
		t.Fatalf("object conflict = %v, want public idempotency conflict", err)
	}

	store = &EncryptedUserPromptStore{objects: &recordingEncryptedPromptProtocol{err: backendFailure}}
	if _, err := store.PutUserPrompt(t.Context(), request); !errors.Is(err, backendFailure) || coredb.HasStateErrorCode(err, coredb.ErrorIdempotencyConflict) {
		t.Fatalf("backend failure = %v, want preserved non-public failure", err)
	}
}

func TestEncryptedUserPromptStoreReconcilesTransientObjectFailureExactly(t *testing.T) {
	transient := errors.New("TLS handshake timeout")
	objects := &recordingEncryptedPromptProtocol{errors: []error{transient, nil}}
	store := &EncryptedUserPromptStore{objects: objects}
	request := UserPromptWriteRequest{
		WorkspaceID: userRunWorkspaceID, SessionID: userRunSessionID, ActorID: userRunActorID,
		IdempotencyKey: "request-transient", Prompt: "hello",
	}

	pointer, err := store.PutUserPrompt(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if objects.calls != 2 || pointer != userPromptObjectPointer(request) ||
		!bytes.Equal(objects.plaintext, []byte(request.Prompt)) {
		t.Fatalf("transient reconciliation = calls %d, pointer %+v, plaintext %q", objects.calls, pointer, objects.plaintext)
	}
}

func TestEncryptedUserPromptStoreRejectsInvalidCallsBeforeObjectProtocol(t *testing.T) {
	if _, err := NewEncryptedUserPromptStore(nil); err == nil {
		t.Fatal("nil encrypted object store was accepted")
	}
	objects := &recordingEncryptedPromptProtocol{}
	store := &EncryptedUserPromptStore{objects: objects}
	valid := UserPromptWriteRequest{
		WorkspaceID: userRunWorkspaceID, SessionID: userRunSessionID, ActorID: userRunActorID,
		IdempotencyKey: "request-1", Prompt: "hello",
	}

	if _, err := store.PutUserPrompt(nil, valid); err == nil {
		t.Fatal("nil context was accepted")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.PutUserPrompt(cancelled, valid); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled context = %v", err)
	}
	empty := valid
	empty.Prompt = ""
	if _, err := store.PutUserPrompt(t.Context(), empty); err == nil {
		t.Fatal("empty prompt was accepted")
	}
	tooLarge := valid
	tooLarge.Prompt = string(bytes.Repeat([]byte{'x'}, maxUserRunPromptBytes+1))
	if _, err := store.PutUserPrompt(t.Context(), tooLarge); err == nil {
		t.Fatal("oversized prompt was accepted")
	}
	if objects.calls != 0 {
		t.Fatalf("invalid calls reached encrypted object protocol %d time(s)", objects.calls)
	}
	var nilStore *EncryptedUserPromptStore
	if _, err := nilStore.PutUserPrompt(t.Context(), valid); err == nil {
		t.Fatal("nil prompt store was accepted")
	}
}

func TestEncryptedUserPromptStoreReadsOnlyDescriptorVerifiedPrompt(t *testing.T) {
	contents := []byte("persisted hello")
	pointer := coredb.ObjectPointer{
		ObjectID:  "94000000-0000-4000-8000-000000000004",
		SHA256:    sha256.Sum256(contents),
		Size:      int64(len(contents)),
		MediaType: userPromptMediaType,
	}
	objects := &recordingEncryptedPromptProtocol{openContents: contents}
	store := &EncryptedUserPromptStore{objects: objects}
	got, err := store.ReadUserPrompt(t.Context(), UserPromptReadRequest{WorkspaceID: userRunWorkspaceID, Pointer: pointer})
	if err != nil || got != string(contents) || objects.openScope.Descriptor.ObjectID != pointer.ObjectID {
		t.Fatalf("ReadUserPrompt() = %q, %v; scope=%+v", got, err, objects.openScope)
	}
	objects.openContents = []byte("tampered hello")
	if _, err := store.ReadUserPrompt(t.Context(), UserPromptReadRequest{WorkspaceID: userRunWorkspaceID, Pointer: pointer}); err == nil {
		t.Fatal("tampered prompt object was accepted")
	}
}

type recordingEncryptedPromptProtocol struct {
	calls        int
	scope        objectstore.Scope
	plaintext    []byte
	err          error
	errors       []error
	openScope    objectstore.Scope
	openContents []byte
	openErr      error
}

func (protocol *recordingEncryptedPromptProtocol) Open(
	_ context.Context,
	scope objectstore.Scope,
) (io.ReadCloser, error) {
	protocol.openScope = scope
	if protocol.openErr != nil {
		return nil, protocol.openErr
	}
	return io.NopCloser(bytes.NewReader(protocol.openContents)), nil
}

func (protocol *recordingEncryptedPromptProtocol) Put(
	_ context.Context,
	scope objectstore.Scope,
	source io.Reader,
) error {
	protocol.calls++
	protocol.scope = scope
	protocol.plaintext, _ = io.ReadAll(source)
	if len(protocol.errors) != 0 {
		err := protocol.errors[0]
		protocol.errors = protocol.errors[1:]
		return err
	}
	return protocol.err
}
