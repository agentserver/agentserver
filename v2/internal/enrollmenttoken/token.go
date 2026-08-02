// Package enrollmenttoken defines the short-lived, one-resource enrollment
// authority used before an executor has an OAuth machine identity.
package enrollmenttoken

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
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
	Version        = 1
	Prefix         = "asv2enr1"
	MaximumTTL     = 15 * time.Minute
	maximumToken   = 8192
	maximumClaims  = 4096
	maximumIssuer  = 512
	maximumSafeInt = int64(1<<53 - 1)
	tokenDomain    = "agentserver-v2/executor-enrollment-token/hmac-sha256-v1\x00"
)

var canonicalUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// Claims is deliberately closed-world. The issuing actor is audit authority;
// the bearer can enroll only the one workspace/executor pair named here.
type Claims struct {
	Version         int    `json:"v"`
	Issuer          string `json:"iss"`
	TokenID         string `json:"jti"`
	WorkspaceID     string `json:"workspace_id"`
	ExecutorID      string `json:"executor_id"`
	IssuedByActorID string `json:"issued_by"`
	IssuedAtUnixMS  int64  `json:"iat_ms"`
	ExpiresAtUnixMS int64  `json:"exp_ms"`
}

type Codec struct {
	issuer string
	key    [sha256.Size]byte
}

func New(issuer string, key []byte) (*Codec, error) {
	if !validText(issuer, maximumIssuer) {
		return nil, errors.New("executor enrollment token issuer must be canonical bounded text")
	}
	if len(key) != sha256.Size {
		return nil, errors.New("executor enrollment token key must contain exactly 256 bits")
	}
	var zero [sha256.Size]byte
	if subtle.ConstantTimeCompare(key, zero[:]) == 1 {
		return nil, errors.New("executor enrollment token key must not be all zero")
	}
	codec := &Codec{issuer: issuer}
	copy(codec.key[:], key)
	return codec, nil
}

func (codec *Codec) Issuer() string {
	if codec == nil {
		return ""
	}
	return codec.issuer
}

func (codec *Codec) Sign(claims Claims) (string, error) {
	if codec == nil || codec.issuer == "" || codec.key == [sha256.Size]byte{} {
		return "", errors.New("executor enrollment token codec is unavailable")
	}
	if claims.Issuer != codec.issuer {
		return "", errors.New("executor enrollment token issuer does not match codec")
	}
	if err := validateClaims(claims); err != nil {
		return "", err
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("encode executor enrollment token claims: %w", err)
	}
	mac := hmac.New(sha256.New, codec.key[:])
	_, _ = mac.Write([]byte(tokenDomain))
	_, _ = mac.Write(payload)
	token := Prefix + "." + base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if len(token) > maximumToken {
		return "", errors.New("executor enrollment token exceeds its size bound")
	}
	return token, nil
}

func (codec *Codec) Verify(token string, now time.Time) (Claims, error) {
	if codec == nil || codec.issuer == "" || codec.key == [sha256.Size]byte{} {
		return Claims{}, errors.New("executor enrollment token codec is unavailable")
	}
	if token == "" || len(token) > maximumToken || strings.TrimSpace(token) != token || strings.ContainsAny(token, "\x00\r\n") {
		return Claims{}, errors.New("executor enrollment token is empty, padded, or outside bounds")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != Prefix {
		return Claims{}, errors.New("executor enrollment token framing is invalid")
	}
	payload, err := decodeBase64(parts[1], maximumClaims)
	if err != nil {
		return Claims{}, errors.New("executor enrollment token claims are not canonical base64url")
	}
	signature, err := decodeBase64(parts[2], sha256.Size)
	if err != nil || len(signature) != sha256.Size {
		return Claims{}, errors.New("executor enrollment token MAC is invalid")
	}
	mac := hmac.New(sha256.New, codec.key[:])
	_, _ = mac.Write([]byte(tokenDomain))
	_, _ = mac.Write(payload)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return Claims{}, errors.New("executor enrollment token MAC verification failed")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var claims Claims
	if err := decoder.Decode(&claims); err != nil {
		return Claims{}, fmt.Errorf("decode executor enrollment token claims: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Claims{}, errors.New("executor enrollment token claims contain trailing JSON")
	}
	canonical, err := json.Marshal(claims)
	if err != nil || !bytes.Equal(payload, canonical) {
		return Claims{}, errors.New("executor enrollment token claims are not canonical closed-world JSON")
	}
	if err := validateClaims(claims); err != nil {
		return Claims{}, err
	}
	if claims.Issuer != codec.issuer {
		return Claims{}, errors.New("executor enrollment token issuer is not trusted")
	}
	if now.IsZero() || now.UnixMilli() < claims.IssuedAtUnixMS || now.UnixMilli() >= claims.ExpiresAtUnixMS {
		return Claims{}, errors.New("executor enrollment token is not currently valid")
	}
	return claims, nil
}

func Fingerprint(token string) [sha256.Size]byte {
	return sha256.Sum256([]byte(token))
}

func validateClaims(claims Claims) error {
	if claims.Version != Version {
		return fmt.Errorf("executor enrollment token version must be %d", Version)
	}
	if !validText(claims.Issuer, maximumIssuer) {
		return errors.New("executor enrollment token issuer is invalid")
	}
	for field, value := range map[string]string{
		"token ID": claims.TokenID, "workspace ID": claims.WorkspaceID,
		"executor ID": claims.ExecutorID, "issuing actor ID": claims.IssuedByActorID,
	} {
		if value == "00000000-0000-0000-0000-000000000000" || !canonicalUUID.MatchString(value) {
			return fmt.Errorf("executor enrollment token %s must be a non-zero canonical UUID", field)
		}
	}
	if claims.IssuedAtUnixMS < 1 || claims.ExpiresAtUnixMS < 1 ||
		claims.IssuedAtUnixMS > maximumSafeInt || claims.ExpiresAtUnixMS > maximumSafeInt ||
		claims.ExpiresAtUnixMS <= claims.IssuedAtUnixMS ||
		claims.ExpiresAtUnixMS-claims.IssuedAtUnixMS > MaximumTTL.Milliseconds() {
		return errors.New("executor enrollment token time window is invalid")
	}
	return nil
}

func decodeBase64(encoded string, maximum int) ([]byte, error) {
	if encoded == "" {
		return nil, errors.New("empty base64url")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(decoded) > maximum || base64.RawURLEncoding.EncodeToString(decoded) != encoded {
		return nil, errors.New("non-canonical base64url")
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
