package coreserver

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

type fixedUserIntrospector struct {
	result UserTokenIntrospection
	err    error
	token  string
}

func (introspector *fixedUserIntrospector) IntrospectUserToken(_ context.Context, token string) (UserTokenIntrospection, error) {
	introspector.token = token
	return introspector.result, introspector.err
}

func TestIntrospectedUserAuthorizerRequiresExactHydraWorkspaceAuthority(t *testing.T) {
	now := time.Date(2026, 7, 31, 16, 30, 0, 0, time.UTC)
	base := browserUserIntrospection(now, userRunWorkspaceID, corecontract.BrowserOAuthRunsCreateScope)
	for _, test := range []struct {
		name   string
		mutate func(*UserTokenIntrospection)
		valid  bool
	}{
		{name: "valid", valid: true},
		{name: "inactive", mutate: func(value *UserTokenIntrospection) { value.Active = false }},
		{name: "wrong audience", mutate: func(value *UserTokenIntrospection) { value.Audience = []string{"executor-gateway"} }},
		{name: "mixed audience", mutate: func(value *UserTokenIntrospection) { value.Audience = append(value.Audience, "executor-gateway") }},
		{name: "wrong issuer", mutate: func(value *UserTokenIntrospection) { value.Issuer = "https://other.example/" }},
		{name: "wrong client", mutate: func(value *UserTokenIntrospection) { value.ClientID = corecontract.PlatformOAuthClientID }},
		{name: "wrong authority", mutate: func(value *UserTokenIntrospection) {
			value.Authority.Authority = corecontract.UserOAuthPlatformAuthority
		}},
		{name: "missing scope", mutate: func(value *UserTokenIntrospection) { value.Scope = corecontract.OAuthOpenIDScope }},
		{name: "missing grant", mutate: func(value *UserTokenIntrospection) { value.Authority.WorkspaceGrants = nil }},
		{name: "global Browser permission", mutate: func(value *UserTokenIntrospection) {
			value.Authority.GlobalPermissions = []string{corecontract.BrowserOAuthRunsCreateScope}
		}},
		{name: "wrong workspace", mutate: func(value *UserTokenIntrospection) {
			value.Authority.WorkspaceGrants[0].WorkspaceID = "90000000-0000-4000-8000-000000000009"
		}},
		{name: "missing expiry", mutate: func(value *UserTokenIntrospection) { value.ExpiresAt = 0 }},
		{name: "expired", mutate: func(value *UserTokenIntrospection) { value.ExpiresAt = now.Unix() }},
		{name: "external subject", mutate: func(value *UserTokenIntrospection) { value.Subject = "oidc-user" }},
		{name: "wrong token type", mutate: func(value *UserTokenIntrospection) { value.TokenType = "MAC" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := cloneUserIntrospection(base)
			if test.mutate != nil {
				test.mutate(&result)
			}
			introspector := &fixedUserIntrospector{result: result}
			authorizer, err := newBrowserUserAuthorizer(introspector, func() time.Time { return now })
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "https://core/v2/runs", nil)
			request.SetPathValue("workspaceId", userRunWorkspaceID)
			request.Header.Set("Authorization", "Bearer opaque-token")
			actor, err := authorizer.AuthorizeUser(request, "runs.create")
			if test.valid {
				if err != nil || actor != userRunActorID || introspector.token != "opaque-token" {
					t.Fatalf("AuthorizeUser() = %q, %v", actor, err)
				}
			} else if !errors.Is(err, ErrInvalidUserAccessToken) {
				t.Fatalf("AuthorizeUser() error = %v", err)
			}
		})
	}
}

func TestIntrospectedUserAuthorizerSeparatesInvalidTokenFromAuthOutage(t *testing.T) {
	authorizer, _ := newBrowserUserAuthorizer(&fixedUserIntrospector{err: errors.New("Hydra unavailable")}, time.Now)
	request := httptest.NewRequest(http.MethodPost, "https://core/v2/runs", nil)
	request.SetPathValue("workspaceId", userRunWorkspaceID)
	if _, err := authorizer.AuthorizeUser(request, "runs.create"); !errors.Is(err, ErrInvalidUserAccessToken) {
		t.Fatalf("missing bearer error = %v", err)
	}
	request.Header.Set("Authorization", "Bearer opaque-token")
	if _, err := authorizer.AuthorizeUser(request, "runs.create"); !errors.Is(err, ErrUserAuthUnavailable) {
		t.Fatalf("introspection outage error = %v", err)
	}
	request.Header.Add("Authorization", "Bearer second")
	if _, err := authorizer.AuthorizeUser(request, "runs.create"); !errors.Is(err, ErrInvalidUserAccessToken) {
		t.Fatalf("duplicate bearer error = %v", err)
	}
}

func TestIntrospectedUserAuthorizerSeparatesPlatformGlobalAndWorkspaceAuthority(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	permissions := []string{
		corecontract.PlatformOAuthExecutorsCreateScope,
		corecontract.PlatformOAuthWorkspacesCreateScope,
		corecontract.PlatformOAuthWorkspacesReadScope,
	}
	introspection := UserTokenIntrospection{
		Active: true, Subject: userRunActorID, ClientID: corecontract.PlatformOAuthClientID,
		Audience: []string{corecontract.PlatformOAuthAudience}, Scope: strings.Join(append([]string{corecontract.OAuthOpenIDScope}, permissions...), " "),
		ExpiresAt: now.Add(time.Hour).Unix(), Issuer: "https://hydra.example/", TokenType: "Bearer", TokenUse: "access_token",
		Authority: corecontract.UserOAuthAuthority{
			Version: corecontract.UserOAuthAuthorityVersion, Authority: corecontract.UserOAuthPlatformAuthority,
			GlobalPermissions: []string{
				corecontract.PlatformOAuthWorkspacesCreateScope,
				corecontract.PlatformOAuthWorkspacesReadScope,
			},
			WorkspaceGrants: []corecontract.UserOAuthWorkspaceGrant{{
				WorkspaceID: userRunWorkspaceID, Generation: 7,
				Permissions: []string{corecontract.PlatformOAuthExecutorsCreateScope, corecontract.PlatformOAuthWorkspacesReadScope},
			}},
		},
	}
	authorizer, err := NewIntrospectedUserAuthorizer(IntrospectedUserAuthorizerConfig{
		Introspector: &fixedUserIntrospector{result: introspection}, ExpectedIssuer: "https://hydra.example/",
		ExpectedClientID: corecontract.PlatformOAuthClientID, ExpectedAudience: corecontract.PlatformOAuthAudience,
		ExpectedAuthority: corecontract.UserOAuthPlatformAuthority, AllowedScopes: corecontract.PlatformOAuthScopes(),
		ActionPermissions: corecontract.PlatformOAuthActionPermissions(), Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	for action, workspaceID := range map[string]string{
		"workspaces.create": "",
		"workspaces.get":    userRunWorkspaceID,
		"executors.create":  userRunWorkspaceID,
	} {
		request := httptest.NewRequest(http.MethodPost, "https://core/v2/platform", nil)
		request.SetPathValue("workspaceId", workspaceID)
		request.Header.Set("Authorization", "Bearer platform-token")
		if actor, err := authorizer.AuthorizeUser(request, action); err != nil || actor != userRunActorID {
			t.Fatalf("AuthorizeUser(%s) = %q, %v", action, actor, err)
		}
	}
	for action, workspaceID := range map[string]string{
		"workspaces.create": userRunWorkspaceID,
		"executors.create":  "90000000-0000-4000-8000-000000000009",
		"runs.create":       userRunWorkspaceID,
	} {
		request := httptest.NewRequest(http.MethodPost, "https://core/v2/platform", nil)
		request.SetPathValue("workspaceId", workspaceID)
		request.Header.Set("Authorization", "Bearer platform-token")
		if _, err := authorizer.AuthorizeUser(request, action); !errors.Is(err, ErrInvalidUserAccessToken) {
			t.Fatalf("AuthorizeUser(%s, %s) error = %v", action, workspaceID, err)
		}
	}
	browser, _ := newBrowserUserAuthorizer(&fixedUserIntrospector{result: introspection}, func() time.Time { return now })
	request := httptest.NewRequest(http.MethodPost, "https://core/v2/runs", nil)
	request.SetPathValue("workspaceId", userRunWorkspaceID)
	request.Header.Set("Authorization", "Bearer platform-token")
	if _, err := browser.AuthorizeUser(request, "runs.create"); !errors.Is(err, ErrInvalidUserAccessToken) {
		t.Fatalf("Platform token crossed into Browser authority: %v", err)
	}
}

func newBrowserUserAuthorizer(introspector UserTokenIntrospector, now func() time.Time) (*IntrospectedUserAuthorizer, error) {
	return NewIntrospectedUserAuthorizer(IntrospectedUserAuthorizerConfig{
		Introspector:   introspector,
		ExpectedIssuer: "https://hydra.example/", ExpectedClientID: corecontract.BrowserOAuthClientID,
		ExpectedAudience: corecontract.BrowserOAuthAudience, ExpectedAuthority: corecontract.UserOAuthBrowserAuthority,
		AllowedScopes: corecontract.BrowserOAuthScopes(), ActionPermissions: corecontract.BrowserOAuthActionPermissions(), Now: now,
	})
}

func browserUserIntrospection(now time.Time, workspaceID string, permissions ...string) UserTokenIntrospection {
	return UserTokenIntrospection{
		Active: true, Subject: userRunActorID, ClientID: corecontract.BrowserOAuthClientID,
		Audience:  []string{corecontract.BrowserOAuthAudience},
		Scope:     strings.Join(append([]string{corecontract.OAuthOpenIDScope}, permissions...), " "),
		ExpiresAt: now.Add(time.Hour).Unix(), Issuer: "https://hydra.example/", TokenType: "Bearer", TokenUse: "access_token",
		Authority: corecontract.UserOAuthAuthority{
			Version: corecontract.UserOAuthAuthorityVersion, Authority: corecontract.UserOAuthBrowserAuthority,
			GlobalPermissions: []string{},
			WorkspaceGrants: []corecontract.UserOAuthWorkspaceGrant{{
				WorkspaceID: workspaceID, Generation: 1, Permissions: append([]string(nil), permissions...),
			}},
		},
	}
}

func cloneUserIntrospection(input UserTokenIntrospection) UserTokenIntrospection {
	result := input
	result.Audience = append([]string(nil), input.Audience...)
	result.Authority.GlobalPermissions = append([]string(nil), input.Authority.GlobalPermissions...)
	result.Authority.WorkspaceGrants = append([]corecontract.UserOAuthWorkspaceGrant(nil), input.Authority.WorkspaceGrants...)
	for index := range result.Authority.WorkspaceGrants {
		result.Authority.WorkspaceGrants[index].Permissions = append(
			[]string(nil), input.Authority.WorkspaceGrants[index].Permissions...,
		)
	}
	return result
}

func TestHydraUserIntrospectorBoundsResponseAndDoesNotFollowRedirects(t *testing.T) {
	requests := 0
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		if request.Method != http.MethodPost || request.FormValue("token") != "opaque-token" {
			t.Fatalf("introspection request = %s token=%q", request.Method, request.FormValue("token"))
		}
		return &http.Response{
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{"active":true,"sub":"` + userRunActorID +
				`","client_id":"agentserver-browser","aud":"agentserver-browser-api","scope":"openid runs:create","exp":2000000000,` +
				`"iat":1999999700,"nbf":1999999700,"iss":"https://hydra.example/","token_type":"Bearer",` +
				`"token_use":"access_token","ext":{"provider":"ignored","agentserver":{"version":1,"authority":"browser",` +
				`"global_permissions":[],"workspace_grants":[{"workspace_id":"` + userRunWorkspaceID +
				`","generation":1,"permissions":["runs:create"]}]}},"extra":"ignored"}`)),
			Request: request,
		}, nil
	})}

	introspector, err := NewHydraUserIntrospector("https://hydra-admin/admin/oauth2/introspect", client, false)
	if err != nil {
		t.Fatal(err)
	}
	result, err := introspector.IntrospectUserToken(t.Context(), "opaque-token")
	if err != nil || !result.Active || result.Subject != userRunActorID || result.ClientID != corecontract.BrowserOAuthClientID ||
		len(result.Audience) != 1 || result.IssuedAt != 1999999700 || result.NotBefore != 1999999700 ||
		result.Issuer != "https://hydra.example/" || result.TokenType != "Bearer" || result.TokenUse != "access_token" ||
		result.Authority.Authority != corecontract.UserOAuthBrowserAuthority || len(result.Authority.WorkspaceGrants) != 1 ||
		result.Authority.WorkspaceGrants[0].WorkspaceID != userRunWorkspaceID {
		t.Fatalf("IntrospectUserToken() = %+v, %v", result, err)
	}

	redirect, _ := NewHydraUserIntrospector("https://hydra-admin/admin/oauth2/introspect?redirect=1", client, false)
	if redirect != nil {
		t.Fatal("introspection endpoint with query was accepted")
	}
	redirectRequests := 0
	redirectClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		redirectRequests++
		return &http.Response{
			StatusCode: http.StatusFound, Header: http.Header{"Location": []string{"https://token-sink.invalid/stolen"}},
			Body: io.NopCloser(strings.NewReader("redirect")), Request: request,
		}, nil
	})}
	redirect, _ = NewHydraUserIntrospector("https://hydra-admin/admin/oauth2/introspect", redirectClient, false)
	if _, err := redirect.IntrospectUserToken(t.Context(), "opaque-token"); err == nil || redirectRequests != 1 {
		t.Fatalf("redirect error = %v, requests = %d", err, redirectRequests)
	}
	if requests != 1 {
		t.Fatalf("valid introspection requests = %d", requests)
	}
}

func TestHydraUserIntrospectorRejectsDuplicateAuthorityFields(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}},
			Body:    io.NopCloser(strings.NewReader(`{"active":true,"active":false,"sub":"` + userRunActorID + `","aud":"agentserver-api","scope":"runs:write","exp":2000000000}`)),
			Request: request,
		}, nil
	})}
	introspector, err := NewHydraUserIntrospector("https://hydra.example/admin/oauth2/introspect", httpClient, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := introspector.IntrospectUserToken(t.Context(), "opaque-token"); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate introspection authority error = %v", err)
	}
}

func TestHydraUserIntrospectorRequiresExplicitCleartextOptIn(t *testing.T) {
	client := &http.Client{Timeout: time.Second}
	if _, err := NewHydraUserIntrospector("http://hydra-admin:4445/admin/oauth2/introspect", client, false); err == nil {
		t.Fatal("cluster cleartext endpoint was accepted without opt-in")
	}
	if _, err := NewHydraUserIntrospector("https://hydra-admin/admin/oauth2/introspect", client, false); err != nil {
		t.Fatal(err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}
