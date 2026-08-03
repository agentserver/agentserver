package harnesspool

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/runmanifest"
)

func TestLoadEd25519ManifestSignerAcceptsSeedAndPKCS8(t *testing.T) {
	seed := sha256.Sum256([]byte("manifest-key-loader"))
	privateKey := ed25519.NewKeyFromSeed(seed[:])
	pkcs8, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		raw  []byte
		mode os.FileMode
	}{
		{name: "seed", raw: seed[:], mode: 0o600},
		{name: "group-readable Secret", raw: seed[:], mode: 0o440},
		{name: "private", raw: privateKey, mode: 0o600},
		{name: "pkcs8", raw: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8}), mode: 0o600},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "manifest-signing-key")
			if err := os.WriteFile(path, test.raw, test.mode); err != nil {
				t.Fatal(err)
			}
			signer, err := LoadEd25519ManifestSigner("cluster-key-2026-07", path)
			if err != nil {
				t.Fatal(err)
			}
			signed, err := signer.SignRunManifest(validManifestForKeyLoader(t))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := signed.Verify("cluster-key-2026-07", privateKey.Public().(ed25519.PublicKey)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestLoadEd25519ManifestSignerRejectsUnsafeFilesAndKeys(t *testing.T) {
	seed := sha256.Sum256([]byte("manifest-key-loader"))
	zeroPrivateKey := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	zeroPKCS8, err := x509.MarshalPKCS8PrivateKey(zeroPrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		raw  []byte
		mode os.FileMode
		want string
	}{
		{name: "broad permissions", raw: seed[:], mode: 0o644, want: "inaccessible to other"},
		{name: "zero seed", raw: make([]byte, ed25519.SeedSize), mode: 0o600, want: "all zero"},
		{name: "zero seed private", raw: zeroPrivateKey, mode: 0o600, want: "all zero"},
		{name: "zero seed PKCS8", raw: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: zeroPKCS8}), mode: 0o600, want: "all zero"},
		{name: "unsupported text", raw: []byte("not-a-private-key"), mode: 0o600, want: "raw Ed25519"},
		{name: "noncanonical private", raw: append(seed[:], make([]byte, ed25519.PublicKeySize)...), mode: 0o600, want: "not canonical"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "manifest-signing-key")
			if err := os.WriteFile(path, test.raw, test.mode); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, test.mode); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadEd25519ManifestSigner("cluster-key-1", path); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("LoadEd25519ManifestSigner() error = %v, want %q", err, test.want)
			}
		})
	}
	if _, err := LoadEd25519ManifestSigner("cluster-key-1", "relative-key"); err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("relative path error = %v", err)
	}
	path := filepath.Join(t.TempDir(), "manifest-signing-key")
	if err := os.WriteFile(path, seed[:], 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadEd25519ManifestSigner("bad\x00key", path); err == nil || !strings.Contains(err.Error(), "without NUL") {
		t.Fatalf("invalid key ID error = %v", err)
	}
	if _, err := NewEd25519ManifestSigner("cluster-key-1", append(seed[:], make([]byte, ed25519.PublicKeySize)...)); err == nil || !strings.Contains(err.Error(), "not canonical") {
		t.Fatalf("noncanonical direct signer key error = %v", err)
	}
}

func validManifestForKeyLoader(t *testing.T) runmanifest.Manifest {
	t.Helper()
	inputs := testRunLaunchInputs()
	proposal, err := BuildExecutorCatalog(inputs.ExecutorCatalogPolicy)
	if err != nil {
		t.Fatal(err)
	}
	executorMCP, err := runmanifest.ExecutorMCPFromCatalog(
		inputs.ExecutorMCPEndpoint,
		inputs.ExecutorMCPTLSIdentity,
		inputs.ExecutorMCPAudience,
		proposal.ContractVersion,
		"45000000-0000-4000-8000-000000000004",
		proposal.Catalog,
	)
	if err != nil {
		t.Fatal(err)
	}
	return runmanifest.Manifest{
		ManifestVersion: runmanifest.CurrentVersion, CanonicalizerVersion: runmanifest.Canonicalizer,
		WorkspaceID: "40000000-0000-4000-8000-000000000004", SessionID: testSessionID,
		RunID: testRunID, RunAttemptID: testRunAttemptID, RunAttemptGeneration: 1, HolderID: "pool-instance",
		Prompt: inputs.Prompt, CodexRuntimeManifestDigest: inputs.CodexRuntimeManifestDigest,
		Model: inputs.Model, ExecutorMCP: executorMCP,
		ExecutorPolicy: runmanifest.ExecutorPolicy{Version: proposal.PolicyVersion, ContextDigest: strings.Repeat("d", 64)},
		Limits:         inputs.Limits, CheckpointAllowlistVersion: inputs.CheckpointAllowlistVersion,
		WorkerImageDigest: inputs.WorkerImageDigest, ExpectedServiceAccount: inputs.ExpectedServiceAccount,
		ControllerCallback: runmanifest.ControllerCallback{
			Endpoint: inputs.ControllerCallbackEndpoint, TLSIdentity: inputs.ControllerCallbackIdentity,
			Audience: inputs.ControllerCallbackAudience, HolderID: "pool-instance",
		},
	}
}
