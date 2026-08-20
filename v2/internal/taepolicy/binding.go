// Package taepolicy contains the release-time contract for the TAE network
// policy used by the managed executor.  TAE binds a network policy to a
// Sandbox/PSM (not to an individual Session create request), so the contract
// is deliberately an attestation/lock shared by every service that relies on
// that policy.  It does not pretend to provision a policy through the Session
// API; the published/approved/evidence fields are the explicit hand-off to
// the TAE control-plane release process.
package taepolicy

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/agentserver/agentserver/v2/internal/larkegresspolicy"
)

const (
	BindingVersion        = 1
	WebhookProtocol       = "v1"
	WebhookPath           = "/v1/policy"
	PublicHost            = larkegresspolicy.OpenAPIHost
	PublicAccessWhitelist = "whitelist"
	SystemDefaultHost     = "*.feishu.cn"
	SystemDefaultAccess   = "system_default"
)

var revisionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// Binding is the non-secret description of the TAE policy which must already
// be published before managed sessions are admitted.
type Binding struct {
	Version               int    `json:"version"`
	Region                string `json:"region"`
	SandboxPSM            string `json:"sandboxPsm"`
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

// Validate checks the exact production binding expected by AgentServer.  The
// expected values are supplied by the caller so this package can also be used
// by the provider-linked module without importing deployment configuration.
func (binding Binding) Validate(expectedRegion, expectedSandboxPSM, expectedPolicySHA256 string) error {
	if err := binding.validateShape(expectedRegion, expectedSandboxPSM, expectedPolicySHA256); err != nil {
		return err
	}
	if !binding.Published || !binding.Approved {
		return errors.New("TAE policy must be published and security-approved before managed execution")
	}
	if !bounded(binding.EvidenceRef, 1024) {
		return errors.New("TAE policy evidence reference is required")
	}
	return nil
}

// ValidateDraft validates the fail-closed lifecycle before the managed
// runtime is activated. A webhook profile may expose only its deny-only
// bootstrap; a direct profile exposes no webhook authority. A draft cannot
// claim publication or approval and cannot carry evidence from a future review.
func (binding Binding) ValidateDraft(expectedRegion, expectedSandboxPSM, expectedPolicySHA256 string) error {
	if err := binding.validateShape(expectedRegion, expectedSandboxPSM, expectedPolicySHA256); err != nil {
		return err
	}
	if binding.Published || binding.Approved || binding.EvidenceRef != "" {
		return errors.New("TAE draft policy must be unpublished, unapproved, and have no evidence reference")
	}
	return nil
}

func (binding Binding) validateShape(expectedRegion, expectedSandboxPSM, expectedPolicySHA256 string) error {
	if binding.Version != BindingVersion {
		return fmt.Errorf("TAE policy binding version must be %d", BindingVersion)
	}
	if binding.Region != expectedRegion || binding.Region == "" {
		return fmt.Errorf("TAE policy binding region must be %q", expectedRegion)
	}
	if !bounded(binding.SandboxPSM, 256) || binding.SandboxPSM != expectedSandboxPSM {
		return errors.New("TAE policy binding sandbox PSM does not match the configured provider")
	}
	if !revisionPattern.MatchString(binding.Revision) {
		return errors.New("TAE policy binding revision is invalid")
	}
	if !digest(binding.PolicySHA256) || binding.PolicySHA256 != expectedPolicySHA256 {
		return errors.New("TAE policy binding policy digest does not match the compiled Lark policy")
	}
	if !binding.PublicWebhookRequired {
		if binding.PublicHost != SystemDefaultHost || binding.PublicAccess != SystemDefaultAccess {
			return errors.New("TAE direct policy binding must use the system-default *.feishu.cn allowlist")
		}
		if binding.WebhookMode != "" || binding.WebhookPSM != "" || binding.WebhookURL != "" || binding.WebhookPath != "" {
			return errors.New("TAE direct policy binding must not contain webhook configuration")
		}
		return nil
	}
	if binding.PublicHost != PublicHost || binding.PublicAccess != PublicAccessWhitelist {
		return errors.New("TAE webhook policy binding must whitelist the exact public Lark host")
	}
	if binding.WebhookPath != WebhookPath {
		return fmt.Errorf("TAE policy webhook path must be exactly %s", WebhookPath)
	}
	switch binding.WebhookMode {
	case "psm":
		if !bounded(binding.WebhookPSM, 256) || binding.WebhookPSM == binding.SandboxPSM || binding.WebhookURL != "" {
			return errors.New("TAE PSM webhook binding is incomplete or points at the sandbox PSM")
		}
	case "url":
		if binding.WebhookPSM != "" || !validWebhookURL(binding.WebhookURL) {
			return errors.New("TAE URL webhook binding is incomplete")
		}
	default:
		return errors.New("TAE policy webhook mode must be psm or url")
	}
	return nil
}

func validWebhookURL(raw string) bool {
	if !bounded(raw, 2048) {
		return false
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" || parsed.ForceQuery ||
		parsed.Path != WebhookPath || net.ParseIP(parsed.Hostname()) != nil {
		return false
	}
	return parsed.String() == raw
}

func digest(value string) bool {
	if len(value) != sha256.Size*2 || strings.Trim(value, "0") == "" {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func bounded(value string, maximum int) bool {
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
