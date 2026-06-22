//go:build integration

// Package server provides stubs for the e2e_tui_test.go test which references
// the old TUI API (removed with the stateless-cc stack). The stubs make the
// file compile but always skip the test.
package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// newTestServerForEvents returns nil to signal that the TUI E2E test should be
// skipped. The TUI API (handleTUIEventStream, handleTUIInbound,
// handlePermissionDecision) was removed with the stateless-cc stack.
func newTestServerForEvents(t *testing.T, _ string) (*Server, func()) {
	t.Helper()
	t.Skip("TUI API removed — e2e_tui_test.go is a relic; skipping")
	return nil, nil
}

// mustAuthRequest constructs a fake authenticated HTTP request for TUI tests.
// Only reached if newTestServerForEvents returns non-nil (it never does).
func mustAuthRequest(t *testing.T, method, path, body string) *http.Request {
	t.Helper()
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	return r
}

// handleTUIEventStream is a stub for the removed TUI event-stream handler.
func (s *Server) handleTUIEventStream(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "TUI API removed", http.StatusGone)
}

// handleTUIInbound is a stub for the removed TUI inbound handler.
func (s *Server) handleTUIInbound(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "TUI API removed", http.StatusGone)
}

// handlePermissionDecision is a stub for the removed TUI permission handler.
func (s *Server) handlePermissionDecision(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "TUI API removed", http.StatusGone)
}
