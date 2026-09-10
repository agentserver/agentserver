package coreserver

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/coredb"
)

func TestBotmuxOAuthKeepsBrowserWorkspaceAuthority(t *testing.T) {
	now := time.Now()
	for _, test := range []struct {
		name    string
		client  string
		enabled bool
		mutate  func(*UserTokenIntrospection)
		valid   bool
	}{
		{name: "native client", client: corecontract.BotmuxOAuthClientID, enabled: true, valid: true},
		{name: "native opt in required", client: corecontract.BotmuxOAuthClientID},
		{name: "browser cannot gain offline scope", client: corecontract.BrowserOAuthClientID, enabled: true},
		{name: "platform rejected", client: corecontract.PlatformOAuthClientID, enabled: true},
		{name: "unknown client rejected", client: "unregistered-client", enabled: true},
		{name: "wrong workspace", client: corecontract.BotmuxOAuthClientID, enabled: true, mutate: func(v *UserTokenIntrospection) {
			v.Authority.WorkspaceGrants[0].WorkspaceID = "90000000-0000-4000-8000-000000000009"
		}},
		{name: "wrong audience", client: corecontract.BotmuxOAuthClientID, enabled: true, mutate: func(v *UserTokenIntrospection) { v.Audience = []string{corecontract.PlatformOAuthAudience} }},
		{name: "offline is not business permission", client: corecontract.BotmuxOAuthClientID, enabled: true, mutate: func(v *UserTokenIntrospection) {
			v.Authority.WorkspaceGrants[0].Permissions = append(v.Authority.WorkspaceGrants[0].Permissions, corecontract.OAuthOfflineAccessScope)
			slices.Sort(v.Authority.WorkspaceGrants[0].Permissions)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			token := browserUserIntrospection(now, userRunWorkspaceID, corecontract.BrowserOAuthRunsCreateScope)
			token.ClientID = test.client
			token.Scope += " " + corecontract.OAuthOfflineAccessScope
			if test.mutate != nil {
				test.mutate(&token)
			}
			authorizer, err := NewIntrospectedUserAuthorizer(IntrospectedUserAuthorizerConfig{
				Introspector: &fixedUserIntrospector{result: token}, ExpectedIssuer: token.Issuer,
				ExpectedClientID: corecontract.BrowserOAuthClientID, ExpectedAudience: corecontract.BrowserOAuthAudience,
				ExpectedAuthority: corecontract.UserOAuthBrowserAuthority, AllowedScopes: corecontract.BrowserOAuthScopes(),
				ActionPermissions: corecontract.BrowserOAuthActionPermissions(), AcceptBotmuxClient: test.enabled,
			})
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "https://core/v2/runs", nil)
			request.SetPathValue("workspaceId", userRunWorkspaceID)
			request.Header.Set("Authorization", "Bearer opaque-test-token")
			actor, err := authorizer.AuthorizeUser(request, "runs.create")
			if test.valid {
				if err != nil || actor != userRunActorID {
					t.Fatalf("AuthorizeUser=%s,%v", actor, err)
				}
			} else if err == nil {
				t.Fatal("invalid authority accepted")
			}
		})
	}
}

func TestBotmuxOAuthOfflineConsentIsNotAResourcePermission(t *testing.T) {
	profiles := []LoginBridgeOAuthProfile{
		{Authority: corecontract.UserOAuthPlatformAuthority, ClientID: corecontract.PlatformOAuthClientID, Scopes: corecontract.PlatformOAuthScopes(), Audience: []string{corecontract.PlatformOAuthAudience}},
		{Authority: corecontract.UserOAuthBrowserAuthority, ClientID: corecontract.BrowserOAuthClientID, Scopes: corecontract.BrowserOAuthScopes(), Audience: []string{corecontract.BrowserOAuthAudience}},
		{Authority: corecontract.UserOAuthBrowserAuthority, ClientID: corecontract.BotmuxOAuthClientID, Scopes: corecontract.BotmuxOAuthScopes(), Audience: []string{corecontract.BrowserOAuthAudience}},
	}
	if _, err := validateLoginBridgeOAuthProfiles(profiles); err != nil {
		t.Fatal(err)
	}
	grant, err := compileUserOAuthConsentGrant(profiles[2], corecontract.BotmuxOAuthScopes(), loginBridgeTestWorkspaceID,
		[]coredb.UserOAuthMembership{{WorkspaceID: loginBridgeTestWorkspaceID, Role: "owner", Generation: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(grant.Scope, corecontract.OAuthOfflineAccessScope) {
		t.Fatal("offline consent missing")
	}
	if slices.Contains(grant.Authority.WorkspaceGrants[0].Permissions, corecontract.OAuthOfflineAccessScope) {
		t.Fatal("offline scope became a business permission")
	}
	if _, err := compileUserOAuthConsentGrant(profiles[1], corecontract.BotmuxOAuthScopes(), loginBridgeTestWorkspaceID,
		[]coredb.UserOAuthMembership{{WorkspaceID: loginBridgeTestWorkspaceID, Role: "owner", Generation: 1}}); err == nil {
		t.Fatal("Browser accepted native offline consent")
	}
	if _, err := validateLoginBridgeOAuthProfiles([]LoginBridgeOAuthProfile{profiles[0], profiles[2]}); err == nil {
		t.Fatal("native client replaced required Browser profile")
	}
	profiles[2].Audience = []string{corecontract.PlatformOAuthAudience}
	if _, err := validateLoginBridgeOAuthProfiles(profiles); err == nil {
		t.Fatal("native profile accepted Platform audience")
	}
}

func TestBotmuxOAuthContinuationUsesOnlyRegisteredLoopback(t *testing.T) {
	bridge, _, _, _ := newLoginBridgeFixture(t)
	bridge.oauthProfiles[corecontract.BotmuxOAuthClientID] = LoginBridgeOAuthProfile{
		Authority: corecontract.UserOAuthBrowserAuthority, ClientID: corecontract.BotmuxOAuthClientID,
		Scopes: corecontract.BotmuxOAuthScopes(), Audience: []string{corecontract.BrowserOAuthAudience},
	}
	for _, callback := range []string{"http://127.0.0.1:39647/oauth/callback", "http://localhost:39647/oauth/callback", "http://127.0.0.1:39648/oauth/callback", "https://attacker.example/", "http://127.0.0.1:39647/oauth/callback?next=bad"} {
		raw := testAuthorizationRequestURL(corecontract.BotmuxOAuthClientID, corecontract.BrowserOAuthAudience, corecontract.BotmuxOAuthScopes(), corecontract.UserOAuthWorkspaceURNPrefix+loginBridgeTestWorkspaceID)
		parsed, _ := url.Parse(raw)
		query := parsed.Query()
		query.Set("redirect_uri", callback)
		query.Set("prompt", "consent")
		parsed.RawQuery = query.Encode()
		continuation := hydraTestContinuationURL(parsed.String(), hydraLoginVerifierQuery, "opaque")
		err := bridge.validateHydraRedirect(continuation, hydraLoginVerifierQuery)
		if callback == "http://127.0.0.1:39647/oauth/callback" {
			if err != nil {
				t.Fatal(err)
			}
		} else if err == nil {
			t.Fatalf("invalid callback accepted: %s", callback)
		}
	}
}
