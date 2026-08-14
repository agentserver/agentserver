package executorgateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/larkegresspolicy"
	"github.com/agentserver/agentserver/v2/internal/managedcredential"
)

func TestCoreManagedLarkAuthorityUsesInjectedAuthorizationClock(t *testing.T) {
	now := time.Date(2040, 1, 2, 3, 4, 5, 0, time.UTC)
	request := testManagedLarkEnvironmentRequest(now)
	for _, test := range []struct {
		name         string
		authorizedAt time.Time
		wantError    bool
	}{
		{name: "within clock skew", authorizedAt: now.Add(4 * time.Second)},
		{name: "beyond clock skew", authorizedAt: now.Add(6 * time.Second), wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, incoming *http.Request) {
				if incoming.Method != http.MethodPost || incoming.URL.Path != corecontract.ResolveExecutionCredentialAuthorityPath {
					t.Fatalf("managed Lark authority request = %s %s", incoming.Method, incoming.URL.Path)
				}
				response.Header().Set("Content-Type", "application/json")
				response.Header().Set("Cache-Control", "no-store")
				_ = json.NewEncoder(response).Encode(corecontract.ResolveEgressCredentialAuthorityResponse{
					CredentialMode: managedcredential.ModeWebhookSwap,
					ProviderKind:   "lark", ApplicationID: "cli_agentserver_sg",
					BindingID:        "90000000-0000-4000-8000-000000000009",
					AuthorityVersion: 7, CredentialVersion: 11,
					PolicySHA256: larkegresspolicy.SHA256Hex(), AuthorizedAt: test.authorizedAt,
				})
			}))
			defer server.Close()
			client, err := NewCoreConnectionClient(server.URL, server.Client())
			if err != nil {
				t.Fatal(err)
			}
			client.authorizationNow = func() time.Time { return now }
			authority, err := client.ResolveManagedCredentialAuthority(t.Context(), request)
			if test.wantError {
				if err == nil {
					t.Fatalf("future Core authorization was accepted: %+v", authority)
				}
				return
			}
			if err != nil || authority.ApplicationID != "cli_agentserver_sg" || authority.AuthorityVersion != 7 || authority.CredentialVersion != 11 {
				t.Fatalf("ResolveManagedCredentialAuthority() = %+v, %v", authority, err)
			}
		})
	}
}
