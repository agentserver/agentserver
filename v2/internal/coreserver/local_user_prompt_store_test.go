package coreserver

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/coredb"
)

func TestLocalUserPromptStoreIsStableForExactRetryAndConflictsWithoutOverwrite(t *testing.T) {
	root := t.TempDir()
	store, err := NewLocalUserPromptStore(root)
	if err != nil {
		t.Fatal(err)
	}
	request := UserPromptWriteRequest{
		WorkspaceID: userRunWorkspaceID, SessionID: userRunSessionID, ActorID: userRunActorID,
		IdempotencyKey: "request-1", Prompt: "hello",
	}
	first, err := store.PutUserPrompt(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.PutUserPrompt(t.Context(), request)
	if err != nil || second != first {
		t.Fatalf("exact retry = %+v, %v; want %+v", second, err, first)
	}
	path := filepath.Join(root, first.ObjectID+".prompt")
	contents, err := os.ReadFile(path)
	if err != nil || string(contents) != "hello" {
		t.Fatalf("stored prompt = %q, %v", contents, err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("prompt permissions = %o", info.Mode().Perm())
	}
	read, err := store.ReadUserPrompt(t.Context(), UserPromptReadRequest{WorkspaceID: request.WorkspaceID, Pointer: first})
	if err != nil || read != request.Prompt {
		t.Fatalf("ReadUserPrompt() = %q, %v", read, err)
	}
	request.Prompt = "different"
	if _, err := store.PutUserPrompt(t.Context(), request); !coredb.HasStateErrorCode(err, coredb.ErrorIdempotencyConflict) {
		t.Fatalf("conflicting retry error = %v", err)
	}
	contents, _ = os.ReadFile(path)
	if string(contents) != "hello" {
		t.Fatalf("conflicting retry overwrote prompt: %q", contents)
	}
}

func TestLocalUserPromptStoreRejectsUnsafeRootAndExistingSymlink(t *testing.T) {
	if _, err := NewLocalUserPromptStore("relative"); err == nil {
		t.Fatal("relative prompt root was accepted")
	}
	root := t.TempDir()
	store, _ := NewLocalUserPromptStore(root)
	request := UserPromptWriteRequest{
		WorkspaceID: userRunWorkspaceID, SessionID: userRunSessionID, ActorID: userRunActorID,
		IdempotencyKey: "request-1", Prompt: "hello",
	}
	pointer, err := store.PutUserPrompt(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, pointer.ObjectID+".prompt")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "missing"), path); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := store.PutUserPrompt(t.Context(), request); err == nil || errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unsafe symlink error = %v", err)
	}
}
