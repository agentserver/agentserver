// internal/codexappgateway/scheduler/script.go
package scheduler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"time"
)

const (
	scriptMaxOutput = 1 << 20 // 1 MiB
	scriptHardLimit = 60 * time.Second
)

// RunPreScript runs a user-supplied bash script and parses its stdout as
// {wakeAgent: bool, data: any}. Mirrors nanoclaw's pre-task script protocol.
func RunPreScript(ctx context.Context, script string, env []string) (wake bool, data json.RawMessage, err error) {
	sctx, cancel := context.WithTimeout(ctx, scriptHardLimit)
	defer cancel()
	cmd := exec.CommandContext(sctx, "bash", "-c", script)
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &limitedWriter{w: &stdout, max: scriptMaxOutput}
	cmd.Stderr = &limitedWriter{w: &stderr, max: scriptMaxOutput}
	if err := cmd.Run(); err != nil {
		return false, nil, fmt.Errorf("script exec: %w (stderr: %s)", err, stderr.String())
	}
	var parsed struct {
		WakeAgent bool            `json:"wakeAgent"`
		Data      json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &parsed); err != nil {
		return false, nil, fmt.Errorf("script must print JSON {wakeAgent,data}: %w", err)
	}
	return parsed.WakeAgent, parsed.Data, nil
}

type limitedWriter struct {
	w   io.Writer
	max int
	n   int
}

func (lw *limitedWriter) Write(p []byte) (int, error) {
	rem := lw.max - lw.n
	if rem <= 0 {
		return len(p), nil
	} // silently drop overflow
	if len(p) > rem {
		p = p[:rem]
	}
	n, err := lw.w.Write(p)
	lw.n += n
	return n, err
}
