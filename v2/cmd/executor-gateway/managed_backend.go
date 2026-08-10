package main

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/agentserver/agentserver/v2/internal/egresscapability"
	"github.com/agentserver/agentserver/v2/internal/executorgateway"
	"github.com/agentserver/agentserver/v2/internal/sandboxcapability"
	"github.com/agentserver/agentserver/v2/internal/sandboxclient"
)

func configureTAEBackend(
	getenv func(string) string,
	mode gatewayServeMode,
	clientCertificateFile, clientKeyFile, clientSPIFFEIdentity string,
) (*executorgateway.TAEBackend, *http.Client, error) {
	if getenv == nil {
		return nil, nil, errors.New("TAE backend configuration source is required")
	}
	baseURL := strings.TrimSpace(getenv(gatewaySandboxGatewayURLEnvironment))
	configuredNames := []string{
		gatewaySandboxGatewayCAEnvironment, gatewaySandboxGatewayServerNameEnvironment,
		gatewaySandboxCapabilityIssuerEnvironment, gatewaySandboxCapabilityKeyIDEnvironment,
		gatewaySandboxCapabilityKeyEnvironment,
		gatewaySandboxFencerIssuerEnvironment, gatewaySandboxFencerKeyIDEnvironment,
		gatewaySandboxFencerKeyEnvironment,
		gatewayEgressPlaceholderIssuerEnvironment, gatewayEgressPlaceholderKeyIDEnvironment,
		gatewayEgressPlaceholderKeyEnvironment,
		gatewayManagedTAEPSMEnvironment,
	}
	if baseURL == "" {
		for _, name := range configuredNames {
			if strings.TrimSpace(getenv(name)) != "" {
				return nil, nil, fmt.Errorf("%s is required when %s is configured", gatewaySandboxGatewayURLEnvironment, name)
			}
		}
		return nil, nil, nil
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawPath != "" ||
		parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" || parsed.ForceQuery ||
		(parsed.Path != "" && parsed.Path != "/") {
		return nil, nil, fmt.Errorf("%s must be an absolute canonical HTTP(S) origin", gatewaySandboxGatewayURLEnvironment)
	}
	if mode == gatewayServeProduction && parsed.Scheme != "https" {
		return nil, nil, fmt.Errorf("%s must use HTTPS in production", gatewaySandboxGatewayURLEnvironment)
	}
	if parsed.Scheme != "https" && (parsed.Scheme != "http" || !loopbackGatewayHost(parsed.Hostname()) || mode != gatewayServeInsecureDevelopment) {
		return nil, nil, fmt.Errorf("%s permits cleartext HTTP only on loopback in insecure development", gatewaySandboxGatewayURLEnvironment)
	}
	required := func(name string) (string, error) {
		value := strings.TrimSpace(getenv(name))
		if value == "" {
			return "", fmt.Errorf("%s is required when the TAE backend is enabled", name)
		}
		return value, nil
	}
	issuer, err := required(gatewaySandboxCapabilityIssuerEnvironment)
	if err != nil {
		return nil, nil, err
	}
	keyID, err := required(gatewaySandboxCapabilityKeyIDEnvironment)
	if err != nil {
		return nil, nil, err
	}
	keyFile, err := required(gatewaySandboxCapabilityKeyEnvironment)
	if err != nil {
		return nil, nil, err
	}
	signer, err := sandboxcapability.LoadSigner(issuer, sandboxcapability.AudienceBackend, keyID, keyFile)
	if err != nil {
		return nil, nil, fmt.Errorf("configure sandbox backend capability signer: %w", err)
	}
	tokens, err := executorgateway.NewSignedSandboxGatewayTokenSource(signer, time.Now, 30*time.Second)
	if err != nil {
		return nil, nil, err
	}
	var httpClient *http.Client
	if parsed.Scheme == "https" {
		caFile, err := required(gatewaySandboxGatewayCAEnvironment)
		if err != nil {
			return nil, nil, err
		}
		serverName, err := required(gatewaySandboxGatewayServerNameEnvironment)
		if err != nil {
			return nil, nil, err
		}
		httpClient, err = newCoreHTTPClientWithIdentity(
			caFile, clientCertificateFile, clientKeyFile, serverName, clientSPIFFEIdentity,
		)
		if err != nil {
			return nil, nil, fmt.Errorf("configure sandbox-gateway HTTP client: %w", err)
		}
	} else {
		transport := &http.Transport{
			Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			MaxIdleConns: 32, MaxIdleConnsPerHost: 32, IdleConnTimeout: time.Minute,
			ResponseHeaderTimeout: 30 * time.Second, DisableCompression: true,
		}
		httpClient = &http.Client{Transport: transport}
	}
	backend, err := executorgateway.NewTAEBackend(baseURL, httpClient, tokens)
	if err != nil {
		httpClient.CloseIdleConnections()
		return nil, nil, err
	}
	return backend, httpClient, nil
}

func configureManagedExecutionSecurity(
	getenv func(string) string,
	mode gatewayServeMode,
	backend *executorgateway.TAEBackend,
	httpClient *http.Client,
	coreAuthorities executorgateway.ManagedLarkEgressAuthoritySource,
	coreProcessCredentials executorgateway.ManagedLarkProcessCredentialSource,
) (executorgateway.ManagedProcessEnvironmentIssuer, executorgateway.ManagedTargetFencer, error) {
	if getenv == nil {
		return nil, nil, errors.New("managed execution security configuration source is required")
	}
	if backend == nil {
		return nil, nil, nil
	}
	if mode != gatewayServeProduction && mode != gatewayServeInsecureDevelopment {
		return nil, nil, errors.New("managed execution serve mode is invalid")
	}
	if httpClient == nil {
		return nil, nil, errors.New("managed execution requires the sandbox-gateway HTTP client")
	}
	required := func(name string) (string, error) {
		value := strings.TrimSpace(getenv(name))
		if value == "" {
			return "", fmt.Errorf("%s is required when the TAE backend is enabled", name)
		}
		return value, nil
	}
	fencerIssuer, err := required(gatewaySandboxFencerIssuerEnvironment)
	if err != nil {
		return nil, nil, err
	}
	fencerKeyID, err := required(gatewaySandboxFencerKeyIDEnvironment)
	if err != nil {
		return nil, nil, err
	}
	fencerKeyFile, err := required(gatewaySandboxFencerKeyEnvironment)
	if err != nil {
		return nil, nil, err
	}
	if fencerKeyID == strings.TrimSpace(getenv(gatewaySandboxCapabilityKeyIDEnvironment)) {
		return nil, nil, errors.New("sandbox backend and fencer capabilities must use distinct key IDs")
	}
	fencerSigner, err := sandboxcapability.LoadSigner(
		fencerIssuer, sandboxcapability.AudienceLifecycle, fencerKeyID, fencerKeyFile,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("configure sandbox fencer capability signer: %w", err)
	}
	fencerTokens, err := sandboxclient.NewSignedTokenSource(fencerSigner, time.Now, 30*time.Second)
	if err != nil {
		return nil, nil, err
	}
	baseURL := strings.TrimSpace(getenv(gatewaySandboxGatewayURLEnvironment))
	baseURL = strings.TrimSuffix(baseURL, "/")
	lifecycleClient, err := sandboxclient.New(baseURL, httpClient, fencerTokens)
	if err != nil {
		return nil, nil, err
	}
	fencer, err := executorgateway.NewDefaultGatewayManagedTargetFencer(lifecycleClient)
	if err != nil {
		return nil, nil, err
	}

	taePSM, err := required(gatewayManagedTAEPSMEnvironment)
	if err != nil {
		return nil, nil, err
	}
	if len(taePSM) > 256 || strings.ContainsAny(taePSM, "\x00\r\n") {
		return nil, nil, fmt.Errorf("%s is invalid", gatewayManagedTAEPSMEnvironment)
	}

	egressNames := []string{
		gatewayEgressPlaceholderIssuerEnvironment, gatewayEgressPlaceholderKeyIDEnvironment,
		gatewayEgressPlaceholderKeyEnvironment,
	}
	for _, name := range egressNames {
		if strings.TrimSpace(getenv(name)) == "" {
			return nil, nil, fmt.Errorf("%s is required for workspace-managed Lark credential delivery", name)
		}
	}
	egressIssuer, err := required(gatewayEgressPlaceholderIssuerEnvironment)
	if err != nil {
		return nil, nil, err
	}
	egressKeyID, err := required(gatewayEgressPlaceholderKeyIDEnvironment)
	if err != nil {
		return nil, nil, err
	}
	egressKeyFile, err := required(gatewayEgressPlaceholderKeyEnvironment)
	if err != nil {
		return nil, nil, err
	}
	if egressKeyID == fencerKeyID || egressKeyID == strings.TrimSpace(getenv(gatewaySandboxCapabilityKeyIDEnvironment)) {
		return nil, nil, errors.New("egress placeholder and sandbox capabilities must use distinct key IDs")
	}
	egressSigner, err := egresscapability.LoadSigner(egressIssuer, egressKeyID, egressKeyFile)
	if err != nil {
		return nil, nil, fmt.Errorf("configure egress placeholder signer: %w", err)
	}
	if coreAuthorities == nil {
		return nil, nil, errors.New("managed credential authority source must be v2 Core")
	}
	if coreProcessCredentials == nil {
		return nil, nil, errors.New("managed process credential source must be v2 Core")
	}
	// The CLI application identity is a non-secret runtime hint. Workspace
	// Lark/ByteCloud/GitHub credentials are selected by Platform and resolved
	// by corecredentials; no client ID or token is deployment configuration.
	issuer, err := executorgateway.NewDefaultWorkspaceManagedLarkEnvironmentIssuer(
		egressSigner, coreAuthorities, coreProcessCredentials,
		executorgateway.ManagedCredentialApplicationID, taePSM,
	)
	if err != nil {
		return nil, nil, err
	}
	return issuer, fencer, nil
}

func loopbackGatewayHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}
