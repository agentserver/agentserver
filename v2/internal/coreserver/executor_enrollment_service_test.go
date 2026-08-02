package coreserver

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/coredb"
	"github.com/agentserver/agentserver/v2/internal/enrollmenttoken"
	"github.com/agentserver/agentserver/v2/internal/executorenrollment"
)

const (
	enrollmentTestIssuer    = "https://agentserver.example.test/core"
	enrollmentTestTokenID   = "71000000-0000-4000-8000-000000000001"
	enrollmentTestWorkspace = "71000000-0000-4000-8000-000000000002"
	enrollmentTestExecutor  = "71000000-0000-4000-8000-000000000003"
	enrollmentTestActor     = "71000000-0000-4000-8000-000000000004"
	enrollmentTestEnv       = "71000000-0000-4000-8000-000000000005"
)

func TestExecutorEnrollmentServiceCompletesMachineOwnedHydraIdentity(t *testing.T) {
	now := time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC)
	codec := enrollmentTestCodec(t)
	claims := enrollmentTestClaims(now)
	bearer, err := codec.Sign(claims)
	if err != nil {
		t.Fatal(err)
	}
	privateKey := ed25519.NewKeyFromSeed([]byte(strings.Repeat("m", ed25519.SeedSize)))
	request := signedEnrollmentRequest(t, bearer, privateKey)
	store := &recordingEnrollmentStore{completed: coredb.ExecutorResource{
		ID: enrollmentTestExecutor, WorkspaceID: enrollmentTestWorkspace, Status: coredb.ExecutorStatusOffline,
		Version: 3, CreatedAt: now.Add(-time.Minute), UpdatedAt: now,
	}}
	hydra := &recordingHydraExecutorAdmin{}
	service, err := NewExecutorEnrollmentService(ExecutorEnrollmentServiceConfig{
		Store: store, Tokens: codec, Hydra: hydra, TokenTTL: 10 * time.Minute,
		Now: func() time.Time { return now }, NewID: func() (string, error) { return enrollmentTestTokenID, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := service.CompleteEnrollment(t.Context(), bearer, request)
	if err != nil {
		t.Fatal(err)
	}
	if response.Executor.ExecutorID != enrollmentTestExecutor || response.Executor.Status != coredb.ExecutorStatusOffline ||
		response.OAuthClientID != "agentserver-executor-"+enrollmentTestExecutor ||
		response.Audience != ExecutorOAuthAudience || response.Scope != ExecutorOAuthScope {
		t.Fatalf("complete enrollment response = %+v", response)
	}
	if len(store.claims) != 1 || len(store.completions) != 1 || len(hydra.creates) != 1 {
		t.Fatalf("claim/complete/Hydra calls = %d/%d/%d", len(store.claims), len(store.completions), len(hydra.creates))
	}
	claim := store.claims[0]
	if claim.TokenID != claims.TokenID || claim.ExecutorID != claims.ExecutorID ||
		claim.MachineKeySHA256 != sha256.Sum256(claim.MachinePublicKeyEd25519[:]) ||
		claim.OAuthClientID != response.OAuthClientID || claim.OAuthKeySHA256 == [32]byte{} ||
		claim.EnrollmentRequestSHA256 == [32]byte{} ||
		len(claim.Environments) != 1 || claim.Environments[0].InsecureDev {
		t.Fatalf("Core enrollment claim = %+v", claim)
	}
	document := hydra.creates[0]
	if document.ClientID != response.OAuthClientID || document.TokenEndpointAuthMethod != "private_key_jwt" ||
		document.TokenEndpointAuthSigningAlg != "ES256" || document.AccessTokenStrategy != "opaque" ||
		document.ClientCredentialsGrantAccessTokenLifespan != "5m0s" || len(document.JSONWebKeys.Keys) != 1 ||
		document.JSONWebKeys.Keys[0].X != request.OAuthPublicKeyP256X || document.JSONWebKeys.Keys[0].Y != request.OAuthPublicKeyP256Y {
		t.Fatalf("Hydra executor client = %+v", document)
	}
}

func TestExecutorEnrollmentServiceReconcilesOnlyExactHydraConflict(t *testing.T) {
	now := time.Date(2026, 8, 2, 10, 30, 0, 0, time.UTC)
	codec := enrollmentTestCodec(t)
	bearer, _ := codec.Sign(enrollmentTestClaims(now))
	privateKey := ed25519.NewKeyFromSeed([]byte(strings.Repeat("m", ed25519.SeedSize)))
	request := signedEnrollmentRequest(t, bearer, privateKey)
	baseStore := func() *recordingEnrollmentStore {
		return &recordingEnrollmentStore{completed: coredb.ExecutorResource{ID: enrollmentTestExecutor, WorkspaceID: enrollmentTestWorkspace, Status: coredb.ExecutorStatusOffline, Version: 3, CreatedAt: now, UpdatedAt: now}}
	}

	t.Run("exact existing client", func(t *testing.T) {
		store := baseStore()
		hydra := &recordingHydraExecutorAdmin{createErr: &HydraAdminError{StatusCode: http.StatusConflict, Operation: "create"}}
		service, _ := NewExecutorEnrollmentService(ExecutorEnrollmentServiceConfig{Store: store, Tokens: codec, Hydra: hydra, TokenTTL: 10 * time.Minute, Now: func() time.Time { return now }})
		validated, _ := executorenrollment.Validate(request)
		hydra.getResult = executorOAuthClientDocument(
			"agentserver-executor-"+enrollmentTestExecutor, enrollmentTestExecutor,
			validated.OAuthPublicKeyP256X, validated.OAuthPublicKeyP256Y, validated.OAuthKeySHA256,
		)
		if _, err := service.CompleteEnrollment(t.Context(), bearer, request); err != nil {
			t.Fatal(err)
		}
		if len(hydra.gets) != 1 || len(store.completions) != 1 {
			t.Fatalf("Hydra get/completion calls = %d/%d", len(hydra.gets), len(store.completions))
		}
	})

	t.Run("conflicting existing client", func(t *testing.T) {
		store := baseStore()
		hydra := &recordingHydraExecutorAdmin{createErr: &HydraAdminError{StatusCode: http.StatusConflict, Operation: "create"}}
		service, _ := NewExecutorEnrollmentService(ExecutorEnrollmentServiceConfig{Store: store, Tokens: codec, Hydra: hydra, TokenTTL: 10 * time.Minute, Now: func() time.Time { return now }})
		validated, _ := executorenrollment.Validate(request)
		hydra.getResult = executorOAuthClientDocument(
			"agentserver-executor-"+enrollmentTestExecutor, enrollmentTestExecutor,
			validated.OAuthPublicKeyP256X, validated.OAuthPublicKeyP256Y, validated.OAuthKeySHA256,
		)
		hydra.getResult.Scope = "runs:write"
		if _, err := service.CompleteEnrollment(t.Context(), bearer, request); err == nil || len(store.completions) != 0 {
			t.Fatalf("conflicting client error/completions = %v/%d", err, len(store.completions))
		}
	})
}

func TestExecutorEnrollmentServiceRejectsInvalidProofBeforeStateOrHydra(t *testing.T) {
	now := time.Date(2026, 8, 2, 11, 0, 0, 0, time.UTC)
	codec := enrollmentTestCodec(t)
	bearer, _ := codec.Sign(enrollmentTestClaims(now))
	privateKey := ed25519.NewKeyFromSeed([]byte(strings.Repeat("m", ed25519.SeedSize)))
	request := signedEnrollmentRequest(t, bearer, privateKey)
	request.MachineProofEd25519 = base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("x", ed25519.SignatureSize)))
	store := &recordingEnrollmentStore{}
	hydra := &recordingHydraExecutorAdmin{}
	service, _ := NewExecutorEnrollmentService(ExecutorEnrollmentServiceConfig{Store: store, Tokens: codec, Hydra: hydra, TokenTTL: 10 * time.Minute, Now: func() time.Time { return now }})
	if _, err := service.CompleteEnrollment(t.Context(), bearer, request); err == nil || len(store.claims) != 0 || len(hydra.creates) != 0 {
		t.Fatalf("invalid proof error/state/Hydra = %v/%d/%d", err, len(store.claims), len(hydra.creates))
	}
}

func TestExecutorEnrollmentServiceIssuesStableDatabaseBackedToken(t *testing.T) {
	now := time.Date(2026, 8, 2, 11, 30, 0, 0, time.UTC)
	codec := enrollmentTestCodec(t)
	store := &recordingEnrollmentStore{issued: coredb.IssueExecutorEnrollmentTokenResult{
		Token: coredb.ExecutorEnrollmentToken{
			ID: enrollmentTestTokenID, WorkspaceID: enrollmentTestWorkspace, ExecutorID: enrollmentTestExecutor,
			IssuedBy: enrollmentTestActor, IdempotencyKey: "enroll-1", IssuedAt: now, ExpiresAt: now.Add(10 * time.Minute), Version: 1,
		}, Created: true,
	}}
	service, _ := NewExecutorEnrollmentService(ExecutorEnrollmentServiceConfig{
		Store: store, Tokens: codec, Hydra: &recordingHydraExecutorAdmin{}, TokenTTL: 10 * time.Minute,
		Now: func() time.Time { return now }, NewID: func() (string, error) { return "71000000-0000-4000-8000-000000000099", nil },
	})
	response, err := service.IssueEnrollmentToken(t.Context(), enrollmentTestActor, enrollmentTestWorkspace, enrollmentTestExecutor, "enroll-1")
	if err != nil {
		t.Fatal(err)
	}
	claims, err := codec.Verify(response.Token, now)
	if err != nil || claims.TokenID != enrollmentTestTokenID || !response.Created || len(store.issues) != 1 {
		t.Fatalf("issued enrollment response/claims = %+v / %+v / %v", response, claims, err)
	}
}

func enrollmentTestCodec(t *testing.T) *enrollmenttoken.Codec {
	t.Helper()
	codec, err := enrollmenttoken.New(enrollmentTestIssuer, []byte(strings.Repeat("t", 32)))
	if err != nil {
		t.Fatal(err)
	}
	return codec
}

func enrollmentTestClaims(now time.Time) enrollmenttoken.Claims {
	return enrollmenttoken.Claims{
		Version: enrollmenttoken.Version, Issuer: enrollmentTestIssuer, TokenID: enrollmentTestTokenID,
		WorkspaceID: enrollmentTestWorkspace, ExecutorID: enrollmentTestExecutor, IssuedByActorID: enrollmentTestActor,
		IssuedAtUnixMS: now.Add(-time.Minute).UnixMilli(), ExpiresAtUnixMS: now.Add(9 * time.Minute).UnixMilli(),
	}
}

func signedEnrollmentRequest(t *testing.T, token string, privateKey ed25519.PrivateKey) corecontract.CompleteExecutorEnrollmentRequest {
	t.Helper()
	oauthPrivateKey := enrollmentTestOAuthPrivateKey()
	runtimeDigest := sha256.Sum256([]byte("runtime"))
	protocolDigest := sha256.Sum256([]byte("protocol"))
	codexDigest := sha256.Sum256([]byte("codex"))
	policyDigest := sha256.Sum256([]byte("owner-policy"))
	request := corecontract.CompleteExecutorEnrollmentRequest{
		MachinePublicKeyEd25519: base64.RawURLEncoding.EncodeToString(privateKey.Public().(ed25519.PublicKey)),
		MachineProofEd25519:     base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("p", ed25519.SignatureSize))),
		OAuthPublicKeyP256X:     base64.RawURLEncoding.EncodeToString(oauthPrivateKey.X.FillBytes(make([]byte, 32))),
		OAuthPublicKeyP256Y:     base64.RawURLEncoding.EncodeToString(oauthPrivateKey.Y.FillBytes(make([]byte, 32))),
		OAuthProofES256:         base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("o", 64))),
		AgentxVersion:           "0.1.0", RuntimeManifestSHA256: hex.EncodeToString(runtimeDigest[:]), ExecProtocolSourceSHA256: hex.EncodeToString(protocolDigest[:]),
		Environments: []corecontract.ExecutorEnrollmentEnvironment{{
			EnvironmentDeclaration: corecontract.EnvironmentDeclaration{
				ID: enrollmentTestEnv, Platform: "linux-amd64", CodexRelease: "0.146.0", CodexCommit: strings.Repeat("a", 40),
				CodexSHA256: hex.EncodeToString(codexDigest[:]), OuterProfileVersion: "process-v1",
				ProcessMethods: []string{"process/start", "process/read", "process/write", "process/terminate"},
			},
			RootDescriptor: json.RawMessage(`{"kind":"local","root":"/workspace"}`), OwnerPolicySHA256: hex.EncodeToString(policyDigest[:]),
		}},
	}
	validated, err := executorenrollment.Validate(request)
	if err != nil {
		t.Fatal(err)
	}
	request.MachineProofEd25519 = base64.RawURLEncoding.EncodeToString(ed25519.Sign(privateKey,
		executorenrollment.ProofMessage(sha256.Sum256([]byte(token)), validated.EnrollmentRequestSHA256),
	))
	request.OAuthProofES256 = base64.RawURLEncoding.EncodeToString(enrollmentTestOAuthProof(
		t, oauthPrivateKey, executorenrollment.OAuthProofDigest(sha256.Sum256([]byte(token)), validated.EnrollmentRequestSHA256),
	))
	return request
}

func enrollmentTestOAuthPrivateKey() *ecdsa.PrivateKey {
	curve := elliptic.P256()
	d := big.NewInt(73)
	x, y := curve.ScalarBaseMult(d.Bytes())
	return &ecdsa.PrivateKey{PublicKey: ecdsa.PublicKey{Curve: curve, X: x, Y: y}, D: d}
}

func enrollmentTestOAuthAuthority() (x, y, thumbprint [32]byte) {
	key := enrollmentTestOAuthPrivateKey()
	copy(x[:], key.X.FillBytes(make([]byte, 32)))
	copy(y[:], key.Y.FillBytes(make([]byte, 32)))
	thumbprint = executorenrollment.OAuthJWKThumbprint(
		base64.RawURLEncoding.EncodeToString(x[:]), base64.RawURLEncoding.EncodeToString(y[:]),
	)
	return x, y, thumbprint
}

func enrollmentTestOAuthProof(t *testing.T, privateKey *ecdsa.PrivateKey, digest [sha256.Size]byte) []byte {
	t.Helper()
	r, s, err := ecdsa.Sign(rand.Reader, privateKey, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	halfOrder := new(big.Int).Rsh(new(big.Int).Set(privateKey.Curve.Params().N), 1)
	if s.Cmp(halfOrder) > 0 {
		s.Sub(privateKey.Curve.Params().N, s)
	}
	proof := make([]byte, 64)
	r.FillBytes(proof[:32])
	s.FillBytes(proof[32:])
	return proof
}

type recordingEnrollmentStore struct {
	issues      []coredb.IssueExecutorEnrollmentTokenCommand
	issued      coredb.IssueExecutorEnrollmentTokenResult
	claims      []coredb.ClaimExecutorEnrollmentCommand
	completions []coredb.CompleteExecutorEnrollmentCommand
	completed   coredb.ExecutorResource
}

func (store *recordingEnrollmentStore) CreateExecutorResource(context.Context, coredb.CreateExecutorResourceCommand) (coredb.CreateExecutorResourceResult, error) {
	return coredb.CreateExecutorResourceResult{}, errors.New("not configured")
}

func (store *recordingEnrollmentStore) IssueExecutorEnrollmentToken(_ context.Context, command coredb.IssueExecutorEnrollmentTokenCommand) (coredb.IssueExecutorEnrollmentTokenResult, error) {
	store.issues = append(store.issues, command)
	return store.issued, nil
}

func (store *recordingEnrollmentStore) ClaimExecutorEnrollment(_ context.Context, command coredb.ClaimExecutorEnrollmentCommand) (coredb.ExecutorEnrollmentReservation, error) {
	store.claims = append(store.claims, command)
	return coredb.ExecutorEnrollmentReservation{Executor: coredb.ExecutorResource{ID: command.ExecutorID, WorkspaceID: command.WorkspaceID, Status: coredb.ExecutorStatusEnrolling}, OAuthClientID: command.OAuthClientID, Created: true}, nil
}

func (store *recordingEnrollmentStore) CompleteExecutorEnrollment(_ context.Context, command coredb.CompleteExecutorEnrollmentCommand) (coredb.ExecutorResource, error) {
	store.completions = append(store.completions, command)
	return store.completed, nil
}

type recordingHydraExecutorAdmin struct {
	creates   []HydraExecutorOAuthClient
	createErr error
	gets      []string
	getResult HydraExecutorOAuthClient
	getErr    error
}

func (admin *recordingHydraExecutorAdmin) CreateExecutorOAuthClient(_ context.Context, document HydraExecutorOAuthClient) (HydraExecutorOAuthClient, error) {
	admin.creates = append(admin.creates, document)
	if admin.createErr != nil {
		return HydraExecutorOAuthClient{}, admin.createErr
	}
	return document, nil
}

func (admin *recordingHydraExecutorAdmin) GetExecutorOAuthClient(_ context.Context, clientID string) (HydraExecutorOAuthClient, error) {
	admin.gets = append(admin.gets, clientID)
	return admin.getResult, admin.getErr
}
