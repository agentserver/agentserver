package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/devstack"
)

func TestRunPrepareRequiresExactDevelopmentMode(t *testing.T) {
	for _, arguments := range [][]string{
		nil,
		{"prepare"},
		{"prepare", "--config=/tmp/config.json", "--insecure-dev", "--output-dir=/tmp/output"},
		{"prepare", "--insecure-dev", "--config=", "--output-dir=/tmp/output"},
		{"prepare", "--insecure-dev", "--config=/tmp/config.json", "--output-dir="},
	} {
		var stderr bytes.Buffer
		called := false
		exitCode := run(context.Background(), arguments, &bytes.Buffer{}, &stderr, commandFunctions{
			prepare: func(string, string) (devstack.Result, error) {
				called = true
				return devstack.Result{}, nil
			},
		})
		if exitCode != 2 || called || !strings.Contains(stderr.String(), "prepare --insecure-dev") {
			t.Fatalf("run(%v) = %d, called=%v, stderr=%q", arguments, exitCode, called, stderr.String())
		}
	}
}

func TestRunPrepareReportsCreatedMaterial(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := run(
		context.Background(),
		[]string{"prepare", "--insecure-dev", "--config=/tmp/config.json", "--output-dir=/tmp/output"},
		&stdout, &stderr,
		commandFunctions{prepare: func(configPath, outputDirectory string) (devstack.Result, error) {
			if configPath != "/tmp/config.json" || outputDirectory != "/tmp/output" {
				t.Fatalf("prepare inputs = %q, %q", configPath, outputDirectory)
			}
			return devstack.Result{
				OutputDirectory: "/tmp/output", MetadataFile: "/tmp/output/metadata.json",
				BootstrapConfigFile: "/tmp/output/config/core-bootstrap.json",
				FixturesConfigFile:  "/tmp/output/config/dev-fixtures.json",
			}, nil
		}},
	)
	if exitCode != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), "INSECURE DEV material created") ||
		!strings.Contains(stdout.String(), "/tmp/output/config/core-bootstrap.json") {
		t.Fatalf("prepare result = exit %d, stdout=%q, stderr=%q", exitCode, stdout.String(), stderr.String())
	}
}

func TestRunPrepareReportsFailure(t *testing.T) {
	var stderr bytes.Buffer
	exitCode := run(
		context.Background(),
		[]string{"prepare", "--insecure-dev", "--config=/tmp/config.json", "--output-dir=/tmp/output"},
		&bytes.Buffer{}, &stderr,
		commandFunctions{prepare: func(string, string) (devstack.Result, error) {
			return devstack.Result{}, errors.New("output already exists")
		}},
	)
	if exitCode != 1 || !strings.Contains(stderr.String(), "output already exists") {
		t.Fatalf("prepare failure = exit %d, stderr=%q", exitCode, stderr.String())
	}
}

func TestRunFixturesRequiresExactDevelopmentMode(t *testing.T) {
	for _, arguments := range [][]string{
		{"fixtures"},
		{"fixtures", "--bundle=/tmp/output", "--insecure-dev"},
		{"fixtures", "--insecure-dev", "--bundle="},
		{"fixtures", "--insecure-dev", "--bundle=/tmp/output", "extra"},
	} {
		var stderr bytes.Buffer
		called := false
		exitCode := run(context.Background(), arguments, &bytes.Buffer{}, &stderr, commandFunctions{
			fixtures: func(context.Context, string, io.Writer) error {
				called = true
				return nil
			},
		})
		if exitCode != 2 || called || !strings.Contains(stderr.String(), "fixtures --insecure-dev") {
			t.Fatalf("run(%v) = %d, called=%v, stderr=%q", arguments, exitCode, called, stderr.String())
		}
	}
}

func TestRunFixturesServesPreparedBundle(t *testing.T) {
	var stdout, stderr bytes.Buffer
	ctx := context.Background()
	exitCode := run(ctx, []string{"fixtures", "--insecure-dev", "--bundle=/tmp/output"}, &stdout, &stderr, commandFunctions{
		fixtures: func(gotContext context.Context, bundle string, writer io.Writer) error {
			if gotContext != ctx || bundle != "/tmp/output" || writer != &stdout {
				t.Fatalf("fixture inputs = %v, %q, %T", gotContext, bundle, writer)
			}
			return nil
		},
	})
	if exitCode != 0 || stderr.Len() != 0 {
		t.Fatalf("fixtures result = exit %d stderr=%q", exitCode, stderr.String())
	}
}

func TestRunFixturesReportsFailure(t *testing.T) {
	var stderr bytes.Buffer
	exitCode := run(context.Background(), []string{"fixtures", "--insecure-dev", "--bundle=/tmp/output"}, &bytes.Buffer{}, &stderr, commandFunctions{
		fixtures: func(context.Context, string, io.Writer) error { return errors.New("listener occupied") },
	})
	if exitCode != 1 || !strings.Contains(stderr.String(), "listener occupied") {
		t.Fatalf("fixtures failure = exit %d stderr=%q", exitCode, stderr.String())
	}
}
