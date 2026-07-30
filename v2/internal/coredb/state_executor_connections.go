package coredb

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type enrolledExecutor struct {
	status                   string
	agentxVersion            *string
	runtimeManifestSHA256    []byte
	execProtocolSourceSHA256 []byte
}

type enrolledExecutorEnvironment struct {
	id                  string
	platform            string
	codexRelease        string
	codexCommit         string
	codexSHA256         []byte
	outerProfileVersion string
	processMethods      []string
	insecureDev         bool
	status              string
}

type executorConnectionAttempt struct {
	connectionID             string
	executorID               string
	generation               int64
	sessionID                string
	gatewayInstanceID        string
	agentxVersion            string
	runtimeManifestSHA256    []byte
	execProtocolSourceSHA256 []byte
	environmentSetSHA256     []byte
}

// AcquireExecutorConnection is the authoritative fresh-connection CAS. It
// serializes on the executor row, validates enrolled build/environment facts,
// and only then assigns a monotonically increasing generation. An exact retry
// using the same connection_id returns the committed generation without
// incrementing it again.
func (s *StateStore) AcquireExecutorConnection(ctx context.Context, command AcquireExecutorConnectionCommand) (AcquireExecutorConnectionResult, error) {
	const operation = "AcquireExecutorConnection"
	leaseMilliseconds, err := validateAcquireExecutorConnection(command)
	if err != nil {
		return AcquireExecutorConnectionResult{}, commandError(ErrorInvalidArgument, operation, "executor", command.ExecutorID, err.Error())
	}

	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (AcquireExecutorConnectionResult, error) {
		executor, err := s.lockEnrolledExecutor(ctx, transaction, operation, command.ExecutorID)
		if err != nil {
			return AcquireExecutorConnectionResult{}, err
		}
		if err := enrolledExecutorMatchesAcquire(executor, command); err != nil {
			return AcquireExecutorConnectionResult{}, commandError(ErrorConnectionFenced, operation, "executor", command.ExecutorID, err.Error())
		}
		if err := s.validateEnrolledEnvironments(ctx, transaction, operation, command.ExecutorID, command.Environments); err != nil {
			return AcquireExecutorConnectionResult{}, err
		}
		environmentSetSHA256 := hashExecutorEnvironmentDeclarations(command.Environments)

		attempt, attempted, err := s.lockExecutorConnectionAttempt(ctx, transaction, operation, command.ConnectionID)
		if err != nil {
			return AcquireExecutorConnectionResult{}, err
		}
		existing, found, err := s.lockExecutorConnection(ctx, transaction, operation, command.ExecutorID)
		if err != nil {
			return AcquireExecutorConnectionResult{}, err
		}
		if attempted {
			if !executorConnectionAttemptMatchesAcquire(attempt, command, environmentSetSHA256) {
				return AcquireExecutorConnectionResult{}, commandError(ErrorIdempotencyConflict, operation, "executor_connection", command.ExecutorID, "connection_id was already used with different session, gateway, or build metadata")
			}
			if !found || existing.ConnectionID != command.ConnectionID {
				if found {
					return AcquireExecutorConnectionResult{}, fencedExecutorConnectionError(operation, existing, "connection attempt was superseded by a newer generation")
				}
				return AcquireExecutorConnectionResult{}, &StateError{
					Code:              ErrorConnectionFenced,
					Operation:         operation,
					Resource:          "executor_connection",
					ResourceID:        command.ExecutorID,
					CurrentGeneration: attempt.generation,
					Message:           "connection attempt is no longer the current holder",
				}
			}
			if !executorConnectionMatchesAcquire(existing, command, environmentSetSHA256) {
				return AcquireExecutorConnectionResult{}, commandError(ErrorIdempotencyConflict, operation, "executor_connection", command.ExecutorID, "current connection differs from its immutable attempt record")
			}
			var live bool
			if err := transaction.QueryRow(ctx, "SELECT $1 > pg_catalog.clock_timestamp()", existing.ExpiresAt).Scan(&live); err != nil {
				return AcquireExecutorConnectionResult{}, databaseError(operation+" evaluate existing lease", err)
			}
			if !live {
				return AcquireExecutorConnectionResult{}, fencedExecutorConnectionError(operation, existing, "exact fresh acquire retry arrived after its connection lease expired")
			}
			return AcquireExecutorConnectionResult{Connection: existing, Acquired: false}, nil
		}

		generation := int64(1)
		if found {
			generation = existing.Generation + 1
		} else {
			latestGeneration, err := s.latestExecutorConnectionGeneration(ctx, transaction, operation, command.ExecutorID)
			if err != nil {
				return AcquireExecutorConnectionResult{}, err
			}
			generation = latestGeneration + 1
		}
		if generation < 1 || (found && generation <= existing.Generation) {
			return AcquireExecutorConnectionResult{}, commandError(ErrorConnectionFenced, operation, "executor_connection", command.ExecutorID, "connection generation is exhausted")
		}
		if err := s.insertExecutorConnectionAttempt(ctx, transaction, operation, command, environmentSetSHA256, generation); err != nil {
			return AcquireExecutorConnectionResult{}, err
		}
		if found {
			if err := s.endExecutorConnectionAttempt(ctx, transaction, operation, existing); err != nil {
				return AcquireExecutorConnectionResult{}, err
			}
		}
		query := fmt.Sprintf(`
INSERT INTO %s AS c
    (executor_id, generation, connection_id, session_id, gateway_instance_id,
     agentx_version, runtime_manifest_sha256, exec_protocol_source_sha256,
     environment_set_sha256, status, expires_at, acquired_at, renewed_at, version)
VALUES
    ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'connecting',
     pg_catalog.clock_timestamp() + ($10 * interval '1 millisecond'),
     pg_catalog.clock_timestamp(), pg_catalog.clock_timestamp(), 1)
ON CONFLICT (executor_id) DO UPDATE
SET generation = EXCLUDED.generation,
    connection_id = EXCLUDED.connection_id,
    session_id = EXCLUDED.session_id,
    gateway_instance_id = EXCLUDED.gateway_instance_id,
    agentx_version = EXCLUDED.agentx_version,
    runtime_manifest_sha256 = EXCLUDED.runtime_manifest_sha256,
    exec_protocol_source_sha256 = EXCLUDED.exec_protocol_source_sha256,
    environment_set_sha256 = EXCLUDED.environment_set_sha256,
    status = EXCLUDED.status,
    expires_at = EXCLUDED.expires_at,
    acquired_at = EXCLUDED.acquired_at,
    renewed_at = EXCLUDED.renewed_at,
    version = c.version + 1
RETURNING %s`, s.table("executor_connections"), executorConnectionColumns(""))
		connection, err := scanExecutorConnection(transaction.QueryRow(ctx, query,
			command.ExecutorID,
			generation,
			command.ConnectionID,
			command.SessionID,
			command.GatewayInstanceID,
			command.AgentxVersion,
			command.RuntimeManifestSHA256[:],
			command.ExecProtocolSourceSHA256[:],
			environmentSetSHA256[:],
			leaseMilliseconds,
		))
		if err != nil {
			var postgresError *pgconn.PgError
			if pgxErrorAs(err, &postgresError) && postgresError.Code == "23505" {
				return AcquireExecutorConnectionResult{}, commandError(ErrorConflict, operation, "executor_connection", command.ExecutorID, "connection_id or session_id is already in use")
			}
			return AcquireExecutorConnectionResult{}, databaseError(operation+" replace connection", err)
		}
		if err := s.markExecutorOffline(ctx, transaction, operation, command.ExecutorID); err != nil {
			return AcquireExecutorConnectionResult{}, err
		}
		return AcquireExecutorConnectionResult{Connection: connection, Acquired: true}, nil
	})
}

// RenewExecutorConnection extends only the exact live holder tuple. It never
// revives an expired generation and never changes session ownership.
func (s *StateStore) RenewExecutorConnection(ctx context.Context, command RenewExecutorConnectionCommand) (ExecutorConnection, error) {
	const operation = "RenewExecutorConnection"
	leaseMilliseconds, err := validateRenewExecutorConnection(command)
	if err != nil {
		return ExecutorConnection{}, commandError(ErrorInvalidArgument, operation, "executor_connection", command.ExecutorID, err.Error())
	}

	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (ExecutorConnection, error) {
		executor, err := s.lockEnrolledExecutor(ctx, transaction, operation, command.ExecutorID)
		if err != nil {
			return ExecutorConnection{}, err
		}
		if executor.status == ExecutorStatusRevoked || executor.status == ExecutorStatusEnrolling {
			return ExecutorConnection{}, commandError(ErrorConnectionFenced, operation, "executor", command.ExecutorID, "executor is not eligible to hold a connection")
		}
		connection, found, err := s.lockExecutorConnection(ctx, transaction, operation, command.ExecutorID)
		if err != nil {
			return ExecutorConnection{}, err
		}
		if !found {
			return ExecutorConnection{}, commandError(ErrorConnectionFenced, operation, "executor_connection", command.ExecutorID, "connection does not exist")
		}
		if !executorConnectionIdentityMatches(connection, command.SessionID, command.GatewayInstanceID, command.Generation) {
			return ExecutorConnection{}, fencedExecutorConnectionError(operation, connection, "session, gateway instance, or generation no longer owns the executor")
		}
		if connection.Status != ExecutorConnectionStatusConnecting && connection.Status != ExecutorConnectionStatusOnline {
			return ExecutorConnection{}, fencedExecutorConnectionError(operation, connection, "connection is already fenced")
		}

		query := fmt.Sprintf(`
UPDATE %s
SET expires_at = pg_catalog.clock_timestamp() + ($5 * interval '1 millisecond'),
    renewed_at = pg_catalog.clock_timestamp(),
    version = version + 1
WHERE executor_id = $1
  AND session_id = $2
  AND gateway_instance_id = $3
  AND generation = $4
  AND expires_at > pg_catalog.clock_timestamp()
RETURNING %s`, s.table("executor_connections"), executorConnectionColumns(""))
		renewed, err := scanExecutorConnection(transaction.QueryRow(ctx, query,
			command.ExecutorID,
			command.SessionID,
			command.GatewayInstanceID,
			command.Generation,
			leaseMilliseconds,
		))
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ExecutorConnection{}, fencedExecutorConnectionError(operation, connection, "connection lease expired before renewal")
			}
			return ExecutorConnection{}, databaseError(operation+" renew connection", err)
		}
		return renewed, nil
	})
}

// ActivateExecutorConnection publishes environments as online only after the
// remote initialize/initialized lifecycle has completed on the acquired
// session. An exact retry is idempotent; stale or expired holders fail closed.
func (s *StateStore) ActivateExecutorConnection(ctx context.Context, command ActivateExecutorConnectionCommand) (ActivateExecutorConnectionResult, error) {
	const operation = "ActivateExecutorConnection"
	if err := validateActivateExecutorConnection(command); err != nil {
		return ActivateExecutorConnectionResult{}, commandError(ErrorInvalidArgument, operation, "executor_connection", command.ExecutorID, err.Error())
	}
	environmentSetSHA256 := hashExecutorEnvironmentDeclarations(command.Environments)

	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (ActivateExecutorConnectionResult, error) {
		executor, err := s.lockEnrolledExecutor(ctx, transaction, operation, command.ExecutorID)
		if err != nil {
			return ActivateExecutorConnectionResult{}, err
		}
		if executor.status == ExecutorStatusRevoked || executor.status == ExecutorStatusEnrolling {
			return ActivateExecutorConnectionResult{}, commandError(ErrorConnectionFenced, operation, "executor", command.ExecutorID, "executor is not eligible to activate a connection")
		}
		if err := s.validateEnrolledEnvironments(ctx, transaction, operation, command.ExecutorID, command.Environments); err != nil {
			return ActivateExecutorConnectionResult{}, err
		}
		connection, found, err := s.lockExecutorConnection(ctx, transaction, operation, command.ExecutorID)
		if err != nil {
			return ActivateExecutorConnectionResult{}, err
		}
		if !found {
			return ActivateExecutorConnectionResult{}, commandError(ErrorConnectionFenced, operation, "executor_connection", command.ExecutorID, "connection does not exist")
		}
		if !executorConnectionIdentityMatches(connection, command.SessionID, command.GatewayInstanceID, command.Generation) {
			return ActivateExecutorConnectionResult{}, fencedExecutorConnectionError(operation, connection, "session, gateway instance, or generation no longer owns the executor")
		}
		if connection.EnvironmentSetSHA256 != environmentSetSHA256 {
			return ActivateExecutorConnectionResult{}, fencedExecutorConnectionError(operation, connection, "environment set differs from the acquired hello")
		}
		var live bool
		if err := transaction.QueryRow(ctx, "SELECT $1 > pg_catalog.clock_timestamp()", connection.ExpiresAt).Scan(&live); err != nil {
			return ActivateExecutorConnectionResult{}, databaseError(operation+" evaluate connection lease", err)
		}
		if !live || connection.Status == ExecutorConnectionStatusFenced {
			return ActivateExecutorConnectionResult{}, fencedExecutorConnectionError(operation, connection, "connection lease expired or was fenced before activation")
		}
		if connection.Status == ExecutorConnectionStatusOnline {
			return ActivateExecutorConnectionResult{Connection: connection, Activated: false}, nil
		}

		query := fmt.Sprintf(`
UPDATE %s
SET status = 'online',
    version = version + 1
WHERE executor_id = $1
  AND session_id = $2
  AND gateway_instance_id = $3
  AND generation = $4
  AND status = 'connecting'
  AND expires_at > pg_catalog.clock_timestamp()
RETURNING %s`, s.table("executor_connections"), executorConnectionColumns(""))
		activated, err := scanExecutorConnection(transaction.QueryRow(ctx, query,
			command.ExecutorID,
			command.SessionID,
			command.GatewayInstanceID,
			command.Generation,
		))
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ActivateExecutorConnectionResult{}, fencedExecutorConnectionError(operation, connection, "connection changed or expired during activation")
			}
			return ActivateExecutorConnectionResult{}, databaseError(operation+" activate connection", err)
		}
		if err := s.markExecutorConnected(ctx, transaction, operation, command.ExecutorID, command.Environments); err != nil {
			return ActivateExecutorConnectionResult{}, err
		}
		return ActivateExecutorConnectionResult{Connection: activated, Activated: true}, nil
	})
}

// FenceExecutorConnection expires the exact holder and marks its executor and
// currently online environments offline. It is for terminal protocol/revoke
// paths; a resumable transport disconnect must leave the lease intact.
func (s *StateStore) FenceExecutorConnection(ctx context.Context, command FenceExecutorConnectionCommand) (bool, error) {
	const operation = "FenceExecutorConnection"
	if err := validateFenceExecutorConnection(command); err != nil {
		return false, commandError(ErrorInvalidArgument, operation, "executor_connection", command.ExecutorID, err.Error())
	}

	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (bool, error) {
		if _, err := s.lockEnrolledExecutor(ctx, transaction, operation, command.ExecutorID); err != nil {
			return false, err
		}
		connection, found, err := s.lockExecutorConnection(ctx, transaction, operation, command.ExecutorID)
		if err != nil {
			return false, err
		}
		if !found {
			return false, commandError(ErrorConnectionFenced, operation, "executor_connection", command.ExecutorID, "connection does not exist")
		}
		if !executorConnectionIdentityMatches(connection, command.SessionID, command.GatewayInstanceID, command.Generation) {
			return false, fencedExecutorConnectionError(operation, connection, "session, gateway instance, or generation no longer owns the executor")
		}

		query := fmt.Sprintf(`
UPDATE %s
SET expires_at = LEAST(expires_at, pg_catalog.clock_timestamp()),
    renewed_at = pg_catalog.clock_timestamp(),
    status = 'fenced',
    version = version + 1
WHERE executor_id = $1
  AND status <> 'fenced'`, s.table("executor_connections"))
		tag, err := transaction.Exec(ctx, query, command.ExecutorID)
		if err != nil {
			return false, databaseError(operation+" expire connection", err)
		}
		if tag.RowsAffected() == 0 {
			return false, nil
		}
		query = fmt.Sprintf(`
UPDATE %s
SET ended_at = COALESCE(ended_at, pg_catalog.clock_timestamp()),
    end_reason = COALESCE(end_reason, 'fenced')
WHERE connection_id = $1`, s.table("executor_connection_attempts"))
		if _, err := transaction.Exec(ctx, query, connection.ConnectionID); err != nil {
			return false, databaseError(operation+" close connection attempt", err)
		}
		if err := s.markExecutorOffline(ctx, transaction, operation, command.ExecutorID); err != nil {
			return false, err
		}
		return true, nil
	})
}

func (s *StateStore) lockEnrolledExecutor(ctx context.Context, transaction pgx.Tx, operation, executorID string) (enrolledExecutor, error) {
	query := fmt.Sprintf(`
SELECT status, agentx_version, runtime_manifest_sha256, exec_protocol_source_sha256
FROM %s
WHERE id = $1
FOR UPDATE`, s.table("executors"))
	var executor enrolledExecutor
	err := transaction.QueryRow(ctx, query, executorID).Scan(
		&executor.status,
		&executor.agentxVersion,
		&executor.runtimeManifestSHA256,
		&executor.execProtocolSourceSHA256,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return enrolledExecutor{}, commandError(ErrorNotFound, operation, "executor", executorID, "executor does not exist")
	}
	if err != nil {
		return enrolledExecutor{}, databaseError(operation+" lock executor", err)
	}
	return executor, nil
}

func (s *StateStore) lockExecutorConnection(ctx context.Context, transaction pgx.Tx, operation, executorID string) (ExecutorConnection, bool, error) {
	query := fmt.Sprintf(`
SELECT %s
FROM %s
WHERE executor_id = $1
FOR UPDATE`, executorConnectionColumns(""), s.table("executor_connections"))
	connection, err := scanExecutorConnection(transaction.QueryRow(ctx, query, executorID))
	if errors.Is(err, pgx.ErrNoRows) {
		return ExecutorConnection{}, false, nil
	}
	if err != nil {
		return ExecutorConnection{}, false, databaseError(operation+" lock connection", err)
	}
	return connection, true, nil
}

func (s *StateStore) lockExecutorConnectionAttempt(ctx context.Context, transaction pgx.Tx, operation, connectionID string) (executorConnectionAttempt, bool, error) {
	query := fmt.Sprintf(`
SELECT connection_id::text, executor_id::text, generation, session_id::text,
       gateway_instance_id::text, agentx_version, runtime_manifest_sha256,
       exec_protocol_source_sha256, environment_set_sha256
FROM %s
WHERE connection_id = $1
FOR UPDATE`, s.table("executor_connection_attempts"))
	var attempt executorConnectionAttempt
	err := transaction.QueryRow(ctx, query, connectionID).Scan(
		&attempt.connectionID,
		&attempt.executorID,
		&attempt.generation,
		&attempt.sessionID,
		&attempt.gatewayInstanceID,
		&attempt.agentxVersion,
		&attempt.runtimeManifestSHA256,
		&attempt.execProtocolSourceSHA256,
		&attempt.environmentSetSHA256,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return executorConnectionAttempt{}, false, nil
	}
	if err != nil {
		return executorConnectionAttempt{}, false, databaseError(operation+" lock connection attempt", err)
	}
	return attempt, true, nil
}

func (s *StateStore) latestExecutorConnectionGeneration(ctx context.Context, transaction pgx.Tx, operation, executorID string) (int64, error) {
	query := fmt.Sprintf(`
SELECT COALESCE(MAX(generation), 0)
FROM %s
WHERE executor_id = $1`, s.table("executor_connection_attempts"))
	var generation int64
	if err := transaction.QueryRow(ctx, query, executorID).Scan(&generation); err != nil {
		return 0, databaseError(operation+" read latest connection generation", err)
	}
	return generation, nil
}

func (s *StateStore) insertExecutorConnectionAttempt(
	ctx context.Context,
	transaction pgx.Tx,
	operation string,
	command AcquireExecutorConnectionCommand,
	environmentSetSHA256 [32]byte,
	generation int64,
) error {
	query := fmt.Sprintf(`
INSERT INTO %s
    (connection_id, executor_id, generation, session_id, gateway_instance_id,
     agentx_version, runtime_manifest_sha256, exec_protocol_source_sha256,
     environment_set_sha256)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`, s.table("executor_connection_attempts"))
	if _, err := transaction.Exec(ctx, query,
		command.ConnectionID,
		command.ExecutorID,
		generation,
		command.SessionID,
		command.GatewayInstanceID,
		command.AgentxVersion,
		command.RuntimeManifestSHA256[:],
		command.ExecProtocolSourceSHA256[:],
		environmentSetSHA256[:],
	); err != nil {
		var postgresError *pgconn.PgError
		if pgxErrorAs(err, &postgresError) && postgresError.Code == "23505" {
			return commandError(ErrorConflict, operation, "executor_connection", command.ExecutorID, "connection_id, session_id, or generation is already in use")
		}
		return databaseError(operation+" insert connection attempt", err)
	}
	return nil
}

func (s *StateStore) endExecutorConnectionAttempt(ctx context.Context, transaction pgx.Tx, operation string, connection ExecutorConnection) error {
	query := fmt.Sprintf(`
UPDATE %s
SET ended_at = COALESCE(ended_at, pg_catalog.clock_timestamp()),
    end_reason = COALESCE(
        end_reason,
        CASE
            WHEN $2 > pg_catalog.clock_timestamp() THEN 'replaced'
            ELSE 'expired'
        END
    )
WHERE connection_id = $1`, s.table("executor_connection_attempts"))
	if _, err := transaction.Exec(ctx, query, connection.ConnectionID, connection.ExpiresAt); err != nil {
		return databaseError(operation+" close previous connection attempt", err)
	}
	return nil
}

func enrolledExecutorMatchesAcquire(executor enrolledExecutor, command AcquireExecutorConnectionCommand) error {
	if executor.status == ExecutorStatusEnrolling {
		return errors.New("executor enrollment is incomplete")
	}
	if executor.status == ExecutorStatusRevoked {
		return errors.New("executor is revoked")
	}
	if executor.status != ExecutorStatusOffline && executor.status != ExecutorStatusOnline {
		return fmt.Errorf("executor status %q cannot acquire a connection", executor.status)
	}
	if executor.agentxVersion == nil || *executor.agentxVersion != command.AgentxVersion {
		return errors.New("agentx version does not match enrollment")
	}
	if !bytes.Equal(executor.runtimeManifestSHA256, command.RuntimeManifestSHA256[:]) {
		return errors.New("runtime manifest digest does not match enrollment")
	}
	if !bytes.Equal(executor.execProtocolSourceSHA256, command.ExecProtocolSourceSHA256[:]) {
		return errors.New("exec protocol source digest does not match enrollment")
	}
	return nil
}

func (s *StateStore) validateEnrolledEnvironments(ctx context.Context, transaction pgx.Tx, operation, executorID string, declarations []ExecutorEnvironmentDeclaration) error {
	query := fmt.Sprintf(`
SELECT id::text, platform, codex_release, codex_commit, codex_sha256,
       outer_profile_version, process_methods, insecure_dev, status
FROM %s
WHERE executor_id = $1
FOR UPDATE`, s.table("executor_environments"))
	rows, err := transaction.Query(ctx, query, executorID)
	if err != nil {
		return databaseError(operation+" lock environments", err)
	}
	defer rows.Close()

	enrolled := make(map[string]enrolledExecutorEnvironment)
	for rows.Next() {
		var environment enrolledExecutorEnvironment
		if err := rows.Scan(
			&environment.id,
			&environment.platform,
			&environment.codexRelease,
			&environment.codexCommit,
			&environment.codexSHA256,
			&environment.outerProfileVersion,
			&environment.processMethods,
			&environment.insecureDev,
			&environment.status,
		); err != nil {
			return databaseError(operation+" scan environment", err)
		}
		enrolled[environment.id] = environment
	}
	if err := rows.Err(); err != nil {
		return databaseError(operation+" read environments", err)
	}

	for _, declaration := range declarations {
		environment, found := enrolled[declaration.ID]
		if !found {
			return commandError(ErrorConnectionFenced, operation, "executor_environment", declaration.ID, "environment is not enrolled for this executor")
		}
		if environment.status == ExecutorEnvironmentStatusDisabled {
			return commandError(ErrorConnectionFenced, operation, "executor_environment", declaration.ID, "environment is disabled")
		}
		if !enrolledEnvironmentMatchesDeclaration(environment, declaration) {
			return commandError(ErrorConnectionFenced, operation, "executor_environment", declaration.ID, "build or process profile does not match enrollment")
		}
	}
	return nil
}

func enrolledEnvironmentMatchesDeclaration(enrolled enrolledExecutorEnvironment, declaration ExecutorEnvironmentDeclaration) bool {
	return enrolled.platform == declaration.Platform &&
		enrolled.codexRelease == declaration.CodexRelease &&
		enrolled.codexCommit == declaration.CodexCommit &&
		bytes.Equal(enrolled.codexSHA256, declaration.CodexSHA256[:]) &&
		enrolled.outerProfileVersion == declaration.OuterProfileVersion &&
		slices.Equal(enrolled.processMethods, declaration.ProcessMethods) &&
		enrolled.insecureDev == declaration.InsecureDev
}

func executorConnectionMatchesAcquire(connection ExecutorConnection, command AcquireExecutorConnectionCommand, environmentSetSHA256 [32]byte) bool {
	return connection.ExecutorID == command.ExecutorID &&
		connection.ConnectionID == command.ConnectionID &&
		connection.SessionID == command.SessionID &&
		connection.GatewayInstanceID == command.GatewayInstanceID &&
		connection.AgentxVersion == command.AgentxVersion &&
		connection.RuntimeManifestSHA256 == command.RuntimeManifestSHA256 &&
		connection.ExecProtocolSourceSHA256 == command.ExecProtocolSourceSHA256 &&
		connection.EnvironmentSetSHA256 == environmentSetSHA256
}

func executorConnectionAttemptMatchesAcquire(attempt executorConnectionAttempt, command AcquireExecutorConnectionCommand, environmentSetSHA256 [32]byte) bool {
	return attempt.connectionID == command.ConnectionID &&
		attempt.executorID == command.ExecutorID &&
		attempt.sessionID == command.SessionID &&
		attempt.gatewayInstanceID == command.GatewayInstanceID &&
		attempt.agentxVersion == command.AgentxVersion &&
		bytes.Equal(attempt.runtimeManifestSHA256, command.RuntimeManifestSHA256[:]) &&
		bytes.Equal(attempt.execProtocolSourceSHA256, command.ExecProtocolSourceSHA256[:]) &&
		bytes.Equal(attempt.environmentSetSHA256, environmentSetSHA256[:])
}

func executorConnectionIdentityMatches(connection ExecutorConnection, sessionID, gatewayInstanceID string, generation int64) bool {
	return connection.SessionID == sessionID &&
		connection.GatewayInstanceID == gatewayInstanceID &&
		connection.Generation == generation
}

func (s *StateStore) markExecutorConnected(ctx context.Context, transaction pgx.Tx, operation, executorID string, environments []ExecutorEnvironmentDeclaration) error {
	query := fmt.Sprintf(`
UPDATE %s
SET status = 'online',
    version = version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $1`, s.table("executors"))
	if _, err := transaction.Exec(ctx, query, executorID); err != nil {
		return databaseError(operation+" mark executor online", err)
	}

	environmentIDs := make([]string, len(environments))
	for index, environment := range environments {
		environmentIDs[index] = environment.ID
	}
	query = fmt.Sprintf(`
UPDATE %s
SET status = CASE WHEN id = ANY($2::uuid[]) THEN 'online' ELSE 'offline' END,
    version = version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE executor_id = $1
  AND status <> 'disabled'
  AND status IS DISTINCT FROM CASE WHEN id = ANY($2::uuid[]) THEN 'online' ELSE 'offline' END`, s.table("executor_environments"))
	if _, err := transaction.Exec(ctx, query, executorID, environmentIDs); err != nil {
		return databaseError(operation+" project environment status", err)
	}
	return nil
}

func (s *StateStore) markExecutorOffline(ctx context.Context, transaction pgx.Tx, operation, executorID string) error {
	query := fmt.Sprintf(`
UPDATE %s
SET status = 'offline',
    version = version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $1 AND status = 'online'`, s.table("executors"))
	if _, err := transaction.Exec(ctx, query, executorID); err != nil {
		return databaseError(operation+" mark executor offline", err)
	}
	query = fmt.Sprintf(`
UPDATE %s
SET status = 'offline',
    version = version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE executor_id = $1 AND status = 'online'`, s.table("executor_environments"))
	if _, err := transaction.Exec(ctx, query, executorID); err != nil {
		return databaseError(operation+" mark environments offline", err)
	}
	return nil
}

func fencedExecutorConnectionError(operation string, connection ExecutorConnection, message string) error {
	return &StateError{
		Code:              ErrorConnectionFenced,
		Operation:         operation,
		Resource:          "executor_connection",
		ResourceID:        connection.ExecutorID,
		CurrentVersion:    connection.Version,
		CurrentGeneration: connection.Generation,
		Message:           message,
	}
}
