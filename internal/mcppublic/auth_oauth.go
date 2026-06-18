package mcppublic

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// OAuthIntrospector is the slice of Hydra's admin API mcppublic needs
// to validate an opaque access token. Extracted to an interface so
// tests can stub it without standing up a real Hydra. The production
// implementation is *HydraIntrospector below — a hand-rolled HTTP
// client that talks to /admin/oauth2/introspect, identical in shape to
// internal/auth.HydraClient.IntrospectToken but inlined here to keep
// the envmcp-public-gateway binary from depending on the
// internal/auth package (which transitively pulls in chi + the whole
// agentserver session-cookie subsystem).
type OAuthIntrospector interface {
	// Introspect calls Hydra's /admin/oauth2/introspect over the
	// shared cluster-internal admin URL with `token` and returns the
	// decoded result. A non-nil result with Active=false is a
	// legitimate "this token is invalid" answer (e.g. expired,
	// revoked) — not an error; the resolver maps that to ErrInvalid.
	// Network/decode failures are surfaced as errors and become 500s
	// at the middleware layer (we cannot fail-open on Hydra outages
	// without letting forged tokens through).
	Introspect(ctx context.Context, token string) (*IntrospectionResult, error)
}

// IntrospectionResult mirrors RFC 7662 + the extra `ext` block Hydra
// returns. Only the fields the resolver actually reads are kept;
// everything else in the JSON is ignored.
type IntrospectionResult struct {
	Active    bool                   `json:"active"`
	Subject   string                 `json:"sub"`
	Scope     string                 `json:"scope"`
	ClientID  string                 `json:"client_id"`
	Audience  audience               `json:"aud,omitempty"`
	ExpiresAt int64                  `json:"exp,omitempty"`
	Ext       map[string]interface{} `json:"ext,omitempty"`
}

// audience is a tiny RFC-7519-compliant unmarshaler that accepts both
// a bare string and a string array. Hydra emits the array form when
// the client was registered with multiple audiences (our DCR-minted
// MCP clients carry one — the resource indicator from RFC 8707 — but
// other admin-created clients may have more). Treating both forms as
// `[]string` lets the resolver's RFC-8707 check be a simple Contains.
type audience []string

func (a *audience) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	if data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		*a = audience{s}
		return nil
	}
	var arr []string
	if err := json.Unmarshal(data, &arr); err != nil {
		return err
	}
	*a = audience(arr)
	return nil
}

// HydraIntrospector is the production *OAuthIntrospector*. It POSTs
// to {AdminURL}/admin/oauth2/introspect with form data. The admin URL
// is reachable only from within the cluster (chart wires a ClusterIP
// Service) — exposing it publicly would let anyone validate tokens
// they don't own, so we never proxy it.
type HydraIntrospector struct {
	// AdminURL is the cluster-internal base URL of Hydra's admin API
	// (e.g. "http://agentserver-hydra-admin:4445"). No trailing slash.
	AdminURL string

	// HTTPClient is the http.Client used for /admin/oauth2/introspect.
	// Tests inject a stub; production sets a 10s-timeout client.
	HTTPClient *http.Client
}

// NewHydraIntrospector builds the production introspector.
func NewHydraIntrospector(adminURL string) *HydraIntrospector {
	return &HydraIntrospector{
		AdminURL:   strings.TrimRight(adminURL, "/"),
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// Introspect POSTs the token to Hydra's introspection endpoint. Hydra
// returns 200 with `{"active": false}` for invalid tokens — that is
// NOT an HTTP error, so we don't treat it as one. Only network /
// 5xx / decode failures become Go errors.
func (h *HydraIntrospector) Introspect(ctx context.Context, token string) (*IntrospectionResult, error) {
	form := url.Values{"token": {token}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		h.AdminURL+"/admin/oauth2/introspect", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build introspect request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := h.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("introspect: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("introspect: status %d: %s", resp.StatusCode, body)
	}
	var out IntrospectionResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode introspect response: %w", err)
	}
	return &out, nil
}

// OAuthResolver turns a Hydra-issued opaque access token into a
// Principal. The token's `ext` claims carry the workspace selection
// the user made on the consent screen (see internal/server/oauth_
// provider.go:174-184) — we trust those claims because the consent
// handler already verified workspace membership at consent time AND
// we re-verify at resolve time (defense in depth, plus the user may
// have been kicked from the workspace between token issuance and use).
//
// Token format: Hydra defaults to opaque random strings; this resolver
// works with that format. If we ever flip Hydra's `strategies.access_
// token` to `jwt` the introspection path still works (Hydra introspects
// JWTs by decoding them server-side), so this resolver is agnostic to
// the underlying access-token strategy.
//
// Resolution pipeline:
//  1. Prefix gate — Hydra opaque tokens start with `ory_at_`; bail
//     fast with ErrUnknown on anything else so PAT-only callers don't
//     pay the introspection round trip.
//  2. Introspect via Hydra admin.
//  3. Reject if !Active (revoked / expired / unknown).
//  4. Verify `aud` includes our gateway URL (RFC 8707 resource-
//     indicator binding — token issued for one MCP server must not
//     work against another).
//  5. Pull `workspace_id` + `workspace_role` from ext claims.
//  6. Re-check the user is still a member of that workspace (catches
//     kick-out-between-issuance-and-use; same belt-and-braces check
//     PATResolver does at line 130).
//  7. Map `scope` string → tool set.
type OAuthResolver struct {
	// DB is the same dbReader interface PATResolver uses; we only
	// need ListWorkspacesByUser for the membership re-check, but
	// reusing the interface keeps the wiring in cmd/ symmetric.
	DB dbReader

	// Introspector talks to Hydra. Nil = resolver is disabled (every
	// call returns ErrUnknown so the middleware falls through to PAT).
	Introspector OAuthIntrospector

	// ExpectedAudience is the canonical resource URL our gateway
	// publishes in the `oauth-protected-resource` doc, e.g.
	// "https://mcp.agent.cs.ac.cn/mcp". Tokens whose `aud` claim
	// does not contain this string are rejected per RFC 8707. Empty
	// disables the check (dev only; never leave unset in prod —
	// without it a token issued for a different MCP server would
	// authenticate here).
	ExpectedAudience string

	// Logger receives one attrs-rich line per Resolve; nil falls back
	// to slog.Default().
	Logger *slog.Logger
}

// hydraTokenPrefix is the literal prefix Hydra prepends to every
// opaque access token (Ory Hydra v2 source: oauth2/handler.go).
// Using it as the resolver's gate avoids running introspection on
// PATs or arbitrary junk.
const hydraTokenPrefix = "ory_at_"

// Resolve implements PrincipalResolver.
func (r *OAuthResolver) Resolve(ctx context.Context, raw string) (*Principal, error) {
	log := r.Logger
	if log == nil {
		log = slog.Default()
	}
	if r.Introspector == nil {
		// Disabled — fall through to next resolver.
		return nil, ErrUnknown
	}
	if !strings.HasPrefix(raw, hydraTokenPrefix) {
		return nil, ErrUnknown
	}

	intro, err := r.Introspector.Introspect(ctx, raw)
	if err != nil {
		// Network / 5xx — surface so middleware returns 500 (we cannot
		// fall through on infrastructure failures without risking
		// fail-open behavior the first time Hydra hiccups).
		return nil, fmt.Errorf("introspect: %w", err)
	}
	if !intro.Active {
		// Revoked / expired / never-issued. Hydra collapses all three
		// into the same response, so we do too.
		log.Info("mcppublic.OAuth: introspect inactive",
			"client_id", intro.ClientID, "sub", intro.Subject)
		return nil, ErrInvalid
	}

	// RFC 8707 resource-indicator binding. Defensive but lenient:
	//
	//   - If the token carries an `aud` claim, it MUST contain our
	//     ExpectedAudience. Hard-fail on mismatch to keep replay
	//     defense for any token that does properly identify a
	//     resource.
	//   - If `aud` is empty, allow through with a warn log. Hydra/
	//     fosite v2.x does NOT implement RFC 8707 — the `resource`
	//     query parameter every spec-compliant MCP client sends
	//     (Claude Code, Codex CLI's `oauth_resource =`) is silently
	//     dropped by Hydra and the issued token has no audience.
	//     See ory/fosite#879 (still in draft, CLA blocked, opened
	//     2026-05-25) for the upstream fix.
	//
	// !!! SECURITY: This empty-aud branch is a fail-open relaxation.
	// It is only safe because:
	//   1. Single-tenant: exactly one MCP gateway exists on this
	//      Hydra (mcp-claude-code,
	//      mcp-codex, mcp-claude-desktop all
	//      target mcp.<host>/mcp). A token from our consent flow can
	//      only have been meant for here.
	//   2. DCR is OFF (#258, hydra.yaml comment): nobody can
	//      register a malicious client to mint mcp:* tokens.
	//   3. Both mcp clients are provisioned by helm with a fixed
	//      `audience` list, so even if a token DOES carry `aud`, it
	//      can only carry our gateway URL.
	//
	// !!! BEFORE adding a second MCP server on the same Hydra,
	// DELETE the empty-aud branch. Otherwise tokens issued for the
	// new gateway will be accepted here (and vice-versa) since
	// neither will carry `aud`. Upstream fix that lets us safely
	// re-enable strict checking: ory/fosite#879 (RFC 8707), OR
	// patch internal/server/oauth_provider.go's consent handler to
	// hardcode GrantAccessTokenAudience: [r.ExpectedAudience] when
	// scope contains mcp:*.
	if r.ExpectedAudience != "" && len(intro.Audience) > 0 {
		if !audienceContains(intro.Audience, r.ExpectedAudience) {
			log.Info("mcppublic.OAuth: audience mismatch",
				"sub", intro.Subject, "want", r.ExpectedAudience, "got", []string(intro.Audience))
			return nil, ErrInvalid
		}
	} else if r.ExpectedAudience != "" {
		log.Warn("mcppublic.OAuth: token has no audience claim, accepting (Hydra lacks RFC 8707)",
			"sub", intro.Subject, "want", r.ExpectedAudience, "client_id", intro.ClientID)
	}

	// Pull the consent-screen-selected workspace from ext claims. The
	// consent handler always populates both keys (oauth_provider.go:
	// 176-184); a missing/empty workspace_id means the token came from
	// a different consent path (e.g. the codex-auth device flow which
	// doesn't pick a workspace) and shouldn't authenticate against
	// this gateway.
	wsID, _ := intro.Ext["workspace_id"].(string)
	role, _ := intro.Ext["workspace_role"].(string)
	if wsID == "" {
		log.Info("mcppublic.OAuth: token missing workspace_id ext claim",
			"sub", intro.Subject, "client_id", intro.ClientID)
		return nil, ErrInvalid
	}
	if role == "" {
		// Possible if the token was issued by an older consent handler
		// that didn't write the role. Treat as invalid rather than
		// guessing; force a re-consent.
		log.Info("mcppublic.OAuth: token missing workspace_role ext claim",
			"sub", intro.Subject, "workspace_id", wsID)
		return nil, ErrInvalid
	}

	// Membership re-check — kicked-out-of-workspace must invalidate
	// the token even though Hydra doesn't know about workspace
	// membership. Mirrors PATResolver.Resolve step 4.
	memberships, err := r.DB.ListWorkspacesByUser(intro.Subject)
	if err != nil {
		return nil, fmt.Errorf("lookup workspace memberships: %w", err)
	}
	stillMember := false
	for _, w := range memberships {
		if w.ID == wsID {
			stillMember = true
			break
		}
	}
	if !stillMember {
		log.Info("mcppublic.OAuth: user no longer member of token's workspace",
			"sub", intro.Subject, "workspace_id", wsID)
		return nil, ErrInvalid
	}

	// Map scope string → tool set. Scope vocabulary is the same the
	// PATResolver consumes — duplicated to avoid coupling.
	const (
		scopeRead = "mcp:read"
		scopeExec = "mcp:exec"
	)
	tools := map[string]struct{}{}
	for _, s := range strings.Fields(intro.Scope) {
		switch s {
		case scopeRead:
			for k := range ToolsRead {
				tools[k] = struct{}{}
			}
		case scopeExec:
			for k := range ToolsExec {
				tools[k] = struct{}{}
			}
		default:
			// OIDC scopes like openid / profile leak in here when the
			// client requests them alongside mcp:*; silently ignore
			// (logging would be too noisy for the common case).
		}
	}

	return &Principal{
		UserID:      intro.Subject,
		WorkspaceID: wsID,
		Tools:       tools,
		OAuthSub:    intro.Subject,
	}, nil
}

// audienceContains reports whether want appears in aud. Trivial linear
// scan — the audience list is always tiny (1-3 entries per the OAuth
// 2.1 spec's MCP profile, often exactly 1).
func audienceContains(aud audience, want string) bool {
	for _, a := range aud {
		if a == want {
			return true
		}
	}
	return false
}
