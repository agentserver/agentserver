package coreserver

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/coredb"
	"github.com/agentserver/agentserver/v2/internal/publichttps"
)

func validWorkspaceLLMAPIKey(key string) bool {
	if len(key) < 1 || len(key) > 8192 {
		return false
	}
	for _, c := range []byte(key) {
		if c < 33 || c > 126 {
			return false
		}
	}
	return true
}

// The credential is authenticated to its workspace, gateway and destination.
// Changing a URL cannot silently redirect an existing encrypted API key.
func workspaceAPIKeyAAD(workspaceID, gatewayID, responsesURL string) ([]byte, error) {
	if !canonicalPublicUUID(workspaceID) || !canonicalPublicUUID(gatewayID) {
		return nil, errors.New("invalid workspace API key scope")
	}
	if _, err := publichttps.ValidateResponsesURL(responsesURL); err != nil {
		return nil, errors.New("invalid workspace API key destination")
	}
	return json.Marshal([]string{"agentserver-v2/workspace-llm-api-key/v1", workspaceID, gatewayID, responsesURL})
}

func (s *LLMGatewayGrantSealer) SealWorkspaceAPIKey(workspaceID, gatewayID, responsesURL, key string) ([]byte, error) {
	if !validWorkspaceLLMAPIKey(key) {
		return nil, errors.New("API key must be a non-empty bounded printable token without whitespace")
	}
	aad, err := workspaceAPIKeyAAD(workspaceID, gatewayID, responsesURL)
	if err != nil {
		return nil, err
	}
	raw := []byte(key)
	defer clear(raw)
	return s.sealEnvelope(raw, aad)
}

func (s *LLMGatewayGrantSealer) OpenWorkspaceAPIKey(workspaceID, gatewayID, responsesURL string, sealed []byte) ([]byte, error) {
	aad, err := workspaceAPIKeyAAD(workspaceID, gatewayID, responsesURL)
	if err != nil {
		return nil, err
	}
	raw, err := s.openEnvelope(sealed, aad)
	if err != nil {
		return nil, errors.New("workspace API key could not be decrypted")
	}
	if !validWorkspaceLLMAPIKey(string(raw)) {
		clear(raw)
		return nil, errors.New("stored workspace API key is invalid")
	}
	return raw, nil
}

func validateAPIKeyGatewayRequest(workspaceID, gatewayID, actorID, name, model, baseURL, responsesURL, issuer, clientID, bearer string, scopes []string) (string, error) {
	if !canonicalPublicUUID(workspaceID) || !canonicalPublicUUID(gatewayID) || !canonicalPublicUUID(actorID) ||
		!validWorkspaceLLMGatewayPublicText(name, 128) || !validWorkspaceLLMGatewayPublicText(model, 256) {
		return "", errors.New("gateway identity, name or model is invalid")
	}
	if responsesURL != "" || issuer != "" || clientID != "" || len(scopes) != 0 || (bearer != "" && bearer != coredb.LLMGatewayBearerAccessToken) {
		return "", errors.New("API key gateways use baseUrl and cannot include OAuth settings or responsesUrl")
	}
	url, err := publichttps.ResponsesURLFromBase(baseURL)
	if err != nil {
		return "", errors.New("baseUrl must be a public HTTPS API prefix without credentials, query or fragment")
	}
	return url, nil
}

func (s *WorkspaceLLMGatewayService) createAPIKeyGateway(ctx context.Context, workspaceID, actorID string, r corecontract.CreateWorkspaceLLMGatewayRequest) (corecontract.CreateWorkspaceLLMGatewayResponse, error) {
	const op = "CreateWorkspaceLLMGateway"
	if s == nil {
		return corecontract.CreateWorkspaceLLMGatewayResponse{}, errors.New("workspace LLM gateway service is unavailable")
	}
	url, err := validateAPIKeyGatewayRequest(workspaceID, r.GatewayID, actorID, r.Name, r.DefaultModel, r.BaseURL, r.ResponsesURL, r.OIDCIssuer, r.OIDCClientID, r.BearerTokenType, r.OIDCScopes)
	if err != nil {
		return corecontract.CreateWorkspaceLLMGatewayResponse{}, llmGatewayStateError(coredb.ErrorInvalidArgument, op, r.GatewayID, err.Error())
	}
	if !validWorkspaceLLMAPIKey(r.APIKey) {
		return corecontract.CreateWorkspaceLLMGatewayResponse{}, llmGatewayStateError(coredb.ErrorInvalidArgument, op, r.GatewayID, "API key is required and must be a bounded token without whitespace")
	}
	if err := s.store.RequireWorkspaceLLMGatewayOwner(ctx, workspaceID, actorID); err != nil {
		return corecontract.CreateWorkspaceLLMGatewayResponse{}, err
	}
	sealed, err := s.sealer.SealWorkspaceAPIKey(workspaceID, r.GatewayID, url, r.APIKey)
	if err != nil {
		return corecontract.CreateWorkspaceLLMGatewayResponse{}, errors.New("could not encrypt workspace API key")
	}
	result, err := s.store.CreateWorkspaceLLMGateway(ctx, coredb.CreateWorkspaceLLMGatewayCommand{ID: r.GatewayID, WorkspaceID: workspaceID, ActorID: actorID, Name: r.Name, ResponsesURL: url, AuthType: coredb.LLMGatewayAuthAPIKey, SealedAPIKey: sealed, BearerTokenType: coredb.LLMGatewayBearerAccessToken, DefaultModel: r.DefaultModel, MakeDefault: r.MakeDefault})
	if err != nil {
		return corecontract.CreateWorkspaceLLMGatewayResponse{}, err
	}
	return corecontract.CreateWorkspaceLLMGatewayResponse{Gateway: contractWorkspaceLLMGateway(result.Gateway), Created: result.Created}, nil
}

func (s *WorkspaceLLMGatewayService) updateAPIKeyGateway(ctx context.Context, workspaceID, gatewayID, actorID string, r corecontract.UpdateWorkspaceLLMGatewayRequest) (corecontract.UpdateWorkspaceLLMGatewayResponse, error) {
	const op = "UpdateWorkspaceLLMGateway"
	if s == nil {
		return corecontract.UpdateWorkspaceLLMGatewayResponse{}, errors.New("workspace LLM gateway service is unavailable")
	}
	url, err := validateAPIKeyGatewayRequest(workspaceID, gatewayID, actorID, r.Name, r.DefaultModel, r.BaseURL, r.ResponsesURL, r.OIDCIssuer, r.OIDCClientID, r.BearerTokenType, r.OIDCScopes)
	if err != nil {
		return corecontract.UpdateWorkspaceLLMGatewayResponse{}, llmGatewayStateError(coredb.ErrorInvalidArgument, op, gatewayID, err.Error())
	}
	if r.APIKey != nil && !validWorkspaceLLMAPIKey(*r.APIKey) {
		return corecontract.UpdateWorkspaceLLMGatewayResponse{}, llmGatewayStateError(coredb.ErrorInvalidArgument, op, gatewayID, "API key must be a bounded non-empty token; omit it to keep the existing key")
	}
	if err := s.store.RequireWorkspaceLLMGatewayOwner(ctx, workspaceID, actorID); err != nil {
		return corecontract.UpdateWorkspaceLLMGatewayResponse{}, err
	}
	var sealed []byte
	if r.APIKey != nil {
		sealed, err = s.sealer.SealWorkspaceAPIKey(workspaceID, gatewayID, url, *r.APIKey)
		if err != nil {
			return corecontract.UpdateWorkspaceLLMGatewayResponse{}, errors.New("could not encrypt workspace API key")
		}
	}
	result, err := s.store.UpdateWorkspaceLLMGateway(ctx, coredb.UpdateWorkspaceLLMGatewayCommand{ID: gatewayID, WorkspaceID: workspaceID, ActorID: actorID, Name: r.Name, ResponsesURL: url, AuthType: coredb.LLMGatewayAuthAPIKey, SealedAPIKey: sealed, BearerTokenType: coredb.LLMGatewayBearerAccessToken, DefaultModel: r.DefaultModel, MakeDefault: r.MakeDefault, ExpectedVersion: r.ExpectedVersion})
	if err != nil {
		return corecontract.UpdateWorkspaceLLMGatewayResponse{}, err
	}
	return corecontract.UpdateWorkspaceLLMGatewayResponse{Gateway: contractWorkspaceLLMGateway(result.Gateway), Changed: result.Changed}, nil
}

func (s *WorkspaceLLMGatewayService) resolveAPIKeyUpstream(a coredb.LLMGatewayLiveAuthority, b coredb.RunLLMGatewayBinding) (LLMGatewayUpstreamAuthorization, error) {
	raw, err := s.sealer.OpenWorkspaceAPIKey(a.Gateway.WorkspaceID, a.Gateway.ID, a.Gateway.ResponsesURL, a.SealedAPIKey)
	if err != nil {
		s.logGatewayResolutionFailure("api_key_open")
		return LLMGatewayUpstreamAuthorization{}, err
	}
	defer clear(raw)
	return LLMGatewayUpstreamAuthorization{GatewayID: b.GatewayID, GatewayConfigVersion: b.ConfigVersion, GrantUserID: b.GrantUserID, Model: b.Model, ResponsesURL: a.Gateway.ResponsesURL, Authorization: "Bearer " + string(raw),
		// This bounds this authorization response, not the API key's lifetime.
		// Core rechecks membership and the configuration version on every call.
		BearerExpiresAt: s.now().UTC().Add(time.Hour)}, nil
}
