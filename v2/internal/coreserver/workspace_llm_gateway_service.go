package coreserver

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/coredb"
	"github.com/agentserver/agentserver/v2/internal/publichttps"
)

const (
	defaultLLMGatewayAuthorizationTTL = 5 * time.Minute
	defaultLLMGatewayRefreshSkew      = time.Minute
)

type WorkspaceLLMGatewayStore interface {
	RequireWorkspaceLLMGatewayOwner(context.Context, string, string) error
	CreateWorkspaceLLMGateway(context.Context, coredb.CreateWorkspaceLLMGatewayCommand) (coredb.CreateWorkspaceLLMGatewayResult, error)
	UpdateWorkspaceLLMGateway(context.Context, coredb.UpdateWorkspaceLLMGatewayCommand) (coredb.UpdateWorkspaceLLMGatewayResult, error)
	ListWorkspaceLLMGateways(context.Context, string, string) ([]coredb.WorkspaceLLMGateway, error)
	ReadWorkspaceLLMGatewayForAuthorization(context.Context, string, string, string) (coredb.WorkspaceLLMGateway, error)
	CreateWorkspaceLLMGatewayAuthTransaction(context.Context, coredb.CreateWorkspaceLLMGatewayAuthTransactionCommand) (coredb.WorkspaceLLMGatewayAuthTransaction, error)
	ClaimWorkspaceLLMGatewayAuthTransaction(context.Context, coredb.ClaimWorkspaceLLMGatewayAuthTransactionCommand) (coredb.WorkspaceLLMGatewayAuthTransaction, error)
	CompleteWorkspaceLLMGatewayAuthTransaction(context.Context, coredb.CompleteWorkspaceLLMGatewayAuthTransactionCommand) (coredb.WorkspaceLLMGatewayGrant, error)
	FailWorkspaceLLMGatewayAuthTransaction(context.Context, coredb.FailWorkspaceLLMGatewayAuthTransactionCommand) (coredb.WorkspaceLLMGatewayAuthTransaction, error)
	RevokeWorkspaceLLMGatewayGrant(context.Context, coredb.RevokeWorkspaceLLMGatewayGrantCommand) (coredb.RevokeWorkspaceLLMGatewayGrantResult, error)
	DisableWorkspaceLLMGateway(context.Context, coredb.DisableWorkspaceLLMGatewayCommand) (coredb.DisableWorkspaceLLMGatewayResult, error)
	UpdateWorkspaceLLMGatewayGrantTokens(context.Context, string, int64, []byte, time.Time) (coredb.WorkspaceLLMGatewayGrant, error)
	MarkWorkspaceLLMGatewayGrantReauthRequired(context.Context, string, int64) error
	ReadWorkspaceLLMGatewayLiveAuthority(context.Context, string, coredb.RunLLMGatewayBinding) (coredb.LLMGatewayLiveAuthority, error)
}

type WorkspaceLLMGatewayServiceConfig struct {
	Store          WorkspaceLLMGatewayStore
	Sealer         *LLMGatewayGrantSealer
	Providers      WorkspaceLLMGatewayOIDCProviderFactory
	RedirectURL    string
	TransactionTTL time.Duration
	RefreshSkew    time.Duration
	NewID          func() (string, error)
	Random         io.Reader
	Now            func() time.Time
	Logger         *slog.Logger
}

type WorkspaceLLMGatewayService struct {
	store          WorkspaceLLMGatewayStore
	sealer         *LLMGatewayGrantSealer
	providers      WorkspaceLLMGatewayOIDCProviderFactory
	redirectURL    string
	transactionTTL time.Duration
	refreshSkew    time.Duration
	newID          func() (string, error)
	random         io.Reader
	now            func() time.Time
	logger         *slog.Logger
}

type LLMGatewayUpstreamAuthorization struct {
	GatewayID            string
	GatewayConfigVersion int64
	GrantUserID          string
	Model                string
	ResponsesURL         string
	Authorization        string
	BearerExpiresAt      time.Time
}

func NewWorkspaceLLMGatewayService(config WorkspaceLLMGatewayServiceConfig) (*WorkspaceLLMGatewayService, error) {
	if config.Store == nil || config.Sealer == nil || config.Providers == nil {
		return nil, errors.New("workspace LLM gateway store, sealer, and OIDC provider factory are required")
	}
	if _, err := publichttps.ValidateURL(config.RedirectURL, corecontract.LLMGatewayOIDCCallbackPath); err != nil {
		return nil, fmt.Errorf("workspace LLM gateway redirect URL: %w", err)
	}
	if config.TransactionTTL == 0 {
		config.TransactionTTL = defaultLLMGatewayAuthorizationTTL
	}
	if config.TransactionTTL < time.Minute || config.TransactionTTL > coredb.MaxLLMGatewayAuthTransactionTTL || config.TransactionTTL%time.Millisecond != 0 {
		return nil, errors.New("workspace LLM gateway authorization TTL must be a whole-millisecond duration between one and ten minutes")
	}
	if config.RefreshSkew == 0 {
		config.RefreshSkew = defaultLLMGatewayRefreshSkew
	}
	if config.RefreshSkew < 5*time.Second || config.RefreshSkew > 10*time.Minute {
		return nil, errors.New("workspace LLM gateway refresh skew must be between five seconds and ten minutes")
	}
	if config.NewID == nil {
		config.NewID = newCoreUUID
	}
	if config.Random == nil {
		config.Random = rand.Reader
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.Logger == nil {
		config.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &WorkspaceLLMGatewayService{
		store: config.Store, sealer: config.Sealer, providers: config.Providers,
		redirectURL: config.RedirectURL, transactionTTL: config.TransactionTTL,
		refreshSkew: config.RefreshSkew, newID: config.NewID, random: config.Random, now: config.Now,
		logger: config.Logger,
	}, nil
}

func (service *WorkspaceLLMGatewayService) CreateGateway(
	ctx context.Context,
	workspaceID, actorID string,
	request corecontract.CreateWorkspaceLLMGatewayRequest,
) (corecontract.CreateWorkspaceLLMGatewayResponse, error) {
	if service == nil {
		return corecontract.CreateWorkspaceLLMGatewayResponse{}, errors.New("workspace LLM gateway service is unavailable")
	}
	if !canonicalPublicUUID(workspaceID) || !canonicalPublicUUID(actorID) || !canonicalPublicUUID(request.GatewayID) {
		return corecontract.CreateWorkspaceLLMGatewayResponse{}, llmGatewayStateError(coredb.ErrorInvalidArgument, "CreateWorkspaceLLMGateway", request.GatewayID, "workspace, actor, and gateway IDs must be canonical UUIDs")
	}
	if _, err := publichttps.ValidateURL(request.ResponsesURL, "/v1/responses"); err != nil {
		return corecontract.CreateWorkspaceLLMGatewayResponse{}, llmGatewayStateError(coredb.ErrorInvalidArgument, "CreateWorkspaceLLMGateway", request.GatewayID, "responsesUrl is not an allowed public Responses endpoint")
	}
	if _, err := publichttps.ValidateIssuer(request.OIDCIssuer); err != nil {
		return corecontract.CreateWorkspaceLLMGatewayResponse{}, llmGatewayStateError(coredb.ErrorInvalidArgument, "CreateWorkspaceLLMGateway", request.GatewayID, "oidcIssuer is not an allowed public OIDC issuer")
	}
	scopes, canonicalScopes, err := canonicalWorkspaceLLMGatewayScopes(request.OIDCScopes)
	if err != nil {
		return corecontract.CreateWorkspaceLLMGatewayResponse{}, llmGatewayStateError(coredb.ErrorInvalidArgument, "CreateWorkspaceLLMGateway", request.GatewayID, err.Error())
	}
	bearerType := request.BearerTokenType
	if bearerType == "" {
		bearerType = coredb.LLMGatewayBearerIDToken
	}
	if !validWorkspaceLLMGatewayPublicText(request.Name, 128) || !validWorkspaceLLMGatewayPublicText(request.OIDCClientID, 512) ||
		!validWorkspaceLLMGatewayPublicText(request.DefaultModel, 256) ||
		(bearerType != coredb.LLMGatewayBearerIDToken && bearerType != coredb.LLMGatewayBearerAccessToken) {
		return corecontract.CreateWorkspaceLLMGatewayResponse{}, llmGatewayStateError(coredb.ErrorInvalidArgument, "CreateWorkspaceLLMGateway", request.GatewayID, "gateway name, OIDC client ID, model, or bearer type is invalid")
	}
	// Owner authorization precedes the first externally observable operation.
	// CreateWorkspaceLLMGateway repeats the check in its write transaction so
	// a concurrent membership change cannot authorize the durable config.
	if err := service.store.RequireWorkspaceLLMGatewayOwner(ctx, workspaceID, actorID); err != nil {
		return corecontract.CreateWorkspaceLLMGatewayResponse{}, err
	}
	// Discovery at configuration time catches an issuer mismatch and unsafe
	// metadata endpoints before a durable workspace route exists.
	if _, err := service.providers.Discover(ctx, WorkspaceLLMGatewayOIDCConfig{
		Issuer: request.OIDCIssuer, ClientID: request.OIDCClientID, Scopes: scopes, RedirectURL: service.redirectURL,
	}); err != nil {
		return corecontract.CreateWorkspaceLLMGatewayResponse{}, llmGatewayStateError(coredb.ErrorInvalidArgument, "CreateWorkspaceLLMGateway", request.GatewayID, "OIDC discovery failed or returned unsafe metadata")
	}
	created, err := service.store.CreateWorkspaceLLMGateway(ctx, coredb.CreateWorkspaceLLMGatewayCommand{
		ID: request.GatewayID, WorkspaceID: workspaceID, ActorID: actorID,
		Name: request.Name, ResponsesURL: request.ResponsesURL, OIDCIssuer: request.OIDCIssuer,
		OIDCClientID: request.OIDCClientID, OIDCScopes: canonicalScopes,
		BearerTokenType: bearerType, DefaultModel: request.DefaultModel, MakeDefault: request.MakeDefault,
	})
	if err != nil {
		return corecontract.CreateWorkspaceLLMGatewayResponse{}, err
	}
	return corecontract.CreateWorkspaceLLMGatewayResponse{Gateway: contractWorkspaceLLMGateway(created.Gateway), Created: created.Created}, nil
}

func (service *WorkspaceLLMGatewayService) ListGateways(
	ctx context.Context,
	workspaceID, actorID string,
) (corecontract.ListWorkspaceLLMGatewaysResponse, error) {
	if service == nil {
		return corecontract.ListWorkspaceLLMGatewaysResponse{}, errors.New("workspace LLM gateway service is unavailable")
	}
	gateways, err := service.store.ListWorkspaceLLMGateways(ctx, workspaceID, actorID)
	if err != nil {
		return corecontract.ListWorkspaceLLMGatewaysResponse{}, err
	}
	result := make([]corecontract.WorkspaceLLMGatewayState, len(gateways))
	for index := range gateways {
		result[index] = contractWorkspaceLLMGateway(gateways[index])
	}
	return corecontract.ListWorkspaceLLMGatewaysResponse{Gateways: result}, nil
}

func (service *WorkspaceLLMGatewayService) UpdateGateway(
	ctx context.Context,
	workspaceID, gatewayID, actorID string,
	request corecontract.UpdateWorkspaceLLMGatewayRequest,
) (corecontract.UpdateWorkspaceLLMGatewayResponse, error) {
	const operation = "UpdateWorkspaceLLMGateway"
	if service == nil {
		return corecontract.UpdateWorkspaceLLMGatewayResponse{}, errors.New("workspace LLM gateway service is unavailable")
	}
	if !canonicalPublicUUID(workspaceID) || !canonicalPublicUUID(gatewayID) || !canonicalPublicUUID(actorID) || request.ExpectedVersion < 1 {
		return corecontract.UpdateWorkspaceLLMGatewayResponse{}, llmGatewayStateError(coredb.ErrorInvalidArgument, operation, gatewayID, "workspace, gateway, actor, and expected version must be canonical")
	}
	if _, err := publichttps.ValidateURL(request.ResponsesURL, "/v1/responses"); err != nil {
		return corecontract.UpdateWorkspaceLLMGatewayResponse{}, llmGatewayStateError(coredb.ErrorInvalidArgument, operation, gatewayID, "responsesUrl is not an allowed public Responses endpoint")
	}
	if _, err := publichttps.ValidateIssuer(request.OIDCIssuer); err != nil {
		return corecontract.UpdateWorkspaceLLMGatewayResponse{}, llmGatewayStateError(coredb.ErrorInvalidArgument, operation, gatewayID, "oidcIssuer is not an allowed public OIDC issuer")
	}
	scopes, canonicalScopes, err := canonicalWorkspaceLLMGatewayScopes(request.OIDCScopes)
	if err != nil {
		return corecontract.UpdateWorkspaceLLMGatewayResponse{}, llmGatewayStateError(coredb.ErrorInvalidArgument, operation, gatewayID, err.Error())
	}
	if !validWorkspaceLLMGatewayPublicText(request.Name, 128) || !validWorkspaceLLMGatewayPublicText(request.OIDCClientID, 512) ||
		!validWorkspaceLLMGatewayPublicText(request.DefaultModel, 256) ||
		(request.BearerTokenType != coredb.LLMGatewayBearerIDToken && request.BearerTokenType != coredb.LLMGatewayBearerAccessToken) {
		return corecontract.UpdateWorkspaceLLMGatewayResponse{}, llmGatewayStateError(coredb.ErrorInvalidArgument, operation, gatewayID, "gateway name, OIDC client ID, model, or bearer type is invalid")
	}
	// Authorize the owner before performing externally observable discovery;
	// the store repeats this check in the final write transaction.
	if err := service.store.RequireWorkspaceLLMGatewayOwner(ctx, workspaceID, actorID); err != nil {
		return corecontract.UpdateWorkspaceLLMGatewayResponse{}, err
	}
	if _, err := service.providers.Discover(ctx, WorkspaceLLMGatewayOIDCConfig{
		Issuer: request.OIDCIssuer, ClientID: request.OIDCClientID, Scopes: scopes, RedirectURL: service.redirectURL,
	}); err != nil {
		return corecontract.UpdateWorkspaceLLMGatewayResponse{}, llmGatewayStateError(coredb.ErrorInvalidArgument, operation, gatewayID, "OIDC discovery failed or returned unsafe metadata")
	}
	updated, err := service.store.UpdateWorkspaceLLMGateway(ctx, coredb.UpdateWorkspaceLLMGatewayCommand{
		ID: gatewayID, WorkspaceID: workspaceID, ActorID: actorID,
		Name: request.Name, ResponsesURL: request.ResponsesURL, OIDCIssuer: request.OIDCIssuer,
		OIDCClientID: request.OIDCClientID, OIDCScopes: canonicalScopes,
		BearerTokenType: request.BearerTokenType, DefaultModel: request.DefaultModel,
		MakeDefault: request.MakeDefault, ExpectedVersion: request.ExpectedVersion,
	})
	if err != nil {
		return corecontract.UpdateWorkspaceLLMGatewayResponse{}, err
	}
	return corecontract.UpdateWorkspaceLLMGatewayResponse{
		Gateway: contractWorkspaceLLMGateway(updated.Gateway), Changed: updated.Changed,
	}, nil
}

func (service *WorkspaceLLMGatewayService) BeginAuthorization(
	ctx context.Context,
	workspaceID, gatewayID, actorID string,
	request corecontract.BeginWorkspaceLLMGatewayAuthorizationRequest,
) (corecontract.BeginWorkspaceLLMGatewayAuthorizationResponse, error) {
	if service == nil {
		return corecontract.BeginWorkspaceLLMGatewayAuthorizationResponse{}, errors.New("workspace LLM gateway service is unavailable")
	}
	if validateOIDCSecret("browser binding", request.BrowserBinding) != nil {
		return corecontract.BeginWorkspaceLLMGatewayAuthorizationResponse{}, llmGatewayStateError(coredb.ErrorInvalidArgument, "BeginWorkspaceLLMGatewayAuthorization", gatewayID, "browser binding is invalid")
	}
	gateway, err := service.store.ReadWorkspaceLLMGatewayForAuthorization(ctx, workspaceID, gatewayID, actorID)
	if err != nil {
		return corecontract.BeginWorkspaceLLMGatewayAuthorizationResponse{}, err
	}
	provider, err := service.discoverGatewayProvider(ctx, gateway)
	if err != nil {
		return corecontract.BeginWorkspaceLLMGatewayAuthorizationResponse{}, llmGatewayStateError(coredb.ErrorInvalidState, "BeginWorkspaceLLMGatewayAuthorization", gatewayID, "gateway OIDC provider is unavailable or unsafe")
	}
	transactionID, err := service.allocateID("authorization transaction")
	if err != nil {
		return corecontract.BeginWorkspaceLLMGatewayAuthorizationResponse{}, err
	}
	secrets := workspaceLLMGatewayAuthorizationSecrets{BrowserBinding: request.BrowserBinding}
	for destination, name := range map[*string]string{
		&secrets.State: "state", &secrets.Nonce: "nonce", &secrets.PKCEVerifier: "PKCE verifier",
	} {
		*destination, err = service.randomSecret(name)
		if err != nil {
			return corecontract.BeginWorkspaceLLMGatewayAuthorizationResponse{}, err
		}
	}
	authorizationURL, err := provider.AuthorizationURL(secrets.State, secrets.Nonce, secrets.PKCEVerifier)
	if err != nil {
		return corecontract.BeginWorkspaceLLMGatewayAuthorizationResponse{}, llmGatewayStateError(coredb.ErrorInvalidState, "BeginWorkspaceLLMGatewayAuthorization", gatewayID, "gateway OIDC authorization endpoint rejected the request")
	}
	rawSecrets, err := json.Marshal(secrets)
	if err != nil {
		return corecontract.BeginWorkspaceLLMGatewayAuthorizationResponse{}, errors.New("encode workspace LLM gateway authorization secrets")
	}
	sealed, err := service.sealer.SealAuthorizationSecrets(LLMGatewaySealScope{
		WorkspaceID: workspaceID, GatewayID: gatewayID, UserID: actorID,
		GatewayVersion: gateway.Version, TransactionID: transactionID,
	}, rawSecrets)
	clear(rawSecrets)
	if err != nil {
		return corecontract.BeginWorkspaceLLMGatewayAuthorizationResponse{}, err
	}
	created, err := service.store.CreateWorkspaceLLMGatewayAuthTransaction(ctx, coredb.CreateWorkspaceLLMGatewayAuthTransactionCommand{
		ID: transactionID, WorkspaceID: workspaceID, GatewayID: gatewayID,
		GatewayVersion: gateway.Version, UserID: actorID,
		OIDCStateSHA256:      sha256.Sum256([]byte(secrets.State)),
		BrowserBindingSHA256: sha256.Sum256([]byte(secrets.BrowserBinding)),
		SealedSecrets:        sealed, TTL: service.transactionTTL,
	})
	if err != nil {
		return corecontract.BeginWorkspaceLLMGatewayAuthorizationResponse{}, err
	}
	if created.Status != coredb.LLMGatewayAuthStatusPending || created.GatewayVersion != gateway.Version {
		return corecontract.BeginWorkspaceLLMGatewayAuthorizationResponse{}, errors.New("workspace LLM gateway store returned an invalid authorization transaction")
	}
	return corecontract.BeginWorkspaceLLMGatewayAuthorizationResponse{
		GatewayID: gatewayID, AuthorizationURL: authorizationURL, ExpiresAt: created.ExpiresAt.UTC(),
	}, nil
}

func (service *WorkspaceLLMGatewayService) CompleteAuthorization(
	ctx context.Context,
	workspaceID, gatewayID, actorID string,
	request corecontract.CompleteWorkspaceLLMGatewayAuthorizationRequest,
) (corecontract.CompleteWorkspaceLLMGatewayAuthorizationResponse, error) {
	operation := "CompleteWorkspaceLLMGatewayAuthorization"
	if service == nil {
		return corecontract.CompleteWorkspaceLLMGatewayAuthorizationResponse{}, errors.New("workspace LLM gateway service is unavailable")
	}
	if validateOIDCSecret("state", request.State) != nil || validateOIDCSecret("browser binding", request.BrowserBinding) != nil ||
		(request.Code == "") == (request.ProviderError == "") ||
		(request.Code != "" && (len(request.Code) > 8192 || strings.ContainsAny(request.Code, "\x00\r\n"))) ||
		(request.ProviderError != "" && (len(request.ProviderError) > 128 || strings.ContainsAny(request.ProviderError, "\x00\r\n"))) {
		return corecontract.CompleteWorkspaceLLMGatewayAuthorizationResponse{}, llmGatewayStateError(coredb.ErrorInvalidArgument, operation, gatewayID, "authorization callback framing is invalid")
	}
	claimed, err := service.store.ClaimWorkspaceLLMGatewayAuthTransaction(ctx, coredb.ClaimWorkspaceLLMGatewayAuthTransactionCommand{
		WorkspaceID: workspaceID, GatewayID: gatewayID, UserID: actorID,
		OIDCStateSHA256:      sha256.Sum256([]byte(request.State)),
		BrowserBindingSHA256: sha256.Sum256([]byte(request.BrowserBinding)),
	})
	if err != nil {
		return corecontract.CompleteWorkspaceLLMGatewayAuthorizationResponse{}, err
	}
	if claimed.Status == coredb.LLMGatewayAuthStatusExpired {
		return corecontract.CompleteWorkspaceLLMGatewayAuthorizationResponse{}, llmGatewayStateError(coredb.ErrorForbidden, operation, gatewayID, "authorization transaction expired")
	}
	if claimed.Status != coredb.LLMGatewayAuthStatusCallbackClaimed {
		return corecontract.CompleteWorkspaceLLMGatewayAuthorizationResponse{}, errors.New("workspace LLM gateway callback claim returned an invalid state")
	}
	secrets, err := service.openAuthorizationSecrets(claimed)
	if err != nil {
		return corecontract.CompleteWorkspaceLLMGatewayAuthorizationResponse{}, err
	}
	if !constantTimeTextEqual(secrets.State, request.State) || !constantTimeTextEqual(secrets.BrowserBinding, request.BrowserBinding) {
		return corecontract.CompleteWorkspaceLLMGatewayAuthorizationResponse{}, errors.New("workspace LLM gateway callback does not match sealed authorization authority")
	}
	if request.ProviderError != "" {
		return corecontract.CompleteWorkspaceLLMGatewayAuthorizationResponse{}, service.failClaimedAuthorization(ctx, claimed, "identity_provider_denied")
	}
	gateway, err := service.store.ReadWorkspaceLLMGatewayForAuthorization(ctx, workspaceID, gatewayID, actorID)
	if err != nil || gateway.Version != claimed.GatewayVersion {
		if err == nil {
			err = llmGatewayStateError(coredb.ErrorConflict, operation, gatewayID, "gateway changed during authorization")
		}
		return corecontract.CompleteWorkspaceLLMGatewayAuthorizationResponse{}, errors.Join(err, service.failClaimedOnly(ctx, claimed, "gateway_changed"))
	}
	provider, err := service.discoverGatewayProvider(ctx, gateway)
	if err != nil {
		return corecontract.CompleteWorkspaceLLMGatewayAuthorizationResponse{}, errors.Join(
			llmGatewayStateError(coredb.ErrorInvalidState, operation, gatewayID, "gateway OIDC provider is unavailable or unsafe"),
			service.failClaimedOnly(ctx, claimed, "oidc_discovery_failed"),
		)
	}
	grant, err := provider.Exchange(ctx, request.Code, secrets.PKCEVerifier, secrets.Nonce)
	if err != nil || grant.Issuer != gateway.OIDCIssuer {
		return corecontract.CompleteWorkspaceLLMGatewayAuthorizationResponse{}, errors.Join(
			llmGatewayStateError(coredb.ErrorForbidden, operation, gatewayID, "gateway OIDC identity verification failed"),
			service.failClaimedOnly(ctx, claimed, "identity_exchange_failed"),
		)
	}
	if grant.Tokens.RefreshToken == "" {
		return corecontract.CompleteWorkspaceLLMGatewayAuthorizationResponse{}, errors.Join(
			llmGatewayStateError(coredb.ErrorForbidden, operation, gatewayID, "gateway OIDC provider did not issue the required offline refresh grant"),
			service.failClaimedOnly(ctx, claimed, "refresh_token_missing"),
		)
	}
	bearer, bearerExpiry, err := workspaceLLMGatewayBearer(grant.Tokens, gateway.BearerTokenType)
	if err != nil || bearer == "" || !bearerExpiry.After(service.now().UTC()) {
		return corecontract.CompleteWorkspaceLLMGatewayAuthorizationResponse{}, errors.Join(
			llmGatewayStateError(coredb.ErrorForbidden, operation, gatewayID, "gateway OIDC bearer is already expired or invalid"),
			service.failClaimedOnly(ctx, claimed, "invalid_bearer"),
		)
	}
	sealedTokens, err := service.sealTokenSet(coredb.RunLLMGatewayBinding{
		GatewayID: gateway.ID, ConfigVersion: gateway.Version, GrantUserID: actorID, Model: gateway.DefaultModel,
	}, workspaceID, grant.Tokens)
	if err != nil {
		return corecontract.CompleteWorkspaceLLMGatewayAuthorizationResponse{}, errors.Join(err, service.failClaimedOnly(ctx, claimed, "token_sealing_failed"))
	}
	grantID, err := service.allocateID("gateway grant")
	if err != nil {
		return corecontract.CompleteWorkspaceLLMGatewayAuthorizationResponse{}, errors.Join(err, service.failClaimedOnly(ctx, claimed, "grant_identity_failed"))
	}
	stored, err := service.store.CompleteWorkspaceLLMGatewayAuthTransaction(ctx, coredb.CompleteWorkspaceLLMGatewayAuthTransactionCommand{
		TransactionID: claimed.ID, ExpectedVersion: claimed.Version, GrantID: grantID,
		OIDCIssuer: grant.Issuer, OIDCSubject: grant.Subject, SealedTokenSet: sealedTokens,
		BearerExpiresAt: bearerExpiry.UTC(),
	})
	if err != nil {
		return corecontract.CompleteWorkspaceLLMGatewayAuthorizationResponse{}, err
	}
	return corecontract.CompleteWorkspaceLLMGatewayAuthorizationResponse{
		GatewayID: gatewayID, GrantStatus: stored.Status, BearerExpiresAt: stored.BearerExpiresAt.UTC(),
	}, nil
}

func (service *WorkspaceLLMGatewayService) RevokeGrant(
	ctx context.Context,
	workspaceID, gatewayID, actorID string,
) (corecontract.RevokeWorkspaceLLMGatewayGrantResponse, error) {
	if service == nil {
		return corecontract.RevokeWorkspaceLLMGatewayGrantResponse{}, errors.New("workspace LLM gateway service is unavailable")
	}
	result, err := service.store.RevokeWorkspaceLLMGatewayGrant(ctx, coredb.RevokeWorkspaceLLMGatewayGrantCommand{
		WorkspaceID: workspaceID, GatewayID: gatewayID, UserID: actorID,
	})
	if err != nil {
		return corecontract.RevokeWorkspaceLLMGatewayGrantResponse{}, err
	}
	return corecontract.RevokeWorkspaceLLMGatewayGrantResponse{
		GatewayID: gatewayID, GrantStatus: result.Grant.Status, Changed: result.Changed,
	}, nil
}

func (service *WorkspaceLLMGatewayService) DisableGateway(
	ctx context.Context,
	workspaceID, gatewayID, actorID string,
) (corecontract.DisableWorkspaceLLMGatewayResponse, error) {
	if service == nil {
		return corecontract.DisableWorkspaceLLMGatewayResponse{}, errors.New("workspace LLM gateway service is unavailable")
	}
	result, err := service.store.DisableWorkspaceLLMGateway(ctx, coredb.DisableWorkspaceLLMGatewayCommand{
		WorkspaceID: workspaceID, GatewayID: gatewayID, ActorID: actorID,
	})
	if err != nil {
		return corecontract.DisableWorkspaceLLMGatewayResponse{}, err
	}
	return corecontract.DisableWorkspaceLLMGatewayResponse{
		GatewayID: result.Gateway.ID, Status: result.Gateway.Status,
		Version: result.Gateway.Version, Changed: result.Changed,
	}, nil
}

// ResolveUpstream is called only after Core has live-authorized the run and
// returned the exact gateway/grant projection from the same PostgreSQL
// snapshot. It refreshes only when needed and never returns a token for a
// different workspace user or gateway version.
func (service *WorkspaceLLMGatewayService) ResolveUpstream(
	ctx context.Context,
	authority coredb.LLMGatewayLiveAuthority,
) (LLMGatewayUpstreamAuthorization, error) {
	if service == nil {
		return LLMGatewayUpstreamAuthorization{}, errors.New("workspace LLM gateway service is unavailable")
	}
	binding := coredb.RunLLMGatewayBinding{
		GatewayID: authority.Gateway.ID, ConfigVersion: authority.Gateway.Version,
		GrantUserID: authority.Grant.UserID, Model: authority.Model,
	}
	if err := validateLLMGatewayLiveProjection(authority, binding); err != nil {
		service.logGatewayResolutionFailure("live_projection")
		return LLMGatewayUpstreamAuthorization{}, err
	}
	tokens, err := service.openTokenSet(authority, binding)
	if err != nil {
		service.markGrantReauthRequired(ctx, authority.Grant)
		service.logGatewayResolutionFailure("sealed_token_open")
		return LLMGatewayUpstreamAuthorization{}, err
	}
	bearer, expiresAt, err := workspaceLLMGatewayBearer(tokens, authority.Gateway.BearerTokenType)
	if err != nil || !sameStoredLLMGatewayTimestamp(expiresAt, authority.Grant.BearerExpiresAt) {
		service.markGrantReauthRequired(ctx, authority.Grant)
		service.logGatewayResolutionFailure("sealed_token_metadata")
		return LLMGatewayUpstreamAuthorization{}, errors.New("sealed workspace LLM gateway token set contradicts grant metadata")
	}
	now := service.now().UTC()
	if !expiresAt.After(now.Add(service.refreshSkew)) {
		bearer, expiresAt, authority, err = service.refreshGrant(ctx, authority, binding, tokens)
		if err != nil {
			return LLMGatewayUpstreamAuthorization{}, err
		}
	}
	if bearer == "" || !expiresAt.After(now) || strings.ContainsAny(bearer, "\x00\r\n") {
		service.logGatewayResolutionFailure("resolved_bearer")
		return LLMGatewayUpstreamAuthorization{}, errors.New("workspace LLM gateway bearer is unavailable")
	}
	if _, err := publichttps.ValidateURL(authority.Gateway.ResponsesURL, "/v1/responses"); err != nil {
		service.logGatewayResolutionFailure("responses_url")
		return LLMGatewayUpstreamAuthorization{}, errors.New("workspace LLM gateway Responses endpoint is no longer safe")
	}
	return LLMGatewayUpstreamAuthorization{
		GatewayID: authority.Gateway.ID, GatewayConfigVersion: authority.Gateway.Version,
		GrantUserID: authority.Grant.UserID, Model: authority.Model,
		ResponsesURL: authority.Gateway.ResponsesURL, Authorization: "Bearer " + bearer,
		BearerExpiresAt: expiresAt.UTC(),
	}, nil
}

func (service *WorkspaceLLMGatewayService) refreshGrant(
	ctx context.Context,
	authority coredb.LLMGatewayLiveAuthority,
	binding coredb.RunLLMGatewayBinding,
	current WorkspaceLLMGatewayOIDCTokenSet,
) (string, time.Time, coredb.LLMGatewayLiveAuthority, error) {
	provider, err := service.discoverGatewayProvider(ctx, authority.Gateway)
	if err != nil {
		service.markGrantReauthRequired(ctx, authority.Grant)
		service.logGatewayResolutionFailure("oidc_discovery")
		return "", time.Time{}, coredb.LLMGatewayLiveAuthority{}, errors.New("workspace LLM gateway OIDC refresh authority is unavailable")
	}
	refreshed, err := provider.Refresh(ctx, current, authority.Grant.OIDCSubject, authority.Gateway.BearerTokenType)
	if err != nil || refreshed.Issuer != authority.Gateway.OIDCIssuer || refreshed.Subject != authority.Grant.OIDCSubject {
		service.markGrantReauthRequired(ctx, authority.Grant)
		service.logGatewayResolutionFailure("oidc_refresh")
		return "", time.Time{}, coredb.LLMGatewayLiveAuthority{}, errors.New("workspace LLM gateway OIDC grant requires reauthorization")
	}
	bearer, expiry, err := workspaceLLMGatewayBearer(refreshed.Tokens, authority.Gateway.BearerTokenType)
	if err != nil || !expiry.After(service.now().UTC()) {
		service.markGrantReauthRequired(ctx, authority.Grant)
		service.logGatewayResolutionFailure("refreshed_bearer")
		return "", time.Time{}, coredb.LLMGatewayLiveAuthority{}, errors.New("workspace LLM gateway refreshed bearer is invalid")
	}
	sealed, err := service.sealTokenSet(binding, authority.Gateway.WorkspaceID, refreshed.Tokens)
	if err != nil {
		service.logGatewayResolutionFailure("refreshed_token_seal")
		return "", time.Time{}, coredb.LLMGatewayLiveAuthority{}, err
	}
	updated, err := service.store.UpdateWorkspaceLLMGatewayGrantTokens(
		ctx, authority.Grant.ID, authority.Grant.Version, sealed, expiry.UTC(),
	)
	if err == nil {
		authority.Grant = updated
		return bearer, expiry.UTC(), authority, nil
	}
	if !coredb.HasStateErrorCode(err, coredb.ErrorVersionConflict) {
		service.logGatewayResolutionFailure("refreshed_token_persist")
		return "", time.Time{}, coredb.LLMGatewayLiveAuthority{}, err
	}
	// Another Core may have refreshed, revoked, or fenced this grant. Re-read
	// the complete live authority and accept only its exact active projection.
	raced, readErr := service.store.ReadWorkspaceLLMGatewayLiveAuthority(ctx, authority.Gateway.WorkspaceID, binding)
	if readErr != nil {
		service.logGatewayResolutionFailure("refresh_race_read")
		return "", time.Time{}, coredb.LLMGatewayLiveAuthority{}, readErr
	}
	latest, openErr := service.openTokenSet(raced, binding)
	if openErr != nil {
		service.logGatewayResolutionFailure("refresh_race_token_open")
		return "", time.Time{}, coredb.LLMGatewayLiveAuthority{}, openErr
	}
	bearer, expiry, openErr = workspaceLLMGatewayBearer(latest, raced.Gateway.BearerTokenType)
	if openErr != nil || !expiry.After(service.now().UTC()) || !sameStoredLLMGatewayTimestamp(expiry, raced.Grant.BearerExpiresAt) {
		service.logGatewayResolutionFailure("refresh_race_bearer")
		return "", time.Time{}, coredb.LLMGatewayLiveAuthority{}, errors.New("raced workspace LLM gateway grant is not usable")
	}
	return bearer, expiry.UTC(), raced, nil
}

// logGatewayResolutionFailure records only a fixed implementation stage. OIDC
// provider errors, endpoints, subjects, token material, and user content are
// deliberately excluded.
func (service *WorkspaceLLMGatewayService) logGatewayResolutionFailure(stage string) {
	if service != nil && service.logger != nil {
		service.logger.Warn("workspace LLM gateway resolution did not complete", "stage", stage)
	}
}

func (service *WorkspaceLLMGatewayService) discoverGatewayProvider(
	ctx context.Context,
	gateway coredb.WorkspaceLLMGateway,
) (WorkspaceLLMGatewayOIDCProvider, error) {
	scopes := strings.Fields(gateway.OIDCScopes)
	return service.providers.Discover(ctx, WorkspaceLLMGatewayOIDCConfig{
		Issuer: gateway.OIDCIssuer, ClientID: gateway.OIDCClientID,
		Scopes: scopes, RedirectURL: service.redirectURL,
	})
}

func (service *WorkspaceLLMGatewayService) sealTokenSet(
	binding coredb.RunLLMGatewayBinding,
	workspaceID string,
	tokens WorkspaceLLMGatewayOIDCTokenSet,
) ([]byte, error) {
	if err := validateWorkspaceLLMGatewayOIDCTokenSet(tokens); err != nil {
		return nil, err
	}
	// PostgreSQL timestamptz stores microseconds while oauth2 derives access
	// token expiry from time.Now and therefore commonly retains nanoseconds.
	// Seal the database-representable value so the ciphertext and its indexed
	// live-authority projection remain stable across a database round trip.
	tokens.AccessTokenExpiresAt = canonicalStoredLLMGatewayTimestamp(tokens.AccessTokenExpiresAt)
	tokens.IDTokenExpiresAt = canonicalStoredLLMGatewayTimestamp(tokens.IDTokenExpiresAt)
	raw, err := json.Marshal(tokens)
	if err != nil {
		return nil, errors.New("encode workspace LLM gateway token set")
	}
	defer clear(raw)
	return service.sealer.SealGrantTokenSet(LLMGatewaySealScope{
		WorkspaceID: workspaceID, GatewayID: binding.GatewayID,
		UserID: binding.GrantUserID, GatewayVersion: binding.ConfigVersion,
	}, raw)
}

func canonicalStoredLLMGatewayTimestamp(value time.Time) time.Time {
	return value.UTC().Truncate(time.Microsecond)
}

// sameStoredLLMGatewayTimestamp compares at PostgreSQL's representable
// timestamptz precision. This accepts legacy grants sealed before expiries were
// canonicalized, while still rejecting any metadata change of one microsecond
// or more.
func sameStoredLLMGatewayTimestamp(sealed, stored time.Time) bool {
	if sealed.IsZero() || stored.IsZero() {
		return false
	}
	return canonicalStoredLLMGatewayTimestamp(sealed).Equal(canonicalStoredLLMGatewayTimestamp(stored))
}

func (service *WorkspaceLLMGatewayService) openTokenSet(
	authority coredb.LLMGatewayLiveAuthority,
	binding coredb.RunLLMGatewayBinding,
) (WorkspaceLLMGatewayOIDCTokenSet, error) {
	raw, err := service.sealer.OpenGrantTokenSet(LLMGatewaySealScope{
		WorkspaceID: authority.Gateway.WorkspaceID, GatewayID: binding.GatewayID,
		UserID: binding.GrantUserID, GatewayVersion: binding.ConfigVersion,
	}, authority.Grant.SealedTokenSet)
	if err != nil {
		return WorkspaceLLMGatewayOIDCTokenSet{}, err
	}
	defer clear(raw)
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var tokens WorkspaceLLMGatewayOIDCTokenSet
	if err := decoder.Decode(&tokens); err != nil {
		return WorkspaceLLMGatewayOIDCTokenSet{}, errors.New("sealed workspace LLM gateway token set is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return WorkspaceLLMGatewayOIDCTokenSet{}, errors.New("sealed workspace LLM gateway token set contains trailing JSON")
	}
	if err := validateWorkspaceLLMGatewayOIDCTokenSet(tokens); err != nil {
		return WorkspaceLLMGatewayOIDCTokenSet{}, errors.New("sealed workspace LLM gateway token set is outside protocol bounds")
	}
	return tokens, nil
}

func (service *WorkspaceLLMGatewayService) openAuthorizationSecrets(
	transaction coredb.WorkspaceLLMGatewayAuthTransaction,
) (workspaceLLMGatewayAuthorizationSecrets, error) {
	raw, err := service.sealer.OpenAuthorizationSecrets(LLMGatewaySealScope{
		WorkspaceID: transaction.WorkspaceID, GatewayID: transaction.GatewayID,
		UserID: transaction.UserID, GatewayVersion: transaction.GatewayVersion,
		TransactionID: transaction.ID,
	}, transaction.SealedSecrets)
	if err != nil {
		return workspaceLLMGatewayAuthorizationSecrets{}, err
	}
	defer clear(raw)
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var secrets workspaceLLMGatewayAuthorizationSecrets
	if err := decoder.Decode(&secrets); err != nil {
		return workspaceLLMGatewayAuthorizationSecrets{}, errors.New("sealed workspace LLM gateway authorization secrets are invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return workspaceLLMGatewayAuthorizationSecrets{}, errors.New("sealed workspace LLM gateway authorization secrets contain trailing JSON")
	}
	if validateOIDCCorrelation(secrets.State, secrets.Nonce, secrets.PKCEVerifier) != nil ||
		validateOIDCSecret("browser binding", secrets.BrowserBinding) != nil ||
		sha256.Sum256([]byte(secrets.State)) != transaction.OIDCStateSHA256 ||
		sha256.Sum256([]byte(secrets.BrowserBinding)) != transaction.BrowserBindingSHA256 {
		return workspaceLLMGatewayAuthorizationSecrets{}, errors.New("sealed workspace LLM gateway authorization secrets contradict database correlation hashes")
	}
	return secrets, nil
}

func (service *WorkspaceLLMGatewayService) failClaimedAuthorization(
	ctx context.Context,
	transaction coredb.WorkspaceLLMGatewayAuthTransaction,
	failureCode string,
) error {
	err := service.failClaimedOnly(ctx, transaction, failureCode)
	return errors.Join(llmGatewayStateError(coredb.ErrorForbidden, "CompleteWorkspaceLLMGatewayAuthorization", transaction.GatewayID, "gateway authorization was denied"), err)
}

func (service *WorkspaceLLMGatewayService) failClaimedOnly(
	ctx context.Context,
	transaction coredb.WorkspaceLLMGatewayAuthTransaction,
	failureCode string,
) error {
	_, err := service.store.FailWorkspaceLLMGatewayAuthTransaction(ctx, coredb.FailWorkspaceLLMGatewayAuthTransactionCommand{
		TransactionID: transaction.ID, ExpectedVersion: transaction.Version,
		Status: coredb.LLMGatewayAuthStatusFailed, FailureCode: failureCode,
	})
	return err
}

func (service *WorkspaceLLMGatewayService) markGrantReauthRequired(ctx context.Context, grant coredb.WorkspaceLLMGatewayGrant) {
	_ = service.store.MarkWorkspaceLLMGatewayGrantReauthRequired(ctx, grant.ID, grant.Version)
}

func (service *WorkspaceLLMGatewayService) allocateID(label string) (string, error) {
	value, err := service.newID()
	if err != nil || !canonicalPublicUUID(value) {
		return "", fmt.Errorf("allocate canonical workspace LLM gateway %s identity", label)
	}
	return value, nil
}

func (service *WorkspaceLLMGatewayService) randomSecret(label string) (string, error) {
	raw := make([]byte, 32)
	if _, err := io.ReadFull(service.random, raw); err != nil {
		return "", fmt.Errorf("generate workspace LLM gateway OIDC %s: %w", label, err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func validateLLMGatewayLiveProjection(authority coredb.LLMGatewayLiveAuthority, binding coredb.RunLLMGatewayBinding) error {
	if authority.Gateway.ID != binding.GatewayID || authority.Gateway.WorkspaceID == "" ||
		authority.Gateway.Version != binding.ConfigVersion || authority.Gateway.Status != coredb.LLMGatewayStatusActive ||
		authority.Gateway.DefaultModel != binding.Model || authority.Model != binding.Model ||
		authority.Grant.GatewayID != binding.GatewayID || authority.Grant.WorkspaceID != authority.Gateway.WorkspaceID ||
		authority.Grant.UserID != binding.GrantUserID || authority.Grant.Status != coredb.LLMGatewayGrantStatusActive ||
		authority.Grant.OIDCIssuer != authority.Gateway.OIDCIssuer || authority.Grant.Version < 1 ||
		len(authority.Grant.SealedTokenSet) < 29 {
		return errors.New("Core returned an inconsistent workspace LLM gateway live authority projection")
	}
	return nil
}

func contractWorkspaceLLMGateway(source coredb.WorkspaceLLMGateway) corecontract.WorkspaceLLMGatewayState {
	result := corecontract.WorkspaceLLMGatewayState{
		GatewayID: source.ID, WorkspaceID: source.WorkspaceID, Name: source.Name,
		ResponsesURL: source.ResponsesURL, OIDCIssuer: source.OIDCIssuer,
		OIDCClientID: source.OIDCClientID, OIDCScopes: strings.Fields(source.OIDCScopes),
		BearerTokenType: source.BearerTokenType, DefaultModel: source.DefaultModel,
		Status: source.Status, Default: source.Default, Version: source.Version,
		GrantStatus: source.GrantStatus, CreatedAt: source.CreatedAt.UTC(), UpdatedAt: source.UpdatedAt.UTC(),
	}
	if source.GrantExpiresAt != nil {
		expiresAt := source.GrantExpiresAt.UTC()
		result.GrantExpiresAt = &expiresAt
	}
	return result
}

func validWorkspaceLLMGatewayPublicText(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\x00\r\n")
}

func llmGatewayStateError(code coredb.StateErrorCode, operation, gatewayID, message string) error {
	return &coredb.StateError{Code: code, Operation: operation, Resource: "llm_gateway", ResourceID: gatewayID, Message: message}
}

type workspaceLLMGatewayAuthorizationSecrets struct {
	State          string `json:"state"`
	Nonce          string `json:"nonce"`
	PKCEVerifier   string `json:"pkceVerifier"`
	BrowserBinding string `json:"browserBinding"`
}
