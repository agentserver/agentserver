package coredb

import (
	"context"
	"errors"

	"github.com/agentserver/agentserver/v2/internal/managedsandboxprofile"

	"github.com/jackc/pgx/v5"
)

// TransactionDatabase is the pgx transaction surface required by StateStore.
// Both pgx.Conn and pgxpool.Pool implement it.
type TransactionDatabase interface {
	BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
}

// StateStore is the only PostgreSQL write boundary for Phase 1 run and
// execution state.
type StateStore struct {
	database             TransactionDatabase
	schema               string
	managedDefaultRegion string
	managedProfileIDs    []string
}

// WithManagedSandboxCatalog configures deployment-owned shared profile IDs,
// not user-supplied environment authority. Per-run/operation ownership remains
// bound to the session's workspace in managed_sandboxes and dispatch checks.
func (s *StateStore) WithManagedSandboxCatalog(catalog *managedsandboxprofile.Catalog) *StateStore {
	copy := *s
	copy.managedDefaultRegion = ""
	copy.managedProfileIDs = nil
	if catalog != nil {
		copy.managedDefaultRegion = catalog.DefaultRegion()
		for _, binding := range catalog.Bindings() {
			// Every deployment-owned profile is a valid shared Kubernetes/TAE
			// environment. Keep the complete catalog here so a workspace that
			// selects CN can reserve its CN environment just like SG.
			copy.managedProfileIDs = append(copy.managedProfileIDs, binding.EnvironmentID)
		}
	}
	return &copy
}

func (s *StateStore) defaultManagedRegion() string {
	if s.managedDefaultRegion != "" {
		return s.managedDefaultRegion
	}
	return managedsandboxprofile.DefaultRegion
}

// NewStateStore constructs a production store against agentserver_v2.
func NewStateStore(database TransactionDatabase) *StateStore {
	return newStateStore(database, SchemaName)
}

func newStateStore(database TransactionDatabase, schema string) *StateStore {
	if database == nil {
		panic("coredb: nil state store database")
	}
	if !schemaNamePattern.MatchString(schema) {
		panic("coredb: invalid state store schema")
	}
	return &StateStore{database: database, schema: schema}
}

func (s *StateStore) table(name string) string {
	return quoteIdentifier(s.schema) + "." + quoteIdentifier(name)
}

func withStateTransaction[T any](ctx context.Context, store *StateStore, operation string, command func(pgx.Tx) (T, error)) (result T, returnErr error) {
	return withStateTransactionOptions(ctx, store, operation, pgx.TxOptions{}, command)
}

// withStateReadTransaction gives multi-query authorization checks one
// repeatable, read-only snapshot without taking locks that would delay lease
// heartbeats. A successful read is only evidence for that instant; callers
// still re-run it for every externally authorized request.
func withStateReadTransaction[T any](ctx context.Context, store *StateStore, operation string, query func(pgx.Tx) (T, error)) (result T, returnErr error) {
	return withStateTransactionOptions(ctx, store, operation, pgx.TxOptions{
		IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly,
	}, query)
}

func withStateTransactionOptions[T any](ctx context.Context, store *StateStore, operation string, options pgx.TxOptions, command func(pgx.Tx) (T, error)) (result T, returnErr error) {
	transaction, err := store.database.BeginTx(ctx, options)
	if err != nil {
		return result, databaseError(operation, err)
	}
	defer func() {
		rollbackContext, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()
		if err := transaction.Rollback(rollbackContext); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			returnErr = errors.Join(returnErr, databaseError(operation+" rollback", err))
		}
	}()

	result, err = command(transaction)
	if err != nil {
		var zero T
		return zero, err
	}
	if err := transaction.Commit(ctx); err != nil {
		var zero T
		return zero, databaseError(operation+" commit", err)
	}
	return result, nil
}
