package objectstore

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestPlainStorePersistsExactBytesAndVerifiesExactRetry(t *testing.T) {
	backend := newMemoryBlobStore()
	store := newTestPlainStore(t, backend)
	contents := []byte("plaintext prompt stored exactly as supplied")
	scope := testObjectScope(KindUserPrompt, "b0000000-0000-4000-8000-00000000000b", contents)

	for attempt := 0; attempt < 2; attempt++ {
		if err := store.Put(t.Context(), scope, bytes.NewReader(contents)); err != nil {
			t.Fatalf("Put attempt %d: %v", attempt, err)
		}
	}
	if raw := backend.snapshot(t, store.objectKey(scope)); !bytes.Equal(raw, contents) {
		t.Fatalf("provider bytes = %q, want exact plaintext", raw)
	}
	if backend.createdCount.Load() != 1 {
		t.Fatalf("provider creates = %d, want 1", backend.createdCount.Load())
	}
	reader, err := store.Open(t.Context(), scope)
	if err != nil {
		t.Fatal(err)
	}
	opened, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil || !bytes.Equal(opened, contents) {
		t.Fatalf("Open = %q, read=%v close=%v", opened, readErr, closeErr)
	}
}

func TestPlainStoreRejectsSourceAndProviderAuthorityDrift(t *testing.T) {
	backend := newMemoryBlobStore()
	store := newTestPlainStore(t, backend)
	contents := []byte("committed plaintext checkpoint")
	scope := testObjectScope(KindCheckpoint, "c0000000-0000-4000-8000-00000000000c", contents)

	if err := store.Put(t.Context(), scope, bytes.NewReader([]byte("wrong plaintext checkpoint"))); err == nil {
		t.Fatal("Put accepted source bytes that differ from the descriptor")
	}
	if backend.objectCount() != 0 {
		t.Fatal("failed plaintext validation published an object")
	}
	if err := store.Put(t.Context(), scope, bytes.NewReader(contents)); err != nil {
		t.Fatal(err)
	}
	tampered := bytes.Repeat([]byte{'x'}, len(contents))
	backend.set(store.objectKey(scope), tampered)
	if err := store.Put(t.Context(), scope, bytes.NewReader(contents)); !errors.Is(err, ErrObjectConflict) {
		t.Fatalf("tampered exact retry error = %v, want ErrObjectConflict", err)
	}
	reader, err := store.Open(t.Context(), scope)
	if err != nil {
		t.Fatal(err)
	}
	one := make([]byte, 1)
	_, _ = reader.Read(one)
	if err := reader.Close(); err == nil {
		t.Fatal("Close did not verify the unread plaintext tail")
	}
}

func TestPlainStoreRejectsInvalidConfiguration(t *testing.T) {
	backend := newMemoryBlobStore()
	for _, config := range []PlainConfig{
		{MaximumPlaintextBytes: 1},
		{Backend: backend, Prefix: "../objects", MaximumPlaintextBytes: 1},
		{Backend: backend, MaximumPlaintextBytes: 0},
	} {
		if _, err := NewPlain(config); err == nil {
			t.Fatal("NewPlain accepted invalid configuration")
		}
	}
}

func newTestPlainStore(t *testing.T, backend ImmutableBlobStore) *PlainStore {
	t.Helper()
	store, err := NewPlain(PlainConfig{Backend: backend, MaximumPlaintextBytes: objectChunkBytes})
	if err != nil {
		t.Fatal(err)
	}
	return store
}
