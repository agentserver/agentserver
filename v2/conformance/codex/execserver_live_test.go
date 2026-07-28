package codex_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/url"
	"os"
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
		if _, inherited := os.LookupEnv("HOME"); inherited {
			_, _ = io.WriteString(os.Stderr, "unexpected inherited HOME\n")
			return 90
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
	default:
		return reportExecChildError(fmt.Errorf("unknown helper mode %q", mode))
	}
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

type rpcCollector struct {
	process       *codexprocess.Process
	responses     map[string]codexwire.Message
	notifications []codexwire.Message
}

func newRPCCollector(process *codexprocess.Process) *rpcCollector {
	return &rpcCollector{process: process, responses: make(map[string]codexwire.Message)}
}

func (c *rpcCollector) response(t *testing.T, id string) codexwire.Message {
	t.Helper()
	if response, exists := c.responses[id]; exists {
		delete(c.responses, id)
		return response
	}
	for {
		message := c.receive(t)
		switch message.Kind {
		case codexwire.KindResponse, codexwire.KindError:
			messageID := string(message.ID)
			if messageID == id {
				return message
			}
			if _, duplicate := c.responses[messageID]; duplicate {
				t.Fatalf("duplicate response id %s", messageID)
			}
			c.responses[messageID] = message
		case codexwire.KindNotification:
			c.notifications = append(c.notifications, message)
		case codexwire.KindRequest:
			t.Fatalf("unexpected exec-server reverse request %q while waiting for response %s", message.Method, id)
		default:
			t.Fatalf("unexpected Codex wire message kind %s", message.Kind)
		}
	}
}

func (c *rpcCollector) nextNotification(t *testing.T) codexwire.Message {
	t.Helper()
	if len(c.notifications) != 0 {
		message := c.notifications[0]
		c.notifications = c.notifications[1:]
		return message
	}
	for {
		message := c.receive(t)
		switch message.Kind {
		case codexwire.KindNotification:
			return message
		case codexwire.KindResponse, codexwire.KindError:
			messageID := string(message.ID)
			if _, duplicate := c.responses[messageID]; duplicate {
				t.Fatalf("duplicate response id %s", messageID)
			}
			c.responses[messageID] = message
		case codexwire.KindRequest:
			t.Fatalf("unexpected exec-server reverse request %q while waiting for notification", message.Method)
		default:
			t.Fatalf("unexpected Codex wire message kind %s", message.Kind)
		}
	}
}

func (c *rpcCollector) receive(t *testing.T) codexwire.Message {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), liveProbeTimeout)
	defer cancel()
	message, err := c.process.Peer.Receive(ctx)
	if err != nil {
		stderr, truncated := c.process.Stderr()
		t.Fatalf("receive exec-server message: %v (stderr_truncated=%t)\nstderr: %s", err, truncated, stderr)
	}
	return message
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
	lastOutputSeq uint64
	closedSeq     uint64
}

func assertCompletedProcessEvents(t *testing.T, events []observedProcessEvent, wantExitCode int) observedProcessOutput {
	t.Helper()
	observed, exitCode := inspectTerminalProcessEvents(t, events)
	if exitCode != wantExitCode {
		t.Fatalf("process exit code = %d, want %d", exitCode, wantExitCode)
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
			default:
				t.Fatalf("non-tty process/output stream = %q", event.stream)
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
	return map[string]any{
		"processId":             processID,
		"argv":                  []string{binary},
		"cwd":                   localFileURI(t, paths.cwd),
		"env":                   environment,
		"tty":                   false,
		"pipeStdin":             pipeStdin,
		"arg0":                  nil,
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

func sendRPC(t *testing.T, process *codexprocess.Process, request any) {
	t.Helper()
	if err := process.Peer.Send(request); err != nil {
		t.Fatalf("send exec-server request: %v", err)
	}
}

func mustDecodeResult(t *testing.T, message codexwire.Message, destination any) {
	t.Helper()
	if message.Kind == codexwire.KindError {
		t.Fatalf("exec-server returned error %d: %s", message.Error.Code, message.Error.Message)
	}
	if err := message.DecodeResult(destination); err != nil {
		t.Fatal(err)
	}
}

func mustRPCError(t *testing.T, message codexwire.Message) {
	t.Helper()
	if message.Kind != codexwire.KindError || message.Error == nil || message.Error.Message == "" {
		t.Fatalf("message kind = %s, want non-empty RPC error", message.Kind)
	}
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
