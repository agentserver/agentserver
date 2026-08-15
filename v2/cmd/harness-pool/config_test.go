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
		config.maxRunDuration != defaultMaxRunDuration || config.maxApprovalTTL != defaultMaxApprovalTTL || config.allowlistVersion != 1 ||
		config.appCredential.UID != 65532 || config.appCredential.GID != 65532 ||
		config.workerCredential != nil || config.capabilityCodec == nil || config.managedSandbox != nil {
		t.Fatalf("loaded development config = %+v", config)
	}
}

func TestLoadHarnessPoolDevelopmentConfigEnablesManagedSandboxExactly(t *testing.T) {
	configuration := validHarnessPoolConfiguration(t)
	addValidManagedSandboxConfiguration(t, configuration)
	config, err := loadHarnessPoolDevelopmentConfig(func(name string) string { return configuration[name] })
	if err != nil {
		t.Fatal(err)
	}
	if config.managedSandbox == nil ||
		config.managedSandbox.EnvironmentID != configuration[poolManagedEnvironmentIDEnvironment] ||
		config.managedSandbox.RuntimeProfileDigest != configuration[poolManagedRuntimeDigestEnvironment] ||
		config.managedSandbox.PackSetDigest != configuration[poolManagedPackSetDigestEnvironment] ||
		config.managedSandbox.SandboxTTL != 30*time.Minute || config.managedSandbox.ActivityTTL != 45*time.Second {
		t.Fatalf("managed development config = %+v", config)
	}
	profile := runLaunchProfile(config, "https://127.0.0.1:9999/internal/v2/harness-control", false)
	if profile.ManagedSandbox == nil || profile.ManagedSandbox == config.managedSandbox || *profile.ManagedSandbox != *config.managedSandbox {
		t.Fatalf("managed launch profile = %+v", profile.ManagedSandbox)
	}
}

func TestLoadHarnessPoolProductionConfigEnablesManagedToolPack(t *testing.T) {
	configuration := validHarnessPoolProductionConfiguration(t)
	addValidManagedSandboxConfiguration(t, configuration)
	config, err := loadHarnessPoolProductionConfig(func(name string) string { return configuration[name] })
	if err != nil {
		t.Fatal(err)
	}
	if config.managedSandbox == nil {
		t.Fatalf("managed production config = %+v", config)
	}
}

func TestLoadHarnessPoolManagedSandboxConfigRejectsPartialAndInvalidValues(t *testing.T) {
	for name, mutation := range map[string]func(map[string]string){
		"partial": func(config map[string]string) {
			config[poolManagedEnvironmentIDEnvironment] = "22000000-0000-4000-8000-000000000002"
		},
		"bad-runtime-digest": func(config map[string]string) {
			addValidManagedSandboxConfiguration(t, config)
			config[poolManagedRuntimeDigestEnvironment] = strings.Repeat("A", 64)
		},
		"activity-too-short": func(config map[string]string) {
			addValidManagedSandboxConfiguration(t, config)
			config[poolManagedActivityTTLEnvironment] = "2s"
		},
		"activity-over-sandbox": func(config map[string]string) {
			addValidManagedSandboxConfiguration(t, config)
			config[poolManagedActivityTTLEnvironment] = "31m"
		},
	} {
		t.Run(name, func(t *testing.T) {
			configuration := validHarnessPoolConfiguration(t)
			mutation(configuration)
			if _, err := loadHarnessPoolDevelopmentConfig(func(name string) string { return configuration[name] }); err == nil {
				t.Fatal("unsafe managed sandbox configuration was accepted")
			}
		})
	}
}

func TestLoadHarnessPoolProductionConfig(t *testing.T) {
	configuration := validHarnessPoolProductionConfiguration(t)
	config, err := loadHarnessPoolProductionConfig(func(name string) string { return configuration[name] })
	if err != nil {
		t.Fatal(err)
	}
	if config.listenAddress != "127.0.0.1:8443" || config.executorID != configuration[poolExecutorIDEnvironment] ||
		config.objectRoot != "" || config.capabilityCodec != nil || config.workerCredential == nil ||
		config.workerCredential.UID != 65531 || config.workerCredential.GID != 65531 ||
		config.appCredential.UID != 65532 || config.appCredential.GID != 65532 {
		t.Fatalf("loaded production config = %+v", config)
	}
}

func TestLoadHarnessPoolProductionConfigDoesNotReadDevelopmentSecrets(t *testing.T) {
	configuration := validHarnessPoolProductionConfiguration(t)
	configuration[poolDevExecutorIDEnvironment] = "not-a-uuid"
	configuration[poolDevRunCapabilityKeyEnvironment] = "not-a-capability-key"
	configuration[poolDevObjectRootEnvironment] = "relative-development-object-root"
	if _, err := loadHarnessPoolProductionConfig(func(name string) string { return configuration[name] }); err != nil {
		t.Fatalf("production configuration consumed a development-only value: %v", err)
	}
}

func TestLoadHarnessPoolProductionConfigRejectsUnsafeValues(t *testing.T) {
	for name, mutation := range map[string]func(map[string]string){
		"missing executor": func(config map[string]string) { delete(config, poolExecutorIDEnvironment) },
		"executor id":      func(config map[string]string) { config[poolExecutorIDEnvironment] = "not-a-uuid" },
		"missing fork":     func(config map[string]string) { delete(config, poolPrivilegedForkEnvironment) },
		"disabled fork":    func(config map[string]string) { config[poolPrivilegedForkEnvironment] = "false" },
		"missing worker uid": func(config map[string]string) {
			delete(config, poolWorkerUIDEnvironment)
		},
		"shared worker app uid": func(config map[string]string) {
			config[poolWorkerUIDEnvironment] = config[poolAppUIDEnvironment]
		},
		"shared worker app gid": func(config map[string]string) {
			config[poolWorkerGIDEnvironment] = config[poolAppGIDEnvironment]
		},
	} {
		t.Run(name, func(t *testing.T) {
			configuration := validHarnessPoolProductionConfiguration(t)
			mutation(configuration)
			if _, err := loadHarnessPoolProductionConfig(func(name string) string { return configuration[name] }); err == nil {
				t.Fatal("unsafe production harness-pool configuration was accepted")
			}
		})
	}
}

func TestLoadHarnessPoolDevelopmentConfigSelectsPrivilegedFork(t *testing.T) {
	configuration := validHarnessPoolConfiguration(t)
	configuration[poolPrivilegedForkEnvironment] = "true"
	configuration[poolWorkerUIDEnvironment] = "65531"
	configuration[poolWorkerGIDEnvironment] = "65531"
	config, err := loadHarnessPoolDevelopmentConfig(func(name string) string { return configuration[name] })
	if err != nil {
		t.Fatal(err)
	}
	if config.workerCredential == nil || config.workerCredential.UID != 65531 || config.workerCredential.GID != 65531 {
		t.Fatalf("privileged worker credential = %+v", config.workerCredential)
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
		"fork-mode":         func(config map[string]string) { config[poolPrivilegedForkEnvironment] = "yes" },
		"fork-worker-uid": func(config map[string]string) {
			config[poolPrivilegedForkEnvironment] = "true"
			config[poolWorkerUIDEnvironment] = "0"
			config[poolWorkerGIDEnvironment] = "65531"
		},
		"fork-shared-gid": func(config map[string]string) {
			config[poolPrivilegedForkEnvironment] = "true"
			config[poolWorkerUIDEnvironment] = "65531"
			config[poolWorkerGIDEnvironment] = "65532"
		},
		"concurrency":  func(config map[string]string) { config[poolMaxConcurrentEnvironment] = "65" },
		"duration":     func(config map[string]string) { config[poolMaxRunDurationEnvironment] = "25h" },
		"approval ttl": func(config map[string]string) { config[poolMaxApprovalTTLEnvironment] = "25h" },
		"approval over run": func(config map[string]string) {
			config[poolMaxRunDurationEnvironment] = "2s"
			config[poolMaxApprovalTTLEnvironment] = "3s"
		},
		"worker-config": func(config map[string]string) { config[poolWorkerConfigEnvironment] = "relative.json" },
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

func validHarnessPoolProductionConfiguration(t *testing.T) map[string]string {
	t.Helper()
	configuration := validHarnessPoolConfiguration(t)
	configuration[poolListenAddressEnvironment] = "127.0.0.1:8443"
	configuration[poolExecutorIDEnvironment] = "21000000-0000-4000-8000-000000000002"
	configuration[poolPrivilegedForkEnvironment] = "true"
	configuration[poolWorkerUIDEnvironment] = "65531"
	configuration[poolWorkerGIDEnvironment] = "65531"
	delete(configuration, poolDevExecutorIDEnvironment)
	delete(configuration, poolDevRunCapabilityKeyEnvironment)
	delete(configuration, poolDevObjectRootEnvironment)
	return configuration
}

func addValidManagedSandboxConfiguration(t *testing.T, configuration map[string]string) {
	t.Helper()
	configuration[poolManagedEnvironmentIDEnvironment] = "22000000-0000-4000-8000-000000000002"
	configuration[poolManagedRuntimeDigestEnvironment] = strings.Repeat("b", 64)
	configuration[poolManagedPackSetDigestEnvironment] = strings.Repeat("c", 64)
	configuration[poolManagedSkillDigestEnvironment] = strings.Repeat("d", 64)
	configuration[poolManagedSandboxTTLEnvironment] = "30m"
	configuration[poolManagedActivityTTLEnvironment] = "45s"
}

func bytesRepeat(value byte, count int) []byte {
	result := make([]byte, count)
	for index := range result {
		result[index] = value
	}
	return result
}
