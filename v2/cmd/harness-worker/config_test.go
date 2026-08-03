package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/harnessworker"
	"github.com/agentserver/agentserver/v2/internal/runmanifest"
	"github.com/agentserver/agentserver/v2/internal/runtimelock"
)

func TestLoadWorkerDeploymentVerifiesPinnedArtifacts(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		fixture := newWorkerDeploymentFixture(t)
		deployment, err := loadWorkerDeployment(fixture.configPath, fixture.attemptRoot)
		if err != nil {
			t.Fatal(err)
		}
		if deployment.keyring == nil || deployment.preparer == nil || deployment.controlClient == nil || deployment.executorClient == nil {
			t.Fatalf("incomplete loaded worker deployment: %+v", deployment)
		}
		deployment.controlClient.CloseIdleConnections()
		deployment.executorClient.CloseIdleConnections()
	})

	t.Run("Codex runtime hash drift", func(t *testing.T) {
		fixture := newWorkerDeploymentFixture(t)
		if err := os.Chmod(fixture.codexPath, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fixture.codexPath, []byte("changed-codex"), 0o500); err != nil {
			t.Fatal(err)
		}
		if _, err := loadWorkerDeployment(fixture.configPath, fixture.attemptRoot); err == nil || !strings.Contains(err.Error(), "verify codex artifact") {
			t.Fatalf("runtime drift error = %v", err)
		}
	})

	t.Run("final exec hash drift", func(t *testing.T) {
		fixture := newWorkerDeploymentFixture(t)
		if err := os.Chmod(fixture.finalExecPath, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fixture.finalExecPath, []byte("changed-final-exec"), 0o500); err != nil {
			t.Fatal(err)
		}
		if _, err := loadWorkerDeployment(fixture.configPath, fixture.attemptRoot); err == nil || !strings.Contains(err.Error(), "harness-final-exec") {
			t.Fatalf("final-exec drift error = %v", err)
		}
	})

	t.Run("read-only Pod-visible TLS private key", func(t *testing.T) {
		fixture := newWorkerDeploymentFixture(t)
		if err := os.Chmod(fixture.tlsKeyPath, 0o444); err != nil {
			t.Fatal(err)
		}
		deployment, err := loadWorkerDeployment(fixture.configPath, fixture.attemptRoot)
		if err != nil {
			t.Fatalf("read-only Pod-visible TLS private key = %v", err)
		}
		deployment.controlClient.CloseIdleConnections()
		deployment.executorClient.CloseIdleConnections()
	})

	t.Run("writable TLS private key", func(t *testing.T) {
		fixture := newWorkerDeploymentFixture(t)
		if err := os.Chmod(fixture.tlsKeyPath, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := loadWorkerDeployment(fixture.configPath, fixture.attemptRoot); err == nil || !strings.Contains(err.Error(), "Pod-wide read-only") {
			t.Fatalf("writable TLS private key error = %v", err)
		}
	})

	t.Run("writable deployment config", func(t *testing.T) {
		fixture := newWorkerDeploymentFixture(t)
		if err := os.Chmod(fixture.configPath, 0o622); err != nil {
			t.Fatal(err)
		}
		if _, err := loadWorkerDeployment(fixture.configPath, fixture.attemptRoot); err == nil || !strings.Contains(err.Error(), "direct regular file") {
			t.Fatalf("writable deployment config error = %v", err)
		}
	})

	t.Run("writable final exec", func(t *testing.T) {
		fixture := newWorkerDeploymentFixture(t)
		if err := os.Chmod(fixture.finalExecPath, 0o522); err != nil {
			t.Fatal(err)
		}
		if _, err := loadWorkerDeployment(fixture.configPath, fixture.attemptRoot); err == nil || !strings.Contains(err.Error(), "deployment-immutable") {
			t.Fatalf("writable final-exec error = %v", err)
		}
	})
}

func TestWorkerDeploymentDocumentRejectsOpenEndedAuthority(t *testing.T) {
	valid := workerDeploymentDocument{
		Version:                workerDeploymentConfigVersion,
		RunManifestKeyringFile: "/etc/agentserver/run-manifest-keyring.json",
		RuntimeManifestFile:    "/opt/agentserver/runtime-manifest.json",
		RuntimeBundleRoot:      "/opt/agentserver/runtime",
		FinalExec: workerArtifactDocument{
			Path: "/opt/agentserver/bin/harness-final-exec", SHA256: strings.Repeat("a", 64), SizeBytes: 1024,
		},
		CodexConfigProfile: harnessworker.CodexConfigProfileStable0146,
		WorkerUID:          65531, WorkerGID: 65531, AppUID: 65532, AppGID: 65532,
		TLS: workerTLSDocument{
			CAFile: "/etc/agentserver/tls/ca.pem", CertificateFile: "/etc/agentserver/tls/tls.crt",
			KeyFile: "/etc/agentserver/tls/tls.key",
		},
	}
	if err := validateWorkerDeploymentDocument(valid); err != nil {
		t.Fatalf("valid worker deployment document: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*workerDeploymentDocument)
		want   string
	}{
		{name: "relative runtime root", mutate: func(d *workerDeploymentDocument) { d.RuntimeBundleRoot = "runtime" }, want: "absolute"},
		{name: "uppercase digest", mutate: func(d *workerDeploymentDocument) { d.FinalExec.SHA256 = strings.Repeat("A", 64) }, want: "canonical SHA-256"},
		{name: "shared identity", mutate: func(d *workerDeploymentDocument) { d.AppUID = d.WorkerUID }, want: "distinct"},
		{name: "future config profile", mutate: func(d *workerDeploymentDocument) { d.CodexConfigProfile = "future" }, want: "unsupported"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			test.mutate(&candidate)
			if err := validateWorkerDeploymentDocument(candidate); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validation error = %v, want %q", err, test.want)
			}
		})
	}
}

type workerDeploymentFixture struct {
	configPath    string
	attemptRoot   string
	codexPath     string
	finalExecPath string
	tlsKeyPath    string
}

func newWorkerDeploymentFixture(t *testing.T) workerDeploymentFixture {
	t.Helper()
	root := t.TempDir()
	bundleRoot := filepath.Join(root, "runtime")
	if err := os.MkdirAll(filepath.Join(bundleRoot, "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	codexPath := filepath.Join(bundleRoot, "bin", "codex")
	if err := os.WriteFile(codexPath, []byte("pinned-stock-codex"), 0o500); err != nil {
		t.Fatal(err)
	}
	codexDigest, codexSize, err := runtimelock.HashFile(codexPath)
	if err != nil {
		t.Fatal(err)
	}
	runtimeManifest := workerTestRuntimeManifest(runtimelock.CurrentPlatform(), codexDigest, codexSize)
	runtimeBytes, err := json.Marshal(runtimeManifest)
	if err != nil {
		t.Fatal(err)
	}
	runtimeManifestPath := filepath.Join(root, "runtime-manifest.json")
	if err := os.WriteFile(runtimeManifestPath, runtimeBytes, 0o400); err != nil {
		t.Fatal(err)
	}

	finalExecPath := filepath.Join(root, "harness-final-exec")
	if err := os.WriteFile(finalExecPath, []byte("pinned-harness-final-exec"), 0o500); err != nil {
		t.Fatal(err)
	}
	finalExecDigest, finalExecSize, err := runtimelock.HashFile(finalExecPath)
	if err != nil {
		t.Fatal(err)
	}

	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keyringBytes, err := json.Marshal(runmanifest.VerificationKeyringDocument{
		Version: runmanifest.VerificationKeyringVersion,
		Keys: []runmanifest.VerificationKeyDocument{{
			KeyID: "worker-test-key", Algorithm: runmanifest.SignatureAlgorithm,
			PublicKey: base64.RawURLEncoding.EncodeToString(publicKey),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	keyringPath := filepath.Join(root, "keyring.json")
	if err := os.WriteFile(keyringPath, keyringBytes, 0o400); err != nil {
		t.Fatal(err)
	}

	caPath, certificatePath, keyPath := writeWorkerTestTLS(t, root)
	attemptRoot := filepath.Join(root, "attempt")
	if err := os.Mkdir(attemptRoot, 0o701); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(attemptRoot, 0o701); err != nil {
		t.Fatal(err)
	}
	document := workerDeploymentDocument{
		Version:                workerDeploymentConfigVersion,
		RunManifestKeyringFile: keyringPath,
		RuntimeManifestFile:    runtimeManifestPath,
		RuntimeBundleRoot:      bundleRoot,
		FinalExec: workerArtifactDocument{
			Path: finalExecPath, SHA256: finalExecDigest, SizeBytes: finalExecSize,
		},
		CodexConfigProfile: harnessworker.CodexConfigProfileStable0146,
		WorkerUID:          65531, WorkerGID: 65531, AppUID: 65532, AppGID: 65532,
		TLS: workerTLSDocument{CAFile: caPath, CertificateFile: certificatePath, KeyFile: keyPath},
	}
	configBytes, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "worker.json")
	if err := os.WriteFile(configPath, configBytes, 0o400); err != nil {
		t.Fatal(err)
	}
	return workerDeploymentFixture{
		configPath: configPath, attemptRoot: attemptRoot, codexPath: codexPath,
		finalExecPath: finalExecPath, tlsKeyPath: keyPath,
	}
}

func workerTestRuntimeManifest(platform, codexDigest string, codexSize int64) runtimelock.Manifest {
	return runtimelock.Manifest{
		ManifestVersion:                runtimelock.CurrentManifestVersion,
		CodexRelease:                   "0.146.0",
		CodexCommit:                    strings.Repeat("a", 40),
		AppServerSchemaSHA256:          strings.Repeat("b", 64),
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
			MaxArgvElements: 256, MaxArgvBytes: 16 * 1024,
			MaxEnvVariables: 256, MaxEnvBytes: 16 * 1024,
			MaxWriteIDBytes: 128, MaxOutputBufferBytesPerProcess: 8 * 1024 * 1024,
		},
		CheckpointAllowlistVersion: 1,
		AgentxProtocolVersion:      "2.0",
		Artifacts: map[string]runtimelock.PlatformArtifacts{
			platform: {
				Codex: runtimelock.FileArtifact{
					Path: "bin/codex", SourceURL: "https://example.test/stock-codex/0.146.0/" + runtime.GOOS,
					SHA256: codexDigest, SizeBytes: codexSize,
				},
				ExternalExecutables: map[string]runtimelock.FileArtifact{},
			},
		},
	}
}

func writeWorkerTestTLS(t *testing.T, root string) (caPath, certificatePath, keyPath string) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "worker-test"},
		NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true, IsCA: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, publicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	caPath = filepath.Join(root, "ca.pem")
	certificatePath = filepath.Join(root, "tls.crt")
	keyPath = filepath.Join(root, "tls.key")
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(caPath, certificatePEM, 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certificatePath, certificatePEM, 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}), 0o400); err != nil {
		t.Fatal(err)
	}
	return caPath, certificatePath, keyPath
}
