package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/productiondeploy"
)

func TestRunRenderUsesExactClosedArgumentsAndReportsChecksums(t *testing.T) {
	var stdout, stderr bytes.Buffer
	called := []string{}
	bundle := productiondeploy.Bundle{Files: []productiondeploy.RenderedFile{{Name: "file.json", SHA256: strings.Repeat("a", 64), Content: []byte("x")}}}
	exitCode := run(
		[]string{"render", "--output=/absolute/output", "--config=/absolute/config.json"},
		&stdout, &stderr,
		deployCommands{
			load: func(path string) (productiondeploy.LoadedConfig, error) {
				called = append(called, "load:"+path)
				return productiondeploy.LoadedConfig{}, nil
			},
			render: func(productiondeploy.LoadedConfig) (productiondeploy.Bundle, error) {
				called = append(called, "render")
				return bundle, nil
			},
			write: func(actual productiondeploy.Bundle, path string) error {
				called = append(called, "write:"+path)
				if len(actual.Files) != 1 {
					t.Fatal("writer received wrong bundle")
				}
				return nil
			},
		},
	)
	if exitCode != 0 || stderr.Len() != 0 || strings.Join(called, ",") != "load:/absolute/config.json,render,write:/absolute/output" ||
		!strings.Contains(stdout.String(), strings.Repeat("a", 64)+"  file.json") {
		t.Fatalf("run = %d, calls %v, stdout %q, stderr %q", exitCode, called, stdout.String(), stderr.String())
	}
}

func TestRunRejectsDuplicateUnknownAndMissingArguments(t *testing.T) {
	for _, arguments := range [][]string{
		{}, {"render"},
		{"render", "--config=/a", "--config=/b"},
		{"render", "--config=/a", "--future=/b"},
		{"validate", "config=/a"},
	} {
		if exitCode := run(arguments, &bytes.Buffer{}, &bytes.Buffer{}, deployCommands{}); exitCode != 2 {
			t.Fatalf("arguments %v exit = %d", arguments, exitCode)
		}
	}
}

func TestRunStopsBeforeWriteOnRenderFailure(t *testing.T) {
	exitCode := run(
		[]string{"render", "--config=/absolute/config", "--output=/absolute/output"},
		&bytes.Buffer{}, &bytes.Buffer{},
		deployCommands{
			load: func(string) (productiondeploy.LoadedConfig, error) { return productiondeploy.LoadedConfig{}, nil },
			render: func(productiondeploy.LoadedConfig) (productiondeploy.Bundle, error) {
				return productiondeploy.Bundle{}, errors.New("render failed")
			},
			write: func(productiondeploy.Bundle, string) error {
				t.Fatal("write called after render failure")
				return nil
			},
		},
	)
	if exitCode != 1 {
		t.Fatalf("render failure exit = %d", exitCode)
	}
}
