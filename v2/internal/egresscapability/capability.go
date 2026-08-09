// Package egresscapability implements the short-lived placeholder carried by
// one managed lark-cli process. A placeholder is request authority for the
// egress authorizer; it is never a third-party credential.
package egresscapability

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
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	Version                  = 1
	AudienceLarkReadOnly     = "tae-egress/lark-readonly"
	AudienceCredentialPrefix = "tae-egress/credential/"
	SignatureAlgorithm       = "ed25519-v1"
	PackLarkReadOnly         = "lark-readonly@v1"

	tokenPrefix        = "asv2egress1"
	signatureDomain    = "agentserver-v2/egress-placeholder/ed25519-v1\x00"
	maximumTokenBytes  = 32 * 1024
	maximumClaimsBytes = 24 * 1024
	maximumTextBytes   = 2048
	maximumLifetime    = 2 * time.Minute
)

var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type Claims struct {
	Version      int    `json:"version"`
	Issuer       string `json:"issuer"`
	Audience     string `json:"audience"`
	CapabilityID string `json:"capabilityId"`

	WorkspaceID          string `json:"workspaceId"`
	SessionID            string `json:"sessionId"`
	ActorID              string `json:"actorId"`
	EnvironmentID        string `json:"environmentId"`
	RunID                string `json:"runId"`
	RunAttemptID         string `json:"runAttemptId"`
	RunAttemptGeneration int64  `json:"runAttemptGeneration"`
	ExecutionID          string `json:"executionId"`
	OperationID          string `json:"operationId"`
	SandboxID            string `json:"sandboxId"`
	TargetGeneration     int64  `json:"targetGeneration"`

	PackID           string `json:"packId"`
	GrantID          string `json:"grantId"`
	GrantVersion     int64  `json:"grantVersion"`
	ProviderKind     string `json:"providerKind,omitempty"`
	BindingID        string `json:"bindingId,omitempty"`
	AuthorityVersion int64  `json:"authorityVersion,omitempty"`
	PolicySHA256     string `json:"policySha256"`
	Executable       string `json:"executable"`

	IssuedAtUnixMS  int64 `json:"issuedAtUnixMs"`
	ExpiresAtUnixMS int64 `json:"expiresAtUnixMs"`
}

func (claims Claims) Validate() error {
	if claims.Version != Version ||
		!validText(claims.Issuer, 512) || !validText(claims.CapabilityID, maximumTextBytes) {
		return errors.New("egress placeholder version or authority is invalid")
	}
	for name, value := range map[string]string{
		"workspaceId": claims.WorkspaceID, "sessionId": claims.SessionID,
		"actorId": claims.ActorID, "environmentId": claims.EnvironmentID,
		"runId": claims.RunID, "runAttemptId": claims.RunAttemptID,
		"executionId": claims.ExecutionID, "operationId": claims.OperationID,
		"sandboxId": claims.SandboxID,
	} {
		if !validText(value, maximumTextBytes) {
			return fmt.Errorf("egress placeholder %s is invalid", name)
		}
	}
	if claims.RunAttemptGeneration < 1 || claims.TargetGeneration < 1 ||
		!validText(claims.PackID, maximumTextBytes) || !validText(claims.Executable, maximumTextBytes) ||
		!digestPattern.MatchString(claims.PolicySHA256) {
		return errors.New("egress placeholder target, pack, executable, or policy binding is invalid")
	}
	switch {
	case claims.Audience == AudienceLarkReadOnly:
		if !validText(claims.GrantID, maximumTextBytes) || claims.GrantVersion < 1 ||
			claims.PackID != PackLarkReadOnly || claims.Executable != "lark-cli" ||
			claims.ProviderKind != "" || claims.BindingID != "" || claims.AuthorityVersion != 0 {
			return errors.New("legacy Lark egress placeholder authority is invalid")
		}
	case strings.HasPrefix(claims.Audience, AudienceCredentialPrefix):
		if !validProviderKind(claims.ProviderKind) || claims.Audience != AudienceForProvider(claims.ProviderKind) ||
			!validText(claims.BindingID, maximumTextBytes) || claims.AuthorityVersion < 1 ||
			claims.GrantID != "" || claims.GrantVersion != 0 {
			return errors.New("provider credential egress placeholder authority is invalid")
		}
	default:
		return errors.New("egress placeholder audience is invalid")
	}
	if claims.IssuedAtUnixMS < 1 || claims.ExpiresAtUnixMS <= claims.IssuedAtUnixMS ||
		time.Duration(claims.ExpiresAtUnixMS-claims.IssuedAtUnixMS)*time.Millisecond > maximumLifetime {
		return errors.New("egress placeholder validity interval is invalid")
	}
	return nil
}

func AudienceForProvider(kind string) string {
	return AudienceCredentialPrefix + kind
}

func validProviderKind(kind string) bool {
	if kind == "" || len(kind) > 128 {
		return false
	}
	for index, character := range kind {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') {
			continue
		}
		if index > 0 && (character == '.' || character == '_' || character == ':' || character == '-') {
			continue
		}
		return false
	}
	return true
}

type Signer struct {
	issuer     string
	keyID      string
	privateKey ed25519.PrivateKey
}

func NewSigner(issuer, keyID string, privateKey ed25519.PrivateKey) (*Signer, error) {
	if !validText(issuer, 512) || !validText(keyID, 256) || len(privateKey) != ed25519.PrivateKeySize {
		return nil, errors.New("egress placeholder signer authority or key is invalid")
	}
	seed := privateKey.Seed()
	defer clear(seed)
	var zero [ed25519.SeedSize]byte
	if subtle.ConstantTimeCompare(seed, zero[:]) == 1 {
		return nil, errors.New("egress placeholder private key must not be all zero")
	}
	canonical := ed25519.NewKeyFromSeed(seed)
	if subtle.ConstantTimeCompare(privateKey, canonical) != 1 {
		clear(canonical)
		return nil, errors.New("egress placeholder private key is not canonical")
	}
	return &Signer{issuer: issuer, keyID: keyID, privateKey: canonical}, nil
}

func (signer *Signer) Issuer() string {
	if signer == nil {
		return ""
	}
	return signer.issuer
}

func (signer *Signer) Sign(claims Claims) (string, error) {
	if signer == nil || len(signer.privateKey) != ed25519.PrivateKeySize || claims.Issuer != signer.issuer {
		return "", errors.New("egress placeholder signer does not match the claims")
	}
	if err := claims.Validate(); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	signature := ed25519.Sign(signer.privateKey, signatureMessage(signer.keyID, canonical))
	token := strings.Join([]string{
		tokenPrefix,
		base64.RawURLEncoding.EncodeToString([]byte(signer.keyID)),
		base64.RawURLEncoding.EncodeToString(canonical),
		base64.RawURLEncoding.EncodeToString(signature),
	}, ".")
	if len(token) > maximumTokenBytes {
		return "", errors.New("egress placeholder exceeds its size limit")
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
		return nil, errors.New("egress placeholder verifier must contain between 1 and 64 keys")
	}
	trusted := make(map[string]TrustedKey, len(keys))
	for index, key := range keys {
		if key.Audience == "" {
			key.Audience = AudienceLarkReadOnly
		}
		if !validText(key.Issuer, 512) || !validText(key.KeyID, 256) || len(key.PublicKey) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("egress placeholder verifier key %d is invalid", index)
		}
		if key.Audience != AudienceLarkReadOnly && !strings.HasPrefix(key.Audience, AudienceCredentialPrefix) {
			return nil, fmt.Errorf("egress placeholder verifier key %d audience is invalid", index)
		}
		if _, duplicate := trusted[key.KeyID]; duplicate {
			return nil, fmt.Errorf("egress placeholder verifier repeats key ID %q", key.KeyID)
		}
		copy := key
		copy.PublicKey = append(ed25519.PublicKey(nil), key.PublicKey...)
		trusted[key.KeyID] = copy
	}
	return &Verifier{keys: trusted}, nil
}

func (verifier *Verifier) Verify(token string, now time.Time) (Claims, error) {
	if verifier == nil || token == "" || len(token) > maximumTokenBytes || strings.TrimSpace(token) != token {
		return Claims{}, errors.New("egress placeholder verifier or token is invalid")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 4 || parts[0] != tokenPrefix {
		return Claims{}, errors.New("egress placeholder framing is invalid")
	}
	keyID, err := decodePart(parts[1], 256)
	if err != nil || !validText(string(keyID), 256) {
		return Claims{}, errors.New("egress placeholder key ID is invalid")
	}
	key, found := verifier.keys[string(keyID)]
	if !found {
		return Claims{}, errors.New("egress placeholder signing key is not trusted")
	}
	canonical, err := decodePart(parts[2], maximumClaimsBytes)
	if err != nil {
		return Claims{}, err
	}
	signature, err := decodePart(parts[3], ed25519.SignatureSize)
	if err != nil || len(signature) != ed25519.SignatureSize ||
		!ed25519.Verify(key.PublicKey, signatureMessage(key.KeyID, canonical), signature) {
		return Claims{}, errors.New("egress placeholder signature verification failed")
	}
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	var claims Claims
	if err := decoder.Decode(&claims); err != nil {
		return Claims{}, err
	}
	if err := finishJSON(decoder); err != nil {
		return Claims{}, err
	}
	normalized, err := json.Marshal(claims)
	if err != nil || !bytes.Equal(canonical, normalized) || claims.Issuer != key.Issuer {
		return Claims{}, errors.New("egress placeholder claims are not canonical or trusted")
	}
	if err := claims.Validate(); err != nil {
		return Claims{}, err
	}
	if claims.Audience != key.Audience {
		return Claims{}, errors.New("egress placeholder audience is not trusted by the signing key")
	}
	if now.IsZero() || now.UnixMilli() < claims.IssuedAtUnixMS || now.UnixMilli() >= claims.ExpiresAtUnixMS {
		return Claims{}, errors.New("egress placeholder is not currently valid")
	}
	return claims, nil
}

func signatureMessage(keyID string, claims []byte) []byte {
	message := make([]byte, 0, len(signatureDomain)+2+len(keyID)+len(claims))
	message = append(message, signatureDomain...)
	var size [2]byte
	binary.BigEndian.PutUint16(size[:], uint16(len(keyID)))
	message = append(message, size[:]...)
	message = append(message, keyID...)
	message = append(message, claims...)
	return message
}

func decodePart(encoded string, maximum int) ([]byte, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || encoded == "" || len(decoded) > maximum || base64.RawURLEncoding.EncodeToString(decoded) != encoded {
		return nil, errors.New("egress placeholder contains invalid canonical base64url")
	}
	return decoded, nil
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
