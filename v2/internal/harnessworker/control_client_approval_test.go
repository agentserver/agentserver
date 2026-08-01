package harnessworker

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/harnesscontrol"
	"github.com/agentserver/agentserver/v2/internal/runmanifest"
)

func TestWorkerApprovalOutcomeDecisionMapping(t *testing.T) {
	client, request, outcome := workerApprovalOutcomeFixture()
	wantApproved := map[string]any{
		"approvalId": request.ApprovalID, "executionId": request.ExecutionID,
		"runId": request.RunID, "runAttemptId": client.config.Manifest.RunAttemptID,
		"runAttemptGeneration": request.RunAttemptGeneration,
		"nonce":                request.Nonce, "contextHash": request.ContextHash,
		"status": "approved", "approvalVersion": outcome.ApprovalVersion,
	}
	tests := []struct {
		status  string
		action  ApprovalAction
		content map[string]any
	}{
		{status: "approved", action: ApprovalAccept, content: wantApproved},
		{status: "denied", action: ApprovalDecline},
		{status: "expired", action: ApprovalDecline},
		{status: "cancelled", action: ApprovalCancel},
		{status: "consumed", action: ApprovalCancel},
	}
	for _, test := range tests {
		t.Run(test.status, func(t *testing.T) {
			candidate := outcome
			candidate.Status = test.status
			decision, err := client.decisionFromApprovalOutcome(request, candidate)
			if err != nil {
				t.Fatal(err)
			}
			if decision.Action != test.action || !reflect.DeepEqual(decision.Content, test.content) {
				t.Fatalf("decision = %#v, want action %q content %#v", decision, test.action, test.content)
			}
		})
	}
}

func TestWorkerApprovalOutcomeRejectsCorrelationDrift(t *testing.T) {
	client, request, outcome := workerApprovalOutcomeFixture()
	if err := validateWorkerApprovalOutcome(request, client.config.Manifest, outcome); err != nil {
		t.Fatalf("canonical outcome failed validation: %v", err)
	}

	otherUUID := "90000000-0000-4000-8000-000000000009"
	tests := []struct {
		name   string
		mutate func(*runmanifest.Manifest, *harnesscontrol.ApprovalOutcomeCommand)
	}{
		{name: "manifest run", mutate: func(manifest *runmanifest.Manifest, _ *harnesscontrol.ApprovalOutcomeCommand) {
			manifest.RunID = otherUUID
		}},
		{name: "run", mutate: func(_ *runmanifest.Manifest, value *harnesscontrol.ApprovalOutcomeCommand) { value.RunID = otherUUID }},
		{name: "call", mutate: func(_ *runmanifest.Manifest, value *harnesscontrol.ApprovalOutcomeCommand) {
			value.CallID = "call-other"
		}},
		{name: "generation", mutate: func(_ *runmanifest.Manifest, value *harnesscontrol.ApprovalOutcomeCommand) {
			value.RunAttemptGeneration++
		}},
		{name: "catalog", mutate: func(_ *runmanifest.Manifest, value *harnesscontrol.ApprovalOutcomeCommand) {
			value.ToolCatalogDigest = strings.Repeat("b", 64)
		}},
		{name: "execution", mutate: func(_ *runmanifest.Manifest, value *harnesscontrol.ApprovalOutcomeCommand) {
			value.ExecutionID = otherUUID
		}},
		{name: "approval", mutate: func(_ *runmanifest.Manifest, value *harnesscontrol.ApprovalOutcomeCommand) {
			value.ApprovalID = otherUUID
		}},
		{name: "nonce", mutate: func(_ *runmanifest.Manifest, value *harnesscontrol.ApprovalOutcomeCommand) { value.Nonce = otherUUID }},
		{name: "context", mutate: func(_ *runmanifest.Manifest, value *harnesscontrol.ApprovalOutcomeCommand) {
			value.ContextHash = strings.Repeat("c", 64)
		}},
		{name: "non-incrementing version", mutate: func(_ *runmanifest.Manifest, value *harnesscontrol.ApprovalOutcomeCommand) {
			value.ApprovalVersion = request.ApprovalVersion
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest := client.config.Manifest
			candidate := outcome
			test.mutate(&manifest, &candidate)
			err := validateWorkerApprovalOutcome(request, manifest, candidate)
			var protocolErr *harnesscontrol.ProtocolError
			if !errors.As(err, &protocolErr) || protocolErr.Code != harnesscontrol.ErrorAttemptMismatch || !protocolErr.Terminal {
				t.Fatalf("correlation drift error = %v, want terminal attempt_mismatch", err)
			}
		})
	}
}

func workerApprovalOutcomeFixture() (*WorkerControlClient, ElicitationRequest, harnesscontrol.ApprovalOutcomeCommand) {
	request := ElicitationRequest{
		RunID: "10000000-0000-4000-8000-000000000001", CallID: "call-approval",
		RunAttemptGeneration: 7, ToolCatalogDigest: strings.Repeat("a", 64),
		ExecutionID: "20000000-0000-4000-8000-000000000002",
		ApprovalID:  "30000000-0000-4000-8000-000000000003",
		Nonce:       "40000000-0000-4000-8000-000000000004",
		ContextHash: strings.Repeat("d", 64), ApprovalVersion: 1,
	}
	manifest := runmanifest.Manifest{
		RunID: request.RunID, RunAttemptID: "50000000-0000-4000-8000-000000000005",
		RunAttemptGeneration: request.RunAttemptGeneration,
		ExecutorMCP:          runmanifest.ExecutorMCP{CatalogDigest: request.ToolCatalogDigest},
	}
	client := &WorkerControlClient{config: WorkerControlClientConfig{Manifest: manifest}}
	outcome := harnesscontrol.ApprovalOutcomeCommand{
		Kind:  harnesscontrol.CommandKindApprovalOutcome,
		RunID: request.RunID, CallID: request.CallID,
		RunAttemptGeneration: request.RunAttemptGeneration,
		ToolCatalogDigest:    request.ToolCatalogDigest,
		ExecutionID:          request.ExecutionID, ApprovalID: request.ApprovalID,
		Nonce: request.Nonce, ContextHash: request.ContextHash,
		Status: "approved", ApprovalVersion: 2,
	}
	return client, request, outcome
}
