package main

import (
	"bytes"
	"context"
	"crypto/tls"
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

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/executionbackend"
	"github.com/agentserver/agentserver/v2/internal/executorgateway"
	"github.com/agentserver/agentserver/v2/internal/managedsandboxprofile"
	"github.com/agentserver/agentserver/v2/internal/repositorycheckout"
	"github.com/agentserver/agentserver/v2/internal/sandboxcapability"
	"github.com/agentserver/agentserver/v2/internal/sandboxclient"
	"github.com/agentserver/agentserver/v2/internal/sandboxcontract"
)

type managedSandboxGatewayProfilesDocument struct {
	Profiles []managedSandboxGatewayProfileDocument `json:"profiles"`
}

type managedSandboxGatewayProfileDocument struct {
	BackendKind              executionbackend.Kind `json:"backendKind,omitempty"`
	Region                   string                `json:"region"`
	EnvironmentID            string                `json:"environmentId"`
	SandboxGatewayURL        string                `json:"sandboxGatewayUrl"`
	SandboxGatewayServerName string                `json:"sandboxGatewayServerName,omitempty"`
	SandboxTTL               string                `json:"sandboxTtl"`
	ActivityTTL              string                `json:"activityTtl"`
	ExternalTLS              bool                  `json:"externalTls,omitempty"`
}

type configuredManagedSandboxGatewayProfile struct {
	kind         executionbackend.Kind
	binding      managedsandboxprofile.Binding
	baseURL      string
	serverName   string
	provisioning executorgateway.ManagedSandboxProvisioningSpec
	externalTLS  bool
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
		gatewayManagedSandboxRegionEnvironment, gatewayManagedEnvironmentIDEnvironment,
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
	fencerByRegion := make(map[string]executorgateway.ManagedTargetFencer, len(profiles))
	acquirerByRegion := make(map[string]executorgateway.ManagedSandboxSessionAcquirer, len(profiles))
	for _, profile := range profiles {
		httpClient, clientErr := newManagedSandboxGatewayHTTPClient(
			profile, mode, caFile, clientCertificateFile, clientKeyFile, clientSPIFFEIdentity,
		)
		if clientErr != nil {
			closeClients()
			return nil, nil, nil, nil, nil, clientErr
		}
		clients = append(clients, httpClient)
		kind := profile.kind
		if kind == "" {
			kind = executionbackend.KindTAE
		}
		backend, clientErr := executorgateway.NewManagedBackend(kind, profile.baseURL, httpClient, backendTokens, slog.Default())
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
		if kind == executionbackend.KindKubernetes {
			profile.provisioning.RepositoryPreparer = lifecycle
		}
		acquirer, clientErr := executorgateway.NewDefaultGatewayManagedSandboxSessionAcquirer(
			lifecycle, profile.provisioning, slog.Default(),
		)
		if clientErr != nil {
			closeClients()
			return nil, nil, nil, nil, nil, clientErr
		}
		if credentialClient, ok := coreAuthorities.(executorgateway.RepositoryCredentialClient); ok {
			acquirer.SetRepositoryCredentialResolver(func(ctx context.Context, principal executorgateway.ExecutorMCPPrincipal, ref sandboxcontract.SandboxRef, bindingID string) (*repositorycheckout.Credential, error) {
				response, err := credentialClient.ResolveRepositoryCredential(ctx, corecontract.ResolveRepositoryCredentialRequest{Operation: corecontract.EgressCredentialOperation{WorkspaceID: principal.WorkspaceID, SessionID: principal.SessionID, ActorID: principal.ActorID, EnvironmentID: principal.Workspace.EnvironmentID, RunID: principal.Run.RunID, RunAttemptID: principal.Run.RunAttemptID, RunAttemptGeneration: principal.Run.RunAttemptGeneration, SandboxID: ref.SandboxID, TargetGeneration: ref.TargetGeneration}, BindingID: bindingID, EnvironmentID: principal.Workspace.EnvironmentID, RunID: principal.Run.RunID, RunAttemptID: principal.Run.RunAttemptID, HolderID: principal.Run.HolderID})
				if err != nil {
					return nil, err
				}
				if !response.Configured {
					return nil, errors.New("repository Git credential is not configured")
				}
				return &repositorycheckout.Credential{Username: response.Username, Token: response.Token}, nil
			})
		}
		backendByEnvironment[profile.binding.EnvironmentID] = backend
		fencerByRegion[profile.binding.Region] = fencer
		acquirerByRegion[profile.binding.Region] = acquirer
	}
	kind := profiles[0].kind
	if kind == "" {
		kind = executionbackend.KindTAE
	}
	backendRouter, err := executorgateway.NewManagedBackendRouter(kind, backendByEnvironment)
	if err != nil {
		closeClients()
		return nil, nil, nil, nil, nil, err
	}
	fencerRouter, err := executorgateway.NewManagedTargetFencerRouter(fencerByRegion)
	if err != nil {
		closeClients()
		return nil, nil, nil, nil, nil, err
	}
	acquirerRouter, err := executorgateway.NewManagedSandboxSessionAcquirerRouter(acquirerByRegion)
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
	regions := make(map[string]struct{}, len(document.Profiles))
	environments := make(map[string]struct{}, len(document.Profiles))
	for _, source := range document.Profiles {
		kind := source.BackendKind
		if kind == "" {
			kind = executionbackend.KindTAE
		}
		if !kind.Managed() || (kind == executionbackend.KindKubernetes && source.Region != managedsandboxprofile.RegionSG && source.Region != managedsandboxprofile.RegionCN) || (kind == executionbackend.KindTAE && source.Region == managedsandboxprofile.RegionSG) {
			return nil, errors.New("managed provider kind and region do not match")
		}
		if source.ExternalTLS && kind != executionbackend.KindKubernetes {
			return nil, errors.New("external TLS is only supported for Kubernetes sandbox profiles")
		}
		if len(profiles) > 0 && profiles[0].kind != kind {
			return nil, errors.New("mixed managed providers require separate router configuration")
		}
		binding := managedsandboxprofile.Binding{
			Region: source.Region, EnvironmentID: source.EnvironmentID,
		}
		if err := binding.Validate(); err != nil {
			return nil, err
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
			return nil, fmt.Errorf("managed sandbox region %q: %w", binding.Region, err)
		}
		sandboxTTL, err := parseProfiledManagedDuration(source.SandboxTTL, 30*time.Second, 24*time.Hour)
		if err != nil {
			return nil, fmt.Errorf("managed sandbox region %q sandboxTtl: %w", binding.Region, err)
		}
		activityTTL, err := parseProfiledManagedDuration(source.ActivityTTL, 3*time.Second, sandboxTTL)
		if err != nil {
			return nil, fmt.Errorf("managed sandbox region %q activityTtl: %w", binding.Region, err)
		}
		provisioning := executorgateway.ManagedSandboxProvisioningSpec{
			Region: binding.Region, EnvironmentID: binding.EnvironmentID,
			SandboxTTL: sandboxTTL, ActivityTTL: activityTTL,
		}
		if err := executorgateway.ValidateManagedSandboxProvisioningSpec(provisioning); err != nil {
			return nil, fmt.Errorf("managed sandbox region %q: %w", binding.Region, err)
		}
		profiles = append(profiles, configuredManagedSandboxGatewayProfile{
			kind: kind, binding: binding, baseURL: baseURL, serverName: serverName, provisioning: provisioning, externalTLS: source.ExternalTLS,
		})
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
		if profile.externalTLS {
			return newServerOnlyHTTPSClient(profile.baseURL)
		}
		client, err := newCoreHTTPClientWithIdentity(
			caFile, clientCertificateFile, clientKeyFile, profile.serverName, clientSPIFFEIdentity,
		)
		if err != nil {
			return nil, fmt.Errorf("configure sandbox-gateway client for region %q: %w", profile.binding.Region, err)
		}
		if profile.kind == executionbackend.KindKubernetes {
			client.Transport.(*http.Transport).ResponseHeaderTimeout = 4 * time.Minute
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

func newServerOnlyHTTPSClient(baseURL string) (*http.Client, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("external sandbox gateway URL must be a canonical HTTPS origin")
	}
	// The HTTPRoute edge presents the certificate for the public hostname. The
	// profile's serverName is reserved for the in-cluster BackendTLSPolicy and
	// must not be used as the cross-cluster SNI name.
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext, ForceAttemptHTTP2: true, MaxIdleConns: 32, MaxIdleConnsPerHost: 32, IdleConnTimeout: time.Minute, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 4 * time.Minute, DisableCompression: true, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, ServerName: parsed.Hostname()}}
	return &http.Client{Transport: transport}, nil
}

func parseProfiledManagedDuration(value string, minimum, maximum time.Duration) (time.Duration, error) {
	parsed, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil || parsed < minimum || parsed > maximum || parsed%time.Second != 0 {
		return 0, fmt.Errorf("must be a whole-second Go duration between %s and %s", minimum, maximum)
	}
	return parsed, nil
}
