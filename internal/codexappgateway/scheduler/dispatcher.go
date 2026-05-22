// internal/codexappgateway/scheduler/dispatcher.go
package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type agentClient interface {
	LeaseDue(ctx context.Context, req LeaseRequest) ([]Task, error)
	PostResult(ctx context.Context, req ResultRequest) error
	ListChannels(ctx context.Context, workspaceID string) ([]ChannelRef, error)
}

type spawner interface {
	Run(ctx context.Context, in SpawnInput) (SpawnResult, error)
}

type broadcaster interface {
	Send(ctx context.Context, workspaceID, text string, channels []ChannelRef) BroadcastReport
}

// Dispatcher implements the fire pipeline: script gate → spawn → report → broadcast.
type Dispatcher struct {
	agent       agentClient
	spawner     spawner
	broadcaster broadcaster
}

// NewDispatcher constructs a Dispatcher from its three collaborators.
func NewDispatcher(a agentClient, sp spawner, br broadcaster) *Dispatcher {
	return &Dispatcher{agent: a, spawner: sp, broadcaster: br}
}

// Fire executes the full scheduled-task fire pipeline for a single Task.
func (d *Dispatcher) Fire(ctx context.Context, t Task) error {
	start := time.Now()
	prompt := t.Prompt

	// 1. Script gate — three outcomes:
	//    a) wakeAgent=false  → skipped (no broadcast)
	//    b) error            → failed  (broadcast)
	//    c) wakeAgent=true   → prepend script_data to prompt, continue to spawn
	if t.Script != nil && *t.Script != "" {
		wake, data, err := RunPreScript(ctx, *t.Script, scriptEnv(t))
		switch {
		case err != nil:
			return d.report(ctx, t, ResultRequest{
				TaskID:          t.ID,
				RunID:           t.RunID,
				Status:          "failed",
				Summary:         truncErr(err),
				DurationMS:      time.Since(start).Milliseconds(),
				BroadcastTo:     nil,
				BroadcastErrors: json.RawMessage("{}"),
			}, truncErr(err), true)
		case !wake:
			return d.report(ctx, t, ResultRequest{
				TaskID:          t.ID,
				RunID:           t.RunID,
				Status:          "skipped",
				Summary:         "script gated (wakeAgent=false)",
				DurationMS:      time.Since(start).Milliseconds(),
				BroadcastTo:     nil,
				BroadcastErrors: json.RawMessage("{}"),
			}, "", false) // do NOT broadcast skipped runs
		default:
			if len(data) > 0 {
				prompt = "## script_data:\n" + string(data) + "\n\n" + prompt
			}
		}
	}

	// 2. Spawn codex
	timeout := time.Duration(t.TimeoutSeconds) * time.Second
	res, err := d.spawner.Run(ctx, SpawnInput{Prompt: prompt, Env: codexEnv(t), Timeout: timeout})

	status := "succeeded"
	summary := res.Summary
	switch {
	case err != nil:
		status = "failed"
		summary = truncErr(err)
	case res.TimedOut:
		status = "timeout"
		if summary == "" {
			summary = "(codex exec timed out)"
		}
	case res.ExitCode != 0:
		status = "failed"
		if summary == "" {
			summary = fmt.Sprintf("(codex exec exit %d)", res.ExitCode)
		}
	}

	return d.report(ctx, t, ResultRequest{
		TaskID:     t.ID,
		RunID:      t.RunID,
		Status:     status,
		ExitCode:   res.ExitCode,
		DurationMS: res.DurationMS,
		Summary:    summary,
		CostUSD:    res.CostUSD,
		NumTurns:   res.NumTurns,
	}, summary, true)
}

// report posts the result to agentserver and — if shouldBroadcast — fans out
// to all workspace IM channels. Always ensures BroadcastTo and BroadcastErrors
// are non-nil so JSON never sees null.
func (d *Dispatcher) report(ctx context.Context, t Task, r ResultRequest, broadcastText string, shouldBroadcast bool) error {
	if shouldBroadcast {
		channels, err := d.agent.ListChannels(ctx, t.WorkspaceID)
		switch {
		case err != nil:
			// Record so the operator can distinguish "no IM channels bound" from
			// "agentserver unreachable" when grepping run history.
			b, _ := json.Marshal(map[string]string{"_list_channels": err.Error()})
			r.BroadcastErrors = b
		case len(channels) > 0:
			rep := d.broadcaster.Send(ctx, t.WorkspaceID, renderIMText(t, r, broadcastText), channels)
			r.BroadcastTo = rep.To
			b, _ := json.Marshal(rep.Errors)
			r.BroadcastErrors = b
		}
	}
	if r.BroadcastErrors == nil {
		r.BroadcastErrors = json.RawMessage("{}")
	}
	if r.BroadcastTo == nil {
		r.BroadcastTo = []string{}
	}
	return d.agent.PostResult(ctx, r)
}

func renderIMText(t Task, r ResultRequest, body string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "[scheduled task fired — %s]\n", t.SeriesID)
	fmt.Fprintf(&sb, "at: %s  (took %dms)\n", time.Now().UTC().Format(time.RFC3339), r.DurationMS)
	if body == "" {
		body = "(no output)"
	}
	if len(body) > 1500 {
		body = body[:1500] + "…(truncated)"
	}
	sb.WriteString(body)
	return sb.String()
}

func codexEnv(t Task) []string {
	return []string{"TZ=" + t.Timezone}
	// production: append workspace creds from a sealed config (see Task 12 wiring).
}

func scriptEnv(t Task) []string {
	// Whitelist only — explicitly NOT including ANTHROPIC_API_KEY etc.
	return []string{"TZ=" + t.Timezone, "TASK_ID=" + t.ID}
}

func truncErr(err error) string {
	s := err.Error()
	if len(s) > 1500 {
		s = s[:1500]
	}
	return s
}
