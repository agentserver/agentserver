package codexprocess

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/codexwire"
)

func TestProcessRoundTripAndGracefulEOF(t *testing.T) {
	root := t.TempDir()
	environment, err := Environment(
		filepath.Join(root, "home"),
		filepath.Join(root, "codex-home"),
		filepath.Join(root, "tmp"),
		map[string]string{"AGENTSERVER_CODEXPROCESS_HELPER": "1"},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{"home", "codex-home", "tmp", "cwd"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0o700); err != nil {
			t.Fatal(err)
		}
	}

	process, err := Start(context.Background(), Config{
		Binary:      os.Args[0],
		Args:        []string{"-test.run=^TestCodexProcessHelper$"},
		Dir:         filepath.Join(root, "cwd"),
		Env:         environment,
		StderrBytes: 8,
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = process.Kill() })

	if err := process.Peer.Send(map[string]any{
		"id":     1,
		"method": "initialize",
		"params": map[string]any{"clientName": "process-test"},
	}); err != nil {
		t.Fatal(err)
	}
	receiveContext, cancelReceive := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelReceive()
	message, err := process.Peer.Receive(receiveContext)
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		SessionID string `json:"sessionId"`
	}
	if err := message.DecodeResult(&response); err != nil {
		t.Fatal(err)
	}
	if response.SessionID != "helper-session" {
		t.Fatalf("session id = %q", response.SessionID)
	}

	if err := process.CloseStdin(); err != nil {
		t.Fatalf("CloseStdin() error = %v", err)
	}
	waitContext, cancelWait := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelWait()
	if err := process.Wait(waitContext); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	stderr, truncated := process.Stderr()
	if got, want := string(stderr), "helper-s"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
	if !truncated {
		t.Fatal("stderr capture should report truncation")
	}
}

func TestStartRejectsInheritedEnvironment(t *testing.T) {
	_, err := Start(context.Background(), Config{
		Binary: os.Args[0],
		Dir:    t.TempDir(),
		Env:    nil,
	})
	if err == nil || !strings.Contains(err.Error(), "must be explicit") {
		t.Fatalf("Start() error = %v, want explicit environment error", err)
	}
}

func TestEnvironmentIsSortedAndDoesNotInheritSecrets(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "must-not-be-inherited")
	t.Setenv("CODEX_ACCESS_TOKEN", "must-not-be-inherited")
	root := t.TempDir()
	environment, err := Environment(
		filepath.Join(root, "home"),
		filepath.Join(root, "codex"),
		filepath.Join(root, "tmp"),
		map[string]string{"Z_LAST": "yes", "A_FIRST": "yes"},
	)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(environment, "\n")
	if strings.Contains(joined, "OPENAI_API_KEY") || strings.Contains(joined, "CODEX_ACCESS_TOKEN") {
		t.Fatalf("environment inherited a model or machine credential: %s", joined)
	}
	if strings.Index(joined, "A_FIRST=") > strings.Index(joined, "Z_LAST=") {
		t.Fatalf("environment is not sorted: %v", environment)
	}
}

func TestEnvironmentRejectsReservedOverride(t *testing.T) {
	root := t.TempDir()
	_, err := Environment(
		filepath.Join(root, "home"),
		filepath.Join(root, "codex"),
		filepath.Join(root, "tmp"),
		map[string]string{"CODEX_HOME": filepath.Join(root, "other")},
	)
	if err == nil || !strings.Contains(err.Error(), "reserved variable") {
		t.Fatalf("Environment() error = %v, want reserved override error", err)
	}
}

func TestCodexProcessHelper(t *testing.T) {
	if os.Getenv("AGENTSERVER_CODEXPROCESS_HELPER") != "1" {
		return
	}

	decoder, err := codexwire.NewDecoder(os.Stdin, codexwire.DefaultMaxFrameBytes)
	if err != nil {
		panic(err)
	}
	encoder, err := codexwire.NewEncoder(os.Stdout, codexwire.DefaultMaxFrameBytes)
	if err != nil {
		panic(err)
	}
	request, err := decoder.Next()
	if err != nil {
		panic(err)
	}
	if err := encoder.Write(map[string]any{
		"id":     json.RawMessage(request.ID),
		"result": map[string]any{"sessionId": "helper-session"},
	}); err != nil {
		panic(err)
	}
	_, _ = fmt.Fprint(os.Stderr, "helper-stderr-is-bounded")
	if _, err := decoder.Next(); !errors.Is(err, io.EOF) {
		panic(fmt.Sprintf("expected stdin EOF, got %v", err))
	}
}
