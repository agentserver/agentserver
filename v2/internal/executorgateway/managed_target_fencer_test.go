package executorgateway

import (
	"context"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/executionbackend"
	"github.com/agentserver/agentserver/v2/internal/sandboxclient"
	"github.com/agentserver/agentserver/v2/internal/sandboxcontract"
)

func TestGatewayManagedTargetFencerDeletesExactGeneration(t *testing.T) {
	now := time.Now().UTC()
	managedRequest := testManagedLarkEnvironmentRequest(now)
	client := &recordingManagedTargetDeleteClient{}
	fencer, err := NewGatewayManagedTargetFencer(client,
		func() (string, error) { return "92000000-0000-4000-8000-000000000009", nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := fencer.FenceManagedTarget(t.Context(), managedRequest.Principal, managedRequest.Target, "managed shell outcome unknown"); err != nil {
		t.Fatal(err)
	}
	wantRef := sandboxcontract.SandboxRef{SandboxID: managedRequest.Target.ID, TargetGeneration: managedRequest.Target.Generation}
	if client.request.Ref != wantRef || client.request.Session.EnvironmentID != managedRequest.Target.EnvironmentID ||
		client.authority.Action != sandboxclient.ActionDelete || client.authority.Ref != wantRef ||
		client.authority.RunAttemptID != managedRequest.Principal.Run.RunAttemptID ||
		client.request.Reason != "managed shell outcome unknown" {
		t.Fatalf("delete request = %+v authority = %+v", client.request, client.authority)
	}
}

func TestGatewayManagedTargetFencerRejectsAgentX(t *testing.T) {
	now := time.Now().UTC()
	managedRequest := testManagedLarkEnvironmentRequest(now)
	client := &recordingManagedTargetDeleteClient{}
	fencer, err := NewGatewayManagedTargetFencer(client,
		func() (string, error) { return "92000000-0000-4000-8000-000000000009", nil })
	if err != nil {
		t.Fatal(err)
	}
	target := managedRequest.Target
	target.Kind = executionbackend.KindAgentX
	if err := fencer.FenceManagedTarget(t.Context(), managedRequest.Principal, target, "unknown"); err == nil || client.called {
		t.Fatalf("AgentX fence error = %v called=%v", err, client.called)
	}
}

type recordingManagedTargetDeleteClient struct {
	called    bool
	request   sandboxcontract.DeleteSandboxRequest
	authority sandboxclient.TokenRequest
}

func (client *recordingManagedTargetDeleteClient) Delete(_ context.Context, request sandboxcontract.DeleteSandboxRequest, authority sandboxclient.TokenRequest) (sandboxcontract.SandboxResponse, error) {
	client.called = true
	client.request = request
	client.authority = authority
	return sandboxcontract.SandboxResponse{Sandbox: sandboxcontract.Sandbox{
		Profile: sandboxcontract.ProfileV1, Ref: request.Ref, State: sandboxcontract.SandboxDeleted,
	}}, nil
}
