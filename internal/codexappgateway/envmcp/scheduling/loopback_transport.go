package scheduling

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// LoopbackTransport implements ScheduleTransport by POSTing to the
// app-gateway loopback endpoint at
// http://127.0.0.1:<port>/internal/scheduled-tasks/<action>.
// All 6 actions are sent as POST (the proxy translates list → GET upstream).
type LoopbackTransport struct {
	baseURL string // e.g. "http://127.0.0.1:8086/internal/scheduled-tasks"
	lbToken string // X-Loopback-Token value
	http    *http.Client
}

// NewLoopbackTransport returns a ScheduleTransport that forwards to the
// app-gateway loopback. base should be the full prefix including the
// "/internal/scheduled-tasks" path segment (no trailing slash).
func NewLoopbackTransport(base, token string) *LoopbackTransport {
	return &LoopbackTransport{
		baseURL: base,
		lbToken: token,
		http:    &http.Client{Timeout: 10 * time.Second},
	}
}

// Call POSTs body as JSON to baseURL/<action> with X-Loopback-Token set.
func (t *LoopbackTransport) Call(ctx context.Context, action string, body any) (json.RawMessage, error) {
	raw, _ := json.Marshal(body)
	raw = injectTimezone(raw)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL+"/"+action, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Loopback-Token", t.lbToken)
	resp, err := t.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("loopback %s: status %d: %s", action, resp.StatusCode, string(out))
	}
	return out, nil
}

// injectTimezone adds {"timezone": os.Getenv("TZ")} into a JSON object body
// when the body is an object, "timezone" is absent, and TZ is non-empty.
// Non-object bodies (or already-set timezone) pass through unchanged.
func injectTimezone(body json.RawMessage) json.RawMessage {
	tz := os.Getenv("TZ")
	if tz == "" {
		return body
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return body // not an object → pass through
	}
	if _, present := m["timezone"]; present {
		return body
	}
	tzBytes, _ := json.Marshal(tz)
	m["timezone"] = tzBytes
	out, err := json.Marshal(m)
	if err != nil {
		return body
	}
	return out
}
