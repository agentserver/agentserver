package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestRunRequiresExplicitInsecureDevMode(t *testing.T) {
	var stderr bytes.Buffer
	exitCode := run(t.Context(), []string{"serve"}, func(string) string { return "" }, &bytes.Buffer{}, &stderr, nil)
	if exitCode != 2 || !strings.Contains(stderr.String(), "production agentx OAuth key binding is not implemented") {
		t.Fatalf("run() = %d, stderr %q", exitCode, stderr.String())
	}
}

func TestRunInsecureDevServe(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	called := false
	exitCode := run(t.Context(), []string{"serve", "--insecure-dev"}, func(string) string { return "configured" }, &stdout, &stderr,
		func(_ context.Context, getenv func(string) string, output io.Writer) error {
			called = true
			if getenv("value") != "configured" {
				t.Fatal("getenv was not forwarded")
			}
			fmt.Fprintln(output, "serving")
			return nil
		})
	if exitCode != 0 || !called || stdout.String() != "serving\n" || stderr.Len() != 0 {
		t.Fatalf("run() = %d, called %v, stdout %q, stderr %q", exitCode, called, stdout.String(), stderr.String())
	}
}

func TestRequireLoopbackAddress(t *testing.T) {
	for _, address := range []string{"127.0.0.1:8443", "[::1]:8443", "localhost:8443"} {
		if err := requireLoopbackAddress(address); err != nil {
			t.Fatalf("requireLoopbackAddress(%q) error = %v", address, err)
		}
	}
	if err := requireLoopbackAddress(":8443"); err == nil {
		t.Fatal("wildcard insecure-dev listen address was accepted")
	}
}
