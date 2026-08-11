package coreserver

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"strings"
	"testing"
)

func TestCredentialAuthorizationProviderFailureLogDoesNotExposeProviderError(t *testing.T) {
	var output bytes.Buffer
	commands := StateStoreWorkspaceCredentialCommands{
		Logger: slog.New(slog.NewJSONHandler(&output, nil)),
	}
	commands.logCredentialAuthorizationProviderFailure(
		t.Context(), "bytecloud", "begin", errors.New("secret ticket and upstream URL must not be logged"),
	)
	logged := output.String()
	for _, forbidden := range []string{"secret ticket", "upstream URL"} {
		if strings.Contains(logged, forbidden) {
			t.Fatalf("provider log leaked %q: %s", forbidden, logged)
		}
	}
	for _, wanted := range []string{`"provider_kind":"bytecloud"`, `"stage":"begin"`, `"failure_class":"rejected_or_invalid_response"`} {
		if !strings.Contains(logged, wanted) {
			t.Fatalf("provider log omitted %s: %s", wanted, logged)
		}
	}
}

func TestCredentialAuthorizationProviderFailureClass(t *testing.T) {
	if got := credentialAuthorizationProviderFailureClass(context.DeadlineExceeded); got != "timeout" {
		t.Fatalf("deadline failure class = %q", got)
	}
	if got := credentialAuthorizationProviderFailureClass(&net.DNSError{Err: "unreachable", Name: "provider.invalid"}); got != "network" {
		t.Fatalf("network failure class = %q", got)
	}
}
