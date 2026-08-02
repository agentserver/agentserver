// Package runmanifest defines the immutable, signed authority document used
// to boot one per-attempt harness worker.
package runmanifest

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/agentserver/agentserver/v2/internal/braincatalog"
	checkpointartifact "github.com/agentserver/agentserver/v2/internal/checkpoint"
	"github.com/ucarion/jcs"
)

const (
	CurrentVersion     = 2
	Canonicalizer      = "rfc8785-v1"
	SignatureAlgorithm = "ed25519-v1"
	MCPProtocolProfile = "2025-11-25"

	maxManifestBytes = 2 * 1024 * 1024
	maxBufferBytes   = 64 * 1024 * 1024
	maxDurationMS    = int64(24 * 60 * 60 * 1000)
	maxJSONInteger   = int64(1<<53 - 1)

	signatureDomain = "agentserver-v2/run-manifest/ed25519-v1\x00"
	digestDomain    = "agentserver-v2/run-manifest/rfc8785-v1\x00"
)

var (
	uuidPattern           = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	digestPattern         = regexp.MustCompile(`^[0-9a-f]{64}$`)
	serviceAccountPattern = regexp.MustCompile(`^[a-z0-9](?:[-a-z0-9]{0,61}[a-z0-9])?$`)
)

type ObjectPointer struct {
	ObjectID  string `json:"objectId"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"sizeBytes"`
	MediaType string `json:"mediaType"`
}

type PreviousCheckpoint struct {
	CheckpointID               string        `json:"checkpointId"`
	RunID                      string        `json:"runId"`
	RunAttemptID               string        `json:"runAttemptId"`
	RunAttemptGeneration       int64         `json:"runAttemptGeneration"`
	ThreadID                   string        `json:"threadId"`
	TurnID                     string        `json:"turnId"`
	ManifestDigest             string        `json:"manifestDigest"`
	CatalogDigest              string        `json:"catalogDigest"`
	CodexRuntimeManifestDigest string        `json:"codexRuntimeManifestDigest"`
	CheckpointAllowlistVersion int64         `json:"checkpointAllowlistVersion"`
	Object                     ObjectPointer `json:"object"`
}

type ModelRoute struct {
	Model                 string `json:"model"`
	Provider              string `json:"provider"`
	Endpoint              string `json:"endpoint"`
	TLSIdentity           string `json:"tlsIdentity"`
	Audience              string `json:"audience"`
	LLMGatewayID          string `json:"llmGatewayId,omitempty"`
	LLMGatewayVersion     int64  `json:"llmGatewayVersion,omitempty"`
	LLMGatewayGrantUserID string `json:"llmGatewayGrantUserId,omitempty"`
}

type MCPTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
	Digest      string          `json:"digest"`
}

type ExecutorMCP struct {
	Endpoint             string          `json:"endpoint"`
	TLSIdentity          string          `json:"tlsIdentity"`
	Audience             string          `json:"audience"`
	ProtocolProfile      string          `json:"protocolProfile"`
	ContractVersion      string          `json:"contractVersion"`
	CatalogID            string          `json:"catalogId"`
	CanonicalizerVersion string          `json:"canonicalizerVersion"`
	CatalogDigest        string          `json:"catalogDigest"`
	CanonicalCatalog     json.RawMessage `json:"canonicalCatalog"`
	Namespace            string          `json:"namespace"`
	NamespaceDescription string          `json:"namespaceDescription"`
	DeferLoading         bool            `json:"deferLoading"`
	Tools                []MCPTool       `json:"tools"`
}

type ExecutorPolicy struct {
	Version       string `json:"version"`
	ContextDigest string `json:"contextDigest"`
}

type RunLimits struct {
	MaxRunDurationMS                int64 `json:"maxRunDurationMs"`
	MaxApprovalTTLMS                int64 `json:"maxApprovalTtlMs"`
	GatewayActiveExecutionTimeoutMS int64 `json:"gatewayActiveExecutionTimeoutMs"`
	MCPTransportGraceMS             int64 `json:"mcpTransportGraceMs"`
	WorkerCallbackGraceMS           int64 `json:"workerCallbackGraceMs"`
	MaxEventBufferBytes             int64 `json:"maxEventBufferBytes"`
	MaxControlBufferBytes           int64 `json:"maxControlBufferBytes"`
}

type ControllerCallback struct {
	Endpoint    string `json:"endpoint"`
	TLSIdentity string `json:"tlsIdentity"`
	Audience    string `json:"audience"`
	HolderID    string `json:"holderId"`
}

type Manifest struct {
	ManifestVersion            int                 `json:"manifestVersion"`
	CanonicalizerVersion       string              `json:"canonicalizerVersion"`
	WorkspaceID                string              `json:"workspaceId"`
	SessionID                  string              `json:"sessionId"`
	RunID                      string              `json:"runId"`
	RunAttemptID               string              `json:"runAttemptId"`
	RunAttemptGeneration       int64               `json:"runAttemptGeneration"`
	HolderID                   string              `json:"holderId"`
	Prompt                     ObjectPointer       `json:"prompt"`
	PreviousCheckpoint         *PreviousCheckpoint `json:"previousCheckpoint,omitempty"`
	CodexRuntimeManifestDigest string              `json:"codexRuntimeManifestDigest"`
	Model                      ModelRoute          `json:"model"`
	ExecutorMCP                ExecutorMCP         `json:"executorMcp"`
	ExecutorPolicy             ExecutorPolicy      `json:"executorPolicy"`
	Limits                     RunLimits           `json:"limits"`
	CheckpointAllowlistVersion int                 `json:"checkpointAllowlistVersion"`
	WorkerImageDigest          string              `json:"workerImageDigest"`
	ExpectedServiceAccount     string              `json:"expectedServiceAccount"`
	ControllerCallback         ControllerCallback  `json:"controllerCallback"`
}

type SignedManifest struct {
	KeyID     string          `json:"keyId"`
	Algorithm string          `json:"algorithm"`
	Manifest  json.RawMessage `json:"manifest"`
	Signature string          `json:"signature"`
}

func ExecutorMCPFromCatalog(endpoint, tlsIdentity, audience, contractVersion, catalogID string, catalog *braincatalog.Catalog) (ExecutorMCP, error) {
	if catalog == nil {
		return ExecutorMCP{}, errors.New("brain tool catalog is required")
	}
	tools := catalog.Tools()
	manifestTools := make([]MCPTool, len(tools))
	for index, tool := range tools {
		manifestTools[index] = MCPTool{
			Name: tool.Name, Description: tool.Description,
			InputSchema: append(json.RawMessage(nil), tool.InputSchema...), Digest: tool.Digest,
		}
	}
	return ExecutorMCP{
		Endpoint: endpoint, TLSIdentity: tlsIdentity, Audience: audience,
		ProtocolProfile: MCPProtocolProfile, ContractVersion: contractVersion, CatalogID: catalogID,
		CanonicalizerVersion: braincatalog.CatalogCanonicalizer, CatalogDigest: catalog.Digest(),
		CanonicalCatalog: catalog.CanonicalBytes(), Namespace: catalog.Namespace(),
		NamespaceDescription: catalog.NamespaceDescription(), DeferLoading: false, Tools: manifestTools,
	}, nil
}

func (manifest Manifest) Validate() error {
	if manifest.ManifestVersion != CurrentVersion {
		return fmt.Errorf("manifestVersion must be %d", CurrentVersion)
	}
	if manifest.CanonicalizerVersion != Canonicalizer {
		return fmt.Errorf("canonicalizerVersion must be %q", Canonicalizer)
	}
	for field, value := range map[string]string{
		"workspaceId": manifest.WorkspaceID, "sessionId": manifest.SessionID,
		"runId": manifest.RunID, "runAttemptId": manifest.RunAttemptID,
	} {
		if err := validateUUID(field, value); err != nil {
			return err
		}
	}
	if manifest.RunAttemptGeneration < 1 || manifest.RunAttemptGeneration > maxJSONInteger {
		return fmt.Errorf("runAttemptGeneration must be between 1 and %d", maxJSONInteger)
	}
	if err := validateText("holderId", manifest.HolderID, 256, true); err != nil {
		return err
	}
	if err := manifest.Prompt.validate("prompt"); err != nil {
		return err
	}
	if manifest.PreviousCheckpoint != nil {
		if err := manifest.PreviousCheckpoint.validate(); err != nil {
			return err
		}
	}
	if err := validateDigest("codexRuntimeManifestDigest", manifest.CodexRuntimeManifestDigest); err != nil {
		return err
	}
	if err := manifest.Model.validate(); err != nil {
		return err
	}
	if err := manifest.ExecutorMCP.validate(); err != nil {
		return err
	}
	if manifest.PreviousCheckpoint != nil && !equalDigest(manifest.PreviousCheckpoint.CatalogDigest, manifest.ExecutorMCP.CatalogDigest) {
		return errors.New("previousCheckpoint.catalogDigest must match executorMcp.catalogDigest")
	}
	if manifest.PreviousCheckpoint != nil &&
		(!equalDigest(manifest.PreviousCheckpoint.CodexRuntimeManifestDigest, manifest.CodexRuntimeManifestDigest) ||
			manifest.PreviousCheckpoint.CheckpointAllowlistVersion != int64(manifest.CheckpointAllowlistVersion)) {
		return errors.New("previousCheckpoint runtime manifest and allowlist version must match the current run manifest")
	}
	if err := validateText("executorPolicy.version", manifest.ExecutorPolicy.Version, 128, true); err != nil {
		return err
	}
	if err := validateDigest("executorPolicy.contextDigest", manifest.ExecutorPolicy.ContextDigest); err != nil {
		return err
	}
	if err := manifest.Limits.validate(); err != nil {
		return err
	}
	if manifest.CheckpointAllowlistVersion < 1 || int64(manifest.CheckpointAllowlistVersion) > maxJSONInteger {
		return fmt.Errorf("checkpointAllowlistVersion must be between 1 and %d", maxJSONInteger)
	}
	if err := validateDigest("workerImageDigest", manifest.WorkerImageDigest); err != nil {
		return err
	}
	if !serviceAccountPattern.MatchString(manifest.ExpectedServiceAccount) {
		return errors.New("expectedServiceAccount must be a canonical Kubernetes service account name")
	}
	if err := manifest.ControllerCallback.validate(); err != nil {
		return err
	}
	if manifest.ControllerCallback.HolderID != manifest.HolderID {
		return errors.New("controllerCallback.holderId must match holderId")
	}
	return nil
}

func CanonicalBytes(manifest Manifest) ([]byte, error) {
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("encode run manifest: %w", err)
	}
	value, _, err := decodeCanonical(raw)
	if err != nil {
		return nil, err
	}
	canonical, err := jcs.Append(nil, value)
	if err != nil {
		return nil, fmt.Errorf("canonicalize run manifest: %w", err)
	}
	if len(canonical) > maxManifestBytes {
		return nil, fmt.Errorf("canonical run manifest exceeds %d bytes", maxManifestBytes)
	}
	return canonical, nil
}

func ParseCanonical(raw []byte) (Manifest, error) {
	manifest, _, err := parseManifest(raw, true)
	return manifest, err
}

// parseManifest returns the RFC 8785 representation used for signature
// verification. A manifest embedded in another JSON document can be rewritten
// by an otherwise conforming serializer (for example, by HTML-escaping '<').
// The signature is therefore checked against the reconstructed canonical
// representation, while standalone persisted manifests remain byte-strict.
func parseManifest(raw []byte, requireCanonical bool) (Manifest, []byte, error) {
	if len(raw) == 0 {
		return Manifest{}, nil, errors.New("run manifest is empty")
	}
	if len(raw) > maxManifestBytes {
		return Manifest{}, nil, fmt.Errorf("run manifest exceeds %d bytes", maxManifestBytes)
	}
	value, canonical, err := decodeCanonical(raw)
	if err != nil {
		return Manifest{}, nil, err
	}
	if requireCanonical && !bytes.Equal(raw, canonical) {
		return Manifest{}, nil, errors.New("run manifest bytes are not RFC 8785 canonical JSON")
	}
	root, ok := value.(map[string]any)
	if !ok {
		return Manifest{}, nil, errors.New("run manifest root must be an object")
	}
	executor, ok := root["executorMcp"].(map[string]any)
	if !ok {
		return Manifest{}, nil, errors.New("executorMcp must be an object")
	}
	deferLoading, ok := executor["deferLoading"].(bool)
	if !ok || deferLoading {
		return Manifest{}, nil, errors.New("executorMcp.deferLoading is required and must be false")
	}
	if _, ok := executor["tools"].([]any); !ok {
		return Manifest{}, nil, errors.New("executorMcp.tools is required and must be an array")
	}
	if checkpoint, present := root["previousCheckpoint"]; present {
		if _, ok := checkpoint.(map[string]any); !ok {
			return Manifest{}, nil, errors.New("previousCheckpoint must be an object when present")
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, nil, fmt.Errorf("decode run manifest: %w", err)
	}
	if err := finishJSON(decoder); err != nil {
		return Manifest{}, nil, fmt.Errorf("finish run manifest: %w", err)
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, nil, err
	}
	return manifest, canonical, nil
}

func Sign(manifest Manifest, keyID string, privateKey ed25519.PrivateKey) (SignedManifest, error) {
	if err := validateText("keyId", keyID, 256, true); err != nil {
		return SignedManifest{}, err
	}
	if len(privateKey) != ed25519.PrivateKeySize {
		return SignedManifest{}, errors.New("Ed25519 private key has invalid length")
	}
	canonical, err := CanonicalBytes(manifest)
	if err != nil {
		return SignedManifest{}, err
	}
	signature := ed25519.Sign(privateKey, signatureMessage(canonical))
	return SignedManifest{
		KeyID: keyID, Algorithm: SignatureAlgorithm, Manifest: append(json.RawMessage(nil), canonical...),
		Signature: base64.RawURLEncoding.EncodeToString(signature),
	}, nil
}

func (signed SignedManifest) Verify(expectedKeyID string, publicKey ed25519.PublicKey) (Manifest, error) {
	if err := validateText("keyId", signed.KeyID, 256, true); err != nil {
		return Manifest{}, err
	}
	if signed.KeyID != expectedKeyID || expectedKeyID == "" {
		return Manifest{}, errors.New("run manifest signing key ID is not trusted")
	}
	if signed.Algorithm != SignatureAlgorithm {
		return Manifest{}, fmt.Errorf("run manifest signature algorithm must be %q", SignatureAlgorithm)
	}
	if len(publicKey) != ed25519.PublicKeySize {
		return Manifest{}, errors.New("Ed25519 public key has invalid length")
	}
	signature, err := base64.RawURLEncoding.DecodeString(signed.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize || base64.RawURLEncoding.EncodeToString(signature) != signed.Signature {
		return Manifest{}, errors.New("run manifest signature is not canonical base64url")
	}
	manifest, canonical, err := parseManifest(signed.Manifest, false)
	if err != nil {
		return Manifest{}, err
	}
	if !ed25519.Verify(publicKey, signatureMessage(canonical), signature) {
		return Manifest{}, errors.New("run manifest signature verification failed")
	}
	return manifest, nil
}

func ParseSigned(raw []byte) (SignedManifest, error) {
	if len(raw) == 0 || len(raw) > maxManifestBytes+4096 {
		return SignedManifest{}, errors.New("signed run manifest envelope size is invalid")
	}
	limits := braincatalog.DefaultLimits()
	limits.MaxCatalogBytes = maxManifestBytes + 4096
	limits.MaxSchemaBytes = limits.MaxCatalogBytes
	// The envelope adds a shallow object and four values around a manifest
	// that may itself be exactly at the standalone token/depth limits.
	limits.MaxJSONValues += 16
	limits.MaxJSONDepth++
	if _, _, err := braincatalog.DecodeCanonicalJSON(raw, len(raw), limits); err != nil {
		return SignedManifest{}, fmt.Errorf("validate signed run manifest envelope: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var signed SignedManifest
	if err := decoder.Decode(&signed); err != nil {
		return SignedManifest{}, fmt.Errorf("decode signed run manifest envelope: %w", err)
	}
	if err := finishJSON(decoder); err != nil {
		return SignedManifest{}, fmt.Errorf("finish signed run manifest envelope: %w", err)
	}
	if len(signed.Manifest) == 0 || signed.Signature == "" {
		return SignedManifest{}, errors.New("signed run manifest envelope is incomplete")
	}
	if err := validateText("keyId", signed.KeyID, 256, true); err != nil {
		return SignedManifest{}, err
	}
	if signed.Algorithm != SignatureAlgorithm {
		return SignedManifest{}, fmt.Errorf("run manifest signature algorithm must be %q", SignatureAlgorithm)
	}
	signature, err := base64.RawURLEncoding.DecodeString(signed.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize || base64.RawURLEncoding.EncodeToString(signature) != signed.Signature {
		return SignedManifest{}, errors.New("run manifest signature is not canonical base64url")
	}
	signed.Manifest = append(json.RawMessage(nil), signed.Manifest...)
	return signed, nil
}

func Digest(canonicalManifest []byte) (string, error) {
	if _, err := ParseCanonical(canonicalManifest); err != nil {
		return "", err
	}
	hasher := sha256.New()
	_, _ = io.WriteString(hasher, digestDomain)
	_, _ = hasher.Write(canonicalManifest)
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func signatureMessage(canonical []byte) []byte {
	message := make([]byte, 0, len(signatureDomain)+len(canonical))
	message = append(message, signatureDomain...)
	message = append(message, canonical...)
	return message
}

func decodeCanonical(raw []byte) (any, []byte, error) {
	limits := braincatalog.DefaultLimits()
	limits.MaxCatalogBytes = maxManifestBytes
	limits.MaxSchemaBytes = maxManifestBytes
	value, canonical, err := braincatalog.DecodeCanonicalJSON(raw, maxManifestBytes, limits)
	if err != nil {
		return nil, nil, fmt.Errorf("validate run manifest JSON: %w", err)
	}
	return value, canonical, nil
}

func (pointer ObjectPointer) validate(field string) error {
	if err := validateUUID(field+".objectId", pointer.ObjectID); err != nil {
		return err
	}
	if err := validateDigest(field+".sha256", pointer.SHA256); err != nil {
		return err
	}
	if pointer.SizeBytes < 1 || pointer.SizeBytes > 1<<40 {
		return fmt.Errorf("%s.sizeBytes must be between 1 and 1099511627776", field)
	}
	return validateText(field+".mediaType", pointer.MediaType, 255, true)
}

func (checkpoint PreviousCheckpoint) validate() error {
	if err := validateUUID("previousCheckpoint.checkpointId", checkpoint.CheckpointID); err != nil {
		return err
	}
	if err := validateUUID("previousCheckpoint.runId", checkpoint.RunID); err != nil {
		return err
	}
	if err := validateUUID("previousCheckpoint.runAttemptId", checkpoint.RunAttemptID); err != nil {
		return err
	}
	if checkpoint.RunAttemptGeneration < 1 || checkpoint.RunAttemptGeneration > maxJSONInteger {
		return fmt.Errorf("previousCheckpoint.runAttemptGeneration must be between 1 and %d", maxJSONInteger)
	}
	if err := validateText("previousCheckpoint.threadId", checkpoint.ThreadID, 256, true); err != nil {
		return err
	}
	if err := validateText("previousCheckpoint.turnId", checkpoint.TurnID, 256, true); err != nil {
		return err
	}
	if err := validateDigest("previousCheckpoint.manifestDigest", checkpoint.ManifestDigest); err != nil {
		return err
	}
	if err := validateDigest("previousCheckpoint.catalogDigest", checkpoint.CatalogDigest); err != nil {
		return err
	}
	if err := validateDigest("previousCheckpoint.codexRuntimeManifestDigest", checkpoint.CodexRuntimeManifestDigest); err != nil {
		return err
	}
	if checkpoint.CheckpointAllowlistVersion < 1 || checkpoint.CheckpointAllowlistVersion > maxJSONInteger {
		return fmt.Errorf("previousCheckpoint.checkpointAllowlistVersion must be between 1 and %d", maxJSONInteger)
	}
	if err := checkpoint.Object.validate("previousCheckpoint.object"); err != nil {
		return err
	}
	if checkpoint.Object.MediaType != checkpointartifact.ArtifactMediaType ||
		checkpoint.Object.SizeBytes > checkpointartifact.MaximumArtifactBytes {
		return errors.New("previousCheckpoint.object must use the bounded checkpoint artifact v1 media profile")
	}
	return nil
}

func (route ModelRoute) validate() error {
	for field, value := range map[string]string{"model.model": route.Model, "model.provider": route.Provider, "model.audience": route.Audience} {
		if err := validateText(field, value, 256, true); err != nil {
			return err
		}
	}
	if err := validateHTTPSURL("model.endpoint", route.Endpoint); err != nil {
		return err
	}
	if err := validateSPIFFEIdentity("model.tlsIdentity", route.TLSIdentity); err != nil {
		return err
	}
	gatewayBound := route.LLMGatewayID != "" || route.LLMGatewayVersion != 0 || route.LLMGatewayGrantUserID != ""
	if route.Provider == "workspace-gateway" {
		if err := validateUUID("model.llmGatewayId", route.LLMGatewayID); err != nil {
			return err
		}
		if err := validateUUID("model.llmGatewayGrantUserId", route.LLMGatewayGrantUserID); err != nil {
			return err
		}
		if route.LLMGatewayVersion < 1 || route.LLMGatewayVersion > maxJSONInteger {
			return fmt.Errorf("model.llmGatewayVersion must be between 1 and %d", maxJSONInteger)
		}
	} else if gatewayBound {
		return errors.New("non-workspace model route contains workspace LLM gateway authority")
	}
	return nil
}

func (executor ExecutorMCP) validate() error {
	if err := validateHTTPSURL("executorMcp.endpoint", executor.Endpoint); err != nil {
		return err
	}
	if err := validateSPIFFEIdentity("executorMcp.tlsIdentity", executor.TLSIdentity); err != nil {
		return err
	}
	if err := validateText("executorMcp.audience", executor.Audience, 256, true); err != nil {
		return err
	}
	if executor.ProtocolProfile != MCPProtocolProfile {
		return fmt.Errorf("executorMcp.protocolProfile must be %q", MCPProtocolProfile)
	}
	if err := validateText("executorMcp.contractVersion", executor.ContractVersion, 128, true); err != nil {
		return err
	}
	if err := validateUUID("executorMcp.catalogId", executor.CatalogID); err != nil {
		return err
	}
	if executor.CanonicalizerVersion != braincatalog.CatalogCanonicalizer {
		return fmt.Errorf("executorMcp.canonicalizerVersion must be %q", braincatalog.CatalogCanonicalizer)
	}
	if err := validateDigest("executorMcp.catalogDigest", executor.CatalogDigest); err != nil {
		return err
	}
	catalog, err := braincatalog.ParseCanonical(executor.CanonicalCatalog, braincatalog.DefaultLimits())
	if err != nil {
		return fmt.Errorf("executorMcp.canonicalCatalog: %w", err)
	}
	if err := catalog.VerifyFrozen(executor.CatalogDigest, executor.CanonicalCatalog); err != nil {
		return err
	}
	if executor.Namespace != catalog.Namespace() || executor.NamespaceDescription != catalog.NamespaceDescription() {
		return errors.New("executorMcp namespace projection does not match canonicalCatalog")
	}
	if executor.DeferLoading {
		return errors.New("executorMcp.deferLoading must be false")
	}
	frozenTools := catalog.Tools()
	if len(executor.Tools) != len(frozenTools) {
		return errors.New("executorMcp tools projection length does not match canonicalCatalog")
	}
	for index, tool := range executor.Tools {
		frozen := frozenTools[index]
		if tool.Name != frozen.Name || tool.Description != frozen.Description ||
			!bytes.Equal(tool.InputSchema, frozen.InputSchema) || !equalDigest(tool.Digest, frozen.Digest) {
			return fmt.Errorf("executorMcp tools[%d] does not match canonicalCatalog", index)
		}
	}
	return nil
}

func (limits RunLimits) validate() error {
	durations := map[string]int64{
		"limits.maxRunDurationMs":                limits.MaxRunDurationMS,
		"limits.maxApprovalTtlMs":                limits.MaxApprovalTTLMS,
		"limits.gatewayActiveExecutionTimeoutMs": limits.GatewayActiveExecutionTimeoutMS,
		"limits.mcpTransportGraceMs":             limits.MCPTransportGraceMS,
		"limits.workerCallbackGraceMs":           limits.WorkerCallbackGraceMS,
	}
	for field, value := range durations {
		if value < 1 || value > maxDurationMS {
			return fmt.Errorf("%s must be between 1 and %d", field, maxDurationMS)
		}
	}
	if limits.MaxApprovalTTLMS > limits.MaxRunDurationMS || limits.GatewayActiveExecutionTimeoutMS > limits.MaxRunDurationMS {
		return errors.New("approval and gateway execution timeouts cannot exceed maxRunDurationMs")
	}
	for field, value := range map[string]int64{
		"limits.maxEventBufferBytes":   limits.MaxEventBufferBytes,
		"limits.maxControlBufferBytes": limits.MaxControlBufferBytes,
	} {
		if value < 1 || value > maxBufferBytes {
			return fmt.Errorf("%s must be between 1 and %d", field, maxBufferBytes)
		}
	}
	return nil
}

func (callback ControllerCallback) validate() error {
	if err := validateHTTPSURL("controllerCallback.endpoint", callback.Endpoint); err != nil {
		return err
	}
	if err := validateSPIFFEIdentity("controllerCallback.tlsIdentity", callback.TLSIdentity); err != nil {
		return err
	}
	if err := validateText("controllerCallback.audience", callback.Audience, 256, true); err != nil {
		return err
	}
	return validateText("controllerCallback.holderId", callback.HolderID, 256, true)
}

func validateUUID(field, value string) error {
	if value == "00000000-0000-0000-0000-000000000000" || !uuidPattern.MatchString(value) {
		return fmt.Errorf("%s must be a non-zero canonical lowercase UUID", field)
	}
	return nil
}

func validateDigest(field, value string) error {
	if !digestPattern.MatchString(value) {
		return fmt.Errorf("%s must be lowercase 64-character SHA-256 hex", field)
	}
	return nil
}

func validateText(field, value string, maximum int, required bool) error {
	if (!required && value == "") || (utf8.ValidString(value) && !strings.ContainsRune(value, 0) && len(value) <= maximum && (!required || len(value) > 0)) {
		return nil
	}
	return fmt.Errorf("%s must contain between 1 and %d valid UTF-8 bytes without NUL", field, maximum)
}

func validateHTTPSURL(field, value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" || parsed.RawPath != "" {
		return fmt.Errorf("%s must be a canonical credential-free HTTPS URL without query or fragment", field)
	}
	if parsed.Host != strings.ToLower(parsed.Host) || strings.HasSuffix(parsed.Hostname(), ".") || parsed.String() != value {
		return fmt.Errorf("%s must use a canonical lowercase host", field)
	}
	return nil
}

func validateSPIFFEIdentity(field, value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "spiffe" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path == "" || parsed.String() != value || parsed.Host != strings.ToLower(parsed.Host) || strings.HasSuffix(parsed.Host, ".") {
		return fmt.Errorf("%s must be an absolute canonical SPIFFE URI", field)
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
