// Package sandboxcapability signs short-lived, action-scoped capabilities for
// the provider-neutral sandbox gateway. Tokens are request authority, not
// third-party credentials, and never contain provider session references.
package sandboxcapability

import (
	"bytes"
	"crypto/ed25519"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	Version            = 1
	SignatureAlgorithm = "ed25519-v1"
	AudienceLifecycle  = "sandbox-lifecycle"
	AudienceBackend    = "sandbox-backend"

	tokenPrefix        = "asv2sandbox1"
	signatureDomain    = "agentserver-v2/sandbox-capability/ed25519-v1\x00"
	maximumTokenBytes  = 32 * 1024
	maximumClaimsBytes = 24 * 1024
	maximumTextBytes   = 2048
	maximumKeyIDBytes  = 256
	maximumIssuerBytes = 512
	maximumLifetime    = 2 * time.Minute
)

type Claims struct {
	Version      int    `json:"version"`
	Issuer       string `json:"issuer"`
	Audience     string `json:"audience"`
	Action       string `json:"action"`
	CapabilityID string `json:"capabilityId"`

	WorkspaceID          string `json:"workspaceId"`
	SessionID            string `json:"sessionId"`
	EnvironmentID        string `json:"environmentId"`
	RunID                string `json:"runId"`
	RunAttemptID         string `json:"runAttemptId"`
	RunAttemptGeneration int64  `json:"runAttemptGeneration"`
	HolderID             string `json:"holderId,omitempty"`

	ExecutionID      string `json:"executionId,omitempty"`
	OperationID      string `json:"operationId,omitempty"`
	MutationKey      string `json:"mutationKey,omitempty"`
	SandboxID        string `json:"sandboxId,omitempty"`
	TargetGeneration int64  `json:"targetGeneration,omitempty"`

	IssuedAtUnixMS  int64 `json:"issuedAtUnixMs"`
	ExpiresAtUnixMS int64 `json:"expiresAtUnixMs"`
}

type Signer struct {
	issuer     string
	audience   string
	keyID      string
	privateKey ed25519.PrivateKey
}

func NewSigner(issuer, audience, keyID string, privateKey ed25519.PrivateKey) (*Signer, error) {
	if !validText(issuer, maximumIssuerBytes) || !validAudience(audience) || !validText(keyID, maximumKeyIDBytes) {
		return nil, errors.New("sandbox capability signer issuer, audience, or key ID is invalid")
	}
	if len(privateKey) != ed25519.PrivateKeySize {
		return nil, errors.New("sandbox capability signer requires an Ed25519 private key")
	}
	seed := privateKey.Seed()
	defer clear(seed)
	var zero [ed25519.SeedSize]byte
	if subtle.ConstantTimeCompare(seed, zero[:]) == 1 {
		return nil, errors.New("sandbox capability signer private key must not be all zero")
	}
	canonical := ed25519.NewKeyFromSeed(seed)
	if subtle.ConstantTimeCompare(privateKey, canonical) != 1 {
		clear(canonical)
		return nil, errors.New("sandbox capability signer private key is not canonical")
	}
	return &Signer{issuer: issuer, audience: audience, keyID: keyID, privateKey: canonical}, nil
}

func (signer *Signer) Issuer() string {
	if signer == nil {
		return ""
	}
	return signer.issuer
}

func (signer *Signer) Audience() string {
	if signer == nil {
		return ""
	}
	return signer.audience
}

func (signer *Signer) Sign(claims Claims) (string, error) {
	if signer == nil || len(signer.privateKey) != ed25519.PrivateKeySize {
		return "", errors.New("sandbox capability signer is required")
	}
	if claims.Issuer != signer.issuer || claims.Audience != signer.audience {
		return "", errors.New("sandbox capability claims differ from signer authority")
	}
	if err := claims.Validate(); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("encode sandbox capability claims: %w", err)
	}
	signature := ed25519.Sign(signer.privateKey, signatureMessage(signer.keyID, canonical))
	token := strings.Join([]string{
		tokenPrefix,
		base64.RawURLEncoding.EncodeToString([]byte(signer.keyID)),
		base64.RawURLEncoding.EncodeToString(canonical),
		base64.RawURLEncoding.EncodeToString(signature),
	}, ".")
	if len(token) > maximumTokenBytes {
		return "", errors.New("sandbox capability token exceeds its size bound")
	}
	return token, nil
}

type TrustedKey struct {
	Issuer    string
	Audience  string
	KeyID     string
	PublicKey ed25519.PublicKey
}

type Verifier struct {
	keys map[string]TrustedKey
}

func NewVerifier(keys []TrustedKey) (*Verifier, error) {
	if len(keys) < 1 || len(keys) > 64 {
		return nil, errors.New("sandbox capability verifier must contain between 1 and 64 keys")
	}
	trusted := make(map[string]TrustedKey, len(keys))
	for index, key := range keys {
		if !validText(key.Issuer, maximumIssuerBytes) || !validAudience(key.Audience) || !validText(key.KeyID, maximumKeyIDBytes) {
			return nil, fmt.Errorf("sandbox capability verifier key %d authority is invalid", index)
		}
		if len(key.PublicKey) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("sandbox capability verifier key %d is not Ed25519", index)
		}
		var zero [ed25519.PublicKeySize]byte
		if subtle.ConstantTimeCompare(key.PublicKey, zero[:]) == 1 {
			return nil, fmt.Errorf("sandbox capability verifier key %d must not be all zero", index)
		}
		if _, duplicate := trusted[key.KeyID]; duplicate {
			return nil, fmt.Errorf("sandbox capability verifier repeats key ID %q", key.KeyID)
		}
		copy := key
		copy.PublicKey = append(ed25519.PublicKey(nil), key.PublicKey...)
		trusted[key.KeyID] = copy
	}
	return &Verifier{keys: trusted}, nil
}

func (verifier *Verifier) Verify(token, expectedAudience, expectedAction string, now time.Time) (Claims, error) {
	if verifier == nil || len(verifier.keys) == 0 {
		return Claims{}, errors.New("sandbox capability verifier is required")
	}
	if token == "" || len(token) > maximumTokenBytes || strings.TrimSpace(token) != token {
		return Claims{}, errors.New("sandbox capability token is empty, padded, or too large")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 4 || parts[0] != tokenPrefix {
		return Claims{}, errors.New("sandbox capability token framing is invalid")
	}
	keyIDBytes, err := decodeBase64("key ID", parts[1], maximumKeyIDBytes)
	if err != nil || !validText(string(keyIDBytes), maximumKeyIDBytes) {
		return Claims{}, errors.New("sandbox capability key ID is invalid")
	}
	key, found := verifier.keys[string(keyIDBytes)]
	if !found {
		return Claims{}, errors.New("sandbox capability signing key is not trusted")
	}
	canonical, err := decodeBase64("claims", parts[2], maximumClaimsBytes)
	if err != nil {
		return Claims{}, err
	}
	signature, err := decodeBase64("signature", parts[3], ed25519.SignatureSize)
	if err != nil || len(signature) != ed25519.SignatureSize ||
		!ed25519.Verify(key.PublicKey, signatureMessage(key.KeyID, canonical), signature) {
		return Claims{}, errors.New("sandbox capability signature verification failed")
	}
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	var claims Claims
	if err := decoder.Decode(&claims); err != nil {
		return Claims{}, fmt.Errorf("decode sandbox capability claims: %w", err)
	}
	if err := finishJSON(decoder); err != nil {
		return Claims{}, fmt.Errorf("finish sandbox capability claims: %w", err)
	}
	normalized, err := json.Marshal(claims)
	if err != nil || !bytes.Equal(canonical, normalized) {
		return Claims{}, errors.New("sandbox capability claims are not canonical JSON")
	}
	if err := claims.Validate(); err != nil {
		return Claims{}, err
	}
	if claims.Issuer != key.Issuer || claims.Audience != key.Audience ||
		claims.Audience != expectedAudience || claims.Action != expectedAction {
		return Claims{}, errors.New("sandbox capability authority, audience, or action does not match")
	}
	if now.IsZero() || now.UnixMilli() < claims.IssuedAtUnixMS || now.UnixMilli() >= claims.ExpiresAtUnixMS {
		return Claims{}, errors.New("sandbox capability is not currently valid")
	}
	return claims, nil
}

func (claims Claims) Validate() error {
	if claims.Version != Version || !validText(claims.Issuer, maximumIssuerBytes) || !validAudience(claims.Audience) ||
		!validText(claims.Action, 64) || !validText(claims.CapabilityID, maximumTextBytes) {
		return errors.New("sandbox capability version or authority fields are invalid")
	}
	for name, value := range map[string]string{
		"workspaceId": claims.WorkspaceID, "sessionId": claims.SessionID,
		"environmentId": claims.EnvironmentID, "runId": claims.RunID,
		"runAttemptId": claims.RunAttemptID,
	} {
		if !validText(value, maximumTextBytes) {
			return fmt.Errorf("sandbox capability %s is invalid", name)
		}
	}
	if claims.RunAttemptGeneration < 1 || claims.IssuedAtUnixMS < 1 || claims.ExpiresAtUnixMS <= claims.IssuedAtUnixMS ||
		time.Duration(claims.ExpiresAtUnixMS-claims.IssuedAtUnixMS)*time.Millisecond > maximumLifetime {
		return errors.New("sandbox capability generation or validity interval is invalid")
	}
	switch claims.Audience {
	case AudienceLifecycle:
		if !validText(claims.HolderID, maximumTextBytes) || claims.ExecutionID != "" || claims.OperationID != "" || claims.MutationKey != "" {
			return errors.New("sandbox lifecycle capability holder or operation projection is invalid")
		}
		switch claims.Action {
		case "ensure":
			if claims.SandboxID != "" || claims.TargetGeneration != 0 {
				return errors.New("sandbox ensure capability must not preselect a target")
			}
		case "renew_activity", "release_activity", "delete", "get", "set_timeout":
			if !validText(claims.SandboxID, maximumTextBytes) || claims.TargetGeneration < 1 {
				return errors.New("sandbox lifecycle target binding is invalid")
			}
		default:
			return errors.New("sandbox lifecycle capability action is unsupported")
		}
	case AudienceBackend:
		if claims.HolderID != "" || !validText(claims.ExecutionID, maximumTextBytes) ||
			!validText(claims.OperationID, maximumTextBytes) || !validText(claims.MutationKey, maximumTextBytes) ||
			!validText(claims.SandboxID, maximumTextBytes) || claims.TargetGeneration < 1 {
			return errors.New("sandbox backend capability operation target binding is invalid")
		}
		switch claims.Action {
		case "run_command", "signal_command", "read_file":
		default:
			return errors.New("sandbox backend capability action is unsupported")
		}
	default:
		return errors.New("sandbox capability audience is unsupported")
	}
	return nil
}

func signatureMessage(keyID string, canonical []byte) []byte {
	message := make([]byte, 0, len(signatureDomain)+2+len(keyID)+len(canonical))
	message = append(message, signatureDomain...)
	var size [2]byte
	binary.BigEndian.PutUint16(size[:], uint16(len(keyID)))
	message = append(message, size[:]...)
	message = append(message, keyID...)
	message = append(message, canonical...)
	return message
}

func decodeBase64(label, encoded string, maximum int) ([]byte, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || encoded == "" || len(decoded) > maximum || base64.RawURLEncoding.EncodeToString(decoded) != encoded {
		return nil, fmt.Errorf("sandbox capability %s is not canonical bounded base64url", label)
	}
	return decoded, nil
}

func validAudience(value string) bool {
	return value == AudienceLifecycle || value == AudienceBackend
}

func validText(value string, maximum int) bool {
	if value == "" || len(value) > maximum || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func finishJSON(decoder *json.Decoder) error {
	var trailing any
	err := decoder.Decode(&trailing)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("additional JSON value")
	}
	return err
}
