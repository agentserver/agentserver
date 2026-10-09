// Package k8sruntime is the process/files data plane inside a Kubernetes
// Sandbox. It has no Kubernetes client, Core credentials, or TAE dependency.
package k8sruntime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/agentserver/agentserver/v2/internal/executionbackend"
	"github.com/agentserver/agentserver/v2/internal/repositorycheckout"
	"github.com/agentserver/agentserver/v2/internal/sandboxcontract"
)

const (
	IdentityPath    = "/internal/runtime/identity"
	BindPath        = "/internal/runtime/bind"
	PodHeader       = "X-Agentserver-Pod-Uid"
	BootHeader      = "X-Agentserver-Runtime-Boot-Id"
	maxRequestBytes = 256 * 1024
	maxOperations   = 4096
)

type Identity struct {
	PodUID string `json:"podUid"`
	BootID string `json:"bootId"`
}
type Binding struct {
	Identity Identity                        `json:"runtime"`
	Session  sandboxcontract.SessionIdentity `json:"session"`
	Ref      sandboxcontract.SandboxRef      `json:"ref"`
}

type Config struct {
	PodUID          string
	GatewayIdentity string
	Workspace       string
	Bwrap           string
	ProxyURL        string
	// Empty leaves repository preparation disabled. This is a dedicated
	// persistent session volume, never the model-visible /workspace itself.
	RepositoryStorage string
	repositoryTree    string
}

type Server struct {
	config        Config
	identity      Identity
	mu            sync.Mutex
	bound         *Binding
	ready         bool
	closed        bool
	ops           map[string]struct{}
	processes     map[string]*process
	command       func(sandboxcontract.RunCommandRequest) (*exec.Cmd, error)
	projectMu     sync.RWMutex
	checkout      *repositorycheckout.Manager
	projectID     string
	projectTree   string
	projectCommit string
	projectCancel context.CancelFunc // guarded by mu; cancelled during shutdown
}

type process struct {
	identity  sandboxcontract.OperationIdentity
	ref       sandboxcontract.SandboxRef
	cmd       *exec.Cmd
	done      chan struct{}
	mu        sync.Mutex
	finished  bool
	cancelled bool
}

func New(config Config) (*Server, error) {
	if config.ProxyURL != "" {
		u, err := url.Parse(config.ProxyURL)
		if err != nil || u.Scheme != "socks5h" || u.Hostname() == "" || u.Port() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(config.ProxyURL, "\x00\r\n") {
			return nil, errors.New("runtime proxy must be an operator-configured SOCKS5 DNS endpoint without credentials")
		}
	}
	if config.PodUID == "" || len(config.PodUID) > 128 || strings.ContainsAny(config.PodUID, "\x00\r\n") || !strings.HasPrefix(config.GatewayIdentity, "spiffe://") {
		return nil, errors.New("runtime Pod UID and gateway identity are required")
	}
	if config.Workspace != "/workspace" || !filepath.IsAbs(config.Bwrap) || filepath.Clean(config.Bwrap) != config.Bwrap {
		return nil, errors.New("runtime requires /workspace and an absolute bubblewrap path")
	}
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	s := &Server{config: config, identity: Identity{config.PodUID, hex.EncodeToString(nonce[:])}, ops: map[string]struct{}{}, processes: map[string]*process{}}
	if config.RepositoryStorage != "" {
		if config.RepositoryStorage != "/var/lib/agentserver/repositories" {
			return nil, errors.New("repository storage must use the dedicated session volume mount")
		}
		var err error
		s.checkout, err = repositorycheckout.New(config.RepositoryStorage, "/usr/bin/git")
		if err != nil {
			return nil, err
		}
	}
	s.command = func(r sandboxcontract.RunCommandRequest) (*exec.Cmd, error) {
		c := s.config
		c.repositoryTree = s.projectTree
		return sandboxCommand(c, r)
	}
	return s, nil
}

// Probe proves that this container can actually create the required nested
// process/filesystem boundary. There is no unsandboxed fallback on failure.
func (s *Server) Probe(ctx context.Context) error {
	cmd, err := s.command(sandboxcontract.RunCommandRequest{Executable: "/usr/bin/true", WorkingDirectory: s.config.Workspace, WorkspaceAccess: "read"})
	if err != nil {
		return err
	}
	err = cmd.Start()
	closeExtraFiles(cmd)
	if err != nil {
		return errors.New("runtime sandbox probe could not start")
	}
	finished := make(chan error, 1)
	go func() { finished <- cmd.Wait() }()
	select {
	case err = <-finished:
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		<-finished
		return ctx.Err()
	}
	if err != nil {
		return errors.New("runtime filesystem/process sandbox probe failed")
	}
	s.mu.Lock()
	s.ready = true
	s.mu.Unlock()
	return nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.RawQuery != "" || r.URL.RawPath != "" || r.URL.ForceQuery || r.URL.Fragment != "" {
		reject(w, "invalid_url", 400)
		return
	}
	// Health probes carry no credential or execution authority. The TLS server
	// verifies a client certificate when supplied; every other path requires it.
	if r.URL.Path == "/readyz" && r.Method == http.MethodGet {
		s.mu.Lock()
		ready := s.ready && !s.closed
		s.mu.Unlock()
		if !ready {
			reject(w, "not_ready", 503)
			return
		}
		writeJSON(w, map[string]string{"status": "ready"})
		return
	}
	if !s.authorized(r) {
		reject(w, "forbidden", 403)
		return
	}
	if r.URL.Path == IdentityPath && r.Method == http.MethodGet {
		writeJSON(w, s.identity)
		return
	}
	if r.Header.Get(PodHeader) != s.identity.PodUID || r.Header.Get(BootHeader) != s.identity.BootID {
		reject(w, "runtime_replaced", 409)
		return
	}
	if r.URL.Path == BindPath && r.Method == http.MethodPost {
		var b Binding
		if !decode(w, r, &b) {
			return
		}
		if b.Identity != s.identity || b.Session.Validate() != nil || b.Ref.Validate() != nil || b.Ref.Kind() != executionbackend.KindKubernetes {
			reject(w, "invalid_binding", 400)
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.closed || !s.ready {
			reject(w, "not_ready", 503)
			return
		}
		if s.bound != nil && *s.bound != b {
			reject(w, "binding_conflict", 409)
			return
		}
		s.bound = &b
		writeJSON(w, b)
		return
	}
	if r.Method != http.MethodPost {
		reject(w, "not_found", 404)
		return
	}
	switch {
	case r.URL.Path == PrepareRepositoryPath:
		s.prepareRepository(w, r)
	case r.URL.Path == RepositoryContextPath:
		s.repositoryContext(w, r)
	case strings.HasSuffix(r.URL.Path, "/commands:run"):
		var command sandboxcontract.RunCommandRequest
		if !decode(w, r, &command) {
			return
		}
		path, err := sandboxcontract.RunCommandPath(command.Ref.SandboxID)
		if err != nil || path != r.URL.Path || command.Validate(sandboxcontract.DefaultLimits()) != nil {
			reject(w, "invalid_command", 400)
			return
		}
		if !s.projectMu.TryRLock() {
			reject(w, "repository_busy", 409)
			return
		}
		defer s.projectMu.RUnlock()
		if !s.accept(command.Identity, command.Ref, command.ProcessID) {
			reject(w, "duplicate_or_fenced", 409)
			return
		}
		s.run(w, r, command)
	case strings.HasSuffix(r.URL.Path, ":signal"):
		var command sandboxcontract.SignalCommandRequest
		if !decode(w, r, &command) {
			return
		}
		path, err := sandboxcontract.SignalProcessPath(command.Ref.SandboxID, command.ProcessID)
		if err != nil || path != r.URL.Path || command.Validate(sandboxcontract.DefaultLimits()) != nil {
			reject(w, "invalid_signal", 400)
			return
		}
		if !s.accept(command.Identity, command.Ref, "") {
			reject(w, "duplicate_or_fenced", 409)
			return
		}
		s.signal(w, r, command)
	case strings.HasSuffix(r.URL.Path, "/files:read"):
		var command sandboxcontract.ReadFileRequest
		if !decode(w, r, &command) {
			return
		}
		path, err := sandboxcontract.ReadFilePath(command.Ref.SandboxID)
		if err != nil || path != r.URL.Path || command.Validate(sandboxcontract.DefaultLimits()) != nil {
			reject(w, "invalid_read", 400)
			return
		}
		if !s.projectMu.TryRLock() {
			reject(w, "repository_busy", 409)
			return
		}
		defer s.projectMu.RUnlock()
		if !s.accept(command.Identity, command.Ref, "") {
			reject(w, "duplicate_or_fenced", 409)
			return
		}
		s.read(w, r, command)
	default:
		reject(w, "not_found", 404)
	}
}

func (s *Server) authorized(r *http.Request) bool {
	if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 || len(r.TLS.PeerCertificates) == 0 {
		return false
	}
	leaf := r.TLS.PeerCertificates[0]
	return len(leaf.URIs) == 1 && leaf.URIs[0].String() == s.config.GatewayIdentity
}

func (s *Server) accept(id sandboxcontract.OperationIdentity, ref sandboxcontract.SandboxRef, processID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || !s.ready || s.bound == nil || s.bound.Session != id.Session || s.bound.Ref != ref || len(s.ops) >= maxOperations {
		return false
	}
	if _, ok := s.ops[id.OperationID]; ok {
		return false
	}
	if _, ok := s.ops["mutation/"+id.MutationKey]; ok {
		return false
	}
	if processID != "" {
		if _, ok := s.ops["process/"+processID]; ok {
			return false
		}
		s.ops["process/"+processID] = struct{}{}
	}
	s.ops[id.OperationID] = struct{}{}
	s.ops["mutation/"+id.MutationKey] = struct{}{}
	return true
}

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(dst) != nil {
		reject(w, "invalid_json", 400)
		return false
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		reject(w, "invalid_json", 400)
		return false
	}
	return true
}
func reject(w http.ResponseWriter, code string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(sandboxcontract.ErrorResponse{Code: code, Message: code, Outcome: string(executionbackend.OutcomeRejected)})
}
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

type stream struct {
	w   http.ResponseWriter
	id  sandboxcontract.OperationIdentity
	ref sandboxcontract.SandboxRef
	seq uint64
}

func newStream(w http.ResponseWriter, id sandboxcontract.OperationIdentity, ref sandboxcontract.SandboxRef) *stream {
	w.Header().Set("Content-Type", "application/x-ndjson")
	return &stream{w: w, id: id, ref: ref}
}
func (s *stream) frame(f sandboxcontract.OperationFrame) error {
	f.Profile = sandboxcontract.ProfileV1
	f.Identity = s.id
	f.Ref = s.ref
	controller := http.NewResponseController(s.w)
	if err := controller.SetWriteDeadline(time.Now().Add(15 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return err
	}
	// Bound only Encode/Flush, not the time until the process next emits output.
	// HTTP/2 actively resets the stream when this deadline expires, even while
	// no write is in progress. Leaving it armed after ACK kills quiet commands.
	defer controller.SetWriteDeadline(time.Time{})
	if err := json.NewEncoder(s.w).Encode(f); err != nil {
		return err
	}
	return controller.Flush()
}
func (s *stream) ack(handle string) error {
	return s.frame(sandboxcontract.OperationFrame{Type: sandboxcontract.OperationFrameAcknowledgement, Acknowledgement: &executionbackend.Acknowledgement{ProviderOperationID: handle, AcceptedAt: time.Now().UTC()}})
}
func (s *stream) event(kind executionbackend.EventKind, b []byte) error {
	s.seq++
	return s.frame(sandboxcontract.OperationFrame{Type: sandboxcontract.OperationFrameEvent, Event: &executionbackend.Event{Sequence: s.seq, Kind: kind, Data: b}})
}
func (s *stream) terminal(result executionbackend.TerminalResult) {
	result.CompletedAt = time.Now().UTC()
	_ = s.frame(sandboxcontract.OperationFrame{Type: sandboxcontract.OperationFrameTerminal, Terminal: &result})
}

type output struct {
	kind executionbackend.EventKind
	data []byte
}
type outputBuffer struct {
	mu        sync.Mutex
	remaining int64
	truncated bool
	events    chan output
	ctx       context.Context
	kill      func()
}
type channelWriter struct {
	b    *outputBuffer
	kind executionbackend.EventKind
}

func (w channelWriter) Write(data []byte) (int, error) {
	w.b.mu.Lock()
	defer w.b.mu.Unlock()
	written := 0
	for len(data) > 0 {
		if w.b.remaining == 0 {
			w.b.truncated = true
			w.b.kill()
			return written, errors.New("output_limit")
		}
		n := min(len(data), 16*1024, int(w.b.remaining))
		b := append([]byte(nil), data[:n]...)
		select {
		case w.b.events <- output{w.kind, b}:
		case <-w.b.ctx.Done():
			return written, w.b.ctx.Err()
		}
		w.b.remaining -= int64(n)
		written += n
		data = data[n:]
	}
	return written, nil
}

func (p *process) kill() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.finished {
		p.cancelled = true
		_ = p.cmd.Process.Kill()
	}
}

func (s *Server) run(w http.ResponseWriter, r *http.Request, req sandboxcontract.RunCommandRequest) {
	cmd, err := s.command(req)
	if err != nil {
		reject(w, "invalid_execution_profile", 400)
		return
	}
	clear(req.Environment)
	p := &process{identity: req.Identity, ref: req.Ref, cmd: cmd, done: make(chan struct{})}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	b := &outputBuffer{remaining: req.OutputLimitBytes, events: make(chan output, 64), ctx: ctx, kill: p.kill}
	cmd.Stdout = channelWriter{b, executionbackend.EventStdout}
	cmd.Stderr = channelWriter{b, executionbackend.EventStderr}
	err = cmd.Start()
	closeExtraFiles(cmd)
	if err != nil {
		reject(w, "process_start_failed", 422)
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		p.kill()
		_ = cmd.Wait()
		reject(w, "runtime_closed", 503)
		return
	}
	s.processes[req.ProcessID] = p
	s.mu.Unlock()
	var waitErr error
	go func() {
		waitErr = cmd.Wait()
		p.mu.Lock()
		p.finished = true
		p.cmd = nil
		p.mu.Unlock()
		close(b.events)
		close(p.done)
	}()
	timer := time.NewTimer(time.Duration(req.TimeoutMillis) * time.Millisecond)
	defer timer.Stop()
	stop := context.AfterFunc(ctx, p.kill)
	defer stop()
	stream := newStream(w, req.Identity, req.Ref)
	if stream.ack(req.ProcessID) != nil {
		cancel()
		p.kill()
		<-p.done
		return
	}
	timedOut := false
	for {
		select {
		case <-timer.C:
			timedOut = true
			p.kill()
		case <-ctx.Done():
			p.kill()
			<-p.done
			return
		case e, ok := <-b.events:
			if !ok {
				<-p.done
				code := int32(cmd.ProcessState.ExitCode())
				result := executionbackend.TerminalResult{Status: executionbackend.TerminalSucceeded, ExitCode: &code, OutputComplete: !b.truncated}
				if waitErr != nil {
					result.Status = executionbackend.TerminalFailed
					result.ReasonCode = "process_failed"
				}
				p.mu.Lock()
				cancelled := p.cancelled
				p.mu.Unlock()
				if cancelled {
					result.Status = executionbackend.TerminalCancelled
					result.ReasonCode = "process_cancelled"
				}
				if timedOut {
					result.ReasonCode = "process_timeout"
				}
				if b.truncated {
					result.Status = executionbackend.TerminalFailed
					result.ReasonCode = "output_limit"
				}
				stream.terminal(result)
				return
			}
			if stream.event(e.kind, e.data) != nil {
				cancel()
				p.kill()
				<-p.done
				return
			}
		}
	}
}

func (s *Server) signal(w http.ResponseWriter, r *http.Request, req sandboxcontract.SignalCommandRequest) {
	s.mu.Lock()
	p := s.processes[req.ProcessID]
	s.mu.Unlock()
	if p == nil || p.ref != req.Ref || p.identity.Session != req.Identity.Session || p.identity.RunID != req.Identity.RunID || p.identity.RunAttemptID != req.Identity.RunAttemptID || p.identity.RunAttemptGeneration != req.Identity.RunAttemptGeneration || p.identity.ExecutionID != req.Identity.ExecutionID || (req.ProviderHandle != "" && req.ProviderHandle != req.ProcessID) {
		reject(w, "process_not_found", 404)
		return
	}
	// Killing the bubblewrap supervisor tears down its entire PID namespace,
	// including setsid/double-fork children. Do not claim delivery of an
	// interrupt as a graceful signal: only terminate/kill are supported here.
	if req.Signal == executionbackend.SignalInterrupt {
		reject(w, "interrupt_unsupported", 422)
		return
	}
	p.kill()
	stream := newStream(w, req.Identity, req.Ref)
	if stream.ack(req.ProcessID) != nil {
		return
	}
	select {
	case <-p.done:
		stream.terminal(executionbackend.TerminalResult{Status: executionbackend.TerminalSucceeded, OutputComplete: true})
	case <-time.After(10 * time.Second):
		stream.terminal(executionbackend.TerminalResult{Status: executionbackend.TerminalUnknown, ReasonCode: "termination_unconfirmed"})
	case <-r.Context().Done():
	}
}

func (s *Server) read(w http.ResponseWriter, r *http.Request, req sandboxcontract.ReadFileRequest) {
	// accept() has already journaled this exact read operation. File-level
	// errors are terminal operation results, not ambiguous transport failures.
	stream := newStream(w, req.Identity, req.Ref)
	if stream.ack("") != nil {
		return
	}
	fail := func(code string) {
		stream.terminal(executionbackend.TerminalResult{Status: executionbackend.TerminalFailed, ReasonCode: code, OutputComplete: true})
	}
	rel, err := filepath.Rel(s.config.Workspace, req.Path)
	if err != nil || !filepath.IsLocal(rel) || rel == "." || req.Offset > 1<<63-1 {
		fail("path_outside_workspace")
		return
	}
	root, err := os.OpenRoot(s.workspaceSource())
	if err != nil {
		fail("workspace_unavailable")
		return
	}
	defer root.Close()
	file, err := root.OpenFile(rel, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		fail("file_unavailable")
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		fail("not_regular_file")
		return
	}
	if _, err = file.Seek(int64(req.Offset), io.SeekStart); err != nil {
		fail("read_failed")
		return
	}
	reader := io.LimitReader(file, int64(req.Limit))
	buf := make([]byte, 16*1024)
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			if stream.event(executionbackend.EventFileBytes, buf[:n]) != nil {
				return
			}
		}
		if err == io.EOF {
			stream.terminal(executionbackend.TerminalResult{Status: executionbackend.TerminalSucceeded, OutputComplete: true})
			return
		}
		if err != nil {
			stream.terminal(executionbackend.TerminalResult{Status: executionbackend.TerminalFailed, ReasonCode: "read_failed"})
			return
		}
		if r.Context().Err() != nil {
			return
		}
	}
}

func (s *Server) Close() {
	s.mu.Lock()
	s.closed = true
	cancelProject := s.projectCancel
	all := make([]*process, 0, len(s.processes))
	for _, p := range s.processes {
		all = append(all, p)
	}
	s.mu.Unlock()
	if cancelProject != nil {
		cancelProject()
	}
	for _, p := range all {
		p.kill()
	}
}

func closeExtraFiles(cmd *exec.Cmd) {
	for _, file := range cmd.ExtraFiles {
		_ = file.Close()
	}
	cmd.ExtraFiles = nil
}
