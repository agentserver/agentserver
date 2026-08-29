package runmanifest

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/braincatalog"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/ucarion/jcs"
)

func TestRunManifestCanonicalSignVerifyAndDigest(t *testing.T) {
	manifest := validManifest(t)
	// Exercise an otherwise valid value that encoding/json rewrites when the
	// canonical manifest is embedded as json.RawMessage in the signed envelope.
	manifest.Model.Model = "gpt<5>&\u2028route"
	seed := sha256.Sum256([]byte("run-manifest-test-key"))
	privateKey := ed25519.NewKeyFromSeed(seed[:])
	signed, err := Sign(manifest, "cluster-key-1", privateKey)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := signed.Verify("cluster-key-1", privateKey.Public().(ed25519.PublicKey))
	if err != nil {
		t.Fatal(err)
	}
	if verified.RunID != manifest.RunID || !bytes.Equal(signed.Manifest, mustCanonical(t, manifest)) {
		t.Fatalf("verified manifest differs: %+v", verified)
	}
	digest, err := Digest(signed.Manifest)
	if err != nil || len(digest) != 64 {
		t.Fatalf("Digest() = %q, %v", digest, err)
	}

	rawEnvelope, err := json.Marshal(signed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(rawEnvelope, []byte(`\u003c`)) || !bytes.Contains(rawEnvelope, []byte(`\u2028`)) {
		t.Fatalf("test envelope did not exercise JSON escaping: %s", rawEnvelope)
	}
	parsed, err := ParseSigned(rawEnvelope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parsed.Verify("cluster-key-1", privateKey.Public().(ed25519.PublicKey)); err != nil {
		t.Fatal(err)
	}

	signature, err := base64.RawURLEncoding.DecodeString(signed.Signature)
	if err != nil {
		t.Fatal(err)
	}
	if ed25519.Verify(privateKey.Public().(ed25519.PublicKey), signed.Manifest, signature) {
		t.Fatal("signature was not domain separated")
	}
}

func TestRunManifestSignsAndVerifiesCodexPermissionMode(t *testing.T) {
	manifest := validManifest(t)
	manifest.PermissionMode = CodexPermissionModeAuto
	manifest.PermissionModeVersion = 1
	seed := sha256.Sum256([]byte("run-manifest-permission-mode-key"))
	privateKey := ed25519.NewKeyFromSeed(seed[:])
	signed, err := Sign(manifest, "cluster-key-1", privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(signed.Manifest, []byte(`"permissionMode":"auto"`)) {
		t.Fatalf("signed manifest omitted permission mode: %s", signed.Manifest)
	}
	verified, err := signed.Verify("cluster-key-1", privateKey.Public().(ed25519.PublicKey))
	if err != nil {
		t.Fatal(err)
	}
	if verified.PermissionMode != CodexPermissionModeAuto {
		t.Fatalf("verified permission mode = %q", verified.PermissionMode)
	}

	legacy := validManifest(t)
	legacy.PermissionMode = ""
	canonical, err := CanonicalBytes(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(canonical, []byte(`"permissionMode"`)) {
		t.Fatalf("legacy manifest unexpectedly emitted permission mode: %s", canonical)
	}
	if mode, err := legacy.EffectivePermissionMode(); err != nil || mode != CodexPermissionModeReadOnly {
		t.Fatalf("legacy effective permission mode = %q, %v", mode, err)
	}
}

func TestRunManifestSignsAndVerifiesWorkspaceAuthority(t *testing.T) {
	manifest := validManifest(t)
	manifest.Workspace = &WorkspaceAuthority{
		EnvironmentID: "50000000-0000-4000-8000-000000000005", EnvironmentVersion: 3,
		RootSHA256: strings.Repeat("1", 64), WorkingDirectory: "rtm-aihub", WorkingDirectoryVersion: 4,
	}
	seed := sha256.Sum256([]byte("run-manifest-workspace-key"))
	signed, err := Sign(manifest, "cluster-key-1", ed25519.NewKeyFromSeed(seed[:]))
	if err != nil {
		t.Fatal(err)
	}
	verified, err := signed.Verify("cluster-key-1", ed25519.NewKeyFromSeed(seed[:]).Public().(ed25519.PublicKey))
	if err != nil || verified.Workspace == nil || verified.Workspace.WorkingDirectory != "rtm-aihub" {
		t.Fatalf("verified workspace authority = %+v, %v", verified.Workspace, err)
	}
	if _, err := verified.Workspace.Binding(); err != nil {
		t.Fatalf("verified workspace binding = %v", err)
	}
	invalid := manifest
	invalid.Workspace = &WorkspaceAuthority{
		EnvironmentID: manifest.Workspace.EnvironmentID, EnvironmentVersion: 3,
		RootSHA256: strings.Repeat("1", 64), WorkingDirectory: "../rtm-aihub", WorkingDirectoryVersion: 4,
	}
	if err := invalid.Validate(); err == nil || !strings.Contains(err.Error(), "working directory") {
		t.Fatalf("unsafe workspace directory validation error = %v", err)
	}
	readOnlyCatalog, err := braincatalog.BuildCatalog("executor", "Executor tools.", []braincatalog.ToolDescriptor{{
		Name: "read_file", Description: "Read one file.", InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`),
	}}, braincatalog.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	withoutDiscovery := manifest
	withoutDiscovery.ExecutorMCP, err = ExecutorMCPFromCatalog(
		manifest.ExecutorMCP.Endpoint, manifest.ExecutorMCP.TLSIdentity, manifest.ExecutorMCP.Audience,
		manifest.ExecutorMCP.ContractVersion, manifest.ExecutorMCP.CatalogID, readOnlyCatalog,
	)
	if err != nil {
		t.Fatal(err)
	}
	withoutDiscovery.PreviousCheckpoint = nil
	if err := withoutDiscovery.Validate(); err == nil || !strings.Contains(err.Error(), "list_environments") {
		t.Fatalf("workspace tool catalog without environment discovery error = %v", err)
	}
}

func TestRunManifestRejectsUnknownCodexPermissionMode(t *testing.T) {
	manifest := validManifest(t)
	manifest.PermissionMode = "future-mode"
	if err := manifest.Validate(); err == nil || !strings.Contains(err.Error(), "permission mode") {
		t.Fatalf("unknown permission mode error = %v", err)
	}
}

func TestRunManifestRejectsTamperUnknownAndMissingSecurityField(t *testing.T) {
	manifest := validManifest(t)
	seed := sha256.Sum256([]byte("run-manifest-test-key"))
	privateKey := ed25519.NewKeyFromSeed(seed[:])
	signed, err := Sign(manifest, "cluster-key-1", privateKey)
	if err != nil {
		t.Fatal(err)
	}
	tampered := signed
	tampered.Manifest = bytes.Replace(append([]byte(nil), signed.Manifest...), []byte(manifest.RunID), []byte("40000000-0000-4000-8000-000000000099"), 1)
	if _, err := tampered.Verify("cluster-key-1", privateKey.Public().(ed25519.PublicKey)); err == nil || !strings.Contains(err.Error(), "verification failed") {
		t.Fatalf("tamper verification error = %v", err)
	}
	if _, err := signed.Verify("cluster-key-2", privateKey.Public().(ed25519.PublicKey)); err == nil || !strings.Contains(err.Error(), "not trusted") {
		t.Fatalf("wrong-key error = %v", err)
	}

	value := decodeTestObject(t, signed.Manifest)
	value["futureAuthority"] = true
	unknown := canonicalTestValue(t, value)
	if _, err := ParseCanonical(unknown); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown-field error = %v", err)
	}
	value = decodeTestObject(t, signed.Manifest)
	executor := value["executorMcp"].(map[string]any)
	delete(executor, "deferLoading")
	missing := canonicalTestValue(t, value)
	if _, err := ParseCanonical(missing); err == nil || !strings.Contains(err.Error(), "deferLoading is required") {
		t.Fatalf("missing deferLoading error = %v", err)
	}
	value = decodeTestObject(t, signed.Manifest)
	value["previousCheckpoint"] = nil
	explicitNull := canonicalTestValue(t, value)
	if _, err := ParseCanonical(explicitNull); err == nil || !strings.Contains(err.Error(), "must be an object") {
		t.Fatalf("explicit-null checkpoint error = %v", err)
	}
}

func TestRunManifestValidatesCatalogProjectionAndEndpoints(t *testing.T) {
	manifest := validManifest(t)
	manifest.ExecutorMCP.Tools[0].Description = "changed"
	if err := manifest.Validate(); err == nil || !strings.Contains(err.Error(), "does not match canonicalCatalog") {
		t.Fatalf("changed catalog projection error = %v", err)
	}
	manifest = validManifest(t)
	manifest.ExecutorMCP.Endpoint = "https://executor.internal/mcp?token=secret"
	if err := manifest.Validate(); err == nil || !strings.Contains(err.Error(), "credential-free HTTPS") {
		t.Fatalf("unsafe endpoint error = %v", err)
	}
	manifest = validManifest(t)
	manifest.ControllerCallback.HolderID = "other-holder"
	if err := manifest.Validate(); err == nil || !strings.Contains(err.Error(), "must match holderId") {
		t.Fatalf("callback holder error = %v", err)
	}
	manifest = validManifest(t)
	manifest.RunAttemptGeneration = maxJSONInteger + 1
	if err := manifest.Validate(); err == nil || !strings.Contains(err.Error(), "runAttemptGeneration") {
		t.Fatalf("unsafe JSON integer error = %v", err)
	}
}

func TestRunManifestBindsCheckpointArtifactProfileAndToolPack(t *testing.T) {
	manifest := validManifest(t)
	manifest.PreviousCheckpoint.RunAttemptID = ""
	if err := manifest.Validate(); err == nil || !strings.Contains(err.Error(), "runAttemptId") {
		t.Fatalf("missing checkpoint source attempt error = %v", err)
	}
	manifest = validManifest(t)
	manifest.PreviousCheckpoint.CodexRuntimeManifestDigest = strings.Repeat("0", 64)
	if err := manifest.Validate(); err == nil || !strings.Contains(err.Error(), "runtime manifest") {
		t.Fatalf("checkpoint runtime drift error = %v", err)
	}
	manifest = validManifest(t)
	manifest.PreviousCheckpoint.Object.MediaType = "application/octet-stream"
	if err := manifest.Validate(); err == nil || !strings.Contains(err.Error(), "artifact v1") {
		t.Fatalf("checkpoint artifact profile error = %v", err)
	}
	manifest = validManifest(t)
	manifest.ToolPack = &ToolPackAuthority{
		PackID: "lark-readonly@v1", SkillSHA256: strings.Repeat("2", 64),
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("valid tool-pack resume authority rejected: %v", err)
	}
	manifest = validManifest(t)
	manifest.ToolPack = &ToolPackAuthority{
		PackID: "Lark/latest", SkillSHA256: strings.Repeat("2", 64),
	}
	if err := manifest.Validate(); err == nil || !strings.Contains(err.Error(), "versioned pack ID") {
		t.Fatalf("invalid pack ID error = %v", err)
	}
}

func TestRunManifestJSONSchemaAcceptsSignedEnvelope(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate runmanifest package")
	}
	rawSchema, err := os.ReadFile(filepath.Join(filepath.Dir(currentFile), "..", "..", "api", "schema", "run-manifest.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(rawSchema, &schema); err != nil {
		t.Fatalf("run manifest schema is invalid JSON: %v", err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatalf("resolve run manifest schema: %v", err)
	}
	seed := sha256.Sum256([]byte("run-manifest-schema-key"))
	manifest := validManifest(t)
	manifest.ToolPack = &ToolPackAuthority{
		PackID: "lark-readonly@v1", SkillSHA256: strings.Repeat("2", 64),
	}
	signed, err := Sign(manifest, "cluster-key-1", ed25519.NewKeyFromSeed(seed[:]))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(signed)
	if err != nil {
		t.Fatal(err)
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	if err := resolved.Validate(value); err != nil {
		t.Fatalf("valid signed run manifest rejected by schema: %v", err)
	}

	// The permission mode authority is optional only for legacy manifests, but
	// its mode and CAS version must be emitted as a pair for new manifests.
	withMode := validManifest(t)
	withMode.PermissionMode = CodexPermissionModeAuto
	withMode.PermissionModeVersion = 7
	withModeSigned, err := Sign(withMode, "cluster-key-1", ed25519.NewKeyFromSeed(seed[:]))
	if err != nil {
		t.Fatal(err)
	}
	withModeRaw, err := json.Marshal(withModeSigned)
	if err != nil {
		t.Fatal(err)
	}
	var withModeValue map[string]any
	if err := json.Unmarshal(withModeRaw, &withModeValue); err != nil {
		t.Fatal(err)
	}
	if err := resolved.Validate(withModeValue); err != nil {
		t.Fatalf("signed run manifest with permission mode rejected by schema: %v", err)
	}
	for name, field := range map[string]string{
		"mode without version": "permissionModeVersion",
		"version without mode": "permissionMode",
	} {
		t.Run(name, func(t *testing.T) {
			encoded, err := json.Marshal(withModeValue)
			if err != nil {
				t.Fatal(err)
			}
			var candidate map[string]any
			if err := json.Unmarshal(encoded, &candidate); err != nil {
				t.Fatal(err)
			}
			manifestValue, ok := candidate["manifest"].(map[string]any)
			if !ok {
				t.Fatal("signed manifest envelope has no manifest object")
			}
			delete(manifestValue, field)
			if err := resolved.Validate(candidate); err == nil {
				t.Fatalf("schema accepted permission authority with %s omitted", field)
			}
		})
	}
}

func validManifest(t *testing.T) Manifest {
	t.Helper()
	catalog, err := braincatalog.BuildCatalog("executor", "Deterministic executor tools.", []braincatalog.ToolDescriptor{
		{
			Name: "list_environments", Description: "List environments.",
			InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`),
		},
		{
			Name: "read_file", Description: "Read one file.",
			InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`),
		},
	}, braincatalog.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	executorMCP, err := ExecutorMCPFromCatalog(
		"https://executor-gateway.agentserver.svc/mcp",
		"spiffe://agentserver.local/ns/agentserver/sa/executor-gateway",
		"executor-mcp",
		"executor-mcp/1.1",
		"45000000-0000-4000-8000-000000000004",
		catalog,
	)
	if err != nil {
		t.Fatal(err)
	}
	return Manifest{
		ManifestVersion: CurrentVersion, CanonicalizerVersion: Canonicalizer,
		WorkspaceID:          "40000000-0000-4000-8000-000000000004",
		SessionID:            "41000000-0000-4000-8000-000000000004",
		RunID:                "42000000-0000-4000-8000-000000000004",
		RunAttemptID:         "43000000-0000-4000-8000-000000000004",
		RunAttemptGeneration: 3, HolderID: "pool-holder-1",
		Prompt: ObjectPointer{
			ObjectID: "44000000-0000-4000-8000-000000000004", SHA256: strings.Repeat("a", 64),
			SizeBytes: 128, MediaType: "application/json",
		},
		PreviousCheckpoint: &PreviousCheckpoint{
			CheckpointID: "46000000-0000-4000-8000-000000000004",
			RunID:        "48000000-0000-4000-8000-000000000004", RunAttemptID: "49000000-0000-4000-8000-000000000004",
			RunAttemptGeneration: 2, ThreadID: "thread-previous", TurnID: "turn-previous",
			ManifestDigest: strings.Repeat("b", 64), CatalogDigest: catalog.Digest(),
			CodexRuntimeManifestDigest: strings.Repeat("d", 64), CheckpointAllowlistVersion: 1,
			Object: ObjectPointer{ObjectID: "47000000-0000-4000-8000-000000000004", SHA256: strings.Repeat("c", 64), SizeBytes: 1024, MediaType: "application/vnd.agentserver.codex-checkpoint.v1"},
		},
		CodexRuntimeManifestDigest: strings.Repeat("d", 64),
		Model: ModelRoute{
			Model: "gpt-5", Provider: "llmproxy", Endpoint: "https://llmproxy.agentserver.svc/v1",
			TLSIdentity: "spiffe://agentserver.local/ns/agentserver/sa/llmproxy", Audience: "llmproxy",
		},
		ExecutorMCP:    executorMCP,
		ExecutorPolicy: ExecutorPolicy{Version: "executor-policy/1", ContextDigest: strings.Repeat("e", 64)},
		Limits: RunLimits{
			MaxRunDurationMS: 3_600_000, MaxApprovalTTLMS: 300_000,
			GatewayActiveExecutionTimeoutMS: 600_000, MCPTransportGraceMS: 5_000,
			WorkerCallbackGraceMS: 10_000, MaxEventBufferBytes: 8 * 1024 * 1024,
			MaxControlBufferBytes: 2 * 1024 * 1024,
		},
		CheckpointAllowlistVersion: 1, WorkerImageDigest: strings.Repeat("f", 64),
		ExpectedServiceAccount: "harness-worker",
		ControllerCallback: ControllerCallback{
			Endpoint:    "https://pool-holder-1.agentserver.svc/control",
			TLSIdentity: "spiffe://agentserver.local/ns/agentserver/sa/harness-pool",
			Audience:    "harness-pool-control", HolderID: "pool-holder-1",
		},
	}
}

func mustCanonical(t *testing.T, manifest Manifest) []byte {
	t.Helper()
	canonical, err := CanonicalBytes(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func decodeTestObject(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func canonicalTestValue(t *testing.T, value any) []byte {
	t.Helper()
	canonical, err := jcs.Append(nil, value)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}
