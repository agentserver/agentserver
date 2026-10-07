package productiondeploy

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/stockruntime"
)

func TestKubernetesCodexUpgradeIsExplicitAndPreservesDeployment(t *testing.T) {
	base, err := ValidateConfig(kubernetesConfigDocument())
	if err != nil {
		t.Fatal(err)
	}
	d := base.Document
	d.Runtime.RuntimeManifestSHA256 = stockruntime.PreviousManifestSHA256
	previous := ManagedCompatibilityRuntimeDocument{CodexRelease: "0.146.0", CodexCommit: "e363b08c9175ac1cbe5893615dd2cb9ddf95043b", CodexSHA256: "2e863156ed35ecc5253b1e2f907a9143077b9f7cb51942070c61996471ff6e04"}
	d.Managed.Environment.Compatibility = previous
	d.SandboxProfiles[0].Environment.Compatibility = previous
	path := filepath.Join(t.TempDir(), "production.json")
	write := func() {
		raw, err := json.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write()
	if _, err := LoadKubernetesReleaseBase(path, false); err == nil {
		t.Fatal("implicit upgrade accepted")
	}
	got, err := LoadKubernetesReleaseBase(path, true)
	if err != nil {
		t.Fatal(err)
	}
	want := d
	want.Runtime.RuntimeManifestSHA256 = stockruntime.ManifestSHA256
	want.Managed.Environment.Compatibility = ManagedCompatibilityRuntimeDocument{CodexRelease: stockruntime.CodexRelease, CodexCommit: stockruntime.CodexCommit, CodexSHA256: stockruntime.LinuxAMD64CodexSHA256}
	want.SandboxProfiles = append([]ManagedSandboxProfileDocument(nil), d.SandboxProfiles...)
	want.SandboxProfiles[0].Environment.Compatibility = want.Managed.Environment.Compatibility
	gotJSON, _ := json.Marshal(got.Document)
	wantJSON, _ := json.Marshal(want)
	if !bytes.Equal(gotJSON, wantJSON) {
		t.Fatal("upgrade changed fields outside runtime identity")
	}
	if _, err := PrepareKubernetesRelease(got, KubernetesRelease{HarnessImage: got.Document.Images.Harness, UpgradeCodex: true}); err == nil {
		t.Fatal("upgrade accepted unchanged harness image")
	}
	d.Runtime.RuntimeManifestSHA256 = strings.Repeat("f", 64)
	write()
	if _, err := LoadKubernetesReleaseBase(path, true); err == nil {
		t.Fatal("uncharacterized source accepted")
	}
}
