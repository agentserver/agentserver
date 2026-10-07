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

func TestRepositoryGitHubActionsKeepPublishingOnClusterRunner(t *testing.T) {
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
		publicFrontend := isPublicFrontendBuild(entry.Name(), string(contents))
		for index, line := range strings.Split(string(contents), "\n") {
			trimmed := strings.TrimSpace(line)
			if !strings.HasPrefix(trimmed, "runs-on:") {
				continue
			}
			declarations++
			if trimmed != "runs-on: "+repositoryCIRunner && !(publicFrontend && trimmed == "runs-on: ubuntu-latest") {
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

// The upstream frontend compiler needs more memory than the SG publisher.
// Only this public-source artifact build may run outside the cluster; no
// secrets, registry writes or deployment tools are admitted by this exception.
func isPublicFrontendBuild(name, contents string) bool {
	if name != "dsh-frontend.yml" || strings.Count(contents, "runs-on:") != 1 {
		return false
	}
	for _, required := range []string{"workflow_call:", "permissions:\n  contents: read", "persist-credentials: false", "git submodule update --init third_party/deepseek-harness", "bash v2/dsh-web/build.sh", "actions/upload-artifact@v4", "name: dsh-frontend"} {
		if !strings.Contains(contents, required) {
			return false
		}
	}
	for _, forbidden := range []string{"secrets.", "secrets:", "packages:", "write-all", "id-token:", "docker", "kubectl", "pulumi", "ghcr.io", "hub.byted.org"} {
		if strings.Contains(contents, forbidden) {
			return false
		}
	}
	return true
}

func TestPublicFrontendRunnerExceptionDoesNotAdmitPublishing(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), "../../../.github/workflows/dsh-frontend.yml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !isPublicFrontendBuild("dsh-frontend.yml", text) {
		t.Fatal("public build not recognized")
	}
	if isPublicFrontendBuild("v2-kubernetes.yml", text) {
		t.Fatal("exception escaped frontend workflow")
	}
	for _, bad := range []string{"secrets.TOKEN", "packages: write", "docker push ghcr.io/agentserver/test", "kubectl apply", "pulumi up", "runs-on: ubuntu-latest"} {
		if isPublicFrontendBuild("dsh-frontend.yml", text+"\n"+bad) {
			t.Fatalf("accepted %q in frontend-only workflow", bad)
		}
	}
	if isPublicFrontendBuild("dsh-frontend.yml", strings.ReplaceAll(text, "persist-credentials: false", "persist-credentials: true")) {
		t.Fatal("persisted checkout credential")
	}
}
