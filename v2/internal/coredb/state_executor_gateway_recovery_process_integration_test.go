//go:build linux || darwin

package coredb

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/executorgateway"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	gatewayRecoveryCrashHelperEnvironment = "AGENTSERVER_V2_GATEWAY_RECOVERY_CRASH_HELPER"
	gatewayRecoveryCrashConfigEnvironment = "AGENTSERVER_V2_GATEWAY_RECOVERY_CRASH_CONFIG"
	gatewayRecoveryCrashNotifyFD          = 3
)

type gatewayRecoveryCrashConfig struct {
	Schema                          string `json:"schema"`
	CrashPoint                      string `json:"crashPoint"`
	SendMarkerPath                  string `json:"sendMarkerPath"`
	RunID                           string `json:"runId"`
	AttemptID                       string `json:"attemptId"`
	HolderID                        string `json:"holderId"`
	ExecutionID                     string `json:"executionId"`
	StartOperationID                string `json:"startOperationId"`
	TimeoutOperationID              string `json:"timeoutOperationId"`
	ProducerInstanceID              string `json:"producerInstanceId"`
	AttemptGeneration               int64  `json:"attemptGeneration"`
	ConnectionGeneration            int64  `json:"connectionGeneration"`
	ExpectedExecutionVersion        int64  `json:"expectedExecutionVersion"`
	ExpectedStartOperationVersion   int64  `json:"expectedStartOperationVersion"`
	ExpectedTimeoutOperationVersion int64  `json:"expectedTimeoutOperationVersion"`
	PolicyContextHashSeed           int    `json:"policyContextHashSeed"`
	OperationPlanHashSeed           int    `json:"operationPlanHashSeed"`
	StartParamsHashSeed             int    `json:"startParamsHashSeed"`
	TransitionIdentitySeed          int    `json:"transitionIdentitySeed"`
	AcknowledgementHashSeed         int    `json:"acknowledgementHashSeed"`
	OperationResultHashSeed         int    `json:"operationResultHashSeed"`
	TimeoutResultHashSeed           int    `json:"timeoutResultHashSeed"`
	ExecutionResultHashSeed         int    `json:"executionResultHashSeed"`
}

func TestPostgreSQLExecutorGatewayRecoveryHardKillMatrix(t *testing.T) {
	tests := []struct {
		crashPoint      string
		executionStatus string
		startStatus     string
		timeoutStatus   string
		recovered       int
		sends           int
	}{
		{"before_begin", ExecutionStatusApproved, OperationStatusPrepared, OperationStatusPrepared, 0, 0},
		{"after_begin", ExecutionStatusUnknown, OperationStatusUnknown, OperationStatusSkipped, 1, 0},
		{"before_wss_send", ExecutionStatusUnknown, OperationStatusUnknown, OperationStatusSkipped, 1, 0},
		{"after_wss_send", ExecutionStatusUnknown, OperationStatusUnknown, OperationStatusSkipped, 1, 1},
		{"before_ack", ExecutionStatusUnknown, OperationStatusUnknown, OperationStatusSkipped, 1, 1},
		{"after_ack", ExecutionStatusUnknown, OperationStatusUnknown, OperationStatusSkipped, 1, 1},
		{"before_operation_terminal", ExecutionStatusUnknown, OperationStatusUnknown, OperationStatusSkipped, 1, 1},
		{"after_operation_terminal", ExecutionStatusSucceeded, OperationStatusSucceeded, OperationStatusSkipped, 1, 1},
		{"before_execution_terminal", ExecutionStatusSucceeded, OperationStatusSucceeded, OperationStatusSkipped, 1, 1},
		{"after_execution_terminal", ExecutionStatusSucceeded, OperationStatusSucceeded, OperationStatusSkipped, 0, 1},
	}
	for index, test := range tests {
		t.Run(test.crashPoint, func(t *testing.T) {
			store, pool, schema := newPostgresStateStore(t)
			seed := 900_000 + index*10_000
			running := startExecutionTestRun(t, store, pool, schema, seed)
			executionSeed := seed + 1_000
			prepared, err := store.PrepareExecution(t.Context(), executionTestPrepareCommand(
				t, executionSeed, running, "gateway-hard-kill-"+test.crashPoint, 2,
			))
			if err != nil {
				t.Fatal(err)
			}
			startSeed := seed + 1_100
			start, err := store.PrepareOperation(t.Context(), executionTestPrepareOperationCommand(
				t, startSeed, running, prepared.Execution, 1,
			))
			if err != nil {
				t.Fatal(err)
			}
			timeoutSeed := seed + 1_200
			timeoutCommand := executionTestPrepareOperationCommand(t, timeoutSeed, running, start.Execution, 2)
			timeoutCommand.Kind = OperationKindTimeoutTerminate
			timeout, err := store.PrepareOperation(t.Context(), timeoutCommand)
			if err != nil {
				t.Fatal(err)
			}

			connectionGeneration := int64(71 + index)
			connectionSeed := seed + 2_000
			installExecutionTestConnection(t, pool, schema, running, timeout.Execution, connectionGeneration, connectionSeed)
			oldGatewayID := stateTestUUID(connectionSeed + 2)

			controlCommand := executionTestPrepareCommand(t, seed+2_100, running, "gateway-hard-kill-stale-control", 1)
			controlCommand.ExecutorID = timeout.Execution.ExecutorID
			controlCommand.EnvID = timeout.Execution.EnvID
			controlExecution, err := store.PrepareExecution(t.Context(), controlCommand)
			if err != nil {
				t.Fatal(err)
			}
			controlOperation, err := store.PrepareOperation(t.Context(), executionTestPrepareOperationCommand(
				t, seed+2_200, running, controlExecution.Execution, 1,
			))
			if err != nil {
				t.Fatal(err)
			}

			config := gatewayRecoveryCrashConfig{
				Schema: schema, CrashPoint: test.crashPoint,
				SendMarkerPath:                  filepath.Join(t.TempDir(), "wss-send.log"),
				RunID:                           running.Run.ID,
				AttemptID:                       running.Attempt.ID,
				HolderID:                        running.Attempt.HolderID,
				ExecutionID:                     timeout.Execution.ID,
				StartOperationID:                start.Operation.ID,
				TimeoutOperationID:              timeout.Operation.ID,
				ProducerInstanceID:              oldGatewayID,
				AttemptGeneration:               running.Attempt.Generation,
				ConnectionGeneration:            connectionGeneration,
				ExpectedExecutionVersion:        timeout.Execution.Version,
				ExpectedStartOperationVersion:   start.Operation.Version,
				ExpectedTimeoutOperationVersion: timeout.Operation.Version,
				PolicyContextHashSeed:           executionSeed + 103,
				OperationPlanHashSeed:           executionSeed + 102,
				StartParamsHashSeed:             startSeed + 100,
				TransitionIdentitySeed:          seed + 3_000,
				AcknowledgementHashSeed:         seed + 3_100,
				OperationResultHashSeed:         seed + 3_200,
				TimeoutResultHashSeed:           seed + 3_300,
				ExecutionResultHashSeed:         seed + 3_400,
			}
			runGatewayRecoveryCrashHelper(t, config)
			if got := countGatewayRecoverySendMarkers(t, config.SendMarkerPath); got != test.sends {
				t.Fatalf("irreversible WSS send markers after crash = %d, want %d", got, test.sends)
			}

			recoveringGatewayID := stateTestUUID(seed + 4_000)
			summary := recoverGatewayAfterHardKill(t, store, timeout.Execution.ExecutorID, recoveringGatewayID, seed+4_100)
			if summary.FencedConnectionGeneration != connectionGeneration || !summary.ConnectionFenced ||
				summary.RecoveredExecutions != test.recovered || summary.Passes != 1 {
				t.Fatalf("startup recovery summary = %+v", summary)
			}
			assertGatewayRecoveryExecutionStatus(t, pool, schema, timeout.Execution.ID, test.executionStatus)
			assertGatewayRecoveryOperationStatus(t, pool, schema, start.Operation.ID, test.startStatus)
			assertGatewayRecoveryOperationStatus(t, pool, schema, timeout.Operation.ID, test.timeoutStatus)
			assertGatewayRecoveryExecutionStatus(t, pool, schema, controlExecution.Execution.ID, ExecutionStatusApproved)
			assertGatewayRecoveryOperationStatus(t, pool, schema, controlOperation.Operation.ID, OperationStatusPrepared)
			assertGatewayRecoveryConnectionFenced(t, pool, schema, timeout.Execution.ExecutorID, connectionGeneration)

			lateBegin := executionTestBeginCommand(t, seed+4_500, running, controlOperation, connectionGeneration)
			if _, err := store.BeginOperationDispatch(t.Context(), lateBegin); !HasStateErrorCode(err, ErrorConnectionFenced) {
				t.Fatalf("old-generation BeginOperationDispatch() error = %v, want connection_fenced", err)
			}
			assertGatewayRecoveryEventCount(t, pool, schema, recoveringGatewayID, test.recovered)

			retryGatewayID := stateTestUUID(seed + 5_000)
			retry := recoverGatewayAfterHardKill(t, store, timeout.Execution.ExecutorID, retryGatewayID, seed+5_100)
			if retry.FencedConnectionGeneration != connectionGeneration || retry.ConnectionFenced ||
				retry.RecoveredExecutions != 0 || retry.Passes != 1 {
				t.Fatalf("fresh-process recovery retry summary = %+v", retry)
			}
			assertGatewayRecoveryEventCount(t, pool, schema, recoveringGatewayID, test.recovered)
			assertGatewayRecoveryEventCount(t, pool, schema, retryGatewayID, 0)
			if got := countGatewayRecoverySendMarkers(t, config.SendMarkerPath); got != test.sends {
				t.Fatalf("WSS send markers after two recovery startups = %d, want %d", got, test.sends)
			}
		})
	}
}

// TestPostgreSQLExecutorGatewayRecoveryCrashHelper is run only as a child test
// process. The parent kills it after an exact observable boundary; reaching a
// barrier always means the preceding PostgreSQL command has returned from its
// commit. The append+fsync marker stands in for the irreversible WSS send and
// is deliberately outside the database transaction.
func TestPostgreSQLExecutorGatewayRecoveryCrashHelper(t *testing.T) {
	if os.Getenv(gatewayRecoveryCrashHelperEnvironment) != "1" {
		t.Skip("gateway recovery crash helper runs only as a child process")
	}
	encoded := os.Getenv(gatewayRecoveryCrashConfigEnvironment)
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal("decode crash helper configuration")
	}
	var config gatewayRecoveryCrashConfig
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal("parse crash helper configuration")
	}
	if !schemaNamePattern.MatchString(config.Schema) || config.CrashPoint == "" || config.SendMarkerPath == "" {
		t.Fatal("invalid crash helper configuration")
	}
	notify := os.NewFile(uintptr(gatewayRecoveryCrashNotifyFD), "gateway-recovery-crash-notify")
	if notify == nil {
		t.Fatal("open crash helper notification pipe")
	}
	defer notify.Close()
	crashAt := func(point string) {
		if config.CrashPoint != point {
			return
		}
		if _, err := fmt.Fprintln(notify, point); err != nil {
			t.Fatalf("notify crash boundary %s: %v", point, err)
		}
		if err := notify.Close(); err != nil {
			t.Fatalf("close crash notification pipe: %v", err)
		}
		select {}
	}

	databaseURL := os.Getenv("AGENTSERVER_V2_TEST_DATABASE_URL")
	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal("AGENTSERVER_V2_TEST_DATABASE_URL is not a valid PostgreSQL pool configuration")
	}
	pool, err := pgxpool.NewWithConfig(t.Context(), poolConfig)
	if err != nil {
		t.Fatal("open crash helper PostgreSQL pool")
	}
	defer pool.Close()
	if err := pool.Ping(t.Context()); err != nil {
		t.Fatal("ping crash helper PostgreSQL pool")
	}
	store := newStateStore(pool, config.Schema)

	crashAt("before_begin")
	begin, err := store.BeginOperationDispatch(t.Context(), BeginOperationDispatchCommand{
		OperationID: config.StartOperationID, ExecutionID: config.ExecutionID,
		RunID: config.RunID, AttemptID: config.AttemptID, HolderID: config.HolderID,
		Generation: config.AttemptGeneration, ConnectionGeneration: config.ConnectionGeneration,
		ExpectedExecutionVersion: config.ExpectedExecutionVersion,
		ExpectedOperationVersion: config.ExpectedStartOperationVersion,
		PolicyContextHash:        executionTestHash(t, HashDomainPolicyContext, config.PolicyContextHashSeed),
		OperationPlanHash:        executionTestHash(t, HashDomainOperationPlan, config.OperationPlanHashSeed),
		ParamsHash:               executionTestHash(t, HashDomainOperationParams, config.StartParamsHashSeed),
		Record:                   gatewayRecoveryCrashTransition(config, 1),
	})
	if err != nil || !begin.Began {
		t.Fatalf("begin crash helper dispatch = %+v, %v", begin, err)
	}
	crashAt("after_begin")
	crashAt("before_wss_send")
	appendGatewayRecoverySendMarker(t, config.SendMarkerPath)
	crashAt("after_wss_send")
	crashAt("before_ack")

	acknowledged, err := store.AcknowledgeOperation(t.Context(), AcknowledgeOperationCommand{
		OperationID: config.StartOperationID, ExecutionID: config.ExecutionID,
		RunID: config.RunID, AttemptID: config.AttemptID, Generation: config.AttemptGeneration,
		ConnectionGeneration:     config.ConnectionGeneration,
		ExpectedExecutionVersion: begin.Execution.Version,
		ExpectedOperationVersion: begin.Operation.Version,
		AcknowledgementHash:      executionTestHash(t, HashDomainOperationAck, config.AcknowledgementHashSeed),
		Record:                   gatewayRecoveryCrashTransition(config, 2),
	})
	if err != nil {
		t.Fatal(err)
	}
	crashAt("after_ack")
	crashAt("before_operation_terminal")

	completedOperation, err := store.CompleteOperation(t.Context(), CompleteOperationCommand{
		OperationID: config.StartOperationID, ExecutionID: config.ExecutionID,
		RunID: config.RunID, AttemptID: config.AttemptID, Generation: config.AttemptGeneration,
		ConnectionGeneration:     config.ConnectionGeneration,
		ExpectedExecutionVersion: acknowledged.Execution.Version,
		ExpectedOperationVersion: acknowledged.Operation.Version,
		TerminalStatus:           OperationStatusSucceeded,
		ResultHash:               executionTestHash(t, HashDomainOperationResult, config.OperationResultHashSeed),
		Record:                   gatewayRecoveryCrashTransition(config, 3),
	})
	if err != nil {
		t.Fatal(err)
	}
	crashAt("after_operation_terminal")

	skipped, err := store.SkipOperation(t.Context(), SkipOperationCommand{
		OperationID: config.TimeoutOperationID, ExecutionID: config.ExecutionID,
		RunID: config.RunID, AttemptID: config.AttemptID, HolderID: config.HolderID,
		Generation:               config.AttemptGeneration,
		ExpectedExecutionVersion: completedOperation.Execution.Version,
		ExpectedOperationVersion: config.ExpectedTimeoutOperationVersion,
		ResultHash:               executionTestHash(t, HashDomainOperationResult, config.TimeoutResultHashSeed),
		Record:                   gatewayRecoveryCrashTransition(config, 4),
	})
	if err != nil {
		t.Fatal(err)
	}
	crashAt("before_execution_terminal")

	completedExecution, err := store.CompleteExecution(t.Context(), CompleteExecutionCommand{
		ExecutionID: config.ExecutionID, RunID: config.RunID, AttemptID: config.AttemptID,
		Generation: config.AttemptGeneration, ExpectedExecutionVersion: skipped.Execution.Version,
		TerminalStatus: ExecutionStatusSucceeded,
		ResultHash:     executionTestHash(t, HashDomainExecutionResult, config.ExecutionResultHashSeed),
		Record:         gatewayRecoveryCrashTransition(config, 5),
	})
	if err != nil || completedExecution.Execution.Status != ExecutionStatusSucceeded {
		t.Fatalf("complete crash helper execution = %+v, %v", completedExecution, err)
	}
	crashAt("after_execution_terminal")
	t.Fatalf("crash helper did not stop at configured point %q", config.CrashPoint)
}

func runGatewayRecoveryCrashHelper(t *testing.T, config gatewayRecoveryCrashConfig) {
	t.Helper()
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer readPipe.Close()
	command := exec.Command(os.Args[0], "-test.run=^TestPostgreSQLExecutorGatewayRecoveryCrashHelper$", "-test.v")
	command.Env = append(os.Environ(),
		gatewayRecoveryCrashHelperEnvironment+"=1",
		gatewayRecoveryCrashConfigEnvironment+"="+base64.RawURLEncoding.EncodeToString(raw),
	)
	command.ExtraFiles = []*os.File{writePipe}
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Start(); err != nil {
		writePipe.Close()
		t.Fatal(err)
	}
	if err := writePipe.Close(); err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		t.Fatal(err)
	}
	waited := false
	defer func() {
		if waited {
			return
		}
		_ = command.Process.Kill()
		_ = command.Wait()
	}()
	type notification struct {
		point string
		err   error
	}
	notifications := make(chan notification, 1)
	go func() {
		line, readErr := bufio.NewReader(readPipe).ReadString('\n')
		notifications <- notification{point: strings.TrimSpace(line), err: readErr}
	}()
	select {
	case received := <-notifications:
		if received.err != nil || received.point != config.CrashPoint {
			_ = command.Process.Kill()
			_ = command.Wait()
			waited = true
			t.Fatalf("crash helper boundary = %q, %v; output:\n%s", received.point, received.err, output.String())
		}
	case <-time.After(10 * time.Second):
		_ = command.Process.Kill()
		_ = command.Wait()
		waited = true
		t.Fatalf("crash helper did not reach %q; output:\n%s", config.CrashPoint, output.String())
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	waitErr := command.Wait()
	waited = true
	var exitErr *exec.ExitError
	if waitErr == nil || !errors.As(waitErr, &exitErr) || exitErr.Success() {
		t.Fatalf("hard-killed crash helper wait error = %v; output:\n%s", waitErr, output.String())
	}
}

func gatewayRecoveryCrashTransition(config gatewayRecoveryCrashConfig, sequence int64) TransitionRecord {
	seed := config.TransitionIdentitySeed + int(sequence)*10
	return TransitionRecord{
		EventID: stateTestUUID(seed), ProducerInstanceID: config.ProducerInstanceID,
		ProducerSeq: sequence, OutboxID: stateTestUUID(seed + 1),
	}
}

func appendGatewayRecoverySendMarker(t *testing.T, path string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("process/start\n"); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func countGatewayRecoverySendMarkers(t *testing.T, path string) int {
	t.Helper()
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) == 0 || raw[len(raw)-1] != '\n' {
		t.Fatalf("invalid WSS send marker contents %q", raw)
	}
	return bytes.Count(raw, []byte("process/start\n"))
}

type stateStoreGatewayRecoveryAuthority struct {
	store *StateStore
}

func (authority stateStoreGatewayRecoveryAuthority) RecoverExecutorGateway(
	ctx context.Context,
	request executorgateway.RecoverGatewayRequest,
) (executorgateway.RecoverGatewayResult, error) {
	records := make([]TransitionRecord, len(request.Records))
	for index, record := range request.Records {
		records[index] = TransitionRecord{
			EventID: record.EventID, ProducerInstanceID: record.ProducerInstanceID,
			ProducerSeq: record.ProducerSeq, OutboxID: record.OutboxID,
		}
	}
	result, err := authority.store.RecoverExecutorGateway(ctx, RecoverExecutorGatewayCommand{
		ExecutorID: request.ExecutorID, GatewayInstanceID: request.GatewayInstanceID, Records: records,
	})
	if err != nil {
		return executorgateway.RecoverGatewayResult{}, err
	}
	return executorgateway.RecoverGatewayResult{
		FencedConnectionGeneration: result.FencedConnectionGeneration,
		ConnectionFenced:           result.ConnectionFenced,
		RecoveredExecutions:        result.RecoveredExecutions,
		Remaining:                  result.Remaining,
	}, nil
}

func recoverGatewayAfterHardKill(
	t *testing.T,
	store *StateStore,
	executorID, gatewayID string,
	identitySeed int,
) executorgateway.GatewayStartupRecoveryResult {
	t.Helper()
	nextIdentity := 0
	transitions, err := executorgateway.NewExecutionTransitionAllocator(gatewayID, func() (string, error) {
		nextIdentity++
		return stateTestUUID(identitySeed + nextIdentity), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := executorgateway.RecoverGatewayStartup(
		t.Context(), stateStoreGatewayRecoveryAuthority{store: store}, executorID, gatewayID, transitions,
	)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func assertGatewayRecoveryEventCount(t *testing.T, pool *pgxpool.Pool, schema, producerID string, want int) {
	t.Helper()
	query := fmt.Sprintf("SELECT pg_catalog.count(*) FROM %s.run_events WHERE producer_instance_id = $1", quoteIdentifier(schema))
	var count int
	if err := pool.QueryRow(t.Context(), query, producerID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("gateway recovery event count for %s = %d, want %d", producerID, count, want)
	}
}
