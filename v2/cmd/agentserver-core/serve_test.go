package main

import (
	"io"
	"strings"
	"testing"
)

func TestServeCoreRequiresDistinctHarnessPoolIdentityBeforeOpeningDatabase(t *testing.T) {
	configuration := map[string]string{
		databaseURLEnvironment:             "postgres://unused",
		coreListenAddressEnvironment:       "127.0.0.1:0",
		coreTLSCertificateEnvironment:      "/unused/server.crt",
		coreTLSKeyEnvironment:              "/unused/server.key",
		coreClientCAEnvironment:            "/unused/client-ca.crt",
		coreGatewayIdentityEnvironment:     "spiffe://agentserver.local/ns/agentserver/sa/executor-gateway",
		coreHarnessPoolIdentityEnvironment: "",
	}
	getenv := func(name string) string { return configuration[name] }
	err := serveCore(t.Context(), getenv, io.Discard)
	if err == nil || !strings.Contains(err.Error(), coreHarnessPoolIdentityEnvironment+" is required") {
		t.Fatalf("missing harness-pool identity error = %v", err)
	}

	configuration[coreHarnessPoolIdentityEnvironment] = configuration[coreGatewayIdentityEnvironment]
	err = serveCore(t.Context(), getenv, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "must be distinct") {
		t.Fatalf("shared workload identity error = %v", err)
	}
}
