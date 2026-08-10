package coredb

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgreSQLExecutorConnectionCAS(t *testing.T) {
	store, pool, schema := newPostgresStateStore(t)
	command := insertExecutorConnectionFixture(t, pool, schema, 800)

	first, err := store.AcquireExecutorConnection(t.Context(), command)
	if err != nil {
		t.Fatalf("first AcquireExecutorConnection() error = %v", err)
	}
	if !first.Acquired || first.Connection.Generation != 1 || first.Connection.Version != 1 {
		t.Fatalf("first acquire = %+v, want generation/version 1", first)
	}

	retry, err := store.AcquireExecutorConnection(t.Context(), command)
	if err != nil {
		t.Fatalf("exact AcquireExecutorConnection() retry error = %v", err)
	}
	if retry.Acquired || retry.Connection != first.Connection {
		t.Fatalf("exact acquire retry = %+v, want unchanged committed connection", retry)
	}
	assertExecutorRuntimeStatus(t, pool, schema, command.ExecutorID, command.Environments[0].ID, ExecutorStatusOffline, ExecutorEnvironmentStatusOffline)
	activated, err := store.ActivateExecutorConnection(t.Context(), ActivateExecutorConnectionCommand{
		ExecutorID:        command.ExecutorID,
		SessionID:         command.SessionID,
		GatewayInstanceID: command.GatewayInstanceID,
		Generation:        first.Connection.Generation,
		Environments:      command.Environments,
	})
	if err != nil || !activated.Activated || activated.Connection.Status != ExecutorConnectionStatusOnline {
		t.Fatalf("ActivateExecutorConnection() = %+v, %v", activated, err)
	}
	activationRetry, err := store.ActivateExecutorConnection(t.Context(), ActivateExecutorConnectionCommand{
		ExecutorID:        command.ExecutorID,
		SessionID:         command.SessionID,
		GatewayInstanceID: command.GatewayInstanceID,
		Generation:        first.Connection.Generation,
		Environments:      command.Environments,
	})
	if err != nil || activationRetry.Activated {
		t.Fatalf("exact ActivateExecutorConnection() retry = %+v, %v", activationRetry, err)
	}
	assertExecutorRuntimeStatus(t, pool, schema, command.ExecutorID, command.Environments[0].ID, ExecutorStatusOnline, ExecutorEnvironmentStatusOnline)
	online, err := store.ListOnlineExecutorEnvironments(t.Context(), ListOnlineExecutorEnvironmentsQuery{
		WorkspaceID: stateTestUUID(805),
		ExecutorID:  command.ExecutorID,
	})
	if err != nil || len(online) != 1 {
		t.Fatalf("ListOnlineExecutorEnvironments() = %+v, %v", online, err)
	}
	var rootDescriptor map[string]any
	if err := json.Unmarshal(online[0].RootDescriptor, &rootDescriptor); err != nil || rootDescriptor["root"] != "/workspace" {
		t.Fatalf("online root descriptor = %s, %v", online[0].RootDescriptor, err)
	}
	if online[0].ConnectionGeneration != first.Connection.Generation || online[0].EnvironmentVersion < 1 ||
		online[0].OuterProfileVersion != command.Environments[0].OuterProfileVersion {
		t.Fatalf("online environment generation/version = %+v", online[0])
	}

	wrongHolder := RenewExecutorConnectionCommand{
		ExecutorID:        command.ExecutorID,
		SessionID:         command.SessionID,
		GatewayInstanceID: stateTestUUID(899),
		Generation:        first.Connection.Generation,
		LeaseTTL:          command.LeaseTTL,
	}
	if _, err := store.RenewExecutorConnection(t.Context(), wrongHolder); !HasStateErrorCode(err, ErrorConnectionFenced) {
		t.Fatalf("wrong-holder RenewExecutorConnection() error = %v, want connection_fenced", err)
	}

	renewed, err := store.RenewExecutorConnection(t.Context(), RenewExecutorConnectionCommand{
		ExecutorID:        command.ExecutorID,
		SessionID:         command.SessionID,
		GatewayInstanceID: command.GatewayInstanceID,
		Generation:        first.Connection.Generation,
		LeaseTTL:          command.LeaseTTL,
	})
	if err != nil {
		t.Fatalf("RenewExecutorConnection() error = %v", err)
	}
	if renewed.Version != activated.Connection.Version+1 || !renewed.ExpiresAt.After(first.Connection.ExpiresAt) {
		t.Fatalf("renewed connection = %+v, first = %+v", renewed, first.Connection)
	}

	secondCommand := command
	secondCommand.ConnectionID = stateTestUUID(806)
	secondCommand.SessionID = stateTestUUID(807)
	second, err := store.AcquireExecutorConnection(t.Context(), secondCommand)
	if err != nil {
		t.Fatalf("replacement AcquireExecutorConnection() error = %v", err)
	}
	if !second.Acquired || second.Connection.Generation != 2 {
		t.Fatalf("replacement acquire = %+v, want generation 2", second)
	}
	if second.Connection.Status != ExecutorConnectionStatusConnecting {
		t.Fatalf("replacement connection status = %q, want connecting", second.Connection.Status)
	}
	assertExecutorRuntimeStatus(t, pool, schema, command.ExecutorID, command.Environments[0].ID, ExecutorStatusOffline, ExecutorEnvironmentStatusOffline)
	online, err = store.ListOnlineExecutorEnvironments(t.Context(), ListOnlineExecutorEnvironmentsQuery{WorkspaceID: stateTestUUID(805)})
	if err != nil || len(online) != 0 {
		t.Fatalf("online environments after fresh connecting generation = %+v, %v", online, err)
	}
	if _, err := store.AcquireExecutorConnection(t.Context(), command); !HasStateErrorCode(err, ErrorConnectionFenced) {
		t.Fatalf("superseded exact acquire retry error = %v, want connection_fenced", err)
	}
	assertExecutorConnectionGeneration(t, pool, schema, command.ExecutorID, 2)

	if _, err := store.RenewExecutorConnection(t.Context(), RenewExecutorConnectionCommand{
		ExecutorID:        command.ExecutorID,
		SessionID:         command.SessionID,
		GatewayInstanceID: command.GatewayInstanceID,
		Generation:        first.Connection.Generation,
		LeaseTTL:          command.LeaseTTL,
	}); !HasStateErrorCode(err, ErrorConnectionFenced) {
		t.Fatalf("stale RenewExecutorConnection() error = %v, want connection_fenced", err)
	} else {
		var stateError *StateError
		if !errors.As(err, &stateError) || stateError.CurrentGeneration != 2 {
			t.Fatalf("stale renew state error = %+v, want current generation 2", stateError)
		}
	}

	mismatched := secondCommand
	mismatched.ConnectionID = stateTestUUID(808)
	mismatched.SessionID = stateTestUUID(809)
	mismatched.Environments = cloneExecutorDeclarations(secondCommand.Environments)
	mismatched.Environments[0].CodexSHA256 = sha256.Sum256([]byte("different-codex"))
	if _, err := store.AcquireExecutorConnection(t.Context(), mismatched); !HasStateErrorCode(err, ErrorConnectionFenced) {
		t.Fatalf("build-mismatch AcquireExecutorConnection() error = %v, want connection_fenced", err)
	}
	assertExecutorConnectionGeneration(t, pool, schema, command.ExecutorID, 2)

	fenced, err := store.FenceExecutorConnection(t.Context(), FenceExecutorConnectionCommand{
		ExecutorID:        command.ExecutorID,
		SessionID:         secondCommand.SessionID,
		GatewayInstanceID: secondCommand.GatewayInstanceID,
		Generation:        second.Connection.Generation,
	})
	if err != nil || !fenced {
		t.Fatalf("FenceExecutorConnection() = %v, %v, want true", fenced, err)
	}
	fenced, err = store.FenceExecutorConnection(t.Context(), FenceExecutorConnectionCommand{
		ExecutorID:        command.ExecutorID,
		SessionID:         secondCommand.SessionID,
		GatewayInstanceID: secondCommand.GatewayInstanceID,
		Generation:        second.Connection.Generation,
	})
	if err != nil || fenced {
		t.Fatalf("repeat FenceExecutorConnection() = %v, %v, want no-op", fenced, err)
	}
	assertExecutorRuntimeStatus(t, pool, schema, command.ExecutorID, command.Environments[0].ID, ExecutorStatusOffline, ExecutorEnvironmentStatusOffline)

	if _, err := store.RenewExecutorConnection(t.Context(), RenewExecutorConnectionCommand{
		ExecutorID:        command.ExecutorID,
		SessionID:         secondCommand.SessionID,
		GatewayInstanceID: secondCommand.GatewayInstanceID,
		Generation:        second.Connection.Generation,
		LeaseTTL:          command.LeaseTTL,
	}); !HasStateErrorCode(err, ErrorConnectionFenced) {
		t.Fatalf("fenced RenewExecutorConnection() error = %v, want connection_fenced", err)
	}
}

func TestPostgreSQLExecutorConnectionGenerationIsMonotonicUnderConcurrency(t *testing.T) {
	store, pool, schema := newPostgresStateStore(t)
	base := insertExecutorConnectionFixture(t, pool, schema, 900)
	commands := []AcquireExecutorConnectionCommand{base, base}
	commands[1].ConnectionID = stateTestUUID(906)
	commands[1].SessionID = stateTestUUID(907)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	start := make(chan struct{})
	type outcome struct {
		result AcquireExecutorConnectionResult
		err    error
	}
	outcomes := make(chan outcome, len(commands))
	for _, command := range commands {
		go func(command AcquireExecutorConnectionCommand) {
			<-start
			result, err := store.AcquireExecutorConnection(ctx, command)
			outcomes <- outcome{result: result, err: err}
		}(command)
	}
	close(start)

	generations := make([]int64, 0, len(commands))
	for range commands {
		outcome := <-outcomes
		if outcome.err != nil {
			t.Fatalf("concurrent AcquireExecutorConnection() error = %v", outcome.err)
		}
		if !outcome.result.Acquired {
			t.Fatalf("concurrent acquire did not cross a fresh generation: %+v", outcome.result)
		}
		generations = append(generations, outcome.result.Connection.Generation)
	}
	slices.Sort(generations)
	if !slices.Equal(generations, []int64{1, 2}) {
		t.Fatalf("concurrent generations = %v, want [1 2]", generations)
	}
	assertExecutorConnectionGeneration(t, pool, schema, base.ExecutorID, 2)
}

func TestPostgreSQLExpiredExecutorConnectionCannotBeRevived(t *testing.T) {
	store, pool, schema := newPostgresStateStore(t)
	command := insertExecutorConnectionFixture(t, pool, schema, 1000)
	first, err := store.AcquireExecutorConnection(t.Context(), command)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ActivateExecutorConnection(t.Context(), ActivateExecutorConnectionCommand{
		ExecutorID:        command.ExecutorID,
		SessionID:         command.SessionID,
		GatewayInstanceID: command.GatewayInstanceID,
		Generation:        first.Connection.Generation,
		Environments:      command.Environments,
	}); err != nil {
		t.Fatal(err)
	}
	query := fmt.Sprintf("UPDATE %s.executor_connections SET expires_at = pg_catalog.clock_timestamp() - interval '1 second' WHERE executor_id = $1", quoteIdentifier(schema))
	if _, err := pool.Exec(t.Context(), query, command.ExecutorID); err != nil {
		t.Fatal(err)
	}
	online, err := store.ListOnlineExecutorEnvironments(t.Context(), ListOnlineExecutorEnvironmentsQuery{WorkspaceID: stateTestUUID(1005)})
	if err != nil || len(online) != 0 {
		t.Fatalf("online environments after database-clock lease expiry = %+v, %v", online, err)
	}
	if _, err := store.AcquireExecutorConnection(t.Context(), command); !HasStateErrorCode(err, ErrorConnectionFenced) {
		t.Fatalf("expired exact acquire retry error = %v, want connection_fenced", err)
	}

	fresh := command
	fresh.ConnectionID = stateTestUUID(1006)
	fresh.SessionID = stateTestUUID(1007)
	result, err := store.AcquireExecutorConnection(t.Context(), fresh)
	if err != nil {
		t.Fatalf("fresh acquire after expiry error = %v", err)
	}
	if !result.Acquired || result.Connection.Generation != first.Connection.Generation+1 {
		t.Fatalf("fresh acquire after expiry = %+v", result)
	}
}

func insertExecutorConnectionFixture(t *testing.T, pool *pgxpool.Pool, schema string, seed int) AcquireExecutorConnectionCommand {
	t.Helper()
	command := validAcquireExecutorConnectionCommand()
	command.ExecutorID = stateTestUUID(seed)
	command.ConnectionID = stateTestUUID(seed + 1)
	command.SessionID = stateTestUUID(seed + 2)
	command.GatewayInstanceID = stateTestUUID(seed + 3)
	command.Environments = cloneExecutorDeclarations(command.Environments)
	command.Environments[0].ID = stateTestUUID(seed + 4)
	command.Environments[0].OuterProfileVersion = executorFilesystemReadProfileVersion
	workspaceID := stateTestUUID(seed + 5)

	quotedSchema := quoteIdentifier(schema)
	machineKeySHA256 := sha256.Sum256([]byte("machine-key"))
	if _, err := pool.Exec(t.Context(), fmt.Sprintf("INSERT INTO %s.workspaces (id, status, managed_lark_credential_mode) VALUES ($1, 'active', 'webhook_swap')", quotedSchema), workspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), fmt.Sprintf(`
INSERT INTO %s.executors
    (id, workspace_id, status, machine_key_sha256, agentx_version,
     runtime_manifest_sha256, exec_protocol_source_sha256)
VALUES ($1, $2, 'offline', $3, $4, $5, $6)`, quotedSchema),
		command.ExecutorID,
		workspaceID,
		machineKeySHA256[:],
		command.AgentxVersion,
		command.RuntimeManifestSHA256[:],
		command.ExecProtocolSourceSHA256[:],
	); err != nil {
		t.Fatal(err)
	}
	environment := command.Environments[0]
	ownerPolicySHA256 := sha256.Sum256([]byte("owner-policy"))
	if _, err := pool.Exec(t.Context(), fmt.Sprintf(`
INSERT INTO %s.executor_environments
    (id, executor_id, root_descriptor, owner_policy_sha256,
     platform, codex_release, codex_commit, codex_sha256,
     outer_profile_version, process_methods, insecure_dev, status)
VALUES ($1, $2, '{"kind":"local","root":"/workspace"}'::jsonb, $3,
        $4, $5, $6, $7, $8, $9, $10, 'offline')`, quotedSchema),
		environment.ID,
		command.ExecutorID,
		ownerPolicySHA256[:],
		environment.Platform,
		environment.CodexRelease,
		environment.CodexCommit,
		environment.CodexSHA256[:],
		environment.OuterProfileVersion,
		environment.ProcessMethods,
		environment.InsecureDev,
	); err != nil {
		t.Fatal(err)
	}
	return command
}

func cloneExecutorDeclarations(source []ExecutorEnvironmentDeclaration) []ExecutorEnvironmentDeclaration {
	cloned := append([]ExecutorEnvironmentDeclaration(nil), source...)
	for index := range cloned {
		cloned[index].ProcessMethods = append([]string(nil), cloned[index].ProcessMethods...)
	}
	return cloned
}

func assertExecutorConnectionGeneration(t *testing.T, pool *pgxpool.Pool, schema, executorID string, want int64) {
	t.Helper()
	query := fmt.Sprintf("SELECT generation FROM %s.executor_connections WHERE executor_id = $1", quoteIdentifier(schema))
	var generation int64
	if err := pool.QueryRow(t.Context(), query, executorID).Scan(&generation); err != nil {
		t.Fatal(err)
	}
	if generation != want {
		t.Fatalf("executor connection generation = %d, want %d", generation, want)
	}
}

func assertExecutorRuntimeStatus(t *testing.T, pool *pgxpool.Pool, schema, executorID, environmentID, wantExecutor, wantEnvironment string) {
	t.Helper()
	quotedSchema := quoteIdentifier(schema)
	var executorStatus string
	if err := pool.QueryRow(t.Context(), fmt.Sprintf("SELECT status FROM %s.executors WHERE id = $1", quotedSchema), executorID).Scan(&executorStatus); err != nil {
		t.Fatal(err)
	}
	var environmentStatus string
	if err := pool.QueryRow(t.Context(), fmt.Sprintf("SELECT status FROM %s.executor_environments WHERE id = $1", quotedSchema), environmentID).Scan(&environmentStatus); err != nil {
		t.Fatal(err)
	}
	if executorStatus != wantExecutor || environmentStatus != wantEnvironment {
		t.Fatalf("executor/environment status = %s/%s, want %s/%s", executorStatus, environmentStatus, wantExecutor, wantEnvironment)
	}
}
