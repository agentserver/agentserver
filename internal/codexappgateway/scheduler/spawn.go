// internal/codexappgateway/scheduler/spawn.go
package scheduler

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"
)

type SpawnInput struct {
	Prompt  string
	Env     []string
	Timeout time.Duration
}

type SpawnResult struct {
	ExitCode   int
	Transcript string  // full captured stdout (truncated at 256 KiB)
	Summary    string  // last non-empty line, truncated to 4 KiB
	CostUSD    *float64
	NumTurns   *int
	TimedOut   bool
	DurationMS int64
}

type Spawner struct {
	bin      string
	extraEnv []string
}

func NewSpawner(bin string, extraEnv []string) *Spawner {
	return &Spawner{bin: bin, extraEnv: extraEnv}
}

const (
	transcriptCap = 256 << 10
	summaryCap    = 4 << 10
)

func (s *Spawner) Run(ctx context.Context, in SpawnInput) (SpawnResult, error) {
	if in.Timeout <= 0 {
		in.Timeout = 10 * time.Minute
	}
	cctx, cancel := context.WithTimeout(ctx, in.Timeout)
	defer cancel()

	// Default invocation: `codex exec --json -` (stdin = prompt). Adjust
	// args once we wire the real supervisor in Task 11.
	cmd := exec.CommandContext(cctx, s.bin, "exec", "--json", "-")
	cmd.Env = append([]string{}, in.Env...)
	cmd.Env = append(cmd.Env, s.extraEnv...)
	cmd.Stdin = strings.NewReader(in.Prompt)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &limitedWriter{w: &stdout, max: transcriptCap}
	cmd.Stderr = &limitedWriter{w: &stderr, max: 64 << 10}

	start := time.Now()
	err := cmd.Run()
	dur := time.Since(start).Milliseconds()

	res := SpawnResult{
		ExitCode:   exitCodeOf(err, cmd),
		Transcript: stdout.String(),
		DurationMS: dur,
		TimedOut:   errors.Is(cctx.Err(), context.DeadlineExceeded),
	}
	res.Summary = lastNonEmpty(res.Transcript, summaryCap)
	// CostUSD / NumTurns parsing is best-effort and codex-version specific;
	// see Task 11 for the parser hook. Empty placeholders are fine for tests.
	return res, nil
}

func exitCodeOf(err error, cmd *exec.Cmd) int {
	if err == nil {
		return 0
	}
	if cmd.ProcessState != nil {
		return cmd.ProcessState.ExitCode()
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

func lastNonEmpty(s string, cap int) string {
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		ln := strings.TrimSpace(lines[i])
		if ln != "" {
			if len(ln) > cap {
				ln = ln[:cap]
			}
			return ln
		}
	}
	return ""
}
