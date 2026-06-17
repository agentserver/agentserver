
---

## Amendment 2026-06-17 (afternoon) — DCR off, static client_id in

The DCR-based design above shipped as #249/#251 and **worked**, but
investigation of the Codex CLI source (rmcp-client/perform_oauth_login.rs)
showed Codex calls `start_authorization()`, not the SDK's
`start_authorization_with_metadata_url()` — i.e. **Codex CLI does
not implement CIMD**, only DCR or pre-registered `client_id`. Claude
Code supports DCR + CIMD + pre-registered.

Combined with RFC 7591 §5's explicit warning that open registration
endpoints invite table-bloat attacks, we flipped strategy:

- **Disable DCR** in Hydra (`OIDC_DYNAMIC_CLIENT_REGISTRATION_ENABLED=false`)
- **Stop advertising** `registration_endpoint` in AS metadata
- **Stop reverse-proxying** `/oauth2/register` through agentserver
- **Add per-user static OAuth client management** at
  `POST /api/me/oauth-clients` (and GET, DELETE). User-owned table
  `mcp_oauth_clients` mapping our opaque id → Hydra's client_id; the
  HTTP handler authenticates the user, calls Hydra admin to mint a
  public client (`token_endpoint_auth_method=none`, PKCE-protected),
  records the ownership row, and returns the `client_id` for the user
  to paste into Codex / Claude Code config.

Cost: ~400 LOC backend (migration + db layer + 3 HTTP handlers + auth
admin wrapper + tests). PAT path unchanged.

UX trade: one extra `curl POST` per developer per machine vs. open
DCR. Acceptable because:
- It's a one-time setup, cached in CLI config thereafter
- Maps cleanly to enterprise OAuth norms (every developer creates
  their own GitHub Personal OAuth App once)
- Cuts off the table-bloat vector with no rate-limiting infrastructure

Frontend UI for /api/me/oauth-clients management lives in a follow-up
PR; for v1 docs show the `curl` flow directly.
