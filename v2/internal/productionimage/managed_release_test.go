package productionimage

import (
	"strings"
	"testing"
)

func TestManagedReleaseLockBindsImageCLIAndSkillDigests(t *testing.T) {
	harness := validHarnessManifest(PlatformLinuxAMD64)
	sandbox := validManagedSandboxManifest()
	harnessFiles := fileMap(harness.Files)
	sandboxFiles := fileMap(sandbox.Files)
	harnessDigest := "sha256:" + strings.Repeat("c", 64)
	sandboxDigest := "sha256:" + strings.Repeat("d", 64)
	lock := ManagedReleaseLock{
		Platform:            sandbox.Platform,
		HarnessImage:        "registry.example.test/harness@" + harnessDigest,
		ManagedSandboxImage: "registry.example.test/managed@" + sandboxDigest,
		CLISHA256:           sandboxFiles["usr/local/bin/lark-cli"].SHA256,
		SkillSHA256:         harnessFiles[ManagedLarkSkillPath].SHA256,
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
		"platform": func(value *ManagedReleaseLock) { value.Platform = PlatformLinuxARM64 },
		"CLI":      func(value *ManagedReleaseLock) { value.CLISHA256 = strings.Repeat("e", 64) },
		"skill":    func(value *ManagedReleaseLock) { value.SkillSHA256 = strings.Repeat("e", 64) },
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
	if err := VerifyManagedReleaseLock(drifted, lock); err == nil || !strings.Contains(err.Error(), "cross-image skill") {
		t.Fatalf("cross-image skill drift error = %v", err)
	}
}
