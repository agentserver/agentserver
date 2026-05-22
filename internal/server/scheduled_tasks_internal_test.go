package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestScheduledTasks_LeaseAndResult(t *testing.T) {
	secret := "test-internal-secret"
	t.Setenv("INTERNAL_API_SECRET", secret)
	srv, cleanup := newTestServerTUI(t, "")
	defer cleanup()

	wsID := "ws_sched_lease_" + strings.ReplaceAll(t.Name(), "/", "_")
	if _, err := srv.DB.Exec(`INSERT INTO workspaces (id, name) VALUES ($1, $2) ON CONFLICT DO NOTHING`, wsID, "test"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.DB.Exec(`DELETE FROM workspaces WHERE id = $1`, wsID) })

	// Seed one due, recurring task.
	if _, err := srv.DB.Exec(`INSERT INTO scheduled_tasks
		(id, workspace_id, series_id, creator_kind, prompt, timezone, recurrence, process_after, status, timeout_seconds)
		VALUES ('sch_x', $1, 'sch_x', 'rest', 'say hi', 'UTC', '*/5 * * * *', NOW() - interval '1 second', 'pending', 30)`, wsID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.DB.Exec(`DELETE FROM scheduled_tasks WHERE series_id = 'sch_x'`) })

	// LEASE
	r := httptest.NewRequest("POST", "/api/internal/scheduled-tasks/lease",
		strings.NewReader(`{"limit":10,"leaseSeconds":60,"owner":"test/1"}`))
	r.Header.Set("X-Internal-Secret", secret)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("lease: %d %s", w.Code, w.Body.String())
	}
	var leased []map[string]any
	if err := json.NewDecoder(w.Body).Decode(&leased); err != nil {
		t.Fatal(err)
	}
	if len(leased) != 1 || leased[0]["id"] != "sch_x" {
		t.Fatalf("leased = %v", leased)
	}
	// Verify runId is present in the response.
	if leased[0]["runId"] == "" || leased[0]["runId"] == nil {
		t.Fatalf("runId missing from lease response: %v", leased[0])
	}

	// Fetch the real runId that the lease handler stamped on the row.
	var realRunID string
	if err := srv.DB.QueryRow(`SELECT last_run_id FROM scheduled_tasks WHERE id = 'sch_x'`).Scan(&realRunID); err != nil {
		t.Fatalf("fetch last_run_id: %v", err)
	}
	if realRunID == "" {
		t.Fatal("last_run_id was not stamped on the task row")
	}

	// RESULT (succeeded, recurring → expect a new sibling row)
	body := fmt.Sprintf(`{
	  "taskId":"sch_x","runId":%q,"status":"succeeded",
	  "summary":"ok","durationMs":120,"exitCode":0,
	  "broadcastTo":[],"broadcastErrors":{}
	}`, realRunID)
	r = httptest.NewRequest("POST", "/api/internal/scheduled-tasks/result",
		strings.NewReader(body))
	r.Header.Set("X-Internal-Secret", secret)
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	srv.Router().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("result: %d %s", w.Code, w.Body.String())
	}

	// Expect: original row 'completed', a new sibling row 'pending' in same series.
	var liveCount, completedCount int
	if err := srv.DB.QueryRow(
		`SELECT
		   COUNT(*) FILTER (WHERE status='pending'),
		   COUNT(*) FILTER (WHERE status='completed')
		 FROM scheduled_tasks WHERE series_id='sch_x'`).Scan(&liveCount, &completedCount); err != nil {
		t.Fatal(err)
	}
	if liveCount != 1 || completedCount != 1 {
		t.Fatalf("series rows: live=%d completed=%d", liveCount, completedCount)
	}
}
