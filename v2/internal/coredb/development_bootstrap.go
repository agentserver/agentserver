package coredb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/agentserver/agentserver/v2/internal/execprofile"
	"github.com/jackc/pgx/v5"
)

const developmentBootstrapAdvisoryLockKey int64 = 0x646576626f6f74

// ErrInsecureDevelopmentBootstrapConflict means an immutable development
// identity already exists with authority different from the requested seed.
// The bootstrap transaction never rewrites or adopts the conflicting row.
var ErrInsecureDevelopmentBootstrapConflict = errors.New("insecure development bootstrap conflicts with existing authority")

// InsecureDevelopmentBootstrap is the complete seed required before the
// development browser, harness, executor-gateway, and agentx chain can run.
// It is deliberately not a production enrollment or workspace-management API.
type InsecureDevelopmentBootstrap struct {
	WorkspaceID string
	SessionID   string
	ActorID     string

	ExternalOIDCIssuer  string
	ExternalOIDCSubject string

	ExecutorID               string
	MachineKeySHA256         [sha256.Size]byte
	AgentxVersion            string
	RuntimeManifestSHA256    [sha256.Size]byte
	ExecProtocolSourceSHA256 [sha256.Size]byte
	Environment              InsecureDevelopmentEnvironment
}

type InsecureDevelopmentEnvironment struct {
	EnvironmentID       string
	RootDescriptor      json.RawMessage
	OwnerPolicySHA256   [sha256.Size]byte
	Platform            string
	CodexRelease        string
	CodexCommit         string
	CodexSHA256         [sha256.Size]byte
	OuterProfileVersion string
	ProcessMethods      []string
	InsecureDev         bool
}

type InsecureDevelopmentBootstrapResult struct {
	CreatedRows int
}

// BootstrapInsecureDevelopment opens one dedicated PostgreSQL connection and
// inserts the exact development seed. Callers must run Migrate first. Exact
// retries are no-ops; conflicting existing authority is never overwritten.
func BootstrapInsecureDevelopment(
	ctx context.Context,
	databaseURL string,
	bootstrap InsecureDevelopmentBootstrap,
) (InsecureDevelopmentBootstrapResult, error) {
	if ctx == nil {
		return InsecureDevelopmentBootstrapResult{}, errors.New("insecure development bootstrap context is required")
	}
	connectionConfig, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return InsecureDevelopmentBootstrapResult{}, errors.New("parse AGENTSERVER_V2_DATABASE_URL: invalid PostgreSQL connection string")
	}
	return bootstrapInsecureDevelopmentConfig(ctx, connectionConfig, SchemaName, bootstrap)
}

func bootstrapInsecureDevelopmentConfig(
	ctx context.Context,
	connectionConfig *pgx.ConnConfig,
	schema string,
	bootstrap InsecureDevelopmentBootstrap,
) (result InsecureDevelopmentBootstrapResult, returnErr error) {
	if connectionConfig == nil {
		return result, errors.New("PostgreSQL connection config is nil")
	}
	if !schemaNamePattern.MatchString(schema) {
		return result, fmt.Errorf("invalid development bootstrap schema name %q", schema)
	}
	if err := validateInsecureDevelopmentBootstrap(bootstrap); err != nil {
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
			returnErr = errors.Join(returnErr, fmt.Errorf("close development bootstrap connection: %w", err))
		}
	}()

	transaction, err := connection.Begin(ctx)
	if err != nil {
		return result, databaseError("begin insecure development bootstrap", err)
	}
	defer func() {
		rollbackContext, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()
		_ = transaction.Rollback(rollbackContext)
	}()
	if _, err := transaction.Exec(ctx, "SELECT pg_catalog.pg_advisory_xact_lock($1)", developmentBootstrapAdvisoryLockKey); err != nil {
		return result, databaseError("serialize insecure development bootstrap", err)
	}
	if err := requireCurrentDevelopmentBootstrapSchema(ctx, transaction, schema); err != nil {
		return result, err
	}

	quotedSchema := quoteIdentifier(schema)
	created, err := insertDevelopmentWorkspace(ctx, transaction, quotedSchema, bootstrap)
	if err != nil {
		return result, err
	}
	result.CreatedRows += created
	created, err = insertDevelopmentUser(ctx, transaction, quotedSchema, bootstrap)
	if err != nil {
		return result, err
	}
	result.CreatedRows += created
	created, err = insertDevelopmentIdentity(ctx, transaction, quotedSchema, bootstrap)
	if err != nil {
		return result, err
	}
	result.CreatedRows += created
	created, err = insertDevelopmentMembership(ctx, transaction, quotedSchema, bootstrap)
	if err != nil {
		return result, err
	}
	result.CreatedRows += created
	created, err = insertDevelopmentSession(ctx, transaction, quotedSchema, bootstrap)
	if err != nil {
		return result, err
	}
	result.CreatedRows += created
	created, err = insertDevelopmentExecutor(ctx, transaction, quotedSchema, bootstrap)
	if err != nil {
		return result, err
	}
	result.CreatedRows += created
	created, err = insertDevelopmentEnvironment(ctx, transaction, quotedSchema, bootstrap)
	if err != nil {
		return result, err
	}
	result.CreatedRows += created

	if err := transaction.Commit(ctx); err != nil {
		return InsecureDevelopmentBootstrapResult{}, databaseError("commit insecure development bootstrap", err)
	}
	return result, nil
}

func validateInsecureDevelopmentBootstrap(bootstrap InsecureDevelopmentBootstrap) error {
	for _, identity := range []struct {
		name  string
		value string
	}{
		{name: "workspace_id", value: bootstrap.WorkspaceID},
		{name: "session_id", value: bootstrap.SessionID},
		{name: "actor_id", value: bootstrap.ActorID},
		{name: "executor_id", value: bootstrap.ExecutorID},
	} {
		if err := validateUUID(identity.name, identity.value); err != nil {
			return fmt.Errorf("invalid insecure development bootstrap: %w", err)
		}
	}
	if isZeroDigest(bootstrap.MachineKeySHA256) || isZeroDigest(bootstrap.RuntimeManifestSHA256) ||
		isZeroDigest(bootstrap.ExecProtocolSourceSHA256) {
		return errors.New("invalid insecure development bootstrap: executor enrollment digests must not be all zeroes")
	}
	if err := validateBoundedText("agentx_version", bootstrap.AgentxVersion, 256); err != nil {
		return fmt.Errorf("invalid insecure development bootstrap: %w", err)
	}
	issuer, err := url.Parse(bootstrap.ExternalOIDCIssuer)
	if err != nil || issuer.Scheme != "http" || !isLoopbackBootstrapHost(issuer.Hostname()) || issuer.User != nil ||
		issuer.RawQuery != "" || issuer.Fragment != "" || issuer.Path == "" || issuer.Path == "/" || strings.HasSuffix(issuer.Path, "/") {
		return errors.New("invalid insecure development bootstrap: external OIDC issuer must be an exact cleartext loopback URL with a non-root path")
	}
	if err := validateBoundedText("external_oidc_subject", bootstrap.ExternalOIDCSubject, 2048); err != nil {
		return fmt.Errorf("invalid insecure development bootstrap: %w", err)
	}
	environment := bootstrap.Environment
	declaration := ExecutorEnvironmentDeclaration{
		ID: environment.EnvironmentID, Platform: environment.Platform,
		CodexRelease: environment.CodexRelease, CodexCommit: environment.CodexCommit,
		CodexSHA256: environment.CodexSHA256, OuterProfileVersion: environment.OuterProfileVersion,
		ProcessMethods: environment.ProcessMethods, InsecureDev: environment.InsecureDev,
	}
	if err := validateExecutorEnvironmentDeclaration(declaration); err != nil {
		return fmt.Errorf("invalid insecure development bootstrap environment: %w", err)
	}
	if !environment.InsecureDev || environment.OuterProfileVersion != execprofile.FilesystemReadVersion {
		return errors.New("invalid insecure development bootstrap environment: insecure_dev and the complete filesystem-read profile are required")
	}
	if isZeroDigest(environment.OwnerPolicySHA256) {
		return errors.New("invalid insecure development bootstrap environment: owner policy digest must not be all zeroes")
	}
	if len(environment.RootDescriptor) < 2 || len(environment.RootDescriptor) > 64*1024 {
		return errors.New("invalid insecure development bootstrap environment: root descriptor is empty or too large")
	}
	if err := validateStoredRootDescriptor(environment.RootDescriptor); err != nil {
		return fmt.Errorf("invalid insecure development bootstrap environment: %w", err)
	}
	return nil
}

func requireCurrentDevelopmentBootstrapSchema(ctx context.Context, transaction pgx.Tx, schema string) error {
	catalog, err := EmbeddedMigrations()
	if err != nil {
		return fmt.Errorf("load development bootstrap migration catalog: %w", err)
	}
	wantVersion := catalog[len(catalog)-1].Version
	query := fmt.Sprintf("SELECT COALESCE(MAX(version), 0), COUNT(*) FROM %s.schema_migrations", quoteIdentifier(schema))
	var currentVersion int64
	var applied int
	if err := transaction.QueryRow(ctx, query).Scan(&currentVersion, &applied); err != nil {
		return databaseError("verify insecure development bootstrap schema; run agentserver-core migrate first", err)
	}
	if currentVersion != wantVersion || applied != len(catalog) {
		return fmt.Errorf("insecure development bootstrap requires schema version %04d; current version is %04d with %d migration(s)", wantVersion, currentVersion, applied)
	}
	return nil
}

func insertDevelopmentWorkspace(ctx context.Context, transaction pgx.Tx, schema string, bootstrap InsecureDevelopmentBootstrap) (int, error) {
	insert := fmt.Sprintf("INSERT INTO %s.workspaces (id, status, managed_lark_credential_mode) VALUES ($1, 'active', 'webhook_swap') ON CONFLICT DO NOTHING", schema)
	created, err := developmentInsert(ctx, transaction, "insert development workspace", insert, bootstrap.WorkspaceID)
	if err != nil {
		return 0, err
	}
	query := fmt.Sprintf("SELECT status FROM %s.workspaces WHERE id = $1 FOR UPDATE", schema)
	var status string
	if err := transaction.QueryRow(ctx, query, bootstrap.WorkspaceID).Scan(&status); err != nil {
		return 0, databaseError("verify development workspace", err)
	}
	if status != "active" {
		return 0, developmentBootstrapConflict("workspace", bootstrap.WorkspaceID)
	}
	return created, nil
}

func insertDevelopmentSession(ctx context.Context, transaction pgx.Tx, schema string, bootstrap InsecureDevelopmentBootstrap) (int, error) {
	insert := fmt.Sprintf("INSERT INTO %s.sessions (id, workspace_id, creator_id) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING", schema)
	created, err := developmentInsert(ctx, transaction, "insert development session", insert, bootstrap.SessionID, bootstrap.WorkspaceID, bootstrap.ActorID)
	if err != nil {
		return 0, err
	}
	query := fmt.Sprintf("SELECT workspace_id::text, creator_id::text, status FROM %s.sessions WHERE id = $1 FOR UPDATE", schema)
	var workspaceID, creatorID, status string
	if err := transaction.QueryRow(ctx, query, bootstrap.SessionID).Scan(&workspaceID, &creatorID, &status); err != nil {
		return 0, databaseError("verify development session", err)
	}
	if workspaceID != bootstrap.WorkspaceID || creatorID != bootstrap.ActorID || status != UserSessionStatusActive {
		return 0, developmentBootstrapConflict("session", bootstrap.SessionID)
	}
	return created, nil
}

func insertDevelopmentMembership(ctx context.Context, transaction pgx.Tx, schema string, bootstrap InsecureDevelopmentBootstrap) (int, error) {
	insert := fmt.Sprintf("INSERT INTO %s.workspace_members (workspace_id, user_id, role) VALUES ($1, $2, 'owner') ON CONFLICT DO NOTHING", schema)
	created, err := developmentInsert(ctx, transaction, "insert development workspace membership", insert, bootstrap.WorkspaceID, bootstrap.ActorID)
	if err != nil {
		return 0, err
	}
	query := fmt.Sprintf("SELECT role FROM %s.workspace_members WHERE workspace_id = $1 AND user_id = $2 FOR UPDATE", schema)
	var role string
	if err := transaction.QueryRow(ctx, query, bootstrap.WorkspaceID, bootstrap.ActorID).Scan(&role); err != nil {
		return 0, databaseError("verify development workspace membership", err)
	}
	if role != "owner" {
		return 0, developmentBootstrapConflict("workspace membership", bootstrap.ActorID)
	}
	return created, nil
}

func insertDevelopmentUser(ctx context.Context, transaction pgx.Tx, schema string, bootstrap InsecureDevelopmentBootstrap) (int, error) {
	insert := fmt.Sprintf("INSERT INTO %s.users (id, status) VALUES ($1, 'active') ON CONFLICT DO NOTHING", schema)
	created, err := developmentInsert(ctx, transaction, "insert development user", insert, bootstrap.ActorID)
	if err != nil {
		return 0, err
	}
	query := fmt.Sprintf("SELECT status FROM %s.users WHERE id = $1 FOR UPDATE", schema)
	var status string
	if err := transaction.QueryRow(ctx, query, bootstrap.ActorID).Scan(&status); err != nil {
		return 0, databaseError("verify development user", err)
	}
	if status != "active" {
		return 0, developmentBootstrapConflict("user", bootstrap.ActorID)
	}
	return created, nil
}

func insertDevelopmentIdentity(ctx context.Context, transaction pgx.Tx, schema string, bootstrap InsecureDevelopmentBootstrap) (int, error) {
	insert := fmt.Sprintf(`
INSERT INTO %s.user_identities (issuer, subject, user_id, status)
VALUES ($1, $2, $3, 'active')
ON CONFLICT DO NOTHING`, schema)
	created, err := developmentInsert(
		ctx, transaction, "insert development OIDC identity", insert,
		bootstrap.ExternalOIDCIssuer, bootstrap.ExternalOIDCSubject, bootstrap.ActorID,
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
		return 0, databaseError("verify development OIDC identity", err)
	}
	if userID != bootstrap.ActorID || status != "active" {
		return 0, developmentBootstrapConflict("OIDC identity", bootstrap.ExternalOIDCSubject)
	}
	return created, nil
}

func isLoopbackBootstrapHost(host string) bool {
	return host == "127.0.0.1" || host == "::1" || strings.EqualFold(host, "localhost")
}

func insertDevelopmentExecutor(ctx context.Context, transaction pgx.Tx, schema string, bootstrap InsecureDevelopmentBootstrap) (int, error) {
	insert := fmt.Sprintf(`
INSERT INTO %s.executors
    (id, workspace_id, status, machine_key_sha256, agentx_version,
     runtime_manifest_sha256, exec_protocol_source_sha256)
VALUES ($1, $2, 'offline', $3, $4, $5, $6)
ON CONFLICT DO NOTHING`, schema)
	created, err := developmentInsert(
		ctx, transaction, "insert development executor", insert,
		bootstrap.ExecutorID, bootstrap.WorkspaceID, bootstrap.MachineKeySHA256[:],
		bootstrap.AgentxVersion, bootstrap.RuntimeManifestSHA256[:], bootstrap.ExecProtocolSourceSHA256[:],
	)
	if err != nil {
		return 0, err
	}
	query := fmt.Sprintf(`
SELECT workspace_id::text, status, machine_key_sha256, agentx_version,
       runtime_manifest_sha256, exec_protocol_source_sha256
FROM %s.executors WHERE id = $1 FOR UPDATE`, schema)
	var workspaceID, status, agentxVersion string
	var machineKey, runtimeManifest, execProtocol []byte
	if err := transaction.QueryRow(ctx, query, bootstrap.ExecutorID).Scan(
		&workspaceID, &status, &machineKey, &agentxVersion, &runtimeManifest, &execProtocol,
	); err != nil {
		return 0, databaseError("verify development executor", err)
	}
	if workspaceID != bootstrap.WorkspaceID || (status != ExecutorStatusOffline && status != ExecutorStatusOnline) ||
		agentxVersion != bootstrap.AgentxVersion || !bytes.Equal(machineKey, bootstrap.MachineKeySHA256[:]) ||
		!bytes.Equal(runtimeManifest, bootstrap.RuntimeManifestSHA256[:]) ||
		!bytes.Equal(execProtocol, bootstrap.ExecProtocolSourceSHA256[:]) {
		return 0, developmentBootstrapConflict("executor", bootstrap.ExecutorID)
	}
	return created, nil
}

func insertDevelopmentEnvironment(ctx context.Context, transaction pgx.Tx, schema string, bootstrap InsecureDevelopmentBootstrap) (int, error) {
	environment := bootstrap.Environment
	insert := fmt.Sprintf(`
INSERT INTO %s.executor_environments
    (id, executor_id, root_descriptor, owner_policy_sha256, platform,
     codex_release, codex_commit, codex_sha256, outer_profile_version,
     process_methods, insecure_dev, status)
VALUES ($1, $2, $3::jsonb, $4, $5, $6, $7, $8, $9, $10, true, 'offline')
ON CONFLICT DO NOTHING`, schema)
	created, err := developmentInsert(
		ctx, transaction, "insert development executor environment", insert,
		environment.EnvironmentID, bootstrap.ExecutorID, string(environment.RootDescriptor),
		environment.OwnerPolicySHA256[:], environment.Platform, environment.CodexRelease,
		environment.CodexCommit, environment.CodexSHA256[:], environment.OuterProfileVersion,
		environment.ProcessMethods,
	)
	if err != nil {
		return 0, err
	}
	query := fmt.Sprintf(`
SELECT executor_id::text, root_descriptor = $2::jsonb, owner_policy_sha256,
       platform, codex_release, codex_commit, codex_sha256,
       outer_profile_version, process_methods, insecure_dev, status
FROM %s.executor_environments WHERE id = $1 FOR UPDATE`, schema)
	var executorID, platform, codexRelease, codexCommit, profile, status string
	var rootMatches, insecureDev bool
	var ownerPolicy, codexDigest []byte
	var processMethods []string
	if err := transaction.QueryRow(ctx, query, environment.EnvironmentID, string(environment.RootDescriptor)).Scan(
		&executorID, &rootMatches, &ownerPolicy, &platform, &codexRelease, &codexCommit,
		&codexDigest, &profile, &processMethods, &insecureDev, &status,
	); err != nil {
		return 0, databaseError("verify development executor environment", err)
	}
	if executorID != bootstrap.ExecutorID || !rootMatches || !bytes.Equal(ownerPolicy, environment.OwnerPolicySHA256[:]) ||
		platform != environment.Platform || codexRelease != environment.CodexRelease || codexCommit != environment.CodexCommit ||
		!bytes.Equal(codexDigest, environment.CodexSHA256[:]) || profile != environment.OuterProfileVersion ||
		!slices.Equal(processMethods, environment.ProcessMethods) || !insecureDev ||
		(status != ExecutorEnvironmentStatusOffline && status != ExecutorEnvironmentStatusOnline) {
		return 0, developmentBootstrapConflict("executor environment", environment.EnvironmentID)
	}
	return created, nil
}

func developmentInsert(ctx context.Context, transaction pgx.Tx, operation, statement string, arguments ...any) (int, error) {
	tag, err := transaction.Exec(ctx, statement, arguments...)
	if err != nil {
		return 0, databaseError(operation, err)
	}
	if tag.RowsAffected() > 1 {
		return 0, databaseError(operation, errors.New("insert affected more than one row"))
	}
	return int(tag.RowsAffected()), nil
}

func developmentBootstrapConflict(resource, resourceID string) error {
	return fmt.Errorf("%w: %s %s differs", ErrInsecureDevelopmentBootstrapConflict, resource, resourceID)
}
