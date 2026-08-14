package bkectlpolicy

import (
	"errors"
	"strings"
	"testing"
)

func TestCredentialRequiredAllowsPinnedReadCommands(t *testing.T) {
	for _, arguments := range [][]string{
		{"k8s", "pod", "get", "--cluster", "echo-hl", "--namespace", "default", "--name", "demo", "--json"},
		{"bytebox", "host", "get", "10.0.0.1", "--region", "i18n"},
		{"spacex", "release-orchestration", "progress", "--id", "123"},
	} {
		required, err := CredentialRequired(arguments)
		if err != nil || !required {
			t.Fatalf("CredentialRequired(%q) = %t, %v; want true, nil", arguments, required, err)
		}
	}
}

func TestCredentialRequiredAllowsDiscoveryWithoutCredential(t *testing.T) {
	for _, arguments := range [][]string{
		nil, {"--help"}, {"k8s", "pod", "get", "--help"}, {"help", "quota"}, {"version"},
	} {
		required, err := CredentialRequired(arguments)
		if err != nil || required {
			t.Fatalf("CredentialRequired(%q) = %t, %v; want false, nil", arguments, required, err)
		}
	}
}

func TestCredentialRequiredDeniesCredentialDisclosureAndMutation(t *testing.T) {
	for _, arguments := range [][]string{
		{"auth", "get", "jwt", "--json"},
		{"auth", "status"},
		{"k8s", "node", "shell", "--cluster", "echo-hl", "--name", "node-1"},
		{"bytesd", "node", "block", "--ip", "10.0.0.1"},
		{"--confirm-write", "quota", "resource-pool", "create"},
		{"k8s", "pod", "get", "--debug"},
		{"unknown", "command"},
	} {
		required, err := CredentialRequired(arguments)
		if required || !errors.Is(err, ErrInvocationDenied) {
			t.Fatalf("CredentialRequired(%q) = %t, %v; want denied", arguments, required, err)
		}
	}
}

func TestPolicyPinsExpectedUpstreamSurface(t *testing.T) {
	if len(allowedCommandPaths) != 245 {
		t.Fatalf("allowed command count = %d, want 245", len(allowedCommandPaths))
	}
	if got := SHA256Hex(); len(got) != 64 || strings.Trim(got, "0123456789abcdef") != "" {
		t.Fatalf("SHA256Hex() = %q", got)
	}
	for index := 1; index < len(allowedCommandPaths); index++ {
		if allowedCommandPaths[index-1] >= allowedCommandPaths[index] {
			t.Fatalf("command surface is not strictly sorted at %q", allowedCommandPaths[index])
		}
	}
}
