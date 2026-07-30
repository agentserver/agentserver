package coredb

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	// SchemaName isolates v2 tables from any v1 objects in the same database.
	SchemaName = "agentserver_v2"

	// The ASCII bytes for "agentv2" form a stable database-wide migration key.
	migrationAdvisoryLockKey int64 = 0x6167656e747632
	cleanupTimeout                 = 5 * time.Second
)

var schemaNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

// MigrationResult reports the schema version reached by a migration run.
type MigrationResult struct {
	Applied        int
	CurrentVersion int64
}

type runnerConfig struct {
	schema  string
	lockKey int64
	catalog []Migration
}

// Migrate opens one dedicated physical PostgreSQL connection, serializes all
// migration runners with an advisory lock, and applies the embedded catalog.
// The connection is always closed so PostgreSQL releases the session lock even
// if the explicit unlock path fails.
func Migrate(ctx context.Context, databaseURL string) (MigrationResult, error) {
	connectionConfig, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		// pgx's ParseConfigError includes the complete connection string, which
		// may contain a password. Never return it to the CLI.
		return MigrationResult{}, errors.New("parse AGENTSERVER_V2_DATABASE_URL: invalid PostgreSQL connection string")
	}
	catalog, err := EmbeddedMigrations()
	if err != nil {
		return MigrationResult{}, fmt.Errorf("load embedded migration catalog: %w", err)
	}
	return migrateConfig(ctx, connectionConfig, runnerConfig{
		schema:  SchemaName,
		lockKey: migrationAdvisoryLockKey,
		catalog: catalog,
	})
}

func migrateConfig(ctx context.Context, connectionConfig *pgx.ConnConfig, runner runnerConfig) (result MigrationResult, returnErr error) {
	if connectionConfig == nil {
		return result, errors.New("PostgreSQL connection config is nil")
	}
	if !schemaNamePattern.MatchString(runner.schema) {
		return result, fmt.Errorf("invalid migration schema name %q", runner.schema)
	}
	if runner.lockKey == 0 {
		return result, errors.New("migration advisory lock key must be non-zero")
	}
	if err := validateCatalog(runner.catalog); err != nil {
		return result, err
	}

	connection, err := pgx.ConnectConfig(ctx, connectionConfig.Copy())
	if err != nil {
		return result, safeConnectError(connectionConfig, err)
	}
	defer func() {
		closeContext, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()
		if err := connection.Close(closeContext); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close dedicated migration connection: %w", err))
		}
	}()

	if _, err := connection.Exec(ctx, "SELECT pg_catalog.pg_advisory_lock($1)", runner.lockKey); err != nil {
		return result, fmt.Errorf("acquire migration advisory lock: %w", err)
	}
	defer func() {
		unlockContext, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()
		var unlocked bool
		if err := connection.QueryRow(unlockContext, "SELECT pg_catalog.pg_advisory_unlock($1)", runner.lockKey).Scan(&unlocked); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("release migration advisory lock: %w", err))
			return
		}
		if !unlocked {
			returnErr = errors.Join(returnErr, errors.New("release migration advisory lock: PostgreSQL reported that this session did not own the lock"))
		}
	}()

	if err := bootstrapMigrationHistory(ctx, connection, runner.schema); err != nil {
		return result, err
	}
	if err := setSearchPath(ctx, connection, runner.schema); err != nil {
		return result, err
	}
	applied, err := readAppliedMigrations(ctx, connection, runner.schema)
	if err != nil {
		return result, err
	}
	pending, err := pendingMigrations(runner.catalog, applied)
	if err != nil {
		return result, err
	}

	result.CurrentVersion = int64(len(applied))
	for _, migration := range pending {
		if err := applyMigration(ctx, connection, runner.schema, migration); err != nil {
			return result, err
		}
		result.Applied++
		result.CurrentVersion = migration.Version
	}
	return result, nil
}

func bootstrapMigrationHistory(ctx context.Context, connection *pgx.Conn, schema string) error {
	transaction, err := connection.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin migration bootstrap transaction: %w", err)
	}
	defer func() {
		rollbackContext, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()
		_ = transaction.Rollback(rollbackContext)
	}()

	quotedSchema := quoteIdentifier(schema)
	if _, err := transaction.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+quotedSchema); err != nil {
		return fmt.Errorf("create migration schema: %w", err)
	}
	createHistorySQL := fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s.schema_migrations (
    version bigint PRIMARY KEY,
    name text NOT NULL,
    sha256 bytea NOT NULL,
    applied_at timestamptz NOT NULL DEFAULT pg_catalog.clock_timestamp(),
    CONSTRAINT schema_migrations_version_positive CHECK (version > 0),
    CONSTRAINT schema_migrations_name_bounded CHECK (pg_catalog.octet_length(name) BETWEEN 1 AND 63),
    CONSTRAINT schema_migrations_sha256_exact CHECK (pg_catalog.octet_length(sha256) = 32)
)`, quotedSchema)
	if _, err := transaction.Exec(ctx, createHistorySQL); err != nil {
		return fmt.Errorf("create migration history table: %w", err)
	}
	if err := transaction.Commit(ctx); err != nil {
		return fmt.Errorf("commit migration bootstrap transaction: %w", err)
	}
	return nil
}

func setSearchPath(ctx context.Context, connection *pgx.Conn, schema string) error {
	searchPath := quoteIdentifier(schema) + ", pg_catalog"
	if _, err := connection.Exec(ctx, "SELECT pg_catalog.set_config('search_path', $1, false)", searchPath); err != nil {
		return fmt.Errorf("set migration search_path: %w", err)
	}
	return nil
}

func readAppliedMigrations(ctx context.Context, connection *pgx.Conn, schema string) ([]AppliedMigration, error) {
	query := fmt.Sprintf("SELECT version, name, sha256 FROM %s.schema_migrations ORDER BY version", quoteIdentifier(schema))
	rows, err := connection.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("read migration history: %w", err)
	}
	defer rows.Close()

	var applied []AppliedMigration
	for rows.Next() {
		var migration AppliedMigration
		var digest []byte
		if err := rows.Scan(&migration.Version, &migration.Name, &digest); err != nil {
			return nil, fmt.Errorf("scan migration history: %w", err)
		}
		if len(digest) != sha256.Size {
			return nil, fmt.Errorf("migration %04d_%s has invalid %d-byte checksum in database", migration.Version, migration.Name, len(digest))
		}
		copy(migration.SHA256[:], digest)
		applied = append(applied, migration)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read migration history rows: %w", err)
	}
	return applied, nil
}

func applyMigration(ctx context.Context, connection *pgx.Conn, schema string, migration Migration) error {
	transaction, err := connection.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin migration %04d_%s: %w", migration.Version, migration.Name, err)
	}
	defer func() {
		rollbackContext, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()
		_ = transaction.Rollback(rollbackContext)
	}()

	searchPath := quoteIdentifier(schema) + ", pg_catalog"
	if _, err := transaction.Exec(ctx, "SELECT pg_catalog.set_config('search_path', $1, true)", searchPath); err != nil {
		return fmt.Errorf("set search_path for migration %04d_%s: %w", migration.Version, migration.Name, err)
	}
	if _, err := transaction.Exec(ctx, migration.SQL, pgx.QueryExecModeSimpleProtocol); err != nil {
		return fmt.Errorf("execute migration %04d_%s: %w", migration.Version, migration.Name, err)
	}
	insertHistorySQL := fmt.Sprintf("INSERT INTO %s.schema_migrations (version, name, sha256) VALUES ($1, $2, $3)", quoteIdentifier(schema))
	if _, err := transaction.Exec(ctx, insertHistorySQL, migration.Version, migration.Name, migration.SHA256[:]); err != nil {
		return fmt.Errorf("record migration %04d_%s: %w", migration.Version, migration.Name, err)
	}
	if err := transaction.Commit(ctx); err != nil {
		return fmt.Errorf("commit migration %04d_%s: %w", migration.Version, migration.Name, err)
	}
	return nil
}

func quoteIdentifier(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}

func safeConnectError(config *pgx.ConnConfig, err error) error {
	message := err.Error()
	if config.Password != "" {
		message = strings.ReplaceAll(message, config.Password, "[REDACTED]")
	}
	return fmt.Errorf("connect to PostgreSQL as user %q database %q: %s", config.User, config.Database, message)
}
