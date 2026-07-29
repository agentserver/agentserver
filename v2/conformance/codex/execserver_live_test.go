package codex_test

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/conformance/codex/internal/codexprocess"
	"github.com/agentserver/agentserver/v2/internal/codexwire"
)

const (
	execChildModeEnvironment    = "AGENTSERVER_EXEC_CHILD_MODE"
	execChildPIDFileEnvironment = "AGENTSERVER_EXEC_CHILD_PID_FILE"
	execChildOutputStdout       = "stdout:deterministic\n"
	execChildOutputStderr       = "stderr:deterministic\n"
	execChildEchoInput          = "deterministic-input\n"
	execChildReadyOutput        = "ready\n"
	execChildInterruptedOutput  = "interrupted\n"
	execChildPTYOutput          = "tty:stdout|tty:stderr|"
	execChildArgument           = "deterministic-argument"
	execChildArg0               = "agentserver-deterministic-arg0"
	execChildExpectedCWDEnv     = "AGENTSERVER_EXEC_CHILD_EXPECTED_CWD"
	execChildInterruptExitCode  = 42
	execChildRootCrashExitCode  = 43
	execChildLargeOutputBytes   = (1 << 20) + (64 << 10)
	execServerRetainedOutputMax = 1 << 20
)

// TestMain lets a live exec-server launch the already-built Go test binary as
// a deterministic child. The helper path bypasses the testing harness so its
// stdout and stderr contain only bytes intentionally emitted by the probe.
func TestMain(m *testing.M) {
	if mode, helper := os.LookupEnv(execChildModeEnvironment); helper {
		os.Exit(runExecChild(mode))
	}
	os.Exit(m.Run())
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
	default:
		return reportExecChildError(fmt.Errorf("unknown helper mode %q", mode))
	}
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
	t.Log("E03 remains blocked: process/signal returns indistinguishable success for missing, delivered, and already-exited targets")

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

	sendRPC(t, process, map[string]any{
		"id":     3,
		"method": "fs/readFile",
		"params": map[string]any{"path": filePath, "sandbox": nil},
	})
	mustRPCError(t, collector.response(t, "3"))

	sendRPC(t, process, map[string]any{
		"id":     4,
		"method": "fs/open",
		"params": map[string]any{"handleId": "handle-1", "path": pathURI, "sandbox": nil},
	})
	var opened struct {
		HandleID string `json:"handleId"`
	}
	mustDecodeResult(t, collector.response(t, "4"), &opened)
	if opened.HandleID != "handle-1" {
		t.Fatalf("fs/open handleId = %q, want handle-1", opened.HandleID)
	}

	first := readFileBlock(t, process, collector, 5, "handle-1", 0, 3)
	if !bytes.Equal(first.chunk, contents[:3]) || first.eof {
		t.Fatalf("first fs/readBlock = %+v, want first three bytes and eof=false", first)
	}
	second := readFileBlock(t, process, collector, 6, "handle-1", 3, 64)
	if !bytes.Equal(second.chunk, contents[3:]) || !second.eof {
		t.Fatalf("second fs/readBlock = %+v, want remaining bytes and eof=true", second)
	}

	sendRPC(t, process, map[string]any{
		"id":     7,
		"method": "fs/close",
		"params": map[string]any{"handleId": "handle-1"},
	})
	var closed map[string]any
	mustDecodeResult(t, collector.response(t, "7"), &closed)
	if len(closed) != 0 {
		t.Fatalf("fs/close result = %+v, want empty object", closed)
	}

	sendRPC(t, process, map[string]any{
		"id":     8,
		"method": "fs/readBlock",
		"params": map[string]any{"handleId": "handle-1", "offset": 0, "len": 1},
	})
	mustRPCError(t, collector.response(t, "8"))

	uncanonicalPath := paths.cwd + string(os.PathSeparator) + "nested" + string(os.PathSeparator) + ".." + string(os.PathSeparator) + "data.bin"
	sendRPC(t, process, map[string]any{
		"id":     9,
		"method": "fs/canonicalize",
		"params": map[string]any{"path": localFileURI(t, uncanonicalPath), "sandbox": nil},
	})
	var canonicalized struct {
		Path string `json:"path"`
	}
	mustDecodeResult(t, collector.response(t, "9"), &canonicalized)
	assertFileURIPath(t, canonicalized.Path, filePath)

	closeAndWait(t, process)
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
	t.Log("E07 remains blocked: root exit strands descendants until the entire stdio exec-server connection shuts down")
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
	if len(replayed.stdout) > execServerRetainedOutputMax || len(replayed.stdout) <= execServerRetainedOutputMax-8_192 {
		t.Fatalf("retained output bytes = %d, want (%d, %d]", len(replayed.stdout), execServerRetainedOutputMax-8_192, execServerRetainedOutputMax)
	}
	wantSuffix := observed.stdout[len(observed.stdout)-len(replayed.stdout):]
	if !bytes.Equal(replayed.stdout, wantSuffix) {
		t.Fatal("retained process/read output is not the exact suffix of streamed output")
	}

	closeAndWait(t, process)
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
