package codex_test

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/conformance/codex/internal/codexprocess"
	"github.com/agentserver/agentserver/v2/internal/codexwire"
)

const liveProbeTimeout = 15 * time.Second

func TestAppServerInitializeLifecycle(t *testing.T) {
	process, paths := startLiveCodex(t, "app-server", "--listen", "stdio://", "--strict-config")

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
				"experimentalApi": true,
			},
		},
	}
	if err := process.Peer.Send(request); err != nil {
		t.Fatalf("send app-server initialize: %v", err)
	}
	response := receiveResponse(t, process, "1")
	var initialize struct {
		UserAgent      string `json:"userAgent"`
		CodexHome      string `json:"codexHome"`
		PlatformFamily string `json:"platformFamily"`
		PlatformOS     string `json:"platformOs"`
	}
	if err := response.DecodeResult(&initialize); err != nil {
		t.Fatal(err)
	}
	if initialize.UserAgent == "" || initialize.PlatformFamily == "" || initialize.PlatformOS == "" {
		t.Fatalf("incomplete app-server initialize response: %+v", initialize)
	}
	assertSamePath(t, initialize.CodexHome, paths.codexHome)

	if err := process.Peer.Send(map[string]any{"method": "initialized"}); err != nil {
		t.Fatalf("send app-server initialized: %v", err)
	}
	closeAndWait(t, process)
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
	codexHome string
	cwd       string
}

func startLiveCodex(t *testing.T, arguments ...string) (*codexprocess.Process, livePaths) {
	t.Helper()
	if os.Getenv("AGENTSERVER_RUN_LIVE_CODEX") != "1" {
		t.Skip("set AGENTSERVER_RUN_LIVE_CODEX=1 and AGENTSERVER_CODEX_BIN to run stock Codex probes")
	}
	binary := os.Getenv("AGENTSERVER_CODEX_BIN")
	if binary == "" || !filepath.IsAbs(binary) {
		t.Fatal("AGENTSERVER_CODEX_BIN must be an absolute path")
	}

	root := t.TempDir()
	paths := struct {
		home      string
		codexHome string
		temporary string
		cwd       string
	}{
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

	processContext, cancelProcess := context.WithTimeout(context.Background(), liveProbeTimeout)
	process, err := codexprocess.Start(processContext, codexprocess.Config{
		Binary: binary,
		Args:   arguments,
		Dir:    paths.cwd,
		Env:    environment,
	})
	if err != nil {
		cancelProcess()
		t.Fatalf("start stock Codex: %v", err)
	}
	t.Cleanup(func() {
		_ = process.Kill()
		cancelProcess()
	})
	return process, livePaths{codexHome: paths.codexHome, cwd: paths.cwd}
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
