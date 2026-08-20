package productiondeploy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"path"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/agentserver/agentserver/v2/internal/bkectlpolicy"
	"github.com/agentserver/agentserver/v2/internal/larkegresspolicy"
	"github.com/agentserver/agentserver/v2/internal/managedsandboxprofile"
	"github.com/agentserver/agentserver/v2/internal/productionimage"
	"github.com/agentserver/agentserver/v2/internal/stockruntime"
	"github.com/agentserver/agentserver/v2/internal/taeimage"
	"github.com/agentserver/agentserver/v2/internal/taenetworkreport"
	"github.com/agentserver/agentserver/v2/internal/taepolicy"
)

const (
	managedBaseInstructionsPath    = "/opt/agentserver/packs/managed-cli-readonly/SKILL.md"
	managedSandboxRootPath         = "/workspace"
	managedNetworkEvidenceVersion  = 5
	ProductionTAEPSM               = "bytedance.sandbox.agentserver"
	ProductionByteCloudJWTEndpoint = "https://cloud-i18n-sg.bytedance.net"
	ProductionTAEControlPlaneHost  = "controlplane.sg.ai-sandbox-i18n.byted.org"
	ProductionTAEDataPlaneSuffix   = "sg.ai-sandbox-i18n.byted.org"
	ProductionTAEProxyURL          = "socks5h://ssh-egress-merlin-i18nbd-syd2a-83092-headless.ssh-egress.svc.cluster.local:1080"
	ProductionTAEProxyNamespace    = "ssh-egress"
	ProductionTAEProxyPodApp       = "ssh-egress-merlin-i18nbd-syd2a-83092"
	ProductionTAEProxyPort         = uint16(1080)
	ManagedExecutorStageDisabled   = "disabled"
	ManagedExecutorStageBootstrap  = "policy-bootstrap"
	ManagedExecutorStageActive     = "active"
)

type ManagedExecutorDocument struct {
	Enabled                bool                       `json:"enabled"`
	Stage                  string                     `json:"stage"`
	WorkspaceAllowlist     []string                   `json:"workspaceAllowlist"`
	BaseInstructionsSHA256 string                     `json:"baseInstructionsSha256"`
	Environment            ManagedEnvironmentDocument `json:"environment"`
	TAE                    ManagedTAEDocument         `json:"tae"`
	Lark                   ManagedLarkDocument        `json:"lark"`
	Bkectl                 ManagedBkectlDocument      `json:"bkectl"`
}

func managedExecutionActive(managed ManagedExecutorDocument) bool {
	return managed.Enabled && managed.Stage == ManagedExecutorStageActive
}

func managedPolicyBootstrap(managed ManagedExecutorDocument) bool {
	return managed.Enabled && managed.Stage == ManagedExecutorStageBootstrap
}

func managedEgressAuthorizerEnabled(managed ManagedExecutorDocument) bool {
	return managed.Enabled && managed.TAE.Policy.PublicWebhookRequired &&
		(managed.Stage == ManagedExecutorStageBootstrap || managed.Stage == ManagedExecutorStageActive)
}

type ManagedEnvironmentDocument struct {
	EnvironmentID string                              `json:"environmentId"`
	Root          ManagedEnvironmentRootDocument      `json:"root"`
	Compatibility ManagedCompatibilityRuntimeDocument `json:"compatibilityRuntime"`
	SandboxTTL    string                              `json:"sandboxTtl"`
	ActivityTTL   string                              `json:"activityTtl"`
	IdleTTL       string                              `json:"idleTtl"`
}

type ManagedEnvironmentRootDocument struct {
	Path        string `json:"path"`
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
	DefaultCWD  string `json:"defaultCwd"`
}

type ManagedCompatibilityRuntimeDocument struct {
	CodexRelease string `json:"codexRelease"`
	CodexCommit  string `json:"codexCommit"`
	CodexSHA256  string `json:"codexSha256"`
}

type ManagedTAEDocument struct {
	Region               string                            `json:"region"`
	PSM                  string                            `json:"psm"`
	SandboxID            string                            `json:"sandboxId"`
	RevisionID           string                            `json:"sandboxRevisionId"`
	ControlPlaneURL      string                            `json:"controlPlaneUrl"`
	DataPlaneSuffix      string                            `json:"dataPlaneSuffix"`
	ByteCloudSite        string                            `json:"bytecloudSite"`
	ByteCloudJWTEndpoint string                            `json:"bytecloudJwtEndpoint"`
	ProxyProfile         string                            `json:"proxyProfile"`
	Policy               ManagedTAEPolicyDocument          `json:"policy"`
	NetworkEvidence      ManagedTAENetworkEvidenceDocument `json:"networkEvidence"`
}

// ManagedTAENetworkEvidenceDocument records the reviewed network report used
// to activate a regional managed sandbox configuration.
type ManagedTAENetworkEvidenceDocument struct {
	Version      int    `json:"version"`
	ReportSHA256 string `json:"reportSha256"`
	EvidenceRef  string `json:"evidenceRef"`
}

// PreparePolicyBootstrapDocument derives the only pre-approval production
// stage from an otherwise valid SG document. It deliberately removes every
// field that could claim an approved policy, verified TAE network path, or
// active managed runtime. A webhook profile retains only its deny-only policy
// endpoint; a direct profile exposes no webhook authority. The ordinary
// platform remains available.
func PreparePolicyBootstrapDocument(document ConfigDocument) (ConfigDocument, error) {
	loaded, err := ValidateConfig(document)
	if err != nil {
		return ConfigDocument{}, fmt.Errorf("validate policy bootstrap source: %w", err)
	}
	return preparePolicyBootstrapDocument(loaded.Document)
}

func preparePolicyBootstrapDocument(document ConfigDocument) (ConfigDocument, error) {
	if !document.Managed.Enabled {
		return ConfigDocument{}, errors.New("managed executor must be enabled before preparing policy bootstrap")
	}
	document.Managed.Stage = ManagedExecutorStageBootstrap
	for index := range document.SandboxProfiles {
		profile := document.SandboxProfiles[index]
		managed := document.Managed
		managed.Stage = ManagedExecutorStageBootstrap
		managed.Environment = profile.Environment
		managed.TAE = profile.TAE
		managed.TAE.Policy.Published = false
		managed.TAE.Policy.Approved = false
		managed.TAE.Policy.EvidenceRef = ""
		managed.TAE.NetworkEvidence = ManagedTAENetworkEvidenceDocument{}
		if err := refreshManagedSandboxProfileFromManaged(&document, index, managed); err != nil {
			return ConfigDocument{}, err
		}
	}
	prepared, err := ValidateConfig(document)
	if err != nil {
		return ConfigDocument{}, fmt.Errorf("validate prepared policy bootstrap: %w", err)
	}
	return prepared.Document, nil
}

func PreparePolicyBootstrapJSON(raw []byte) ([]byte, error) {
	document, err := decodeConfigDocument(raw)
	if err != nil {
		return nil, err
	}
	document, err = PreparePolicyBootstrapDocument(document)
	if err != nil {
		return nil, err
	}
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode policy bootstrap config: %w", err)
	}
	encoded = append(encoded, '\n')
	if _, err := ParseConfig(encoded); err != nil {
		return nil, fmt.Errorf("verify policy bootstrap config: %w", err)
	}
	return encoded, nil
}

func PreparePolicyBootstrapFile(input, output string) error {
	raw, err := readProductionConfigFile(input)
	if err != nil {
		return err
	}
	prepared, err := PreparePolicyBootstrapJSON(raw)
	if err != nil {
		return err
	}
	return WriteReleaseConfig(prepared, output)
}

// PinManagedTerminalRevisionDocument changes only the management-plane
// Terminal revision selected by a fail-closed policy-bootstrap document. The
// caller must repeat the already-pinned Sandbox ID so a typo cannot silently
// retarget production to another Sandbox. Active documents are immutable:
// their revision is already bound into SG network evidence and runtime locks.
func PinManagedTerminalRevisionDocument(
	document ConfigDocument, sandboxID, revisionID string,
) (ConfigDocument, error) {
	loaded, err := ValidateConfig(document)
	if err != nil {
		return ConfigDocument{}, fmt.Errorf("validate Terminal revision source: %w", err)
	}
	document = loaded.Document
	if !managedPolicyBootstrap(document.Managed) {
		return ConfigDocument{}, errors.New("Terminal revision can be pinned only in the policy-bootstrap stage")
	}
	if !dnsLabelPattern.MatchString(sandboxID) || sandboxID != document.Managed.TAE.SandboxID {
		return ConfigDocument{}, errors.New("Terminal Sandbox ID does not match the policy-bootstrap configuration")
	}
	if !dnsLabelPattern.MatchString(revisionID) || containsReleaseSentinel(revisionID) ||
		strings.Contains(strings.ToUpper(revisionID), "PENDING") {
		return ConfigDocument{}, errors.New("Terminal Sandbox revision ID must be a concrete canonical lowercase TAE identity")
	}
	document.Managed.TAE.RevisionID = revisionID
	if err := refreshDefaultManagedSandboxProfile(&document); err != nil {
		return ConfigDocument{}, err
	}
	pinned, err := ValidateConfig(document)
	if err != nil {
		return ConfigDocument{}, fmt.Errorf("validate pinned Terminal revision: %w", err)
	}
	return pinned.Document, nil
}

func PinManagedTerminalRevisionJSON(raw []byte, sandboxID, revisionID string) ([]byte, error) {
	document, err := decodeConfigDocument(raw)
	if err != nil {
		return nil, err
	}
	document, err = PinManagedTerminalRevisionDocument(document, sandboxID, revisionID)
	if err != nil {
		return nil, err
	}
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode pinned Terminal revision config: %w", err)
	}
	encoded = append(encoded, '\n')
	if _, err := ParseConfig(encoded); err != nil {
		return nil, fmt.Errorf("verify pinned Terminal revision config: %w", err)
	}
	return encoded, nil
}

func PinManagedTerminalRevisionFile(input, output, sandboxID, revisionID string) error {
	raw, err := readProductionConfigFile(input)
	if err != nil {
		return err
	}
	pinned, err := PinManagedTerminalRevisionJSON(raw, sandboxID, revisionID)
	if err != nil {
		return err
	}
	return WriteReleaseConfig(pinned, output)
}

// RetargetManagedTerminalDocument atomically updates the published Terminal
// that describe one published Terminal runtime: the TAE Sandbox, its revision,
// the deployment-owned environment metadata, and the digest-pinned managed
// sandbox artifact. The environment identity may be retained because bootstrap
// updates deployment-owned TAE profile metadata in place. This edge is deliberately
// restricted to policy-bootstrap, where no active runtime, policy approval, or
// network evidence exists. Callers must repeat the current Sandbox ID so a
// stale production document or typo cannot silently select another service.
func RetargetManagedTerminalDocument(
	document ConfigDocument, expectedSandboxID, sandboxID, revisionID, environmentID, managedSandboxImage string,
) (ConfigDocument, error) {
	loaded, err := ValidateConfig(document)
	if err != nil {
		return ConfigDocument{}, fmt.Errorf("validate Terminal retarget source: %w", err)
	}
	document = loaded.Document
	if !managedPolicyBootstrap(document.Managed) {
		return ConfigDocument{}, errors.New("Terminal Sandbox can be retargeted only in the policy-bootstrap stage")
	}
	if !dnsLabelPattern.MatchString(expectedSandboxID) || expectedSandboxID != document.Managed.TAE.SandboxID {
		return ConfigDocument{}, errors.New("current Terminal Sandbox ID does not match the policy-bootstrap configuration")
	}
	if !dnsLabelPattern.MatchString(sandboxID) || containsReleaseSentinel(sandboxID) {
		return ConfigDocument{}, errors.New("new Terminal Sandbox ID must be a concrete canonical lowercase TAE identity")
	}
	if !dnsLabelPattern.MatchString(revisionID) || containsReleaseSentinel(revisionID) ||
		strings.Contains(strings.ToUpper(revisionID), "PENDING") {
		return ConfigDocument{}, errors.New("Terminal Sandbox revision ID must be a concrete canonical lowercase TAE identity")
	}
	if !validUUID(environmentID) {
		return ConfigDocument{}, errors.New("managed environment ID must be a non-zero canonical lowercase UUID")
	}
	if !imagePattern.MatchString(managedSandboxImage) ||
		!strings.HasPrefix(managedSandboxImage, ProductionManagedSandboxImage+"@sha256:") {
		return ConfigDocument{}, errors.New("managed Terminal artifact must be a digest-pinned SG managed sandbox mirror image")
	}
	document.Managed.TAE.SandboxID = sandboxID
	document.Managed.TAE.RevisionID = revisionID
	document.Managed.Environment.EnvironmentID = environmentID
	document.Images.ManagedSandbox = managedSandboxImage
	if err := refreshDefaultManagedSandboxProfile(&document); err != nil {
		return ConfigDocument{}, err
	}
	retargeted, err := ValidateConfig(document)
	if err != nil {
		return ConfigDocument{}, fmt.Errorf("validate retargeted Terminal Sandbox: %w", err)
	}
	return retargeted.Document, nil
}

func RetargetManagedTerminalJSON(
	raw []byte, expectedSandboxID, sandboxID, revisionID, environmentID, managedSandboxImage string,
) ([]byte, error) {
	document, err := decodeConfigDocument(raw)
	if err != nil {
		return nil, err
	}
	document, err = RetargetManagedTerminalDocument(
		document, expectedSandboxID, sandboxID, revisionID, environmentID, managedSandboxImage,
	)
	if err != nil {
		return nil, err
	}
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode retargeted Terminal Sandbox config: %w", err)
	}
	encoded = append(encoded, '\n')
	if _, err := ParseConfig(encoded); err != nil {
		return nil, fmt.Errorf("verify retargeted Terminal Sandbox config: %w", err)
	}
	return encoded, nil
}

func RetargetManagedTerminalFile(
	input, output, expectedSandboxID, sandboxID, revisionID, environmentID, managedSandboxImage string,
) error {
	raw, err := readProductionConfigFile(input)
	if err != nil {
		return err
	}
	retargeted, err := RetargetManagedTerminalJSON(
		raw, expectedSandboxID, sandboxID, revisionID, environmentID, managedSandboxImage,
	)
	if err != nil {
		return err
	}
	return WriteReleaseConfig(retargeted, output)
}

// RetargetDirectManagedTerminalDocument performs the same atomic Terminal
// identity rotation as RetargetManagedTerminalDocument and also binds the
// target to TAE's system-default *.feishu.cn allowlist. The resulting
// fail-closed bootstrap contains no webhook authority. It is the promotion
// source for process_env workspaces; a future webhook_swap profile must use a
// different webhook-enabled Sandbox instead of weakening this binding.
func RetargetDirectManagedTerminalDocument(
	document ConfigDocument, expectedSandboxID, sandboxID, revisionID, environmentID, managedSandboxImage string,
) (ConfigDocument, error) {
	retargeted, err := RetargetManagedTerminalDocument(
		document, expectedSandboxID, sandboxID, revisionID, environmentID, managedSandboxImage,
	)
	if err != nil {
		return ConfigDocument{}, err
	}
	policy := &retargeted.Managed.TAE.Policy
	policy.PublicHost = taepolicy.SystemDefaultHost
	policy.PublicAccess = taepolicy.SystemDefaultAccess
	policy.PublicWebhookRequired = false
	policy.WebhookMode = ""
	policy.WebhookPSM = ""
	policy.WebhookURL = ""
	policy.WebhookPath = ""
	if err := refreshDefaultManagedSandboxProfile(&retargeted); err != nil {
		return ConfigDocument{}, err
	}
	validated, err := ValidateConfig(retargeted)
	if err != nil {
		return ConfigDocument{}, fmt.Errorf("validate direct Terminal Sandbox retarget: %w", err)
	}
	return validated.Document, nil
}

func RetargetDirectManagedTerminalJSON(
	raw []byte, expectedSandboxID, sandboxID, revisionID, environmentID, managedSandboxImage string,
) ([]byte, error) {
	document, err := decodeConfigDocument(raw)
	if err != nil {
		return nil, err
	}
	document, err = RetargetDirectManagedTerminalDocument(
		document, expectedSandboxID, sandboxID, revisionID, environmentID, managedSandboxImage,
	)
	if err != nil {
		return nil, err
	}
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode direct Terminal Sandbox config: %w", err)
	}
	encoded = append(encoded, '\n')
	if _, err := ParseConfig(encoded); err != nil {
		return nil, fmt.Errorf("verify direct Terminal Sandbox config: %w", err)
	}
	return encoded, nil
}

func RetargetDirectManagedTerminalFile(
	input, output, expectedSandboxID, sandboxID, revisionID, environmentID, managedSandboxImage string,
) error {
	raw, err := readProductionConfigFile(input)
	if err != nil {
		return err
	}
	retargeted, err := RetargetDirectManagedTerminalJSON(
		raw, expectedSandboxID, sandboxID, revisionID, environmentID, managedSandboxImage,
	)
	if err != nil {
		return err
	}
	return WriteReleaseConfig(retargeted, output)
}

// ManagedSandboxProxyRetarget is an operator-confirmed replacement of one
// installed regional proxy authority. ExpectedName and ExpectedURL fence the
// source bootstrap; Proxy is the complete replacement authority used by both
// the runtime environment and the rendered NetworkPolicy.
type ManagedSandboxProxyRetarget struct {
	Region       string
	ExpectedName string
	ExpectedURL  string
	Proxy        ManagedSandboxProxyProfileDocument
}

// RetargetManagedSandboxProxyDocument atomically replaces one regional proxy
// authority and recomputes the affected immutable profile identity and
// binding. Active configurations are immutable because their network evidence
// describes the old route; callers must first derive and deploy a fail-closed
// policy bootstrap, probe the replacement, and activate it with fresh evidence.
func RetargetManagedSandboxProxyDocument(
	document ConfigDocument, retarget ManagedSandboxProxyRetarget,
) (ConfigDocument, error) {
	document.ProxyProfiles = append([]ManagedSandboxProxyProfileDocument(nil), document.ProxyProfiles...)
	for index := range document.ProxyProfiles {
		document.ProxyProfiles[index].PodSelector = cloneStringMap(document.ProxyProfiles[index].PodSelector)
	}
	document.SandboxProfiles = append([]ManagedSandboxProfileDocument(nil), document.SandboxProfiles...)
	retarget.Proxy.PodSelector = cloneStringMap(retarget.Proxy.PodSelector)
	loaded, err := ValidateConfig(document)
	if err != nil {
		return ConfigDocument{}, fmt.Errorf("validate managed sandbox proxy retarget source: %w", err)
	}
	document = loaded.Document
	if !managedPolicyBootstrap(document.Managed) {
		return ConfigDocument{}, errors.New("managed sandbox proxy can be retargeted only in the policy-bootstrap stage")
	}
	if !managedsandboxprofile.ValidRegion(retarget.Region) || retarget.Region == managedsandboxprofile.RegionBOE {
		return ConfigDocument{}, errors.New("managed sandbox proxy retarget region must be a supported proxied TAE region")
	}
	if retarget.ExpectedName == "" || retarget.ExpectedURL == "" {
		return ConfigDocument{}, errors.New("expected managed sandbox proxy name and URL are required")
	}

	profileIndex := -1
	for index := range document.SandboxProfiles {
		if document.SandboxProfiles[index].Region == retarget.Region {
			if profileIndex != -1 {
				return ConfigDocument{}, errors.New("managed sandbox proxy retarget region is repeated")
			}
			profileIndex = index
		}
	}
	if profileIndex == -1 {
		return ConfigDocument{}, errors.New("managed sandbox proxy retarget region is not installed")
	}
	profile := document.SandboxProfiles[profileIndex]
	if profile.TAE.ProxyProfile != retarget.ExpectedName {
		return ConfigDocument{}, errors.New("current managed sandbox proxy name does not match the policy-bootstrap configuration")
	}

	proxyIndex := -1
	for index := range document.ProxyProfiles {
		proxy := document.ProxyProfiles[index]
		if proxy.Name == retarget.ExpectedName {
			if proxyIndex != -1 {
				return ConfigDocument{}, errors.New("current managed sandbox proxy is repeated")
			}
			proxyIndex = index
		}
		if index != proxyIndex && proxy.Name == retarget.Proxy.Name && retarget.Proxy.Name != retarget.ExpectedName {
			return ConfigDocument{}, errors.New("replacement managed sandbox proxy name is already configured")
		}
	}
	if proxyIndex == -1 || document.ProxyProfiles[proxyIndex].URL != retarget.ExpectedURL {
		return ConfigDocument{}, errors.New("current managed sandbox proxy URL does not match the policy-bootstrap configuration")
	}
	for index := range document.SandboxProfiles {
		if index != profileIndex && document.SandboxProfiles[index].TAE.ProxyProfile == retarget.ExpectedName {
			return ConfigDocument{}, errors.New("current managed sandbox proxy is shared by another installed region")
		}
	}
	if reflect.DeepEqual(document.ProxyProfiles[proxyIndex], retarget.Proxy) {
		return ConfigDocument{}, errors.New("replacement managed sandbox proxy authority is unchanged")
	}

	document.ProxyProfiles[proxyIndex] = retarget.Proxy
	managed := document.Managed
	managed.Stage = ManagedExecutorStageBootstrap
	managed.Environment = profile.Environment
	managed.TAE = profile.TAE
	managed.TAE.ProxyProfile = retarget.Proxy.Name
	if err := refreshManagedSandboxProfileFromManaged(&document, profileIndex, managed); err != nil {
		return ConfigDocument{}, err
	}
	retargeted, err := ValidateConfig(document)
	if err != nil {
		return ConfigDocument{}, fmt.Errorf("validate retargeted managed sandbox proxy: %w", err)
	}
	return retargeted.Document, nil
}

func RetargetManagedSandboxProxyJSON(raw []byte, retarget ManagedSandboxProxyRetarget) ([]byte, error) {
	document, err := decodeConfigDocument(raw)
	if err != nil {
		return nil, err
	}
	document, err = RetargetManagedSandboxProxyDocument(document, retarget)
	if err != nil {
		return nil, err
	}
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode retargeted managed sandbox proxy config: %w", err)
	}
	encoded = append(encoded, '\n')
	if _, err := ParseConfig(encoded); err != nil {
		return nil, fmt.Errorf("verify retargeted managed sandbox proxy config: %w", err)
	}
	return encoded, nil
}

func RetargetManagedSandboxProxyFile(
	input, output string, retarget ManagedSandboxProxyRetarget,
) error {
	raw, err := readProductionConfigFile(input)
	if err != nil {
		return err
	}
	retargeted, err := RetargetManagedSandboxProxyJSON(raw, retarget)
	if err != nil {
		return err
	}
	return WriteReleaseConfig(retargeted, output)
}

// ActivateManagedExecutorDocument is the single promotion edge out of the
// deny-only bootstrap. Every externally issued policy/network evidence value
// is explicit, and all dependent bindings are recomputed atomically.
type ManagedSandboxActivationEvidence struct {
	Region              string
	PolicyRevision      string
	PolicyEvidenceRef   string
	NetworkReport       taenetworkreport.Report
	NetworkReportSHA256 string
	NetworkEvidenceRef  string
}

func ActivateManagedExecutorDocument(
	document ConfigDocument,
	policyRevision, policyEvidenceRef string,
	networkReport taenetworkreport.Report, networkReportSHA256, networkEvidenceRef string,
) (ConfigDocument, error) {
	return ActivateManagedSandboxProfilesDocument(document, []ManagedSandboxActivationEvidence{{
		Region: networkReport.Configuration.Region, PolicyRevision: policyRevision,
		PolicyEvidenceRef: policyEvidenceRef, NetworkReport: networkReport,
		NetworkReportSHA256: networkReportSHA256, NetworkEvidenceRef: networkEvidenceRef,
	}})
}

// ActivateManagedSandboxProfilesDocument is the all-or-nothing promotion edge
// for a multi-region deployment. Every installed profile must supply its own
// canonical passing report and immutable policy/network evidence references;
// no region can become active with evidence inherited from the default.
func ActivateManagedSandboxProfilesDocument(
	document ConfigDocument,
	evidence []ManagedSandboxActivationEvidence,
) (ConfigDocument, error) {
	loaded, err := ValidateConfig(document)
	if err != nil {
		return ConfigDocument{}, fmt.Errorf("validate managed activation source: %w", err)
	}
	document = loaded.Document
	if !managedPolicyBootstrap(document.Managed) {
		return ConfigDocument{}, errors.New("managed executor activation source must be the policy-bootstrap stage")
	}
	if len(evidence) != len(document.SandboxProfiles) {
		return ConfigDocument{}, fmt.Errorf("managed executor activation requires exactly %d regional evidence entries", len(document.SandboxProfiles))
	}
	evidenceByRegion := make(map[string]ManagedSandboxActivationEvidence, len(evidence))
	for _, entry := range evidence {
		if !managedsandboxprofile.ValidRegion(entry.Region) {
			return ConfigDocument{}, fmt.Errorf("managed executor activation region %q is invalid", entry.Region)
		}
		if _, duplicate := evidenceByRegion[entry.Region]; duplicate {
			return ConfigDocument{}, fmt.Errorf("managed executor activation region %q is repeated", entry.Region)
		}
		if err := validateText("TAE policy revision", entry.PolicyRevision, 1, 128); err != nil {
			return ConfigDocument{}, err
		}
		if containsReleaseSentinel(entry.PolicyRevision) || strings.Contains(strings.ToUpper(entry.PolicyRevision), "PENDING") {
			return ConfigDocument{}, fmt.Errorf("TAE policy revision for %s contains a template sentinel", entry.Region)
		}
		for name, value := range map[string]string{
			"TAE policy evidence reference":  entry.PolicyEvidenceRef,
			"TAE network evidence reference": entry.NetworkEvidenceRef,
		} {
			if err := validateText(name, value, 1, 1024); err != nil {
				return ConfigDocument{}, err
			}
			if containsReleaseSentinel(value) {
				return ConfigDocument{}, fmt.Errorf("%s for %s contains a template sentinel", name, entry.Region)
			}
		}
		if !nonzeroDigest(entry.NetworkReportSHA256) || repeatedDigest(entry.NetworkReportSHA256) {
			return ConfigDocument{}, fmt.Errorf("computed TAE network report digest for %s is invalid", entry.Region)
		}
		evidenceByRegion[entry.Region] = entry
	}
	for _, profile := range document.SandboxProfiles {
		entry, found := evidenceByRegion[profile.Region]
		if !found {
			return ConfigDocument{}, fmt.Errorf("managed executor activation is missing evidence for %s", profile.Region)
		}
		if err := validateTAENetworkReportForProfileActivation(document, profile, entry.PolicyRevision, entry.NetworkReport); err != nil {
			return ConfigDocument{}, fmt.Errorf("validate TAE network report for %s: %w", profile.Region, err)
		}
	}

	document.Managed.Stage = ManagedExecutorStageActive
	for index := range document.SandboxProfiles {
		profile := document.SandboxProfiles[index]
		entry := evidenceByRegion[profile.Region]
		managed := document.Managed
		managed.Stage = ManagedExecutorStageActive
		managed.Environment = profile.Environment
		managed.TAE = profile.TAE
		managed.TAE.Policy.Revision = entry.PolicyRevision
		managed.TAE.Policy.Published = true
		managed.TAE.Policy.Approved = true
		managed.TAE.Policy.EvidenceRef = entry.PolicyEvidenceRef
		managed.TAE.NetworkEvidence = ManagedTAENetworkEvidenceDocument{
			Version: managedNetworkEvidenceVersion, ReportSHA256: entry.NetworkReportSHA256,
			EvidenceRef: entry.NetworkEvidenceRef,
		}
		synthetic := document
		synthetic.Managed = managed
		synthetic.Services.SandboxGateway = InternalServiceDocument{
			ClusterIP: profile.Gateway.ClusterIP, Port: profile.Gateway.Port,
		}
		synthetic.Secrets.SandboxGateway = profile.Gateway.Secret
		synthetic.Network.SandboxExternalEgress = append([]EgressRuleDocument{}, profile.SandboxExternalEgress...)
		if err := refreshManagedSandboxProfileFromManaged(&document, index, managed); err != nil {
			return ConfigDocument{}, err
		}
	}
	if err := validateManagedReleaseEvidence(document); err != nil {
		return ConfigDocument{}, fmt.Errorf("validate managed activation evidence: %w", err)
	}
	activated, err := ValidateConfig(document)
	if err != nil {
		return ConfigDocument{}, fmt.Errorf("validate activated managed executor: %w", err)
	}
	return activated.Document, nil
}

func ActivateManagedExecutorJSON(
	raw, networkReportRaw []byte,
	policyRevision, policyEvidenceRef, networkEvidenceRef string,
) ([]byte, error) {
	document, err := decodeConfigDocument(raw)
	if err != nil {
		return nil, err
	}
	networkReport, err := taenetworkreport.Parse(networkReportRaw)
	if err != nil {
		return nil, err
	}
	networkReportSHA256 := taenetworkreport.SHA256(networkReportRaw)
	document, err = ActivateManagedExecutorDocument(
		document, policyRevision, policyEvidenceRef, networkReport, networkReportSHA256, networkEvidenceRef,
	)
	if err != nil {
		return nil, err
	}
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode activated managed executor config: %w", err)
	}
	encoded = append(encoded, '\n')
	if _, err := ParseConfig(encoded); err != nil {
		return nil, fmt.Errorf("verify activated managed executor config: %w", err)
	}
	return encoded, nil
}

func ActivateManagedExecutorFile(
	input, output, networkReportPath, policyRevision, policyEvidenceRef, networkEvidenceRef string,
) error {
	raw, err := readProductionConfigFile(input)
	if err != nil {
		return err
	}
	_, networkReportRaw, err := taenetworkreport.Load(networkReportPath)
	if err != nil {
		return err
	}
	activated, err := ActivateManagedExecutorJSON(
		raw, networkReportRaw, policyRevision, policyEvidenceRef, networkEvidenceRef,
	)
	if err != nil {
		return err
	}
	return WriteReleaseConfig(activated, output)
}

type ManagedSandboxActivationManifest struct {
	SchemaVersion int                                       `json:"schemaVersion"`
	Profiles      []ManagedSandboxActivationProfileDocument `json:"profiles"`
}

type ManagedSandboxActivationProfileDocument struct {
	Region             string `json:"region"`
	PolicyRevision     string `json:"policyRevision"`
	PolicyEvidenceRef  string `json:"policyEvidenceRef"`
	NetworkReportPath  string `json:"networkReportPath"`
	NetworkEvidenceRef string `json:"networkEvidenceRef"`
}

// ActivateManagedSandboxProfilesFile promotes every installed profile from a
// single strict manifest. Report paths remain workstation-local operational
// inputs; only their canonical digests and immutable references enter the
// resulting production configuration.
func ActivateManagedSandboxProfilesFile(input, output, manifestPath string) error {
	raw, err := readProductionConfigFile(input)
	if err != nil {
		return err
	}
	document, err := decodeConfigDocument(raw)
	if err != nil {
		return err
	}
	manifestRaw, err := readProductionConfigFile(manifestPath)
	if err != nil {
		return fmt.Errorf("read managed sandbox activation manifest: %w", err)
	}
	manifest, err := parseManagedSandboxActivationManifest(manifestRaw)
	if err != nil {
		return err
	}
	evidence := make([]ManagedSandboxActivationEvidence, 0, len(manifest.Profiles))
	for _, profile := range manifest.Profiles {
		report, reportRaw, loadErr := taenetworkreport.Load(profile.NetworkReportPath)
		if loadErr != nil {
			return fmt.Errorf("load TAE network report for %s: %w", profile.Region, loadErr)
		}
		evidence = append(evidence, ManagedSandboxActivationEvidence{
			Region: profile.Region, PolicyRevision: profile.PolicyRevision,
			PolicyEvidenceRef: profile.PolicyEvidenceRef, NetworkReport: report,
			NetworkReportSHA256: taenetworkreport.SHA256(reportRaw), NetworkEvidenceRef: profile.NetworkEvidenceRef,
		})
	}
	activated, err := ActivateManagedSandboxProfilesDocument(document, evidence)
	if err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(activated, "", "  ")
	if err != nil {
		return fmt.Errorf("encode activated managed sandbox profiles: %w", err)
	}
	encoded = append(encoded, '\n')
	if _, err := ParseConfig(encoded); err != nil {
		return fmt.Errorf("verify activated managed sandbox profiles: %w", err)
	}
	return WriteReleaseConfig(encoded, output)
}

func parseManagedSandboxActivationManifest(raw []byte) (ManagedSandboxActivationManifest, error) {
	if len(raw) == 0 || len(raw) > int(maximumConfigBytes) {
		return ManagedSandboxActivationManifest{}, errors.New("managed sandbox activation manifest has an invalid size")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var document ManagedSandboxActivationManifest
	if err := decoder.Decode(&document); err != nil {
		return ManagedSandboxActivationManifest{}, fmt.Errorf("decode managed sandbox activation manifest: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ManagedSandboxActivationManifest{}, errors.New("managed sandbox activation manifest must contain exactly one JSON value")
	}
	if document.SchemaVersion != 1 {
		return ManagedSandboxActivationManifest{}, errors.New("managed sandbox activation manifest schemaVersion must be 1")
	}
	if len(document.Profiles) < 1 || len(document.Profiles) > len(managedsandboxprofile.Regions()) {
		return ManagedSandboxActivationManifest{}, errors.New("managed sandbox activation manifest must contain between one and four profiles")
	}
	regions := make(map[string]struct{}, len(document.Profiles))
	for index, profile := range document.Profiles {
		if !managedsandboxprofile.ValidRegion(profile.Region) {
			return ManagedSandboxActivationManifest{}, fmt.Errorf("managed sandbox activation manifest profiles[%d].region is invalid", index)
		}
		if _, duplicate := regions[profile.Region]; duplicate {
			return ManagedSandboxActivationManifest{}, fmt.Errorf("managed sandbox activation manifest region %q is repeated", profile.Region)
		}
		regions[profile.Region] = struct{}{}
		if !versionPattern.MatchString(profile.PolicyRevision) || containsReleaseSentinel(profile.PolicyRevision) || strings.Contains(strings.ToUpper(profile.PolicyRevision), "PENDING") {
			return ManagedSandboxActivationManifest{}, fmt.Errorf("managed sandbox activation manifest profiles[%d].policyRevision is invalid", index)
		}
		for name, value := range map[string]string{
			"policyEvidenceRef":  profile.PolicyEvidenceRef,
			"networkEvidenceRef": profile.NetworkEvidenceRef,
		} {
			if err := validateText("managed sandbox activation manifest "+name, value, 1, 1024); err != nil || containsReleaseSentinel(value) {
				return ManagedSandboxActivationManifest{}, fmt.Errorf("managed sandbox activation manifest profiles[%d].%s is invalid", index, name)
			}
		}
		if profile.NetworkReportPath == "" || !path.IsAbs(profile.NetworkReportPath) || path.Clean(profile.NetworkReportPath) != profile.NetworkReportPath {
			return ManagedSandboxActivationManifest{}, fmt.Errorf("managed sandbox activation manifest profiles[%d].networkReportPath must be absolute and clean", index)
		}
	}
	return document, nil
}

func validateTAENetworkReportForActivation(document ConfigDocument, policyRevision string, report taenetworkreport.Report) error {
	for _, profile := range document.SandboxProfiles {
		if profile.Region == document.SandboxRegions.DefaultRegion {
			return validateTAENetworkReportForProfileActivation(document, profile, policyRevision, report)
		}
	}
	return errors.New("default managed sandbox profile is unavailable")
}

func validateTAENetworkReportForProfileActivation(
	document ConfigDocument,
	profile ManagedSandboxProfileDocument,
	policyRevision string,
	report taenetworkreport.Report,
) error {
	if err := taenetworkreport.Validate(report); err != nil {
		return err
	}
	if !report.Passed || !report.CleanupConfirmed {
		return errors.New("TAE network report did not pass or did not confirm cleanup")
	}
	repository, err := productionTAEManagedSandboxRepository(profile.Region)
	if err != nil {
		return err
	}
	taeSandboxImage, err := taeimage.ContentTagForRepository(repository, document.Images.ManagedSandbox)
	if err != nil {
		return err
	}
	configuration := report.Configuration
	proxyURL := ""
	for _, proxy := range document.ProxyProfiles {
		if proxy.Name == profile.TAE.ProxyProfile {
			proxyURL = proxy.URL
			break
		}
	}
	for name, values := range map[string][2]string{
		"source.namespace":      {report.Source.Namespace, document.Namespace},
		"source.serviceAccount": {report.Source.ServiceAccount, taeNetworkProbeComponent},
		"region":                {configuration.Region, profile.TAE.Region},
		"psm":                   {configuration.PSM, ProductionTAEPSM},
		"policyRevision":        {configuration.PolicyRevision, policyRevision},
		"bytecloudSite":         {configuration.ByteCloudSite, profile.TAE.ByteCloudSite},
		"jwtEndpoint":           {configuration.JWTEndpoint, profile.TAE.ByteCloudJWTEndpoint},
		"proxyUrl":              {configuration.ProxyURL, proxyURL},
		"controlPlaneHost":      {configuration.ControlPlaneHost, strings.TrimPrefix(profile.TAE.ControlPlaneURL, "https://")},
		"dataPlaneDomainSuffix": {configuration.DataPlaneDomainSuffix, profile.TAE.DataPlaneSuffix},
		"sandboxImage":          {configuration.SandboxImage, taeSandboxImage},
		"sandboxId":             {configuration.SandboxID, profile.TAE.SandboxID},
		"sandboxRevisionId":     {configuration.SandboxRevisionID, profile.TAE.RevisionID},
		"larkCliVersion":        {configuration.LarkCLIVersion, productionimage.ManagedLarkCLIVersion},
		"larkCliSha256":         {configuration.LarkCLISHA256, document.Managed.Lark.CLISHA256},
		"larkSkillSha256":       {configuration.LarkSkillSHA256, document.Managed.Lark.SkillSHA256},
		"managedSkillSha256":    {configuration.ManagedSkillSHA256, document.Managed.BaseInstructionsSHA256},
		"bkectlSourceRevision":  {configuration.BkectlSourceRevision, document.Managed.Bkectl.SourceRevision},
		"bkectlCliSha256":       {configuration.BkectlCLISHA256, document.Managed.Bkectl.CLISHA256},
		"bkectlSkillPackSha256": {configuration.BkectlSkillPackSHA256, document.Managed.Bkectl.SkillPackSHA256},
	} {
		if values[0] != values[1] {
			return fmt.Errorf("TAE network report %s does not match the activation source", name)
		}
	}
	if configuration.ConnectivityAttempts < 20 {
		return errors.New("TAE network report requires at least 20 JWT and control-plane connectivity attempts")
	}
	if configuration.LifecycleAttempts < 1 || configuration.LifecycleAttempts > 5 {
		return errors.New("TAE network report lifecycle attempt count is invalid")
	}
	required := map[string]int{
		"jwt_force_refresh":                configuration.ConnectivityAttempts,
		"control_search_missing":           configuration.ConnectivityAttempts,
		"control_create":                   configuration.LifecycleAttempts,
		"control_search_created":           configuration.LifecycleAttempts,
		"control_wait_ready":               configuration.LifecycleAttempts,
		"control_update_ttl":               configuration.LifecycleAttempts,
		"data_exec_terminal":               configuration.LifecycleAttempts,
		"data_exec_lark_version":           configuration.LifecycleAttempts,
		"data_exec_bkectl_version":         configuration.LifecycleAttempts,
		"data_stat_lark_cli":               configuration.LifecycleAttempts,
		"data_read_lark_cli":               configuration.LifecycleAttempts,
		"data_stat_lark_skill":             configuration.LifecycleAttempts,
		"data_read_lark_skill":             configuration.LifecycleAttempts,
		"data_stat_managed_skill":          configuration.LifecycleAttempts,
		"data_read_managed_skill":          configuration.LifecycleAttempts,
		"data_stat_bkectl_cli":             configuration.LifecycleAttempts,
		"data_read_bkectl_cli":             configuration.LifecycleAttempts,
		"data_stat_bkectl_skill":           configuration.LifecycleAttempts,
		"data_read_bkectl_skill":           configuration.LifecycleAttempts,
		"data_stat_bkectl_command_surface": configuration.LifecycleAttempts,
		"data_read_bkectl_command_surface": configuration.LifecycleAttempts,
		"data_stat_bkectl_domain_guides":   configuration.LifecycleAttempts,
		"data_read_bkectl_domain_guides":   configuration.LifecycleAttempts,
		"data_stat_bkectl_invocation":      configuration.LifecycleAttempts,
		"data_read_bkectl_invocation":      configuration.LifecycleAttempts,
		"control_delete":                   configuration.LifecycleAttempts,
		"control_confirm_deleted":          configuration.LifecycleAttempts,
		"control_cleanup":                  configuration.LifecycleAttempts,
	}
	if len(report.Checks) != len(required) {
		return errors.New("TAE network report does not contain the exact required check set")
	}
	checks := make(map[string]taenetworkreport.Check, len(report.Checks))
	for _, check := range report.Checks {
		checks[check.Name] = check
	}
	for name, attempts := range required {
		check, found := checks[name]
		if !found || check.Attempts != attempts || check.Succeeded != attempts || check.Failed != 0 || len(check.Errors) != 0 {
			return fmt.Errorf("TAE network report check %s did not complete every required attempt", name)
		}
	}
	wantCLIBytes := productionimage.ManagedLarkCLISizeBytes * int64(configuration.LifecycleAttempts)
	if checks["data_read_lark_cli"].BytesRead != wantCLIBytes {
		return errors.New("TAE network report did not read and verify the complete pinned lark-cli in every lifecycle")
	}
	skillBytes := checks["data_read_lark_skill"].BytesRead
	if skillBytes < int64(configuration.LifecycleAttempts) || skillBytes > int64(configuration.LifecycleAttempts)*256*1024 ||
		skillBytes%int64(configuration.LifecycleAttempts) != 0 {
		return errors.New("TAE network report did not read and verify one bounded Lark skill in every lifecycle")
	}
	for checkName, size := range map[string]int64{
		"data_read_managed_skill":          productionimage.ManagedSkillSizeBytes,
		"data_read_bkectl_cli":             productionimage.ManagedBkectlCLISizeBytes,
		"data_read_bkectl_skill":           productionimage.ManagedBkectlSkillSizeBytes,
		"data_read_bkectl_command_surface": productionimage.ManagedBkectlCommandSurfaceSizeBytes,
		"data_read_bkectl_domain_guides":   productionimage.ManagedBkectlDomainGuidesSizeBytes,
		"data_read_bkectl_invocation":      productionimage.ManagedBkectlInvocationSizeBytes,
	} {
		if checks[checkName].BytesRead != size*int64(configuration.LifecycleAttempts) {
			return fmt.Errorf("TAE network report did not read and verify complete pinned artifact %s in every lifecycle", checkName)
		}
	}
	return nil
}

// ManagedTAEPolicyDocument is the release lock for the policy configured in
// the TAE Sandbox/PSM control plane. Session creation cannot carry these
// fields; both provider and webhook workloads validate the same lock instead.
type ManagedTAEPolicyDocument struct {
	Version               int    `json:"version"`
	Revision              string `json:"revision"`
	PolicySHA256          string `json:"policySha256"`
	PublicHost            string `json:"publicHost"`
	PublicAccess          string `json:"publicAccess"`
	PublicWebhookRequired bool   `json:"publicWebhookRequired"`
	WebhookMode           string `json:"webhookMode"`
	WebhookPSM            string `json:"webhookPsm"`
	WebhookURL            string `json:"webhookUrl"`
	WebhookPath           string `json:"webhookPath"`
	Published             bool   `json:"published"`
	Approved              bool   `json:"approved"`
	EvidenceRef           string `json:"evidenceRef"`
}

type ManagedLarkDocument struct {
	Enabled      bool   `json:"enabled"`
	CLISHA256    string `json:"cliSha256"`
	SkillSHA256  string `json:"skillSha256"`
	PolicySHA256 string `json:"policySha256"`
}

type ManagedBkectlDocument struct {
	Enabled         bool   `json:"enabled"`
	SourceRevision  string `json:"sourceRevision"`
	CLISHA256       string `json:"cliSha256"`
	SkillPackSHA256 string `json:"skillPackSha256"`
	PolicySHA256    string `json:"policySha256"`
}

// managedToolsEnabled controls the immutable managed CLI pack shipped to the
// harness and sandbox images. Workspace credentials remain dynamic Core state.
func managedToolsEnabled(managed ManagedExecutorDocument) bool {
	return managedExecutionActive(managed) && managed.Lark.Enabled && managed.Bkectl.Enabled
}

func validateManagedExecutor(managed ManagedExecutorDocument, document ConfigDocument) (LoadedConfig, error) {
	switch {
	case !managed.Enabled && managed.Stage != ManagedExecutorStageDisabled:
		return LoadedConfig{}, fmt.Errorf("managedExecutor.stage must be %q while the managed executor is disabled", ManagedExecutorStageDisabled)
	case managed.Enabled && managed.Stage != ManagedExecutorStageBootstrap && managed.Stage != ManagedExecutorStageActive:
		return LoadedConfig{}, fmt.Errorf("managedExecutor.stage must be %q or %q while enabled", ManagedExecutorStageBootstrap, ManagedExecutorStageActive)
	}
	if len(managed.WorkspaceAllowlist) < 1 || len(managed.WorkspaceAllowlist) > 64 {
		return LoadedConfig{}, errors.New("managedExecutor.workspaceAllowlist must contain between 1 and 64 workspace UUIDs")
	}
	allowlist := append([]string(nil), managed.WorkspaceAllowlist...)
	for index, workspaceID := range allowlist {
		if !validUUID(workspaceID) {
			return LoadedConfig{}, fmt.Errorf("managedExecutor.workspaceAllowlist[%d] must be a non-zero canonical lowercase UUID", index)
		}
	}
	slices.Sort(allowlist)
	if len(slices.Compact(allowlist)) != len(managed.WorkspaceAllowlist) {
		return LoadedConfig{}, errors.New("managedExecutor.workspaceAllowlist must not repeat a workspace")
	}
	if !slices.Contains(allowlist, document.Bootstrap.WorkspaceID) {
		return LoadedConfig{}, errors.New("managedExecutor.workspaceAllowlist must explicitly opt in the bootstrapped production workspace")
	}
	managed.WorkspaceAllowlist = allowlist
	if !managedsandboxprofile.ValidRegion(managed.TAE.Region) {
		return LoadedConfig{}, errors.New("managedExecutor.tae.region must be cn, boe, i18n-bd, or i18n-tt")
	}
	if managed.TAE.PSM != ProductionTAEPSM {
		return LoadedConfig{}, fmt.Errorf("managedExecutor.tae.psm must be exactly %s", ProductionTAEPSM)
	}
	proxies, err := validateManagedSandboxProxyProfiles(document.ProxyProfiles)
	if err != nil {
		return LoadedConfig{}, err
	}
	if _, err := validateManagedSandboxTAEAuthority("managedExecutor.tae", managed.TAE, proxies); err != nil {
		return LoadedConfig{}, err
	}
	if !dnsLabelPattern.MatchString(managed.TAE.SandboxID) {
		return LoadedConfig{}, errors.New("managedExecutor.tae.sandboxId must be a canonical lowercase TAE identity")
	}
	if !dnsLabelPattern.MatchString(managed.TAE.RevisionID) {
		return LoadedConfig{}, errors.New("managedExecutor.tae.sandboxRevisionId must be a canonical lowercase TAE identity")
	}
	if !validUUID(managed.Environment.EnvironmentID) {
		return LoadedConfig{}, errors.New("managedExecutor.environment.environmentId must be a non-zero canonical lowercase UUID")
	}
	reservedIDs := []string{
		document.Bootstrap.WorkspaceID, document.Bootstrap.SessionID, document.Bootstrap.OwnerUserID,
		document.Bootstrap.ExecutorID,
	}
	for _, reserved := range reservedIDs {
		if managed.Environment.EnvironmentID == reserved {
			return LoadedConfig{}, errors.New("managed executor environment ID must be distinct from all other production authorities")
		}
	}
	root := managed.Environment.Root
	if root.Path != managedSandboxRootPath {
		return LoadedConfig{}, fmt.Errorf("managedExecutor.environment.root.path must be exactly %s for the closed-world image", managedSandboxRootPath)
	}
	if err := validateText("managedExecutor.environment.root.displayName", root.DisplayName, 1, 256); err != nil {
		return LoadedConfig{}, err
	}
	if err := validateText("managedExecutor.environment.root.description", root.Description, 0, 2048); err != nil {
		return LoadedConfig{}, err
	}
	if root.DefaultCWD != "" && (strings.HasPrefix(root.DefaultCWD, "/") || path.Clean(root.DefaultCWD) != root.DefaultCWD ||
		root.DefaultCWD == ".." || strings.HasPrefix(root.DefaultCWD, "../") || strings.Contains(root.DefaultCWD, "\\")) {
		return LoadedConfig{}, errors.New("managedExecutor.environment.root.defaultCwd must be a clean relative Unix path without parent traversal")
	}
	compatibility := managed.Environment.Compatibility
	if err := validateText("managedExecutor.environment.compatibilityRuntime.codexRelease", compatibility.CodexRelease, 1, 128); err != nil {
		return LoadedConfig{}, err
	}
	if len(compatibility.CodexCommit) != 40 || strings.Trim(compatibility.CodexCommit, "0123456789abcdef") != "" {
		return LoadedConfig{}, errors.New("managedExecutor.environment.compatibilityRuntime.codexCommit must be a lowercase 40-character Git SHA")
	}
	if compatibility.CodexRelease != stockruntime.CodexRelease ||
		compatibility.CodexCommit != stockruntime.CodexCommit ||
		compatibility.CodexSHA256 != stockruntime.LinuxAMD64CodexSHA256 {
		return LoadedConfig{}, errors.New("managedExecutor.environment.compatibilityRuntime must equal the pinned SG harness Codex runtime")
	}
	for name, digest := range map[string]string{
		"environment.compatibilityRuntime.codexSha256": compatibility.CodexSHA256,
	} {
		if !nonzeroDigest(digest) {
			return LoadedConfig{}, fmt.Errorf("managedExecutor.%s must be a non-zero lowercase SHA-256 digest", name)
		}
	}
	bootstrap := managedPolicyBootstrap(managed)
	if managed.Enabled && (!managed.Lark.Enabled || !managed.Bkectl.Enabled) {
		return LoadedConfig{}, errors.New("managedExecutor requires both the pinned lark and managed bkectl tools while enabled")
	}
	if managed.BaseInstructionsSHA256 != "" && managed.BaseInstructionsSHA256 != productionimage.ManagedSkillSHA256 {
		return LoadedConfig{}, fmt.Errorf("managedExecutor.baseInstructionsSha256 must equal pinned managed CLI instructions digest %s", productionimage.ManagedSkillSHA256)
	}
	if managed.Enabled && managed.BaseInstructionsSHA256 == "" {
		return LoadedConfig{}, errors.New("managedExecutor.baseInstructionsSha256 must pin the managed CLI instructions while enabled")
	}
	larkEnabled := managed.Lark.Enabled
	if larkEnabled {
		for name, digest := range map[string]string{
			"lark.cliSha256":    managed.Lark.CLISHA256,
			"lark.skillSha256":  managed.Lark.SkillSHA256,
			"lark.policySha256": managed.Lark.PolicySHA256,
		} {
			if !nonzeroDigest(digest) {
				return LoadedConfig{}, fmt.Errorf("managedExecutor.%s must be a non-zero lowercase SHA-256 digest", name)
			}
		}
	} else {
		// Static Lark metadata may remain in a no-grant release so the same
		// production document can be promoted later. It is never projected to
		// a workload while Enabled is false, but any supplied digest must still
		// be canonical and policy-bound.
		for name, digest := range map[string]string{
			"lark.cliSha256":    managed.Lark.CLISHA256,
			"lark.skillSha256":  managed.Lark.SkillSHA256,
			"lark.policySha256": managed.Lark.PolicySHA256,
		} {
			if digest != "" && !nonzeroDigest(digest) {
				return LoadedConfig{}, fmt.Errorf("managedExecutor.%s must be empty or a non-zero lowercase SHA-256 digest", name)
			}
		}
	}
	if managed.Lark.CLISHA256 != "" && managed.Lark.CLISHA256 != productionimage.ManagedLarkCLISHA256 {
		return LoadedConfig{}, fmt.Errorf("managedExecutor.lark.cliSha256 must equal pinned lark-cli %s digest %s", productionimage.ManagedLarkCLIVersion, productionimage.ManagedLarkCLISHA256)
	}
	if managed.Lark.PolicySHA256 != "" && managed.Lark.PolicySHA256 != larkegresspolicy.SHA256Hex() {
		return LoadedConfig{}, fmt.Errorf("managedExecutor.lark.policySha256 must equal compiled policy %s", larkegresspolicy.SHA256Hex())
	}
	bkectlEnabled := managed.Bkectl.Enabled
	if bkectlEnabled {
		for name, digest := range map[string]string{
			"bkectl.cliSha256":       managed.Bkectl.CLISHA256,
			"bkectl.skillPackSha256": managed.Bkectl.SkillPackSHA256,
			"bkectl.policySha256":    managed.Bkectl.PolicySHA256,
		} {
			if !nonzeroDigest(digest) {
				return LoadedConfig{}, fmt.Errorf("managedExecutor.%s must be a non-zero lowercase SHA-256 digest", name)
			}
		}
		if len(managed.Bkectl.SourceRevision) != 40 || strings.Trim(managed.Bkectl.SourceRevision, "0123456789abcdef") != "" {
			return LoadedConfig{}, errors.New("managedExecutor.bkectl.sourceRevision must be a lowercase 40-character Git SHA")
		}
	} else {
		for name, digest := range map[string]string{
			"bkectl.cliSha256":       managed.Bkectl.CLISHA256,
			"bkectl.skillPackSha256": managed.Bkectl.SkillPackSHA256,
			"bkectl.policySha256":    managed.Bkectl.PolicySHA256,
		} {
			if digest != "" && !nonzeroDigest(digest) {
				return LoadedConfig{}, fmt.Errorf("managedExecutor.%s must be empty or a non-zero lowercase SHA-256 digest", name)
			}
		}
		if managed.Bkectl.SourceRevision != "" &&
			(len(managed.Bkectl.SourceRevision) != 40 || strings.Trim(managed.Bkectl.SourceRevision, "0123456789abcdef") != "") {
			return LoadedConfig{}, errors.New("managedExecutor.bkectl.sourceRevision must be empty or a lowercase 40-character Git SHA")
		}
	}
	for name, values := range map[string][2]string{
		"sourceRevision":  {managed.Bkectl.SourceRevision, bkectlpolicy.SourceRevision},
		"cliSha256":       {managed.Bkectl.CLISHA256, bkectlpolicy.CLISHA256},
		"skillPackSha256": {managed.Bkectl.SkillPackSHA256, bkectlpolicy.SkillPackSHA256},
		"policySha256":    {managed.Bkectl.PolicySHA256, bkectlpolicy.SHA256Hex()},
	} {
		if values[0] != "" && values[0] != values[1] {
			return LoadedConfig{}, fmt.Errorf("managedExecutor.bkectl.%s must equal pinned value %s", name, values[1])
		}
	}
	policy := taepolicy.Binding{
		Version: managed.TAE.Policy.Version, Region: managed.TAE.Region, SandboxPSM: managed.TAE.PSM,
		Revision: managed.TAE.Policy.Revision, PolicySHA256: managed.TAE.Policy.PolicySHA256,
		PublicHost:   managed.TAE.Policy.PublicHost,
		PublicAccess: managed.TAE.Policy.PublicAccess, PublicWebhookRequired: managed.TAE.Policy.PublicWebhookRequired,
		WebhookMode: managed.TAE.Policy.WebhookMode, WebhookPSM: managed.TAE.Policy.WebhookPSM,
		WebhookURL: managed.TAE.Policy.WebhookURL, WebhookPath: managed.TAE.Policy.WebhookPath,
		Published: managed.TAE.Policy.Published, Approved: managed.TAE.Policy.Approved,
		EvidenceRef: managed.TAE.Policy.EvidenceRef,
	}
	if managed.TAE.Policy.PublicWebhookRequired && managed.TAE.Policy.WebhookMode == "url" {
		if managed.TAE.Policy.WebhookURL != ProductionEgressAuthorizerURL || managed.TAE.Policy.WebhookPSM != "" {
			return LoadedConfig{}, fmt.Errorf("managedExecutor.tae.policy URL webhook must be exactly %s", ProductionEgressAuthorizerURL)
		}
	}
	if policy.PolicySHA256 != larkegresspolicy.SHA256Hex() {
		return LoadedConfig{}, errors.New("managedExecutor.tae.policy.policySha256 must equal the compiled managed egress policy")
	}
	var policyError error
	if bootstrap {
		policyError = policy.ValidateDraft(managed.TAE.Region, managed.TAE.PSM, larkegresspolicy.SHA256Hex())
	} else {
		policyError = policy.Validate(managed.TAE.Region, managed.TAE.PSM, larkegresspolicy.SHA256Hex())
	}
	if policyError != nil {
		return LoadedConfig{}, fmt.Errorf("managedExecutor.tae.policy: %w", policyError)
	}
	evidence := managed.TAE.NetworkEvidence
	if bootstrap {
		if evidence != (ManagedTAENetworkEvidenceDocument{}) {
			return LoadedConfig{}, errors.New("managedExecutor policy-bootstrap stage must not claim SG network evidence")
		}
	} else if evidence.Version != managedNetworkEvidenceVersion {
		return LoadedConfig{}, fmt.Errorf("managedExecutor.tae.networkEvidence.version must be %d", managedNetworkEvidenceVersion)
	} else if !nonzeroDigest(evidence.ReportSHA256) {
		return LoadedConfig{}, errors.New("managedExecutor.tae.networkEvidence.reportSha256 must be a non-zero lowercase SHA-256 digest")
	} else if err := validateText("managedExecutor.tae.networkEvidence.evidenceRef", evidence.EvidenceRef, 1, 1024); err != nil {
		return LoadedConfig{}, err
	}
	sandboxTTL, err := parseManagedDuration("managedExecutor.environment.sandboxTtl", managed.Environment.SandboxTTL, 30*time.Second, 24*time.Hour)
	if err != nil {
		return LoadedConfig{}, err
	}
	activityTTL, err := parseManagedDuration("managedExecutor.environment.activityTtl", managed.Environment.ActivityTTL, 3*time.Second, 24*time.Hour)
	if err != nil {
		return LoadedConfig{}, err
	}
	idleTTL, err := parseManagedDuration("managedExecutor.environment.idleTtl", managed.Environment.IdleTTL, time.Second, 24*time.Hour)
	if err != nil {
		return LoadedConfig{}, err
	}
	if activityTTL > sandboxTTL || idleTTL > sandboxTTL {
		return LoadedConfig{}, errors.New("managed executor activityTtl and idleTtl must not exceed sandboxTtl")
	}
	return LoadedConfig{
		Document: ConfigDocument{Managed: managed}, ManagedSandboxTTL: sandboxTTL,
		ManagedActivityTTL: activityTTL, ManagedIdleTTL: idleTTL,
	}, nil
}

func managedTAEPolicyBinding(managed ManagedTAEDocument) taepolicy.Binding {
	return taepolicy.Binding{
		Version: managed.Policy.Version, Region: managed.Region, SandboxPSM: managed.PSM,
		Revision: managed.Policy.Revision, PolicySHA256: managed.Policy.PolicySHA256,
		PublicHost:   managed.Policy.PublicHost,
		PublicAccess: managed.Policy.PublicAccess, PublicWebhookRequired: managed.Policy.PublicWebhookRequired,
		WebhookMode: managed.Policy.WebhookMode, WebhookPSM: managed.Policy.WebhookPSM,
		WebhookURL: managed.Policy.WebhookURL, WebhookPath: managed.Policy.WebhookPath,
		Published: managed.Policy.Published, Approved: managed.Policy.Approved,
		EvidenceRef: managed.Policy.EvidenceRef,
	}
}

func nonzeroDigest(value string) bool {
	return digestPattern.MatchString(value) && strings.Trim(value, "0") != ""
}

func parseManagedDuration(name, raw string, minimum, maximum time.Duration) (time.Duration, error) {
	value, err := time.ParseDuration(raw)
	if err != nil || value < minimum || value > maximum || value%time.Second != 0 {
		return 0, fmt.Errorf("%s must be a whole-second Go duration between %s and %s", name, minimum, maximum)
	}
	return value, nil
}

func validateIngressPrefixes(name string, values *[]string) error {
	if values == nil || len(*values) > 64 {
		return fmt.Errorf("%s must contain at most 64 CIDRs", name)
	}
	if len(*values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(*values))
	has4, has6 := false, false
	for index, value := range *values {
		prefix, err := netip.ParsePrefix(value)
		if err != nil || prefix.Masked().String() != value ||
			(prefix.Addr().Is4() && prefix.Bits() < 8) ||
			(prefix.Addr().Is6() && (prefix.Addr().Is4In6() || prefix.Bits() < 32)) {
			return fmt.Errorf("%s[%d] must be a canonical IP CIDR no broader than IPv4 /8 or IPv6 /32", name, index)
		}
		if prefix.Addr().Is4() {
			has4 = true
		} else {
			has6 = true
		}
		if _, duplicate := seen[value]; duplicate {
			return fmt.Errorf("%s repeats %s", name, value)
		}
		seen[value] = struct{}{}
	}
	if !has4 || !has6 {
		return fmt.Errorf("%s must explicitly close both IPv4 and IPv6 webhook source ranges", name)
	}
	slices.Sort(*values)
	return nil
}

func requireDualStackEgress(name string, rules []EgressRuleDocument) error {
	has4, has6 := false, false
	for _, rule := range rules {
		prefix, err := netip.ParsePrefix(rule.CIDR)
		if err != nil {
			return fmt.Errorf("%s contains an invalid CIDR", name)
		}
		if prefix.Addr().Is4() {
			has4 = true
		} else {
			has6 = true
		}
	}
	if !has4 || !has6 {
		return fmt.Errorf("%s must explicitly close both IPv4 and IPv6 destinations", name)
	}
	return nil
}
