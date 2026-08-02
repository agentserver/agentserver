package coredb

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const postgresTestRunEnvironment = "AGENTSERVER_RUN_POSTGRES_TESTS"

func TestPostgreSQLMigrationKernel(t *testing.T) {
	connectionConfig := postgresIntegrationConfig(t)
	catalog, err := EmbeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	schema := newPostgresTestSchema(t, connectionConfig)
	runner := runnerConfig{schema: schema, lockKey: migrationAdvisoryLockKey, catalog: catalog}

	result, err := migrateConfig(t.Context(), connectionConfig, runner)
	if err != nil {
		t.Fatalf("first migrateConfig() error = %v", err)
	}
	wantVersion := catalog[len(catalog)-1].Version
	if result.Applied != len(catalog) || result.CurrentVersion != wantVersion {
		t.Fatalf("first migration result = %+v, want %d applied migrations at version %d", result, len(catalog), wantVersion)
	}
	result, err = migrateConfig(t.Context(), connectionConfig, runner)
	if err != nil {
		t.Fatalf("repeat migrateConfig() error = %v", err)
	}
	if result.Applied != 0 || result.CurrentVersion != wantVersion {
		t.Fatalf("repeat migration result = %+v, want no-op at version %d", result, wantVersion)
	}

	connection := openPostgresTestConnection(t, connectionConfig)
	defer connection.Close(context.Background())
	assertDatabaseObjects(t, connection, schema)
	assertKernelConstraints(t, connection, schema)
}

func TestPostgreSQLConcurrentMigrationRunsOnce(t *testing.T) {
	connectionConfig := postgresIntegrationConfig(t)
	schema := newPostgresTestSchema(t, connectionConfig)
	catalog := []Migration{migrationForTest(1, "concurrent_once", `
CREATE TABLE migration_marker (
    id integer PRIMARY KEY
);
INSERT INTO migration_marker (id) VALUES (1);
SELECT pg_catalog.pg_sleep(0.2);
`)}
	runner := runnerConfig{schema: schema, lockKey: migrationAdvisoryLockKey, catalog: catalog}

	start := make(chan struct{})
	results := make(chan MigrationResult, 2)
	errorsChannel := make(chan error, 2)
	var waitGroup sync.WaitGroup
	for range 2 {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			<-start
			result, err := migrateConfig(t.Context(), connectionConfig, runner)
			results <- result
			errorsChannel <- err
		}()
	}
	close(start)
	waitGroup.Wait()
	close(results)
	close(errorsChannel)

	for err := range errorsChannel {
		if err != nil {
			t.Fatalf("concurrent migrateConfig() error = %v", err)
		}
	}
	totalApplied := 0
	for result := range results {
		if result.CurrentVersion != 1 {
			t.Fatalf("concurrent migration result = %+v, want current version 1", result)
		}
		totalApplied += result.Applied
	}
	if totalApplied != 1 {
		t.Fatalf("concurrent runners applied %d migrations in total, want exactly 1", totalApplied)
	}

	connection := openPostgresTestConnection(t, connectionConfig)
	defer connection.Close(context.Background())
	var historyCount int
	if err := connection.QueryRow(t.Context(), fmt.Sprintf("SELECT pg_catalog.count(*) FROM %s.schema_migrations", quoteIdentifier(schema))).Scan(&historyCount); err != nil {
		t.Fatal(err)
	}
	var markerCount int
	if err := connection.QueryRow(t.Context(), fmt.Sprintf("SELECT pg_catalog.count(*) FROM %s.migration_marker", quoteIdentifier(schema))).Scan(&markerCount); err != nil {
		t.Fatal(err)
	}
	if historyCount != 1 || markerCount != 1 {
		t.Fatalf("history rows = %d, marker rows = %d; want exactly one of each", historyCount, markerCount)
	}
}

func TestPostgreSQLRejectsTamperedChecksum(t *testing.T) {
	connectionConfig := postgresIntegrationConfig(t)
	schema := newPostgresTestSchema(t, connectionConfig)
	catalog := []Migration{migrationForTest(1, "checksum", "CREATE TABLE checksum_marker (id integer PRIMARY KEY);\n")}
	runner := runnerConfig{schema: schema, lockKey: migrationAdvisoryLockKey, catalog: catalog}
	if _, err := migrateConfig(t.Context(), connectionConfig, runner); err != nil {
		t.Fatalf("initial migrateConfig() error = %v", err)
	}

	connection := openPostgresTestConnection(t, connectionConfig)
	update := fmt.Sprintf("UPDATE %s.schema_migrations SET sha256 = pg_catalog.decode(pg_catalog.repeat('00', 32), 'hex') WHERE version = 1", quoteIdentifier(schema))
	if _, err := connection.Exec(t.Context(), update); err != nil {
		connection.Close(context.Background())
		t.Fatal(err)
	}
	connection.Close(context.Background())

	_, err := migrateConfig(t.Context(), connectionConfig, runner)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("migrateConfig() error = %v, want checksum mismatch", err)
	}
}

func TestPostgreSQLFailedMigrationRollsBack(t *testing.T) {
	connectionConfig := postgresIntegrationConfig(t)
	schema := newPostgresTestSchema(t, connectionConfig)
	catalog := []Migration{
		migrationForTest(1, "durable_first", "CREATE TABLE durable_marker (id integer PRIMARY KEY);\n"),
		migrationForTest(2, "rollback_second", "CREATE TABLE rolled_back_marker (id integer PRIMARY KEY);\nSELECT 1 / 0;\n"),
	}
	runner := runnerConfig{schema: schema, lockKey: migrationAdvisoryLockKey, catalog: catalog}

	result, err := migrateConfig(t.Context(), connectionConfig, runner)
	if err == nil || !strings.Contains(err.Error(), "execute migration 0002_rollback_second") {
		t.Fatalf("migrateConfig() result = %+v, error = %v; want second migration failure", result, err)
	}
	if result.Applied != 1 || result.CurrentVersion != 1 {
		t.Fatalf("migration result after failure = %+v, want first migration committed", result)
	}

	connection := openPostgresTestConnection(t, connectionConfig)
	defer connection.Close(context.Background())
	var appliedCount int
	if err := connection.QueryRow(t.Context(), fmt.Sprintf("SELECT pg_catalog.count(*) FROM %s.schema_migrations", quoteIdentifier(schema))).Scan(&appliedCount); err != nil {
		t.Fatal(err)
	}
	if appliedCount != 1 {
		t.Fatalf("applied migration count = %d, want 1", appliedCount)
	}
	var rolledBackTable *string
	qualifiedTable := schema + ".rolled_back_marker"
	if err := connection.QueryRow(t.Context(), "SELECT pg_catalog.to_regclass($1)::text", qualifiedTable).Scan(&rolledBackTable); err != nil {
		t.Fatal(err)
	}
	if rolledBackTable != nil {
		t.Fatalf("failed migration left table %q behind", *rolledBackTable)
	}

	retryContext, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, err := migrateConfig(retryContext, connectionConfig, runner); err == nil || !strings.Contains(err.Error(), "execute migration 0002_rollback_second") {
		t.Fatalf("retry migrateConfig() error = %v, want the same migration failure after lock release", err)
	}
}

func TestPostgreSQLMigration0002RejectsAmbiguousDevelopmentEvents(t *testing.T) {
	connectionConfig := postgresIntegrationConfig(t)
	schema := newPostgresTestSchema(t, connectionConfig)
	catalog, err := EmbeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog) < 2 {
		t.Fatal("embedded catalog does not contain migration 0002")
	}
	runner := runnerConfig{schema: schema, lockKey: migrationAdvisoryLockKey, catalog: catalog[:1]}
	if _, err := migrateConfig(t.Context(), connectionConfig, runner); err != nil {
		t.Fatalf("apply migration 0001: %v", err)
	}

	connection := openPostgresTestConnection(t, connectionConfig)
	quotedSchema := quoteIdentifier(schema)
	workspaceID := "71000000-0000-0000-0000-000000000001"
	sessionID := "72000000-0000-0000-0000-000000000001"
	runID := "73000000-0000-0000-0000-000000000001"
	if _, err := connection.Exec(t.Context(), fmt.Sprintf("INSERT INTO %s.workspaces (id, status) VALUES ($1, 'active')", quotedSchema), workspaceID); err != nil {
		connection.Close(context.Background())
		t.Fatal(err)
	}
	if _, err := connection.Exec(t.Context(), fmt.Sprintf("INSERT INTO %s.sessions (id, workspace_id) VALUES ($1, $2)", quotedSchema), sessionID, workspaceID); err != nil {
		connection.Close(context.Background())
		t.Fatal(err)
	}
	if _, err := connection.Exec(t.Context(), fmt.Sprintf(`
INSERT INTO %s.runs
    (id, workspace_id, session_id, actor_id, status, request_hash, idempotency_key)
VALUES ($1, $2, $3, $4, 'queued', $5, 'manual-development-row')`, quotedSchema),
		runID, workspaceID, sessionID, "74000000-0000-0000-0000-000000000001", make([]byte, 32)); err != nil {
		connection.Close(context.Background())
		t.Fatal(err)
	}
	if _, err := connection.Exec(t.Context(), fmt.Sprintf(`
INSERT INTO %s.run_events
    (run_id, seq, event_id, producer_instance_id, producer_seq, kind, schema_version, payload)
VALUES ($1, 1, $2, $3, 1, 'manual.event', 1, '{}'::jsonb)`, quotedSchema),
		runID,
		"75000000-0000-0000-0000-000000000001",
		"76000000-0000-0000-0000-000000000001"); err != nil {
		connection.Close(context.Background())
		t.Fatal(err)
	}
	connection.Close(context.Background())

	runner.catalog = catalog[:7]
	result, err := migrateConfig(t.Context(), connectionConfig, runner)
	if err == nil || !strings.Contains(err.Error(), "empty pre-runtime run_events table") {
		t.Fatalf("migration 0002 result = %+v, error = %v; want explicit development-row refusal", result, err)
	}
	if result.Applied != 0 || result.CurrentVersion != 1 {
		t.Fatalf("migration result after 0002 failure = %+v, want version 1 unchanged", result)
	}

	connection = openPostgresTestConnection(t, connectionConfig)
	defer connection.Close(context.Background())
	var historyCount int
	if err := connection.QueryRow(t.Context(), fmt.Sprintf("SELECT pg_catalog.count(*) FROM %s.schema_migrations", quotedSchema)).Scan(&historyCount); err != nil {
		t.Fatal(err)
	}
	if historyCount != 1 {
		t.Fatalf("migration history count = %d, want 1", historyCount)
	}
	var sourceColumnCount int
	if err := connection.QueryRow(t.Context(), `
SELECT pg_catalog.count(*)
FROM information_schema.columns
WHERE table_schema = $1 AND table_name = 'run_events' AND column_name = 'source'`, schema).Scan(&sourceColumnCount); err != nil {
		t.Fatal(err)
	}
	if sourceColumnCount != 0 {
		t.Fatalf("failed migration 0002 left source column count = %d", sourceColumnCount)
	}
}

func TestPostgreSQLMigration0008UpgradesPublishedVersion0007(t *testing.T) {
	connectionConfig := postgresIntegrationConfig(t)
	schema := newPostgresTestSchema(t, connectionConfig)
	catalog, err := EmbeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog) < 8 {
		t.Fatal("embedded catalog does not contain migration 0008")
	}
	runner := runnerConfig{schema: schema, lockKey: migrationAdvisoryLockKey, catalog: catalog[:7]}
	result, err := migrateConfig(t.Context(), connectionConfig, runner)
	if err != nil {
		t.Fatalf("apply published migrations through 0007: %v", err)
	}
	if result.Applied != 7 || result.CurrentVersion != 7 {
		t.Fatalf("published migration result = %+v, want version 7", result)
	}

	runner.catalog = catalog[:8]
	result, err = migrateConfig(t.Context(), connectionConfig, runner)
	if err != nil {
		t.Fatalf("upgrade to migration 0008: %v", err)
	}
	if result.Applied != 1 || result.CurrentVersion != 8 {
		t.Fatalf("0008 upgrade result = %+v, want one applied migration at version 8", result)
	}
}

func TestPostgreSQLMigration0007UpgradesPublishedVersion0006(t *testing.T) {
	connectionConfig := postgresIntegrationConfig(t)
	schema := newPostgresTestSchema(t, connectionConfig)
	catalog, err := EmbeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog) < 7 {
		t.Fatal("embedded catalog does not contain migration 0007")
	}
	runner := runnerConfig{schema: schema, lockKey: migrationAdvisoryLockKey, catalog: catalog[:6]}
	result, err := migrateConfig(t.Context(), connectionConfig, runner)
	if err != nil {
		t.Fatalf("apply published migrations through 0006: %v", err)
	}
	if result.Applied != 6 || result.CurrentVersion != 6 {
		t.Fatalf("published migration result = %+v, want version 6", result)
	}

	runner.catalog = catalog[:7]
	result, err = migrateConfig(t.Context(), connectionConfig, runner)
	if err != nil {
		t.Fatalf("upgrade to migration 0007: %v", err)
	}
	if result.Applied != 1 || result.CurrentVersion != 7 {
		t.Fatalf("0007 upgrade result = %+v, want one applied migration at version 7", result)
	}
}

func TestPostgreSQLMigration0004UpgradesPublishedVersion0003(t *testing.T) {
	connectionConfig := postgresIntegrationConfig(t)
	schema := newPostgresTestSchema(t, connectionConfig)
	catalog, err := EmbeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog) < 4 {
		t.Fatal("embedded catalog does not contain migration 0004")
	}
	runner := runnerConfig{schema: schema, lockKey: migrationAdvisoryLockKey, catalog: catalog[:3]}
	result, err := migrateConfig(t.Context(), connectionConfig, runner)
	if err != nil {
		t.Fatalf("apply published migrations through 0003: %v", err)
	}
	if result.Applied != 3 || result.CurrentVersion != 3 {
		t.Fatalf("published migration result = %+v, want version 3", result)
	}

	runner.catalog = catalog[:4]
	result, err = migrateConfig(t.Context(), connectionConfig, runner)
	if err != nil {
		t.Fatalf("upgrade to migration 0004: %v", err)
	}
	if result.Applied != 1 || result.CurrentVersion != 4 {
		t.Fatalf("0004 upgrade result = %+v, want one applied migration at version 4", result)
	}
}

func TestPostgreSQLMigration0005UpgradesPublishedVersion0004(t *testing.T) {
	connectionConfig := postgresIntegrationConfig(t)
	schema := newPostgresTestSchema(t, connectionConfig)
	catalog, err := EmbeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog) < 5 {
		t.Fatal("embedded catalog does not contain migration 0005")
	}
	runner := runnerConfig{schema: schema, lockKey: migrationAdvisoryLockKey, catalog: catalog[:4]}
	result, err := migrateConfig(t.Context(), connectionConfig, runner)
	if err != nil {
		t.Fatalf("apply published migrations through 0004: %v", err)
	}
	if result.Applied != 4 || result.CurrentVersion != 4 {
		t.Fatalf("published migration result = %+v, want version 4", result)
	}

	runner.catalog = catalog[:5]
	result, err = migrateConfig(t.Context(), connectionConfig, runner)
	if err != nil {
		t.Fatalf("upgrade to migration 0005: %v", err)
	}
	if result.Applied != 1 || result.CurrentVersion != 5 {
		t.Fatalf("0005 upgrade result = %+v, want one applied migration at version 5", result)
	}
}

func TestPostgreSQLMigration0003UpgradesPublishedVersion0002(t *testing.T) {
	connectionConfig := postgresIntegrationConfig(t)
	schema := newPostgresTestSchema(t, connectionConfig)
	catalog, err := EmbeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog) < 3 {
		t.Fatal("embedded catalog does not contain migration 0003")
	}
	runner := runnerConfig{schema: schema, lockKey: migrationAdvisoryLockKey, catalog: catalog[:2]}
	result, err := migrateConfig(t.Context(), connectionConfig, runner)
	if err != nil {
		t.Fatalf("apply published migrations through 0002: %v", err)
	}
	if result.Applied != 2 || result.CurrentVersion != 2 {
		t.Fatalf("published migration result = %+v, want version 2", result)
	}

	runner.catalog = catalog[:3]
	result, err = migrateConfig(t.Context(), connectionConfig, runner)
	if err != nil {
		t.Fatalf("upgrade to migration 0003: %v", err)
	}
	if result.Applied != 1 || result.CurrentVersion != 3 {
		t.Fatalf("0003 upgrade result = %+v, want one applied migration at version 3", result)
	}
	result, err = migrateConfig(t.Context(), connectionConfig, runner)
	if err != nil {
		t.Fatalf("repeat migration 0003: %v", err)
	}
	if result.Applied != 0 || result.CurrentVersion != 3 {
		t.Fatalf("repeat 0003 result = %+v, want no-op at version 3", result)
	}
}

func postgresIntegrationConfig(t *testing.T) *pgx.ConnConfig {
	t.Helper()
	if os.Getenv(postgresTestRunEnvironment) != "1" {
		t.Skipf("set %s=1 to run real PostgreSQL tests", postgresTestRunEnvironment)
	}
	databaseURL := os.Getenv("AGENTSERVER_V2_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Fatalf("AGENTSERVER_V2_TEST_DATABASE_URL is required when %s=1", postgresTestRunEnvironment)
	}
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal("AGENTSERVER_V2_TEST_DATABASE_URL is not a valid PostgreSQL connection string")
	}
	connection := openPostgresTestConnection(t, config)
	if err := connection.Ping(t.Context()); err != nil {
		connection.Close(context.Background())
		t.Fatalf("ping PostgreSQL test database: %v", safeConnectError(config, err))
	}
	connection.Close(context.Background())
	return config
}

func newPostgresTestSchema(t *testing.T, connectionConfig *pgx.ConnConfig) string {
	t.Helper()
	schema := fmt.Sprintf("agentserver_v2_it_%x_%x", os.Getpid(), time.Now().UnixNano())
	if !schemaNamePattern.MatchString(schema) || !strings.HasPrefix(schema, "agentserver_v2_it_") {
		t.Fatalf("generated unsafe PostgreSQL test schema %q", schema)
	}
	t.Cleanup(func() {
		cleanupContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		connection, err := pgx.ConnectConfig(cleanupContext, connectionConfig.Copy())
		if err != nil {
			t.Errorf("connect to clean test schema %q: %v", schema, safeConnectError(connectionConfig, err))
			return
		}
		defer connection.Close(context.Background())
		if _, err := connection.Exec(cleanupContext, "DROP SCHEMA IF EXISTS "+quoteIdentifier(schema)+" CASCADE"); err != nil {
			t.Errorf("drop isolated test schema %q: %v", schema, err)
		}
	})
	return schema
}

func openPostgresTestConnection(t *testing.T, connectionConfig *pgx.ConnConfig) *pgx.Conn {
	t.Helper()
	connection, err := pgx.ConnectConfig(t.Context(), connectionConfig.Copy())
	if err != nil {
		t.Fatalf("connect to PostgreSQL test database: %v", safeConnectError(connectionConfig, err))
	}
	return connection
}

func assertDatabaseObjects(t *testing.T, connection *pgx.Conn, schema string) {
	t.Helper()
	wantTables := map[string]bool{
		"approvals":                    false,
		"attempt_leases":               false,
		"brain_tool_catalogs":          false,
		"checkpoints":                  false,
		"executor_connection_attempts": false,
		"executor_connections":         false,
		"executor_enrollment_tokens":   false,
		"executor_environments":        false,
		"executors":                    false,
		"execution_operations":         false,
		"executions":                   false,
		"outbox":                       false,
		"production_bootstrap_seeds":   false,
		"run_attempts":                 false,
		"run_events":                   false,
		"run_event_rebases":            false,
		"run_launch_allowed_tools":     false,
		"run_launch_states":            false,
		"runs":                         false,
		"schema_migrations":            false,
		"session_leases":               false,
		"sessions":                     false,
		"workspace_members":            false,
		"workspaces":                   false,
	}
	rows, err := connection.Query(t.Context(), `
SELECT table_name
FROM information_schema.tables
WHERE table_schema = $1 AND table_type = 'BASE TABLE'`, schema)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if _, expected := wantTables[name]; expected {
			wantTables[name] = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	rows.Close()
	for name, found := range wantTables {
		if !found {
			t.Errorf("expected table %s.%s was not created", schema, name)
		}
	}

	wantConstraints := map[string]bool{
		"approvals_execution_unique":                              false,
		"approvals_nonce_unique":                                  false,
		"approvals_execution_scope_fk":                            false,
		"approvals_context_hash_sha256":                           false,
		"approvals_decision_evidence_matches_status":              false,
		"executions_identity_attempt_scope_unique":                false,
		"runs_session_workspace_fk":                               false,
		"sessions_active_run_same_session_fk":                     false,
		"runs_idempotency_unique":                                 false,
		"run_events_attempt_fk":                                   false,
		"run_events_source_valid":                                 false,
		"run_event_rebases_after_seq_json_safe":                   false,
		"run_event_rebases_materialization_time_order":            false,
		"run_event_rebases_snapshot_object_bounded":               false,
		"run_event_rebases_status_valid":                          false,
		"run_event_rebases_version_json_safe":                     false,
		"run_events_payload_or_object":                            false,
		"run_events_object_size_positive":                         false,
		"run_events_object_media_type_bounded":                    false,
		"outbox_lock_pair":                                        false,
		"schema_migrations_sha256_exact":                          false,
		"attempt_leases_attempt_generation_fk":                    false,
		"brain_tool_catalogs_attempt_scope_fk":                    false,
		"brain_tool_catalogs_attempt_unique":                      false,
		"brain_tool_catalogs_digests_sha256":                      false,
		"brain_tool_catalogs_identity_scope_thread_unique":        false,
		"brain_tool_catalogs_run_scope_fk":                        false,
		"brain_tool_catalogs_thread_unique":                       false,
		"checkpoints_attempt_scope_fk":                            false,
		"checkpoints_catalog_scope_thread_fk":                     false,
		"checkpoints_digests_sha256":                              false,
		"checkpoints_object_media_type_bounded":                   false,
		"checkpoints_object_size_bounded":                         false,
		"checkpoints_run_scope_fk":                                false,
		"run_launch_allowed_tools_name_unique":                    false,
		"run_launch_states_run_scope_fk":                          false,
		"sessions_latest_checkpoint_same_session_fk":              false,
		"session_leases_run_session_fk":                           false,
		"run_attempts_run_generation_unique":                      false,
		"run_attempts_terminal_identity_complete":                 false,
		"run_attempts_terminal_thread_bounded":                    false,
		"run_attempts_terminal_turn_bounded":                      false,
		"run_events_producer_key_unique":                          false,
		"sessions_identity_workspace_unique":                      false,
		"runs_identity_workspace_session_unique":                  false,
		"executions_run_tool_call_unique":                         false,
		"executions_attempt_fk":                                   false,
		"executions_hashes_sha256":                                false,
		"execution_operations_execution_ordinal_unique":           false,
		"execution_operations_mutation_key_unique":                false,
		"execution_operations_terminal_matches_status":            false,
		"execution_operations_skipped_kind_valid":                 false,
		"executors_enrollment_metadata_complete":                  false,
		"executors_production_machine_identity_complete":          false,
		"executor_enrollment_tokens_executor_scope_fk":            false,
		"executor_enrollment_tokens_request_unique":               false,
		"executor_enrollment_tokens_idempotency_bounded":          false,
		"executor_enrollment_tokens_expiry_order":                 false,
		"executor_enrollment_tokens_request_hash_exact":           false,
		"executor_enrollment_tokens_lifecycle":                    false,
		"executor_enrollment_tokens_terminal_exclusive":           false,
		"executor_enrollment_tokens_revocation_order":             false,
		"executor_enrollment_tokens_version_positive":             false,
		"workspace_members_role_valid":                            false,
		"executor_environments_process_profile_valid":             false,
		"executor_connections_session_id_unique":                  false,
		"executor_connections_build_hashes_sha256":                false,
		"executor_connections_status_valid":                       false,
		"executor_connection_attempts_executor_generation_unique": false,
		"executor_connection_attempts_end_pair":                   false,
		"executor_connections_attempt_fk":                         false,
	}
	rows, err = connection.Query(t.Context(), `
SELECT constraint_name
FROM information_schema.table_constraints
WHERE constraint_schema = $1`, schema)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if _, expected := wantConstraints[name]; expected {
			wantConstraints[name] = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	rows.Close()
	for name, found := range wantConstraints {
		if !found {
			t.Errorf("expected constraint %s was not created", name)
		}
	}

	wantIndexes := map[string]bool{
		"approvals_run_status_expiry_idx":                    false,
		"brain_tool_catalogs_session_created_idx":            false,
		"checkpoints_session_created_idx":                    false,
		"runs_session_status_created_idx":                    false,
		"run_attempts_run_created_idx":                       false,
		"run_events_run_created_idx":                         false,
		"outbox_claim_idx":                                   false,
		"executions_run_status_created_idx":                  false,
		"execution_operations_execution_status_ordinal_idx":  false,
		"executors_workspace_status_created_idx":             false,
		"executor_environments_executor_status_created_idx":  false,
		"executor_connections_expiry_idx":                    false,
		"executor_connection_attempts_executor_acquired_idx": false,
		"executors_oauth_client_id_unique":                   false,
		"executor_enrollment_tokens_one_live_per_executor":   false,
		"executor_enrollment_tokens_expiry_idx":              false,
		"workspace_members_user_workspace_idx":               false,
	}
	rows, err = connection.Query(t.Context(), "SELECT indexname FROM pg_catalog.pg_indexes WHERE schemaname = $1", schema)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if _, expected := wantIndexes[name]; expected {
			wantIndexes[name] = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	rows.Close()
	for name, found := range wantIndexes {
		if !found {
			t.Errorf("expected index %s was not created", name)
		}
	}
}

func assertKernelConstraints(t *testing.T, connection *pgx.Conn, schema string) {
	t.Helper()
	quotedSchema := quoteIdentifier(schema)
	workspaceID := "10000000-0000-0000-0000-000000000001"
	secondWorkspaceID := "10000000-0000-0000-0000-000000000002"
	sessionID := "20000000-0000-0000-0000-000000000001"
	secondSessionID := "20000000-0000-0000-0000-000000000002"
	runID := "30000000-0000-0000-0000-000000000001"
	actorID := "40000000-0000-0000-0000-000000000001"

	if _, err := connection.Exec(t.Context(), fmt.Sprintf("INSERT INTO %s.workspaces (id, status) VALUES ($1, 'active'), ($2, 'active')", quotedSchema), workspaceID, secondWorkspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Exec(t.Context(), fmt.Sprintf("INSERT INTO %s.sessions (id, workspace_id) VALUES ($1, $2), ($3, $2)", quotedSchema), sessionID, workspaceID, secondSessionID); err != nil {
		t.Fatal(err)
	}
	insertRun := fmt.Sprintf(`
INSERT INTO %s.runs
    (id, workspace_id, session_id, actor_id, status, request_hash, idempotency_key)
VALUES ($1, $2, $3, $4, 'queued', $5, $6)`, quotedSchema)
	if _, err := connection.Exec(t.Context(), insertRun, runID, workspaceID, sessionID, actorID, make([]byte, 32), "create-run-key"); err != nil {
		t.Fatal(err)
	}

	_, err := connection.Exec(t.Context(), insertRun,
		"30000000-0000-0000-0000-000000000002", workspaceID, sessionID, actorID, make([]byte, 32), "create-run-key")
	assertPostgreSQLState(t, err, "23505")

	_, err = connection.Exec(t.Context(), insertRun,
		"30000000-0000-0000-0000-000000000003", workspaceID, sessionID,
		"40000000-0000-0000-0000-000000000002", make([]byte, 31), "invalid-hash")
	assertPostgreSQLState(t, err, "23514")

	_, err = connection.Exec(t.Context(), fmt.Sprintf("UPDATE %s.sessions SET active_run_id = $1 WHERE id = $2", quotedSchema), runID, secondSessionID)
	assertPostgreSQLState(t, err, "23503")

	insertInvalidEvent := fmt.Sprintf(`
INSERT INTO %s.run_events
    (run_id, seq, event_id, producer_instance_id, producer_seq, source, kind, schema_version)
VALUES ($1, 1, $2, $3, 1, 'system', 'run.created', 1)`, quotedSchema)
	_, err = connection.Exec(t.Context(), insertInvalidEvent, runID,
		"50000000-0000-0000-0000-000000000001",
		"60000000-0000-0000-0000-000000000001")
	assertPostgreSQLState(t, err, "23514")
}

func assertPostgreSQLState(t *testing.T, err error, wantState string) {
	t.Helper()
	if err == nil {
		t.Fatalf("PostgreSQL operation succeeded, want SQLSTATE %s", wantState)
	}
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) {
		t.Fatalf("PostgreSQL error = %T %v, want SQLSTATE %s", err, err, wantState)
	}
	if postgresError.Code != wantState {
		t.Fatalf("PostgreSQL SQLSTATE = %s, want %s (error: %v)", postgresError.Code, wantState, postgresError)
	}
}
