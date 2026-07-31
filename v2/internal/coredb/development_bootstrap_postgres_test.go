package coredb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

func TestPostgreSQLInsecureDevelopmentBootstrapIsAtomicAndIdempotent(t *testing.T) {
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

	bootstrap := validInsecureDevelopmentBootstrap()
	first, err := bootstrapInsecureDevelopmentConfig(t.Context(), connectionConfig, schema, bootstrap)
	if err != nil || first.CreatedRows != 5 {
		t.Fatalf("first development bootstrap = %+v, %v", first, err)
	}
	retry, err := bootstrapInsecureDevelopmentConfig(t.Context(), connectionConfig, schema, bootstrap)
	if err != nil || retry.CreatedRows != 0 {
		t.Fatalf("exact development bootstrap retry = %+v, %v", retry, err)
	}

	connection := openPostgresTestConnection(t, connectionConfig)
	defer connection.Close(context.Background())
	quotedSchema := quoteIdentifier(schema)
	for table, want := range map[string]int{
		"workspaces": 1, "sessions": 1, "workspace_members": 1,
		"executors": 1, "executor_environments": 1,
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
	conflicting.Environment.RootDescriptor = json.RawMessage(`{"kind":"local","root":"/different"}`)
	if _, err := bootstrapInsecureDevelopmentConfig(t.Context(), connectionConfig, schema, conflicting); !errors.Is(err, ErrInsecureDevelopmentBootstrapConflict) {
		t.Fatalf("conflicting development bootstrap error = %v", err)
	}
	var insertedSessionCount int
	if err := connection.QueryRow(
		t.Context(), fmt.Sprintf("SELECT COUNT(*) FROM %s.sessions WHERE id = $1", quotedSchema), conflicting.SessionID,
	).Scan(&insertedSessionCount); err != nil {
		t.Fatal(err)
	}
	if insertedSessionCount != 0 {
		t.Fatal("conflicting development bootstrap did not roll back its newly inserted session")
	}
}
