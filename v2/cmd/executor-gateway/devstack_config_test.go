//go:build linux || darwin

package main

import (
	"testing"

	"github.com/agentserver/agentserver/v2/internal/devstack"
	"github.com/agentserver/agentserver/v2/internal/devstacktest"
)

func TestGeneratedDevelopmentStackLoadsExecutorGatewayTLSAndCapability(t *testing.T) {
	fixture, err := devstacktest.Prepare(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	environment, err := devstack.ReadEnvironmentFile(fixture.Prepared.EnvironmentFiles["executor-gateway"])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gatewayTLSConfig(
		environment[gatewayTLSCertificateEnvironment], environment[gatewayTLSKeyEnvironment],
	); err != nil {
		t.Fatalf("load generated executor server TLS: %v", err)
	}
	client, err := newCoreHTTPClient(
		environment[gatewayCoreCAEnvironment], environment[gatewayCoreClientCertificateEnvironment],
		environment[gatewayCoreClientKeyEnvironment], "",
	)
	if err != nil {
		t.Fatalf("load generated executor core mTLS: %v", err)
	}
	client.CloseIdleConnections()
	authenticator, err := configuredDevMCPAuthenticator(
		func(name string) string { return environment[name] }, fixture.Config.Authority.ExecutorID,
	)
	if err != nil || authenticator == nil {
		t.Fatalf("load generated dynamic MCP capability authority: %v", err)
	}
}
