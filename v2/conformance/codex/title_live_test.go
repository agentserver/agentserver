package codex_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/conformance/codex/internal/scriptedmodel"
	"github.com/agentserver/agentserver/v2/internal/codexwire"
	"github.com/agentserver/agentserver/v2/internal/harnessworker"
	"github.com/agentserver/agentserver/v2/internal/sessiontitle"
)

// Real stock app-server, deterministic loopback model: no external account,
// model key, production data, filesystem tools or real model calls.
func TestAppServerTemporaryTitleAlongsidePrimaryTurn(t *testing.T) {
	binary, paths := prepareLiveCodex(t)
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()
	generated := make(chan struct{})
	var once sync.Once
	var mu sync.Mutex
	var titleRequests int
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(io.LimitReader(r.Body, 4*1024*1024))
		if err != nil {
			t.Error(err)
			return
		}
		isTitle := strings.Contains(string(raw), "Generate a concise, single-line task title")
		text := conformanceFinalText
		if isTitle {
			mu.Lock()
			titleRequests++
			mu.Unlock()
			var body struct {
				Tools []json.RawMessage `json:"tools"`
				Text  map[string]any    `json:"text"`
			}
			if json.Unmarshal(raw, &body) != nil || len(body.Tools) != 0 || !strings.Contains(string(raw), `"json_schema"`) {
				t.Error("title request is not tool-free structured output")
			}
			text = `{"title":"检查权限同步"}`
		} else {
			select {
			case <-generated:
			case <-ctx.Done():
				return
			}
		}
		response, err := scriptedmodel.AssistantMessage("response-title-test", "message-title-test", text)
		if err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write(response.Body)
	}))
	defer func() { cancel(); model.Close() }()
	writeScriptedModelConfigWithOptions(t, paths.codexHome, model.URL, scriptedModelConfigOptions{disableUpdatePlan: true})
	process := startPreparedLiveCodex(t, binary, paths, "app-server", "--listen", "stdio://", "--strict-config")
	catalog := approvedDynamicExecutorCatalog(t)
	bridge, err := harnessworker.NewDynamicBridge(&appServerRunnerDynamicCaller{catalog: catalog, calls: make(chan harnessworker.DynamicCall, 1)}, 8, harnessworker.DefaultLimits().MaxArgumentBytes)
	if err != nil {
		t.Fatal(err)
	}
	options := harnessworker.DefaultAppServerRunnerOptions()
	options.TitleHandler = func(_ context.Context, p sessiontitle.Proposal) error {
		if p.Source == "generated" {
			if p.Title != "检查权限同步" {
				t.Error("wrong generated title")
			}
			once.Do(func() { close(generated) })
		}
		return nil
	}
	trace := &titleTraceTransport{AppServerTransport: process.Peer}
	runner, err := harnessworker.NewAppServerRunner(trace, bridge, options)
	if err != nil {
		t.Fatal(err)
	}
	drained := drainAppServerRunnerEvents(runner)
	result, err := runner.Run(ctx, harnessworker.AppServerRunRequest{RunID: "run-title-conformance", RunAttemptGeneration: 1, ClientInfo: harnessworker.AppServerClientInfo{Name: "agentserver_v2_conformance", Title: "title test", Version: "0.0.0"}, Catalog: catalog, Start: &harnessworker.AppServerThreadStart{Model: conformanceModelName, CWD: paths.cwd, BaseInstructions: "Return the test response."}, UserText: "检查一下权限同步"})
	<-drained
	if err != nil {
		stderr, _ := process.Stderr()
		t.Fatalf("title conformance failed: %v\n%s\ntitle RPC errors: %v", err, stderr, trace.snapshotErrors())
	}
	if result.Terminal.Turn.Status != "completed" {
		t.Fatal(result.Terminal)
	}
	mu.Lock()
	count := titleRequests
	mu.Unlock()
	if count != 1 {
		t.Fatalf("title model requests=%d", count)
	}
	closeAndWait(t, process)
	var rollouts int
	if err := filepath.WalkDir(filepath.Join(paths.codexHome, "sessions"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasSuffix(path, ".jsonl") {
			rollouts++
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if rollouts != 1 {
		t.Fatalf("ephemeral title persisted a rollout: count=%d", rollouts)
	}
}

type titleTraceTransport struct {
	harnessworker.AppServerTransport
	mu     sync.Mutex
	errors []string
}

func (trace *titleTraceTransport) Receive(ctx context.Context) (codexwire.Message, error) {
	message, err := trace.AppServerTransport.Receive(ctx)
	if message.Kind == codexwire.KindError && message.Error != nil {
		trace.mu.Lock()
		trace.errors = append(trace.errors, message.Error.Message)
		trace.mu.Unlock()
	}
	return message, err
}

func (trace *titleTraceTransport) snapshotErrors() []string {
	trace.mu.Lock()
	defer trace.mu.Unlock()
	return append([]string(nil), trace.errors...)
}
