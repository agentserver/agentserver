package harnessworker

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/codexwire"
	"github.com/agentserver/agentserver/v2/internal/sessiontitle"
)

func TestTemporaryTitleIsIsolatedFromMainTurn(t *testing.T) {
	for _, scenario := range []string{"success", "model-error", "tool-request", "main-finishes-first"} {
		t.Run(scenario, func(t *testing.T) {
			catalog := runnerTestCatalog(t)
			options := DefaultAppServerRunnerOptions()
			var titles []sessiontitle.Proposal
			options.TitleHandler = func(_ context.Context, p sessiontitle.Proposal) error { titles = append(titles, p); return nil }
			runner, _, server := newRunnerFixture(t, &fakeDynamicCaller{call: func(context.Context, DynamicCall) (DynamicToolResult, error) {
				t.Error("title reached executor")
				return DynamicToolResult{}, nil
			}}, catalog, options)
			done := make(chan error, 1)
			go func() {
				done <- func() error {
					ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
					defer cancel()
					if err := serveRunnerStartLifecycle(ctx, server, catalog); err != nil {
						return err
					}
					if _, err := receiveRunnerMessage(ctx, server, codexwire.KindRequest, "config/read", "100"); err != nil {
						return err
					}
					if err := server.Send(map[string]any{"id": 100, "result": map[string]any{"config": map[string]any{"mcp_servers": map[string]any{"unexpected": map[string]any{"enabled": true}}}}}); err != nil {
						return err
					}
					start, err := receiveRunnerMessage(ctx, server, codexwire.KindRequest, "thread/start", "101")
					if err != nil {
						return err
					}
					var params map[string]any
					if err := start.DecodeParams(&params); err != nil {
						return err
					}
					config := params["config"].(map[string]any)
					if params["ephemeral"] != true || params["sandbox"] != "read-only" || params["threadSource"] != "thread_title" || len(params["dynamicTools"].([]any)) != 0 || len(params["environments"].([]any)) != 0 || config["features.shell_tool"] != false || config["mcp_servers"].(map[string]any)["unexpected"].(map[string]any)["enabled"] != false {
						return fmt.Errorf("title isolation absent: %+v", params)
					}
					if scenario == "model-error" {
						if err := server.Send(map[string]any{"id": 101, "error": map[string]any{"code": -1, "message": "title unavailable"}}); err != nil {
							return err
						}
						return sendRunnerTerminal(server, "completed")
					}
					thread := map[string]any{"id": "hidden-title", "ephemeral": true}
					// A thread/started notification can precede its RPC response.
					if err := server.Send(map[string]any{"method": "thread/started", "params": map[string]any{"thread": thread}}); err != nil {
						return err
					}
					if err := server.Send(map[string]any{"id": 101, "result": map[string]any{"thread": thread, "model": "scripted-model", "modelProvider": "scripted-provider", "sandbox": map[string]any{"type": "readOnly"}}}); err != nil {
						return err
					}
					turn, err := receiveRunnerMessage(ctx, server, codexwire.KindRequest, "turn/start", "102")
					if err != nil {
						return err
					}
					if !strings.Contains(string(turn.Params), "outputSchema") || strings.Contains(string(turn.Params), "frozen dynamic tools") {
						return fmt.Errorf("bad title turn: %s", turn.Params)
					}
					if err := server.Send(map[string]any{"id": 102, "result": map[string]any{"turn": map[string]any{"id": "hidden-turn", "status": "inProgress"}}}); err != nil {
						return err
					}
					if err := server.Send(map[string]any{"method": "turn/started", "params": map[string]any{"threadId": "hidden-title", "turn": map[string]any{"id": "hidden-turn", "status": "inProgress"}}}); err != nil {
						return err
					}
					if scenario == "main-finishes-first" {
						if err := sendRunnerTerminal(server, "completed"); err != nil {
							return err
						}
					} else if scenario == "tool-request" {
						if err := server.Send(map[string]any{"id": "forbidden-title-tool", "method": "item/tool/call", "params": map[string]any{"threadId": "hidden-title", "turnId": "hidden-turn"}}); err != nil {
							return err
						}
						if _, err := receiveRunnerMessage(ctx, server, codexwire.KindError, "", `"forbidden-title-tool"`); err != nil {
							return err
						}
					} else {
						if err := server.Send(map[string]any{"method": "item/completed", "params": map[string]any{"threadId": "hidden-title", "turnId": "hidden-turn", "item": map[string]any{"type": "agentMessage", "text": `{"title":"检查权限同步"}`}}}); err != nil {
							return err
						}
						if err := server.Send(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": "hidden-title", "turn": map[string]any{"id": "hidden-turn", "status": "completed"}}}); err != nil {
							return err
						}
					}
					if scenario != "success" {
						if _, err := receiveRunnerMessage(ctx, server, codexwire.KindRequest, "turn/interrupt", "103"); err != nil {
							return err
						}
					}
					if _, err := receiveRunnerMessage(ctx, server, codexwire.KindRequest, "thread/unsubscribe", "104"); err != nil {
						return err
					}
					if scenario != "main-finishes-first" {
						return sendRunnerTerminal(server, "completed")
					}
					return nil
				}()
			}()
			result, err := runner.Run(t.Context(), runnerStartRequest(catalog))
			if err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if result.Terminal.Turn.Status != "completed" {
				t.Fatal("title failed main run")
			}
			if len(titles) < 1 || titles[0].Source != "fallback" {
				t.Fatal("no fallback", titles)
			}
			if scenario == "success" {
				if len(titles) != 2 || titles[1].Title != "检查权限同步" {
					t.Fatal("no generated title", titles)
				}
			} else if len(titles) != 1 {
				t.Fatal("accepted failed title", titles)
			}
			if err := runner.ConsumeEvents(func(message codexwire.Message) {
				raw, _ := json.Marshal(message)
				if strings.Contains(string(raw), "hidden-title") || strings.Contains(string(raw), "检查权限同步") {
					t.Error("title leaked into main history")
				}
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
