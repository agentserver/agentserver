package llmproxy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestFileCredentialSourceReadsEveryRequestAndObservesAtomicRotation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("credential permission contract requires Unix modes")
	}
	path := filepath.Join(t.TempDir(), "upstream-credential")
	writeRestrictedCredential(t, path, "Bearer first-secret")
	source, err := NewFileCredentialSource(path, "Authorization")
	if err != nil {
		t.Fatal(err)
	}
	first, err := source.Credential(t.Context(), Principal{})
	if err != nil || first.HeaderName != "Authorization" || first.HeaderValue != "Bearer first-secret" {
		t.Fatalf("first credential = %+v, %v", first, err)
	}
	replacement := filepath.Join(filepath.Dir(path), "replacement")
	writeRestrictedCredential(t, replacement, "Bearer rotated-secret")
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	second, err := source.Credential(t.Context(), Principal{})
	if err != nil || second.HeaderValue != "Bearer rotated-secret" {
		t.Fatalf("rotated credential = %+v, %v", second, err)
	}
}

func TestFileCredentialSourceFailsClosedWithoutLeakingSecret(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("credential permission contract requires Unix modes")
	}
	for _, test := range []struct {
		name       string
		contents   string
		permission os.FileMode
	}{
		{name: "broad permissions", contents: "Bearer broad-secret", permission: 0o640},
		{name: "newline", contents: "Bearer newline-secret\n", permission: 0o600},
		{name: "empty", contents: "", permission: 0o600},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "credential")
			if err := os.WriteFile(path, []byte(test.contents), test.permission); err != nil {
				t.Fatal(err)
			}
			source, err := NewFileCredentialSource(path, "Authorization")
			if err != nil {
				t.Fatal(err)
			}
			_, err = source.Credential(t.Context(), Principal{})
			if err == nil || (test.contents != "" && strings.Contains(err.Error(), strings.TrimSpace(test.contents))) {
				t.Fatalf("unsafe credential error = %v", err)
			}
		})
	}
}

func TestFileCredentialSourceValidatesConstructionAndContext(t *testing.T) {
	if _, err := NewFileCredentialSource("relative", "Authorization"); err == nil {
		t.Fatal("relative credential path was accepted")
	}
	if _, err := NewFileCredentialSource("/absolute/credential", "X-Api-Key"); err == nil {
		t.Fatal("open-world credential header was accepted")
	}
	source, err := NewFileCredentialSource("/absolute/credential", "api-key")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := source.Credential(ctx, Principal{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled credential read error = %v", err)
	}
}

func writeRestrictedCredential(t *testing.T, path, value string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
}
