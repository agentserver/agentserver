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
func TarUpload(ctx context.Context, store ObjectStore, key, src string) error {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

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
		info, err := d.Info()
		if err != nil {
			return err
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
		_, copyErr := io.Copy(tw, f)
		_ = f.Close()
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
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar next: %w", err)
		}
		if strings.Contains(hdr.Name, "..") {
			return fmt.Errorf("untrusted path: %s", hdr.Name)
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
			if _, err := io.Copy(f, tr); err != nil {
				_ = f.Close()
				return fmt.Errorf("copy %s: %w", target, err)
			}
			_ = f.Close()
		default:
			// Skip symlinks / fifo / devices — claude doesn't write them.
		}
	}
	return nil
}
