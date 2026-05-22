// internal/codexappgateway/scheduler/dispatcher.go
package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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
	tokens      WorkspaceTokenFetcher
	modelEnvKey string
}

// NewDispatcher constructs a Dispatcher from its collaborators. tokens and
// modelEnvKey are optional: when tokens is nil, codexEnv skips credential
// injection (useful in tests).
func NewDispatcher(a agentClient, sp spawner, br broadcaster, tokens WorkspaceTokenFetcher, modelEnvKey string) *Dispatcher {
	return &Dispatcher{agent: a, spawner: sp, broadcaster: br, tokens: tokens, modelEnvKey: modelEnvKey}
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
	env, err := d.codexEnv(ctx, t)
	if err != nil {
		return d.report(ctx, t, ResultRequest{
			TaskID:          t.ID,
			RunID:           t.RunID,
			Status:          "failed",
			Summary:         truncErr(err),
			DurationMS:      time.Since(start).Milliseconds(),
			BroadcastErrors: json.RawMessage("{}"),
		}, truncErr(err), true)
	}
	timeout := time.Duration(t.TimeoutSeconds) * time.Second
	res, err := d.spawner.Run(ctx, SpawnInput{Prompt: prompt, Env: env, Timeout: timeout})

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

// codexEnv builds the env for the spawned `codex exec` process.
// Includes TZ, the workspace-scoped Bearer token (via modelEnvKey), and a
// selective inheritance of PATH/HOME/CODEX_HOME from the dispatcher's process.
// Returns an error if the token fetch fails; the caller must report this
// as a 'failed' run rather than spawning a credential-less codex.
func (d *Dispatcher) codexEnv(ctx context.Context, t Task) ([]string, error) {
	env := []string{"TZ=" + t.Timezone}
	for _, k := range []string{"PATH", "HOME", "CODEX_HOME"} {
		if v := os.Getenv(k); v != "" {
			env = append(env, k+"="+v)
		}
	}
	if d.tokens != nil && d.modelEnvKey != "" {
		tok, err := d.tokens.GetOrCreate(ctx, t.WorkspaceID)
		if err != nil {
			return nil, fmt.Errorf("fetch workspace token: %w", err)
		}
		env = append(env, d.modelEnvKey+"="+tok)
	}
	return env, nil
}

func scriptEnv(t Task) []string {
	// Whitelist only — explicitly NOT including provider credentials etc.
	env := []string{"TZ=" + t.Timezone, "TASK_ID=" + t.ID}
	for _, k := range []string{"PATH", "HOME"} {
		if v := os.Getenv(k); v != "" {
			env = append(env, k+"="+v)
		}
	}
	return env
}

func truncErr(err error) string {
	s := err.Error()
	if len(s) > 1500 {
		s = s[:1500]
	}
	return s
}
