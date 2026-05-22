// internal/codexappgateway/scheduler/agentserver_client_test.go
package scheduler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAgentserverClient_LeaseDue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/internal/scheduled-tasks/lease" { t.Fatalf("path=%s", r.URL.Path) }
		if r.Header.Get("X-Internal-Secret") != "s3cr3t" { t.Fatal("bad secret") }
		var req LeaseRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Limit != 5 || req.Owner != "pod-1/123" { t.Fatalf("req=%+v", req) }
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"sch_a","workspaceId":"ws","seriesId":"sch_a","prompt":"p","timezone":"UTC","processAfter":"2026-05-22T00:00:00Z","timeoutSeconds":600}]`))
	}))
	defer srv.Close()

	c := NewAgentserverClient(srv.URL, "s3cr3t", "pod-1", 123)
	leased, err := c.LeaseDue(context.Background(), LeaseRequest{Limit: 5, LeaseSeconds: 60, Owner: "pod-1/123"})
	if err != nil { t.Fatal(err) }
	if len(leased) != 1 || leased[0].ID != "sch_a" { t.Fatalf("got %#v", leased) }
}
