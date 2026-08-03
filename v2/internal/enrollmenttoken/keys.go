package enrollmenttoken

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const maximumKeyFileBytes = 4096

// LoadCodec reads the Core-only enrollment MAC key from a restricted regular
// file target. A Kubernetes read-only Secret mount may grant read access to
// the Pod's fsGroup; group write/execute and every other-user bit remain
// forbidden. The file contains canonical unpadded base64url, with no newline.
func LoadCodec(issuer, path string) (*Codec, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errors.New("executor enrollment token key path must be absolute")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open executor enrollment token key: %w", err)
	}
	before, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("stat executor enrollment token key: %w", err)
	}
	if !before.Mode().IsRegular() || before.Mode().Perm()&0o037 != 0 || before.Size() < 1 || before.Size() > maximumKeyFileBytes {
		_ = file.Close()
		return nil, errors.New("executor enrollment token key must resolve to a bounded regular file only group-readable and inaccessible to other")
	}
	raw, readErr := io.ReadAll(io.LimitReader(file, maximumKeyFileBytes+1))
	defer clear(raw)
	after, statErr := file.Stat()
	closeErr := file.Close()
	if readErr != nil || statErr != nil || closeErr != nil {
		return nil, errors.Join(readErr, statErr, closeErr)
	}
	if !after.Mode().IsRegular() || after.Mode().Perm()&0o037 != 0 || !os.SameFile(before, after) ||
		before.Mode() != after.Mode() || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) ||
		int64(len(raw)) != after.Size() || len(raw) > maximumKeyFileBytes {
		return nil, errors.New("executor enrollment token key file changed or exceeds its size bound")
	}
	key := make([]byte, base64.RawURLEncoding.DecodedLen(len(raw)))
	decodedBytes, err := base64.RawURLEncoding.Decode(key, raw)
	if err == nil {
		key = key[:decodedBytes]
	}
	canonical := make([]byte, base64.RawURLEncoding.EncodedLen(len(key)))
	base64.RawURLEncoding.Encode(canonical, key)
	canonicalMatches := bytes.Equal(canonical, raw)
	clear(canonical)
	if err != nil || len(key) != 32 || !canonicalMatches {
		clear(key)
		return nil, errors.New("executor enrollment token key must be canonical unpadded base64url containing exactly 256 bits")
	}
	defer clear(key)
	return New(issuer, key)
}
