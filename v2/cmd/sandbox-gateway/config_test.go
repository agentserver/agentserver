package main

import (
	"path/filepath"
	"testing"
	"time"
)

func TestLoadSandboxGatewayDevelopmentConfig(t *testing.T) {
	environment := validSandboxGatewayDevelopmentEnvironment(t)
	config, err := loadSandboxGatewayConfig(func(name string) string { return environment[name] }, sandboxGatewayServeInsecureDevelopment)
	if err != nil {
		t.Fatal(err)
	}
	if config.production || config.providerMode != "fake" || config.listenAddress != "127.0.0.1:0" ||
		config.coreURL != "http://127.0.0.1:9443" || config.idleTTL != defaultSandboxIdleTTL ||
		config.ensureTimeout != defaultSandboxEnsureTimeout || config.ensurePoll != defaultSandboxEnsurePoll ||
		config.reconcileInterval != defaultSandboxReconcileInterval || config.reconcileLimit != defaultSandboxReconcileLimit ||
		config.root != "/workspace" || config.platform != "linux-amd64" {
		t.Fatalf("development config = %+v", config)
	}
}

func TestLoadSandboxGatewayProductionConfig(t *testing.T) {
	environment := validSandboxGatewayProductionEnvironment(t)
	config, err := loadSandboxGatewayConfig(func(name string) string { return environment[name] }, sandboxGatewayServeProduction)
	if err != nil {
		t.Fatal(err)
	}
	if !config.production || config.providerMode != "tae" || config.spiffeIdentity != environment[sandboxSPIFFEIdentityEnvironment] ||
		config.executorIdentity != environment[sandboxExecutorIdentityEnvironment] || config.harnessIdentity != environment[sandboxHarnessIdentityEnvironment] ||
		config.coreCertificate == "" || config.coreServerName != "core.internal" {
		t.Fatalf("production config = %+v", config)
	}
}

func TestLoadSandboxGatewayConfigRejectsUnsafeDevelopmentValues(t *testing.T) {
	for name, mutation := range map[string]func(map[string]string){
		"wildcard-listen":       func(config map[string]string) { config[sandboxListenAddressEnvironment] = ":8080" },
		"remote-cleartext-core": func(config map[string]string) { config[sandboxCoreURLEnvironment] = "http://core.internal" },
		"core-credentials":      func(config map[string]string) { config[sandboxCoreURLEnvironment] = "http://user@127.0.0.1:9443" },
		"wrong-provider":        func(config map[string]string) { config[sandboxProviderModeEnvironment] = "tae" },
		"relative-root":         func(config map[string]string) { config[sandboxRootEnvironment] = "workspace" },
		"fractional-idle":       func(config map[string]string) { config[sandboxIdleTTLEnvironment] = "1500ms" },
		"poll-over-timeout": func(config map[string]string) {
			config[sandboxEnsureTimeoutEnvironment] = "2s"
			config[sandboxEnsurePollEnvironment] = "3s"
		},
	} {
		t.Run(name, func(t *testing.T) {
			environment := validSandboxGatewayDevelopmentEnvironment(t)
			mutation(environment)
			if _, err := loadSandboxGatewayConfig(func(name string) string { return environment[name] }, sandboxGatewayServeInsecureDevelopment); err == nil {
				t.Fatal("unsafe development configuration was accepted")
			}
		})
	}
}

func TestLoadSandboxGatewayConfigRejectsUnsafeProductionValues(t *testing.T) {
	for name, mutation := range map[string]func(map[string]string){
		"fake-provider":  func(config map[string]string) { config[sandboxProviderModeEnvironment] = "fake" },
		"cleartext-core": func(config map[string]string) { config[sandboxCoreURLEnvironment] = "http://127.0.0.1:9443" },
		"shared-identities": func(config map[string]string) {
			config[sandboxHarnessIdentityEnvironment] = config[sandboxExecutorIdentityEnvironment]
		},
	} {
		t.Run(name, func(t *testing.T) {
			environment := validSandboxGatewayProductionEnvironment(t)
			mutation(environment)
			if _, err := loadSandboxGatewayConfig(func(name string) string { return environment[name] }, sandboxGatewayServeProduction); err == nil {
				t.Fatal("unsafe production configuration was accepted")
			}
		})
	}
}

func TestOptionalSandboxConfigurationBounds(t *testing.T) {
	if value, err := optionalSandboxDuration("1500ms", time.Second, time.Millisecond, time.Second*2, "TEST"); err != nil || value != 1500*time.Millisecond {
		t.Fatalf("duration = %s, %v", value, err)
	}
	if value, err := optionalSandboxInt("", 10, 1, 20, "TEST"); err != nil || value != 10 {
		t.Fatalf("integer = %d, %v", value, err)
	}
}

func validSandboxGatewayDevelopmentEnvironment(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{
		sandboxListenAddressEnvironment:     "127.0.0.1:0",
		sandboxCoreURLEnvironment:           "http://127.0.0.1:9443",
		sandboxCapabilityKeyringEnvironment: filepath.Join(t.TempDir(), "capabilities.json"),
		sandboxProviderRegionEnvironment:    "sg",
		sandboxProviderPSMEnvironment:       "psm.agentserver.tae",
	}
}

func validSandboxGatewayProductionEnvironment(t *testing.T) map[string]string {
	t.Helper()
	root := t.TempDir()
	file := func(name string) string { return filepath.Join(root, name) }
	return map[string]string{
		sandboxListenAddressEnvironment:     ":8443",
		sandboxTLSCertificateEnvironment:    file("sandbox.crt"),
		sandboxTLSKeyEnvironment:            file("sandbox.key"),
		sandboxClientCAEnvironment:          file("clients-ca.crt"),
		sandboxSPIFFEIdentityEnvironment:    "spiffe://agentserver.test/ns/agentserver/sa/sandbox-gateway",
		sandboxExecutorIdentityEnvironment:  "spiffe://agentserver.test/ns/agentserver/sa/executor-gateway",
		sandboxHarnessIdentityEnvironment:   "spiffe://agentserver.test/ns/agentserver/sa/harness-pool",
		sandboxCoreURLEnvironment:           "https://core.internal",
		sandboxCoreCAEnvironment:            file("core-ca.crt"),
		sandboxCoreCertificateEnvironment:   file("sandbox.crt"),
		sandboxCoreKeyEnvironment:           file("sandbox.key"),
		sandboxCoreServerNameEnvironment:    "core.internal",
		sandboxCapabilityKeyringEnvironment: file("capabilities.json"),
		sandboxProviderModeEnvironment:      "tae",
		sandboxProviderRegionEnvironment:    "sg",
		sandboxProviderPSMEnvironment:       "psm.agentserver.tae",
	}
}
