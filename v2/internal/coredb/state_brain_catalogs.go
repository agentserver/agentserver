package coredb

import (
	"bytes"
	"context"
	"crypto/subtle"
	"errors"
	"fmt"

	"github.com/agentserver/agentserver/v2/internal/braincatalog"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func (s *StateStore) FreezeBrainToolCatalog(ctx context.Context, command FreezeBrainToolCatalogCommand) (FreezeBrainToolCatalogResult, error) {
	const operation = "FreezeBrainToolCatalog"
	canonical, err := validateFreezeBrainToolCatalog(command)
	if err != nil {
		return FreezeBrainToolCatalogResult{}, commandError(ErrorInvalidArgument, operation, "brain_tool_catalog", command.CatalogID, err.Error())
	}

	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (FreezeBrainToolCatalogResult, error) {
		run, err := s.lockRun(ctx, transaction, operation, command.RunID)
		if err != nil {
			return FreezeBrainToolCatalogResult{}, err
		}

		existingQuery := fmt.Sprintf(`
SELECT %s
FROM %s AS c
WHERE c.id = $1
FOR UPDATE`, brainToolCatalogColumns("c"), s.table("brain_tool_catalogs"))
		existing, err := scanBrainToolCatalog(transaction.QueryRow(ctx, existingQuery, command.CatalogID))
		if err == nil {
			if !brainToolCatalogMatchesFreeze(existing, command) {
				return FreezeBrainToolCatalogResult{}, &StateError{
					Code:       ErrorIdempotencyConflict,
					Operation:  operation,
					Resource:   "brain_tool_catalog",
					ResourceID: existing.ID,
					Message:    "catalog identity was already used with a different frozen catalog fingerprint",
				}
			}
			return FreezeBrainToolCatalogResult{Catalog: existing, Created: false}, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return FreezeBrainToolCatalogResult{}, databaseError(operation+" read catalog identity", err)
		}

		attemptCatalogQuery := fmt.Sprintf(`
SELECT id::text
FROM %s
WHERE created_run_attempt_id = $1
FOR UPDATE`, s.table("brain_tool_catalogs"))
		var existingCatalogID string
		err = transaction.QueryRow(ctx, attemptCatalogQuery, command.AttemptID).Scan(&existingCatalogID)
		if err == nil {
			return FreezeBrainToolCatalogResult{}, commandError(ErrorIdempotencyConflict, operation, "attempt", command.AttemptID, "run attempt already has a different frozen brain tool catalog "+existingCatalogID)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return FreezeBrainToolCatalogResult{}, databaseError(operation+" read attempt catalog", err)
		}

		attempt, err := s.lockAttempt(ctx, transaction, operation, command.RunID, command.AttemptID)
		if err != nil {
			return FreezeBrainToolCatalogResult{}, err
		}
		if run.WorkspaceID != command.WorkspaceID || run.SessionID != command.SessionID {
			return FreezeBrainToolCatalogResult{}, commandError(ErrorConflict, operation, "run", run.ID, "run does not belong to the requested workspace and session")
		}
		if run.Version != command.ExpectedRunVersion {
			return FreezeBrainToolCatalogResult{}, versionConflict(operation, "run", run.ID, run.Version)
		}
		if attempt.Version != command.ExpectedAttemptVersion {
			return FreezeBrainToolCatalogResult{}, versionConflict(operation, "attempt", attempt.ID, attempt.Version)
		}
		if err := s.requireLiveCatalogPreparationContext(ctx, transaction, operation, run, attempt, command.HolderID, command.Generation); err != nil {
			return FreezeBrainToolCatalogResult{}, err
		}

		catalogDigest := canonical.DigestSHA256()
		insertQuery := fmt.Sprintf(`
INSERT INTO %s
    (id, workspace_id, session_id,
     created_run_id, created_run_attempt_id, created_attempt_generation,
     created_holder_id, created_run_version, created_attempt_version,
     contract_version, canonicalizer_version, canonical_catalog, catalog_digest,
     policy_version, policy_context_digest)
VALUES
    ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
RETURNING %s`, s.table("brain_tool_catalogs"), brainToolCatalogColumns(""))
		inserted, err := scanBrainToolCatalog(transaction.QueryRow(ctx, insertQuery,
			command.CatalogID,
			command.WorkspaceID,
			command.SessionID,
			command.RunID,
			command.AttemptID,
			command.Generation,
			command.HolderID,
			command.ExpectedRunVersion,
			command.ExpectedAttemptVersion,
			command.ContractVersion,
			command.CanonicalizerVersion,
			canonical.CanonicalBytes(),
			catalogDigest[:],
			command.PolicyVersion,
			command.PolicyContextDigest[:],
		))
		if err != nil {
			var postgresError *pgconn.PgError
			if pgxErrorAs(err, &postgresError) && postgresError.Code == "23505" {
				return FreezeBrainToolCatalogResult{}, commandError(ErrorConflict, operation, "brain_tool_catalog", command.CatalogID, "catalog identity or run attempt is already in use")
			}
			return FreezeBrainToolCatalogResult{}, databaseError(operation+" insert catalog", err)
		}
		return FreezeBrainToolCatalogResult{Catalog: inserted, Created: true}, nil
	})
}

func (s *StateStore) BindBrainThreadCatalog(ctx context.Context, command BindBrainThreadCatalogCommand) (BindBrainThreadCatalogResult, error) {
	const operation = "BindBrainThreadCatalog"
	if err := validateBindBrainThreadCatalog(command); err != nil {
		return BindBrainThreadCatalogResult{}, commandError(ErrorInvalidArgument, operation, "brain_tool_catalog", command.CatalogID, err.Error())
	}

	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (BindBrainThreadCatalogResult, error) {
		run, err := s.lockRun(ctx, transaction, operation, command.RunID)
		if err != nil {
			return BindBrainThreadCatalogResult{}, err
		}
		catalogQuery := fmt.Sprintf(`
SELECT %s
FROM %s AS c
WHERE c.id = $1
FOR UPDATE`, brainToolCatalogColumns("c"), s.table("brain_tool_catalogs"))
		catalog, err := scanBrainToolCatalog(transaction.QueryRow(ctx, catalogQuery, command.CatalogID))
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return BindBrainThreadCatalogResult{}, commandError(ErrorNotFound, operation, "brain_tool_catalog", command.CatalogID, "catalog does not exist")
			}
			return BindBrainThreadCatalogResult{}, databaseError(operation+" lock catalog", err)
		}
		if catalog.CreatedRunID != command.RunID || catalog.CreatedRunAttemptID != command.AttemptID ||
			catalog.CreatedAttemptGeneration != command.Generation || catalog.CreatedHolderID != command.HolderID {
			return BindBrainThreadCatalogResult{}, fencedAttemptError(operation, command.AttemptID, run.CurrentAttemptGeneration, "catalog belongs to a different attempt authority tuple")
		}
		if catalog.ThreadID != "" {
			if catalog.ThreadID != command.ThreadID {
				return BindBrainThreadCatalogResult{}, commandError(ErrorIdempotencyConflict, operation, "brain_tool_catalog", catalog.ID, "catalog is already bound to a different brain thread")
			}
			return BindBrainThreadCatalogResult{Catalog: catalog, Changed: false}, nil
		}

		attempt, err := s.lockAttempt(ctx, transaction, operation, command.RunID, command.AttemptID)
		if err != nil {
			return BindBrainThreadCatalogResult{}, err
		}
		if run.Version != command.ExpectedRunVersion {
			return BindBrainThreadCatalogResult{}, versionConflict(operation, "run", run.ID, run.Version)
		}
		if attempt.Version != command.ExpectedAttemptVersion {
			return BindBrainThreadCatalogResult{}, versionConflict(operation, "attempt", attempt.ID, attempt.Version)
		}
		if catalog.Version != command.ExpectedCatalogVersion {
			return BindBrainThreadCatalogResult{}, versionConflict(operation, "brain_tool_catalog", catalog.ID, catalog.Version)
		}
		if err := s.requireLiveCatalogPreparationContext(ctx, transaction, operation, run, attempt, command.HolderID, command.Generation); err != nil {
			return BindBrainThreadCatalogResult{}, err
		}

		updateQuery := fmt.Sprintf(`
UPDATE %s
SET thread_id = $1,
    version = version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $2 AND version = $3 AND thread_id IS NULL
RETURNING %s`, s.table("brain_tool_catalogs"), brainToolCatalogColumns(""))
		updated, err := scanBrainToolCatalog(transaction.QueryRow(ctx, updateQuery, command.ThreadID, catalog.ID, catalog.Version))
		if err != nil {
			var postgresError *pgconn.PgError
			if pgxErrorAs(err, &postgresError) && postgresError.Code == "23505" {
				return BindBrainThreadCatalogResult{}, commandError(ErrorConflict, operation, "brain_thread", command.ThreadID, "brain thread is already bound to another frozen catalog")
			}
			if errors.Is(err, pgx.ErrNoRows) {
				return BindBrainThreadCatalogResult{}, versionConflict(operation, "brain_tool_catalog", catalog.ID, catalog.Version)
			}
			return BindBrainThreadCatalogResult{}, databaseError(operation+" bind thread", err)
		}
		return BindBrainThreadCatalogResult{Catalog: updated, Changed: true}, nil
	})
}

func validateFreezeBrainToolCatalog(command FreezeBrainToolCatalogCommand) (*braincatalog.Catalog, error) {
	identifiers := []struct {
		field string
		value string
	}{
		{"catalog_id", command.CatalogID},
		{"workspace_id", command.WorkspaceID},
		{"session_id", command.SessionID},
		{"run_id", command.RunID},
		{"attempt_id", command.AttemptID},
	}
	for _, identifier := range identifiers {
		if err := validateUUID(identifier.field, identifier.value); err != nil {
			return nil, err
		}
	}
	if err := validateBoundedText("holder_id", command.HolderID, 256); err != nil {
		return nil, err
	}
	if command.Generation < 1 || command.ExpectedRunVersion < 1 || command.ExpectedAttemptVersion < 1 {
		return nil, errors.New("generation and expected versions must be positive")
	}
	if err := validateBoundedText("contract_version", command.ContractVersion, 128); err != nil {
		return nil, err
	}
	if command.CanonicalizerVersion != braincatalog.CatalogCanonicalizer {
		return nil, fmt.Errorf("canonicalizer_version must be %q", braincatalog.CatalogCanonicalizer)
	}
	if err := validateBoundedText("policy_version", command.PolicyVersion, 128); err != nil {
		return nil, err
	}
	if command.PolicyContextDigest == ([32]byte{}) {
		return nil, errors.New("policy_context_digest is required")
	}
	catalog, err := braincatalog.ParseCanonical(command.CanonicalCatalog, braincatalog.DefaultLimits())
	if err != nil {
		return nil, fmt.Errorf("canonical_catalog: %w", err)
	}
	gotDigest := catalog.DigestSHA256()
	if subtle.ConstantTimeCompare(gotDigest[:], command.CatalogDigest[:]) != 1 {
		return nil, errors.New("catalog_digest does not match canonical_catalog")
	}
	return catalog, nil
}

func validateBindBrainThreadCatalog(command BindBrainThreadCatalogCommand) error {
	for _, identifier := range []struct {
		field string
		value string
	}{
		{"catalog_id", command.CatalogID},
		{"run_id", command.RunID},
		{"attempt_id", command.AttemptID},
	} {
		if err := validateUUID(identifier.field, identifier.value); err != nil {
			return err
		}
	}
	if err := validateBoundedText("holder_id", command.HolderID, 256); err != nil {
		return err
	}
	if err := validateBoundedText("thread_id", command.ThreadID, 256); err != nil {
		return err
	}
	if command.Generation < 1 || command.ExpectedRunVersion < 1 || command.ExpectedAttemptVersion < 1 || command.ExpectedCatalogVersion < 1 {
		return errors.New("generation and expected versions must be positive")
	}
	return nil
}

func brainToolCatalogMatchesFreeze(catalog BrainToolCatalog, command FreezeBrainToolCatalogCommand) bool {
	return catalog.ID == command.CatalogID &&
		catalog.WorkspaceID == command.WorkspaceID &&
		catalog.SessionID == command.SessionID &&
		catalog.CreatedRunID == command.RunID &&
		catalog.CreatedRunAttemptID == command.AttemptID &&
		catalog.CreatedAttemptGeneration == command.Generation &&
		catalog.CreatedHolderID == command.HolderID &&
		catalog.CreatedRunVersion == command.ExpectedRunVersion &&
		catalog.CreatedAttemptVersion == command.ExpectedAttemptVersion &&
		catalog.ContractVersion == command.ContractVersion &&
		catalog.CanonicalizerVersion == command.CanonicalizerVersion &&
		bytes.Equal(catalog.CanonicalCatalog, command.CanonicalCatalog) &&
		subtle.ConstantTimeCompare(catalog.CatalogDigest[:], command.CatalogDigest[:]) == 1 &&
		catalog.PolicyVersion == command.PolicyVersion &&
		subtle.ConstantTimeCompare(catalog.PolicyContextDigest[:], command.PolicyContextDigest[:]) == 1
}

func (s *StateStore) requireLiveCatalogPreparationContext(
	ctx context.Context,
	transaction pgx.Tx,
	operation string,
	run Run,
	attempt RunAttempt,
	holderID string,
	generation int64,
) error {
	if run.CurrentAttemptGeneration != generation || attempt.Generation != generation {
		return fencedAttemptError(operation, attempt.ID, run.CurrentAttemptGeneration, "attempt generation was fenced")
	}
	if attempt.HolderID != holderID {
		return fencedAttemptError(operation, attempt.ID, run.CurrentAttemptGeneration, "attempt holder was fenced")
	}
	if run.Status != RunStatusStarting || (attempt.Status != AttemptStatusLeased && attempt.Status != AttemptStatusStarting) || attempt.TurnStartedAt != nil {
		return commandError(ErrorInvalidState, operation, "attempt", attempt.ID, "brain tool catalog preparation must complete before turn acceptance")
	}
	activeQuery := fmt.Sprintf("SELECT active_run_id::text FROM %s WHERE id = $1", s.table("sessions"))
	var activeRunID *string
	if err := transaction.QueryRow(ctx, activeQuery, run.SessionID).Scan(&activeRunID); err != nil {
		return databaseError(operation+" read active run", err)
	}
	if activeRunID == nil || *activeRunID != run.ID {
		return commandError(ErrorInvalidState, operation, "run", run.ID, "run is not the session active run")
	}
	return s.requireLiveLeases(ctx, transaction, run, attempt, holderID, generation)
}
