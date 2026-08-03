package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/productiondeploytest"
)

func TestProductionRendererBrowserEnvironmentMatchesCommandContract(t *testing.T) {
	environment, err := productiondeploytest.ExampleDeploymentEnvironment("browser-gateway")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		browserListenAddressEnvironment,
		browserFrontendOriginEnvironment,
		browserAPIOriginEnvironment,
		browserCoreURLEnvironment,
		browserCoreCAEnvironment,
		browserCoreClientCertificateEnvironment,
		browserCoreClientKeyEnvironment,
		browserCoreServerNameEnvironment,
		browserHydraPublicUpstreamEnvironment,
		browserHydraCAEnvironment,
		browserHydraServerNameEnvironment,
		browserOAuthClientIDEnvironment,
		browserOAuthAudienceEnvironment,
		browserOAuthScopesEnvironment,
	}
	slices.Sort(want)
	if got := environment.Names(); !slices.Equal(got, want) {
		t.Fatalf("rendered browser environment names = %v, want %v", got, want)
	}
	for _, name := range environment.Names() {
		if strings.HasPrefix(name, "AGENTSERVER_V2_DEV_") {
			t.Fatalf("production renderer emitted development authority %s", name)
		}
	}
	if err := validateBrowserCoreURL(environment.Get(browserCoreURLEnvironment)); err != nil {
		t.Fatalf("browser rejected rendered Core URL: %v", err)
	}
	if _, err := validateBrowserOAuthAuthority(
		environment.Get(browserOAuthAudienceEnvironment),
		environment.Get(browserOAuthScopesEnvironment),
	); err != nil {
		t.Fatalf("browser rejected rendered OAuth authority: %v", err)
	}
	frontend, api, split, err := browserPublicOrigins(environment.Get)
	if err != nil || !split || frontend != "https://agent.byted.bps.dev" || api != "https://browser-gateway.byted.bps.dev" {
		t.Fatalf("browser rejected rendered split origins: %q %q split=%v error=%v", frontend, api, split, err)
	}
}
