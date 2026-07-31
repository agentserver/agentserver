package runmanifest

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/agentserver/agentserver/v2/internal/braincatalog"
)

const (
	VerificationKeyringVersion = 1
	maximumVerificationKeys    = 32
	maximumKeyringBytes        = 64 * 1024
)

type VerificationKeyDocument struct {
	KeyID     string `json:"keyId"`
	Algorithm string `json:"algorithm"`
	PublicKey string `json:"publicKey"`
}

type VerificationKeyringDocument struct {
	Version int                       `json:"version"`
	Keys    []VerificationKeyDocument `json:"keys"`
}

// VerificationKeyring is an immutable overlap set used by workers during a
// manifest-signing-key rotation. The signed envelope selects a key by keyId;
// an unknown ID never falls back to another key.
type VerificationKeyring struct {
	keys map[string]ed25519.PublicKey
}

func ParseVerificationKeyring(raw []byte) (*VerificationKeyring, error) {
	if len(raw) == 0 || len(raw) > maximumKeyringBytes {
		return nil, fmt.Errorf("run manifest verification keyring must contain between 1 and %d bytes", maximumKeyringBytes)
	}
	limits := braincatalog.DefaultLimits()
	limits.MaxCatalogBytes = maximumKeyringBytes
	limits.MaxSchemaBytes = maximumKeyringBytes
	if _, _, err := braincatalog.DecodeCanonicalJSON(raw, maximumKeyringBytes, limits); err != nil {
		return nil, fmt.Errorf("validate run manifest verification keyring JSON: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var document VerificationKeyringDocument
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("decode run manifest verification keyring: %w", err)
	}
	if err := finishJSON(decoder); err != nil {
		return nil, fmt.Errorf("finish run manifest verification keyring: %w", err)
	}
	if document.Version != VerificationKeyringVersion {
		return nil, fmt.Errorf("run manifest verification keyring version must be %d", VerificationKeyringVersion)
	}
	if len(document.Keys) < 1 || len(document.Keys) > maximumVerificationKeys {
		return nil, fmt.Errorf("run manifest verification keyring must contain between 1 and %d keys", maximumVerificationKeys)
	}

	keyring := &VerificationKeyring{keys: make(map[string]ed25519.PublicKey, len(document.Keys))}
	for index, entry := range document.Keys {
		if err := validateText(fmt.Sprintf("keys[%d].keyId", index), entry.KeyID, 256, true); err != nil {
			return nil, err
		}
		if entry.Algorithm != SignatureAlgorithm {
			return nil, fmt.Errorf("keys[%d].algorithm must be %q", index, SignatureAlgorithm)
		}
		decoded, err := base64.RawURLEncoding.DecodeString(entry.PublicKey)
		if err != nil || len(decoded) != ed25519.PublicKeySize || base64.RawURLEncoding.EncodeToString(decoded) != entry.PublicKey {
			return nil, fmt.Errorf("keys[%d].publicKey must be canonical base64url for a 32-byte Ed25519 public key", index)
		}
		if _, duplicate := keyring.keys[entry.KeyID]; duplicate {
			return nil, fmt.Errorf("run manifest verification keyring repeats key ID %q", entry.KeyID)
		}
		keyring.keys[entry.KeyID] = append(ed25519.PublicKey(nil), decoded...)
	}
	return keyring, nil
}

func (keyring *VerificationKeyring) Verify(signed SignedManifest) (Manifest, error) {
	if keyring == nil || len(keyring.keys) == 0 {
		return Manifest{}, errors.New("run manifest verification keyring is required")
	}
	publicKey, ok := keyring.keys[signed.KeyID]
	if !ok {
		return Manifest{}, errors.New("run manifest signing key ID is not trusted")
	}
	return signed.Verify(signed.KeyID, publicKey)
}

func (keyring *VerificationKeyring) KeyIDs() []string {
	if keyring == nil {
		return nil
	}
	result := make([]string, 0, len(keyring.keys))
	for keyID := range keyring.keys {
		result = append(result, keyID)
	}
	sort.Strings(result)
	return result
}
