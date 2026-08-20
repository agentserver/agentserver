package productiondeploy

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/bkectlpolicy"
	"github.com/agentserver/agentserver/v2/internal/productionimage"
)

func releaseDigest(character string) string { return strings.Repeat(character, 64) }

func releaseLockForDocument(document ConfigDocument) ReleaseLock {
	return ReleaseLock{
		ServiceImage: document.Images.Service, HarnessImage: document.Images.Harness,
		HydraImage: document.Images.Hydra, ManagedSandboxImage: document.Images.ManagedSandbox,
		ManagedSkillSHA256: document.Managed.BaseInstructionsSHA256,
		LarkCLISHA256:      document.Managed.Lark.CLISHA256, LarkSkillSHA256: document.Managed.Lark.SkillSHA256,
		BkectlSourceRevision: document.Managed.Bkectl.SourceRevision,
		BkectlCLISHA256:      document.Managed.Bkectl.CLISHA256, BkectlSkillPackSHA256: document.Managed.Bkectl.SkillPackSHA256,
		BkectlPolicySHA256: document.Managed.Bkectl.PolicySHA256,
	}
}

func TestLockReleasePreservesEvidenceBoundActiveArtifacts(t *testing.T) {
	base, err := ValidateConfig(validConfigDocument())
	if err != nil {
		t.Fatal(err)
	}
	document := base.Document
	lock := releaseLockForDocument(document)
	raw, err := LockRelease(base, lock)
	if err != nil {
		t.Fatal(err)
	}
	locked, err := ParseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	lockedDocument := locked.Document
	if !releaseLockMatches(lockedDocument, lock) {
		t.Fatalf("locked release = %+v", lockedDocument)
	}
}

func TestLockReleaseRejectsEvidenceBoundActiveArtifactDrift(t *testing.T) {
	base, err := ValidateConfig(validConfigDocument())
	if err != nil {
		t.Fatal(err)
	}
	document := base.Document
	valid := releaseLockForDocument(document)
	for name, mutate := range map[string]func(*ReleaseLock){
		"service image": func(lock *ReleaseLock) {
			lock.ServiceImage = ProductionServiceImage + "@sha256:" + releaseDigest("a")
		},
		"harness image": func(lock *ReleaseLock) {
			lock.HarnessImage = ProductionHarnessImage + "@sha256:" + releaseDigest("b")
		},
		"hydra image": func(lock *ReleaseLock) {
			lock.HydraImage = ProductionHydraImage + "@sha256:" + releaseDigest("c")
		},
		"managed sandbox image": func(lock *ReleaseLock) {
			lock.ManagedSandboxImage = ProductionManagedSandboxImage + "@sha256:" + releaseDigest("d")
		},
		"lark cli":      func(lock *ReleaseLock) { lock.LarkCLISHA256 = releaseDigest("e") },
		"lark skill":    func(lock *ReleaseLock) { lock.LarkSkillSHA256 = releaseDigest("f") },
		"managed skill": func(lock *ReleaseLock) { lock.ManagedSkillSHA256 = releaseDigest("1") },
		"bkectl source": func(lock *ReleaseLock) { lock.BkectlSourceRevision = strings.Repeat("2", 40) },
		"bkectl cli":    func(lock *ReleaseLock) { lock.BkectlCLISHA256 = releaseDigest("3") },
		"bkectl skill":  func(lock *ReleaseLock) { lock.BkectlSkillPackSHA256 = releaseDigest("4") },
		"bkectl policy": func(lock *ReleaseLock) { lock.BkectlPolicySHA256 = releaseDigest("5") },
	} {
		t.Run(name, func(t *testing.T) {
			lock := valid
			mutate(&lock)
			_, err := LockRelease(base, lock)
			if err == nil || !strings.Contains(err.Error(), "active managed executor artifacts are immutable") {
				t.Fatalf("active artifact drift error = %v", err)
			}
		})
	}
}

func TestLockDeveloperServiceReleaseChangesOnlyServiceImage(t *testing.T) {
	base, err := ValidateConfig(validConfigDocument())
	if err != nil {
		t.Fatal(err)
	}
	wantImage := ProductionServiceImage + "@sha256:" + releaseDigest("a")
	raw, err := LockDeveloperServiceRelease(base, wantImage)
	if err != nil {
		t.Fatal(err)
	}
	locked, err := ParseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := base.Document
	want.Images.Service = wantImage
	if !reflect.DeepEqual(locked.Document, want) {
		t.Fatal("developer service release changed facts outside images.service")
	}
}

func TestLockDeveloperServiceReleaseFailsClosed(t *testing.T) {
	active, err := ValidateConfig(validConfigDocument())
	if err != nil {
		t.Fatal(err)
	}
	for name, testCase := range map[string]struct {
		config LoadedConfig
		image  string
	}{
		"mutable image": {active, ProductionServiceImage + ":main"},
		"wrong mirror":  {active, "ghcr.io/agentserver/v2-service@sha256:" + releaseDigest("a")},
		"bootstrap stage": {func() LoadedConfig {
			loaded, loadErr := ValidateConfig(policyBootstrapConfigDocument())
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			return loaded
		}(), ProductionServiceImage + "@sha256:" + releaseDigest("a")},
	} {
		t.Run(name, func(t *testing.T) {
			if _, lockErr := LockDeveloperServiceRelease(testCase.config, testCase.image); lockErr == nil {
				t.Fatal("unsafe developer service release was accepted")
			}
		})
	}
}

func TestLockReleasePreservesEvidenceFreePolicyBootstrap(t *testing.T) {
	base, err := ValidateConfig(policyBootstrapConfigDocument())
	if err != nil {
		t.Fatal(err)
	}
	document := base.Document
	raw, err := LockRelease(base, releaseLockForDocument(document))
	if err != nil {
		t.Fatal(err)
	}
	locked, err := ParseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !managedPolicyBootstrap(locked.Document.Managed) ||
		locked.Document.Managed.TAE.NetworkEvidence != (ManagedTAENetworkEvidenceDocument{}) ||
		locked.Document.Managed.TAE.Policy.Published || locked.Document.Managed.TAE.Policy.Approved ||
		locked.Document.Managed.TAE.Policy.EvidenceRef != "" {
		t.Fatalf("locked bootstrap gained premature authority: %+v", locked.Document.Managed)
	}
}

func TestLockReleaseRejectsUnverifiedInputs(t *testing.T) {
	base, err := ValidateConfig(policyBootstrapConfigDocument())
	if err != nil {
		t.Fatal(err)
	}
	valid := ReleaseLock{
		ServiceImage:        ProductionServiceImage + "@sha256:" + releaseDigest("a"),
		HarnessImage:        ProductionHarnessImage + "@sha256:" + releaseDigest("b"),
		HydraImage:          ProductionHydraImage + "@sha256:" + releaseDigest("c"),
		ManagedSandboxImage: ProductionManagedSandboxImage + "@sha256:" + releaseDigest("d"),
		ManagedSkillSHA256:  productionimage.ManagedSkillSHA256,
		LarkCLISHA256:       productionimage.ManagedLarkCLISHA256, LarkSkillSHA256: releaseDigest("f"),
		BkectlSourceRevision: bkectlpolicy.SourceRevision, BkectlCLISHA256: bkectlpolicy.CLISHA256,
		BkectlSkillPackSHA256: bkectlpolicy.SkillPackSHA256, BkectlPolicySHA256: bkectlpolicy.SHA256Hex(),
	}
	for name, mutate := range map[string]func(*ReleaseLock){
		"wrong image repository": func(lock *ReleaseLock) {
			lock.ManagedSandboxImage = "registry.test/sandbox@sha256:" + releaseDigest("d")
		},
		"mutable image":        func(lock *ReleaseLock) { lock.HarnessImage = ProductionHarnessImage + ":latest" },
		"zero CLI digest":      func(lock *ReleaseLock) { lock.LarkCLISHA256 = strings.Repeat("0", 64) },
		"invalid skill digest": func(lock *ReleaseLock) { lock.LarkSkillSHA256 = strings.Repeat("A", 64) },
	} {
		t.Run(name, func(t *testing.T) {
			lock := valid
			mutate(&lock)
			if _, err := LockRelease(base, lock); err == nil {
				t.Fatal("unsafe release lock was accepted")
			}
		})
	}
}

func TestLockReleaseRejectsTemplateEvidence(t *testing.T) {
	valid := validConfigDocument()
	cases := map[string]func(*ConfigDocument){
		"policy replace sentinel": func(document *ConfigDocument) {
			document.Managed.TAE.Policy.EvidenceRef = "REPLACE_WITH_TAE_TICKET"
		},
		"network TODO sentinel": func(document *ConfigDocument) {
			document.Managed.TAE.NetworkEvidence.EvidenceRef = "TODO/network-report"
		},
		"network example sentinel": func(document *ConfigDocument) {
			document.Managed.TAE.NetworkEvidence.EvidenceRef = "artifact://EXAMPLE/report.json"
		},
		"synthetic report digest": func(document *ConfigDocument) {
			document.Managed.TAE.NetworkEvidence.ReportSHA256 = strings.Repeat("9", 64)
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			document := valid
			mutate(&document)
			if err := refreshDefaultManagedSandboxProfile(&document); err != nil {
				t.Fatal(err)
			}
			base, err := ValidateConfig(document)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := LockRelease(base, releaseLockForDocument(document)); err == nil {
				t.Fatal("template evidence was promoted into a release")
			}
		})
	}
}

func TestWriteReleaseConfigIsExclusiveAndOwnerReadable(t *testing.T) {
	base, err := ValidateConfig(validConfigDocument())
	if err != nil {
		t.Fatal(err)
	}
	document := base.Document
	raw, err := LockRelease(base, releaseLockForDocument(document))
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "production.json")
	if err := WriteReleaseConfig(raw, destination); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("release config mode = %o", info.Mode().Perm())
	}
	if err := WriteReleaseConfig(raw, destination); err == nil {
		t.Fatal("release config writer overwrote an existing path")
	}
}
