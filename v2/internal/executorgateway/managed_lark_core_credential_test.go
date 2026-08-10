package executorgateway

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/larkegresspolicy"
	"github.com/agentserver/agentserver/v2/internal/managedcredential"
)

func TestCoreManagedLarkProcessCredentialUsesNoStoreClosedContract(t *testing.T) {
	now := time.Date(2040, 1, 2, 3, 4, 5, 0, time.UTC)
	request := testManagedLarkEnvironmentRequest(now)
	transport := roundTripFunc(func(incoming *http.Request) (*http.Response, error) {
		if incoming.Method != http.MethodPost || incoming.URL.Path != corecontract.ResolveExecutionLarkCredentialPath {
			t.Fatalf("process credential request = %s %s", incoming.Method, incoming.URL.Path)
		}
		var command corecontract.ResolveExecutionLarkCredentialRequest
		if err := json.NewDecoder(incoming.Body).Decode(&command); err != nil {
			t.Fatal(err)
		}
		if command.ToolName != "shell" || command.Executable != "lark-cli" ||
			command.Operation.OperationID != request.Operation.OperationID ||
			command.Operation.SandboxID != request.Target.ID {
			t.Fatalf("process credential command = %#v", command)
		}
		raw, _ := json.Marshal(corecontract.ResolveExecutionLarkCredentialResponse{
			Configured: true, CredentialMode: managedcredential.ModeProcessEnv,
			AccessToken: "real-workspace-token", ProviderKind: "lark",
			BindingID: "90000000-0000-4000-8000-000000000009", AuthorityVersion: 7, CredentialVersion: 11,
			PolicySHA256: larkegresspolicy.SHA256Hex(), TAEPSM: "bytedance.sandbox.agentserver", ResolvedAt: now,
		})
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}, "Cache-Control": []string{"no-store"}},
			Body:       io.NopCloser(bytes.NewReader(raw)), Request: incoming,
		}, nil
	})
	client, err := NewCoreConnectionClient("https://core.agentserver.internal", &http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	client.authorizationNow = func() time.Time { return now }
	authority := ManagedLarkEgressAuthority{
		CredentialMode: managedcredential.ModeProcessEnv,
		BindingID:      "90000000-0000-4000-8000-000000000009", AuthorityVersion: 7,
		CredentialVersion: 11, PolicySHA256: larkegresspolicy.SHA256Hex(),
	}
	credential, err := client.ResolveManagedLarkProcessCredential(t.Context(), request, "bytedance.sandbox.agentserver", authority)
	if err != nil || !credential.Configured || credential.AccessToken != "real-workspace-token" || credential.CredentialVersion != 11 {
		t.Fatalf("process credential = %#v, %v", credential, err)
	}
}
