package bkectlpolicy

import (
	"errors"
	"strings"
	"testing"
)

func TestCredentialRequiredDoesNotAuthorizeCommandPaths(t *testing.T) {
	for _, arguments := range [][]string{
		{"--region", "cn", "k8s", "pod", "get", "--cluster", "echo-hl", "--name", "demo"},
		{"future", "command", "introduced", "after", "this", "release"},
		{"bytesd", "node", "block", "--ip", "10.0.0.1"},
		{"--confirm-write", "quota", "resource-pool", "create"},
		{"k8s", "pod", "get", "--debug"},
		{"auth", "status"},
	} {
		required, err := CredentialRequired(arguments)
		if err != nil || !required {
			t.Fatalf("CredentialRequired(%q) = %t, %v; want true, nil", arguments, required, err)
		}
	}
}

func TestCredentialRequiredAllowsDiscoveryWithoutCredential(t *testing.T) {
	for _, arguments := range [][]string{
		nil, {"--help"}, {"--region", "cn", "k8s", "pod", "get", "--help"},
		{"help", "quota"}, {"version"},
	} {
		required, err := CredentialRequired(arguments)
		if err != nil || required {
			t.Fatalf("CredentialRequired(%q) = %t, %v; want false, nil", arguments, required, err)
		}
	}
}

func TestCredentialRequiredDeniesOnlyCredentialDisclosure(t *testing.T) {
	for _, arguments := range [][]string{
		{"auth", "get", "jwt", "--json"},
		{"--region", "cn", "auth", "get", "jwt"},
		{"auth", "--json", "get", "jwt"},
	} {
		required, err := CredentialRequired(arguments)
		if required || !errors.Is(err, ErrCredentialDisclosureDenied) {
			t.Fatalf("CredentialRequired(%q) = %t, %v; want credential disclosure denied", arguments, required, err)
		}
	}

	// Discovery of the command is harmless because no credential is injected.
	required, err := CredentialRequired([]string{"auth", "get", "jwt", "--help"})
	if err != nil || required {
		t.Fatalf("credential help discovery = %t, %v; want false, nil", required, err)
	}
}

func TestCredentialContractDigest(t *testing.T) {
	if !strings.Contains(CredentialContractDocument, "command_paths=unrestricted") {
		t.Fatal("credential contract does not explicitly leave command authorization downstream")
	}
	if got := SHA256Hex(); len(got) != 64 || strings.Trim(got, "0123456789abcdef") != "" {
		t.Fatalf("SHA256Hex() = %q", got)
	}
}
