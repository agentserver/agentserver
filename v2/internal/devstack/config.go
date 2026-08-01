// Package devstack prepares the complete local authority and configuration
// material used by the explicitly insecure agentserver v2 development stack.
// It is not a production deployment generator.
package devstack

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/agentserver/agentserver/v2/internal/braincatalog"
	"github.com/agentserver/agentserver/v2/internal/harnessworker"
	"github.com/agentserver/agentserver/v2/internal/runtimelock"
)

const (
	CurrentConfigVersion = 1

	maximumConfigBytes = int64(128 * 1024)
	maximumTextBytes   = 4096
)

var (
	uuidPattern       = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	policyToolPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,127}$`)
	policyVersionRE   = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z._-]{0,127}$`)
)

// ConfigDocument is the closed-world input accepted by agentserver-dev. Facts
// that already belong to the runtime manifest (platform, release, digests,
// protocol and checkpoint allowlist) are intentionally absent.
type ConfigDocument struct {
	Version     int                `json:"version"`
	DatabaseURL string             `json:"databaseUrl"`
	Authority   AuthorityDocument  `json:"authority"`
	Runtime     RuntimeDocument    `json:"runtime"`
	Network     NetworkDocument    `json:"network"`
	Model       ModelDocument      `json:"model"`
	Policy      PolicyDocument     `json:"policy"`
	Harness     HarnessDocument    `json:"harness"`
	Identities  IdentitiesDocument `json:"identities"`
}

type AuthorityDocument struct {
	WorkspaceID   string `json:"workspaceId"`
	SessionID     string `json:"sessionId"`
	ActorID       string `json:"actorId"`
	ExecutorID    string `json:"executorId"`
	EnvironmentID string `json:"environmentId"`
	AgentxVersion string `json:"agentxVersion"`
	WorkspaceRoot string `json:"workspaceRoot"`
	DisplayName   string `json:"displayName,omitempty"`
	Description   string `json:"description,omitempty"`
	DefaultCWD    string `json:"defaultCwd,omitempty"`
}

type RuntimeDocument struct {
	ManifestFile           string `json:"manifestFile"`
	BundleRoot             string `json:"bundleRoot"`
	AgentxBinary           string `json:"agentxBinary"`
	HarnessWorkerBinary    string `json:"harnessWorkerBinary"`
	HarnessFinalExecBinary string `json:"harnessFinalExecBinary"`
}

type NetworkDocument struct {
	CoreListenAddress            string `json:"coreListenAddress"`
	BrowserGatewayListenAddress  string `json:"browserGatewayListenAddress"`
	ExecutorGatewayListenAddress string `json:"executorGatewayListenAddress"`
	HarnessPoolListenAddress     string `json:"harnessPoolListenAddress"`
	HydraIntrospectionURL        string `json:"hydraIntrospectionUrl"`
	LLMProxyEndpoint             string `json:"llmproxyEndpoint"`
}

type ModelDocument struct {
	Name     string `json:"name"`
	Provider string `json:"provider"`
}

type PolicyDocument struct {
	Version      string   `json:"version"`
	AllowedTools []string `json:"allowedTools"`
}

type HarnessDocument struct {
	MaxConcurrentAttempts int    `json:"maxConcurrentAttempts"`
	MaxRunDuration        string `json:"maxRunDuration"`
	MaxApprovalTTL        string `json:"maxApprovalTtl"`
}

type IdentitiesDocument struct {
	WorkerUID uint32 `json:"workerUid"`
	WorkerGID uint32 `json:"workerGid"`
	AppUID    uint32 `json:"appUid"`
	AppGID    uint32 `json:"appGid"`
}

// LoadedConfig contains only validated, copied values and runtime-manifest
// facts derived from the exact bytes that will be referenced by the bundle.
type LoadedConfig struct {
	Document          ConfigDocument
	Manifest          runtimelock.Manifest
	ManifestBytes     []byte
	ManifestSHA256    string
	Platform          string
	MaxRunDuration    time.Duration
	MaxApprovalTTL    time.Duration
	CoreOrigin        string
	BrowserOrigin     string
	ExecutorOrigin    string
	HarnessPoolOrigin string
}

func LoadConfig(configPath string) (LoadedConfig, error) {
	raw, err := readSecretConfig(configPath)
	if err != nil {
		return LoadedConfig{}, err
	}
	limits := braincatalog.DefaultLimits()
	limits.MaxJSONValues = 2048
	limits.MaxJSONDepth = 16
	if _, _, err := braincatalog.DecodeCanonicalJSON(raw, int(maximumConfigBytes), limits); err != nil {
		return LoadedConfig{}, fmt.Errorf("validate insecure development stack JSON: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var document ConfigDocument
	if err := decoder.Decode(&document); err != nil {
		return LoadedConfig{}, fmt.Errorf("decode insecure development stack config: %w", err)
	}
	if err := finishJSON(decoder); err != nil {
		return LoadedConfig{}, fmt.Errorf("finish insecure development stack config: %w", err)
	}
	return ValidateConfig(document)
}

func ValidateConfig(document ConfigDocument) (LoadedConfig, error) {
	if document.Version != CurrentConfigVersion {
		return LoadedConfig{}, fmt.Errorf("insecure development stack version must be %d", CurrentConfigVersion)
	}
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		return LoadedConfig{}, errors.New("insecure development stack preparation is supported only on Linux and Darwin")
	}
	if err := validateDatabaseURL(document.DatabaseURL); err != nil {
		return LoadedConfig{}, err
	}
	if err := validateAuthority(document.Authority); err != nil {
		return LoadedConfig{}, err
	}
	if err := validateRuntimePaths(document.Runtime); err != nil {
		return LoadedConfig{}, err
	}
	if err := validateModel(document.Model); err != nil {
		return LoadedConfig{}, err
	}
	if err := validatePolicy(&document.Policy); err != nil {
		return LoadedConfig{}, err
	}
	if err := validateIdentities(document.Identities); err != nil {
		return LoadedConfig{}, err
	}
	if document.Harness.MaxConcurrentAttempts < 1 || document.Harness.MaxConcurrentAttempts > 64 {
		return LoadedConfig{}, errors.New("harness.maxConcurrentAttempts must be between 1 and 64")
	}
	maxRunDuration, err := time.ParseDuration(document.Harness.MaxRunDuration)
	if err != nil || maxRunDuration < time.Second || maxRunDuration > 24*time.Hour {
		return LoadedConfig{}, errors.New("harness.maxRunDuration must be a Go duration between 1s and 24h")
	}
	maxApprovalTTL, err := time.ParseDuration(document.Harness.MaxApprovalTTL)
	if err != nil || maxApprovalTTL < time.Second || maxApprovalTTL > 24*time.Hour {
		return LoadedConfig{}, errors.New("harness.maxApprovalTtl must be a Go duration between 1s and 24h")
	}
	if maxApprovalTTL > maxRunDuration {
		return LoadedConfig{}, errors.New("harness.maxApprovalTtl must not exceed harness.maxRunDuration")
	}

	addresses := []struct {
		name  string
		value string
	}{
		{"network.coreListenAddress", document.Network.CoreListenAddress},
		{"network.browserGatewayListenAddress", document.Network.BrowserGatewayListenAddress},
		{"network.executorGatewayListenAddress", document.Network.ExecutorGatewayListenAddress},
		{"network.harnessPoolListenAddress", document.Network.HarnessPoolListenAddress},
	}
	origins := make([]string, len(addresses))
	seenAddresses := make(map[string]struct{}, len(addresses))
	for index, address := range addresses {
		origin, canonicalAddress, err := validateLoopbackListenAddress(address.name, address.value)
		if err != nil {
			return LoadedConfig{}, err
		}
		if _, duplicate := seenAddresses[canonicalAddress]; duplicate {
			return LoadedConfig{}, errors.New("development service listen addresses must be distinct")
		}
		seenAddresses[canonicalAddress] = struct{}{}
		origins[index] = origin
	}
	hydraAddress, err := validateHydraURL(document.Network.HydraIntrospectionURL)
	if err != nil {
		return LoadedConfig{}, err
	}
	if _, duplicate := seenAddresses[hydraAddress]; duplicate {
		return LoadedConfig{}, errors.New("development Hydra fixture listener conflicts with a service listen address")
	}
	seenAddresses[hydraAddress] = struct{}{}
	llmproxyAddress, err := validateLLMProxyURL(document.Network.LLMProxyEndpoint)
	if err != nil {
		return LoadedConfig{}, err
	}
	if _, duplicate := seenAddresses[llmproxyAddress]; duplicate {
		return LoadedConfig{}, errors.New("development llmproxy fixture listener conflicts with another development listener")
	}

	manifestBytes, err := readImmutableFile("runtime manifest", document.Runtime.ManifestFile, 1024*1024, false)
	if err != nil {
		return LoadedConfig{}, err
	}
	manifest, err := runtimelock.Parse(manifestBytes)
	if err != nil {
		return LoadedConfig{}, fmt.Errorf("parse runtime manifest: %w", err)
	}
	if manifest.CodexRelease != "0.146.0" {
		return LoadedConfig{}, fmt.Errorf("runtime manifest Codex release must be 0.146.0 for profile %s", harnessworker.CodexConfigProfileStable0146)
	}
	if _, err := manifest.VerifyCurrentPlatform(document.Runtime.BundleRoot); err != nil {
		return LoadedConfig{}, fmt.Errorf("verify current-platform runtime bundle: %w", err)
	}
	manifestDigestRaw := sha256.Sum256(manifestBytes)
	manifestDigest := hex.EncodeToString(manifestDigestRaw[:])

	return LoadedConfig{
		Document: document, Manifest: manifest, ManifestBytes: append([]byte(nil), manifestBytes...),
		ManifestSHA256: manifestDigest, Platform: runtimelock.CurrentPlatform(),
		MaxRunDuration: maxRunDuration, MaxApprovalTTL: maxApprovalTTL,
		CoreOrigin: origins[0], BrowserOrigin: origins[1], ExecutorOrigin: origins[2], HarnessPoolOrigin: origins[3],
	}, nil
}

func validateDatabaseURL(value string) error {
	if err := validateCanonicalText("databaseUrl", value, 1, maximumTextBytes); err != nil {
		return err
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || parsed.Fragment != "" {
		return errors.New("databaseUrl must be a PostgreSQL URL without a fragment")
	}
	return nil
}

func validateAuthority(authority AuthorityDocument) error {
	for name, value := range map[string]string{
		"authority.workspaceId":   authority.WorkspaceID,
		"authority.sessionId":     authority.SessionID,
		"authority.actorId":       authority.ActorID,
		"authority.executorId":    authority.ExecutorID,
		"authority.environmentId": authority.EnvironmentID,
	} {
		if value == "00000000-0000-0000-0000-000000000000" || !uuidPattern.MatchString(value) {
			return fmt.Errorf("%s must be a non-zero canonical lowercase UUID", name)
		}
	}
	if authority.AgentxVersion != "0.1.0-dev" {
		return errors.New("authority.agentxVersion must exactly match the current insecure development agentx release 0.1.0-dev")
	}
	if err := validateExistingCanonicalDirectory("authority.workspaceRoot", authority.WorkspaceRoot); err != nil {
		return err
	}
	if err := validateCanonicalText("authority.displayName", authority.DisplayName, 0, 256); err != nil {
		return err
	}
	if err := validateCanonicalText("authority.description", authority.Description, 0, 2048); err != nil {
		return err
	}
	if authority.DefaultCWD != "" {
		if err := validateRelativePath(authority.DefaultCWD); err != nil {
			return fmt.Errorf("authority.defaultCwd: %w", err)
		}
		candidate := filepath.Join(authority.WorkspaceRoot, authority.DefaultCWD)
		if err := validateExistingContainedDirectory(authority.WorkspaceRoot, candidate); err != nil {
			return fmt.Errorf("authority.defaultCwd: %w", err)
		}
	}
	return nil
}

func validateRuntimePaths(document RuntimeDocument) error {
	if err := validateImmutablePath("runtime.manifestFile", document.ManifestFile, false); err != nil {
		return err
	}
	if err := validateExistingCanonicalDirectory("runtime.bundleRoot", document.BundleRoot); err != nil {
		return err
	}
	for name, value := range map[string]string{
		"runtime.agentxBinary":           document.AgentxBinary,
		"runtime.harnessWorkerBinary":    document.HarnessWorkerBinary,
		"runtime.harnessFinalExecBinary": document.HarnessFinalExecBinary,
	} {
		if err := validateImmutablePath(name, value, true); err != nil {
			return err
		}
	}
	return nil
}

func validateModel(document ModelDocument) error {
	for name, value := range map[string]string{"model.name": document.Name, "model.provider": document.Provider} {
		if err := validateCanonicalText(name, value, 1, 256); err != nil {
			return err
		}
	}
	return nil
}

func validatePolicy(document *PolicyDocument) error {
	if document == nil || !policyVersionRE.MatchString(document.Version) {
		return errors.New("policy.version must be canonical alphanumeric version text")
	}
	if len(document.AllowedTools) < 1 || len(document.AllowedTools) > 3 {
		return errors.New("policy.allowedTools must contain between one and three implemented executor tools")
	}
	tools := append([]string(nil), document.AllowedTools...)
	slices.Sort(tools)
	known := map[string]struct{}{"list_environments": {}, "read_file": {}, "shell": {}}
	for index, tool := range tools {
		if !policyToolPattern.MatchString(tool) {
			return errors.New("policy.allowedTools contains a non-canonical tool name")
		}
		if _, ok := known[tool]; !ok {
			return fmt.Errorf("policy.allowedTools contains unsupported tool %q", tool)
		}
		if index > 0 && tools[index-1] == tool {
			return fmt.Errorf("policy.allowedTools repeats %q", tool)
		}
	}
	if _, found := slices.BinarySearch(tools, "list_environments"); !found {
		return errors.New("policy.allowedTools must include list_environments for the scripted development fixture")
	}
	document.AllowedTools = tools
	return nil
}

func validateIdentities(document IdentitiesDocument) error {
	for name, value := range map[string]uint32{
		"identities.workerUid": document.WorkerUID,
		"identities.workerGid": document.WorkerGID,
		"identities.appUid":    document.AppUID,
		"identities.appGid":    document.AppGID,
	} {
		if value == 0 || value == ^uint32(0) {
			return fmt.Errorf("%s must be an unprivileged uint32 identity", name)
		}
	}
	if document.WorkerUID == document.AppUID || document.WorkerGID == document.AppGID {
		return errors.New("worker and app uid/gid identities must be distinct")
	}
	return nil
}

func validateLoopbackListenAddress(name, value string) (origin, canonical string, err error) {
	if strings.TrimSpace(value) != value || value == "" {
		return "", "", fmt.Errorf("%s must be a canonical host:port", name)
	}
	host, portText, splitErr := net.SplitHostPort(value)
	if splitErr != nil || host == "" {
		return "", "", fmt.Errorf("%s must be a canonical explicit loopback host:port", name)
	}
	port, parseErr := strconv.ParseUint(portText, 10, 16)
	if parseErr != nil || port == 0 || strconv.FormatUint(port, 10) != portText {
		return "", "", fmt.Errorf("%s must use a non-zero canonical decimal port", name)
	}
	if !isCanonicalLoopbackHost(host) {
		return "", "", fmt.Errorf("%s must use an explicit loopback host", name)
	}
	canonical = net.JoinHostPort(host, portText)
	if canonical != value {
		return "", "", fmt.Errorf("%s must use canonical host:port spelling", name)
	}
	return "https://" + canonical, canonical, nil
}

func validateHydraURL(raw string) (string, error) {
	return validateLoopbackEndpointURL(
		raw, "http",
		"network.hydraIntrospectionUrl must be an exact cleartext loopback HTTP endpoint with an explicit non-zero port and without credentials, query, fragment, or trailing slash",
	)
}

func validateLLMProxyURL(raw string) (string, error) {
	return validateLoopbackEndpointURL(
		raw, "https",
		"network.llmproxyEndpoint must be an exact loopback HTTPS endpoint with an explicit non-zero port and without credentials, query, fragment, or trailing slash",
	)
}

func validateLoopbackEndpointURL(raw, scheme, message string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != scheme || parsed.Opaque != "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		parsed.RawPath != "" || parsed.ForceQuery || parsed.Path == "" || parsed.Path == "/" || strings.HasSuffix(parsed.Path, "/") || strings.Contains(parsed.Path, "%") {
		return "", errors.New(message)
	}
	host, portText, err := net.SplitHostPort(parsed.Host)
	if err != nil || host == "" {
		return "", errors.New(message)
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil || port == 0 || strconv.FormatUint(port, 10) != portText || net.JoinHostPort(host, portText) != parsed.Host || !isCanonicalLoopbackHost(host) || parsed.String() != raw {
		return "", errors.New(message)
	}
	return parsed.Host, nil
}

func isCanonicalLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback() && ip.String() == host
}

func validateExistingCanonicalDirectory(name, value string) error {
	if value == "" || !filepath.IsAbs(value) || filepath.Clean(value) != value || strings.ContainsRune(value, 0) {
		return fmt.Errorf("%s must be an absolute clean path", name)
	}
	if err := validateShellValue(name, value); err != nil {
		return err
	}
	info, err := os.Lstat(value)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", name, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s must be a direct directory", name)
	}
	resolved, err := filepath.EvalSymlinks(value)
	if err != nil || resolved != value {
		return fmt.Errorf("%s must have no symlinked path components", name)
	}
	return nil
}

func validateExistingContainedDirectory(root, candidate string) error {
	if err := validateExistingCanonicalDirectory("resolved directory", candidate); err != nil {
		return err
	}
	relative, err := filepath.Rel(root, candidate)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("directory escapes workspace root")
	}
	return nil
}

func validateImmutablePath(name, value string, executable bool) error {
	if value == "" || !filepath.IsAbs(value) || filepath.Clean(value) != value || strings.ContainsRune(value, 0) {
		return fmt.Errorf("%s must be an absolute clean path", name)
	}
	if err := validateShellValue(name, value); err != nil {
		return err
	}
	info, err := os.Lstat(value)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", name, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o022 != 0 || info.Size() < 1 {
		return fmt.Errorf("%s must be a direct non-empty deployment-immutable regular file", name)
	}
	if executable && info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("%s must be executable", name)
	}
	return nil
}

func readSecretConfig(path string) ([]byte, error) {
	if err := validateImmutablePath("insecure development stack config", path, false); err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("insecure development stack config contains database authority and must be mode 0600 or stricter")
	}
	return readImmutableFile("insecure development stack config", path, maximumConfigBytes, false)
}

func readImmutableFile(name, path string, maximum int64, executable bool) ([]byte, error) {
	if err := validateImmutablePath(name, path, executable); err != nil {
		return nil, err
	}
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if before.Size() > maximum {
		return nil, fmt.Errorf("%s exceeds %d bytes", name, maximum)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", name, err)
	}
	raw, readErr := io.ReadAll(io.LimitReader(file, maximum+1))
	after, statErr := file.Stat()
	closeErr := file.Close()
	if readErr != nil || statErr != nil || closeErr != nil {
		return nil, errors.Join(readErr, statErr, closeErr)
	}
	if int64(len(raw)) != before.Size() || int64(len(raw)) > maximum || !os.SameFile(before, after) || after.Size() != before.Size() {
		return nil, fmt.Errorf("%s identity or size changed while reading", name)
	}
	return raw, nil
}

func validateCanonicalText(name, value string, minimum, maximum int) error {
	if len(value) < minimum || len(value) > maximum || !utf8.ValidString(value) || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\x00\r\n") {
		return fmt.Errorf("%s must contain between %d and %d canonical UTF-8 bytes", name, minimum, maximum)
	}
	return validateShellValue(name, value)
}

func validateShellValue(name, value string) error {
	if strings.ContainsAny(value, "\x00\r\n") {
		return fmt.Errorf("%s cannot be represented in a one-line environment file", name)
	}
	return nil
}

func validateRelativePath(value string) error {
	if value == "" || filepath.IsAbs(value) || filepath.Clean(value) != value || value == ".." || strings.HasPrefix(value, ".."+string(filepath.Separator)) || strings.ContainsRune(value, 0) {
		return errors.New("path must be a clean contained relative path")
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
