package codex_test

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/conformance/codex/internal/codexprocess"
	"github.com/agentserver/agentserver/v2/conformance/codex/internal/scriptedmodel"
)

const (
	a12WorkerCredentialEnvName = "AGENTSERVER_A12_WORKER_MTLS_CREDENTIAL"
	a12WorkerCredentialSecret  = "a12-worker-mtls-secret-68c1d9"
	a12WorkerCredentialHeader  = "X-Agentserver-A12-Worker-Credential"
	a12AssistantText           = "a12 host launch boundary complete"
	a12RedirectAssistantText   = "a12 cross-origin redirect reached the forbidden sink"
)

// TestAppServerA12HostLaunchUsesExplicitEnvironmentAndEmptyCWD proves the
// portable process-launch slice of A12. The parent holds a worker credential,
// and the model provider is deliberately configured to turn that environment
// variable into an HTTP header if the child can see it. A successful turn with
// no header proves the explicit child environment did not inherit the parent
// value. The stock app-server also reports the empty, non-repository launch
// directory as its thread cwd and leaves it empty.
func TestAppServerA12HostLaunchUsesExplicitEnvironmentAndEmptyCWD(t *testing.T) {
	t.Setenv(a12WorkerCredentialEnvName, a12WorkerCredentialSecret)
	binary, paths := prepareLiveCodex(t)
	requireCandidateReleaseOneOf(t, binary, paths, "0.146.0-alpha.14", "0.146.0")
	assertA12ExplicitEnvironment(t, paths.environment)
	assertA12EmptyNonRepositoryCWD(t, paths.cwd)

	controlBinary, controlPaths := prepareLiveCodex(t)
	if controlBinary != binary {
		t.Fatalf("A12 control binary = %q, want %q", controlBinary, binary)
	}
	controlEnvironment, err := codexprocess.Environment(
		controlPaths.home,
		controlPaths.codexHome,
		controlPaths.temporary,
		map[string]string{a12WorkerCredentialEnvName: a12WorkerCredentialSecret},
	)
	if err != nil {
		t.Fatalf("build A12 positive-control environment: %v", err)
	}
	controlPaths.environment = controlEnvironment
	control := runA12EnvironmentHeaderTurn(t, controlBinary, controlPaths, "control")
	canonicalHeader := http.CanonicalHeaderKey(a12WorkerCredentialHeader)
	controlValues, controlPresent := control.request.Header[canonicalHeader]
	if !controlPresent || len(controlValues) != 1 || controlValues[0] != a12WorkerCredentialSecret {
		t.Fatalf("A12 environment-header positive control mismatch: present=%t values=%d", controlPresent, len(controlValues))
	}

	isolated := runA12EnvironmentHeaderTurn(t, binary, paths, "isolated")
	if values, present := isolated.request.Header[canonicalHeader]; present {
		t.Fatalf("A12 child inherited the worker credential header: present=true values=%d", len(values))
	}
	if bytes.Contains(isolated.request.Body, []byte(a12WorkerCredentialSecret)) {
		t.Fatal("A12 model request body contains the worker credential")
	}
	if bytes.Contains(isolated.stderr, []byte(a12WorkerCredentialSecret)) {
		t.Fatal("A12 app-server stderr contains the worker credential")
	}
	assertA12EmptyNonRepositoryCWD(t, paths.cwd)
}

type a12EnvironmentHeaderResult struct {
	request scriptedmodel.Request
	stderr  []byte
}

func runA12EnvironmentHeaderTurn(t *testing.T, binary string, paths livePaths, suffix string) a12EnvironmentHeaderResult {
	t.Helper()
	response, err := scriptedmodel.AssistantMessage(
		"response-a12-launch-"+suffix,
		"message-a12-launch-"+suffix,
		a12AssistantText,
	)
	if err != nil {
		t.Fatal(err)
	}
	modelServer, err := scriptedmodel.Start(scriptedmodel.Config{
		Responses: []scriptedmodel.Response{response},
	})
	if err != nil {
		t.Fatalf("start A12 %s model: %v", suffix, err)
	}
	defer modelServer.Close()
	writeScriptedModelConfigWithOptions(t, paths.codexHome, modelServer.URL(), scriptedModelConfigOptions{
		disableUpdatePlan:  true,
		modelEnvHeaderName: a12WorkerCredentialHeader,
		modelEnvHeaderVar:  a12WorkerCredentialEnvName,
	})

	process := startPreparedLiveCodex(t, binary, paths, "app-server", "--listen", "stdio://", "--strict-config")
	initializeAppServer(t, process)
	collector := newRPCCollector(process)
	thread, turn := startMinimalAppServerTurn(t, collector, paths.cwd, "verify the "+suffix+" app-server launch boundary")
	assertSamePath(t, thread.CWD, paths.cwd)
	assertSamePath(t, thread.Thread.CWD, paths.cwd)
	assertAgentItemCompleted(t, collector, thread.Thread.ID, turn.ID, a12AssistantText)
	collector.notification(t, "turn/completed")
	closeAndWait(t, process)

	if failures := modelServer.Failures(); len(failures) != 0 {
		t.Fatalf("A12 %s model failures: %v", suffix, failures)
	}
	requests := modelServer.Requests()
	if len(requests) != 1 {
		t.Fatalf("A12 %s model received %d requests, want one", suffix, len(requests))
	}
	stderr, truncated := process.Stderr()
	if truncated {
		t.Fatalf("A12 %s app-server stderr exceeded the probe bound", suffix)
	}
	return a12EnvironmentHeaderResult{request: requests[0], stderr: stderr}
}

// TestAppServerA12StockClientFollowsCrossOriginModelRedirect is a negative
// characterization. The configured endpoint acts as llmproxy and returns a
// 307 to another origin. Stock Codex follows it and completes the turn from
// that otherwise unapproved sink. URL configuration is therefore routing, not
// an egress boundary; A12 still requires enforcement outside the child.
func TestAppServerA12StockClientFollowsCrossOriginModelRedirect(t *testing.T) {
	binary, paths := prepareLiveCodex(t)
	requireCandidateReleaseOneOf(t, binary, paths, "0.146.0-alpha.14", "0.146.0")
	finalResponse, err := scriptedmodel.AssistantMessage(
		"response-a12-redirect",
		"message-a12-redirect",
		a12RedirectAssistantText,
	)
	if err != nil {
		t.Fatal(err)
	}
	forbiddenSink, err := scriptedmodel.Start(scriptedmodel.Config{
		Responses: []scriptedmodel.Response{finalResponse},
	})
	if err != nil {
		t.Fatalf("start A12 forbidden redirect sink: %v", err)
	}
	t.Cleanup(forbiddenSink.Close)
	configuredEndpoint, err := scriptedmodel.Start(scriptedmodel.Config{
		Responses: []scriptedmodel.Response{{
			StatusCode:  http.StatusTemporaryRedirect,
			RedirectURL: forbiddenSink.URL() + "/v1/responses",
		}},
	})
	if err != nil {
		t.Fatalf("start A12 configured model endpoint: %v", err)
	}
	t.Cleanup(configuredEndpoint.Close)

	writeScriptedModelConfigWithOptions(t, paths.codexHome, configuredEndpoint.URL(), scriptedModelConfigOptions{
		disableUpdatePlan: true,
	})
	process := startPreparedLiveCodex(t, binary, paths, "app-server", "--listen", "stdio://", "--strict-config")
	initializeAppServer(t, process)
	collector := newRPCCollector(process)
	thread, turn := startMinimalAppServerTurn(t, collector, paths.cwd, "attempt a model request through the configured endpoint")
	assertAgentItemCompleted(t, collector, thread.Thread.ID, turn.ID, a12RedirectAssistantText)
	collector.notification(t, "turn/completed")
	closeAndWait(t, process)

	if failures := configuredEndpoint.Failures(); len(failures) != 0 {
		t.Fatalf("A12 configured endpoint failures: %v", failures)
	}
	if failures := forbiddenSink.Failures(); len(failures) != 0 {
		t.Fatalf("A12 forbidden redirect sink failures: %v", failures)
	}
	configuredRequests := configuredEndpoint.Requests()
	forbiddenRequests := forbiddenSink.Requests()
	if len(configuredRequests) != 1 || len(forbiddenRequests) != 1 {
		t.Fatalf("A12 redirect requests: configured=%d forbidden=%d, want one each", len(configuredRequests), len(forbiddenRequests))
	}
	if !bytes.Equal(configuredRequests[0].Body, forbiddenRequests[0].Body) {
		t.Fatal("A12 redirected model request body changed across the 307")
	}
	t.Log("A12 remains open: stock Codex followed a cross-origin redirect without an external egress boundary")
}

func assertA12ExplicitEnvironment(t *testing.T, environment []string) {
	t.Helper()
	for _, entry := range environment {
		name, value, found := strings.Cut(entry, "=")
		if !found {
			t.Fatalf("A12 child environment contains malformed entry %q", entry)
		}
		if name == a12WorkerCredentialEnvName || strings.Contains(value, a12WorkerCredentialSecret) {
			t.Fatal("A12 explicit child environment contains the worker credential")
		}
	}
}

func assertA12EmptyNonRepositoryCWD(t *testing.T, cwd string) {
	t.Helper()
	entries, err := os.ReadDir(cwd)
	if err != nil {
		t.Fatalf("read A12 child cwd: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("A12 child cwd is not empty: %+v", entries)
	}
	sourceCWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("read conformance source cwd: %v", err)
	}
	canonicalSource, err := filepath.EvalSymlinks(sourceCWD)
	if err != nil {
		t.Fatalf("canonicalize conformance source cwd: %v", err)
	}
	canonicalChild, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		t.Fatalf("canonicalize A12 child cwd: %v", err)
	}
	relative, err := filepath.Rel(canonicalSource, canonicalChild)
	if err != nil {
		t.Fatalf("compare source and child cwd: %v", err)
	}
	if relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator))) {
		t.Fatalf("A12 child cwd %q is inside the conformance source tree %q", canonicalChild, canonicalSource)
	}
}
