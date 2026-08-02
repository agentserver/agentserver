package coreserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// hydraExecutorClientResponse is deliberately tied to the complete OAuth2
// client schema in Hydra v26.2.0 spec/api.json (SHA-256
// aa95a73bb14a90b8f2ce1f852cc41418cdbef3ac3bdedc09a48df0bc97a40540).
// A new Hydra field is rejected until it is classified here; otherwise an
// Admin response could hide a newly introduced authorization surface behind
// Go's default unknown-field behavior.
type hydraExecutorClientResponse struct {
	document                 HydraExecutorOAuthClient
	allowProvisioningSecrets bool
}

func (response *hydraExecutorClientResponse) UnmarshalJSON(raw []byte) error {
	if response == nil {
		return errors.New("Hydra executor client response destination is nil")
	}
	document, err := decodeHydraExecutorClientResponse(raw, response.allowProvisioningSecrets)
	if err != nil {
		return err
	}
	response.document = document
	return nil
}

type hydraExecutorClientWire struct {
	AccessTokenStrategy                       string          `json:"access_token_strategy"`
	AllowedCORSOrigins                        []string        `json:"allowed_cors_origins"`
	Audience                                  []string        `json:"audience"`
	AuthorizationCodeGrantAccessTokenLifespan string          `json:"authorization_code_grant_access_token_lifespan"`
	AuthorizationCodeGrantIDTokenLifespan     string          `json:"authorization_code_grant_id_token_lifespan"`
	AuthorizationCodeGrantRefreshLifespan     string          `json:"authorization_code_grant_refresh_token_lifespan"`
	BackchannelLogoutSessionRequired          bool            `json:"backchannel_logout_session_required"`
	BackchannelLogoutURI                      string          `json:"backchannel_logout_uri"`
	ClientCredentialsGrantAccessTokenLifespan string          `json:"client_credentials_grant_access_token_lifespan"`
	ClientID                                  string          `json:"client_id"`
	ClientName                                string          `json:"client_name"`
	ClientSecret                              string          `json:"client_secret"`
	ClientSecretExpiresAt                     int64           `json:"client_secret_expires_at"`
	ClientURI                                 string          `json:"client_uri"`
	Contacts                                  []string        `json:"contacts"`
	CreatedAt                                 time.Time       `json:"created_at"`
	DeviceGrantAccessTokenLifespan            string          `json:"device_authorization_grant_access_token_lifespan"`
	DeviceGrantIDTokenLifespan                string          `json:"device_authorization_grant_id_token_lifespan"`
	DeviceGrantRefreshTokenLifespan           string          `json:"device_authorization_grant_refresh_token_lifespan"`
	FrontchannelLogoutSessionRequired         bool            `json:"frontchannel_logout_session_required"`
	FrontchannelLogoutURI                     string          `json:"frontchannel_logout_uri"`
	GrantTypes                                []string        `json:"grant_types"`
	ImplicitGrantAccessTokenLifespan          string          `json:"implicit_grant_access_token_lifespan"`
	ImplicitGrantIDTokenLifespan              string          `json:"implicit_grant_id_token_lifespan"`
	JSONWebKeys                               json.RawMessage `json:"jwks"`
	JSONWebKeysURI                            string          `json:"jwks_uri"`
	JWTBearerGrantAccessTokenLifespan         string          `json:"jwt_bearer_grant_access_token_lifespan"`
	LogoURI                                   string          `json:"logo_uri"`
	Metadata                                  json.RawMessage `json:"metadata"`
	Owner                                     string          `json:"owner"`
	PolicyURI                                 string          `json:"policy_uri"`
	PostLogoutRedirectURIs                    []string        `json:"post_logout_redirect_uris"`
	RedirectURIs                              []string        `json:"redirect_uris"`
	RefreshTokenGrantAccessTokenLifespan      string          `json:"refresh_token_grant_access_token_lifespan"`
	RefreshTokenGrantIDTokenLifespan          string          `json:"refresh_token_grant_id_token_lifespan"`
	RefreshTokenGrantRefreshTokenLifespan     string          `json:"refresh_token_grant_refresh_token_lifespan"`
	RegistrationAccessToken                   string          `json:"registration_access_token"`
	RegistrationClientURI                     string          `json:"registration_client_uri"`
	RequestObjectSigningAlgorithm             string          `json:"request_object_signing_alg"`
	RequestURIs                               []string        `json:"request_uris"`
	ResponseTypes                             []string        `json:"response_types"`
	Scope                                     string          `json:"scope"`
	SectorIdentifierURI                       string          `json:"sector_identifier_uri"`
	SkipConsent                               bool            `json:"skip_consent"`
	SkipLogoutConsent                         bool            `json:"skip_logout_consent"`
	SubjectType                               string          `json:"subject_type"`
	TokenEndpointAuthMethod                   string          `json:"token_endpoint_auth_method"`
	TokenEndpointAuthSigningAlg               string          `json:"token_endpoint_auth_signing_alg"`
	TermsOfServiceURI                         string          `json:"tos_uri"`
	UpdatedAt                                 time.Time       `json:"updated_at"`
	UserinfoSignedResponseAlgorithm           string          `json:"userinfo_signed_response_alg"`
}

var hydraV262OAuthClientFields = map[string]struct{}{
	"access_token_strategy": {}, "allowed_cors_origins": {}, "audience": {},
	"authorization_code_grant_access_token_lifespan": {}, "authorization_code_grant_id_token_lifespan": {},
	"authorization_code_grant_refresh_token_lifespan": {}, "backchannel_logout_session_required": {},
	"backchannel_logout_uri": {}, "client_credentials_grant_access_token_lifespan": {}, "client_id": {},
	"client_name": {}, "client_secret": {}, "client_secret_expires_at": {}, "client_uri": {}, "contacts": {},
	"created_at": {}, "device_authorization_grant_access_token_lifespan": {},
	"device_authorization_grant_id_token_lifespan": {}, "device_authorization_grant_refresh_token_lifespan": {},
	"frontchannel_logout_session_required": {}, "frontchannel_logout_uri": {}, "grant_types": {},
	"implicit_grant_access_token_lifespan": {}, "implicit_grant_id_token_lifespan": {}, "jwks": {}, "jwks_uri": {},
	"jwt_bearer_grant_access_token_lifespan": {}, "logo_uri": {}, "metadata": {}, "owner": {}, "policy_uri": {},
	"post_logout_redirect_uris": {}, "redirect_uris": {}, "refresh_token_grant_access_token_lifespan": {},
	"refresh_token_grant_id_token_lifespan": {}, "refresh_token_grant_refresh_token_lifespan": {},
	"registration_access_token": {}, "registration_client_uri": {}, "request_object_signing_alg": {},
	"request_uris": {}, "response_types": {}, "scope": {}, "sector_identifier_uri": {}, "skip_consent": {},
	"skip_logout_consent": {}, "subject_type": {}, "token_endpoint_auth_method": {},
	"token_endpoint_auth_signing_alg": {}, "tos_uri": {}, "updated_at": {}, "userinfo_signed_response_alg": {},
}

func decodeHydraExecutorClientResponse(raw []byte, allowProvisioningSecrets bool) (HydraExecutorOAuthClient, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return HydraExecutorOAuthClient{}, errors.New("Hydra executor OAuth client is not a JSON object")
	}
	for field := range fields {
		if _, known := hydraV262OAuthClientFields[field]; !known {
			return HydraExecutorOAuthClient{}, fmt.Errorf("Hydra executor OAuth client contains unsupported field %q", field)
		}
	}
	var wire hydraExecutorClientWire
	if err := json.Unmarshal(raw, &wire); err != nil {
		return HydraExecutorOAuthClient{}, errors.New("Hydra executor OAuth client fields have invalid types")
	}
	jwks, err := decodeExactHydraExecutorJWKSet(wire.JSONWebKeys)
	if err != nil {
		return HydraExecutorOAuthClient{}, err
	}
	document := HydraExecutorOAuthClient{
		ClientID: wire.ClientID, ClientName: wire.ClientName, GrantTypes: wire.GrantTypes,
		ResponseTypes: wire.ResponseTypes, Scope: wire.Scope, Audience: wire.Audience,
		TokenEndpointAuthMethod:                   wire.TokenEndpointAuthMethod,
		TokenEndpointAuthSigningAlg:               wire.TokenEndpointAuthSigningAlg,
		AccessTokenStrategy:                       wire.AccessTokenStrategy,
		ClientCredentialsGrantAccessTokenLifespan: wire.ClientCredentialsGrantAccessTokenLifespan,
		JSONWebKeys:                               jwks,
	}
	if err := validateHydraExecutorClientInput(document); err != nil {
		return HydraExecutorOAuthClient{}, err
	}
	if err := validateHydraExecutorDisabledSurfaces(wire); err != nil {
		return HydraExecutorOAuthClient{}, err
	}
	if err := validateHydraProvisioningSecrets(wire, allowProvisioningSecrets); err != nil {
		return HydraExecutorOAuthClient{}, err
	}
	return document, nil
}

func validateHydraExecutorDisabledSurfaces(wire hydraExecutorClientWire) error {
	if len(wire.AllowedCORSOrigins) != 0 || len(wire.RedirectURIs) != 0 || len(wire.Contacts) != 0 ||
		len(wire.PostLogoutRedirectURIs) != 0 || len(wire.RequestURIs) != 0 ||
		wire.BackchannelLogoutSessionRequired || wire.FrontchannelLogoutSessionRequired || wire.SkipConsent || wire.SkipLogoutConsent ||
		wire.BackchannelLogoutURI != "" || wire.FrontchannelLogoutURI != "" || wire.ClientURI != "" || wire.JSONWebKeysURI != "" ||
		wire.LogoURI != "" || wire.Owner != "" || wire.PolicyURI != "" || wire.RequestObjectSigningAlgorithm != "" ||
		wire.SectorIdentifierURI != "" || wire.TermsOfServiceURI != "" || wire.ClientSecretExpiresAt != 0 {
		return errors.New("Hydra executor OAuth client enables a forbidden client surface")
	}
	if wire.AuthorizationCodeGrantAccessTokenLifespan != "" || wire.AuthorizationCodeGrantIDTokenLifespan != "" ||
		wire.AuthorizationCodeGrantRefreshLifespan != "" || wire.DeviceGrantAccessTokenLifespan != "" ||
		wire.DeviceGrantIDTokenLifespan != "" || wire.DeviceGrantRefreshTokenLifespan != "" ||
		wire.ImplicitGrantAccessTokenLifespan != "" || wire.ImplicitGrantIDTokenLifespan != "" ||
		wire.JWTBearerGrantAccessTokenLifespan != "" || wire.RefreshTokenGrantAccessTokenLifespan != "" ||
		wire.RefreshTokenGrantIDTokenLifespan != "" || wire.RefreshTokenGrantRefreshTokenLifespan != "" {
		return errors.New("Hydra executor OAuth client configures a forbidden grant lifespan")
	}
	if wire.SubjectType != "public" || wire.UserinfoSignedResponseAlgorithm != "none" {
		return errors.New("Hydra executor OAuth client has unexpected OIDC defaults")
	}
	metadata := bytes.TrimSpace(wire.Metadata)
	if len(metadata) != 0 && !bytes.Equal(metadata, []byte("null")) && !bytes.Equal(metadata, []byte("{}")) {
		return errors.New("Hydra executor OAuth client metadata is not empty")
	}
	if !wire.CreatedAt.IsZero() && !wire.UpdatedAt.IsZero() && wire.UpdatedAt.Before(wire.CreatedAt) {
		return errors.New("Hydra executor OAuth client timestamps are inconsistent")
	}
	return nil
}

func validateHydraProvisioningSecrets(wire hydraExecutorClientWire, allowed bool) error {
	if !allowed {
		if wire.ClientSecret != "" || wire.RegistrationAccessToken != "" || wire.RegistrationClientURI != "" {
			return errors.New("Hydra returned provisioning credentials from a client read")
		}
		return nil
	}
	for _, value := range []string{wire.ClientSecret, wire.RegistrationAccessToken} {
		if value != "" && (len(value) > 8192 || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\x00\r\n")) {
			return errors.New("Hydra returned malformed executor client provisioning credentials")
		}
	}
	if wire.RegistrationClientURI != "" {
		parsed, err := url.Parse(wire.RegistrationClientURI)
		if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.User != nil ||
			parsed.RawQuery != "" || parsed.Fragment != "" || len(wire.RegistrationClientURI) > 8192 {
			return errors.New("Hydra returned a malformed executor registration client URI")
		}
	}
	return nil
}

func decodeExactHydraExecutorJWKSet(raw json.RawMessage) (HydraJSONWebKeySet, error) {
	var setFields map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &setFields) != nil || len(setFields) != 1 {
		return HydraJSONWebKeySet{}, errors.New("Hydra executor OAuth client JWK set is invalid")
	}
	keysRaw, found := setFields["keys"]
	if !found {
		return HydraJSONWebKeySet{}, errors.New("Hydra executor OAuth client JWK set has no keys")
	}
	var rawKeys []json.RawMessage
	if err := json.Unmarshal(keysRaw, &rawKeys); err != nil || len(rawKeys) != 1 {
		return HydraJSONWebKeySet{}, errors.New("Hydra executor OAuth client must have exactly one JWK")
	}
	var keyFields map[string]json.RawMessage
	if err := json.Unmarshal(rawKeys[0], &keyFields); err != nil || len(keyFields) != 7 {
		return HydraJSONWebKeySet{}, errors.New("Hydra executor OAuth client JWK fields are not exact")
	}
	for _, field := range []string{"alg", "crv", "kid", "kty", "use", "x", "y"} {
		if _, found := keyFields[field]; !found {
			return HydraJSONWebKeySet{}, errors.New("Hydra executor OAuth client JWK fields are incomplete")
		}
	}
	var key HydraJSONWebKey
	if err := json.Unmarshal(rawKeys[0], &key); err != nil {
		return HydraJSONWebKeySet{}, errors.New("Hydra executor OAuth client JWK is malformed")
	}
	return HydraJSONWebKeySet{Keys: []HydraJSONWebKey{key}}, nil
}
