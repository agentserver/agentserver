package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/agentserver/agentserver/v2/internal/executionbackend"
	"github.com/agentserver/agentserver/v2/internal/executorgateway"
	"github.com/agentserver/agentserver/v2/internal/managedsandboxprofile"
	"github.com/agentserver/agentserver/v2/internal/sandboxcapability"
	"github.com/agentserver/agentserver/v2/internal/sandboxclient"
)

type managedSandboxGatewayProfilesDocument struct {
	Profiles []managedSandboxGatewayProfileDocument `json:"profiles"`
}

type managedSandboxGatewayProfileDocument struct {
	Region                   string `json:"region"`
	ProfileID                string `json:"profileId"`
	BindingSHA256            string `json:"bindingSha256"`
	EnvironmentID            string `json:"environmentId"`
	SandboxGatewayURL        string `json:"sandboxGatewayUrl"`
	SandboxGatewayServerName string `json:"sandboxGatewayServerName,omitempty"`
	RuntimeProfileSHA256     string `json:"runtimeProfileSha256"`
	PackSetSHA256            string `json:"packSetSha256"`
	SandboxTTL               string `json:"sandboxTtl"`
	ActivityTTL              string `json:"activityTtl"`
}

type configuredManagedSandboxGatewayProfile struct {
	binding      managedsandboxprofile.Binding
	baseURL      string
	serverName   string
	provisioning executorgateway.ManagedSandboxProvisioningSpec
}

// configureProfiledTAEExecution constructs one closed routing graph for all
// installed managed sandbox profiles. Acquisition, lifecycle fencing, and
// data-plane execution are derived from the same profile document.
func configureProfiledTAEExecution(
	getenv func(string) string,
	mode gatewayServeMode,
	clientCertificateFile, clientKeyFile, clientSPIFFEIdentity string,
	coreAuthorities executorgateway.ManagedCredentialAuthoritySource,
	coreProcessCredentials executorgateway.ManagedProcessCredentialSource,
) (
	executionbackend.Backend,
	[]*http.Client,
	executorgateway.ManagedProcessEnvironmentIssuer,
	executorgateway.ManagedTargetFencer,
	executorgateway.ManagedSandboxSessionAcquirer,
	error,
) {
	if getenv == nil {
		return nil, nil, nil, nil, nil, errors.New("profiled TAE configuration source is required")
	}
	profiles, err := parseManagedSandboxGatewayProfiles([]byte(strings.TrimSpace(getenv(gatewayManagedProfilesEnvironment))), mode)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("%s: %w", gatewayManagedProfilesEnvironment, err)
	}
	for _, legacyName := range []string{
		gatewaySandboxGatewayURLEnvironment, gatewaySandboxGatewayServerNameEnvironment,
		gatewayManagedSandboxRegionEnvironment, gatewayManagedSandboxProfileIDEnvironment,
		gatewayManagedSandboxBindingEnvironment, gatewayManagedEnvironmentIDEnvironment,
		gatewayManagedRuntimeDigestEnvironment, gatewayManagedPackSetDigestEnvironment,
		gatewayManagedSandboxTTLEnvironment, gatewayManagedActivityTTLEnvironment,
	} {
		if strings.TrimSpace(getenv(legacyName)) != "" {
			return nil, nil, nil, nil, nil, fmt.Errorf("%s cannot be combined with legacy setting %s", gatewayManagedProfilesEnvironment, legacyName)
		}
	}
	required := func(name string) (string, error) {
		value := strings.TrimSpace(getenv(name))
		if value == "" {
			return "", fmt.Errorf("%s is required when profiled TAE execution is enabled", name)
		}
		return value, nil
	}
	backendIssuer, err := required(gatewaySandboxCapabilityIssuerEnvironment)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	backendKeyID, err := required(gatewaySandboxCapabilityKeyIDEnvironment)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	backendKeyFile, err := required(gatewaySandboxCapabilityKeyEnvironment)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	fencerIssuer, err := required(gatewaySandboxFencerIssuerEnvironment)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	fencerKeyID, err := required(gatewaySandboxFencerKeyIDEnvironment)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	fencerKeyFile, err := required(gatewaySandboxFencerKeyEnvironment)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	if backendKeyID == fencerKeyID {
		return nil, nil, nil, nil, nil, errors.New("sandbox backend and fencer capabilities must use distinct key IDs")
	}
	backendSigner, err := sandboxcapability.LoadSigner(
		backendIssuer, sandboxcapability.AudienceBackend, backendKeyID, backendKeyFile,
	)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("configure profiled sandbox backend capability signer: %w", err)
	}
	backendTokens, err := executorgateway.NewSignedSandboxGatewayTokenSource(backendSigner, time.Now, 30*time.Second)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	fencerSigner, err := sandboxcapability.LoadSigner(
		fencerIssuer, sandboxcapability.AudienceLifecycle, fencerKeyID, fencerKeyFile,
	)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("configure profiled sandbox lifecycle capability signer: %w", err)
	}
	fencerTokens, err := sandboxclient.NewSignedTokenSource(fencerSigner, time.Now, 30*time.Second)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	issuer, err := configureManagedProcessEnvironmentIssuer(getenv, mode, coreAuthorities, coreProcessCredentials)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}

	var caFile string
	for _, profile := range profiles {
		if strings.HasPrefix(profile.baseURL, "https://") {
			caFile, err = required(gatewaySandboxGatewayCAEnvironment)
			if err != nil {
				return nil, nil, nil, nil, nil, err
			}
			break
		}
	}
	clients := make([]*http.Client, 0, len(profiles))
	closeClients := func() {
		for _, client := range clients {
			client.CloseIdleConnections()
		}
	}
	backendByEnvironment := make(map[string]executionbackend.Backend, len(profiles))
	fencerByProfile := make(map[string]executorgateway.ManagedTargetFencer, len(profiles))
	acquirerByProfile := make(map[string]executorgateway.ManagedSandboxSessionAcquirer, len(profiles))
	for _, profile := range profiles {
		httpClient, clientErr := newManagedSandboxGatewayHTTPClient(
			profile, mode, caFile, clientCertificateFile, clientKeyFile, clientSPIFFEIdentity,
		)
		if clientErr != nil {
			closeClients()
			return nil, nil, nil, nil, nil, clientErr
		}
		clients = append(clients, httpClient)
		backend, clientErr := executorgateway.NewTAEBackendWithLogger(profile.baseURL, httpClient, backendTokens, slog.Default())
		if clientErr != nil {
			closeClients()
			return nil, nil, nil, nil, nil, clientErr
		}
		lifecycle, clientErr := sandboxclient.New(profile.baseURL, httpClient, fencerTokens)
		if clientErr != nil {
			closeClients()
			return nil, nil, nil, nil, nil, clientErr
		}
		fencer, clientErr := executorgateway.NewDefaultGatewayManagedTargetFencer(lifecycle)
		if clientErr != nil {
			closeClients()
			return nil, nil, nil, nil, nil, clientErr
		}
		acquirer, clientErr := executorgateway.NewDefaultGatewayManagedSandboxSessionAcquirer(
			lifecycle, profile.provisioning, slog.Default(),
		)
		if clientErr != nil {
			closeClients()
			return nil, nil, nil, nil, nil, clientErr
		}
		backendByEnvironment[profile.binding.EnvironmentID] = backend
		fencerByProfile[profile.binding.ProfileID] = fencer
		acquirerByProfile[profile.binding.ProfileID] = acquirer
	}
	backendRouter, err := executorgateway.NewTAEBackendRouter(backendByEnvironment)
	if err != nil {
		closeClients()
		return nil, nil, nil, nil, nil, err
	}
	fencerRouter, err := executorgateway.NewManagedTargetFencerRouter(fencerByProfile)
	if err != nil {
		closeClients()
		return nil, nil, nil, nil, nil, err
	}
	acquirerRouter, err := executorgateway.NewManagedSandboxSessionAcquirerRouter(acquirerByProfile)
	if err != nil {
		closeClients()
		return nil, nil, nil, nil, nil, err
	}
	return backendRouter, clients, issuer, fencerRouter, acquirerRouter, nil
}

func parseManagedSandboxGatewayProfiles(raw []byte, mode gatewayServeMode) ([]configuredManagedSandboxGatewayProfile, error) {
	if mode != gatewayServeProduction && mode != gatewayServeInsecureDevelopment {
		return nil, errors.New("managed sandbox gateway profile serve mode is invalid")
	}
	if len(raw) == 0 || len(raw) > 128*1024 {
		return nil, errors.New("managed sandbox gateway profile catalog must contain between 1 and 131072 bytes")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var document managedSandboxGatewayProfilesDocument
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("decode managed sandbox gateway profile catalog: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("managed sandbox gateway profile catalog contains trailing data")
	}
	if len(document.Profiles) < 1 || len(document.Profiles) > len(managedsandboxprofile.Regions()) {
		return nil, errors.New("managed sandbox gateway profile catalog must contain between one and four profiles")
	}
	profiles := make([]configuredManagedSandboxGatewayProfile, 0, len(document.Profiles))
	profileIDs := make(map[string]struct{}, len(document.Profiles))
	regions := make(map[string]struct{}, len(document.Profiles))
	environments := make(map[string]struct{}, len(document.Profiles))
	for _, source := range document.Profiles {
		binding := managedsandboxprofile.Binding{
			Region: source.Region, ProfileID: source.ProfileID,
			BindingSHA256: source.BindingSHA256, EnvironmentID: source.EnvironmentID,
		}
		if err := binding.Validate(); err != nil {
			return nil, err
		}
		if _, duplicate := profileIDs[binding.ProfileID]; duplicate {
			return nil, fmt.Errorf("managed sandbox profile %q is repeated", binding.ProfileID)
		}
		if _, duplicate := regions[binding.Region]; duplicate {
			return nil, fmt.Errorf("managed sandbox region %q is repeated", binding.Region)
		}
		if _, duplicate := environments[binding.EnvironmentID]; duplicate {
			return nil, fmt.Errorf("managed sandbox environment %q is repeated", binding.EnvironmentID)
		}
		baseURL, serverName, err := validateManagedSandboxGatewayEndpoint(
			source.SandboxGatewayURL, source.SandboxGatewayServerName, mode,
		)
		if err != nil {
			return nil, fmt.Errorf("managed sandbox profile %q: %w", binding.ProfileID, err)
		}
		sandboxTTL, err := parseProfiledManagedDuration(source.SandboxTTL, 30*time.Second, 24*time.Hour)
		if err != nil {
			return nil, fmt.Errorf("managed sandbox profile %q sandboxTtl: %w", binding.ProfileID, err)
		}
		activityTTL, err := parseProfiledManagedDuration(source.ActivityTTL, 3*time.Second, sandboxTTL)
		if err != nil {
			return nil, fmt.Errorf("managed sandbox profile %q activityTtl: %w", binding.ProfileID, err)
		}
		provisioning := executorgateway.ManagedSandboxProvisioningSpec{
			Region: binding.Region, ProfileID: binding.ProfileID, ProfileBindingSHA256: binding.BindingSHA256,
			EnvironmentID: binding.EnvironmentID, RuntimeProfileDigest: source.RuntimeProfileSHA256,
			PackSetDigest: source.PackSetSHA256, SandboxTTL: sandboxTTL, ActivityTTL: activityTTL,
		}
		if err := executorgateway.ValidateManagedSandboxProvisioningSpec(provisioning); err != nil {
			return nil, fmt.Errorf("managed sandbox profile %q: %w", binding.ProfileID, err)
		}
		profiles = append(profiles, configuredManagedSandboxGatewayProfile{
			binding: binding, baseURL: baseURL, serverName: serverName, provisioning: provisioning,
		})
		profileIDs[binding.ProfileID] = struct{}{}
		regions[binding.Region] = struct{}{}
		environments[binding.EnvironmentID] = struct{}{}
	}
	return profiles, nil
}

func validateManagedSandboxGatewayEndpoint(raw, serverName string, mode gatewayServeMode) (string, string, error) {
	raw = strings.TrimSpace(raw)
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawPath != "" ||
		parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" || parsed.ForceQuery ||
		(parsed.Path != "" && parsed.Path != "/") {
		return "", "", errors.New("sandboxGatewayUrl must be an absolute canonical HTTP(S) origin")
	}
	if parsed.Scheme == "https" {
		serverName = strings.TrimSpace(serverName)
		if serverName == "" || len(serverName) > 253 || strings.ContainsAny(serverName, "\x00\r\n /:@") {
			return "", "", errors.New("sandboxGatewayServerName is required and must be a bounded DNS name for HTTPS")
		}
	} else if parsed.Scheme == "http" && mode == gatewayServeInsecureDevelopment && loopbackGatewayHost(parsed.Hostname()) {
		if strings.TrimSpace(serverName) != "" {
			return "", "", errors.New("sandboxGatewayServerName must be empty for insecure-development HTTP")
		}
		serverName = ""
	} else {
		return "", "", errors.New("sandboxGatewayUrl must use HTTPS except for loopback insecure development")
	}
	return strings.TrimSuffix(raw, "/"), serverName, nil
}

func newManagedSandboxGatewayHTTPClient(
	profile configuredManagedSandboxGatewayProfile,
	mode gatewayServeMode,
	caFile, clientCertificateFile, clientKeyFile, clientSPIFFEIdentity string,
) (*http.Client, error) {
	if strings.HasPrefix(profile.baseURL, "https://") {
		client, err := newCoreHTTPClientWithIdentity(
			caFile, clientCertificateFile, clientKeyFile, profile.serverName, clientSPIFFEIdentity,
		)
		if err != nil {
			return nil, fmt.Errorf("configure sandbox-gateway client for profile %q: %w", profile.binding.ProfileID, err)
		}
		return client, nil
	}
	if mode != gatewayServeInsecureDevelopment {
		return nil, errors.New("cleartext managed sandbox gateway client is forbidden in production")
	}
	transport := &http.Transport{
		Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		MaxIdleConns: 32, MaxIdleConnsPerHost: 32, IdleConnTimeout: time.Minute,
		ResponseHeaderTimeout: 30 * time.Second, DisableCompression: true,
	}
	return &http.Client{Transport: transport}, nil
}

func parseProfiledManagedDuration(value string, minimum, maximum time.Duration) (time.Duration, error) {
	parsed, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil || parsed < minimum || parsed > maximum || parsed%time.Second != 0 {
		return 0, fmt.Errorf("must be a whole-second Go duration between %s and %s", minimum, maximum)
	}
	return parsed, nil
}
