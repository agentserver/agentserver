// Package sandboxgatewayapp contains the production server shell shared by
// provider-linked sandbox-gateway binaries. Provider SDKs remain outside the
// main module and are passed in through sandboxgateway.Provider.
package sandboxgatewayapp

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/agentserver/agentserver/v2/internal/larkegresspolicy"
	"github.com/agentserver/agentserver/v2/internal/taepolicy"
)

const (
	ListenAddressEnvironment      = "AGENTSERVER_V2_SANDBOX_GATEWAY_LISTEN_ADDR"
	TLSCertificateEnvironment     = "AGENTSERVER_V2_SANDBOX_GATEWAY_TLS_CERT_FILE"
	TLSKeyEnvironment             = "AGENTSERVER_V2_SANDBOX_GATEWAY_TLS_KEY_FILE"
	ClientCAEnvironment           = "AGENTSERVER_V2_SANDBOX_GATEWAY_CLIENT_CA_FILE"
	SPIFFEIdentityEnvironment     = "AGENTSERVER_V2_SANDBOX_GATEWAY_SPIFFE_ID"
	ExecutorIdentityEnvironment   = "AGENTSERVER_V2_EXECUTOR_GATEWAY_SPIFFE_ID"
	HarnessIdentityEnvironment    = "AGENTSERVER_V2_HARNESS_POOL_SPIFFE_ID"
	CoreURLEnvironment            = "AGENTSERVER_V2_CORE_URL"
	CoreCAEnvironment             = "AGENTSERVER_V2_CORE_CA_FILE"
	CoreCertificateEnvironment    = "AGENTSERVER_V2_CORE_CLIENT_CERT_FILE"
	CoreKeyEnvironment            = "AGENTSERVER_V2_CORE_CLIENT_KEY_FILE"
	CoreServerNameEnvironment     = "AGENTSERVER_V2_CORE_SERVER_NAME"
	CapabilityKeyringEnvironment  = "AGENTSERVER_V2_SANDBOX_CAPABILITY_KEYRING_FILE"
	ProviderModeEnvironment       = "AGENTSERVER_V2_SANDBOX_PROVIDER"
	ProviderRegionEnvironment     = "AGENTSERVER_V2_TAE_REGION"
	ProviderPSMEnvironment        = "AGENTSERVER_V2_TAE_PSM"
	TAEPolicyRevisionEnvironment  = "AGENTSERVER_V2_TAE_POLICY_REVISION"
	TAEPolicySHA256Environment    = "AGENTSERVER_V2_TAE_POLICY_SHA256"
	TAEPolicyBindingEnvironment   = "AGENTSERVER_V2_TAE_POLICY_BINDING_SHA256"
	TAEPolicyHostEnvironment      = "AGENTSERVER_V2_TAE_POLICY_HOST"
	TAEPolicyAccessEnvironment    = "AGENTSERVER_V2_TAE_POLICY_ACCESS"
	TAEPolicyWebhookRequiredEnv   = "AGENTSERVER_V2_TAE_POLICY_WEBHOOK_REQUIRED"
	TAEPolicyWebhookModeEnv       = "AGENTSERVER_V2_TAE_WEBHOOK_MODE"
	TAEPolicyWebhookPSMEnv        = "AGENTSERVER_V2_TAE_WEBHOOK_PSM"
	TAEPolicyWebhookURLEnv        = "AGENTSERVER_V2_TAE_WEBHOOK_URL"
	TAEPolicyWebhookPathEnv       = "AGENTSERVER_V2_TAE_WEBHOOK_PATH"
	TAEPolicyPublishedEnv         = "AGENTSERVER_V2_TAE_POLICY_PUBLISHED"
	TAEPolicyApprovedEnv          = "AGENTSERVER_V2_TAE_POLICY_APPROVED"
	TAEPolicyEvidenceEnv          = "AGENTSERVER_V2_TAE_POLICY_EVIDENCE_REF"
	IdleTTLEnvironment            = "AGENTSERVER_V2_MANAGED_IDLE_TTL"
	EnsureTimeoutEnvironment      = "AGENTSERVER_V2_SANDBOX_ENSURE_TIMEOUT"
	EnsurePollEnvironment         = "AGENTSERVER_V2_SANDBOX_ENSURE_POLL_INTERVAL"
	ReconcileIntervalEnvironment  = "AGENTSERVER_V2_SANDBOX_RECONCILE_INTERVAL"
	ReconcileLimitEnvironment     = "AGENTSERVER_V2_SANDBOX_RECONCILE_LIMIT"
	RootEnvironment               = "AGENTSERVER_V2_MANAGED_SANDBOX_ROOT"
	PlatformEnvironment           = "AGENTSERVER_V2_MANAGED_SANDBOX_PLATFORM"
	WorkspaceAllowlistEnvironment = "AGENTSERVER_V2_MANAGED_WORKSPACE_ALLOWLIST"
)

var workspaceUUIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type Config struct {
	ListenAddress string

	TLSCertificate   string
	TLSKey           string
	ClientCA         string
	SPIFFEIdentity   string
	ExecutorIdentity string
	HarnessIdentity  string

	CoreURL         string
	CoreCA          string
	CoreCertificate string
	CoreKey         string
	CoreServerName  string

	CapabilityKeyring  string
	ProviderRegion     string
	ProviderPSM        string
	TAEPolicy          taepolicy.Binding
	IdleTTL            time.Duration
	EnsureTimeout      time.Duration
	EnsurePoll         time.Duration
	ReconcileInterval  time.Duration
	ReconcileLimit     int
	Root               string
	Platform           string
	WorkspaceAllowlist []string
}

func LoadProductionConfig(getenv func(string) string) (Config, error) {
	if getenv == nil {
		return Config{}, errors.New("sandbox-gateway configuration source is required")
	}
	required := func(name string) (string, error) {
		value := strings.TrimSpace(getenv(name))
		if value == "" {
			return "", fmt.Errorf("%s is required", name)
		}
		return value, nil
	}
	config := Config{}
	for destination, name := range map[*string]string{
		&config.ListenAddress: ListenAddressEnvironment, &config.TLSCertificate: TLSCertificateEnvironment,
		&config.TLSKey: TLSKeyEnvironment, &config.ClientCA: ClientCAEnvironment,
		&config.SPIFFEIdentity: SPIFFEIdentityEnvironment, &config.ExecutorIdentity: ExecutorIdentityEnvironment,
		&config.HarnessIdentity: HarnessIdentityEnvironment, &config.CoreURL: CoreURLEnvironment,
		&config.CoreCA: CoreCAEnvironment, &config.CoreCertificate: CoreCertificateEnvironment,
		&config.CoreKey: CoreKeyEnvironment, &config.CoreServerName: CoreServerNameEnvironment,
		&config.CapabilityKeyring: CapabilityKeyringEnvironment,
		&config.ProviderRegion:    ProviderRegionEnvironment, &config.ProviderPSM: ProviderPSMEnvironment,
	} {
		value, err := required(name)
		if err != nil {
			return Config{}, err
		}
		*destination = value
	}
	if _, _, err := net.SplitHostPort(config.ListenAddress); err != nil {
		return Config{}, fmt.Errorf("parse production sandbox-gateway listen address: %w", err)
	}
	if strings.TrimSpace(getenv(ProviderModeEnvironment)) != "tae" {
		return Config{}, fmt.Errorf("%s must be exactly tae", ProviderModeEnvironment)
	}
	if config.ProviderRegion != "sg" {
		return Config{}, fmt.Errorf("%s must be exactly sg", ProviderRegionEnvironment)
	}
	if len(config.ProviderPSM) > 256 || strings.ContainsAny(config.ProviderPSM, "\x00\r\n") {
		return Config{}, errors.New("TAE provider PSM is invalid")
	}
	policyText := func(name string) (string, error) {
		return required(name)
	}
	var policyErr error
	if config.TAEPolicy.Revision, policyErr = policyText(TAEPolicyRevisionEnvironment); policyErr != nil {
		return Config{}, policyErr
	}
	if config.TAEPolicy.PolicySHA256, policyErr = policyText(TAEPolicySHA256Environment); policyErr != nil {
		return Config{}, policyErr
	}
	if config.TAEPolicy.BindingSHA256, policyErr = policyText(TAEPolicyBindingEnvironment); policyErr != nil {
		return Config{}, policyErr
	}
	if config.TAEPolicy.PublicHost, policyErr = policyText(TAEPolicyHostEnvironment); policyErr != nil {
		return Config{}, policyErr
	}
	if config.TAEPolicy.PublicAccess, policyErr = policyText(TAEPolicyAccessEnvironment); policyErr != nil {
		return Config{}, policyErr
	}
	if config.TAEPolicy.WebhookMode, policyErr = policyText(TAEPolicyWebhookModeEnv); policyErr != nil {
		return Config{}, policyErr
	}
	if config.TAEPolicy.WebhookPath, policyErr = policyText(TAEPolicyWebhookPathEnv); policyErr != nil {
		return Config{}, policyErr
	}
	config.TAEPolicy.WebhookPSM = strings.TrimSpace(getenv(TAEPolicyWebhookPSMEnv))
	config.TAEPolicy.WebhookURL = strings.TrimSpace(getenv(TAEPolicyWebhookURLEnv))
	if config.TAEPolicy.EvidenceRef, policyErr = policyText(TAEPolicyEvidenceEnv); policyErr != nil {
		return Config{}, policyErr
	}
	config.TAEPolicy.Region = config.ProviderRegion
	config.TAEPolicy.SandboxPSM = config.ProviderPSM
	config.TAEPolicy.Version = taepolicy.BindingVersion
	config.TAEPolicy.PublicWebhookRequired, policyErr = requiredBool(getenv(TAEPolicyWebhookRequiredEnv), TAEPolicyWebhookRequiredEnv)
	if policyErr != nil {
		return Config{}, policyErr
	}
	config.TAEPolicy.Published, policyErr = requiredBool(getenv(TAEPolicyPublishedEnv), TAEPolicyPublishedEnv)
	if policyErr != nil {
		return Config{}, policyErr
	}
	config.TAEPolicy.Approved, policyErr = requiredBool(getenv(TAEPolicyApprovedEnv), TAEPolicyApprovedEnv)
	if policyErr != nil {
		return Config{}, policyErr
	}
	if err := config.TAEPolicy.Validate(config.ProviderRegion, config.ProviderPSM, larkegresspolicy.SHA256Hex()); err != nil {
		return Config{}, fmt.Errorf("TAE policy binding: %w", err)
	}
	if config.SPIFFEIdentity == config.ExecutorIdentity || config.SPIFFEIdentity == config.HarnessIdentity || config.ExecutorIdentity == config.HarnessIdentity {
		return Config{}, errors.New("sandbox-gateway, executor-gateway, and harness-pool SPIFFE identities must be distinct")
	}
	for name, identity := range map[string]string{
		SPIFFEIdentityEnvironment: config.SPIFFEIdentity, ExecutorIdentityEnvironment: config.ExecutorIdentity,
		HarnessIdentityEnvironment: config.HarnessIdentity,
	} {
		if err := validateSPIFFEIdentity(identity); err != nil {
			return Config{}, fmt.Errorf("%s: %w", name, err)
		}
	}
	coreURL, err := url.Parse(config.CoreURL)
	if err != nil || coreURL.Scheme != "https" || coreURL.Host == "" || coreURL.Hostname() == "" || coreURL.User != nil ||
		coreURL.Opaque != "" || coreURL.RawPath != "" || coreURL.RawQuery != "" || coreURL.ForceQuery || coreURL.Fragment != "" ||
		(coreURL.Path != "" && coreURL.Path != "/") {
		return Config{}, fmt.Errorf("%s must be a canonical HTTPS origin", CoreURLEnvironment)
	}
	if config.CoreServerName != coreURL.Hostname() || net.ParseIP(config.CoreServerName) != nil {
		return Config{}, errors.New("Core TLS server name must exactly match the non-IP Core URL hostname")
	}
	config.IdleTTL, err = durationValue(getenv(IdleTTLEnvironment), 5*time.Minute, time.Second, 24*time.Hour, IdleTTLEnvironment)
	if err != nil || config.IdleTTL%time.Second != 0 {
		if err == nil {
			err = errors.New("duration must use whole seconds")
		}
		return Config{}, fmt.Errorf("%s: %w", IdleTTLEnvironment, err)
	}
	config.EnsureTimeout, err = durationValue(getenv(EnsureTimeoutEnvironment), 45*time.Second, time.Second, time.Minute, EnsureTimeoutEnvironment)
	if err != nil {
		return Config{}, err
	}
	config.EnsurePoll, err = durationValue(getenv(EnsurePollEnvironment), 250*time.Millisecond, 10*time.Millisecond, config.EnsureTimeout, EnsurePollEnvironment)
	if err != nil {
		return Config{}, err
	}
	config.ReconcileInterval, err = durationValue(getenv(ReconcileIntervalEnvironment), 30*time.Second, time.Second, 10*time.Minute, ReconcileIntervalEnvironment)
	if err != nil {
		return Config{}, err
	}
	config.ReconcileLimit, err = intValue(getenv(ReconcileLimitEnvironment), 100, 1, 1000, ReconcileLimitEnvironment)
	if err != nil {
		return Config{}, err
	}
	config.Root = strings.TrimSpace(getenv(RootEnvironment))
	if config.Root == "" {
		config.Root = "/workspace"
	}
	if !filepath.IsAbs(config.Root) || filepath.Clean(config.Root) != config.Root || config.Root == "/" || strings.ContainsRune(config.Root, '\x00') {
		return Config{}, fmt.Errorf("%s must be a clean non-root absolute path", RootEnvironment)
	}
	config.Platform = strings.TrimSpace(getenv(PlatformEnvironment))
	if config.Platform == "" {
		config.Platform = "linux-amd64"
	}
	if config.Platform != "linux-amd64" {
		return Config{}, fmt.Errorf("%s must be exactly linux-amd64", PlatformEnvironment)
	}
	allowlist, err := required(WorkspaceAllowlistEnvironment)
	if err != nil {
		return Config{}, err
	}
	config.WorkspaceAllowlist = strings.Split(allowlist, ",")
	if len(config.WorkspaceAllowlist) < 1 || len(config.WorkspaceAllowlist) > 64 {
		return Config{}, fmt.Errorf("%s must contain between 1 and 64 workspace UUIDs", WorkspaceAllowlistEnvironment)
	}
	seen := make(map[string]struct{}, len(config.WorkspaceAllowlist))
	for index, workspaceID := range config.WorkspaceAllowlist {
		if workspaceID == "00000000-0000-0000-0000-000000000000" || !workspaceUUIDPattern.MatchString(workspaceID) {
			return Config{}, fmt.Errorf("%s[%d] must be a non-zero canonical lowercase UUID", WorkspaceAllowlistEnvironment, index)
		}
		if index > 0 && config.WorkspaceAllowlist[index-1] >= workspaceID {
			return Config{}, fmt.Errorf("%s must be strictly sorted without duplicates", WorkspaceAllowlistEnvironment)
		}
		if _, duplicate := seen[workspaceID]; duplicate {
			return Config{}, fmt.Errorf("%s must not repeat a workspace", WorkspaceAllowlistEnvironment)
		}
		seen[workspaceID] = struct{}{}
	}
	return config, nil
}

func validateSPIFFEIdentity(identity string) error {
	parsed, err := url.Parse(identity)
	if err != nil || parsed.Scheme != "spiffe" || parsed.Host == "" || parsed.Path == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" || parsed.RawPath != "" {
		return errors.New("identity must be a canonical SPIFFE URI")
	}
	return nil
}

func durationValue(value string, fallback, minimum, maximum time.Duration, name string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed < minimum || parsed > maximum || parsed%time.Millisecond != 0 {
		return 0, fmt.Errorf("%s must be a whole-millisecond duration between %s and %s", name, minimum, maximum)
	}
	return parsed, nil
}

func requiredBool(value, name string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("%s must be exactly true or false", name)
	}
}

func intValue(value string, fallback, minimum, maximum int, name string) (int, error) {
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
