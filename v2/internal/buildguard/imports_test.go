package buildguard

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestFindV1RuntimeImports(t *testing.T) {
	root := t.TempDir()
	writeSource(t, root, "allowed.go", `package sample

import "github.com/agentserver/agentserver/v2/internal/codexwire"
`)
	writeSource(t, root, "nested/forbidden.go", `package nested

import (
	"fmt"
	legacy "github.com/agentserver/agentserver/internal/server"
)

var _ = fmt.Sprintf
var _ = legacy.Server{}
`)

	violations, err := FindV1RuntimeImports(root)
	if err != nil {
		t.Fatalf("FindV1RuntimeImports() error = %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("FindV1RuntimeImports() returned %d violations, want 1: %v", len(violations), violations)
	}
	if got, want := violations[0].File, "nested/forbidden.go"; got != want {
		t.Errorf("violation file = %q, want %q", got, want)
	}
	if got, want := violations[0].ImportPath, v1RuntimeImportPrefix+"/server"; got != want {
		t.Errorf("violation import = %q, want %q", got, want)
	}
}

func TestRepositoryHasNoV1RuntimeImports(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller could not locate the buildguard package")
	}
	moduleRoot := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))

	violations, err := FindV1RuntimeImports(moduleRoot)
	if err != nil {
		t.Fatalf("scan v2 module: %v", err)
	}
	if len(violations) == 0 {
		return
	}

	var lines []string
	for _, violation := range violations {
		lines = append(lines, violation.String())
	}
	t.Fatalf("v2 must not import the v1 runtime:\n%s", strings.Join(lines, "\n"))
}

func writeSource(t *testing.T, root, name, contents string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create source directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
}
