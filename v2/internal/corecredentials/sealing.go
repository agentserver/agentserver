package corecredentials

// This file contains the envelope used by workspace credential bindings.  It
// is intentionally local to corecredentials: Core and the deployment
// renderer deal in sealed bytes and key *references*, while only this process
// gets a keyring capable of opening an envelope.

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

const (
	SealingKeyringVersion = 1
	SealingAlgorithm      = "aes-256-gcm-v1"
	maximumKeyringBytes   = 64 * 1024
	maximumKeyCount       = 64
	maximumKeyIDBytes     = 128
	maximumSecretBytes    = 256 * 1024
	minimumEnvelopeBytes  = 4 + 1 + 1 + 1 + 12 + 16
	envelopeVersion       = 1
)

var (
	envelopeMagic = [4]byte{'A', 'S', 'C', 'P'}
	keyIDPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
)

// BindingSealScope is authenticated but not encrypted context.  A ciphertext
// copied to another workspace, binding, or credential version must fail GCM
// authentication.
type BindingSealScope struct {
	WorkspaceID       string
	BindingID         string
	CredentialVersion int64
}

func (scope BindingSealScope) validate() error {
	if !identifierPattern.MatchString(scope.WorkspaceID) ||
		!identifierPattern.MatchString(scope.BindingID) || scope.CredentialVersion < 1 {
		return errors.New("credential sealing scope is invalid")
	}
	return nil
}

// SealingKeyDocument and SealingKeyringDocument are the only JSON shape
// accepted from a mounted Kubernetes Secret.  Keys are base64url encoded
// raw AES-256 material.  The parser rejects unknown fields and non-canonical
// values in order to make rotation/reload deterministic.
type SealingKeyDocument struct {
	KeyID     string `json:"keyId"`
	Algorithm string `json:"algorithm"`
	Key       string `json:"key"`
}

type SealingKeyringDocument struct {
	Version     int                  `json:"version"`
	ActiveKeyID string               `json:"activeKeyId"`
	Keys        []SealingKeyDocument `json:"keys"`
}

// Keyring holds immutable AEAD instances.  Construct it once per process and
// replace the whole value on rotation; never mutate the map while requests
// are in flight.
type Keyring struct {
	activeKeyID string
	keys        map[string]cipher.AEAD
	random      io.Reader
}

func NewKeyring(activeKeyID string, keys map[string][]byte) (*Keyring, error) {
	if !keyIDPattern.MatchString(activeKeyID) || len(keys) < 1 || len(keys) > maximumKeyCount {
		return nil, errors.New("credential sealing keyring identity or key count is invalid")
	}
	result := &Keyring{activeKeyID: activeKeyID, keys: make(map[string]cipher.AEAD, len(keys)), random: rand.Reader}
	for keyID, material := range keys {
		if !keyIDPattern.MatchString(keyID) || len(material) != 32 {
			return nil, fmt.Errorf("credential sealing key %q must be a non-zero AES-256 key", keyID)
		}
		var zero [32]byte
		if subtle.ConstantTimeCompare(material, zero[:]) == 1 {
			return nil, fmt.Errorf("credential sealing key %q must not be all zero", keyID)
		}
		block, err := aes.NewCipher(material)
		if err != nil {
			return nil, fmt.Errorf("initialize credential sealing key %q: %w", keyID, err)
		}
		aead, err := cipher.NewGCM(block)
		if err != nil || aead.NonceSize() != 12 || aead.Overhead() != 16 {
			return nil, fmt.Errorf("initialize credential sealing GCM key %q", keyID)
		}
		result.keys[keyID] = aead
	}
	if _, ok := result.keys[activeKeyID]; !ok {
		return nil, errors.New("credential sealing keyring active key is unavailable")
	}
	return result, nil
}

func (keyring *Keyring) ActiveKeyID() string {
	if keyring == nil {
		return ""
	}
	return keyring.activeKeyID
}

func (keyring *Keyring) Seal(scope BindingSealScope, plaintext []byte) ([]byte, error) {
	if keyring == nil || keyring.random == nil || len(keyring.keys) == 0 {
		return nil, errors.New("credential sealing keyring is not initialized")
	}
	if err := scope.validate(); err != nil {
		return nil, err
	}
	if len(plaintext) == 0 || len(plaintext) > maximumSecretBytes {
		return nil, errors.New("credential secret is outside sealing bounds")
	}
	aead := keyring.keys[keyring.activeKeyID]
	if aead == nil {
		return nil, errors.New("credential sealing active key is unavailable")
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(keyring.random, nonce); err != nil {
		return nil, fmt.Errorf("generate credential sealing nonce: %w", err)
	}
	keyID := []byte(keyring.activeKeyID)
	if len(keyID) > 255 {
		return nil, errors.New("credential sealing key ID is too long")
	}
	result := make([]byte, 0, minimumEnvelopeBytes+len(keyID)+len(plaintext)+aead.Overhead())
	result = append(result, envelopeMagic[:]...)
	result = append(result, envelopeVersion, byte(len(keyID)))
	result = append(result, keyID...)
	result = append(result, nonce...)
	result = aead.Seal(result, nonce, plaintext, bindingSealAAD(scope))
	return result, nil
}

func (keyring *Keyring) Open(scope BindingSealScope, sealed []byte) ([]byte, error) {
	if keyring == nil || len(keyring.keys) == 0 {
		return nil, errors.New("credential sealing keyring is not initialized")
	}
	if err := scope.validate(); err != nil {
		return nil, err
	}
	if len(sealed) < minimumEnvelopeBytes || len(sealed) > maximumSecretBytes+512 ||
		!bytes.Equal(sealed[:4], envelopeMagic[:]) || sealed[4] != envelopeVersion {
		return nil, errors.New("sealed credential is outside protocol bounds")
	}
	keyIDLength := int(sealed[5])
	keyIDEnd := 6 + keyIDLength
	if keyIDLength < 1 || keyIDEnd > len(sealed) || !keyIDPattern.MatchString(string(sealed[6:keyIDEnd])) {
		return nil, errors.New("sealed credential key ID is invalid")
	}
	aead := keyring.keys[string(sealed[6:keyIDEnd])]
	if aead == nil {
		return nil, errors.New("sealed credential uses an unavailable rotation key")
	}
	nonceEnd := keyIDEnd + aead.NonceSize()
	if nonceEnd+aead.Overhead()+1 > len(sealed) {
		return nil, errors.New("sealed credential is truncated")
	}
	plaintext, err := aead.Open(nil, sealed[keyIDEnd:nonceEnd], sealed[nonceEnd:], bindingSealAAD(scope))
	if err != nil || len(plaintext) == 0 || len(plaintext) > maximumSecretBytes {
		clear(plaintext)
		return nil, errors.New("sealed credential authentication failed")
	}
	return plaintext, nil
}

func bindingSealAAD(scope BindingSealScope) []byte {
	// Length-prefix fields so concatenation cannot create an ambiguous scope.
	const domain = "agentserver-v2/corecredentials/aes-256-gcm/v1"
	result := make([]byte, 0, len(domain)+64+len(scope.WorkspaceID)+len(scope.BindingID))
	result = append(result, domain...)
	result = append(result, 0)
	appendField := func(value string) {
		var size [4]byte
		binary.BigEndian.PutUint32(size[:], uint32(len(value)))
		result = append(result, size[:]...)
		result = append(result, value...)
	}
	appendField(scope.WorkspaceID)
	appendField(scope.BindingID)
	var version [8]byte
	binary.BigEndian.PutUint64(version[:], uint64(scope.CredentialVersion))
	result = append(result, version[:]...)
	return result
}

func ParseKeyring(raw []byte) (*Keyring, error) {
	if len(raw) < 1 || len(raw) > maximumKeyringBytes {
		return nil, errors.New("credential sealing keyring size is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var document SealingKeyringDocument
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("decode credential sealing keyring: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("credential sealing keyring contains trailing JSON")
	}
	if document.Version != SealingKeyringVersion || !keyIDPattern.MatchString(document.ActiveKeyID) ||
		len(document.Keys) < 1 || len(document.Keys) > maximumKeyCount {
		return nil, errors.New("credential sealing keyring version, active key, or key count is invalid")
	}
	keys := make(map[string][]byte, len(document.Keys))
	for index, entry := range document.Keys {
		if !keyIDPattern.MatchString(entry.KeyID) || entry.Algorithm != SealingAlgorithm {
			return nil, fmt.Errorf("credential sealing key %d identity or algorithm is invalid", index)
		}
		decoded, err := base64.RawURLEncoding.DecodeString(entry.Key)
		if err != nil || len(decoded) != 32 || base64.RawURLEncoding.EncodeToString(decoded) != entry.Key {
			clear(decoded)
			return nil, fmt.Errorf("credential sealing key %d is not canonical AES-256 material", index)
		}
		if _, duplicate := keys[entry.KeyID]; duplicate {
			clear(decoded)
			return nil, fmt.Errorf("credential sealing keyring repeats key ID %q", entry.KeyID)
		}
		keys[entry.KeyID] = decoded
	}
	keyring, err := NewKeyring(document.ActiveKeyID, keys)
	for keyID := range keys {
		clear(keys[keyID])
	}
	if err != nil {
		return nil, err
	}
	return keyring, nil
}

func LoadKeyring(path string) (*Keyring, error) {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errors.New("credential sealing keyring path must be absolute and clean")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open credential sealing keyring: %w", err)
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Mode().Perm()&0o037 != 0 || before.Size() < 1 || before.Size() > maximumKeyringBytes {
		return nil, errors.New("credential sealing keyring must be a bounded regular file readable only by owner/group")
	}
	raw, err := io.ReadAll(io.LimitReader(file, maximumKeyringBytes+1))
	if err != nil || len(raw) > maximumKeyringBytes {
		clear(raw)
		return nil, errors.New("read bounded credential sealing keyring")
	}
	defer clear(raw)
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return nil, errors.New("credential sealing keyring changed while it was being read")
	}
	return ParseKeyring(raw)
}

// MarshalKeyring is useful to provisioning tooling and tests. It never
// returns plaintext key material except in the explicit output requested by
// the caller; production services should load a mounted Secret instead.
func MarshalKeyring(document SealingKeyringDocument) ([]byte, error) {
	if document.Version == 0 {
		document.Version = SealingKeyringVersion
	}
	if _, err := ParseKeyringMust(document); err != nil {
		return nil, err
	}
	return json.Marshal(document)
}

func ParseKeyringMust(document SealingKeyringDocument) (*Keyring, error) {
	raw, err := json.Marshal(document)
	if err != nil {
		return nil, err
	}
	return ParseKeyring(raw)
}

// clear is best-effort memory hygiene. Go cannot promise compiler-level
// eradication, but clearing buffers at ownership boundaries avoids retaining
// secrets in long-lived slices and makes accidental logging less likely.
func clear(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
