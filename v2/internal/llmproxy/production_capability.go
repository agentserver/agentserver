// Package llmproxy owns the agentserver-facing authorization and forwarding
// boundary in front of a production model provider.
package llmproxy

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/runcapability"
)

const maximumRunCapabilityBytes = 16 * 1024

var (
	ErrUnauthenticated = errors.New("llmproxy request is unauthenticated")
	ErrForbidden       = errors.New("llmproxy request is forbidden")
)

type RunCapabilityAuthorizationRequest struct {
	Token                 string
	CapabilityID          string
	Model                 string
	Provider              string
	LLMGatewayID          string
	LLMGatewayVersion     int64
	LLMGatewayGrantUserID string
	RunID                 string
	RunAttemptID          string
	RunAttemptGeneration  int64
}

type RunCapabilityAuthorization struct {
	CapabilityID          string
	Audience              string
	RunID                 string
	RunAttemptID          string
	RunAttemptGeneration  int64
	RunVersion            int64
	RunAttemptVersion     int64
	AuthorizedAt          time.Time
	Model                 string
	Provider              string
	LLMGatewayID          string
	LLMGatewayVersion     int64
	LLMGatewayGrantUserID string
	ResponsesURL          string
	UpstreamAuthorization string
	BearerExpiresAt       time.Time
}

type RunCapabilityAuthorizer interface {
	AuthorizeLLMProxyRunCapability(context.Context, RunCapabilityAuthorizationRequest) (RunCapabilityAuthorization, error)
}

type Principal struct {
	CapabilityID          string
	WorkspaceID           string
	SessionID             string
	RunID                 string
	RunAttemptID          string
	RunAttemptGeneration  int64
	ActorID               string
	HolderID              string
	Model                 string
	Provider              string
	LLMGatewayID          string
	LLMGatewayVersion     int64
	LLMGatewayGrantUserID string
	ResponsesURL          string
	UpstreamAuthorization string
	BearerExpiresAt       time.Time
	RunDeadline           time.Time
	CapabilityExpiresAt   time.Time
	AuthorizedAt          time.Time
}

// ProductionAuthenticator applies both authorization layers to every model
// HTTP request: local Ed25519 verification first and a Core live-authorize
// decision second. It deliberately has no positive cache or offline fallback.
type ProductionAuthenticator struct {
	verifier   *runcapability.ProductionVerifier
	authorizer RunCapabilityAuthorizer
	now        func() time.Time
}

func NewProductionAuthenticator(
	verifier *runcapability.ProductionVerifier,
	authorizer RunCapabilityAuthorizer,
	now func() time.Time,
) (*ProductionAuthenticator, error) {
	if verifier == nil || verifier.Issuer() == "" {
		return nil, errors.New("production llmproxy capability verifier is required")
	}
	if authorizer == nil {
		return nil, errors.New("production llmproxy live authorizer is required")
	}
	if now == nil {
		return nil, errors.New("production llmproxy clock is required")
	}
	return &ProductionAuthenticator{
		verifier: verifier, authorizer: authorizer, now: now,
	}, nil
}

func (authenticator *ProductionAuthenticator) AuthenticateModelRequest(
	request *http.Request,
	model string,
) (Principal, error) {
	if request == nil || authenticator == nil || authenticator.verifier == nil ||
		authenticator.authorizer == nil || authenticator.now == nil {
		return Principal{}, errors.New("production llmproxy authenticator is unavailable")
	}
	token, err := exactBearer(request.Header)
	if err != nil {
		return Principal{}, ErrUnauthenticated
	}
	now := authenticator.now().UTC()
	claims, err := authenticator.verifier.Verify(token, runcapability.AudienceLLMProxy, now)
	if err != nil {
		return Principal{}, ErrUnauthenticated
	}
	if claims.Model != model || claims.Provider != corecontract.WorkspaceLLMGatewayProvider ||
		now.UnixMilli() >= claims.RunDeadlineUnixMS {
		return Principal{}, ErrForbidden
	}
	authorized, err := authenticator.authorizer.AuthorizeLLMProxyRunCapability(
		request.Context(),
		RunCapabilityAuthorizationRequest{
			Token: token, CapabilityID: claims.CapabilityID,
			Model: claims.Model, Provider: claims.Provider,
			LLMGatewayID: claims.LLMGatewayID, LLMGatewayVersion: claims.LLMGatewayVersion,
			LLMGatewayGrantUserID: claims.LLMGatewayGrantUserID,
			RunID:                 claims.RunID, RunAttemptID: claims.RunAttemptID,
			RunAttemptGeneration: claims.RunAttemptGeneration,
		},
	)
	if err != nil {
		return Principal{}, ErrForbidden
	}
	if !authorizationMatchesClaims(authorized, claims) {
		return Principal{}, ErrForbidden
	}
	return Principal{
		CapabilityID: claims.CapabilityID, WorkspaceID: claims.WorkspaceID,
		SessionID: claims.SessionID, RunID: claims.RunID,
		RunAttemptID: claims.RunAttemptID, RunAttemptGeneration: claims.RunAttemptGeneration,
		ActorID: claims.ActorID, HolderID: claims.HolderID,
		Model: claims.Model, Provider: claims.Provider,
		LLMGatewayID: claims.LLMGatewayID, LLMGatewayVersion: claims.LLMGatewayVersion,
		LLMGatewayGrantUserID: claims.LLMGatewayGrantUserID,
		ResponsesURL:          authorized.ResponsesURL, UpstreamAuthorization: authorized.UpstreamAuthorization,
		BearerExpiresAt:     authorized.BearerExpiresAt.UTC(),
		RunDeadline:         time.UnixMilli(claims.RunDeadlineUnixMS).UTC(),
		CapabilityExpiresAt: time.UnixMilli(claims.ExpiresAtUnixMS).UTC(),
		AuthorizedAt:        authorized.AuthorizedAt.UTC(),
	}, nil
}

func exactBearer(header http.Header) (string, error) {
	values := header.Values("Authorization")
	if len(values) != 1 || strings.Contains(values[0], ",") || !strings.HasPrefix(values[0], "Bearer ") {
		return "", errors.New("production llmproxy capability is missing")
	}
	token := strings.TrimPrefix(values[0], "Bearer ")
	if token == "" || strings.TrimSpace(token) != token || len(token) > maximumRunCapabilityBytes {
		return "", errors.New("production llmproxy capability framing is invalid")
	}
	return token, nil
}

func authorizationMatchesClaims(authorized RunCapabilityAuthorization, claims runcapability.Claims) bool {
	return authorized.CapabilityID == claims.CapabilityID &&
		authorized.Audience == runcapability.AudienceLLMProxy &&
		authorized.RunID == claims.RunID &&
		authorized.RunAttemptID == claims.RunAttemptID &&
		authorized.RunAttemptGeneration == claims.RunAttemptGeneration &&
		authorized.Model == claims.Model && authorized.Provider == claims.Provider &&
		authorized.LLMGatewayID == claims.LLMGatewayID &&
		authorized.LLMGatewayVersion == claims.LLMGatewayVersion &&
		authorized.LLMGatewayGrantUserID == claims.LLMGatewayGrantUserID &&
		authorized.ResponsesURL != "" && authorized.UpstreamAuthorization != "" &&
		!authorized.BearerExpiresAt.IsZero() &&
		safeVersion(authorized.RunVersion) && safeVersion(authorized.RunAttemptVersion) &&
		!authorized.AuthorizedAt.IsZero()
}

func safeVersion(value int64) bool {
	return value >= 1 && value <= 1<<53-1
}

func validRouteText(value string) bool {
	if value == "" || len(value) > 256 || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}
