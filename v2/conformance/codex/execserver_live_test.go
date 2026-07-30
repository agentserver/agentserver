package codex_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode"

	"github.com/agentserver/agentserver/v2/conformance/codex/internal/codexprocess"
	"github.com/agentserver/agentserver/v2/conformance/codex/internal/execadapter"
	"github.com/agentserver/agentserver/v2/internal/codexwire"
	"github.com/agentserver/agentserver/v2/internal/runtimelock"
)

const (
	execChildModeEnvironment      = "AGENTSERVER_EXEC_CHILD_MODE"
	execChildPIDFileEnvironment   = "AGENTSERVER_EXEC_CHILD_PID_FILE"
	execChildNetworkTargetEnv     = "AGENTSERVER_EXEC_CHILD_NETWORK_TARGET"
	execChildOutputStdout         = "stdout:deterministic\n"
	execChildOutputStderr         = "stderr:deterministic\n"
	execChildEchoInput            = "deterministic-input\n"
	execChildReadyOutput          = "ready\n"
	execChildInterruptedOutput    = "interrupted\n"
	execChildPTYOutput            = "tty:stdout|tty:stderr|"
	execChildArgument             = "deterministic-argument"
	execChildArg0                 = "agentserver-deterministic-arg0"
	execChildExpectedCWDEnv       = "AGENTSERVER_EXEC_CHILD_EXPECTED_CWD"
	execChildInterruptExitCode    = 42
	execChildRootCrashExitCode    = 43
	execChildLargeOutputBytes     = (1 << 20) + (64 << 10)
	execChildNetworkOriginBody    = "agentserver-e05-origin\n"
	execChildWriteIDWindowOutput  = "write-id-window:4096\n"
	execChildOversizedInputOutput = "stock-accepted-over-agentx-input-limits\n"
	execChildTinyOutputByte       = byte('c')
	execChildTinyOutputACK        = byte('a')
	execChildE09ReadPathEnv       = "AGENTSERVER_EXEC_CHILD_E09_READ_PATH"
	execChildE09WorkspacePathEnv  = "AGENTSERVER_EXEC_CHILD_E09_WORKSPACE_PATH"
	execChildE09OutsidePathEnv    = "AGENTSERVER_EXEC_CHILD_E09_OUTSIDE_PATH"
	execChildE09ExpectedPathEnv   = "AGENTSERVER_EXEC_CHILD_E09_EXPECTED_PATH"
	execChildE09ReadOutput        = "e09:read-only-ok\n"
	execChildE09WorkspaceOutput   = "e09:workspace-write-ok;outside-denied\n"
	e09PoisonMarkerEnvironment    = "AGENTSERVER_E09_POISON_MARKER"
	e09BwrapArgv0Probe            = "agentserver-e09-bwrap-argv0"
	e09BwrapArgv0ProbeOutput      = "e09:bwrap-argv0-ok\n"

	e10StockMaxStdioFrameBytes                 = codexwire.DefaultMaxFrameBytes
	e10StockMaxJSONValues                      = codexwire.DefaultMaxJSONNodes
	e10StockRetainedOutputBytesPerProcess      = 1024 * 1024
	e10StockRetainedOutputChunksPerProcess     = 50_000
	e10StockRetainedStdinWriteIDsPerProcess    = 4096
	e10StockExitedProcessRetentionMilliseconds = 30_000
	e10AgentxMaxFrameBytes                     = 8 * 1024 * 1024
	e10AgentxMaxJSONValues                     = 64 * 1024
	e10AgentxMaxArgvElements                   = 256
	e10AgentxMaxArgvBytes                      = 16 * 1024
	e10AgentxMaxEnvVariables                   = 256
	e10AgentxMaxEnvBytes                       = 16 * 1024
	e10AgentxMaxWriteIDBytes                   = 128
	e10AgentxMaxOutputBufferBytesPerProcess    = 8 * 1024 * 1024
)

var e09CandidateCommits = map[string]string{
	"0.146.0-alpha.14": "9d84cad281364eb7f6be75e23067b0adc5e26106",
	"0.146.0":          "e363b08c9175ac1cbe5893615dd2cb9ddf95043b",
}

var e10CandidateBounds = map[string]runtimelock.ExecServerBounds{
	"0.146.0-alpha.14": characterizedE10StockBounds(),
	"0.146.0":          characterizedE10StockBounds(),
}

func characterizedE10StockBounds() runtimelock.ExecServerBounds {
	return runtimelock.ExecServerBounds{
		MaxStdioFrameBytes:                 e10StockMaxStdioFrameBytes,
		MaxJSONValues:                      e10StockMaxJSONValues,
		ArgvEnvLimit:                       runtimelock.ArgvEnvLimitTransportAndPlatformOnly,
		RetainedOutputBytesPerProcess:      e10StockRetainedOutputBytesPerProcess,
		RetainedOutputChunksPerProcess:     e10StockRetainedOutputChunksPerProcess,
		RetainedStdinWriteIDsPerProcess:    e10StockRetainedStdinWriteIDsPerProcess,
		ExitedProcessRetentionMilliseconds: e10StockExitedProcessRetentionMilliseconds,
	}
}

func characterizedE10AgentxLimits() runtimelock.AgentxLimits {
	return runtimelock.AgentxLimits{
		MaxFrameBytes:                  e10AgentxMaxFrameBytes,
		MaxJSONValues:                  e10AgentxMaxJSONValues,
		MaxArgvElements:                e10AgentxMaxArgvElements,
		MaxArgvBytes:                   e10AgentxMaxArgvBytes,
		MaxEnvVariables:                e10AgentxMaxEnvVariables,
		MaxEnvBytes:                    e10AgentxMaxEnvBytes,
		MaxWriteIDBytes:                e10AgentxMaxWriteIDBytes,
		MaxOutputBufferBytesPerProcess: e10AgentxMaxOutputBufferBytesPerProcess,
	}
}

// TestMain lets a live exec-server launch the already-built Go test binary as
// a deterministic child. The helper path bypasses the testing harness so its
// stdout and stderr contain only bytes intentionally emitted by the probe.
func TestMain(m *testing.M) {
	if handled, exitCode := runA12ImageSubprocess(); handled {
		os.Exit(exitCode)
	}
	if os.Args[0] == e09BwrapArgv0Probe {
		_, _ = io.WriteString(os.Stdout, e09BwrapArgv0ProbeOutput)
		os.Exit(0)
	}
	if poisonMarker := os.Getenv(e09PoisonMarkerEnvironment); poisonMarker != "" && filepath.Base(os.Args[0]) == "bwrap" {
		os.Exit(runE09PoisonBwrap(poisonMarker))
	}
	if mode, helper := os.LookupEnv(execChildModeEnvironment); helper {
		os.Exit(runExecChild(mode))
	}
	os.Exit(m.Run())
}

func runE09PoisonBwrap(marker string) int {
	if err := os.WriteFile(marker, []byte("ambient bwrap executed\n"), 0o600); err != nil {
		return reportExecChildError(fmt.Errorf("write E09 poison marker: %w", err))
	}
	if len(os.Args) == 2 && os.Args[1] == "--help" {
		_, _ = io.WriteString(os.Stdout, "--argv0\n--perms\n")
		return 0
	}
	return 97
}

func runExecChild(mode string) int {
	switch mode {
	case "output":
		if len(os.Args) != 2 || os.Args[0] != execChildArg0 || os.Args[1] != execChildArgument {
			return reportExecChildError(fmt.Errorf("argv = %q, want [%q %q]", os.Args, execChildArg0, execChildArgument))
		}
		expectedCWD := os.Getenv(execChildExpectedCWDEnv)
		cwd, err := os.Getwd()
		if err != nil {
			return reportExecChildError(err)
		}
		if cwd != expectedCWD {
			return reportExecChildError(fmt.Errorf("cwd = %q, want %q", cwd, expectedCWD))
		}
		gotEnvironment := os.Environ()
		sort.Strings(gotEnvironment)
		wantEnvironment := []string{
			execChildModeEnvironment + "=output",
			execChildExpectedCWDEnv + "=" + expectedCWD,
		}
		sort.Strings(wantEnvironment)
		if !equalStrings(gotEnvironment, wantEnvironment) {
			return reportExecChildError(fmt.Errorf("environment = %q, want %q", gotEnvironment, wantEnvironment))
		}
		if _, err := io.WriteString(os.Stdout, execChildOutputStdout); err != nil {
			return reportExecChildError(err)
		}
		if _, err := io.WriteString(os.Stderr, execChildOutputStderr); err != nil {
			return reportExecChildError(err)
		}
		return 0
	case "echo":
		input := make([]byte, len(execChildEchoInput))
		if _, err := io.ReadFull(os.Stdin, input); err != nil {
			return reportExecChildError(err)
		}
		if _, err := io.WriteString(os.Stdout, "echo:"+string(input)); err != nil {
			return reportExecChildError(err)
		}
		return 0
	case "e09-read-only":
		if err := validateE09ChildPATH(os.Getenv("PATH"), os.Getenv(execChildE09ExpectedPathEnv)); err != nil {
			return reportExecChildError(err)
		}
		readPath := os.Getenv(execChildE09ReadPathEnv)
		contents, err := os.ReadFile(readPath)
		if err != nil {
			return reportExecChildError(fmt.Errorf("read E09 fixture: %w", err))
		}
		if string(contents) != "e09-readable\n" {
			return reportExecChildError(fmt.Errorf("E09 read fixture = %q", contents))
		}
		if _, err := io.WriteString(os.Stdout, execChildE09ReadOutput); err != nil {
			return reportExecChildError(err)
		}
		return 0
	case "e09-workspace-write":
		if err := validateE09ChildPATH(os.Getenv("PATH"), os.Getenv(execChildE09ExpectedPathEnv)); err != nil {
			return reportExecChildError(err)
		}
		workspacePath := os.Getenv(execChildE09WorkspacePathEnv)
		outsidePath := os.Getenv(execChildE09OutsidePathEnv)
		if err := os.WriteFile(workspacePath, []byte("workspace write allowed\n"), 0o600); err != nil {
			return reportExecChildError(fmt.Errorf("write E09 workspace fixture: %w", err))
		}
		if err := os.WriteFile(outsidePath, []byte("sandbox escaped\n"), 0o600); err == nil {
			return reportExecChildError(errors.New("E09 sandbox allowed an outside-workspace write"))
		}
		if _, err := io.WriteString(os.Stdout, execChildE09WorkspaceOutput); err != nil {
			return reportExecChildError(err)
		}
		return 0
	case "block":
		pidFile := os.Getenv(execChildPIDFileEnvironment)
		if pidFile == "" {
			return reportExecChildError(fmt.Errorf("%s is required", execChildPIDFileEnvironment))
		}
		if err := os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
			return reportExecChildError(err)
		}
		if _, err := io.WriteString(os.Stdout, execChildReadyOutput); err != nil {
			return reportExecChildError(err)
		}
		// A bounded lifetime prevents a failed conformance probe from leaving a
		// permanent child behind. Successful terminate/EOF probes kill it first.
		time.Sleep(30 * time.Second)
		return 91
	case "tty-output":
		for name, file := range map[string]*os.File{"stdin": os.Stdin, "stdout": os.Stdout, "stderr": os.Stderr} {
			info, err := file.Stat()
			if err != nil {
				return reportExecChildError(fmt.Errorf("stat %s: %w", name, err))
			}
			if info.Mode()&os.ModeCharDevice == 0 {
				return reportExecChildError(fmt.Errorf("%s is not a character device: mode=%s", name, info.Mode()))
			}
		}
		if _, err := io.WriteString(os.Stdout, "tty:stdout|"); err != nil {
			return reportExecChildError(err)
		}
		if _, err := io.WriteString(os.Stderr, "tty:stderr|"); err != nil {
			return reportExecChildError(err)
		}
		return 0
	case "interrupt":
		interrupts := make(chan os.Signal, 1)
		signal.Notify(interrupts, os.Interrupt)
		defer signal.Stop(interrupts)
		pidFile := os.Getenv(execChildPIDFileEnvironment)
		if pidFile == "" {
			return reportExecChildError(fmt.Errorf("%s is required", execChildPIDFileEnvironment))
		}
		if err := os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
			return reportExecChildError(err)
		}
		if _, err := io.WriteString(os.Stdout, execChildReadyOutput); err != nil {
			return reportExecChildError(err)
		}
		select {
		case <-interrupts:
			if _, err := io.WriteString(os.Stdout, execChildInterruptedOutput); err != nil {
				return reportExecChildError(err)
			}
			return execChildInterruptExitCode
		case <-time.After(10 * time.Second):
			return reportExecChildError(fmt.Errorf("interrupt was not delivered"))
		}
	case "spawn-descendant":
		pidFile := os.Getenv(execChildPIDFileEnvironment)
		if pidFile == "" {
			return reportExecChildError(fmt.Errorf("%s is required", execChildPIDFileEnvironment))
		}
		child := exec.Command(os.Args[0])
		child.Env = []string{
			execChildModeEnvironment + "=descendant",
			execChildPIDFileEnvironment + "=" + pidFile,
		}
		child.Stdout = os.Stdout
		child.Stderr = os.Stderr
		if err := child.Start(); err != nil {
			return reportExecChildError(err)
		}
		if err := child.Process.Release(); err != nil {
			return reportExecChildError(err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			if _, err := os.Stat(pidFile); err == nil {
				break
			} else if !os.IsNotExist(err) {
				return reportExecChildError(err)
			}
			if time.Now().After(deadline) {
				return reportExecChildError(fmt.Errorf("descendant did not become ready"))
			}
			time.Sleep(10 * time.Millisecond)
		}
		if _, err := io.WriteString(os.Stdout, "parent-exiting\n"); err != nil {
			return reportExecChildError(err)
		}
		return execChildRootCrashExitCode
	case "descendant":
		pidFile := os.Getenv(execChildPIDFileEnvironment)
		if pidFile == "" {
			return reportExecChildError(fmt.Errorf("%s is required", execChildPIDFileEnvironment))
		}
		if _, err := io.WriteString(os.Stdout, "descendant-ready\n"); err != nil {
			return reportExecChildError(err)
		}
		if err := os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
			return reportExecChildError(err)
		}
		time.Sleep(30 * time.Second)
		return 94
	case "large-output":
		pattern := []byte("0123456789abcdef")
		output := bytes.Repeat(pattern, execChildLargeOutputBytes/len(pattern))
		if len(output) != execChildLargeOutputBytes {
			return reportExecChildError(fmt.Errorf("large output fixture has %d bytes", len(output)))
		}
		if _, err := os.Stdout.Write(output); err != nil {
			return reportExecChildError(err)
		}
		return 0
	case "write-id-window":
		payload := make([]byte, e10StockRetainedStdinWriteIDsPerProcess+2)
		if _, err := io.ReadFull(os.Stdin, payload); err != nil {
			return reportExecChildError(err)
		}
		if !bytes.Equal(payload[:len(payload)-1], bytes.Repeat([]byte{'d'}, len(payload)-1)) || payload[len(payload)-1] != 'e' {
			return reportExecChildError(fmt.Errorf("write-id payload = %q, want %d d bytes followed by e", payload, len(payload)-1))
		}
		if _, err := io.WriteString(os.Stdout, execChildWriteIDWindowOutput); err != nil {
			return reportExecChildError(err)
		}
		return 0
	case "tiny-output-chunks":
		ack := []byte{0}
		for index := 0; index < e10StockRetainedOutputChunksPerProcess+1; index++ {
			if _, err := os.Stdout.Write([]byte{execChildTinyOutputByte}); err != nil {
				return reportExecChildError(err)
			}
			if _, err := io.ReadFull(os.Stdin, ack); err != nil {
				return reportExecChildError(err)
			}
			if ack[0] != execChildTinyOutputACK {
				return reportExecChildError(fmt.Errorf("tiny-output ack = %q, want %q", ack[0], execChildTinyOutputACK))
			}
		}
		return 0
	case "oversized-input":
		if len(os.Args) != e10AgentxMaxArgvElements+1 {
			return reportExecChildError(fmt.Errorf("oversized argv count = %d, want %d", len(os.Args), e10AgentxMaxArgvElements+1))
		}
		if len(os.Args[1]) != e10AgentxMaxArgvBytes+1 || strings.Trim(os.Args[1], "a") != "" {
			return reportExecChildError(fmt.Errorf("oversized argv payload has %d bytes", len(os.Args[1])))
		}
		oversizedEnvironment := os.Getenv("AGENTSERVER_E10_OVERSIZED_ENV")
		if len(oversizedEnvironment) != e10AgentxMaxEnvBytes+1 || strings.Trim(oversizedEnvironment, "e") != "" {
			return reportExecChildError(fmt.Errorf("oversized environment payload has %d bytes", len(oversizedEnvironment)))
		}
		if _, err := io.WriteString(os.Stdout, execChildOversizedInputOutput); err != nil {
			return reportExecChildError(err)
		}
		return 0
	case "network-http":
		return runExecChildNetworkHTTP()
	default:
		return reportExecChildError(fmt.Errorf("unknown helper mode %q", mode))
	}
}

func validateE09ChildPATH(got, controlledBase string) error {
	entries := filepath.SplitList(got)
	if len(entries) == 0 || entries[len(entries)-1] != controlledBase {
		return fmt.Errorf("PATH = %q, want Codex aliases followed by %q", got, controlledBase)
	}
	for _, entry := range entries[:len(entries)-1] {
		if entry == "" || !filepath.IsAbs(entry) {
			return fmt.Errorf("PATH contains invalid Codex alias entry %q", entry)
		}
		if _, err := os.Lstat(filepath.Join(entry, "bwrap")); !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("PATH alias entry %q exposes bwrap or cannot be inspected: %w", entry, err)
		}
	}
	return nil
}

func runExecChildNetworkHTTP() int {
	if pidFile := os.Getenv(execChildPIDFileEnvironment); pidFile != "" {
		if err := os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
			return reportExecChildError(err)
		}
	}
	target, err := url.Parse(os.Getenv(execChildNetworkTargetEnv))
	if err != nil || target.Scheme != "http" || target.Host == "" {
		return reportExecChildError(fmt.Errorf("invalid %s: %q", execChildNetworkTargetEnv, os.Getenv(execChildNetworkTargetEnv)))
	}
	proxy, err := url.Parse(os.Getenv("HTTP_PROXY"))
	if err != nil || proxy.Scheme != "http" || proxy.Host == "" {
		return reportExecChildError(fmt.Errorf("invalid injected HTTP_PROXY: %q", os.Getenv("HTTP_PROXY")))
	}

	// Go's HTTP transport bypasses loopback targets even when proxy variables
	// are set. Dial the injected endpoint explicitly so this probe cannot pass
	// without traversing the executor-local proxy and its policy callback.
	connection, err := net.DialTimeout("tcp", proxy.Host, 3*time.Second)
	if err != nil {
		return reportExecChildError(fmt.Errorf("dial injected HTTP proxy: %w", err))
	}
	defer connection.Close()
	if err := connection.SetDeadline(time.Now().Add(12 * time.Second)); err != nil {
		return reportExecChildError(err)
	}
	if _, err := fmt.Fprintf(
		connection,
		"GET %s HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n",
		target.String(),
		target.Host,
	); err != nil {
		return reportExecChildError(fmt.Errorf("write proxy request: %w", err))
	}
	response, err := http.ReadResponse(bufio.NewReader(connection), &http.Request{Method: http.MethodGet})
	if err != nil {
		return reportExecChildError(fmt.Errorf("read proxy response: %w", err))
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return reportExecChildError(fmt.Errorf("read proxy response body: %w", err))
	}
	if _, err := fmt.Fprintf(os.Stdout, "status=%d\n%s", response.StatusCode, body); err != nil {
		return reportExecChildError(err)
	}
	return 0
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func reportExecChildError(err error) int {
	_, _ = fmt.Fprintf(os.Stderr, "exec child helper: %v\n", err)
	return 92
}

func TestExecServerE02ProcessOutputAndReplay(t *testing.T) {
	process, paths := startLiveCodex(t, "exec-server", "--listen", "stdio", "--strict-config")
	initializeExecServer(t, process)
	collector := newRPCCollector(process)

	sendRPC(t, process, map[string]any{
		"id":     2,
		"method": "process/start",
		"params": execStartParams(t, paths, "output-process", "output", false, nil),
	})
	startResponse := collector.response(t, "2")
	var started struct {
		ProcessID string `json:"processId"`
	}
	mustDecodeResult(t, startResponse, &started)
	if started.ProcessID != "output-process" {
		t.Fatalf("process/start processId = %q, want output-process", started.ProcessID)
	}

	events := collector.processEventsUntilClosed(t, "output-process")
	observed := assertCompletedProcessEvents(t, events, 0)
	if !bytes.Equal(observed.stdout, []byte(execChildOutputStdout)) {
		t.Fatalf("stdout = %q, want %q", observed.stdout, execChildOutputStdout)
	}
	if !bytes.Equal(observed.stderr, []byte(execChildOutputStderr)) {
		t.Fatalf("stderr = %q, want %q", observed.stderr, execChildOutputStderr)
	}

	sendRPC(t, process, map[string]any{
		"id":     3,
		"method": "process/read",
		"params": map[string]any{
			"processId": "output-process",
			"afterSeq":  0,
			"maxBytes":  1024,
			"waitMs":    0,
		},
	})
	readResponse := collector.response(t, "3")
	read := decodeProcessRead(t, readResponse)
	if !read.Exited || !read.Closed || read.ExitCode == nil || *read.ExitCode != 0 {
		t.Fatalf("unexpected terminal process/read state: %+v", read)
	}
	if read.Failure != nil || read.SandboxDenied {
		t.Fatalf("unexpected process/read failure state: %+v", read)
	}
	if read.NextSeq != observed.lastOutputSeq+1 {
		t.Fatalf("bounded process/read nextSeq = %d, want %d", read.NextSeq, observed.lastOutputSeq+1)
	}
	replayed := aggregateReadChunks(t, read.Chunks)
	if !bytes.Equal(replayed.stdout, observed.stdout) || !bytes.Equal(replayed.stderr, observed.stderr) {
		t.Fatalf("process/read replay differs from notifications: replay=%+v notifications=%+v", replayed, observed)
	}

	sendRPC(t, process, map[string]any{
		"id":     4,
		"method": "process/read",
		"params": map[string]any{
			"processId": "output-process",
			"afterSeq":  observed.lastOutputSeq,
			"waitMs":    0,
		},
	})
	terminalRead := decodeProcessRead(t, collector.response(t, "4"))
	if len(terminalRead.Chunks) != 0 || !terminalRead.Exited || !terminalRead.Closed {
		t.Fatalf("unexpected terminal-only process/read state: %+v", terminalRead)
	}
	if terminalRead.NextSeq != observed.closedSeq+1 {
		t.Fatalf("unbounded process/read nextSeq = %d, want %d", terminalRead.NextSeq, observed.closedSeq+1)
	}

	sendRPC(t, process, map[string]any{
		"id":     5,
		"method": "process/write",
		"params": map[string]any{
			"processId": "output-process",
			"chunk":     base64.StdEncoding.EncodeToString([]byte("ignored")),
			"writeId":   "write-after-close",
		},
	})
	assertWriteStatus(t, collector.response(t, "5"), "stdinClosed")

	closeAndWait(t, process)
}

func TestExecServerE09ShellV1MinimalSandboxRunsSystemBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the shell-v1 Windows executable and sandbox profile require a native fixture")
	}
	process, paths := startLiveCodex(t, "exec-server", "--listen", "stdio", "--strict-config")
	initializeExecServer(t, process)
	collector := newRPCCollector(process)
	workspaceURI := localFileURI(t, paths.cwd)
	requestID := 2

	if runtime.GOOS == "darwin" {
		pathOnlyEntries := []any{
			map[string]any{
				"path":   map[string]any{"type": "path", "path": workspaceURI},
				"access": "read",
			},
			map[string]any{
				"path":   map[string]any{"type": "path", "path": workspaceURI},
				"access": "write",
			},
		}
		sendRPC(t, process, map[string]any{
			"id":     requestID,
			"method": "process/start",
			"params": shellV1LiveStartParams(workspaceURI, "shell-v1-path-only", pathOnlyEntries),
		})
		var pathOnlyStarted struct {
			ProcessID string `json:"processId"`
		}
		mustDecodeResult(t, collector.response(t, strconv.Itoa(requestID)), &pathOnlyStarted)
		_, exitCode := inspectTerminalProcessEvents(t, collector.processEventsUntilClosed(t, pathOnlyStarted.ProcessID))
		if exitCode == 0 {
			t.Fatal("workspace-only shell-v1 sandbox unexpectedly supplied the macOS platform runtime")
		}
		requestID++
	}

	minimalEntries := []any{
		map[string]any{
			"path":   map[string]any{"type": "special", "value": map[string]any{"kind": "minimal"}},
			"access": "read",
		},
		map[string]any{
			"path":   map[string]any{"type": "path", "path": workspaceURI},
			"access": "write",
		},
	}
	sendRPC(t, process, map[string]any{
		"id":     requestID,
		"method": "process/start",
		"params": shellV1LiveStartParams(workspaceURI, "shell-v1-minimal", minimalEntries),
	})
	var started struct {
		ProcessID string `json:"processId"`
	}
	mustDecodeResult(t, collector.response(t, strconv.Itoa(requestID)), &started)
	if started.ProcessID != "shell-v1-minimal" {
		t.Fatalf("process/start processId = %q, want shell-v1-minimal", started.ProcessID)
	}

	events := collector.processEventsUntilClosed(t, started.ProcessID)
	observed := assertCompletedProcessEvents(t, events, 0)
	if string(observed.stdout) != "agentserver-shell-v1\n" || len(observed.stderr) != 0 || len(observed.pty) != 0 {
		t.Fatalf("shell-v1 system executable output: stdout=%q stderr=%q pty=%q", observed.stdout, observed.stderr, observed.pty)
	}
	closeAndWait(t, process)
}

func shellV1LiveStartParams(workspaceURI, processID string, entries []any) map[string]any {
	return map[string]any{
		"processId": processID,
		"argv":      []string{"/bin/echo", "agentserver-shell-v1"},
		"cwd":       workspaceURI,
		"env":       map[string]string{},
		"envPolicy": map[string]any{
			"inherit":               "none",
			"ignoreDefaultExcludes": false,
			"exclude":               []string{},
			"set":                   map[string]string{},
			"includeOnly":           []string{},
		},
		"tty":       false,
		"pipeStdin": false,
		"arg0":      nil,
		"sandbox": map[string]any{
			"permissions": map[string]any{
				"type": "managed",
				"file_system": map[string]any{
					"type":    "restricted",
					"entries": entries,
				},
				"network": "restricted",
			},
			"cwd":                          workspaceURI,
			"workspaceRoots":               []string{workspaceURI},
			"windowsSandboxLevel":          "disabled",
			"windowsSandboxPrivateDesktop": false,
			"useLegacyLandlock":            false,
		},
		"enforceManagedNetwork": true,
	}
}

func TestExecServerE02PTYOutputAndReplay(t *testing.T) {
	process, paths := startLiveCodex(t, "exec-server", "--listen", "stdio", "--strict-config")
	initializeExecServer(t, process)
	collector := newRPCCollector(process)
	params := execStartParams(t, paths, "pty-process", "tty-output", false, nil)
	params["tty"] = true

	sendRPC(t, process, map[string]any{
		"id":     2,
		"method": "process/start",
		"params": params,
	})
	var started struct {
		ProcessID string `json:"processId"`
	}
	mustDecodeResult(t, collector.response(t, "2"), &started)
	if started.ProcessID != "pty-process" {
		t.Fatalf("process/start processId = %q, want pty-process", started.ProcessID)
	}

	events := collector.processEventsUntilClosed(t, "pty-process")
	observed := assertCompletedProcessEvents(t, events, 0)
	if len(observed.stdout) != 0 || len(observed.stderr) != 0 || !bytes.Equal(observed.pty, []byte(execChildPTYOutput)) {
		t.Fatalf("PTY output = %+v, want one merged pty stream %q", observed, execChildPTYOutput)
	}

	sendRPC(t, process, map[string]any{
		"id":     3,
		"method": "process/read",
		"params": map[string]any{
			"processId": "pty-process",
			"afterSeq":  0,
			"maxBytes":  1024,
			"waitMs":    0,
		},
	})
	read := decodeProcessRead(t, collector.response(t, "3"))
	if !read.Exited || !read.Closed || read.ExitCode == nil || *read.ExitCode != 0 {
		t.Fatalf("unexpected terminal PTY process/read state: %+v", read)
	}
	replayed := aggregateReadChunks(t, read.Chunks)
	if len(replayed.stdout) != 0 || len(replayed.stderr) != 0 || !bytes.Equal(replayed.pty, observed.pty) {
		t.Fatalf("PTY process/read replay differs from notifications: replay=%+v notifications=%+v", replayed, observed)
	}

	closeAndWait(t, process)
}

func TestExecServerE03ProcessWriteIsIdempotent(t *testing.T) {
	process, paths := startLiveCodex(t, "exec-server", "--listen", "stdio", "--strict-config")
	initializeExecServer(t, process)
	collector := newRPCCollector(process)

	sendRPC(t, process, map[string]any{
		"id":     2,
		"method": "process/start",
		"params": execStartParams(t, paths, "echo-process", "echo", true, nil),
	})
	mustDecodeResult(t, collector.response(t, "2"), &struct {
		ProcessID string `json:"processId"`
	}{})

	encodedInput := base64.StdEncoding.EncodeToString([]byte(execChildEchoInput))
	sendRPC(t, process, map[string]any{
		"id":     3,
		"method": "process/write",
		"params": map[string]any{
			"processId": "missing-process",
			"chunk":     encodedInput,
			"writeId":   "unknown-write",
		},
	})
	assertWriteStatus(t, collector.response(t, "3"), "unknownProcess")

	write := map[string]any{
		"processId": "echo-process",
		"chunk":     encodedInput,
		"writeId":   "echo-write-1",
	}
	sendRPC(t, process, map[string]any{"id": 4, "method": "process/write", "params": write})
	assertWriteStatus(t, collector.response(t, "4"), "accepted")
	sendRPC(t, process, map[string]any{"id": 5, "method": "process/write", "params": write})
	assertWriteStatus(t, collector.response(t, "5"), "accepted")

	events := collector.processEventsUntilClosed(t, "echo-process")
	observed := assertCompletedProcessEvents(t, events, 0)
	want := []byte("echo:" + execChildEchoInput)
	if !bytes.Equal(observed.stdout, want) {
		t.Fatalf("echo stdout = %q, want exactly one %q", observed.stdout, want)
	}
	if len(observed.stderr) != 0 {
		t.Fatalf("echo stderr = %q, want empty", observed.stderr)
	}

	closeAndWait(t, process)
}

func TestExecServerE03ProcessTerminate(t *testing.T) {
	process, paths := startLiveCodex(t, "exec-server", "--listen", "stdio", "--strict-config")
	initializeExecServer(t, process)
	collector := newRPCCollector(process)
	pidFile := filepath.Join(paths.temporary, "terminate-child.pid")

	sendRPC(t, process, map[string]any{
		"id":     2,
		"method": "process/start",
		"params": execStartParams(t, paths, "block-process", "block", false, map[string]string{
			execChildPIDFileEnvironment: pidFile,
		}),
	})
	mustDecodeResult(t, collector.response(t, "2"), &struct {
		ProcessID string `json:"processId"`
	}{})
	pid := waitForChildPID(t, pidFile)
	disableChildCleanup := cleanupChildProcess(t, pid)

	sendRPC(t, process, map[string]any{
		"id":     3,
		"method": "process/terminate",
		"params": map[string]any{"processId": "missing-process"},
	})
	assertTerminateRunning(t, collector.response(t, "3"), false)

	sendRPC(t, process, map[string]any{
		"id":     4,
		"method": "process/terminate",
		"params": map[string]any{"processId": "block-process"},
	})
	assertTerminateRunning(t, collector.response(t, "4"), true)
	events := collector.processEventsUntilClosed(t, "block-process")
	observed := assertTerminalProcessEvents(t, events)
	if !bytes.Equal(observed.stdout, []byte(execChildReadyOutput)) {
		t.Fatalf("terminated process stdout = %q, want %q", observed.stdout, execChildReadyOutput)
	}
	waitForProcessGone(t, pid)
	disableChildCleanup()

	sendRPC(t, process, map[string]any{
		"id":     5,
		"method": "process/terminate",
		"params": map[string]any{"processId": "block-process"},
	})
	assertTerminateRunning(t, collector.response(t, "5"), false)

	closeAndWait(t, process)
}

// This is a negative E03 characterization. Stock Codex 0.146.0 delivers an
// interrupt to a running process, but returns the same empty success object for
// a missing process, a delivered signal, and an already-exited process. Agentx
// therefore cannot infer signal delivery from the RPC response alone.
func TestExecServerE03SignalDeliveryAndAmbiguousNoop(t *testing.T) {
	process, paths := startLiveCodex(t, "exec-server", "--listen", "stdio", "--strict-config")
	initializeExecServer(t, process)
	collector := newRPCCollector(process)
	pidFile := filepath.Join(paths.temporary, "interrupt-child.pid")

	sendRPC(t, process, map[string]any{
		"id":     2,
		"method": "process/start",
		"params": execStartParams(t, paths, "interrupt-process", "interrupt", false, map[string]string{
			execChildPIDFileEnvironment: pidFile,
		}),
	})
	mustDecodeResult(t, collector.response(t, "2"), &struct {
		ProcessID string `json:"processId"`
	}{})
	pid := waitForChildPID(t, pidFile)
	disableChildCleanup := cleanupChildProcess(t, pid)

	sendRPC(t, process, map[string]any{
		"id":     3,
		"method": "process/signal",
		"params": map[string]any{"processId": "missing-process", "signal": "interrupt"},
	})
	assertEmptyObjectResult(t, collector.response(t, "3"))

	sendRPC(t, process, map[string]any{
		"id":     4,
		"method": "process/signal",
		"params": map[string]any{"processId": "interrupt-process", "signal": "interrupt"},
	})
	assertEmptyObjectResult(t, collector.response(t, "4"))
	events := collector.processEventsUntilClosed(t, "interrupt-process")
	observed := assertCompletedProcessEvents(t, events, execChildInterruptExitCode)
	wantOutput := []byte(execChildReadyOutput + execChildInterruptedOutput)
	if !bytes.Equal(observed.stdout, wantOutput) || len(observed.stderr) != 0 || len(observed.pty) != 0 {
		t.Fatalf("interrupted process output = %+v, want stdout %q", observed, wantOutput)
	}
	waitForProcessGone(t, pid)
	disableChildCleanup()

	sendRPC(t, process, map[string]any{
		"id":     5,
		"method": "process/signal",
		"params": map[string]any{"processId": "interrupt-process", "signal": "interrupt"},
	})
	assertEmptyObjectResult(t, collector.response(t, "5"))
	t.Log("E03 stock negative fact retained: process/signal returns indistinguishable success, so the outer profile must exclude it")

	closeAndWait(t, process)
}

func TestExecServerE04FilesystemReadLifecycle(t *testing.T) {
	process, paths := startLiveCodex(t, "exec-server", "--listen", "stdio", "--strict-config")
	initializeExecServer(t, process)
	collector := newRPCCollector(process)

	contents := []byte{0x00, 0x01, 0x02, 0xff, 'x', '\n'}
	filePath := filepath.Join(paths.cwd, "data.bin")
	if err := os.WriteFile(filePath, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(paths.cwd, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	pathURI := localFileURI(t, filePath)
	workspaceURI := localFileURI(t, paths.cwd)

	sendRPC(t, process, map[string]any{
		"id":     2,
		"method": "fs/readFile",
		"params": map[string]any{"path": pathURI, "sandbox": nil},
	})
	var readFile struct {
		DataBase64 string `json:"dataBase64"`
	}
	mustDecodeResult(t, collector.response(t, "2"), &readFile)
	decoded, err := base64.StdEncoding.DecodeString(readFile.DataBase64)
	if err != nil {
		t.Fatalf("decode fs/readFile dataBase64: %v", err)
	}
	if !bytes.Equal(decoded, contents) {
		t.Fatalf("fs/readFile = %v, want %v", decoded, contents)
	}

	// Stock 0.146.0 deliberately rejects platform-sandboxed streaming reads.
	// read_file therefore cannot forward a restricted context to fs/open and
	// must instead use an agentx-authorized, OS-contained fs-only lane.
	sendRPC(t, process, map[string]any{
		"id":     3,
		"method": "fs/open",
		"params": map[string]any{
			"handleId": "sandboxed-handle",
			"path":     pathURI,
			"sandbox":  fsReadRestrictedSandbox(workspaceURI),
		},
	})
	mustRPCError(t, collector.response(t, "3"))

	sendRPC(t, process, map[string]any{
		"id":     4,
		"method": "fs/readFile",
		"params": map[string]any{"path": filePath, "sandbox": nil},
	})
	mustRPCError(t, collector.response(t, "4"))

	sendRPC(t, process, map[string]any{
		"id":     5,
		"method": "fs/open",
		"params": map[string]any{"handleId": "handle-1", "path": pathURI, "sandbox": nil},
	})
	var opened struct {
		HandleID string `json:"handleId"`
	}
	mustDecodeResult(t, collector.response(t, "5"), &opened)
	if opened.HandleID != "handle-1" {
		t.Fatalf("fs/open handleId = %q, want handle-1", opened.HandleID)
	}

	first := readFileBlock(t, process, collector, 6, "handle-1", 0, 3)
	if !bytes.Equal(first.chunk, contents[:3]) || first.eof {
		t.Fatalf("first fs/readBlock = %+v, want first three bytes and eof=false", first)
	}
	second := readFileBlock(t, process, collector, 7, "handle-1", 3, 64)
	if !bytes.Equal(second.chunk, contents[3:]) || !second.eof {
		t.Fatalf("second fs/readBlock = %+v, want remaining bytes and eof=true", second)
	}

	sendRPC(t, process, map[string]any{
		"id":     8,
		"method": "fs/close",
		"params": map[string]any{"handleId": "handle-1"},
	})
	var closed map[string]any
	mustDecodeResult(t, collector.response(t, "8"), &closed)
	if len(closed) != 0 {
		t.Fatalf("fs/close result = %+v, want empty object", closed)
	}

	sendRPC(t, process, map[string]any{
		"id":     9,
		"method": "fs/readBlock",
		"params": map[string]any{"handleId": "handle-1", "offset": 0, "len": 1},
	})
	mustRPCError(t, collector.response(t, "9"))

	uncanonicalPath := paths.cwd + string(os.PathSeparator) + "nested" + string(os.PathSeparator) + ".." + string(os.PathSeparator) + "data.bin"
	sendRPC(t, process, map[string]any{
		"id":     10,
		"method": "fs/canonicalize",
		"params": map[string]any{"path": localFileURI(t, uncanonicalPath), "sandbox": nil},
	})
	var canonicalized struct {
		Path string `json:"path"`
	}
	mustDecodeResult(t, collector.response(t, "10"), &canonicalized)
	assertFileURIPath(t, canonicalized.Path, filePath)
	t.Log("E04 stock fact: fs/open is bounded by fs/readBlock but rejects platform-sandboxed streaming reads")

	closeAndWait(t, process)
}

func fsReadRestrictedSandbox(workspaceURI string) map[string]any {
	return map[string]any{
		"permissions": map[string]any{
			"type": "managed",
			"file_system": map[string]any{
				"type": "restricted",
				"entries": []any{
					map[string]any{
						"path":   map[string]any{"type": "special", "value": map[string]any{"kind": "minimal"}},
						"access": "read",
					},
					map[string]any{
						"path":   map[string]any{"type": "path", "path": workspaceURI},
						"access": "read",
					},
				},
			},
			"network": "restricted",
		},
		"cwd":                          workspaceURI,
		"workspaceRoots":               []string{workspaceURI},
		"windowsSandboxLevel":          "disabled",
		"windowsSandboxPrivateDesktop": false,
		"useLegacyLandlock":            false,
	}
}

func TestExecServerE05NetworkPolicyReverseDecisions(t *testing.T) {
	tests := []struct {
		name             string
		decisionType     string
		reason           string
		rpcError         bool
		invalidResult    bool
		wantStatus       int
		wantBodyContains string
		wantOriginHits   int64
	}{
		{
			name:             "allow",
			decisionType:     "allow",
			wantStatus:       http.StatusOK,
			wantBodyContains: execChildNetworkOriginBody,
			wantOriginHits:   1,
		},
		{
			name:             "deny",
			decisionType:     "deny",
			reason:           "e05_owner_denied",
			wantStatus:       http.StatusForbidden,
			wantBodyContains: "e05_owner_denied",
		},
		{
			name:             "ask",
			decisionType:     "ask",
			reason:           "e05_owner_approval_required",
			wantStatus:       http.StatusForbidden,
			wantBodyContains: "e05_owner_approval_required",
		},
		{
			name:             "rpc-error-fails-closed",
			rpcError:         true,
			wantStatus:       http.StatusForbidden,
			wantBodyContains: "not_allowed",
		},
		{
			name:             "unknown-decision-fails-closed",
			invalidResult:    true,
			wantStatus:       http.StatusForbidden,
			wantBodyContains: "not_allowed",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			originURL, originHits := startE05Origin(t)
			process, paths := startLiveCodex(t, "exec-server", "--listen", "stdio", "--strict-config")
			initializeExecServer(t, process)
			collector := newRPCCollector(process)
			processID := "network-" + test.name

			sendRPC(t, process, map[string]any{
				"id":     2,
				"method": "process/start",
				"params": e05ExecStartParams(t, paths, processID, originURL, 1_000, nil),
			})
			reverse := collector.request(t, "network/policyRequest")
			assertE05NetworkPolicyRequest(t, reverse, processID, originURL)
			if test.rpcError {
				sendRPC(t, process, e05ReverseRPCError(reverse, -32601, "unsupported exec-server reverse request"))
			} else if test.invalidResult {
				sendRPC(t, process, map[string]any{
					"id": reverse.ID,
					"result": map[string]any{
						"decision": map[string]any{"type": "future_allow"},
					},
				})
			} else {
				sendRPC(t, process, e05ReferenceClientReply(reverse, test.decisionType, test.reason))
			}

			var started struct {
				ProcessID string `json:"processId"`
			}
			mustDecodeResult(t, collector.response(t, "2"), &started)
			if started.ProcessID != processID {
				t.Fatalf("process/start processId = %q, want %q", started.ProcessID, processID)
			}
			body := assertE05NetworkProcessResult(t, collector, processID, test.wantStatus)
			if !strings.Contains(body, test.wantBodyContains) {
				t.Fatalf("network response body = %q, want substring %q", body, test.wantBodyContains)
			}
			if test.decisionType == "ask" && !strings.Contains(body, `"decision":"ask"`) {
				t.Fatalf("ask network response body = %q, want explicit ask decision", body)
			}
			if got := originHits.Load(); got != test.wantOriginHits {
				t.Fatalf("origin requests = %d, want %d", got, test.wantOriginHits)
			}

			closeAndWait(t, process)
		})
	}
}

func TestExecServerE05NetworkPolicyTimeoutFailsClosed(t *testing.T) {
	originURL, originHits := startE05Origin(t)
	process, paths := startLiveCodex(t, "exec-server", "--listen", "stdio", "--strict-config")
	initializeExecServer(t, process)
	collector := newRPCCollector(process)

	sendRPC(t, process, map[string]any{
		"id":     2,
		"method": "process/start",
		"params": e05ExecStartParams(t, paths, "network-timeout", originURL, 1, nil),
	})
	reverse := collector.request(t, "network/policyRequest")
	assertE05NetworkPolicyRequest(t, reverse, "network-timeout", originURL)
	// Intentionally leave the reverse request unresolved. Stock exec-server
	// adds a five-second transport margin to the configured controller budget
	// and must turn expiry into deny("not_allowed").
	var started struct {
		ProcessID string `json:"processId"`
	}
	mustDecodeResult(t, collector.response(t, "2"), &started)
	if started.ProcessID != "network-timeout" {
		t.Fatalf("process/start processId = %q, want network-timeout", started.ProcessID)
	}
	body := assertE05NetworkProcessResult(t, collector, "network-timeout", http.StatusForbidden)
	if !strings.Contains(body, "not_allowed") {
		t.Fatalf("timed-out network response body = %q, want not_allowed", body)
	}
	if got := originHits.Load(); got != 0 {
		t.Fatalf("origin requests after policy timeout = %d, want 0", got)
	}

	closeAndWait(t, process)
}

func TestExecServerE05ConnectionEOFFailsClosed(t *testing.T) {
	if !processLivenessProbeSupported {
		t.Skip("OS process liveness probe is not supported on this platform")
	}
	originURL, originHits := startE05Origin(t)
	process, paths := startLiveCodex(t, "exec-server", "--listen", "stdio", "--strict-config")
	initializeExecServer(t, process)
	collector := newRPCCollector(process)
	pidFile := filepath.Join(paths.temporary, "network-disconnect-child.pid")

	sendRPC(t, process, map[string]any{
		"id":     2,
		"method": "process/start",
		"params": e05ExecStartParams(t, paths, "network-disconnect", originURL, 60_000, map[string]string{
			execChildPIDFileEnvironment: pidFile,
		}),
	})
	reverse := collector.request(t, "network/policyRequest")
	assertE05NetworkPolicyRequest(t, reverse, "network-disconnect", originURL)
	var started struct {
		ProcessID string `json:"processId"`
	}
	mustDecodeResult(t, collector.response(t, "2"), &started)
	if started.ProcessID != "network-disconnect" {
		t.Fatalf("process/start processId = %q, want network-disconnect", started.ProcessID)
	}
	pid := waitForChildPID(t, pidFile)
	disableChildCleanup := cleanupChildProcess(t, pid)

	closeAndWait(t, process)
	waitForProcessGone(t, pid)
	disableChildCleanup()
	if got := originHits.Load(); got != 0 {
		t.Fatalf("origin requests after exec-server connection EOF = %d, want 0", got)
	}
}

func TestE05ReferenceClientRejectsUnknownReverseMethod(t *testing.T) {
	request, err := codexwire.Parse([]byte(`{"id":"future-1","method":"network/futureRequest","params":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	reply := e05ReferenceClientReply(request, "allow", "")
	var encoded bytes.Buffer
	encoder, err := codexwire.NewEncoder(&encoded, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if err := encoder.Write(reply); err != nil {
		t.Fatal(err)
	}
	response, err := codexwire.Parse(bytes.TrimSpace(encoded.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if response.Kind != codexwire.KindError || response.Error == nil || response.Error.Code != -32601 {
		t.Fatalf("unknown reverse method response = %+v, want -32601 error", response)
	}
}

func TestE05ReferenceClientDeniesInvalidNetworkPolicyParams(t *testing.T) {
	tests := []string{
		`{"id":"invalid-1","method":"network/policyRequest","params":{}}`,
		`{"id":"invalid-2","method":"network/policyRequest","params":{"processId":"","request":{"protocol":"http","host":"example.com","port":80}}}`,
		`{"id":"invalid-3","method":"network/policyRequest","params":{"processId":"process","request":{"protocol":"future_protocol","host":"example.com","port":80}}}`,
		`{"id":"invalid-4","method":"network/policyRequest","params":{"processId":"process","request":{"protocol":"http","host":"host name","port":80}}}`,
		`{"id":"invalid-5","method":"network/policyRequest","params":{"processId":"process","request":{"protocol":"http","host":"example.com","port":0}}}`,
		`{"id":"invalid-6","method":"network/policyRequest","params":{"processId":"process","request":{"protocol":"http","host":"example.com","port":80,"future":true}}}`,
		fmt.Sprintf(`{"id":"invalid-7","method":"network/policyRequest","params":{"processId":"%s","request":{"protocol":"http","host":"example.com","port":80}}}`, strings.Repeat("p", 257)),
		fmt.Sprintf(`{"id":"invalid-8","method":"network/policyRequest","params":{"processId":"process","request":{"protocol":"http","host":"%s","port":80}}}`, strings.Repeat("h", 254)),
	}
	for _, frame := range tests {
		request, err := codexwire.Parse([]byte(frame))
		if err != nil {
			t.Fatal(err)
		}
		reply := e05ReferenceClientReply(request, "allow", "")
		result, ok := reply["result"].(map[string]any)
		if !ok {
			t.Fatalf("invalid network params reply = %+v, want decision result", reply)
		}
		decision, ok := result["decision"].(map[string]any)
		if !ok || decision["type"] != "deny" || decision["reason"] != "not_allowed" {
			t.Fatalf("invalid network params reply = %+v, want deny(not_allowed)", reply)
		}
	}
}

func TestE05ReferenceClientDeniesInvalidDecisionReasons(t *testing.T) {
	request, err := codexwire.Parse([]byte(`{"id":"reason-1","method":"network/policyRequest","params":{"processId":"process","request":{"protocol":"http","host":"example.com","port":80}}}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, reason := range []string{"", "control\ncharacter", strings.Repeat("r", 1025)} {
		reply := e05ReferenceClientReply(request, "deny", reason)
		result := reply["result"].(map[string]any)
		decision := result["decision"].(map[string]any)
		if decision["type"] != "deny" || decision["reason"] != "not_allowed" {
			t.Fatalf("invalid decision reason reply = %+v, want deny(not_allowed)", reply)
		}
	}
}

func TestExecServerE06StdioEOFTerminatesManagedChild(t *testing.T) {
	if !processLivenessProbeSupported {
		t.Skip("OS process liveness probe is not supported on this platform")
	}
	process, paths := startLiveCodex(t, "exec-server", "--listen", "stdio", "--strict-config")
	initializeExecServer(t, process)
	collector := newRPCCollector(process)
	pidFile := filepath.Join(paths.temporary, "eof-child.pid")

	sendRPC(t, process, map[string]any{
		"id":     2,
		"method": "process/start",
		"params": execStartParams(t, paths, "eof-process", "block", false, map[string]string{
			execChildPIDFileEnvironment: pidFile,
		}),
	})
	mustDecodeResult(t, collector.response(t, "2"), &struct {
		ProcessID string `json:"processId"`
	}{})
	pid := waitForChildPID(t, pidFile)
	disableChildCleanup := cleanupChildProcess(t, pid)
	alive, err := processIsAlive(pid)
	if err != nil {
		t.Fatalf("probe child %d before EOF: %v", pid, err)
	}
	if !alive {
		t.Fatalf("managed child %d exited before exec-server stdin EOF", pid)
	}

	closeAndWait(t, process)
	waitForProcessGone(t, pid)
	disableChildCleanup()
}

// This is a negative E07 characterization. If the root process exits while a
// descendant keeps the inherited pipes open, process/terminate observes the
// root as already exited and does not kill the remaining process group. Stdio
// connection shutdown does eventually drop the session and kill the group,
// but that is not operation-scoped crash recovery.
func TestExecServerE07RootExitLeavesDescendantUntilConnectionShutdown(t *testing.T) {
	if !processLivenessProbeSupported {
		t.Skip("OS process liveness probe is not supported on this platform")
	}
	process, paths := startLiveCodex(t, "exec-server", "--listen", "stdio", "--strict-config")
	initializeExecServer(t, process)
	collector := newRPCCollector(process)
	pidFile := filepath.Join(paths.temporary, "descendant.pid")

	sendRPC(t, process, map[string]any{
		"id":     2,
		"method": "process/start",
		"params": execStartParams(t, paths, "crashed-root-process", "spawn-descendant", false, map[string]string{
			execChildPIDFileEnvironment: pidFile,
		}),
	})
	mustDecodeResult(t, collector.response(t, "2"), &struct {
		ProcessID string `json:"processId"`
	}{})
	descendantPID := waitForChildPID(t, pidFile)
	disableDescendantCleanup := cleanupChildProcess(t, descendantPID)

	exitedMessage := collector.notification(t, "process/exited")
	var exited struct {
		ProcessID     string `json:"processId"`
		Seq           uint64 `json:"seq"`
		ExitCode      int    `json:"exitCode"`
		SandboxDenied *bool  `json:"sandboxDenied"`
	}
	if err := exitedMessage.DecodeParams(&exited); err != nil {
		t.Fatal(err)
	}
	if exited.ProcessID != "crashed-root-process" || exited.Seq == 0 || exited.ExitCode != execChildRootCrashExitCode ||
		exited.SandboxDenied == nil || *exited.SandboxDenied {
		t.Fatalf("unexpected root process/exited notification: %+v", exited)
	}
	assertProcessAlive(t, descendantPID, "after root exit")

	sendRPC(t, process, map[string]any{
		"id":     3,
		"method": "process/read",
		"params": map[string]any{"processId": "crashed-root-process", "afterSeq": 0, "waitMs": 0},
	})
	read := decodeProcessRead(t, collector.response(t, "3"))
	if !read.Exited || read.ExitCode == nil || *read.ExitCode != execChildRootCrashExitCode || read.Closed {
		t.Fatalf("root crash process/read state = %+v, want exited but not closed", read)
	}

	sendRPC(t, process, map[string]any{
		"id":     4,
		"method": "process/terminate",
		"params": map[string]any{"processId": "crashed-root-process"},
	})
	assertTerminateRunning(t, collector.response(t, "4"), false)
	assertProcessAlive(t, descendantPID, "after process/terminate returned running=false")

	closeAndWait(t, process)
	waitForProcessGone(t, descendantPID)
	disableDescendantCleanup()
	t.Log("E07 stock negative fact retained: root exit strands descendants, so cleanup must shut down a dedicated instance")
}

// This is the positive E03/E07 reference adapter gate. Each outer process owns
// a different stock exec-server stdio instance. The adapter never negotiates
// process/signal; when the first root exits without process/closed, it shuts
// down only that connection and verifies the descendant is gone. The second
// process must remain alive and independently terminable throughout.
func TestExecServerE03E07DedicatedInstanceAdapterProfile(t *testing.T) {
	if !processLivenessProbeSupported {
		t.Skip("OS process liveness probe is not supported on this platform")
	}
	if execadapter.AllowsOuterProcessMethod("process/signal") {
		t.Fatal("reference agentx outer profile exposes process/signal")
	}

	firstProcess, firstPaths := startLiveCodex(t, "exec-server", "--listen", "stdio", "--strict-config")
	secondProcess, secondPaths := startLiveCodex(t, "exec-server", "--listen", "stdio", "--strict-config")
	var descendantPID atomic.Int64
	var secondPID atomic.Int64
	first := newLiveExecAdapter(t, firstProcess, "e07-instance-first", &descendantPID)
	second := newLiveExecAdapter(t, secondProcess, "e07-instance-second", &secondPID)

	ctx, cancel := context.WithTimeout(context.Background(), liveProbeTimeout)
	defer cancel()
	secondPIDFile := filepath.Join(secondPaths.temporary, "dedicated-second.pid")
	if err := second.Start(ctx, execStartParams(t, secondPaths, "dedicated-second", "block", false, map[string]string{
		execChildPIDFileEnvironment: secondPIDFile,
	})); err != nil {
		t.Fatalf("start second dedicated process: %v", err)
	}
	secondPIDValue := waitForChildPID(t, secondPIDFile)
	secondPID.Store(int64(secondPIDValue))
	disableSecondCleanup := cleanupChildProcess(t, secondPIDValue)

	descendantPIDFile := filepath.Join(firstPaths.temporary, "dedicated-descendant.pid")
	if err := first.Start(ctx, execStartParams(t, firstPaths, "dedicated-first", "spawn-descendant", false, map[string]string{
		execChildPIDFileEnvironment: descendantPIDFile,
	})); err != nil {
		t.Fatalf("start first dedicated process: %v", err)
	}
	descendantPIDValue := waitForChildPID(t, descendantPIDFile)
	descendantPID.Store(int64(descendantPIDValue))
	disableDescendantCleanup := cleanupChildProcess(t, descendantPIDValue)

	firstResult, err := first.Wait(ctx)
	if err != nil {
		t.Fatalf("wait for forced first cleanup: %v", err)
	}
	if firstResult.InstanceID != "e07-instance-first" || firstResult.ProcessID != "dedicated-first" ||
		firstResult.State != execadapter.TerminalCleanupForced || firstResult.ProtocolClosed ||
		firstResult.ExitCode == nil || *firstResult.ExitCode != execChildRootCrashExitCode ||
		!errors.Is(firstResult.Cause, execadapter.ErrProcessDidNotClose) {
		t.Fatalf("first dedicated terminal = %+v", firstResult)
	}
	waitForProcessGone(t, descendantPIDValue)
	disableDescendantCleanup()
	assertProcessAlive(t, secondPIDValue, "after unrelated dedicated instance shutdown")

	if _, err := second.Forward(ctx, "process/signal", json.RawMessage(`{"processId":"dedicated-second","signal":"interrupt"}`)); !errors.Is(err, execadapter.ErrMethodNotNegotiated) {
		t.Fatalf("outer process/signal error = %v", err)
	}
	terminateRaw, err := second.Forward(ctx, "process/terminate", json.RawMessage(`{"processId":"dedicated-second"}`))
	if err != nil {
		t.Fatalf("terminate second dedicated process: %v", err)
	}
	var terminate struct {
		Running bool `json:"running"`
	}
	if err := json.Unmarshal(terminateRaw, &terminate); err != nil || !terminate.Running {
		t.Fatalf("second process/terminate = %s, error %v", terminateRaw, err)
	}
	secondResult, err := second.Wait(ctx)
	if err != nil {
		t.Fatalf("wait for second dedicated process: %v", err)
	}
	if secondResult.InstanceID != "e07-instance-second" || secondResult.ProcessID != "dedicated-second" ||
		secondResult.State != execadapter.TerminalClosed || !secondResult.ProtocolClosed {
		t.Fatalf("second dedicated terminal = %+v", secondResult)
	}
	waitForProcessGone(t, secondPIDValue)
	disableSecondCleanup()
}

func TestExecServerE08IgnoresUserHomeAndDoesNotInheritServerSecrets(t *testing.T) {
	binary, paths := prepareLiveCodex(t)
	userCodexHome := filepath.Join(paths.home, ".codex")
	if err := os.MkdirAll(userCodexHome, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(userCodexHome, "config.toml"),
		[]byte("agentserver_unknown_poison_key = true\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(userCodexHome, "auth.json"),
		[]byte(`{"poison":"must-not-be-read"}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	paths.environment = append(
		paths.environment,
		"CODEX_ACCESS_TOKEN=exec-server-only-sentinel",
		"AGENTSERVER_EXECUTOR_CAPABILITY=exec-server-only-sentinel",
	)

	process := startPreparedLiveCodex(t, binary, paths, "exec-server", "--listen", "stdio", "--strict-config")
	initializeExecServer(t, process)
	collector := newRPCCollector(process)
	sendRPC(t, process, map[string]any{
		"id":     2,
		"method": "process/start",
		"params": execStartParams(t, paths, "isolated-environment-process", "output", false, nil),
	})
	mustDecodeResult(t, collector.response(t, "2"), &struct {
		ProcessID string `json:"processId"`
	}{})
	events := collector.processEventsUntilClosed(t, "isolated-environment-process")
	observed := assertCompletedProcessEvents(t, events, 0)
	if !bytes.Equal(observed.stdout, []byte(execChildOutputStdout)) ||
		!bytes.Equal(observed.stderr, []byte(execChildOutputStderr)) || len(observed.pty) != 0 {
		t.Fatalf("isolated child output = %+v", observed)
	}

	closeAndWait(t, process)
}

func TestExecServerE09VerifiedLaunchExcludesAmbientPATH(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the Phase 1 exec-server launch profile is not characterized on Windows")
	}
	binary, paths := prepareLiveCodex(t)
	release := candidateRelease(t, binary, paths)
	commit, characterized := e09CandidateCommits[release]
	if !characterized {
		t.Skipf("E09 launch probe characterizes 0.146.0-alpha.14 and 0.146.0; candidate is %s", release)
	}

	bundleRoot := filepath.Join(paths.root, "e09-runtime")
	codexArtifact := copyE09Executable(t, binary, bundleRoot, "bin/codex", release)
	externalExecutables := map[string]runtimelock.FileArtifact{}
	if runtime.GOOS == "linux" {
		bwrapSource := findE09BundledBwrap(t, binary)
		externalExecutables["bwrap"] = copyE09Executable(
			t,
			bwrapSource,
			bundleRoot,
			"codex-resources/bwrap",
			release,
		)
	}
	manifest := runtimelock.Manifest{
		ManifestVersion:                runtimelock.CurrentManifestVersion,
		CodexRelease:                   release,
		CodexCommit:                    commit,
		AppServerSchemaSHA256:          strings.Repeat("a", 64),
		AppServerSchemaDigestAlgorithm: runtimelock.AppServerSchemaDigestAlgorithmV1,
		ExecProtocolSourceSHA256:       strings.Repeat("b", 64),
		ExecServerBounds:               e10CandidateBounds[release],
		AgentxLimits:                   characterizedE10AgentxLimits(),
		CheckpointAllowlistVersion:     1,
		AgentxProtocolVersion:          "2.0",
		Artifacts: map[string]runtimelock.PlatformArtifacts{
			runtimelock.CurrentPlatform(): {
				Codex:               codexArtifact,
				ExternalExecutables: externalExecutables,
			},
		},
	}

	poisonDirectory := filepath.Join(paths.root, "e09-poison-path")
	if err := os.MkdirAll(poisonDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	poisonMarker := filepath.Join(paths.root, "e09-poison-executed")
	for _, name := range []string{"codex", "bwrap", "rg"} {
		poison := filepath.Join(poisonDirectory, name)
		contents := []byte("#!/bin/sh\nprintf poison > \"$AGENTSERVER_E09_POISON_MARKER\"\nexit 97\n")
		if err := os.WriteFile(poison, contents, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	poisonedBase := replaceEnvironmentValue(paths.environment, "PATH", poisonDirectory)
	poisonedBase = append(poisonedBase, "AGENTSERVER_E09_POISON_MARKER="+poisonMarker)

	var process *codexprocess.Process
	err := manifest.VerifyAndStartExecServer(bundleRoot, runtimelock.CurrentPlatform(), func(plan runtimelock.ExecServerLaunchPlan) error {
		environment, err := plan.Environment(poisonedBase)
		if err != nil {
			return err
		}
		launchPaths := paths
		launchPaths.environment = environment
		process = startPreparedLiveCodex(t, plan.Program(), launchPaths, plan.Arguments()...)
		return nil
	})
	if err != nil {
		t.Fatalf("verified exec-server launch: %v", err)
	}
	initializeExecServer(t, process)
	closeAndWait(t, process)
	if _, err := os.Stat(poisonMarker); !os.IsNotExist(err) {
		t.Fatalf("ambient PATH executable was selected, marker stat error = %v", err)
	}

	badArtifacts := manifest.Artifacts[runtimelock.CurrentPlatform()]
	badArtifacts.Codex.SHA256 = strings.Repeat("0", 64)
	badManifest := manifest
	badManifest.Artifacts = map[string]runtimelock.PlatformArtifacts{
		runtimelock.CurrentPlatform(): badArtifacts,
	}
	starterCalled := false
	err = badManifest.VerifyAndStartExecServer(bundleRoot, runtimelock.CurrentPlatform(), func(runtimelock.ExecServerLaunchPlan) error {
		starterCalled = true
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("digest-mismatch launch error = %v", err)
	}
	if starterCalled {
		t.Fatal("digest-mismatch launch invoked the process starter")
	}

	if runtime.GOOS == "linux" {
		t.Log("Linux host launch excluded ambient bwrap; E09 still requires a production-image sandbox request proving bundled bwrap selection")
	} else {
		t.Log("Darwin host launch excluded ambient PATH; Linux bundled-bwrap selection remains an image-level E09 gate")
	}
}

func TestExecServerE10CandidateReleaseIsExplicitlyCharacterized(t *testing.T) {
	binary, paths := prepareLiveCodex(t)
	release := candidateRelease(t, binary, paths)
	if _, characterized := e10CandidateBounds[release]; !characterized {
		t.Fatalf("Codex %s has no explicit E10 bounds characterization", release)
	}
}

func TestExecServerE10RetainedOutputReplayIsBounded(t *testing.T) {
	process, paths := startLiveCodex(t, "exec-server", "--listen", "stdio", "--strict-config")
	initializeExecServer(t, process)
	collector := newRPCCollector(process)

	sendRPC(t, process, map[string]any{
		"id":     2,
		"method": "process/start",
		"params": execStartParams(t, paths, "large-output-process", "large-output", false, nil),
	})
	mustDecodeResult(t, collector.response(t, "2"), &struct {
		ProcessID string `json:"processId"`
	}{})
	events := collector.processEventsUntilClosed(t, "large-output-process")
	observed := assertCompletedProcessEvents(t, events, 0)
	if len(observed.stdout) != execChildLargeOutputBytes || len(observed.stderr) != 0 || len(observed.pty) != 0 {
		t.Fatalf("large output notification bytes: stdout=%d stderr=%d pty=%d", len(observed.stdout), len(observed.stderr), len(observed.pty))
	}

	sendRPC(t, process, map[string]any{
		"id":     3,
		"method": "process/read",
		"params": map[string]any{"processId": "large-output-process", "afterSeq": 0, "waitMs": 0},
	})
	read := decodeProcessRead(t, collector.response(t, "3"))
	if !read.Exited || !read.Closed || read.ExitCode == nil || *read.ExitCode != 0 {
		t.Fatalf("large output process/read terminal state = %+v", read)
	}
	if len(read.Chunks) == 0 {
		t.Fatal("large output replay returned no retained chunks")
	}
	if read.Chunks[0].Seq <= 1 {
		t.Fatalf("large output replay did not expose prefix eviction: firstSeq=%d", read.Chunks[0].Seq)
	}
	if read.NextSeq != observed.closedSeq+1 {
		t.Fatalf("large output process/read nextSeq = %d, want %d", read.NextSeq, observed.closedSeq+1)
	}
	replayed := aggregateReadChunks(t, read.Chunks)
	if len(replayed.stderr) != 0 || len(replayed.pty) != 0 {
		t.Fatalf("large output replay used unexpected streams: %+v", replayed)
	}
	if len(replayed.stdout) > e10StockRetainedOutputBytesPerProcess || len(replayed.stdout) <= e10StockRetainedOutputBytesPerProcess-8_192 {
		t.Fatalf("retained output bytes = %d, want (%d, %d]", len(replayed.stdout), e10StockRetainedOutputBytesPerProcess-8_192, e10StockRetainedOutputBytesPerProcess)
	}
	wantSuffix := observed.stdout[len(observed.stdout)-len(replayed.stdout):]
	if !bytes.Equal(replayed.stdout, wantSuffix) {
		t.Fatal("retained process/read output is not the exact suffix of streamed output")
	}

	closeAndWait(t, process)
}

func TestExecServerE10RetainedOutputChunkLimitIs50000(t *testing.T) {
	process, paths := startLiveCodexWithLifetime(t, 90*time.Second, "exec-server", "--listen", "stdio", "--strict-config")
	initializeExecServer(t, process)
	collector := newRPCCollector(process)
	const processID = "tiny-output-chunks"
	sendRPC(t, process, map[string]any{
		"id":     2,
		"method": "process/start",
		"params": execStartParams(t, paths, processID, "tiny-output-chunks", true, nil),
	})
	mustDecodeResult(t, collector.response(t, "2"), &struct {
		ProcessID string `json:"processId"`
	}{})

	for index := 0; index < e10StockRetainedOutputChunksPerProcess+1; index++ {
		notification := collector.nextNotification(t)
		if notification.Method != "process/output" {
			t.Fatalf("tiny-output notification %d method = %q, want process/output", index, notification.Method)
		}
		var output struct {
			ProcessID string `json:"processId"`
			Seq       uint64 `json:"seq"`
			Stream    string `json:"stream"`
			Chunk     string `json:"chunk"`
		}
		if err := notification.DecodeParams(&output); err != nil {
			t.Fatal(err)
		}
		chunk, err := base64.StdEncoding.DecodeString(output.Chunk)
		if err != nil {
			t.Fatal(err)
		}
		if output.ProcessID != processID || output.Seq != uint64(index+1) || output.Stream != "stdout" || !bytes.Equal(chunk, []byte{execChildTinyOutputByte}) {
			t.Fatalf("tiny-output notification %d = %+v chunk=%q", index, output, chunk)
		}

		requestID := index + 3
		sendRPC(t, process, map[string]any{
			"id":     requestID,
			"method": "process/write",
			"params": map[string]any{
				"processId": processID,
				"chunk":     base64.StdEncoding.EncodeToString([]byte{execChildTinyOutputACK}),
				"writeId":   fmt.Sprintf("e10-chunk-ack-%05d", index),
			},
		})
		assertWriteStatus(t, collector.response(t, strconv.Itoa(requestID)), "accepted")
	}

	var exitedSeq, closedSeq uint64
	for terminalEvents := 0; terminalEvents < 2; terminalEvents++ {
		notification := collector.nextNotification(t)
		var terminal struct {
			ProcessID string `json:"processId"`
			Seq       uint64 `json:"seq"`
			ExitCode  *int   `json:"exitCode"`
		}
		if err := notification.DecodeParams(&terminal); err != nil {
			t.Fatal(err)
		}
		if terminal.ProcessID != processID {
			t.Fatalf("tiny-output terminal processId = %q", terminal.ProcessID)
		}
		switch notification.Method {
		case "process/exited":
			if terminal.ExitCode == nil || *terminal.ExitCode != 0 {
				t.Fatalf("tiny-output exit = %+v", terminal)
			}
			exitedSeq = terminal.Seq
		case "process/closed":
			closedSeq = terminal.Seq
		default:
			t.Fatalf("tiny-output terminal notification = %q", notification.Method)
		}
	}
	wantExitedSeq := uint64(e10StockRetainedOutputChunksPerProcess + 2)
	if exitedSeq != wantExitedSeq || closedSeq != wantExitedSeq+1 {
		t.Fatalf("tiny-output terminal seqs: exited=%d closed=%d, want %d/%d", exitedSeq, closedSeq, wantExitedSeq, wantExitedSeq+1)
	}

	readRequestID := e10StockRetainedOutputChunksPerProcess + 4
	sendRPC(t, process, map[string]any{
		"id":     readRequestID,
		"method": "process/read",
		"params": map[string]any{"processId": processID, "afterSeq": 0, "waitMs": 0},
	})
	read := decodeProcessRead(t, collector.response(t, strconv.Itoa(readRequestID)))
	if !read.Exited || !read.Closed || len(read.Chunks) != e10StockRetainedOutputChunksPerProcess {
		t.Fatalf("tiny-output retained state: exited=%t closed=%t chunks=%d", read.Exited, read.Closed, len(read.Chunks))
	}
	if read.Chunks[0].Seq != 2 || read.Chunks[len(read.Chunks)-1].Seq != uint64(e10StockRetainedOutputChunksPerProcess+1) || read.NextSeq != closedSeq+1 {
		t.Fatalf("tiny-output retained sequence: first=%d last=%d next=%d closed=%d", read.Chunks[0].Seq, read.Chunks[len(read.Chunks)-1].Seq, read.NextSeq, closedSeq)
	}
	for index, chunk := range read.Chunks {
		decoded, err := base64.StdEncoding.DecodeString(chunk.Chunk)
		if err != nil {
			t.Fatal(err)
		}
		if chunk.Stream != "stdout" || !bytes.Equal(decoded, []byte{execChildTinyOutputByte}) {
			t.Fatalf("tiny-output retained chunk %d = stream %q bytes %q", index, chunk.Stream, decoded)
		}
	}
	closeAndWait(t, process)
}

func TestExecServerE10StdioFrameBoundary(t *testing.T) {
	t.Run("exact limit is accepted", func(t *testing.T) {
		process, _ := startLiveCodexWithLifetime(t, 45*time.Second, "exec-server", "--listen", "stdio", "--strict-config")
		initializeExecServer(t, process)
		collector := newRPCCollector(process)
		frame := e10PaddedUnknownRequest(t, 2, e10StockMaxStdioFrameBytes)
		if err := process.SendRawFrame(frame); err != nil {
			t.Fatalf("send exact-limit stdio frame: %v", err)
		}
		assertRPCErrorCode(t, collector.response(t, "2"), -32601, "does not implement")
		closeAndWait(t, process)
	})

	t.Run("first byte over limit disconnects", func(t *testing.T) {
		process, _ := startLiveCodexWithLifetime(t, 45*time.Second, "exec-server", "--listen", "stdio", "--strict-config")
		initializeExecServer(t, process)
		frame := e10PaddedUnknownRequest(t, 2, e10StockMaxStdioFrameBytes+1)
		writeErr := process.SendRawFrame(frame)

		receiveContext, cancelReceive := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancelReceive()
		if message, err := process.Peer.Receive(receiveContext); err == nil {
			t.Fatalf("over-limit stdio frame returned message %+v instead of disconnecting", message)
		} else if !errors.Is(err, io.EOF) {
			t.Fatalf("receive after over-limit stdio frame: %v (write_error=%v)", err, writeErr)
		}
		waitForCleanCodexExit(t, process, "over-limit stdio frame")
		if err := process.CloseStdin(); err != nil && !errors.Is(err, os.ErrClosed) {
			t.Fatalf("close local stdin after frame disconnect: %v", err)
		}
	})
}

func TestExecServerE10JSONComplexityBoundary(t *testing.T) {
	process, _ := startLiveCodexWithLifetime(t, 30*time.Second, "exec-server", "--listen", "stdio", "--strict-config")
	initializeExecServer(t, process)
	collector := newRPCCollector(process)

	exact := e10ComplexityRequest(t, 2, e10StockMaxJSONValues)
	if err := process.SendRawFrame(exact); err != nil {
		t.Fatalf("send exact JSON-value-limit frame: %v", err)
	}
	assertRPCErrorCode(t, collector.response(t, "2"), -32601, "does not implement")

	over := e10ComplexityRequest(t, 3, e10StockMaxJSONValues+1)
	if err := process.SendRawFrame(over); err != nil {
		t.Fatalf("send over JSON-value-limit frame: %v", err)
	}
	assertRPCErrorCode(t, collector.response(t, "-1"), -32600, "exceeds the limit")

	// Complexity rejection is message-scoped rather than a transport
	// disconnect. A subsequent ordinary request must still succeed.
	sendRPC(t, process, map[string]any{"id": 4, "method": "environment/status", "params": nil})
	var status struct {
		Status string `json:"status"`
	}
	mustDecodeResult(t, collector.response(t, "4"), &status)
	if status.Status != "ready" {
		t.Fatalf("environment/status after complexity rejection = %q, want ready", status.Status)
	}
	closeAndWait(t, process)
}

func TestExecServerE10StockDoesNotEnforceAgentxArgvEnvLimits(t *testing.T) {
	process, paths := startLiveCodexWithLifetime(t, 30*time.Second, "exec-server", "--listen", "stdio", "--strict-config")
	initializeExecServer(t, process)
	collector := newRPCCollector(process)

	params := execStartParams(t, paths, "over-agentx-input-limits", "oversized-input", false, map[string]string{
		"AGENTSERVER_E10_OVERSIZED_ENV": strings.Repeat("e", e10AgentxMaxEnvBytes+1),
	})
	argv := make([]string, e10AgentxMaxArgvElements+1)
	argv[0] = os.Args[0]
	if !filepath.IsAbs(argv[0]) {
		absolute, err := filepath.Abs(argv[0])
		if err != nil {
			t.Fatal(err)
		}
		argv[0] = absolute
	}
	argv[1] = strings.Repeat("a", e10AgentxMaxArgvBytes+1)
	for index := 2; index < len(argv); index++ {
		argv[index] = "x"
	}
	params["argv"] = argv
	params["arg0"] = nil
	environment := params["env"].(map[string]string)
	if err := characterizedE10AgentxLimits().ValidateProcessStart(argv, nil, environment); err == nil {
		t.Fatal("reference agentx limits accepted deliberately oversized argv/env")
	}

	sendRPC(t, process, map[string]any{"id": 2, "method": "process/start", "params": params})
	mustDecodeResult(t, collector.response(t, "2"), &struct {
		ProcessID string `json:"processId"`
	}{})
	events := collector.processEventsUntilClosed(t, "over-agentx-input-limits")
	observed := assertCompletedProcessEvents(t, events, 0)
	if !bytes.Equal(observed.stdout, []byte(execChildOversizedInputOutput)) || len(observed.stderr) != 0 || len(observed.pty) != 0 {
		t.Fatalf("oversized-input child output = %+v", observed)
	}
	closeAndWait(t, process)
}

func TestExecServerE10WriteIDRetentionIs4096FIFO(t *testing.T) {
	process, paths := startLiveCodexWithLifetime(t, 45*time.Second, "exec-server", "--listen", "stdio", "--strict-config")
	initializeExecServer(t, process)
	collector := newRPCCollector(process)
	sendRPC(t, process, map[string]any{
		"id":     2,
		"method": "process/start",
		"params": execStartParams(t, paths, "write-id-window", "write-id-window", true, nil),
	})
	mustDecodeResult(t, collector.response(t, "2"), &struct {
		ProcessID string `json:"processId"`
	}{})

	nextRequestID := 3
	sendWrite := func(writeID string, chunk byte) {
		t.Helper()
		requestID := nextRequestID
		nextRequestID++
		sendRPC(t, process, map[string]any{
			"id":     requestID,
			"method": "process/write",
			"params": map[string]any{
				"processId": "write-id-window",
				"chunk":     base64.StdEncoding.EncodeToString([]byte{chunk}),
				"writeId":   "e10-write-" + writeID,
			},
		})
		assertWriteStatus(t, collector.response(t, strconv.Itoa(requestID)), "accepted")
	}

	for index := 0; index < e10StockRetainedStdinWriteIDsPerProcess+1; index++ {
		sendWrite(strconv.Itoa(index), 'd')
	}
	// After 4097 distinct ids, id 1 is the oldest retained entry while id 0
	// has been evicted. A retained retry must not write 'r'; the evicted retry
	// must write the final 'e' byte that lets the child finish.
	sendWrite("1", 'r')
	sendWrite("0", 'e')

	events := collector.processEventsUntilClosed(t, "write-id-window")
	observed := assertCompletedProcessEvents(t, events, 0)
	if !bytes.Equal(observed.stdout, []byte(execChildWriteIDWindowOutput)) || len(observed.stderr) != 0 || len(observed.pty) != 0 {
		t.Fatalf("write-id-window child output = %+v", observed)
	}
	closeAndWait(t, process)
}

func TestExecServerE10ExitedProcessRetentionIs30Seconds(t *testing.T) {
	process, paths := startLiveCodexWithLifetime(t, 55*time.Second, "exec-server", "--listen", "stdio", "--strict-config")
	initializeExecServer(t, process)
	collector := newRPCCollector(process)
	const processID = "retained-exited-process"
	sendRPC(t, process, map[string]any{
		"id":     2,
		"method": "process/start",
		"params": execStartParams(t, paths, processID, "output", false, nil),
	})
	mustDecodeResult(t, collector.response(t, "2"), &struct {
		ProcessID string `json:"processId"`
	}{})
	events := collector.processEventsUntilClosed(t, processID)
	assertCompletedProcessEvents(t, events, 0)
	closedObservedAt := time.Now()

	sendRPC(t, process, map[string]any{
		"id":     3,
		"method": "process/read",
		"params": map[string]any{"processId": processID, "afterSeq": 0, "waitMs": 0},
	})
	if immediate := decodeProcessRead(t, collector.response(t, "3")); !immediate.Exited || !immediate.Closed {
		t.Fatalf("immediate retained process/read = %+v", immediate)
	}

	lowerBound := time.Duration(e10StockExitedProcessRetentionMilliseconds-2_000) * time.Millisecond
	upperBound := time.Duration(e10StockExitedProcessRetentionMilliseconds+8_000) * time.Millisecond
	timer := time.NewTimer(lowerBound)
	defer timer.Stop()
	<-timer.C
	sendRPC(t, process, map[string]any{
		"id":     4,
		"method": "process/read",
		"params": map[string]any{"processId": processID, "afterSeq": 0, "waitMs": 0},
	})
	if retained := collector.response(t, "4"); retained.Kind != codexwire.KindResponse {
		t.Fatalf("closed process was evicted before %s: %+v", lowerBound, retained.Error)
	}

	requestID := 5
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var evictedAfter time.Duration
	for evictedAfter == 0 {
		if elapsed := time.Since(closedObservedAt); elapsed > upperBound {
			t.Fatalf("closed process remained readable after %s", elapsed)
		}
		<-ticker.C
		sendRPC(t, process, map[string]any{
			"id":     requestID,
			"method": "process/read",
			"params": map[string]any{"processId": processID, "afterSeq": 0, "waitMs": 0},
		})
		response := collector.response(t, strconv.Itoa(requestID))
		requestID++
		if response.Kind == codexwire.KindResponse {
			continue
		}
		assertRPCErrorCode(t, response, -32600, "unknown process id")
		evictedAfter = time.Since(closedObservedAt)
	}
	if evictedAfter < lowerBound || evictedAfter > upperBound {
		t.Fatalf("closed process eviction observed after %s, want [%s, %s]", evictedAfter, lowerBound, upperBound)
	}

	// Eviction removes the process-map key, so the same logical id becomes
	// reusable rather than referring to stale terminal state.
	sendRPC(t, process, map[string]any{
		"id":     requestID,
		"method": "process/start",
		"params": execStartParams(t, paths, processID, "output", false, nil),
	})
	mustDecodeResult(t, collector.response(t, strconv.Itoa(requestID)), &struct {
		ProcessID string `json:"processId"`
	}{})
	assertCompletedProcessEvents(t, collector.processEventsUntilClosed(t, processID), 0)
	closeAndWait(t, process)
}

func e10PaddedUnknownRequest(t *testing.T, requestID, frameBytes int) []byte {
	t.Helper()
	prefix := []byte(fmt.Sprintf(`{"id":%d,"method":"e10/padded","params":{"padding":"`, requestID))
	suffix := []byte(`"}}`)
	paddingBytes := frameBytes - len(prefix) - len(suffix)
	if paddingBytes < 0 {
		t.Fatalf("E10 frame bound %d is too small for fixture", frameBytes)
	}
	frame := make([]byte, 0, frameBytes)
	frame = append(frame, prefix...)
	paddingStart := len(frame)
	frame = frame[:paddingStart+paddingBytes]
	for index := paddingStart; index < len(frame); index++ {
		frame[index] = 'p'
	}
	frame = append(frame, suffix...)
	if len(frame) != frameBytes {
		t.Fatalf("E10 padded frame bytes = %d, want %d", len(frame), frameBytes)
	}
	return frame
}

func e10ComplexityRequest(t *testing.T, requestID, valueNodes int) []byte {
	t.Helper()
	// Root object, id, method, and params array consume four values. Object
	// keys do not consume the stock serde value-node budget.
	arrayValues := valueNodes - 4
	if arrayValues < 0 {
		t.Fatalf("E10 JSON value bound %d is too small for fixture", valueNodes)
	}
	frame := make([]byte, 0, 64+arrayValues*5)
	frame = append(frame, fmt.Sprintf(`{"id":%d,"method":"e10/complexity","params":[`, requestID)...)
	for index := 0; index < arrayValues; index++ {
		if index != 0 {
			frame = append(frame, ',')
		}
		frame = append(frame, "null"...)
	}
	frame = append(frame, ']', '}')
	return frame
}

func assertRPCErrorCode(t *testing.T, message codexwire.Message, code int64, contains string) {
	t.Helper()
	if message.Kind != codexwire.KindError || message.Error == nil || message.Error.Code != code || !strings.Contains(message.Error.Message, contains) {
		t.Fatalf("RPC message = %+v, want error %d containing %q", message, code, contains)
	}
}

func copyE09Executable(t *testing.T, sourcePath, bundleRoot, relativePath, release string) runtimelock.FileArtifact {
	t.Helper()
	resolvedSource, err := filepath.EvalSymlinks(sourcePath)
	if err != nil {
		t.Fatalf("resolve E09 source executable: %v", err)
	}
	source, err := os.Open(resolvedSource)
	if err != nil {
		t.Fatalf("open E09 source executable: %v", err)
	}
	defer source.Close()

	destinationPath := filepath.Join(bundleRoot, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(destinationPath), 0o700); err != nil {
		t.Fatal(err)
	}
	destination, err := os.OpenFile(destinationPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
	if err != nil {
		t.Fatalf("create E09 bundle executable: %v", err)
	}
	_, copyErr := io.Copy(destination, source)
	closeErr := destination.Close()
	if copyErr != nil {
		t.Fatalf("copy E09 bundle executable: %v", copyErr)
	}
	if closeErr != nil {
		t.Fatalf("close E09 bundle executable: %v", closeErr)
	}
	if err := os.Chmod(destinationPath, 0o700); err != nil {
		t.Fatal(err)
	}
	digest, size, err := runtimelock.HashFile(destinationPath)
	if err != nil {
		t.Fatalf("hash E09 bundle executable: %v", err)
	}
	return runtimelock.FileArtifact{
		Path:      relativePath,
		SourceURL: e09NPMPlatformSourceURL(t, release),
		SHA256:    digest,
		SizeBytes: size,
	}
}

func e09NPMPlatformSourceURL(t *testing.T, release string) string {
	t.Helper()
	osName := runtime.GOOS
	if osName == "windows" {
		osName = "win32"
	}
	archName := runtime.GOARCH
	if archName == "amd64" {
		archName = "x64"
	}
	if (osName != "darwin" && osName != "linux" && osName != "win32") ||
		(archName != "arm64" && archName != "x64") {
		t.Fatalf("no official npm platform package mapping for %s-%s", runtime.GOOS, runtime.GOARCH)
	}
	return fmt.Sprintf(
		"https://registry.npmjs.org/@openai/codex/-/codex-%s-%s-%s.tgz",
		release,
		osName,
		archName,
	)
}

func findE09BundledBwrap(t *testing.T, binary string) string {
	t.Helper()
	resolvedBinary, err := filepath.EvalSymlinks(binary)
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Dir(resolvedBinary)
	candidates := []string{
		filepath.Join(directory, "codex-resources", "bwrap"),
		filepath.Join(filepath.Dir(directory), "codex-resources", "bwrap"),
	}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
			return candidate
		}
	}
	t.Fatalf("candidate Linux release has no bundled bwrap at %q", candidates)
	return ""
}

func replaceEnvironmentValue(environment []string, name, value string) []string {
	replaced := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		entryName, _, found := strings.Cut(entry, "=")
		if found && strings.EqualFold(entryName, name) {
			continue
		}
		replaced = append(replaced, entry)
	}
	return append(replaced, name+"="+value)
}

type observedProcessEvent struct {
	method        string
	seq           uint64
	stream        string
	chunk         []byte
	exitCode      *int
	sandboxDenied *bool
}

func (c *rpcCollector) processEventsUntilClosed(t *testing.T, processID string) []observedProcessEvent {
	t.Helper()
	var events []observedProcessEvent
	seenSequences := make(map[uint64]struct{})
	var closedSequence uint64
	for {
		message := c.nextNotification(t)
		if message.Method != "process/output" && message.Method != "process/exited" && message.Method != "process/closed" {
			t.Fatalf("unexpected exec-server notification %q", message.Method)
		}
		var params struct {
			ProcessID     string `json:"processId"`
			Seq           uint64 `json:"seq"`
			Stream        string `json:"stream"`
			Chunk         string `json:"chunk"`
			ExitCode      *int   `json:"exitCode"`
			SandboxDenied *bool  `json:"sandboxDenied"`
		}
		if err := message.DecodeParams(&params); err != nil {
			t.Fatal(err)
		}
		if params.ProcessID != processID {
			t.Fatalf("notification processId = %q, want %q", params.ProcessID, processID)
		}
		if params.Seq == 0 {
			t.Fatalf("process notification %q has zero sequence", message.Method)
		}
		if _, duplicate := seenSequences[params.Seq]; duplicate {
			t.Fatalf("duplicate process event sequence %d", params.Seq)
		}
		seenSequences[params.Seq] = struct{}{}
		event := observedProcessEvent{
			method:        message.Method,
			seq:           params.Seq,
			stream:        params.Stream,
			exitCode:      params.ExitCode,
			sandboxDenied: params.SandboxDenied,
		}
		if message.Method == "process/output" {
			chunk, err := base64.StdEncoding.DecodeString(params.Chunk)
			if err != nil {
				t.Fatalf("decode process/output chunk: %v", err)
			}
			event.chunk = chunk
		}
		events = append(events, event)
		if message.Method == "process/closed" {
			if closedSequence != 0 {
				t.Fatalf("duplicate process/closed notification for %q", processID)
			}
			closedSequence = params.Seq
		}
		// Notification senders can race even though seq assignment is
		// serialized. Treat seq, rather than wire arrival, as authoritative
		// and wait until every event through closed has arrived.
		if closedSequence != 0 && uint64(len(seenSequences)) == closedSequence {
			return events
		}
	}
}

type observedProcessOutput struct {
	stdout        []byte
	stderr        []byte
	pty           []byte
	lastOutputSeq uint64
	closedSeq     uint64
}

func assertCompletedProcessEvents(t *testing.T, events []observedProcessEvent, wantExitCode int) observedProcessOutput {
	t.Helper()
	observed, exitCode := inspectTerminalProcessEvents(t, events)
	if exitCode != wantExitCode {
		t.Fatalf(
			"process exit code = %d, want %d; stdout=%q stderr=%q pty=%q",
			exitCode,
			wantExitCode,
			observed.stdout,
			observed.stderr,
			observed.pty,
		)
	}
	return observed
}

func assertTerminalProcessEvents(t *testing.T, events []observedProcessEvent) observedProcessOutput {
	t.Helper()
	observed, _ := inspectTerminalProcessEvents(t, events)
	return observed
}

func inspectTerminalProcessEvents(t *testing.T, events []observedProcessEvent) (observedProcessOutput, int) {
	t.Helper()
	if len(events) < 2 {
		t.Fatalf("process emitted only %d events, want exited and closed", len(events))
	}
	ordered := append([]observedProcessEvent(nil), events...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].seq < ordered[j].seq })
	var observed observedProcessOutput
	exitCount := 0
	closedCount := 0
	exitCode := 0
	for index, event := range ordered {
		wantSeq := uint64(index + 1)
		if event.seq != wantSeq {
			t.Fatalf("process event sequence = %d at sorted index %d, want %d (events=%+v)", event.seq, index, wantSeq, ordered)
		}
		switch event.method {
		case "process/output":
			observed.lastOutputSeq = event.seq
			switch event.stream {
			case "stdout":
				observed.stdout = append(observed.stdout, event.chunk...)
			case "stderr":
				observed.stderr = append(observed.stderr, event.chunk...)
			case "pty":
				observed.pty = append(observed.pty, event.chunk...)
			default:
				t.Fatalf("process/output stream = %q", event.stream)
			}
		case "process/exited":
			exitCount++
			if event.exitCode == nil {
				t.Fatal("process/exited omitted exitCode")
			}
			exitCode = *event.exitCode
			if event.sandboxDenied == nil || *event.sandboxDenied {
				t.Fatalf("process/exited sandboxDenied = %v, want explicit false", event.sandboxDenied)
			}
		case "process/closed":
			closedCount++
			observed.closedSeq = event.seq
			if index != len(ordered)-1 {
				t.Fatalf("process/closed seq %d is not the final process event", event.seq)
			}
		default:
			t.Fatalf("unexpected process event %q", event.method)
		}
	}
	if exitCount != 1 || closedCount != 1 {
		t.Fatalf("terminal event counts: exited=%d closed=%d", exitCount, closedCount)
	}
	return observed, exitCode
}

type processReadResponse struct {
	Chunks []struct {
		Seq    uint64 `json:"seq"`
		Stream string `json:"stream"`
		Chunk  string `json:"chunk"`
	} `json:"chunks"`
	NextSeq       uint64  `json:"nextSeq"`
	Exited        bool    `json:"exited"`
	ExitCode      *int    `json:"exitCode"`
	Closed        bool    `json:"closed"`
	Failure       *string `json:"failure"`
	SandboxDenied bool    `json:"sandboxDenied"`
}

func decodeProcessRead(t *testing.T, message codexwire.Message) processReadResponse {
	t.Helper()
	var response processReadResponse
	mustDecodeResult(t, message, &response)
	return response
}

func aggregateReadChunks(t *testing.T, chunks []struct {
	Seq    uint64 `json:"seq"`
	Stream string `json:"stream"`
	Chunk  string `json:"chunk"`
}) observedProcessOutput {
	t.Helper()
	ordered := append([]struct {
		Seq    uint64 `json:"seq"`
		Stream string `json:"stream"`
		Chunk  string `json:"chunk"`
	}(nil), chunks...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Seq < ordered[j].Seq })
	var observed observedProcessOutput
	for _, chunk := range ordered {
		decoded, err := base64.StdEncoding.DecodeString(chunk.Chunk)
		if err != nil {
			t.Fatalf("decode process/read chunk: %v", err)
		}
		switch chunk.Stream {
		case "stdout":
			observed.stdout = append(observed.stdout, decoded...)
		case "stderr":
			observed.stderr = append(observed.stderr, decoded...)
		case "pty":
			observed.pty = append(observed.pty, decoded...)
		default:
			t.Fatalf("process/read stream = %q", chunk.Stream)
		}
	}
	return observed
}

func startE05Origin(t *testing.T) (string, *atomic.Int64) {
	t.Helper()
	hits := &atomic.Int64{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		hits.Add(1)
		if request.Method != http.MethodGet || request.URL.Path != "/e05" {
			http.Error(writer, "unexpected E05 origin request", http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(writer, execChildNetworkOriginBody)
	}))
	t.Cleanup(server.Close)
	return server.URL + "/e05", hits
}

func e05ExecStartParams(
	t *testing.T,
	paths livePaths,
	processID string,
	targetURL string,
	policyDecisionTimeoutMS int,
	extraEnvironment map[string]string,
) map[string]any {
	t.Helper()
	environment := map[string]string{execChildNetworkTargetEnv: targetURL}
	for name, value := range extraEnvironment {
		environment[name] = value
	}
	params := execStartParams(t, paths, processID, "network-http", false, environment)
	params["networkProxy"] = map[string]any{
		"proxy": map[string]any{
			"enabled":                        true,
			"enableSocks5":                   false,
			"enableSocks5Udp":                false,
			"allowUpstreamProxy":             false,
			"dangerouslyAllowAllUnixSockets": false,
			"mode":                           "full",
			"domains":                        nil,
			"unixSockets":                    nil,
			"allowLocalBinding":              true,
		},
		"environmentId":           "e05-environment",
		"executionId":             "e05-execution",
		"policyDecisionTimeoutMs": policyDecisionTimeoutMS,
	}
	return params
}

type e05NetworkPolicyRequestParams struct {
	ProcessID string `json:"processId"`
	Request   struct {
		Protocol string `json:"protocol"`
		Host     string `json:"host"`
		Port     uint16 `json:"port"`
	} `json:"request"`
}

func assertE05NetworkPolicyRequest(t *testing.T, message codexwire.Message, processID, targetURL string) {
	t.Helper()
	if len(message.ID) == 0 {
		t.Fatal("network/policyRequest omitted its reverse request id")
	}
	var params e05NetworkPolicyRequestParams
	if err := message.DecodeParams(&params); err != nil {
		t.Fatal(err)
	}
	target, err := url.Parse(targetURL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.ParseUint(target.Port(), 10, 16)
	if err != nil {
		t.Fatalf("parse E05 target port: %v", err)
	}
	if params.ProcessID != processID || params.Request.Protocol != "http" ||
		params.Request.Host != target.Hostname() || params.Request.Port != uint16(port) {
		t.Fatalf(
			"network/policyRequest params = %+v, want process=%q http://%s:%d",
			params,
			processID,
			target.Hostname(),
			port,
		)
	}
}

func e05ReferenceClientReply(request codexwire.Message, decisionType, reason string) map[string]any {
	if request.Method != "network/policyRequest" {
		return e05ReverseRPCError(request, -32601, "unsupported exec-server reverse request")
	}
	if !validE05NetworkPolicyRequest(request.Params) {
		return e05NetworkPolicyDecisionReply(request, "deny", "not_allowed")
	}
	if decisionType == "allow" && reason == "" {
		return e05NetworkPolicyDecisionReply(request, decisionType, reason)
	}
	if (decisionType == "deny" || decisionType == "ask") && validE05PolicyReason(reason) {
		return e05NetworkPolicyDecisionReply(request, decisionType, reason)
	}
	return e05NetworkPolicyDecisionReply(request, "deny", "not_allowed")
}

func validE05NetworkPolicyRequest(raw json.RawMessage) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var params e05NetworkPolicyRequestParams
	if err := decoder.Decode(&params); err != nil {
		return false
	}
	if len(params.ProcessID) == 0 || len(params.ProcessID) > 256 ||
		len(params.Request.Host) == 0 || len(params.Request.Host) > 253 ||
		params.Request.Port == 0 {
		return false
	}
	if strings.IndexFunc(params.Request.Host, func(character rune) bool {
		return unicode.IsControl(character) || unicode.IsSpace(character)
	}) != -1 {
		return false
	}
	switch params.Request.Protocol {
	case "http", "https_connect", "socks5_tcp", "socks5_udp":
		return true
	default:
		return false
	}
}

func validE05PolicyReason(reason string) bool {
	return reason != "" && len(reason) <= 1024 && strings.IndexFunc(reason, unicode.IsControl) == -1
}

func e05NetworkPolicyDecisionReply(request codexwire.Message, decisionType, reason string) map[string]any {
	decision := map[string]any{"type": decisionType}
	switch decisionType {
	case "allow":
	case "deny", "ask":
		decision["reason"] = reason
	default:
		decision = map[string]any{"type": "deny", "reason": "not_allowed"}
	}
	return map[string]any{
		"id":     request.ID,
		"result": map[string]any{"decision": decision},
	}
}

func e05ReverseRPCError(request codexwire.Message, code int, message string) map[string]any {
	return map[string]any{
		"id": request.ID,
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	}
}

func assertE05NetworkProcessResult(t *testing.T, collector *rpcCollector, processID string, wantStatus int) string {
	t.Helper()
	events := collector.processEventsUntilClosed(t, processID)
	observed := assertCompletedProcessEvents(t, events, 0)
	if len(observed.stderr) != 0 || len(observed.pty) != 0 {
		t.Fatalf("network child emitted unexpected output: stderr=%q pty=%q", observed.stderr, observed.pty)
	}
	prefix := fmt.Sprintf("status=%d\n", wantStatus)
	if !bytes.HasPrefix(observed.stdout, []byte(prefix)) {
		t.Fatalf("network child stdout = %q, want prefix %q", observed.stdout, prefix)
	}
	return string(observed.stdout[len(prefix):])
}

func execStartParams(t *testing.T, paths livePaths, processID, mode string, pipeStdin bool, extraEnvironment map[string]string) map[string]any {
	t.Helper()
	binary := os.Args[0]
	if !filepath.IsAbs(binary) {
		absolute, err := filepath.Abs(binary)
		if err != nil {
			t.Fatalf("resolve test helper binary: %v", err)
		}
		binary = absolute
	}
	environment := map[string]string{execChildModeEnvironment: mode}
	for name, value := range extraEnvironment {
		environment[name] = value
	}
	argv := []string{binary}
	var arg0 any
	if mode == "output" {
		argv = append(argv, execChildArgument)
		arg0 = execChildArg0
		canonicalCWD, err := filepath.EvalSymlinks(paths.cwd)
		if err != nil {
			t.Fatalf("canonicalize expected child cwd: %v", err)
		}
		environment[execChildExpectedCWDEnv] = canonicalCWD
	}
	return map[string]any{
		"processId":             processID,
		"argv":                  argv,
		"cwd":                   localFileURI(t, paths.cwd),
		"env":                   environment,
		"tty":                   false,
		"pipeStdin":             pipeStdin,
		"arg0":                  arg0,
		"sandbox":               nil,
		"enforceManagedNetwork": false,
	}
}

func localFileURI(t *testing.T, path string) string {
	t.Helper()
	if !filepath.IsAbs(path) {
		t.Fatalf("file URI path must be absolute: %q", path)
	}
	return (&url.URL{Scheme: "file", Path: path}).String()
}

func assertWriteStatus(t *testing.T, message codexwire.Message, want string) {
	t.Helper()
	var response struct {
		Status string `json:"status"`
	}
	mustDecodeResult(t, message, &response)
	if response.Status != want {
		t.Fatalf("process/write status = %q, want %q", response.Status, want)
	}
}

func assertTerminateRunning(t *testing.T, message codexwire.Message, want bool) {
	t.Helper()
	var response struct {
		Running bool `json:"running"`
	}
	mustDecodeResult(t, message, &response)
	if response.Running != want {
		t.Fatalf("process/terminate running = %t, want %t", response.Running, want)
	}
}

func assertEmptyObjectResult(t *testing.T, message codexwire.Message) {
	t.Helper()
	var response map[string]any
	mustDecodeResult(t, message, &response)
	if len(response) != 0 {
		t.Fatalf("RPC result = %+v, want empty object", response)
	}
}

type fileBlock struct {
	chunk []byte
	eof   bool
}

func readFileBlock(t *testing.T, process *codexprocess.Process, collector *rpcCollector, id int, handleID string, offset uint64, length int) fileBlock {
	t.Helper()
	sendRPC(t, process, map[string]any{
		"id":     id,
		"method": "fs/readBlock",
		"params": map[string]any{"handleId": handleID, "offset": offset, "len": length},
	})
	var response struct {
		Chunk string `json:"chunk"`
		EOF   bool   `json:"eof"`
	}
	mustDecodeResult(t, collector.response(t, strconv.Itoa(id)), &response)
	chunk, err := base64.StdEncoding.DecodeString(response.Chunk)
	if err != nil {
		t.Fatalf("decode fs/readBlock chunk: %v", err)
	}
	return fileBlock{chunk: chunk, eof: response.EOF}
}

type liveExecAdapterTransport struct {
	process *codexprocess.Process
}

func (transport *liveExecAdapterTransport) Send(value any) error {
	return transport.process.Peer.Send(value)
}

func (transport *liveExecAdapterTransport) Receive(ctx context.Context) (codexwire.Message, error) {
	return transport.process.Peer.Receive(ctx)
}

func (transport *liveExecAdapterTransport) CloseStdin() error {
	return transport.process.CloseStdin()
}

func (transport *liveExecAdapterTransport) Wait(ctx context.Context) error {
	return transport.process.Wait(ctx)
}

func (transport *liveExecAdapterTransport) Kill() error {
	return transport.process.Kill()
}

func newLiveExecAdapter(
	t *testing.T,
	process *codexprocess.Process,
	instanceID string,
	managedPID *atomic.Int64,
) *execadapter.Instance {
	t.Helper()
	instance, err := execadapter.New(
		&liveExecAdapterTransport{process: process},
		instanceID,
		execadapter.Options{
			ClientName:    "agentserver-v2-dedicated-instance-gate",
			CleanupGrace:  500 * time.Millisecond,
			ShutdownGrace: 5 * time.Second,
			EventBuffer:   64,
			MaxEventBytes: 128 * 1024,
			Limits:        characterizedE10AgentxLimits(),
			VerifyTreeEmpty: func(ctx context.Context, _ string) error {
				ticker := time.NewTicker(10 * time.Millisecond)
				defer ticker.Stop()
				for {
					pid := managedPID.Load()
					if pid > 0 {
						alive, err := processIsAlive(int(pid))
						if err != nil {
							return fmt.Errorf("probe managed process %d: %w", pid, err)
						}
						if !alive {
							return nil
						}
					}
					select {
					case <-ctx.Done():
						return fmt.Errorf("managed process tree was not confirmed empty: %w", ctx.Err())
					case <-ticker.C:
					}
				}
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { instance.Abort(errors.New("dedicated adapter test cleanup")) })
	return instance
}

func waitForChildPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		contents, err := os.ReadFile(path)
		if err == nil {
			pid, parseErr := strconv.Atoi(string(contents))
			if parseErr != nil || pid <= 0 {
				t.Fatalf("invalid child pid file %q: %q", path, contents)
			}
			return pid
		}
		if !os.IsNotExist(err) {
			t.Fatalf("read child pid file: %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("child did not create pid file %q", path)
		}
		<-ticker.C
	}
}

func waitForProcessGone(t *testing.T, pid int) {
	t.Helper()
	if !processLivenessProbeSupported {
		return
	}
	deadline := time.Now().Add(5 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		alive, err := processIsAlive(pid)
		if err != nil {
			t.Fatalf("probe child %d: %v", pid, err)
		}
		if !alive {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("managed child %d survived its terminal lifecycle", pid)
		}
		<-ticker.C
	}
}

func assertProcessAlive(t *testing.T, pid int, phase string) {
	t.Helper()
	alive, err := processIsAlive(pid)
	if err != nil {
		t.Fatalf("probe child %d %s: %v", pid, phase, err)
	}
	if !alive {
		t.Fatalf("child %d is not alive %s", pid, phase)
	}
}

func cleanupChildProcess(t *testing.T, pid int) func() {
	t.Helper()
	cleanupEnabled := true
	t.Cleanup(func() {
		if !cleanupEnabled {
			return
		}
		child, err := os.FindProcess(pid)
		if err == nil {
			_ = child.Kill()
		}
	})
	return func() {
		cleanupEnabled = false
	}
}
