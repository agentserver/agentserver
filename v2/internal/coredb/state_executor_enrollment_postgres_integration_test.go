package coredb

import (
	"context"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestPostgreSQLExecutorEnrollmentIsIdempotentKeyBoundAndRevocable(t *testing.T) {
	store, pool, schema := newPostgresStateStore(t)
	workspaceID := stateTestUUID(210_000)
	ownerID := stateTestUUID(210_001)
	developerID := stateTestUUID(210_002)
	executorID := stateTestUUID(210_003)
	environmentID := stateTestUUID(210_004)
	quotedSchema := quoteIdentifier(schema)
	if _, err := pool.Exec(t.Context(), fmt.Sprintf("INSERT INTO %s.workspaces (id, status) VALUES ($1, 'active')", quotedSchema), workspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), fmt.Sprintf("INSERT INTO %s.users (id, status) VALUES ($1, 'active'), ($2, 'active')", quotedSchema), ownerID, developerID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), fmt.Sprintf("INSERT INTO %s.workspace_members (workspace_id, user_id, role) VALUES ($1, $2, 'owner'), ($1, $3, 'developer')", quotedSchema), workspaceID, ownerID, developerID); err != nil {
		t.Fatal(err)
	}

	created, err := store.CreateExecutorResource(t.Context(), CreateExecutorResourceCommand{ExecutorID: executorID, WorkspaceID: workspaceID, ActorID: ownerID})
	if err != nil || !created.Created || created.Executor.Status != ExecutorStatusEnrolling {
		t.Fatalf("create executor = %+v, %v", created, err)
	}
	retry, err := store.CreateExecutorResource(t.Context(), CreateExecutorResourceCommand{ExecutorID: executorID, WorkspaceID: workspaceID, ActorID: ownerID})
	if err != nil || retry.Created || retry.Executor.ID != executorID {
		t.Fatalf("create executor retry = %+v, %v", retry, err)
	}
	if _, err := store.IssueExecutorEnrollmentToken(t.Context(), IssueExecutorEnrollmentTokenCommand{
		TokenID: stateTestUUID(210_005), WorkspaceID: workspaceID, ExecutorID: executorID,
		ActorID: developerID, IdempotencyKey: "developer-token", TTL: 10 * time.Minute,
	}); !HasStateErrorCode(err, ErrorForbidden) {
		t.Fatalf("developer token issue error = %v", err)
	}

	issueCommand := IssueExecutorEnrollmentTokenCommand{
		TokenID: stateTestUUID(210_006), WorkspaceID: workspaceID, ExecutorID: executorID,
		ActorID: ownerID, IdempotencyKey: "owner-token", TTL: 10 * time.Minute,
	}
	issued, err := store.IssueExecutorEnrollmentToken(t.Context(), issueCommand)
	if err != nil || !issued.Created || issued.Token.ID != issueCommand.TokenID || issued.Token.ExpiresAt.Sub(issued.Token.IssuedAt) != 10*time.Minute {
		t.Fatalf("issue token = %+v, %v", issued, err)
	}
	issuedRetry, err := store.IssueExecutorEnrollmentToken(t.Context(), issueCommand)
	if err != nil || issuedRetry.Created || issuedRetry.Token.ID != issued.Token.ID || !issuedRetry.Token.IssuedAt.Equal(issued.Token.IssuedAt) {
		t.Fatalf("issue token retry = %+v, %v", issuedRetry, err)
	}

	publicKey := [32]byte{1, 2, 3, 4}
	machineHash := sha256.Sum256(publicKey[:])
	curve := elliptic.P256()
	oauthPointX, oauthPointY := curve.ScalarBaseMult(big.NewInt(91).Bytes())
	var oauthX, oauthY [32]byte
	copy(oauthX[:], oauthPointX.FillBytes(make([]byte, 32)))
	copy(oauthY[:], oauthPointY.FillBytes(make([]byte, 32)))
	oauthHash := sha256.Sum256([]byte(`{"crv":"P-256","kty":"EC","x":"` +
		base64.RawURLEncoding.EncodeToString(oauthX[:]) + `","y":"` + base64.RawURLEncoding.EncodeToString(oauthY[:]) + `"}`))
	runtimeHash := sha256.Sum256([]byte("runtime"))
	protocolHash := sha256.Sum256([]byte("protocol"))
	enrollmentHash := sha256.Sum256([]byte("enrollment"))
	codexHash := sha256.Sum256([]byte("codex"))
	policyHash := sha256.Sum256([]byte("owner-policy"))
	claim := ClaimExecutorEnrollmentCommand{
		TokenID: issued.Token.ID, WorkspaceID: workspaceID, ExecutorID: executorID, IssuedByActorID: ownerID,
		IssuedAt: issued.Token.IssuedAt, ExpiresAt: issued.Token.ExpiresAt,
		MachinePublicKeyEd25519: publicKey, MachineKeySHA256: machineHash,
		OAuthPublicKeyP256X: oauthX, OAuthPublicKeyP256Y: oauthY, OAuthKeySHA256: oauthHash,
		OAuthClientID: "agentserver-executor-" + executorID, AgentxVersion: "0.1.0",
		RuntimeManifestSHA256: runtimeHash, ExecProtocolSourceSHA256: protocolHash,
		EnrollmentRequestSHA256: enrollmentHash,
		Environments: []ExecutorEnrollmentEnvironment{{
			ExecutorEnvironmentDeclaration: ExecutorEnvironmentDeclaration{
				ID: environmentID, Platform: "linux-amd64", CodexRelease: "0.146.0", CodexCommit: strings.Repeat("a", 40),
				CodexSHA256: codexHash, OuterProfileVersion: executorProcessProfileVersion,
				ProcessMethods: append([]string(nil), executorProcessMethods...),
			},
			RootDescriptor: json.RawMessage(`{"kind":"local","root":"/workspace"}`), OwnerPolicySHA256: policyHash,
		}},
	}
	reservation, err := store.ClaimExecutorEnrollment(t.Context(), claim)
	if err != nil || !reservation.Created || reservation.Executor.Status != ExecutorStatusEnrolling {
		t.Fatalf("claim enrollment = %+v, %v", reservation, err)
	}
	claimRetry, err := store.ClaimExecutorEnrollment(t.Context(), claim)
	if err != nil || claimRetry.Created || claimRetry.OAuthClientID != claim.OAuthClientID {
		t.Fatalf("claim enrollment retry = %+v, %v", claimRetry, err)
	}
	conflicting := claim
	conflicting.EnrollmentRequestSHA256 = sha256.Sum256([]byte("different enrollment"))
	if _, err := store.ClaimExecutorEnrollment(t.Context(), conflicting); !HasStateErrorCode(err, ErrorIdempotencyConflict) {
		t.Fatalf("conflicting enrollment claim error = %v", err)
	}
	if _, err := store.AuthorizeExecutorOAuthClient(t.Context(), claim.OAuthClientID); !HasStateErrorCode(err, ErrorForbidden) {
		t.Fatalf("pre-completion OAuth authorization error = %v", err)
	}
	replacement, err := store.IssueExecutorEnrollmentToken(t.Context(), IssueExecutorEnrollmentTokenCommand{
		TokenID: stateTestUUID(210_007), WorkspaceID: workspaceID, ExecutorID: executorID,
		ActorID: ownerID, IdempotencyKey: "owner-token-replacement", TTL: 10 * time.Minute,
	})
	if err != nil || !replacement.Created {
		t.Fatalf("replacement enrollment token = %+v, %v", replacement, err)
	}
	if _, err := store.ClaimExecutorEnrollment(t.Context(), claim); !HasStateErrorCode(err, ErrorForbidden) {
		t.Fatalf("revoked original token claim error = %v", err)
	}
	if _, err := store.IssueExecutorEnrollmentToken(t.Context(), issueCommand); !HasStateErrorCode(err, ErrorIdempotencyConflict) {
		t.Fatalf("superseded token idempotency error = %v", err)
	}
	replacementClaim := claim
	replacementClaim.TokenID = replacement.Token.ID
	replacementClaim.IssuedAt = replacement.Token.IssuedAt
	replacementClaim.ExpiresAt = replacement.Token.ExpiresAt
	recovered, err := store.ClaimExecutorEnrollment(t.Context(), replacementClaim)
	if err != nil || recovered.Created || recovered.OAuthClientID != claim.OAuthClientID {
		t.Fatalf("replacement token resumed frozen enrollment = %+v, %v", recovered, err)
	}

	completed, err := store.CompleteExecutorEnrollment(t.Context(), CompleteExecutorEnrollmentCommand{
		TokenID: replacement.Token.ID, WorkspaceID: workspaceID, ExecutorID: executorID, EnrollmentRequestSHA256: enrollmentHash,
	})
	if err != nil || completed.Status != ExecutorStatusOffline {
		t.Fatalf("complete enrollment = %+v, %v", completed, err)
	}
	completedRetry, err := store.CompleteExecutorEnrollment(t.Context(), CompleteExecutorEnrollmentCommand{
		TokenID: replacement.Token.ID, WorkspaceID: workspaceID, ExecutorID: executorID, EnrollmentRequestSHA256: enrollmentHash,
	})
	if err != nil || completedRetry.Status != ExecutorStatusOffline || completedRetry.Version != completed.Version {
		t.Fatalf("complete enrollment retry = %+v, %v", completedRetry, err)
	}
	authority, err := store.AuthorizeExecutorOAuthClient(t.Context(), claim.OAuthClientID)
	if err != nil || authority.ExecutorID != executorID || authority.WorkspaceID != workspaceID ||
		authority.MachinePublicKeyEd25519 != publicKey || authority.MachineKeySHA256 != machineHash ||
		authority.OAuthPublicKeyP256X != oauthX || authority.OAuthPublicKeyP256Y != oauthY || authority.OAuthKeySHA256 != oauthHash {
		t.Fatalf("machine OAuth authority = %+v, %v", authority, err)
	}

	if _, err := pool.Exec(t.Context(), fmt.Sprintf("UPDATE %s.executors SET status = 'revoked', version = version + 1 WHERE id = $1", quotedSchema), executorID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AuthorizeExecutorOAuthClient(t.Context(), claim.OAuthClientID); !HasStateErrorCode(err, ErrorForbidden) {
		t.Fatalf("revoked OAuth authorization error = %v", err)
	}
}

func TestPostgreSQLExecutorEnrollmentUsesOneLockOrderAcrossIssueAndClaim(t *testing.T) {
	store, pool, schema := newPostgresStateStore(t)
	workspaceID := stateTestUUID(211_000)
	ownerID := stateTestUUID(211_001)
	executorID := stateTestUUID(211_002)
	environmentID := stateTestUUID(211_003)
	quotedSchema := quoteIdentifier(schema)
	if _, err := pool.Exec(t.Context(), fmt.Sprintf("INSERT INTO %s.workspaces (id, status) VALUES ($1, 'active')", quotedSchema), workspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), fmt.Sprintf("INSERT INTO %s.users (id, status) VALUES ($1, 'active')", quotedSchema), ownerID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), fmt.Sprintf("INSERT INTO %s.workspace_members (workspace_id, user_id, role) VALUES ($1, $2, 'owner')", quotedSchema), workspaceID, ownerID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateExecutorResource(t.Context(), CreateExecutorResourceCommand{
		ExecutorID: executorID, WorkspaceID: workspaceID, ActorID: ownerID,
	}); err != nil {
		t.Fatal(err)
	}
	issued, err := store.IssueExecutorEnrollmentToken(t.Context(), IssueExecutorEnrollmentTokenCommand{
		TokenID: stateTestUUID(211_004), WorkspaceID: workspaceID, ExecutorID: executorID,
		ActorID: ownerID, IdempotencyKey: "initial", TTL: 10 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}

	publicKey := [32]byte{1, 2, 3, 4}
	machineHash := sha256.Sum256(publicKey[:])
	oauthPointX, oauthPointY := elliptic.P256().ScalarBaseMult(big.NewInt(92).Bytes())
	var oauthX, oauthY [32]byte
	oauthPointX.FillBytes(oauthX[:])
	oauthPointY.FillBytes(oauthY[:])
	oauthHash := sha256.Sum256([]byte(`{"crv":"P-256","kty":"EC","x":"` +
		base64.RawURLEncoding.EncodeToString(oauthX[:]) + `","y":"` + base64.RawURLEncoding.EncodeToString(oauthY[:]) + `"}`))
	claim := ClaimExecutorEnrollmentCommand{
		TokenID: issued.Token.ID, WorkspaceID: workspaceID, ExecutorID: executorID, IssuedByActorID: ownerID,
		IssuedAt: issued.Token.IssuedAt, ExpiresAt: issued.Token.ExpiresAt,
		MachinePublicKeyEd25519: publicKey, MachineKeySHA256: machineHash,
		OAuthPublicKeyP256X: oauthX, OAuthPublicKeyP256Y: oauthY, OAuthKeySHA256: oauthHash,
		OAuthClientID: "agentserver-executor-" + executorID, AgentxVersion: "0.1.0",
		RuntimeManifestSHA256:    sha256.Sum256([]byte("runtime-lock-order")),
		ExecProtocolSourceSHA256: sha256.Sum256([]byte("protocol-lock-order")),
		EnrollmentRequestSHA256:  sha256.Sum256([]byte("enrollment-lock-order")),
		Environments: []ExecutorEnrollmentEnvironment{{
			ExecutorEnvironmentDeclaration: ExecutorEnvironmentDeclaration{
				ID: environmentID, Platform: "linux-amd64", CodexRelease: "0.146.0", CodexCommit: strings.Repeat("b", 40),
				CodexSHA256: sha256.Sum256([]byte("codex-lock-order")), OuterProfileVersion: executorProcessProfileVersion,
				ProcessMethods: append([]string(nil), executorProcessMethods...),
			},
			RootDescriptor:    json.RawMessage(`{"kind":"local","root":"/workspace"}`),
			OwnerPolicySHA256: sha256.Sum256([]byte("owner-policy-lock-order")),
		}},
	}

	blocker, err := pool.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Release()
	blockerTx, err := blocker.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer blockerTx.Rollback(t.Context()) //nolint:errcheck -- cleanup after the explicit commit is intentionally idempotent
	if _, err := blockerTx.Exec(t.Context(), fmt.Sprintf("SELECT id FROM %s.executors WHERE id = $1 FOR UPDATE", quotedSchema), executorID); err != nil {
		t.Fatal(err)
	}

	issueConnection, err := pool.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer issueConnection.Release()
	claimConnection, err := pool.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer claimConnection.Release()
	issueStore := newStateStore(issueConnection.Conn(), schema)
	claimStore := newStateStore(claimConnection.Conn(), schema)
	issueResult := make(chan error, 1)
	claimResult := make(chan error, 1)
	go func() {
		_, commandErr := issueStore.IssueExecutorEnrollmentToken(t.Context(), IssueExecutorEnrollmentTokenCommand{
			TokenID: stateTestUUID(211_005), WorkspaceID: workspaceID, ExecutorID: executorID,
			ActorID: ownerID, IdempotencyKey: "replacement", TTL: 10 * time.Minute,
		})
		issueResult <- commandErr
	}()
	waitForPostgreSQLEnrollmentLockWait(t, pool, issueConnection.Conn().PgConn().PID())
	go func() {
		_, commandErr := claimStore.ClaimExecutorEnrollment(t.Context(), claim)
		claimResult <- commandErr
	}()
	waitForPostgreSQLEnrollmentLockWait(t, pool, claimConnection.Conn().PgConn().PID())
	if err := blockerTx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-issueResult:
		if err != nil {
			t.Fatalf("replacement token issue after lock contention: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("replacement token issue did not finish after releasing executor lock")
	}
	select {
	case err := <-claimResult:
		if err != nil && !HasStateErrorCode(err, ErrorForbidden) {
			t.Fatalf("contending enrollment claim returned a database/deadlock error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("contending enrollment claim did not finish after releasing executor lock")
	}
}

func waitForPostgreSQLEnrollmentLockWait(t *testing.T, pool interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, pid uint32) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var blocked bool
		if err := pool.QueryRow(t.Context(), "SELECT pg_catalog.cardinality(pg_catalog.pg_blocking_pids($1)) > 0", int32(pid)).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("PostgreSQL backend %d did not enter the expected lock wait", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
