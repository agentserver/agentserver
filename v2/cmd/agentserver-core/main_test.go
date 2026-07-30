package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/coredb"
)

func TestRunMigrate(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	migrate := func(_ context.Context, databaseURL string) (coredb.MigrationResult, error) {
		if databaseURL != "postgres://configured" {
			t.Fatalf("database URL = %q", databaseURL)
		}
		return coredb.MigrationResult{Applied: 1, CurrentVersion: 1}, nil
	}

	exitCode := run(t.Context(), []string{"migrate"}, func(name string) string {
		if name != databaseURLEnvironment {
			t.Fatalf("environment lookup = %q", name)
		}
		return "postgres://configured"
	}, &stdout, &stderr, migrate)
	if exitCode != 0 {
		t.Fatalf("run() exit code = %d, stderr = %q", exitCode, stderr.String())
	}
	if !strings.Contains(stdout.String(), "version 0001; applied 1 migration(s)") {
		t.Fatalf("run() stdout = %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("run() stderr = %q", stderr.String())
	}
}

func TestRunRejectsMissingConfiguration(t *testing.T) {
	var stderr bytes.Buffer
	exitCode := run(t.Context(), []string{"migrate"}, func(string) string { return "" }, &bytes.Buffer{}, &stderr,
		func(context.Context, string) (coredb.MigrationResult, error) {
			t.Fatal("migration function must not be called")
			return coredb.MigrationResult{}, nil
		})
	if exitCode != 2 || !strings.Contains(stderr.String(), databaseURLEnvironment+" is required") {
		t.Fatalf("run() exit code = %d, stderr = %q", exitCode, stderr.String())
	}
}

func TestRunRejectsUnknownCommand(t *testing.T) {
	var stderr bytes.Buffer
	exitCode := run(t.Context(), []string{"serve"}, func(string) string { return "" }, &bytes.Buffer{}, &stderr, nil)
	if exitCode != 2 || !strings.Contains(stderr.String(), "usage: agentserver-core migrate") {
		t.Fatalf("run() exit code = %d, stderr = %q", exitCode, stderr.String())
	}
}

func TestRunReportsMigrationFailure(t *testing.T) {
	var stderr bytes.Buffer
	exitCode := run(t.Context(), []string{"migrate"}, func(string) string { return "postgres://configured" }, &bytes.Buffer{}, &stderr,
		func(context.Context, string) (coredb.MigrationResult, error) {
			return coredb.MigrationResult{}, errors.New("checksum mismatch")
		})
	if exitCode != 1 || !strings.Contains(stderr.String(), "checksum mismatch") {
		t.Fatalf("run() exit code = %d, stderr = %q", exitCode, stderr.String())
	}
}
