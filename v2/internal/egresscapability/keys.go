package egresscapability

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
	"strings"

	"github.com/agentserver/agentserver/v2/internal/braincatalog"
)

const (
	KeyringVersion         = 1
	maximumPrivateKeyBytes = 4 * 1024
	maximumKeyringBytes    = 64 * 1024
)

type VerificationKeyDocument struct {
	Issuer    string `json:"issuer"`
	Audience  string `json:"audience"`
	KeyID     string `json:"keyId"`
	Algorithm string `json:"algorithm"`
	PublicKey string `json:"publicKey"`
}

type KeyringDocument struct {
	Version int                       `json:"version"`
	Keys    []VerificationKeyDocument `json:"keys"`
}

func LoadSigner(issuer, keyID, filePath string) (*Signer, error) {
	raw, err := readStableFile("egress placeholder signing key", filePath, maximumPrivateKeyBytes, true)
	if err != nil {
		return nil, err
	}
	defer clear(raw)
	privateKey, err := decodePrivateKey(raw)
	if err != nil {
		return nil, err
	}
	defer clear(privateKey)
	return NewSigner(issuer, keyID, privateKey)
}

func LoadVerifier(filePath string) (*Verifier, error) {
	raw, err := readStableFile("egress placeholder verification keyring", filePath, maximumKeyringBytes, false)
	if err != nil {
		return nil, err
	}
	return ParseVerifier(raw)
}

func ParseVerifier(raw []byte) (*Verifier, error) {
	if len(raw) < 1 || len(raw) > maximumKeyringBytes {
		return nil, errors.New("egress placeholder keyring size is invalid")
	}
	limits := braincatalog.DefaultLimits()
	limits.MaxCatalogBytes = maximumKeyringBytes
	limits.MaxSchemaBytes = maximumKeyringBytes
	limits.MaxJSONValues = 512
	limits.MaxJSONDepth = 4
	if _, _, err := braincatalog.DecodeCanonicalJSON(raw, maximumKeyringBytes, limits); err != nil {
		return nil, fmt.Errorf("validate egress placeholder keyring JSON: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var document KeyringDocument
	if err := decoder.Decode(&document); err != nil {
		return nil, err
	}
	if err := finishJSON(decoder); err != nil {
		return nil, err
	}
	if document.Version != KeyringVersion || len(document.Keys) < 1 || len(document.Keys) > 64 {
		return nil, errors.New("egress placeholder keyring version or key count is invalid")
	}
	keys := make([]TrustedKey, 0, len(document.Keys))
	for index, entry := range document.Keys {
		if (entry.Audience != AudienceLarkReadOnly && !strings.HasPrefix(entry.Audience, AudienceCredentialPrefix)) ||
			entry.Algorithm != SignatureAlgorithm {
			return nil, fmt.Errorf("keys[%d] audience or algorithm is invalid", index)
		}
		publicKey, err := base64.RawURLEncoding.DecodeString(entry.PublicKey)
		if err != nil || len(publicKey) != ed25519.PublicKeySize || base64.RawURLEncoding.EncodeToString(publicKey) != entry.PublicKey {
			return nil, fmt.Errorf("keys[%d].publicKey is not a canonical Ed25519 key", index)
		}
		keys = append(keys, TrustedKey{Issuer: entry.Issuer, Audience: entry.Audience, KeyID: entry.KeyID, PublicKey: ed25519.PublicKey(publicKey)})
	}
	return NewVerifier(keys)
}

func readStableFile(label, filePath string, maximum int64, secret bool) ([]byte, error) {
	if filePath == "" || !filepath.IsAbs(filePath) || filepath.Clean(filePath) != filePath {
		return nil, fmt.Errorf("%s path must be absolute and clean", label)
	}
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", label, err)
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Size() < 1 || before.Size() > maximum {
		return nil, fmt.Errorf("%s must be a bounded regular file", label)
	}
	if secret && before.Mode().Perm()&0o037 != 0 {
		return nil, fmt.Errorf("%s must only be group-readable and inaccessible to other", label)
	}
	raw, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil || int64(len(raw)) > maximum {
		return nil, fmt.Errorf("read bounded %s: %w", label, err)
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return nil, fmt.Errorf("%s changed while it was being read", label)
	}
	return raw, nil
}

func decodePrivateKey(raw []byte) (ed25519.PrivateKey, error) {
	if len(raw) == ed25519.SeedSize {
		return ed25519.NewKeyFromSeed(raw), nil
	}
	if len(raw) == ed25519.PrivateKeySize {
		return append(ed25519.PrivateKey(nil), raw...), nil
	}
	block, rest := pem.Decode(raw)
	if block == nil || block.Type != "PRIVATE KEY" || len(block.Headers) != 0 || len(bytes.TrimSpace(rest)) != 0 {
		return nil, errors.New("egress placeholder signing key must be raw Ed25519 or one PKCS#8 PRIVATE KEY PEM block")
	}
	defer clear(block.Bytes)
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	privateKey, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("egress placeholder PKCS#8 key is not Ed25519")
	}
	return append(ed25519.PrivateKey(nil), privateKey...), nil
}
