package objectstore

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"
)

const (
	testWorkspaceA = "10000000-0000-4000-8000-000000000001"
	testWorkspaceB = "20000000-0000-4000-8000-000000000002"
)

func TestEncryptedObjectStoreRoundTripsAndReconcilesExactRetry(t *testing.T) {
	backend := newMemoryBlobStore()
	keys := newTestDataKeyProvider(t)
	store := newTestEncryptedStore(t, backend, keys, 4*objectChunkBytes)
	contents := bytes.Repeat([]byte("bounded encrypted checkpoint\n"), 40_000)
	scope := testObjectScope(KindCheckpoint, "30000000-0000-4000-8000-000000000003", contents)

	if err := store.Put(t.Context(), scope, bytes.NewReader(contents)); err != nil {
		t.Fatal(err)
	}
	key := store.objectKey(scope)
	firstCiphertext := backend.snapshot(t, key)
	if len(firstCiphertext) <= len(contents) || bytes.Contains(firstCiphertext, contents[:1024]) {
		t.Fatal("persisted object exposes plaintext or lacks authenticated framing")
	}
	reader, err := store.Open(t.Context(), scope)
	if err != nil {
		t.Fatal(err)
	}
	opened, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("read encrypted object = %v, close = %v", readErr, closeErr)
	}
	if !bytes.Equal(opened, contents) {
		t.Fatal("decrypted object differs from committed plaintext")
	}

	if err := store.Put(t.Context(), scope, bytes.NewReader(contents)); err != nil {
		t.Fatalf("exact retry: %v", err)
	}
	if after := backend.snapshot(t, key); !bytes.Equal(after, firstCiphertext) {
		t.Fatal("exact retry replaced the immutable ciphertext")
	}
	if backend.objectCount() != 1 || backend.createdCount.Load() != 1 || keys.decryptCount.Load() < 2 {
		t.Fatalf(
			"retry evidence = objects %d creates %d decrypts %d",
			backend.objectCount(), backend.createdCount.Load(), keys.decryptCount.Load(),
		)
	}
}

func TestEncryptedObjectStoreHandlesChunkBoundaries(t *testing.T) {
	for index, size := range []int{1, objectChunkBytes - 1, objectChunkBytes, objectChunkBytes + 1, 2*objectChunkBytes + 17} {
		t.Run(fmt.Sprintf("size-%d", size), func(t *testing.T) {
			backend := newMemoryBlobStore()
			store := newTestEncryptedStore(t, backend, newTestDataKeyProvider(t), 3*objectChunkBytes)
			contents := make([]byte, size)
			for offset := range contents {
				contents[offset] = byte((offset*31 + 7) % 251)
			}
			objectID := fmt.Sprintf("40000000-0000-4000-8000-%012d", index+1)
			scope := testObjectScope(KindCheckpoint, objectID, contents)
			if err := store.Put(t.Context(), scope, bytes.NewReader(contents)); err != nil {
				t.Fatal(err)
			}
			reader, err := store.Open(t.Context(), scope)
			if err != nil {
				t.Fatal(err)
			}
			opened, err := io.ReadAll(reader)
			if closeErr := reader.Close(); err == nil {
				err = closeErr
			}
			if err != nil || !bytes.Equal(opened, contents) {
				t.Fatalf("round trip size %d = %d bytes, %v", size, len(opened), err)
			}
		})
	}
}

func TestEncryptedObjectStoreRejectsPlaintextDriftAndImmutableConflict(t *testing.T) {
	contents := []byte("the exact immutable prompt")
	tests := []struct {
		name   string
		scope  Scope
		source []byte
	}{
		{name: "short source", scope: testObjectScope(KindUserPrompt, "50000000-0000-4000-8000-000000000001", contents), source: contents[:len(contents)-1]},
		{name: "long source", scope: testObjectScope(KindUserPrompt, "50000000-0000-4000-8000-000000000002", contents), source: append(append([]byte(nil), contents...), '!')},
		{name: "digest mismatch", scope: testObjectScope(KindUserPrompt, "50000000-0000-4000-8000-000000000003", contents), source: bytes.Repeat([]byte{'x'}, len(contents))},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend := newMemoryBlobStore()
			store := newTestEncryptedStore(t, backend, newTestDataKeyProvider(t), objectChunkBytes)
			if err := store.Put(t.Context(), test.scope, bytes.NewReader(test.source)); err == nil {
				t.Fatal("Put() accepted plaintext that drifted from its pointer")
			}
			if backend.objectCount() != 0 {
				t.Fatal("failed plaintext validation published an immutable object")
			}
		})
	}

	backend := newMemoryBlobStore()
	store := newTestEncryptedStore(t, backend, newTestDataKeyProvider(t), objectChunkBytes)
	scope := testObjectScope(KindUserPrompt, "50000000-0000-4000-8000-000000000004", contents)
	if err := store.Put(t.Context(), scope, bytes.NewReader(contents)); err != nil {
		t.Fatal(err)
	}
	conflict := scope
	conflict.Descriptor.MediaType = "application/octet-stream"
	if err := store.Put(t.Context(), conflict, bytes.NewReader(contents)); !errors.Is(err, ErrObjectConflict) {
		t.Fatalf("immutable pointer conflict = %v, want ErrObjectConflict", err)
	}
}

func TestEncryptedObjectStoreDoesNotMisclassifyRetryVerificationFailureAsConflict(t *testing.T) {
	backend := &failingOpenBlobStore{memoryBlobStore: newMemoryBlobStore()}
	store := newTestEncryptedStore(t, backend, newTestDataKeyProvider(t), objectChunkBytes)
	contents := []byte("exact immutable prompt")
	scope := testObjectScope(KindUserPrompt, "51000000-0000-4000-8000-000000000005", contents)
	if err := store.Put(t.Context(), scope, bytes.NewReader(contents)); err != nil {
		t.Fatal(err)
	}

	backend.openErr = errors.New("transient object backend failure")
	err := store.Put(t.Context(), scope, bytes.NewReader(contents))
	if !errors.Is(err, backend.openErr) || errors.Is(err, ErrObjectConflict) {
		t.Fatalf("exact retry verification failure = %v, want preserved non-conflict failure", err)
	}
}

func TestEncryptedObjectStoreRejectsCrossScopeSubstitution(t *testing.T) {
	backend := newMemoryBlobStore()
	keys := newTestDataKeyProvider(t)
	store := newTestEncryptedStore(t, backend, keys, objectChunkBytes)
	contents := []byte("workspace-bound prompt")
	original := testObjectScope(KindUserPrompt, "60000000-0000-4000-8000-000000000006", contents)
	if err := store.Put(t.Context(), original, bytes.NewReader(contents)); err != nil {
		t.Fatal(err)
	}

	for _, replacement := range []Scope{
		{WorkspaceID: testWorkspaceB, Kind: original.Kind, Descriptor: original.Descriptor},
		{WorkspaceID: original.WorkspaceID, Kind: KindCheckpoint, Descriptor: original.Descriptor},
	} {
		backend.set(store.objectKey(replacement), backend.snapshot(t, store.objectKey(original)))
		if _, err := store.Open(t.Context(), replacement); err == nil {
			t.Fatalf("Open() accepted ciphertext substituted into scope %+v", replacement)
		}
	}

	// Editing the clear header to mimic the destination still cannot move the
	// object: the wrapped data key was created with the original authority as
	// KMS encryption context.
	replacement := Scope{WorkspaceID: testWorkspaceB, Kind: original.Kind, Descriptor: original.Descriptor}
	forged := backend.snapshot(t, store.objectKey(original))
	bodyStart := headerPrefixBytes
	copy(forged[bodyStart+53:bodyStart+89], replacement.WorkspaceID)
	backend.set(store.objectKey(replacement), forged)
	if _, err := store.Open(t.Context(), replacement); err == nil {
		t.Fatal("Open() accepted a header rewritten across KMS authority")
	}
}

func TestEncryptedObjectStoreRejectsMalformedOrTamperedCiphertext(t *testing.T) {
	contents := bytes.Repeat([]byte("authenticated object payload"), 50_000)
	scope := testObjectScope(KindCheckpoint, "70000000-0000-4000-8000-000000000007", contents)
	baseBackend := newMemoryBlobStore()
	keys := newTestDataKeyProvider(t)
	baseStore := newTestEncryptedStore(t, baseBackend, keys, 2*objectChunkBytes)
	if err := baseStore.Put(t.Context(), scope, bytes.NewReader(contents)); err != nil {
		t.Fatal(err)
	}
	original := baseBackend.snapshot(t, baseStore.objectKey(scope))
	headerLength := headerPrefixBytes + int(binary.BigEndian.Uint32(original[16:20]))

	tests := []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{name: "magic", mutate: func(raw []byte) []byte { raw[0] ^= 0x80; return raw }},
		{name: "frame length", mutate: func(raw []byte) []byte { raw[headerLength+3] ^= 0x01; return raw }},
		{name: "ciphertext", mutate: func(raw []byte) []byte { raw[len(raw)-1] ^= 0x01; return raw }},
		{name: "truncated", mutate: func(raw []byte) []byte { return raw[:len(raw)-1] }},
		{name: "trailing", mutate: func(raw []byte) []byte { return append(raw, 0) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend := newMemoryBlobStore()
			store := newTestEncryptedStore(t, backend, keys, 2*objectChunkBytes)
			backend.set(store.objectKey(scope), test.mutate(append([]byte(nil), original...)))
			reader, err := store.Open(t.Context(), scope)
			if err == nil {
				_, readErr := io.Copy(io.Discard, reader)
				closeErr := reader.Close()
				err = errors.Join(readErr, closeErr)
			}
			if err == nil {
				t.Fatal("tampered ciphertext was accepted")
			}
		})
	}
}

func TestEncryptedObjectCloseAuthenticatesUnreadTail(t *testing.T) {
	backend := newMemoryBlobStore()
	store := newTestEncryptedStore(t, backend, newTestDataKeyProvider(t), 3*objectChunkBytes)
	contents := bytes.Repeat([]byte{'p'}, objectChunkBytes+128)
	scope := testObjectScope(KindCheckpoint, "80000000-0000-4000-8000-000000000008", contents)
	if err := store.Put(t.Context(), scope, bytes.NewReader(contents)); err != nil {
		t.Fatal(err)
	}
	key := store.objectKey(scope)
	tampered := backend.snapshot(t, key)
	tampered[len(tampered)-1] ^= 1
	backend.set(key, tampered)

	reader, err := store.Open(t.Context(), scope)
	if err != nil {
		t.Fatal(err)
	}
	first := make([]byte, 32)
	if _, err := io.ReadFull(reader, first); err != nil || !bytes.Equal(first, contents[:len(first)]) {
		t.Fatalf("read authenticated first chunk = %q, %v", first, err)
	}
	if err := reader.Close(); err == nil {
		t.Fatal("Close() did not authenticate the unread tampered tail")
	}
}

func TestEncryptedObjectStoreConcurrentExactPutPublishesOnce(t *testing.T) {
	backend := newMemoryBlobStore()
	store := newTestEncryptedStore(t, backend, newTestDataKeyProvider(t), 2*objectChunkBytes)
	contents := bytes.Repeat([]byte("same exact object"), 10_000)
	scope := testObjectScope(KindCheckpoint, "90000000-0000-4000-8000-000000000009", contents)

	const writers = 12
	start := make(chan struct{})
	errorsSeen := make(chan error, writers)
	var group sync.WaitGroup
	for range writers {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			errorsSeen <- store.Put(context.Background(), scope, bytes.NewReader(contents))
		}()
	}
	close(start)
	group.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		if err != nil {
			t.Fatalf("concurrent exact Put() = %v", err)
		}
	}
	if backend.objectCount() != 1 || backend.createdCount.Load() != 1 {
		t.Fatalf("concurrent publication = objects %d creates %d", backend.objectCount(), backend.createdCount.Load())
	}
}

func TestEncryptedObjectStoreRejectsInvalidConfigurationAndScope(t *testing.T) {
	backend := newMemoryBlobStore()
	keys := newTestDataKeyProvider(t)
	for _, config := range []Config{
		{Keys: keys, MaximumPlaintextBytes: 1},
		{Backend: backend, MaximumPlaintextBytes: 1},
		{Backend: backend, Keys: keys, Prefix: "../objects", MaximumPlaintextBytes: 1},
		{Backend: backend, Keys: keys, MaximumPlaintextBytes: 0},
	} {
		if _, err := New(config); err == nil {
			t.Fatalf("New(%+v) accepted invalid configuration", config)
		}
	}
	store := newTestEncryptedStore(t, backend, keys, objectChunkBytes)
	valid := testObjectScope(KindUserPrompt, "a0000000-0000-4000-8000-00000000000a", []byte("prompt"))
	invalid := []Scope{valid, valid, valid, valid}
	invalid[0].WorkspaceID = "not-a-workspace"
	invalid[1].Kind = "future-kind"
	invalid[2].Descriptor.ObjectID = "not-an-object"
	invalid[3].Descriptor.MediaType = "text/plain; charset=\"utf-8\""
	for _, scope := range invalid {
		if err := store.Put(t.Context(), scope, bytes.NewReader([]byte("prompt"))); err == nil {
			t.Fatalf("Put() accepted invalid scope %+v", scope)
		}
	}
}

func testObjectScope(kind Kind, objectID string, contents []byte) Scope {
	mediaType := "text/plain; charset=utf-8"
	if kind == KindCheckpoint {
		mediaType = "application/vnd.agentserver.codex-checkpoint.v1"
	}
	return Scope{
		WorkspaceID: testWorkspaceA, Kind: kind,
		Descriptor: Descriptor{
			ObjectID: objectID, SHA256: sha256.Sum256(contents), Size: int64(len(contents)), MediaType: mediaType,
		},
	}
}

func newTestEncryptedStore(t *testing.T, backend ImmutableBlobStore, keys DataKeyProvider, maximum int) *Store {
	t.Helper()
	store, err := New(Config{Backend: backend, Keys: keys, MaximumPlaintextBytes: int64(maximum)})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

type memoryBlobStore struct {
	mu           sync.Mutex
	objects      map[string][]byte
	createdCount atomic.Int64
}

type failingOpenBlobStore struct {
	*memoryBlobStore
	openErr error
}

func (store *failingOpenBlobStore) Open(ctx context.Context, key string) (Blob, error) {
	if store.openErr != nil {
		return Blob{}, store.openErr
	}
	return store.memoryBlobStore.Open(ctx, key)
}

func newMemoryBlobStore() *memoryBlobStore {
	return &memoryBlobStore{objects: make(map[string][]byte)}
}

func (store *memoryBlobStore) PutIfAbsent(ctx context.Context, key string, size int64, source io.Reader) (PutResult, error) {
	if err := ctx.Err(); err != nil {
		return PutResult{}, err
	}
	raw, err := io.ReadAll(io.LimitReader(source, size+1))
	if err != nil {
		return PutResult{}, err
	}
	if int64(len(raw)) != size {
		return PutResult{}, fmt.Errorf("test backend received %d bytes, want %d", len(raw), size)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, exists := store.objects[key]; exists {
		return PutResult{Created: false}, nil
	}
	store.objects[key] = append([]byte(nil), raw...)
	store.createdCount.Add(1)
	return PutResult{Created: true}, nil
}

func (store *memoryBlobStore) Open(ctx context.Context, key string) (Blob, error) {
	if err := ctx.Err(); err != nil {
		return Blob{}, err
	}
	store.mu.Lock()
	raw, exists := store.objects[key]
	raw = append([]byte(nil), raw...)
	store.mu.Unlock()
	if !exists {
		return Blob{}, ErrBlobNotFound
	}
	return Blob{Reader: io.NopCloser(bytes.NewReader(raw)), Size: int64(len(raw))}, nil
}

func (store *memoryBlobStore) snapshot(t *testing.T, key string) []byte {
	t.Helper()
	store.mu.Lock()
	defer store.mu.Unlock()
	raw, exists := store.objects[key]
	if !exists {
		t.Fatalf("test backend object %q is missing", key)
	}
	return append([]byte(nil), raw...)
}

func (store *memoryBlobStore) set(key string, raw []byte) {
	store.mu.Lock()
	store.objects[key] = append([]byte(nil), raw...)
	store.mu.Unlock()
}

func (store *memoryBlobStore) objectCount() int {
	store.mu.Lock()
	defer store.mu.Unlock()
	return len(store.objects)
}

type testDataKeyProvider struct {
	master       cipher.AEAD
	counter      atomic.Uint64
	decryptCount atomic.Int64
}

func newTestDataKeyProvider(t *testing.T) *testDataKeyProvider {
	t.Helper()
	masterKey := bytes.Repeat([]byte{0x5a}, 32)
	block, err := aes.NewCipher(masterKey)
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	return &testDataKeyProvider{master: aead}
}

func (provider *testDataKeyProvider) GenerateDataKey(ctx context.Context, aad []byte) (GeneratedDataKey, error) {
	if err := ctx.Err(); err != nil {
		return GeneratedDataKey{}, err
	}
	sequence := provider.counter.Add(1)
	seed := append(append([]byte(nil), aad...), make([]byte, 8)...)
	binary.BigEndian.PutUint64(seed[len(seed)-8:], sequence)
	plaintext := sha256.Sum256(seed)
	var nonce [12]byte
	binary.BigEndian.PutUint64(nonce[4:], sequence)
	wrapped := append([]byte(nil), nonce[:]...)
	wrapped = provider.master.Seal(wrapped, nonce[:], plaintext[:], aad)
	return GeneratedDataKey{KeyID: "test-kms-key-v1", Plaintext: plaintext, WrappedKey: wrapped}, nil
}

func (provider *testDataKeyProvider) DecryptDataKey(ctx context.Context, keyID string, wrapped, aad []byte) ([32]byte, error) {
	provider.decryptCount.Add(1)
	if err := ctx.Err(); err != nil {
		return [32]byte{}, err
	}
	if keyID != "test-kms-key-v1" || len(wrapped) < provider.master.NonceSize()+provider.master.Overhead()+32 {
		return [32]byte{}, errors.New("test KMS envelope is invalid")
	}
	nonce := wrapped[:provider.master.NonceSize()]
	plaintext, err := provider.master.Open(nil, nonce, wrapped[provider.master.NonceSize():], aad)
	if err != nil || len(plaintext) != 32 {
		return [32]byte{}, errors.New("test KMS encryption context does not match")
	}
	var key [32]byte
	copy(key[:], plaintext)
	clear(plaintext)
	return key, nil
}
