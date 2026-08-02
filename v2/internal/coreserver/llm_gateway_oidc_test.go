package coreserver

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestDiscoveredWorkspaceLLMGatewayOIDCProviderUsesPublicPKCEAndRefreshGrant(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	authority := &workspaceLLMGatewayOIDCTestAuthority{
		t: t, key: key, issuer: "https://id.example.com", clientID: "workspace-public-client",
		redirectURL: "https://agent.example.com/auth/llm-gateway/callback",
		nonce:       strings.Repeat("n", 43), tokenType: "Bearer", now: time.Now().UTC(),
	}
	factory, err := NewDiscoveredWorkspaceLLMGatewayOIDCFactory(&http.Client{
		Transport: roundTripFunc(authority.roundTrip),
	})
	if err != nil {
		t.Fatal(err)
	}
	provider, err := factory.Discover(t.Context(), WorkspaceLLMGatewayOIDCConfig{
		Issuer: authority.issuer, ClientID: authority.clientID,
		Scopes: []string{"openid", "profile", "offline_access"}, RedirectURL: authority.redirectURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	state := strings.Repeat("s", 43)
	verifier := strings.Repeat("v", 43)
	authorizationURL, err := provider.AuthorizationURL(state, authority.nonce, verifier)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(authorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if parsed.Scheme+"://"+parsed.Host+parsed.Path != authority.issuer+"/authorize" ||
		query.Get("client_id") != authority.clientID || query.Get("redirect_uri") != authority.redirectURL ||
		query.Get("response_type") != "code" || query.Get("state") != state || query.Get("nonce") != authority.nonce ||
		query.Get("code_challenge_method") != "S256" || query.Get("code_challenge") != oauth2.S256ChallengeFromVerifier(verifier) {
		t.Fatalf("workspace Gateway authorization URL = %s", authorizationURL)
	}
	authority.verifier = verifier
	grant, err := provider.Exchange(t.Context(), "authorization-code", verifier, authority.nonce)
	if err != nil {
		t.Fatal(err)
	}
	if grant.Issuer != authority.issuer || grant.Subject != "workspace-user" || grant.Tokens.RefreshToken != "refresh-1" ||
		grant.Tokens.AccessToken != "access-1" || grant.Tokens.IDToken == "" || !grant.Tokens.IDTokenExpiresAt.After(authority.now) {
		t.Fatalf("workspace Gateway OIDC grant = %+v", grant)
	}
	refreshed, err := provider.Refresh(t.Context(), grant.Tokens, grant.Subject, "id_token")
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.Issuer != authority.issuer || refreshed.Subject != grant.Subject || refreshed.Tokens.AccessToken != "access-2" ||
		refreshed.Tokens.RefreshToken != "refresh-2" || refreshed.Tokens.IDToken == grant.Tokens.IDToken || authority.tokenRequests != 2 {
		t.Fatalf("refreshed workspace Gateway OIDC grant = %+v, requests=%d", refreshed, authority.tokenRequests)
	}
	authority.tokenType = "MAC"
	if _, err := provider.Exchange(t.Context(), "wrong-token-type", verifier, authority.nonce); err == nil || !strings.Contains(err.Error(), "bearer token") {
		t.Fatalf("non-bearer token response error = %v", err)
	}
}

func TestDiscoveredWorkspaceLLMGatewayOIDCProviderBoundsEveryResponseBody(t *testing.T) {
	for _, test := range []struct {
		name          string
		contentLength int64
		body          []byte
	}{
		{
			name:          "declared oversized",
			contentLength: maximumLLMGatewayOIDCResponseBytes + 1,
			body:          []byte(`{"issuer":"https://id.example.com"}`),
		},
		{
			name:          "streamed oversized",
			contentLength: -1,
			body:          bytes.Repeat([]byte(" "), maximumLLMGatewayOIDCResponseBytes+1),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			factory, err := NewDiscoveredWorkspaceLLMGatewayOIDCFactory(&http.Client{
				Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode:    http.StatusOK,
						Header:        http.Header{"Content-Type": []string{"application/json"}},
						Body:          io.NopCloser(bytes.NewReader(test.body)),
						ContentLength: test.contentLength,
						Request:       request,
					}, nil
				}),
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = factory.Discover(t.Context(), WorkspaceLLMGatewayOIDCConfig{
				Issuer: "https://id.example.com", ClientID: "workspace-public-client",
				Scopes:      []string{"openid", "offline_access"},
				RedirectURL: "https://agent.example.com/auth/llm-gateway/callback",
			})
			if err == nil || !strings.Contains(err.Error(), errLLMGatewayOIDCResponseTooLarge.Error()) {
				t.Fatalf("oversized OIDC response error = %v", err)
			}
		})
	}
}

type workspaceLLMGatewayOIDCTestAuthority struct {
	t             *testing.T
	key           *rsa.PrivateKey
	issuer        string
	clientID      string
	redirectURL   string
	nonce         string
	verifier      string
	tokenType     string
	now           time.Time
	tokenRequests int
}

func (authority *workspaceLLMGatewayOIDCTestAuthority) roundTrip(request *http.Request) (*http.Response, error) {
	authority.t.Helper()
	if request.URL.Scheme != "https" || request.URL.Host != "id.example.com" {
		authority.t.Fatalf("OIDC request escaped configured authority: %s", request.URL)
	}
	switch {
	case request.Method == http.MethodGet && request.URL.Path == "/.well-known/openid-configuration":
		return workspaceLLMGatewayOIDCTestJSONResponse(request, map[string]any{
			"issuer": authority.issuer, "authorization_endpoint": authority.issuer + "/authorize",
			"token_endpoint": authority.issuer + "/token", "jwks_uri": authority.issuer + "/jwks",
			"response_types_supported": []string{"code"}, "subject_types_supported": []string{"public"},
			"id_token_signing_alg_values_supported": []string{"RS256"},
		}), nil
	case request.Method == http.MethodGet && request.URL.Path == "/jwks":
		return workspaceLLMGatewayOIDCTestJSONResponse(request, map[string]any{
			"keys": []map[string]string{{
				"kty": "RSA", "use": "sig", "alg": "RS256", "kid": "workspace-test",
				"n": base64.RawURLEncoding.EncodeToString(authority.key.PublicKey.N.Bytes()),
				"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(authority.key.PublicKey.E)).Bytes()),
			}},
		}), nil
	case request.Method == http.MethodPost && request.URL.Path == "/token":
		return authority.tokenResponse(request), nil
	default:
		authority.t.Fatalf("unexpected workspace Gateway OIDC request: %s %s", request.Method, request.URL)
		return nil, nil
	}
}

func (authority *workspaceLLMGatewayOIDCTestAuthority) tokenResponse(request *http.Request) *http.Response {
	raw, err := io.ReadAll(request.Body)
	if err != nil {
		authority.t.Fatal(err)
	}
	form, err := url.ParseQuery(string(raw))
	if err != nil {
		authority.t.Fatal(err)
	}
	if form.Get("client_id") != authority.clientID || form.Has("client_secret") {
		authority.t.Fatalf("workspace Gateway token request was not a public client request: %v", form)
	}
	authority.tokenRequests++
	nonce := ""
	switch form.Get("grant_type") {
	case "authorization_code":
		if form.Get("code") == "" || form.Get("redirect_uri") != authority.redirectURL || form.Get("code_verifier") != authority.verifier {
			authority.t.Fatalf("workspace Gateway code exchange = %v", form)
		}
		nonce = authority.nonce
	case "refresh_token":
		if form.Get("refresh_token") != "refresh-1" {
			authority.t.Fatalf("workspace Gateway refresh request = %v", form)
		}
	default:
		authority.t.Fatalf("workspace Gateway token grant = %v", form)
	}
	accessToken := fmt.Sprintf("access-%d", authority.tokenRequests)
	idToken := authority.signIDToken(accessToken, nonce)
	return workspaceLLMGatewayOIDCTestJSONResponse(request, map[string]any{
		"access_token": accessToken, "token_type": authority.tokenType, "expires_in": 600,
		"refresh_token": fmt.Sprintf("refresh-%d", authority.tokenRequests), "id_token": idToken,
	})
}

func (authority *workspaceLLMGatewayOIDCTestAuthority) signIDToken(_ string, nonce string) string {
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "workspace-test", "typ": "JWT"})
	claims := map[string]any{
		"iss": authority.issuer, "sub": "workspace-user", "aud": authority.clientID,
		"iat": authority.now.Unix(), "exp": authority.now.Add(10 * time.Minute).Unix(),
	}
	if nonce != "" {
		claims["nonce"] = nonce
	}
	payload, _ := json.Marshal(claims)
	signingInput := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, authority.key, crypto.SHA256, digest[:])
	if err != nil {
		authority.t.Fatal(err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func workspaceLLMGatewayOIDCTestJSONResponse(request *http.Request, value any) *http.Response {
	raw, _ := json.Marshal(value)
	return &http.Response{
		StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(bytes.NewReader(raw)), ContentLength: int64(len(raw)), Request: request,
	}
}
