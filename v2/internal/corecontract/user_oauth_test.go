package corecontract

import (
	"slices"
	"strings"
	"testing"
)

func TestUserOAuthPermissionRegistriesAreClosedAndDisjoint(t *testing.T) {
	platform := PlatformOAuthScopes()
	browser := BrowserOAuthScopes()
	assertOAuthScopeRegistry(t, "platform", platform, PlatformOAuthActionPermissions())
	assertOAuthScopeRegistry(t, "browser", browser, BrowserOAuthActionPermissions())

	platformSet := textSet(platform)
	for _, scope := range browser {
		if scope != OAuthOpenIDScope {
			if _, exists := platformSet[scope]; exists {
				t.Fatalf("business OAuth scope %q is shared by Platform and Browser", scope)
			}
		}
	}
	if PlatformOAuthClientID == BrowserOAuthClientID || PlatformOAuthAudience == BrowserOAuthAudience {
		t.Fatal("Platform and Browser OAuth profiles are not distinct")
	}
}

func TestUserOAuthRegistriesReturnFreshAuthority(t *testing.T) {
	platform := PlatformOAuthScopes()
	platform[0] = "mutated"
	if PlatformOAuthScopes()[0] != OAuthOpenIDScope {
		t.Fatal("Platform scope registry leaked mutable shared state")
	}
	browserActions := BrowserOAuthActionPermissions()
	authority := browserActions["runs.create"]
	authority.Permissions[0] = "mutated"
	if BrowserOAuthActionPermissions()["runs.create"].Permissions[0] != BrowserOAuthRunsCreateScope {
		t.Fatal("Browser action registry leaked mutable shared state")
	}
}

func TestUserOAuthRoleCompilationIsLeastPrivilegeAndCanonical(t *testing.T) {
	platformOwner, ok := PlatformOAuthWorkspacePermissions("owner")
	if !ok || !slices.IsSorted(platformOwner) {
		t.Fatalf("Platform owner permissions = %v, %v", platformOwner, ok)
	}
	platformBusiness := append([]string(nil), PlatformOAuthScopes()[1:]...)
	slices.Sort(platformBusiness)
	global := textSet(PlatformOAuthGlobalPermissions())
	wantWorkspace := make([]string, 0, len(platformBusiness))
	for _, permission := range platformBusiness {
		if permission != PlatformOAuthWorkspacesCreateScope {
			wantWorkspace = append(wantWorkspace, permission)
		}
	}
	if !slices.Equal(platformOwner, wantWorkspace) || len(global) != 2 {
		t.Fatalf("Platform owner/global permissions = %v / %v", platformOwner, global)
	}
	developer, _ := PlatformOAuthWorkspacePermissions("developer")
	if slices.Contains(developer, PlatformOAuthExecutorsCreateScope) ||
		!slices.Contains(developer, PlatformOAuthLLMGrantsAuthorizeScope) {
		t.Fatalf("Platform developer permissions = %v", developer)
	}
	viewer, _ := PlatformOAuthWorkspacePermissions("viewer")
	if !slices.Equal(viewer, []string{PlatformOAuthWorkspacesReadScope}) {
		t.Fatalf("Platform viewer permissions = %v", viewer)
	}
	browserOwner, _ := BrowserOAuthWorkspacePermissions("owner")
	browserDeveloper, _ := BrowserOAuthWorkspacePermissions("developer")
	browserViewer, _ := BrowserOAuthWorkspacePermissions("viewer")
	if !slices.Equal(browserOwner, browserDeveloper) || !slices.IsSorted(browserOwner) ||
		!slices.Equal(browserViewer, []string{BrowserOAuthRunsReadScope, BrowserOAuthSessionsReadScope}) {
		t.Fatalf("Browser role permissions = owner %v developer %v viewer %v", browserOwner, browserDeveloper, browserViewer)
	}
	if _, ok := BrowserOAuthWorkspacePermissions("future-role"); ok {
		t.Fatal("unknown workspace role received Browser permissions")
	}
}

func assertOAuthScopeRegistry(t *testing.T, name string, scopes []string, actions map[string]UserOAuthActionAuthority) {
	t.Helper()
	if len(scopes) < 2 || scopes[0] != OAuthOpenIDScope {
		t.Fatalf("%s OAuth scopes = %v", name, scopes)
	}
	known := make(map[string]bool, len(scopes))
	used := make(map[string]bool, len(scopes))
	for _, scope := range scopes {
		if scope == "" || strings.TrimSpace(scope) != scope || strings.ContainsAny(scope, " \t\r\n\x00") || known[scope] {
			t.Fatalf("%s OAuth scope %q is not unique canonical text", name, scope)
		}
		known[scope] = true
	}
	for action, authority := range actions {
		if action == "" || strings.TrimSpace(action) != action || strings.ContainsAny(action, " \t\r\n\x00") ||
			len(authority.Permissions) == 0 ||
			(authority.Resource != UserOAuthGlobalResource && authority.Resource != UserOAuthWorkspaceResource) {
			t.Fatalf("%s OAuth action %q has invalid authority", name, action)
		}
		if name == "browser" && authority.Resource != UserOAuthWorkspaceResource {
			t.Fatalf("Browser OAuth action %q is not workspace-bound", action)
		}
		copyPermissions := append([]string(nil), authority.Permissions...)
		slices.Sort(copyPermissions)
		if slices.Contains(copyPermissions, OAuthOpenIDScope) {
			t.Fatalf("%s OAuth action %q uses openid as a business permission", name, action)
		}
		for index, permission := range copyPermissions {
			if !known[permission] || (index > 0 && permission == copyPermissions[index-1]) {
				t.Fatalf("%s OAuth action %q has unknown or duplicate permission %q", name, action, permission)
			}
			used[permission] = true
		}
	}
	for _, scope := range scopes[1:] {
		if !used[scope] {
			t.Errorf("%s OAuth scope %q is not used by an endpoint action", name, scope)
		}
	}
}

func textSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}
