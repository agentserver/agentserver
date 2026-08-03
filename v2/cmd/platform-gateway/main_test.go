package main

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestRunRequiresServeCommand(t *testing.T) {
	called := false
	serve := func(context.Context, func(string) string, io.Writer) error { called = true; return nil }
	for _, args := range [][]string{nil, {"other"}, {"serve", "extra"}} {
		var stdout, stderr strings.Builder
		if status := run(t.Context(), args, func(string) string { return "" }, &stdout, &stderr, serve); status != 2 || called {
			t.Fatalf("run(%v) = %d called=%v stderr=%q", args, status, called, stderr.String())
		}
	}
}
