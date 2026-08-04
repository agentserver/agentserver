package coreserver

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/coredb"
	"github.com/agentserver/agentserver/v2/internal/enrollmenttoken"
	"github.com/agentserver/agentserver/v2/internal/executorenrollment"
)

const (
	ExecutorOAuthAudience            = "executor-gateway"
	ExecutorOAuthScope               = "executor:connect"
	ExecutorOAuthAccessTokenLifespan = 5 * time.Minute
	// Hydra v26.2.0 normalizes its duration response to Go's canonical
	// duration spelling. Sending that same spelling makes create/read exact
	// reconciliation stable instead of treating "5m" and "5m0s" as equal by
	// accident at the authorization boundary.
	executorOAuthAccessTokenLifespanWire = "5m0s"
)

type ExecutorEnrollmentStateStore interface {
	CreateExecutorResource(context.Context, coredb.CreateExecutorResourceCommand) (coredb.CreateExecutorResourceResult, error)
	ListExecutorResources(context.Context, string, string) ([]coredb.ExecutorResource, error)
	ArchiveExecutorResource(context.Context, coredb.ArchiveExecutorResourceCommand) (coredb.ArchiveExecutorResourceResult, error)
	IssueExecutorEnrollmentToken(context.Context, coredb.IssueExecutorEnrollmentTokenCommand) (coredb.IssueExecutorEnrollmentTokenResult, error)
	ClaimExecutorEnrollment(context.Context, coredb.ClaimExecutorEnrollmentCommand) (coredb.ExecutorEnrollmentReservation, error)
	CompleteExecutorEnrollment(context.Context, coredb.CompleteExecutorEnrollmentCommand) (coredb.ExecutorResource, error)
}

type ExecutorEnrollmentServiceConfig struct {
	Store    ExecutorEnrollmentStateStore
	Tokens   *enrollmenttoken.Codec
	Hydra    HydraExecutorClientAdmin
	TokenTTL time.Duration
	NewID    UserRunIDGenerator
	Now      func() time.Time
}

type ExecutorEnrollmentService struct {
	store    ExecutorEnrollmentStateStore
	tokens   *enrollmenttoken.Codec
	hydra    HydraExecutorClientAdmin
	tokenTTL time.Duration
	newID    UserRunIDGenerator
	now      func() time.Time
}

func NewExecutorEnrollmentService(config ExecutorEnrollmentServiceConfig) (*ExecutorEnrollmentService, error) {
	if config.Store == nil || config.Tokens == nil || config.Tokens.Issuer() == "" || config.Hydra == nil {
		return nil, errors.New("executor enrollment store, token codec, and Hydra client admin are required")
	}
	if config.TokenTTL <= 0 || config.TokenTTL > enrollmenttoken.MaximumTTL || config.TokenTTL%time.Millisecond != 0 {
		return nil, errors.New("executor enrollment token TTL must be a positive whole-millisecond duration no greater than 15 minutes")
	}
	if config.NewID == nil {
		config.NewID = newCoreUUID
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &ExecutorEnrollmentService{
		store: config.Store, tokens: config.Tokens, hydra: config.Hydra,
		tokenTTL: config.TokenTTL, newID: config.NewID, now: config.Now,
	}, nil
}

func (service *ExecutorEnrollmentService) CreateExecutor(ctx context.Context, actorID, workspaceID, executorID string) (corecontract.CreateExecutorResourceResponse, error) {
	result, err := service.store.CreateExecutorResource(ctx, coredb.CreateExecutorResourceCommand{
		ExecutorID: executorID, WorkspaceID: workspaceID, ActorID: actorID,
	})
	if err != nil {
		return corecontract.CreateExecutorResourceResponse{}, err
	}
	return corecontract.CreateExecutorResourceResponse{Executor: contractExecutorResource(result.Executor), Created: result.Created}, nil
}

func (service *ExecutorEnrollmentService) ListExecutors(ctx context.Context, actorID, workspaceID string) (corecontract.ListExecutorResourcesResponse, error) {
	items, err := service.store.ListExecutorResources(ctx, workspaceID, actorID)
	if err != nil {
		return corecontract.ListExecutorResourcesResponse{}, err
	}
	result := make([]corecontract.ExecutorResourceState, len(items))
	for index := range items {
		result[index] = contractExecutorResource(items[index])
	}
	return corecontract.ListExecutorResourcesResponse{Executors: result}, nil
}

func (service *ExecutorEnrollmentService) ArchiveExecutor(ctx context.Context, actorID, workspaceID, executorID string) (corecontract.ArchiveExecutorResourceResponse, error) {
	result, err := service.store.ArchiveExecutorResource(ctx, coredb.ArchiveExecutorResourceCommand{
		WorkspaceID: workspaceID, ActorID: actorID, ExecutorID: executorID,
	})
	return corecontract.ArchiveExecutorResourceResponse{Executor: contractExecutorResource(result.Executor), Changed: result.Changed}, err
}

func (service *ExecutorEnrollmentService) IssueEnrollmentToken(ctx context.Context, actorID, workspaceID, executorID, idempotencyKey string) (corecontract.IssueExecutorEnrollmentTokenResponse, error) {
	tokenID, err := service.newID()
	if err != nil {
		return corecontract.IssueExecutorEnrollmentTokenResponse{}, fmt.Errorf("allocate executor enrollment token identity: %w", err)
	}
	result, err := service.store.IssueExecutorEnrollmentToken(ctx, coredb.IssueExecutorEnrollmentTokenCommand{
		TokenID: tokenID, WorkspaceID: workspaceID, ExecutorID: executorID,
		ActorID: actorID, IdempotencyKey: idempotencyKey, TTL: service.tokenTTL,
	})
	if err != nil {
		return corecontract.IssueExecutorEnrollmentTokenResponse{}, err
	}
	claims := enrollmenttoken.Claims{
		Version: enrollmenttoken.Version, Issuer: service.tokens.Issuer(), TokenID: result.Token.ID,
		WorkspaceID: result.Token.WorkspaceID, ExecutorID: result.Token.ExecutorID,
		IssuedByActorID: result.Token.IssuedBy, IssuedAtUnixMS: result.Token.IssuedAt.UnixMilli(),
		ExpiresAtUnixMS: result.Token.ExpiresAt.UnixMilli(),
	}
	wire, err := service.tokens.Sign(claims)
	if err != nil {
		return corecontract.IssueExecutorEnrollmentTokenResponse{}, fmt.Errorf("sign executor enrollment token: %w", err)
	}
	return corecontract.IssueExecutorEnrollmentTokenResponse{
		ExecutorID: result.Token.ExecutorID, Token: wire, ExpiresAt: result.Token.ExpiresAt, Created: result.Created,
	}, nil
}

func (service *ExecutorEnrollmentService) CompleteEnrollment(
	ctx context.Context,
	bearer string,
	expectedExecutorID string,
	request corecontract.CompleteExecutorEnrollmentRequest,
) (corecontract.CompleteExecutorEnrollmentResponse, error) {
	if service == nil || service.store == nil || service.tokens == nil || service.hydra == nil || service.now == nil {
		return corecontract.CompleteExecutorEnrollmentResponse{}, errors.New("executor enrollment service is unavailable")
	}
	claims, err := service.tokens.Verify(bearer, service.now().UTC())
	if err != nil {
		return corecontract.CompleteExecutorEnrollmentResponse{}, &coredb.StateError{
			Code: coredb.ErrorForbidden, Operation: "CompleteExecutorEnrollment", Resource: "executor_enrollment_token",
			Message: "enrollment token is invalid",
		}
	}
	if !canonicalPublicUUID(expectedExecutorID) || claims.ExecutorID != expectedExecutorID {
		return corecontract.CompleteExecutorEnrollmentResponse{}, &coredb.StateError{
			Code: coredb.ErrorForbidden, Operation: "CompleteExecutorEnrollment", Resource: "executor",
			ResourceID: claims.ExecutorID, Message: "enrollment token belongs to another gateway deployment",
		}
	}
	validated, err := executorenrollment.Validate(request)
	if err != nil {
		return corecontract.CompleteExecutorEnrollmentResponse{}, &coredb.StateError{
			Code: coredb.ErrorInvalidArgument, Operation: "CompleteExecutorEnrollment", Resource: "executor",
			ResourceID: claims.ExecutorID, Message: err.Error(),
		}
	}
	if err := validated.VerifyProof(bearer); err != nil {
		return corecontract.CompleteExecutorEnrollmentResponse{}, &coredb.StateError{
			Code: coredb.ErrorForbidden, Operation: "CompleteExecutorEnrollment", Resource: "executor",
			ResourceID: claims.ExecutorID, Message: "machine-key possession proof is invalid",
		}
	}
	oauthClientID := "agentserver-executor-" + claims.ExecutorID
	_, err = service.store.ClaimExecutorEnrollment(ctx, coredb.ClaimExecutorEnrollmentCommand{
		TokenID: claims.TokenID, WorkspaceID: claims.WorkspaceID, ExecutorID: claims.ExecutorID,
		IssuedByActorID: claims.IssuedByActorID,
		IssuedAt:        time.UnixMilli(claims.IssuedAtUnixMS).UTC(), ExpiresAt: time.UnixMilli(claims.ExpiresAtUnixMS).UTC(),
		MachinePublicKeyEd25519: validated.MachinePublicKeyEd25519, MachineKeySHA256: validated.MachineKeySHA256,
		OAuthPublicKeyP256X: validated.OAuthPublicKeyP256X, OAuthPublicKeyP256Y: validated.OAuthPublicKeyP256Y,
		OAuthKeySHA256: validated.OAuthKeySHA256,
		OAuthClientID:  oauthClientID, AgentxVersion: validated.AgentxVersion,
		RuntimeManifestSHA256: validated.RuntimeManifestSHA256, ExecProtocolSourceSHA256: validated.ExecProtocolSourceSHA256,
		EnrollmentRequestSHA256: validated.EnrollmentRequestSHA256, Environments: validated.Environments,
	})
	if err != nil {
		return corecontract.CompleteExecutorEnrollmentResponse{}, err
	}
	document := executorOAuthClientDocument(
		oauthClientID, claims.ExecutorID, validated.OAuthPublicKeyP256X, validated.OAuthPublicKeyP256Y, validated.OAuthKeySHA256,
	)
	if err := service.ensureHydraExecutorClient(ctx, document); err != nil {
		return corecontract.CompleteExecutorEnrollmentResponse{}, fmt.Errorf("reconcile executor OAuth client: %w", err)
	}
	executor, err := service.store.CompleteExecutorEnrollment(ctx, coredb.CompleteExecutorEnrollmentCommand{
		TokenID: claims.TokenID, WorkspaceID: claims.WorkspaceID, ExecutorID: claims.ExecutorID,
		EnrollmentRequestSHA256: validated.EnrollmentRequestSHA256,
	})
	if err != nil {
		return corecontract.CompleteExecutorEnrollmentResponse{}, err
	}
	return corecontract.CompleteExecutorEnrollmentResponse{
		Executor: contractExecutorResource(executor), OAuthClientID: oauthClientID,
		Audience: ExecutorOAuthAudience, Scope: ExecutorOAuthScope,
	}, nil
}

func (service *ExecutorEnrollmentService) ensureHydraExecutorClient(ctx context.Context, expected HydraExecutorOAuthClient) error {
	actual, err := service.hydra.CreateExecutorOAuthClient(ctx, expected)
	if err == nil {
		if !equalHydraExecutorClient(actual, expected) {
			return errors.New("Hydra created an executor OAuth client outside the requested closed profile")
		}
		return nil
	}
	var adminError *HydraAdminError
	if !errors.As(err, &adminError) || adminError.StatusCode != http.StatusConflict {
		return err
	}
	actual, err = service.hydra.GetExecutorOAuthClient(ctx, expected.ClientID)
	if err != nil {
		return err
	}
	if !equalHydraExecutorClient(actual, expected) {
		return errors.New("existing Hydra executor OAuth client conflicts with enrolled machine identity")
	}
	return nil
}

func executorOAuthClientDocument(clientID, executorID string, publicKeyX, publicKeyY, thumbprint [32]byte) HydraExecutorOAuthClient {
	x := base64.RawURLEncoding.EncodeToString(publicKeyX[:])
	y := base64.RawURLEncoding.EncodeToString(publicKeyY[:])
	kid := base64.RawURLEncoding.EncodeToString(thumbprint[:])
	return HydraExecutorOAuthClient{
		ClientID: clientID, ClientName: "agentserver executor " + executorID,
		GrantTypes: []string{"client_credentials"}, ResponseTypes: []string{},
		Scope: ExecutorOAuthScope, Audience: []string{ExecutorOAuthAudience},
		TokenEndpointAuthMethod: "private_key_jwt", TokenEndpointAuthSigningAlg: "ES256",
		AccessTokenStrategy: "opaque", ClientCredentialsGrantAccessTokenLifespan: executorOAuthAccessTokenLifespanWire,
		JSONWebKeys: HydraJSONWebKeySet{Keys: []HydraJSONWebKey{{
			KeyType: "EC", Use: "sig", Curve: "P-256", KeyID: kid, X: x, Y: y, Algorithm: "ES256",
		}}},
	}
}

func equalHydraExecutorClient(left, right HydraExecutorOAuthClient) bool {
	return left.ClientID == right.ClientID && left.ClientName == right.ClientName &&
		slices.Equal(left.GrantTypes, right.GrantTypes) && slices.Equal(left.ResponseTypes, right.ResponseTypes) &&
		left.Scope == right.Scope && slices.Equal(left.Audience, right.Audience) &&
		left.TokenEndpointAuthMethod == right.TokenEndpointAuthMethod &&
		left.TokenEndpointAuthSigningAlg == right.TokenEndpointAuthSigningAlg &&
		left.AccessTokenStrategy == right.AccessTokenStrategy &&
		left.ClientCredentialsGrantAccessTokenLifespan == right.ClientCredentialsGrantAccessTokenLifespan &&
		slices.Equal(left.JSONWebKeys.Keys, right.JSONWebKeys.Keys)
}

func contractExecutorResource(executor coredb.ExecutorResource) corecontract.ExecutorResourceState {
	return corecontract.ExecutorResourceState{
		ExecutorID: executor.ID, WorkspaceID: executor.WorkspaceID, Status: executor.Status,
		Version: executor.Version, CreatedAt: executor.CreatedAt, UpdatedAt: executor.UpdatedAt,
	}
}
