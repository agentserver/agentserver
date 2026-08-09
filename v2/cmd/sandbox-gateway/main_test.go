package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestSandboxGatewayRunSelectsExactMode(t *testing.T) {
	for _, test := range []struct {
		args []string
		mode sandboxGatewayServeMode
	}{
		{args: []string{"serve"}, mode: sandboxGatewayServeProduction},
		{args: []string{"serve", "--insecure-dev"}, mode: sandboxGatewayServeInsecureDevelopment},
	} {
		var stdout, stderr bytes.Buffer
		called := false
		exitCode := run(t.Context(), test.args, func(string) string { return "configured" }, &stdout, &stderr,
			func(_ context.Context, getenv func(string) string, output, _ io.Writer, mode sandboxGatewayServeMode) error {
				called = true
				if mode != test.mode || getenv("key") != "configured" {
					t.Fatalf("serve mode = %d", mode)
				}
				fmt.Fprintln(output, "serving")
				return nil
			})
		if exitCode != 0 || !called || stdout.String() != "serving\n" || stderr.Len() != 0 {
			t.Fatalf("run(%v) = %d called=%v stdout=%q stderr=%q", test.args, exitCode, called, stdout.String(), stderr.String())
		}
	}
}

func TestSandboxGatewayRunRejectsUnknownMode(t *testing.T) {
	var stderr bytes.Buffer
	if exitCode := run(t.Context(), []string{"unknown"}, func(string) string { return "" }, io.Discard, &stderr, nil); exitCode != 2 ||
		!strings.Contains(stderr.String(), "sandbox-gateway serve --insecure-dev") {
		t.Fatalf("run() = %d stderr=%q", exitCode, stderr.String())
	}
}
