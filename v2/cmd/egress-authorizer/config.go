package main

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/agentserver/agentserver/v2/internal/larkegresspolicy"
	"github.com/agentserver/agentserver/v2/internal/taepolicy"
)

const (
	egressListenAddressEnvironment         = "AGENTSERVER_V2_EGRESS_AUTHORIZER_LISTEN_ADDR"
	egressTLSCertificateEnvironment        = "AGENTSERVER_V2_EGRESS_AUTHORIZER_TLS_CERT_FILE"
	egressTLSKeyEnvironment                = "AGENTSERVER_V2_EGRESS_AUTHORIZER_TLS_KEY_FILE"
	egressSPIFFEIdentityEnvironment        = "AGENTSERVER_V2_EGRESS_AUTHORIZER_SPIFFE_ID"
	egressCoreURLEnvironment               = "AGENTSERVER_V2_CORE_URL"
	egressCoreCAEnvironment                = "AGENTSERVER_V2_CORE_CA_FILE"
	egressCoreCertificateEnvironment       = "AGENTSERVER_V2_CORE_CLIENT_CERT_FILE"
	egressCoreKeyEnvironment               = "AGENTSERVER_V2_CORE_CLIENT_KEY_FILE"
	egressCoreServerNameEnvironment        = "AGENTSERVER_V2_CORE_SERVER_NAME"
	egressPlaceholderKeyringEnvironment    = "AGENTSERVER_V2_EGRESS_PLACEHOLDER_KEYRING_FILE"
	egressAllowedTAEPSMEnvironment         = "AGENTSERVER_V2_EGRESS_ALLOWED_TAE_PSM"
	egressTAEPolicyRevisionEnvironment     = "AGENTSERVER_V2_TAE_POLICY_REVISION"
	egressTAEPolicySHA256Environment       = "AGENTSERVER_V2_TAE_POLICY_SHA256"
	egressTAEPolicyBindingEnvironment      = "AGENTSERVER_V2_TAE_POLICY_BINDING_SHA256"
	egressTAEPolicyHostEnvironment         = "AGENTSERVER_V2_TAE_POLICY_HOST"
	egressTAEPolicyAccessEnvironment       = "AGENTSERVER_V2_TAE_POLICY_ACCESS"
	egressTAEWebhookRequiredEnvironment    = "AGENTSERVER_V2_TAE_POLICY_WEBHOOK_REQUIRED"
	egressTAEWebhookModeEnvironment        = "AGENTSERVER_V2_TAE_WEBHOOK_MODE"
	egressTAEWebhookPSMEnvironment         = "AGENTSERVER_V2_TAE_WEBHOOK_PSM"
	egressTAEWebhookURLEnvironment         = "AGENTSERVER_V2_TAE_WEBHOOK_URL"
	egressTAEWebhookPathEnvironment        = "AGENTSERVER_V2_TAE_WEBHOOK_PATH"
	egressTAEPolicyPublishedEnvironment    = "AGENTSERVER_V2_TAE_POLICY_PUBLISHED"
	egressTAEPolicyApprovedEnvironment     = "AGENTSERVER_V2_TAE_POLICY_APPROVED"
	egressTAEPolicyEvidenceEnvironment     = "AGENTSERVER_V2_TAE_POLICY_EVIDENCE_REF"
	egressDecisionTimeoutEnvironment       = "AGENTSERVER_V2_EGRESS_DECISION_TIMEOUT"
	egressDevZTITokenEnvironment           = "AGENTSERVER_V2_DEV_TAE_ZTI_TOKEN"
	egressDevLarkAccessTokenEnvironment    = "AGENTSERVER_V2_DEV_LARK_ACCESS_TOKEN"
	egressDevCredentialLifetimeEnvironment = "AGENTSERVER_V2_DEV_LARK_CREDENTIAL_LIFETIME"

	defaultEgressDecisionTimeout  = 350 * time.Millisecond
	defaultDevCredentialLifetime  = time.Hour
	maximumDevelopmentSecretBytes = 16 * 1024
)

type egressAuthorizerConfig struct {
	listenAddress   string
	production      bool
	policyBootstrap bool

	tlsCertificate string
	tlsKey         string
	spiffeIdentity string

	coreURL         string
	coreCA          string
	coreCertificate string
	coreKey         string
	coreServerName  string

	placeholderKeyring string
	allowedTAEPSM      string
	taePolicy          taepolicy.Binding
	decisionTimeout    time.Duration

	devZTIToken           string
	devLarkAccessToken    string
	devCredentialLifetime time.Duration
}

func loadEgressAuthorizerConfig(getenv func(string) string, mode egressAuthorizerServeMode) (egressAuthorizerConfig, error) {
	if getenv == nil {
		return egressAuthorizerConfig{}, errors.New("egress-authorizer configuration source is required")
	}
	production := mode == egressAuthorizerServeProduction || mode == egressAuthorizerServePolicyBootstrap
	policyBootstrap := mode == egressAuthorizerServePolicyBootstrap
	if !production && mode != egressAuthorizerServeInsecureDevelopment {
		return egressAuthorizerConfig{}, errors.New("egress-authorizer serve mode is invalid")
	}
	required := func(name string) (string, error) {
		value := strings.TrimSpace(getenv(name))
		if value == "" {
			return "", fmt.Errorf("%s is required", name)
		}
		return value, nil
	}
	config := egressAuthorizerConfig{production: production, policyBootstrap: policyBootstrap}
	var err error
	if config.listenAddress, err = required(egressListenAddressEnvironment); err != nil {
		return egressAuthorizerConfig{}, err
	}
	if _, _, err := net.SplitHostPort(config.listenAddress); err != nil {
		return egressAuthorizerConfig{}, fmt.Errorf("parse egress-authorizer listen address: %w", err)
	}
	if !production && !egressLoopbackHost(config.listenAddress) {
		return egressAuthorizerConfig{}, errors.New("insecure-dev egress-authorizer must bind an explicit loopback address")
	}
	if production {
		for destination, name := range map[*string]string{
			&config.tlsCertificate: egressTLSCertificateEnvironment,
			&config.tlsKey:         egressTLSKeyEnvironment,
			&config.spiffeIdentity: egressSPIFFEIdentityEnvironment,
		} {
			*destination, err = required(name)
			if err != nil {
				return egressAuthorizerConfig{}, err
			}
		}
		for _, path := range []string{config.tlsCertificate, config.tlsKey} {
			if !cleanAbsolutePath(path) {
				return egressAuthorizerConfig{}, errors.New("egress-authorizer TLS files must use clean absolute paths")
			}
		}
		if !validEgressSPIFFEIdentity(config.spiffeIdentity) {
			return egressAuthorizerConfig{}, fmt.Errorf("%s must be an exact bounded SPIFFE URI", egressSPIFFEIdentityEnvironment)
		}
	}
	if !policyBootstrap {
		if config.placeholderKeyring, err = required(egressPlaceholderKeyringEnvironment); err != nil {
			return egressAuthorizerConfig{}, err
		}
		if !cleanAbsolutePath(config.placeholderKeyring) {
			return egressAuthorizerConfig{}, fmt.Errorf("%s must be a clean absolute path", egressPlaceholderKeyringEnvironment)
		}
		if config.allowedTAEPSM, err = required(egressAllowedTAEPSMEnvironment); err != nil {
			return egressAuthorizerConfig{}, err
		}
		if !validEgressText(config.allowedTAEPSM, 256) {
			return egressAuthorizerConfig{}, fmt.Errorf("%s is invalid", egressAllowedTAEPSMEnvironment)
		}
	}
	if production && !policyBootstrap {
		for destination, name := range map[*string]string{
			&config.coreURL:         egressCoreURLEnvironment,
			&config.coreCA:          egressCoreCAEnvironment,
			&config.coreCertificate: egressCoreCertificateEnvironment,
			&config.coreKey:         egressCoreKeyEnvironment,
			&config.coreServerName:  egressCoreServerNameEnvironment,
		} {
			*destination, err = required(name)
			if err != nil {
				return egressAuthorizerConfig{}, err
			}
		}
		for _, path := range []string{config.coreCA, config.coreCertificate, config.coreKey} {
			if !cleanAbsolutePath(path) {
				return egressAuthorizerConfig{}, errors.New("egress-authorizer Core TLS files must use clean absolute paths")
			}
		}
		coreOrigin, parseError := url.Parse(config.coreURL)
		if parseError != nil || coreOrigin.Scheme != "https" || coreOrigin.Host == "" || coreOrigin.Hostname() == "" ||
			coreOrigin.User != nil || coreOrigin.RawPath != "" || coreOrigin.RawQuery != "" || coreOrigin.Fragment != "" ||
			coreOrigin.Opaque != "" || coreOrigin.ForceQuery || (coreOrigin.Path != "" && coreOrigin.Path != "/") {
			return egressAuthorizerConfig{}, fmt.Errorf("%s must be an absolute canonical HTTPS origin", egressCoreURLEnvironment)
		}
		if !validEgressText(config.coreServerName, 253) || strings.ContainsAny(config.coreServerName, "/:@") {
			return egressAuthorizerConfig{}, fmt.Errorf("%s is invalid", egressCoreServerNameEnvironment)
		}
		config.taePolicy.Version = taepolicy.BindingVersion
		config.taePolicy.Region = "sg"
		config.taePolicy.SandboxPSM = config.allowedTAEPSM
		for destination, name := range map[*string]string{
			&config.taePolicy.Revision:      egressTAEPolicyRevisionEnvironment,
			&config.taePolicy.PolicySHA256:  egressTAEPolicySHA256Environment,
			&config.taePolicy.BindingSHA256: egressTAEPolicyBindingEnvironment,
			&config.taePolicy.PublicHost:    egressTAEPolicyHostEnvironment,
			&config.taePolicy.PublicAccess:  egressTAEPolicyAccessEnvironment,
			&config.taePolicy.WebhookMode:   egressTAEWebhookModeEnvironment,
			&config.taePolicy.WebhookPath:   egressTAEWebhookPathEnvironment,
			&config.taePolicy.EvidenceRef:   egressTAEPolicyEvidenceEnvironment,
		} {
			*destination, err = required(name)
			if err != nil {
				return egressAuthorizerConfig{}, err
			}
		}
		config.taePolicy.WebhookPSM = strings.TrimSpace(getenv(egressTAEWebhookPSMEnvironment))
		config.taePolicy.WebhookURL = strings.TrimSpace(getenv(egressTAEWebhookURLEnvironment))
		if config.taePolicy.PublicWebhookRequired, err = requiredEgressBool(getenv(egressTAEWebhookRequiredEnvironment), egressTAEWebhookRequiredEnvironment); err != nil {
			return egressAuthorizerConfig{}, err
		}
		if config.taePolicy.Published, err = requiredEgressBool(getenv(egressTAEPolicyPublishedEnvironment), egressTAEPolicyPublishedEnvironment); err != nil {
			return egressAuthorizerConfig{}, err
		}
		if config.taePolicy.Approved, err = requiredEgressBool(getenv(egressTAEPolicyApprovedEnvironment), egressTAEPolicyApprovedEnvironment); err != nil {
			return egressAuthorizerConfig{}, err
		}
		if err := config.taePolicy.Validate("sg", config.allowedTAEPSM, larkegresspolicy.SHA256Hex()); err != nil {
			return egressAuthorizerConfig{}, fmt.Errorf("TAE policy binding: %w", err)
		}
	}
	config.decisionTimeout, err = optionalEgressDuration(
		getenv(egressDecisionTimeoutEnvironment), defaultEgressDecisionTimeout,
		10*time.Millisecond, 450*time.Millisecond, egressDecisionTimeoutEnvironment,
	)
	if err != nil {
		return egressAuthorizerConfig{}, err
	}
	if !production {
		if config.devZTIToken, err = required(egressDevZTITokenEnvironment); err != nil {
			return egressAuthorizerConfig{}, err
		}
		if config.devLarkAccessToken, err = required(egressDevLarkAccessTokenEnvironment); err != nil {
			return egressAuthorizerConfig{}, err
		}
		if !validDevelopmentSecret(config.devZTIToken) || !validDevelopmentSecret(config.devLarkAccessToken) {
			return egressAuthorizerConfig{}, errors.New("insecure-dev ZTI and Lark tokens must be bounded non-whitespace values")
		}
		config.devCredentialLifetime, err = optionalEgressDuration(
			getenv(egressDevCredentialLifetimeEnvironment), defaultDevCredentialLifetime,
			time.Minute, 24*time.Hour, egressDevCredentialLifetimeEnvironment,
		)
		if err != nil {
			return egressAuthorizerConfig{}, err
		}
	}
	return config, nil
}

func optionalEgressDuration(value string, fallback, minimum, maximum time.Duration, name string) (time.Duration, error) {
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

func requiredEgressBool(value, name string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("%s must be exactly true or false", name)
	}
}

func egressLoopbackHost(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	parsed := net.ParseIP(host)
	return parsed != nil && parsed.IsLoopback()
}

func cleanAbsolutePath(value string) bool {
	return filepath.IsAbs(value) && filepath.Clean(value) == value && !strings.ContainsRune(value, '\x00')
}

func validEgressSPIFFEIdentity(raw string) bool {
	identity, err := url.Parse(raw)
	return err == nil && identity.Scheme == "spiffe" && identity.Host != "" && identity.User == nil &&
		identity.Path != "" && identity.RawPath == "" && identity.RawQuery == "" && identity.Fragment == "" &&
		identity.Opaque == "" && !identity.ForceQuery && identity.String() == raw && validEgressText(raw, 2048)
}

func validDevelopmentSecret(value string) bool {
	return len(value) >= 16 && len(value) <= maximumDevelopmentSecretBytes && validEgressText(value, maximumDevelopmentSecretBytes) &&
		!strings.ContainsAny(value, " \t\r\n")
}

func validEgressText(value string, maximum int) bool {
	if value == "" || len(value) > maximum || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}
