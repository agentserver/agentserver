package main

import (
	"net/http"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

// mountCoreCredentialRoutes keeps direct process_env execution independent
// from the optional Policy Webhook surface. Execution routes are mounted from
// the executor-only handler even when the egress handler is deliberately nil.
func mountCoreCredentialRoutes(mux *http.ServeMux, egressHandler, executionHandler http.Handler) {
	if egressHandler != nil {
		mux.Handle(corecontract.ResolveEgressCredentialPath, egressHandler)
		mux.Handle(corecontract.AuthorizeProcessEnvironmentEgressPath, egressHandler)
		mux.Handle(corecontract.RecordEgressCredentialAuditPath, egressHandler)
	}
	if executionHandler != nil {
		mux.Handle(corecontract.ResolveExecutionLarkCredentialAuthorityPath, executionHandler)
		mux.Handle(corecontract.ResolveExecutionLarkCredentialPath, executionHandler)
	}
}
