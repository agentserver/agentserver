package main

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestRunPassesOnlyExplicitProcessInputsToFinalExec(t *testing.T) {
	arguments := []string{"--program=/opt/codex", "--directory=/empty", "--expected-uid=1", "--expected-gid=2"}
	environment := []string{"CODEX_HOME=/codex", "AGENTSERVER_LLM_CAPABILITY=sensitive"}
	called := false
	exitCode := run(arguments, environment, &bytes.Buffer{}, func(gotArguments, gotEnvironment []string) error {
		called = true
		if !reflect.DeepEqual(gotArguments, arguments) || !reflect.DeepEqual(gotEnvironment, environment) {
			t.Fatalf("execute inputs = %v / %v", gotArguments, gotEnvironment)
		}
		return nil
	})
	if exitCode != 0 || !called {
		t.Fatalf("run() = %d, called=%t", exitCode, called)
	}
}

func TestRunReportsFailureWithoutDumpingEnvironment(t *testing.T) {
	const secret = "must-not-be-logged"
	stderr := &bytes.Buffer{}
	exitCode := run(nil, []string{"TOKEN=" + secret}, stderr, func([]string, []string) error {
		return errors.New("synthetic final exec failure")
	})
	if exitCode != 1 || !strings.Contains(stderr.String(), "synthetic final exec failure") || strings.Contains(stderr.String(), secret) {
		t.Fatalf("run() = %d stderr=%q", exitCode, stderr.String())
	}
}
