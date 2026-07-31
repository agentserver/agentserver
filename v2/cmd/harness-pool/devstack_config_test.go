//go:build linux || darwin

package main

import (
	"testing"

	"github.com/agentserver/agentserver/v2/internal/devstack"
	"github.com/agentserver/agentserver/v2/internal/devstacktest"
)

func TestGeneratedDevelopmentStackLoadsHarnessPoolConfiguration(t *testing.T) {
	fixture, err := devstacktest.Prepare(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	environment, err := devstack.ReadEnvironmentFile(fixture.Prepared.EnvironmentFiles["harness-pool"])
	if err != nil {
		t.Fatal(err)
	}
	config, err := loadHarnessPoolDevelopmentConfig(func(name string) string { return environment[name] })
	if err != nil {
		t.Fatalf("load generated harness-pool configuration: %v", err)
	}
	if config.executorID != fixture.Config.Authority.ExecutorID || config.capabilityCodec == nil ||
		config.runtimeDigest == "" || config.workerDigest == "" || config.maxConcurrent != fixture.Config.Harness.MaxConcurrentAttempts {
		t.Fatalf("generated harness-pool configuration = %+v", config)
	}
	coreClient, err := newHarnessPoolCoreHTTPClient(
		config.coreCA, config.tlsCertificate, config.tlsKey, config.coreServerName, config.poolTLSIdentity,
	)
	if err != nil {
		t.Fatalf("load generated harness-pool core mTLS: %v", err)
	}
	coreClient.CloseIdleConnections()
	if _, err := newHarnessPoolControlTLSConfig(
		config.tlsCertificate, config.tlsKey, config.workerClientCA, config.poolTLSIdentity,
	); err != nil {
		t.Fatalf("load generated harness-pool control TLS: %v", err)
	}
}
