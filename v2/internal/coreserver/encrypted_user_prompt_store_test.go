package coreserver

import (
	"bytes"
	"context"
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

type recordingEncryptedPromptProtocol struct {
	calls     int
	scope     objectstore.Scope
	plaintext []byte
	err       error
}

func (protocol *recordingEncryptedPromptProtocol) Put(
	_ context.Context,
	scope objectstore.Scope,
	source io.Reader,
) error {
	protocol.calls++
	protocol.scope = scope
	protocol.plaintext, _ = io.ReadAll(source)
	return protocol.err
}
