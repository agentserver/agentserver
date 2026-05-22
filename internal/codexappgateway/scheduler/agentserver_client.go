// internal/codexappgateway/scheduler/agentserver_client.go
package scheduler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

type LeaseRequest struct {
	Limit        int    `json:"limit"`
	LeaseSeconds int    `json:"leaseSeconds"`
	Owner        string `json:"owner"`
}

type Task struct {
	ID             string  `json:"id"`
	WorkspaceID    string  `json:"workspaceId"`
	SeriesID       string  `json:"seriesId"`
	Prompt         string  `json:"prompt"`
	Script         *string `json:"script,omitempty"`
	Timezone       string  `json:"timezone"`
	Recurrence     *string `json:"recurrence,omitempty"`
	ProcessAfter   string  `json:"processAfter"`
	TimeoutSeconds int     `json:"timeoutSeconds"`
	RunID          string  `json:"runId,omitempty"` // populated server-side
}

type ResultRequest struct {
	TaskID          string          `json:"taskId"`
	RunID           string          `json:"runId"`
	Status          string          `json:"status"`
	ExitCode        int             `json:"exitCode"`
	DurationMS      int64           `json:"durationMs"`
	Summary         string          `json:"summary"`
	TranscriptURI   string          `json:"transcriptUri"`
	CostUSD         *float64        `json:"costUsd,omitempty"`
	NumTurns        *int            `json:"numTurns,omitempty"`
	BroadcastTo     []string        `json:"broadcastTo"`
	BroadcastErrors json.RawMessage `json:"broadcastErrors"`
}

type AgentserverClient struct {
	base, secret, owner string
	http                *http.Client
}

func NewAgentserverClient(base, secret, pod string, pid int) *AgentserverClient {
	return &AgentserverClient{
		base:   base,
		secret: secret,
		owner:  pod + "/" + strconv.Itoa(pid),
		http:   &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *AgentserverClient) LeaseDue(ctx context.Context, req LeaseRequest) ([]Task, error) {
	if req.Owner == "" { req.Owner = c.owner }
	var out []Task
	if err := c.post(ctx, "/api/internal/scheduled-tasks/lease", req, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *AgentserverClient) PostResult(ctx context.Context, req ResultRequest) error {
	return c.post(ctx, "/api/internal/scheduled-tasks/result", req, nil)
}

// ListChannels fetches the IM channels for a workspace so the dispatcher can
// fan-out broadcast results. Called by Dispatcher.report.
func (c *AgentserverClient) ListChannels(ctx context.Context, workspaceID string) ([]ChannelRef, error) {
	var out []ChannelRef
	req, err := http.NewRequestWithContext(ctx, "GET",
		c.base+"/api/internal/workspaces/"+workspaceID+"/im-channels", nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("X-Internal-Secret", c.secret)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list channels: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("list channels: status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode channels: %w", err)
	}
	return out, nil
}

func (c *AgentserverClient) post(ctx context.Context, path string, body, out any) error {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(ctx, "POST", c.base+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Secret", c.secret)
	resp, err := c.http.Do(req)
	if err != nil { return fmt.Errorf("%s: %w", path, err) }
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%s: status %d", path, resp.StatusCode)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
