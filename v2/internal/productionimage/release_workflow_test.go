package productionimage

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/bkectlpolicy"
	"github.com/agentserver/agentserver/v2/internal/managedruntime"
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
	managedContainerfile, err := os.ReadFile(filepath.Join(v2Root, "deploy", "production", "managed-sandbox.Containerfile"))
	if err != nil {
		t.Fatal(err)
	}
	skillDigest := sha256.Sum256(skill)
	for _, required := range []string{
		"MANAGED_SANDBOX_REPOSITORY: ghcr.io/agentserver/v2-managed-sandbox",
		"LARK_CLI_SHA256: " + ManagedLarkCLISHA256,
		"LARK_SKILL_SHA256: " + hex.EncodeToString(skillDigest[:]),
		"MANAGED_SKILL_SHA256: " + ManagedSkillSHA256,
		"BKECTL_REPOSITORY: https://code.byted.org/bd-sre/bkectl.git",
		"BKECTL_SOURCE_REVISION: " + ManagedBkectlSourceRevision,
		"BKECTL_CLI_SHA256: " + ManagedBkectlCLISHA256,
		"BKECTL_SKILL_PACK_SHA256: " + ManagedBkectlSkillPackSHA256,
		"BKECTL_POLICY_SHA256: " + bkectlpolicy.SHA256Hex(),
		`--lark-cli="${LARK_CLI_PATH}"`,
		`--lark-skill="${LARK_SKILL_PATH}"`,
		`--bkectl="${BKECTL_PATH}"`,
		`--bkectl-skill-root="${BKECTL_SKILL_ROOT}"`,
		`--managed-skill="${MANAGED_SKILL_PATH}"`,
		`--managed-sandbox-image="${MANAGED_SANDBOX_REPOSITORY}:sha-${GITHUB_SHA}"`,
		"agentserver-deploy lock-release",
		`--bkectl-source-revision="${BKECTL_SOURCE_REVISION}"`,
		`--bkectl-cli-sha256="${BKECTL_CLI_SHA256}"`,
		`--bkectl-skill-pack-sha256="${BKECTL_SKILL_PACK_SHA256}"`,
		`--bkectl-policy-sha256="${BKECTL_POLICY_SHA256}"`,
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
	for _, required := range []string{
		"FROM " + managedSandboxBaseImageReference,
		`LABEL org.opencontainers.image.description="` + managedSandboxDescription + `"`,
		"USER 0:0\nWORKDIR /workspace\nCMD [\"" + managedruntime.ExecutablePath + "\"]\nSTOPSIGNAL SIGTERM",
	} {
		if !strings.Contains(string(managedContainerfile), required) {
			t.Fatalf("managed sandbox Containerfile is missing runtime contract %q", required)
		}
	}
	if strings.Contains(string(managedContainerfile), "FROM scratch") {
		t.Fatal("managed sandbox Containerfile must retain the digest-pinned minimal Debian base")
	}
	if strings.Contains(string(managedContainerfile), "FROM aliyun-sin-hub.byted.org/faas/bytedance.sandbox.terminal_faas") {
		t.Fatal("managed sandbox Containerfile must not inherit the official terminal_faas image")
	}
}

func TestProductionWorkflowHasBoundedServiceOnlyDevelopmentPath(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate production workflow contract test")
	}
	v2Root := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	workflow, err := os.ReadFile(filepath.Join(v2Root, "..", ".github", "workflows", "v2-production.yml"))
	if err != nil {
		t.Fatal(err)
	}
	fastPath := string(workflow)
	start := strings.Index(fastPath, "  publish-service-only:")
	end := strings.Index(fastPath, "  promote-active-chart:")
	if start < 0 || end <= start {
		t.Fatal("production workflow is missing the service-only job boundary")
	}
	fastPath = fastPath[start:end]
	for _, required := range []string{
		"build-service-image.sh",
		"--cache=gha",
		"lock-developer-service",
		"del(.images.service)",
		"cmp \"${release_dir}/before.json\" \"${release_dir}/after.json\"",
	} {
		if !strings.Contains(fastPath, required) {
			t.Fatalf("service-only workflow is missing fast-path contract %q", required)
		}
	}
	for _, forbidden := range []string{
		"pnpm", "make -C v2 check", "CODEX_URL", "LARK_CLI_URL",
		"build-images.sh", "MANAGED_SANDBOX_REPOSITORY", "HARNESS_REPOSITORY",
	} {
		if strings.Contains(fastPath, forbidden) {
			t.Fatalf("service-only workflow retained redundant work %q", forbidden)
		}
	}
}
