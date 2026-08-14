package productionimage

import (
	"errors"
	"fmt"
	"strings"

	"github.com/agentserver/agentserver/v2/internal/bkectlpolicy"
)

type ManagedReleaseLock struct {
	Platform              string
	HarnessImage          string
	ManagedSandboxImage   string
	ManagedSkillSHA256    string
	LarkCLISHA256         string
	LarkSkillSHA256       string
	BkectlSourceRevision  string
	BkectlCLISHA256       string
	BkectlSkillPackSHA256 string
	BkectlPolicySHA256    string
}

type ManagedReleaseArtifacts struct {
	HarnessManifest        Manifest
	HarnessEvidence        OCIImageEvidence
	ManagedSandboxManifest Manifest
	ManagedSandboxEvidence OCIImageEvidence
}

// VerifyManagedReleaseLock links deployment authority to both OCI images
// already proven by VerifyImageOCIWithEvidence. It prevents independently
// edited image, CLI, skill, and source-revision facts from describing a
// release that was never built together.
func VerifyManagedReleaseLock(artifacts ManagedReleaseArtifacts, lock ManagedReleaseLock) error {
	harness := artifacts.HarnessManifest
	sandbox := artifacts.ManagedSandboxManifest
	if err := harness.Validate(); err != nil {
		return err
	}
	if err := sandbox.Validate(); err != nil {
		return err
	}
	if harness.Kind != KindHarness || sandbox.Kind != KindManagedSandbox {
		return errors.New("managed release requires harness and managed-sandbox image manifests")
	}
	if lock.Platform != harness.Platform || lock.Platform != sandbox.Platform {
		return fmt.Errorf("managed release platform %q does not match harness %q and sandbox %q", lock.Platform, harness.Platform, sandbox.Platform)
	}
	if harness.SourceRevision != sandbox.SourceRevision {
		return errors.New("managed release harness and sandbox source revisions differ")
	}
	if err := verifyManagedReleaseImagePin("harness", lock.HarnessImage, artifacts.HarnessEvidence); err != nil {
		return err
	}
	if err := verifyManagedReleaseImagePin("managed sandbox", lock.ManagedSandboxImage, artifacts.ManagedSandboxEvidence); err != nil {
		return err
	}
	harnessFiles := fileMap(harness.Files)
	sandboxFiles := fileMap(sandbox.Files)
	for _, expected := range []struct {
		path   string
		digest string
	}{
		{ManagedSkillPath, lock.ManagedSkillSHA256},
		{ManagedLarkSkillPath, lock.LarkSkillSHA256},
		{ManagedBkectlSkillPath, ManagedBkectlSkillSHA256},
		{ManagedBkectlCommandSurfacePath, ManagedBkectlCommandSurfaceSHA256},
		{ManagedBkectlDomainGuidesPath, ManagedBkectlDomainGuidesSHA256},
		{ManagedBkectlInvocationPath, ManagedBkectlInvocationSHA256},
	} {
		harnessEntry, harnessFound := harnessFiles[expected.path]
		sandboxEntry, sandboxFound := sandboxFiles[expected.path]
		if !harnessFound || !sandboxFound || harnessEntry.SHA256 != expected.digest || sandboxEntry.SHA256 != expected.digest {
			return fmt.Errorf("managed release cross-image artifact %s does not match its release lock", expected.path)
		}
	}
	larkCLI, larkFound := sandboxFiles["usr/local/bin/lark-cli"]
	bkectlCLI, bkectlFound := sandboxFiles["usr/local/bin/bkectl"]
	if !larkFound || larkCLI.SHA256 != lock.LarkCLISHA256 ||
		!bkectlFound || bkectlCLI.SHA256 != lock.BkectlCLISHA256 {
		return errors.New("managed release CLI digest does not match the verified sandbox image manifest")
	}
	if lock.ManagedSkillSHA256 != ManagedSkillSHA256 ||
		lock.BkectlSourceRevision != ManagedBkectlSourceRevision ||
		lock.BkectlSkillPackSHA256 != ManagedBkectlSkillPackSHA256 ||
		lock.BkectlPolicySHA256 != bkectlpolicy.SHA256Hex() {
		return errors.New("managed release tool source, skill pack, instructions, or policy lock is not pinned")
	}
	return nil
}

func verifyManagedReleaseImagePin(label, image string, evidence OCIImageEvidence) error {
	separator := strings.LastIndexByte(image, '@')
	if separator <= 0 || separator == len(image)-1 || image[separator+1:] != evidence.ImageManifestDigest ||
		!strings.HasPrefix(evidence.ImageManifestDigest, "sha256:") ||
		!digestPattern.MatchString(strings.TrimPrefix(evidence.ImageManifestDigest, "sha256:")) {
		return fmt.Errorf("managed release %s image reference does not pin the verified OCI image manifest digest", label)
	}
	return nil
}
