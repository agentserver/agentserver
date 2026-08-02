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
	projectionDirectory := filepath.Join(directory, "..2026_08_02")
	if err := os.Mkdir(projectionDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	projectedTarget := filepath.Join(projectionDirectory, "enrollment.key")
	if err := os.WriteFile(projectedTarget, []byte(encoded), 0o600); err != nil {
		t.Fatal(err)
	}
	projection := filepath.Join(directory, "projected.key")
	if err := os.Symlink(filepath.Join(filepath.Base(projectionDirectory), "enrollment.key"), projection); err != nil {
		t.Fatal(err)
	}
	if codec, err := LoadCodec("issuer", projection); err != nil || codec.Issuer() != "issuer" {
		t.Fatalf("restricted projected Secret = %v / %v", codec, err)
	}
	if err := os.Chmod(projectedTarget, 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCodec("issuer", projection); err == nil {
		t.Fatal("broad projected Secret target was accepted")
	}
}
