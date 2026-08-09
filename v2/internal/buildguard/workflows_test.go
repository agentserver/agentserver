package buildguard

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const repositoryCIRunner = "k8s-sg"

func TestRepositoryGitHubActionsUseClusterRunner(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller could not locate the buildguard package")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", ".."))
	workflowRoot := filepath.Join(repositoryRoot, ".github", "workflows")
	entries, err := os.ReadDir(workflowRoot)
	if err != nil {
		t.Fatalf("read GitHub workflow directory: %v", err)
	}

	declarations := 0
	var violations []string
	for _, entry := range entries {
		if entry.IsDir() || (filepath.Ext(entry.Name()) != ".yml" && filepath.Ext(entry.Name()) != ".yaml") {
			continue
		}
		contents, err := os.ReadFile(filepath.Join(workflowRoot, entry.Name()))
		if err != nil {
			t.Fatalf("read GitHub workflow %s: %v", entry.Name(), err)
		}
		for index, line := range strings.Split(string(contents), "\n") {
			trimmed := strings.TrimSpace(line)
			if !strings.HasPrefix(trimmed, "runs-on:") {
				continue
			}
			declarations++
			if trimmed != "runs-on: "+repositoryCIRunner {
				violations = append(violations, fmt.Sprintf("%s:%d: %s", entry.Name(), index+1, trimmed))
			}
		}
	}
	if declarations == 0 {
		t.Fatal("repository GitHub workflows contain no runs-on declarations")
	}
	if len(violations) != 0 {
		t.Fatalf("repository CI jobs must use runs-on: %s:\n%s", repositoryCIRunner, strings.Join(violations, "\n"))
	}
}
