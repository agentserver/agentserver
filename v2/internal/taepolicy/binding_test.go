package taepolicy

import (
	"strings"
	"testing"
)

func validBinding() Binding {
	binding := Binding{
		Version: BindingVersion, Region: "sg", SandboxPSM: "prod.tae.agentserver",
		Revision: "lark-readonly-v1", PolicySHA256: strings.Repeat("a", 64),
		PublicHost: PublicHost, PublicAccess: PublicAccessWhitelist, PublicWebhookRequired: true,
		WebhookMode: "psm", WebhookPSM: "agentserver.egress-authorizer", WebhookPath: WebhookPath,
		Published: true, Approved: true, EvidenceRef: "tae-change/sg-2026-08-06",
	}
	binding.BindingSHA256 = binding.DigestHex()
	return binding
}

func TestBindingValidatesCanonicalReleaseContract(t *testing.T) {
	binding := validBinding()
	if err := binding.Validate("sg", binding.SandboxPSM, binding.PolicySHA256); err != nil {
		t.Fatal(err)
	}
	if len(binding.CanonicalJSON()) == 0 || binding.DigestHex() != binding.BindingSHA256 {
		t.Fatal("binding canonical encoding is not deterministic")
	}
}

func TestBindingValidatesSystemDefaultDirectContract(t *testing.T) {
	binding := validBinding()
	binding.PublicHost = SystemDefaultHost
	binding.PublicAccess = SystemDefaultAccess
	binding.PublicWebhookRequired = false
	binding.WebhookMode = ""
	binding.WebhookPSM = ""
	binding.WebhookURL = ""
	binding.WebhookPath = ""
	binding.EvidenceRef = "tae-system-default-policy/group:system.out.limit"
	binding.BindingSHA256 = binding.DigestHex()
	if err := binding.Validate("sg", binding.SandboxPSM, binding.PolicySHA256); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Binding){
		"exact host instead of wildcard": func(value *Binding) { value.PublicHost = PublicHost },
		"custom access":                  func(value *Binding) { value.PublicAccess = PublicAccessWhitelist },
		"webhook mode":                   func(value *Binding) { value.WebhookMode = "url" },
		"webhook psm":                    func(value *Binding) { value.WebhookPSM = "agentserver.egress-authorizer" },
		"webhook url":                    func(value *Binding) { value.WebhookURL = "https://egress.example/v1/policy" },
		"webhook path":                   func(value *Binding) { value.WebhookPath = WebhookPath },
	} {
		t.Run(name, func(t *testing.T) {
			changed := binding
			mutate(&changed)
			changed.BindingSHA256 = changed.DigestHex()
			if err := changed.Validate("sg", changed.SandboxPSM, changed.PolicySHA256); err == nil {
				t.Fatal("direct binding with webhook or custom policy fields was accepted")
			}
		})
	}
}

func TestBindingRejectsUnpublishedOrMismatchedWebhook(t *testing.T) {
	cases := map[string]func(*Binding){
		"unpublished": func(binding *Binding) { binding.Published = false },
		"unapproved":  func(binding *Binding) { binding.Approved = false },
		"wrong host":  func(binding *Binding) { binding.PublicHost = "example.com" },
		"mixed direct host": func(binding *Binding) {
			binding.PublicWebhookRequired = false
		},
		"wrong path":   func(binding *Binding) { binding.WebhookPath = "/v1/other" },
		"wrong digest": func(binding *Binding) { binding.BindingSHA256 = strings.Repeat("b", 64) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			binding := validBinding()
			mutate(&binding)
			if err := binding.Validate("sg", binding.SandboxPSM, binding.PolicySHA256); err == nil {
				t.Fatal("invalid TAE policy binding was accepted")
			}
		})
	}
}

func TestBindingRejectsInsecureWebhookURL(t *testing.T) {
	binding := validBinding()
	binding.WebhookMode = "url"
	binding.WebhookPSM = ""
	binding.WebhookURL = "http://egress.example/v1/policy"
	binding.BindingSHA256 = binding.DigestHex()
	if err := binding.Validate("sg", binding.SandboxPSM, binding.PolicySHA256); err == nil {
		t.Fatal("insecure webhook URL was accepted")
	}
}

func TestBindingDraftRequiresExactFailClosedLifecycle(t *testing.T) {
	binding := validBinding()
	binding.Published = false
	binding.Approved = false
	binding.EvidenceRef = ""
	binding.BindingSHA256 = binding.DigestHex()
	if err := binding.ValidateDraft("sg", binding.SandboxPSM, binding.PolicySHA256); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Binding){
		"published": func(value *Binding) { value.Published = true },
		"approved":  func(value *Binding) { value.Approved = true },
		"evidence":  func(value *Binding) { value.EvidenceRef = "tae-change/early" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := binding
			mutate(&changed)
			changed.BindingSHA256 = changed.DigestHex()
			if err := changed.ValidateDraft("sg", changed.SandboxPSM, changed.PolicySHA256); err == nil {
				t.Fatal("unsafe draft lifecycle was accepted")
			}
		})
	}
}
