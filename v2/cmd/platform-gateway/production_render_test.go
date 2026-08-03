package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/productiondeploytest"
)

func TestProductionRendererPlatformEnvironmentMatchesCommandContract(t *testing.T) {
	environment, err := productiondeploytest.ExampleDeploymentEnvironment("platform-gateway")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		platformListenAddressEnvironment,
		platformPublicOriginEnvironment,
		platformBrowserOriginEnvironment,
		platformCoreURLEnvironment,
		platformCoreCAEnvironment,
		platformCoreClientCertificateEnvironment,
		platformCoreClientKeyEnvironment,
		platformCoreServerNameEnvironment,
		platformHydraPublicUpstreamEnvironment,
		platformHydraCAEnvironment,
		platformHydraServerNameEnvironment,
		platformOAuthClientIDEnvironment,
		platformOAuthAudienceEnvironment,
		platformOAuthScopesEnvironment,
	}
	slices.Sort(want)
	if got := environment.Names(); !slices.Equal(got, want) {
		t.Fatalf("rendered platform-gateway environment names = %v, want %v", got, want)
	}
	for _, name := range environment.Names() {
		if strings.HasPrefix(name, "AGENTSERVER_V2_DEV_") {
			t.Fatalf("production renderer emitted development authority %s", name)
		}
	}
	if got := environment.Get(platformPublicOriginEnvironment); got != "https://agent.byted.bps.dev" {
		t.Fatalf("rendered Platform origin = %q", got)
	}
	if got := environment.Get(platformBrowserOriginEnvironment); got != "https://browser.byted.bps.dev" {
		t.Fatalf("rendered Browser origin = %q", got)
	}
	if err := validatePlatformCoreURL(environment.Get(platformCoreURLEnvironment)); err != nil {
		t.Fatalf("platform-gateway rejected rendered Core URL: %v", err)
	}
	if _, err := validatePlatformOAuthAuthority(
		environment.Get(platformOAuthClientIDEnvironment),
		environment.Get(platformOAuthAudienceEnvironment),
		environment.Get(platformOAuthScopesEnvironment),
	); err != nil {
		t.Fatalf("platform-gateway rejected rendered OAuth authority: %v", err)
	}
}
