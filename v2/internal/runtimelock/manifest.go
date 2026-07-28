// Package runtimelock validates and verifies the stock Codex runtime manifest
// shared by agentserver harness images and independently released agentx.
package runtimelock

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

const (
	CurrentManifestVersion           = 1
	AppServerSchemaDigestAlgorithmV1 = "canonical-json-tree-v1"
	maxManifestBytes                 = 1024 * 1024
	maxManifestJSONValues            = 32 * 1024
	maxRuntimeArtifactBytes          = int64(2 * 1024 * 1024 * 1024)
	maxRuntimeFilesPerPlatform       = 32
	maxRuntimePlatformBytes          = int64(4 * 1024 * 1024 * 1024)
)

var (
	releasePattern      = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?$`)
	commitPattern       = regexp.MustCompile(`^[0-9a-f]{40}$`)
	digestPattern       = regexp.MustCompile(`^[0-9a-f]{64}$`)
	protocolPattern     = regexp.MustCompile(`^[0-9]+\.[0-9]+$`)
	platformPattern     = regexp.MustCompile(`^(linux|darwin|windows)-(amd64|arm64)$`)
	helperPattern       = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z._-]*$`)
	artifactPathPattern = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z._/-]*$`)
)

type Manifest struct {
	ManifestVersion                int                          `json:"manifestVersion"`
	CodexRelease                   string                       `json:"codexRelease"`
	CodexCommit                    string                       `json:"codexCommit"`
	AppServerSchemaSHA256          string                       `json:"appServerSchemaSha256"`
	AppServerSchemaDigestAlgorithm string                       `json:"appServerSchemaDigestAlgorithm"`
	ExecProtocolSourceSHA256       string                       `json:"execProtocolSourceSha256"`
	CheckpointAllowlistVersion     int                          `json:"checkpointAllowlistVersion"`
	AgentxProtocolVersion          string                       `json:"agentxProtocolVersion"`
	Artifacts                      map[string]PlatformArtifacts `json:"artifacts"`
}

type PlatformArtifacts struct {
	Codex   FileArtifact            `json:"codex"`
	Helpers map[string]FileArtifact `json:"helpers"`
}

type FileArtifact struct {
	Path      string `json:"path"`
	SourceURL string `json:"sourceUrl"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"sizeBytes"`
}

// Parse validates manifest bytes after the caller has verified the detached
// release signature against its configured trust root.
func Parse(data []byte) (Manifest, error) {
	if len(data) == 0 {
		return Manifest{}, errors.New("runtime manifest is empty")
	}
	if len(data) > maxManifestBytes {
		return Manifest{}, fmt.Errorf("runtime manifest exceeds %d bytes", maxManifestBytes)
	}
	if err := validateJSONDocument(data, maxManifestJSONValues); err != nil {
		return Manifest{}, fmt.Errorf("validate runtime manifest JSON: %w", err)
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode runtime manifest: %w", err)
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func (m Manifest) Validate() error {
	if m.ManifestVersion != CurrentManifestVersion {
		return fmt.Errorf("manifestVersion must be %d", CurrentManifestVersion)
	}
	if !releasePattern.MatchString(m.CodexRelease) {
		return errors.New("codexRelease must be a semantic stock release version")
	}
	if !commitPattern.MatchString(m.CodexCommit) {
		return errors.New("codexCommit must be a lowercase 40-character Git SHA")
	}
	if err := validateDigest("appServerSchemaSha256", m.AppServerSchemaSHA256); err != nil {
		return err
	}
	if m.AppServerSchemaDigestAlgorithm != AppServerSchemaDigestAlgorithmV1 {
		return fmt.Errorf("appServerSchemaDigestAlgorithm must be %q", AppServerSchemaDigestAlgorithmV1)
	}
	if err := validateDigest("execProtocolSourceSha256", m.ExecProtocolSourceSHA256); err != nil {
		return err
	}
	if m.CheckpointAllowlistVersion < 1 {
		return errors.New("checkpointAllowlistVersion must be positive")
	}
	if !protocolPattern.MatchString(m.AgentxProtocolVersion) {
		return errors.New("agentxProtocolVersion must be major.minor")
	}
	if len(m.Artifacts) == 0 {
		return errors.New("artifacts must contain at least one platform")
	}
	if len(m.Artifacts) > 6 {
		return errors.New("artifacts contains too many platforms")
	}

	platforms := make([]string, 0, len(m.Artifacts))
	for platform := range m.Artifacts {
		platforms = append(platforms, platform)
	}
	sort.Strings(platforms)
	for _, platform := range platforms {
		if !platformPattern.MatchString(platform) {
			return fmt.Errorf("artifacts platform %q is not supported", platform)
		}
		artifacts := m.Artifacts[platform]
		if artifacts.Helpers == nil {
			return fmt.Errorf("artifacts[%q].helpers must be present", platform)
		}
		if err := artifacts.Codex.validate(fmt.Sprintf("artifacts[%q].codex", platform)); err != nil {
			return err
		}
		if len(artifacts.Helpers) > maxRuntimeFilesPerPlatform-1 {
			return fmt.Errorf("artifacts[%q] contains too many helpers", platform)
		}
		totalBytes := artifacts.Codex.SizeBytes
		paths := map[string]string{artifacts.Codex.Path: "codex"}

		helperNames := make([]string, 0, len(artifacts.Helpers))
		for name := range artifacts.Helpers {
			helperNames = append(helperNames, name)
		}
		sort.Strings(helperNames)
		for _, name := range helperNames {
			if !helperPattern.MatchString(name) {
				return fmt.Errorf("artifacts[%q].helpers name %q is invalid", platform, name)
			}
			helper := artifacts.Helpers[name]
			if err := helper.validate(fmt.Sprintf("artifacts[%q].helpers[%q]", platform, name)); err != nil {
				return err
			}
			if helper.SizeBytes > maxRuntimePlatformBytes-totalBytes {
				return fmt.Errorf("artifacts[%q] exceeds %d total bytes", platform, maxRuntimePlatformBytes)
			}
			totalBytes += helper.SizeBytes
			if owner, duplicate := paths[helper.Path]; duplicate {
				return fmt.Errorf("artifacts[%q] path %q is shared by %s and helper %q", platform, helper.Path, owner, name)
			}
			paths[helper.Path] = "helper " + name
		}
	}
	return nil
}

func (a FileArtifact) validate(field string) error {
	if len(a.Path) > 512 || a.Path == "." || !fs.ValidPath(a.Path) || strings.ContainsRune(a.Path, '\\') || !artifactPathPattern.MatchString(a.Path) {
		return fmt.Errorf("%s.path must be a normalized slash-separated relative path", field)
	}
	parsed, err := url.Parse(a.SourceURL)
	if len(a.SourceURL) > 2048 || err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("%s.sourceUrl must be a stable credential-free HTTPS URL without query or fragment", field)
	}
	if err := validateDigest(field+".sha256", a.SHA256); err != nil {
		return err
	}
	if a.SizeBytes < 1 {
		return fmt.Errorf("%s.sizeBytes must be positive", field)
	}
	if a.SizeBytes > maxRuntimeArtifactBytes {
		return fmt.Errorf("%s.sizeBytes exceeds %d", field, maxRuntimeArtifactBytes)
	}
	return nil
}

func validateDigest(field, value string) error {
	if !digestPattern.MatchString(value) {
		return fmt.Errorf("%s must be lowercase 64-character SHA-256 hex", field)
	}
	return nil
}

func validateJSONDocument(data []byte, maxValues int) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	values := 0
	if err := validateJSONValue(decoder, &values, maxValues); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("document contains more than one JSON value")
		}
		return err
	}
	return nil
}

func validateJSONValue(decoder *json.Decoder, values *int, maxValues int) error {
	(*values)++
	if *values > maxValues {
		return fmt.Errorf("document exceeds %d JSON values", maxValues)
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
				return errors.New("object key must be a string")
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("duplicate object key %q", key)
			}
			seen[key] = struct{}{}
			if err := validateJSONValue(decoder, values, maxValues); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim('}') {
			return errors.New("object did not terminate")
		}
	case '[':
		for decoder.More() {
			if err := validateJSONValue(decoder, values, maxValues); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim(']') {
			return errors.New("array did not terminate")
		}
	default:
		return fmt.Errorf("unexpected delimiter %q", delimiter)
	}
	return nil
}
