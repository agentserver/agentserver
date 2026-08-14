package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

func TestMountCoreCredentialRoutesKeepsDirectExecutionWithoutEgressHandler(t *testing.T) {
	mux := http.NewServeMux()
	execution := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	})
	mountCoreCredentialRoutes(mux, nil, execution)

	for _, path := range []string{
		corecontract.ResolveExecutionCredentialAuthorityPath,
		corecontract.ResolveExecutionCredentialPath,
	} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, nil))
		if response.Code != http.StatusNoContent {
			t.Fatalf("direct execution route %q returned %d", path, response.Code)
		}
	}
	for _, path := range []string{
		corecontract.ResolveEgressCredentialPath,
		corecontract.AuthorizeProcessEnvironmentEgressPath,
		corecontract.RecordEgressCredentialAuditPath,
	} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("direct profile unexpectedly mounted egress route %q: %d", path, response.Code)
		}
	}
}
