package productiondeploy

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/agentserver/agentserver/v2/internal/managedsandboxprofile"
)

const (
	ManagedSandboxProxyCN           = "merlin-hl-1"
	ManagedSandboxProxyI18NBD       = "merlin-useast14a-1"
	ManagedSandboxProxyI18NTT       = "merlin-i18nbd-syd2a"
	managedSandboxProxyI18NTTLegacy = "merlin-maliva-1"
	managedSandboxProxyLegacyURL    = "socks5h://ssh-egress-merlin-i18ntt-maliva-62204-headless.ssh-egress.svc.cluster.local:1080"
	managedSandboxProxyLegacyApp    = "ssh-egress-merlin-i18ntt-maliva-62204"
)

// ManagedSandboxRegionsDocument is the public, stable region catalog exposed
// to Core. Regions contains only profiles installed by this deployment.
type ManagedSandboxRegionsDocument struct {
	DefaultRegion string   `json:"defaultRegion"`
	Regions       []string `json:"regions"`
}

// ManagedSandboxProxyProfileDocument contains the Kubernetes-local SOCKS
// authority for one reviewed Merlin route. No proxy address is inferred from
// its logical name: URL and Pod selector are operator-owned configuration.
type ManagedSandboxProxyProfileDocument struct {
	Name        string            `json:"name"`
	URL         string            `json:"url"`
	Namespace   string            `json:"namespace"`
	PodSelector map[string]string `json:"podSelector"`
	Port        uint16            `json:"port"`
}

type ManagedSandboxGatewayDocument struct {
	Component  string `json:"component"`
	ClusterIP  string `json:"clusterIp"`
	Port       uint16 `json:"port"`
	ServerName string `json:"serverName"`
	Secret     string `json:"secret"`
}

// ManagedSandboxProfileDocument is one immutable provider/network/runtime
// shard. BindingSHA256 changes whenever any gateway, TAE, proxy, environment,
// or direct-egress fact changes.
type ManagedSandboxProfileDocument struct {
	Region                string                        `json:"region"`
	ProfileID             string                        `json:"profileId"`
	BindingSHA256         string                        `json:"bindingSha256"`
	Gateway               ManagedSandboxGatewayDocument `json:"gateway"`
	Environment           ManagedEnvironmentDocument    `json:"environment"`
	TAE                   ManagedTAEDocument            `json:"tae"`
	SandboxExternalEgress []EgressRuleDocument          `json:"sandboxExternalEgress"`
}

type LoadedManagedSandboxProfile struct {
	Document          ManagedSandboxProfileDocument
	Proxy             *ManagedSandboxProxyProfileDocument
	SandboxTTL        time.Duration
	ActivityTTL       time.Duration
	IdleTTL           time.Duration
	OwnerPolicySHA256 string
}

func validateManagedSandboxProfiles(document *ConfigDocument) ([]LoadedManagedSandboxProfile, error) {
	if document == nil {
		return nil, errors.New("managed sandbox production configuration is required")
	}
	if len(document.SandboxProfiles) == 0 {
		if document.Managed.Enabled {
			return nil, errors.New("sandboxProfiles must install at least one profile while managed execution is enabled")
		}
		if document.SandboxRegions.DefaultRegion != "" || len(document.SandboxRegions.Regions) != 0 || len(document.ProxyProfiles) != 0 {
			return nil, errors.New("disabled managed execution must not publish an empty managed sandbox catalog")
		}
		return nil, nil
	}
	if len(document.SandboxProfiles) > len(managedsandboxprofile.Regions()) {
		return nil, errors.New("sandboxProfiles must contain between one and four profiles")
	}
	if document.SandboxRegions.DefaultRegion != managedsandboxprofile.DefaultRegion {
		return nil, fmt.Errorf("sandboxRegions.defaultRegion must be %q because workspace settings initialize to that region", managedsandboxprofile.DefaultRegion)
	}

	proxies, err := validateManagedSandboxProxyProfiles(document.ProxyProfiles)
	if err != nil {
		return nil, err
	}
	regions := make(map[string]struct{}, len(document.SandboxProfiles))
	profiles := make(map[string]struct{}, len(document.SandboxProfiles))
	environments := make(map[string]struct{}, len(document.SandboxProfiles))
	usedProxies := make(map[string]struct{}, len(proxies))
	serviceIPs := configuredServiceIPs(document.Services)
	secrets := configuredSecretNames(document.Secrets)
	loaded := make([]LoadedManagedSandboxProfile, 0, len(document.SandboxProfiles))
	var defaultProfile *ManagedSandboxProfileDocument

	for index := range document.SandboxProfiles {
		profile := document.SandboxProfiles[index]
		path := fmt.Sprintf("sandboxProfiles[%d]", index)
		binding := managedsandboxprofile.Binding{
			Region: profile.Region, ProfileID: profile.ProfileID,
			BindingSHA256: profile.BindingSHA256, EnvironmentID: profile.Environment.EnvironmentID,
		}
		if err := binding.Validate(); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if profile.TAE.Region != profile.Region {
			return nil, fmt.Errorf("%s.tae.region must equal profile region %s", path, profile.Region)
		}
		if _, duplicate := regions[profile.Region]; duplicate {
			return nil, fmt.Errorf("sandbox region %q is repeated", profile.Region)
		}
		if _, duplicate := profiles[profile.ProfileID]; duplicate {
			return nil, fmt.Errorf("sandbox profile %q is repeated", profile.ProfileID)
		}
		if _, duplicate := environments[profile.Environment.EnvironmentID]; duplicate {
			return nil, fmt.Errorf("sandbox environment %q is repeated", profile.Environment.EnvironmentID)
		}
		regions[profile.Region] = struct{}{}
		profiles[profile.ProfileID] = struct{}{}
		environments[profile.Environment.EnvironmentID] = struct{}{}

		if err := validateManagedSandboxGateway(path+".gateway", profile.Region, profile.Gateway); err != nil {
			return nil, err
		}
		address, _ := netip.ParseAddr(profile.Gateway.ClusterIP)
		if owner, duplicate := serviceIPs[address]; duplicate && !(profile.Region == document.SandboxRegions.DefaultRegion && owner == "sandboxGateway") {
			return nil, fmt.Errorf("%s.gateway.clusterIp repeats services.%s.clusterIp", path, owner)
		}
		serviceIPs[address] = profile.Gateway.Component
		if owner, duplicate := secrets[profile.Gateway.Secret]; duplicate && !(profile.Region == document.SandboxRegions.DefaultRegion && owner == "sandboxGateway") {
			return nil, fmt.Errorf("%s.gateway.secret repeats secrets.%s", path, owner)
		}
		secrets[profile.Gateway.Secret] = profile.Gateway.Component

		proxy, authorityErr := validateManagedSandboxTAEAuthority(path+".tae", profile.TAE, proxies)
		if authorityErr != nil {
			return nil, authorityErr
		}
		if proxy != nil {
			usedProxies[proxy.Name] = struct{}{}
		}
		if err := normalizeEgressRules(path+".sandboxExternalEgress", &profile.SandboxExternalEgress); err != nil {
			return nil, err
		}
		if profile.SandboxExternalEgress == nil {
			profile.SandboxExternalEgress = []EgressRuleDocument{}
		}
		if profile.Region == managedsandboxprofile.RegionBOE {
			if len(profile.SandboxExternalEgress) == 0 {
				return nil, fmt.Errorf("%s.sandboxExternalEgress must explicitly allowlist direct BOE destinations", path)
			}
			if err := requireDualStackEgress(path+".sandboxExternalEgress", profile.SandboxExternalEgress); err != nil {
				return nil, err
			}
		} else if len(profile.SandboxExternalEgress) != 0 {
			if err := requireDualStackEgress(path+".sandboxExternalEgress", profile.SandboxExternalEgress); err != nil {
				return nil, err
			}
		}

		synthetic := *document
		synthetic.Managed.Environment = profile.Environment
		synthetic.Managed.TAE = profile.TAE
		synthetic.Services.SandboxGateway = InternalServiceDocument{ClusterIP: profile.Gateway.ClusterIP, Port: profile.Gateway.Port}
		synthetic.Secrets.SandboxGateway = profile.Gateway.Secret
		synthetic.Network.SandboxExternalEgress = append([]EgressRuleDocument{}, profile.SandboxExternalEgress...)
		managedLoaded, managedErr := validateManagedExecutor(synthetic.Managed, synthetic)
		if managedErr != nil {
			return nil, fmt.Errorf("%s: %w", path, managedErr)
		}
		profile.Environment = managedLoaded.Document.Managed.Environment
		profile.TAE = managedLoaded.Document.Managed.TAE
		wantBinding := managedSandboxProfileBindingSHA256(profile, proxy)
		if profile.BindingSHA256 != wantBinding {
			return nil, fmt.Errorf("%s.bindingSha256 must equal the canonical profile lock %s", path, wantBinding)
		}
		document.SandboxProfiles[index] = profile
		loaded = append(loaded, LoadedManagedSandboxProfile{
			Document: profile, Proxy: cloneManagedSandboxProxy(proxy),
			SandboxTTL: managedLoaded.ManagedSandboxTTL, ActivityTTL: managedLoaded.ManagedActivityTTL,
			IdleTTL: managedLoaded.ManagedIdleTTL, OwnerPolicySHA256: managedLoaded.ManagedOwnerPolicySHA256,
		})
		if profile.Region == document.SandboxRegions.DefaultRegion {
			copy := profile
			defaultProfile = &copy
		}
	}
	if defaultProfile == nil {
		return nil, errors.New("sandboxRegions.defaultRegion has no installed profile")
	}
	defaultPolicy := defaultProfile.TAE.Policy
	for _, profile := range document.SandboxProfiles {
		policy := profile.TAE.Policy
		if policy.PublicWebhookRequired != defaultPolicy.PublicWebhookRequired ||
			policy.WebhookMode != defaultPolicy.WebhookMode || policy.WebhookPSM != defaultPolicy.WebhookPSM ||
			policy.WebhookURL != defaultPolicy.WebhookURL || policy.WebhookPath != defaultPolicy.WebhookPath {
			return nil, errors.New("all installed sandbox profiles must use one deployment-wide webhook topology")
		}
	}
	canonicalRegions := installedRegionOrder(regions)
	if !slices.Equal(document.SandboxRegions.Regions, canonicalRegions) {
		return nil, fmt.Errorf("sandboxRegions.regions must list installed regions in canonical order %v", canonicalRegions)
	}
	if len(usedProxies) != len(proxies) {
		return nil, errors.New("proxyProfiles must not contain an unused proxy authority")
	}
	if canonicalDigest(defaultProfile.Environment) != canonicalDigest(document.Managed.Environment) ||
		canonicalDigest(defaultProfile.TAE) != canonicalDigest(document.Managed.TAE) {
		return nil, errors.New("managedExecutor environment and TAE fields must exactly mirror the default sandbox profile")
	}
	if defaultProfile.Gateway.ClusterIP != document.Services.SandboxGateway.ClusterIP ||
		defaultProfile.Gateway.Port != document.Services.SandboxGateway.Port ||
		defaultProfile.Gateway.Secret != document.Secrets.SandboxGateway ||
		canonicalDigest(defaultProfile.SandboxExternalEgress) != canonicalDigest(document.Network.SandboxExternalEgress) {
		return nil, errors.New("legacy sandbox service, secret, and egress fields must exactly mirror the default sandbox profile")
	}
	slices.SortFunc(loaded, func(left, right LoadedManagedSandboxProfile) int {
		return regionOrdinal(left.Document.Region) - regionOrdinal(right.Document.Region)
	})
	slices.SortFunc(document.SandboxProfiles, func(left, right ManagedSandboxProfileDocument) int {
		return regionOrdinal(left.Region) - regionOrdinal(right.Region)
	})
	slices.SortFunc(document.ProxyProfiles, func(left, right ManagedSandboxProxyProfileDocument) int {
		return strings.Compare(left.Name, right.Name)
	})
	return loaded, nil
}

func validateManagedSandboxProxyProfiles(source []ManagedSandboxProxyProfileDocument) (map[string]ManagedSandboxProxyProfileDocument, error) {
	if len(source) > 3 {
		return nil, errors.New("proxyProfiles may contain at most three reviewed Merlin routes")
	}
	allowed := map[string]struct{}{
		ManagedSandboxProxyCN: {}, ManagedSandboxProxyI18NBD: {}, ManagedSandboxProxyI18NTT: {},
		// Kept read-compatible so a locked bootstrap can be migrated through
		// RetargetManagedSandboxProxyDocument. New templates and upgrades use
		// ManagedSandboxProxyI18NTT exclusively.
		managedSandboxProxyI18NTTLegacy: {},
	}
	result := make(map[string]ManagedSandboxProxyProfileDocument, len(source))
	for index, proxy := range source {
		path := fmt.Sprintf("proxyProfiles[%d]", index)
		if _, ok := allowed[proxy.Name]; !ok {
			return nil, fmt.Errorf("%s.name is not a reviewed managed sandbox proxy profile", path)
		}
		if _, duplicate := result[proxy.Name]; duplicate {
			return nil, fmt.Errorf("proxy profile %q is repeated", proxy.Name)
		}
		parsed, err := url.Parse(proxy.URL)
		if err != nil || parsed.Scheme != "socks5h" || parsed.Hostname() == "" || parsed.User != nil ||
			parsed.Path != "" || parsed.RawPath != "" || parsed.RawQuery != "" || parsed.Fragment != "" ||
			parsed.Opaque != "" || parsed.ForceQuery {
			return nil, fmt.Errorf("%s.url must be a canonical credential-free socks5h origin", path)
		}
		port, err := strconv.ParseUint(parsed.Port(), 10, 16)
		if err != nil || port == 0 || uint16(port) != proxy.Port {
			return nil, fmt.Errorf("%s.port must equal the explicit URL port", path)
		}
		if !validDNSName(strings.ToLower(parsed.Hostname())) || parsed.Hostname() != strings.ToLower(parsed.Hostname()) {
			return nil, fmt.Errorf("%s.url must use a canonical lowercase DNS host", path)
		}
		if !dnsLabelPattern.MatchString(proxy.Namespace) {
			return nil, fmt.Errorf("%s.namespace must be a canonical Kubernetes namespace", path)
		}
		if err := validateManagedSandboxPodSelector(path+".podSelector", proxy.PodSelector); err != nil {
			return nil, err
		}
		if proxy.Name == managedSandboxProxyI18NTTLegacy &&
			(proxy.URL != managedSandboxProxyLegacyURL || proxy.Namespace != ProductionTAEProxyNamespace ||
				proxy.Port != ProductionTAEProxyPort || len(proxy.PodSelector) != 1 ||
				proxy.PodSelector["app"] != managedSandboxProxyLegacyApp) {
			return nil, fmt.Errorf("%s legacy i18n-tt proxy must match the retired locked authority exactly", path)
		}
		proxy.PodSelector = cloneStringMap(proxy.PodSelector)
		result[proxy.Name] = proxy
	}
	return result, nil
}

func validateManagedSandboxGateway(path, region string, gateway ManagedSandboxGatewayDocument) error {
	if !dnsLabelPattern.MatchString(gateway.Component) ||
		(gateway.Component != sandboxComponent && !strings.HasPrefix(gateway.Component, "sandbox-gateway-")) {
		return fmt.Errorf("%s.component must be a canonical sandbox-gateway workload name", path)
	}
	address, err := netip.ParseAddr(gateway.ClusterIP)
	if err != nil || !address.Is4() || address.IsUnspecified() || address.IsLoopback() || address.IsMulticast() || address.String() != gateway.ClusterIP {
		return fmt.Errorf("%s.clusterIp must be a usable canonical IPv4 address", path)
	}
	if gateway.Port != HarnessControlPort {
		return fmt.Errorf("%s.port must be exactly %d", path, HarnessControlPort)
	}
	if !validDNSName(gateway.ServerName) || strings.Contains(gateway.ServerName, "..") {
		return fmt.Errorf("%s.serverName must be a canonical lowercase TLS DNS name", path)
	}
	if !dnsLabelPattern.MatchString(gateway.Secret) {
		return fmt.Errorf("%s.secret must be a canonical Kubernetes Secret name", path)
	}
	want, ok := map[string]ManagedSandboxGatewayDocument{
		managedsandboxprofile.RegionCN: {
			Component: "sandbox-gateway-cn", ServerName: "sandbox-gateway-cn.agentserver.internal",
			Secret: "agentserver-sandbox-cn-secrets",
		},
		managedsandboxprofile.RegionBOE: {
			Component: "sandbox-gateway-boe", ServerName: "sandbox-gateway-boe.agentserver.internal",
			Secret: "agentserver-sandbox-boe-secrets",
		},
		managedsandboxprofile.RegionI18NBD: {
			Component: "sandbox-gateway-i18n-bd", ServerName: "sandbox-gateway-i18n-bd.agentserver.internal",
			Secret: "agentserver-sandbox-i18n-bd-secrets",
		},
		managedsandboxprofile.RegionI18NTT: {
			Component: sandboxComponent, ServerName: SandboxInternalHost, Secret: ProductionSandboxSecret,
		},
	}[region]
	if !ok {
		return fmt.Errorf("%s region is unsupported", path)
	}
	if gateway.Component != want.Component || gateway.ServerName != want.ServerName || gateway.Secret != want.Secret {
		return fmt.Errorf("%s component, serverName, and secret must match the deployment-owned %s gateway authority", path, region)
	}
	return nil
}

func validateManagedSandboxTAEAuthority(path string, tae ManagedTAEDocument, proxies map[string]ManagedSandboxProxyProfileDocument) (*ManagedSandboxProxyProfileDocument, error) {
	controlPlaneURL, dataPlaneSuffix, err := managedSandboxTAEAuthority(tae.Region)
	if err != nil {
		return nil, fmt.Errorf("%s.region: %w", path, err)
	}
	if tae.ControlPlaneURL != controlPlaneURL || tae.DataPlaneSuffix != dataPlaneSuffix {
		return nil, fmt.Errorf("%s controlPlaneUrl/dataPlaneSuffix must equal the selected official SDK authority", path)
	}
	wantSite := map[string]string{
		managedsandboxprofile.RegionCN: "cn", managedsandboxprofile.RegionBOE: "cn",
		managedsandboxprofile.RegionI18NBD: "i18n-bd",
		managedsandboxprofile.RegionI18NTT: "i18n-tt",
	}[tae.Region]
	if tae.ByteCloudSite != wantSite {
		return nil, fmt.Errorf("%s.bytecloudSite must be %q", path, wantSite)
	}
	if err := validateHTTPSOrigin(path+".bytecloudJwtEndpoint", tae.ByteCloudJWTEndpoint); err != nil {
		return nil, err
	}
	wantProxy := map[string]string{
		managedsandboxprofile.RegionCN:     ManagedSandboxProxyCN,
		managedsandboxprofile.RegionI18NBD: ManagedSandboxProxyI18NBD,
		managedsandboxprofile.RegionI18NTT: ManagedSandboxProxyI18NTT,
	}[tae.Region]
	legacyI18NTT := tae.Region == managedsandboxprofile.RegionI18NTT && tae.ProxyProfile == managedSandboxProxyI18NTTLegacy
	if tae.ProxyProfile != wantProxy && !legacyI18NTT {
		if wantProxy == "" {
			return nil, fmt.Errorf("%s.proxyProfile must be empty for direct BOE routing", path)
		}
		return nil, fmt.Errorf("%s.proxyProfile must be %q", path, wantProxy)
	}
	if wantProxy == "" {
		return nil, nil
	}
	selectedProxy := wantProxy
	if legacyI18NTT {
		selectedProxy = managedSandboxProxyI18NTTLegacy
	}
	proxy, ok := proxies[selectedProxy]
	if !ok {
		return nil, fmt.Errorf("%s.proxyProfile %q is not configured", path, selectedProxy)
	}
	return &proxy, nil
}

func managedSandboxProfileBindingSHA256(profile ManagedSandboxProfileDocument, proxy *ManagedSandboxProxyProfileDocument) string {
	profile.BindingSHA256 = ""
	return canonicalDigest(struct {
		Version int                                 `json:"version"`
		Profile ManagedSandboxProfileDocument       `json:"profile"`
		Proxy   *ManagedSandboxProxyProfileDocument `json:"proxy,omitempty"`
	}{Version: 1, Profile: profile, Proxy: proxy})
}

func refreshDefaultManagedSandboxProfile(document *ConfigDocument) error {
	if document == nil || len(document.SandboxProfiles) == 0 {
		return errors.New("default managed sandbox profile is unavailable")
	}
	index := -1
	for candidate := range document.SandboxProfiles {
		if document.SandboxProfiles[candidate].Region == document.SandboxRegions.DefaultRegion {
			index = candidate
			break
		}
	}
	if index < 0 {
		return errors.New("default managed sandbox profile is unavailable")
	}
	profile := document.SandboxProfiles[index]
	profile.Environment = document.Managed.Environment
	profile.TAE = document.Managed.TAE
	profile.SandboxExternalEgress = append([]EgressRuleDocument{}, document.Network.SandboxExternalEgress...)
	identityDigest := canonicalDigest(struct {
		Region      string                     `json:"region"`
		Environment ManagedEnvironmentDocument `json:"environment"`
		TAE         ManagedTAEDocument         `json:"tae"`
	}{Region: profile.Region, Environment: profile.Environment, TAE: profile.TAE})
	profile.ProfileID = "tae-" + profile.Region + "-" + identityDigest[:16]
	var proxy *ManagedSandboxProxyProfileDocument
	for proxyIndex := range document.ProxyProfiles {
		if document.ProxyProfiles[proxyIndex].Name == profile.TAE.ProxyProfile {
			copy := document.ProxyProfiles[proxyIndex]
			proxy = &copy
			break
		}
	}
	if profile.TAE.ProxyProfile != "" && proxy == nil {
		return fmt.Errorf("default managed sandbox proxy profile %q is unavailable", profile.TAE.ProxyProfile)
	}
	profile.BindingSHA256 = managedSandboxProfileBindingSHA256(profile, proxy)
	document.SandboxProfiles[index] = profile
	return nil
}

// refreshManagedSandboxProfileFromManaged rewrites one immutable profile from
// a region-scoped managed executor document. It is used by the all-profile
// bootstrap/activation edges so no installed region can retain stale policy,
// network-evidence, runtime, or pack locks while the global stage changes.
func refreshManagedSandboxProfileFromManaged(document *ConfigDocument, index int, managed ManagedExecutorDocument) error {
	if document == nil || index < 0 || index >= len(document.SandboxProfiles) {
		return errors.New("managed sandbox profile is unavailable")
	}
	profile := document.SandboxProfiles[index]
	if managed.TAE.Region != profile.Region {
		return fmt.Errorf("managed sandbox profile %q region does not match its TAE authority", profile.ProfileID)
	}
	profile.Environment = managed.Environment
	profile.TAE = managed.TAE
	identityDigest := canonicalDigest(struct {
		Region      string                     `json:"region"`
		Environment ManagedEnvironmentDocument `json:"environment"`
		TAE         ManagedTAEDocument         `json:"tae"`
	}{Region: profile.Region, Environment: profile.Environment, TAE: profile.TAE})
	profile.ProfileID = "tae-" + profile.Region + "-" + identityDigest[:16]
	var proxy *ManagedSandboxProxyProfileDocument
	for proxyIndex := range document.ProxyProfiles {
		if document.ProxyProfiles[proxyIndex].Name == profile.TAE.ProxyProfile {
			copy := document.ProxyProfiles[proxyIndex]
			proxy = &copy
			break
		}
	}
	if profile.TAE.ProxyProfile != "" && proxy == nil {
		return fmt.Errorf("managed sandbox proxy profile %q is unavailable", profile.TAE.ProxyProfile)
	}
	profile.BindingSHA256 = managedSandboxProfileBindingSHA256(profile, proxy)
	document.SandboxProfiles[index] = profile
	if profile.Region == document.SandboxRegions.DefaultRegion {
		document.Managed = managed
		document.Services.SandboxGateway = InternalServiceDocument{
			ClusterIP: profile.Gateway.ClusterIP, Port: profile.Gateway.Port,
		}
		document.Secrets.SandboxGateway = profile.Gateway.Secret
		document.Network.SandboxExternalEgress = append([]EgressRuleDocument{}, profile.SandboxExternalEgress...)
	}
	return nil
}

func upgradeLegacyConfig(document ConfigDocument) (ConfigDocument, error) {
	if document.Version != LegacyVersion {
		return ConfigDocument{}, fmt.Errorf("legacy version must be %d", LegacyVersion)
	}
	document.Version = CurrentVersion
	if !document.Managed.Enabled {
		document.SandboxRegions = ManagedSandboxRegionsDocument{}
		document.SandboxProfiles = nil
		document.ProxyProfiles = nil
		return document, nil
	}
	if document.Managed.TAE.Region != ProductionRegion {
		return ConfigDocument{}, fmt.Errorf("legacy managedExecutor.tae.region must be %q", ProductionRegion)
	}
	controlPlaneURL, dataPlaneSuffix, err := managedSandboxTAEAuthority(managedsandboxprofile.RegionI18NTT)
	if err != nil {
		return ConfigDocument{}, err
	}
	document.Managed.TAE.Region = managedsandboxprofile.RegionI18NTT
	document.Managed.TAE.ControlPlaneURL = controlPlaneURL
	document.Managed.TAE.DataPlaneSuffix = dataPlaneSuffix
	document.Managed.TAE.ByteCloudSite = managedsandboxprofile.RegionI18NTT
	document.Managed.TAE.ByteCloudJWTEndpoint = ProductionByteCloudJWTEndpoint
	document.Managed.TAE.ProxyProfile = ManagedSandboxProxyI18NTT
	document.Managed.TAE.Policy.BindingSHA256 = managedTAEPolicyBinding(document.Managed.TAE).DigestHex()
	document.ProxyProfiles = []ManagedSandboxProxyProfileDocument{{
		Name: ManagedSandboxProxyI18NTT, URL: ProductionTAEProxyURL,
		Namespace: ProductionTAEProxyNamespace, PodSelector: map[string]string{"app": ProductionTAEProxyPodApp}, Port: ProductionTAEProxyPort,
	}}
	document.SandboxRegions = ManagedSandboxRegionsDocument{
		DefaultRegion: managedsandboxprofile.RegionI18NTT, Regions: []string{managedsandboxprofile.RegionI18NTT},
	}
	if !managedPolicyBootstrap(document.Managed) {
		document.Managed.TAE.NetworkEvidence.BindingSHA256 = managedTAENetworkEvidenceDigest(document)
		document.Managed.Environment.RuntimeProfileSHA256 = managedRuntimeProfileDigest(document, document.Managed)
		document.Managed.Environment.PackSetSHA256 = managedPackSetDigest(document.Managed)
	}
	profile := ManagedSandboxProfileDocument{
		Region:    managedsandboxprofile.RegionI18NTT,
		ProfileID: "tae-i18n-tt-" + document.Managed.TAE.NetworkEvidence.BindingSHA256,
		Gateway: ManagedSandboxGatewayDocument{
			Component: sandboxComponent, ClusterIP: document.Services.SandboxGateway.ClusterIP,
			Port: document.Services.SandboxGateway.Port, ServerName: SandboxInternalHost, Secret: document.Secrets.SandboxGateway,
		},
		Environment: document.Managed.Environment, TAE: document.Managed.TAE,
		SandboxExternalEgress: append([]EgressRuleDocument{}, document.Network.SandboxExternalEgress...),
	}
	if managedPolicyBootstrap(document.Managed) {
		profile.ProfileID = "tae-i18n-tt-policy-bootstrap"
	}
	proxy := document.ProxyProfiles[0]
	profile.BindingSHA256 = managedSandboxProfileBindingSHA256(profile, &proxy)
	document.SandboxProfiles = []ManagedSandboxProfileDocument{profile}
	return document, nil
}

func validateManagedSandboxPodSelector(path string, selector map[string]string) error {
	if len(selector) < 1 || len(selector) > 8 {
		return fmt.Errorf("%s must contain between one and eight exact pod labels", path)
	}
	for key, value := range selector {
		if !validLabelKey(key) || !labelValuePattern.MatchString(value) {
			return fmt.Errorf("%s contains invalid Kubernetes label %q=%q", path, key, value)
		}
	}
	return nil
}

func configuredServiceIPs(services ServicesDocument) map[netip.Addr]string {
	result := make(map[netip.Addr]string)
	for name, value := range map[string]string{
		"core": services.Core.ClusterIP, "platformGateway": services.PlatformGateway.ClusterIP,
		"browserGateway": services.BrowserGateway.ClusterIP, "executorGateway": services.ExecutorGateway.ClusterIP,
		"llmproxy": services.LLMProxy.ClusterIP, "hydra": services.Hydra.ClusterIP,
		"sandboxGateway": services.SandboxGateway.ClusterIP, "egressAuthorizer": services.EgressAuthorizer.ClusterIP,
	} {
		address, err := netip.ParseAddr(value)
		if err == nil {
			result[address] = name
		}
	}
	return result
}

func configuredSecretNames(secrets SecretsDocument) map[string]string {
	return map[string]string{
		secrets.Core: "core", secrets.PlatformGateway: "platformGateway", secrets.BrowserGateway: "browserGateway",
		secrets.ExecutorGateway: "executorGateway", secrets.HarnessPool: "harnessPool", secrets.HarnessWorker: "harnessWorker",
		secrets.LLMProxy: "llmproxy", secrets.ObjectStore: "objectStore", secrets.Hydra: "hydra",
		secrets.SandboxGateway: "sandboxGateway", secrets.EgressAuthorizer: "egressAuthorizer",
	}
}

func installedRegionOrder(installed map[string]struct{}) []string {
	result := make([]string, 0, len(installed))
	for _, region := range managedsandboxprofile.Regions() {
		if _, ok := installed[region]; ok {
			result = append(result, region)
		}
	}
	return result
}

func regionOrdinal(region string) int {
	return slices.Index(managedsandboxprofile.Regions(), region)
}

func cloneManagedSandboxProxy(source *ManagedSandboxProxyProfileDocument) *ManagedSandboxProxyProfileDocument {
	if source == nil {
		return nil
	}
	copy := *source
	copy.PodSelector = cloneStringMap(source.PodSelector)
	return &copy
}

func cloneStringMap(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func managedSandboxGatewayOrigin(gateway ManagedSandboxGatewayDocument) string {
	return "https://" + net.JoinHostPort(gateway.ServerName, strconv.Itoa(int(gateway.Port)))
}

// managedSandboxTAEAuthority mirrors the reviewed official TAE SDK mapping.
// The provider independently resolves and compares these exact values during
// startup, so a deployment/provider drift fails closed before serving.
func managedSandboxTAEAuthority(region string) (string, string, error) {
	suffix := map[string]string{
		managedsandboxprofile.RegionCN:     "cn.ai-sandbox.bytedance.net",
		managedsandboxprofile.RegionBOE:    "cn-north.ai-sandbox-boe.byted.org",
		managedsandboxprofile.RegionI18NBD: "i18nbd.ai-sandbox.byteintl.net",
		managedsandboxprofile.RegionI18NTT: "sg.ai-sandbox-i18n.byted.org",
	}[region]
	if suffix == "" {
		return "", "", errors.New("TAE SDK logical region is unsupported")
	}
	return "https://controlplane." + suffix, suffix, nil
}

// productionTAEManagedSandboxRepository selects the registry local to the
// reviewed TAE control plane. The release pipeline publishes one verified OCI
// archive to CN and SG independently; this function never falls back
// between registries at runtime. i18n-bd remains disabled in production and
// retains the existing SG repository until that region is re-qualified.
func productionTAEManagedSandboxRepository(region string) (string, error) {
	repository := map[string]string{
		managedsandboxprofile.RegionCN:     ProductionTAEManagedSandboxCNImage,
		managedsandboxprofile.RegionBOE:    ProductionTAEManagedSandboxCNImage,
		managedsandboxprofile.RegionI18NBD: ProductionTAEManagedSandboxSGImage,
		managedsandboxprofile.RegionI18NTT: ProductionTAEManagedSandboxSGImage,
	}[region]
	if repository == "" {
		return "", errors.New("TAE managed sandbox image region is unsupported")
	}
	return repository, nil
}
