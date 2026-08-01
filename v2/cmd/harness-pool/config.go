package main

import (
	"errors"
	"fmt"
	"math"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/agentserver/agentserver/v2/internal/harnesspool"
	"github.com/agentserver/agentserver/v2/internal/runcapability"
	"github.com/agentserver/agentserver/v2/internal/runtimelock"
)

const (
	poolListenAddressEnvironment         = "AGENTSERVER_V2_HARNESS_POOL_LISTEN_ADDR"
	poolTLSCertificateEnvironment        = "AGENTSERVER_V2_HARNESS_POOL_TLS_CERT_FILE"
	poolTLSKeyEnvironment                = "AGENTSERVER_V2_HARNESS_POOL_TLS_KEY_FILE"
	poolWorkerClientCAEnvironment        = "AGENTSERVER_V2_HARNESS_POOL_WORKER_CA_FILE"
	poolTLSIdentityEnvironment           = "AGENTSERVER_V2_HARNESS_POOL_SPIFFE_ID"
	poolWorkerTLSIdentityEnvironment     = "AGENTSERVER_V2_HARNESS_WORKER_SPIFFE_ID"
	poolCoreURLEnvironment               = "AGENTSERVER_V2_CORE_URL"
	poolCoreCAEnvironment                = "AGENTSERVER_V2_CORE_CA_FILE"
	poolCoreServerNameEnvironment        = "AGENTSERVER_V2_CORE_SERVER_NAME"
	poolDevExecutorIDEnvironment         = "AGENTSERVER_V2_DEV_EXECUTOR_ID"
	poolDevRunCapabilityKeyEnvironment   = "AGENTSERVER_V2_DEV_RUN_CAPABILITY_KEY"
	poolDevObjectRootEnvironment         = "AGENTSERVER_V2_DEV_PROMPT_OBJECT_DIR"
	poolRuntimeRootEnvironment           = "AGENTSERVER_V2_HARNESS_RUNTIME_DIR"
	poolCheckpointStagingRootEnvironment = "AGENTSERVER_V2_CHECKPOINT_STAGING_DIR"
	poolWorkerExecutableEnvironment      = "AGENTSERVER_V2_HARNESS_WORKER_BIN"
	poolWorkerConfigEnvironment          = "AGENTSERVER_V2_HARNESS_WORKER_CONFIG_FILE"
	poolManifestSigningKeyIDEnvironment  = "AGENTSERVER_V2_RUN_MANIFEST_SIGNING_KEY_ID"
	poolManifestSigningKeyEnvironment    = "AGENTSERVER_V2_RUN_MANIFEST_SIGNING_KEY_FILE"
	poolRuntimeManifestDigestEnvironment = "AGENTSERVER_V2_CODEX_RUNTIME_MANIFEST_SHA256"
	poolCheckpointAllowlistEnvironment   = "AGENTSERVER_V2_CHECKPOINT_ALLOWLIST_VERSION"
	poolWorkerServiceAccountEnvironment  = "AGENTSERVER_V2_HARNESS_WORKER_SERVICE_ACCOUNT"
	poolPrivilegedForkEnvironment        = "AGENTSERVER_V2_HARNESS_PRIVILEGED_FORK"
	poolWorkerUIDEnvironment             = "AGENTSERVER_V2_HARNESS_WORKER_UID"
	poolWorkerGIDEnvironment             = "AGENTSERVER_V2_HARNESS_WORKER_GID"
	poolAppUIDEnvironment                = "AGENTSERVER_V2_HARNESS_APP_UID"
	poolAppGIDEnvironment                = "AGENTSERVER_V2_HARNESS_APP_GID"
	poolExecutorMCPEndpointEnvironment   = "AGENTSERVER_V2_EXECUTOR_MCP_ENDPOINT"
	poolExecutorMCPIdentityEnvironment   = "AGENTSERVER_V2_EXECUTOR_GATEWAY_SPIFFE_ID"
	poolModelEnvironment                 = "AGENTSERVER_V2_MODEL"
	poolModelProviderEnvironment         = "AGENTSERVER_V2_MODEL_PROVIDER"
	poolModelEndpointEnvironment         = "AGENTSERVER_V2_LLMPROXY_ENDPOINT"
	poolModelTLSIdentityEnvironment      = "AGENTSERVER_V2_LLMPROXY_SPIFFE_ID"
	poolMaxConcurrentEnvironment         = "AGENTSERVER_V2_HARNESS_MAX_CONCURRENT_ATTEMPTS"
	poolMaxRunDurationEnvironment        = "AGENTSERVER_V2_MAX_RUN_DURATION"
	poolMaxApprovalTTLEnvironment        = "AGENTSERVER_V2_MAX_APPROVAL_TTL"

	developmentControlAudience     = "harness-pool-control"
	developmentExecutorMCPAudience = runcapability.AudienceExecutorMCP
	developmentModelAudience       = runcapability.AudienceLLMProxy
	developmentWorkerArguments     = "run"

	defaultPoolMaxConcurrent  = 2
	maximumCommandConcurrency = 64
	defaultMaxRunDuration     = 30 * time.Minute
	defaultMaxApprovalTTL     = 10 * time.Second
)

var (
	poolUUIDPattern   = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	poolDigestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type harnessPoolDevelopmentConfig struct {
	listenAddress     string
	tlsCertificate    string
	tlsKey            string
	workerClientCA    string
	poolTLSIdentity   string
	workerTLSIdentity string
	coreURL           string
	coreCA            string
	coreServerName    string

	executorID       string
	capabilityCodec  *runcapability.DevelopmentCodec
	objectRoot       string
	runtimeRoot      string
	checkpointRoot   string
	workerExecutable string
	workerConfig     string
	workerDigest     string
	manifestKeyID    string
	manifestKeyFile  string
	runtimeDigest    string
	allowlistVersion int
	serviceAccount   string
	workerCredential *harnesspool.LocalProcessCredential
	appCredential    harnesspool.LocalProcessCredential

	executorMCPEndpoint string
	executorMCPIdentity string
	model               string
	modelProvider       string
	modelEndpoint       string
	modelTLSIdentity    string
	maxConcurrent       int
	maxRunDuration      time.Duration
	maxApprovalTTL      time.Duration
}

func loadHarnessPoolDevelopmentConfig(getenv func(string) string) (harnessPoolDevelopmentConfig, error) {
	if getenv == nil {
		return harnessPoolDevelopmentConfig{}, errors.New("harness-pool configuration source is required")
	}
	required := func(name string) (string, error) {
		value := strings.TrimSpace(getenv(name))
		if value == "" {
			return "", fmt.Errorf("%s is required", name)
		}
		return value, nil
	}
	var config harnessPoolDevelopmentConfig
	var err error
	if config.listenAddress, err = required(poolListenAddressEnvironment); err != nil {
		return harnessPoolDevelopmentConfig{}, err
	}
	if err := requireDevelopmentLoopbackAddress(config.listenAddress); err != nil {
		return harnessPoolDevelopmentConfig{}, err
	}
	for target, name := range map[*string]string{
		&config.tlsCertificate:      poolTLSCertificateEnvironment,
		&config.tlsKey:              poolTLSKeyEnvironment,
		&config.workerClientCA:      poolWorkerClientCAEnvironment,
		&config.poolTLSIdentity:     poolTLSIdentityEnvironment,
		&config.workerTLSIdentity:   poolWorkerTLSIdentityEnvironment,
		&config.coreURL:             poolCoreURLEnvironment,
		&config.coreCA:              poolCoreCAEnvironment,
		&config.executorID:          poolDevExecutorIDEnvironment,
		&config.objectRoot:          poolDevObjectRootEnvironment,
		&config.runtimeRoot:         poolRuntimeRootEnvironment,
		&config.checkpointRoot:      poolCheckpointStagingRootEnvironment,
		&config.workerExecutable:    poolWorkerExecutableEnvironment,
		&config.workerConfig:        poolWorkerConfigEnvironment,
		&config.manifestKeyID:       poolManifestSigningKeyIDEnvironment,
		&config.manifestKeyFile:     poolManifestSigningKeyEnvironment,
		&config.runtimeDigest:       poolRuntimeManifestDigestEnvironment,
		&config.serviceAccount:      poolWorkerServiceAccountEnvironment,
		&config.executorMCPEndpoint: poolExecutorMCPEndpointEnvironment,
		&config.executorMCPIdentity: poolExecutorMCPIdentityEnvironment,
		&config.model:               poolModelEnvironment,
		&config.modelProvider:       poolModelProviderEnvironment,
		&config.modelEndpoint:       poolModelEndpointEnvironment,
		&config.modelTLSIdentity:    poolModelTLSIdentityEnvironment,
	} {
		*target, err = required(name)
		if err != nil {
			return harnessPoolDevelopmentConfig{}, err
		}
	}
	config.coreServerName = strings.TrimSpace(getenv(poolCoreServerNameEnvironment))
	if err := requireHTTPSOrigin(config.coreURL, poolCoreURLEnvironment); err != nil {
		return harnessPoolDevelopmentConfig{}, err
	}
	if !validPoolUUID(config.executorID) {
		return harnessPoolDevelopmentConfig{}, fmt.Errorf("%s must be a non-zero canonical lowercase UUID", poolDevExecutorIDEnvironment)
	}
	encodedCapabilityKey, err := required(poolDevRunCapabilityKeyEnvironment)
	if err != nil {
		return harnessPoolDevelopmentConfig{}, err
	}
	config.capabilityCodec, err = runcapability.NewDevelopmentCodecFromBase64Key(encodedCapabilityKey)
	if err != nil {
		return harnessPoolDevelopmentConfig{}, fmt.Errorf("%s: %w", poolDevRunCapabilityKeyEnvironment, err)
	}
	if !poolDigestPattern.MatchString(config.runtimeDigest) {
		return harnessPoolDevelopmentConfig{}, fmt.Errorf("%s must be a lowercase SHA-256 digest", poolRuntimeManifestDigestEnvironment)
	}
	config.allowlistVersion, err = requiredSafePositiveInt(getenv, poolCheckpointAllowlistEnvironment)
	if err != nil {
		return harnessPoolDevelopmentConfig{}, err
	}
	appUID, err := requiredCredentialID(getenv, poolAppUIDEnvironment)
	if err != nil {
		return harnessPoolDevelopmentConfig{}, err
	}
	appGID, err := requiredCredentialID(getenv, poolAppGIDEnvironment)
	if err != nil {
		return harnessPoolDevelopmentConfig{}, err
	}
	config.appCredential = harnesspool.LocalProcessCredential{UID: appUID, GID: appGID}
	privilegedFork := strings.TrimSpace(getenv(poolPrivilegedForkEnvironment))
	switch privilegedFork {
	case "":
	case "true":
		workerUID, err := requiredCredentialID(getenv, poolWorkerUIDEnvironment)
		if err != nil {
			return harnessPoolDevelopmentConfig{}, err
		}
		workerGID, err := requiredCredentialID(getenv, poolWorkerGIDEnvironment)
		if err != nil {
			return harnessPoolDevelopmentConfig{}, err
		}
		if workerUID == appUID || workerGID == appGID {
			return harnessPoolDevelopmentConfig{}, errors.New("privileged-fork worker and app identities must be distinct")
		}
		config.workerCredential = &harnesspool.LocalProcessCredential{UID: workerUID, GID: workerGID}
	default:
		return harnessPoolDevelopmentConfig{}, fmt.Errorf("%s must be exactly true when present", poolPrivilegedForkEnvironment)
	}
	config.maxConcurrent, err = optionalBoundedInt(getenv(poolMaxConcurrentEnvironment), defaultPoolMaxConcurrent, 1, maximumCommandConcurrency, poolMaxConcurrentEnvironment)
	if err != nil {
		return harnessPoolDevelopmentConfig{}, err
	}
	config.maxRunDuration, err = optionalBoundedDuration(getenv(poolMaxRunDurationEnvironment), defaultMaxRunDuration, time.Second, 24*time.Hour, poolMaxRunDurationEnvironment)
	if err != nil {
		return harnessPoolDevelopmentConfig{}, err
	}
	config.maxApprovalTTL, err = optionalBoundedDuration(getenv(poolMaxApprovalTTLEnvironment), defaultMaxApprovalTTL, time.Second, 24*time.Hour, poolMaxApprovalTTLEnvironment)
	if err != nil {
		return harnessPoolDevelopmentConfig{}, err
	}
	if config.maxApprovalTTL > config.maxRunDuration {
		return harnessPoolDevelopmentConfig{}, fmt.Errorf("%s must not exceed %s", poolMaxApprovalTTLEnvironment, poolMaxRunDurationEnvironment)
	}
	if err := validateDirectConfigurationFile(config.workerConfig); err != nil {
		return harnessPoolDevelopmentConfig{}, fmt.Errorf("%s: %w", poolWorkerConfigEnvironment, err)
	}
	workerDigest, _, err := runtimelock.HashFile(config.workerExecutable)
	if err != nil {
		return harnessPoolDevelopmentConfig{}, fmt.Errorf("hash local harness-worker executable: %w", err)
	}
	config.workerDigest = workerDigest
	return config, nil
}

func requireDevelopmentLoopbackAddress(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("parse insecure-dev harness-pool listen address: %w", err)
	}
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("insecure-dev harness-pool must bind an explicit loopback address")
	}
	return nil
}

func requireHTTPSOrigin(raw, name string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return fmt.Errorf("%s must be an HTTPS origin without credentials, path, query, or fragment", name)
	}
	return nil
}

func requiredSafePositiveInt(getenv func(string) string, name string) (int, error) {
	value := strings.TrimSpace(getenv(name))
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 1 || parsed > int64(math.MaxInt) || parsed > 1<<53-1 {
		return 0, fmt.Errorf("%s must be a positive JSON-safe base-10 integer", name)
	}
	return int(parsed), nil
}

func requiredCredentialID(getenv func(string) string, name string) (uint32, error) {
	value := strings.TrimSpace(getenv(name))
	parsed, err := strconv.ParseUint(value, 10, 32)
	if err != nil || parsed == 0 || parsed == math.MaxUint32 {
		return 0, fmt.Errorf("%s must be a valid unprivileged uint32 identity", name)
	}
	return uint32(parsed), nil
}

func optionalBoundedInt(value string, fallback, minimum, maximum int, name string) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < minimum || parsed > maximum {
		return 0, fmt.Errorf("%s must be between %d and %d", name, minimum, maximum)
	}
	return parsed, nil
}

func optionalBoundedDuration(value string, fallback, minimum, maximum time.Duration, name string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed < minimum || parsed > maximum {
		return 0, fmt.Errorf("%s must be a Go duration between %s and %s", name, minimum, maximum)
	}
	return parsed, nil
}

func validateDirectConfigurationFile(path string) error {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("path must be absolute and clean")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o022 != 0 || info.Size() < 1 {
		return fmt.Errorf("must be a direct non-empty deployment-immutable regular file: mode=%s size=%d", info.Mode(), info.Size())
	}
	return nil
}

func validPoolUUID(value string) bool {
	return value != "00000000-0000-0000-0000-000000000000" && poolUUIDPattern.MatchString(value)
}
