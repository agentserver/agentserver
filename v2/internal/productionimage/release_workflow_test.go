package productionimage

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestProductionWorkflowPublishesAndLocksManagedSandbox(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate production workflow contract test")
	}
	v2Root := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	workflow, err := os.ReadFile(filepath.Join(v2Root, "..", ".github", "workflows", "v2-production.yml"))
	if err != nil {
		t.Fatal(err)
	}
	skill, err := os.ReadFile(filepath.Join(v2Root, "deploy", "production", "lark-readonly.SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	skillDigest := sha256.Sum256(skill)
	for _, required := range []string{
		"MANAGED_SANDBOX_REPOSITORY: ghcr.io/agentserver/v2-managed-sandbox",
		"LARK_CLI_SHA256: " + ManagedLarkCLISHA256,
		"LARK_SKILL_SHA256: " + hex.EncodeToString(skillDigest[:]),
		`--lark-cli="${LARK_CLI_PATH}"`,
		`--lark-skill="${LARK_SKILL_PATH}"`,
		`--managed-sandbox-image="${MANAGED_SANDBOX_REPOSITORY}:sha-${GITHUB_SHA}"`,
		"agentserver-deploy lock-release",
		"agentserver-image verify-managed-release",
		`--harness-manifest="${RUNNER_TEMP}/agentserver-v2-image-evidence/harness-image-manifest.json"`,
		`--harness-archive="${RUNNER_TEMP}/agentserver-v2-image-evidence/harness-image.oci.tar"`,
		`--manifest="${RUNNER_TEMP}/agentserver-v2-image-evidence/managed-sandbox-image-manifest.json"`,
		`--archive="${RUNNER_TEMP}/agentserver-v2-image-evidence/managed-sandbox-image.oci.tar"`,
		"managed-release-verification.txt",
	} {
		if !strings.Contains(string(workflow), required) {
			t.Fatalf("production workflow is missing managed release contract %q", required)
		}
	}
}
