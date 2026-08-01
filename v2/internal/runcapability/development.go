// Package runcapability contains the explicit insecure-development run token
// used to connect a dynamically claiming harness-pool to local gateway
// processes. Production capability issuance remains a separate asymmetric,
// online-revocable boundary.
package runcapability

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
	"unicode/utf8"

	"github.com/agentserver/agentserver/v2/internal/braincatalog"
)

const (
	DevelopmentVersion = 1

	AudienceExecutorMCP = "executor-mcp"
	AudienceLLMProxy    = "llmproxy"

	developmentTokenPrefix = "asv2dev1"
	developmentTokenDomain = "agentserver-v2/insecure-development-run-capability/hmac-sha256-v1\x00"
	maximumTokenBytes      = 32 * 1024
	maximumClaimsBytes     = 16 * 1024
	maximumTextBytes       = 256
	maxSafeJSONInteger     = int64(1<<53 - 1)
	maximumApprovalTTLMS   = int64(24 * time.Hour / time.Millisecond)
)

var (
	developmentUUIDPattern   = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	developmentDigestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type Claims struct {
	Version              int    `json:"version"`
	CapabilityID         string `json:"capabilityId"`
	Audience             string `json:"audience"`
	WorkspaceID          string `json:"workspaceId"`
	SessionID            string `json:"sessionId"`
	RunID                string `json:"runId"`
	RunAttemptID         string `json:"runAttemptId"`
	RunAttemptGeneration int64  `json:"runAttemptGeneration"`
	ActorID              string `json:"actorId"`
	HolderID             string `json:"holderId"`
	IssuedAtUnixMS       int64  `json:"issuedAtUnixMs"`
	RunDeadlineUnixMS    int64  `json:"runDeadlineUnixMs"`
	ExpiresAtUnixMS      int64  `json:"expiresAtUnixMs"`

	ExecutorID                string `json:"executorId,omitempty"`
	ToolCatalogDigest         string `json:"toolCatalogDigest,omitempty"`
	ExpectedRunVersion        int64  `json:"expectedRunVersion,omitempty"`
	ExpectedRunAttemptVersion int64  `json:"expectedRunAttemptVersion,omitempty"`
	MaxApprovalTTLMillis      int64  `json:"maxApprovalTtlMs,omitempty"`

	Model    string `json:"model,omitempty"`
	Provider string `json:"provider,omitempty"`
}

// DevelopmentCodec uses one shared HMAC key and therefore must never be
// accepted by a production serve mode. It exists so local components can
// exercise dynamic per-attempt authority without a static run-specific env.
type DevelopmentCodec struct {
	key [sha256.Size]byte
}

func NewDevelopmentCodec(key []byte) (*DevelopmentCodec, error) {
	if len(key) != sha256.Size {
		return nil, errors.New("development run capability key must contain exactly 32 bytes")
	}
	codec := &DevelopmentCodec{}
	copy(codec.key[:], key)
	return codec, nil
}

func NewDevelopmentCodecFromBase64Key(encoded string) (*DevelopmentCodec, error) {
	key, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(key) != sha256.Size || base64.RawURLEncoding.EncodeToString(key) != encoded {
		return nil, errors.New("development run capability key must be canonical 256-bit base64url")
	}
	defer clear(key)
	return NewDevelopmentCodec(key)
}

func (codec *DevelopmentCodec) Sign(claims Claims) (string, error) {
	if codec == nil {
		return "", errors.New("development run capability codec is required")
	}
	if err := claims.Validate(); err != nil {
		return "", err
	}
	raw, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("encode development run capability claims: %w", err)
	}
	value, canonical, err := decodeCanonicalClaims(raw)
	if err != nil {
		return "", err
	}
	_ = value
	signature := codec.signature(canonical)
	token := developmentTokenPrefix + "." + base64.RawURLEncoding.EncodeToString(canonical) + "." + base64.RawURLEncoding.EncodeToString(signature)
	if len(token) > maximumTokenBytes {
		return "", errors.New("development run capability token exceeds its size bound")
	}
	return token, nil
}

func (codec *DevelopmentCodec) Verify(token, expectedAudience string, now time.Time) (Claims, error) {
	if codec == nil {
		return Claims{}, errors.New("development run capability codec is required")
	}
	if token == "" || len(token) > maximumTokenBytes || strings.TrimSpace(token) != token {
		return Claims{}, errors.New("development run capability token is empty, padded, or too large")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != developmentTokenPrefix {
		return Claims{}, errors.New("development run capability token prefix or framing is invalid")
	}
	canonical, err := decodeCanonicalBase64("claims", parts[1], maximumClaimsBytes)
	if err != nil {
		return Claims{}, err
	}
	signature, err := decodeCanonicalBase64("signature", parts[2], sha256.Size)
	if err != nil || len(signature) != sha256.Size {
		return Claims{}, errors.New("development run capability signature is invalid")
	}
	want := codec.signature(canonical)
	if subtle.ConstantTimeCompare(signature, want) != 1 {
		return Claims{}, errors.New("development run capability signature does not match")
	}
	_, normalized, err := decodeCanonicalClaims(canonical)
	if err != nil {
		return Claims{}, err
	}
	if !bytes.Equal(canonical, normalized) {
		return Claims{}, errors.New("development run capability claims are not canonical JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	var claims Claims
	if err := decoder.Decode(&claims); err != nil {
		return Claims{}, fmt.Errorf("decode development run capability claims: %w", err)
	}
	if err := finishClaimsJSON(decoder); err != nil {
		return Claims{}, fmt.Errorf("finish development run capability claims: %w", err)
	}
	if err := claims.Validate(); err != nil {
		return Claims{}, err
	}
	if expectedAudience == "" || claims.Audience != expectedAudience {
		return Claims{}, errors.New("development run capability audience does not match")
	}
	nowMS := now.UnixMilli()
	if now.IsZero() || nowMS < claims.IssuedAtUnixMS || nowMS >= claims.ExpiresAtUnixMS {
		return Claims{}, errors.New("development run capability is not currently valid")
	}
	return claims, nil
}

func (claims Claims) Validate() error {
	if claims.Version != DevelopmentVersion {
		return fmt.Errorf("development run capability version must be %d", DevelopmentVersion)
	}
	for field, value := range map[string]string{
		"capabilityId": claims.CapabilityID, "workspaceId": claims.WorkspaceID,
		"sessionId": claims.SessionID, "runId": claims.RunID,
		"runAttemptId": claims.RunAttemptID,
	} {
		if !validDevelopmentUUID(value) {
			return fmt.Errorf("development run capability %s must be a non-zero canonical lowercase UUID", field)
		}
	}
	for field, value := range map[string]string{"actorId": claims.ActorID, "holderId": claims.HolderID} {
		if !validDevelopmentText(value, maximumTextBytes) {
			return fmt.Errorf("development run capability %s is invalid", field)
		}
	}
	if claims.RunAttemptGeneration < 1 || claims.RunAttemptGeneration > maxSafeJSONInteger {
		return errors.New("development run capability generation must be a positive safe integer")
	}
	if claims.IssuedAtUnixMS < 1 || claims.IssuedAtUnixMS > maxSafeJSONInteger ||
		claims.RunDeadlineUnixMS <= claims.IssuedAtUnixMS || claims.RunDeadlineUnixMS > maxSafeJSONInteger ||
		claims.ExpiresAtUnixMS < claims.RunDeadlineUnixMS || claims.ExpiresAtUnixMS > maxSafeJSONInteger {
		return errors.New("development run capability time bounds are invalid")
	}
	switch claims.Audience {
	case AudienceExecutorMCP:
		if !validDevelopmentUUID(claims.ExecutorID) || !developmentDigestPattern.MatchString(claims.ToolCatalogDigest) ||
			claims.ExpectedRunVersion < 1 || claims.ExpectedRunVersion > maxSafeJSONInteger ||
			claims.ExpectedRunAttemptVersion < 1 || claims.ExpectedRunAttemptVersion > maxSafeJSONInteger ||
			claims.MaxApprovalTTLMillis < 1 || claims.MaxApprovalTTLMillis > maximumApprovalTTLMS {
			return errors.New("development executor capability authority is invalid")
		}
		if claims.Model != "" || claims.Provider != "" {
			return errors.New("development executor capability contains model authority")
		}
	case AudienceLLMProxy:
		if !validDevelopmentText(claims.Model, maximumTextBytes) || !validDevelopmentText(claims.Provider, maximumTextBytes) {
			return errors.New("development model capability route is invalid")
		}
		if claims.ExecutorID != "" || claims.ToolCatalogDigest != "" ||
			claims.ExpectedRunVersion != 0 || claims.ExpectedRunAttemptVersion != 0 || claims.MaxApprovalTTLMillis != 0 {
			return errors.New("development model capability contains executor authority")
		}
	default:
		return errors.New("development run capability audience is unsupported")
	}
	return nil
}

func (codec *DevelopmentCodec) signature(canonical []byte) []byte {
	hasher := hmac.New(sha256.New, codec.key[:])
	_, _ = hasher.Write([]byte(developmentTokenDomain))
	_, _ = hasher.Write(canonical)
	return hasher.Sum(nil)
}

func decodeCanonicalClaims(raw []byte) (any, []byte, error) {
	limits := braincatalog.DefaultLimits()
	limits.MaxCatalogBytes = maximumClaimsBytes
	limits.MaxSchemaBytes = maximumClaimsBytes
	limits.MaxJSONValues = 64
	limits.MaxJSONDepth = 4
	value, canonical, err := braincatalog.DecodeCanonicalJSON(raw, maximumClaimsBytes, limits)
	if err != nil {
		return nil, nil, fmt.Errorf("validate development run capability JSON: %w", err)
	}
	if _, ok := value.(map[string]any); !ok {
		return nil, nil, errors.New("development run capability claims root must be an object")
	}
	return value, canonical, nil
}

func decodeCanonicalBase64(field, encoded string, maximum int) ([]byte, error) {
	if encoded == "" {
		return nil, fmt.Errorf("development run capability %s is empty", field)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(decoded) > maximum || base64.RawURLEncoding.EncodeToString(decoded) != encoded {
		return nil, fmt.Errorf("development run capability %s is not canonical bounded base64url", field)
	}
	return decoded, nil
}

func validDevelopmentUUID(value string) bool {
	return value != "00000000-0000-0000-0000-000000000000" && developmentUUIDPattern.MatchString(value)
}

func validDevelopmentText(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && utf8.ValidString(value) && !strings.ContainsRune(value, 0)
}

func finishClaimsJSON(decoder *json.Decoder) error {
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
