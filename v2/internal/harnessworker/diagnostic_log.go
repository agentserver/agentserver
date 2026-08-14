package harnessworker

import (
	"context"
	"log/slog"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/agentserver/agentserver/v2/internal/harnesscontrol"
	"github.com/agentserver/agentserver/v2/internal/runmanifest"
)

const maximumWorkerDiagnosticFieldBytes = 16 * 1024

var (
	workerDiagnosticJSONSecret     = regexp.MustCompile(`(?i)("(?:access[_-]?token|refresh[_-]?token|id[_-]?token|x[_-]?jwt[_-]?token|authorization|api[_-]?key|client[_-]?secret|secret[_-]?access[_-]?key|password|credential|capability|token|secret)"\s*:\s*")[^"]*(")`)
	workerDiagnosticKeyValueSecret = regexp.MustCompile(`(?i)\b(access[_-]?token|refresh[_-]?token|id[_-]?token|x[_-]?jwt[_-]?token|authorization|api[_-]?key|client[_-]?secret|secret[_-]?access[_-]?key|password|credential|capability|token|secret)(\s*[:=]\s*)([^\s,;]+)`)
	workerDiagnosticBearerSecret   = regexp.MustCompile(`(?i)\b(bearer)(\s+)[A-Za-z0-9._~+/=-]{8,}`)
	workerDiagnosticJWTSecret      = regexp.MustCompile(`\b[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`)
	workerDiagnosticANSI           = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
)

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
	if len(contents) == 0 {
		return workerDiagnosticText{}
	}
	value := strings.ToValidUTF8(string(contents), "�")
	value = strings.ReplaceAll(value, "\x00", "�")
	value = workerDiagnosticANSI.ReplaceAllString(value, "")
	redacted := workerDiagnosticJSONSecret.ReplaceAllString(value, `${1}<redacted>${2}`)
	redacted = workerDiagnosticBearerSecret.ReplaceAllString(redacted, `${1}${2}<redacted>`)
	redacted = workerDiagnosticKeyValueSecret.ReplaceAllString(redacted, `${1}${2}<redacted>`)
	redacted = workerDiagnosticJWTSecret.ReplaceAllString(redacted, `<redacted-jwt>`)
	changed := redacted != value
	truncated := len(redacted) > maximumWorkerDiagnosticFieldBytes
	if truncated {
		limit := maximumWorkerDiagnosticFieldBytes
		for limit > 0 && !utf8.ValidString(redacted[:limit]) {
			limit--
		}
		redacted = redacted[:limit] + "…(truncated)"
	}
	return workerDiagnosticText{
		value: redacted, originalBytes: len(contents), truncated: truncated, redacted: changed,
	}
}
