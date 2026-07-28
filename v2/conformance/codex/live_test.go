package codex_test

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/conformance/codex/internal/codexprocess"
	"github.com/agentserver/agentserver/v2/internal/codexwire"
	"github.com/agentserver/agentserver/v2/internal/runtimelock"
)

const liveProbeTimeout = 15 * time.Second

var candidateVersionPattern = regexp.MustCompile(`^codex-cli ([0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?)$`)

func TestCandidateBinaryAndAppServerSchemaFingerprint(t *testing.T) {
	binary, paths := prepareLiveCodex(t)
	commandContext, cancelCommand := context.WithTimeout(context.Background(), liveProbeTimeout)
	defer cancelCommand()
	versionResult, err := codexprocess.RunCommand(commandContext, codexprocess.CommandConfig{
		Binary: binary,
		Args:   []string{"--version"},
		Dir:    paths.cwd,
		Env:    paths.environment,
	})
	if err != nil {
		t.Fatalf("read Codex version: %v\nstderr: %s", err, versionResult.Stderr)
	}
	assertCaptureComplete(t, versionResult)
	versionOutput := strings.TrimSpace(string(versionResult.Stdout))
	match := candidateVersionPattern.FindStringSubmatch(versionOutput)
	if match == nil {
		t.Fatalf("unexpected Codex version output %q", versionOutput)
	}

	resolvedBinary, err := filepath.EvalSymlinks(binary)
	if err != nil {
		t.Fatalf("resolve Codex binary: %v", err)
	}
	binaryDigest, binarySize, err := runtimelock.HashFile(resolvedBinary)
	if err != nil {
		t.Fatalf("hash Codex binary: %v", err)
	}

	schemaDigest := generateAppServerSchemaDigest(t, binary, paths, "app-server-schema-a")
	secondSchemaDigest := generateAppServerSchemaDigest(t, binary, paths, "app-server-schema-b")
	if schemaDigest.canonical.SHA256 != secondSchemaDigest.canonical.SHA256 {
		t.Fatalf("canonical app-server schema generation is not deterministic: %s != %s", schemaDigest.canonical.SHA256, secondSchemaDigest.canonical.SHA256)
	}
	if schemaDigest.raw.SHA256 != secondSchemaDigest.raw.SHA256 {
		t.Logf("stock generator raw JSON ordering is nondeterministic: %s != %s", schemaDigest.raw.SHA256, secondSchemaDigest.raw.SHA256)
	}
	t.Logf(
		"candidate only (not a runtime pin): release=%s binary_sha256=%s binary_size=%d app_server_schema_sha256=%s schema_files=%d",
		match[1], binaryDigest, binarySize, schemaDigest.canonical.SHA256, len(schemaDigest.canonical.Files),
	)
}

type schemaDigests struct {
	raw       runtimelock.TreeDigest
	canonical runtimelock.TreeDigest
}

func generateAppServerSchemaDigest(t *testing.T, binary string, paths livePaths, directoryName string) schemaDigests {
	t.Helper()
	schemaOutput := filepath.Join(paths.root, directoryName)
	schemaContext, cancelSchema := context.WithTimeout(context.Background(), liveProbeTimeout)
	defer cancelSchema()
	schemaResult, err := codexprocess.RunCommand(schemaContext, codexprocess.CommandConfig{
		Binary: binary,
		Args: []string{
			"app-server", "generate-json-schema", "--experimental", "--out", schemaOutput,
		},
		Dir: paths.cwd,
		Env: paths.environment,
	})
	if err != nil {
		t.Fatalf("generate app-server schema: %v\nstderr: %s", err, schemaResult.Stderr)
	}
	assertCaptureComplete(t, schemaResult)
	rawDigest, err := runtimelock.HashTree(schemaOutput, runtimelock.DefaultTreeLimits())
	if err != nil {
		t.Fatalf("hash raw app-server schema bundle: %v", err)
	}
	canonicalDigest, err := runtimelock.HashCanonicalJSONTree(schemaOutput, runtimelock.DefaultTreeLimits())
	if err != nil {
		t.Fatalf("hash canonical app-server schema bundle: %v", err)
	}
	return schemaDigests{raw: rawDigest, canonical: canonicalDigest}
}

func TestAppServerInitializeLifecycle(t *testing.T) {
	process, paths := startLiveCodex(t, "app-server", "--listen", "stdio://", "--strict-config")
	initialize := initializeAppServer(t, process)
	assertSamePath(t, initialize.CodexHome, paths.codexHome)
	closeAndWait(t, process)
}

type appServerInitializeResult struct {
	UserAgent      string `json:"userAgent"`
	CodexHome      string `json:"codexHome"`
	PlatformFamily string `json:"platformFamily"`
	PlatformOS     string `json:"platformOs"`
}

func initializeAppServer(t *testing.T, process *codexprocess.Process) appServerInitializeResult {
	t.Helper()
	return initializeAppServerWithExperimental(t, process, true)
}

func initializeAppServerWithExperimental(t *testing.T, process *codexprocess.Process, experimentalAPI bool) appServerInitializeResult {
	t.Helper()
	request := map[string]any{
		"id":     1,
		"method": "initialize",
		"params": map[string]any{
			"clientInfo": map[string]any{
				"name":    "agentserver_v2_conformance",
				"title":   "agentserver v2 conformance",
				"version": "0.0.0",
			},
			"capabilities": map[string]any{
				"experimentalApi": experimentalAPI,
			},
		},
	}
	if err := process.Peer.Send(request); err != nil {
		t.Fatalf("send app-server initialize: %v", err)
	}
	response := receiveResponse(t, process, "1")
	var initialize appServerInitializeResult
	if err := response.DecodeResult(&initialize); err != nil {
		t.Fatal(err)
	}
	if initialize.UserAgent == "" || initialize.PlatformFamily == "" || initialize.PlatformOS == "" {
		t.Fatalf("incomplete app-server initialize response: %+v", initialize)
	}
	if err := process.Peer.Send(map[string]any{"method": "initialized"}); err != nil {
		t.Fatalf("send app-server initialized: %v", err)
	}
	return initialize
}

func TestExecServerInitializeLifecycle(t *testing.T) {
	process, _ := startLiveCodex(t, "exec-server", "--listen", "stdio", "--strict-config")

	if err := process.Peer.Send(map[string]any{
		"id":     1,
		"method": "initialize",
		"params": map[string]any{"clientName": "agentserver-v2-conformance"},
	}); err != nil {
		t.Fatalf("send exec-server initialize: %v", err)
	}
	response := receiveResponse(t, process, "1")
	var initialize struct {
		SessionID string `json:"sessionId"`
	}
	if err := response.DecodeResult(&initialize); err != nil {
		t.Fatal(err)
	}
	if initialize.SessionID == "" {
		t.Fatal("exec-server initialize response has an empty sessionId")
	}
	if err := process.Peer.Send(map[string]any{"method": "initialized", "params": nil}); err != nil {
		t.Fatalf("send exec-server initialized: %v", err)
	}

	closeAndWait(t, process)
}

func TestExecServerEnvironmentMetadata(t *testing.T) {
	process, paths := startLiveCodex(t, "exec-server", "--listen", "stdio", "--strict-config")
	initializeExecServer(t, process)

	if err := process.Peer.Send(map[string]any{"id": 2, "method": "environment/info"}); err != nil {
		t.Fatalf("send environment/info: %v", err)
	}
	infoResponse := receiveResponse(t, process, "2")
	var info struct {
		Shell struct {
			Name string `json:"name"`
			Path string `json:"path"`
		} `json:"shell"`
		CWD          *string        `json:"cwd"`
		Capabilities map[string]any `json:"capabilities"`
	}
	if err := infoResponse.DecodeResult(&info); err != nil {
		t.Fatal(err)
	}
	if info.Shell.Name == "" || info.Shell.Path == "" {
		t.Fatalf("environment/info returned incomplete shell metadata: %+v", info.Shell)
	}
	if info.CWD == nil {
		t.Fatal("environment/info returned a null cwd")
	}
	assertFileURIPath(t, *info.CWD, paths.cwd)

	if err := process.Peer.Send(map[string]any{"id": 3, "method": "environment/status"}); err != nil {
		t.Fatalf("send environment/status: %v", err)
	}
	statusResponse := receiveResponse(t, process, "3")
	var status struct {
		Status string `json:"status"`
	}
	if err := statusResponse.DecodeResult(&status); err != nil {
		t.Fatal(err)
	}
	if status.Status != "ready" {
		t.Fatalf("environment/status = %q, want ready", status.Status)
	}

	closeAndWait(t, process)
}

func initializeExecServer(t *testing.T, process *codexprocess.Process) {
	t.Helper()
	if err := process.Peer.Send(map[string]any{
		"id":     1,
		"method": "initialize",
		"params": map[string]any{"clientName": "agentserver-v2-conformance"},
	}); err != nil {
		t.Fatal(err)
	}
	response := receiveResponse(t, process, "1")
	var initialize struct {
		SessionID string `json:"sessionId"`
	}
	if err := response.DecodeResult(&initialize); err != nil {
		t.Fatal(err)
	}
	if initialize.SessionID == "" {
		t.Fatal("exec-server initialize response has an empty sessionId")
	}
	if err := process.Peer.Send(map[string]any{"method": "initialized", "params": nil}); err != nil {
		t.Fatal(err)
	}
}

func receiveResponse(t *testing.T, process *codexprocess.Process, id string) codexwire.Message {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), liveProbeTimeout)
	defer cancel()
	message, err := process.Peer.Receive(ctx)
	if err != nil {
		stderr, _ := process.Stderr()
		t.Fatalf("receive response %s: %v\nstderr: %s", id, err, stderr)
	}
	if string(message.ID) != id {
		t.Fatalf("response id = %s, want %s (kind=%s method=%q)", message.ID, id, message.Kind, message.Method)
	}
	if message.Kind == codexwire.KindError {
		t.Fatalf("response %s returned error %d: %s", id, message.Error.Code, message.Error.Message)
	}
	if message.Kind != codexwire.KindResponse {
		t.Fatalf("message kind = %s, want response", message.Kind)
	}
	return message
}

type livePaths struct {
	root        string
	home        string
	codexHome   string
	temporary   string
	cwd         string
	environment []string
}

func startLiveCodex(t *testing.T, arguments ...string) (*codexprocess.Process, livePaths) {
	t.Helper()
	binary, paths := prepareLiveCodex(t)
	return startPreparedLiveCodex(t, binary, paths, arguments...), paths
}

func startPreparedLiveCodex(t *testing.T, binary string, paths livePaths, arguments ...string) *codexprocess.Process {
	t.Helper()
	processContext, cancelProcess := context.WithTimeout(context.Background(), liveProbeTimeout)
	process, err := codexprocess.Start(processContext, codexprocess.Config{
		Binary: binary,
		Args:   arguments,
		Dir:    paths.cwd,
		Env:    paths.environment,
	})
	if err != nil {
		cancelProcess()
		t.Fatalf("start stock Codex: %v", err)
	}
	t.Cleanup(func() {
		_ = process.Kill()
		cancelProcess()
	})
	return process
}

func prepareLiveCodex(t *testing.T) (string, livePaths) {
	t.Helper()
	if os.Getenv("AGENTSERVER_RUN_LIVE_CODEX") != "1" {
		t.Skip("set AGENTSERVER_RUN_LIVE_CODEX=1 and AGENTSERVER_CODEX_BIN to run stock Codex probes")
	}
	binary := os.Getenv("AGENTSERVER_CODEX_BIN")
	if binary == "" || !filepath.IsAbs(binary) {
		t.Fatal("AGENTSERVER_CODEX_BIN must be an absolute path")
	}

	root := t.TempDir()
	paths := livePaths{
		root:      root,
		home:      filepath.Join(root, "home"),
		codexHome: filepath.Join(root, "codex-home"),
		temporary: filepath.Join(root, "tmp"),
		cwd:       filepath.Join(root, "cwd"),
	}
	for _, directory := range []string{paths.home, paths.codexHome, paths.temporary, paths.cwd} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	environment, err := codexprocess.Environment(paths.home, paths.codexHome, paths.temporary, nil)
	if err != nil {
		t.Fatal(err)
	}
	paths.environment = environment
	return binary, paths
}

func closeAndWait(t *testing.T, process *codexprocess.Process) {
	t.Helper()
	if err := process.CloseStdin(); err != nil {
		t.Fatalf("close Codex stdin: %v", err)
	}
	waitContext, cancelWait := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelWait()
	if err := process.Wait(waitContext); err != nil {
		_ = process.Kill()
		stderr, truncated := process.Stderr()
		t.Fatalf("Codex did not exit cleanly after stdin EOF: %v (stderr_truncated=%t)\nstderr: %s", err, truncated, stderr)
	}
	stderr, truncated := process.Stderr()
	if truncated {
		t.Fatalf("Codex stderr exceeded the probe bound")
	}
	for _, forbidden := range []string{"OPENAI_API_KEY", "CODEX_ACCESS_TOKEN", "AGENTSERVER_EXECUTOR_CAPABILITY"} {
		if strings.Contains(string(stderr), forbidden) {
			t.Fatalf("Codex stderr contains forbidden credential name %q", forbidden)
		}
	}
}

func assertSamePath(t *testing.T, got, want string) {
	t.Helper()
	canonical := func(path string) string {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return filepath.Clean(path)
		}
		return filepath.Clean(resolved)
	}
	if canonical(got) != canonical(want) {
		t.Fatalf("path = %q, want %q", got, want)
	}
}

func assertFileURIPath(t *testing.T, rawURI, want string) {
	t.Helper()
	parsed, err := url.Parse(rawURI)
	if err != nil {
		t.Fatalf("parse file URI %q: %v", rawURI, err)
	}
	if parsed.Scheme != "file" || (parsed.Host != "" && parsed.Host != "localhost") {
		t.Fatalf("cwd = %q, want a local file URI", rawURI)
	}
	assertSamePath(t, parsed.Path, want)
}

func assertCaptureComplete(t *testing.T, result codexprocess.CommandResult) {
	t.Helper()
	if result.StdoutTruncated || result.StderrTruncated {
		t.Fatalf("Codex command output exceeded capture bounds (stdout=%t stderr=%t)", result.StdoutTruncated, result.StderrTruncated)
	}
}
