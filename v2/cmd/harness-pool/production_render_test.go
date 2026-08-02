package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/productiondeploytest"
)

func TestProductionRendererHarnessPoolEnvironmentPassesCommandLoader(t *testing.T) {
	environment, err := productiondeploytest.ExampleDeploymentEnvironment("harness-pool")
	if err != nil {
		t.Fatal(err)
	}
	assertHarnessPoolProductionEnvironmentNames(t, environment.Names())
	for _, name := range environment.Names() {
		if strings.HasPrefix(name, "AGENTSERVER_V2_DEV_") {
			t.Fatalf("production renderer emitted development authority %s", name)
		}
	}

	workerConfig := filepath.Join(t.TempDir(), "worker-deployment.json")
	if err := os.WriteFile(workerConfig, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	workerExecutable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	environment.Values[poolWorkerConfigEnvironment] = workerConfig
	environment.Values[poolWorkerExecutableEnvironment] = workerExecutable
	loaded, err := loadHarnessPoolProductionConfig(environment.Get)
	if err != nil {
		t.Fatalf("harness-pool rejected rendered production environment: %v", err)
	}
	if loaded.executorID != environment.Get(poolExecutorIDEnvironment) || loaded.workerCredential == nil ||
		loaded.workerCredential.UID != 65531 || loaded.appCredential.UID != 65532 {
		t.Fatalf("harness-pool loaded different production authority: %+v", loaded)
	}
}

func assertHarnessPoolProductionEnvironmentNames(t *testing.T, got []string) {
	t.Helper()
	want := []string{
		poolListenAddressEnvironment,
		poolTLSCertificateEnvironment,
		poolTLSKeyEnvironment,
		poolWorkerClientCAEnvironment,
		poolTLSIdentityEnvironment,
		poolWorkerTLSIdentityEnvironment,
		poolCoreURLEnvironment,
		poolCoreCAEnvironment,
		poolCoreServerNameEnvironment,
		poolExecutorIDEnvironment,
		poolRuntimeRootEnvironment,
		poolCheckpointStagingRootEnvironment,
		poolWorkerExecutableEnvironment,
		poolWorkerConfigEnvironment,
		poolManifestSigningKeyIDEnvironment,
		poolManifestSigningKeyEnvironment,
		poolRuntimeManifestDigestEnvironment,
		poolCheckpointAllowlistEnvironment,
		poolWorkerServiceAccountEnvironment,
		poolPrivilegedForkEnvironment,
		poolWorkerUIDEnvironment,
		poolWorkerGIDEnvironment,
		poolAppUIDEnvironment,
		poolAppGIDEnvironment,
		poolExecutorMCPEndpointEnvironment,
		poolExecutorMCPIdentityEnvironment,
		poolModelEnvironment,
		poolModelProviderEnvironment,
		poolModelEndpointEnvironment,
		poolModelTLSIdentityEnvironment,
		poolMaxConcurrentEnvironment,
		poolMaxRunDurationEnvironment,
		poolMaxApprovalTTLEnvironment,
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
	if !slices.Equal(got, want) {
		t.Fatalf("rendered harness-pool environment names = %v, want %v", got, want)
	}
}
