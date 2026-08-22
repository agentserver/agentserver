package coredb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/agentserver/agentserver/v2/internal/execprofile"
	"github.com/jackc/pgx/v5"
)

const managedEnvironmentBootstrapAdvisoryLockKey int64 = 0x6d616e6167656465

// ErrManagedEnvironmentProfileConflict means the requested environment ID is
// already owned by a different executor. Deployment-owned metadata for an
// environment belonging to the same executor is updated in place.
var ErrManagedEnvironmentProfileConflict = errors.New("managed environment ID belongs to another executor")

// ManagedEnvironmentProfile is deployment-owned metadata for one managed
// execution environment. ExecutorID remains a logical catalog/capability owner;
// it is never used as the TAE dispatch target. The live sandbox generation is
// projected separately from managed_sandboxes.
type ManagedEnvironmentProfile struct {
	WorkspaceID   string
	ExecutorID    string
	EnvironmentID string

	RootDescriptor json.RawMessage
	CodexRelease   string
	CodexCommit    string
	CodexSHA256    [sha256.Size]byte
}

type ManagedEnvironmentProfileBootstrapResult struct {
	Created       bool
	SchemaVersion int64
}

// BootstrapManagedEnvironmentProfile reconciles one retry-safe TAE
// environment profile after migrations and workspace/executor bootstrap have
// completed. It is intentionally a deployment command rather than a serve-time
// mutation or a model-visible management API.
func BootstrapManagedEnvironmentProfile(
	ctx context.Context,
	databaseURL string,
	profile ManagedEnvironmentProfile,
) (ManagedEnvironmentProfileBootstrapResult, error) {
	if ctx == nil {
		return ManagedEnvironmentProfileBootstrapResult{}, errors.New("managed environment profile bootstrap context is required")
	}
	connectionConfig, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return ManagedEnvironmentProfileBootstrapResult{}, errors.New("parse AGENTSERVER_V2_DATABASE_URL: invalid PostgreSQL connection string")
	}
	return bootstrapManagedEnvironmentProfileConfig(ctx, connectionConfig, SchemaName, profile)
}

func bootstrapManagedEnvironmentProfileConfig(
	ctx context.Context,
	connectionConfig *pgx.ConnConfig,
	schema string,
	profile ManagedEnvironmentProfile,
) (result ManagedEnvironmentProfileBootstrapResult, returnErr error) {
	if connectionConfig == nil {
		return result, errors.New("PostgreSQL connection config is nil")
	}
	if !schemaNamePattern.MatchString(schema) {
		return result, fmt.Errorf("invalid managed environment bootstrap schema name %q", schema)
	}
	if err := validateManagedEnvironmentProfile(profile); err != nil {
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
			returnErr = errors.Join(returnErr, fmt.Errorf("close managed environment bootstrap connection: %w", err))
		}
	}()

	transaction, err := connection.Begin(ctx)
	if err != nil {
		return result, databaseError("begin managed environment profile bootstrap", err)
	}
	defer func() {
		rollbackContext, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()
		_ = transaction.Rollback(rollbackContext)
	}()
	if _, err := transaction.Exec(ctx, "SELECT pg_catalog.pg_advisory_xact_lock($1)", managedEnvironmentBootstrapAdvisoryLockKey); err != nil {
		return result, databaseError("serialize managed environment profile bootstrap", err)
	}
	result.SchemaVersion, err = requireCurrentManagedEnvironmentBootstrapSchema(ctx, transaction, schema)
	if err != nil {
		return result, err
	}

	quotedSchema := quoteIdentifier(schema)
	if err := requireManagedEnvironmentExecutor(ctx, transaction, quotedSchema, profile); err != nil {
		return result, err
	}
	created, err := insertManagedEnvironmentProfile(ctx, transaction, quotedSchema, profile)
	if err != nil {
		return result, err
	}
	result.Created = created == 1
	if err := transaction.Commit(ctx); err != nil {
		return ManagedEnvironmentProfileBootstrapResult{}, databaseError("commit managed environment profile bootstrap", err)
	}
	return result, nil
}

func validateManagedEnvironmentProfile(profile ManagedEnvironmentProfile) error {
	for _, identity := range []struct {
		name  string
		value string
	}{
		{"workspace_id", profile.WorkspaceID},
		{"executor_id", profile.ExecutorID},
		{"environment_id", profile.EnvironmentID},
	} {
		if err := validateUUID(identity.name, identity.value); err != nil {
			return fmt.Errorf("invalid managed environment profile: %w", err)
		}
	}
	declaration := ExecutorEnvironmentDeclaration{
		ID: profile.EnvironmentID, Platform: "linux-amd64",
		CodexRelease: profile.CodexRelease, CodexCommit: profile.CodexCommit,
		CodexSHA256: profile.CodexSHA256, OuterProfileVersion: execprofile.FilesystemReadVersion,
		ProcessMethods: execprofile.ProcessMethods(), InsecureDev: false,
	}
	if err := validateExecutorEnvironmentDeclaration(declaration); err != nil {
		return fmt.Errorf("invalid managed environment profile: %w", err)
	}
	if err := validateManagedRootDescriptor(profile.RootDescriptor); err != nil {
		return fmt.Errorf("invalid managed environment profile: %w", err)
	}
	return nil
}

func validateManagedRootDescriptor(raw json.RawMessage) error {
	if len(raw) < 2 || len(raw) > 64*1024 {
		return errors.New("root descriptor is empty or too large")
	}
	var descriptor struct {
		Kind        string `json:"kind"`
		Root        string `json:"root"`
		DisplayName string `json:"displayName,omitempty"`
		Description string `json:"description,omitempty"`
		DefaultCWD  string `json:"defaultCwd,omitempty"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&descriptor); err != nil {
		return fmt.Errorf("decode managed root descriptor: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("managed root descriptor contains additional JSON")
		}
		return err
	}
	if descriptor.Kind != "managed" {
		return errors.New("managed root descriptor kind must be managed")
	}
	if len(descriptor.Root) < 1 || len(descriptor.Root) > 4096 || !strings.HasPrefix(descriptor.Root, "/") || path.Clean(descriptor.Root) != descriptor.Root {
		return errors.New("managed root must be a clean absolute Unix path")
	}
	if err := validateBoundedText("managed display_name", descriptor.DisplayName, 256); descriptor.DisplayName != "" && err != nil {
		return err
	}
	if err := validateBoundedText("managed description", descriptor.Description, 2048); descriptor.Description != "" && err != nil {
		return err
	}
	if descriptor.DefaultCWD != "" {
		if len(descriptor.DefaultCWD) > 4096 || strings.HasPrefix(descriptor.DefaultCWD, "/") || path.Clean(descriptor.DefaultCWD) != descriptor.DefaultCWD ||
			descriptor.DefaultCWD == ".." || strings.HasPrefix(descriptor.DefaultCWD, "../") {
			return errors.New("managed default cwd must be a clean relative path within the root")
		}
	}
	return nil
}

func requireCurrentManagedEnvironmentBootstrapSchema(ctx context.Context, transaction pgx.Tx, schema string) (int64, error) {
	catalog, err := EmbeddedMigrations()
	if err != nil {
		return 0, fmt.Errorf("load managed environment bootstrap migration catalog: %w", err)
	}
	wantVersion := catalog[len(catalog)-1].Version
	query := fmt.Sprintf("SELECT COALESCE(MAX(version), 0), COUNT(*) FROM %s.schema_migrations", quoteIdentifier(schema))
	var currentVersion int64
	var applied int
	if err := transaction.QueryRow(ctx, query).Scan(&currentVersion, &applied); err != nil {
		return 0, databaseError("verify managed environment bootstrap schema; run agentserver-core migrate first", err)
	}
	if currentVersion != wantVersion || applied != len(catalog) {
		return 0, fmt.Errorf("managed environment bootstrap requires schema version %04d; current version is %04d with %d migration(s)", wantVersion, currentVersion, applied)
	}
	return currentVersion, nil
}

func requireManagedEnvironmentExecutor(ctx context.Context, transaction pgx.Tx, schema string, profile ManagedEnvironmentProfile) error {
	query := fmt.Sprintf("SELECT workspace_id::text, status FROM %s.executors WHERE id = $1 FOR UPDATE", schema)
	var workspaceID, status string
	if err := transaction.QueryRow(ctx, query, profile.ExecutorID).Scan(&workspaceID, &status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errors.New("managed environment profile executor does not exist; run workspace bootstrap first")
		}
		return databaseError("read managed environment profile executor", err)
	}
	if workspaceID != profile.WorkspaceID || status == ExecutorStatusRevoked {
		return managedEnvironmentProfileConflict("executor", profile.ExecutorID)
	}
	return nil
}

func insertManagedEnvironmentProfile(ctx context.Context, transaction pgx.Tx, schema string, profile ManagedEnvironmentProfile) (int, error) {
	var existingExecutorID string
	lookup := fmt.Sprintf("SELECT executor_id::text FROM %s.executor_environments WHERE id = $1 FOR UPDATE", schema)
	err := transaction.QueryRow(ctx, lookup, profile.EnvironmentID).Scan(&existingExecutorID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return 0, databaseError("read managed environment profile", err)
	}
	if err == nil && existingExecutorID != profile.ExecutorID {
		return 0, managedEnvironmentProfileConflict("environment", profile.EnvironmentID)
	}

	if errors.Is(err, pgx.ErrNoRows) {
		insert := fmt.Sprintf(`
INSERT INTO %s.executor_environments
    (id, executor_id, root_descriptor, owner_policy_sha256, platform,
     codex_release, codex_commit, codex_sha256, outer_profile_version,
     process_methods, insecure_dev, status, backend_kind)
VALUES
    ($1, $2, $3::jsonb, $4, 'linux-amd64',
	     $5, $6, $7, $8, $9, false, 'online', 'tae')`, schema)
		created, insertErr := developmentInsert(
			ctx, transaction, "insert managed environment profile", insert,
			profile.EnvironmentID, profile.ExecutorID, string(profile.RootDescriptor), nil,
			profile.CodexRelease, profile.CodexCommit, profile.CodexSHA256[:],
			execprofile.FilesystemReadVersion, execprofile.ProcessMethods(),
		)
		if insertErr != nil {
			return 0, insertErr
		}
		return created, nil
	}

	update := fmt.Sprintf(`
UPDATE %s.executor_environments
SET root_descriptor = $2::jsonb,
    owner_policy_sha256 = $3,
    platform = 'linux-amd64',
    codex_release = $4,
    codex_commit = $5,
    codex_sha256 = $6,
    outer_profile_version = $7,
    process_methods = $8,
    insecure_dev = false,
    status = 'online',
    backend_kind = 'tae'
WHERE id = $1 AND executor_id = $9`, schema)
	tag, err := transaction.Exec(
		ctx, update, profile.EnvironmentID, string(profile.RootDescriptor), nil,
		profile.CodexRelease, profile.CodexCommit, profile.CodexSHA256[:],
		execprofile.FilesystemReadVersion, execprofile.ProcessMethods(), profile.ExecutorID,
	)
	if err != nil {
		return 0, databaseError("update managed environment profile", err)
	}
	if tag.RowsAffected() != 1 {
		return 0, managedEnvironmentProfileConflict("environment", profile.EnvironmentID)
	}
	return 0, nil
}

func managedEnvironmentProfileConflict(resource, resourceID string) error {
	return fmt.Errorf("%w: %s %s differs", ErrManagedEnvironmentProfileConflict, resource, resourceID)
}
