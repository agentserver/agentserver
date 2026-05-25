// Package supervisor spawns and tracks per-thread `codex app-server`
// subprocesses inside the codex-app-gateway pod.
package supervisor

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// ChildHandle is what spawnCodexAppServer returns.
type ChildHandle struct {
	cmd       *exec.Cmd
	WSURL     string // ws://127.0.0.1:PORT
	HTTPURL   string // http://127.0.0.1:PORT  (for /readyz, /healthz)
	CodexHome string
	// lastActiveAt is set by Supervisor.EnsureSubprocess at registration
	// time; nil before then. broker.Conn writes through this pointer on
	// every ws frame so the supervisor's clock (which the IdleReaper
	// reads) sees broker traffic without going through Touch. See
	// LastActiveAt accessor.
	lastActiveAt *atomic.Int64
	done         chan struct{} // closed after cmd.Wait returns
	waitErr      error         // set before done is closed
	waitMu       sync.Mutex    // guards waitErr
}

// LastActiveAt returns the shared activity clock for this subprocess.
// Callers that observe traffic (e.g. broker.Conn on every ws frame)
// should Store(time.Now().UnixNano()) into it; the supervisor's
// IdleReaper reads from the same pointer when deciding whether the
// subprocess is idle. Nil only for handles produced outside of
// Supervisor.EnsureSubprocess (test fakes); callers should tolerate
// nil with a no-op write.
func (h *ChildHandle) LastActiveAt() *atomic.Int64 { return h.lastActiveAt }

// IsAlive reports whether the subprocess is still running. Cheap;
// suitable for calling on every EnsureSubprocess hit.
func (h *ChildHandle) IsAlive() bool {
	if h == nil || h.done == nil {
		return false
	}
	select {
	case <-h.done:
		return false
	default:
		return true
	}
}

// Done returns a channel closed after the subprocess exits. Useful for
// callers that want to be notified of unexpected termination.
func (h *ChildHandle) Done() <-chan struct{} { return h.done }

// Stop sends SIGTERM, waits up to 10s, then SIGKILLs.
func (h *ChildHandle) Stop(ctx context.Context) error {
	if h.cmd == nil || h.cmd.Process == nil {
		return nil
	}
	if err := h.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		// Process may already have exited; treat as success.
		select {
		case <-h.done:
			return nil
		default:
			return fmt.Errorf("SIGTERM: %w", err)
		}
	}
	select {
	case <-h.done:
		return nil
	case <-time.After(10 * time.Second):
		_ = h.cmd.Process.Signal(syscall.SIGKILL)
		<-h.done
		return nil
	case <-ctx.Done():
		_ = h.cmd.Process.Signal(syscall.SIGKILL)
		<-h.done
		return ctx.Err()
	}
}

// spawnCodexAppServer launches `codexBin app-server --listen ws://127.0.0.1:0`,
// reads the listen URL from its output, polls /readyz, and returns a handle.
//
// The real `codex` binary writes a multi-line startup banner to stderr:
//
//	codex app-server (WebSockets)
//	  listening on: ws://127.0.0.1:PORT
//	  readyz: http://127.0.0.1:PORT/readyz
//	  ...
//
// Test fakes write a bare "ws://127.0.0.1:PORT\n" to stdout. We scan both
// streams concurrently and extract the first line containing "ws://".
func spawnCodexAppServer(ctx context.Context, codexBin, codexHome string, extraEnv []string) (*ChildHandle, error) {
	cmd := exec.Command(codexBin, "app-server", "--listen", "ws://127.0.0.1:0")
	cmd.Env = append(append(os.Environ(), extraEnv...), "CODEX_HOME="+codexHome)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start: %w", err)
	}

	// Scan both stdout and stderr concurrently for a line containing "ws://".
	// Real codex writes the URL to stderr; test fakes write it to stdout.
	type result struct {
		url string
		err error
	}
	urlCh := make(chan result, 1)
	scanStream := func(r io.Reader) {
		br := bufio.NewReader(r)
		for {
			line, err := br.ReadString('\n')
			trimmed := strings.TrimSpace(line)
			if idx := strings.Index(trimmed, "ws://"); idx >= 0 {
				select {
				case urlCh <- result{url: trimmed[idx:]}:
				default:
				}
				// Pipe remainder to gateway's stderr so codex app-server
				// + env-mcp child logs surface in `kubectl logs`. Each
				// line is prefixed for grep-ability.
				go func() { _, _ = io.Copy(prefixedWriter{prefix: "[codex-subproc] ", w: os.Stderr}, br) }()
				return
			}
			if err != nil {
				select {
				case urlCh <- result{err: err}:
				default:
				}
				return
			}
		}
	}
	go scanStream(stdout)
	go scanStream(stderr)

	var wsURL string
	select {
	case r := <-urlCh:
		if r.err != nil {
			_ = cmd.Process.Kill()
			return nil, fmt.Errorf("read listen line: %w", r.err)
		}
		wsURL = r.url
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		return nil, ctx.Err()
	}

	if !strings.HasPrefix(wsURL, "ws://") {
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("unexpected listen line %q", wsURL)
	}
	httpURL := "http://" + strings.TrimPrefix(wsURL, "ws://")
	// Both pipes are drained in background goroutines already.

	deadline := time.Now().Add(5 * time.Second)
	for {
		req, _ := http.NewRequestWithContext(ctx, "GET", httpURL+"/readyz", nil)
		resp, err := http.DefaultClient.Do(req)
		if err == nil && resp.StatusCode == 200 {
			_ = resp.Body.Close()
			break
		}
		if resp != nil {
			_ = resp.Body.Close()
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			return nil, fmt.Errorf("readyz never returned 200: last err=%v", err)
		}
		select {
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}

	handle := &ChildHandle{
		cmd:       cmd,
		WSURL:     wsURL,
		HTTPURL:   httpURL,
		CodexHome: codexHome,
		done:      make(chan struct{}),
	}
	go func() {
		err := cmd.Wait()
		handle.waitMu.Lock()
		handle.waitErr = err
		handle.waitMu.Unlock()
		close(handle.done)
	}()
	return handle, nil
}

// prefixedWriter writes each newline-terminated chunk to w with a fixed
// prefix. Used to tag spawned subprocess output (codex app-server +
// env-mcp child) in the gateway pod's stderr so `kubectl logs` is
// grep-friendly when diagnosing per-spawn issues.
type prefixedWriter struct {
	prefix string
	w      io.Writer
}

func (p prefixedWriter) Write(b []byte) (int, error) {
	// Simple line-buffered: split on '\n', prefix each non-empty line.
	// Imperfect across partial reads (mid-line splits) but adequate for
	// rough diagnostic visibility — full structured forwarding is a
	// follow-up.
	n := len(b)
	for {
		idx := indexByte(b, '\n')
		if idx < 0 {
			if len(b) > 0 {
				_, _ = p.w.Write([]byte(p.prefix))
				_, _ = p.w.Write(b)
				_, _ = p.w.Write([]byte{'\n'})
			}
			return n, nil
		}
		_, _ = p.w.Write([]byte(p.prefix))
		_, _ = p.w.Write(b[:idx+1])
		b = b[idx+1:]
	}
}

func indexByte(b []byte, c byte) int {
	for i := range b {
		if b[i] == c {
			return i
		}
	}
	return -1
}
