// Package taenetworkreport defines the immutable, secret-free evidence
// produced by the SG TAE network probe. The provider-linked probe lives in a
// nested module, while release activation lives in the main module; keeping
// the wire contract here lets both sides validate exactly the same document.
package taenetworkreport

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/agentserver/agentserver/v2/internal/managedsandboxprofile"
	"github.com/agentserver/agentserver/v2/internal/taeimage"
)

const (
	CurrentVersion = 5
	Kind           = "agentserver.tae.network-report"
	maximumBytes   = int64(256 * 1024)
)

var (
	digestPattern    = regexp.MustCompile(`^[0-9a-f]{64}$`)
	checkNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	errorCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	podUIDPattern    = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	taeIDPattern     = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
)

type Report struct {
	SchemaVersion    int           `json:"schemaVersion"`
	Kind             string        `json:"kind"`
	StartedAt        time.Time     `json:"startedAt"`
	FinishedAt       time.Time     `json:"finishedAt"`
	Passed           bool          `json:"passed"`
	CleanupConfirmed bool          `json:"cleanupConfirmed"`
	Source           Source        `json:"source"`
	Configuration    Configuration `json:"configuration"`
	Checks           []Check       `json:"checks"`
}

type Source struct {
	Namespace      string `json:"namespace"`
	PodName        string `json:"podName"`
	PodUID         string `json:"podUid"`
	NodeName       string `json:"nodeName"`
	ServiceAccount string `json:"serviceAccount"`
}

type Configuration struct {
	DeploymentConfigSHA256 string `json:"deploymentConfigSha256"`
	Region                 string `json:"region"`
	PSM                    string `json:"psm"`
	PolicyRevision         string `json:"policyRevision"`
	ByteCloudSite          string `json:"bytecloudSite"`
	JWTEndpoint            string `json:"jwtEndpoint"`
	ProxyURL               string `json:"proxyUrl"`
	ControlPlaneHost       string `json:"controlPlaneHost"`
	DataPlaneDomainSuffix  string `json:"dataPlaneDomainSuffix"`
	SandboxImage           string `json:"sandboxImage"`
	SandboxID              string `json:"sandboxId"`
	SandboxRevisionID      string `json:"sandboxRevisionId"`
	LarkCLIVersion         string `json:"larkCliVersion"`
	LarkCLISHA256          string `json:"larkCliSha256"`
	LarkSkillSHA256        string `json:"larkSkillSha256"`
	ManagedSkillSHA256     string `json:"managedSkillSha256"`
	BkectlSourceRevision   string `json:"bkectlSourceRevision"`
	BkectlCLISHA256        string `json:"bkectlCliSha256"`
	BkectlSkillPackSHA256  string `json:"bkectlSkillPackSha256"`
	ConnectivityAttempts   int    `json:"connectivityAttempts"`
	LifecycleAttempts      int    `json:"lifecycleAttempts"`
}

type Check struct {
	Name            string         `json:"name"`
	Attempts        int            `json:"attempts"`
	Succeeded       int            `json:"succeeded"`
	Failed          int            `json:"failed"`
	DurationsMillis []int64        `json:"durationsMillis"`
	Errors          map[string]int `json:"errors,omitempty"`
	BytesRead       int64          `json:"bytesRead,omitempty"`
}

// Marshal returns the only accepted report representation: compact JSON with
// one trailing LF. A probe emits this as a single log line, so unrelated SDK
// diagnostics can be excluded before the artifact is hashed.
func Marshal(report Report) ([]byte, error) {
	if err := Validate(report); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(report)
	if err != nil {
		return nil, fmt.Errorf("encode TAE network report: %w", err)
	}
	return append(raw, '\n'), nil
}

// Parse rejects alternate JSON encodings. This makes the digest printed by a
// release workstation unambiguous and detects log prefixes/suffixes or manual
// edits before the report is accepted as production evidence.
func Parse(raw []byte) (Report, error) {
	if len(raw) == 0 || int64(len(raw)) > maximumBytes {
		return Report{}, fmt.Errorf("TAE network report must contain between 1 and %d bytes", maximumBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var report Report
	if err := decoder.Decode(&report); err != nil {
		return Report{}, fmt.Errorf("decode TAE network report: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Report{}, errors.New("TAE network report must contain exactly one JSON value")
	}
	canonical, err := Marshal(report)
	if err != nil {
		return Report{}, err
	}
	if !bytes.Equal(raw, canonical) {
		return Report{}, errors.New("TAE network report is not in canonical single-line form")
	}
	return report, nil
}

func Load(path string) (Report, []byte, error) {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsRune(path, 0) {
		return Report{}, nil, errors.New("TAE network report path must be absolute and clean")
	}
	file, err := os.Open(path)
	if err != nil {
		return Report{}, nil, fmt.Errorf("open TAE network report: %w", err)
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil {
		return Report{}, nil, fmt.Errorf("inspect TAE network report: %w", err)
	}
	if !before.Mode().IsRegular() || before.Mode().Perm()&0o022 != 0 || before.Size() < 1 || before.Size() > maximumBytes {
		return Report{}, nil, fmt.Errorf("TAE network report must be a regular file between 1 and %d bytes not writable by group or other", maximumBytes)
	}
	raw, err := io.ReadAll(io.LimitReader(file, maximumBytes+1))
	if err != nil {
		return Report{}, nil, fmt.Errorf("read TAE network report: %w", err)
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() ||
		!before.ModTime().Equal(after.ModTime()) || int64(len(raw)) != before.Size() {
		return Report{}, nil, errors.New("TAE network report changed while it was being read")
	}
	report, err := Parse(raw)
	if err != nil {
		return Report{}, nil, err
	}
	return report, raw, nil
}

func SHA256(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func Validate(report Report) error {
	if report.SchemaVersion != CurrentVersion || report.Kind != Kind {
		return fmt.Errorf("TAE network report must use schemaVersion %d and kind %q", CurrentVersion, Kind)
	}
	if report.StartedAt.IsZero() || report.FinishedAt.IsZero() || report.StartedAt.Location() != time.UTC ||
		report.FinishedAt.Location() != time.UTC || report.FinishedAt.Before(report.StartedAt) ||
		report.FinishedAt.Sub(report.StartedAt) > 2*time.Hour {
		return errors.New("TAE network report timestamps must be UTC and span at most two hours")
	}
	for name, value := range map[string]string{
		"source.namespace": report.Source.Namespace, "source.podName": report.Source.PodName,
		"source.nodeName": report.Source.NodeName, "source.serviceAccount": report.Source.ServiceAccount,
		"configuration.region": report.Configuration.Region, "configuration.psm": report.Configuration.PSM,
		"configuration.policyRevision":        report.Configuration.PolicyRevision,
		"configuration.bytecloudSite":         report.Configuration.ByteCloudSite,
		"configuration.jwtEndpoint":           report.Configuration.JWTEndpoint,
		"configuration.controlPlaneHost":      report.Configuration.ControlPlaneHost,
		"configuration.dataPlaneDomainSuffix": report.Configuration.DataPlaneDomainSuffix,
		"configuration.larkCliVersion":        report.Configuration.LarkCLIVersion,
		"configuration.bkectlSourceRevision":  report.Configuration.BkectlSourceRevision,
	} {
		if !boundedText(value, 1, 1024) {
			return fmt.Errorf("TAE network report %s is invalid", name)
		}
	}
	if report.Configuration.ProxyURL != "" && !boundedText(report.Configuration.ProxyURL, 1, 1024) {
		return errors.New("TAE network report configuration.proxyUrl is invalid")
	}
	if !managedsandboxprofile.ValidRegion(report.Configuration.Region) {
		return errors.New("TAE network report configuration.region is unsupported")
	}
	if report.Configuration.ControlPlaneHost != "controlplane."+report.Configuration.DataPlaneDomainSuffix {
		return errors.New("TAE network report control/data-plane authorities do not match")
	}
	if report.Configuration.Region == managedsandboxprofile.RegionBOE && report.Configuration.ProxyURL != "" {
		return errors.New("TAE BOE network report must prove direct routing")
	}
	if report.Configuration.Region != managedsandboxprofile.RegionBOE && report.Configuration.ProxyURL == "" {
		return errors.New("TAE non-BOE network report must bind a regional proxy")
	}
	if !podUIDPattern.MatchString(report.Source.PodUID) {
		return errors.New("TAE network report source.podUid must be a canonical lowercase UUID")
	}
	for name, value := range map[string]string{
		"deploymentConfigSha256": report.Configuration.DeploymentConfigSHA256,
		"larkCliSha256":          report.Configuration.LarkCLISHA256,
		"larkSkillSha256":        report.Configuration.LarkSkillSHA256,
		"managedSkillSha256":     report.Configuration.ManagedSkillSHA256,
		"bkectlCliSha256":        report.Configuration.BkectlCLISHA256,
		"bkectlSkillPackSha256":  report.Configuration.BkectlSkillPackSHA256,
	} {
		if !digestPattern.MatchString(value) || strings.Trim(value, "0") == "" {
			return fmt.Errorf("TAE network report configuration.%s must be a non-zero lowercase SHA-256", name)
		}
	}
	if len(report.Configuration.BkectlSourceRevision) != 40 ||
		strings.Trim(report.Configuration.BkectlSourceRevision, "0123456789abcdef") != "" {
		return errors.New("TAE network report configuration.bkectlSourceRevision must be a lowercase 40-character Git SHA")
	}
	if err := taeimage.ValidateContentTag(report.Configuration.SandboxImage); err != nil {
		return fmt.Errorf("TAE network report configuration.sandboxImage: %w", err)
	}
	if !taeIDPattern.MatchString(report.Configuration.SandboxID) {
		return errors.New("TAE network report configuration.sandboxId must be a canonical lowercase TAE identity")
	}
	if !taeIDPattern.MatchString(report.Configuration.SandboxRevisionID) {
		return errors.New("TAE network report configuration.sandboxRevisionId must be a canonical lowercase TAE identity")
	}
	if report.Configuration.ConnectivityAttempts < 1 || report.Configuration.ConnectivityAttempts > 100 ||
		report.Configuration.LifecycleAttempts < 1 || report.Configuration.LifecycleAttempts > 5 {
		return errors.New("TAE network report attempt counts are outside the reviewed bounds")
	}
	if len(report.Checks) < 2 || len(report.Checks) > 64 {
		return errors.New("TAE network report must contain between 2 and 64 checks")
	}
	seen := make(map[string]struct{}, len(report.Checks))
	allPassed := report.CleanupConfirmed
	for index := range report.Checks {
		check := report.Checks[index]
		if !checkNamePattern.MatchString(check.Name) {
			return fmt.Errorf("TAE network report check %d has an invalid name", index)
		}
		if _, duplicate := seen[check.Name]; duplicate {
			return fmt.Errorf("TAE network report check %q is duplicated", check.Name)
		}
		seen[check.Name] = struct{}{}
		if check.Attempts < 1 || check.Attempts > 100 || check.Succeeded < 0 || check.Failed < 0 ||
			check.Succeeded+check.Failed != check.Attempts || len(check.DurationsMillis) != check.Attempts {
			return fmt.Errorf("TAE network report check %q has inconsistent counters", check.Name)
		}
		for _, duration := range check.DurationsMillis {
			if duration < 0 || duration > int64((15*time.Minute)/time.Millisecond) {
				return fmt.Errorf("TAE network report check %q has an invalid duration", check.Name)
			}
		}
		errorTotal := 0
		for code, count := range check.Errors {
			if !errorCodePattern.MatchString(code) || count < 1 || count > check.Failed {
				return fmt.Errorf("TAE network report check %q has an invalid error counter", check.Name)
			}
			errorTotal += count
		}
		if errorTotal != check.Failed || check.BytesRead < 0 || check.BytesRead > 1024*1024*1024 {
			return fmt.Errorf("TAE network report check %q has inconsistent results", check.Name)
		}
		if check.Failed != 0 {
			allPassed = false
		}
	}
	if report.Passed != allPassed {
		return errors.New("TAE network report passed flag does not match checks and cleanup")
	}
	return nil
}

func boundedText(value string, minimum, maximum int) bool {
	if len(value) < minimum || len(value) > maximum || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}
