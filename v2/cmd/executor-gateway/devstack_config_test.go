//go:build linux || darwin

package main

import (
	"crypto/x509"
	"encoding/pem"
	"os"
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
	certificatePEM, err := os.ReadFile(environment[gatewayTLSCertificateEnvironment])
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(certificatePEM)
	if block == nil {
		t.Fatal("generated executor certificate is not PEM")
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil || len(leaf.URIs) != 1 {
		t.Fatalf("parse generated executor identity: URIs %v, error %v", leaf.URIs, err)
	}
	identity := leaf.URIs[0].String()
	if _, err := productionGatewayTLSConfig(
		environment[gatewayTLSCertificateEnvironment], environment[gatewayTLSKeyEnvironment], identity,
	); err != nil {
		t.Fatalf("load generated production server identity: %v", err)
	}
	productionClient, err := newCoreHTTPClientWithIdentity(
		environment[gatewayCoreCAEnvironment], environment[gatewayCoreClientCertificateEnvironment],
		environment[gatewayCoreClientKeyEnvironment], "core.agentserver.test", identity,
	)
	if err != nil {
		t.Fatalf("load generated production Core client identity: %v", err)
	}
	productionClient.CloseIdleConnections()
	if _, err := productionGatewayTLSConfig(
		environment[gatewayTLSCertificateEnvironment], environment[gatewayTLSKeyEnvironment],
		"spiffe://agentserver.invalid/ns/invalid/sa/invalid",
	); err == nil {
		t.Fatal("wrong production SPIFFE identity was accepted")
	}
	authenticator, err := configuredDevMCPAuthenticator(
		func(name string) string { return environment[name] }, fixture.Config.Authority.ExecutorID,
	)
	if err != nil || authenticator == nil {
		t.Fatalf("load generated dynamic MCP capability authority: %v", err)
	}
}
