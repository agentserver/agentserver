package runcapability

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

func TestLoadProductionSignerAcceptsRestrictedSeedPrivateKeyAndPKCS8(t *testing.T) {
	privateKey := ed25519.NewKeyFromSeed(bytesOf(0x91, ed25519.SeedSize))
	pkcs8, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		raw  []byte
	}{
		{name: "seed", raw: privateKey.Seed()},
		{name: "private key", raw: privateKey},
		{name: "PKCS8", raw: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "run-capability.key")
			if err := os.WriteFile(path, test.raw, 0o600); err != nil {
				t.Fatal(err)
			}
			signer, err := LoadProductionSigner(productionTestIssuer, productionTestKeyID, path)
			if err != nil {
				t.Fatal(err)
			}
			verifier, err := NewProductionVerifier(
				productionTestIssuer,
				map[string]ed25519.PublicKey{productionTestKeyID: privateKey.Public().(ed25519.PublicKey)},
			)
			if err != nil {
				t.Fatal(err)
			}
			token, err := signer.Sign(productionTestClaims(AudienceExecutorMCP))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := verifier.Verify(token, AudienceExecutorMCP, productionTestNow); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestLoadProductionSignerRejectsUnsafeFilesAndKeyMaterial(t *testing.T) {
	validSeed := bytesOf(0x92, ed25519.SeedSize)
	noncanonical := ed25519.NewKeyFromSeed(validSeed)
	noncanonical[len(noncanonical)-1] ^= 1
	nonEd25519, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	nonEd25519PKCS8, err := x509.MarshalPKCS8PrivateKey(nonEd25519)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		raw  []byte
		mode os.FileMode
		want string
	}{
		{name: "broad permissions", raw: validSeed, mode: 0o640, want: "group or other"},
		{name: "zero seed", raw: make([]byte, ed25519.SeedSize), mode: 0o600, want: "all zero"},
		{name: "noncanonical private", raw: noncanonical, mode: 0o600, want: "not canonical"},
		{name: "unsupported", raw: []byte("not-a-key"), mode: 0o600, want: "raw Ed25519"},
		{name: "non-Ed25519 PKCS8", raw: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: nonEd25519PKCS8}), mode: 0o600, want: "not Ed25519"},
		{name: "multiple PEM", raw: append(
			pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("invalid")}),
			pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("second")})...,
		), mode: 0o600, want: "one unencrypted"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "run-capability.key")
			if err := os.WriteFile(path, test.raw, test.mode); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, test.mode); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadProductionSigner(productionTestIssuer, productionTestKeyID, path); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("LoadProductionSigner() error = %v, want %q", err, test.want)
			}
		})
	}
	if _, err := LoadProductionSigner(productionTestIssuer, productionTestKeyID, "relative.key"); err == nil {
		t.Fatal("relative production signing key path was accepted")
	}
	directory := t.TempDir()
	if _, err := LoadProductionSigner(productionTestIssuer, productionTestKeyID, directory); err == nil {
		t.Fatal("directory production signing key was accepted")
	}
}

func TestParseProductionVerifierSupportsExplicitRotationOverlap(t *testing.T) {
	oldKey := ed25519.NewKeyFromSeed(bytesOf(0x93, ed25519.SeedSize))
	newKey := ed25519.NewKeyFromSeed(bytesOf(0x94, ed25519.SeedSize))
	raw := productionKeyringJSON(t, []ProductionVerificationKeyDocument{
		productionVerificationKey("run-capability-old", oldKey),
		productionVerificationKey("run-capability-new", newKey),
	})
	verifier, err := ParseProductionVerifier(productionTestIssuer, raw)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(verifier.KeyIDs(), []string{"run-capability-new", "run-capability-old"}) {
		t.Fatalf("production verifier key IDs = %v", verifier.KeyIDs())
	}
	for keyID, privateKey := range map[string]ed25519.PrivateKey{
		"run-capability-old": oldKey,
		"run-capability-new": newKey,
	} {
		signer, err := NewProductionSigner(productionTestIssuer, keyID, privateKey)
		if err != nil {
			t.Fatal(err)
		}
		token, err := signer.Sign(productionTestClaims(AudienceLLMProxy))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := verifier.Verify(token, AudienceLLMProxy, productionTestNow); err != nil {
			t.Fatalf("verify %s: %v", keyID, err)
		}
	}
}

func TestLoadProductionVerifierRequiresAbsoluteBoundedRegularFile(t *testing.T) {
	privateKey := ed25519.NewKeyFromSeed(bytesOf(0x96, ed25519.SeedSize))
	raw := productionKeyringJSON(t, []ProductionVerificationKeyDocument{
		productionVerificationKey(productionTestKeyID, privateKey),
	})
	path := filepath.Join(t.TempDir(), "keyring.json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	verifier, err := LoadProductionVerifier(productionTestIssuer, path)
	if err != nil || !slices.Equal(verifier.KeyIDs(), []string{productionTestKeyID}) {
		t.Fatalf("LoadProductionVerifier() = %+v, %v", verifier, err)
	}
	if _, err := LoadProductionVerifier(productionTestIssuer, "relative.json"); err == nil {
		t.Fatal("relative keyring path was accepted")
	}
	if _, err := LoadProductionVerifier(productionTestIssuer, t.TempDir()); err == nil {
		t.Fatal("directory keyring was accepted")
	}
	empty := filepath.Join(t.TempDir(), "empty.json")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadProductionVerifier(productionTestIssuer, empty); err == nil {
		t.Fatal("empty keyring was accepted")
	}
}

func TestParseProductionVerifierRejectsOpenWorldAndAmbiguousKeyrings(t *testing.T) {
	privateKey := ed25519.NewKeyFromSeed(bytesOf(0x95, ed25519.SeedSize))
	entry := productionVerificationKey(productionTestKeyID, privateKey)
	for _, test := range []struct {
		name string
		raw  string
		want string
	}{
		{name: "unknown", raw: `{"version":1,"keys":[],"future":true}`, want: "unknown field"},
		{name: "duplicate JSON", raw: `{"version":1,"version":1,"keys":[]}`, want: "duplicate"},
		{name: "version", raw: `{"version":2,"keys":[]}`, want: "version"},
		{name: "empty", raw: `{"version":1,"keys":[]}`, want: "between 1 and"},
		{name: "duplicate ID", raw: string(productionKeyringJSON(t, []ProductionVerificationKeyDocument{entry, entry})), want: "repeats"},
		{name: "algorithm", raw: string(productionKeyringJSON(t, []ProductionVerificationKeyDocument{{KeyID: entry.KeyID, Algorithm: "future", PublicKey: entry.PublicKey}})), want: "algorithm"},
		{name: "public key", raw: string(productionKeyringJSON(t, []ProductionVerificationKeyDocument{{KeyID: entry.KeyID, Algorithm: ProductionSignatureAlgorithm, PublicKey: "AA"}})), want: "32-byte"},
		{name: "zero public key", raw: string(productionKeyringJSON(t, []ProductionVerificationKeyDocument{{KeyID: entry.KeyID, Algorithm: ProductionSignatureAlgorithm, PublicKey: base64.RawURLEncoding.EncodeToString(make([]byte, 32))}})), want: "all zero"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ParseProductionVerifier(productionTestIssuer, []byte(test.raw)); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ParseProductionVerifier() error = %v, want %q", err, test.want)
			}
		})
	}
	if _, err := ParseProductionVerifier("", productionKeyringJSON(t, []ProductionVerificationKeyDocument{entry})); err == nil {
		t.Fatal("empty expected production issuer was accepted")
	}
}

func TestProductionCapabilityKeyringJSONSchemaContract(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate runcapability package")
	}
	rawSchema, err := os.ReadFile(filepath.Join(filepath.Dir(currentFile), "..", "..", "api", "schema", "run-capability-keyring.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(rawSchema, &schema); err != nil {
		t.Fatal(err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	privateKey := ed25519.NewKeyFromSeed(bytesOf(0x96, ed25519.SeedSize))
	var document any
	if err := json.Unmarshal(productionKeyringJSON(t, []ProductionVerificationKeyDocument{
		productionVerificationKey(productionTestKeyID, privateKey),
	}), &document); err != nil {
		t.Fatal(err)
	}
	if err := resolved.Validate(document); err != nil {
		t.Fatalf("valid production capability keyring rejected by schema: %v", err)
	}
}

func productionVerificationKey(keyID string, privateKey ed25519.PrivateKey) ProductionVerificationKeyDocument {
	return ProductionVerificationKeyDocument{
		KeyID: keyID, Algorithm: ProductionSignatureAlgorithm,
		PublicKey: base64.RawURLEncoding.EncodeToString(privateKey.Public().(ed25519.PublicKey)),
	}
}

func productionKeyringJSON(t *testing.T, keys []ProductionVerificationKeyDocument) []byte {
	t.Helper()
	raw, err := json.Marshal(ProductionKeyringDocument{Version: ProductionKeyringVersion, Keys: keys})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
