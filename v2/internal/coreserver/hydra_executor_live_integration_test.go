package coreserver

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/coredb"
	"github.com/agentserver/agentserver/v2/internal/executorenrollment"
)

const (
	hydraLiveTestEnvironment         = "AGENTSERVER_RUN_HYDRA_LIVE_TESTS"
	hydraLiveAdminOriginEnvironment  = "AGENTSERVER_V2_TEST_HYDRA_ADMIN_ORIGIN"
	hydraLivePublicOriginEnvironment = "AGENTSERVER_V2_TEST_HYDRA_PUBLIC_ORIGIN"
	hydraLiveMaximumBodyBytes        = int64(128 * 1024)
)

// TestHydraV262ExecutorPrivateKeyJWTLive is deliberately opt-in. It is a
// compatibility gate for the exact Hydra release selected by deployment, not
// a mock of OAuth semantics. The launcher is responsible for pinning and
// recording the Hydra artifact/source digest supplied at these endpoints.
func TestHydraV262ExecutorPrivateKeyJWTLive(t *testing.T) {
	if os.Getenv(hydraLiveTestEnvironment) != "1" {
		t.Skip(hydraLiveTestEnvironment + "=1 is required")
	}
	adminOrigin := requiredHydraLiveOrigin(t, hydraLiveAdminOriginEnvironment)
	publicOrigin := requiredHydraLiveOrigin(t, hydraLivePublicOriginEnvironment)
	issuer := publicOrigin + "/"
	tokenEndpoint := publicOrigin + "/oauth2/token"
	httpClient := &http.Client{Timeout: 10 * time.Second}

	admin, err := NewHydraAdminClient(adminOrigin, httpClient, true)
	if err != nil {
		t.Fatal(err)
	}
	executorID, err := newCoreUUID()
	if err != nil {
		t.Fatal(err)
	}
	clientID := "agentserver-executor-" + executorID
	oauthPrivateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	oauthX, oauthY, oauthThumbprint := hydraLiveOAuthPublicKey(oauthPrivateKey)
	document := executorOAuthClientDocument(clientID, executorID, oauthX, oauthY, oauthThumbprint)
	t.Cleanup(func() { deleteHydraLiveClient(t, httpClient, adminOrigin, clientID) })

	created, err := admin.CreateExecutorOAuthClient(t.Context(), document)
	if err != nil {
		t.Fatalf("create exact Hydra executor client: %v", err)
	}
	if !equalHydraExecutorClient(created, document) {
		t.Fatalf("created Hydra executor client drifted: %+v", created)
	}
	read, err := admin.GetExecutorOAuthClient(t.Context(), clientID)
	if err != nil {
		t.Fatalf("read exact Hydra executor client: %v", err)
	}
	if !equalHydraExecutorClient(read, document) {
		t.Fatalf("read Hydra executor client drifted: %+v", read)
	}

	assertion := hydraLiveClientAssertion(t, oauthPrivateKey, document.JSONWebKeys.Keys[0].KeyID, clientID, tokenEndpoint)
	token := requestHydraLiveToken(t, httpClient, tokenEndpoint, clientID, assertion, []string{ExecutorOAuthScope}, []string{ExecutorOAuthAudience}, http.StatusOK)
	if token.AccessToken == "" || !strings.EqualFold(token.TokenType, "Bearer") || token.Scope != ExecutorOAuthScope ||
		token.ExpiresIn < int64(ExecutorOAuthAccessTokenLifespan/time.Second)-1 || token.ExpiresIn > int64(ExecutorOAuthAccessTokenLifespan/time.Second) {
		t.Fatalf("Hydra token response is outside executor profile: type=%q scope=%q expires_in=%d has_token=%t",
			token.TokenType, token.Scope, token.ExpiresIn, token.AccessToken != "")
	}

	introspector, err := NewHydraUserIntrospector(adminOrigin+"/admin/oauth2/introspect", httpClient, true)
	if err != nil {
		t.Fatal(err)
	}
	introspection, err := introspector.IntrospectUserToken(t.Context(), token.AccessToken)
	if err != nil {
		t.Fatalf("introspect live Hydra executor token: %v", err)
	}
	if !introspection.Active || introspection.ClientID != clientID || introspection.Subject != clientID ||
		len(introspection.Audience) != 1 || introspection.Audience[0] != ExecutorOAuthAudience ||
		introspection.Scope != ExecutorOAuthScope || introspection.Issuer != issuer ||
		introspection.TokenType != "Bearer" || introspection.TokenUse != "access_token" ||
		introspection.NotBefore != introspection.IssuedAt ||
		introspection.ExpiresAt-introspection.IssuedAt != int64(ExecutorOAuthAccessTokenLifespan/time.Second) {
		t.Fatalf("Hydra introspection is outside executor profile: %+v", introspection)
	}

	machinePublicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var machineKey [ed25519.PublicKeySize]byte
	copy(machineKey[:], machinePublicKey)
	authorityStore := hydraLiveAuthorityStore{authority: coredb.ExecutorMachineAuthority{
		ExecutorID: executorID, WorkspaceID: "71000000-0000-4000-8000-000000000002", OAuthClientID: clientID,
		MachinePublicKeyEd25519: machineKey, MachineKeySHA256: sha256.Sum256(machineKey[:]),
		OAuthPublicKeyP256X: oauthX, OAuthPublicKeyP256Y: oauthY, OAuthKeySHA256: oauthThumbprint,
		ExecutorVersion: 1, AuthorizedAt: time.Now().UTC(),
	}}
	authorizer, err := NewExecutorOAuthAuthorizer(ExecutorOAuthAuthorizerConfig{
		Introspector: introspector, Store: authorityStore, Hydra: admin, ExpectedIssuer: issuer,
	})
	if err != nil {
		t.Fatal(err)
	}
	authorized, err := authorizer.Authorize(t.Context(), token.AccessToken)
	if err != nil {
		t.Fatalf("Core rejected live Hydra executor authority: %v", err)
	}
	if authorized.ExecutorID != executorID || authorized.OAuthClientID != clientID || authorized.ExecutorVersion != 1 ||
		!authorized.TokenExpiresAt.Equal(time.Unix(introspection.ExpiresAt, 0).UTC()) {
		t.Fatalf("live executor authority drifted: %+v", authorized)
	}

	requestHydraLiveToken(t, httpClient, tokenEndpoint, clientID,
		hydraLiveClientAssertion(t, oauthPrivateKey, document.JSONWebKeys.Keys[0].KeyID, clientID, tokenEndpoint),
		[]string{ExecutorOAuthScope, "runs:write"}, []string{ExecutorOAuthAudience}, http.StatusBadRequest)
	requestHydraLiveToken(t, httpClient, tokenEndpoint, clientID,
		hydraLiveClientAssertion(t, oauthPrivateKey, document.JSONWebKeys.Keys[0].KeyID, clientID, tokenEndpoint),
		[]string{ExecutorOAuthScope}, []string{ExecutorOAuthAudience, "agentserver-api"}, http.StatusBadRequest)
	assertHydraDynamicRegistrationDisabled(t, httpClient, publicOrigin)
}

type hydraLiveTokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int64  `json:"expires_in"`
	Scope       string `json:"scope"`
	TokenType   string `json:"token_type"`
}

func requestHydraLiveToken(
	t *testing.T,
	client *http.Client,
	endpoint, clientID, assertion string,
	scopes, audiences []string,
	wantStatus int,
) hydraLiveTokenResponse {
	t.Helper()
	form := url.Values{
		"grant_type":            {"client_credentials"},
		"client_id":             {clientID},
		"client_assertion_type": {"urn:ietf:params:oauth:client-assertion-type:jwt-bearer"},
		"client_assertion":      {assertion},
	}
	if len(scopes) != 0 {
		form.Set("scope", strings.Join(scopes, " "))
	}
	for _, audience := range audiences {
		form.Add("audience", audience)
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("request Hydra token: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, hydraLiveMaximumBodyBytes+1))
	if err != nil || int64(len(body)) > hydraLiveMaximumBodyBytes {
		t.Fatal("Hydra token response is unreadable or oversized")
	}
	if response.StatusCode != wantStatus {
		if strings.Contains(string(body), "access_token") {
			t.Fatalf("Hydra token status = %d, want %d; response contained an access token", response.StatusCode, wantStatus)
		}
		t.Fatalf("Hydra token status = %d, want %d; body=%s", response.StatusCode, wantStatus, body)
	}
	var token hydraLiveTokenResponse
	if wantStatus == http.StatusOK {
		if err := json.Unmarshal(body, &token); err != nil {
			t.Fatalf("decode Hydra token response: %v", err)
		}
		return token
	}
	if strings.Contains(string(body), "access_token") {
		t.Fatalf("rejected Hydra request leaked an access token: %s", body)
	}
	return hydraLiveTokenResponse{}
}

func hydraLiveClientAssertion(t *testing.T, key *ecdsa.PrivateKey, keyID, clientID, audience string) string {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	jti, err := newCoreUUID()
	if err != nil {
		t.Fatal(err)
	}
	header, err := json.Marshal(map[string]string{"alg": "ES256", "kid": keyID, "typ": "JWT"})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := json.Marshal(map[string]any{
		"aud": audience, "exp": now.Add(time.Minute).Unix(), "iat": now.Unix(),
		"iss": clientID, "jti": jti, "nbf": now.Unix(), "sub": clientID,
	})
	if err != nil {
		t.Fatal(err)
	}
	signingInput := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	halfOrder := new(big.Int).Rsh(new(big.Int).Set(key.Curve.Params().N), 1)
	if s.Cmp(halfOrder) > 0 {
		s.Sub(key.Curve.Params().N, s)
	}
	signature := make([]byte, 64)
	r.FillBytes(signature[:32])
	s.FillBytes(signature[32:])
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func hydraLiveOAuthPublicKey(key *ecdsa.PrivateKey) ([32]byte, [32]byte, [32]byte) {
	var x, y [32]byte
	key.X.FillBytes(x[:])
	key.Y.FillBytes(y[:])
	thumbprint := executorenrollment.OAuthJWKThumbprint(
		base64.RawURLEncoding.EncodeToString(x[:]), base64.RawURLEncoding.EncodeToString(y[:]),
	)
	return x, y, thumbprint
}

func requiredHydraLiveOrigin(t *testing.T, name string) string {
	t.Helper()
	raw := strings.TrimSuffix(os.Getenv(name), "/")
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "http" || parsed.Hostname() != "127.0.0.1" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		t.Fatalf("%s must be an exact http://127.0.0.1:<port> test origin", name)
	}
	return raw
}

func assertHydraDynamicRegistrationDisabled(t *testing.T, client *http.Client, publicOrigin string) {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, publicOrigin+"/oauth2/register", strings.NewReader(`{"client_name":"must-not-exist"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("probe Hydra dynamic registration: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, hydraLiveMaximumBodyBytes+1))
	if err != nil || int64(len(body)) > hydraLiveMaximumBodyBytes {
		t.Fatal("Hydra dynamic-registration response is unreadable or oversized")
	}
	if response.StatusCode != http.StatusNotFound || strings.Contains(string(body), "registration_access_token") {
		t.Fatalf("Hydra dynamic registration is not disabled: status=%d body=%s", response.StatusCode, body)
	}
}

func deleteHydraLiveClient(t *testing.T, client *http.Client, adminOrigin, clientID string) {
	t.Helper()
	request, err := http.NewRequestWithContext(context.Background(), http.MethodDelete, adminOrigin+"/admin/clients/"+url.PathEscape(clientID), nil)
	if err != nil {
		t.Errorf("construct Hydra client cleanup: %v", err)
		return
	}
	response, err := client.Do(request)
	if err != nil {
		t.Errorf("delete Hydra test client: %v", err)
		return
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, hydraLiveMaximumBodyBytes))
	if response.StatusCode != http.StatusNoContent && response.StatusCode != http.StatusNotFound {
		t.Errorf("delete Hydra test client status = %d", response.StatusCode)
	}
}

type hydraLiveAuthorityStore struct {
	authority coredb.ExecutorMachineAuthority
}

func (store hydraLiveAuthorityStore) AuthorizeExecutorOAuthClient(_ context.Context, clientID string) (coredb.ExecutorMachineAuthority, error) {
	if store.authority.OAuthClientID == "" || clientID != store.authority.OAuthClientID {
		return coredb.ExecutorMachineAuthority{}, errors.New("live Hydra executor authority is unavailable")
	}
	return store.authority, nil
}
