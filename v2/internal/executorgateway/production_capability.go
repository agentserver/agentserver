package executorgateway

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/agentserver/agentserver/v2/internal/runcapability"
)

type ExecutorRunCapabilityAuthorizationRequest struct {
	Token                     string
	CapabilityID              string
	ExecutorID                string
	ToolCatalogDigest         string
	RunID                     string
	RunAttemptID              string
	RunAttemptGeneration      int64
	ExpectedRunVersion        int64
	ExpectedRunAttemptVersion int64
}

type ExecutorRunCapabilityAuthorization struct {
	CapabilityID         string
	Audience             string
	RunID                string
	RunAttemptID         string
	RunAttemptGeneration int64
	RunVersion           int64
	RunAttemptVersion    int64
	AuthorizedAt         time.Time
}

type ExecutorRunCapabilityAuthorizer interface {
	AuthorizeExecutorRunCapability(context.Context, ExecutorRunCapabilityAuthorizationRequest) (ExecutorRunCapabilityAuthorization, error)
}

// ProductionExecutorMCPAuthenticator performs both required authorization
// layers for every MCP HTTP request: local Ed25519 verification first, then a
// Core live-authorize call. Core unavailability is always a denial; there is
// no offline fallback or positive-result cache.
type ProductionExecutorMCPAuthenticator struct {
	verifier   *runcapability.ProductionVerifier
	authorizer ExecutorRunCapabilityAuthorizer
	executorID string
	now        func() time.Time
}

func NewProductionExecutorMCPAuthenticator(
	verifier *runcapability.ProductionVerifier,
	authorizer ExecutorRunCapabilityAuthorizer,
	executorID string,
	now func() time.Time,
) (*ProductionExecutorMCPAuthenticator, error) {
	if verifier == nil || verifier.Issuer() == "" {
		return nil, errors.New("production executor MCP capability verifier is required")
	}
	if authorizer == nil {
		return nil, errors.New("production executor MCP live authorizer is required")
	}
	if err := validateRegistryIdentity("production executor ID", executorID); err != nil {
		return nil, err
	}
	if now == nil {
		return nil, errors.New("production executor MCP clock is required")
	}
	return &ProductionExecutorMCPAuthenticator{
		verifier: verifier, authorizer: authorizer, executorID: executorID, now: now,
	}, nil
}

func (authenticator *ProductionExecutorMCPAuthenticator) AuthenticateExecutorMCP(
	request *http.Request,
) (ExecutorMCPPrincipal, error) {
	if request == nil || authenticator == nil || authenticator.verifier == nil ||
		authenticator.authorizer == nil || authenticator.now == nil {
		return ExecutorMCPPrincipal{}, errors.New("production executor MCP authenticator is unavailable")
	}
	token, err := exactExecutorMCPBearer(request)
	if err != nil {
		return ExecutorMCPPrincipal{}, err
	}
	now := authenticator.now().UTC()
	claims, err := authenticator.verifier.Verify(token, runcapability.AudienceExecutorMCP, now)
	if err != nil {
		return ExecutorMCPPrincipal{}, errors.New("production executor MCP capability verification failed")
	}
	if claims.ExecutorID != authenticator.executorID || now.UnixMilli() >= claims.RunDeadlineUnixMS {
		return ExecutorMCPPrincipal{}, errors.New("production executor MCP capability is outside its configured authority")
	}
	authorized, err := authenticator.authorizer.AuthorizeExecutorRunCapability(
		request.Context(),
		ExecutorRunCapabilityAuthorizationRequest{
			Token: token, CapabilityID: claims.CapabilityID,
			ExecutorID: claims.ExecutorID, ToolCatalogDigest: claims.ToolCatalogDigest,
			RunID: claims.RunID, RunAttemptID: claims.RunAttemptID,
			RunAttemptGeneration:      claims.RunAttemptGeneration,
			ExpectedRunVersion:        claims.ExpectedRunVersion,
			ExpectedRunAttemptVersion: claims.ExpectedRunAttemptVersion,
		},
	)
	if err != nil {
		return ExecutorMCPPrincipal{}, errors.New("production executor MCP capability is not live-authorized")
	}
	if !authorizedExecutorCapabilityResponseMatchesClaims(authorized, claims) {
		return ExecutorMCPPrincipal{}, errors.New("Core returned inconsistent production executor MCP authority")
	}
	return ExecutorMCPPrincipal{
		CapabilityID: claims.CapabilityID, WorkspaceID: claims.WorkspaceID, SessionID: claims.SessionID,
		ActorID: claims.ActorID, ExecutorID: claims.ExecutorID,
		ToolCatalogDigest:   claims.ToolCatalogDigest,
		MaxApprovalTTL:      time.Duration(claims.MaxApprovalTTLMillis) * time.Millisecond,
		RunDeadline:         time.UnixMilli(claims.RunDeadlineUnixMS).UTC(),
		CapabilityExpiresAt: time.UnixMilli(claims.ExpiresAtUnixMS).UTC(),
		Production:          true,
		Run: ExecutorMCPRunContext{
			RunID: claims.RunID, RunAttemptID: claims.RunAttemptID,
			RunAttemptGeneration: claims.RunAttemptGeneration, HolderID: claims.HolderID,
			ExpectedRunVersion:        claims.ExpectedRunVersion,
			ExpectedRunAttemptVersion: claims.ExpectedRunAttemptVersion,
		},
	}, nil
}

func exactExecutorMCPBearer(request *http.Request) (string, error) {
	values := request.Header.Values("Authorization")
	if len(values) != 1 || strings.Contains(values[0], ",") || !strings.HasPrefix(values[0], "Bearer ") {
		return "", errors.New("production executor MCP capability is missing")
	}
	token := strings.TrimPrefix(values[0], "Bearer ")
	if token == "" || strings.TrimSpace(token) != token || len(token) > 16*1024 {
		return "", errors.New("production executor MCP capability framing is invalid")
	}
	return token, nil
}

func authorizedExecutorCapabilityResponseMatchesClaims(
	authorized ExecutorRunCapabilityAuthorization,
	claims runcapability.Claims,
) bool {
	if authorized.CapabilityID != claims.CapabilityID || authorized.Audience != runcapability.AudienceExecutorMCP ||
		authorized.RunID != claims.RunID || authorized.RunAttemptID != claims.RunAttemptID ||
		authorized.RunAttemptGeneration != claims.RunAttemptGeneration || authorized.AuthorizedAt.IsZero() {
		return false
	}
	acceptedVersions := authorized.RunVersion == claims.ExpectedRunVersion &&
		authorized.RunAttemptVersion == claims.ExpectedRunAttemptVersion
	preTurnVersions := authorized.RunVersion > 0 && authorized.RunAttemptVersion > 0 &&
		authorized.RunVersion+1 == claims.ExpectedRunVersion &&
		authorized.RunAttemptVersion+1 == claims.ExpectedRunAttemptVersion
	return acceptedVersions || preTurnVersions
}
