package main

import (
	"bytes"
	"errors"
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
		exitCode := run(arguments, &bytes.Buffer{}, &stderr, func(string, string) (devstack.Result, error) {
			called = true
			return devstack.Result{}, nil
		})
		if exitCode != 2 || called || !strings.Contains(stderr.String(), "prepare --insecure-dev") {
			t.Fatalf("run(%v) = %d, called=%v, stderr=%q", arguments, exitCode, called, stderr.String())
		}
	}
}

func TestRunPrepareReportsCreatedMaterial(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := run(
		[]string{"prepare", "--insecure-dev", "--config=/tmp/config.json", "--output-dir=/tmp/output"},
		&stdout, &stderr,
		func(configPath, outputDirectory string) (devstack.Result, error) {
			if configPath != "/tmp/config.json" || outputDirectory != "/tmp/output" {
				t.Fatalf("prepare inputs = %q, %q", configPath, outputDirectory)
			}
			return devstack.Result{
				OutputDirectory: "/tmp/output", MetadataFile: "/tmp/output/metadata.json",
				BootstrapConfigFile: "/tmp/output/config/core-bootstrap.json",
			}, nil
		},
	)
	if exitCode != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), "INSECURE DEV material created") ||
		!strings.Contains(stdout.String(), "/tmp/output/config/core-bootstrap.json") {
		t.Fatalf("prepare result = exit %d, stdout=%q, stderr=%q", exitCode, stdout.String(), stderr.String())
	}
}

func TestRunPrepareReportsFailure(t *testing.T) {
	var stderr bytes.Buffer
	exitCode := run(
		[]string{"prepare", "--insecure-dev", "--config=/tmp/config.json", "--output-dir=/tmp/output"},
		&bytes.Buffer{}, &stderr,
		func(string, string) (devstack.Result, error) {
			return devstack.Result{}, errors.New("output already exists")
		},
	)
	if exitCode != 1 || !strings.Contains(stderr.String(), "output already exists") {
		t.Fatalf("prepare failure = exit %d, stderr=%q", exitCode, stderr.String())
	}
}
