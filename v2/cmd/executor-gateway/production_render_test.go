package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/executorgateway"
	"github.com/agentserver/agentserver/v2/internal/productiondeploytest"
)

func TestProductionRendererExecutorEnvironmentMatchesCommandContract(t *testing.T) {
	environment, err := productiondeploytest.ExampleDeploymentEnvironment("executor-gateway")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		gatewayListenAddressEnvironment,
		gatewayPublicListenAddressEnvironment,
		gatewayTLSCertificateEnvironment,
		gatewayTLSKeyEnvironment,
		gatewayCoreURLEnvironment,
		gatewayCoreCAEnvironment,
		gatewayCoreClientCertificateEnvironment,
		gatewayCoreClientKeyEnvironment,
		gatewayCoreServerNameEnvironment,
		gatewaySPIFFEIdentityEnvironment,
		gatewayExecutorIDEnvironment,
		gatewayCapabilityIssuerEnvironment,
		gatewayCapabilityKeyringEnvironment,
		gatewayExecutionPolicyVersionEnvironment,
		gatewayShellPolicyDecisionEnvironment,
		gatewayReadPolicyDecisionEnvironment,
		gatewaySandboxGatewayCAEnvironment,
		gatewaySandboxCapabilityIssuerEnvironment,
		gatewaySandboxCapabilityKeyIDEnvironment,
		gatewaySandboxCapabilityKeyEnvironment,
		gatewaySandboxFencerIssuerEnvironment,
		gatewaySandboxFencerKeyIDEnvironment,
		gatewaySandboxFencerKeyEnvironment,
		gatewayManagedTAEPSMEnvironment,
		gatewayTAEWebhookRequiredEnvironment,
		gatewayManagedProfilesEnvironment,
	}
	slices.Sort(want)
	if got := environment.Names(); !slices.Equal(got, want) {
		t.Fatalf("rendered executor-gateway environment names = %v, want %v", got, want)
	}
	for _, name := range environment.Names() {
		if strings.HasPrefix(name, "AGENTSERVER_V2_DEV_") {
			t.Fatalf("production renderer emitted development authority %s", name)
		}
	}
	if err := validateGatewayHTTPSOrigin(environment.Get(gatewayCoreURLEnvironment), gatewayCoreURLEnvironment); err != nil {
		t.Fatalf("executor-gateway rejected rendered Core URL: %v", err)
	}
	if err := validateGatewaySPIFFEIdentity(environment.Get(gatewaySPIFFEIdentityEnvironment)); err != nil {
		t.Fatalf("executor-gateway rejected rendered SPIFFE identity: %v", err)
	}
	if err := validateGatewayExecutorID(gatewayExecutorIDEnvironment, environment.Get(gatewayExecutorIDEnvironment)); err != nil {
		t.Fatalf("executor-gateway rejected rendered executor ID: %v", err)
	}
	if _, err := executorgateway.NewPermissionModeExecutionPolicyResolver(
		environment.Get(gatewayExecutionPolicyVersionEnvironment),
		map[string]string{
			"shell":     environment.Get(gatewayShellPolicyDecisionEnvironment),
			"read_file": environment.Get(gatewayReadPolicyDecisionEnvironment),
		},
	); err != nil {
		t.Fatalf("executor-gateway rejected rendered policy: %v", err)
	}
	profiles, err := parseManagedSandboxGatewayProfiles(
		[]byte(environment.Get(gatewayManagedProfilesEnvironment)), gatewayServeProduction,
	)
	if err != nil {
		t.Fatalf("executor-gateway rejected rendered managed sandbox profiles: %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("executor-gateway loaded %d managed sandbox profiles, want 1", len(profiles))
	}
}
