package productionimage

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"time"
)

const (
	ociImageLayoutMediaType   = "application/vnd.oci.image.index.v1+json"
	ociImageManifestMediaType = "application/vnd.oci.image.manifest.v1+json"
	ociImageConfigMediaType   = "application/vnd.oci.image.config.v1+json"
	ociGzipLayerMediaType     = "application/vnd.oci.image.layer.v1.tar+gzip"
	ociDefaultPath            = "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

	maximumOCIDocumentBytes = int64(1024 * 1024)
	maximumOCIBlobBytes     = int64(512 * 1024 * 1024)
	maximumOCITotalBytes    = int64(768 * 1024 * 1024)
	maximumOCILayerBytes    = int64(512 * 1024 * 1024)
	maximumOCIEntries       = 128
)

type ociArchive struct {
	layout []byte
	index  []byte
	blobs  map[string][]byte
}

type ociLayoutDocument struct {
	ImageLayoutVersion string `json:"imageLayoutVersion"`
}

type ociPlatform struct {
	Architecture string `json:"architecture"`
	OS           string `json:"os"`
}

type ociDescriptor struct {
	MediaType   string            `json:"mediaType"`
	Digest      string            `json:"digest"`
	Size        int64             `json:"size"`
	Platform    *ociPlatform      `json:"platform,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

type ociIndexDocument struct {
	SchemaVersion int             `json:"schemaVersion"`
	MediaType     string          `json:"mediaType"`
	Manifests     []ociDescriptor `json:"manifests"`
}

type ociManifestDocument struct {
	SchemaVersion int             `json:"schemaVersion"`
	MediaType     string          `json:"mediaType"`
	Config        ociDescriptor   `json:"config"`
	Layers        []ociDescriptor `json:"layers"`
}

type ociImageConfig struct {
	Architecture string           `json:"architecture"`
	Config       ociRuntimeConfig `json:"config"`
	Created      string           `json:"created"`
	History      []ociHistory     `json:"history"`
	OS           string           `json:"os"`
	RootFS       ociRootFS        `json:"rootfs"`
}

type ociRuntimeConfig struct {
	User       string            `json:"User"`
	Env        []string          `json:"Env"`
	Entrypoint []string          `json:"Entrypoint,omitempty"`
	Cmd        []string          `json:"Cmd,omitempty"`
	WorkingDir string            `json:"WorkingDir"`
	Labels     map[string]string `json:"Labels"`
	StopSignal string            `json:"StopSignal"`
}

type ociHistory struct {
	Created    string `json:"created"`
	CreatedBy  string `json:"created_by"`
	Comment    string `json:"comment"`
	EmptyLayer bool   `json:"empty_layer,omitempty"`
}

type ociRootFS struct {
	Type    string   `json:"type"`
	DiffIDs []string `json:"diff_ids"`
}

type OCIImageEvidence struct {
	ImageManifestDigest string
}

// VerifyImageOCI proves the saved OCI image itself has one manifest for the
// platform named by the external manifest, the locked runtime configuration,
// exact descriptor digests, and
// only manifest-declared rootfs entries. It intentionally avoids a container
// export because runtimes inject dev, proc, sys, hostname, and hosts entries.
func VerifyImageOCI(reader io.Reader, manifestBytes []byte) error {
	_, err := VerifyImageOCIWithEvidence(reader, manifestBytes)
	return err
}

// VerifyImageOCIWithEvidence performs the same closed-world verification and
// returns the digest of the exact single-platform OCI image manifest. A
// production deployment may pin that digest only after this verification has
// succeeded; the digest is never inferred from a tag or builder output.
func VerifyImageOCIWithEvidence(reader io.Reader, manifestBytes []byte) (OCIImageEvidence, error) {
	if reader == nil {
		return OCIImageEvidence{}, errors.New("production OCI archive reader is required")
	}
	manifest, err := ParseManifest(manifestBytes)
	if err != nil {
		return OCIImageEvidence{}, err
	}
	directories := make(map[string]DirectoryEntry, len(manifest.Directories))
	for _, entry := range manifest.Directories {
		directories[entry.Path] = entry
	}
	files := fileMap(manifest.Files)
	files[ManifestPath] = FileEntry{
		Path: ManifestPath, SHA256: sha256Hex(manifestBytes), SizeBytes: int64(len(manifestBytes)), Mode: 0o444,
	}
	return verifyOCIArchiveWithEvidence(reader, manifest, directories, files)
}

func verifyOCIArchive(reader io.Reader, manifest Manifest, directories map[string]DirectoryEntry, files map[string]FileEntry) error {
	_, err := verifyOCIArchiveWithEvidence(reader, manifest, directories, files)
	return err
}

func verifyOCIArchiveWithEvidence(reader io.Reader, manifest Manifest, directories map[string]DirectoryEntry, files map[string]FileEntry) (OCIImageEvidence, error) {
	archive, err := readOCIArchive(reader)
	if err != nil {
		return OCIImageEvidence{}, err
	}
	var layout ociLayoutDocument
	if err := decodeOCIDocument("OCI layout", archive.layout, &layout); err != nil {
		return OCIImageEvidence{}, err
	}
	if layout.ImageLayoutVersion != "1.0.0" {
		return OCIImageEvidence{}, errors.New("production OCI archive has unsupported image layout version")
	}

	referenced := make(map[string]struct{}, len(archive.blobs))
	var rootIndex ociIndexDocument
	if err := decodeOCIDocument("OCI root index", archive.index, &rootIndex); err != nil {
		return OCIImageEvidence{}, err
	}
	architecture := platformArchitecture(manifest.Platform)
	manifestBytes, err := resolveOCIImageManifest(rootIndex, architecture, archive.blobs, referenced)
	if err != nil {
		return OCIImageEvidence{}, err
	}

	var imageManifest ociManifestDocument
	if err := decodeOCIDocument("OCI image manifest", manifestBytes, &imageManifest); err != nil {
		return OCIImageEvidence{}, err
	}
	if imageManifest.SchemaVersion != 2 || imageManifest.MediaType != ociImageManifestMediaType || len(imageManifest.Layers) != 2 {
		return OCIImageEvidence{}, errors.New("production OCI image manifest must contain exactly two layers")
	}
	if imageManifest.Config.Platform != nil || len(imageManifest.Config.Annotations) != 0 {
		return OCIImageEvidence{}, errors.New("production OCI config descriptor contains unexpected metadata")
	}
	configBytes, err := resolveOCIDescriptor(imageManifest.Config, ociImageConfigMediaType, archive.blobs, referenced)
	if err != nil {
		return OCIImageEvidence{}, fmt.Errorf("resolve OCI image config: %w", err)
	}

	var config ociImageConfig
	if err := decodeOCIDocument("OCI image config", configBytes, &config); err != nil {
		return OCIImageEvidence{}, err
	}
	if err := validateOCIImageConfig(config, manifest); err != nil {
		return OCIImageEvidence{}, err
	}
	if len(config.RootFS.DiffIDs) != len(imageManifest.Layers) {
		return OCIImageEvidence{}, errors.New("production OCI config diff ID count differs from layer count")
	}

	entryVerifier := newTarEntryVerifier(directories, files)
	for index, descriptor := range imageManifest.Layers {
		if descriptor.Platform != nil || len(descriptor.Annotations) != 0 {
			return OCIImageEvidence{}, fmt.Errorf("production OCI layer %d contains unexpected metadata", index)
		}
		layerBytes, err := resolveOCIDescriptor(descriptor, ociGzipLayerMediaType, archive.blobs, referenced)
		if err != nil {
			return OCIImageEvidence{}, fmt.Errorf("resolve OCI layer %d: %w", index, err)
		}
		diffID, err := verifyOCILayer(layerBytes, entryVerifier)
		if err != nil {
			return OCIImageEvidence{}, fmt.Errorf("verify OCI layer %d: %w", index, err)
		}
		if config.RootFS.DiffIDs[index] != diffID {
			return OCIImageEvidence{}, fmt.Errorf("production OCI layer %d uncompressed digest differs from config", index)
		}
	}
	if err := entryVerifier.complete(); err != nil {
		return OCIImageEvidence{}, err
	}
	if len(referenced) != len(archive.blobs) {
		return OCIImageEvidence{}, errors.New("production OCI archive contains unreferenced blobs")
	}
	return OCIImageEvidence{ImageManifestDigest: "sha256:" + sha256Hex(manifestBytes)}, nil
}

// resolveOCIImageManifest accepts both OCI archive layouts emitted by the
// production builders. Apple container save emits a named root index whose
// sole descriptor resolves to a platform index. Docker BuildKit's OCI exporter
// emits a root index whose sole descriptor resolves directly to the image
// manifest. In either layout there is exactly one image, and the image config
// remains the final authority for the required OS and architecture.
func resolveOCIImageManifest(rootIndex ociIndexDocument, architecture string, blobs map[string][]byte, referenced map[string]struct{}) ([]byte, error) {
	if rootIndex.SchemaVersion != 2 || rootIndex.MediaType != ociImageLayoutMediaType || len(rootIndex.Manifests) != 1 {
		return nil, errors.New("validate OCI root index: index must contain exactly one OCI image descriptor")
	}
	rootDescriptor := rootIndex.Manifests[0]
	switch rootDescriptor.MediaType {
	case ociImageManifestMediaType:
		if rootDescriptor.Platform != nil &&
			(rootDescriptor.Platform.OS != "linux" || rootDescriptor.Platform.Architecture != architecture) {
			return nil, fmt.Errorf("validate OCI root index: direct descriptor must select linux/%s", architecture)
		}
		manifestBytes, err := resolveOCIDescriptor(rootDescriptor, ociImageManifestMediaType, blobs, referenced)
		if err != nil {
			return nil, fmt.Errorf("resolve OCI image manifest: %w", err)
		}
		return manifestBytes, nil
	case ociImageLayoutMediaType:
		if rootDescriptor.Platform != nil {
			return nil, errors.New("validate OCI root index: platform index descriptor must not select a platform")
		}
		innerIndexBytes, err := resolveOCIDescriptor(rootDescriptor, ociImageLayoutMediaType, blobs, referenced)
		if err != nil {
			return nil, fmt.Errorf("resolve OCI platform index: %w", err)
		}

		var platformIndex ociIndexDocument
		if err := decodeOCIDocument("OCI platform index", innerIndexBytes, &platformIndex); err != nil {
			return nil, err
		}
		if err := validateOCIIndex(platformIndex, ociImageManifestMediaType, true, architecture); err != nil {
			return nil, fmt.Errorf("validate OCI platform index: %w", err)
		}
		manifestBytes, err := resolveOCIDescriptor(platformIndex.Manifests[0], ociImageManifestMediaType, blobs, referenced)
		if err != nil {
			return nil, fmt.Errorf("resolve OCI image manifest: %w", err)
		}
		return manifestBytes, nil
	default:
		return nil, errors.New("validate OCI root index: index descriptor has unexpected media type")
	}
}

func readOCIArchive(reader io.Reader) (ociArchive, error) {
	result := ociArchive{blobs: make(map[string][]byte)}
	wantedDirectories := map[string]struct{}{"blobs": {}, "blobs/sha256": {}}
	seenDirectories := make(map[string]struct{}, len(wantedDirectories))
	seenFiles := make(map[string]struct{})
	archive := tar.NewReader(reader)
	var totalBlobBytes int64
	seenRoot := false
	for entryCount := 0; ; entryCount++ {
		if entryCount >= maximumOCIEntries {
			return ociArchive{}, errors.New("production OCI archive contains too many entries")
		}
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return ociArchive{}, fmt.Errorf("read production OCI archive: %w", err)
		}
		name, isRoot, err := normalizeTarPath(header.Name)
		if err != nil {
			return ociArchive{}, fmt.Errorf("normalize production OCI archive entry: %w", err)
		}
		if isRoot {
			if seenRoot || header.Typeflag != tar.TypeDir {
				return ociArchive{}, errors.New("production OCI archive root is not a directory")
			}
			seenRoot = true
			continue
		}
		if header.Typeflag == tar.TypeDir {
			if _, wanted := wantedDirectories[name]; !wanted {
				return ociArchive{}, fmt.Errorf("production OCI archive contains unexpected directory %s", name)
			}
			if _, duplicate := seenDirectories[name]; duplicate {
				return ociArchive{}, fmt.Errorf("production OCI archive repeats directory %s", name)
			}
			seenDirectories[name] = struct{}{}
			continue
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			return ociArchive{}, fmt.Errorf("production OCI archive contains forbidden type %d at %s", header.Typeflag, name)
		}
		if _, duplicate := seenFiles[name]; duplicate {
			return ociArchive{}, fmt.Errorf("production OCI archive repeats file %s", name)
		}
		seenFiles[name] = struct{}{}
		switch {
		case name == "oci-layout" || name == "index.json":
			contents, err := readOCIEntry(archive, header.Size, maximumOCIDocumentBytes)
			if err != nil {
				return ociArchive{}, fmt.Errorf("read production OCI file %s: %w", name, err)
			}
			if name == "oci-layout" {
				result.layout = contents
			} else {
				result.index = contents
			}
		case strings.HasPrefix(name, "blobs/sha256/"):
			digest := strings.TrimPrefix(name, "blobs/sha256/")
			if !digestPattern.MatchString(digest) {
				return ociArchive{}, fmt.Errorf("production OCI archive has invalid blob path %s", name)
			}
			if header.Size <= 0 || header.Size > maximumOCIBlobBytes || totalBlobBytes > maximumOCITotalBytes-header.Size {
				return ociArchive{}, fmt.Errorf("production OCI blob %s exceeds size limits", digest)
			}
			contents, err := readOCIEntry(archive, header.Size, maximumOCIBlobBytes)
			if err != nil {
				return ociArchive{}, fmt.Errorf("read production OCI blob %s: %w", digest, err)
			}
			if actual := sha256Hex(contents); actual != digest {
				return ociArchive{}, fmt.Errorf("production OCI blob %s has digest %s", digest, actual)
			}
			result.blobs["sha256:"+digest] = contents
			totalBlobBytes += header.Size
		default:
			return ociArchive{}, fmt.Errorf("production OCI archive contains unexpected file %s", name)
		}
	}
	if len(result.layout) == 0 || len(result.index) == 0 || len(result.blobs) == 0 {
		return ociArchive{}, errors.New("production OCI archive is incomplete")
	}
	for directory := range wantedDirectories {
		if _, found := seenDirectories[directory]; !found {
			return ociArchive{}, fmt.Errorf("production OCI archive lacks directory %s", directory)
		}
	}
	return result, nil
}

func readOCIEntry(reader io.Reader, size, maximum int64) ([]byte, error) {
	if size <= 0 || size > maximum {
		return nil, fmt.Errorf("entry size %d is outside 1..%d", size, maximum)
	}
	contents := make([]byte, size)
	if _, err := io.ReadFull(reader, contents); err != nil {
		return nil, err
	}
	return contents, nil
}

func decodeOCIDocument(label string, raw []byte, destination any) error {
	if len(raw) == 0 || int64(len(raw)) > maximumOCIDocumentBytes {
		return fmt.Errorf("%s must contain between 1 and %d bytes", label, maximumOCIDocumentBytes)
	}
	if err := validateJSONDocument(raw, 128); err != nil {
		return fmt.Errorf("validate %s JSON: %w", label, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode %s: %w", label, err)
	}
	if err := finishJSON(decoder); err != nil {
		return fmt.Errorf("finish %s: %w", label, err)
	}
	return nil
}

func validateOCIIndex(index ociIndexDocument, descriptorMediaType string, requirePlatform bool, architecture string) error {
	if index.SchemaVersion != 2 || index.MediaType != ociImageLayoutMediaType || len(index.Manifests) != 1 {
		return errors.New("index must contain exactly one OCI image descriptor")
	}
	descriptor := index.Manifests[0]
	if descriptor.MediaType != descriptorMediaType {
		return errors.New("index descriptor has unexpected media type")
	}
	if requirePlatform {
		if descriptor.Platform == nil || descriptor.Platform.OS != "linux" || descriptor.Platform.Architecture != architecture {
			return fmt.Errorf("index descriptor must select linux/%s", architecture)
		}
		if len(descriptor.Annotations) != 0 {
			return errors.New("platform descriptor contains unexpected annotations")
		}
	} else if descriptor.Platform != nil {
		return errors.New("root index descriptor contains unexpected platform")
	}
	return nil
}

func resolveOCIDescriptor(descriptor ociDescriptor, mediaType string, blobs map[string][]byte, referenced map[string]struct{}) ([]byte, error) {
	if descriptor.MediaType != mediaType || !strings.HasPrefix(descriptor.Digest, "sha256:") ||
		!digestPattern.MatchString(strings.TrimPrefix(descriptor.Digest, "sha256:")) || descriptor.Size <= 0 {
		return nil, errors.New("invalid OCI descriptor")
	}
	contents, found := blobs[descriptor.Digest]
	if !found || int64(len(contents)) != descriptor.Size {
		return nil, errors.New("OCI descriptor blob is missing or has the wrong size")
	}
	if _, duplicate := referenced[descriptor.Digest]; duplicate {
		return nil, errors.New("OCI descriptor repeats a referenced blob")
	}
	referenced[descriptor.Digest] = struct{}{}
	return contents, nil
}

func validateOCIImageConfig(config ociImageConfig, manifest Manifest) error {
	architecture := platformArchitecture(manifest.Platform)
	if config.Architecture != architecture || config.OS != "linux" || config.RootFS.Type != "layers" || len(config.RootFS.DiffIDs) != 2 {
		return fmt.Errorf("production OCI config is not a two-layer linux/%s image", architecture)
	}
	if _, err := time.Parse(time.RFC3339Nano, config.Created); err != nil {
		return errors.New("production OCI config has invalid creation time")
	}
	for _, digest := range config.RootFS.DiffIDs {
		if !strings.HasPrefix(digest, "sha256:") || !digestPattern.MatchString(strings.TrimPrefix(digest, "sha256:")) {
			return errors.New("production OCI config has invalid diff ID")
		}
	}
	expectedUser := "65534:65534"
	expectedWorkingDirectory := "/"
	expectedTitle := "agentserver v2 production services"
	expectedDescription := "Closed-world service binaries for agentserver v2"
	if manifest.Kind == KindHarness {
		expectedUser = "65530:65530"
		expectedTitle = "agentserver v2 production harness"
		expectedDescription = "Fork-based stateless harness with pinned stock Codex app-server"
	} else if manifest.Kind == KindManagedSandbox {
		expectedWorkingDirectory = "/workspace"
		expectedTitle = "agentserver v2 managed sandbox"
		expectedDescription = "Closed-world TAE sandbox with pinned Lark CLI and skill"
	}
	expectedLabels := map[string]string{
		"org.opencontainers.image.description": expectedDescription,
		"org.opencontainers.image.revision":    manifest.SourceRevision,
		"org.opencontainers.image.source":      "https://github.com/agentserver/agentserver",
		"org.opencontainers.image.title":       expectedTitle,
	}
	if config.Config.User != expectedUser || !slices.Equal(config.Config.Env, []string{ociDefaultPath}) ||
		len(config.Config.Entrypoint) != 0 || len(config.Config.Cmd) != 0 || config.Config.WorkingDir != expectedWorkingDirectory ||
		config.Config.StopSignal != "SIGTERM" || !maps.Equal(config.Config.Labels, expectedLabels) {
		return errors.New("production OCI runtime configuration differs from the locked profile")
	}
	nonEmptyLayers := 0
	for _, history := range config.History {
		if _, err := time.Parse(time.RFC3339Nano, history.Created); err != nil || history.CreatedBy == "" || history.Comment != "buildkit.dockerfile.v0" {
			return errors.New("production OCI config contains invalid history")
		}
		if !history.EmptyLayer {
			nonEmptyLayers++
		}
	}
	if nonEmptyLayers != 2 {
		return errors.New("production OCI config history does not describe exactly two layers")
	}
	return nil
}

func verifyOCILayer(compressed []byte, verifier *tarEntryVerifier) (string, error) {
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return "", fmt.Errorf("open gzip layer: %w", err)
	}
	hasher := sha256.New()
	limited := &io.LimitedReader{R: reader, N: maximumOCILayerBytes + 1}
	uncompressed := io.TeeReader(limited, hasher)
	verifyErr := verifier.verifyLayer(uncompressed)
	_, drainErr := io.Copy(io.Discard, uncompressed)
	closeErr := reader.Close()
	if limited.N == 0 {
		return "", errors.New("uncompressed OCI layer exceeds size limit")
	}
	if verifyErr != nil || drainErr != nil || closeErr != nil {
		return "", errors.Join(verifyErr, drainErr, closeErr)
	}
	return "sha256:" + hex.EncodeToString(hasher.Sum(nil)), nil
}
