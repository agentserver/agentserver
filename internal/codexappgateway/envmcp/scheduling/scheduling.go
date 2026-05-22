// Package scheduling implements the 6 MCP scheduling tools that codex
// uses to schedule, list, update, cancel, pause, and resume tasks.
// The tools forward every call to a Transport (production: loopback HTTP
// to the app-gateway; tests: transportFunc stub).
package scheduling

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/agentserver/agentserver/internal/envtools/tools"
)

// ScheduleTransport is the abstraction the scheduling tools use to forward
// calls to the app-gateway loopback. In production it is a LoopbackTransport
// (HTTP POST to /internal/scheduled-tasks/<action>). In tests it can be any
// function via transportFunc.
type ScheduleTransport interface {
	Call(ctx context.Context, action string, body any) (json.RawMessage, error)
}

// transportFunc is an adapter that lets a plain function satisfy ScheduleTransport.
// Used in tests.
type transportFunc func(ctx context.Context, action string, body any) (json.RawMessage, error)

func (f transportFunc) Call(ctx context.Context, action string, body any) (json.RawMessage, error) {
	return f(ctx, action, body)
}

// NewSchedulingTools returns the 6 nanoclaw-aligned scheduling tools.
// transport may be nil if only metadata (Name/Description/InputSchema) is needed.
func NewSchedulingTools(transport ScheduleTransport) []tools.Tool {
	return []tools.Tool{
		&scheduleTaskTool{t: transport},
		&listTasksTool{t: transport},
		&updateTaskTool{t: transport},
		&cancelTaskTool{t: transport},
		&pauseTaskTool{t: transport},
		&resumeTaskTool{t: transport},
	}
}

// ---- schedule_task ----

type scheduleTaskTool struct{ t ScheduleTransport }

func (*scheduleTaskTool) Name() string { return "schedule_task" }
func (*scheduleTaskTool) Description() string {
	return `Schedule a one-shot or recurring task. Tasks persist across sessions and restarts.

TIMEZONE: your local timezone is attached automatically server-side from the TZ env. Use naive local timestamps like "2026-01-15T21:00:00" for processAfter — they will be interpreted in your zone. Cron expressions in recurrence are also evaluated in your zone.

RECURRING TASKS WITH script (recommended for frequent polling):
Frequent recurring tasks — more than a few times a day — consume API credits and can risk account restrictions. Add a bash ` + "`script`" + ` that runs first; you will only be called when the check passes.

How it works:
  1. Provide a bash script alongside the prompt.
  2. When the task fires, the script runs first.
  3. Script must print JSON to stdout: {"wakeAgent": true|false, "data": {...}}
  4. If wakeAgent=false → nothing happens, the task waits for next run.
  5. If wakeAgent=true → you receive the script's data + your prompt and handle.

When NOT to use scripts:
  If a task requires your judgment every time (daily briefings, reminders, reports), skip the script. Do not attempt sentiment analysis or NLP in scripts.

Always test your script first by running it directly to verify the JSON shape.`
}
func (*scheduleTaskTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "prompt":       {"type": "string", "description": "Task instructions/prompt"},
    "processAfter": {"type": "string", "description": "ISO 8601 timestamp for the first run. Accepts either UTC (ending in \"Z\" or \"+00:00\") or a naive local timestamp (no offset) which is interpreted in the user's timezone (e.g. \"2026-01-15T21:00:00\" = 9pm user-local). Prefer naive local."},
    "recurrence":   {"type": "string", "description": "Cron expression for recurring tasks (e.g., \"0 9 * * 1-5\" = weekdays at 9am user-local). Evaluated in the user's timezone."},
    "script":       {"type": "string", "description": "Optional pre-agent script to run before processing"}
  },
  "required": ["prompt", "processAfter"]
}`)
}
func (s *scheduleTaskTool) Call(ctx context.Context, args json.RawMessage) (tools.MCPCallToolResult, error) {
	return forward(ctx, s.t, "schedule", args)
}

// ---- list_tasks ----

type listTasksTool struct{ t ScheduleTransport }

func (*listTasksTool) Name() string { return "list_tasks" }
func (*listTasksTool) Description() string {
	return "List scheduled tasks. Returns one row per series — the live (pending or paused) occurrence. The id shown is the series id, which is what update_task / cancel_task / pause_task / resume_task expect."
}
func (*listTasksTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "status": {"type": "string", "description": "Filter by status: pending or paused (default: both)"}
  }
}`)
}
func (l *listTasksTool) Call(ctx context.Context, args json.RawMessage) (tools.MCPCallToolResult, error) {
	return forward(ctx, l.t, "list", args)
}

// ---- update_task ----

type updateTaskTool struct{ t ScheduleTransport }

func (*updateTaskTool) Name() string { return "update_task" }
func (*updateTaskTool) Description() string {
	return "Update a scheduled task. Pass the series id from list_tasks. Any field omitted is left unchanged. Use this instead of cancel + reschedule when adjusting an existing task."
}
func (*updateTaskTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "taskId":       {"type": "string", "description": "Series id of the task to update (as shown by list_tasks)"},
    "prompt":       {"type": "string", "description": "New task prompt (optional)"},
    "recurrence":   {"type": "string", "description": "New cron expression (optional). Pass empty string to clear and make the task one-shot."},
    "processAfter": {"type": "string", "description": "New ISO 8601 timestamp for the next run (optional). Accepts either UTC (ending in \"Z\" / \"+00:00\") or a naive local timestamp interpreted in the user's timezone."},
    "script":       {"type": "string", "description": "New pre-agent script (optional). Pass empty string to clear."}
  },
  "required": ["taskId"]
}`)
}
func (u *updateTaskTool) Call(ctx context.Context, args json.RawMessage) (tools.MCPCallToolResult, error) {
	// Reject empty updates (only taskId) — nanoclaw parity.
	var m map[string]json.RawMessage
	if err := json.Unmarshal(args, &m); err != nil {
		return errorResult("invalid args"), nil
	}
	if len(m) <= 1 {
		return errorResult("at least one field to update is required"), nil
	}
	return forward(ctx, u.t, "update", args)
}

// ---- cancel_task / pause_task / resume_task ----

type cancelTaskTool struct{ t ScheduleTransport }

func (*cancelTaskTool) Name() string        { return "cancel_task" }
func (*cancelTaskTool) Description() string { return "Cancel a scheduled task." }
func (*cancelTaskTool) InputSchema() json.RawMessage {
	return taskIDOnlySchema("Task ID to cancel")
}
func (c *cancelTaskTool) Call(ctx context.Context, args json.RawMessage) (tools.MCPCallToolResult, error) {
	return forward(ctx, c.t, "cancel", args)
}

type pauseTaskTool struct{ t ScheduleTransport }

func (*pauseTaskTool) Name() string        { return "pause_task" }
func (*pauseTaskTool) Description() string { return "Pause a scheduled task. It will not run until resumed." }
func (*pauseTaskTool) InputSchema() json.RawMessage {
	return taskIDOnlySchema("Task ID to pause")
}
func (p *pauseTaskTool) Call(ctx context.Context, args json.RawMessage) (tools.MCPCallToolResult, error) {
	return forward(ctx, p.t, "pause", args)
}

type resumeTaskTool struct{ t ScheduleTransport }

func (*resumeTaskTool) Name() string        { return "resume_task" }
func (*resumeTaskTool) Description() string { return "Resume a paused task." }
func (*resumeTaskTool) InputSchema() json.RawMessage {
	return taskIDOnlySchema("Task ID to resume")
}
func (r *resumeTaskTool) Call(ctx context.Context, args json.RawMessage) (tools.MCPCallToolResult, error) {
	return forward(ctx, r.t, "resume", args)
}

// ---- helpers ----

func taskIDOnlySchema(desc string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{
  "type": "object",
  "properties": {"taskId": {"type": "string", "description": %q}},
  "required": ["taskId"]
}`, desc))
}

func forward(ctx context.Context, t ScheduleTransport, action string, body json.RawMessage) (tools.MCPCallToolResult, error) {
	if t == nil {
		return errorResult("transport not configured"), nil
	}
	out, err := t.Call(ctx, action, body)
	if err != nil {
		return errorResult(err.Error()), nil
	}
	return tools.MCPCallToolResult{
		Content: []tools.MCPToolContent{{Type: "text", Text: string(out)}},
	}, nil
}

func errorResult(msg string) tools.MCPCallToolResult {
	return tools.MCPCallToolResult{
		Content: []tools.MCPToolContent{{Type: "text", Text: "Error: " + msg}},
		IsError: true,
	}
}
