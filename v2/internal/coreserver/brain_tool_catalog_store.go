package coreserver

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/coredb"
)

type BrainToolCatalogStateStore interface {
	FreezeBrainToolCatalog(context.Context, coredb.FreezeBrainToolCatalogCommand) (coredb.FreezeBrainToolCatalogResult, error)
	BindBrainThreadCatalog(context.Context, coredb.BindBrainThreadCatalogCommand) (coredb.BindBrainThreadCatalogResult, error)
}

type StateStoreBrainToolCatalogCommands struct {
	Store BrainToolCatalogStateStore
}

var _ BrainToolCatalogCommands = StateStoreBrainToolCatalogCommands{}

func (commands StateStoreBrainToolCatalogCommands) FreezeBrainToolCatalog(ctx context.Context, request corecontract.FreezeBrainToolCatalogRequest) (corecontract.FreezeBrainToolCatalogResponse, error) {
	if commands.Store == nil {
		return corecontract.FreezeBrainToolCatalogResponse{}, errors.New("nil core state store")
	}
	catalogDigest, err := decodeCanonicalSHA256(request.CatalogDigest)
	if err != nil {
		return corecontract.FreezeBrainToolCatalogResponse{}, brainToolCatalogConversionError("FreezeBrainToolCatalog", request.CatalogID, fmt.Errorf("catalogDigest: %w", err))
	}
	policyContextDigest, err := decodeCanonicalSHA256(request.PolicyContextDigest)
	if err != nil {
		return corecontract.FreezeBrainToolCatalogResponse{}, brainToolCatalogConversionError("FreezeBrainToolCatalog", request.CatalogID, fmt.Errorf("policyContextDigest: %w", err))
	}
	result, err := commands.Store.FreezeBrainToolCatalog(ctx, coredb.FreezeBrainToolCatalogCommand{
		CatalogID: request.CatalogID, WorkspaceID: request.WorkspaceID, SessionID: request.SessionID,
		RunID: request.RunID, AttemptID: request.RunAttemptID, HolderID: request.HolderID,
		Generation: request.RunAttemptGeneration, ExpectedRunVersion: request.ExpectedRunVersion,
		ExpectedAttemptVersion: request.ExpectedRunAttemptVersion, ContractVersion: request.ContractVersion,
		CanonicalizerVersion: request.CanonicalizerVersion, CanonicalCatalog: append([]byte(nil), request.CanonicalCatalog...),
		CatalogDigest: catalogDigest, PolicyVersion: request.PolicyVersion, PolicyContextDigest: policyContextDigest,
	})
	if err != nil {
		return corecontract.FreezeBrainToolCatalogResponse{}, err
	}
	return corecontract.FreezeBrainToolCatalogResponse{Catalog: contractBrainToolCatalog(result.Catalog), Created: result.Created}, nil
}

func (commands StateStoreBrainToolCatalogCommands) BindBrainThreadCatalog(ctx context.Context, request corecontract.BindBrainThreadCatalogRequest) (corecontract.BindBrainThreadCatalogResponse, error) {
	if commands.Store == nil {
		return corecontract.BindBrainThreadCatalogResponse{}, errors.New("nil core state store")
	}
	result, err := commands.Store.BindBrainThreadCatalog(ctx, coredb.BindBrainThreadCatalogCommand{
		CatalogID: request.CatalogID, RunID: request.RunID, AttemptID: request.RunAttemptID,
		HolderID: request.HolderID, Generation: request.RunAttemptGeneration,
		ExpectedRunVersion: request.ExpectedRunVersion, ExpectedAttemptVersion: request.ExpectedRunAttemptVersion,
		ExpectedCatalogVersion: request.ExpectedCatalogVersion, ThreadID: request.ThreadID,
	})
	if err != nil {
		return corecontract.BindBrainThreadCatalogResponse{}, err
	}
	return corecontract.BindBrainThreadCatalogResponse{Catalog: contractBrainToolCatalog(result.Catalog), Changed: result.Changed}, nil
}

func contractBrainToolCatalog(catalog coredb.BrainToolCatalog) corecontract.BrainToolCatalogState {
	return corecontract.BrainToolCatalogState{
		CatalogID: catalog.ID, WorkspaceID: catalog.WorkspaceID, SessionID: catalog.SessionID,
		CreatedRunID: catalog.CreatedRunID, CreatedRunAttemptID: catalog.CreatedRunAttemptID,
		CreatedAttemptGeneration: catalog.CreatedAttemptGeneration, CreatedHolderID: catalog.CreatedHolderID,
		CreatedRunVersion: catalog.CreatedRunVersion, CreatedAttemptVersion: catalog.CreatedAttemptVersion,
		ThreadID: catalog.ThreadID, ContractVersion: catalog.ContractVersion,
		CanonicalizerVersion: catalog.CanonicalizerVersion, CanonicalCatalog: append([]byte(nil), catalog.CanonicalCatalog...),
		CatalogDigest: hex.EncodeToString(catalog.CatalogDigest[:]), PolicyVersion: catalog.PolicyVersion,
		PolicyContextDigest: hex.EncodeToString(catalog.PolicyContextDigest[:]), Version: catalog.Version,
		CreatedAt: catalog.CreatedAt, UpdatedAt: catalog.UpdatedAt,
	}
}

func brainToolCatalogConversionError(operation, catalogID string, err error) error {
	return &coredb.StateError{
		Code: coredb.ErrorInvalidArgument, Operation: operation, Resource: "brain_tool_catalog", ResourceID: catalogID,
		Message: fmt.Sprintf("invalid internal command: %v", err),
	}
}
