package coreserver

import (
	"crypto/tls"
	"net/http/httptest"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

func TestExternalSandboxAuthorityCoversRealCoreContractOnly(t *testing.T) {
	auth, err := NewSPIFFEWorkloadAuthorizerWithExternalToken("test-capability", "spiffe://agentserver.test/ns/agentserver/sa/sandbox-gateway")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{corecontract.ReserveManagedSandboxPath, corecontract.ListManagedSandboxesForReconcilePath, corecontract.AuthorizeManagedSandboxOperationPath, corecontract.ManagedSandboxPathPrefix + "a:observe"} {
		t.Run(p, func(t *testing.T) {
			r := httptest.NewRequest("POST", "https://core.test"+p, nil)
			r.TLS = &tls.ConnectionState{}
			r.Header.Set(externalManagedSandboxTokenHeader, "test-capability")
			if err := auth.AuthorizeWorkload(r, "test"); err != nil {
				t.Fatal(err)
			}
			r.Header.Add(externalManagedSandboxTokenHeader, "test-capability")
			if err := auth.AuthorizeWorkload(r, "test"); err == nil {
				t.Fatal("duplicate credentials accepted")
			}
		})
	}
	for _, p := range []string{"/internal/v2/executions", "/internal/v2/managed-sandboxes-evil", "/internal/v2/managed-sandboxes:unknown"} {
		r := httptest.NewRequest("POST", "https://core.test"+p, nil)
		r.Header.Set(externalManagedSandboxTokenHeader, "test-capability")
		if err := auth.AuthorizeWorkload(r, "test"); err == nil {
			t.Fatalf("accepted unrelated route %s", p)
		}
	}
	if auth.AuthorizeWorkload(nil, "test") == nil {
		t.Fatal("accepted nil request")
	}
}
