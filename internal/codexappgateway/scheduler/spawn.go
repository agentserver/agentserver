// internal/codexappgateway/scheduler/spawn.go
package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/agentserver/agentserver/internal/codexappgateway/broker"
)

// SpawnInput is the per-fire request to the spawner.
type SpawnInput struct {
	WorkspaceID string
	Prompt      string
	Timeout     time.Duration
}

// SpawnResult is the outcome the dispatcher reports back to agentserver.
// ExitCode is 0 on success and 1 on any failure mode (transport, RPC,
// turn.status != completed) — we intentionally collapse the kinds since
// the broker.Turn surface doesn't have a process-style exit code.
type SpawnResult struct {
	ExitCode   int
	Transcript string  // full Turn JSON for debugging
	Summary    string  // assistant's last agentMessage text, or error msg
	CostUSD    *float64
	NumTurns   *int
	TimedOut   bool
	DurationMS int64
}

// BrokerSpawner executes a scheduled task by submitting a new
// per-fire codex thread through the shared broker.Pool. It reuses
// the same supervisor-managed `codex app-server` subprocess that
// WeChat traffic uses, so scheduled runs share the per-workspace
// model_provider config (modelserver), the agentserver MCP block,
// and the S3-backed CODEX_HOME state — none of which the legacy
// `codex exec` subprocess path had.
//
// Per-fire thread: each Fire calls thread/start to get a clean
// session. We do NOT inherit any user-facing thread (WeChat / TUI)
// because (a) scheduled_tasks has no targeting field and (b) most
// recently used isn't well-defined for a workspace with multiple
// IM channels and TUI sessions. Clean slate keeps the result
// independent of whatever user conversation happens to be active.
type BrokerSpawner struct {
	pool *broker.Pool
}

func NewBrokerSpawner(pool *broker.Pool) *BrokerSpawner {
	return &BrokerSpawner{pool: pool}
}

const summaryCap = 4 << 10

func (s *BrokerSpawner) Run(ctx context.Context, in SpawnInput) (SpawnResult, error) {
	if in.WorkspaceID == "" {
		return SpawnResult{ExitCode: 1, Summary: "spawner: empty workspace id"}, nil
	}
	if in.Timeout <= 0 {
		in.Timeout = 10 * time.Minute
	}
	start := time.Now()

	conn, err := s.pool.Get(ctx, in.WorkspaceID)
	if err != nil {
		return failedResult(err, start), nil
	}
	threadID, err := conn.StartThread(ctx)
	if err != nil {
		return failedResult(fmt.Errorf("thread/start: %w", err), start), nil
	}

	params, err := json.Marshal(map[string]any{
		"input": []map[string]any{{"type": "text", "text": in.Prompt}},
	})
	if err != nil {
		return failedResult(fmt.Errorf("marshal params: %w", err), start), nil
	}

	rawTurn, err := conn.Turn(ctx, threadID, params, in.Timeout)
	dur := time.Since(start).Milliseconds()
	if err != nil {
		res := SpawnResult{ExitCode: 1, Summary: truncate(err.Error(), summaryCap), DurationMS: dur}
		var timeoutErr *broker.TimeoutError
		if errors.As(err, &timeoutErr) {
			res.TimedOut = true
		}
		return res, nil
	}

	var turn struct {
		Status string            `json:"status"`
		Items  []json.RawMessage `json:"items"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rawTurn, &turn); err != nil {
		return SpawnResult{ExitCode: 1, Summary: "decode turn: " + err.Error(), DurationMS: dur, Transcript: string(rawTurn)}, nil
	}

	res := SpawnResult{
		Transcript: string(rawTurn),
		DurationMS: dur,
	}
	switch turn.Status {
	case "completed":
		res.ExitCode = 0
		res.Summary = truncate(lastAgentMessageText(turn.Items), summaryCap)
		if res.Summary == "" {
			res.Summary = "(no text response)"
		}
	default:
		res.ExitCode = 1
		if turn.Error != nil && turn.Error.Message != "" {
			res.Summary = truncate(turn.Error.Message, summaryCap)
		} else {
			res.Summary = fmt.Sprintf("(turn status=%s)", turn.Status)
		}
	}
	return res, nil
}

func failedResult(err error, start time.Time) SpawnResult {
	return SpawnResult{
		ExitCode:   1,
		Summary:    truncate(err.Error(), summaryCap),
		DurationMS: time.Since(start).Milliseconds(),
	}
}

// lastAgentMessageText scans items in reverse for the last
// {type:"agentMessage"} entry and returns its text. Returns "" if none.
// Duplicated from internal/server/codex_im_inbound.go to avoid the
// scheduler depending on the server package.
func lastAgentMessageText(items []json.RawMessage) string {
	for i := len(items) - 1; i >= 0; i-- {
		var shell struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(items[i], &shell); err != nil {
			continue
		}
		if shell.Type == "agentMessage" && shell.Text != "" {
			return shell.Text
		}
	}
	return ""
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
