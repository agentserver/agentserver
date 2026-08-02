package harnessinit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadNetworkGuardConfigBuildsClosedIPv4UIDPolicy(t *testing.T) {
	document := NetworkGuardDocument{
		Version: 1,
		Table:   "agentserver_harness",
		Policies: []NetworkGuardPolicyDocument{
			{UID: 65531, TCP: []NetworkGuardEndpointDocument{{Address: "127.0.0.1", Port: 8443}, {Address: "10.96.0.20", Port: 8443}}},
			{UID: 65532, TCP: []NetworkGuardEndpointDocument{{Address: "10.96.0.21", Port: 8443}}},
		},
	}
	path := writeNetworkGuardDocument(t, document)
	config, err := LoadNetworkGuardConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if config.Table != document.Table || len(config.Policies) != 2 || config.Policies[0].UID != 65531 ||
		len(config.Policies[0].AllowedEndpoints) != 2 || config.Policies[0].AllowedEndpoints[1].Address.String() != "10.96.0.20" {
		t.Fatalf("network guard config = %+v", config)
	}
}

func TestLoadNetworkGuardConfigRejectsUnknownDuplicateAndNonIPv4(t *testing.T) {
	valid := NetworkGuardDocument{
		Version: 1, Table: "agentserver_harness",
		Policies: []NetworkGuardPolicyDocument{{UID: 65531, TCP: []NetworkGuardEndpointDocument{{Address: "127.0.0.1", Port: 8443}}}},
	}
	for name, mutate := range map[string]func(*NetworkGuardDocument){
		"version":   func(value *NetworkGuardDocument) { value.Version = 2 },
		"root uid":  func(value *NetworkGuardDocument) { value.Policies[0].UID = 0 },
		"IPv6":      func(value *NetworkGuardDocument) { value.Policies[0].TCP[0].Address = "::1" },
		"zero port": func(value *NetworkGuardDocument) { value.Policies[0].TCP[0].Port = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			document := valid
			document.Policies = append([]NetworkGuardPolicyDocument(nil), valid.Policies...)
			document.Policies[0].TCP = append([]NetworkGuardEndpointDocument(nil), valid.Policies[0].TCP...)
			mutate(&document)
			if _, err := LoadNetworkGuardConfig(writeNetworkGuardDocument(t, document)); err == nil {
				t.Fatal("unsafe network guard config was accepted")
			}
		})
	}

	root := t.TempDir()
	unknown := filepath.Join(root, "unknown.json")
	if err := os.WriteFile(unknown, []byte(`{"version":1,"table":"x","policies":[],"future":true}`), 0o400); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadNetworkGuardConfig(unknown); err == nil {
		t.Fatal("unknown network guard field was accepted")
	}
	duplicate := filepath.Join(root, "duplicate.json")
	if err := os.WriteFile(duplicate, []byte(`{"version":1,"version":1}`), 0o400); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadNetworkGuardConfig(duplicate); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate network guard error = %v", err)
	}
}

func writeNetworkGuardDocument(t *testing.T, document NetworkGuardDocument) string {
	t.Helper()
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "network.json")
	if err := os.WriteFile(path, raw, 0o400); err != nil {
		t.Fatal(err)
	}
	return path
}
