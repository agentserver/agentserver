package coredb

import (
	"crypto/sha256"
	"encoding/json"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/braincatalog"
)

func TestValidateFreezeBrainToolCatalogRequiresCanonicalMatchingCatalog(t *testing.T) {
	command := validFreezeBrainToolCatalogCommand(t)
	catalog, err := validateFreezeBrainToolCatalog(command)
	if err != nil {
		t.Fatal(err)
	}
	if catalog.DigestSHA256() != command.CatalogDigest {
		t.Fatal("validated catalog digest changed")
	}

	tests := []struct {
		name string
		edit func(*FreezeBrainToolCatalogCommand)
		want string
	}{
		{name: "digest", edit: func(command *FreezeBrainToolCatalogCommand) { command.CatalogDigest[0] ^= 0xff }, want: "catalog_digest"},
		{name: "canonicalizer", edit: func(command *FreezeBrainToolCatalogCommand) { command.CanonicalizerVersion = "json-v0" }, want: "canonicalizer_version"},
		{name: "non canonical", edit: func(command *FreezeBrainToolCatalogCommand) {
			command.CanonicalCatalog = append([]byte(" "), command.CanonicalCatalog...)
		}, want: "not RFC 8785 canonical"},
		{name: "version", edit: func(command *FreezeBrainToolCatalogCommand) { command.ExpectedAttemptVersion = 0 }, want: "positive"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changed := command
			changed.CanonicalCatalog = append([]byte(nil), command.CanonicalCatalog...)
			test.edit(&changed)
			_, err := validateFreezeBrainToolCatalog(changed)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validation error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestBrainToolCatalogFreezeFingerprintAndBindValidation(t *testing.T) {
	command := validFreezeBrainToolCatalogCommand(t)
	persisted := BrainToolCatalog{
		ID: command.CatalogID, WorkspaceID: command.WorkspaceID, SessionID: command.SessionID,
		CreatedRunID: command.RunID, CreatedRunAttemptID: command.AttemptID,
		CreatedAttemptGeneration: command.Generation, CreatedHolderID: command.HolderID,
		CreatedRunVersion: command.ExpectedRunVersion, CreatedAttemptVersion: command.ExpectedAttemptVersion,
		ContractVersion: command.ContractVersion, CanonicalizerVersion: command.CanonicalizerVersion,
		CanonicalCatalog: append([]byte(nil), command.CanonicalCatalog...), CatalogDigest: command.CatalogDigest,
		PolicyVersion: command.PolicyVersion, PolicyContextDigest: command.PolicyContextDigest,
	}
	if !brainToolCatalogMatchesFreeze(persisted, command) {
		t.Fatal("exact freeze retry fingerprint did not match")
	}
	changed := command
	changed.PolicyVersion = "policy-v2"
	if brainToolCatalogMatchesFreeze(persisted, changed) {
		t.Fatal("changed freeze fingerprint matched")
	}

	bind := BindBrainThreadCatalogCommand{
		CatalogID: command.CatalogID, RunID: command.RunID, AttemptID: command.AttemptID,
		HolderID: command.HolderID, Generation: command.Generation,
		ExpectedRunVersion: command.ExpectedRunVersion, ExpectedAttemptVersion: command.ExpectedAttemptVersion,
		ExpectedCatalogVersion: 1, ThreadID: "thread-1",
	}
	if err := validateBindBrainThreadCatalog(bind); err != nil {
		t.Fatal(err)
	}
	bind.ThreadID = ""
	if err := validateBindBrainThreadCatalog(bind); err == nil || !strings.Contains(err.Error(), "thread_id") {
		t.Fatalf("empty thread validation error = %v", err)
	}
}

func validFreezeBrainToolCatalogCommand(t *testing.T) FreezeBrainToolCatalogCommand {
	t.Helper()
	catalog, err := braincatalog.BuildCatalog("executor", "Deterministic executor tools.", []braincatalog.ToolDescriptor{{
		Name: "read_file", Description: "Read one file.", InputSchema: json.RawMessage(`{"type":"object"}`),
	}}, braincatalog.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	return FreezeBrainToolCatalogCommand{
		CatalogID: stateTestUUID(800), WorkspaceID: stateTestUUID(801), SessionID: stateTestUUID(802),
		RunID: stateTestUUID(803), AttemptID: stateTestUUID(804), HolderID: "pool-holder",
		Generation: 2, ExpectedRunVersion: 3, ExpectedAttemptVersion: 1,
		ContractVersion: "executor-mcp/1.1", CanonicalizerVersion: braincatalog.CatalogCanonicalizer,
		CanonicalCatalog: catalog.CanonicalBytes(), CatalogDigest: catalog.DigestSHA256(),
		PolicyVersion: "executor-policy/1", PolicyContextDigest: sha256.Sum256([]byte("policy-context")),
	}
}
