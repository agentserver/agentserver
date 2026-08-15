package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/corecredentials"
	"github.com/agentserver/agentserver/v2/internal/productiondeploytest"
)

func TestProductionRendererCoreEnvironmentMatchesCommandContract(t *testing.T) {
	environment, err := productiondeploytest.ExampleDeploymentEnvironment("agentserver-core")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		databaseURLEnvironment,
		coreListenAddressEnvironment,
		coreTLSCertificateEnvironment,
		coreTLSKeyEnvironment,
		coreClientCAEnvironment,
		coreManagedExecutorEnabledEnvironment,
		coreTAEWebhookRequiredEnvironment,
		coreGatewayIdentityEnvironment,
		coreHarnessPoolIdentityEnvironment,
		coreSandboxGatewayIdentitiesEnvironment,
		coreBrowserIdentityEnvironment,
		corePlatformIdentityEnvironment,
		coreLLMProxyIdentityEnvironment,
		coreHydraIntrospectionEnvironment,
		coreHydraAdminEnvironment,
		coreHydraPublicOriginEnvironment,
		coreHydraIssuerEnvironment,
		coreHydraPlatformClientEnvironment,
		coreHydraBrowserClientEnvironment,
		coreHydraCAEnvironment,
		coreHydraServerNameEnvironment,
		coreExternalOIDCIssuerEnvironment,
		coreExternalOIDCClientEnvironment,
		coreExternalOIDCSecretEnvironment,
		coreExternalOIDCRedirectEnvironment,
		coreLoginTransactionKeyEnvironment,
		coreRunCursorKeyEnvironment,
		coreRunPolicyVersionEnvironment,
		coreRunAllowedToolsEnvironment,
		coreCapabilityIssuerEnvironment,
		coreCapabilityKeyIDEnvironment,
		coreCapabilityPrivateKeyEnvironment,
		coreCapabilityKeyringEnvironment,
		coreProductionExecutorEnvironment,
		coreLLMGatewaySealingKeyringEnvironment,
		coreLLMGatewayRedirectURLEnvironment,
		coreMaxRunDurationEnvironment,
		coreMaxApprovalTTLEnvironment,
		coreCapabilityExpiryGraceEnvironment,
		coreEnrollmentKeyEnvironment,
		coreEnrollmentTTLEnvironment,
		coreCredentialSealingKeyringEnvironment,
		coreManagedTAEPSMEnvironment,
		coreManagedSandboxProfilesEnvironment,
		coreLarkDeviceAppIDEnvironment,
		coreLarkDeviceAppSecretEnvironment,
		coreLarkDeviceScopesEnvironment,
		coreByteCloudDeviceAPIEnvironment,
		"AGENTSERVER_V2_OBJECT_PREFIX",
		"AGENTSERVER_V2_S3_BUCKET",
		"AGENTSERVER_V2_S3_REGION",
		"AGENTSERVER_V2_S3_ENDPOINT",
		"AGENTSERVER_V2_S3_USE_PATH_STYLE",
		"AGENTSERVER_V2_S3_ACCESS_KEY_ID",
		"AGENTSERVER_V2_S3_SECRET_ACCESS_KEY",
	}
	slices.Sort(want)
	if got := environment.Names(); !slices.Equal(got, want) {
		t.Fatalf("rendered Core environment names = %v, want %v", got, want)
	}
	for _, name := range environment.Names() {
		if strings.HasPrefix(name, "AGENTSERVER_V2_DEV_") {
			t.Fatalf("production renderer emitted development authority %s", name)
		}
	}
	for _, name := range []string{
		databaseURLEnvironment,
		coreExternalOIDCSecretEnvironment,
		coreLoginTransactionKeyEnvironment,
		coreRunCursorKeyEnvironment,
		coreLarkDeviceAppIDEnvironment,
		coreLarkDeviceAppSecretEnvironment,
		"AGENTSERVER_V2_S3_ACCESS_KEY_ID",
		"AGENTSERVER_V2_S3_SECRET_ACCESS_KEY",
	} {
		if environment.Secrets[name].Name == "" || environment.Secrets[name].Key == "" {
			t.Fatalf("Core secret environment %s is not sourced from one exact Secret key", name)
		}
	}
	if got := environment.Get(coreLarkDeviceScopesEnvironment); got != corecredentials.DefaultManagedLarkScopes {
		t.Fatalf("rendered Lark device scopes = %q, want %q", got, corecredentials.DefaultManagedLarkScopes)
	}
	if got := environment.Get(coreByteCloudDeviceAPIEnvironment); got != corecredentials.DefaultByteCloudDeviceAPIBaseURL {
		t.Fatalf("rendered ByteCloud device API = %q, want %q", got, corecredentials.DefaultByteCloudDeviceAPIBaseURL)
	}
}
