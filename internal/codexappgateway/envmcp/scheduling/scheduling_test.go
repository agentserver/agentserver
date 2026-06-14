package scheduling

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/internal/envtools/tools"
)

func TestSchedulingTools_MatchGolden(t *testing.T) {
	ts := NewSchedulingTools(nil) // nil transport is fine for metadata
	got := struct {
		Tools []map[string]any `json:"tools"`
	}{}
	for _, tool := range ts {
		var schema map[string]any
		_ = json.Unmarshal(tool.InputSchema(), &schema)
		got.Tools = append(got.Tools, map[string]any{
			"name":        tool.Name(),
			"description": tool.Description(),
			"inputSchema": schema,
		})
	}
	gotBytes, _ := json.MarshalIndent(got, "", "  ")

	want, err := os.ReadFile("testdata/scheduling.golden.json")
	if err != nil {
		t.Fatal(err)
	}

	// Normalize: re-marshal both via map[string]any so key ordering is consistent.
	var a, b any
	_ = json.Unmarshal(want, &a)
	_ = json.Unmarshal(gotBytes, &b)
	wantNorm, _ := json.Marshal(a)
	gotNorm, _ := json.Marshal(b)
	if string(wantNorm) != string(gotNorm) {
		t.Errorf("MCP surface drift!\nwant:\n%s\ngot:\n%s", wantNorm, gotNorm)
	}
}

func TestScheduleTask_ForwardsToTransport(t *testing.T) {
	var captured struct {
		action string
		body   map[string]any
	}
	transport := transportFunc(func(_ context.Context, action string, body any) (json.RawMessage, error) {
		captured.action = action
		b, _ := json.Marshal(body)
		_ = json.Unmarshal(b, &captured.body)
		return json.RawMessage(`{"taskId":"sch_x","runsAt":"2099-01-01T00:00:00Z","status":"pending","timezone":"UTC"}`), nil
	})
	ts := NewSchedulingTools(transport)
	sch := findTool(t, ts, "schedule_task")
	res, err := sch.Call(context.Background(), json.RawMessage(`{"prompt":"hi","processAfter":"2099-01-01T00:00:00Z"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("got isError; content=%+v", res.Content)
	}
	if captured.action != "schedule" {
		t.Fatalf("action=%s", captured.action)
	}
	if captured.body["prompt"] != "hi" {
		t.Fatalf("body=%v", captured.body)
	}
	want := "Task scheduled (id: sch_x, runs at: 2099-01-01T00:00:00Z)"
	if len(res.Content) == 0 || res.Content[0].Text != want {
		t.Fatalf("expected formatted response %q, got %+v", want, res.Content)
	}
}

func TestFormatScheduleResponse_WithRecurrence(t *testing.T) {
	recur := "0 9 * * 1-5"
	raw, _ := json.Marshal(map[string]any{
		"taskId":     "sch_abc",
		"runsAt":     "2026-06-01T09:00:00Z",
		"recurrence": recur,
	})
	got := formatScheduleResponse(raw)
	want := "Task scheduled (id: sch_abc, runs at: 2026-06-01T09:00:00Z, recurrence: 0 9 * * 1-5)"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestFormatListResponse_Empty(t *testing.T) {
	got := formatListResponse(json.RawMessage(`[]`))
	if got != "No tasks found." {
		t.Fatalf("got %q", got)
	}
}

func TestFormatListResponse_WithRows(t *testing.T) {
	raw := json.RawMessage(`[{"taskId":"sch_1","status":"pending","runsAt":"2026-06-01T09:00:00Z","prompt":"do the thing"}]`)
	got := formatListResponse(raw)
	if !strings.Contains(got, "sch_1") || !strings.Contains(got, "pending") || !strings.Contains(got, "do the thing") {
		t.Fatalf("unexpected list format: %q", got)
	}
}

func TestFormatActionResponse_CancelNoMatch(t *testing.T) {
	args := json.RawMessage(`{"taskId":"sch_x"}`)
	raw := json.RawMessage(`{"cancelled":0}`)
	got := formatActionResponse("cancel", args)(raw)
	want := `Task cancellation: no live task matched id "sch_x".`
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestFormatActionResponse_CancelMatch(t *testing.T) {
	args := json.RawMessage(`{"taskId":"sch_y"}`)
	raw := json.RawMessage(`{"cancelled":1}`)
	got := formatActionResponse("cancel", args)(raw)
	want := "Task cancellation requested: sch_y"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestUpdateTask_RejectsEmptyUpdate(t *testing.T) {
	ts := NewSchedulingTools(nil)
	upd := findTool(t, ts, "update_task")
	res, err := upd.Call(context.Background(), json.RawMessage(`{"taskId":"sch_x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("expected isError for empty update, got: %+v", res.Content)
	}
	if len(res.Content) == 0 || res.Content[0].Text != "Error: at least one field to update is required" {
		t.Fatalf("unexpected error content: %+v", res.Content)
	}
}

func TestHTTPTransport_InjectsTimezone(t *testing.T) {
	t.Setenv("TZ", "Asia/Shanghai")
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = string(b)
		w.WriteHeader(200)
		w.Write([]byte("{}"))
	}))
	defer srv.Close()

	tr := NewHTTPTransport(srv.URL, "ws_a", "cap-tok", nil)
	_, err := tr.Call(context.Background(), "schedule", json.RawMessage(`{"prompt":"x","processAfter":"2099-01-01T09:00:00"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `"timezone":"Asia/Shanghai"`) {
		t.Fatalf("expected timezone injected, got: %s", got)
	}
}

func TestHTTPTransport_RespectsExplicitTimezone(t *testing.T) {
	t.Setenv("TZ", "Asia/Shanghai")
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = string(b)
		w.WriteHeader(200)
		w.Write([]byte("{}"))
	}))
	defer srv.Close()
	tr := NewHTTPTransport(srv.URL, "ws_a", "cap-tok", nil)
	_, err := tr.Call(context.Background(), "schedule", json.RawMessage(`{"prompt":"x","processAfter":"...","timezone":"UTC"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `"timezone":"UTC"`) || strings.Contains(got, "Asia/Shanghai") {
		t.Fatalf("explicit tz must win; got: %s", got)
	}
}

// TestHTTPTransport_RoutesPerAction pins the action→(method, path)
// translation that used to live in app-gateway's loopback handler.
// One row per supported action; verifies the right verb + URL +
// Authorization: Bearer.
func TestHTTPTransport_RoutesPerAction(t *testing.T) {
	type seen struct {
		method, path, authz string
		body                string
	}
	var got seen
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = seen{r.Method, r.URL.Path + (func() string {
			if r.URL.RawQuery != "" {
				return "?" + r.URL.RawQuery
			}
			return ""
		})(), r.Header.Get("Authorization"), string(b)}
		w.WriteHeader(200)
		w.Write([]byte("{}"))
	}))
	defer srv.Close()
	tr := NewHTTPTransport(srv.URL, "ws_a", "cap-tok", nil)

	cases := []struct {
		action, body, method, path string
	}{
		{"schedule", `{"prompt":"p","processAfter":"...","timezone":"UTC"}`, "POST", "/api/internal/workspaces/ws_a/scheduled-tasks"},
		{"list", `{"timezone":"UTC"}`, "GET", "/api/internal/workspaces/ws_a/scheduled-tasks"},
		{"list", `{"status":"pending","timezone":"UTC"}`, "GET", "/api/internal/workspaces/ws_a/scheduled-tasks?status=pending"},
		{"cancel", `{"taskId":"sch_x","timezone":"UTC"}`, "POST", "/api/internal/workspaces/ws_a/scheduled-tasks/sch_x/cancel"},
		{"pause", `{"taskId":"sch_x","timezone":"UTC"}`, "POST", "/api/internal/workspaces/ws_a/scheduled-tasks/sch_x/pause"},
		{"resume", `{"taskId":"sch_x","timezone":"UTC"}`, "POST", "/api/internal/workspaces/ws_a/scheduled-tasks/sch_x/resume"},
		{"update", `{"taskId":"sch_x","prompt":"new","timezone":"UTC"}`, "PATCH", "/api/internal/workspaces/ws_a/scheduled-tasks/sch_x"},
	}
	for _, c := range cases {
		_, err := tr.Call(context.Background(), c.action, json.RawMessage(c.body))
		if err != nil {
			t.Fatalf("%s: %v", c.action, err)
		}
		if got.method != c.method || got.path != c.path {
			t.Errorf("%s: method=%q path=%q, want %q %q", c.action, got.method, got.path, c.method, c.path)
		}
		if got.authz != "Bearer cap-tok" {
			t.Errorf("%s: Authorization = %q, want Bearer cap-tok", c.action, got.authz)
		}
	}
}

func TestHTTPTransport_CancelMissingTaskID(t *testing.T) {
	tr := NewHTTPTransport("http://unused", "ws_a", "cap-tok", nil)
	_, err := tr.Call(context.Background(), "cancel", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("want error for missing taskId")
	}
}

func TestHTTPTransport_UnknownAction(t *testing.T) {
	tr := NewHTTPTransport("http://unused", "ws_a", "cap-tok", nil)
	_, err := tr.Call(context.Background(), "bogus", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("want error for unknown action")
	}
}

// findTool returns the named tool from a slice, fataling if not found.
func findTool(t interface {
	Helper()
	Fatalf(string, ...any)
}, ts []tools.Tool, name string) tools.Tool {
	t.Helper()
	for _, x := range ts {
		if x.Name() == name {
			return x
		}
	}
	t.Fatalf("tool %q not found", name)
	return nil
}
