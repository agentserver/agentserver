package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunRejectsUnknownAndIncompleteImageCommands(t *testing.T) {
	for _, arguments := range [][]string{
		nil,
		{"unknown"},
		{"prepare", "--kind=service"},
		{"verify-oci", "--manifest=/missing", "--archive=/missing"},
		{"verify-tar", "--manifest=/missing", "--tar=/missing"},
	} {
		var stdout, stderr bytes.Buffer
		if code := run(arguments, &stdout, &stderr); code == 0 || stderr.Len() == 0 {
			t.Fatalf("run(%v) = %d stdout=%q stderr=%q", arguments, code, stdout.String(), stderr.String())
		}
	}
}

func TestOpenDirectFileRejectsSymlink(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "manifest.json")
	if err := writeTestFile(target, []byte("{}")); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.json")
	if err := createTestSymlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := openDirectFile("manifest", link); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink error = %v", err)
	}
}
