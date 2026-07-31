package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestRunServesBrowserGateway(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	called := false
	exitCode := run(t.Context(), []string{"serve"}, func(string) string { return "configured" }, &stdout, &stderr,
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

func TestRunRejectsUnknownBrowserGatewayCommand(t *testing.T) {
	var stderr bytes.Buffer
	if exitCode := run(t.Context(), []string{"unknown"}, func(string) string { return "" }, io.Discard, &stderr, nil); exitCode != 2 || !strings.Contains(stderr.String(), "usage: browser-gateway serve") {
		t.Fatalf("run() = %d, stderr %q", exitCode, stderr.String())
	}
}

func TestRunReportsBrowserGatewayFailure(t *testing.T) {
	var stderr bytes.Buffer
	exitCode := run(t.Context(), []string{"serve"}, func(string) string { return "" }, io.Discard, &stderr,
		func(context.Context, func(string) string, io.Writer) error { return errors.New("configuration failed") })
	if exitCode != 1 || !strings.Contains(stderr.String(), "configuration failed") {
		t.Fatalf("run() = %d, stderr %q", exitCode, stderr.String())
	}
}
