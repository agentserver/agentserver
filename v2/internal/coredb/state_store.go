package coredb

import (
	"context"
	"errors"

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
	database TransactionDatabase
	schema   string
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
	transaction, err := store.database.BeginTx(ctx, pgx.TxOptions{})
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
