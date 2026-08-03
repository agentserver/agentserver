package coreserver

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
	"strconv"
	"strings"

	"github.com/agentserver/agentserver/v2/internal/braincatalog"
)

const (
	LLMGatewaySealingKeyringVersion = 1
	LLMGatewaySealingAlgorithm      = "aes-256-gcm-v1"

	maximumLLMGatewaySealingKeys      = 16
	maximumLLMGatewaySealingKeyID     = 128
	maximumLLMGatewayKeyringBytes     = 64 * 1024
	maximumLLMGatewaySealedPlaintext  = 256 * 1024
	llmGatewaySealedEnvelopeVersion   = byte(1)
	llmGatewaySealingAADDomain        = "agentserver-v2/workspace-llm-gateway/aes-256-gcm/v1\x00"
	llmGatewayAuthorizationPurpose    = "authorization-transaction"
	llmGatewayGrantTokenSetPurpose    = "grant-token-set"
	minimumLLMGatewaySealedCiphertext = 4 + 1 + 1 + 12 + 16 + 1
)

var llmGatewaySealedMagic = [4]byte{'A', 'G', 'S', '1'}

type LLMGatewaySealingKeyDocument struct {
	KeyID     string `json:"keyId"`
	Algorithm string `json:"algorithm"`
	Key       string `json:"key"`
}

type LLMGatewaySealingKeyringDocument struct {
	Version     int                            `json:"version"`
	ActiveKeyID string                         `json:"activeKeyId"`
	Keys        []LLMGatewaySealingKeyDocument `json:"keys"`
}

// LLMGatewaySealScope is authenticated, but not encrypted, context. A token
// set cannot be substituted across workspace, gateway, user, or gateway
// configuration version. Authorization transaction secrets additionally bind
// the one-time transaction identity.
type LLMGatewaySealScope struct {
	WorkspaceID    string
	GatewayID      string
	UserID         string
	GatewayVersion int64
	TransactionID  string
}

type LLMGatewayGrantSealer struct {
	activeKeyID string
	keys        map[string]cipher.AEAD
	random      io.Reader
}

func LoadLLMGatewayGrantSealer(path string) (*LLMGatewayGrantSealer, error) {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errors.New("LLM gateway sealing keyring path must be absolute and clean")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open LLM gateway sealing keyring: %w", err)
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat LLM gateway sealing keyring: %w", err)
	}
	if !before.Mode().IsRegular() || before.Mode().Perm()&0o037 != 0 || before.Size() < 1 || before.Size() > maximumLLMGatewayKeyringBytes {
		return nil, errors.New("LLM gateway sealing keyring must be a bounded regular file only group-readable and inaccessible to other")
	}
	raw, err := io.ReadAll(io.LimitReader(file, maximumLLMGatewayKeyringBytes+1))
	if err != nil || len(raw) > maximumLLMGatewayKeyringBytes {
		clear(raw)
		return nil, errors.New("read bounded LLM gateway sealing keyring")
	}
	defer clear(raw)
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return nil, errors.New("LLM gateway sealing keyring changed while it was being read")
	}
	return ParseLLMGatewayGrantSealer(raw)
}

func ParseLLMGatewayGrantSealer(raw []byte) (*LLMGatewayGrantSealer, error) {
	if len(raw) < 1 || len(raw) > maximumLLMGatewayKeyringBytes {
		return nil, errors.New("LLM gateway sealing keyring is outside protocol bounds")
	}
	limits := braincatalog.DefaultLimits()
	limits.MaxCatalogBytes = maximumLLMGatewayKeyringBytes
	limits.MaxSchemaBytes = maximumLLMGatewayKeyringBytes
	limits.MaxJSONValues = 256
	limits.MaxJSONDepth = 4
	if _, _, err := braincatalog.DecodeCanonicalJSON(raw, maximumLLMGatewayKeyringBytes, limits); err != nil {
		return nil, fmt.Errorf("validate LLM gateway sealing keyring JSON: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var document LLMGatewaySealingKeyringDocument
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("decode LLM gateway sealing keyring: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("LLM gateway sealing keyring contains trailing JSON")
	}
	if document.Version != LLMGatewaySealingKeyringVersion || !validLLMGatewaySealingKeyID(document.ActiveKeyID) {
		return nil, errors.New("LLM gateway sealing keyring version or active key ID is invalid")
	}
	if len(document.Keys) < 1 || len(document.Keys) > maximumLLMGatewaySealingKeys {
		return nil, fmt.Errorf("LLM gateway sealing keyring must contain between one and %d keys", maximumLLMGatewaySealingKeys)
	}
	keys := make(map[string]cipher.AEAD, len(document.Keys))
	for index, entry := range document.Keys {
		if !validLLMGatewaySealingKeyID(entry.KeyID) || entry.Algorithm != LLMGatewaySealingAlgorithm {
			return nil, fmt.Errorf("keys[%d] identity or algorithm is invalid", index)
		}
		decoded, err := base64.RawURLEncoding.DecodeString(entry.Key)
		if err != nil || len(decoded) != 32 || base64.RawURLEncoding.EncodeToString(decoded) != entry.Key {
			clear(decoded)
			return nil, fmt.Errorf("keys[%d].key must be a canonical base64url AES-256 key", index)
		}
		var zero [32]byte
		if subtle.ConstantTimeCompare(decoded, zero[:]) == 1 {
			clear(decoded)
			return nil, fmt.Errorf("keys[%d].key must not be all zero", index)
		}
		block, err := aes.NewCipher(decoded)
		clear(decoded)
		if err != nil {
			return nil, fmt.Errorf("initialize LLM gateway sealing key %q: %w", entry.KeyID, err)
		}
		aead, err := cipher.NewGCM(block)
		if err != nil || aead.NonceSize() != 12 || aead.Overhead() != 16 {
			return nil, fmt.Errorf("initialize LLM gateway GCM key %q", entry.KeyID)
		}
		if _, duplicate := keys[entry.KeyID]; duplicate {
			return nil, fmt.Errorf("LLM gateway sealing keyring repeats key ID %q", entry.KeyID)
		}
		keys[entry.KeyID] = aead
	}
	if _, found := keys[document.ActiveKeyID]; !found {
		return nil, errors.New("LLM gateway sealing keyring does not contain its active key")
	}
	return &LLMGatewayGrantSealer{activeKeyID: document.ActiveKeyID, keys: keys, random: rand.Reader}, nil
}

func (sealer *LLMGatewayGrantSealer) SealAuthorizationSecrets(scope LLMGatewaySealScope, plaintext []byte) ([]byte, error) {
	if scope.TransactionID == "" {
		return nil, errors.New("LLM gateway authorization transaction identity is required")
	}
	return sealer.seal(scope, llmGatewayAuthorizationPurpose, plaintext)
}

func (sealer *LLMGatewayGrantSealer) OpenAuthorizationSecrets(scope LLMGatewaySealScope, sealed []byte) ([]byte, error) {
	if scope.TransactionID == "" {
		return nil, errors.New("LLM gateway authorization transaction identity is required")
	}
	return sealer.open(scope, llmGatewayAuthorizationPurpose, sealed)
}

func (sealer *LLMGatewayGrantSealer) SealGrantTokenSet(scope LLMGatewaySealScope, plaintext []byte) ([]byte, error) {
	if scope.TransactionID != "" {
		return nil, errors.New("LLM gateway grant token scope must not contain a transaction identity")
	}
	return sealer.seal(scope, llmGatewayGrantTokenSetPurpose, plaintext)
}

func (sealer *LLMGatewayGrantSealer) OpenGrantTokenSet(scope LLMGatewaySealScope, sealed []byte) ([]byte, error) {
	if scope.TransactionID != "" {
		return nil, errors.New("LLM gateway grant token scope must not contain a transaction identity")
	}
	return sealer.open(scope, llmGatewayGrantTokenSetPurpose, sealed)
}

func (sealer *LLMGatewayGrantSealer) seal(scope LLMGatewaySealScope, purpose string, plaintext []byte) ([]byte, error) {
	if sealer == nil || sealer.random == nil || sealer.activeKeyID == "" || len(sealer.keys) == 0 {
		return nil, errors.New("LLM gateway grant sealer is not initialized")
	}
	if err := validateLLMGatewaySealScope(scope); err != nil {
		return nil, err
	}
	if len(plaintext) < 1 || len(plaintext) > maximumLLMGatewaySealedPlaintext {
		return nil, errors.New("LLM gateway sealing plaintext is outside protocol bounds")
	}
	aead := sealer.keys[sealer.activeKeyID]
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(sealer.random, nonce); err != nil {
		return nil, fmt.Errorf("generate LLM gateway sealing nonce: %w", err)
	}
	result := make([]byte, 0, 4+1+1+len(sealer.activeKeyID)+len(nonce)+len(plaintext)+aead.Overhead())
	result = append(result, llmGatewaySealedMagic[:]...)
	result = append(result, llmGatewaySealedEnvelopeVersion, byte(len(sealer.activeKeyID)))
	result = append(result, sealer.activeKeyID...)
	result = append(result, nonce...)
	result = aead.Seal(result, nonce, plaintext, llmGatewaySealAAD(scope, purpose))
	return result, nil
}

func (sealer *LLMGatewayGrantSealer) open(scope LLMGatewaySealScope, purpose string, sealed []byte) ([]byte, error) {
	if sealer == nil || len(sealer.keys) == 0 {
		return nil, errors.New("LLM gateway grant sealer is not initialized")
	}
	if err := validateLLMGatewaySealScope(scope); err != nil {
		return nil, err
	}
	if len(sealed) < minimumLLMGatewaySealedCiphertext || len(sealed) > maximumLLMGatewaySealedPlaintext+512 ||
		!bytes.Equal(sealed[:4], llmGatewaySealedMagic[:]) || sealed[4] != llmGatewaySealedEnvelopeVersion {
		return nil, errors.New("sealed LLM gateway value is outside protocol bounds")
	}
	keyIDLength := int(sealed[5])
	keyIDEnd := 6 + keyIDLength
	if keyIDLength < 1 || keyIDEnd > len(sealed) || !validLLMGatewaySealingKeyID(string(sealed[6:keyIDEnd])) {
		return nil, errors.New("sealed LLM gateway value has an invalid key ID")
	}
	aead, found := sealer.keys[string(sealed[6:keyIDEnd])]
	if !found {
		return nil, errors.New("sealed LLM gateway value uses an unavailable rotation key")
	}
	nonceEnd := keyIDEnd + aead.NonceSize()
	if nonceEnd+aead.Overhead()+1 > len(sealed) {
		return nil, errors.New("sealed LLM gateway value is truncated")
	}
	plaintext, err := aead.Open(nil, sealed[keyIDEnd:nonceEnd], sealed[nonceEnd:], llmGatewaySealAAD(scope, purpose))
	if err != nil {
		return nil, errors.New("sealed LLM gateway value failed authentication")
	}
	if len(plaintext) < 1 || len(plaintext) > maximumLLMGatewaySealedPlaintext {
		clear(plaintext)
		return nil, errors.New("opened LLM gateway value is outside protocol bounds")
	}
	return plaintext, nil
}

func validateLLMGatewaySealScope(scope LLMGatewaySealScope) error {
	for name, value := range map[string]string{
		"workspace ID": scope.WorkspaceID, "gateway ID": scope.GatewayID, "user ID": scope.UserID,
	} {
		if !canonicalPublicUUID(value) {
			return fmt.Errorf("LLM gateway sealing %s is invalid", name)
		}
	}
	if scope.GatewayVersion < 1 || scope.GatewayVersion > 1<<53-1 {
		return errors.New("LLM gateway sealing configuration version is invalid")
	}
	if scope.TransactionID != "" && !canonicalPublicUUID(scope.TransactionID) {
		return errors.New("LLM gateway sealing transaction identity is invalid")
	}
	return nil
}

func llmGatewaySealAAD(scope LLMGatewaySealScope, purpose string) []byte {
	fields := []string{
		llmGatewaySealingAADDomain, purpose, scope.WorkspaceID, scope.GatewayID,
		scope.UserID, strconv.FormatInt(scope.GatewayVersion, 10), scope.TransactionID,
	}
	capacity := 0
	for _, field := range fields {
		capacity += 4 + len(field)
	}
	result := make([]byte, 0, capacity)
	for _, field := range fields {
		var length [4]byte
		binary.BigEndian.PutUint32(length[:], uint32(len(field)))
		result = append(result, length[:]...)
		result = append(result, field...)
	}
	return result
}

func validLLMGatewaySealingKeyID(value string) bool {
	if value == "" || len(value) > maximumLLMGatewaySealingKeyID || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if !((character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '-' || character == '_') {
			return false
		}
	}
	return true
}
