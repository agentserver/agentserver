package main

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

type probeTestConnection struct{ closed bool }

func (connection *probeTestConnection) Close() error {
	connection.closed = true
	return nil
}

func TestRunAcceptsOnlyExactLoopbackTCPProbe(t *testing.T) {
	connection := &probeTestConnection{}
	called := false
	exitCode := run([]string{"tcp", "--address=127.0.0.1:8443"}, &bytes.Buffer{}, func(_ context.Context, network, address string) (probeConnection, error) {
		called = true
		if network != "tcp" || address != "127.0.0.1:8443" {
			t.Fatalf("dial = %q, %q", network, address)
		}
		return connection, nil
	})
	if exitCode != 0 || !called || !connection.closed {
		t.Fatalf("probe = exit %d, called %v, closed %v", exitCode, called, connection.closed)
	}
}

func TestRunRejectsOpenOrMalformedProbeTarget(t *testing.T) {
	for _, arguments := range [][]string{
		{}, {"tcp"}, {"http", "--address=127.0.0.1:8443"},
		{"tcp", "--address=0.0.0.0:8443"}, {"tcp", "--address=localhost:8443"},
		{"tcp", "--address=127.0.0.1:0"}, {"tcp", "--address=127.0.0.1:65536"},
		{"tcp", "--address=127.0.0.1:080"}, {"tcp", "--address=127.0.0.1:https"},
		{"tcp", "--address=127.0.0.1:8443", "future"},
	} {
		if exitCode := run(arguments, &bytes.Buffer{}, func(context.Context, string, string) (probeConnection, error) {
			t.Fatal("invalid probe reached dialer")
			return nil, nil
		}); exitCode != 2 {
			t.Fatalf("arguments %v exit = %d", arguments, exitCode)
		}
	}
}

func TestRunReportsDialFailure(t *testing.T) {
	exitCode := run([]string{"tcp", "--address=127.0.0.1:8443"}, &bytes.Buffer{}, func(context.Context, string, string) (probeConnection, error) {
		return nil, errors.New("unavailable")
	})
	if exitCode != 1 {
		t.Fatalf("dial failure exit = %d", exitCode)
	}
}
