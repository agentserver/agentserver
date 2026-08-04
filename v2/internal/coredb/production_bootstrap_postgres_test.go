package coredb

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestPostgreSQLProductionBootstrapIsAtomicAndIdempotent(t *testing.T) {
	connectionConfig := postgresIntegrationConfig(t)
	schema := newPostgresTestSchema(t, connectionConfig)
	catalog, err := EmbeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrateConfig(t.Context(), connectionConfig, runnerConfig{
		schema: schema, lockKey: migrationAdvisoryLockKey, catalog: catalog,
	}); err != nil {
		t.Fatal(err)
	}

	bootstrap := validProductionBootstrap()
	first, err := bootstrapProductionConfig(t.Context(), connectionConfig, schema, bootstrap)
	if err != nil || first.CreatedRows != 6 || first.SchemaVersion != int64(len(catalog)) {
		t.Fatalf("first production bootstrap = %+v, %v", first, err)
	}
	retry, err := bootstrapProductionConfig(t.Context(), connectionConfig, schema, bootstrap)
	if err != nil || retry.CreatedRows != 0 || retry.SchemaVersion != first.SchemaVersion {
		t.Fatalf("exact production bootstrap retry = %+v, %v", retry, err)
	}

	connection := openPostgresTestConnection(t, connectionConfig)
	defer connection.Close(context.Background())
	quotedSchema := quoteIdentifier(schema)
	for table, want := range map[string]int{
		"workspaces": 1, "sessions": 1, "users": 1, "user_identities": 1,
		"workspace_members": 1, "executors": 1, "production_bootstrap_seeds": 1,
	} {
		var count int
		if err := connection.QueryRow(
			t.Context(), fmt.Sprintf("SELECT COUNT(*) FROM %s.%s", quotedSchema, quoteIdentifier(table)),
		).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != want {
			t.Fatalf("%s row count = %d, want %d", table, count, want)
		}
	}

	conflicting := bootstrap
	conflicting.SessionID = stateTestUUID(996)
	conflicting.ExternalOIDCSubject = "different-subject"
	if _, err := bootstrapProductionConfig(t.Context(), connectionConfig, schema, conflicting); !errors.Is(err, ErrProductionBootstrapConflict) {
		t.Fatalf("conflicting production bootstrap error = %v", err)
	}
	var insertedSessionCount int
	if err := connection.QueryRow(
		t.Context(), fmt.Sprintf("SELECT COUNT(*) FROM %s.sessions WHERE id = $1", quotedSchema), conflicting.SessionID,
	).Scan(&insertedSessionCount); err != nil {
		t.Fatal(err)
	}
	if insertedSessionCount != 0 {
		t.Fatal("conflicting production bootstrap did not roll back its newly inserted session")
	}
}

func TestPostgreSQLProductionBootstrapAdoptsOnlyExactPreLedgerAuthority(t *testing.T) {
	connectionConfig := postgresIntegrationConfig(t)
	schema := newPostgresTestSchema(t, connectionConfig)
	catalog, err := EmbeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog) < 17 {
		t.Fatalf("migration catalog has only %d entries", len(catalog))
	}
	if _, err := migrateConfig(t.Context(), connectionConfig, runnerConfig{
		schema: schema, lockKey: migrationAdvisoryLockKey, catalog: catalog[:16],
	}); err != nil {
		t.Fatal(err)
	}

	bootstrap := validProductionBootstrap()
	connection := openPostgresTestConnection(t, connectionConfig)
	transaction, err := connection.Begin(t.Context())
	if err != nil {
		connection.Close(context.Background())
		t.Fatal(err)
	}
	quotedSchema := quoteIdentifier(schema)
	for _, step := range []func(context.Context, pgx.Tx, string, ProductionBootstrap) (int, error){
		insertProductionWorkspace,
		insertProductionUser,
		insertProductionIdentity,
		insertProductionMembership,
		insertProductionExecutor,
	} {
		if _, err := step(t.Context(), transaction, quotedSchema, bootstrap); err != nil {
			_ = transaction.Rollback(context.Background())
			connection.Close(context.Background())
			t.Fatal(err)
		}
	}
	// Version 16 predates creator_id, title, and status. Build the historical
	// row shape explicitly so this remains an upgrade test instead of calling
	// the current-schema bootstrap helper against an old schema.
	if _, err := transaction.Exec(
		t.Context(), fmt.Sprintf("INSERT INTO %s.sessions (id, workspace_id) VALUES ($1, $2)", quotedSchema),
		bootstrap.SessionID, bootstrap.WorkspaceID,
	); err != nil {
		_ = transaction.Rollback(context.Background())
		connection.Close(context.Background())
		t.Fatal(err)
	}
	if err := transaction.Commit(t.Context()); err != nil {
		connection.Close(context.Background())
		t.Fatal(err)
	}
	connection.Close(context.Background())

	if _, err := migrateConfig(t.Context(), connectionConfig, runnerConfig{
		schema: schema, lockKey: migrationAdvisoryLockKey, catalog: catalog,
	}); err != nil {
		t.Fatal(err)
	}
	adopted, err := bootstrapProductionConfig(t.Context(), connectionConfig, schema, bootstrap)
	if err != nil || adopted.CreatedRows != 0 || adopted.SchemaVersion != int64(len(catalog)) {
		t.Fatalf("adopt exact pre-ledger production bootstrap = %+v, %v", adopted, err)
	}

	conflicting := bootstrap
	conflicting.SessionID = stateTestUUID(997)
	if _, err := bootstrapProductionConfig(t.Context(), connectionConfig, schema, conflicting); !errors.Is(err, ErrProductionBootstrapConflict) {
		t.Fatalf("adopt conflicting pre-ledger production bootstrap error = %v", err)
	}
}
