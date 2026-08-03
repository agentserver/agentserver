package main

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestExactArgumentsRejectsMissingPrefixDuplicateAndUnknown(t *testing.T) {
	valid, ok := exactArguments([]string{"--source=/source", "--destination=/destination", "--uid=65531", "--gid=65531"}, "source", "destination", "uid", "gid")
	if !ok || valid["uid"] != "65531" {
		t.Fatalf("valid exact arguments = %v, %v", valid, ok)
	}
	for _, arguments := range [][]string{
		{"source=/source", "--destination=/destination", "--uid=65531", "--gid=65531"},
		{"--source=/source", "--destination=/destination", "--uid=65531", "--uid=65532"},
		{"--source=/source", "--destination=/destination", "--uid=65531", "--future=x"},
	} {
		if _, ok := exactArguments(arguments, "source", "destination", "uid", "gid"); ok {
			t.Fatalf("unsafe arguments %v were accepted", arguments)
		}
	}
}

func TestRunRejectsInvalidDirectoryIdentity(t *testing.T) {
	var stderr bytes.Buffer
	exitCode := run(
		[]string{"prepare-harness-directories", "--runtime=/runtime", "--checkpoint=/checkpoint", "--scratch=/scratch", "--uid=0", "--gid=65531"},
		io.Discard, &stderr,
	)
	if exitCode != 2 || !strings.Contains(stderr.String(), "unprivileged") {
		t.Fatalf("run = %d, stderr %q", exitCode, stderr.String())
	}
}
