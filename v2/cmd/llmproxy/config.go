package main

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	llmProxyListenAddressEnvironment     = "AGENTSERVER_V2_LLMPROXY_LISTEN_ADDR"
	llmProxyTLSCertificateEnvironment    = "AGENTSERVER_V2_LLMPROXY_TLS_CERT_FILE"
	llmProxyTLSKeyEnvironment            = "AGENTSERVER_V2_LLMPROXY_TLS_KEY_FILE"
	llmProxySPIFFEIdentityEnvironment    = "AGENTSERVER_V2_LLMPROXY_SPIFFE_ID"
	llmProxyCoreURLEnvironment           = "AGENTSERVER_V2_CORE_URL"
	llmProxyCoreCAEnvironment            = "AGENTSERVER_V2_CORE_CA_FILE"
	llmProxyCoreServerNameEnvironment    = "AGENTSERVER_V2_CORE_SERVER_NAME"
	llmProxyCapabilityIssuerEnvironment  = "AGENTSERVER_V2_RUN_CAPABILITY_ISSUER"
	llmProxyCapabilityKeyringEnvironment = "AGENTSERVER_V2_RUN_CAPABILITY_KEYRING_FILE"

	maximumLLMProxyConfigurationBytes = 4 * 1024
)

type llmProxyConfig struct {
	listenAddress     string
	tlsCertificate    string
	tlsKey            string
	spiffeIdentity    string
	coreURL           string
	coreCA            string
	coreServerName    string
	capabilityIssuer  string
	capabilityKeyring string
}

func loadLLMProxyConfig(getenv func(string) string) (llmProxyConfig, error) {
	if getenv == nil {
		return llmProxyConfig{}, errors.New("llmproxy configuration source is required")
	}
	required := func(name string) (string, error) {
		value := getenv(name)
		if value == "" {
			return "", fmt.Errorf("%s is required", name)
		}
		if !validLLMProxyConfigurationText(value) {
			return "", fmt.Errorf("%s must be bounded UTF-8 text without surrounding whitespace or control bytes", name)
		}
		return value, nil
	}
	var config llmProxyConfig
	var err error
	for target, name := range map[*string]string{
		&config.listenAddress:     llmProxyListenAddressEnvironment,
		&config.tlsCertificate:    llmProxyTLSCertificateEnvironment,
		&config.tlsKey:            llmProxyTLSKeyEnvironment,
		&config.spiffeIdentity:    llmProxySPIFFEIdentityEnvironment,
		&config.coreURL:           llmProxyCoreURLEnvironment,
		&config.coreCA:            llmProxyCoreCAEnvironment,
		&config.capabilityIssuer:  llmProxyCapabilityIssuerEnvironment,
		&config.capabilityKeyring: llmProxyCapabilityKeyringEnvironment,
	} {
		*target, err = required(name)
		if err != nil {
			return llmProxyConfig{}, err
		}
	}
	for target, name := range map[*string]string{
		&config.coreServerName: llmProxyCoreServerNameEnvironment,
	} {
		value := getenv(name)
		if value != "" && !validLLMProxyConfigurationText(value) {
			return llmProxyConfig{}, fmt.Errorf("%s must be bounded UTF-8 text without surrounding whitespace or control bytes", name)
		}
		*target = value
	}
	if _, _, err := net.SplitHostPort(config.listenAddress); err != nil {
		return llmProxyConfig{}, fmt.Errorf("%s must be a valid TCP listen address: %w", llmProxyListenAddressEnvironment, err)
	}
	if err := validateHTTPSOrigin(config.coreURL, llmProxyCoreURLEnvironment); err != nil {
		return llmProxyConfig{}, err
	}
	if err := validateSPIFFEIdentity(config.spiffeIdentity); err != nil {
		return llmProxyConfig{}, fmt.Errorf("%s: %w", llmProxySPIFFEIdentityEnvironment, err)
	}
	for label, path := range map[string]string{
		llmProxyTLSCertificateEnvironment:    config.tlsCertificate,
		llmProxyTLSKeyEnvironment:            config.tlsKey,
		llmProxyCoreCAEnvironment:            config.coreCA,
		llmProxyCapabilityKeyringEnvironment: config.capabilityKeyring,
	} {
		if err := validateAbsoluteConfigPath(path); err != nil {
			return llmProxyConfig{}, fmt.Errorf("%s: %w", label, err)
		}
	}
	return config, nil
}

func validateHTTPSOrigin(raw, name string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" || parsed.RawPath != "" || parsed.Opaque != "" || parsed.ForceQuery ||
		(parsed.Path != "" && parsed.Path != "/") {
		return fmt.Errorf("%s must be an HTTPS origin without credentials, path, query, or fragment", name)
	}
	return nil
}

func validateSPIFFEIdentity(raw string) error {
	identity, err := url.Parse(raw)
	if err != nil || identity.Scheme != "spiffe" || identity.Host == "" || identity.User != nil || identity.Path == "" ||
		identity.RawPath != "" || identity.RawQuery != "" || identity.Fragment != "" || identity.Opaque != "" || identity.ForceQuery {
		return errors.New("value must be an exact SPIFFE URI")
	}
	return nil
}

func validateAbsoluteConfigPath(path string) error {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsRune(path, 0) {
		return errors.New("path must be absolute and clean")
	}
	return nil
}

func validLLMProxyConfigurationText(value string) bool {
	if value == "" || len(value) > maximumLLMProxyConfigurationBytes || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func validLLMProxyRoute(value string) bool {
	return len(value) <= 256 && validLLMProxyConfigurationText(value)
}
