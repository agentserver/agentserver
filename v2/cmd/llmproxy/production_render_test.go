package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/productiondeploytest"
)

func TestProductionRendererLLMProxyEnvironmentPassesCommandLoader(t *testing.T) {
	environment, err := productiondeploytest.ExampleDeploymentEnvironment("llmproxy")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		llmProxyListenAddressEnvironment,
		llmProxyTLSCertificateEnvironment,
		llmProxyTLSKeyEnvironment,
		llmProxySPIFFEIdentityEnvironment,
		llmProxyCoreURLEnvironment,
		llmProxyCoreCAEnvironment,
		llmProxyCoreServerNameEnvironment,
		llmProxyCapabilityIssuerEnvironment,
		llmProxyCapabilityKeyringEnvironment,
		llmProxyModelEnvironment,
		llmProxyModelProviderEnvironment,
		llmProxyUpstreamResponsesURLEnvironment,
		llmProxyUpstreamCAEnvironment,
		llmProxyUpstreamAuthHeaderEnvironment,
		llmProxyUpstreamCredentialEnvironment,
	}
	slices.Sort(want)
	if got := environment.Names(); !slices.Equal(got, want) {
		t.Fatalf("rendered llmproxy environment names = %v, want %v", got, want)
	}
	for _, name := range environment.Names() {
		if strings.HasPrefix(name, "AGENTSERVER_V2_DEV_") {
			t.Fatalf("production renderer emitted development authority %s", name)
		}
	}
	loaded, err := loadLLMProxyConfig(environment.Get)
	if err != nil {
		t.Fatalf("llmproxy rejected rendered production environment: %v", err)
	}
	if loaded.model != environment.Get(llmProxyModelEnvironment) || loaded.provider != environment.Get(llmProxyModelProviderEnvironment) {
		t.Fatalf("llmproxy loaded different production route: %+v", loaded)
	}
}
