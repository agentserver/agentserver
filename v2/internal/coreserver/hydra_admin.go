package coreserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"github.com/agentserver/agentserver/v2/internal/braincatalog"
)

const maximumHydraAdminResponseBytes = int64(512 * 1024)

type HydraOAuth2Client struct {
	ClientID string `json:"client_id"`
}

type HydraJSONWebKey struct {
	KeyType   string `json:"kty"`
	Use       string `json:"use"`
	Curve     string `json:"crv"`
	KeyID     string `json:"kid"`
	X         string `json:"x"`
	Y         string `json:"y,omitempty"`
	Algorithm string `json:"alg"`
}

type HydraJSONWebKeySet struct {
	Keys []HydraJSONWebKey `json:"keys"`
}

// HydraExecutorOAuthClient is the exact non-secret business profile Core
// reconciles for one executor. Hydra may return create-only provisioning
// credentials; the Admin adapter reads them within strict bounds and discards
// them before this profile crosses the boundary. Client reads reject them.
type HydraExecutorOAuthClient struct {
	ClientID                                  string             `json:"client_id"`
	ClientName                                string             `json:"client_name"`
	GrantTypes                                []string           `json:"grant_types"`
	ResponseTypes                             []string           `json:"response_types"`
	Scope                                     string             `json:"scope"`
	Audience                                  []string           `json:"audience"`
	TokenEndpointAuthMethod                   string             `json:"token_endpoint_auth_method"`
	TokenEndpointAuthSigningAlg               string             `json:"token_endpoint_auth_signing_alg"`
	AccessTokenStrategy                       string             `json:"access_token_strategy"`
	ClientCredentialsGrantAccessTokenLifespan string             `json:"client_credentials_grant_access_token_lifespan"`
	JSONWebKeys                               HydraJSONWebKeySet `json:"jwks"`
}

type HydraExecutorClientAdmin interface {
	CreateExecutorOAuthClient(context.Context, HydraExecutorOAuthClient) (HydraExecutorOAuthClient, error)
	GetExecutorOAuthClient(context.Context, string) (HydraExecutorOAuthClient, error)
}

type HydraLoginRequest struct {
	Challenge                    string            `json:"challenge"`
	Skip                         bool              `json:"skip"`
	Subject                      string            `json:"subject"`
	Client                       HydraOAuth2Client `json:"client"`
	RequestedScope               []string          `json:"requested_scope"`
	RequestedAccessTokenAudience []string          `json:"requested_access_token_audience"`
}

type HydraConsentRequest struct {
	Challenge                    string            `json:"challenge"`
	Skip                         bool              `json:"skip"`
	Subject                      string            `json:"subject"`
	Client                       HydraOAuth2Client `json:"client"`
	RequestedScope               []string          `json:"requested_scope"`
	RequestedAccessTokenAudience []string          `json:"requested_access_token_audience"`
	LoginChallenge               string            `json:"login_challenge"`
	LoginSessionID               string            `json:"login_session_id"`
}

type HydraRedirect struct {
	RedirectTo string `json:"redirect_to"`
}

type HydraAdminAPI interface {
	GetLoginRequest(context.Context, string) (HydraLoginRequest, error)
	AcceptLoginRequest(context.Context, string, string) (HydraRedirect, error)
	RejectLoginRequest(context.Context, string, string, string) (HydraRedirect, error)
	GetConsentRequest(context.Context, string) (HydraConsentRequest, error)
	AcceptConsentRequest(context.Context, string, []string, []string) (HydraRedirect, error)
	RejectConsentRequest(context.Context, string, string, string) (HydraRedirect, error)
}

type HydraAdminError struct {
	StatusCode int
	Operation  string
}

func (err *HydraAdminError) Error() string {
	if err == nil {
		return "<nil>"
	}
	return fmt.Sprintf("Hydra Admin %s returned HTTP %d", err.Operation, err.StatusCode)
}

type HydraAdminClient struct {
	origin     *url.URL
	httpClient *http.Client
}

func NewHydraAdminClient(origin string, httpClient *http.Client, allowInsecureHTTP bool) (*HydraAdminClient, error) {
	if httpClient == nil {
		return nil, errors.New("Hydra Admin HTTP client is required")
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return nil, errors.New("Hydra Admin URL must be an absolute HTTP(S) origin without credentials, path, query, or fragment")
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && allowInsecureHTTP) {
		return nil, errors.New("cleartext Hydra Admin access requires an explicit insecure-cluster opt-in")
	}
	parsed.Path = ""
	clientCopy := *httpClient
	clientCopy.CheckRedirect = func(*http.Request, []*http.Request) error {
		return errors.New("Hydra Admin redirects are forbidden")
	}
	return &HydraAdminClient{origin: parsed, httpClient: &clientCopy}, nil
}

func (client *HydraAdminClient) GetLoginRequest(ctx context.Context, challenge string) (HydraLoginRequest, error) {
	var result HydraLoginRequest
	err := client.do(ctx, http.MethodGet, "/admin/oauth2/auth/requests/login", "login_challenge", challenge, nil, &result, "get login request")
	if err != nil {
		return HydraLoginRequest{}, err
	}
	if result.Challenge != challenge || result.Client.ClientID == "" || len(result.Client.ClientID) > 512 {
		return HydraLoginRequest{}, errors.New("Hydra Admin returned an invalid login request scope")
	}
	return result, nil
}

func (client *HydraAdminClient) AcceptLoginRequest(ctx context.Context, challenge, subject string) (HydraRedirect, error) {
	body := struct {
		Subject     string         `json:"subject"`
		Remember    bool           `json:"remember"`
		RememberFor int64          `json:"remember_for"`
		Context     map[string]any `json:"context"`
	}{Subject: subject, Remember: false, RememberFor: 0, Context: map[string]any{}}
	var result HydraRedirect
	err := client.do(ctx, http.MethodPut, "/admin/oauth2/auth/requests/login/accept", "login_challenge", challenge, body, &result, "accept login request")
	return validateHydraRedirect(result, err)
}

func (client *HydraAdminClient) RejectLoginRequest(ctx context.Context, challenge, code, description string) (HydraRedirect, error) {
	body := hydraRejectRequest{Error: code, ErrorDescription: description, StatusCode: http.StatusForbidden}
	var result HydraRedirect
	err := client.do(ctx, http.MethodPut, "/admin/oauth2/auth/requests/login/reject", "login_challenge", challenge, body, &result, "reject login request")
	return validateHydraRedirect(result, err)
}

func (client *HydraAdminClient) GetConsentRequest(ctx context.Context, challenge string) (HydraConsentRequest, error) {
	var result HydraConsentRequest
	err := client.do(ctx, http.MethodGet, "/admin/oauth2/auth/requests/consent", "consent_challenge", challenge, nil, &result, "get consent request")
	if err != nil {
		return HydraConsentRequest{}, err
	}
	if result.Challenge != challenge || result.Client.ClientID == "" || len(result.Client.ClientID) > 512 || result.Subject == "" {
		return HydraConsentRequest{}, errors.New("Hydra Admin returned an invalid consent request scope")
	}
	return result, nil
}

func (client *HydraAdminClient) AcceptConsentRequest(
	ctx context.Context,
	challenge string,
	grantScope, grantAudience []string,
) (HydraRedirect, error) {
	body := struct {
		GrantScope               []string `json:"grant_scope"`
		GrantAccessTokenAudience []string `json:"grant_access_token_audience"`
		Remember                 bool     `json:"remember"`
		RememberFor              int64    `json:"remember_for"`
		Session                  struct {
			AccessToken map[string]any `json:"access_token"`
			IDToken     map[string]any `json:"id_token"`
		} `json:"session"`
	}{
		GrantScope: append([]string(nil), grantScope...), GrantAccessTokenAudience: append([]string(nil), grantAudience...),
		Remember: false, RememberFor: 0,
	}
	body.Session.AccessToken = map[string]any{}
	body.Session.IDToken = map[string]any{}
	var result HydraRedirect
	err := client.do(ctx, http.MethodPut, "/admin/oauth2/auth/requests/consent/accept", "consent_challenge", challenge, body, &result, "accept consent request")
	return validateHydraRedirect(result, err)
}

func (client *HydraAdminClient) RejectConsentRequest(ctx context.Context, challenge, code, description string) (HydraRedirect, error) {
	body := hydraRejectRequest{Error: code, ErrorDescription: description, StatusCode: http.StatusForbidden}
	var result HydraRedirect
	err := client.do(ctx, http.MethodPut, "/admin/oauth2/auth/requests/consent/reject", "consent_challenge", challenge, body, &result, "reject consent request")
	return validateHydraRedirect(result, err)
}

func (client *HydraAdminClient) CreateExecutorOAuthClient(ctx context.Context, document HydraExecutorOAuthClient) (HydraExecutorOAuthClient, error) {
	if err := validateHydraExecutorClientInput(document); err != nil {
		return HydraExecutorOAuthClient{}, err
	}
	result := hydraExecutorClientResponse{allowProvisioningSecrets: true}
	if err := client.doExpected(ctx, http.MethodPost, "/admin/clients", "", "", document, &result, "create executor OAuth client", http.StatusCreated); err != nil {
		return HydraExecutorOAuthClient{}, err
	}
	return result.document, nil
}

func (client *HydraAdminClient) GetExecutorOAuthClient(ctx context.Context, clientID string) (HydraExecutorOAuthClient, error) {
	if !boundedHydraClientText(clientID, 128) {
		return HydraExecutorOAuthClient{}, errors.New("Hydra executor OAuth client ID is invalid")
	}
	result := hydraExecutorClientResponse{}
	if err := client.doExpected(ctx, http.MethodGet, "/admin/clients/"+url.PathEscape(clientID), "", "", nil, &result, "get executor OAuth client", http.StatusOK); err != nil {
		return HydraExecutorOAuthClient{}, err
	}
	return result.document, nil
}

type hydraRejectRequest struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
	StatusCode       int    `json:"status_code"`
}

func (client *HydraAdminClient) do(
	ctx context.Context,
	method, path, challengeName, challenge string,
	body, destination any,
	operation string,
) error {
	return client.doExpected(ctx, method, path, challengeName, challenge, body, destination, operation, http.StatusOK)
}

func (client *HydraAdminClient) doExpected(
	ctx context.Context,
	method, path, challengeName, challenge string,
	body, destination any,
	operation string,
	expectedStatus int,
) error {
	if client == nil || client.origin == nil || client.httpClient == nil {
		return errors.New("Hydra Admin client is not initialized")
	}
	if challengeName != "" && (challenge == "" || len(challenge) > 4096 || strings.ContainsAny(challenge, "\x00\r\n")) {
		return errors.New("Hydra challenge is empty or outside protocol bounds")
	}
	endpoint := *client.origin
	endpoint.Path = path
	if challengeName != "" {
		query := url.Values{}
		query.Set(challengeName, challenge)
		endpoint.RawQuery = query.Encode()
	}

	var requestBody io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode Hydra Admin %s: %w", operation, err)
		}
		if len(encoded) > 64*1024 {
			return errors.New("Hydra Admin request exceeds size limit")
		}
		requestBody = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), requestBody)
	if err != nil {
		return fmt.Errorf("construct Hydra Admin %s: %w", operation, err)
	}
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("execute Hydra Admin %s: %w", operation, err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maximumHydraAdminResponseBytes+1))
	if err != nil {
		return fmt.Errorf("read Hydra Admin %s: %w", operation, err)
	}
	if int64(len(raw)) > maximumHydraAdminResponseBytes {
		return errors.New("Hydra Admin response exceeds size limit")
	}
	if response.StatusCode != expectedStatus {
		return &HydraAdminError{StatusCode: response.StatusCode, Operation: operation}
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return errors.New("Hydra Admin response Content-Type is not application/json")
	}
	limits := braincatalog.DefaultLimits()
	limits.MaxJSONValues = 4096
	limits.MaxJSONDepth = 32
	_, canonical, err := braincatalog.DecodeCanonicalJSON(raw, int(maximumHydraAdminResponseBytes), limits)
	if err != nil {
		return fmt.Errorf("validate Hydra Admin %s response: %w", operation, err)
	}
	if err := json.Unmarshal(canonical, destination); err != nil {
		return fmt.Errorf("decode Hydra Admin %s response: %w", operation, err)
	}
	return nil
}

func validateHydraExecutorClientInput(document HydraExecutorOAuthClient) error {
	if !boundedHydraClientText(document.ClientID, 128) || !boundedHydraClientText(document.ClientName, 256) {
		return errors.New("Hydra executor OAuth client identity is outside bounds")
	}
	if len(document.GrantTypes) != 1 || document.GrantTypes[0] != "client_credentials" || len(document.ResponseTypes) != 0 ||
		document.Scope != "executor:connect" || len(document.Audience) != 1 || document.Audience[0] != "executor-gateway" ||
		document.TokenEndpointAuthMethod != "private_key_jwt" || document.TokenEndpointAuthSigningAlg != "ES256" ||
		document.AccessTokenStrategy != "opaque" || document.ClientCredentialsGrantAccessTokenLifespan != "5m0s" || len(document.JSONWebKeys.Keys) != 1 {
		return errors.New("Hydra executor OAuth client does not use the closed production profile")
	}
	key := document.JSONWebKeys.Keys[0]
	if key.KeyType != "EC" || key.Use != "sig" || key.Curve != "P-256" || key.Algorithm != "ES256" ||
		!boundedHydraClientText(key.KeyID, 128) || !boundedHydraClientText(key.X, 128) || !boundedHydraClientText(key.Y, 128) {
		return errors.New("Hydra executor OAuth client JWK is invalid")
	}
	return nil
}

func boundedHydraClientText(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\x00\r\n")
}

func validateHydraRedirect(result HydraRedirect, err error) (HydraRedirect, error) {
	if err != nil {
		return HydraRedirect{}, err
	}
	parsed, parseErr := url.Parse(result.RedirectTo)
	if parseErr != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil {
		return HydraRedirect{}, errors.New("Hydra Admin returned an invalid HTTPS redirect")
	}
	if len(result.RedirectTo) > 8192 || strings.ContainsAny(result.RedirectTo, "\x00\r\n") {
		return HydraRedirect{}, errors.New("Hydra Admin redirect is outside protocol bounds")
	}
	return result, nil
}
