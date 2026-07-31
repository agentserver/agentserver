package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestLoadHarnessPoolDevelopmentConfig(t *testing.T) {
	configuration := validHarnessPoolConfiguration(t)
	config, err := loadHarnessPoolDevelopmentConfig(func(name string) string { return configuration[name] })
	if err != nil {
		t.Fatal(err)
	}
	if config.listenAddress != "127.0.0.1:0" || config.executorID != configuration[poolDevExecutorIDEnvironment] ||
		config.workerDigest == "" || config.maxConcurrent != defaultPoolMaxConcurrent ||
		config.maxRunDuration != defaultMaxRunDuration || config.allowlistVersion != 1 ||
		config.appCredential.UID != 65532 || config.appCredential.GID != 65532 || config.capabilityCodec == nil {
		t.Fatalf("loaded development config = %+v", config)
	}
}

func TestLoadHarnessPoolDevelopmentConfigRejectsUnsafeValues(t *testing.T) {
	for name, mutation := range map[string]func(map[string]string){
		"wildcard-listener": func(config map[string]string) { config[poolListenAddressEnvironment] = ":8443" },
		"cleartext-core":    func(config map[string]string) { config[poolCoreURLEnvironment] = "http://127.0.0.1:8443" },
		"executor-id":       func(config map[string]string) { config[poolDevExecutorIDEnvironment] = "not-a-uuid" },
		"capability-key":    func(config map[string]string) { config[poolDevRunCapabilityKeyEnvironment] += "=" },
		"runtime-digest":    func(config map[string]string) { config[poolRuntimeManifestDigestEnvironment] = strings.Repeat("A", 64) },
		"allowlist":         func(config map[string]string) { config[poolCheckpointAllowlistEnvironment] = "0" },
		"app-uid":           func(config map[string]string) { config[poolAppUIDEnvironment] = "0" },
		"concurrency":       func(config map[string]string) { config[poolMaxConcurrentEnvironment] = "65" },
		"duration":          func(config map[string]string) { config[poolMaxRunDurationEnvironment] = "25h" },
		"worker-config":     func(config map[string]string) { config[poolWorkerConfigEnvironment] = "relative.json" },
	} {
		t.Run(name, func(t *testing.T) {
			configuration := validHarnessPoolConfiguration(t)
			mutation(configuration)
			if _, err := loadHarnessPoolDevelopmentConfig(func(name string) string { return configuration[name] }); err == nil {
				t.Fatal("unsafe harness-pool configuration was accepted")
			}
		})
	}
}

func TestOptionalHarnessPoolBounds(t *testing.T) {
	if value, err := optionalBoundedDuration("90s", time.Minute, time.Second, 2*time.Minute, "TEST"); err != nil || value != 90*time.Second {
		t.Fatalf("duration = %s, %v", value, err)
	}
	if value, err := optionalBoundedInt("", 2, 1, 4, "TEST"); err != nil || value != 2 {
		t.Fatalf("integer = %d, %v", value, err)
	}
}

func validHarnessPoolConfiguration(t *testing.T) map[string]string {
	t.Helper()
	root := t.TempDir()
	workerExecutable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	workerExecutable, err = filepath.EvalSymlinks(workerExecutable)
	if err != nil {
		t.Fatal(err)
	}
	workerConfig := filepath.Join(root, "worker.json")
	if err := os.WriteFile(workerConfig, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	absolute := func(name string) string { return filepath.Join(root, name) }
	configuration := map[string]string{
		poolListenAddressEnvironment:         "127.0.0.1:0",
		poolTLSCertificateEnvironment:        absolute("pool.crt"),
		poolTLSKeyEnvironment:                absolute("pool.key"),
		poolWorkerClientCAEnvironment:        absolute("worker-ca.crt"),
		poolTLSIdentityEnvironment:           "spiffe://agentserver.test/ns/agentserver/sa/harness-pool",
		poolWorkerTLSIdentityEnvironment:     "spiffe://agentserver.test/ns/agentserver/sa/harness-worker",
		poolCoreURLEnvironment:               "https://127.0.0.1:9443",
		poolCoreCAEnvironment:                absolute("core-ca.crt"),
		poolDevExecutorIDEnvironment:         "20000000-0000-4000-8000-000000000002",
		poolDevRunCapabilityKeyEnvironment:   base64.RawURLEncoding.EncodeToString(bytesRepeat(0x31, 32)),
		poolDevObjectRootEnvironment:         absolute("objects"),
		poolRuntimeRootEnvironment:           absolute("runtime"),
		poolCheckpointStagingRootEnvironment: absolute("checkpoint"),
		poolWorkerExecutableEnvironment:      workerExecutable,
		poolWorkerConfigEnvironment:          workerConfig,
		poolManifestSigningKeyIDEnvironment:  "development-manifest-key",
		poolManifestSigningKeyEnvironment:    absolute("manifest.key"),
		poolRuntimeManifestDigestEnvironment: strings.Repeat("a", 64),
		poolCheckpointAllowlistEnvironment:   "1",
		poolWorkerServiceAccountEnvironment:  "harness-worker",
		poolAppUIDEnvironment:                "65532",
		poolAppGIDEnvironment:                "65532",
		poolExecutorMCPEndpointEnvironment:   "https://127.0.0.1:9444/mcp",
		poolExecutorMCPIdentityEnvironment:   "spiffe://agentserver.test/ns/agentserver/sa/executor-gateway",
		poolModelEnvironment:                 "gpt-5",
		poolModelProviderEnvironment:         "llmproxy",
		poolModelEndpointEnvironment:         "https://127.0.0.1:9445/v1",
		poolModelTLSIdentityEnvironment:      "spiffe://agentserver.test/ns/agentserver/sa/llmproxy",
	}
	if runtime.GOOS == "windows" {
		t.Skip("harness-pool local process command tests require Unix executable paths")
	}
	return configuration
}

func bytesRepeat(value byte, count int) []byte {
	result := make([]byte, count)
	for index := range result {
		result[index] = value
	}
	return result
}
