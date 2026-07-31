package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRunAcceptsOnlyFixedOneShotWorkerCommand(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "worker.json")
	requested := make([]uintptr, 0, 3)
	files := make([]*os.File, 0, 3)
	stderr := &bytes.Buffer{}
	exitCode := run(t.Context(), []string{
		"run", "--config=" + configPath, "--bootstrap-fd=3", "--prompt-fd=4", "--checkpoint-fd=5",
	}, stderr, workerCommandFunctions{
		inheritedFile: func(descriptor uintptr, name string) *os.File {
			requested = append(requested, descriptor)
			file, err := os.CreateTemp(t.TempDir(), name)
			if err != nil {
				t.Fatal(err)
			}
			files = append(files, file)
			return file
		},
		execute: func(_ context.Context, gotPath string, bootstrap, prompt, checkpoint *os.File) error {
			if gotPath != configPath || bootstrap != files[0] || prompt != files[1] || checkpoint != files[2] {
				t.Fatalf("execute inputs = %q / %p %p %p", gotPath, bootstrap, prompt, checkpoint)
			}
			for _, file := range files {
				_ = file.Close()
			}
			return nil
		},
	})
	if exitCode != 0 || stderr.Len() != 0 || !reflect.DeepEqual(requested, []uintptr{3, 4, 5}) {
		t.Fatalf("run() = %d stderr=%q descriptors=%v", exitCode, stderr.String(), requested)
	}
}

func TestRunRejectsOpenEndedWorkerArguments(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "worker.json")
	tests := [][]string{
		nil,
		{"serve", "--config=" + configPath, "--bootstrap-fd=3", "--prompt-fd=4"},
		{"run", "--config=relative.json", "--bootstrap-fd=3", "--prompt-fd=4"},
		{"run", "--config=" + configPath, "--bootstrap-fd=9", "--prompt-fd=4"},
		{"run", "--config=" + configPath, "--bootstrap-fd=3", "--prompt-fd=4", "--checkpoint-fd=6"},
		{"run", "--config=" + configPath, "--bootstrap-fd=3", "--prompt-fd=4", "--endpoint=https://evil.test"},
	}
	for _, arguments := range tests {
		stderr := &bytes.Buffer{}
		called := false
		exitCode := run(t.Context(), arguments, stderr, workerCommandFunctions{
			execute:       func(context.Context, string, *os.File, *os.File, *os.File) error { called = true; return nil },
			inheritedFile: func(uintptr, string) *os.File { called = true; return nil },
		})
		if exitCode != 2 || called || !strings.Contains(stderr.String(), "usage:") {
			t.Fatalf("run(%v) = %d called=%t stderr=%q", arguments, exitCode, called, stderr.String())
		}
	}
}

func TestRunRedactsWorkerExecutionInputs(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "worker.json")
	secret := "must-not-appear"
	stderr := &bytes.Buffer{}
	exitCode := run(t.Context(), []string{
		"run", "--config=" + configPath, "--bootstrap-fd=3", "--prompt-fd=4",
	}, stderr, workerCommandFunctions{
		inheritedFile: func(_ uintptr, name string) *os.File {
			file, err := os.CreateTemp(t.TempDir(), name)
			if err != nil {
				t.Fatal(err)
			}
			return file
		},
		execute: func(_ context.Context, _ string, bootstrap, prompt, _ *os.File) error {
			_ = bootstrap.Close()
			_ = prompt.Close()
			return errors.New("synthetic worker failure")
		},
	})
	if exitCode != 1 || !strings.Contains(stderr.String(), "synthetic worker failure") || strings.Contains(stderr.String(), secret) {
		t.Fatalf("run() = %d stderr=%q", exitCode, stderr.String())
	}
}
