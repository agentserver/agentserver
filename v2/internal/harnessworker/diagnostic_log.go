package harnessworker

import (
	"context"
	"log/slog"

	"github.com/agentserver/agentserver/v2/internal/harnesscontrol"
	"github.com/agentserver/agentserver/v2/internal/runmanifest"
	"github.com/agentserver/agentserver/v2/internal/safediagnostic"
)

const maximumWorkerDiagnosticFieldBytes = 16 * 1024

type workerDiagnosticText struct {
	value         string
	originalBytes int
	truncated     bool
	redacted      bool
}

// logWorkerFailureDiagnostics is the internal observability counterpart of
// the bounded terminal error sent over worker control. The control event keeps
// stable fingerprints because it becomes user-visible durable state; this log
// retains the useful, bounded error text and stderr needed by operators.
// Credential-shaped values are redacted before they leave the worker process.
func logWorkerFailureDiagnostics(
	ctx context.Context,
	logger *slog.Logger,
	manifest runmanifest.Manifest,
	phase, threadID, turnID string,
	result AppServerRunResult,
	failures workerCleanupFailures,
	runCause error,
	stderr []byte,
	stderrCaptureTruncated bool,
	terminal harnesscontrol.TurnTerminalEvent,
	terminalCause error,
) {
	if logger == nil {
		return
	}
	attributes := []slog.Attr{
		slog.String("component", "harness-worker"),
		slog.String("phase", phase),
		slog.String("workspace_id", manifest.WorkspaceID),
		slog.String("session_id", manifest.SessionID),
		slog.String("run_id", manifest.RunID),
		slog.String("run_attempt_id", manifest.RunAttemptID),
		slog.Int64("run_attempt_generation", manifest.RunAttemptGeneration),
		slog.String("thread_id", threadID),
		slog.String("turn_id", turnID),
		slog.String("terminal_status", terminal.Status),
		slog.String("error_code", terminal.ErrorCode),
		slog.String("failure_category", classifyStockTurnFailure(result.Terminal.Turn.Error, stderr)),
		slog.Bool("stderr_capture_truncated", stderrCaptureTruncated),
	}
	appendWorkerDiagnostic(&attributes, "turn_error", result.Terminal.Turn.Error)
	appendWorkerErrorDiagnostic(&attributes, "runner_error", failures.runner)
	appendWorkerErrorDiagnostic(&attributes, "notification_error", failures.notification)
	appendWorkerErrorDiagnostic(&attributes, "close_stdin_error", failures.closeStdin)
	appendWorkerErrorDiagnostic(&attributes, "process_wait_error", failures.processWait)
	appendWorkerErrorDiagnostic(&attributes, "mcp_error", failures.mcp)
	appendWorkerErrorDiagnostic(&attributes, "runtime_error", failures.runtime)
	appendWorkerErrorDiagnostic(&attributes, "run_cause_error", runCause)
	appendWorkerErrorDiagnostic(&attributes, "terminal_cause_error", terminalCause)
	appendWorkerDiagnostic(&attributes, "stderr", stderr)
	logger.LogAttrs(ctx, slog.LevelError, "harness worker turn failed", attributes...)
}

func appendWorkerErrorDiagnostic(attributes *[]slog.Attr, name string, err error) {
	if err == nil {
		return
	}
	appendWorkerDiagnostic(attributes, name, []byte(err.Error()))
}

func appendWorkerDiagnostic(attributes *[]slog.Attr, name string, contents []byte) {
	digest := diagnosticFingerprint(contents)
	if digest == "" {
		return
	}
	diagnostic := safeWorkerDiagnosticText(contents)
	if diagnostic.value == "" {
		return
	}
	*attributes = append(*attributes,
		slog.String(name, diagnostic.value),
		slog.Int(name+"_bytes", diagnostic.originalBytes),
		slog.String(name+"_sha256", digest),
		slog.Bool(name+"_log_truncated", diagnostic.truncated),
		slog.Bool(name+"_redacted", diagnostic.redacted),
	)
}

func safeWorkerDiagnosticText(contents []byte) workerDiagnosticText {
	value := safediagnostic.Sanitize(contents, maximumWorkerDiagnosticFieldBytes)
	return workerDiagnosticText{
		value: value.Value, originalBytes: value.OriginalBytes, truncated: value.Truncated, redacted: value.Redacted,
	}
}
