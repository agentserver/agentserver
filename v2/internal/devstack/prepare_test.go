package devstack

import (
	"bytes"
	"context"
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
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/coreserver"
	"github.com/agentserver/agentserver/v2/internal/devfixtures"
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
		result.WorkerDeploymentFile != filepath.Join(output, workerDeploymentConfigFile) ||
		result.FixturesConfigFile != filepath.Join(output, developmentFixturesConfigFile) ||
		result.BrowserBearerFile != filepath.Join(output, browserBearerTokenFile) {
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
		poolEnvironment["AGENTSERVER_V2_CODEX_RUNTIME_MANIFEST_SHA256"] != loaded.ManifestSHA256 ||
		poolEnvironment["AGENTSERVER_V2_HARNESS_PRIVILEGED_FORK"] != "true" ||
		poolEnvironment["AGENTSERVER_V2_HARNESS_WORKER_UID"] != "65531" ||
		poolEnvironment["AGENTSERVER_V2_HARNESS_WORKER_GID"] != "65531" {
		t.Fatalf("generated service environments do not preserve config: core=%v executor=%v pool=%v", coreEnvironment, executorEnvironment, poolEnvironment)
	}
	capabilityKey := executorEnvironment["AGENTSERVER_V2_DEV_RUN_CAPABILITY_KEY"]
	if capabilityKey == "" || poolEnvironment["AGENTSERVER_V2_DEV_RUN_CAPABILITY_KEY"] != capabilityKey {
		t.Fatal("executor-gateway and harness-pool do not share one generated capability key")
	}
	if _, err := runcapability.NewDevelopmentCodecFromBase64Key(capabilityKey); err != nil {
		t.Fatalf("generated run capability key: %v", err)
	}
	fixtures, err := devfixtures.LoadBundle(output)
	if err != nil {
		t.Fatalf("load generated development fixtures: %v", err)
	}
	fixtures.Close()
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
	browserBearer := mustReadFile(t, result.BrowserBearerFile)
	browserBearer = bytes.TrimSuffix(browserBearer, []byte("\n"))
	if len(browserBearer) == 0 {
		t.Fatal("generated browser bearer is empty")
	}

	workerBytes := mustReadFile(t, result.WorkerDeploymentFile)
	agentxBytes := mustReadFile(t, result.AgentxLaunchFile)
	fixturesBytes := mustReadFile(t, result.FixturesConfigFile)
	metadataBytes := mustReadFile(t, result.MetadataFile)
	for name, secret := range map[string][]byte{
		"capability HMAC": []byte(capabilityKey),
		"cursor HMAC":     []byte(coreEnvironment["AGENTSERVER_V2_RUN_CURSOR_KEY"]),
		"manifest seed":   seed,
		"browser bearer":  browserBearer,
	} {
		for target, contents := range map[string][]byte{
			"worker deployment": workerBytes, "agentx launch": agentxBytes,
			"fixture config": fixturesBytes, "metadata": metadataBytes,
		} {
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

func TestPreparedDevelopmentFixturesServeCoreIntrospectionAndTLSResponses(t *testing.T) {
	fixture := newConfigFixture(t)
	hydraAddress := unusedLoopbackAddress(t)
	llmproxyAddress := unusedLoopbackAddress(t)
	for llmproxyAddress == hydraAddress {
		llmproxyAddress = unusedLoopbackAddress(t)
	}
	fixture.document.Network.HydraIntrospectionURL = "http://" + hydraAddress + "/oauth2/introspect"
	fixture.document.Network.LLMProxyEndpoint = "https://" + llmproxyAddress + "/v1"
	loaded, err := ValidateConfig(fixture.document)
	if err != nil {
		t.Fatal(err)
	}
	createdAt := time.Now().UTC()
	output := filepath.Join(fixture.root, "served-stack")
	prepared, err := Prepare(loaded, output, rand.Reader, createdAt)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := devfixtures.LoadBundle(prepared.OutputDirectory)
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Close()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ready := &fixtureReadyWriter{ready: make(chan struct{})}
	serveDone := make(chan error, 1)
	go func() { serveDone <- bundle.Serve(ctx, ready) }()
	select {
	case <-ready.ready:
	case <-time.After(3 * time.Second):
		t.Fatal("development fixtures did not report readiness")
	}

	browserBearer := strings.TrimSuffix(string(mustReadFile(t, prepared.BrowserBearerFile)), "\n")
	introspector, err := coreserver.NewHydraUserIntrospector(
		fixture.document.Network.HydraIntrospectionURL,
		&http.Client{Timeout: 2 * time.Second},
		true,
	)
	if err != nil {
		t.Fatal(err)
	}
	introspection, err := introspector.IntrospectUserToken(t.Context(), browserBearer)
	if err != nil {
		t.Fatal(err)
	}
	if !introspection.Active || introspection.Subject != fixture.document.Authority.ActorID ||
		len(introspection.Audience) != 1 || introspection.Audience[0] != devfixtures.BrowserTokenAudience ||
		introspection.Scope != devfixtures.BrowserTokenScope || introspection.ExpiresAt <= time.Now().Unix() {
		t.Fatalf("served development introspection = %+v", introspection)
	}

	poolEnvironment := readGeneratedEnvironment(t, prepared.EnvironmentFiles["harness-pool"])
	codec, err := runcapability.NewDevelopmentCodecFromBase64Key(poolEnvironment["AGENTSERVER_V2_DEV_RUN_CAPABILITY_KEY"])
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	modelCapability, err := codec.Sign(runcapability.Claims{
		Version: runcapability.DevelopmentVersion, CapabilityID: "70000000-0000-4000-8000-000000000007",
		Audience: runcapability.AudienceLLMProxy, WorkspaceID: fixture.document.Authority.WorkspaceID,
		SessionID: fixture.document.Authority.SessionID, RunID: "80000000-0000-4000-8000-000000000008",
		RunAttemptID: "90000000-0000-4000-8000-000000000009", RunAttemptGeneration: 1,
		ActorID: fixture.document.Authority.ActorID, HolderID: "fixture-holder",
		IssuedAtUnixMS: now.Add(-time.Minute).UnixMilli(), ExpiresAtUnixMS: now.Add(time.Hour).UnixMilli(),
		Model: fixture.document.Model.Name, Provider: fixture.document.Model.Provider,
	})
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(mustReadFile(t, filepath.Join(output, certificateAuthorityFile))) {
		t.Fatal("generated development CA could not be loaded")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{
		MinVersion: tls.VersionTLS13, RootCAs: roots, ServerName: "127.0.0.1",
		Time: func() time.Time { return createdAt },
	}}
	defer transport.CloseIdleConnections()
	modelClient := &http.Client{Transport: transport, Timeout: 2 * time.Second}
	body := `{"model":"gpt-5","stream":true,"input":[],"tools":[{"type":"namespace","name":"executor","tools":[{"type":"function","name":"list_environments"}]}]}`
	request, err := http.NewRequestWithContext(
		t.Context(), http.MethodPost, fixture.document.Network.LLMProxyEndpoint+"/responses", strings.NewReader(body),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+modelCapability)
	response, err := modelClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	responseBody, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil {
		t.Fatal(errors.Join(readErr, closeErr))
	}
	if response.StatusCode != http.StatusOK || !bytes.Contains(responseBody, []byte(`"namespace":"executor"`)) ||
		!bytes.Contains(responseBody, []byte(`"name":"list_environments"`)) {
		t.Fatalf("served TLS Responses result = %d %s", response.StatusCode, responseBody)
	}

	cancel()
	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("development fixtures did not stop after cancellation")
	}
}

type fixtureReadyWriter struct {
	once  sync.Once
	ready chan struct{}
}

func (writer *fixtureReadyWriter) Write(value []byte) (int, error) {
	writer.once.Do(func() { close(writer.ready) })
	return len(value), nil
}

func unusedLoopbackAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
			t.Skipf("loopback listeners are unavailable in this test environment: %v", err)
		}
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
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

func TestValidateConfigRejectsUnlaunchableDevelopmentFixtures(t *testing.T) {
	fixture := newConfigFixture(t)
	tests := []struct {
		name   string
		mutate func(*ConfigDocument)
		want   string
	}{
		{
			name: "Hydra conflicts with Core",
			mutate: func(document *ConfigDocument) {
				document.Network.HydraIntrospectionURL = "http://" + document.Network.CoreListenAddress + "/oauth2/introspect"
			},
			want: "conflicts",
		},
		{
			name: "llmproxy conflicts with Hydra",
			mutate: func(document *ConfigDocument) {
				document.Network.LLMProxyEndpoint = "https://127.0.0.1:17447/v1"
			},
			want: "conflicts",
		},
		{
			name: "llmproxy omits port",
			mutate: func(document *ConfigDocument) {
				document.Network.LLMProxyEndpoint = "https://127.0.0.1/v1"
			},
			want: "explicit non-zero port",
		},
		{
			name: "script omits list environments",
			mutate: func(document *ConfigDocument) {
				document.Policy.AllowedTools = []string{"read_file"}
			},
			want: "must include list_environments",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := fixture.document
			test.mutate(&document)
			if _, err := ValidateConfig(document); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validation error = %v, want %q", err, test.want)
			}
		})
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
