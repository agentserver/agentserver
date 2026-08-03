package coreserver

import (
	"errors"
	"net/url"
	"sort"
	"strings"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/coredb"
)

func userOAuthWorkspaceResource(authority, rawRequestURL string) (string, error) {
	if rawRequestURL == "" || len(rawRequestURL) > 32*1024 || strings.ContainsAny(rawRequestURL, "\x00\r\n") {
		return "", errors.New("Hydra authorization request URL is outside protocol bounds")
	}
	parsed, err := url.Parse(rawRequestURL)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" ||
		parsed.Hostname() == "" || parsed.User != nil || parsed.Opaque != "" || parsed.Path != "/oauth2/auth" ||
		parsed.RawPath != "" || parsed.Fragment != "" || parsed.RawFragment != "" {
		return "", errors.New("Hydra authorization request URL is invalid")
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return "", errors.New("Hydra authorization request query is invalid")
	}
	resources, hasResource := query["resource"]
	switch authority {
	case corecontract.UserOAuthPlatformAuthority:
		if hasResource {
			return "", errors.New("Platform authorization cannot bind a single workspace resource")
		}
		return "", nil
	case corecontract.UserOAuthBrowserAuthority:
		if !hasResource || len(resources) != 1 || !strings.HasPrefix(resources[0], corecontract.UserOAuthWorkspaceURNPrefix) {
			return "", errors.New("Browser authorization requires exactly one workspace resource")
		}
		workspaceID := strings.TrimPrefix(resources[0], corecontract.UserOAuthWorkspaceURNPrefix)
		if !canonicalPublicUUID(workspaceID) || resources[0] != corecontract.UserOAuthWorkspaceURNPrefix+workspaceID {
			return "", errors.New("Browser workspace resource is not canonical")
		}
		return workspaceID, nil
	default:
		return "", errors.New("user OAuth authority is unsupported")
	}
}

func compileUserOAuthConsentGrant(
	profile LoginBridgeOAuthProfile,
	requestedScopes []string,
	workspaceID string,
	memberships []coredb.UserOAuthMembership,
) (HydraConsentGrant, error) {
	requested, err := canonicalOAuthTextSet(requestedScopes, 32)
	if err != nil {
		return HydraConsentGrant{}, err
	}
	if _, ok := requested[corecontract.OAuthOpenIDScope]; !ok {
		return HydraConsentGrant{}, errors.New("openid is required for user consent")
	}
	authority := corecontract.UserOAuthAuthority{
		Version: corecontract.UserOAuthAuthorityVersion, Authority: profile.Authority,
		GlobalPermissions: []string{}, WorkspaceGrants: []corecontract.UserOAuthWorkspaceGrant{},
	}
	switch profile.Authority {
	case corecontract.UserOAuthPlatformAuthority:
		if workspaceID != "" {
			return HydraConsentGrant{}, errors.New("Platform consent unexpectedly selected a workspace")
		}
		authority.GlobalPermissions = requestedPermissionIntersection(corecontract.PlatformOAuthGlobalPermissions(), requested)
		seen := make(map[string]struct{}, len(memberships))
		for _, membership := range memberships {
			if !canonicalPublicUUID(membership.WorkspaceID) || membership.Generation < 1 {
				return HydraConsentGrant{}, errors.New("workspace membership authority is invalid")
			}
			if _, duplicate := seen[membership.WorkspaceID]; duplicate {
				return HydraConsentGrant{}, errors.New("workspace membership authority is duplicated")
			}
			seen[membership.WorkspaceID] = struct{}{}
			eligible, ok := corecontract.PlatformOAuthWorkspacePermissions(membership.Role)
			if !ok {
				return HydraConsentGrant{}, errors.New("workspace membership role is unsupported")
			}
			permissions := requestedPermissionIntersection(eligible, requested)
			if len(permissions) == 0 {
				continue
			}
			authority.WorkspaceGrants = append(authority.WorkspaceGrants, corecontract.UserOAuthWorkspaceGrant{
				WorkspaceID: membership.WorkspaceID, Generation: membership.Generation, Permissions: permissions,
			})
		}
		sort.Slice(authority.WorkspaceGrants, func(left, right int) bool {
			return authority.WorkspaceGrants[left].WorkspaceID < authority.WorkspaceGrants[right].WorkspaceID
		})
	case corecontract.UserOAuthBrowserAuthority:
		if !canonicalPublicUUID(workspaceID) || len(memberships) != 1 || memberships[0].WorkspaceID != workspaceID || memberships[0].Generation < 1 {
			return HydraConsentGrant{}, errors.New("Browser consent has no exact active workspace membership")
		}
		eligible, ok := corecontract.BrowserOAuthWorkspacePermissions(memberships[0].Role)
		if !ok {
			return HydraConsentGrant{}, errors.New("workspace membership role is unsupported")
		}
		permissions := requestedPermissionIntersection(eligible, requested)
		if len(permissions) == 0 {
			return HydraConsentGrant{}, errors.New("Browser consent requested no permission available to the workspace role")
		}
		authority.WorkspaceGrants = []corecontract.UserOAuthWorkspaceGrant{{
			WorkspaceID: workspaceID, Generation: memberships[0].Generation, Permissions: permissions,
		}}
	default:
		return HydraConsentGrant{}, errors.New("user OAuth consent authority is unsupported")
	}

	permissionUnion := make(map[string]struct{})
	for _, permission := range authority.GlobalPermissions {
		permissionUnion[permission] = struct{}{}
	}
	for _, workspace := range authority.WorkspaceGrants {
		for _, permission := range workspace.Permissions {
			permissionUnion[permission] = struct{}{}
		}
	}
	if len(permissionUnion) == 0 {
		return HydraConsentGrant{}, errors.New("user consent has no granted business permission")
	}
	permissions := make([]string, 0, len(permissionUnion))
	for permission := range permissionUnion {
		permissions = append(permissions, permission)
	}
	sort.Strings(permissions)
	grantScope := make([]string, 1, len(permissions)+1)
	grantScope[0] = corecontract.OAuthOpenIDScope
	grantScope = append(grantScope, permissions...)
	return HydraConsentGrant{
		Scope: grantScope, Audience: append([]string(nil), profile.Audience...), Authority: authority,
	}, nil
}

func requestedPermissionIntersection(eligible []string, requested map[string]struct{}) []string {
	permissions := make([]string, 0, len(eligible))
	for _, permission := range eligible {
		if _, ok := requested[permission]; ok {
			permissions = append(permissions, permission)
		}
	}
	sort.Strings(permissions)
	return permissions
}
