package harnessworker

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/codexwire"
	"github.com/agentserver/agentserver/v2/internal/runmanifest"
)

func TestCodexPermissionModeProjectionMatchesNativeAppServerFields(t *testing.T) {
	tests := []struct {
		mode            runmanifest.CodexPermissionMode
		approval        string
		reviewer        string
		sandbox         string
		turnSandbox     string
		wantTurnSandbox bool
	}{
		{mode: runmanifest.CodexPermissionModeReadOnly, approval: "on-request", reviewer: "auto_review", sandbox: "read-only", turnSandbox: "readOnly", wantTurnSandbox: true},
		{mode: runmanifest.CodexPermissionModeAuto, approval: "on-request", reviewer: "auto_review", sandbox: "workspace-write", turnSandbox: "workspaceWrite", wantTurnSandbox: true},
		{mode: runmanifest.CodexPermissionModeFullAccess, approval: "never", sandbox: "danger-full-access", turnSandbox: "dangerFullAccess", wantTurnSandbox: true},
	}
	for _, test := range tests {
		t.Run(string(test.mode), func(t *testing.T) {
			thread := codexThreadPermissionParams(test.mode)
			if thread.ApprovalPolicy != test.approval || thread.ApprovalsReviewer != test.reviewer || thread.Sandbox != test.sandbox {
				t.Fatalf("thread projection = %+v, want approval=%q reviewer=%q sandbox=%q", thread, test.approval, test.reviewer, test.sandbox)
			}
			turn := codexTurnPermissionParams(test.mode)
			if turn.ApprovalPolicy != test.approval || turn.ApprovalsReviewer != test.reviewer {
				t.Fatalf("turn projection = %+v, want approval=%q reviewer=%q", turn, test.approval, test.reviewer)
			}
			if (turn.SandboxPolicy != nil) != test.wantTurnSandbox {
				t.Fatalf("turn sandbox policy presence = %v, want %v", turn.SandboxPolicy != nil, test.wantTurnSandbox)
			}
			if turn.SandboxPolicy != nil && turn.SandboxPolicy.Type != test.turnSandbox {
				t.Fatalf("turn sandbox policy = %+v, want type %q", turn.SandboxPolicy, test.turnSandbox)
			}
		})
	}
}

func TestCodexPermissionModeLegacyProjectionKeepsReadOnlySandboxOnWire(t *testing.T) {
	projection := codexThreadPermissionParams("")
	raw, err := json.Marshal(appServerThreadStartParams{
		Model: "model", CWD: "/workspace", ApprovalPolicy: projection.ApprovalPolicy,
		ApprovalsReviewer: projection.ApprovalsReviewer, Sandbox: projection.Sandbox,
		Ephemeral: false, ThreadSource: "user", Environments: []any{}, DynamicTools: []DynamicNamespace{}, SelectedCapabilityRoots: []any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"sandbox":"read-only"`) || strings.Contains(string(raw), `"approvalsReviewer"`) {
		t.Fatalf("legacy mode projection = %s", raw)
	}
}

func TestCodexPermissionModeRejectsUnknownAndDefaultsEmpty(t *testing.T) {
	if got, err := runmanifest.CodexPermissionMode("").Effective(); err != nil || got != runmanifest.CodexPermissionModeReadOnly {
		t.Fatalf("empty mode effective = %q, %v", got, err)
	}
	if err := runmanifest.CodexPermissionMode("future-mode").Validate(); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("unknown mode validation error = %v", err)
	}
	if _, err := effectiveAppServerPermissionMode(AppServerRunRequest{
		PermissionMode: runmanifest.CodexPermissionModeReadOnly,
		Start:          &AppServerThreadStart{PermissionMode: runmanifest.CodexPermissionModeFullAccess},
	}); err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("conflicting runner permission mode error = %v", err)
	}
	if _, err := effectiveAppServerPermissionMode(AppServerRunRequest{
		Start: &AppServerThreadStart{PermissionMode: runmanifest.CodexPermissionModeFullAccess},
	}); err == nil || !strings.Contains(err.Error(), "run permission mode authority") {
		t.Fatalf("start-only runner permission mode error = %v", err)
	}
}

func TestAppServerRunnerProjectsCodexPermissionModeOnFreshThread(t *testing.T) {
	tests := []struct {
		mode     runmanifest.CodexPermissionMode
		approval string
		reviewer string
		sandbox  string
	}{
		{mode: runmanifest.CodexPermissionModeReadOnly, approval: "on-request", reviewer: "auto_review", sandbox: "read-only"},
		{mode: runmanifest.CodexPermissionModeAuto, approval: "on-request", reviewer: "auto_review", sandbox: "workspace-write"},
		{mode: runmanifest.CodexPermissionModeFullAccess, approval: "never", sandbox: "danger-full-access"},
	}
	for _, test := range tests {
		t.Run(string(test.mode), func(t *testing.T) {
			catalog := runnerTestCatalog(t)
			runner, _, server := newRunnerFixture(t, &fakeDynamicCaller{call: func(context.Context, DynamicCall) (DynamicToolResult, error) {
				return DynamicToolResult{}, fmt.Errorf("permission-mode fixture must not call a dynamic tool")
			}}, catalog, DefaultAppServerRunnerOptions())
			serverDone := make(chan error, 1)
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				if err := serveRunnerInitialize(ctx, server); err != nil {
					serverDone <- err
					return
				}
				start, err := receiveRunnerMessage(ctx, server, codexwire.KindRequest, "thread/start", "2")
				if err != nil {
					serverDone <- err
					return
				}
				var params appServerThreadStartParams
				if err := start.DecodeParams(&params); err != nil {
					serverDone <- err
					return
				}
				if params.ApprovalPolicy != test.approval || params.ApprovalsReviewer != test.reviewer || params.Sandbox != test.sandbox {
					serverDone <- fmt.Errorf("thread/start permission projection = %+v", params)
					return
				}
				if err := sendRunnerThreadResponse(server, 2); err != nil {
					serverDone <- err
					return
				}
				if err := server.Send(map[string]any{"method": "thread/started", "params": map[string]any{"thread": runnerThreadPayload()}}); err != nil {
					serverDone <- err
					return
				}
				turn, err := receiveRunnerMessage(ctx, server, codexwire.KindRequest, "turn/start", "3")
				if err != nil {
					serverDone <- err
					return
				}
				var turnParams appServerTurnStartParams
				if err := turn.DecodeParams(&turnParams); err != nil {
					serverDone <- err
					return
				}
				if turnParams.ApprovalPolicy != "" || turnParams.ApprovalsReviewer != "" || turnParams.SandboxPolicy != nil {
					serverDone <- fmt.Errorf("fresh turn unexpectedly repeated thread permission fields: %+v", turnParams)
					return
				}
				if err := sendRunnerTurnStarted(server); err != nil {
					serverDone <- err
					return
				}
				serverDone <- sendRunnerTerminal(server, "completed")
			}()

			request := runnerStartRequest(catalog)
			request.PermissionMode = test.mode
			if _, err := runner.Run(t.Context(), request); err != nil {
				t.Fatal(err)
			}
			if err := <-serverDone; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAppServerRunnerProjectsCodexPermissionModeOnResumedTurn(t *testing.T) {
	tests := []struct {
		mode        runmanifest.CodexPermissionMode
		approval    string
		reviewer    string
		sandboxType string
	}{
		{mode: runmanifest.CodexPermissionModeReadOnly, approval: "on-request", reviewer: "auto_review", sandboxType: "readOnly"},
		{mode: runmanifest.CodexPermissionModeAuto, approval: "on-request", reviewer: "auto_review", sandboxType: "workspaceWrite"},
		{mode: runmanifest.CodexPermissionModeFullAccess, approval: "never", sandboxType: "dangerFullAccess"},
	}
	for _, test := range tests {
		t.Run(string(test.mode), func(t *testing.T) {
			catalog := runnerTestCatalog(t)
			runner, _, server := newRunnerFixture(t, &fakeDynamicCaller{call: func(context.Context, DynamicCall) (DynamicToolResult, error) {
				return DynamicToolResult{}, fmt.Errorf("permission-mode fixture must not call a dynamic tool")
			}}, catalog, DefaultAppServerRunnerOptions())
			serverDone := make(chan error, 1)
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				if err := serveRunnerInitialize(ctx, server); err != nil {
					serverDone <- err
					return
				}
				if _, err := receiveRunnerMessage(ctx, server, codexwire.KindRequest, "thread/resume", "2"); err != nil {
					serverDone <- err
					return
				}
				if err := sendRunnerThreadResponse(server, 2); err != nil {
					serverDone <- err
					return
				}
				turn, err := receiveRunnerMessage(ctx, server, codexwire.KindRequest, "turn/start", "3")
				if err != nil {
					serverDone <- err
					return
				}
				var params appServerTurnStartParams
				if err := turn.DecodeParams(&params); err != nil {
					serverDone <- err
					return
				}
				if params.ApprovalPolicy != test.approval || params.ApprovalsReviewer != test.reviewer {
					serverDone <- fmt.Errorf("turn/start permission projection = %+v", params)
					return
				}
				if (params.SandboxPolicy == nil) != (test.sandboxType == "") || params.SandboxPolicy != nil && params.SandboxPolicy.Type != test.sandboxType {
					serverDone <- fmt.Errorf("turn/start sandbox projection = %+v, want %q", params.SandboxPolicy, test.sandboxType)
					return
				}
				if err := sendRunnerTurnStarted(server); err != nil {
					serverDone <- err
					return
				}
				serverDone <- sendRunnerTerminal(server, "completed")
			}()

			request := runnerStartRequest(catalog)
			request.PermissionMode = test.mode
			request.Start = nil
			request.Resume = &AppServerThreadResume{
				ThreadID: runnerTestThreadID, RolloutPath: runnerTestRollout, CWD: runnerTestCWD,
				CheckpointCatalogDigest: catalog.Digest(),
			}
			if _, err := runner.Run(t.Context(), request); err != nil {
				t.Fatal(err)
			}
			if err := <-serverDone; err != nil {
				t.Fatal(err)
			}
		})
	}
}
