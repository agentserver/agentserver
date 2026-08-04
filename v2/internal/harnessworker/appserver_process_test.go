package harnessworker

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/codexwire"
)

func TestAppServerProcessConfigBuildsClosedWorldChildEnvironment(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "must-not-be-inherited")
	t.Setenv("AGENTSERVER_EXECUTOR_MCP_CAPABILITY", "must-not-be-inherited")
	config := validAppServerProcessConfig(t)
	normalized, environment, err := validateAppServerProcessConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	if normalized.MaxFrameBytes != codexwire.DefaultMaxFrameBytes ||
		normalized.IncomingFrames != defaultAppServerIncomingFrames ||
		normalized.MaxStderrBytes != defaultAppServerStderrBytes {
		t.Fatalf("normalized app-server bounds = %+v", normalized)
	}
	want := []string{
		"AGENTSERVER_LLM_CAPABILITY=model-capability",
		"CODEX_HOME=" + config.Environment.CodexHome,
		"HOME=" + config.Environment.Home,
		"LANG=C",
		"LC_ALL=C",
		"NO_COLOR=1",
		"PATH=/usr/bin:/bin",
		"SHELL=/bin/sh",
		"SSL_CERT_FILE=" + config.Environment.TLSRootCertificateFile,
		"TMPDIR=" + config.Environment.Temporary,
	}
	if !reflect.DeepEqual(environment, want) {
		t.Fatalf("app-server environment = %v, want %v", environment, want)
	}
	joined := strings.Join(environment, "\n")
	if strings.Contains(joined, "OPENAI_API_KEY") || strings.Contains(joined, "EXECUTOR_MCP") {
		t.Fatalf("app-server environment inherited worker secrets: %v", environment)
	}
}

func TestAppServerProcessConfigRejectsUnsafeBoundary(t *testing.T) {
	base := validAppServerProcessConfig(t)
	tests := []struct {
		name   string
		mutate func(*AppServerProcessConfig)
		want   string
	}{
		{name: "relative final exec", mutate: func(c *AppServerProcessConfig) { c.FinalExecExecutable = "harness-final-exec" }, want: "absolute"},
		{name: "relative Codex", mutate: func(c *AppServerProcessConfig) { c.CodexExecutable = "codex" }, want: "absolute"},
		{name: "writable cwd", mutate: func(c *AppServerProcessConfig) {
			if err := os.Chmod(c.Directory, 0o700); err != nil {
				t.Fatal(err)
			}
		}, want: "read-only"},
		{name: "nonempty cwd", mutate: func(c *AppServerProcessConfig) {
			if err := os.Chmod(c.Directory, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(c.Directory, "payload"), []byte("x"), 0o400); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(c.Directory, 0o500); err != nil {
				t.Fatal(err)
			}
		}, want: "empty"},
		{name: "missing model capability", mutate: func(c *AppServerProcessConfig) { c.Environment.ModelCapability = "" }, want: "capability"},
		{name: "capability newline", mutate: func(c *AppServerProcessConfig) { c.Environment.ModelCapability = "secret\nvalue" }, want: "capability"},
		{name: "missing TLS root", mutate: func(c *AppServerProcessConfig) { c.Environment.TLSRootCertificateFile = "" }, want: "TLS root"},
		{name: "writable TLS root", mutate: func(c *AppServerProcessConfig) {
			if err := os.Chmod(c.Environment.TLSRootCertificateFile, 0o600); err != nil {
				t.Fatal(err)
			}
		}, want: "read-only"},
		{name: "root worker", mutate: func(c *AppServerProcessConfig) { c.WorkerUID = 0 }, want: "unprivileged"},
		{name: "shared uid", mutate: func(c *AppServerProcessConfig) { c.AppUID = c.WorkerUID }, want: "distinct"},
		{name: "too many frames", mutate: func(c *AppServerProcessConfig) { c.IncomingFrames = maximumAppServerIncomingFrames + 1 }, want: "incoming frame"},
		{name: "too much stderr", mutate: func(c *AppServerProcessConfig) { c.MaxStderrBytes = maximumAppServerStderrBytes + 1 }, want: "stderr"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := base
			// Each filesystem-mutating case gets its own path so parallel table
			// semantics cannot make a later assertion depend on an earlier one.
			if strings.Contains(test.name, "cwd") || strings.Contains(test.name, "TLS root") {
				config = validAppServerProcessConfig(t)
			}
			test.mutate(&config)
			_, _, err := validateAppServerProcessConfig(config)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validateAppServerProcessConfig() error = %v, want %q", err, test.want)
			}
			if strings.Contains(err.Error(), "secret\nvalue") {
				t.Fatalf("validation error exposed model capability: %v", err)
			}
		})
	}
}

func TestAppServerProcessOwnsBoundedStdioAndGracefulEOF(t *testing.T) {
	command := exec.Command(os.Args[0], "-test.run=^TestAppServerProcessHelper$")
	command.Dir = t.TempDir()
	command.Env = []string{"AGENTSERVER_APPSERVER_PROCESS_HELPER=1"}
	process, err := startAppServerCommand(t.Context(), command, appServerProcessBounds{
		maxFrameBytes: 1024 * 1024, incomingFrames: 8, maxStderrBytes: 6,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if process.PID() < 1 {
		t.Fatalf("app-server PID = %d", process.PID())
	}
	if err := process.Send(map[string]any{"id": 1, "method": "initialize", "params": map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	receiveContext, cancelReceive := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancelReceive()
	message, err := process.Receive(receiveContext)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Ready bool `json:"ready"`
	}
	if err := message.DecodeResult(&result); err != nil || !result.Ready {
		t.Fatalf("helper response = %+v, error %v", result, err)
	}
	if err := process.CloseStdin(); err != nil {
		t.Fatal(err)
	}
	if err := process.CloseStdin(); err != nil {
		t.Fatalf("second CloseStdin() = %v", err)
	}
	waitContext, cancelWait := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancelWait()
	if err := process.Wait(waitContext); err != nil {
		t.Fatal(err)
	}
	stderr, truncated := process.Stderr()
	if string(stderr) != "helper" || !truncated {
		t.Fatalf("bounded stderr = %q truncated=%t", stderr, truncated)
	}
}

func TestAppServerProcessHelper(t *testing.T) {
	if os.Getenv("AGENTSERVER_APPSERVER_PROCESS_HELPER") != "1" {
		return
	}
	decoder, err := codexwire.NewDecoder(os.Stdin, 1024*1024)
	if err != nil {
		panic(err)
	}
	encoder, err := codexwire.NewEncoder(os.Stdout, 1024*1024)
	if err != nil {
		panic(err)
	}
	message, err := decoder.Next()
	if err != nil {
		panic(err)
	}
	if message.Kind != codexwire.KindRequest || message.Method != "initialize" {
		panic(fmt.Sprintf("unexpected helper request: %+v", message))
	}
	if err := encoder.Write(map[string]any{"id": 1, "result": map[string]any{"ready": true}}); err != nil {
		panic(err)
	}
	if _, err := decoder.Next(); err == nil {
		panic("expected app-server stdin EOF")
	}
	_, _ = fmt.Fprint(os.Stderr, "helper-stderr")
}

func validAppServerProcessConfig(t *testing.T) AppServerProcessConfig {
	t.Helper()
	root := t.TempDir()
	finalExec := filepath.Join(root, "harness-final-exec")
	codex := filepath.Join(root, "codex")
	for _, executable := range []string{finalExec, codex} {
		if err := os.WriteFile(executable, []byte("fixture"), 0o500); err != nil {
			t.Fatal(err)
		}
	}
	directory := filepath.Join(root, "empty")
	home := filepath.Join(root, "home")
	codexHome := filepath.Join(root, "codex-home")
	temporary := filepath.Join(root, "tmp")
	tlsRoot := filepath.Join(root, "ca.crt")
	for _, path := range []string{directory, home, codexHome, temporary} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(tlsRoot, []byte("test CA certificate"), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(directory, 0o700) })
	return AppServerProcessConfig{
		FinalExecExecutable: finalExec,
		CodexExecutable:     codex,
		Directory:           directory,
		Environment: AppServerRuntimeEnvironment{
			Home: home, CodexHome: codexHome, Temporary: temporary,
			ModelCapability: "model-capability", TLSRootCertificateFile: tlsRoot,
		},
		WorkerUID: 65531, WorkerGID: 65531,
		AppUID: 65532, AppGID: 65532,
	}
}
