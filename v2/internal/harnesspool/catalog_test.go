package harnesspool

import (
	"crypto/sha256"
	"slices"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/executorgateway/mcpcontract"
)

func TestBuildExecutorCatalogFreezesExplicitPolicySubset(t *testing.T) {
	policyDigest := sha256.Sum256([]byte("policy"))
	proposal, err := BuildExecutorCatalog(ExecutorCatalogPolicy{
		Version: "executor-policy/1", ContextDigest: policyDigest,
		AllowedTools: []string{mcpcontract.ToolReadFile, mcpcontract.ToolListEnvironments},
	})
	if err != nil {
		t.Fatal(err)
	}
	tools := proposal.Catalog.Tools()
	names := make([]string, len(tools))
	for index, tool := range tools {
		names[index] = tool.Name
	}
	if !slices.Equal(names, []string{mcpcontract.ToolListEnvironments, mcpcontract.ToolReadFile}) ||
		proposal.ContractVersion != mcpcontract.Version || proposal.PolicyContextDigest != policyDigest ||
		proposal.CatalogDigest != proposal.Catalog.DigestSHA256() {
		t.Fatalf("proposal = %+v, tools = %v", proposal, names)
	}
}

func TestBuildExecutorCatalogTreatsEmptyAsDenyAllAndRejectsAmbiguity(t *testing.T) {
	empty, err := BuildExecutorCatalog(ExecutorCatalogPolicy{Version: "policy/empty", ContextDigest: sha256.Sum256([]byte("empty policy"))})
	if err != nil || len(empty.Catalog.Tools()) != 0 {
		t.Fatalf("empty policy proposal = %+v, %v", empty, err)
	}
	for _, policy := range []ExecutorCatalogPolicy{
		{Version: "policy/duplicate", ContextDigest: sha256.Sum256([]byte("duplicate")), AllowedTools: []string{mcpcontract.ToolShell, mcpcontract.ToolShell}},
		{Version: "policy/unknown", ContextDigest: sha256.Sum256([]byte("unknown")), AllowedTools: []string{"future_tool"}},
	} {
		if _, err := BuildExecutorCatalog(policy); err == nil || (!strings.Contains(err.Error(), "duplicate") && !strings.Contains(err.Error(), "not in contract")) {
			t.Fatalf("ambiguous policy error = %v", err)
		}
	}
}
