package coredb

import (
	"crypto/sha256"
	"fmt"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgreSQLCreateRunIdempotencyAndAtomicity(t *testing.T) {
	store, pool, schema := newPostgresStateStore(t)
	workspaceID := stateTestUUID(1)
	sessionID := stateTestUUID(2)
	insertStateTestSession(t, pool, schema, workspaceID, sessionID)

	command := stateCreateRunCommand(10, workspaceID, sessionID, "create-key")
	result, err := store.CreateRun(t.Context(), command)
	if err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	if !result.Created || result.Run.Status != RunStatusQueued || result.Run.Version != 1 || result.Run.NextEventSeq != 2 || result.SessionVersion != 2 {
		t.Fatalf("CreateRun() result = %+v", result)
	}

	// Simulate losing the response after commit. The same command must return
	// the committed aggregate without creating another event or outbox row.
	retry, err := store.CreateRun(t.Context(), command)
	if err != nil {
		t.Fatalf("retry CreateRun() error = %v", err)
	}
	if retry.Created || retry.Run.ID != result.Run.ID || retry.SessionVersion != 2 {
		t.Fatalf("retry CreateRun() result = %+v", retry)
	}
	assertStateTableCount(t, pool, schema, "runs", 1)
	assertStateTableCount(t, pool, schema, "run_events", 1)
	assertStateTableCount(t, pool, schema, "outbox", 1)

	conflicting := command
	conflicting.RequestHash = sha256.Sum256([]byte("different request"))
	if _, err := store.CreateRun(t.Context(), conflicting); !HasStateErrorCode(err, ErrorIdempotencyConflict) {
		t.Fatalf("conflicting CreateRun() error = %v, want idempotency_conflict", err)
	}

	active := stateCreateRunCommand(20, workspaceID, sessionID, "another-key")
	active.ExpectedSessionVersion = 2
	if _, err := store.CreateRun(t.Context(), active); !HasStateErrorCode(err, ErrorActiveRun) {
		t.Fatalf("active-run CreateRun() error = %v, want active_run", err)
	}

	// A transition-side conflict must roll back the run row, event, and session
	// active_run change together.
	secondSessionID := stateTestUUID(3)
	insertStateTestSessionOnly(t, pool, schema, workspaceID, secondSessionID)
	rollbackCommand := stateCreateRunCommand(30, workspaceID, secondSessionID, "rollback-key")
	preinsertOutbox(t, pool, schema, rollbackCommand.Record.OutboxID, rollbackCommand.RunID)
	if _, err := store.CreateRun(t.Context(), rollbackCommand); !HasStateErrorCode(err, ErrorConflict) {
		t.Fatalf("transition-conflict CreateRun() error = %v, want conflict", err)
	}
	var activeRunID *string
	var sessionVersion int64
	query := fmt.Sprintf("SELECT active_run_id::text, version FROM %s.sessions WHERE id = $1", quoteIdentifier(schema))
	if err := pool.QueryRow(t.Context(), query, secondSessionID).Scan(&activeRunID, &sessionVersion); err != nil {
		t.Fatal(err)
	}
	if activeRunID != nil || sessionVersion != 1 {
		t.Fatalf("rolled-back session active_run_id = %v, version = %d", activeRunID, sessionVersion)
	}
	var rolledBackRunCount int
	query = fmt.Sprintf("SELECT pg_catalog.count(*) FROM %s.runs WHERE id = $1", quoteIdentifier(schema))
	if err := pool.QueryRow(t.Context(), query, rollbackCommand.RunID).Scan(&rolledBackRunCount); err != nil {
		t.Fatal(err)
	}
	if rolledBackRunCount != 0 {
		t.Fatalf("transition conflict left %d run rows", rolledBackRunCount)
	}
}

func TestPostgreSQLConcurrentCreateRunSerializesSession(t *testing.T) {
	store, pool, schema := newPostgresStateStore(t)
	workspaceID := stateTestUUID(100)
	sessionID := stateTestUUID(101)
	insertStateTestSession(t, pool, schema, workspaceID, sessionID)

	commands := []CreateRunCommand{
		stateCreateRunCommand(110, workspaceID, sessionID, "same-key"),
		stateCreateRunCommand(120, workspaceID, sessionID, "same-key"),
	}
	commands[1].RequestHash = commands[0].RequestHash
	commands[1].ActorID = commands[0].ActorID
	results := make(chan CreateRunResult, 2)
	errorsChannel := make(chan error, 2)
	start := make(chan struct{})
	var waitGroup sync.WaitGroup
	for _, command := range commands {
		command := command
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			<-start
			result, err := store.CreateRun(t.Context(), command)
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
			t.Fatalf("concurrent CreateRun() error = %v", err)
		}
	}
	createdCount := 0
	returnedRunIDs := map[string]struct{}{}
	for result := range results {
		if result.Created {
			createdCount++
		}
		returnedRunIDs[result.Run.ID] = struct{}{}
	}
	if createdCount != 1 || len(returnedRunIDs) != 1 {
		t.Fatalf("created count = %d, returned run IDs = %v; want one committed aggregate", createdCount, returnedRunIDs)
	}
	assertStateTableCount(t, pool, schema, "runs", 1)
	assertStateTableCount(t, pool, schema, "run_events", 1)
	assertStateTableCount(t, pool, schema, "outbox", 1)
}

func TestPostgreSQLAttemptLeaseGenerationFencing(t *testing.T) {
	store, pool, schema := newPostgresStateStore(t)
	workspaceID := stateTestUUID(200)
	sessionID := stateTestUUID(201)
	insertStateTestSession(t, pool, schema, workspaceID, sessionID)
	created := mustCreateStateRun(t, store, stateCreateRunCommand(210, workspaceID, sessionID, "lease-key"))

	firstClaimCommand := stateClaimRunCommand(220, created.Run.ID, created.Run.Version, "holder-a")
	firstClaim, err := store.ClaimQueuedRun(t.Context(), firstClaimCommand)
	if err != nil {
		t.Fatalf("ClaimQueuedRun() error = %v", err)
	}
	if !firstClaim.Created || firstClaim.Reclaimed || firstClaim.Run.Status != RunStatusStarting || firstClaim.Run.CurrentAttemptGeneration != 1 || firstClaim.Attempt.Status != AttemptStatusLeased {
		t.Fatalf("first claim = %+v", firstClaim)
	}
	retry, err := store.ClaimQueuedRun(t.Context(), firstClaimCommand)
	if err != nil || retry.Created || retry.Attempt.ID != firstClaim.Attempt.ID {
		t.Fatalf("claim retry = %+v, error = %v", retry, err)
	}

	liveCompeting := stateClaimRunCommand(230, created.Run.ID, firstClaim.Run.Version, "holder-b")
	if _, err := store.ClaimQueuedRun(t.Context(), liveCompeting); !HasStateErrorCode(err, ErrorLeaseHeld) {
		t.Fatalf("live competing claim error = %v, want lease_held", err)
	}

	if _, err := store.RenewSessionLease(t.Context(), RenewSessionLeaseCommand{
		SessionID: sessionID, RunID: created.Run.ID, HolderID: "holder-a", Generation: 1, LeaseTTL: time.Minute,
	}); err != nil {
		t.Fatalf("RenewSessionLease() error = %v", err)
	}
	if _, err := store.RenewAttemptLease(t.Context(), RenewAttemptLeaseCommand{
		RunID: created.Run.ID, AttemptID: firstClaim.Attempt.ID, HolderID: "holder-a", Generation: 1, LeaseTTL: time.Minute,
	}); err != nil {
		t.Fatalf("RenewAttemptLease() error = %v", err)
	}
	if _, err := store.RenewAttemptLease(t.Context(), RenewAttemptLeaseCommand{
		RunID: created.Run.ID, AttemptID: firstClaim.Attempt.ID, HolderID: "holder-b", Generation: 1, LeaseTTL: time.Minute,
	}); !HasStateErrorCode(err, ErrorLeaseLost) {
		t.Fatalf("wrong-holder RenewAttemptLease() error = %v, want lease_lost", err)
	}
	committedBeforeFence := stateAppendEventsCommand(225, created.Run.ID, firstClaim.Attempt.ID, "holder-a", 1)
	committedResult, err := store.AppendAttemptEvents(t.Context(), committedBeforeFence)
	if err != nil || committedResult.NewCount != 1 {
		t.Fatalf("pre-fence AppendAttemptEvents() result = %+v, error = %v", committedResult, err)
	}

	expireSessionLeaseOnly(t, pool, schema, sessionID)
	if _, err := store.RenewAttemptLease(t.Context(), RenewAttemptLeaseCommand{
		RunID: created.Run.ID, AttemptID: firstClaim.Attempt.ID, HolderID: "holder-a", Generation: 1, LeaseTTL: time.Minute,
	}); !HasStateErrorCode(err, ErrorLeaseLost) {
		t.Fatalf("RenewAttemptLease() with expired session lease error = %v, want lease_lost", err)
	}

	expireStateLeases(t, pool, schema, sessionID, firstClaim.Attempt.ID)
	secondClaimCommand := stateClaimRunCommand(240, created.Run.ID, firstClaim.Run.Version, "holder-b")
	secondClaim, err := store.ClaimQueuedRun(t.Context(), secondClaimCommand)
	if err != nil {
		t.Fatalf("reclaim ClaimQueuedRun() error = %v", err)
	}
	if !secondClaim.Created || !secondClaim.Reclaimed || secondClaim.Run.CurrentAttemptGeneration != 2 || secondClaim.Attempt.Generation != 2 {
		t.Fatalf("second claim = %+v", secondClaim)
	}
	var oldStatus string
	query := fmt.Sprintf("SELECT status FROM %s.run_attempts WHERE id = $1", quoteIdentifier(schema))
	if err := pool.QueryRow(t.Context(), query, firstClaim.Attempt.ID).Scan(&oldStatus); err != nil {
		t.Fatal(err)
	}
	if oldStatus != AttemptStatusFenced {
		t.Fatalf("old attempt status = %q, want fenced", oldStatus)
	}
	committedRetry, err := store.AppendAttemptEvents(t.Context(), committedBeforeFence)
	if err != nil || committedRetry.NewCount != 0 || !committedRetry.Events[0].Duplicate || committedRetry.Events[0].RunSeq != committedResult.Events[0].RunSeq {
		t.Fatalf("post-fence exact event retry = %+v, error = %v", committedRetry, err)
	}

	staleEvent := stateAppendEventsCommand(250, created.Run.ID, firstClaim.Attempt.ID, "holder-a", 1)
	if _, err := store.AppendAttemptEvents(t.Context(), staleEvent); !HasStateErrorCode(err, ErrorLeaseLost) {
		t.Fatalf("stale AppendAttemptEvents() error = %v, want lease_lost", err)
	}
	if _, err := store.RenewSessionLease(t.Context(), RenewSessionLeaseCommand{
		SessionID: sessionID, RunID: created.Run.ID, HolderID: "holder-a", Generation: 1, LeaseTTL: time.Minute,
	}); !HasStateErrorCode(err, ErrorLeaseLost) {
		t.Fatalf("stale RenewSessionLease() error = %v, want lease_lost", err)
	}

	acceptedCommand := MarkTurnAcceptedCommand{
		RunID:                  created.Run.ID,
		AttemptID:              secondClaim.Attempt.ID,
		HolderID:               "holder-b",
		Generation:             2,
		ExpectedRunVersion:     secondClaim.Run.Version,
		ExpectedAttemptVersion: secondClaim.Attempt.Version,
		Record:                 stateTransitionRecord(260),
	}
	accepted, err := store.MarkTurnAccepted(t.Context(), acceptedCommand)
	if err != nil {
		t.Fatalf("MarkTurnAccepted() error = %v", err)
	}
	if !accepted.Changed || accepted.Run.Status != RunStatusRunning || accepted.Attempt.Status != AttemptStatusRunning || accepted.Attempt.TurnStartedAt == nil {
		t.Fatalf("accepted result = %+v", accepted)
	}
	wrongHolderAccepted := acceptedCommand
	wrongHolderAccepted.HolderID = "holder-c"
	if _, err := store.MarkTurnAccepted(t.Context(), wrongHolderAccepted); !HasStateErrorCode(err, ErrorLeaseLost) {
		t.Fatalf("wrong-holder MarkTurnAccepted() error = %v, want lease_lost", err)
	}
	acceptedRetry, err := store.MarkTurnAccepted(t.Context(), acceptedCommand)
	if err != nil || acceptedRetry.Changed {
		t.Fatalf("MarkTurnAccepted() retry = %+v, error = %v", acceptedRetry, err)
	}
	expireStateLeases(t, pool, schema, sessionID, secondClaim.Attempt.ID)
	postTurnClaim := stateClaimRunCommand(270, created.Run.ID, accepted.Run.Version, "holder-c")
	if _, err := store.ClaimQueuedRun(t.Context(), postTurnClaim); !HasStateErrorCode(err, ErrorInvalidState) {
		t.Fatalf("post-turn ClaimQueuedRun() error = %v, want invalid_state", err)
	}
}

func TestPostgreSQLAppendAttemptEventsDeduplicatesAndOrders(t *testing.T) {
	store, pool, schema := newPostgresStateStore(t)
	workspaceID := stateTestUUID(300)
	sessionID := stateTestUUID(301)
	insertStateTestSession(t, pool, schema, workspaceID, sessionID)
	created := mustCreateStateRun(t, store, stateCreateRunCommand(310, workspaceID, sessionID, "event-key"))
	claim := mustClaimStateRun(t, store, stateClaimRunCommand(320, created.Run.ID, created.Run.Version, "event-holder"))
	accepted, err := store.MarkTurnAccepted(t.Context(), MarkTurnAcceptedCommand{
		RunID: created.Run.ID, AttemptID: claim.Attempt.ID, HolderID: "event-holder", Generation: 1,
		ExpectedRunVersion: claim.Run.Version, ExpectedAttemptVersion: claim.Attempt.Version,
		Record: stateTransitionRecord(330),
	})
	if err != nil {
		t.Fatal(err)
	}

	appendCommand := stateAppendEventsCommand(340, created.Run.ID, claim.Attempt.ID, "event-holder", 1)
	appendCommand.Events = append(appendCommand.Events, AttemptEvent{
		EventID:            stateTestUUID(345),
		ProducerInstanceID: appendCommand.Events[0].ProducerInstanceID,
		ProducerSeq:        2,
		Source:             EventSourceExecutor,
		Kind:               "tool.output",
		SchemaVersion:      1,
		Object: &ObjectPointer{
			ObjectID:  stateTestUUID(346),
			SHA256:    sha256.Sum256([]byte("object bytes")),
			Size:      12,
			MediaType: "text/plain",
		},
	})
	result, err := store.AppendAttemptEvents(t.Context(), appendCommand)
	if err != nil {
		t.Fatalf("AppendAttemptEvents() error = %v", err)
	}
	if result.NewCount != 2 || result.Events[0].RunSeq != accepted.Run.NextEventSeq || result.Events[1].RunSeq != accepted.Run.NextEventSeq+1 {
		t.Fatalf("append result = %+v, accepted run = %+v", result, accepted.Run)
	}

	retry, err := store.AppendAttemptEvents(t.Context(), appendCommand)
	if err != nil {
		t.Fatalf("retry AppendAttemptEvents() error = %v", err)
	}
	if retry.NewCount != 0 || !retry.Events[0].Duplicate || !retry.Events[1].Duplicate || retry.Events[0].RunSeq != result.Events[0].RunSeq {
		t.Fatalf("retry append result = %+v", retry)
	}
	assertStateTableCount(t, pool, schema, "run_events", 5)
	assertStateTableCount(t, pool, schema, "outbox", 4)

	changed := appendCommand
	changed.Events = append([]AttemptEvent(nil), appendCommand.Events...)
	changed.Events[0].Payload = []byte(`{"changed":true}`)
	if _, err := store.AppendAttemptEvents(t.Context(), changed); !HasStateErrorCode(err, ErrorEventConflict) {
		t.Fatalf("changed duplicate error = %v, want event_conflict", err)
	}

	commands := []AppendAttemptEventsCommand{
		stateAppendEventsCommand(350, created.Run.ID, claim.Attempt.ID, "event-holder", 1),
		stateAppendEventsCommand(360, created.Run.ID, claim.Attempt.ID, "event-holder", 1),
	}
	sequences := make(chan int64, 2)
	errorsChannel := make(chan error, 2)
	var waitGroup sync.WaitGroup
	for _, command := range commands {
		command := command
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			result, err := store.AppendAttemptEvents(t.Context(), command)
			if err == nil {
				sequences <- result.Events[0].RunSeq
			}
			errorsChannel <- err
		}()
	}
	waitGroup.Wait()
	close(sequences)
	close(errorsChannel)
	for err := range errorsChannel {
		if err != nil {
			t.Fatalf("concurrent AppendAttemptEvents() error = %v", err)
		}
	}
	var gotSequences []int64
	for sequence := range sequences {
		gotSequences = append(gotSequences, sequence)
	}
	sort.Slice(gotSequences, func(i, j int) bool { return gotSequences[i] < gotSequences[j] })
	wantSequences := []int64{accepted.Run.NextEventSeq + 2, accepted.Run.NextEventSeq + 3}
	if fmt.Sprint(gotSequences) != fmt.Sprint(wantSequences) {
		t.Fatalf("concurrent run sequences = %v, want %v", gotSequences, wantSequences)
	}
}

func TestPostgreSQLOutboxSkipLockedAndClaimFencing(t *testing.T) {
	store, pool, schema := newPostgresStateStore(t)
	workspaceID := stateTestUUID(400)
	sessionID := stateTestUUID(401)
	insertStateTestSession(t, pool, schema, workspaceID, sessionID)
	created := mustCreateStateRun(t, store, stateCreateRunCommand(410, workspaceID, sessionID, "outbox-key"))
	claim := mustClaimStateRun(t, store, stateClaimRunCommand(420, created.Run.ID, created.Run.Version, "outbox-holder"))
	if _, err := store.MarkTurnAccepted(t.Context(), MarkTurnAcceptedCommand{
		RunID: created.Run.ID, AttemptID: claim.Attempt.ID, HolderID: "outbox-holder", Generation: 1,
		ExpectedRunVersion: claim.Run.Version, ExpectedAttemptVersion: claim.Attempt.Version,
		Record: stateTransitionRecord(430),
	}); err != nil {
		t.Fatal(err)
	}

	first, err := store.ClaimOutbox(t.Context(), ClaimOutboxCommand{Owner: "relay-a", Limit: 1, LockTTL: time.Minute})
	if err != nil || len(first) != 1 || first[0].ClaimGeneration != 1 {
		t.Fatalf("first ClaimOutbox() = %+v, error = %v", first, err)
	}
	second, err := store.ClaimOutbox(t.Context(), ClaimOutboxCommand{Owner: "relay-b", Limit: 10, LockTTL: time.Minute})
	if err != nil || len(second) != 2 {
		t.Fatalf("second ClaimOutbox() = %+v, error = %v", second, err)
	}
	for _, message := range second {
		if message.ID == first[0].ID {
			t.Fatalf("SKIP LOCKED returned already claimed outbox %s", message.ID)
		}
	}

	query := fmt.Sprintf("UPDATE %s.outbox SET lock_until = pg_catalog.clock_timestamp() - interval '1 second' WHERE id = $1", quoteIdentifier(schema))
	if _, err := pool.Exec(t.Context(), query, first[0].ID); err != nil {
		t.Fatal(err)
	}
	reclaimed, err := store.ClaimOutbox(t.Context(), ClaimOutboxCommand{Owner: "relay-c", Limit: 1, LockTTL: time.Minute})
	if err != nil || len(reclaimed) != 1 || reclaimed[0].ID != first[0].ID || reclaimed[0].ClaimGeneration != 2 {
		t.Fatalf("reclaimed ClaimOutbox() = %+v, error = %v", reclaimed, err)
	}
	if _, err := store.CompleteOutbox(t.Context(), CompleteOutboxCommand{
		ID: first[0].ID, Owner: "relay-a", ClaimGeneration: first[0].ClaimGeneration,
	}); !HasStateErrorCode(err, ErrorOutboxClaimLost) {
		t.Fatalf("stale CompleteOutbox() error = %v, want outbox_claim_lost", err)
	}
	released, err := store.ReleaseOutbox(t.Context(), ReleaseOutboxCommand{
		ID: reclaimed[0].ID, Owner: "relay-c", ClaimGeneration: reclaimed[0].ClaimGeneration,
	})
	if err != nil || !released {
		t.Fatalf("ReleaseOutbox() released = %v, error = %v", released, err)
	}
	third, err := store.ClaimOutbox(t.Context(), ClaimOutboxCommand{Owner: "relay-d", Limit: 1, LockTTL: time.Minute})
	if err != nil || len(third) != 1 || third[0].ID != first[0].ID || third[0].ClaimGeneration != 3 {
		t.Fatalf("third ClaimOutbox() = %+v, error = %v", third, err)
	}
	completed, err := store.CompleteOutbox(t.Context(), CompleteOutboxCommand{
		ID: third[0].ID, Owner: "relay-d", ClaimGeneration: third[0].ClaimGeneration,
	})
	if err != nil || !completed {
		t.Fatalf("CompleteOutbox() completed = %v, error = %v", completed, err)
	}
	completed, err = store.CompleteOutbox(t.Context(), CompleteOutboxCommand{
		ID: third[0].ID, Owner: "relay-d", ClaimGeneration: third[0].ClaimGeneration,
	})
	if err != nil || completed {
		t.Fatalf("idempotent CompleteOutbox() completed = %v, error = %v", completed, err)
	}
}

func newPostgresStateStore(t *testing.T) (*StateStore, *pgxpool.Pool, string) {
	t.Helper()
	connectionConfig := postgresIntegrationConfig(t)
	schema := newPostgresTestSchema(t, connectionConfig)
	catalog, err := EmbeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrateConfig(t.Context(), connectionConfig, runnerConfig{
		schema: schema, lockKey: migrationAdvisoryLockKey, catalog: catalog,
	}); err != nil {
		t.Fatalf("migrate state test schema: %v", err)
	}
	poolConfig, err := pgxpool.ParseConfig(os.Getenv("AGENTSERVER_V2_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal("AGENTSERVER_V2_TEST_DATABASE_URL is not a valid PostgreSQL pool configuration")
	}
	pool, err := pgxpool.NewWithConfig(t.Context(), poolConfig)
	if err != nil {
		t.Fatalf("open PostgreSQL state test pool: %v", safeConnectError(connectionConfig, err))
	}
	if err := pool.Ping(t.Context()); err != nil {
		pool.Close()
		t.Fatalf("ping PostgreSQL state test pool: %v", safeConnectError(connectionConfig, err))
	}
	t.Cleanup(pool.Close)
	return newStateStore(pool, schema), pool, schema
}

func insertStateTestSession(t *testing.T, pool *pgxpool.Pool, schema, workspaceID, sessionID string) {
	t.Helper()
	query := fmt.Sprintf("INSERT INTO %s.workspaces (id, status) VALUES ($1, 'active')", quoteIdentifier(schema))
	if _, err := pool.Exec(t.Context(), query, workspaceID); err != nil {
		t.Fatal(err)
	}
	insertStateTestSessionOnly(t, pool, schema, workspaceID, sessionID)
}

func insertStateTestSessionOnly(t *testing.T, pool *pgxpool.Pool, schema, workspaceID, sessionID string) {
	t.Helper()
	query := fmt.Sprintf("INSERT INTO %s.sessions (id, workspace_id) VALUES ($1, $2)", quoteIdentifier(schema))
	if _, err := pool.Exec(t.Context(), query, sessionID, workspaceID); err != nil {
		t.Fatal(err)
	}
}

func stateCreateRunCommand(seed int, workspaceID, sessionID, idempotencyKey string) CreateRunCommand {
	return CreateRunCommand{
		RunID:                  stateTestUUID(seed),
		WorkspaceID:            workspaceID,
		SessionID:              sessionID,
		ActorID:                stateTestUUID(seed + 1),
		RequestHash:            sha256.Sum256([]byte(fmt.Sprintf("request-%d", seed))),
		IdempotencyKey:         idempotencyKey,
		ExpectedSessionVersion: 1,
		Record:                 stateTransitionRecord(seed + 2),
	}
}

func stateClaimRunCommand(seed int, runID string, expectedVersion int64, holderID string) ClaimQueuedRunCommand {
	return ClaimQueuedRunCommand{
		RunID:              runID,
		AttemptID:          stateTestUUID(seed),
		HolderID:           holderID,
		ExpectedRunVersion: expectedVersion,
		LeaseTTL:           time.Minute,
		Record:             stateTransitionRecord(seed + 1),
	}
}

func stateAppendEventsCommand(seed int, runID, attemptID, holderID string, generation int64) AppendAttemptEventsCommand {
	return AppendAttemptEventsCommand{
		RunID:      runID,
		AttemptID:  attemptID,
		HolderID:   holderID,
		Generation: generation,
		OutboxID:   stateTestUUID(seed),
		Events: []AttemptEvent{{
			EventID:            stateTestUUID(seed + 1),
			ProducerInstanceID: stateTestUUID(seed + 2),
			ProducerSeq:        1,
			Source:             EventSourceBrain,
			Kind:               "model.item.completed",
			SchemaVersion:      1,
			Payload:            []byte(`{"text":"done"}`),
		}},
	}
}

func stateTransitionRecord(seed int) TransitionRecord {
	return TransitionRecord{
		EventID:            stateTestUUID(seed),
		ProducerInstanceID: stateTestUUID(seed + 1),
		ProducerSeq:        int64(seed + 1),
		OutboxID:           stateTestUUID(seed + 2),
	}
}

func stateTestUUID(seed int) string {
	return fmt.Sprintf("00000000-0000-4000-8000-%012x", seed)
}

func mustCreateStateRun(t *testing.T, store *StateStore, command CreateRunCommand) CreateRunResult {
	t.Helper()
	result, err := store.CreateRun(t.Context(), command)
	if err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	return result
}

func mustClaimStateRun(t *testing.T, store *StateStore, command ClaimQueuedRunCommand) ClaimQueuedRunResult {
	t.Helper()
	result, err := store.ClaimQueuedRun(t.Context(), command)
	if err != nil {
		t.Fatalf("ClaimQueuedRun() error = %v", err)
	}
	return result
}

func expireStateLeases(t *testing.T, pool *pgxpool.Pool, schema, sessionID, attemptID string) {
	t.Helper()
	expireSessionLeaseOnly(t, pool, schema, sessionID)
	query := fmt.Sprintf("UPDATE %s.attempt_leases SET expires_at = pg_catalog.clock_timestamp() - interval '1 second' WHERE run_attempt_id = $1", quoteIdentifier(schema))
	if _, err := pool.Exec(t.Context(), query, attemptID); err != nil {
		t.Fatal(err)
	}
}

func expireSessionLeaseOnly(t *testing.T, pool *pgxpool.Pool, schema, sessionID string) {
	t.Helper()
	query := fmt.Sprintf("UPDATE %s.session_leases SET expires_at = pg_catalog.clock_timestamp() - interval '1 second' WHERE session_id = $1", quoteIdentifier(schema))
	if _, err := pool.Exec(t.Context(), query, sessionID); err != nil {
		t.Fatal(err)
	}
}

func assertStateTableCount(t *testing.T, pool *pgxpool.Pool, schema, table string, want int) {
	t.Helper()
	query := fmt.Sprintf("SELECT pg_catalog.count(*) FROM %s.%s", quoteIdentifier(schema), quoteIdentifier(table))
	var count int
	if err := pool.QueryRow(t.Context(), query).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("%s.%s row count = %d, want %d", schema, table, count, want)
	}
}

func preinsertOutbox(t *testing.T, pool *pgxpool.Pool, schema, outboxID, aggregateID string) {
	t.Helper()
	query := fmt.Sprintf("INSERT INTO %s.outbox (id, kind, aggregate_id, payload) VALUES ($1, 'test.conflict', $2, '{}'::jsonb)", quoteIdentifier(schema))
	if _, err := pool.Exec(t.Context(), query, outboxID, aggregateID); err != nil {
		t.Fatal(err)
	}
}
