package harnesspool

import (
	"bytes"
	"crypto/ed25519"
	"crypto/subtle"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const maximumManifestSigningKeyFileBytes = 4 * 1024

// LoadEd25519ManifestSigner reads one active manifest signing key from a
// read-only secret projection. Rotation is explicit: a replacement key uses a
// new key ID and a rolling pool deployment; workers keep the corresponding
// old and new public keys during the overlap window.
//
// The file may contain a raw 32-byte Ed25519 seed, a canonical 64-byte
// private key, or one unencrypted PKCS#8 PRIVATE KEY PEM block. A Kubernetes
// read-only Secret mount may grant read access to the pool's fsGroup, while
// group write/execute and every other-user bit remain forbidden.
func LoadEd25519ManifestSigner(keyID, path string) (*Ed25519ManifestSigner, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("run manifest signing key path must be absolute")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open run manifest signing key: %w", err)
	}
	defer file.Close()

	before, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat run manifest signing key: %w", err)
	}
	if !before.Mode().IsRegular() {
		return nil, errors.New("run manifest signing key must resolve to a regular file")
	}
	if before.Mode().Perm()&0o037 != 0 {
		return nil, errors.New("run manifest signing key must only be group-readable and inaccessible to other")
	}
	if before.Size() < 1 || before.Size() > maximumManifestSigningKeyFileBytes {
		return nil, fmt.Errorf("run manifest signing key file must contain between 1 and %d bytes", maximumManifestSigningKeyFileBytes)
	}

	raw, err := io.ReadAll(io.LimitReader(file, maximumManifestSigningKeyFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read run manifest signing key: %w", err)
	}
	defer clear(raw)
	if len(raw) > maximumManifestSigningKeyFileBytes {
		return nil, fmt.Errorf("run manifest signing key file exceeds %d bytes", maximumManifestSigningKeyFileBytes)
	}
	after, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("restat run manifest signing key: %w", err)
	}
	if !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return nil, errors.New("run manifest signing key changed while it was being read")
	}

	privateKey, err := decodeEd25519ManifestPrivateKey(raw)
	if err != nil {
		return nil, err
	}
	defer clear(privateKey)
	return NewEd25519ManifestSigner(keyID, privateKey)
}

func decodeEd25519ManifestPrivateKey(raw []byte) (ed25519.PrivateKey, error) {
	if len(raw) == ed25519.SeedSize {
		if bytes.Equal(raw, make([]byte, ed25519.SeedSize)) {
			return nil, errors.New("run manifest Ed25519 seed must not be all zero")
		}
		return ed25519.NewKeyFromSeed(raw), nil
	}
	if len(raw) == ed25519.PrivateKeySize {
		return validateCanonicalEd25519PrivateKey(raw)
	}

	block, rest := pem.Decode(raw)
	if block == nil || block.Type != "PRIVATE KEY" || len(block.Headers) != 0 || len(bytes.TrimSpace(rest)) != 0 {
		return nil, errors.New("run manifest signing key must be a raw Ed25519 seed/private key or one unencrypted PKCS#8 PRIVATE KEY PEM block")
	}
	defer clear(block.Bytes)
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse run manifest PKCS#8 private key: %w", err)
	}
	privateKey, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("run manifest PKCS#8 private key is not Ed25519")
	}
	defer clear(privateKey)
	return validateCanonicalEd25519PrivateKey(privateKey)
}

func validateCanonicalEd25519PrivateKey(privateKey []byte) (ed25519.PrivateKey, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return nil, errors.New("run manifest Ed25519 private key has invalid length")
	}
	var zeroSeed [ed25519.SeedSize]byte
	if subtle.ConstantTimeCompare(privateKey[:ed25519.SeedSize], zeroSeed[:]) == 1 {
		return nil, errors.New("run manifest Ed25519 seed must not be all zero")
	}
	derived := ed25519.NewKeyFromSeed(privateKey[:ed25519.SeedSize])
	if subtle.ConstantTimeCompare(derived, privateKey) != 1 {
		clear(derived)
		return nil, errors.New("run manifest Ed25519 private key is not canonical for its seed")
	}
	return derived, nil
}
