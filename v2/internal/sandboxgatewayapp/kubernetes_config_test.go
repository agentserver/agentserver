package sandboxgatewayapp

import (
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/executionbackend"
)

func kubernetesConfigFixture() map[string]string {
	return map[string]string{
		ListenAddressEnvironment: "0.0.0.0:8443", TLSCertificateEnvironment: "/material/tls.crt", TLSKeyEnvironment: "/material/tls.key", ClientCAEnvironment: "/material/ca.crt",
		SPIFFEIdentityEnvironment: "spiffe://agentserver.test/ns/agentserver/sa/sandbox-gateway-k8s", ExecutorIdentityEnvironment: "spiffe://agentserver.test/ns/agentserver/sa/executor-gateway", HarnessIdentityEnvironment: "spiffe://agentserver.test/ns/agentserver/sa/harness-pool",
		CoreURLEnvironment: "https://core.agentserver.internal:8443", CoreServerNameEnvironment: "core.agentserver.internal", CoreCAEnvironment: "/material/ca.crt", CoreCertificateEnvironment: "/material/tls.crt", CoreKeyEnvironment: "/material/tls.key",
		CapabilityKeyringEnvironment: "/material/capability-keyring.json", ProviderModeEnvironment: "k8s", "AGENTSERVER_V2_SANDBOX_REGION": "sg", "AGENTSERVER_V2_SANDBOX_SCOPE": "sg-managed-cli", EnsureTimeoutEnvironment: "3m",
		WorkspaceAllowlistEnvironment: "10000000-0000-4000-8000-000000000001",
	}
}

func TestKubernetesConfigDoesNotRequireTAEAuthority(t *testing.T) {
	env := kubernetesConfigFixture()
	c, err := LoadProductionConfig(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if c.ProviderKind != executionbackend.KindKubernetes || c.ProviderRegion != "sg" || c.ProviderPSM != "sg-managed-cli" || c.EnsureTimeout != 3*time.Minute || c.TAEPolicy.SandboxPSM != "" {
		t.Fatalf("unexpected config: %+v", c)
	}
	for _, name := range []string{ProviderPSMEnvironment, ProviderRegionEnvironment, TAEPolicyRevisionEnvironment} {
		env[name] = "legacy"
		if _, err := LoadProductionConfig(func(k string) string { return env[k] }); err == nil {
			t.Fatalf("Kubernetes accepted %s", name)
		}
		delete(env, name)
	}
}

func TestCNKubernetesConfigUsesServerOnlyExternalTLS(t *testing.T) {
	env := kubernetesConfigFixture()
	env["AGENTSERVER_V2_SANDBOX_REGION"] = "cn"
	env[CoreExternalTokenEnvironment] = "cross-cluster-capability"
	env[ExternalTLSEnvironment] = "true"
	delete(env, CoreCAEnvironment)
	delete(env, CoreCertificateEnvironment)
	delete(env, CoreKeyEnvironment)
	delete(env, ClientCAEnvironment)
	c, err := LoadProductionConfig(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if c.ProviderRegion != "cn" || !c.ExternalTLS || c.CoreExternalToken == "" {
		t.Fatalf("unexpected CN external config: %+v", c)
	}
	if _, err := CoreHTTPClient(c); err == nil {
		// CoreHTTPClient should not require the omitted client certificate or CA
		// files in server-only cross-cluster mode. It may still be usable with
		// the platform trust store, so only construction is asserted here.
	} else {
		t.Fatalf("server-only Core client still requires mTLS material: %v", err)
	}
}
