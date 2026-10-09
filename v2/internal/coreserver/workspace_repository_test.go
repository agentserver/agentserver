package coreserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

type recordingRepositoryCommands struct {
	recordingPlatformResourceCommands
	actor, workspace string
	reads, writes    int
	input            corecontract.UpdateWorkspaceRepositoryRequest
}

func (c *recordingRepositoryCommands) GetRepository(_ context.Context, workspace, actor string) (corecontract.GetWorkspaceRepositoryResponse, error) {
	c.actor, c.workspace = actor, workspace
	c.reads++
	return corecontract.GetWorkspaceRepositoryResponse{Setting: corecontract.WorkspaceRepositorySettingState{WorkspaceID: workspace}}, nil
}
func (c *recordingRepositoryCommands) UpdateRepository(_ context.Context, workspace, actor string, input corecontract.UpdateWorkspaceRepositoryRequest) (corecontract.UpdateWorkspaceRepositoryResponse, error) {
	c.actor, c.workspace, c.input = actor, workspace, input
	c.writes++
	return corecontract.UpdateWorkspaceRepositoryResponse{Setting: corecontract.WorkspaceRepositorySettingState{WorkspaceID: workspace, Source: input.Source, Version: input.ExpectedVersion + 1}, Changed: true}, nil
}

func TestWorkspaceRepositoryHandler(t *testing.T) {
	for _, tc := range []struct {
		name, method, suffix, body, action string
		status, reads, writes              int
		identity                           bool
	}{
		{"read", "GET", "", "", "workspaces.get", 200, 1, 0, true},
		{"save", "PATCH", "", `{"source":{"url":"https://code.byted.org/tce/rtm-aihub","ref":"","workingDirectory":"."},"expectedVersion":0}`, "workspaces.update", 200, 0, 1, true},
		{"clear", "PATCH", "", `{"source":null,"expectedVersion":2}`, "workspaces.update", 200, 0, 1, true},
		{"missing-source", "PATCH", "", `{"expectedVersion":2}`, "workspaces.update", 400, 0, 0, true},
		{"missing-version", "PATCH", "", `{"source":null}`, "workspaces.update", 400, 0, 0, true},
		{"null-version", "PATCH", "", `{"source":null,"expectedVersion":null}`, "workspaces.update", 400, 0, 0, true},
		{"unknown-field", "PATCH", "", `{"source":null,"expectedVersion":0,"token":"never-echo"}`, "workspaces.update", 400, 0, 0, true},
		{"nested-token", "PATCH", "", `{"source":{"url":"x","token":"never-echo"},"expectedVersion":0}`, "workspaces.update", 400, 0, 0, true},
		{"query", "GET", "?workspace=other", "", "", 400, 0, 0, true},
		{"body-on-get", "GET", "", `{}`, "workspaces.get", 400, 0, 0, true},
		{"method", "DELETE", "", "", "", 405, 0, 0, true},
		{"no-workload", "GET", "", "", "", 403, 0, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &recordingRepositoryCommands{}
			user := &recordingUserAuthorizer{actorID: platformResourceTestActor}
			h, err := NewPlatformResourceHandler(&identityCapabilityAuthorizer{identity: "platform-gateway"}, user, c)
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest(tc.method, corecontract.WorkspaceRepositoryPath(platformResourceTestWorkspace)+tc.suffix, strings.NewReader(tc.body))
			if tc.identity {
				r.Header.Set("X-Test-Identity", "platform-gateway")
			}
			if tc.body != "" {
				r.Header.Set("Content-Type", "application/json")
			}
			w := httptest.NewRecorder()
			h.Routes().ServeHTTP(w, r)
			if w.Code != tc.status || c.reads != tc.reads || c.writes != tc.writes {
				t.Fatalf("status=%d body=%s reads=%d writes=%d", w.Code, w.Body.String(), c.reads, c.writes)
			}
			if w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "never-echo") {
				t.Fatal("response caching or secret disclosure")
			}
			if tc.action != "" && user.action != tc.action {
				t.Fatalf("action=%q", user.action)
			}
			if c.reads+c.writes > 0 && (c.actor != platformResourceTestActor || c.workspace != platformResourceTestWorkspace) {
				t.Fatal("lost authority scope")
			}
			if tc.name == "clear" && (c.input.Source != nil || c.input.ExpectedVersion != 2) {
				t.Fatal("clear request changed")
			}
			if tc.method == http.MethodDelete && w.Header().Get("Allow") != "GET, PATCH" {
				t.Fatal("missing allowed methods")
			}
		})
	}
}
