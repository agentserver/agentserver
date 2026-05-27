package envmcp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/agentserver/agentserver/internal/envtools/bridge"
	"github.com/agentserver/agentserver/internal/envtools/tools"
)

// blockingTool blocks Call until release is signalled, then returns text.
type blockingTool struct {
	name    string
	release <-chan struct{}
	text    string
}

func (t *blockingTool) Name() string                 { return t.name }
func (t *blockingTool) Description() string          { return "blocking" }
func (t *blockingTool) InputSchema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (t *blockingTool) Call(ctx context.Context, _ json.RawMessage) (tools.MCPCallToolResult, error) {
	select {
	case <-t.release:
	case <-ctx.Done():
		return tools.MCPCallToolResult{}, ctx.Err()
	}
	return tools.MCPCallToolResult{Content: []tools.MCPToolContent{{Type: "text", Text: t.text}}}, nil
}

// instantTool returns text immediately.
type instantTool struct {
	name string
	text string
}

func (t *instantTool) Name() string                 { return t.name }
func (t *instantTool) Description() string          { return "instant" }
func (t *instantTool) InputSchema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (t *instantTool) Call(_ context.Context, _ json.RawMessage) (tools.MCPCallToolResult, error) {
	return tools.MCPCallToolResult{Content: []tools.MCPToolContent{{Type: "text", Text: t.text}}}, nil
}

func encodeCall(id int64, toolName string) []byte {
	params, _ := json.Marshal(tools.MCPCallToolParams{Name: toolName, Arguments: json.RawMessage(`{}`)})
	msg := bridge.JSONRPCMessage{JSONRPC: "2.0", ID: &id, Method: "tools/call", Params: params}
	out, _ := json.Marshal(&msg)
	return append(out, '\n')
}

func decodeResponse(t *testing.T, line []byte) (id int64, text string) {
	t.Helper()
	var env bridge.JSONRPCMessage
	if err := json.Unmarshal(line, &env); err != nil {
		t.Fatalf("decode envelope: %v (line=%q)", err, line)
	}
	if env.ID == nil {
		t.Fatalf("response missing id: %q", line)
	}
	if env.Error != nil {
		t.Fatalf("response had error: %+v", env.Error)
	}
	var res tools.MCPCallToolResult
	if err := json.Unmarshal(env.Result, &res); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if len(res.Content) == 0 {
		t.Fatalf("result has no content")
	}
	return *env.ID, res.Content[0].Text
}

// TestMCPServer_ConcurrentToolsCall verifies a slow tools/call does not
// block a fast tools/call that arrives behind it on the same stdin.
// Under serial dispatch the fast call would queue behind the slow one
// and the test would deadlock.
func TestMCPServer_ConcurrentToolsCall(t *testing.T) {
	release := make(chan struct{})
	srv := NewMCPServer("test",
		[]tools.Tool{
			&blockingTool{name: "slow", release: release, text: "slow-done"},
			&instantTool{name: "fast", text: "fast-done"},
		},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()

	serveErr := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() {
		serveErr <- srv.Serve(ctx, inR, outW)
		_ = outW.Close()
	}()

	if _, err := inW.Write(encodeCall(1, "slow")); err != nil {
		t.Fatalf("write slow: %v", err)
	}
	if _, err := inW.Write(encodeCall(2, "fast")); err != nil {
		t.Fatalf("write fast: %v", err)
	}

	reader := bufio.NewReader(outR)
	// Expect the fast response back BEFORE we release slow.
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("read first response: %v", err)
	}
	id, text := decodeResponse(t, line)
	if id != 2 || text != "fast-done" {
		t.Fatalf("expected fast response first (id=2 text=fast-done), got id=%d text=%q", id, text)
	}

	close(release)
	line, err = reader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("read second response: %v", err)
	}
	id, text = decodeResponse(t, line)
	if id != 1 || text != "slow-done" {
		t.Fatalf("expected slow response second (id=1 text=slow-done), got id=%d text=%q", id, text)
	}

	_ = inW.Close()
	select {
	case err := <-serveErr:
		if err != nil {
			t.Fatalf("Serve returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("Serve did not return after stdin close")
	}
}

// TestMCPServer_EOFCancelsInflightToolCalls verifies that closing
// stdin (with the outer ctx still alive) cancels in-flight tool
// goroutines via the per-Serve ctx, so Serve does not hang waiting
// for a stuck tool to return on its own. Defer order in Serve must be
// `defer wg.Wait(); defer cancelCalls()` so cancel runs FIRST under LIFO.
func TestMCPServer_EOFCancelsInflightToolCalls(t *testing.T) {
	release := make(chan struct{}) // never closed; tool MUST exit via ctx.Done
	srv := NewMCPServer("test",
		[]tools.Tool{&blockingTool{name: "slow", release: release, text: "unused"}},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()

	serveErr := make(chan error, 1)
	// Outer ctx stays alive — only stdin EOF drives shutdown.
	go func() {
		serveErr <- srv.Serve(context.Background(), inR, outW)
		_ = outW.Close()
	}()
	// Drain stdout so the cancel-error response doesn't block on a full pipe.
	go func() { _, _ = io.Copy(io.Discard, outR) }()

	if _, err := inW.Write(encodeCall(1, "slow")); err != nil {
		t.Fatalf("write slow: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	_ = inW.Close()

	select {
	case err := <-serveErr:
		if err != nil {
			t.Fatalf("Serve returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("Serve hung after EOF — in-flight tool was not cancelled")
	}
}

// failingWriter returns an error from Write after failAfter successful writes.
type failingWriter struct{ failAfter int }

func (w *failingWriter) Write(p []byte) (int, error) {
	w.failAfter--
	if w.failAfter < 0 {
		return 0, io.ErrClosedPipe
	}
	return len(p), nil
}

// TestMCPServer_WriteFailureStopsAcceptingNewWork verifies that a
// stdout write failure signals Serve to stop accepting new work and
// surface the error on return. Without this, codex sees N requests
// turn into 0 responses because every Write error gets silently logged.
func TestMCPServer_WriteFailureStopsAcceptingNewWork(t *testing.T) {
	srv := NewMCPServer("test",
		[]tools.Tool{&instantTool{name: "fast", text: "ok"}},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	inR, inW := io.Pipe()
	out := &failingWriter{failAfter: 0} // first Write fails

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- srv.Serve(context.Background(), inR, out)
	}()

	if _, err := inW.Write(encodeCall(1, "fast")); err != nil {
		t.Fatalf("write 1: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	_ = inW.Close()

	select {
	case err := <-serveErr:
		if err == nil {
			t.Fatalf("Serve returned nil; want error reflecting stdout write failure")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("Serve hung after stdin close")
	}
}

// TestMCPServer_InflightCancelOnShutdown verifies that in-flight tool
// calls observe ctx cancellation when Serve's outer context is
// cancelled, and that Serve waits for them to drain before returning.
func TestMCPServer_InflightCancelOnShutdown(t *testing.T) {
	release := make(chan struct{}) // never closed; tool only returns via ctx.Done
	srv := NewMCPServer("test",
		[]tools.Tool{&blockingTool{name: "slow", release: release, text: "unused"}},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()

	serveCtx, cancelServe := context.WithCancel(context.Background())
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- srv.Serve(serveCtx, inR, outW)
		_ = outW.Close()
	}()
	go func() { _, _ = io.Copy(io.Discard, outR) }()

	if _, err := inW.Write(encodeCall(1, "slow")); err != nil {
		t.Fatalf("write slow: %v", err)
	}

	time.Sleep(50 * time.Millisecond)
	cancelServe()
	_ = inW.Close()

	select {
	case <-serveErr:
		// expected
	case <-time.After(2 * time.Second):
		t.Fatalf("Serve did not return after ctx cancel + EOF")
	}
}
