package imbridge

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// TestForwardMessageChannelRoutingOverridesBinding verifies that
// SetChannelRoutingMode's in-memory value wins over the initial
// binding.RoutingMode captured at StartPoller time. This is the
// core property that makes the toggle take effect without restarting
// the poller.
func TestForwardMessageChannelRoutingOverridesBinding(t *testing.T) {
	b := &Bridge{
		providers:        map[string]Provider{},
		pollers:          map[string]pollerEntry{},
		registeredGroups: map[string]string{},
		channelMention:   map[string]bool{},
		channelRouting:   map[string]string{},
		typingSessions:   map[string]func(){},
	}

	// Override with codex in the in-memory map.
	b.SetChannelRoutingMode("ch-abc", "codex")

	// Simulate forwardMessage's routing decision directly. We cannot
	// invoke forwardMessage end-to-end here without a real provider /
	// HTTP target, so we assert on the effective mode computation.
	got := b.getChannelRoutingMode("ch-abc")
	if got != "codex" {
		t.Fatalf("expected in-memory routing=codex, got %q", got)
	}

	// Missing channel → empty string so forwardMessage falls back to
	// binding.RoutingMode.
	if b.getChannelRoutingMode("unknown") != "" {
		t.Fatalf("expected empty routing for unknown channel")
	}
}

// TestSetChannelRoutingModeConcurrent ensures the setter/getter
// are safe under concurrent access (mirrors SetChannelRequireMention
// concurrency assumptions).
func TestSetChannelRoutingModeConcurrent(t *testing.T) {
	b := &Bridge{
		providers:        map[string]Provider{},
		pollers:          map[string]pollerEntry{},
		registeredGroups: map[string]string{},
		channelMention:   map[string]bool{},
		channelRouting:   map[string]string{},
		typingSessions:   map[string]func(){},
	}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			b.SetChannelRoutingMode("ch1", "codex")
		}()
		go func() {
			defer wg.Done()
			_ = b.getChannelRoutingMode("ch1")
		}()
	}
	wg.Wait()

	if b.getChannelRoutingMode("ch1") != "codex" {
		t.Fatalf("expected codex after concurrent writes")
	}
}

// TestForwardMessage_RoutesManagedCC verifies that forwardMessage routes
// "managed_cc" mode to the /api/internal/imbridge/cc/turn endpoint.
func TestForwardMessage_RoutesManagedCC(t *testing.T) {
	var called bool
	var receivedPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		if r.URL.Path == "/api/internal/imbridge/cc/turn" {
			called = true
			w.WriteHeader(http.StatusAccepted)
			w.Write([]byte(`{"queued":true}`))
		} else {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer ts.Close()

	b := &Bridge{
		agentserverURL:   ts.URL,
		providers:        map[string]Provider{},
		pollers:          map[string]pollerEntry{},
		registeredGroups: map[string]string{},
		channelMention:   map[string]bool{},
		channelRouting:   map[string]string{},
		typingSessions:   map[string]func(){},
	}
	binding := BridgeBinding{RoutingMode: "managed_cc", WorkspaceID: "ws_test", ChannelID: "ch_test"}
	msg := InboundMessage{FromUserID: "wxid_test", Text: "hi"}
	success, err := b.forwardMessage(context.Background(), binding, msg)

	if err != nil {
		t.Fatalf("forwardMessage returned error: %v", err)
	}
	if !success {
		t.Fatalf("forwardMessage returned false, expected true")
	}
	if !called {
		t.Errorf("expected POST to /api/internal/imbridge/cc/turn, got %q", receivedPath)
	}
}

// TestForwardMessage_RoutesManagedCC_PayloadShape verifies that forwardToManagedCC
// sends the correct JSON field names and base64-encodes media, mirroring forwardToCodex.
func TestForwardMessage_RoutesManagedCC_PayloadShape(t *testing.T) {
	var capturedBody []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		capturedBody, err = io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("failed to read body: %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte(`{"queued":true}`))
	}))
	defer ts.Close()

	b := &Bridge{
		agentserverURL:   ts.URL,
		providers:        map[string]Provider{},
		pollers:          map[string]pollerEntry{},
		registeredGroups: map[string]string{},
		channelMention:   map[string]bool{},
		channelRouting:   map[string]string{},
		typingSessions:   map[string]func(){},
	}
	binding := BridgeBinding{RoutingMode: "managed_cc", WorkspaceID: "ws_test", ChannelID: "ch_test"}
	pngMagic := []byte{0x89, 0x50, 0x4e, 0x47} // PNG magic bytes
	msg := InboundMessage{
		FromUserID:    "wxid_alice",
		SenderName:    "Alice",
		Text:          "hello world",
		MediaType:     "image",
		MediaData:     pngMagic,
		QuotedText:    "previous message",
		QuotedSender:  "Bob",
		QuotedMediaData: []byte{0xff, 0xd8, 0xff, 0xe0}, // JPEG magic
		QuotedMediaType: "image",
	}
	success, err := b.forwardMessage(context.Background(), binding, msg)

	if err != nil {
		t.Fatalf("forwardMessage returned error: %v", err)
	}
	if !success {
		t.Fatalf("forwardMessage returned false, expected true")
	}

	var payload map[string]any
	if err := json.Unmarshal(capturedBody, &payload); err != nil {
		t.Fatalf("failed to unmarshal payload: %v", err)
	}

	// Check wechat_sender_name (correct field name)
	if got, ok := payload["wechat_sender_name"]; !ok || got != "Alice" {
		t.Errorf("wechat_sender_name: got %q, want %q", got, "Alice")
	}

	// Ensure wechat_sender (wrong field name) is NOT present
	if _, ok := payload["wechat_sender"]; ok {
		t.Error("payload should not have wechat_sender field (use wechat_sender_name)")
	}

	// Check media_data is base64-encoded
	expectedMediaData := base64.StdEncoding.EncodeToString(pngMagic)
	if got, ok := payload["media_data"]; !ok || got != expectedMediaData {
		t.Errorf("media_data: got %q, want %q (base64)", got, expectedMediaData)
	}

	// Check media_type is present
	if got, ok := payload["media_type"]; !ok || got != "image" {
		t.Errorf("media_type: got %q, want %q", got, "image")
	}

	// Check quoted fields
	if got, ok := payload["quoted_text"]; !ok || got != "previous message" {
		t.Errorf("quoted_text: got %q, want %q", got, "previous message")
	}

	if got, ok := payload["quoted_sender"]; !ok || got != "Bob" {
		t.Errorf("quoted_sender: got %q, want %q", got, "Bob")
	}

	// Check quoted_media_data is base64-encoded
	expectedQuotedMediaData := base64.StdEncoding.EncodeToString([]byte{0xff, 0xd8, 0xff, 0xe0})
	if got, ok := payload["quoted_media_data"]; !ok || got != expectedQuotedMediaData {
		t.Errorf("quoted_media_data: got %q, want %q (base64)", got, expectedQuotedMediaData)
	}

	// Check quoted_media_type is present
	if got, ok := payload["quoted_media_type"]; !ok || got != "image" {
		t.Errorf("quoted_media_type: got %q, want %q", got, "image")
	}
}

// TestForwardMessage_RoutesManagedCC_NoMediaWhenEmpty verifies that media fields
// are only included in the payload when present (mirrors forwardToCodex behavior).
func TestForwardMessage_RoutesManagedCC_NoMediaWhenEmpty(t *testing.T) {
	var capturedBody []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		capturedBody, err = io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("failed to read body: %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte(`{"queued":true}`))
	}))
	defer ts.Close()

	b := &Bridge{
		agentserverURL:   ts.URL,
		providers:        map[string]Provider{},
		pollers:          map[string]pollerEntry{},
		registeredGroups: map[string]string{},
		channelMention:   map[string]bool{},
		channelRouting:   map[string]string{},
		typingSessions:   map[string]func(){},
	}
	binding := BridgeBinding{RoutingMode: "managed_cc", WorkspaceID: "ws_test", ChannelID: "ch_test"}
	msg := InboundMessage{
		FromUserID: "wxid_alice",
		SenderName: "Alice",
		Text:       "text only, no media",
	}
	success, err := b.forwardMessage(context.Background(), binding, msg)

	if err != nil {
		t.Fatalf("forwardMessage returned error: %v", err)
	}
	if !success {
		t.Fatalf("forwardMessage returned false, expected true")
	}

	var payload map[string]any
	if err := json.Unmarshal(capturedBody, &payload); err != nil {
		t.Fatalf("failed to unmarshal payload: %v", err)
	}

	// media_data should NOT be present when empty
	if _, ok := payload["media_data"]; ok {
		t.Error("payload should not have media_data field when message has no media")
	}

	// media_type should NOT be present when empty
	if _, ok := payload["media_type"]; ok {
		t.Error("payload should not have media_type field when message has no media")
	}

	// quoted_media_data should NOT be present when empty
	if _, ok := payload["quoted_media_data"]; ok {
		t.Error("payload should not have quoted_media_data field when message has no quoted media")
	}

	// quoted_media_type should NOT be present when empty
	if _, ok := payload["quoted_media_type"]; ok {
		t.Error("payload should not have quoted_media_type field when message has no quoted media")
	}

	// Required fields should still be present
	if got, ok := payload["wechat_sender_name"]; !ok || got != "Alice" {
		t.Errorf("wechat_sender_name: got %q, want %q", got, "Alice")
	}
	if got, ok := payload["text"]; !ok || got != "text only, no media" {
		t.Errorf("text: got %q, want %q", got, "text only, no media")
	}
}
