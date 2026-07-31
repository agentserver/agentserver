package devstack

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/harnesspool"
	"github.com/agentserver/agentserver/v2/internal/runcapability"
	"github.com/agentserver/agentserver/v2/internal/runcursor"
	"github.com/agentserver/agentserver/v2/internal/runmanifest"
	"github.com/agentserver/agentserver/v2/internal/runtimelock"
	"github.com/google/jsonschema-go/jsonschema"
)

func TestPrepareBuildsClosedDevelopmentStackWithoutWorkerSecrets(t *testing.T) {
	fixture := newConfigFixture(t)
	loaded, err := ValidateConfig(fixture.document)
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(fixture.root, "prepared-stack")
	createdAt := time.Date(2026, time.July, 31, 8, 30, 0, 0, time.UTC)
	result, err := Prepare(loaded, output, rand.Reader, createdAt)
	if err != nil {
		t.Fatal(err)
	}
	if result.OutputDirectory != output || result.MetadataFile != filepath.Join(output, metadataFile) ||
		result.BootstrapConfigFile != filepath.Join(output, bootstrapConfigFile) ||
		result.WorkerDeploymentFile != filepath.Join(output, workerDeploymentConfigFile) {
		t.Fatalf("prepare result = %+v", result)
	}
	assertGeneratedModes(t, output)
	assertGeneratedTLS(t, output, createdAt)

	coreEnvironment := readGeneratedEnvironment(t, result.EnvironmentFiles["agentserver-core"])
	executorEnvironment := readGeneratedEnvironment(t, result.EnvironmentFiles["executor-gateway"])
	poolEnvironment := readGeneratedEnvironment(t, result.EnvironmentFiles["harness-pool"])
	if coreEnvironment["AGENTSERVER_V2_DATABASE_URL"] != fixture.document.DatabaseURL ||
		coreEnvironment["AGENTSERVER_V2_RUN_ALLOWED_TOOLS"] != "list_environments,read_file,shell" ||
		executorEnvironment["AGENTSERVER_V2_DEV_EXECUTOR_ID"] != fixture.document.Authority.ExecutorID ||
		poolEnvironment["AGENTSERVER_V2_CODEX_RUNTIME_MANIFEST_SHA256"] != loaded.ManifestSHA256 {
		t.Fatalf("generated service environments do not preserve config: core=%v executor=%v pool=%v", coreEnvironment, executorEnvironment, poolEnvironment)
	}
	capabilityKey := executorEnvironment["AGENTSERVER_V2_DEV_RUN_CAPABILITY_KEY"]
	if capabilityKey == "" || poolEnvironment["AGENTSERVER_V2_DEV_RUN_CAPABILITY_KEY"] != capabilityKey {
		t.Fatal("executor-gateway and harness-pool do not share one generated capability key")
	}
	if _, err := runcapability.NewDevelopmentCodecFromBase64Key(capabilityKey); err != nil {
		t.Fatalf("generated run capability key: %v", err)
	}
	cursorBytes, err := base64.RawURLEncoding.DecodeString(coreEnvironment["AGENTSERVER_V2_RUN_CURSOR_KEY"])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runcursor.NewCodec(cursorBytes); err != nil {
		t.Fatalf("generated cursor key: %v", err)
	}

	signer, err := harnesspool.LoadEd25519ManifestSigner(manifestSigningKeyID, filepath.Join(output, manifestSeedFile))
	if err != nil || signer == nil {
		t.Fatalf("load generated manifest signer: %v", err)
	}
	keyringBytes, err := os.ReadFile(filepath.Join(output, manifestVerificationKeyringFile))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runmanifest.ParseVerificationKeyring(keyringBytes); err != nil {
		t.Fatalf("parse generated manifest keyring: %v", err)
	}
	var keyring runmanifest.VerificationKeyringDocument
	if err := json.Unmarshal(keyringBytes, &keyring); err != nil {
		t.Fatal(err)
	}
	seed, err := os.ReadFile(filepath.Join(output, manifestSeedFile))
	if err != nil {
		t.Fatal(err)
	}
	wantPublic := ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
	if len(keyring.Keys) != 1 || keyring.Keys[0].PublicKey != base64.RawURLEncoding.EncodeToString(wantPublic) {
		t.Fatal("generated manifest keyring does not match the private seed")
	}

	workerBytes := mustReadFile(t, result.WorkerDeploymentFile)
	agentxBytes := mustReadFile(t, result.AgentxLaunchFile)
	metadataBytes := mustReadFile(t, result.MetadataFile)
	for name, secret := range map[string][]byte{
		"capability HMAC": []byte(capabilityKey),
		"cursor HMAC":     []byte(coreEnvironment["AGENTSERVER_V2_RUN_CURSOR_KEY"]),
		"manifest seed":   seed,
	} {
		for target, contents := range map[string][]byte{"worker deployment": workerBytes, "agentx launch": agentxBytes, "metadata": metadataBytes} {
			if bytes.Contains(contents, secret) {
				t.Fatalf("%s leaked into %s", name, target)
			}
		}
	}
	if bytes.Contains(workerBytes, []byte(filepath.Join(output, manifestSeedFile))) {
		t.Fatal("manifest private-key path leaked into worker deployment")
	}
	if !bytes.Contains(workerBytes, []byte(filepath.Join(output, manifestVerificationKeyringFile))) {
		t.Fatal("worker deployment does not reference the public manifest keyring")
	}

	before := sha256.Sum256(metadataBytes)
	if _, err := Prepare(loaded, output, rand.Reader, createdAt.Add(time.Hour)); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("prepare overwrite error = %v", err)
	}
	after := sha256.Sum256(mustReadFile(t, result.MetadataFile))
	if before != after {
		t.Fatal("failed overwrite attempt changed existing generated material")
	}
}

func TestLoadConfigRejectsUnknownDuplicateAndBroadSecretFile(t *testing.T) {
	fixture := newConfigFixture(t)
	raw, err := json.Marshal(fixture.document)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(fixture.root, "stack.json")
	if err := os.WriteFile(configPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(configPath); err != nil {
		t.Fatal(err)
	}

	unknown := bytes.TrimSuffix(raw, []byte("}"))
	unknown = append(unknown, []byte(`,"future":true}`)...)
	unknownPath := filepath.Join(fixture.root, "unknown.json")
	if err := os.WriteFile(unknownPath, unknown, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(unknownPath); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown config error = %v", err)
	}

	duplicatePath := filepath.Join(fixture.root, "duplicate.json")
	if err := os.WriteFile(duplicatePath, []byte(`{"version":1,"version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(duplicatePath); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate config error = %v", err)
	}
	if err := os.Chmod(configPath, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(configPath); err == nil || !strings.Contains(err.Error(), "mode 0600") {
		t.Fatalf("broad config error = %v", err)
	}
}

func TestPrepareRejectsWeakRandomnessBeforeCreatingOutput(t *testing.T) {
	fixture := newConfigFixture(t)
	loaded, err := ValidateConfig(fixture.document)
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(fixture.root, "weak-stack")
	_, err = Prepare(loaded, output, bytes.NewReader(make([]byte, 64*1024)), time.Now().UTC())
	if err == nil || !strings.Contains(err.Error(), "all-zero") {
		t.Fatalf("weak randomness error = %v", err)
	}
	if _, statErr := os.Lstat(output); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("weak-randomness preparation left output: %v", statErr)
	}
}

func TestEnvironmentFormatRoundTripsShellMetacharacters(t *testing.T) {
	want := map[string]string{
		"A": `plain`,
		"B": `postgres://user:p'$a\"s` + "`" + `!@127.0.0.1/db`,
	}
	raw, err := renderEnvironmentFile(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseEnvironment(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) || got["A"] != want["A"] || got["B"] != want["B"] {
		t.Fatalf("environment round trip = %#v, want %#v\n%s", got, want, raw)
	}
}

type configFixture struct {
	root     string
	document ConfigDocument
}

func newConfigFixture(t *testing.T) configFixture {
	t.Helper()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("development stack filesystem preparation is supported only on Unix")
	}
	temporary := t.TempDir()
	root, err := filepath.EvalSymlinks(temporary)
	if err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(filepath.Join(workspace, "src"), 0o700); err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(root, "runtime-bundle")
	if err := os.MkdirAll(filepath.Join(bundle, "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	codexPath := filepath.Join(bundle, "bin", "codex")
	if err := os.WriteFile(codexPath, []byte("pinned-stock-codex-0.146.0"), 0o500); err != nil {
		t.Fatal(err)
	}
	codexDigest, codexSize, err := runtimelock.HashFile(codexPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest := testRuntimeManifest(codexDigest, codexSize)
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(root, "runtime-manifest.json")
	if err := os.WriteFile(manifestPath, manifestBytes, 0o400); err != nil {
		t.Fatal(err)
	}
	binary := func(name string) string {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte("test-binary-"+name), 0o500); err != nil {
			t.Fatal(err)
		}
		return path
	}
	return configFixture{
		root: root,
		document: ConfigDocument{
			Version: CurrentConfigVersion, DatabaseURL: `postgres://agentserver:p$a%22s@127.0.0.1:5432/agentserver?sslmode=disable`,
			Authority: AuthorityDocument{
				WorkspaceID: "40000000-0000-4000-8000-000000000004", SessionID: "50000000-0000-4000-8000-000000000005",
				ActorID: "10000000-0000-4000-8000-000000000001", ExecutorID: "20000000-0000-4000-8000-000000000002",
				EnvironmentID: "60000000-0000-4000-8000-000000000006", AgentxVersion: "0.1.0-dev",
				WorkspaceRoot: workspace, DisplayName: "Local workspace", Description: "insecure development executor", DefaultCWD: "src",
			},
			Runtime: RuntimeDocument{
				ManifestFile: manifestPath, BundleRoot: bundle, AgentxBinary: binary("agentx"),
				HarnessWorkerBinary: binary("harness-worker"), HarnessFinalExecBinary: binary("harness-final-exec"),
			},
			Network: NetworkDocument{
				CoreListenAddress: "127.0.0.1:17443", BrowserGatewayListenAddress: "127.0.0.1:17444",
				ExecutorGatewayListenAddress: "127.0.0.1:17445", HarnessPoolListenAddress: "127.0.0.1:17446",
				HydraIntrospectionURL: "http://127.0.0.1:17447/oauth2/introspect", LLMProxyEndpoint: "https://127.0.0.1:17448/v1",
			},
			Model:      ModelDocument{Name: "gpt-5", Provider: "llmproxy"},
			Policy:     PolicyDocument{Version: "dev-v1", AllowedTools: []string{"shell", "list_environments", "read_file"}},
			Harness:    HarnessDocument{MaxConcurrentAttempts: 2, MaxRunDuration: "30m"},
			Identities: IdentitiesDocument{WorkerUID: 65531, WorkerGID: 65531, AppUID: 65532, AppGID: 65532},
		},
	}
}

func testRuntimeManifest(codexDigest string, codexSize int64) runtimelock.Manifest {
	return runtimelock.Manifest{
		ManifestVersion: runtimelock.CurrentManifestVersion, CodexRelease: "0.146.0",
		CodexCommit: strings.Repeat("a", 40), AppServerSchemaSHA256: strings.Repeat("b", 64),
		AppServerSchemaDigestAlgorithm: runtimelock.AppServerSchemaDigestAlgorithmV1,
		ExecProtocolSourceSHA256:       strings.Repeat("c", 64),
		ExecServerBounds: runtimelock.ExecServerBounds{
			MaxStdioFrameBytes: 64 * 1024 * 1024, MaxJSONValues: 256 * 1024,
			ArgvEnvLimit:                  runtimelock.ArgvEnvLimitTransportAndPlatformOnly,
			RetainedOutputBytesPerProcess: 1024 * 1024, RetainedOutputChunksPerProcess: 50_000,
			RetainedStdinWriteIDsPerProcess: 4096, ExitedProcessRetentionMilliseconds: 30_000,
		},
		AgentxLimits: runtimelock.AgentxLimits{
			MaxFrameBytes: 8 * 1024 * 1024, MaxJSONValues: 64 * 1024,
			MaxArgvElements: 256, MaxArgvBytes: 16 * 1024, MaxEnvVariables: 256, MaxEnvBytes: 16 * 1024,
			MaxWriteIDBytes: 128, MaxOutputBufferBytesPerProcess: 8 * 1024 * 1024,
		},
		CheckpointAllowlistVersion: 1, AgentxProtocolVersion: "2.0",
		Artifacts: map[string]runtimelock.PlatformArtifacts{
			runtimelock.CurrentPlatform(): {
				Codex: runtimelock.FileArtifact{
					Path: "bin/codex", SourceURL: "https://example.test/codex/0.146.0/" + runtime.GOOS,
					SHA256: codexDigest, SizeBytes: codexSize,
				},
				ExternalExecutables: map[string]runtimelock.FileArtifact{},
			},
		},
	}
}

func assertGeneratedModes(t *testing.T, root string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if info.Mode().Perm() != 0o700 {
				t.Errorf("generated directory %s mode = %o", path, info.Mode().Perm())
			}
			return nil
		}
		if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			t.Errorf("generated file %s mode = %s", path, info.Mode())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func assertGeneratedTLS(t *testing.T, root string, now time.Time) {
	t.Helper()
	caPEM := mustReadFile(t, filepath.Join(root, certificateAuthorityFile))
	caBlock, _ := pem.Decode(caPEM)
	if caBlock == nil {
		t.Fatal("generated CA is not PEM")
	}
	ca, err := x509.ParseCertificate(caBlock.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	for _, service := range developmentServices {
		certificatePath := filepath.Join(root, "pki", service+".crt")
		keyPath := filepath.Join(root, "pki", service+".key")
		if _, err := tls.LoadX509KeyPair(certificatePath, keyPath); err != nil {
			t.Fatalf("load %s identity: %v", service, err)
		}
		block, _ := pem.Decode(mustReadFile(t, certificatePath))
		leaf, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		if len(leaf.URIs) != 1 || leaf.URIs[0].String() != "spiffe://"+developmentTrustDomain+"/ns/insecure-dev/sa/"+service {
			t.Fatalf("%s URI identities = %v", service, leaf.URIs)
		}
		if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: "127.0.0.1", CurrentTime: now}); err != nil {
			t.Fatalf("verify %s leaf: %v", service, err)
		}
	}
}

func readGeneratedEnvironment(t *testing.T, path string) map[string]string {
	t.Helper()
	value, err := ReadEnvironmentFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestManifestDigestUsesExactLoadedBytes(t *testing.T) {
	fixture := newConfigFixture(t)
	loaded, err := ValidateConfig(fixture.document)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(loaded.ManifestBytes)
	if loaded.ManifestSHA256 != hex.EncodeToString(digest[:]) {
		t.Fatalf("manifest digest = %s", loaded.ManifestSHA256)
	}
}

func TestInsecureDevelopmentStackJSONSchemaContract(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate devstack package")
	}
	schemaPath := filepath.Join(filepath.Dir(currentFile), "..", "..", "api", "schema", "insecure-dev-stack.schema.json")
	rawSchema := mustReadFile(t, schemaPath)
	var schema jsonschema.Schema
	if err := json.Unmarshal(rawSchema, &schema); err != nil {
		t.Fatal(err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	fixture := newConfigFixture(t)
	rawDocument, err := json.Marshal(fixture.document)
	if err != nil {
		t.Fatal(err)
	}
	var value any
	if err := json.Unmarshal(rawDocument, &value); err != nil {
		t.Fatal(err)
	}
	if err := resolved.Validate(value); err != nil {
		t.Fatalf("valid prepare input rejected by schema: %v", err)
	}
	object := value.(map[string]any)
	object["future"] = true
	if err := resolved.Validate(object); err == nil {
		t.Fatal("schema accepted an unknown top-level prepare field")
	}
}
