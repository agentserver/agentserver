package coredb

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestWithStateTransactionCommitErrorReturnsZeroResult(t *testing.T) {
	store := newStateStore(commitErrorDatabase{}, "commit_error_test")
	result, err := withStateTransaction(t.Context(), store, "commit boundary", func(pgx.Tx) (int, error) {
		return 42, nil
	})
	if result != 0 {
		t.Fatalf("withStateTransaction() result = %d, want zero when commit result is unknown", result)
	}
	if !HasStateErrorCode(err, ErrorDatabase) {
		t.Fatalf("withStateTransaction() error = %v, want database_error", err)
	}
}

func TestWithStateTransactionCommandErrorReturnsZeroResult(t *testing.T) {
	store := newStateStore(commitErrorDatabase{}, "command_error_test")
	result, err := withStateTransaction(t.Context(), store, "command boundary", func(pgx.Tx) (int, error) {
		return 42, commandError(ErrorConflict, "test", "operation", "", "forced rollback")
	})
	if result != 0 {
		t.Fatalf("withStateTransaction() result = %d, want zero when the transaction rolls back", result)
	}
	if !HasStateErrorCode(err, ErrorConflict) {
		t.Fatalf("withStateTransaction() error = %v, want conflict", err)
	}
}

func TestWithStateReadTransactionUsesRepeatableReadOnlySnapshot(t *testing.T) {
	database := &optionsRecordingDatabase{}
	store := newStateStore(database, "read_options_test")
	result, err := withStateReadTransaction(t.Context(), store, "read boundary", func(pgx.Tx) (int, error) {
		return 42, nil
	})
	if err != nil || result != 42 {
		t.Fatalf("withStateReadTransaction() = %d, %v", result, err)
	}
	if database.options.IsoLevel != pgx.RepeatableRead || database.options.AccessMode != pgx.ReadOnly {
		t.Fatalf("read transaction options = %+v", database.options)
	}
}

type commitErrorDatabase struct{}

func (commitErrorDatabase) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) {
	return commitErrorTransaction{}, nil
}

type optionsRecordingDatabase struct {
	options pgx.TxOptions
}

func (database *optionsRecordingDatabase) BeginTx(_ context.Context, options pgx.TxOptions) (pgx.Tx, error) {
	database.options = options
	return successfulStateTransaction{}, nil
}

type successfulStateTransaction struct {
	commitErrorTransaction
}

func (successfulStateTransaction) Commit(context.Context) error { return nil }

type commitErrorTransaction struct{}

func (commitErrorTransaction) Begin(context.Context) (pgx.Tx, error) {
	return nil, errors.New("unexpected nested transaction")
}

func (commitErrorTransaction) Commit(context.Context) error {
	return errors.New("commit result unknown")
}

func (commitErrorTransaction) Rollback(context.Context) error { return pgx.ErrTxClosed }

func (commitErrorTransaction) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, errors.New("unexpected CopyFrom")
}

func (commitErrorTransaction) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults {
	panic("unexpected SendBatch")
}

func (commitErrorTransaction) LargeObjects() pgx.LargeObjects { return pgx.LargeObjects{} }

func (commitErrorTransaction) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, errors.New("unexpected Prepare")
}

func (commitErrorTransaction) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("unexpected Exec")
}

func (commitErrorTransaction) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("unexpected Query")
}

func (commitErrorTransaction) QueryRow(context.Context, string, ...any) pgx.Row {
	panic("unexpected QueryRow")
}

func (commitErrorTransaction) Conn() *pgx.Conn { return nil }
