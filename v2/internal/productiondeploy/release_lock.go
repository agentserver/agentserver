package productiondeploy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ReleaseLock replaces every published artifact authority in an already
// valid production template. The derived managed runtime and pack digests are
// recomputed by LockRelease; callers never supply those values independently.
type ReleaseLock struct {
	ServiceImage        string
	HarnessImage        string
	HydraImage          string
	ManagedSandboxImage string
	LarkCLISHA256       string
	LarkSkillSHA256     string
}

func LockRelease(base LoadedConfig, lock ReleaseLock) ([]byte, error) {
	document := base.Document
	if err := validateManagedReleaseEvidence(document); err != nil {
		return nil, err
	}
	// An active configuration's probe report binds the complete bootstrap
	// deployment, including every executable artifact. In particular, a
	// Terminal Sandbox Session now selects a pinned management-plane revision;
	// changing the config image digest cannot change the image that revision
	// actually runs. Never let the generic release locker create that split-
	// brain state. Artifact upgrades must return to policy-bootstrap, publish a
	// new Terminal revision, run the real SG probe, and activate its report.
	if managedExecutionActive(document.Managed) && !releaseLockMatches(document, lock) {
		return nil, errors.New("active managed executor artifacts are immutable; prepare policy-bootstrap and rerun TAE terminal probe activation")
	}
	document.Images = ImagesDocument{
		Service: lock.ServiceImage, Harness: lock.HarnessImage,
		Hydra: lock.HydraImage, ManagedSandbox: lock.ManagedSandboxImage,
	}
	document.Managed.Lark.CLISHA256 = lock.LarkCLISHA256
	document.Managed.Lark.SkillSHA256 = lock.LarkSkillSHA256
	if managedExecutionActive(document.Managed) {
		document.Managed.Environment.RuntimeProfileSHA256 = managedRuntimeProfileDigest(document, document.Managed)
		document.Managed.Environment.PackSetSHA256 = managedPackSetDigest(document.Managed)
	}

	loaded, err := ValidateConfig(document)
	if err != nil {
		return nil, fmt.Errorf("validate locked production release: %w", err)
	}
	raw, err := json.MarshalIndent(loaded.Document, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode locked production release: %w", err)
	}
	raw = append(raw, '\n')
	if _, err := ParseConfig(raw); err != nil {
		return nil, fmt.Errorf("verify locked production release: %w", err)
	}
	return raw, nil
}

// LockDeveloperServiceRelease changes only the Kubernetes service image in an
// active release. It intentionally keeps the existing harness, managed
// sandbox, Hydra, runtime, pack, and TAE evidence locks byte-for-byte intact.
// Final production promotion still uses LockRelease and its full verification.
func LockDeveloperServiceRelease(base LoadedConfig, serviceImage string) ([]byte, error) {
	document := base.Document
	if err := validateManagedReleaseEvidence(document); err != nil {
		return nil, err
	}
	if !managedExecutionActive(document.Managed) {
		return nil, errors.New("developer service release requires an active managed executor configuration")
	}
	if !imagePattern.MatchString(serviceImage) || !strings.HasPrefix(serviceImage, ProductionServiceImage+"@sha256:") {
		return nil, errors.New("developer service release requires a digest-pinned SG service mirror image")
	}
	harnessImage := document.Images.Harness
	hydraImage := document.Images.Hydra
	managedSandboxImage := document.Images.ManagedSandbox
	runtimeProfileSHA256 := document.Managed.Environment.RuntimeProfileSHA256
	packSetSHA256 := document.Managed.Environment.PackSetSHA256
	networkBindingSHA256 := document.Managed.TAE.NetworkEvidence.BindingSHA256
	document.Images.Service = serviceImage
	loaded, err := ValidateConfig(document)
	if err != nil {
		return nil, fmt.Errorf("validate developer service release: %w", err)
	}
	if loaded.Document.Images.Harness != harnessImage || loaded.Document.Images.Hydra != hydraImage ||
		loaded.Document.Images.ManagedSandbox != managedSandboxImage ||
		loaded.Document.Managed.Environment.RuntimeProfileSHA256 != runtimeProfileSHA256 ||
		loaded.Document.Managed.Environment.PackSetSHA256 != packSetSHA256 ||
		loaded.Document.Managed.TAE.NetworkEvidence.BindingSHA256 != networkBindingSHA256 {
		return nil, errors.New("developer service release changed a TAE runtime, pack, or network evidence lock")
	}
	raw, err := json.MarshalIndent(loaded.Document, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode developer service release: %w", err)
	}
	raw = append(raw, '\n')
	if _, err := ParseConfig(raw); err != nil {
		return nil, fmt.Errorf("verify developer service release: %w", err)
	}
	return raw, nil
}

func releaseLockMatches(document ConfigDocument, lock ReleaseLock) bool {
	return document.Images.Service == lock.ServiceImage &&
		document.Images.Harness == lock.HarnessImage &&
		document.Images.Hydra == lock.HydraImage &&
		document.Images.ManagedSandbox == lock.ManagedSandboxImage &&
		document.Managed.Lark.CLISHA256 == lock.LarkCLISHA256 &&
		document.Managed.Lark.SkillSHA256 == lock.LarkSkillSHA256
}

// validateManagedReleaseEvidence is intentionally stricter than ordinary
// config validation. The checked-in example remains useful for schema and
// renderer tests, but it can never be promoted into a production Chart.
func validateManagedReleaseEvidence(document ConfigDocument) error {
	if !document.Managed.Enabled {
		return errors.New("managed executor must be enabled for the production release")
	}
	if managedPolicyBootstrap(document.Managed) {
		return nil
	}
	if !managedExecutionActive(document.Managed) {
		return errors.New("managed executor release stage must be policy-bootstrap or active")
	}
	for name, reference := range map[string]string{
		"managedExecutor.tae.policy.evidenceRef":          document.Managed.TAE.Policy.EvidenceRef,
		"managedExecutor.tae.networkEvidence.evidenceRef": document.Managed.TAE.NetworkEvidence.EvidenceRef,
	} {
		if containsReleaseSentinel(reference) {
			return fmt.Errorf("%s contains a template sentinel and is not production evidence", name)
		}
	}
	if repeatedDigest(document.Managed.TAE.NetworkEvidence.ReportSHA256) {
		return errors.New("managedExecutor.tae.networkEvidence.reportSha256 is an obvious template digest")
	}
	return nil
}

func containsReleaseSentinel(value string) bool {
	upper := strings.ToUpper(value)
	for _, sentinel := range []string{"REPLACE", "TODO", "TBD", "EXAMPLE", "PLACEHOLDER", "CHANGEME", "SAMPLE", "DUMMY"} {
		if strings.Contains(upper, sentinel) {
			return true
		}
	}
	return false
}

func repeatedDigest(value string) bool {
	return len(value) == 64 && strings.Trim(value, value[:1]) == ""
}

// WriteReleaseConfig publishes one immutable, owner-readable release config.
// It never overwrites an existing path, including an exact retry.
func WriteReleaseConfig(raw []byte, destination string) error {
	if destination == "" || !filepath.IsAbs(destination) || filepath.Clean(destination) != destination || filepath.Base(destination) == "." {
		return errors.New("production release config destination must be an absolute clean child path")
	}
	if len(raw) == 0 || len(raw) > int(maximumConfigBytes) {
		return errors.New("production release config has an invalid size")
	}
	if _, err := ParseConfig(raw); err != nil {
		return err
	}
	parent := filepath.Dir(destination)
	parentInfo, err := os.Lstat(parent)
	if err != nil || !parentInfo.IsDir() || parentInfo.Mode()&os.ModeSymlink != 0 || parentInfo.Mode().Perm()&0o022 != 0 {
		return errors.New("production release config parent must be a direct directory not writable by group or other")
	}
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create production release config: %w", err)
	}
	created := true
	defer func() {
		if created {
			_ = os.Remove(destination)
		}
	}()
	written, writeErr := file.Write(raw)
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil || written != len(raw) {
		return errors.Join(errors.New("write production release config"), writeErr, syncErr, closeErr)
	}
	opened, err := os.Open(destination)
	if err != nil {
		return fmt.Errorf("reopen production release config: %w", err)
	}
	info, statErr := opened.Stat()
	actual, readErr := io.ReadAll(io.LimitReader(opened, maximumConfigBytes+1))
	verifyCloseErr := opened.Close()
	if statErr != nil || readErr != nil || verifyCloseErr != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 ||
		info.Size() != int64(len(raw)) || !bytes.Equal(actual, raw) {
		return errors.Join(errors.New("verify production release config"), statErr, readErr, verifyCloseErr)
	}
	created = false
	return nil
}
