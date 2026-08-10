package productionimage

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

func TestOCIImageVerifierAcceptsExactTwoLayerImage(t *testing.T) {
	for _, layout := range []testOCIRootLayout{testOCINestedIndex, testOCIDirectManifest, testOCIDirectManifestWithPlatform} {
		t.Run(string(layout), func(t *testing.T) {
			archive, manifest, directories, files := testOCIImage(t, false, layout)
			if err := verifyOCIArchive(bytes.NewReader(archive), manifest, directories, files); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestOCIImageVerifierAcceptsLockedManagedSandboxWorkdirLayer(t *testing.T) {
	archive, manifest, directories, files, managedBase := testOCIImageForKind(
		t, false, testOCINestedIndex, KindManagedSandbox, nil, managedWorkdirHistory,
	)
	if err := verifyOCIArchiveWithTestBase(archive, manifest, directories, files, managedBase); err != nil {
		t.Fatal(err)
	}
}

func TestOCIImageVerifierRejectsManagedSandboxWorkdirLayerWithContents(t *testing.T) {
	archive, manifest, directories, files, managedBase := testOCIImageForKind(
		t, false, testOCINestedIndex, KindManagedSandbox,
		[]testLayerEntry{{name: "unexpected", mode: 0o444, contents: []byte("unexpected")}},
		managedWorkdirHistory,
	)
	err := verifyOCIArchiveWithTestBase(archive, manifest, directories, files, managedBase)
	if err == nil || !strings.Contains(err.Error(), "locked empty WORKDIR layer") {
		t.Fatalf("managed sandbox non-empty WORKDIR layer error = %v", err)
	}
}

func TestOCIImageVerifierRejectsManagedSandboxWithoutWorkdirHistory(t *testing.T) {
	archive, manifest, directories, files, managedBase := testOCIImageForKind(
		t, false, testOCINestedIndex, KindManagedSandbox, nil, "RUN true",
	)
	err := verifyOCIArchiveWithTestBase(archive, manifest, directories, files, managedBase)
	if err == nil || !strings.Contains(err.Error(), "locked WORKDIR instruction") {
		t.Fatalf("managed sandbox WORKDIR history error = %v", err)
	}
}

func TestOCIImageVerifierRejectsManagedSandboxBaseDrift(t *testing.T) {
	archive, manifest, directories, files, managedBase := testOCIImageForKind(
		t, false, testOCINestedIndex, KindManagedSandbox, nil, managedWorkdirHistory,
	)

	t.Run("compressed descriptor", func(t *testing.T) {
		drifted := managedBase
		drifted.LayerDigest = "sha256:" + strings.Repeat("f", 64)
		err := verifyOCIArchiveWithTestBase(archive, manifest, directories, files, drifted)
		if err == nil || !strings.Contains(err.Error(), "locked Debian base layer") {
			t.Fatalf("managed sandbox base layer error = %v", err)
		}
	})
	t.Run("diff ID", func(t *testing.T) {
		drifted := managedBase
		drifted.LayerDiffID = "sha256:" + strings.Repeat("f", 64)
		err := verifyOCIArchiveWithTestBase(archive, manifest, directories, files, drifted)
		if err == nil || !strings.Contains(err.Error(), "locked Debian base diff ID") {
			t.Fatalf("managed sandbox base diff ID error = %v", err)
		}
	})
	t.Run("history", func(t *testing.T) {
		drifted := managedBase
		drifted.History.Comment = "drifted"
		err := verifyOCIArchiveWithTestBase(archive, manifest, directories, files, drifted)
		if err == nil || !strings.Contains(err.Error(), "locked Debian base history") {
			t.Fatalf("managed sandbox base history error = %v", err)
		}
	})
}

func TestOCIImageVerifierRejectsUnreferencedBlob(t *testing.T) {
	archive, manifest, directories, files := testOCIImage(t, true, testOCINestedIndex)
	err := verifyOCIArchive(bytes.NewReader(archive), manifest, directories, files)
	if err == nil || !strings.Contains(err.Error(), "unreferenced blobs") {
		t.Fatalf("unreferenced blob error = %v", err)
	}
}

func TestOCIImageVerifierRejectsWrongDirectDescriptorPlatform(t *testing.T) {
	archive, manifest, directories, files := testOCIImage(t, false, testOCIDirectManifestWithWrongPlatform)
	err := verifyOCIArchive(bytes.NewReader(archive), manifest, directories, files)
	if err == nil || !strings.Contains(err.Error(), "direct descriptor must select linux/arm64") {
		t.Fatalf("direct descriptor platform error = %v", err)
	}
}

type testLayerEntry struct {
	name      string
	mode      int64
	contents  []byte
	directory bool
}

type testOCIRootLayout string

const (
	testOCINestedIndex                     testOCIRootLayout = "nested-index"
	testOCIDirectManifest                  testOCIRootLayout = "direct-manifest"
	testOCIDirectManifestWithPlatform      testOCIRootLayout = "direct-manifest-with-platform"
	testOCIDirectManifestWithWrongPlatform testOCIRootLayout = "direct-manifest-with-wrong-platform"
)

func testOCIImage(t *testing.T, extraBlob bool, layout testOCIRootLayout) ([]byte, Manifest, map[string]DirectoryEntry, map[string]FileEntry) {
	t.Helper()
	archive, manifest, directories, files, _ := testOCIImageForKind(t, extraBlob, layout, KindService, nil, "")
	return archive, manifest, directories, files
}

func testOCIImageForKind(
	t *testing.T,
	extraBlob bool,
	layout testOCIRootLayout,
	kind string,
	managedWorkdirEntries []testLayerEntry,
	managedWorkdirCreatedBy string,
) ([]byte, Manifest, map[string]DirectoryEntry, map[string]FileEntry, managedSandboxBaseProfile) {
	t.Helper()
	revision := strings.Repeat("a", 40)
	platform := PlatformLinuxARM64
	architecture := "arm64"
	user := "65534:65534"
	workingDirectory := "/"
	title := "agentserver v2 production services"
	description := "Closed-world service binaries for agentserver v2"
	if kind == KindManagedSandbox {
		platform = PlatformLinuxAMD64
		architecture = "amd64"
		workingDirectory = "/workspace"
		title = "agentserver v2 managed sandbox"
		description = managedSandboxDescription
	}
	manifest := Manifest{Kind: kind, Platform: platform, SourceRevision: revision}
	directories := map[string]DirectoryEntry{
		"etc": {Path: "etc", Mode: 0o555},
		"usr": {Path: "usr", Mode: 0o555},
	}
	payload := []byte("payload")
	ca := []byte("ca")
	files := map[string]FileEntry{
		"usr/payload": testOCIFileEntry("usr/payload", payload, 0o555),
		"etc/ca":      testOCIFileEntry("etc/ca", ca, 0o444),
	}
	firstTar := testOCILayerTar(t, []testLayerEntry{
		{name: "etc/", mode: 0o555, directory: true},
		{name: "usr/", mode: 0o555, directory: true},
		{name: "usr/payload", mode: 0o555, contents: payload},
	})
	secondTar := testOCILayerTar(t, []testLayerEntry{
		{name: "etc/", mode: 0o555, directory: true},
		{name: "etc/ca", mode: 0o444, contents: ca},
	})
	firstLayer := testGzip(t, firstTar)
	secondLayer := testGzip(t, secondTar)
	layers := []ociDescriptor{
		testOCIDescriptor(ociGzipLayerMediaType, firstLayer),
		testOCIDescriptor(ociGzipLayerMediaType, secondLayer),
	}
	diffIDs := []string{
		"sha256:" + testSHA256(firstTar),
		"sha256:" + testSHA256(secondTar),
	}
	created := "2026-08-02T00:00:00Z"
	history := []ociHistory{
		{Created: created, CreatedBy: "ADD rootfs.tar /", Comment: "buildkit.dockerfile.v0"},
		{Created: created, CreatedBy: "COPY ca", Comment: "buildkit.dockerfile.v0"},
	}
	blobs := [][]byte{firstLayer, secondLayer}
	managedBase := lockedManagedSandboxBaseProfile()
	if kind == KindManagedSandbox {
		baseTar := testOCILayerTar(t, []testLayerEntry{
			{name: "bin/", mode: 0o755, directory: true},
			{name: "bin/sh", mode: 0o755, contents: []byte("test shell")},
		})
		baseLayer := testGzip(t, baseTar)
		managedBase = managedSandboxBaseProfile{
			LayerDigest: "sha256:" + testSHA256(baseLayer),
			LayerSize:   int64(len(baseLayer)),
			LayerDiffID: "sha256:" + testSHA256(baseTar),
			History: ociHistory{
				Created:   "2026-08-03T00:00:00Z",
				CreatedBy: managedSandboxBaseHistoryCreatedBy,
				Comment:   managedSandboxBaseHistoryComment,
			},
		}
		layers = append([]ociDescriptor{testOCIDescriptor(ociGzipLayerMediaType, baseLayer)}, layers...)
		diffIDs = append([]string{managedBase.LayerDiffID}, diffIDs...)
		history = append([]ociHistory{managedBase.History}, history...)
		blobs = append([][]byte{baseLayer}, blobs...)

		workdirTar := testOCILayerTar(t, managedWorkdirEntries)
		workdirLayer := testGzip(t, workdirTar)
		if len(managedWorkdirEntries) == 0 {
			if digest := "sha256:" + testSHA256(workdirLayer); digest != ociBuildkitEmptyLayer || int64(len(workdirLayer)) != ociBuildkitEmptyLayerSize {
				t.Fatalf("test canonical empty layer = %s/%d", digest, len(workdirLayer))
			}
			if diffID := "sha256:" + testSHA256(workdirTar); diffID != ociEmptyTarDiffID {
				t.Fatalf("test canonical empty layer diff ID = %s", diffID)
			}
		}
		layers = append(layers, testOCIDescriptor(ociGzipLayerMediaType, workdirLayer))
		diffIDs = append(diffIDs, "sha256:"+testSHA256(workdirTar))
		history = append(history, ociHistory{
			Created: created, CreatedBy: managedWorkdirCreatedBy, Comment: "buildkit.dockerfile.v0",
		})
		blobs = append(blobs, workdirLayer)
	}
	config := ociImageConfig{
		Architecture: architecture,
		Config: ociRuntimeConfig{
			User: user, Env: []string{ociDefaultPath}, ArgsEscaped: kind == KindManagedSandbox,
			WorkingDir: workingDirectory, StopSignal: "SIGTERM",
			Labels: map[string]string{
				"org.opencontainers.image.description": description,
				"org.opencontainers.image.revision":    revision,
				"org.opencontainers.image.source":      "https://github.com/agentserver/agentserver",
				"org.opencontainers.image.title":       title,
			},
		},
		Created: created,
		History: history,
		OS:      "linux",
		RootFS:  ociRootFS{Type: "layers", DiffIDs: diffIDs},
	}
	configBytes := testOCIJSON(t, config)
	imageManifest := ociManifestDocument{
		SchemaVersion: 2,
		MediaType:     ociImageManifestMediaType,
		Config:        testOCIDescriptor(ociImageConfigMediaType, configBytes),
		Layers:        layers,
	}
	imageManifestBytes := testOCIJSON(t, imageManifest)
	blobs = append(blobs, configBytes, imageManifestBytes)
	rootIndex := ociIndexDocument{SchemaVersion: 2, MediaType: ociImageLayoutMediaType}
	switch layout {
	case testOCINestedIndex:
		platformIndex := ociIndexDocument{
			SchemaVersion: 2,
			MediaType:     ociImageLayoutMediaType,
			Manifests: []ociDescriptor{{
				MediaType: ociImageManifestMediaType,
				Digest:    "sha256:" + testSHA256(imageManifestBytes),
				Size:      int64(len(imageManifestBytes)),
				Platform:  &ociPlatform{Architecture: architecture, OS: "linux"},
			}},
		}
		platformIndexBytes := testOCIJSON(t, platformIndex)
		rootIndex.Manifests = []ociDescriptor{testOCIDescriptor(ociImageLayoutMediaType, platformIndexBytes)}
		blobs = append(blobs, platformIndexBytes)
	case testOCIDirectManifest, testOCIDirectManifestWithPlatform, testOCIDirectManifestWithWrongPlatform:
		descriptor := testOCIDescriptor(ociImageManifestMediaType, imageManifestBytes)
		descriptor.Annotations = map[string]string{"org.opencontainers.image.ref.name": "example"}
		if layout == testOCIDirectManifestWithPlatform {
			descriptor.Platform = &ociPlatform{Architecture: architecture, OS: "linux"}
		} else if layout == testOCIDirectManifestWithWrongPlatform {
			wrongArchitecture := "amd64"
			if architecture == wrongArchitecture {
				wrongArchitecture = "arm64"
			}
			descriptor.Platform = &ociPlatform{Architecture: wrongArchitecture, OS: "linux"}
		}
		rootIndex.Manifests = []ociDescriptor{descriptor}
	default:
		t.Fatalf("unknown test OCI root layout %q", layout)
	}
	if extraBlob {
		blobs = append(blobs, []byte("unreferenced"))
	}
	return testOCIOuterTar(t, testOCIJSON(t, rootIndex), blobs), manifest, directories, files, managedBase
}

func verifyOCIArchiveWithTestBase(
	archive []byte,
	manifest Manifest,
	directories map[string]DirectoryEntry,
	files map[string]FileEntry,
	managedBase managedSandboxBaseProfile,
) error {
	_, err := verifyOCIArchiveWithBaseProfile(
		bytes.NewReader(archive), manifest, directories, files, managedBase,
	)
	return err
}

func testOCIFileEntry(name string, contents []byte, mode uint32) FileEntry {
	return FileEntry{
		Path: name, SHA256: testSHA256(contents), SizeBytes: int64(len(contents)), Mode: mode,
	}
}

func testOCILayerTar(t *testing.T, entries []testLayerEntry) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := tar.NewWriter(&output)
	for _, entry := range entries {
		header := &tar.Header{Name: entry.name, Mode: entry.mode, Uid: 0, Gid: 0}
		if entry.directory {
			header.Typeflag = tar.TypeDir
		} else {
			header.Typeflag = tar.TypeReg
			header.Size = int64(len(entry.contents))
		}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if len(entry.contents) != 0 {
			if _, err := writer.Write(entry.contents); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func testGzip(t *testing.T, contents []byte) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := gzip.NewWriter(&output)
	if _, err := writer.Write(contents); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func testOCIDescriptor(mediaType string, contents []byte) ociDescriptor {
	return ociDescriptor{MediaType: mediaType, Digest: "sha256:" + testSHA256(contents), Size: int64(len(contents))}
}

func testOCIJSON(t *testing.T, value any) []byte {
	t.Helper()
	contents, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return contents
}

func testOCIOuterTar(t *testing.T, index []byte, blobs [][]byte) []byte {
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
	write(&tar.Header{Name: "./", Typeflag: tar.TypeDir, Mode: 0o755}, nil)
	write(&tar.Header{Name: "blobs/", Typeflag: tar.TypeDir, Mode: 0o755}, nil)
	write(&tar.Header{Name: "blobs/sha256/", Typeflag: tar.TypeDir, Mode: 0o755}, nil)
	layout := testOCIJSON(t, ociLayoutDocument{ImageLayoutVersion: "1.0.0"})
	write(&tar.Header{Name: "oci-layout", Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(layout))}, layout)
	for _, blob := range blobs {
		write(&tar.Header{
			Name: "blobs/sha256/" + testSHA256(blob), Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(blob)),
		}, blob)
	}
	write(&tar.Header{Name: "index.json", Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(index))}, index)
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func testSHA256(contents []byte) string {
	digest := sha256.Sum256(contents)
	return hex.EncodeToString(digest[:])
}
