package coredb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"

	"github.com/jackc/pgx/v5"
)

// ListOnlineExecutorEnvironments reads only environments whose enrolled
// executor and exact connection holder are online and whose lease is still
// live according to the database clock. It never revives stale projections.
func (s *StateStore) ListOnlineExecutorEnvironments(ctx context.Context, query ListOnlineExecutorEnvironmentsQuery) ([]OnlineExecutorEnvironment, error) {
	const operation = "ListOnlineExecutorEnvironments"
	if err := validateUUID("workspace_id", query.WorkspaceID); err != nil {
		return nil, commandError(ErrorInvalidArgument, operation, "workspace", query.WorkspaceID, err.Error())
	}
	if query.ExecutorID != "" {
		if err := validateUUID("executor_id", query.ExecutorID); err != nil {
			return nil, commandError(ErrorInvalidArgument, operation, "executor", query.ExecutorID, err.Error())
		}
	}
	scoped := query.SessionID != "" || query.RunAttemptID != "" || query.RunAttemptGeneration != 0
	if scoped {
		if err := validateUUID("session_id", query.SessionID); err != nil {
			return nil, commandError(ErrorInvalidArgument, operation, "session", query.SessionID, err.Error())
		}
		if err := validateUUID("run_attempt_id", query.RunAttemptID); err != nil {
			return nil, commandError(ErrorInvalidArgument, operation, "run_attempt", query.RunAttemptID, err.Error())
		}
		if query.RunAttemptGeneration < 1 {
			return nil, commandError(ErrorInvalidArgument, operation, "run_attempt", query.RunAttemptID, "run attempt generation must be positive")
		}
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) ([]OnlineExecutorEnvironment, error) {
		arguments := []any{query.WorkspaceID}
		executorFilter := ""
		if query.ExecutorID != "" {
			arguments = append(arguments, query.ExecutorID)
			executorFilter = " AND env.executor_id = $2"
		}
		statement := fmt.Sprintf(`
SELECT env.id::text,
       env.executor_id::text,
       env.root_descriptor::text,
       env.platform,
       env.outer_profile_version,
       env.insecure_dev,
       env.version,
       connection.generation
FROM %s AS env
JOIN %s AS executor ON executor.id = env.executor_id
JOIN %s AS connection ON connection.executor_id = executor.id
WHERE executor.workspace_id = $1
	  AND env.backend_kind = 'agentx'
  AND executor.status = 'online'
  AND env.status = 'online'
  AND connection.status = 'online'
  AND connection.expires_at > pg_catalog.clock_timestamp()%s
ORDER BY env.executor_id, env.id
LIMIT %d`, s.table("executor_environments"), s.table("executors"), s.table("executor_connections"), executorFilter, MaxListedExecutorEnvironments+1)
		rows, err := transaction.Query(ctx, statement, arguments...)
		if err != nil {
			return nil, databaseError(operation+" query environments", err)
		}
		defer rows.Close()
		result := make([]OnlineExecutorEnvironment, 0)
		for rows.Next() {
			var environment OnlineExecutorEnvironment
			var rootDescriptor []byte
			if err := rows.Scan(
				&environment.EnvironmentID,
				&environment.ExecutorID,
				&rootDescriptor,
				&environment.Platform,
				&environment.OuterProfileVersion,
				&environment.InsecureDev,
				&environment.EnvironmentVersion,
				&environment.ConnectionGeneration,
			); err != nil {
				return nil, databaseError(operation+" scan environment", err)
			}
			if err := validateStoredRootDescriptor(rootDescriptor); err != nil {
				return nil, databaseError(operation+" validate stored root descriptor", err)
			}
			environment.RootDescriptor = append(json.RawMessage(nil), rootDescriptor...)
			result = append(result, environment)
			if len(result) > MaxListedExecutorEnvironments {
				return nil, commandError(ErrorConflict, operation, "workspace", query.WorkspaceID, "online environment result exceeds the Phase 1 bound; use an executor filter")
			}
		}
		if err := rows.Err(); err != nil {
			return nil, databaseError(operation+" iterate environments", err)
		}
		if scoped {
			managedArguments := []any{query.WorkspaceID, query.SessionID, query.RunAttemptID, query.RunAttemptGeneration}
			managedExecutorFilter := ""
			if query.ExecutorID != "" {
				managedArguments = append(managedArguments, query.ExecutorID)
				managedExecutorFilter = " AND env.executor_id = $5"
			}
			managedStatement := fmt.Sprintf(`
SELECT env.id::text,
       env.executor_id::text,
       env.root_descriptor::text,
       env.platform,
       env.outer_profile_version,
       env.insecure_dev,
       env.version,
       sandbox.id::text,
       sandbox.generation
FROM %s AS env
JOIN %s AS executor ON executor.id = env.executor_id
JOIN %s AS sandbox
  ON sandbox.workspace_id = executor.workspace_id
 AND sandbox.session_id = $2
 AND sandbox.environment_id = env.id
JOIN %s AS activity
  ON activity.sandbox_id = sandbox.id
 AND activity.target_generation = sandbox.generation
 AND activity.run_attempt_id = $3
 AND activity.run_attempt_generation = $4
WHERE executor.workspace_id = $1
  AND executor.status <> 'revoked'
  AND env.backend_kind = 'tae'
  AND env.status = 'online'
  AND sandbox.desired_state = 'ready'
  AND sandbox.observed_state = 'ready'
  AND sandbox.expires_at > pg_catalog.clock_timestamp()
  AND activity.released_at IS NULL
  AND activity.lease_expires_at > pg_catalog.clock_timestamp()%s
ORDER BY env.executor_id, env.id
LIMIT %d`, s.table("executor_environments"), s.table("executors"), s.table("managed_sandboxes"),
				s.table("managed_sandbox_activities"), managedExecutorFilter, MaxListedExecutorEnvironments+1)
			managedRows, err := transaction.Query(ctx, managedStatement, managedArguments...)
			if err != nil {
				return nil, databaseError(operation+" query managed environments", err)
			}
			defer managedRows.Close()
			for managedRows.Next() {
				var environment OnlineExecutorEnvironment
				var rootDescriptor []byte
				if err := managedRows.Scan(
					&environment.EnvironmentID,
					&environment.ExecutorID,
					&rootDescriptor,
					&environment.Platform,
					&environment.OuterProfileVersion,
					&environment.InsecureDev,
					&environment.EnvironmentVersion,
					&environment.TargetID,
					&environment.TargetGeneration,
				); err != nil {
					return nil, databaseError(operation+" scan managed environment", err)
				}
				if err := validateStoredRootDescriptor(rootDescriptor); err != nil {
					return nil, databaseError(operation+" validate managed root descriptor", err)
				}
				environment.RootDescriptor = append(json.RawMessage(nil), rootDescriptor...)
				environment.BackendKind = DispatchTargetTAE
				result = append(result, environment)
				if len(result) > MaxListedExecutorEnvironments {
					return nil, commandError(ErrorConflict, operation, "workspace", query.WorkspaceID, "online environment result exceeds the Phase 1 bound; use an executor filter")
				}
			}
			if err := managedRows.Err(); err != nil {
				return nil, databaseError(operation+" iterate managed environments", err)
			}
		}
		sort.Slice(result, func(left, right int) bool {
			if result[left].ExecutorID != result[right].ExecutorID {
				return result[left].ExecutorID < result[right].ExecutorID
			}
			return result[left].EnvironmentID < result[right].EnvironmentID
		})
		return result, nil
	})
}

func validateStoredRootDescriptor(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var value map[string]any
	if err := decoder.Decode(&value); err != nil || value == nil {
		return errors.New("root descriptor is not a JSON object")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("root descriptor contains additional JSON")
		}
		return err
	}
	return nil
}
