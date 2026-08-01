package runcapability

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
	ProductionVersion            = 1
	ProductionSignatureAlgorithm = "ed25519-v1"

	productionTokenPrefix = "asv2cap1"
	productionTokenDomain = "agentserver-v2/production-run-capability/ed25519-v1\x00"
	maximumTrustedKeys    = 32
	maximumIssuerBytes    = 512
	maximumKeyIDBytes     = 256
)

// ProductionSigner is the Core-owned asymmetric capability signer. The
// private key must never be present in harness-pool, gateway, worker or
// llmproxy. Tokens carry an exact key ID so verifier rotation never falls back
// across keys.
type ProductionSigner struct {
	issuer     string
	keyID      string
	privateKey ed25519.PrivateKey
}

func NewProductionSigner(issuer, keyID string, privateKey ed25519.PrivateKey) (*ProductionSigner, error) {
	if !validProductionText(issuer, maximumIssuerBytes) {
		return nil, errors.New("production run capability issuer is required and must be canonical bounded text")
	}
	if !validProductionText(keyID, maximumKeyIDBytes) {
		return nil, errors.New("production run capability key ID is required and must be canonical bounded text")
	}
	if len(privateKey) != ed25519.PrivateKeySize {
		return nil, errors.New("production run capability private key must be an Ed25519 private key")
	}
	seed := privateKey.Seed()
	defer clear(seed)
	var zero [ed25519.SeedSize]byte
	if subtle.ConstantTimeCompare(seed, zero[:]) == 1 {
		return nil, errors.New("production run capability private key seed must not be all zero")
	}
	canonical := ed25519.NewKeyFromSeed(seed)
	if subtle.ConstantTimeCompare(privateKey, canonical) != 1 {
		clear(canonical)
		return nil, errors.New("production run capability private key is not canonical")
	}
	return &ProductionSigner{
		issuer: issuer, keyID: keyID, privateKey: canonical,
	}, nil
}

func (signer *ProductionSigner) Sign(claims Claims) (string, error) {
	if signer == nil || signer.privateKey == nil || signer.issuer == "" || signer.keyID == "" {
		return "", errors.New("production run capability signer is required")
	}
	if claims.Version != ProductionVersion {
		return "", fmt.Errorf("production run capability version must be %d", ProductionVersion)
	}
	if claims.Issuer != signer.issuer {
		return "", errors.New("production run capability issuer does not match signer authority")
	}
	if err := claims.validateProduction(); err != nil {
		return "", err
	}
	raw, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("encode production run capability claims: %w", err)
	}
	_, canonical, err := decodeCanonicalClaims(raw)
	if err != nil {
		return "", fmt.Errorf("canonicalize production run capability claims: %w", err)
	}
	signature := ed25519.Sign(signer.privateKey, productionSignatureMessage(signer.keyID, canonical))
	token := strings.Join([]string{
		productionTokenPrefix,
		base64.RawURLEncoding.EncodeToString([]byte(signer.keyID)),
		base64.RawURLEncoding.EncodeToString(canonical),
		base64.RawURLEncoding.EncodeToString(signature),
	}, ".")
	if len(token) > maximumTokenBytes {
		return "", errors.New("production run capability token exceeds its size bound")
	}
	return token, nil
}

// ProductionVerifier is the gateway/llmproxy public-key boundary. Signature
// validation is necessary but not sufficient for request authorization: each
// consumer must additionally check current Core lease/generation/RBAC state.
type ProductionVerifier struct {
	issuer string
	keys   map[string]ed25519.PublicKey
}

func NewProductionVerifier(issuer string, keys map[string]ed25519.PublicKey) (*ProductionVerifier, error) {
	if !validProductionText(issuer, maximumIssuerBytes) {
		return nil, errors.New("production run capability verifier issuer is required and must be canonical bounded text")
	}
	if len(keys) < 1 || len(keys) > maximumTrustedKeys {
		return nil, fmt.Errorf("production run capability verifier must contain between 1 and %d keys", maximumTrustedKeys)
	}
	copied := make(map[string]ed25519.PublicKey, len(keys))
	for keyID, publicKey := range keys {
		if !validProductionText(keyID, maximumKeyIDBytes) {
			return nil, errors.New("production run capability verifier key ID is invalid")
		}
		if len(publicKey) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("production run capability verifier key %q is not Ed25519", keyID)
		}
		var zero [ed25519.PublicKeySize]byte
		if subtle.ConstantTimeCompare(publicKey, zero[:]) == 1 {
			return nil, fmt.Errorf("production run capability verifier key %q must not be all zero", keyID)
		}
		copied[keyID] = append(ed25519.PublicKey(nil), publicKey...)
	}
	return &ProductionVerifier{issuer: issuer, keys: copied}, nil
}

func (verifier *ProductionVerifier) Verify(token, expectedAudience string, now time.Time) (Claims, error) {
	if verifier == nil || verifier.issuer == "" || len(verifier.keys) == 0 {
		return Claims{}, errors.New("production run capability verifier is required")
	}
	if token == "" || len(token) > maximumTokenBytes || strings.TrimSpace(token) != token {
		return Claims{}, errors.New("production run capability token is empty, padded, or too large")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 4 || parts[0] != productionTokenPrefix {
		return Claims{}, errors.New("production run capability token prefix or framing is invalid")
	}
	keyIDBytes, err := decodeProductionBase64("key ID", parts[1], maximumKeyIDBytes)
	if err != nil || !validProductionText(string(keyIDBytes), maximumKeyIDBytes) {
		return Claims{}, errors.New("production run capability key ID is invalid")
	}
	keyID := string(keyIDBytes)
	publicKey, trusted := verifier.keys[keyID]
	if !trusted {
		return Claims{}, errors.New("production run capability signing key is not trusted")
	}
	canonical, err := decodeProductionBase64("claims", parts[2], maximumClaimsBytes)
	if err != nil {
		return Claims{}, err
	}
	signature, err := decodeProductionBase64("signature", parts[3], ed25519.SignatureSize)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return Claims{}, errors.New("production run capability signature is invalid")
	}
	if !ed25519.Verify(publicKey, productionSignatureMessage(keyID, canonical), signature) {
		return Claims{}, errors.New("production run capability signature verification failed")
	}
	_, normalized, err := decodeCanonicalClaims(canonical)
	if err != nil {
		return Claims{}, fmt.Errorf("validate production run capability JSON: %w", err)
	}
	if !bytes.Equal(canonical, normalized) {
		return Claims{}, errors.New("production run capability claims are not canonical JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	var claims Claims
	if err := decoder.Decode(&claims); err != nil {
		return Claims{}, fmt.Errorf("decode production run capability claims: %w", err)
	}
	if err := finishProductionJSON(decoder); err != nil {
		return Claims{}, fmt.Errorf("finish production run capability claims: %w", err)
	}
	if err := claims.validateProduction(); err != nil {
		return Claims{}, err
	}
	if claims.Issuer != verifier.issuer {
		return Claims{}, errors.New("production run capability issuer is not trusted")
	}
	if expectedAudience == "" || claims.Audience != expectedAudience {
		return Claims{}, errors.New("production run capability audience does not match")
	}
	nowMS := now.UnixMilli()
	if now.IsZero() || nowMS < claims.IssuedAtUnixMS || nowMS >= claims.ExpiresAtUnixMS {
		return Claims{}, errors.New("production run capability is not currently valid")
	}
	return claims, nil
}

func (claims Claims) validateProduction() error {
	if claims.Version != ProductionVersion {
		return fmt.Errorf("production run capability version must be %d", ProductionVersion)
	}
	if !validProductionText(claims.Issuer, maximumIssuerBytes) {
		return errors.New("production run capability issuer is invalid")
	}
	if err := claims.validateAuthority("production"); err != nil {
		return err
	}
	for field, value := range map[string]string{"actorId": claims.ActorID, "holderId": claims.HolderID} {
		if !validProductionText(value, maximumTextBytes) {
			return fmt.Errorf("production run capability %s contains non-canonical text", field)
		}
	}
	if claims.Audience == AudienceLLMProxy {
		for field, value := range map[string]string{"model": claims.Model, "provider": claims.Provider} {
			if !validProductionText(value, maximumTextBytes) {
				return fmt.Errorf("production run capability %s contains non-canonical text", field)
			}
		}
	}
	return nil
}

func productionSignatureMessage(keyID string, canonical []byte) []byte {
	message := make([]byte, 0, len(productionTokenDomain)+2+len(keyID)+len(canonical))
	message = append(message, productionTokenDomain...)
	var length [2]byte
	binary.BigEndian.PutUint16(length[:], uint16(len(keyID)))
	message = append(message, length[:]...)
	message = append(message, keyID...)
	message = append(message, canonical...)
	return message
}

func decodeProductionBase64(field, encoded string, maximum int) ([]byte, error) {
	if encoded == "" {
		return nil, fmt.Errorf("production run capability %s is empty", field)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(decoded) > maximum || base64.RawURLEncoding.EncodeToString(decoded) != encoded {
		return nil, fmt.Errorf("production run capability %s is not canonical bounded base64url", field)
	}
	return decoded, nil
}

func validProductionText(value string, maximum int) bool {
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

func finishProductionJSON(decoder *json.Decoder) error {
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
