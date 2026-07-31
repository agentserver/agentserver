package coreserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/coredb"
)

func TestStateStoreBrainToolCatalogCommandsMapFreezeAndBind(t *testing.T) {
	store := &recordingBrainToolCatalogStore{now: time.Date(2026, 7, 31, 14, 0, 0, 0, time.UTC)}
	commands := StateStoreBrainToolCatalogCommands{Store: store}
	freeze := corecontract.FreezeBrainToolCatalogRequest{
		CatalogID: testBrainToolCatalogID, WorkspaceID: "40000000-0000-4000-8000-000000000004",
		SessionID: "43000000-0000-4000-8000-000000000004", RunID: testRunID,
		RunAttemptID: testRunAttemptID, HolderID: "pool-holder", RunAttemptGeneration: 3,
		ExpectedRunVersion: 2, ExpectedRunAttemptVersion: 1, ContractVersion: "executor-mcp/1.1",
		CanonicalizerVersion: "rfc8785-v1", CanonicalCatalog: json.RawMessage(`{"canonicalizer":"rfc8785-v1"}`),
		CatalogDigest: strings.Repeat("ab", 32), PolicyVersion: "executor-policy/1",
		PolicyContextDigest: strings.Repeat("cd", 32),
	}
	frozen, err := commands.FreezeBrainToolCatalog(t.Context(), freeze)
	if err != nil {
		t.Fatal(err)
	}
	if store.freeze.CatalogDigest[0] != 0xab || store.freeze.PolicyContextDigest[0] != 0xcd ||
		string(store.freeze.CanonicalCatalog) != string(freeze.CanonicalCatalog) || !frozen.Created ||
		frozen.Catalog.CatalogDigest != freeze.CatalogDigest || string(frozen.Catalog.CanonicalCatalog) != string(freeze.CanonicalCatalog) {
		t.Fatalf("freeze store/response = %+v / %+v", store.freeze, frozen)
	}

	bind := corecontract.BindBrainThreadCatalogRequest{
		CatalogID: testBrainToolCatalogID, RunID: testRunID, RunAttemptID: testRunAttemptID,
		HolderID: "pool-holder", RunAttemptGeneration: 3, ExpectedRunVersion: 2,
		ExpectedRunAttemptVersion: 1, ExpectedCatalogVersion: 1, ThreadID: "thread-1",
	}
	bound, err := commands.BindBrainThreadCatalog(t.Context(), bind)
	if err != nil {
		t.Fatal(err)
	}
	if store.bind.ThreadID != bind.ThreadID || !bound.Changed || bound.Catalog.ThreadID != bind.ThreadID {
		t.Fatalf("bind store/response = %+v / %+v", store.bind, bound)
	}
}

func TestStateStoreBrainToolCatalogCommandsRejectNonCanonicalDigest(t *testing.T) {
	store := &recordingBrainToolCatalogStore{}
	commands := StateStoreBrainToolCatalogCommands{Store: store}
	_, err := commands.FreezeBrainToolCatalog(t.Context(), corecontract.FreezeBrainToolCatalogRequest{
		CatalogID: testBrainToolCatalogID, CatalogDigest: strings.Repeat("AB", 32), PolicyContextDigest: strings.Repeat("cd", 32),
	})
	if err == nil || store.freezeCalls != 0 {
		t.Fatalf("uppercase digest error/calls = %v/%d", err, store.freezeCalls)
	}
}

type recordingBrainToolCatalogStore struct {
	now         time.Time
	freeze      coredb.FreezeBrainToolCatalogCommand
	bind        coredb.BindBrainThreadCatalogCommand
	freezeCalls int
}

func (store *recordingBrainToolCatalogStore) FreezeBrainToolCatalog(_ context.Context, command coredb.FreezeBrainToolCatalogCommand) (coredb.FreezeBrainToolCatalogResult, error) {
	store.freeze = command
	store.freezeCalls++
	return coredb.FreezeBrainToolCatalogResult{
		Catalog: testDatabaseBrainToolCatalog(command, store.now, "", 1), Created: true,
	}, nil
}

func (store *recordingBrainToolCatalogStore) BindBrainThreadCatalog(_ context.Context, command coredb.BindBrainThreadCatalogCommand) (coredb.BindBrainThreadCatalogResult, error) {
	store.bind = command
	freeze := store.freeze
	if freeze.CatalogID == "" {
		freeze = coredb.FreezeBrainToolCatalogCommand{
			CatalogID: command.CatalogID, RunID: command.RunID, AttemptID: command.AttemptID,
			HolderID: command.HolderID, Generation: command.Generation,
		}
	}
	return coredb.BindBrainThreadCatalogResult{
		Catalog: testDatabaseBrainToolCatalog(freeze, store.now, command.ThreadID, 2), Changed: true,
	}, nil
}

func testDatabaseBrainToolCatalog(command coredb.FreezeBrainToolCatalogCommand, now time.Time, threadID string, version int64) coredb.BrainToolCatalog {
	return coredb.BrainToolCatalog{
		ID: command.CatalogID, WorkspaceID: command.WorkspaceID, SessionID: command.SessionID,
		CreatedRunID: command.RunID, CreatedRunAttemptID: command.AttemptID,
		CreatedAttemptGeneration: command.Generation, CreatedHolderID: command.HolderID,
		CreatedRunVersion: command.ExpectedRunVersion, CreatedAttemptVersion: command.ExpectedAttemptVersion,
		ThreadID: threadID, ContractVersion: command.ContractVersion, CanonicalizerVersion: command.CanonicalizerVersion,
		CanonicalCatalog: append([]byte(nil), command.CanonicalCatalog...), CatalogDigest: command.CatalogDigest,
		PolicyVersion: command.PolicyVersion, PolicyContextDigest: command.PolicyContextDigest,
		Version: version, CreatedAt: now, UpdatedAt: now,
	}
}
