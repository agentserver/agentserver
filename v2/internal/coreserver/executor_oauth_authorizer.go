package coreserver

import (
	"context"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"math/big"
	"net/url"
	"strings"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/coredb"
	"github.com/agentserver/agentserver/v2/internal/executorenrollment"
)

type ExecutorOAuthAuthorityStore interface {
	AuthorizeExecutorOAuthClient(context.Context, string) (coredb.ExecutorMachineAuthority, error)
}

type ExecutorOAuthAuthorizerConfig struct {
	Introspector   UserTokenIntrospector
	Store          ExecutorOAuthAuthorityStore
	Hydra          HydraExecutorClientReader
	ExpectedIssuer string
	Now            func() time.Time
}

type HydraExecutorClientReader interface {
	GetExecutorOAuthClient(context.Context, string) (HydraExecutorOAuthClient, error)
}

type ExecutorOAuthAuthorizer struct {
	introspector UserTokenIntrospector
	store        ExecutorOAuthAuthorityStore
	hydra        HydraExecutorClientReader
	issuer       string
	now          func() time.Time
}

func NewExecutorOAuthAuthorizer(config ExecutorOAuthAuthorizerConfig) (*ExecutorOAuthAuthorizer, error) {
	if config.Introspector == nil || config.Store == nil || config.Hydra == nil {
		return nil, errors.New("executor OAuth introspector, machine authority store, and Hydra client reader are required")
	}
	issuer, err := url.Parse(config.ExpectedIssuer)
	if err != nil || (issuer.Scheme != "https" && issuer.Scheme != "http") || issuer.Host == "" || issuer.User != nil ||
		issuer.RawQuery != "" || issuer.Fragment != "" || issuer.String() != config.ExpectedIssuer ||
		len(config.ExpectedIssuer) > 2048 || strings.ContainsAny(config.ExpectedIssuer, "\x00\r\n") {
		return nil, errors.New("executor OAuth issuer must be an exact absolute HTTP(S) issuer URL")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &ExecutorOAuthAuthorizer{
		introspector: config.Introspector, store: config.Store, hydra: config.Hydra,
		issuer: config.ExpectedIssuer, now: config.Now,
	}, nil
}

func (authorizer *ExecutorOAuthAuthorizer) Authorize(ctx context.Context, bearer string) (corecontract.AuthorizeExecutorConnectionResponse, error) {
	if authorizer == nil || authorizer.introspector == nil || authorizer.store == nil || authorizer.hydra == nil || authorizer.issuer == "" || authorizer.now == nil {
		return corecontract.AuthorizeExecutorConnectionResponse{}, errors.New("executor OAuth authorizer is unavailable")
	}
	if bearer == "" || len(bearer) > 8192 || strings.ContainsAny(bearer, " \t\x00\r\n") {
		return corecontract.AuthorizeExecutorConnectionResponse{}, &coredb.StateError{
			Code: coredb.ErrorForbidden, Operation: "AuthorizeExecutorConnection", Resource: "executor_oauth_token", Message: "OAuth bearer is invalid",
		}
	}
	introspection, err := authorizer.introspector.IntrospectUserToken(ctx, bearer)
	if err != nil {
		return corecontract.AuthorizeExecutorConnectionResponse{}, errors.New("Hydra executor token introspection failed")
	}
	now := authorizer.now().UTC()
	maximumLifetimeSeconds := int64(ExecutorOAuthAccessTokenLifespan / time.Second)
	if !introspection.Active || introspection.ExpiresAt <= now.Unix() ||
		len(introspection.Audience) != 1 || introspection.Audience[0] != ExecutorOAuthAudience ||
		introspection.Scope != ExecutorOAuthScope || introspection.Issuer != authorizer.issuer ||
		introspection.TokenType != "Bearer" || introspection.TokenUse != "access_token" ||
		introspection.IssuedAt <= 0 || introspection.NotBefore != introspection.IssuedAt ||
		introspection.IssuedAt > now.Add(30*time.Second).Unix() || introspection.ExpiresAt <= introspection.IssuedAt ||
		introspection.ExpiresAt-introspection.IssuedAt > maximumLifetimeSeconds ||
		introspection.ClientID == "" || len(introspection.ClientID) > 128 || strings.TrimSpace(introspection.ClientID) != introspection.ClientID ||
		introspection.Subject != introspection.ClientID {
		return corecontract.AuthorizeExecutorConnectionResponse{}, &coredb.StateError{
			Code: coredb.ErrorForbidden, Operation: "AuthorizeExecutorConnection", Resource: "executor_oauth_token", Message: "OAuth token is outside executor connection authority",
		}
	}
	authority, err := authorizer.store.AuthorizeExecutorOAuthClient(ctx, introspection.ClientID)
	if err != nil {
		return corecontract.AuthorizeExecutorConnectionResponse{}, err
	}
	oauthX := new(big.Int).SetBytes(authority.OAuthPublicKeyP256X[:])
	oauthY := new(big.Int).SetBytes(authority.OAuthPublicKeyP256Y[:])
	oauthFingerprint := executorenrollment.OAuthJWKThumbprint(
		base64.RawURLEncoding.EncodeToString(authority.OAuthPublicKeyP256X[:]),
		base64.RawURLEncoding.EncodeToString(authority.OAuthPublicKeyP256Y[:]),
	)
	if authority.OAuthClientID != introspection.ClientID || authority.OAuthClientID != "agentserver-executor-"+authority.ExecutorID ||
		authority.ExecutorVersion < 1 || authority.AuthorizedAt.IsZero() || authority.MachinePublicKeyEd25519 == [32]byte{} ||
		authority.MachineKeySHA256 != sha256.Sum256(authority.MachinePublicKeyEd25519[:]) ||
		!elliptic.P256().IsOnCurve(oauthX, oauthY) || authority.OAuthKeySHA256 != oauthFingerprint {
		return corecontract.AuthorizeExecutorConnectionResponse{}, errors.New("Core returned inconsistent executor machine authority")
	}
	expectedClient := executorOAuthClientDocument(
		authority.OAuthClientID, authority.ExecutorID,
		authority.OAuthPublicKeyP256X, authority.OAuthPublicKeyP256Y, authority.OAuthKeySHA256,
	)
	actualClient, err := authorizer.hydra.GetExecutorOAuthClient(ctx, authority.OAuthClientID)
	if err != nil {
		var adminError *HydraAdminError
		if errors.As(err, &adminError) && adminError.StatusCode == 404 {
			return corecontract.AuthorizeExecutorConnectionResponse{}, &coredb.StateError{
				Code: coredb.ErrorForbidden, Operation: "AuthorizeExecutorConnection", Resource: "executor_oauth_client",
				ResourceID: authority.OAuthClientID, Message: "Hydra executor OAuth client no longer exists",
			}
		}
		return corecontract.AuthorizeExecutorConnectionResponse{}, errors.New("Hydra executor OAuth client reconciliation failed")
	}
	if !equalHydraExecutorClient(actualClient, expectedClient) {
		return corecontract.AuthorizeExecutorConnectionResponse{}, &coredb.StateError{
			Code: coredb.ErrorForbidden, Operation: "AuthorizeExecutorConnection", Resource: "executor_oauth_client",
			ResourceID: authority.OAuthClientID, Message: "Hydra executor OAuth client no longer matches enrolled authority",
		}
	}
	return corecontract.AuthorizeExecutorConnectionResponse{
		ExecutorID: authority.ExecutorID, WorkspaceID: authority.WorkspaceID, OAuthClientID: authority.OAuthClientID,
		MachinePublicKeyEd25519: base64.RawURLEncoding.EncodeToString(authority.MachinePublicKeyEd25519[:]),
		MachineKeySHA256:        hex.EncodeToString(authority.MachineKeySHA256[:]), ExecutorVersion: authority.ExecutorVersion,
		TokenExpiresAt: time.Unix(introspection.ExpiresAt, 0).UTC(), AuthorizedAt: authority.AuthorizedAt,
	}, nil
}
