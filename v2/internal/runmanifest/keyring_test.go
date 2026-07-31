package runmanifest

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

func TestVerificationKeyringSupportsExplicitRotationOverlap(t *testing.T) {
	oldKey := manifestTestPrivateKey("old")
	newKey := manifestTestPrivateKey("new")
	raw := verificationKeyringJSON(t, []VerificationKeyDocument{
		verificationKeyDocument("cluster-key-old", oldKey),
		verificationKeyDocument("cluster-key-new", newKey),
	})
	keyring, err := ParseVerificationKeyring(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(keyring.KeyIDs(), []string{"cluster-key-new", "cluster-key-old"}) {
		t.Fatalf("key IDs = %v", keyring.KeyIDs())
	}
	for keyID, privateKey := range map[string]ed25519.PrivateKey{
		"cluster-key-old": oldKey,
		"cluster-key-new": newKey,
	} {
		signed, err := Sign(validManifest(t), keyID, privateKey)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := keyring.Verify(signed); err != nil {
			t.Fatalf("verify %s: %v", keyID, err)
		}
	}
	untrusted, err := Sign(validManifest(t), "cluster-key-future", manifestTestPrivateKey("future"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := keyring.Verify(untrusted); err == nil || !strings.Contains(err.Error(), "not trusted") {
		t.Fatalf("unknown-key error = %v", err)
	}
}

func TestVerificationKeyringRejectsAmbiguousDocuments(t *testing.T) {
	key := manifestTestPrivateKey("same")
	entry := verificationKeyDocument("cluster-key-1", key)
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "unknown field", raw: `{"version":1,"keys":[],"future":true}`, want: "unknown field"},
		{name: "duplicate JSON key", raw: `{"version":1,"version":1,"keys":[]}`, want: "duplicate JSON object key"},
		{name: "empty", raw: `{"version":1,"keys":[]}`, want: "between 1 and"},
		{name: "duplicate ID", raw: string(verificationKeyringJSON(t, []VerificationKeyDocument{entry, entry})), want: "repeats key ID"},
		{name: "algorithm", raw: string(verificationKeyringJSON(t, []VerificationKeyDocument{{KeyID: entry.KeyID, Algorithm: "future", PublicKey: entry.PublicKey}})), want: "algorithm"},
		{name: "public key", raw: string(verificationKeyringJSON(t, []VerificationKeyDocument{{KeyID: entry.KeyID, Algorithm: SignatureAlgorithm, PublicKey: "AA"}})), want: "32-byte"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ParseVerificationKeyring([]byte(test.raw)); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ParseVerificationKeyring() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestRunManifestVerificationKeyringJSONSchema(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate runmanifest package")
	}
	rawSchema, err := os.ReadFile(filepath.Join(filepath.Dir(currentFile), "..", "..", "api", "schema", "run-manifest-keyring.schema.json"))
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
	var value any
	if err := json.Unmarshal(verificationKeyringJSON(t, []VerificationKeyDocument{
		verificationKeyDocument("cluster-key-1", manifestTestPrivateKey("schema")),
	}), &value); err != nil {
		t.Fatal(err)
	}
	if err := resolved.Validate(value); err != nil {
		t.Fatalf("valid verification keyring rejected by schema: %v", err)
	}
}

func manifestTestPrivateKey(label string) ed25519.PrivateKey {
	seed := sha256.Sum256([]byte("run-manifest-keyring-" + label))
	return ed25519.NewKeyFromSeed(seed[:])
}

func verificationKeyDocument(keyID string, privateKey ed25519.PrivateKey) VerificationKeyDocument {
	return VerificationKeyDocument{
		KeyID: keyID, Algorithm: SignatureAlgorithm,
		PublicKey: base64.RawURLEncoding.EncodeToString(privateKey.Public().(ed25519.PublicKey)),
	}
}

func verificationKeyringJSON(t *testing.T, keys []VerificationKeyDocument) []byte {
	t.Helper()
	raw, err := json.Marshal(VerificationKeyringDocument{Version: VerificationKeyringVersion, Keys: keys})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
