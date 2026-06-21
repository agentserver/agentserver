package workspace_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/internal/ccappgateway/workspace"
)

// fakeStore is a map-backed in-memory ObjectStore for tests.
type fakeStore struct{ data map[string][]byte }

func newFakeStore() *fakeStore { return &fakeStore{data: make(map[string][]byte)} }

func (f *fakeStore) Put(_ context.Context, key string, data []byte) error {
	f.data[key] = append([]byte(nil), data...)
	return nil
}

func (f *fakeStore) Get(_ context.Context, key string) ([]byte, error) {
	v, ok := f.data[key]
	if !ok {
		return nil, workspace.ErrObjectNotFound
	}
	return append([]byte(nil), v...), nil
}

func (f *fakeStore) Delete(_ context.Context, key string) error {
	delete(f.data, key)
	return nil
}

func TestTarRoundTrip(t *testing.T) {
	src := t.TempDir()
	// Populate src with: file at root, file in subdir, empty subdir.
	if err := os.WriteFile(filepath.Join(src, "root.txt"), []byte("root-content"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(src, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "sub", "nested.txt"), []byte("nested-content"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(src, "empty"), 0o700); err != nil {
		t.Fatal(err)
	}

	store := newFakeStore()
	ctx := context.Background()
	if err := workspace.TarUpload(ctx, store, "test/key.tar.gz", src); err != nil {
		t.Fatalf("TarUpload: %v", err)
	}

	dst := t.TempDir()
	if err := workspace.TarDownload(ctx, store, "test/key.tar.gz", dst); err != nil {
		t.Fatalf("TarDownload: %v", err)
	}

	// Verify root.txt round-tripped.
	if data, err := os.ReadFile(filepath.Join(dst, "root.txt")); err != nil {
		t.Fatalf("read root.txt: %v", err)
	} else if string(data) != "root-content" {
		t.Errorf("root.txt content: got %q, want %q", data, "root-content")
	}

	// Verify nested file.
	if data, err := os.ReadFile(filepath.Join(dst, "sub", "nested.txt")); err != nil {
		t.Fatalf("read sub/nested.txt: %v", err)
	} else if string(data) != "nested-content" {
		t.Errorf("sub/nested.txt content: got %q, want %q", data, "nested-content")
	}

	// Verify empty dir survived (regression for codex's WalkDir behavior).
	info, err := os.Stat(filepath.Join(dst, "empty"))
	if err != nil {
		t.Errorf("empty dir not preserved: %v", err)
	} else if !info.IsDir() {
		t.Errorf("empty/ should be a directory")
	}
}

func TestTarDownloadObjectNotFound(t *testing.T) {
	store := newFakeStore()
	dst := t.TempDir()
	err := workspace.TarDownload(context.Background(), store, "nonexistent", dst)
	if !errors.Is(err, workspace.ErrObjectNotFound) {
		t.Errorf("expected ErrObjectNotFound, got %v", err)
	}
}

func TestTarPathTraversalRejected(t *testing.T) {
	// Craft a tar with a path containing ".." and verify TarDownload rejects it.
	// Use raw tar+gzip writers since we control the malicious archive directly.
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "../escape.txt", Mode: 0o600, Size: 5, Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	tw.Write([]byte("evil!"))
	tw.Close()
	gz.Close()

	store := newFakeStore()
	store.data["evil-key"] = buf.Bytes()
	err := workspace.TarDownload(context.Background(), store, "evil-key", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "untrusted path") {
		t.Errorf("expected 'untrusted path' error, got %v", err)
	}
}

func TestTarUploadRejectsTooManyEntries(t *testing.T) {
	src := t.TempDir()
	// Create maxEntryCount+1 tiny files. Since maxEntryCount is package-private,
	// assume value 10000 from the implementation; test with 10001 entries.
	for i := 0; i < 10001; i++ {
		f := filepath.Join(src, fmt.Sprintf("f%05d.txt", i))
		if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	store := newFakeStore()
	err := workspace.TarUpload(context.Background(), store, "k", src)
	if err == nil {
		t.Error("expected error on too many entries, got nil")
	} else if !strings.Contains(err.Error(), "entry count exceeds") {
		t.Errorf("expected 'entry count exceeds' error, got: %v", err)
	}
}

func TestTarDownloadRejectsTooManyEntries(t *testing.T) {
	// Hand-craft a tarball with 10001 entries.
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for i := 0; i < 10001; i++ {
		hdr := &tar.Header{Name: fmt.Sprintf("f%05d.txt", i), Mode: 0o600, Size: 1, Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	gz.Close()

	store := newFakeStore()
	store.data["k"] = buf.Bytes()
	err := workspace.TarDownload(context.Background(), store, "k", t.TempDir())
	if err == nil {
		t.Error("expected error on too many entries, got nil")
	} else if !strings.Contains(err.Error(), "entry count exceeds") {
		t.Errorf("expected 'entry count exceeds' error, got: %v", err)
	}
}
