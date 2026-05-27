package envmcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"unicode/utf8"

	"github.com/agentserver/agentserver/internal/envtools/bridge"
	"github.com/agentserver/agentserver/internal/envtools/tools"
)

// MCPServer is a minimal newline-delimited JSON-RPC stdio MCP server
// that exposes a fixed set of tools through a registry. Concurrency:
// `tools/call` requests run in their own goroutine so a slow tool
// (e.g. a long-running shell) does not block faster tools (e.g.
// list_environments) arriving behind it on stdin. The MCP/JSON-RPC
// stdio protocol pairs responses to requests by id, so out-of-order
// responses are fine on the wire; writes to stdout are serialized
// through writeMu so concurrent responders don't interleave bytes.
// Trivial RPC methods (initialize, tools/list, prompts/list, ...)
// stay inline since they don't block.
type MCPServer struct {
	name    string // surfaces in initialize/serverInfo
	tools   map[string]tools.Tool
	order   []string // stable tools/list ordering
	writeMu sync.Mutex
	logger  *slog.Logger
}

// NewMCPServer constructs a server bound to a registry. Tool order is
// preserved as supplied (LLM clients sometimes rely on consistent
// ordering for caching).
func NewMCPServer(name string, ts []tools.Tool, logger *slog.Logger) *MCPServer {
	if logger == nil {
		logger = slog.Default()
	}
	reg := make(map[string]tools.Tool, len(ts))
	order := make([]string, 0, len(ts))
	for _, t := range ts {
		if _, dup := reg[t.Name()]; dup {
			logger.Warn("mcp: duplicate tool name; later registration wins", "name", t.Name())
		}
		reg[t.Name()] = t
		order = append(order, t.Name())
	}
	return &MCPServer{name: name, tools: reg, order: order, logger: logger}
}

// previewLine returns up to 200 bytes of line as a string, truncating safely.
func previewLine(line []byte) string {
	const max = 200
	if len(line) <= max {
		return string(line)
	}
	truncated := line[:max]
	for !utf8.Valid(truncated) {
		truncated = truncated[:len(truncated)-1]
	}
	return string(truncated) + "…"
}

// Serve reads requests from in until EOF and writes responses to out.
// Returns nil on clean EOF, error on unrecoverable read/write failure.
// In-flight `tools/call` goroutines observe ctx cancellation; Serve
// waits for them to drain before returning so the caller can rely on
// "Serve returned" meaning "no goroutine is still writing to out".
//
// If any response write fails (stdout pipe gone), Serve records the
// first error, cancels the per-Serve ctx so in-flight tool calls
// abort promptly, and stops dispatching new tools/call work. We don't
// own `in`, so the read loop only unblocks once stdin closes — but no
// further responses are emitted in the interim, and Serve returns
// the original write error rather than scanner.Err().
func (s *MCPServer) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	// Per-Serve ctx so cancelling Serve (or a fatal write error)
	// signals every in-flight tool call. Order of these two defers
	// matters: defers run LIFO, so the LAST one registered runs
	// FIRST. We want cancelCalls() to run BEFORE wg.Wait() so
	// in-flight goroutines observe ctx.Done() and exit promptly;
	// hence cancelCalls is the last defer here.
	callCtx, cancelCalls := context.WithCancel(ctx)
	var wg sync.WaitGroup
	defer wg.Wait()
	defer cancelCalls()

	var fatalErr atomic.Pointer[error]
	onWriteErr := func(err error) {
		s.logger.Warn("mcp: write response failed", "err", err)
		// CAS so we only record + cancel once; subsequent failures are
		// just logged. cancelCalls is idempotent but repeated logging
		// would be noisy.
		if fatalErr.CompareAndSwap(nil, &err) {
			cancelCalls()
		}
	}

	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for scanner.Scan() {
		if callCtx.Err() != nil {
			// outer ctx cancelled OR a prior write failed
			break
		}
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var req bridge.JSONRPCMessage
		if err := json.Unmarshal(line, &req); err != nil {
			s.logger.Warn("mcp: dropping malformed JSON-RPC line", "err", err, "preview", previewLine(line))
			continue
		}
		s.dispatch(callCtx, &req, out, &wg, onWriteErr)
	}
	if e := fatalErr.Load(); e != nil {
		return *e
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return scanner.Err()
}

// dispatch handles one JSON-RPC request. `tools/call` runs in its own
// goroutine (tracked by wg) so slow tools don't block the read loop;
// every other method runs inline since the body is trivial. Response
// writes go through s.respond which holds writeMu. Write failures are
// reported to onWriteErr so Serve can stop accepting new work.
func (s *MCPServer) dispatch(ctx context.Context, req *bridge.JSONRPCMessage, out io.Writer, wg *sync.WaitGroup, onWriteErr func(error)) {
	send := func(id *int64, result any, errObj *bridge.JSONRPCError) {
		if err := s.respond(out, id, result, errObj); err != nil {
			onWriteErr(err)
		}
	}
	switch req.Method {
	case "initialize":
		send(req.ID, tools.MCPInitializeResult{
			ProtocolVersion: "2025-06-18",
			Capabilities:    map[string]any{"tools": map[string]any{}},
			ServerInfo:      tools.MCPServerInfo{Name: s.name, Version: "0.2"},
		}, nil)

	case "notifications/initialized":
		// notification — nothing to send back

	case "tools/list":
		list := make([]tools.MCPTool, 0, len(s.order))
		for _, name := range s.order {
			t := s.tools[name]
			list = append(list, tools.MCPTool{
				Name:        t.Name(),
				Description: t.Description(),
				InputSchema: t.InputSchema(),
			})
		}
		send(req.ID, tools.MCPListToolsResult{Tools: list}, nil)

	case "tools/call":
		var p tools.MCPCallToolParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			send(req.ID, nil, &bridge.JSONRPCError{Code: -32602, Message: "invalid params: " + err.Error()})
			return
		}
		t, ok := s.tools[p.Name]
		if !ok {
			send(req.ID, nil, &bridge.JSONRPCError{Code: -32601, Message: "unknown tool: " + p.Name})
			return
		}
		id, name := req.ID, p.Name
		args := p.Arguments
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := t.Call(ctx, args)
			if err != nil {
				// Tool returned a hard error (not an isError content) —
				// surface as JSON-RPC error so the LLM sees a clear
				// protocol failure rather than a silently-empty content list.
				send(id, nil, &bridge.JSONRPCError{Code: -32000, Message: name + ": " + err.Error()})
				return
			}
			send(id, res, nil)
		}()

	case "prompts/list":
		send(req.ID, map[string]any{"prompts": []any{}}, nil)
	case "resources/list":
		send(req.ID, map[string]any{"resources": []any{}}, nil)
	case "resources/templates/list":
		send(req.ID, map[string]any{"resourceTemplates": []any{}}, nil)

	default:
		if req.ID == nil {
			return // notification of unknown method — drop
		}
		send(req.ID, nil, &bridge.JSONRPCError{Code: -32601, Message: "method not found: " + req.Method})
	}
}

func (s *MCPServer) respond(out io.Writer, id *int64, result any, errObj *bridge.JSONRPCError) error {
	if id == nil && errObj == nil {
		return nil // nothing to say back
	}
	msg := bridge.JSONRPCMessage{JSONRPC: "2.0", ID: id, Error: errObj}
	if errObj == nil {
		raw, err := json.Marshal(result)
		if err != nil {
			return fmt.Errorf("marshal result: %w", err)
		}
		msg.Result = raw
	}
	out2, err := json.Marshal(&msg)
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := out.Write(append(out2, '\n')); err != nil {
		return errors.New("mcp write: " + err.Error())
	}
	return nil
}
