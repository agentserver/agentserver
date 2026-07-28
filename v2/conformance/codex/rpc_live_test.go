package codex_test

import (
	"context"
	"testing"

	"github.com/agentserver/agentserver/v2/conformance/codex/internal/codexprocess"
	"github.com/agentserver/agentserver/v2/internal/codexwire"
)

// rpcCollector is the single consumer for one Codex stdio connection. It
// matches responses by request id and retains notifications that can race ahead
// of a response or another notification the probe is currently awaiting.
type rpcCollector struct {
	process       *codexprocess.Process
	responses     map[string]codexwire.Message
	notifications []codexwire.Message
}

func newRPCCollector(process *codexprocess.Process) *rpcCollector {
	return &rpcCollector{process: process, responses: make(map[string]codexwire.Message)}
}

func (c *rpcCollector) response(t *testing.T, id string) codexwire.Message {
	t.Helper()
	if response, exists := c.responses[id]; exists {
		delete(c.responses, id)
		return response
	}
	for {
		message := c.receive(t)
		switch message.Kind {
		case codexwire.KindResponse, codexwire.KindError:
			messageID := string(message.ID)
			if messageID == id {
				return message
			}
			if _, duplicate := c.responses[messageID]; duplicate {
				t.Fatalf("duplicate response id %s", messageID)
			}
			c.responses[messageID] = message
		case codexwire.KindNotification:
			c.notifications = append(c.notifications, message)
		case codexwire.KindRequest:
			t.Fatalf("unexpected Codex reverse request %q while waiting for response %s", message.Method, id)
		default:
			t.Fatalf("unexpected Codex wire message kind %s", message.Kind)
		}
	}
}

func (c *rpcCollector) nextNotification(t *testing.T) codexwire.Message {
	t.Helper()
	if len(c.notifications) != 0 {
		message := c.notifications[0]
		c.notifications = c.notifications[1:]
		return message
	}
	for {
		message := c.receive(t)
		switch message.Kind {
		case codexwire.KindNotification:
			return message
		case codexwire.KindResponse, codexwire.KindError:
			messageID := string(message.ID)
			if _, duplicate := c.responses[messageID]; duplicate {
				t.Fatalf("duplicate response id %s", messageID)
			}
			c.responses[messageID] = message
		case codexwire.KindRequest:
			t.Fatalf("unexpected Codex reverse request %q while waiting for notification", message.Method)
		default:
			t.Fatalf("unexpected Codex wire message kind %s", message.Kind)
		}
	}
}

func (c *rpcCollector) notification(t *testing.T, method string) codexwire.Message {
	t.Helper()
	for index, message := range c.notifications {
		if message.Method == method {
			c.notifications = append(c.notifications[:index], c.notifications[index+1:]...)
			return message
		}
	}
	for {
		message := c.receive(t)
		switch message.Kind {
		case codexwire.KindNotification:
			if message.Method == method {
				return message
			}
			if len(c.notifications) >= 1024 {
				t.Fatalf("more than 1024 unmatched Codex notifications while waiting for %q", method)
			}
			c.notifications = append(c.notifications, message)
		case codexwire.KindResponse, codexwire.KindError:
			messageID := string(message.ID)
			if _, duplicate := c.responses[messageID]; duplicate {
				t.Fatalf("duplicate response id %s", messageID)
			}
			c.responses[messageID] = message
		case codexwire.KindRequest:
			t.Fatalf("unexpected Codex reverse request %q while waiting for notification %q", message.Method, method)
		default:
			t.Fatalf("unexpected Codex wire message kind %s", message.Kind)
		}
	}
}

func (c *rpcCollector) receive(t *testing.T) codexwire.Message {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), liveProbeTimeout)
	defer cancel()
	message, err := c.process.Peer.Receive(ctx)
	if err != nil {
		stderr, truncated := c.process.Stderr()
		t.Fatalf("receive Codex message: %v (stderr_truncated=%t)\nstderr: %s", err, truncated, stderr)
	}
	return message
}

func sendRPC(t *testing.T, process *codexprocess.Process, request any) {
	t.Helper()
	if err := process.Peer.Send(request); err != nil {
		t.Fatalf("send Codex request: %v", err)
	}
}

func mustDecodeResult(t *testing.T, message codexwire.Message, destination any) {
	t.Helper()
	if message.Kind == codexwire.KindError {
		t.Fatalf("Codex returned error %d: %s", message.Error.Code, message.Error.Message)
	}
	if err := message.DecodeResult(destination); err != nil {
		t.Fatal(err)
	}
}

func mustRPCError(t *testing.T, message codexwire.Message) {
	t.Helper()
	if message.Kind != codexwire.KindError || message.Error == nil || message.Error.Message == "" {
		t.Fatalf("message kind = %s, want non-empty RPC error", message.Kind)
	}
}
