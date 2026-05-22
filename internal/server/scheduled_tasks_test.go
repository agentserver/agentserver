package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/internal/auth"
	"github.com/agentserver/agentserver/internal/db"
)

// mustCreateWorkspaceAndMember inserts a workspace + user + membership for tests.
// Returns the workspace ID. Cleans up on t.Cleanup.
func mustCreateWorkspaceAndMember(t *testing.T, d *db.DB, userID string) string {
	t.Helper()
	wsID := "ws_sched_" + strings.ReplaceAll(t.Name(), "/", "_")
	if _, err := d.Exec(`INSERT INTO workspaces (id, name) VALUES ($1, $2) ON CONFLICT DO NOTHING`, wsID, "test "+t.Name()); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO users (id, username, email) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, userID, "u_"+userID, userID+"@e"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO workspace_members (workspace_id, user_id, role) VALUES ($1, $2, 'owner') ON CONFLICT DO NOTHING`, wsID, userID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		d.Exec(`DELETE FROM workspace_members WHERE workspace_id = $1`, wsID)
		d.Exec(`DELETE FROM workspaces WHERE id = $1`, wsID)
		d.Exec(`DELETE FROM users WHERE id = $1`, userID)
	})
	return wsID
}

func TestScheduledTasks_CreateListCancel(t *testing.T) {
	srv, cleanup := newTestServerTUI(t, "")
	defer cleanup()

	const userID = "u_test_sched"
	wsID := mustCreateWorkspaceAndMember(t, srv.DB, userID)

	makeReq := func(method, path string, body []byte) *http.Request {
		var r *http.Request
		if body != nil {
			r = httptest.NewRequest(method, path, bytes.NewReader(body))
		} else {
			r = httptest.NewRequest(method, path, nil)
		}
		r = r.WithContext(auth.ContextWithUserID(context.Background(), userID))
		r.Header.Set("Content-Type", "application/json")
		// Inject chi URL params via chi context — but we use the router directly below.
		return r
	}

	router := srv.Router()

	// CREATE
	createBody := []byte(`{
		"prompt": "say hi",
		"processAfter": "2099-01-01T00:00:00Z",
		"recurrence": "*/5 * * * *",
		"timezone": "UTC"
	}`)
	r := makeReq("POST", "/api/workspaces/"+wsID+"/scheduled-tasks", createBody)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: got %d, body: %s", w.Code, w.Body.String())
	}
	var created struct {
		TaskID string `json:"taskId"`
		RunsAt string `json:"runsAt"`
	}
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.TaskID == "" {
		t.Fatal("no taskId in create response")
	}

	// LIST
	r = makeReq("GET", "/api/workspaces/"+wsID+"/scheduled-tasks", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("list: got %d, body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), created.TaskID) {
		t.Fatalf("list response missing taskId %s: %s", created.TaskID, w.Body.String())
	}

	// CANCEL
	r = makeReq("POST", "/api/workspaces/"+wsID+"/scheduled-tasks/"+created.TaskID+"/cancel", []byte{})
	w = httptest.NewRecorder()
	router.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("cancel: got %d, body: %s", w.Code, w.Body.String())
	}
}
