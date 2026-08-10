package productiondeploy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/agentserver/agentserver/v2/internal/larkegresspolicy"
	"github.com/agentserver/agentserver/v2/internal/productionimage"
	"github.com/agentserver/agentserver/v2/internal/stockruntime"
	"github.com/agentserver/agentserver/v2/internal/taenetworkreport"
	"github.com/agentserver/agentserver/v2/internal/taepolicy"
)

const (
	managedLarkCLIPath             = "/usr/local/bin/lark-cli"
	managedLarkSkillPath           = "/opt/agentserver/packs/lark-readonly/SKILL.md"
	managedSandboxRootPath         = "/workspace"
	managedNetworkEvidenceVersion  = 3
	ProductionTAEPSM               = "bytedance.sandbox.agentserver"
	ProductionByteCloudJWTEndpoint = "https://cloud-i18n-sg.bytedance.net"
	ProductionTAEControlPlaneHost  = "controlplane.sg.ai-sandbox-i18n.byted.org"
	ProductionTAEDataPlaneSuffix   = "sg.ai-sandbox-i18n.byted.org"
	ProductionTAEProxyURL          = "socks5h://ssh-egress-merlin-i18nbd-syd2a-83092.ssh-egress.svc.cluster.local:1080"
	ProductionTAEProxyNamespace    = "ssh-egress"
	ProductionTAEProxyPodApp       = "ssh-egress-merlin-i18nbd-syd2a-83092"
	ProductionTAEProxyPort         = uint16(1080)
	ManagedExecutorStageDisabled   = "disabled"
	ManagedExecutorStageBootstrap  = "policy-bootstrap"
	ManagedExecutorStageActive     = "active"
)

type ManagedExecutorDocument struct {
	Enabled            bool                       `json:"enabled"`
	Stage              string                     `json:"stage"`
	WorkspaceAllowlist []string                   `json:"workspaceAllowlist"`
	Environment        ManagedEnvironmentDocument `json:"environment"`
	TAE                ManagedTAEDocument         `json:"tae"`
	Lark               ManagedLarkDocument        `json:"lark"`
}

func managedExecutionActive(managed ManagedExecutorDocument) bool {
	return managed.Enabled && managed.Stage == ManagedExecutorStageActive
}

func managedPolicyBootstrap(managed ManagedExecutorDocument) bool {
	return managed.Enabled && managed.Stage == ManagedExecutorStageBootstrap
}

func managedEgressAuthorizerEnabled(managed ManagedExecutorDocument) bool {
	return managedExecutionActive(managed) || managedPolicyBootstrap(managed)
}

type ManagedEnvironmentDocument struct {
	EnvironmentID        string                              `json:"environmentId"`
	Root                 ManagedEnvironmentRootDocument      `json:"root"`
	Compatibility        ManagedCompatibilityRuntimeDocument `json:"compatibilityRuntime"`
	RuntimeProfileSHA256 string                              `json:"runtimeProfileSha256"`
	PackSetSHA256        string                              `json:"packSetSha256"`
	SandboxTTL           string                              `json:"sandboxTtl"`
	ActivityTTL          string                              `json:"activityTtl"`
	IdleTTL              string                              `json:"idleTtl"`
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
	Region          string                            `json:"region"`
	PSM             string                            `json:"psm"`
	Policy          ManagedTAEPolicyDocument          `json:"policy"`
	NetworkEvidence ManagedTAENetworkEvidenceDocument `json:"networkEvidence"`
}

// ManagedTAENetworkEvidenceDocument binds the SG network facts consumed by
// the rendered NetworkPolicies to an immutable verification
// report. The report itself can contain operational details; production.json
// contains only its reference and SHA-256 digest.
type ManagedTAENetworkEvidenceDocument struct {
	Version       int    `json:"version"`
	ReportSHA256  string `json:"reportSha256"`
	BindingSHA256 string `json:"bindingSha256"`
	EvidenceRef   string `json:"evidenceRef"`
}

// PreparePolicyBootstrapDocument derives the only pre-approval production
// stage from an otherwise valid SG document. It deliberately removes every
// field that could claim an approved policy, verified TAE network path, or
// active managed runtime. The resulting chart exposes only the deny-only
// policy Webhook; the ordinary platform remains available.
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
	document.Managed.Environment.RuntimeProfileSHA256 = ""
	document.Managed.Environment.PackSetSHA256 = ""
	document.Managed.TAE.Policy.Published = false
	document.Managed.TAE.Policy.Approved = false
	document.Managed.TAE.Policy.EvidenceRef = ""
	document.Managed.TAE.Policy.BindingSHA256 = managedTAEPolicyBinding(document.Managed.TAE).DigestHex()
	document.Managed.TAE.NetworkEvidence = ManagedTAENetworkEvidenceDocument{}
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

// ActivateManagedExecutorDocument is the single promotion edge out of the
// deny-only bootstrap. Every externally issued policy/network evidence value
// is explicit, and all dependent bindings are recomputed atomically.
func ActivateManagedExecutorDocument(
	document ConfigDocument,
	policyRevision, policyEvidenceRef string,
	networkReport taenetworkreport.Report, networkReportSHA256, networkEvidenceRef string,
) (ConfigDocument, error) {
	loaded, err := ValidateConfig(document)
	if err != nil {
		return ConfigDocument{}, fmt.Errorf("validate managed activation source: %w", err)
	}
	document = loaded.Document
	if !managedPolicyBootstrap(document.Managed) {
		return ConfigDocument{}, errors.New("managed executor activation source must be the policy-bootstrap stage")
	}
	if err := validateText("TAE policy revision", policyRevision, 1, 128); err != nil {
		return ConfigDocument{}, err
	}
	if containsReleaseSentinel(policyRevision) || strings.Contains(strings.ToUpper(policyRevision), "PENDING") {
		return ConfigDocument{}, errors.New("TAE policy revision contains a template sentinel")
	}
	for name, value := range map[string]string{
		"TAE policy evidence reference":  policyEvidenceRef,
		"TAE network evidence reference": networkEvidenceRef,
	} {
		if err := validateText(name, value, 1, 1024); err != nil {
			return ConfigDocument{}, err
		}
		if containsReleaseSentinel(value) {
			return ConfigDocument{}, fmt.Errorf("%s contains a template sentinel", name)
		}
	}
	if err := validateTAENetworkReportForActivation(document, policyRevision, networkReport); err != nil {
		return ConfigDocument{}, fmt.Errorf("validate TAE network report: %w", err)
	}
	if !nonzeroDigest(networkReportSHA256) || repeatedDigest(networkReportSHA256) {
		return ConfigDocument{}, errors.New("computed TAE network report digest is invalid")
	}

	document.Managed.Stage = ManagedExecutorStageActive
	document.Managed.TAE.Policy.Revision = policyRevision
	document.Managed.TAE.Policy.Published = true
	document.Managed.TAE.Policy.Approved = true
	document.Managed.TAE.Policy.EvidenceRef = policyEvidenceRef
	document.Managed.TAE.Policy.BindingSHA256 = managedTAEPolicyBinding(document.Managed.TAE).DigestHex()
	document.Managed.TAE.NetworkEvidence = ManagedTAENetworkEvidenceDocument{
		Version: managedNetworkEvidenceVersion, ReportSHA256: networkReportSHA256,
		EvidenceRef: networkEvidenceRef,
	}
	document.Managed.TAE.NetworkEvidence.BindingSHA256 = managedTAENetworkEvidenceDigest(document)
	document.Managed.Environment.RuntimeProfileSHA256 = managedRuntimeProfileDigest(document, document.Managed)
	document.Managed.Environment.PackSetSHA256 = managedPackSetDigest(document.Managed)
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

func validateTAENetworkReportForActivation(document ConfigDocument, policyRevision string, report taenetworkreport.Report) error {
	if err := taenetworkreport.Validate(report); err != nil {
		return err
	}
	if !report.Passed || !report.CleanupConfirmed {
		return errors.New("TAE network report did not pass or did not confirm cleanup")
	}
	configuration := report.Configuration
	for name, values := range map[string][2]string{
		"source.namespace":       {report.Source.Namespace, document.Namespace},
		"source.serviceAccount":  {report.Source.ServiceAccount, taeNetworkProbeComponent},
		"deploymentConfigSha256": {configuration.DeploymentConfigSHA256, canonicalDigest(document)},
		"region":                 {configuration.Region, ProductionRegion},
		"psm":                    {configuration.PSM, ProductionTAEPSM},
		"policyRevision":         {configuration.PolicyRevision, policyRevision},
		"bytecloudSite":          {configuration.ByteCloudSite, "i18n-tt"},
		"jwtEndpoint":            {configuration.JWTEndpoint, ProductionByteCloudJWTEndpoint},
		"proxyUrl":               {configuration.ProxyURL, ProductionTAEProxyURL},
		"controlPlaneHost":       {configuration.ControlPlaneHost, ProductionTAEControlPlaneHost},
		"dataPlaneDomainSuffix":  {configuration.DataPlaneDomainSuffix, ProductionTAEDataPlaneSuffix},
		"sandboxImage":           {configuration.SandboxImage, document.Images.ManagedSandbox},
		"larkCliVersion":         {configuration.LarkCLIVersion, productionimage.ManagedLarkCLIVersion},
		"larkCliSha256":          {configuration.LarkCLISHA256, document.Managed.Lark.CLISHA256},
		"larkSkillSha256":        {configuration.LarkSkillSHA256, document.Managed.Lark.SkillSHA256},
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
		"jwt_force_refresh":       configuration.ConnectivityAttempts,
		"control_search_missing":  configuration.ConnectivityAttempts,
		"control_create":          configuration.LifecycleAttempts,
		"control_search_created":  configuration.LifecycleAttempts,
		"control_wait_ready":      configuration.LifecycleAttempts,
		"control_update_ttl":      configuration.LifecycleAttempts,
		"data_exec_lark_version":  configuration.LifecycleAttempts,
		"data_stat_lark_cli":      configuration.LifecycleAttempts,
		"data_read_lark_cli":      configuration.LifecycleAttempts,
		"data_stat_lark_skill":    configuration.LifecycleAttempts,
		"data_read_lark_skill":    configuration.LifecycleAttempts,
		"control_delete":          configuration.LifecycleAttempts,
		"control_confirm_deleted": configuration.LifecycleAttempts,
		"control_cleanup":         configuration.LifecycleAttempts,
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
	return nil
}

// ManagedTAEPolicyDocument is the release lock for the policy configured in
// the TAE Sandbox/PSM control plane. Session creation cannot carry these
// fields; both provider and webhook workloads validate the same lock instead.
type ManagedTAEPolicyDocument struct {
	Version               int    `json:"version"`
	Revision              string `json:"revision"`
	PolicySHA256          string `json:"policySha256"`
	BindingSHA256         string `json:"bindingSha256"`
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

// managedLarkEnabled controls only the immutable Lark CLI/skill pack shipped to
// managed sandboxes. Workspace credentials are configured dynamically through
// v2 Core and are intentionally absent from this deployment document. A tool
// invocation without an active binding is denied at runtime by Core.
func managedLarkEnabled(managed ManagedExecutorDocument) bool {
	return managedExecutionActive(managed) && managed.Lark.Enabled
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
	if managed.TAE.Region != ProductionRegion {
		return LoadedConfig{}, fmt.Errorf("managedExecutor.tae.region must be exactly %s", ProductionRegion)
	}
	if managed.TAE.PSM != ProductionTAEPSM {
		return LoadedConfig{}, fmt.Errorf("managedExecutor.tae.psm must be exactly %s", ProductionTAEPSM)
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
		"tae.policy.bindingSha256":                     managed.TAE.Policy.BindingSHA256,
	} {
		if !nonzeroDigest(digest) {
			return LoadedConfig{}, fmt.Errorf("managedExecutor.%s must be a non-zero lowercase SHA-256 digest", name)
		}
	}
	bootstrap := managedPolicyBootstrap(managed)
	if bootstrap {
		if managed.Environment.RuntimeProfileSHA256 != "" || managed.Environment.PackSetSHA256 != "" {
			return LoadedConfig{}, errors.New("managedExecutor policy-bootstrap stage must not claim active runtime or pack locks")
		}
	} else {
		for name, digest := range map[string]string{
			"environment.runtimeProfileSha256": managed.Environment.RuntimeProfileSHA256,
			"environment.packSetSha256":        managed.Environment.PackSetSHA256,
		} {
			if !nonzeroDigest(digest) {
				return LoadedConfig{}, fmt.Errorf("managedExecutor.%s must be a non-zero lowercase SHA-256 digest", name)
			}
		}
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
	policy := taepolicy.Binding{
		Version: managed.TAE.Policy.Version, Region: managed.TAE.Region, SandboxPSM: managed.TAE.PSM,
		Revision: managed.TAE.Policy.Revision, PolicySHA256: managed.TAE.Policy.PolicySHA256,
		BindingSHA256: managed.TAE.Policy.BindingSHA256, PublicHost: managed.TAE.Policy.PublicHost,
		PublicAccess: managed.TAE.Policy.PublicAccess, PublicWebhookRequired: managed.TAE.Policy.PublicWebhookRequired,
		WebhookMode: managed.TAE.Policy.WebhookMode, WebhookPSM: managed.TAE.Policy.WebhookPSM,
		WebhookURL: managed.TAE.Policy.WebhookURL, WebhookPath: managed.TAE.Policy.WebhookPath,
		Published: managed.TAE.Policy.Published, Approved: managed.TAE.Policy.Approved,
		EvidenceRef: managed.TAE.Policy.EvidenceRef,
	}
	if !managed.TAE.Policy.PublicWebhookRequired {
		return LoadedConfig{}, errors.New("managedExecutor.tae.policy must require the shared credential-policy webhook")
	}
	if managed.TAE.Policy.WebhookMode == "url" {
		if managed.TAE.Policy.WebhookURL != ProductionEgressAuthorizerURL || managed.TAE.Policy.WebhookPSM != "" {
			return LoadedConfig{}, fmt.Errorf("managedExecutor.tae.policy URL webhook must be exactly %s", ProductionEgressAuthorizerURL)
		}
	}
	if policy.PolicySHA256 != larkegresspolicy.SHA256Hex() {
		return LoadedConfig{}, errors.New("managedExecutor.tae.policy.policySha256 must equal the compiled managed egress policy")
	}
	var policyError error
	if bootstrap {
		policyError = policy.ValidateDraft(ProductionRegion, managed.TAE.PSM, larkegresspolicy.SHA256Hex())
	} else {
		policyError = policy.Validate(ProductionRegion, managed.TAE.PSM, larkegresspolicy.SHA256Hex())
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
	} else if !nonzeroDigest(evidence.BindingSHA256) {
		return LoadedConfig{}, errors.New("managedExecutor.tae.networkEvidence.bindingSha256 must be a non-zero lowercase SHA-256 digest")
	} else if err := validateText("managedExecutor.tae.networkEvidence.evidenceRef", evidence.EvidenceRef, 1, 1024); err != nil {
		return LoadedConfig{}, err
	}
	if !bootstrap {
		wantNetworkBinding := managedTAENetworkEvidenceDigest(document)
		if evidence.BindingSHA256 != wantNetworkBinding {
			return LoadedConfig{}, fmt.Errorf("managedExecutor.tae.networkEvidence.bindingSha256 must equal the canonical SG network evidence lock %s", wantNetworkBinding)
		}
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
	ownerPolicy := ""
	if !bootstrap {
		wantRuntime := managedRuntimeProfileDigest(document, managed)
		if managed.Environment.RuntimeProfileSHA256 != wantRuntime {
			return LoadedConfig{}, fmt.Errorf("managedExecutor.environment.runtimeProfileSha256 must equal the canonical deployment lock %s", wantRuntime)
		}
		wantPack := managedPackSetDigest(managed)
		if managed.Environment.PackSetSHA256 != wantPack {
			return LoadedConfig{}, fmt.Errorf("managedExecutor.environment.packSetSha256 must equal the canonical managed pack lock %s", wantPack)
		}
		ownerPolicy = managedOwnerPolicyDigest(managed)
	}
	return LoadedConfig{
		Document: ConfigDocument{Managed: managed}, ManagedSandboxTTL: sandboxTTL,
		ManagedActivityTTL: activityTTL, ManagedIdleTTL: idleTTL, ManagedOwnerPolicySHA256: ownerPolicy,
	}, nil
}

func managedRuntimeProfileDigest(document ConfigDocument, managed ManagedExecutorDocument) string {
	larkEnabled := managed.Lark.Enabled
	lock := struct {
		Version                 int    `json:"version"`
		Platform                string `json:"platform"`
		Image                   string `json:"image"`
		Root                    string `json:"root"`
		CodexRelease            string `json:"codexRelease"`
		CodexCommit             string `json:"codexCommit"`
		CodexSHA256             string `json:"codexSha256"`
		LarkEnabled             bool   `json:"larkEnabled"`
		LarkCLIPath             string `json:"larkCliPath"`
		LarkCLISHA256           string `json:"larkCliSha256"`
		PolicySHA256            string `json:"policySha256"`
		TAEPolicyBindingSHA256  string `json:"taePolicyBindingSha256"`
		TAENetworkBindingSHA256 string `json:"taeNetworkBindingSha256"`
		TAEPolicyRevision       string `json:"taePolicyRevision"`
	}{
		Version: 3, Platform: document.Platform, Image: document.Images.ManagedSandbox,
		Root: managed.Environment.Root.Path, CodexRelease: managed.Environment.Compatibility.CodexRelease,
		CodexCommit: managed.Environment.Compatibility.CodexCommit, CodexSHA256: managed.Environment.Compatibility.CodexSHA256,
		LarkEnabled: larkEnabled,
		LarkCLIPath: func() string {
			if larkEnabled {
				return managedLarkCLIPath
			}
			return ""
		}(),
		LarkCLISHA256: func() string {
			if larkEnabled {
				return managed.Lark.CLISHA256
			}
			return ""
		}(),
		PolicySHA256:           managed.TAE.Policy.PolicySHA256,
		TAEPolicyBindingSHA256: managed.TAE.Policy.BindingSHA256, TAEPolicyRevision: managed.TAE.Policy.Revision,
		TAENetworkBindingSHA256: managed.TAE.NetworkEvidence.BindingSHA256,
	}
	return canonicalDigest(lock)
}

func managedPackSetDigest(managed ManagedExecutorDocument) string {
	lock := struct {
		Version                 int    `json:"version"`
		LarkEnabled             bool   `json:"larkEnabled"`
		PackID                  string `json:"packId"`
		SkillSHA256             string `json:"skillSha256"`
		Executable              string `json:"executable"`
		CLISHA256               string `json:"cliSha256"`
		PolicySHA256            string `json:"policySha256"`
		TAEPolicyBindingSHA256  string `json:"taePolicyBindingSha256"`
		TAENetworkBindingSHA256 string `json:"taeNetworkBindingSha256"`
	}{
		Version: 3, LarkEnabled: managed.Lark.Enabled,
		PackID: func() string {
			if managed.Lark.Enabled {
				return larkegresspolicy.PackID
			}
			return ""
		}(),
		SkillSHA256: func() string {
			if managed.Lark.Enabled {
				return managed.Lark.SkillSHA256
			}
			return ""
		}(),
		Executable: func() string {
			if managed.Lark.Enabled {
				return managedLarkCLIPath
			}
			return ""
		}(),
		CLISHA256: func() string {
			if managed.Lark.Enabled {
				return managed.Lark.CLISHA256
			}
			return ""
		}(),
		PolicySHA256: managed.TAE.Policy.PolicySHA256, TAEPolicyBindingSHA256: managed.TAE.Policy.BindingSHA256,
		TAENetworkBindingSHA256: managed.TAE.NetworkEvidence.BindingSHA256,
	}
	return canonicalDigest(lock)
}

func managedOwnerPolicyDigest(managed ManagedExecutorDocument) string {
	lock := struct {
		Version                 int      `json:"version"`
		LarkEnabled             bool     `json:"larkEnabled"`
		WorkspaceAllowlist      []string `json:"workspaceAllowlist"`
		EnvironmentID           string   `json:"environmentId"`
		RuntimeProfileSHA256    string   `json:"runtimeProfileSha256"`
		PackSetSHA256           string   `json:"packSetSha256"`
		TAEPolicyBindingSHA256  string   `json:"taePolicyBindingSha256"`
		TAENetworkBindingSHA256 string   `json:"taeNetworkBindingSha256"`
	}{
		Version: 3, LarkEnabled: managed.Lark.Enabled,
		WorkspaceAllowlist:      managed.WorkspaceAllowlist,
		EnvironmentID:           managed.Environment.EnvironmentID,
		RuntimeProfileSHA256:    managed.Environment.RuntimeProfileSHA256,
		PackSetSHA256:           managed.Environment.PackSetSHA256,
		TAEPolicyBindingSHA256:  managed.TAE.Policy.BindingSHA256,
		TAENetworkBindingSHA256: managed.TAE.NetworkEvidence.BindingSHA256,
	}
	return canonicalDigest(lock)
}

// managedTAENetworkEvidenceDigest binds the exact, normalized SG network
// facts relied upon by both the provider and Policy Webhook. Config validation
// runs after NetworkDocument normalization so semantically identical rule
// ordering produces one canonical digest.
func managedTAENetworkEvidenceDigest(document ConfigDocument) string {
	managed := document.Managed
	evidence := managed.TAE.NetworkEvidence
	lock := struct {
		Version                          int                  `json:"version"`
		Region                           string               `json:"region"`
		SandboxPSM                       string               `json:"sandboxPsm"`
		ClusterDomain                    string               `json:"clusterDomain"`
		DNSClusterIP                     string               `json:"dnsClusterIp"`
		DNSNamespace                     string               `json:"dnsNamespace"`
		DNSPodSelector                   map[string]string    `json:"dnsPodSelector"`
		SandboxServiceClusterIP          string               `json:"sandboxServiceClusterIp"`
		SandboxServicePort               uint16               `json:"sandboxServicePort"`
		EgressAuthorizerServiceClusterIP string               `json:"egressAuthorizerServiceClusterIp"`
		EgressAuthorizerServicePort      uint16               `json:"egressAuthorizerServicePort"`
		PolicyRevision                   string               `json:"policyRevision"`
		PolicyBindingSHA256              string               `json:"policyBindingSha256"`
		WebhookMode                      string               `json:"webhookMode"`
		WebhookPSM                       string               `json:"webhookPsm"`
		WebhookURL                       string               `json:"webhookUrl"`
		WebhookPath                      string               `json:"webhookPath"`
		ByteCloudJWTEndpoint             string               `json:"bytecloudJwtEndpoint"`
		TAEProxyURL                      string               `json:"taeProxyUrl"`
		TAEProxyNamespace                string               `json:"taeProxyNamespace"`
		TAEProxyPodApp                   string               `json:"taeProxyPodApp"`
		TAEProxyPort                     uint16               `json:"taeProxyPort"`
		SandboxExternalEgress            []EgressRuleDocument `json:"sandboxExternalEgress"`
		EgressAuthorizerExternalEgress   []EgressRuleDocument `json:"egressAuthorizerExternalEgress"`
		EgressAuthorizerIngress          []string             `json:"egressAuthorizerIngress"`
		ReportSHA256                     string               `json:"reportSha256"`
		EvidenceRef                      string               `json:"evidenceRef"`
	}{
		Version: evidence.Version, Region: managed.TAE.Region, SandboxPSM: managed.TAE.PSM,
		ClusterDomain: document.ClusterDomain, DNSClusterIP: document.Network.DNSClusterIP,
		DNSNamespace: document.Network.DNSNamespace, DNSPodSelector: document.Network.DNSPodSelector,
		SandboxServiceClusterIP:          document.Services.SandboxGateway.ClusterIP,
		SandboxServicePort:               document.Services.SandboxGateway.Port,
		EgressAuthorizerServiceClusterIP: document.Services.EgressAuthorizer.ClusterIP,
		EgressAuthorizerServicePort:      document.Services.EgressAuthorizer.Port,
		PolicyRevision:                   managed.TAE.Policy.Revision, PolicyBindingSHA256: managed.TAE.Policy.BindingSHA256,
		WebhookMode: managed.TAE.Policy.WebhookMode, WebhookPSM: managed.TAE.Policy.WebhookPSM,
		WebhookURL: managed.TAE.Policy.WebhookURL, WebhookPath: managed.TAE.Policy.WebhookPath,
		ByteCloudJWTEndpoint:           ProductionByteCloudJWTEndpoint,
		TAEProxyURL:                    ProductionTAEProxyURL,
		TAEProxyNamespace:              ProductionTAEProxyNamespace,
		TAEProxyPodApp:                 ProductionTAEProxyPodApp,
		TAEProxyPort:                   ProductionTAEProxyPort,
		SandboxExternalEgress:          document.Network.SandboxExternalEgress,
		EgressAuthorizerExternalEgress: document.Network.EgressAuthorizerExternalEgress,
		EgressAuthorizerIngress:        document.Network.EgressAuthorizerIngress,
		ReportSHA256:                   evidence.ReportSHA256, EvidenceRef: evidence.EvidenceRef,
	}
	return canonicalDigest(lock)
}

func canonicalDigest(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		panic("production managed lock contains an unsupported JSON value")
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func managedTAEPolicyBinding(managed ManagedTAEDocument) taepolicy.Binding {
	return taepolicy.Binding{
		Version: managed.Policy.Version, Region: managed.Region, SandboxPSM: managed.PSM,
		Revision: managed.Policy.Revision, PolicySHA256: managed.Policy.PolicySHA256,
		BindingSHA256: managed.Policy.BindingSHA256, PublicHost: managed.Policy.PublicHost,
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
