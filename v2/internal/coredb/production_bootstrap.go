package coredb

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"
)

const productionBootstrapAdvisoryLockKey int64 = 0x70726f64626f6f74

// ErrProductionBootstrapConflict means an immutable production seed already
// exists with authority different from the requested seed. Bootstrap never
// rewrites or adopts the conflicting row.
var ErrProductionBootstrapConflict = errors.New("production bootstrap conflicts with existing authority")

// ProductionBootstrap is the minimum one-time authority needed to log in,
// open the first session, and enroll the deployment-bound executor. General
// workspace and session management remain a separate product surface.
type ProductionBootstrap struct {
	WorkspaceID string
	SessionID   string
	UserID      string

	ExternalOIDCIssuer  string
	ExternalOIDCSubject string

	ExecutorID string
}

type ProductionBootstrapResult struct {
	CreatedRows   int
	SchemaVersion int64
}

// BootstrapProduction inserts one exact, retry-safe production seed. Callers
// must run Migrate as a separate deployment step first.
func BootstrapProduction(
	ctx context.Context,
	databaseURL string,
	bootstrap ProductionBootstrap,
) (ProductionBootstrapResult, error) {
	if ctx == nil {
		return ProductionBootstrapResult{}, errors.New("production bootstrap context is required")
	}
	connectionConfig, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return ProductionBootstrapResult{}, errors.New("parse AGENTSERVER_V2_DATABASE_URL: invalid PostgreSQL connection string")
	}
	return bootstrapProductionConfig(ctx, connectionConfig, SchemaName, bootstrap)
}

func bootstrapProductionConfig(
	ctx context.Context,
	connectionConfig *pgx.ConnConfig,
	schema string,
	bootstrap ProductionBootstrap,
) (result ProductionBootstrapResult, returnErr error) {
	if connectionConfig == nil {
		return result, errors.New("PostgreSQL connection config is nil")
	}
	if !schemaNamePattern.MatchString(schema) {
		return result, fmt.Errorf("invalid production bootstrap schema name %q", schema)
	}
	if err := validateProductionBootstrap(bootstrap); err != nil {
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
			returnErr = errors.Join(returnErr, fmt.Errorf("close production bootstrap connection: %w", err))
		}
	}()

	transaction, err := connection.Begin(ctx)
	if err != nil {
		return result, databaseError("begin production bootstrap", err)
	}
	defer func() {
		rollbackContext, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()
		_ = transaction.Rollback(rollbackContext)
	}()
	if _, err := transaction.Exec(ctx, "SELECT pg_catalog.pg_advisory_xact_lock($1)", productionBootstrapAdvisoryLockKey); err != nil {
		return result, databaseError("serialize production bootstrap", err)
	}
	result.SchemaVersion, err = requireCurrentProductionBootstrapSchema(ctx, transaction, schema)
	if err != nil {
		return result, err
	}

	quotedSchema := quoteIdentifier(schema)
	seeded, err := readProductionBootstrapSeed(ctx, transaction, quotedSchema, bootstrap)
	if err != nil {
		return ProductionBootstrapResult{}, err
	}
	if !seeded {
		hasAuthority, err := productionBootstrapAuthorityExists(ctx, transaction, quotedSchema)
		if err != nil {
			return ProductionBootstrapResult{}, err
		}
		if hasAuthority {
			matches, err := productionBootstrapRowsMatch(ctx, transaction, quotedSchema, bootstrap)
			if err != nil {
				return ProductionBootstrapResult{}, err
			}
			if !matches {
				return ProductionBootstrapResult{}, productionBootstrapConflict("bootstrap seed", "singleton")
			}
		}
	}
	steps := []func(context.Context, pgx.Tx, string, ProductionBootstrap) (int, error){
		insertProductionWorkspace,
		insertProductionUser,
		insertProductionIdentity,
		insertProductionMembership,
		insertProductionSession,
		insertProductionExecutor,
	}
	for _, step := range steps {
		created, err := step(ctx, transaction, quotedSchema, bootstrap)
		if err != nil {
			return ProductionBootstrapResult{}, err
		}
		result.CreatedRows += created
	}
	if err := ensureProductionBootstrapSeed(ctx, transaction, quotedSchema, bootstrap); err != nil {
		return ProductionBootstrapResult{}, err
	}

	if err := transaction.Commit(ctx); err != nil {
		return ProductionBootstrapResult{}, databaseError("commit production bootstrap", err)
	}
	return result, nil
}

func readProductionBootstrapSeed(
	ctx context.Context,
	transaction pgx.Tx,
	schema string,
	bootstrap ProductionBootstrap,
) (bool, error) {
	query := fmt.Sprintf(`
SELECT workspace_id::text, session_id::text, user_id::text,
       external_oidc_issuer, external_oidc_subject, executor_id::text
FROM %s.production_bootstrap_seeds
WHERE singleton = TRUE
FOR UPDATE`, schema)
	var stored ProductionBootstrap
	err := transaction.QueryRow(ctx, query).Scan(
		&stored.WorkspaceID, &stored.SessionID, &stored.UserID,
		&stored.ExternalOIDCIssuer, &stored.ExternalOIDCSubject, &stored.ExecutorID,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, databaseError("read production bootstrap seed", err)
	}
	if stored != bootstrap {
		return false, productionBootstrapConflict("bootstrap seed", "singleton")
	}
	return true, nil
}

func productionBootstrapAuthorityExists(ctx context.Context, transaction pgx.Tx, schema string) (bool, error) {
	query := fmt.Sprintf(`
SELECT EXISTS (
    SELECT 1 FROM %s.workspaces
    UNION ALL SELECT 1 FROM %s.sessions
    UNION ALL SELECT 1 FROM %s.users
    UNION ALL SELECT 1 FROM %s.user_identities
    UNION ALL SELECT 1 FROM %s.workspace_members
    UNION ALL SELECT 1 FROM %s.executors
)`, schema, schema, schema, schema, schema, schema)
	var exists bool
	if err := transaction.QueryRow(ctx, query).Scan(&exists); err != nil {
		return false, databaseError("inspect pre-ledger production bootstrap authority", err)
	}
	return exists, nil
}

func productionBootstrapRowsMatch(
	ctx context.Context,
	transaction pgx.Tx,
	schema string,
	bootstrap ProductionBootstrap,
) (bool, error) {
	query := fmt.Sprintf(`
SELECT EXISTS (
    SELECT 1
    FROM %s.workspaces AS workspace
    JOIN %s.sessions AS session
      ON session.id = $2 AND session.workspace_id = workspace.id
    JOIN %s.users AS local_user
      ON local_user.id = $3
    JOIN %s.user_identities AS identity
      ON identity.issuer = $4 AND identity.subject = $5
     AND identity.user_id = local_user.id
    JOIN %s.workspace_members AS member
      ON member.workspace_id = workspace.id AND member.user_id = local_user.id
    JOIN %s.executors AS executor
      ON executor.id = $6 AND executor.workspace_id = workspace.id
    WHERE workspace.id = $1
      AND workspace.status = 'active'
      AND session.creator_id = local_user.id
      AND session.status = 'active'
      AND local_user.status = 'active'
      AND identity.status = 'active'
      AND member.role = 'owner'
      AND executor.status IN ('enrolling', 'offline', 'online')
)`, schema, schema, schema, schema, schema, schema)
	var matches bool
	if err := transaction.QueryRow(
		ctx, query,
		bootstrap.WorkspaceID, bootstrap.SessionID, bootstrap.UserID,
		bootstrap.ExternalOIDCIssuer, bootstrap.ExternalOIDCSubject, bootstrap.ExecutorID,
	).Scan(&matches); err != nil {
		return false, databaseError("verify pre-ledger production bootstrap authority", err)
	}
	return matches, nil
}

func ensureProductionBootstrapSeed(
	ctx context.Context,
	transaction pgx.Tx,
	schema string,
	bootstrap ProductionBootstrap,
) error {
	query := fmt.Sprintf(`
INSERT INTO %s.production_bootstrap_seeds
    (singleton, workspace_id, session_id, user_id,
     external_oidc_issuer, external_oidc_subject, executor_id)
VALUES
    (TRUE, $1, $2, $3, $4, $5, $6)
ON CONFLICT (singleton) DO NOTHING`, schema)
	if _, err := productionInsert(
		ctx, transaction, "insert production bootstrap seed", query,
		bootstrap.WorkspaceID, bootstrap.SessionID, bootstrap.UserID,
		bootstrap.ExternalOIDCIssuer, bootstrap.ExternalOIDCSubject, bootstrap.ExecutorID,
	); err != nil {
		return err
	}
	seeded, err := readProductionBootstrapSeed(ctx, transaction, schema, bootstrap)
	if err != nil {
		return err
	}
	if !seeded {
		return productionBootstrapConflict("bootstrap seed", "singleton")
	}
	return nil
}

func validateProductionBootstrap(bootstrap ProductionBootstrap) error {
	for _, identity := range []struct {
		name  string
		value string
	}{
		{name: "workspace_id", value: bootstrap.WorkspaceID},
		{name: "session_id", value: bootstrap.SessionID},
		{name: "user_id", value: bootstrap.UserID},
		{name: "executor_id", value: bootstrap.ExecutorID},
	} {
		if err := validateUUID(identity.name, identity.value); err != nil {
			return fmt.Errorf("invalid production bootstrap: %w", err)
		}
	}
	if len(bootstrap.ExternalOIDCIssuer) < 8 || len(bootstrap.ExternalOIDCIssuer) > 2048 ||
		strings.TrimSpace(bootstrap.ExternalOIDCIssuer) != bootstrap.ExternalOIDCIssuer ||
		strings.HasSuffix(bootstrap.ExternalOIDCIssuer, "/") {
		return errors.New("invalid production bootstrap: external OIDC issuer must be bounded canonical URL text without a trailing slash")
	}
	issuer, err := url.Parse(bootstrap.ExternalOIDCIssuer)
	if err != nil || issuer.Scheme != "https" || issuer.Host == "" || issuer.Hostname() == "" || issuer.User != nil ||
		issuer.RawQuery != "" || issuer.Fragment != "" || issuer.Opaque != "" {
		return errors.New("invalid production bootstrap: external OIDC issuer must be an absolute HTTPS URL without credentials, query, or fragment")
	}
	if err := validateBoundedText("external_oidc_subject", bootstrap.ExternalOIDCSubject, 2048); err != nil {
		return fmt.Errorf("invalid production bootstrap: %w", err)
	}
	return nil
}

func requireCurrentProductionBootstrapSchema(ctx context.Context, transaction pgx.Tx, schema string) (int64, error) {
	catalog, err := EmbeddedMigrations()
	if err != nil {
		return 0, fmt.Errorf("load production bootstrap migration catalog: %w", err)
	}
	wantVersion := catalog[len(catalog)-1].Version
	query := fmt.Sprintf("SELECT COALESCE(MAX(version), 0), COUNT(*) FROM %s.schema_migrations", quoteIdentifier(schema))
	var currentVersion int64
	var applied int
	if err := transaction.QueryRow(ctx, query).Scan(&currentVersion, &applied); err != nil {
		return 0, databaseError("verify production bootstrap schema; run agentserver-core migrate first", err)
	}
	if currentVersion != wantVersion || applied != len(catalog) {
		return 0, fmt.Errorf("production bootstrap requires schema version %04d; current version is %04d with %d migration(s)", wantVersion, currentVersion, applied)
	}
	return currentVersion, nil
}

func insertProductionWorkspace(ctx context.Context, transaction pgx.Tx, schema string, bootstrap ProductionBootstrap) (int, error) {
	insert := fmt.Sprintf("INSERT INTO %s.workspaces (id, status, managed_lark_credential_mode) VALUES ($1, 'active', 'webhook_swap') ON CONFLICT DO NOTHING", schema)
	created, err := productionInsert(ctx, transaction, "insert production workspace", insert, bootstrap.WorkspaceID)
	if err != nil {
		return 0, err
	}
	query := fmt.Sprintf("SELECT status FROM %s.workspaces WHERE id = $1 FOR UPDATE", schema)
	var status string
	if err := transaction.QueryRow(ctx, query, bootstrap.WorkspaceID).Scan(&status); err != nil {
		return 0, databaseError("verify production workspace", err)
	}
	if status != "active" {
		return 0, productionBootstrapConflict("workspace", bootstrap.WorkspaceID)
	}
	return created, nil
}

func insertProductionSession(ctx context.Context, transaction pgx.Tx, schema string, bootstrap ProductionBootstrap) (int, error) {
	insert := fmt.Sprintf("INSERT INTO %s.sessions (id, workspace_id, creator_id) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING", schema)
	created, err := productionInsert(ctx, transaction, "insert production session", insert, bootstrap.SessionID, bootstrap.WorkspaceID, bootstrap.UserID)
	if err != nil {
		return 0, err
	}
	query := fmt.Sprintf("SELECT workspace_id::text, creator_id::text, status FROM %s.sessions WHERE id = $1 FOR UPDATE", schema)
	var workspaceID, creatorID, status string
	if err := transaction.QueryRow(ctx, query, bootstrap.SessionID).Scan(&workspaceID, &creatorID, &status); err != nil {
		return 0, databaseError("verify production session", err)
	}
	if workspaceID != bootstrap.WorkspaceID || creatorID != bootstrap.UserID || status != UserSessionStatusActive {
		return 0, productionBootstrapConflict("session", bootstrap.SessionID)
	}
	return created, nil
}

func insertProductionUser(ctx context.Context, transaction pgx.Tx, schema string, bootstrap ProductionBootstrap) (int, error) {
	insert := fmt.Sprintf("INSERT INTO %s.users (id, status) VALUES ($1, 'active') ON CONFLICT DO NOTHING", schema)
	created, err := productionInsert(ctx, transaction, "insert production user", insert, bootstrap.UserID)
	if err != nil {
		return 0, err
	}
	query := fmt.Sprintf("SELECT status FROM %s.users WHERE id = $1 FOR UPDATE", schema)
	var status string
	if err := transaction.QueryRow(ctx, query, bootstrap.UserID).Scan(&status); err != nil {
		return 0, databaseError("verify production user", err)
	}
	if status != "active" {
		return 0, productionBootstrapConflict("user", bootstrap.UserID)
	}
	return created, nil
}

func insertProductionIdentity(ctx context.Context, transaction pgx.Tx, schema string, bootstrap ProductionBootstrap) (int, error) {
	insert := fmt.Sprintf(`
INSERT INTO %s.user_identities (issuer, subject, user_id, status)
VALUES ($1, $2, $3, 'active')
ON CONFLICT DO NOTHING`, schema)
	created, err := productionInsert(
		ctx, transaction, "insert production OIDC identity", insert,
		bootstrap.ExternalOIDCIssuer, bootstrap.ExternalOIDCSubject, bootstrap.UserID,
	)
	if err != nil {
		return 0, err
	}
	query := fmt.Sprintf(`
SELECT user_id::text, status
FROM %s.user_identities
WHERE issuer = $1 AND subject = $2
FOR UPDATE`, schema)
	var userID, status string
	if err := transaction.QueryRow(ctx, query, bootstrap.ExternalOIDCIssuer, bootstrap.ExternalOIDCSubject).Scan(&userID, &status); err != nil {
		return 0, databaseError("verify production OIDC identity", err)
	}
	if userID != bootstrap.UserID || status != "active" {
		return 0, productionBootstrapConflict("OIDC identity", bootstrap.ExternalOIDCSubject)
	}
	return created, nil
}

func insertProductionMembership(ctx context.Context, transaction pgx.Tx, schema string, bootstrap ProductionBootstrap) (int, error) {
	insert := fmt.Sprintf("INSERT INTO %s.workspace_members (workspace_id, user_id, role) VALUES ($1, $2, 'owner') ON CONFLICT DO NOTHING", schema)
	created, err := productionInsert(ctx, transaction, "insert production workspace membership", insert, bootstrap.WorkspaceID, bootstrap.UserID)
	if err != nil {
		return 0, err
	}
	query := fmt.Sprintf("SELECT role FROM %s.workspace_members WHERE workspace_id = $1 AND user_id = $2 FOR UPDATE", schema)
	var role string
	if err := transaction.QueryRow(ctx, query, bootstrap.WorkspaceID, bootstrap.UserID).Scan(&role); err != nil {
		return 0, databaseError("verify production workspace membership", err)
	}
	if role != "owner" {
		return 0, productionBootstrapConflict("workspace membership", bootstrap.UserID)
	}
	return created, nil
}

func insertProductionExecutor(ctx context.Context, transaction pgx.Tx, schema string, bootstrap ProductionBootstrap) (int, error) {
	insert := fmt.Sprintf(`
INSERT INTO %s.executors (id, workspace_id, status)
VALUES ($1, $2, 'enrolling')
ON CONFLICT DO NOTHING`, schema)
	created, err := productionInsert(ctx, transaction, "insert production executor", insert, bootstrap.ExecutorID, bootstrap.WorkspaceID)
	if err != nil {
		return 0, err
	}
	query := fmt.Sprintf("SELECT workspace_id::text, status FROM %s.executors WHERE id = $1 FOR UPDATE", schema)
	var workspaceID, status string
	if err := transaction.QueryRow(ctx, query, bootstrap.ExecutorID).Scan(&workspaceID, &status); err != nil {
		return 0, databaseError("verify production executor", err)
	}
	if workspaceID != bootstrap.WorkspaceID || (status != ExecutorStatusEnrolling && status != ExecutorStatusOffline && status != ExecutorStatusOnline) {
		return 0, productionBootstrapConflict("executor", bootstrap.ExecutorID)
	}
	return created, nil
}

func productionInsert(ctx context.Context, transaction pgx.Tx, operation, statement string, arguments ...any) (int, error) {
	tag, err := transaction.Exec(ctx, statement, arguments...)
	if err != nil {
		return 0, databaseError(operation, err)
	}
	if tag.RowsAffected() > 1 {
		return 0, databaseError(operation, errors.New("insert affected more than one row"))
	}
	return int(tag.RowsAffected()), nil
}

func productionBootstrapConflict(resource, resourceID string) error {
	return fmt.Errorf("%w: %s %s differs", ErrProductionBootstrapConflict, resource, resourceID)
}
