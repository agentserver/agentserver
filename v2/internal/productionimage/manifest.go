// Package productionimage assembles and verifies the two closed-world
// linux/arm64 OCI root filesystems used by the agentserver v2 production
// deployment. It never downloads dependencies and never accepts an open-ended
// file list.
package productionimage

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"regexp"
	"slices"
	"strings"

	"github.com/agentserver/agentserver/v2/internal/stockruntime"
)

const (
	ManifestVersion = 1
	Platform        = "linux-arm64"
	GoToolchain     = "go1.26.5"

	KindService = "service"
	KindHarness = "harness"

	ManifestPath = "usr/share/agentserver/image-manifest.json"

	CABundleSourceImage = "docker.io/library/postgres@sha256:9b9fb55f7e3b2149854def33c728b781dc44d1c5e86492ad62912a527ae234b3"
	CABundleSHA256      = "657ca6ba4bc43138f89de75fb63794cbfaa897e0e110b069fd1367bd66a5bb6c"
	CABundleSizeBytes   = int64(219404)
	CABundlePath        = "etc/ssl/certs/ca-certificates.crt"

	RequirementsPath      = "etc/codex/requirements.toml"
	RequirementsSHA256    = "10a47c661234f111aa57ac11b9e2e97078b2c6ac5d3cd9a4a306f7d2f6a40917"
	RequirementsSizeBytes = int64(53)

	RuntimeManifestPath = "opt/agentserver/runtime/runtime-manifest.json"
	RuntimeBundleRoot   = "opt/agentserver/runtime/bundle"

	maximumManifestBytes = 1024 * 1024
	maximumManifestFiles = 32
)

var (
	digestPattern   = regexp.MustCompile(`^[0-9a-f]{64}$`)
	revisionPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

type Manifest struct {
	Version        int              `json:"version"`
	Kind           string           `json:"kind"`
	Platform       string           `json:"platform"`
	SourceRevision string           `json:"sourceRevision"`
	GoToolchain    string           `json:"goToolchain"`
	CABundleSource string           `json:"caBundleSource"`
	Directories    []DirectoryEntry `json:"directories"`
	Files          []FileEntry      `json:"files"`
}

type DirectoryEntry struct {
	Path string `json:"path"`
	Mode uint32 `json:"mode"`
	UID  uint32 `json:"uid"`
	GID  uint32 `json:"gid"`
}

type FileEntry struct {
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"sizeBytes"`
	Mode      uint32 `json:"mode"`
	UID       uint32 `json:"uid"`
	GID       uint32 `json:"gid"`
}

func ParseManifest(raw []byte) (Manifest, error) {
	if len(raw) == 0 || len(raw) > maximumManifestBytes {
		return Manifest{}, fmt.Errorf("production image manifest must contain between 1 and %d bytes", maximumManifestBytes)
	}
	if err := validateJSONDocument(raw, 4096); err != nil {
		return Manifest{}, fmt.Errorf("validate production image manifest JSON: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode production image manifest: %w", err)
	}
	if err := finishJSON(decoder); err != nil {
		return Manifest{}, fmt.Errorf("finish production image manifest: %w", err)
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func (manifest Manifest) Validate() error {
	if manifest.Version != ManifestVersion {
		return fmt.Errorf("production image manifest version must be %d", ManifestVersion)
	}
	if manifest.Kind != KindService && manifest.Kind != KindHarness {
		return errors.New("production image manifest kind must be service or harness")
	}
	if manifest.Platform != Platform {
		return fmt.Errorf("production image manifest platform must be %s", Platform)
	}
	if !revisionPattern.MatchString(manifest.SourceRevision) {
		return errors.New("production image source revision must be a lowercase 40-character Git SHA")
	}
	if manifest.GoToolchain != GoToolchain {
		return fmt.Errorf("production image Go toolchain must be %s", GoToolchain)
	}
	if manifest.CABundleSource != CABundleSourceImage {
		return errors.New("production image CA bundle source is not the pinned arm64 image")
	}
	wantDirectories := expectedDirectories(manifest.Kind)
	if !slices.Equal(manifest.Directories, wantDirectories) {
		return errors.New("production image manifest directory layout is not the closed-world profile")
	}
	if len(manifest.Files) == 0 || len(manifest.Files) > maximumManifestFiles {
		return errors.New("production image manifest has an invalid file count")
	}
	wantPaths := expectedFilePaths(manifest.Kind)
	if len(manifest.Files) != len(wantPaths) {
		return errors.New("production image manifest file set is incomplete")
	}
	for index, entry := range manifest.Files {
		if entry.Path != wantPaths[index] {
			return errors.New("production image manifest files must be sorted and match the closed-world profile")
		}
		if !fs.ValidPath(entry.Path) || strings.ContainsAny(entry.Path, "\\\r\n") || entry.Path == ManifestPath {
			return fmt.Errorf("production image manifest contains invalid payload path %q", entry.Path)
		}
		if !digestPattern.MatchString(entry.SHA256) || entry.SizeBytes < 1 || entry.SizeBytes > 1<<30 {
			return fmt.Errorf("production image payload %s has invalid digest or size", entry.Path)
		}
		if entry.UID != 0 || entry.GID != 0 || (entry.Mode != 0o444 && entry.Mode != 0o555) {
			return fmt.Errorf("production image payload %s has invalid ownership or mode", entry.Path)
		}
	}
	files := fileMap(manifest.Files)
	if err := requirePinnedFile(files, CABundlePath, CABundleSHA256, CABundleSizeBytes, 0o444); err != nil {
		return err
	}
	if manifest.Kind == KindHarness {
		if err := requirePinnedFile(files, RequirementsPath, RequirementsSHA256, RequirementsSizeBytes, 0o444); err != nil {
			return err
		}
		if err := requirePinnedFile(files, RuntimeManifestPath, stockruntime.ManifestSHA256, stockruntime.ManifestSizeBytes, 0o444); err != nil {
			return err
		}
		if err := requirePinnedFile(files, RuntimeBundleRoot+"/bin/codex", stockruntime.LinuxARM64CodexSHA256, stockruntime.LinuxARM64CodexSize, 0o555); err != nil {
			return err
		}
		if err := requirePinnedFile(files, RuntimeBundleRoot+"/codex-resources/bwrap", stockruntime.LinuxARM64BwrapSHA256, stockruntime.LinuxARM64BwrapSize, 0o555); err != nil {
			return err
		}
	}
	return nil
}

func MarshalManifest(manifest Manifest) ([]byte, error) {
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(manifest); err != nil {
		return nil, fmt.Errorf("encode production image manifest: %w", err)
	}
	return output.Bytes(), nil
}

func expectedDirectories(kind string) []DirectoryEntry {
	directories := []DirectoryEntry{
		{Path: "etc", Mode: 0o555},
		{Path: "etc/ssl", Mode: 0o555},
		{Path: "etc/ssl/certs", Mode: 0o555},
		{Path: "usr", Mode: 0o555},
		{Path: "usr/local", Mode: 0o555},
		{Path: "usr/local/bin", Mode: 0o555},
		{Path: "usr/share", Mode: 0o555},
		{Path: "usr/share/agentserver", Mode: 0o555},
	}
	if kind == KindHarness {
		directories = append(directories,
			DirectoryEntry{Path: "etc/codex", Mode: 0o555},
			DirectoryEntry{Path: "opt", Mode: 0o555},
			DirectoryEntry{Path: "opt/agentserver", Mode: 0o555},
			DirectoryEntry{Path: "opt/agentserver/runtime", Mode: 0o711},
			DirectoryEntry{Path: RuntimeBundleRoot, Mode: 0o711},
			DirectoryEntry{Path: RuntimeBundleRoot + "/bin", Mode: 0o555},
			DirectoryEntry{Path: RuntimeBundleRoot + "/codex-resources", Mode: 0o555},
		)
	}
	slices.SortFunc(directories, func(left, right DirectoryEntry) int { return strings.Compare(left.Path, right.Path) })
	return directories
}

func expectedFilePaths(kind string) []string {
	paths := []string{CABundlePath}
	for _, binary := range ExpectedBinaries(kind) {
		paths = append(paths, "usr/local/bin/"+binary)
	}
	if kind == KindHarness {
		paths = append(paths,
			RequirementsPath,
			RuntimeManifestPath,
			RuntimeBundleRoot+"/bin/codex",
			RuntimeBundleRoot+"/codex-resources/bwrap",
		)
	}
	slices.Sort(paths)
	return paths
}

func ExpectedBinaries(kind string) []string {
	var binaries []string
	switch kind {
	case KindService:
		binaries = []string{
			"agentserver-core", "agentserver-init", "agentserver-probe",
			"browser-gateway", "executor-gateway", "llmproxy",
		}
	case KindHarness:
		binaries = []string{
			"agentserver-init", "agentserver-probe", "harness-final-exec",
			"harness-pool", "harness-worker",
		}
	}
	slices.Sort(binaries)
	return binaries
}

func sourceCommand(binary string) string {
	if binary == "agentserver-init" {
		return "harness-init"
	}
	return binary
}

func fileMap(files []FileEntry) map[string]FileEntry {
	result := make(map[string]FileEntry, len(files))
	for _, entry := range files {
		result[entry.Path] = entry
	}
	return result
}

func requirePinnedFile(files map[string]FileEntry, path, digest string, size int64, mode uint32) error {
	entry, found := files[path]
	if !found || entry.SHA256 != digest || entry.SizeBytes != size || entry.Mode != mode {
		return fmt.Errorf("production image payload %s does not match its pinned release artifact", path)
	}
	return nil
}

func sha256Hex(contents []byte) string {
	digest := sha256.Sum256(contents)
	return hex.EncodeToString(digest[:])
}

func validateJSONDocument(raw []byte, maximumValues int) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	values := 0
	if err := validateJSONValue(decoder, &values, maximumValues); err != nil {
		return err
	}
	return finishJSON(decoder)
}

func validateJSONValue(decoder *json.Decoder, values *int, maximum int) error {
	*values++
	if *values > maximum {
		return fmt.Errorf("document exceeds %d JSON values", maximum)
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, composite := token.(json.Delim)
	if !composite {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("object key is not a string")
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("duplicate object key %q", key)
			}
			seen[key] = struct{}{}
			if err := validateJSONValue(decoder, values, maximum); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return errors.Join(errors.New("object did not terminate"), err)
		}
	case '[':
		for decoder.More() {
			if err := validateJSONValue(decoder, values, maximum); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return errors.Join(errors.New("array did not terminate"), err)
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
	return nil
}

func finishJSON(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("document contains more than one JSON value")
		}
		return err
	}
	return nil
}
