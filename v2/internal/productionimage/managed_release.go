package productionimage

import (
	"errors"
	"fmt"
	"strings"
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
	for _, path := range []string{ManagedSkillPath, ManagedLarkSkillPath} {
		if _, harnessFound := harnessFiles[path]; !harnessFound {
			return fmt.Errorf("managed release harness image is missing %s", path)
		}
		if _, sandboxFound := sandboxFiles[path]; !sandboxFound {
			return fmt.Errorf("managed release sandbox image is missing %s", path)
		}
	}
	if _, larkFound := sandboxFiles["usr/local/bin/lark-cli"]; !larkFound {
		return errors.New("managed release sandbox image is missing lark-cli")
	}
	if _, bkectlFound := sandboxFiles["usr/local/bin/bkectl"]; !bkectlFound {
		return errors.New("managed release sandbox image is missing bkectl CLI")
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
