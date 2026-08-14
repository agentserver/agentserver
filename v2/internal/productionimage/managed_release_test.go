package productionimage

import (
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/bkectlpolicy"
)

func TestManagedReleaseLockBindsImageCLIAndSkillDigests(t *testing.T) {
	harness := validHarnessManifest(PlatformLinuxAMD64)
	sandbox := validManagedSandboxManifest()
	harnessFiles := fileMap(harness.Files)
	sandboxFiles := fileMap(sandbox.Files)
	harnessDigest := "sha256:" + strings.Repeat("c", 64)
	sandboxDigest := "sha256:" + strings.Repeat("d", 64)
	lock := ManagedReleaseLock{
		Platform:              sandbox.Platform,
		HarnessImage:          "registry.example.test/harness@" + harnessDigest,
		ManagedSandboxImage:   "registry.example.test/managed@" + sandboxDigest,
		ManagedSkillSHA256:    harnessFiles[ManagedSkillPath].SHA256,
		LarkCLISHA256:         sandboxFiles["usr/local/bin/lark-cli"].SHA256,
		LarkSkillSHA256:       harnessFiles[ManagedLarkSkillPath].SHA256,
		BkectlSourceRevision:  bkectlpolicy.SourceRevision,
		BkectlCLISHA256:       sandboxFiles["usr/local/bin/bkectl"].SHA256,
		BkectlSkillPackSHA256: bkectlpolicy.SkillPackSHA256,
		BkectlPolicySHA256:    bkectlpolicy.SHA256Hex(),
	}
	artifacts := ManagedReleaseArtifacts{
		HarnessManifest: harness, HarnessEvidence: OCIImageEvidence{ImageManifestDigest: harnessDigest},
		ManagedSandboxManifest: sandbox, ManagedSandboxEvidence: OCIImageEvidence{ImageManifestDigest: sandboxDigest},
	}
	if err := VerifyManagedReleaseLock(artifacts, lock); err != nil {
		t.Fatal(err)
	}

	for name, mutate := range map[string]func(*ManagedReleaseLock){
		"harness image": func(value *ManagedReleaseLock) {
			value.HarnessImage = "registry.example.test/harness@sha256:" + strings.Repeat("e", 64)
		},
		"sandbox image": func(value *ManagedReleaseLock) {
			value.ManagedSandboxImage = "registry.example.test/managed@sha256:" + strings.Repeat("e", 64)
		},
		"platform":      func(value *ManagedReleaseLock) { value.Platform = PlatformLinuxARM64 },
		"managed skill": func(value *ManagedReleaseLock) { value.ManagedSkillSHA256 = strings.Repeat("e", 64) },
		"lark CLI":      func(value *ManagedReleaseLock) { value.LarkCLISHA256 = strings.Repeat("e", 64) },
		"lark skill":    func(value *ManagedReleaseLock) { value.LarkSkillSHA256 = strings.Repeat("e", 64) },
		"bkectl source": func(value *ManagedReleaseLock) { value.BkectlSourceRevision = strings.Repeat("e", 40) },
		"bkectl CLI":    func(value *ManagedReleaseLock) { value.BkectlCLISHA256 = strings.Repeat("e", 64) },
		"bkectl skill":  func(value *ManagedReleaseLock) { value.BkectlSkillPackSHA256 = strings.Repeat("e", 64) },
		"bkectl policy": func(value *ManagedReleaseLock) { value.BkectlPolicySHA256 = strings.Repeat("e", 64) },
	} {
		t.Run(name, func(t *testing.T) {
			changed := lock
			mutate(&changed)
			if err := VerifyManagedReleaseLock(artifacts, changed); err == nil {
				t.Fatal("drifting managed release lock was accepted")
			}
		})
	}

	drifted := artifacts
	drifted.ManagedSandboxManifest.SourceRevision = strings.Repeat("f", 40)
	if err := VerifyManagedReleaseLock(drifted, lock); err == nil || !strings.Contains(err.Error(), "source revisions") {
		t.Fatalf("source revision drift error = %v", err)
	}
	drifted = artifacts
	for index := range drifted.ManagedSandboxManifest.Files {
		if drifted.ManagedSandboxManifest.Files[index].Path == ManagedLarkSkillPath {
			drifted.ManagedSandboxManifest.Files[index].SHA256 = strings.Repeat("f", 64)
		}
	}
	if err := VerifyManagedReleaseLock(drifted, lock); err == nil || !strings.Contains(err.Error(), "cross-image artifact") {
		t.Fatalf("cross-image skill drift error = %v", err)
	}
}
