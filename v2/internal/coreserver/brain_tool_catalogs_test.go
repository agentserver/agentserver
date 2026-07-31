package coreserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

const testBrainToolCatalogID = "45000000-0000-4000-8000-000000000004"

func TestBrainToolCatalogHandlerRoutesFreezeAndBind(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		command    any
		wantAction string
		wantCall   string
	}{
		{
			name: "freeze", path: corecontract.FreezeBrainToolCatalogPath,
			command:    corecontract.FreezeBrainToolCatalogRequest{CatalogID: testBrainToolCatalogID},
			wantAction: "brain-tool-catalogs.freeze", wantCall: "freeze",
		},
		{
			name: "bind", path: corecontract.BindBrainThreadCatalogPath(testBrainToolCatalogID),
			command:    corecontract.BindBrainThreadCatalogRequest{CatalogID: testBrainToolCatalogID},
			wantAction: "brain-tool-catalogs.bind-thread", wantCall: "bind",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			authorizer := &recordingBrainToolCatalogAuthorizer{}
			commands := &recordingBrainToolCatalogCommands{}
			handler, err := NewBrainToolCatalogHandler(authorizer, commands)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(test.command)
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, test.path, bytes.NewReader(raw))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusOK || authorizer.action != test.wantAction || commands.call != test.wantCall {
				t.Fatalf("status/action/call/body = %d/%q/%q/%s", response.Code, authorizer.action, commands.call, response.Body)
			}
		})
	}
}

func TestBrainToolCatalogHandlerRejectsMismatchedPathAndAuthorization(t *testing.T) {
	commands := &recordingBrainToolCatalogCommands{}
	handler, err := NewBrainToolCatalogHandler(&recordingBrainToolCatalogAuthorizer{}, commands)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(corecontract.BindBrainThreadCatalogRequest{CatalogID: testBrainToolCatalogID})
	request := httptest.NewRequest(http.MethodPost, corecontract.BindBrainThreadCatalogPath("45000000-0000-4000-8000-000000000099"), bytes.NewReader(raw))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || commands.call != "" {
		t.Fatalf("mismatched path status/call/body = %d/%q/%s", response.Code, commands.call, response.Body)
	}

	denied, err := NewBrainToolCatalogHandler(&recordingBrainToolCatalogAuthorizer{err: errors.New("denied")}, commands)
	if err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodPost, corecontract.FreezeBrainToolCatalogPath, strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	denied.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || commands.call != "" {
		t.Fatalf("denied status/call/body = %d/%q/%s", response.Code, commands.call, response.Body)
	}
}

func TestParseBindBrainThreadCatalogPathRejectsAmbiguity(t *testing.T) {
	for _, path := range []string{
		corecontract.BrainToolCatalogPathPrefix,
		corecontract.BrainToolCatalogPathPrefix + testBrainToolCatalogID,
		corecontract.BrainToolCatalogPathPrefix + testBrainToolCatalogID + ":future",
		corecontract.BrainToolCatalogPathPrefix + testBrainToolCatalogID + "/child:bindThread",
	} {
		if _, ok := parseBindBrainThreadCatalogPath(path); ok {
			t.Errorf("parseBindBrainThreadCatalogPath(%q) unexpectedly succeeded", path)
		}
	}
}

type recordingBrainToolCatalogAuthorizer struct {
	action string
	err    error
}

func (authorizer *recordingBrainToolCatalogAuthorizer) AuthorizeWorkload(_ *http.Request, action string) error {
	authorizer.action = action
	return authorizer.err
}

type recordingBrainToolCatalogCommands struct{ call string }

func (commands *recordingBrainToolCatalogCommands) FreezeBrainToolCatalog(context.Context, corecontract.FreezeBrainToolCatalogRequest) (corecontract.FreezeBrainToolCatalogResponse, error) {
	commands.call = "freeze"
	return corecontract.FreezeBrainToolCatalogResponse{}, nil
}

func (commands *recordingBrainToolCatalogCommands) BindBrainThreadCatalog(context.Context, corecontract.BindBrainThreadCatalogRequest) (corecontract.BindBrainThreadCatalogResponse, error) {
	commands.call = "bind"
	return corecontract.BindBrainThreadCatalogResponse{}, nil
}
