package auth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// HydraClient talks to the Ory Hydra Admin API.
type HydraClient struct {
	AdminURL  string // e.g. "http://hydra:4445"
	PublicURL string // e.g. "https://auth.example.com"
	client    *http.Client
}

// NewHydraClient creates a client for the given Hydra Admin URL.
func NewHydraClient(adminURL, publicURL string) *HydraClient {
	return &HydraClient{
		AdminURL:  strings.TrimRight(adminURL, "/"),
		PublicURL: strings.TrimRight(publicURL, "/"),
		client:    &http.Client{Timeout: 10 * time.Second},
	}
}

// --- Types ---

type LoginRequest struct {
	Challenge      string   `json:"challenge"`
	Subject        string   `json:"subject"`
	Skip           bool     `json:"skip"`
	RequestedScope []string `json:"requested_scope"`
	Client         struct {
		ClientID string `json:"client_id"`
	} `json:"client"`
}

type AcceptLoginBody struct {
	Subject     string `json:"subject"`
	Remember    bool   `json:"remember"`
	RememberFor int    `json:"remember_for,omitempty"`
}

type ConsentRequest struct {
	Challenge      string   `json:"challenge"`
	Subject        string   `json:"subject"`
	RequestedScope []string `json:"requested_scope"`
	// RequestedAccessTokenAudience is the `resource` parameter the
	// client sent on /oauth2/auth (RFC 8707). MCP clients set it to
	// the canonical gateway URL (e.g. https://mcp.agent.cs.ac.cn/v1/mcp).
	// We grant whatever the client requested unmodified — the gateway
	// rejects mismatched audiences at resolve time, so there's no
	// security benefit to filtering here.
	RequestedAccessTokenAudience []string `json:"requested_access_token_audience,omitempty"`
	Client                       struct {
		ClientID string `json:"client_id"`
	} `json:"client"`
}

type ConsentSession struct {
	AccessToken map[string]interface{} `json:"access_token,omitempty"`
	IDToken     map[string]interface{} `json:"id_token,omitempty"`
}

type AcceptConsentBody struct {
	GrantScope []string `json:"grant_scope"`
	// GrantAccessTokenAudience tells Hydra which audiences to embed
	// in the issued access token's `aud` claim. Echoes the client's
	// requested audiences (from ConsentRequest.RequestedAccessTokenAudience);
	// omitting it means the token has no audience binding and our
	// resolver would refuse it — every MCP-flow consent submit must
	// pass this through.
	GrantAccessTokenAudience []string       `json:"grant_access_token_audience,omitempty"`
	Session                  ConsentSession `json:"session"`
	Remember                 bool           `json:"remember,omitempty"`
	RememberFor              int            `json:"remember_for,omitempty"`
}

type RejectBody struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description,omitempty"`
}

type RedirectResponse struct {
	RedirectTo string `json:"redirect_to"`
}

type IntrospectionResult struct {
	Active   bool                   `json:"active"`
	Subject  string                 `json:"sub"`
	Scope    string                 `json:"scope"`
	ClientID string                 `json:"client_id"`
	Extra    map[string]interface{} `json:"ext"`
}

// --- Login Provider API ---

func (h *HydraClient) GetLoginRequest(challenge string) (*LoginRequest, error) {
	u := h.AdminURL + "/admin/oauth2/auth/requests/login?login_challenge=" + url.QueryEscape(challenge)
	resp, err := h.client.Get(u)
	if err != nil {
		return nil, fmt.Errorf("get login request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get login request: status %d: %s", resp.StatusCode, body)
	}
	var req LoginRequest
	if err := json.NewDecoder(resp.Body).Decode(&req); err != nil {
		return nil, fmt.Errorf("decode login request: %w", err)
	}
	return &req, nil
}

func (h *HydraClient) AcceptLogin(challenge string, body AcceptLoginBody) (string, error) {
	return h.putJSON("/admin/oauth2/auth/requests/login/accept", "login_challenge", challenge, body)
}

func (h *HydraClient) RejectLogin(challenge string, body RejectBody) (string, error) {
	return h.putJSON("/admin/oauth2/auth/requests/login/reject", "login_challenge", challenge, body)
}

// --- Consent Provider API ---

func (h *HydraClient) GetConsentRequest(challenge string) (*ConsentRequest, error) {
	u := h.AdminURL + "/admin/oauth2/auth/requests/consent?consent_challenge=" + url.QueryEscape(challenge)
	resp, err := h.client.Get(u)
	if err != nil {
		return nil, fmt.Errorf("get consent request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get consent request: status %d: %s", resp.StatusCode, body)
	}
	var req ConsentRequest
	if err := json.NewDecoder(resp.Body).Decode(&req); err != nil {
		return nil, fmt.Errorf("decode consent request: %w", err)
	}
	return &req, nil
}

func (h *HydraClient) AcceptConsent(challenge string, body AcceptConsentBody) (string, error) {
	return h.putJSON("/admin/oauth2/auth/requests/consent/accept", "consent_challenge", challenge, body)
}

func (h *HydraClient) RejectConsent(challenge string, body RejectBody) (string, error) {
	return h.putJSON("/admin/oauth2/auth/requests/consent/reject", "consent_challenge", challenge, body)
}

// --- Device Flow ---

type AcceptDeviceBody struct {
	UserCode string `json:"user_code"`
}

func (h *HydraClient) AcceptDeviceChallenge(challenge string, body AcceptDeviceBody) (string, error) {
	return h.putJSON("/admin/oauth2/auth/requests/device/accept", "device_challenge", challenge, body)
}

// --- Token Introspection ---

func (h *HydraClient) IntrospectToken(token string) (*IntrospectionResult, error) {
	form := url.Values{"token": {token}}
	resp, err := h.client.PostForm(h.AdminURL+"/admin/oauth2/introspect", form)
	if err != nil {
		return nil, fmt.Errorf("introspect token: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("introspect token: status %d: %s", resp.StatusCode, body)
	}
	var result IntrospectionResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode introspection: %w", err)
	}
	return &result, nil
}

// HasScope checks if the introspection result includes the given scope.
func (r *IntrospectionResult) HasScope(scope string) bool {
	for _, s := range strings.Split(r.Scope, " ") {
		if s == scope {
			return true
		}
	}
	return false
}

// --- Helpers ---

func (h *HydraClient) putJSON(path, queryKey, queryVal string, body interface{}) (string, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal body: %w", err)
	}
	u := h.AdminURL + path + "?" + queryKey + "=" + url.QueryEscape(queryVal)
	req, err := http.NewRequest(http.MethodPut, u, bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("put request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("put %s: status %d: %s", path, resp.StatusCode, respBody)
	}
	var rr RedirectResponse
	if err := json.NewDecoder(resp.Body).Decode(&rr); err != nil {
		return "", fmt.Errorf("decode redirect: %w", err)
	}
	return rr.RedirectTo, nil
}

// --- OAuth2 Client Admin (RFC 7591 admin-side, used for "static"
// public clients minted via the agentserver UI / CLI) ---

// HydraOAuth2Client mirrors Hydra's POST /admin/clients body +
// response. We only set the fields agentserver actually cares about;
// Hydra fills the rest with defaults (subject_type=public,
// token_endpoint_auth_method we pin to "none" for public/PKCE).
type HydraOAuth2Client struct {
	ClientID                string   `json:"client_id,omitempty"`
	ClientName              string   `json:"client_name,omitempty"`
	RedirectURIs            []string `json:"redirect_uris,omitempty"`
	GrantTypes              []string `json:"grant_types,omitempty"`
	ResponseTypes           []string `json:"response_types,omitempty"`
	Scope                   string   `json:"scope,omitempty"` // space-separated
	Audience                []string `json:"audience,omitempty"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method,omitempty"`
}

// CreateOAuth2Client POSTs to /admin/clients and returns the
// server-assigned client_id (along with the rest of the row). For
// public clients we deliberately don't ask for a secret and set
// token_endpoint_auth_method=none — the resolved token is bound to
// the workspace via the consent screen + RFC 8707 audience, not via
// client_secret.
func (h *HydraClient) CreateOAuth2Client(c HydraOAuth2Client) (*HydraOAuth2Client, error) {
	data, err := json.Marshal(c)
	if err != nil {
		return nil, fmt.Errorf("marshal client: %w", err)
	}
	req, err := http.NewRequest(http.MethodPost, h.AdminURL+"/admin/clients", bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("create client: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("create client: status %d: %s", resp.StatusCode, body)
	}
	var out HydraOAuth2Client
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode create client: %w", err)
	}
	return &out, nil
}

// DeleteOAuth2Client removes a client by Hydra-assigned id. Idempotent
// on the agentserver side: 404 is collapsed to a nil error so a stale
// row in mcp_oauth_clients (Hydra deleted out-of-band) still cleans up.
func (h *HydraClient) DeleteOAuth2Client(clientID string) error {
	req, err := http.NewRequest(http.MethodDelete,
		h.AdminURL+"/admin/clients/"+url.PathEscape(clientID), nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return fmt.Errorf("delete client: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("delete client: status %d: %s", resp.StatusCode, body)
	}
	return nil
}
