//go:build linux || darwin

package main

import (
	"testing"

	"github.com/agentserver/agentserver/v2/internal/devstack"
	"github.com/agentserver/agentserver/v2/internal/devstacktest"
)

func TestGeneratedDevelopmentStackLoadsBrowserGatewayTLS(t *testing.T) {
	fixture, err := devstacktest.Prepare(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	environment, err := devstack.ReadEnvironmentFile(fixture.Prepared.EnvironmentFiles["browser-gateway"])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := browserGatewayTLSConfig(
		environment[browserTLSCertificateEnvironment], environment[browserTLSKeyEnvironment],
	); err != nil {
		t.Fatalf("load generated browser server TLS: %v", err)
	}
	client, err := newBrowserCoreHTTPClient(
		environment[browserCoreCAEnvironment], environment[browserCoreClientCertificateEnvironment],
		environment[browserCoreClientKeyEnvironment], "",
	)
	if err != nil {
		t.Fatalf("load generated browser core mTLS: %v", err)
	}
	client.CloseIdleConnections()
}
