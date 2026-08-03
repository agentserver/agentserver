package coreserver

import (
	"slices"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/coredb"
)

func TestUserOAuthWorkspaceResourceRequiresOneCanonicalBrowserTarget(t *testing.T) {
	browserURL := browserAuthorizationRequestURL(loginBridgeTestWorkspaceID)
	workspaceID, err := userOAuthWorkspaceResource(corecontract.UserOAuthBrowserAuthority, browserURL)
	if err != nil || workspaceID != loginBridgeTestWorkspaceID {
		t.Fatalf("Browser resource = %q, %v", workspaceID, err)
	}
	if workspaceID, err := userOAuthWorkspaceResource(
		corecontract.UserOAuthPlatformAuthority,
		"https://hydra.internal/oauth2/auth?client_id="+corecontract.PlatformOAuthClientID,
	); err != nil || workspaceID != "" {
		t.Fatalf("Platform resource = %q, %v", workspaceID, err)
	}
	for name, raw := range map[string]string{
		"missing":    "https://hydra.internal/oauth2/auth?client_id=" + corecontract.BrowserOAuthClientID,
		"duplicate":  browserURL + "&resource=" + corecontract.UserOAuthWorkspaceURNPrefix + loginBridgeTestWorkspaceID,
		"wrong urn":  "https://hydra.internal/oauth2/auth?resource=urn:other:workspace:" + loginBridgeTestWorkspaceID,
		"wrong path": "https://hydra.internal/other?resource=" + corecontract.UserOAuthWorkspaceURNPrefix + loginBridgeTestWorkspaceID,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := userOAuthWorkspaceResource(corecontract.UserOAuthBrowserAuthority, raw); err == nil {
				t.Fatalf("invalid Browser resource was accepted: %s", raw)
			}
		})
	}
	if _, err := userOAuthWorkspaceResource(corecontract.UserOAuthPlatformAuthority, browserURL); err == nil {
		t.Fatal("Platform authorization accepted a Browser workspace resource")
	}
}

func TestCompileUserOAuthConsentGrantIntersectsRoleAndRequestedPermissions(t *testing.T) {
	browserProfile := LoginBridgeOAuthProfile{
		Authority: corecontract.UserOAuthBrowserAuthority, ClientID: corecontract.BrowserOAuthClientID,
		Scopes: corecontract.BrowserOAuthScopes(), Audience: []string{corecontract.BrowserOAuthAudience},
	}
	grant, err := compileUserOAuthConsentGrant(
		browserProfile,
		corecontract.BrowserOAuthScopes(),
		loginBridgeTestWorkspaceID,
		[]coredb.UserOAuthMembership{{WorkspaceID: loginBridgeTestWorkspaceID, Role: "viewer", Generation: 9}},
	)
	if err != nil {
		t.Fatal(err)
	}
	wantBrowserScope := []string{
		corecontract.OAuthOpenIDScope,
		corecontract.BrowserOAuthRunsReadScope,
		corecontract.BrowserOAuthSessionsReadScope,
	}
	if !slices.Equal(grant.Scope, wantBrowserScope) || len(grant.Authority.WorkspaceGrants) != 1 ||
		grant.Authority.WorkspaceGrants[0].Generation != 9 ||
		!slices.Equal(grant.Authority.WorkspaceGrants[0].Permissions, wantBrowserScope[1:]) {
		t.Fatalf("viewer Browser consent grant = %+v", grant)
	}
	if _, err := compileUserOAuthConsentGrant(browserProfile, corecontract.BrowserOAuthScopes(), loginBridgeTestWorkspaceID, nil); err == nil {
		t.Fatal("Browser consent without the target membership was compiled")
	}

	platformProfile := LoginBridgeOAuthProfile{
		Authority: corecontract.UserOAuthPlatformAuthority, ClientID: corecontract.PlatformOAuthClientID,
		Scopes: corecontract.PlatformOAuthScopes(), Audience: []string{corecontract.PlatformOAuthAudience},
	}
	requested := []string{
		corecontract.OAuthOpenIDScope,
		corecontract.PlatformOAuthExecutorsCreateScope,
		corecontract.PlatformOAuthExecutorsReadScope,
		corecontract.PlatformOAuthMembersReadScope,
		corecontract.PlatformOAuthWorkspacesCreateScope,
		corecontract.PlatformOAuthWorkspacesReadScope,
	}
	secondWorkspace := "30000000-0000-4000-8000-000000000003"
	grant, err = compileUserOAuthConsentGrant(platformProfile, requested, "", []coredb.UserOAuthMembership{
		{WorkspaceID: secondWorkspace, Role: "developer", Generation: 2},
		{WorkspaceID: loginBridgeTestWorkspaceID, Role: "owner", Generation: 4},
	})
	if err != nil {
		t.Fatal(err)
	}
	if grant.Authority.Authority != corecontract.UserOAuthPlatformAuthority ||
		!slices.Equal(grant.Authority.GlobalPermissions, []string{
			corecontract.PlatformOAuthWorkspacesCreateScope,
			corecontract.PlatformOAuthWorkspacesReadScope,
		}) || len(grant.Authority.WorkspaceGrants) != 2 ||
		grant.Authority.WorkspaceGrants[0].WorkspaceID != loginBridgeTestWorkspaceID ||
		grant.Authority.WorkspaceGrants[1].WorkspaceID != secondWorkspace ||
		slices.Contains(grant.Authority.WorkspaceGrants[1].Permissions, corecontract.PlatformOAuthExecutorsCreateScope) {
		t.Fatalf("Platform consent grant = %+v", grant)
	}
}
