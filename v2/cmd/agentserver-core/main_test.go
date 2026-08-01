package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
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
	}, &stdout, &stderr, commandFunctions{migrate: migrate})
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
		commandFunctions{migrate: func(context.Context, string) (coredb.MigrationResult, error) {
			t.Fatal("migration function must not be called")
			return coredb.MigrationResult{}, nil
		}})
	if exitCode != 2 || !strings.Contains(stderr.String(), databaseURLEnvironment+" is required") {
		t.Fatalf("run() exit code = %d, stderr = %q", exitCode, stderr.String())
	}
}

func TestRunRejectsUnknownCommand(t *testing.T) {
	var stderr bytes.Buffer
	exitCode := run(t.Context(), []string{"unknown"}, func(string) string { return "" }, &bytes.Buffer{}, &stderr, commandFunctions{})
	if exitCode != 2 || !strings.Contains(stderr.String(), "usage: agentserver-core <migrate|serve>") {
		t.Fatalf("run() exit code = %d, stderr = %q", exitCode, stderr.String())
	}
}

func TestRunReportsMigrationFailure(t *testing.T) {
	var stderr bytes.Buffer
	exitCode := run(t.Context(), []string{"migrate"}, func(string) string { return "postgres://configured" }, &bytes.Buffer{}, &stderr,
		commandFunctions{migrate: func(context.Context, string) (coredb.MigrationResult, error) {
			return coredb.MigrationResult{}, errors.New("checksum mismatch")
		}})
	if exitCode != 1 || !strings.Contains(stderr.String(), "checksum mismatch") {
		t.Fatalf("run() exit code = %d, stderr = %q", exitCode, stderr.String())
	}
}

func TestRunServe(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	called := false
	exitCode := run(t.Context(), []string{"serve"}, func(string) string { return "configured" }, &stdout, &stderr, commandFunctions{
		serve: func(_ context.Context, getenv func(string) string, output io.Writer) error {
			called = true
			if getenv("anything") != "configured" {
				t.Fatal("serve getenv was not forwarded")
			}
			fmt.Fprintln(output, "serving")
			return nil
		},
	})
	if exitCode != 0 || !called || stdout.String() != "serving\n" || stderr.Len() != 0 {
		t.Fatalf("serve result = exit %d, called %v, stdout %q, stderr %q", exitCode, called, stdout.String(), stderr.String())
	}
}

func TestRunInsecureDevelopmentBootstrap(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	called := false
	exitCode := run(
		t.Context(), []string{"bootstrap", "--insecure-dev", "--config=/absolute/bootstrap.json"},
		func(name string) string {
			if name != databaseURLEnvironment {
				t.Fatalf("environment lookup = %q", name)
			}
			return "postgres://configured"
		},
		&stdout, &stderr,
		commandFunctions{bootstrap: func(_ context.Context, databaseURL, configPath string) (developmentBootstrapResult, error) {
			called = true
			if databaseURL != "postgres://configured" || configPath != "/absolute/bootstrap.json" {
				t.Fatalf("bootstrap inputs = %q, %q", databaseURL, configPath)
			}
			return developmentBootstrapResult{
				Migration:   coredb.MigrationResult{CurrentVersion: 13},
				Bootstrap:   coredb.InsecureDevelopmentBootstrapResult{CreatedRows: 7},
				WorkspaceID: "workspace", SessionID: "session", ActorID: "actor",
				ExecutorID: "executor", EnvironmentID: "environment",
			}, nil
		}},
	)
	if exitCode != 0 || !called || stderr.Len() != 0 ||
		!strings.Contains(stdout.String(), "INSECURE DEV workspace workspace") ||
		!strings.Contains(stdout.String(), "schema 0013; created 7 row(s)") {
		t.Fatalf("bootstrap result = exit %d, called %v, stdout %q, stderr %q", exitCode, called, stdout.String(), stderr.String())
	}
}

func TestRunInsecureDevelopmentBootstrapRequiresExactMode(t *testing.T) {
	for _, args := range [][]string{
		{"bootstrap"},
		{"bootstrap", "--config=/absolute/bootstrap.json", "--insecure-dev"},
		{"bootstrap", "--insecure-dev", "--config="},
	} {
		var stderr bytes.Buffer
		exitCode := run(t.Context(), args, func(string) string { return "postgres://configured" }, io.Discard, &stderr, commandFunctions{})
		if exitCode != 2 || !strings.Contains(stderr.String(), "bootstrap --insecure-dev") {
			t.Fatalf("run(%v) = %d, stderr %q", args, exitCode, stderr.String())
		}
	}
}
