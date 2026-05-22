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

// Write silently drops bytes beyond max. Always reports len(p) consumed (no
// io.ErrShortWrite) so io.Copy keeps draining the underlying pipe — without
// this, a long-running bash script that printed > max bytes would block on
// stdout once io.Copy stopped reading, hanging us until scriptHardLimit.
func (lw *limitedWriter) Write(p []byte) (int, error) {
	rem := lw.max - lw.n
	if rem <= 0 {
		return len(p), nil
	}
	toWrite := p
	if len(p) > rem {
		toWrite = p[:rem]
	}
	n, err := lw.w.Write(toWrite)
	lw.n += n
	if err != nil {
		return n, err
	}
	return len(p), nil
}
