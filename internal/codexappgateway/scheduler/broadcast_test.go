// internal/codexappgateway/scheduler/broadcast_test.go
package scheduler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestBroadcaster_FanoutAllChannels(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/internal/imbridge/send" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		if r.Header.Get("X-Internal-Secret") != "shh" {
			t.Fatalf("bad secret: %q", r.Header.Get("X-Internal-Secret"))
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["channel_id"] == "" || body["to_user_id"] == "" || body["text"] == "" {
			t.Fatalf("missing field: %v", body)
		}
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	b := NewBroadcaster(srv.URL, "shh")
	channels := []ChannelRef{
		{ID: "ch1", UserID: "u1"},
		{ID: "ch2", UserID: "u2"},
	}
	report := b.Send(context.Background(), "ws", "hello", channels)
	if calls.Load() != 2 {
		t.Fatalf("calls=%d", calls.Load())
	}
	if len(report.Errors) != 0 {
		t.Fatalf("errors=%v", report.Errors)
	}
	if len(report.To) != 2 {
		t.Fatalf("to=%v", report.To)
	}
}

func TestBroadcaster_PartialFailureDoesNotAbort(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	b := NewBroadcaster(srv.URL, "shh")
	channels := []ChannelRef{
		{ID: "ch1", UserID: "u1"},
		{ID: "ch2", UserID: "u2"},
	}
	report := b.Send(context.Background(), "ws", "hi", channels)
	if calls.Load() != 2 {
		t.Fatalf("calls=%d (expected 2; partial failure must not abort fan-out)", calls.Load())
	}
	if len(report.Errors) != 1 {
		t.Fatalf("errors=%v (expected exactly 1)", report.Errors)
	}
}
