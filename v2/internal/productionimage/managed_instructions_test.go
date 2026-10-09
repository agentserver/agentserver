package productionimage

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestKubernetesManagedInstructionStagingUsesCurrentSource(t *testing.T) {
	_, source, _, _ := runtime.Caller(0)
	v2Root := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	script := filepath.Join(v2Root, "deploy", "production", "prepare-managed-instructions.sh")
	for _, kind := range []string{"harness", "runtime"} {
		t.Run(kind, func(t *testing.T) {
			packs := filepath.Join(t.TempDir(), kind, "packs")
			if out, err := exec.Command("bash", script, packs).CombinedOutput(); err != nil {
				t.Fatalf("stage current instructions: %v\n%s", err, out)
			}
			for _, skill := range []string{"managed-cli-readonly", "lark-readonly"} {
				want, err := os.ReadFile(filepath.Join(v2Root, "deploy", "production", skill+".SKILL.md"))
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(packs, skill, "SKILL.md")
				got, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(got, want) {
					t.Fatalf("%s did not stage current source", skill)
				}
				info, err := os.Stat(path)
				if err != nil || info.Mode().Perm() != 0o444 {
					t.Fatalf("%s instruction mode is not immutable", skill)
				}
			}
			if _, err := exec.Command("bash", script, packs).CombinedOutput(); err == nil {
				t.Fatal("instruction staging silently overwrote an existing build artifact")
			}
		})
	}
}
