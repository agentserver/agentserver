package coredb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

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
