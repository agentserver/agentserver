// Package checkpoint defines the deterministic, rollout-only native Codex
// checkpoint format used between harness workers and harness-pool.
package checkpoint

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/agentserver/agentserver/v2/internal/braincatalog"
	"github.com/ucarion/jcs"
)

const (
	CurrentManifestVersion = 1
	Canonicalizer          = "rfc8785-v1"
	ArtifactMediaType      = "application/vnd.agentserver.codex-checkpoint.v1"
	RolloutPurpose         = "codex-rollout"
	RegularFileType        = "regular"
	RolloutMode            = 0o600

	MaximumManifestBytes    = 64 * 1024
	MaximumRolloutBytes     = int64(64 * 1024 * 1024)
	MaximumRolloutLineBytes = 4 * 1024 * 1024
	maxSafeJSONInteger      = int64(1<<53 - 1)

	manifestDigestDomain = "agentserver-v2/checkpoint-manifest/rfc8785-v1\x00"
)

var (
	uuidPattern   = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Manifest binds a completed-turn rollout to the exact attempt and runtime
// facts that produced it. Files must contain exactly one regular rollout.
type Manifest struct {
	ManifestVersion            int    `json:"manifestVersion"`
	CanonicalizerVersion       string `json:"canonicalizerVersion"`
	CheckpointID               string `json:"checkpointId"`
	WorkspaceID                string `json:"workspaceId"`
	SessionID                  string `json:"sessionId"`
	RunID                      string `json:"runId"`
	RunAttemptID               string `json:"runAttemptId"`
	RunAttemptGeneration       int64  `json:"runAttemptGeneration"`
	BrainThreadID              string `json:"brainThreadId"`
	TerminalTurnID             string `json:"terminalTurnId"`
	CodexRuntimeManifestDigest string `json:"codexRuntimeManifestDigest"`
	CheckpointAllowlistVersion int64  `json:"checkpointAllowlistVersion"`
	CatalogDigest              string `json:"catalogDigest"`
	PackSetDigest              string `json:"packSetDigest,omitempty"`
	Files                      []File `json:"files"`
}

// File is the only model-visible state carried by checkpoint artifact v1.
// FileType is explicit so a future serializer cannot silently reinterpret an
// entry as a symlink, directory, device, or other filesystem object.
type File struct {
	Purpose   string `json:"purpose"`
	FileType  string `json:"fileType"`
	Path      string `json:"path"`
	Mode      int    `json:"mode"`
	SizeBytes int64  `json:"sizeBytes"`
	SHA256    string `json:"sha256"`
}

// ResumeAuthority is reconstructed solely from the signed run manifest. It
// is compared with every compatibility and source-identity fact in the
// embedded checkpoint manifest before rollout bytes are restored.
type ResumeAuthority struct {
	ManifestDigest             string
	CheckpointID               string
	WorkspaceID                string
	SessionID                  string
	RunID                      string
	RunAttemptID               string
	RunAttemptGeneration       int64
	BrainThreadID              string
	TerminalTurnID             string
	CodexRuntimeManifestDigest string
	CheckpointAllowlistVersion int64
	CatalogDigest              string
	PackSetDigest              string
}

func (manifest Manifest) Validate() error {
	if manifest.ManifestVersion != CurrentManifestVersion {
		return fmt.Errorf("checkpoint manifestVersion must be %d", CurrentManifestVersion)
	}
	if manifest.CanonicalizerVersion != Canonicalizer {
		return fmt.Errorf("checkpoint canonicalizerVersion must be %q", Canonicalizer)
	}
	for field, value := range map[string]string{
		"checkpointId": manifest.CheckpointID, "workspaceId": manifest.WorkspaceID,
		"sessionId": manifest.SessionID, "runId": manifest.RunID,
		"runAttemptId": manifest.RunAttemptID,
	} {
		if err := validateUUID(field, value); err != nil {
			return err
		}
	}
	if manifest.RunAttemptGeneration < 1 || manifest.RunAttemptGeneration > maxSafeJSONInteger {
		return errors.New("checkpoint runAttemptGeneration must be a positive safe integer")
	}
	if err := validateText("brainThreadId", manifest.BrainThreadID, 256); err != nil {
		return err
	}
	if err := validateText("terminalTurnId", manifest.TerminalTurnID, 256); err != nil {
		return err
	}
	if err := validateDigest("codexRuntimeManifestDigest", manifest.CodexRuntimeManifestDigest); err != nil {
		return err
	}
	if manifest.CheckpointAllowlistVersion < 1 || manifest.CheckpointAllowlistVersion > maxSafeJSONInteger {
		return errors.New("checkpoint checkpointAllowlistVersion must be a positive safe integer")
	}
	if err := validateDigest("catalogDigest", manifest.CatalogDigest); err != nil {
		return err
	}
	if manifest.PackSetDigest != "" {
		if err := validateDigest("packSetDigest", manifest.PackSetDigest); err != nil {
			return err
		}
	}
	if len(manifest.Files) != 1 {
		return errors.New("checkpoint manifest must contain exactly one rollout file")
	}
	return manifest.Files[0].Validate(manifest.CheckpointAllowlistVersion)
}

func (file File) Validate(allowlistVersion int64) error {
	if allowlistVersion != 1 {
		return fmt.Errorf("checkpoint allowlist version %d is not implemented", allowlistVersion)
	}
	if file.Purpose != RolloutPurpose {
		return fmt.Errorf("checkpoint file purpose must be %q", RolloutPurpose)
	}
	if file.FileType != RegularFileType {
		return fmt.Errorf("checkpoint fileType must be %q", RegularFileType)
	}
	if file.Mode != RolloutMode {
		return fmt.Errorf("checkpoint rollout mode must be %d", RolloutMode)
	}
	if err := ValidateRolloutLocator(file.Path); err != nil {
		return err
	}
	if file.SizeBytes < 1 || file.SizeBytes > MaximumRolloutBytes {
		return fmt.Errorf("checkpoint rollout size must be between 1 and %d bytes", MaximumRolloutBytes)
	}
	return validateDigest("checkpoint rollout sha256", file.SHA256)
}

// ValidateRolloutLocator applies the pinned v1 path allowlist before either
// the worker reports a locator or the pool opens the app-owned rollout. It is
// deliberately lexical: the worker proves only containment beneath its
// configured CODEX_HOME, while the trusted pool finalizer separately enforces
// the filesystem boundary and file identity.
func ValidateRolloutLocator(locator string) error {
	if len(locator) == 0 || len(locator) > 512 || !fs.ValidPath(locator) ||
		strings.ContainsRune(locator, '\\') || !strings.HasPrefix(locator, "sessions/") ||
		!strings.HasSuffix(locator, ".jsonl") {
		return errors.New("checkpoint rollout path must be a normalized sessions-relative JSONL path")
	}
	return nil
}

func CanonicalBytes(manifest Manifest) ([]byte, error) {
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("encode checkpoint manifest: %w", err)
	}
	value, _, err := decodeCanonical(raw)
	if err != nil {
		return nil, err
	}
	canonical, err := jcs.Append(nil, value)
	if err != nil {
		return nil, fmt.Errorf("canonicalize checkpoint manifest: %w", err)
	}
	if len(canonical) > MaximumManifestBytes {
		return nil, fmt.Errorf("canonical checkpoint manifest exceeds %d bytes", MaximumManifestBytes)
	}
	return canonical, nil
}

func ParseCanonical(raw []byte) (Manifest, error) {
	if len(raw) == 0 || len(raw) > MaximumManifestBytes {
		return Manifest{}, errors.New("checkpoint manifest size is invalid")
	}
	_, canonical, err := decodeCanonical(raw)
	if err != nil {
		return Manifest{}, err
	}
	if !bytes.Equal(raw, canonical) {
		return Manifest{}, errors.New("checkpoint manifest bytes are not RFC 8785 canonical JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode checkpoint manifest: %w", err)
	}
	if err := finishJSON(decoder); err != nil {
		return Manifest{}, fmt.Errorf("finish checkpoint manifest: %w", err)
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

// Digest returns the domain-separated digest stored independently by core.
func Digest(canonicalManifest []byte) (string, error) {
	if _, err := ParseCanonical(canonicalManifest); err != nil {
		return "", err
	}
	hasher := sha256.New()
	_, _ = io.WriteString(hasher, manifestDigestDomain)
	_, _ = hasher.Write(canonicalManifest)
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// VerifyResume proves that the independently committed manifest digest and
// every signed source/compatibility fact describe this exact artifact.
func VerifyResume(manifest Manifest, canonicalManifest []byte, authority ResumeAuthority) error {
	digest, err := Digest(canonicalManifest)
	if err != nil {
		return err
	}
	if !equalDigest(digest, authority.ManifestDigest) {
		return errors.New("checkpoint manifest does not match the committed manifest digest")
	}
	if manifest.CheckpointID != authority.CheckpointID || manifest.WorkspaceID != authority.WorkspaceID ||
		manifest.SessionID != authority.SessionID || manifest.RunID != authority.RunID ||
		manifest.RunAttemptID != authority.RunAttemptID || manifest.RunAttemptGeneration != authority.RunAttemptGeneration ||
		manifest.BrainThreadID != authority.BrainThreadID || manifest.TerminalTurnID != authority.TerminalTurnID ||
		manifest.CheckpointAllowlistVersion != authority.CheckpointAllowlistVersion ||
		!equalDigest(manifest.CodexRuntimeManifestDigest, authority.CodexRuntimeManifestDigest) ||
		!equalDigest(manifest.CatalogDigest, authority.CatalogDigest) ||
		manifest.PackSetDigest != authority.PackSetDigest {
		return errors.New("checkpoint manifest does not match signed resume authority")
	}
	return nil
}

func decodeCanonical(raw []byte) (any, []byte, error) {
	limits := braincatalog.DefaultLimits()
	limits.MaxCatalogBytes = MaximumManifestBytes
	limits.MaxSchemaBytes = MaximumManifestBytes
	limits.MaxJSONValues = 256
	limits.MaxJSONDepth = 8
	value, canonical, err := braincatalog.DecodeCanonicalJSON(raw, MaximumManifestBytes, limits)
	if err != nil {
		return nil, nil, fmt.Errorf("validate checkpoint manifest JSON: %w", err)
	}
	if _, ok := value.(map[string]any); !ok {
		return nil, nil, errors.New("checkpoint manifest root must be an object")
	}
	return value, canonical, nil
}

func validateUUID(field, value string) error {
	if value == "00000000-0000-0000-0000-000000000000" || !uuidPattern.MatchString(value) {
		return fmt.Errorf("checkpoint %s must be a non-zero canonical lowercase UUID", field)
	}
	return nil
}

func validateDigest(field, value string) error {
	if !digestPattern.MatchString(value) {
		return fmt.Errorf("%s must be lowercase 64-character SHA-256 hex", field)
	}
	return nil
}

func validateText(field, value string, maximum int) error {
	if value == "" || len(value) > maximum || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return fmt.Errorf("checkpoint %s must contain between 1 and %d valid UTF-8 bytes without NUL", field, maximum)
	}
	return nil
}

func equalDigest(left, right string) bool {
	leftBytes, leftErr := hex.DecodeString(left)
	rightBytes, rightErr := hex.DecodeString(right)
	if leftErr != nil || rightErr != nil || len(leftBytes) != sha256.Size || len(rightBytes) != sha256.Size {
		return false
	}
	return subtle.ConstantTimeCompare(leftBytes, rightBytes) == 1
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
