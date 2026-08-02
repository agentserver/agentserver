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
	archive, manifest, directories, files := testOCIImage(t, false)
	if err := verifyOCIArchive(bytes.NewReader(archive), manifest, directories, files); err != nil {
		t.Fatal(err)
	}
}

func TestOCIImageVerifierRejectsUnreferencedBlob(t *testing.T) {
	archive, manifest, directories, files := testOCIImage(t, true)
	err := verifyOCIArchive(bytes.NewReader(archive), manifest, directories, files)
	if err == nil || !strings.Contains(err.Error(), "unreferenced blobs") {
		t.Fatalf("unreferenced blob error = %v", err)
	}
}

type testLayerEntry struct {
	name      string
	mode      int64
	contents  []byte
	directory bool
}

func testOCIImage(t *testing.T, extraBlob bool) ([]byte, Manifest, map[string]DirectoryEntry, map[string]FileEntry) {
	t.Helper()
	revision := strings.Repeat("a", 40)
	manifest := Manifest{Kind: KindService, Platform: PlatformLinuxARM64, SourceRevision: revision}
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
	created := "2026-08-02T00:00:00Z"
	config := ociImageConfig{
		Architecture: "arm64",
		Config: ociRuntimeConfig{
			User: "65534:65534", Env: []string{ociDefaultPath}, WorkingDir: "/", StopSignal: "SIGTERM",
			Labels: map[string]string{
				"org.opencontainers.image.description": "Closed-world service binaries for agentserver v2",
				"org.opencontainers.image.revision":    revision,
				"org.opencontainers.image.source":      "https://github.com/agentserver/agentserver",
				"org.opencontainers.image.title":       "agentserver v2 production services",
			},
		},
		Created: created,
		History: []ociHistory{
			{Created: created, CreatedBy: "ADD rootfs.tar /", Comment: "buildkit.dockerfile.v0"},
			{Created: created, CreatedBy: "COPY ca", Comment: "buildkit.dockerfile.v0"},
		},
		OS: "linux",
		RootFS: ociRootFS{Type: "layers", DiffIDs: []string{
			"sha256:" + testSHA256(firstTar),
			"sha256:" + testSHA256(secondTar),
		}},
	}
	configBytes := testOCIJSON(t, config)
	imageManifest := ociManifestDocument{
		SchemaVersion: 2,
		MediaType:     ociImageManifestMediaType,
		Config:        testOCIDescriptor(ociImageConfigMediaType, configBytes),
		Layers: []ociDescriptor{
			testOCIDescriptor(ociGzipLayerMediaType, firstLayer),
			testOCIDescriptor(ociGzipLayerMediaType, secondLayer),
		},
	}
	imageManifestBytes := testOCIJSON(t, imageManifest)
	platformIndex := ociIndexDocument{
		SchemaVersion: 2,
		MediaType:     ociImageLayoutMediaType,
		Manifests: []ociDescriptor{{
			MediaType: ociImageManifestMediaType,
			Digest:    "sha256:" + testSHA256(imageManifestBytes),
			Size:      int64(len(imageManifestBytes)),
			Platform:  &ociPlatform{Architecture: "arm64", OS: "linux"},
		}},
	}
	platformIndexBytes := testOCIJSON(t, platformIndex)
	rootIndex := ociIndexDocument{
		SchemaVersion: 2,
		MediaType:     ociImageLayoutMediaType,
		Manifests:     []ociDescriptor{testOCIDescriptor(ociImageLayoutMediaType, platformIndexBytes)},
	}
	blobs := [][]byte{configBytes, firstLayer, secondLayer, imageManifestBytes, platformIndexBytes}
	if extraBlob {
		blobs = append(blobs, []byte("unreferenced"))
	}
	return testOCIOuterTar(t, testOCIJSON(t, rootIndex), blobs), manifest, directories, files
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
