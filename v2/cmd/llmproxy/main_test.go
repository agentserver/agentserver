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

func TestRunServesProductionLLMProxy(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	called := false
	exitCode := run(t.Context(), []string{"serve"}, func(string) string { return "configured" }, &stdout, &stderr,
		func(_ context.Context, getenv func(string) string, output io.Writer) error {
			called = true
			if getenv("value") != "configured" {
				t.Fatal("configuration source was not forwarded")
			}
			fmt.Fprintln(output, "serving")
			return nil
		})
	if exitCode != 0 || !called || stdout.String() != "serving\n" || stderr.Len() != 0 {
		t.Fatalf("run() = %d, called %v, stdout %q, stderr %q", exitCode, called, stdout.String(), stderr.String())
	}
}

func TestRunRejectsInvalidArgumentsAndReportsServeFailure(t *testing.T) {
	for _, arguments := range [][]string{nil, {"run"}, {"serve", "--insecure-dev"}} {
		var stderr bytes.Buffer
		called := false
		exitCode := run(t.Context(), arguments, func(string) string { return "" }, io.Discard, &stderr,
			func(context.Context, func(string) string, io.Writer) error {
				called = true
				return nil
			})
		if exitCode != 2 || called || !strings.Contains(stderr.String(), "usage: llmproxy serve") {
			t.Fatalf("run(%q) = %d, called %v, stderr %q", arguments, exitCode, called, stderr.String())
		}
	}
	var stderr bytes.Buffer
	exitCode := run(t.Context(), []string{"serve"}, func(string) string { return "" }, io.Discard, &stderr,
		func(context.Context, func(string) string, io.Writer) error { return errors.New("startup failed") })
	if exitCode != 1 || !strings.Contains(stderr.String(), "startup failed") {
		t.Fatalf("failed run = %d, stderr %q", exitCode, stderr.String())
	}
}
