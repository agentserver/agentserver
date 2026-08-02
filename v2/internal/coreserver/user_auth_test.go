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

func TestIntrospectedUserAuthorizerRequiresExactAudienceScopeExpiryAndUUIDSubject(t *testing.T) {
	now := time.Date(2026, 7, 31, 16, 30, 0, 0, time.UTC)
	base := UserTokenIntrospection{
		Active: true, Subject: userRunActorID, Audience: []string{"agentserver-api"},
		Scope: "openid runs:write", ExpiresAt: now.Add(time.Hour).Unix(),
	}
	for _, test := range []struct {
		name   string
		mutate func(*UserTokenIntrospection)
		valid  bool
	}{
		{name: "valid", valid: true},
		{name: "inactive", mutate: func(value *UserTokenIntrospection) { value.Active = false }},
		{name: "wrong audience", mutate: func(value *UserTokenIntrospection) { value.Audience = []string{"executor-gateway"} }},
		{name: "mixed audience", mutate: func(value *UserTokenIntrospection) { value.Audience = append(value.Audience, "executor-gateway") }},
		{name: "missing scope", mutate: func(value *UserTokenIntrospection) { value.Scope = "openid" }},
		{name: "missing expiry", mutate: func(value *UserTokenIntrospection) { value.ExpiresAt = 0 }},
		{name: "expired", mutate: func(value *UserTokenIntrospection) { value.ExpiresAt = now.Unix() }},
		{name: "external subject", mutate: func(value *UserTokenIntrospection) { value.Subject = "oidc-user" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := base
			result.Audience = append([]string(nil), base.Audience...)
			if test.mutate != nil {
				test.mutate(&result)
			}
			introspector := &fixedUserIntrospector{result: result}
			authorizer, err := NewIntrospectedUserAuthorizer(IntrospectedUserAuthorizerConfig{
				Introspector: introspector, ExpectedAudience: "agentserver-api",
				ActionScopes: map[string]string{"runs.create": "runs:write"}, Now: func() time.Time { return now },
			})
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "https://core/v2/runs", nil)
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
	authorizer, _ := NewIntrospectedUserAuthorizer(IntrospectedUserAuthorizerConfig{
		Introspector:     &fixedUserIntrospector{err: errors.New("Hydra unavailable")},
		ExpectedAudience: "agentserver-api", ActionScopes: map[string]string{"runs.create": "runs:write"},
	})
	request := httptest.NewRequest(http.MethodPost, "https://core/v2/runs", nil)
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
				`","client_id":"executor-client","aud":"agentserver-api","scope":"runs:write","exp":2000000000,` +
				`"iat":1999999700,"nbf":1999999700,"iss":"https://hydra.example/","token_type":"Bearer",` +
				`"token_use":"access_token","extra":"ignored"}`)),
			Request: request,
		}, nil
	})}

	introspector, err := NewHydraUserIntrospector("https://hydra-admin/admin/oauth2/introspect", client, false)
	if err != nil {
		t.Fatal(err)
	}
	result, err := introspector.IntrospectUserToken(t.Context(), "opaque-token")
	if err != nil || !result.Active || result.Subject != userRunActorID || result.ClientID != "executor-client" ||
		len(result.Audience) != 1 || result.IssuedAt != 1999999700 || result.NotBefore != 1999999700 ||
		result.Issuer != "https://hydra.example/" || result.TokenType != "Bearer" || result.TokenUse != "access_token" {
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
