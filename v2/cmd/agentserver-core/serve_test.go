package main

import (
	"encoding/base64"
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

	configuration[coreHarnessPoolIdentityEnvironment] = "spiffe://agentserver.local/ns/agentserver/sa/harness-pool"
	err = serveCore(t.Context(), getenv, io.Discard)
	if err == nil || !strings.Contains(err.Error(), coreBrowserIdentityEnvironment+" is required") {
		t.Fatalf("missing browser-gateway identity error = %v", err)
	}
	configuration[coreBrowserIdentityEnvironment] = configuration[coreGatewayIdentityEnvironment]
	err = serveCore(t.Context(), getenv, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "browser-gateway, executor-gateway, and harness-pool") {
		t.Fatalf("shared browser workload identity error = %v", err)
	}
}

func TestCoreBrowserConfigurationParsersFailClosed(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	encoded := base64.RawURLEncoding.EncodeToString(key)
	decoded, err := decodeRunCursorKey(encoded)
	if err != nil || string(decoded) != string(key) {
		t.Fatalf("decodeRunCursorKey() = %x, %v", decoded, err)
	}
	for _, invalid := range []string{"short", encoded + "=", base64.RawURLEncoding.EncodeToString(key[:31])} {
		if _, err := decodeRunCursorKey(invalid); err == nil {
			t.Fatalf("invalid cursor key %q was accepted", invalid)
		}
	}
	if value, err := strictOptionalBoolean("true", "TEST"); err != nil || !value {
		t.Fatalf("strictOptionalBoolean(true) = %v, %v", value, err)
	}
	if _, err := strictOptionalBoolean("1", "TEST"); err == nil {
		t.Fatal("noncanonical boolean was accepted")
	}
	if tools := commaSeparatedTools("read_file,shell"); len(tools) != 2 || tools[0] != "read_file" || tools[1] != "shell" {
		t.Fatalf("commaSeparatedTools() = %q", tools)
	}
}
