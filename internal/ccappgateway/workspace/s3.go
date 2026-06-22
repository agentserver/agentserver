package workspace

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Tarball bounds for defense-in-depth against decompression-bomb / runaway-claude
// growth. Claude-home tarballs in normal use are << 10 MB. These ceilings give
// us operational visibility (clean error vs OOM crash) if a session grows
// pathologically. Spec § Open Risks #1 documents linear jsonl growth as a
// known limitation; these constants are the hard backstop.
const (
	maxTarballBytes = 2 << 30   // 2 GiB compressed tarball ceiling
	maxEntryBytes   = 256 << 20 // 256 MiB per file
	maxEntryCount   = 10000     // ~100 turns × 100 backup files would still fit; far more than realistic
)

// ErrObjectNotFound is returned by ObjectStore.Get when a key is absent.
// Implementations (s3client.go, test fakes) MUST translate their backend's
// "missing key" error to this sentinel.
var ErrObjectNotFound = errors.New("workspace: object not found")

// ObjectStore is the seam between workspace and the S3 client. Production
// uses internal/ccappgateway/s3client.go's *S3Client; tests use a
// map-backed fakeStore.
type ObjectStore interface {
	Put(ctx context.Context, key string, data []byte) error
	Get(ctx context.Context, key string) ([]byte, error)
	Delete(ctx context.Context, key string) error
}

// TarUpload tars+gzips the directory tree at src and writes it to store at key.
// Walks src with filepath.WalkDir; writes a tar header for every entry
// (including empty dirs); only files (TypeReg) carry data. Symlinks/fifo/
// devices are NOT written — claude-home contains none.
// Enforces bounds: maxEntryCount entries, maxEntryBytes per file, maxTarballBytes total.
func TarUpload(ctx context.Context, store ObjectStore, key, src string) error {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	var totalBytes int64
	var entryCount int

	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		entryCount++
		if entryCount > maxEntryCount {
			return fmt.Errorf("workspace: tarball entry count exceeds %d", maxEntryCount)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.Type().IsRegular() && info.Size() > maxEntryBytes {
			return fmt.Errorf("workspace: entry %q exceeds %d bytes", rel, maxEntryBytes)
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = rel
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		// Only regular files get content; codex's pattern skips
		// symlinks/fifo/devices silently — claude-home has none.
		if !d.Type().IsRegular() {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		n, copyErr := io.Copy(tw, f)
		_ = f.Close()
		totalBytes += n
		if totalBytes > maxTarballBytes {
			return fmt.Errorf("workspace: tarball total exceeds %d bytes", maxTarballBytes)
		}
		return copyErr
	})
	if err != nil {
		return fmt.Errorf("tar walk: %w", err)
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("tar close: %w", err)
	}
	if err := gz.Close(); err != nil {
		return fmt.Errorf("gz close: %w", err)
	}
	return store.Put(ctx, key, buf.Bytes())
}

// TarDownload fetches key from store and untars into dst (which must exist
// and be owned by the caller). Returns ErrObjectNotFound if the key is absent.
// Rejects archives containing ".." path components for safety.
// Enforces bounds: maxEntryCount entries, maxEntryBytes per file, maxTarballBytes total.
func TarDownload(ctx context.Context, store ObjectStore, key, dst string) error {
	data, err := store.Get(ctx, key)
	if err != nil {
		return err
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	tr := tar.NewReader(gz)
	var totalWritten int64
	var entryCount int
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar next: %w", err)
		}
		entryCount++
		if entryCount > maxEntryCount {
			return fmt.Errorf("workspace: tarball entry count exceeds %d", maxEntryCount)
		}
		if strings.Contains(hdr.Name, "..") {
			return fmt.Errorf("untrusted path: %s", hdr.Name)
		}
		if hdr.Size > maxEntryBytes {
			return fmt.Errorf("workspace: entry %q exceeds %d bytes", hdr.Name, maxEntryBytes)
		}
		target := filepath.Join(dst, hdr.Name)
		switch hdr.Typeflag {
		case tar.TypeDir:
			mode := fs.FileMode(hdr.Mode) & 0o700
			if mode == 0 {
				mode = 0o700
			}
			if err := os.MkdirAll(target, mode); err != nil {
				return fmt.Errorf("mkdir %s: %w", target, err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return fmt.Errorf("mkdir parent of %s: %w", target, err)
			}
			mode := fs.FileMode(hdr.Mode) & 0o600
			if mode == 0 {
				mode = 0o600
			}
			f, err := os.OpenFile(target, os.O_RDWR|os.O_CREATE|os.O_TRUNC, mode)
			if err != nil {
				return fmt.Errorf("open %s: %w", target, err)
			}
			// Bounded copy — also enforces per-entry cap even if hdr.Size lied.
			n, err := io.Copy(f, io.LimitReader(tr, maxEntryBytes))
			_ = f.Close()
			if err != nil {
				return fmt.Errorf("copy %s: %w", target, err)
			}
			totalWritten += n
			if totalWritten > maxTarballBytes {
				return fmt.Errorf("workspace: extracted size exceeds %d bytes", maxTarballBytes)
			}
		default:
			// Skip symlinks / fifo / devices — claude doesn't write them.
		}
	}
	return nil
}
