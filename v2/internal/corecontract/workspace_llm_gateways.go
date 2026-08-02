package corecontract

import "time"

const (
	LLMGatewayCollectionRoutePattern = "/v2/workspaces/{workspaceId}/llm-gateways"
	LLMGatewayActionRoutePattern     = "/v2/workspaces/{workspaceId}/llm-gateways/{gatewayAction}"
	LLMGatewayOIDCCallbackPath       = "/auth/llm-gateway/callback"
	WorkspaceLLMGatewayProvider      = "workspace-gateway"
)

func WorkspaceLLMGatewaysPath(workspaceID string) string {
	return "/v2/workspaces/" + workspaceID + "/llm-gateways"
}

func AuthorizeLLMGatewayPath(workspaceID, gatewayID string) string {
	return WorkspaceLLMGatewaysPath(workspaceID) + "/" + gatewayID + ":authorize"
}

func CompleteLLMGatewayAuthorizationPath(workspaceID, gatewayID string) string {
	return WorkspaceLLMGatewaysPath(workspaceID) + "/" + gatewayID + ":completeAuthorization"
}

func RevokeLLMGatewayGrantPath(workspaceID, gatewayID string) string {
	return WorkspaceLLMGatewaysPath(workspaceID) + "/" + gatewayID + ":revoke"
}

func DisableLLMGatewayPath(workspaceID, gatewayID string) string {
	return WorkspaceLLMGatewaysPath(workspaceID) + "/" + gatewayID + ":disable"
}

type CreateWorkspaceLLMGatewayRequest struct {
	GatewayID       string   `json:"gatewayId"`
	Name            string   `json:"name"`
	ResponsesURL    string   `json:"responsesUrl"`
	OIDCIssuer      string   `json:"oidcIssuer"`
	OIDCClientID    string   `json:"oidcClientId"`
	OIDCScopes      []string `json:"oidcScopes"`
	BearerTokenType string   `json:"bearerTokenType"`
	DefaultModel    string   `json:"defaultModel"`
	MakeDefault     bool     `json:"makeDefault"`
}

type WorkspaceLLMGatewayState struct {
	GatewayID       string     `json:"gatewayId"`
	WorkspaceID     string     `json:"workspaceId"`
	Name            string     `json:"name"`
	ResponsesURL    string     `json:"responsesUrl"`
	OIDCIssuer      string     `json:"oidcIssuer"`
	OIDCClientID    string     `json:"oidcClientId"`
	OIDCScopes      []string   `json:"oidcScopes"`
	BearerTokenType string     `json:"bearerTokenType"`
	DefaultModel    string     `json:"defaultModel"`
	Status          string     `json:"status"`
	Default         bool       `json:"default"`
	Version         int64      `json:"version"`
	GrantStatus     string     `json:"grantStatus"`
	GrantExpiresAt  *time.Time `json:"grantExpiresAt,omitempty"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
}

type CreateWorkspaceLLMGatewayResponse struct {
	Gateway WorkspaceLLMGatewayState `json:"gateway"`
	Created bool                     `json:"created"`
}

type ListWorkspaceLLMGatewaysResponse struct {
	Gateways []WorkspaceLLMGatewayState `json:"gateways"`
}

type BeginWorkspaceLLMGatewayAuthorizationRequest struct {
	BrowserBinding string `json:"browserBinding"`
}

type BeginWorkspaceLLMGatewayAuthorizationResponse struct {
	GatewayID        string    `json:"gatewayId"`
	AuthorizationURL string    `json:"authorizationUrl"`
	ExpiresAt        time.Time `json:"expiresAt"`
}

type CompleteWorkspaceLLMGatewayAuthorizationRequest struct {
	State          string `json:"state"`
	Code           string `json:"code,omitempty"`
	ProviderError  string `json:"providerError,omitempty"`
	BrowserBinding string `json:"browserBinding"`
}

type CompleteWorkspaceLLMGatewayAuthorizationResponse struct {
	GatewayID       string    `json:"gatewayId"`
	GrantStatus     string    `json:"grantStatus"`
	BearerExpiresAt time.Time `json:"bearerExpiresAt"`
}

type RevokeWorkspaceLLMGatewayGrantResponse struct {
	GatewayID   string `json:"gatewayId"`
	GrantStatus string `json:"grantStatus"`
	Changed     bool   `json:"changed"`
}

type DisableWorkspaceLLMGatewayResponse struct {
	GatewayID string `json:"gatewayId"`
	Status    string `json:"status"`
	Version   int64  `json:"version"`
	Changed   bool   `json:"changed"`
}
