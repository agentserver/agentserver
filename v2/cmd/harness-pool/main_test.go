package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestRunRequiresExplicitInsecureDevelopmentMode(t *testing.T) {
	var stderr bytes.Buffer
	exitCode := run(t.Context(), []string{"serve"}, func(string) string { return "" }, io.Discard, &stderr, nil)
	if exitCode != 2 || !strings.Contains(stderr.String(), "production capability issuance") {
		t.Fatalf("run() = %d, stderr %q", exitCode, stderr.String())
	}
}

func TestRunInsecureDevelopmentServe(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	called := false
	exitCode := run(
		t.Context(), []string{"serve", "--insecure-dev"},
		func(string) string { return "configured" }, &stdout, &stderr,
		func(_ context.Context, getenv func(string) string, output, errors io.Writer) error {
			called = true
			if getenv("value") != "configured" || errors != &stderr {
				t.Fatal("serve inputs were not forwarded")
			}
			fmt.Fprintln(output, "serving")
			return nil
		},
	)
	if exitCode != 0 || !called || stdout.String() != "serving\n" || stderr.Len() != 0 {
		t.Fatalf("run() = %d, called %v, stdout %q, stderr %q", exitCode, called, stdout.String(), stderr.String())
	}
}
