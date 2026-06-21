package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// RunResult is the return value of Run: the assistant text, result metadata,
// wall-clock duration, and subprocess exit code.
type RunResult struct {
	AssistantText string
	Meta          *ResultMeta
	DurationMs    int64
	ExitCode      int
}

// Run spawns claude with BuildArgs/BuildEnv, writes one SDKUserMessage to stdin,
// closes stdin, drains stdout through Decode + KeepFrame + ExtractAssistantText,
// then waits for the subprocess to exit.
//
// Wall-clock timeout from in.Timeout is enforced via context.WithTimeout.
// On timeout: SIGTERM the process, give it 5 seconds, then SIGKILL.
// The returned error wraps context.DeadlineExceeded and mentions "timeout".
//
// stderr is captured and logged only if the subprocess exits non-zero.
func Run(ctx context.Context, in RunInput) (*RunResult, error) {
	// Apply wall-clock timeout.
	runCtx, cancel := context.WithTimeout(ctx, in.Timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, in.ClaudeBin, BuildArgs(in)...)
	cmd.Dir = in.ProjectDir
	parentEnv := in.ParentEnv
	if parentEnv == nil {
		parentEnv = os.Environ()
	}
	cmd.Env = BuildEnv(in, parentEnv)

	// Use SIGTERM on context cancellation, then WaitDelay gives SIGKILL after 5s.
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			return cmd.Process.Signal(syscall.SIGTERM)
		}
		return nil
	}
	cmd.WaitDelay = 5 * time.Second

	// Set up pipes.
	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("runner: StdinPipe: %w", err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("runner: StdoutPipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("runner: StderrPipe: %w", err)
	}

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("runner: Start: %w", err)
	}

	// Capture stderr in the background; log only on non-zero exit.
	var stderrBuf bytes.Buffer
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		io.Copy(&stderrBuf, stderrPipe) //nolint:errcheck
	}()

	// Write the user message to stdin, then close it. Ignore the write error:
	// if the subprocess already exited (e.g. stdin_early_close scenario) the
	// write will fail with "broken pipe" — that's harmless; we'll see the
	// real failure when we extract from stdout.
	writeErr := EncodeUserMessage(stdinPipe, in.UserMessage)
	_ = writeErr
	stdinPipe.Close() //nolint:errcheck

	// Decode stdout frames, filter them, and extract assistant text.
	raw, decodeErrors := Decode(stdoutPipe)

	kept := make(chan SDKMessage, 16)
	go func() {
		defer close(kept)
		for m := range raw {
			if KeepFrame(m) {
				kept <- m
			}
		}
	}()

	assistantText, meta, extractErr := ExtractAssistantText(kept)

	// Collect decode error (non-blocking; channel has buffer 1).
	var decodeErr error
	select {
	case decodeErr = <-decodeErrors:
	default:
	}

	// Wait for the subprocess to exit and for stderr to be fully read.
	waitErr := cmd.Wait()
	<-stderrDone

	durationMs := time.Since(start).Milliseconds()

	// Determine exit code.
	exitCode := 0
	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		exitCode = exitErr.ExitCode()
		if stderrBuf.Len() > 0 {
			log.Printf("[cc-app-gateway/runner] claude subprocess exited %d; stderr:\n%s", exitCode, stderrBuf.String())
		}
	}

	// Error precedence:
	// 1. Context timeout → wrap DeadlineExceeded, mention "timeout".
	// 2. Decode error (parse failure) → return wrapped.
	// 3. Extract error (no result frame, etc.) → return wrapped.
	// 4. Non-zero exit code → return wrapped.
	if runCtx.Err() != nil && errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return nil, fmt.Errorf("runner: timeout after %v: %w", in.Timeout, context.DeadlineExceeded)
	}

	if decodeErr != nil {
		return nil, fmt.Errorf("runner: stream decode error: %w", decodeErr)
	}

	if extractErr != nil {
		return nil, fmt.Errorf("runner: extract error: %w", extractErr)
	}

	if exitCode != 0 {
		return nil, fmt.Errorf("runner: claude subprocess exited with code %d", exitCode)
	}

	return &RunResult{
		AssistantText: assistantText,
		Meta:          meta,
		DurationMs:    durationMs,
		ExitCode:      exitCode,
	}, nil
}
