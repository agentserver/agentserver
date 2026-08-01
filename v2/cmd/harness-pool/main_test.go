package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestRunProductionServe(t *testing.T) {
	assertHarnessPoolRunMode(t, []string{"serve"}, harnessPoolServeProduction)
}

func TestRunInsecureDevelopmentServe(t *testing.T) {
	assertHarnessPoolRunMode(t, []string{"serve", "--insecure-dev"}, harnessPoolServeInsecureDevelopment)
}

func assertHarnessPoolRunMode(t *testing.T, arguments []string, wantMode harnessPoolServeMode) {
	t.Helper()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	called := false
	exitCode := run(
		t.Context(), arguments,
		func(string) string { return "configured" }, &stdout, &stderr,
		func(_ context.Context, getenv func(string) string, output, errors io.Writer, mode harnessPoolServeMode) error {
			called = true
			if getenv("value") != "configured" || errors != &stderr || mode != wantMode {
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

func TestRunRejectsInvalidServeArguments(t *testing.T) {
	for _, arguments := range [][]string{
		nil,
		{"run"},
		{"serve", "--production"},
		{"serve", "--insecure-dev", "extra"},
	} {
		var stderr bytes.Buffer
		called := false
		exitCode := run(t.Context(), arguments, func(string) string { return "" }, io.Discard, &stderr,
			func(context.Context, func(string) string, io.Writer, io.Writer, harnessPoolServeMode) error {
				called = true
				return nil
			})
		if exitCode != 2 || called || !strings.Contains(stderr.String(), "harness-pool serve") {
			t.Fatalf("run(%q) = %d, called %v, stderr %q", arguments, exitCode, called, stderr.String())
		}
	}
}
