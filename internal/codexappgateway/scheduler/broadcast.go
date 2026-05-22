// internal/codexappgateway/scheduler/broadcast.go
package scheduler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// ChannelRef is the minimal info needed to send to one workspace_im_channels
// row via imbridge /api/internal/imbridge/send.
type ChannelRef struct {
	ID     string `json:"id"`     // workspace_im_channels.id
	UserID string `json:"userId"` // the IM-side user to message
}

type BroadcastReport struct {
	To     []string          // channel ids attempted
	Errors map[string]string // channel id → error text
}

type Broadcaster struct {
	base, secret string
	http         *http.Client
}

func NewBroadcaster(base, secret string) *Broadcaster {
	return &Broadcaster{base: base, secret: secret, http: &http.Client{Timeout: 10 * time.Second}}
}

// Send fan-outs `text` to every channel, accumulating per-channel errors
// without aborting the rest. Returns a report identifying which channels
// were attempted and which failed. `workspaceID` is informational/audit.
func (b *Broadcaster) Send(ctx context.Context, workspaceID, text string, channels []ChannelRef) BroadcastReport {
	_ = workspaceID
	rep := BroadcastReport{Errors: map[string]string{}}
	for _, c := range channels {
		rep.To = append(rep.To, c.ID)
		body, _ := json.Marshal(map[string]string{
			"channel_id": c.ID,
			"to_user_id": c.UserID,
			"text":       text,
		})
		req, _ := http.NewRequestWithContext(ctx, "POST", b.base+"/api/internal/imbridge/send", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if b.secret != "" {
			req.Header.Set("X-Internal-Secret", b.secret)
		}
		resp, err := b.http.Do(req)
		if err != nil {
			rep.Errors[c.ID] = err.Error()
			continue
		}
		if resp.StatusCode/100 != 2 {
			rep.Errors[c.ID] = fmt.Sprintf("status %d", resp.StatusCode)
		}
		_ = resp.Body.Close()
	}
	return rep
}
