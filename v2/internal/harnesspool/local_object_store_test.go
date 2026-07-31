package harnesspool

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/checkpoint"
	"github.com/agentserver/agentserver/v2/internal/runmanifest"
)

func TestLocalDevelopmentObjectStoreReadsPromptAndCheckpointObjects(t *testing.T) {
	root := secureLocalDevelopmentObjectRoot(t)
	store, err := NewLocalDevelopmentObjectStore(root)
	if err != nil {
		t.Fatal(err)
	}
	prompt := []byte("run the deterministic task")
	promptPointer := runmanifest.ObjectPointer{
		ObjectID: "81000000-0000-4000-8000-000000000001", SHA256: localObjectDigestForTest(prompt),
		SizeBytes: int64(len(prompt)), MediaType: localDevelopmentPromptMediaType,
	}
	writeLocalObjectForTest(t, filepath.Join(root, promptPointer.ObjectID+".prompt"), prompt)
	checkpointBytes := []byte("bounded checkpoint object")
	checkpointPointer := checkpointPointerForTest("82000000-0000-4000-8000-000000000001", checkpointBytes)
	if _, err := store.PutCheckpointObject(t.Context(), checkpointPointer, bytes.NewReader(checkpointBytes)); err != nil {
		t.Fatal(err)
	}

	for name, pointer := range map[string]runmanifest.ObjectPointer{
		"prompt": promptPointer,
		"checkpoint": {
			ObjectID: checkpointPointer.ObjectID, SHA256: hex.EncodeToString(checkpointPointer.SHA256[:]),
			SizeBytes: checkpointPointer.Size, MediaType: checkpointPointer.MediaType,
		},
	} {
		t.Run(name, func(t *testing.T) {
			reader, err := store.OpenRunObject(t.Context(), pointer)
			if err != nil {
				t.Fatal(err)
			}
			contents, readErr := io.ReadAll(reader)
			closeErr := reader.Close()
			if readErr != nil || closeErr != nil {
				t.Fatalf("read/close = %v/%v", readErr, closeErr)
			}
			want := prompt
			if name == "checkpoint" {
				want = checkpointBytes
			}
			if !bytes.Equal(contents, want) {
				t.Fatalf("object contents = %q, want %q", contents, want)
			}
		})
	}
}

func TestLocalDevelopmentObjectStoreCheckpointPutIsImmutableAndIdempotent(t *testing.T) {
	root := secureLocalDevelopmentObjectRoot(t)
	store, err := NewLocalDevelopmentObjectStore(root)
	if err != nil {
		t.Fatal(err)
	}
	contents := []byte("first immutable checkpoint")
	pointer := checkpointPointerForTest("83000000-0000-4000-8000-000000000001", contents)
	for attempt := 0; attempt < 2; attempt++ {
		stored, err := store.PutCheckpointObject(t.Context(), pointer, bytes.NewReader(contents))
		if err != nil || stored != pointer {
			t.Fatalf("PutCheckpointObject() attempt %d = %+v, %v", attempt+1, stored, err)
		}
	}
	finalPath := filepath.Join(root, pointer.ObjectID+".checkpoint")
	info, err := os.Lstat(finalPath)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("checkpoint object mode = %s", info.Mode())
	}

	changed := []byte("other immutable checkpoint")
	changedPointer := checkpointPointerForTest(pointer.ObjectID, changed)
	if _, err := store.PutCheckpointObject(t.Context(), changedPointer, bytes.NewReader(changed)); err == nil {
		t.Fatal("same object ID accepted different immutable contents")
	}
	got, err := os.ReadFile(finalPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, contents) {
		t.Fatalf("immutable checkpoint was overwritten with %q", got)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(finalPath) {
		t.Fatalf("object root entries = %+v", entries)
	}
}

func TestLocalDevelopmentObjectStoreRejectsIncompleteCheckpointWrites(t *testing.T) {
	contents := []byte("checkpoint bytes")
	for _, test := range []struct {
		name    string
		pointer EventObjectPointer
		source  []byte
	}{
		{name: "short", pointer: checkpointPointerForTest("84000000-0000-4000-8000-000000000001", contents), source: contents[:len(contents)-1]},
		{name: "long", pointer: checkpointPointerForTest("84000000-0000-4000-8000-000000000002", contents), source: append(append([]byte(nil), contents...), 'x')},
		{name: "digest", pointer: checkpointPointerForTest("84000000-0000-4000-8000-000000000003", []byte("different bytes")), source: contents},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := secureLocalDevelopmentObjectRoot(t)
			store, err := NewLocalDevelopmentObjectStore(root)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.PutCheckpointObject(t.Context(), test.pointer, bytes.NewReader(test.source)); err == nil {
				t.Fatal("PutCheckpointObject() unexpectedly succeeded")
			}
			entries, err := os.ReadDir(root)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("failed checkpoint write retained %d object(s)", len(entries))
			}
		})
	}
}

func TestLocalDevelopmentObjectStoreRejectsUnsafeRootsAndObjects(t *testing.T) {
	if _, err := NewLocalDevelopmentObjectStore("relative"); err == nil {
		t.Fatal("relative object root was accepted")
	}
	broad := t.TempDir()
	if err := os.Chmod(broad, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := NewLocalDevelopmentObjectStore(broad); err == nil {
		t.Fatal("group/other-accessible object root was accepted")
	}
	target := secureLocalDevelopmentObjectRoot(t)
	symlink := filepath.Join(t.TempDir(), "objects")
	if err := os.Symlink(target, symlink); err != nil {
		t.Fatal(err)
	}
	if _, err := NewLocalDevelopmentObjectStore(symlink); err == nil {
		t.Fatal("symlink object root was accepted")
	}

	root := secureLocalDevelopmentObjectRoot(t)
	store, err := NewLocalDevelopmentObjectStore(root)
	if err != nil {
		t.Fatal(err)
	}
	prompt := []byte("prompt")
	pointer := runmanifest.ObjectPointer{
		ObjectID: "85000000-0000-4000-8000-000000000001", SHA256: localObjectDigestForTest(prompt),
		SizeBytes: int64(len(prompt)), MediaType: localDevelopmentPromptMediaType,
	}
	path := filepath.Join(root, pointer.ObjectID+".prompt")
	writeLocalObjectForTest(t, path, prompt)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.OpenRunObject(t.Context(), pointer); err == nil {
		t.Fatal("broad prompt object mode was accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	writeLocalObjectForTest(t, outside, prompt)
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	if _, err := store.OpenRunObject(t.Context(), pointer); err == nil {
		t.Fatal("symlink prompt object was accepted")
	}
}

func TestLocalDevelopmentObjectStoreReadObservesContextCancellation(t *testing.T) {
	root := secureLocalDevelopmentObjectRoot(t)
	store, err := NewLocalDevelopmentObjectStore(root)
	if err != nil {
		t.Fatal(err)
	}
	contents := []byte(strings.Repeat("x", 32))
	pointer := runmanifest.ObjectPointer{
		ObjectID: "86000000-0000-4000-8000-000000000001", SHA256: localObjectDigestForTest(contents),
		SizeBytes: int64(len(contents)), MediaType: localDevelopmentPromptMediaType,
	}
	writeLocalObjectForTest(t, filepath.Join(root, pointer.ObjectID+".prompt"), contents)
	ctx, cancel := context.WithCancel(t.Context())
	reader, err := store.OpenRunObject(ctx, pointer)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	var destination [1]byte
	if _, err := reader.Read(destination[:]); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled object read error = %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
}

func secureLocalDevelopmentObjectRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	return root
}

func checkpointPointerForTest(objectID string, contents []byte) EventObjectPointer {
	return EventObjectPointer{
		ObjectID: objectID, SHA256: sha256.Sum256(contents), Size: int64(len(contents)), MediaType: checkpoint.ArtifactMediaType,
	}
}

func localObjectDigestForTest(contents []byte) string {
	digest := sha256.Sum256(contents)
	return hex.EncodeToString(digest[:])
}

func writeLocalObjectForTest(t *testing.T, path string, contents []byte) {
	t.Helper()
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
}
