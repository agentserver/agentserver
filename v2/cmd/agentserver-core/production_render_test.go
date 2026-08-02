package main

import (
	"slices"
	"strings"
	"testing"

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
		coreGatewayIdentityEnvironment,
		coreHarnessPoolIdentityEnvironment,
		coreBrowserIdentityEnvironment,
		coreLLMProxyIdentityEnvironment,
		coreHydraIntrospectionEnvironment,
		coreHydraAdminEnvironment,
		coreHydraPublicOriginEnvironment,
		coreHydraIssuerEnvironment,
		coreHydraBrowserClientEnvironment,
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
		coreModelEnvironment,
		coreModelProviderEnvironment,
		coreMaxRunDurationEnvironment,
		coreMaxApprovalTTLEnvironment,
		coreCapabilityExpiryGraceEnvironment,
		coreEnrollmentKeyEnvironment,
		coreEnrollmentTTLEnvironment,
		"AGENTSERVER_V2_OBJECT_PREFIX",
		"AGENTSERVER_V2_S3_BUCKET",
		"AGENTSERVER_V2_S3_REGION",
		"AGENTSERVER_V2_S3_USE_PATH_STYLE",
		"AGENTSERVER_V2_KMS_REGION",
		"AGENTSERVER_V2_KMS_KEY_ID",
		"AWS_ROLE_ARN",
		"AWS_WEB_IDENTITY_TOKEN_FILE",
		"AWS_REGION",
		"AWS_DEFAULT_REGION",
		"AWS_EC2_METADATA_DISABLED",
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
	} {
		if environment.Secrets[name].Name == "" || environment.Secrets[name].Key == "" {
			t.Fatalf("Core secret environment %s is not sourced from one exact Secret key", name)
		}
	}
}
