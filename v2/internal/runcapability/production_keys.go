package runcapability

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/agentserver/agentserver/v2/internal/braincatalog"
)

const (
	ProductionKeyringVersion = 1
	maximumPrivateKeyBytes   = 4 * 1024
	maximumKeyringBytes      = 64 * 1024
)

type ProductionVerificationKeyDocument struct {
	KeyID     string `json:"keyId"`
	Algorithm string `json:"algorithm"`
	PublicKey string `json:"publicKey"`
}

type ProductionKeyringDocument struct {
	Version int                                 `json:"version"`
	Keys    []ProductionVerificationKeyDocument `json:"keys"`
}

// LoadProductionSigner reads the Core-only active key from a restricted
// Secret projection. Rotation deploys a new key ID while verifiers retain an
// explicit old/new public-key overlap; unknown IDs never fall back.
func LoadProductionSigner(issuer, keyID, path string) (*ProductionSigner, error) {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errors.New("production run capability signing key path must be absolute and clean")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open production run capability signing key: %w", err)
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat production run capability signing key: %w", err)
	}
	if !before.Mode().IsRegular() {
		return nil, errors.New("production run capability signing key must resolve to a regular file")
	}
	if before.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("production run capability signing key must not be accessible by group or other")
	}
	if before.Size() < 1 || before.Size() > maximumPrivateKeyBytes {
		return nil, fmt.Errorf("production run capability signing key must contain between 1 and %d bytes", maximumPrivateKeyBytes)
	}
	raw, err := io.ReadAll(io.LimitReader(file, maximumPrivateKeyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read production run capability signing key: %w", err)
	}
	defer clear(raw)
	if len(raw) > maximumPrivateKeyBytes {
		return nil, fmt.Errorf("production run capability signing key exceeds %d bytes", maximumPrivateKeyBytes)
	}
	after, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("restat production run capability signing key: %w", err)
	}
	if !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return nil, errors.New("production run capability signing key changed while it was being read")
	}
	privateKey, err := decodeProductionPrivateKey(raw)
	if err != nil {
		return nil, err
	}
	defer clear(privateKey)
	return NewProductionSigner(issuer, keyID, privateKey)
}

func decodeProductionPrivateKey(raw []byte) (ed25519.PrivateKey, error) {
	if len(raw) == ed25519.SeedSize {
		return ed25519.NewKeyFromSeed(raw), nil
	}
	if len(raw) == ed25519.PrivateKeySize {
		return append(ed25519.PrivateKey(nil), raw...), nil
	}
	block, rest := pem.Decode(raw)
	if block == nil || block.Type != "PRIVATE KEY" || len(block.Headers) != 0 || len(bytes.TrimSpace(rest)) != 0 {
		return nil, errors.New("production run capability signing key must be a raw Ed25519 seed/private key or one unencrypted PKCS#8 PRIVATE KEY PEM block")
	}
	defer clear(block.Bytes)
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse production run capability PKCS#8 private key: %w", err)
	}
	privateKey, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("production run capability PKCS#8 private key is not Ed25519")
	}
	return append(ed25519.PrivateKey(nil), privateKey...), nil
}

// ParseProductionVerifier builds an immutable verifier from a closed-world
// public keyring document and the separately configured expected issuer.
func ParseProductionVerifier(issuer string, raw []byte) (*ProductionVerifier, error) {
	if len(raw) < 1 || len(raw) > maximumKeyringBytes {
		return nil, fmt.Errorf("production run capability keyring must contain between 1 and %d bytes", maximumKeyringBytes)
	}
	limits := braincatalog.DefaultLimits()
	limits.MaxCatalogBytes = maximumKeyringBytes
	limits.MaxSchemaBytes = maximumKeyringBytes
	limits.MaxJSONValues = 256
	limits.MaxJSONDepth = 4
	if _, _, err := braincatalog.DecodeCanonicalJSON(raw, maximumKeyringBytes, limits); err != nil {
		return nil, fmt.Errorf("validate production run capability keyring JSON: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var document ProductionKeyringDocument
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("decode production run capability keyring: %w", err)
	}
	if err := finishProductionJSON(decoder); err != nil {
		return nil, fmt.Errorf("finish production run capability keyring: %w", err)
	}
	if document.Version != ProductionKeyringVersion {
		return nil, fmt.Errorf("production run capability keyring version must be %d", ProductionKeyringVersion)
	}
	if len(document.Keys) < 1 || len(document.Keys) > maximumTrustedKeys {
		return nil, fmt.Errorf("production run capability keyring must contain between 1 and %d keys", maximumTrustedKeys)
	}
	keys := make(map[string]ed25519.PublicKey, len(document.Keys))
	for index, entry := range document.Keys {
		if !validProductionText(entry.KeyID, maximumKeyIDBytes) {
			return nil, fmt.Errorf("keys[%d].keyId is invalid", index)
		}
		if entry.Algorithm != ProductionSignatureAlgorithm {
			return nil, fmt.Errorf("keys[%d].algorithm must be %q", index, ProductionSignatureAlgorithm)
		}
		decoded, err := base64.RawURLEncoding.DecodeString(entry.PublicKey)
		if err != nil || len(decoded) != ed25519.PublicKeySize || base64.RawURLEncoding.EncodeToString(decoded) != entry.PublicKey {
			return nil, fmt.Errorf("keys[%d].publicKey must be canonical base64url for a 32-byte Ed25519 public key", index)
		}
		if _, duplicate := keys[entry.KeyID]; duplicate {
			return nil, fmt.Errorf("production run capability keyring repeats key ID %q", entry.KeyID)
		}
		keys[entry.KeyID] = ed25519.PublicKey(decoded)
	}
	return NewProductionVerifier(issuer, keys)
}

func (verifier *ProductionVerifier) KeyIDs() []string {
	if verifier == nil {
		return nil
	}
	result := make([]string, 0, len(verifier.keys))
	for keyID := range verifier.keys {
		result = append(result, keyID)
	}
	sort.Strings(result)
	return result
}
