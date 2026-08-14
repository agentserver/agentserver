package productionimage

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/managedruntime"
	"github.com/agentserver/agentserver/v2/internal/stockruntime"
)

func TestServiceManifestIsClosedWorldAndRoundTrips(t *testing.T) {
	manifest := validServiceManifest()
	raw, err := MarshalManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseManifest(raw)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Kind != KindService || parsed.SourceRevision != strings.Repeat("a", 40) || len(parsed.Files) != len(expectedFilePaths(KindService)) {
		t.Fatalf("parsed production image manifest = %+v", parsed)
	}
	if _, err := ParseManifest([]byte(`{"version":1,"version":1}`)); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate-key error = %v", err)
	}
}

func TestManifestRejectsOpenOrDriftingImageLayouts(t *testing.T) {
	for name, mutate := range map[string]func(*Manifest){
		"kind":      func(value *Manifest) { value.Kind = "combined" },
		"platform":  func(value *Manifest) { value.Platform = "linux-riscv64" },
		"toolchain": func(value *Manifest) { value.GoToolchain = "go1.26.4" },
		"CA source": func(value *Manifest) { value.CABundleSource = "postgres:latest" },
		"directory": func(value *Manifest) { value.Directories[0].Mode = 0o755 },
		"extra file": func(value *Manifest) {
			value.Files = append(value.Files, FileEntry{Path: "tmp/extra", SHA256: strings.Repeat("f", 64), SizeBytes: 1, Mode: 0o444})
		},
		"CA digest": func(value *Manifest) { value.Files[0].SHA256 = strings.Repeat("f", 64) },
	} {
		t.Run(name, func(t *testing.T) {
			manifest := validServiceManifest()
			mutate(&manifest)
			if err := manifest.Validate(); err == nil {
				t.Fatal("unsafe production image manifest was accepted")
			}
		})
	}
}

func TestHarnessManifestPinsSelectedArchitectureArtifacts(t *testing.T) {
	for _, platform := range []string{PlatformLinuxAMD64, PlatformLinuxARM64} {
		t.Run(platform, func(t *testing.T) {
			manifest := validHarnessManifest(platform)
			if err := manifest.Validate(); err != nil {
				t.Fatal(err)
			}
			for index := range manifest.Files {
				if manifest.Files[index].Path == RuntimeBundleRoot+"/bin/codex" {
					manifest.Files[index].SHA256 = strings.Repeat("f", 64)
					break
				}
			}
			if err := manifest.Validate(); err == nil || !strings.Contains(err.Error(), "pinned release artifact") {
				t.Fatalf("cross-architecture Codex artifact error = %v", err)
			}
		})
	}
}

func TestManagedSandboxManifestLocksAMD64RuntimeCLIsAndSkills(t *testing.T) {
	manifest := validManagedSandboxManifest()
	if err := manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	files := fileMap(manifest.Files)
	for _, path := range []string{
		managedruntime.ExecutableImagePath,
		"usr/local/bin/bkectl",
		"usr/local/bin/lark-cli",
		ManagedSkillPath,
		ManagedLarkSkillPath,
		ManagedBkectlSkillPath,
		ManagedBkectlCommandSurfacePath,
		CABundlePath,
	} {
		if _, found := files[path]; !found {
			t.Fatalf("managed sandbox manifest is missing %s", path)
		}
	}

	manifest.Platform = PlatformLinuxARM64
	if err := manifest.Validate(); err == nil || !strings.Contains(err.Error(), "linux-amd64") {
		t.Fatalf("managed sandbox arm64 error = %v", err)
	}
	manifest = validManagedSandboxManifest()
	for index := range manifest.Files {
		if manifest.Files[index].Path == "usr/local/bin/lark-cli" {
			manifest.Files[index].SHA256 = strings.Repeat("f", 64)
		}
	}
	if err := manifest.Validate(); err == nil || !strings.Contains(err.Error(), "pinned release artifact") {
		t.Fatalf("managed Lark CLI drift error = %v", err)
	}

	manifest = validManagedSandboxManifest()
	for index := range manifest.Files {
		if manifest.Files[index].Path == "usr/local/bin/bkectl" {
			manifest.Files[index].SHA256 = strings.Repeat("f", 64)
		}
	}
	if err := manifest.Validate(); err == nil || !strings.Contains(err.Error(), "pinned release artifact") {
		t.Fatalf("managed bkectl CLI drift error = %v", err)
	}

	manifest = validManagedSandboxManifest()
	for index := range manifest.Files {
		if manifest.Files[index].Path == managedruntime.ExecutableImagePath {
			manifest.Files = append(manifest.Files[:index], manifest.Files[index+1:]...)
			break
		}
	}
	if err := manifest.Validate(); err == nil || !strings.Contains(err.Error(), "file set is incomplete") {
		t.Fatalf("missing managed FaaS runtime error = %v", err)
	}
}

func TestTarVerifierAcceptsExactRootOwnedLayout(t *testing.T) {
	payload := []byte("deterministic image payload")
	digest := sha256.Sum256(payload)
	directories := map[string]DirectoryEntry{
		"opt":         {Path: "opt", Mode: 0o555},
		"opt/release": {Path: "opt/release", Mode: 0o711},
	}
	files := map[string]FileEntry{
		"opt/release/payload": {
			Path: "opt/release/payload", SHA256: hex.EncodeToString(digest[:]),
			SizeBytes: int64(len(payload)), Mode: 0o444,
		},
	}
	archive := testImageTar(t, payload, 0, tar.TypeReg, false)
	if err := verifyTarEntries(bytes.NewReader(archive), directories, files); err != nil {
		t.Fatal(err)
	}
	for name, archive := range map[string][]byte{
		"wrong owner": testImageTar(t, payload, 7, tar.TypeReg, false),
		"symlink":     testImageTar(t, payload, 0, tar.TypeSymlink, false),
		"extra file":  testImageTar(t, payload, 0, tar.TypeReg, true),
		"wrong bytes": testImageTar(t, []byte("deterministic image payloae"), 0, tar.TypeReg, false),
	} {
		t.Run(name, func(t *testing.T) {
			if err := verifyTarEntries(bytes.NewReader(archive), directories, files); err == nil {
				t.Fatal("invalid production image tar was accepted")
			}
		})
	}
}

func validServiceManifest() Manifest {
	files := make([]FileEntry, 0, 7)
	for _, path := range expectedFilePaths(KindService) {
		entry := FileEntry{Path: path, SHA256: strings.Repeat("b", 64), SizeBytes: 1, Mode: 0o555}
		if path == CABundlePath {
			entry.SHA256 = CABundleSHA256
			entry.SizeBytes = CABundleSizeBytes
			entry.Mode = 0o444
		}
		files = append(files, entry)
	}
	return Manifest{
		Version: ManifestVersion, Kind: KindService, Platform: Platform,
		SourceRevision: strings.Repeat("a", 40), GoToolchain: GoToolchain,
		CABundleSource: CABundleSourceImage,
		Directories:    expectedDirectories(KindService), Files: files,
	}
}

func validHarnessManifest(platform string) Manifest {
	codexDigest, codexSize, bwrapDigest, bwrapSize := stockRuntimePins(platform)
	files := make([]FileEntry, 0, len(expectedFilePaths(KindHarness)))
	for _, path := range expectedFilePaths(KindHarness) {
		entry := FileEntry{Path: path, SHA256: strings.Repeat("b", 64), SizeBytes: 1, Mode: 0o555}
		switch path {
		case CABundlePath:
			entry.SHA256, entry.SizeBytes, entry.Mode = CABundleSHA256, CABundleSizeBytes, 0o444
		case RequirementsPath:
			entry.SHA256, entry.SizeBytes, entry.Mode = RequirementsSHA256, RequirementsSizeBytes, 0o444
		case RuntimeManifestPath:
			entry.SHA256, entry.SizeBytes, entry.Mode = stockruntime.ManifestSHA256, stockruntime.ManifestSizeBytes, 0o444
		case RuntimeBundleRoot + "/bin/codex":
			entry.SHA256, entry.SizeBytes = codexDigest, codexSize
		case RuntimeBundleRoot + "/codex-resources/bwrap":
			entry.SHA256, entry.SizeBytes = bwrapDigest, bwrapSize
		case ManagedSkillPath:
			entry.SHA256, entry.SizeBytes, entry.Mode = ManagedSkillSHA256, ManagedSkillSizeBytes, 0o444
		case ManagedLarkSkillPath:
			entry.Mode = 0o444
		case ManagedBkectlSkillPath:
			entry.SHA256, entry.SizeBytes, entry.Mode = ManagedBkectlSkillSHA256, ManagedBkectlSkillSizeBytes, 0o444
		case ManagedBkectlCommandSurfacePath:
			entry.SHA256, entry.SizeBytes, entry.Mode = ManagedBkectlCommandSurfaceSHA256, ManagedBkectlCommandSurfaceSizeBytes, 0o444
		case ManagedBkectlDomainGuidesPath:
			entry.SHA256, entry.SizeBytes, entry.Mode = ManagedBkectlDomainGuidesSHA256, ManagedBkectlDomainGuidesSizeBytes, 0o444
		case ManagedBkectlInvocationPath:
			entry.SHA256, entry.SizeBytes, entry.Mode = ManagedBkectlInvocationSHA256, ManagedBkectlInvocationSizeBytes, 0o444
		}
		files = append(files, entry)
	}
	return Manifest{
		Version: ManifestVersion, Kind: KindHarness, Platform: platform,
		SourceRevision: strings.Repeat("a", 40), GoToolchain: GoToolchain,
		CABundleSource: CABundleSourceImage,
		Directories:    expectedDirectories(KindHarness), Files: files,
	}
}

func validManagedSandboxManifest() Manifest {
	files := make([]FileEntry, 0, len(expectedFilePaths(KindManagedSandbox)))
	for _, path := range expectedFilePaths(KindManagedSandbox) {
		entry := FileEntry{Path: path, SHA256: strings.Repeat("b", 64), SizeBytes: 1, Mode: 0o555}
		switch path {
		case CABundlePath:
			entry.SHA256, entry.SizeBytes, entry.Mode = CABundleSHA256, CABundleSizeBytes, 0o444
		case ManagedSkillPath:
			entry.SHA256, entry.SizeBytes, entry.Mode = ManagedSkillSHA256, ManagedSkillSizeBytes, 0o444
		case ManagedLarkSkillPath:
			entry.Mode = 0o444
		case ManagedBkectlSkillPath:
			entry.SHA256, entry.SizeBytes, entry.Mode = ManagedBkectlSkillSHA256, ManagedBkectlSkillSizeBytes, 0o444
		case ManagedBkectlCommandSurfacePath:
			entry.SHA256, entry.SizeBytes, entry.Mode = ManagedBkectlCommandSurfaceSHA256, ManagedBkectlCommandSurfaceSizeBytes, 0o444
		case ManagedBkectlDomainGuidesPath:
			entry.SHA256, entry.SizeBytes, entry.Mode = ManagedBkectlDomainGuidesSHA256, ManagedBkectlDomainGuidesSizeBytes, 0o444
		case ManagedBkectlInvocationPath:
			entry.SHA256, entry.SizeBytes, entry.Mode = ManagedBkectlInvocationSHA256, ManagedBkectlInvocationSizeBytes, 0o444
		case "usr/local/bin/bkectl":
			entry.SHA256, entry.SizeBytes = ManagedBkectlCLISHA256, ManagedBkectlCLISizeBytes
		case "usr/local/bin/lark-cli":
			entry.SHA256, entry.SizeBytes = ManagedLarkCLISHA256, ManagedLarkCLISizeBytes
		}
		files = append(files, entry)
	}
	return Manifest{
		Version: ManifestVersion, Kind: KindManagedSandbox, Platform: PlatformLinuxAMD64,
		SourceRevision: strings.Repeat("a", 40), GoToolchain: GoToolchain,
		CABundleSource: CABundleSourceImage,
		Directories:    expectedDirectories(KindManagedSandbox), Files: files,
	}
}

func testImageTar(t *testing.T, payload []byte, payloadUID int, payloadType byte, extra bool) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := tar.NewWriter(&output)
	write := func(header *tar.Header, contents []byte) {
		t.Helper()
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if len(contents) != 0 {
			if _, err := writer.Write(contents); err != nil {
				t.Fatal(err)
			}
		}
	}
	write(&tar.Header{Name: ".", Typeflag: tar.TypeDir, Mode: 0o755, Uid: 0, Gid: 0}, nil)
	write(&tar.Header{Name: "opt/", Typeflag: tar.TypeDir, Mode: 0o555, Uid: 0, Gid: 0}, nil)
	write(&tar.Header{Name: "opt/release/", Typeflag: tar.TypeDir, Mode: 0o711, Uid: 0, Gid: 0}, nil)
	header := &tar.Header{
		Name: "opt/release/payload", Typeflag: payloadType, Mode: 0o444,
		Uid: payloadUID, Gid: 0, Size: int64(len(payload)),
	}
	if payloadType == tar.TypeSymlink {
		header.Size = 0
		header.Linkname = "elsewhere"
		payload = nil
	}
	write(header, payload)
	if extra {
		write(&tar.Header{Name: "opt/release/extra", Typeflag: tar.TypeReg, Mode: 0o444, Uid: 0, Gid: 0, Size: 1}, []byte("x"))
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
