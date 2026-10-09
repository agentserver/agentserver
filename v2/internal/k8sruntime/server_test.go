package k8sruntime

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/executionbackend"
	"github.com/agentserver/agentserver/v2/internal/sandboxcontract"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	s, err := New(Config{PodUID: "pod-1", GatewayIdentity: "spiffe://agentserver.test/gateway", Workspace: "/workspace", Bwrap: "/usr/local/bin/bwrap"})
	if err != nil {
		t.Fatal(err)
	}
	s.ready = true
	s.config.Workspace = t.TempDir()
	// Protocol-only unit tests use a test-local launcher. Production always
	// uses sandboxCommand; actual containment is tested in Linux live tests.
	s.command = func(r sandboxcontract.RunCommandRequest) (*exec.Cmd, error) {
		cmd := exec.Command(r.Executable, r.Arguments...)
		cmd.Dir = s.config.Workspace
		cmd.Env = []string{"PATH=/usr/bin:/bin"}
		for k, v := range r.Environment {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
		return cmd, nil
	}
	t.Cleanup(s.Close)
	b := Binding{Identity: s.identity, Session: sandboxcontract.SessionIdentity{WorkspaceID: "workspace-1", SessionID: "session-1", EnvironmentID: "env-1"}, Ref: sandboxcontract.SandboxRef{SandboxID: "sandbox-1", TargetGeneration: 1, BackendKind: executionbackend.KindKubernetes}}
	w := call(t, s, http.MethodPost, BindPath, b, true)
	if w.Code != 200 {
		t.Fatalf("bind %d %s", w.Code, w.Body.String())
	}
	return s
}

func call(t *testing.T, s *Server, method, path string, body any, auth bool) *httptest.ResponseRecorder {
	t.Helper()
	var b []byte
	var err error
	if body != nil {
		b, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(b))
	r.Header.Set(PodHeader, s.identity.PodUID)
	r.Header.Set(BootHeader, s.identity.BootID)
	if auth {
		uri, _ := url.Parse(s.config.GatewayIdentity)
		cert := &x509.Certificate{URIs: []*url.URL{uri}}
		r.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}, VerifiedChains: [][]*x509.Certificate{{cert}}}
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func testCommand(s *Server) sandboxcontract.RunCommandRequest {
	return sandboxcontract.RunCommandRequest{Profile: sandboxcontract.ProfileV1, RequestID: "request-1", Identity: sandboxcontract.OperationIdentity{Session: s.bound.Session, RunID: "run-1", RunAttemptID: "attempt-1", RunAttemptGeneration: 1, ExecutionID: "exec-1", OperationID: "op-1", MutationKey: "mutation-1"}, Ref: s.bound.Ref, ProcessID: "process-1", Executable: "/bin/sh", Arguments: []string{"-c", "printf hello; printf problem >&2"}, WorkingDirectory: s.config.Workspace, TimeoutMillis: 1000, OutputLimitBytes: 4096}
}

func frames(t *testing.T, w *httptest.ResponseRecorder) []sandboxcontract.OperationFrame {
	t.Helper()
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	d := json.NewDecoder(w.Body)
	var result []sandboxcontract.OperationFrame
	for {
		var f sandboxcontract.OperationFrame
		err := d.Decode(&f)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if err = f.Validate(); err != nil {
			t.Fatal(err)
		}
		result = append(result, f)
	}
	if len(result) < 2 || result[0].Acknowledgement == nil || result[len(result)-1].Terminal == nil {
		t.Fatalf("incomplete frames: %+v", result)
	}
	return result
}

func TestRuntimeStreamsAndRejectsReplay(t *testing.T) {
	s := testServer(t)
	cmd := testCommand(s)
	path, _ := sandboxcontract.RunCommandPath(cmd.Ref.SandboxID)
	fs := frames(t, call(t, s, http.MethodPost, path, cmd, true))
	var stdout, stderr strings.Builder
	seq := uint64(0)
	for _, f := range fs {
		if f.Identity != cmd.Identity || f.Ref != cmd.Ref {
			t.Fatal("identity changed")
		}
		if f.Event != nil {
			seq++
			if f.Event.Sequence != seq {
				t.Fatal("sequence gap")
			}
			switch f.Event.Kind {
			case executionbackend.EventStdout:
				stdout.Write(f.Event.Data)
			case executionbackend.EventStderr:
				stderr.Write(f.Event.Data)
			}
		}
	}
	terminal := fs[len(fs)-1].Terminal
	if stdout.String() != "hello" || stderr.String() != "problem" || terminal.Status != executionbackend.TerminalSucceeded || terminal.ExitCode == nil || *terminal.ExitCode != 0 || !terminal.OutputComplete {
		t.Fatalf("terminal/output: %q %q %+v", stdout.String(), stderr.String(), terminal)
	}
	if w := call(t, s, http.MethodPost, path, cmd, true); w.Code != 409 {
		t.Fatal("operation replay accepted")
	}
	cmd.Identity.OperationID = "op-2"
	cmd.Identity.MutationKey = "mutation-2"
	if w := call(t, s, http.MethodPost, path, cmd, true); w.Code != 409 {
		t.Fatal("process ID reused")
	}
	p := s.processes[cmd.ProcessID]
	if p.cmd != nil {
		t.Fatal("finished process retained launcher/environment")
	}
}

func TestRuntimeRejectsUnauthenticatedAndCrossSession(t *testing.T) {
	s := testServer(t)
	cmd := testCommand(s)
	path, _ := sandboxcontract.RunCommandPath(cmd.Ref.SandboxID)
	if w := call(t, s, http.MethodPost, path, cmd, false); w.Code != 403 {
		t.Fatal("unauthenticated execution allowed")
	}
	cmd.Identity.Session.SessionID = "other"
	if w := call(t, s, http.MethodPost, path, cmd, true); w.Code != 409 {
		t.Fatal("foreign session executed")
	}
	b := *s.bound
	b.Session.SessionID = "other"
	if w := call(t, s, http.MethodPost, BindPath, b, true); w.Code != 409 {
		t.Fatal("runtime rebound to another session")
	}
	if len(s.processes) != 0 {
		t.Fatal("denied request started a process")
	}
}

func TestRuntimeAllowsCredentialFreeShellPipelines(t *testing.T) {
	s := testServer(t)
	request := testCommand(s)
	request.Arguments = []string{"-c", "printf managed-cli | tr a-z A-Z"}
	path, _ := sandboxcontract.RunCommandPath(request.Ref.SandboxID)
	result := frames(t, call(t, s, http.MethodPost, path, request, true))
	var stdout strings.Builder
	for _, frame := range result {
		if frame.Event != nil && frame.Event.Kind == executionbackend.EventStdout {
			stdout.Write(frame.Event.Data)
		}
	}
	terminal := result[len(result)-1].Terminal
	if terminal.Status != executionbackend.TerminalSucceeded || stdout.String() != "MANAGED-CLI" {
		t.Fatalf("pipeline = %q, terminal=%+v", stdout.String(), terminal)
	}
}

func TestRuntimeRejectsStaleBootAndRequestShape(t *testing.T) {
	s := testServer(t)
	cmd := testCommand(s)
	path, _ := sandboxcontract.RunCommandPath(cmd.Ref.SandboxID)
	other := testServer(t)
	other.config = s.config
	other.bound = s.bound
	b := *s.bound
	if w := call(t, other, http.MethodPost, BindPath, b, true); w.Code != 400 {
		t.Fatal("stale boot binding accepted")
	}
	if w := call(t, s, http.MethodPost, path+"?override=true", cmd, true); w.Code != 400 {
		t.Fatal("query override accepted")
	}
	if w := call(t, s, http.MethodGet, "/readyz", nil, false); w.Code != 200 {
		t.Fatal("health probe requires execution credentials")
	}
	if w := call(t, s, http.MethodGet, IdentityPath, nil, false); w.Code != 403 {
		t.Fatal("identity allowed anonymous")
	}
}

func TestRuntimeFileReadRejectsSymlinkEscape(t *testing.T) {
	s := testServer(t)
	cmd := testCommand(s)
	if err := os.WriteFile(filepath.Join(s.config.Workspace, "file"), []byte("0123456789"), 0600); err != nil {
		t.Fatal(err)
	}
	read := sandboxcontract.ReadFileRequest{Profile: sandboxcontract.ProfileV1, RequestID: "read-1", Identity: cmd.Identity, Ref: cmd.Ref, Path: filepath.Join(s.config.Workspace, "file"), Offset: 2, Limit: 3}
	path, _ := sandboxcontract.ReadFilePath(cmd.Ref.SandboxID)
	fs := frames(t, call(t, s, http.MethodPost, path, read, true))
	if len(fs) != 3 || string(fs[1].Event.Data) != "234" {
		t.Fatalf("offset/limit: %+v", fs)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(s.config.Workspace, "outside")); err != nil {
		t.Fatal(err)
	}
	read.Identity.OperationID = "read-2"
	read.Identity.MutationKey = "mutation-2"
	read.Path = filepath.Join(s.config.Workspace, "outside")
	readFrames := frames(t, call(t, s, http.MethodPost, path, read, true))
	if len(readFrames) != 2 || readFrames[1].Terminal == nil || readFrames[1].Terminal.Status != executionbackend.TerminalFailed || readFrames[1].Terminal.ReasonCode != "file_unavailable" {
		t.Fatalf("symlink read should be a terminal file failure: %+v", readFrames)
	}
}

func TestRuntimeOutputLimitIsNotSuccessful(t *testing.T) {
	s := testServer(t)
	cmd := testCommand(s)
	cmd.Arguments = []string{"-c", "printf 01234567890123456789"}
	cmd.OutputLimitBytes = 5
	path, _ := sandboxcontract.RunCommandPath(cmd.Ref.SandboxID)
	fs := frames(t, call(t, s, http.MethodPost, path, cmd, true))
	terminal := fs[len(fs)-1].Terminal
	if terminal.Status == executionbackend.TerminalSucceeded || terminal.OutputComplete || terminal.ReasonCode != "output_limit" {
		t.Fatalf("truncation hidden: %+v", terminal)
	}
}

func TestRuntimeTimeoutKillsProcess(t *testing.T) {
	s := testServer(t)
	cmd := testCommand(s)
	cmd.Arguments = []string{"-c", "exec sleep 20"}
	cmd.TimeoutMillis = 30
	path, _ := sandboxcontract.RunCommandPath(cmd.Ref.SandboxID)
	fs := frames(t, call(t, s, http.MethodPost, path, cmd, true))
	terminal := fs[len(fs)-1].Terminal
	if terminal.Status != executionbackend.TerminalCancelled || terminal.ReasonCode != "process_timeout" {
		t.Fatalf("timeout hidden: %+v", terminal)
	}
}
