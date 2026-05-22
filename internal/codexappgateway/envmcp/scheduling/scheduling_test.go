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

func TestLoopbackTransport_InjectsTimezone(t *testing.T) {
	t.Setenv("TZ", "Asia/Shanghai")
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = string(b)
		w.WriteHeader(200)
		w.Write([]byte("{}"))
	}))
	defer srv.Close()

	tr := NewLoopbackTransport(srv.URL, "tok")
	_, err := tr.Call(context.Background(), "schedule", json.RawMessage(`{"prompt":"x","processAfter":"2099-01-01T09:00:00"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `"timezone":"Asia/Shanghai"`) {
		t.Fatalf("expected timezone injected, got: %s", got)
	}
}

func TestLoopbackTransport_RespectsExplicitTimezone(t *testing.T) {
	t.Setenv("TZ", "Asia/Shanghai")
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = string(b)
		w.WriteHeader(200)
		w.Write([]byte("{}"))
	}))
	defer srv.Close()
	tr := NewLoopbackTransport(srv.URL, "tok")
	_, err := tr.Call(context.Background(), "schedule", json.RawMessage(`{"prompt":"x","processAfter":"...","timezone":"UTC"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `"timezone":"UTC"`) || strings.Contains(got, "Asia/Shanghai") {
		t.Fatalf("explicit tz must win; got: %s", got)
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
