package main

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	sandboxListenAddressEnvironment     = "AGENTSERVER_V2_SANDBOX_GATEWAY_LISTEN_ADDR"
	sandboxTLSCertificateEnvironment    = "AGENTSERVER_V2_SANDBOX_GATEWAY_TLS_CERT_FILE"
	sandboxTLSKeyEnvironment            = "AGENTSERVER_V2_SANDBOX_GATEWAY_TLS_KEY_FILE"
	sandboxClientCAEnvironment          = "AGENTSERVER_V2_SANDBOX_GATEWAY_CLIENT_CA_FILE"
	sandboxSPIFFEIdentityEnvironment    = "AGENTSERVER_V2_SANDBOX_GATEWAY_SPIFFE_ID"
	sandboxExecutorIdentityEnvironment  = "AGENTSERVER_V2_EXECUTOR_GATEWAY_SPIFFE_ID"
	sandboxHarnessIdentityEnvironment   = "AGENTSERVER_V2_HARNESS_POOL_SPIFFE_ID"
	sandboxCoreURLEnvironment           = "AGENTSERVER_V2_CORE_URL"
	sandboxCoreCAEnvironment            = "AGENTSERVER_V2_CORE_CA_FILE"
	sandboxCoreCertificateEnvironment   = "AGENTSERVER_V2_CORE_CLIENT_CERT_FILE"
	sandboxCoreKeyEnvironment           = "AGENTSERVER_V2_CORE_CLIENT_KEY_FILE"
	sandboxCoreServerNameEnvironment    = "AGENTSERVER_V2_CORE_SERVER_NAME"
	sandboxCapabilityKeyringEnvironment = "AGENTSERVER_V2_SANDBOX_CAPABILITY_KEYRING_FILE"
	sandboxProviderModeEnvironment      = "AGENTSERVER_V2_SANDBOX_PROVIDER"
	sandboxProviderRegionEnvironment    = "AGENTSERVER_V2_TAE_REGION"
	sandboxProviderPSMEnvironment       = "AGENTSERVER_V2_TAE_PSM"
	sandboxIdleTTLEnvironment           = "AGENTSERVER_V2_MANAGED_IDLE_TTL"
	sandboxEnsureTimeoutEnvironment     = "AGENTSERVER_V2_SANDBOX_ENSURE_TIMEOUT"
	sandboxEnsurePollEnvironment        = "AGENTSERVER_V2_SANDBOX_ENSURE_POLL_INTERVAL"
	sandboxReconcileIntervalEnvironment = "AGENTSERVER_V2_SANDBOX_RECONCILE_INTERVAL"
	sandboxReconcileLimitEnvironment    = "AGENTSERVER_V2_SANDBOX_RECONCILE_LIMIT"
	sandboxRootEnvironment              = "AGENTSERVER_V2_MANAGED_SANDBOX_ROOT"
	sandboxPlatformEnvironment          = "AGENTSERVER_V2_MANAGED_SANDBOX_PLATFORM"

	defaultSandboxIdleTTL           = 5 * time.Minute
	defaultSandboxEnsureTimeout     = 45 * time.Second
	defaultSandboxEnsurePoll        = 250 * time.Millisecond
	defaultSandboxReconcileInterval = 30 * time.Second
	defaultSandboxReconcileLimit    = 100
)

type sandboxGatewayConfig struct {
	listenAddress string
	production    bool

	tlsCertificate   string
	tlsKey           string
	clientCA         string
	spiffeIdentity   string
	executorIdentity string
	harnessIdentity  string

	coreURL         string
	coreCA          string
	coreCertificate string
	coreKey         string
	coreServerName  string

	capabilityKeyring string
	providerMode      string
	providerRegion    string
	providerPSM       string
	idleTTL           time.Duration
	ensureTimeout     time.Duration
	ensurePoll        time.Duration
	reconcileInterval time.Duration
	reconcileLimit    int
	root              string
	platform          string
}

func loadSandboxGatewayConfig(getenv func(string) string, mode sandboxGatewayServeMode) (sandboxGatewayConfig, error) {
	if getenv == nil {
		return sandboxGatewayConfig{}, errors.New("sandbox-gateway configuration source is required")
	}
	production := mode == sandboxGatewayServeProduction
	if !production && mode != sandboxGatewayServeInsecureDevelopment {
		return sandboxGatewayConfig{}, errors.New("sandbox-gateway serve mode is invalid")
	}
	required := func(name string) (string, error) {
		value := strings.TrimSpace(getenv(name))
		if value == "" {
			return "", fmt.Errorf("%s is required", name)
		}
		return value, nil
	}
	config := sandboxGatewayConfig{production: production}
	var err error
	if config.listenAddress, err = required(sandboxListenAddressEnvironment); err != nil {
		return sandboxGatewayConfig{}, err
	}
	if production {
		if _, _, err := net.SplitHostPort(config.listenAddress); err != nil {
			return sandboxGatewayConfig{}, fmt.Errorf("parse production sandbox-gateway listen address: %w", err)
		}
		for destination, name := range map[*string]string{
			&config.tlsCertificate:   sandboxTLSCertificateEnvironment,
			&config.tlsKey:           sandboxTLSKeyEnvironment,
			&config.clientCA:         sandboxClientCAEnvironment,
			&config.spiffeIdentity:   sandboxSPIFFEIdentityEnvironment,
			&config.executorIdentity: sandboxExecutorIdentityEnvironment,
			&config.harnessIdentity:  sandboxHarnessIdentityEnvironment,
		} {
			*destination, err = required(name)
			if err != nil {
				return sandboxGatewayConfig{}, err
			}
		}
		if config.spiffeIdentity == config.executorIdentity || config.spiffeIdentity == config.harnessIdentity || config.executorIdentity == config.harnessIdentity {
			return sandboxGatewayConfig{}, errors.New("sandbox-gateway, executor-gateway, and harness-pool SPIFFE identities must be distinct")
		}
	} else if err := requireSandboxLoopbackAddress(config.listenAddress); err != nil {
		return sandboxGatewayConfig{}, err
	}

	if config.coreURL, err = required(sandboxCoreURLEnvironment); err != nil {
		return sandboxGatewayConfig{}, err
	}
	coreOrigin, err := url.Parse(config.coreURL)
	if err != nil || coreOrigin.Host == "" || coreOrigin.Hostname() == "" || coreOrigin.User != nil || coreOrigin.RawPath != "" ||
		coreOrigin.RawQuery != "" || coreOrigin.Fragment != "" || coreOrigin.Opaque != "" || coreOrigin.ForceQuery ||
		(coreOrigin.Path != "" && coreOrigin.Path != "/") || (coreOrigin.Scheme != "http" && coreOrigin.Scheme != "https") {
		return sandboxGatewayConfig{}, fmt.Errorf("%s must be an absolute canonical HTTP(S) origin", sandboxCoreURLEnvironment)
	}
	if coreOrigin.Scheme == "https" {
		for destination, name := range map[*string]string{
			&config.coreCA:          sandboxCoreCAEnvironment,
			&config.coreCertificate: sandboxCoreCertificateEnvironment,
			&config.coreKey:         sandboxCoreKeyEnvironment,
			&config.coreServerName:  sandboxCoreServerNameEnvironment,
		} {
			*destination, err = required(name)
			if err != nil {
				return sandboxGatewayConfig{}, err
			}
		}
		if config.spiffeIdentity == "" {
			config.spiffeIdentity, err = required(sandboxSPIFFEIdentityEnvironment)
			if err != nil {
				return sandboxGatewayConfig{}, err
			}
		}
	} else if production || !sandboxLoopbackHost(coreOrigin.Hostname()) {
		return sandboxGatewayConfig{}, errors.New("cleartext Core URL is allowed only on loopback in insecure development")
	}

	if config.capabilityKeyring, err = required(sandboxCapabilityKeyringEnvironment); err != nil {
		return sandboxGatewayConfig{}, err
	}
	config.providerMode = strings.TrimSpace(getenv(sandboxProviderModeEnvironment))
	if config.providerMode == "" && !production {
		config.providerMode = "fake"
	}
	if production && config.providerMode != "tae" {
		return sandboxGatewayConfig{}, fmt.Errorf("%s must be exactly tae in production", sandboxProviderModeEnvironment)
	}
	if !production && config.providerMode != "fake" {
		return sandboxGatewayConfig{}, fmt.Errorf("%s must be exactly fake in insecure development", sandboxProviderModeEnvironment)
	}
	if config.providerRegion, err = required(sandboxProviderRegionEnvironment); err != nil {
		return sandboxGatewayConfig{}, err
	}
	if config.providerPSM, err = required(sandboxProviderPSMEnvironment); err != nil {
		return sandboxGatewayConfig{}, err
	}
	if len(config.providerRegion) > 128 || len(config.providerPSM) > 256 {
		return sandboxGatewayConfig{}, errors.New("sandbox provider region or PSM is too long")
	}
	config.idleTTL, err = optionalSandboxDuration(getenv(sandboxIdleTTLEnvironment), defaultSandboxIdleTTL, time.Second, 24*time.Hour, sandboxIdleTTLEnvironment)
	if err != nil {
		return sandboxGatewayConfig{}, err
	}
	if config.idleTTL%time.Second != 0 {
		return sandboxGatewayConfig{}, fmt.Errorf("%s must be a whole-second duration", sandboxIdleTTLEnvironment)
	}
	config.ensureTimeout, err = optionalSandboxDuration(getenv(sandboxEnsureTimeoutEnvironment), defaultSandboxEnsureTimeout, time.Second, time.Minute, sandboxEnsureTimeoutEnvironment)
	if err != nil {
		return sandboxGatewayConfig{}, err
	}
	config.ensurePoll, err = optionalSandboxDuration(getenv(sandboxEnsurePollEnvironment), defaultSandboxEnsurePoll, 10*time.Millisecond, config.ensureTimeout, sandboxEnsurePollEnvironment)
	if err != nil {
		return sandboxGatewayConfig{}, err
	}
	config.reconcileInterval, err = optionalSandboxDuration(getenv(sandboxReconcileIntervalEnvironment), defaultSandboxReconcileInterval, time.Second, 10*time.Minute, sandboxReconcileIntervalEnvironment)
	if err != nil {
		return sandboxGatewayConfig{}, err
	}
	config.reconcileLimit, err = optionalSandboxInt(getenv(sandboxReconcileLimitEnvironment), defaultSandboxReconcileLimit, 1, 1000, sandboxReconcileLimitEnvironment)
	if err != nil {
		return sandboxGatewayConfig{}, err
	}
	config.root = strings.TrimSpace(getenv(sandboxRootEnvironment))
	if config.root == "" {
		config.root = "/workspace"
	}
	if !filepath.IsAbs(config.root) || filepath.Clean(config.root) != config.root || strings.ContainsRune(config.root, '\x00') {
		return sandboxGatewayConfig{}, fmt.Errorf("%s must be a clean absolute path", sandboxRootEnvironment)
	}
	config.platform = strings.TrimSpace(getenv(sandboxPlatformEnvironment))
	if config.platform == "" {
		config.platform = "linux-amd64"
	}
	if config.platform != "linux-amd64" {
		return sandboxGatewayConfig{}, fmt.Errorf("%s must be exactly linux-amd64", sandboxPlatformEnvironment)
	}
	return config, nil
}

func requireSandboxLoopbackAddress(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("parse insecure-dev sandbox-gateway listen address: %w", err)
	}
	if !sandboxLoopbackHost(host) {
		return errors.New("insecure-dev sandbox-gateway must bind an explicit loopback address")
	}
	return nil
}

func sandboxLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

func optionalSandboxDuration(value string, fallback, minimum, maximum time.Duration, name string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed < minimum || parsed > maximum || parsed%time.Millisecond != 0 {
		return 0, fmt.Errorf("%s must be a whole-millisecond Go duration between %s and %s", name, minimum, maximum)
	}
	return parsed, nil
}

func optionalSandboxInt(value string, fallback, minimum, maximum int, name string) (int, error) {
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
