package enrollmenttoken

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLoadCodecRequiresRestrictedCanonicalKeyFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission contract")
	}
	directory := t.TempDir()
	valid := filepath.Join(directory, "enrollment.key")
	encoded := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	if err := os.WriteFile(valid, []byte(encoded), 0o600); err != nil {
		t.Fatal(err)
	}
	codec, err := LoadCodec("issuer", valid)
	if err != nil || codec.Issuer() != "issuer" {
		t.Fatalf("load valid codec = %v / %v", codec, err)
	}
	for name, contents := range map[string]string{
		"newline": encoded + "\n",
		"short":   base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("s", 31))),
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(directory, name)
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadCodec("issuer", path); err == nil {
				t.Fatal("unsafe key file was accepted")
			}
		})
	}
	broad := filepath.Join(directory, "broad")
	if err := os.WriteFile(broad, []byte(encoded), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCodec("issuer", broad); err == nil {
		t.Fatal("broad key permissions were accepted")
	}
	symlink := filepath.Join(directory, "symlink")
	if err := os.Symlink(valid, symlink); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCodec("issuer", symlink); err == nil {
		t.Fatal("symlinked key file was accepted")
	}
}
