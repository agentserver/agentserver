# Agent Whoami Identity Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `GET /api/agent/whoami` so observer can map a sandbox `proxy_token` to user, workspace, sandbox, display name, and role identity.

**Architecture:** Persist `user_id` on sandbox-scoped `proxy_tokens` when tokens are created, then resolve whoami through a strict `proxy_tokens`-based DB helper that refuses `tunnel_token` and workspace-scoped tokens. The HTTP handler performs constant 401 responses for unknown/invalid tokens, 403 for known but unusable sandbox identities, and returns the exact seven-field JSON contract.

**Tech Stack:** Go, net/http, chi, PostgreSQL migrations, swaggo OpenAPI annotations, generated docs via `make openapi` and `make api-docs`.

---

## File Map

- Create `internal/db/migrations/034_proxy_token_user_id.sql`: nullable `proxy_tokens.user_id` and supporting indexes.
- Modify `internal/db/proxy_tokens.go`: add `UserID` to `ProxyToken`, scan it, and update sandbox token insert helper signature.
- Modify `internal/db/sandboxes.go`: accept `userID` in sandbox creation helpers and insert it into `proxy_tokens`.
- Modify `internal/sbxstore/store.go`: accept and forward `userID` when creating cloud sandboxes.
- Modify `internal/server/agent_register.go`: pass OAuth subject into `CreateLocalSandbox`.
- Modify `internal/server/server.go`: pass session user into `Sandboxes.Create` and mount `GET /api/agent/whoami`.
- Create `internal/db/agent_whoami.go`: strict lookup helper and result/status types.
- Create `internal/server/agent_whoami.go`: handler, strict bearer parsing, response mapping, OpenAPI annotations.
- Modify `internal/server/api_types.go`: add `AgentWhoamiResponse`.
- Create `internal/server/agent_whoami_test.go`: handler/DB integration tests.
- Update generated docs: `docs/api/openapi.yaml`, `docs/api/openapi.json`, `docs/api/reference/agent.md`.

## Task 1: Persist User Lineage On Proxy Tokens

**Files:**
- Create: `internal/db/migrations/034_proxy_token_user_id.sql`
- Modify: `internal/db/proxy_tokens.go`
- Modify: `internal/db/sandboxes.go`
- Modify: `internal/sbxstore/store.go`

- [ ] **Step 1: Add the migration**

Create `internal/db/migrations/034_proxy_token_user_id.sql`:

```sql
ALTER TABLE proxy_tokens
  ADD COLUMN IF NOT EXISTS user_id TEXT REFERENCES users(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_proxy_tokens_user
  ON proxy_tokens (user_id)
  WHERE user_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_proxy_tokens_workspace_user
  ON proxy_tokens (workspace_id, user_id)
  WHERE user_id IS NOT NULL;
```

- [ ] **Step 2: Extend `ProxyToken` and its scan query**

In `internal/db/proxy_tokens.go`, add:

```go
	UserID      sql.NullString
```

to `ProxyToken`, and change `GetProxyToken` to select and scan `user_id`:

```go
err := db.QueryRow(
	`SELECT token, token_type, sandbox_id, workspace_id, user_id
	   FROM proxy_tokens WHERE token = $1`, token,
).Scan(&pt.Token, &pt.TokenType, &pt.SandboxID, &pt.WorkspaceID, &pt.UserID)
```

- [ ] **Step 3: Update the sandbox token insert helper**

Change `CreateSandboxProxyToken` in `internal/db/proxy_tokens.go` to:

```go
func (db *DB) CreateSandboxProxyToken(token, sandboxID, workspaceID, userID string) error {
	if token == "" {
		return nil
	}
	_, err := db.Exec(
		`INSERT INTO proxy_tokens (token, token_type, sandbox_id, workspace_id, user_id)
		 VALUES ($1, 'sandbox', $2, $3, NULLIF($4, ''))
		 ON CONFLICT (token) DO NOTHING`,
		token, sandboxID, workspaceID, userID,
	)
	if err != nil {
		return fmt.Errorf("create sandbox proxy token: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Thread `userID` through DB sandbox creation**

In `internal/db/sandboxes.go`, change:

```go
func (db *DB) CreateSandbox(id, workspaceID, name, sandboxType, sandboxName, opencodeToken, proxyToken, openclawToken, shortID string, cpu int, memory int64, idleTimeout *int, metadata json.RawMessage) error
```

to:

```go
func (db *DB) CreateSandbox(id, workspaceID, userID, name, sandboxType, sandboxName, opencodeToken, proxyToken, openclawToken, shortID string, cpu int, memory int64, idleTimeout *int, metadata json.RawMessage) error
```

and change the proxy token insert to:

```go
`INSERT INTO proxy_tokens (token, token_type, sandbox_id, workspace_id, user_id)
 VALUES ($1, 'sandbox', $2, $3, NULLIF($4, '')) ON CONFLICT (token) DO NOTHING`,
proxyToken, id, workspaceID, userID,
```

Change `CreateLocalSandbox` to:

```go
func (db *DB) CreateLocalSandbox(id, workspaceID, userID, name, sandboxType, opencodeToken, proxyToken, tunnelToken, shortID string) error
```

and apply the same `user_id` insert pattern.

- [ ] **Step 5: Thread `userID` through `sbxstore.Store.Create`**

In `internal/sbxstore/store.go`, change:

```go
func (s *Store) Create(id, workspaceID, name, sandboxType, sandboxName, opencodeToken, proxyToken, openclawToken, shortID string, cpu int, memory int64, idleTimeout *int, metadata map[string]interface{}) (*Sandbox, error)
```

to:

```go
func (s *Store) Create(id, workspaceID, userID, name, sandboxType, sandboxName, opencodeToken, proxyToken, openclawToken, shortID string, cpu int, memory int64, idleTimeout *int, metadata map[string]interface{}) (*Sandbox, error)
```

and call:

```go
s.db.CreateSandbox(id, workspaceID, userID, name, sandboxType, sandboxName, opencodeToken, proxyToken, openclawToken, shortID, cpu, memory, idleTimeout, metaJSON)
```

- [ ] **Step 6: Run compile check for changed packages**

Run:

```bash
go test ./internal/db ./internal/sbxstore
```

Expected: compile succeeds; DB integration tests may skip if `TEST_DATABASE_URL` is unset.

- [ ] **Step 7: Commit Task 1**

```bash
git add internal/db/migrations/034_proxy_token_user_id.sql internal/db/proxy_tokens.go internal/db/sandboxes.go internal/sbxstore/store.go
git commit -m "feat(db): record user lineage on sandbox proxy tokens"
```

## Task 2: Write User Lineage At Token Issuance

**Files:**
- Modify: `internal/server/agent_register.go`
- Modify: `internal/server/server.go`

- [ ] **Step 1: Update `/api/agent/register` local sandbox creation**

In `internal/server/agent_register.go`, change:

```go
createErr = s.DB.CreateLocalSandbox(sandboxID, workspaceID, req.Name, sandboxType, opencodePassword, proxyToken, tunnelToken, sid)
```

to:

```go
createErr = s.DB.CreateLocalSandbox(sandboxID, workspaceID, userID, req.Name, sandboxType, opencodePassword, proxyToken, tunnelToken, sid)
```

- [ ] **Step 2: Update Web sandbox creation**

In `internal/server/server.go`, inside `handleCreateSandbox`, add near the existing `wsID := chi.URLParam(r, "wid")`:

```go
userID := auth.UserIDFromContext(r.Context())
```

Then change the `s.Sandboxes.Create` call to include `userID`:

```go
sbx, createErr = s.Sandboxes.Create(id, wsID, userID, req.Name, sandboxType, sandboxName, opencodeToken, proxyToken, openclawToken, sid, cpuMillis, memBytes, idleTimeout, req.Metadata)
```

- [ ] **Step 3: Run compile check for server**

Run:

```bash
go test ./internal/server
```

Expected: compile succeeds; integration tests may skip if `TEST_DATABASE_URL` is unset.

- [ ] **Step 4: Commit Task 2**

```bash
git add internal/server/agent_register.go internal/server/server.go
git commit -m "feat(server): write user lineage for agent proxy tokens"
```

## Task 3: Add Strict Whoami DB Helper

**Files:**
- Create: `internal/db/agent_whoami.go`

- [ ] **Step 1: Add lookup result and state types**

Create `internal/db/agent_whoami.go` with:

```go
package db

import (
	"database/sql"
	"fmt"
)

type AgentWhoamiLookupState string

const (
	AgentWhoamiOK        AgentWhoamiLookupState = "ok"
	AgentWhoamiUnknown   AgentWhoamiLookupState = "unknown"
	AgentWhoamiForbidden AgentWhoamiLookupState = "forbidden"
)

type AgentWhoami struct {
	UserID        string
	WorkspaceID   string
	WorkspaceName string
	SandboxID     string
	ShortID       string
	DisplayName   string
	Role          string
	SandboxStatus string
}
```

- [ ] **Step 2: Add `GetAgentWhoamiByProxyToken`**

Append this function to `internal/db/agent_whoami.go`:

```go
func (db *DB) GetAgentWhoamiByProxyToken(token string) (*AgentWhoami, AgentWhoamiLookupState, error) {
	pt := &ProxyToken{}
	err := db.QueryRow(
		`SELECT token, token_type, sandbox_id, workspace_id, user_id
		   FROM proxy_tokens WHERE token = $1`, token,
	).Scan(&pt.Token, &pt.TokenType, &pt.SandboxID, &pt.WorkspaceID, &pt.UserID)
	if err == sql.ErrNoRows {
		return nil, AgentWhoamiUnknown, nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("get whoami proxy token: %w", err)
	}
	if pt.TokenType != ProxyTokenSandbox || !pt.SandboxID.Valid {
		return nil, AgentWhoamiUnknown, nil
	}
	if !pt.UserID.Valid || pt.UserID.String == "" {
		return nil, AgentWhoamiForbidden, nil
	}

	out := &AgentWhoami{}
	err = db.QueryRow(
		`SELECT pt.user_id,
		        pt.workspace_id,
		        w.name,
		        s.id,
		        COALESCE(s.short_id, ''),
		        COALESCE(NULLIF(ac.display_name, ''), s.name, ''),
		        wm.role,
		        s.status
		   FROM proxy_tokens pt
		   JOIN sandboxes s
		     ON s.id = pt.sandbox_id
		    AND s.workspace_id = pt.workspace_id
		   JOIN workspaces w
		     ON w.id = pt.workspace_id
		   JOIN workspace_members wm
		     ON wm.workspace_id = pt.workspace_id
		    AND wm.user_id = pt.user_id
		   LEFT JOIN agent_cards ac
		     ON ac.sandbox_id = s.id
		  WHERE pt.token = $1
		    AND pt.token_type = 'sandbox'`, token,
	).Scan(&out.UserID, &out.WorkspaceID, &out.WorkspaceName, &out.SandboxID, &out.ShortID, &out.DisplayName, &out.Role, &out.SandboxStatus)
	if err == sql.ErrNoRows {
		return nil, AgentWhoamiForbidden, nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("get agent whoami: %w", err)
	}
	return out, AgentWhoamiOK, nil
}
```

- [ ] **Step 3: Run DB compile check**

Run:

```bash
go test ./internal/db
```

Expected: PASS.

- [ ] **Step 4: Commit Task 3**

```bash
git add internal/db/agent_whoami.go
git commit -m "feat(db): resolve agent whoami identity from proxy token"
```

## Task 4: Add Handler, Route, And Response Type

**Files:**
- Create: `internal/server/agent_whoami.go`
- Modify: `internal/server/api_types.go`
- Modify: `internal/server/server.go`

- [ ] **Step 1: Add response type**

In `internal/server/api_types.go`, after `AgentRegisterResponse`, add:

```go
// AgentWhoamiResponse is returned by GET /api/agent/whoami.
// It contains the full public identity contract for a sandbox proxy token.
type AgentWhoamiResponse struct {
	UserID        string `json:"user_id" validate:"required" example:"u_abc123"`
	WorkspaceID   string `json:"workspace_id" validate:"required" example:"ws_xyz789"`
	WorkspaceName string `json:"workspace_name" validate:"required" example:"Alice's Workspace"`
	SandboxID     string `json:"sandbox_id" validate:"required" example:"sbx_456"`
	ShortID       string `json:"short_id" validate:"required" example:"alice-driver-01"`
	DisplayName   string `json:"display_name" validate:"required" example:"Alice Driver"`
	Role          string `json:"role" validate:"required" example:"developer"`
} // @name AgentWhoamiResponse
```

- [ ] **Step 2: Add handler**

Create `internal/server/agent_whoami.go`:

```go
package server

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/agentserver/agentserver/internal/db"
)

func strictBearerToken(r *http.Request) (string, bool) {
	authz := r.Header.Get("Authorization")
	if !strings.HasPrefix(authz, "Bearer ") {
		return "", false
	}
	token := strings.TrimSpace(strings.TrimPrefix(authz, "Bearer "))
	if token == "" {
		return "", false
	}
	return token, true
}

func activeWhoamiSandboxStatus(status string) bool {
	return status == "creating" || status == "running"
}

// handleAgentWhoami returns the identity represented by a sandbox proxy_token.
// GET /api/agent/whoami
//
//	@Summary   Inspect the calling agent identity (proxy_token auth)
//	@Tags      Agent
//	@Produce   json
//	@Success   200  {object}  AgentWhoamiResponse
//	@Failure   401  {string}  string  "unauthorized"
//	@Failure   403  {string}  string  "forbidden"
//	@Failure   500  {string}  string  "internal error"
//	@Router    /api/agent/whoami [get]
func (s *Server) handleAgentWhoami(w http.ResponseWriter, r *http.Request) {
	token, ok := strictBearerToken(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	who, state, err := s.DB.GetAgentWhoamiByProxyToken(token)
	if err != nil {
		log.Printf("agent whoami: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	switch state {
	case db.AgentWhoamiUnknown:
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	case db.AgentWhoamiForbidden:
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if who == nil || !activeWhoamiSandboxStatus(who.SandboxStatus) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(AgentWhoamiResponse{
		UserID:        who.UserID,
		WorkspaceID:   who.WorkspaceID,
		WorkspaceName: who.WorkspaceName,
		SandboxID:     who.SandboxID,
		ShortID:       who.ShortID,
		DisplayName:   who.DisplayName,
		Role:          who.Role,
	})
}
```

- [ ] **Step 3: Mount route**

In `internal/server/server.go`, after `r.Post("/api/agent/register", s.handleAgentRegister)`, add:

```go
	r.Get("/api/agent/whoami", s.handleAgentWhoami)
```

- [ ] **Step 4: Run server compile check**

Run:

```bash
go test ./internal/server
```

Expected: PASS or integration-test skips only.

- [ ] **Step 5: Commit Task 4**

```bash
git add internal/server/agent_whoami.go internal/server/api_types.go internal/server/server.go
git commit -m "feat(server): add agent whoami endpoint"
```

## Task 5: Add Whoami Tests

**Files:**
- Create: `internal/server/agent_whoami_test.go`

- [ ] **Step 1: Add integration test helpers**

Create `internal/server/agent_whoami_test.go` with:

```go
package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newWhoamiTestServer(t *testing.T) *Server {
	t.Helper()
	d := newCodexTestDBForServer(t)
	t.Cleanup(func() {
		d.Exec(`DELETE FROM agent_cards`)
		d.Exec(`DELETE FROM proxy_tokens`)
		d.Exec(`DELETE FROM sandboxes`)
		d.Exec(`DELETE FROM workspace_members`)
		d.Exec(`DELETE FROM workspaces`)
		d.Exec(`DELETE FROM users`)
	})
	return &Server{DB: d}
}

func seedWhoamiSandbox(t *testing.T, srv *Server, token, tunnelToken, status, role, displayName string, withUser bool) {
	t.Helper()
	seedWorkspaceMember(t, srv.DB, "ws_whoami", "u_whoami", role)
	if _, err := srv.DB.Exec(
		`INSERT INTO sandboxes (id, workspace_id, name, type, status, proxy_token, tunnel_token, short_id)
		 VALUES ('sbx_whoami', 'ws_whoami', 'Sandbox Name', 'custom', $1, $2, $3, 'short-whoami')
		 ON CONFLICT (id) DO UPDATE SET status = EXCLUDED.status, proxy_token = EXCLUDED.proxy_token, tunnel_token = EXCLUDED.tunnel_token`,
		status, token, tunnelToken,
	); err != nil {
		t.Fatalf("insert sandbox: %v", err)
	}
	userID := any(nil)
	if withUser {
		userID = "u_whoami"
	}
	if _, err := srv.DB.Exec(
		`INSERT INTO proxy_tokens (token, token_type, sandbox_id, workspace_id, user_id)
		 VALUES ($1, 'sandbox', 'sbx_whoami', 'ws_whoami', $2)
		 ON CONFLICT (token) DO UPDATE SET user_id = EXCLUDED.user_id`,
		token, userID,
	); err != nil {
		t.Fatalf("insert proxy token: %v", err)
	}
	if displayName != "" {
		if _, err := srv.DB.Exec(
			`INSERT INTO agent_cards (sandbox_id, workspace_id, agent_type, display_name)
			 VALUES ('sbx_whoami', 'ws_whoami', 'custom', $1)
			 ON CONFLICT (sandbox_id) DO UPDATE SET display_name = EXCLUDED.display_name`,
			displayName,
		); err != nil {
			t.Fatalf("insert agent card: %v", err)
		}
	}
}

func callWhoami(t *testing.T, srv *Server, authz string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/agent/whoami", nil)
	if authz != "" {
		req.Header.Set("Authorization", authz)
	}
	rr := httptest.NewRecorder()
	srv.handleAgentWhoami(rr, req)
	return rr
}
```

- [ ] **Step 2: Add happy-path test**

Append:

```go
func TestAgentWhoami_HappyPath(t *testing.T) {
	srv := newWhoamiTestServer(t)
	seedWhoamiSandbox(t, srv, "proxy-good", "tunnel-good", "running", "developer", "Display Agent", true)

	rr := callWhoami(t, srv, "Bearer proxy-good")
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", rr.Header().Get("Cache-Control"))
	}
	var out AgentWhoamiResponse
	if err := json.NewDecoder(rr.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.UserID != "u_whoami" || out.WorkspaceID != "ws_whoami" || out.WorkspaceName != "test ws" ||
		out.SandboxID != "sbx_whoami" || out.ShortID != "short-whoami" ||
		out.DisplayName != "Display Agent" || out.Role != "developer" {
		t.Fatalf("unexpected response: %+v", out)
	}
}
```

- [ ] **Step 3: Add auth failure tests**

Append:

```go
func TestAgentWhoami_UnauthorizedCases(t *testing.T) {
	srv := newWhoamiTestServer(t)
	seedWhoamiSandbox(t, srv, "proxy-good", "tunnel-good", "running", "developer", "", true)
	if _, err := srv.DB.Exec(
		`INSERT INTO proxy_tokens (token, token_type, workspace_id)
		 VALUES ('workspace-token', 'workspace', 'ws_whoami')
		 ON CONFLICT DO NOTHING`,
	); err != nil {
		t.Fatalf("insert workspace token: %v", err)
	}

	cases := []struct {
		name  string
		authz string
	}{
		{"missing", ""},
		{"malformed", "Basic proxy-good"},
		{"empty bearer", "Bearer "},
		{"unknown", "Bearer nope"},
		{"workspace token", "Bearer workspace-token"},
		{"tunnel token", "Bearer tunnel-good"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := callWhoami(t, srv, tc.authz)
			if rr.Code != http.StatusUnauthorized {
				t.Fatalf("want 401, got %d: %s", rr.Code, rr.Body.String())
			}
			if rr.Body.String() != "unauthorized\n" {
				t.Fatalf("body = %q", rr.Body.String())
			}
		})
	}
}
```

- [ ] **Step 4: Add forbidden tests**

Append:

```go
func TestAgentWhoami_ForbiddenCases(t *testing.T) {
	for _, status := range []string{"paused", "offline", "deleting", "pausing"} {
		t.Run("status_"+status, func(t *testing.T) {
			srv := newWhoamiTestServer(t)
			seedWhoamiSandbox(t, srv, "proxy-forbidden", "tunnel-forbidden", status, "developer", "", true)
			rr := callWhoami(t, srv, "Bearer proxy-forbidden")
			if rr.Code != http.StatusForbidden {
				t.Fatalf("want 403, got %d: %s", rr.Code, rr.Body.String())
			}
		})
	}

	t.Run("legacy_null_user", func(t *testing.T) {
		srv := newWhoamiTestServer(t)
		seedWhoamiSandbox(t, srv, "proxy-legacy", "tunnel-legacy", "running", "developer", "", false)
		rr := callWhoami(t, srv, "Bearer proxy-legacy")
		if rr.Code != http.StatusForbidden {
			t.Fatalf("want 403, got %d: %s", rr.Code, rr.Body.String())
		}
	})

	t.Run("membership_removed", func(t *testing.T) {
		srv := newWhoamiTestServer(t)
		seedWhoamiSandbox(t, srv, "proxy-removed", "tunnel-removed", "running", "developer", "", true)
		if _, err := srv.DB.Exec(`DELETE FROM workspace_members WHERE workspace_id = 'ws_whoami' AND user_id = 'u_whoami'`); err != nil {
			t.Fatalf("delete membership: %v", err)
		}
		rr := callWhoami(t, srv, "Bearer proxy-removed")
		if rr.Code != http.StatusForbidden {
			t.Fatalf("want 403, got %d: %s", rr.Code, rr.Body.String())
		}
	})
}
```

- [ ] **Step 5: Add display-name fallback test**

Append:

```go
func TestAgentWhoami_DisplayNameFallsBackToSandboxName(t *testing.T) {
	srv := newWhoamiTestServer(t)
	seedWhoamiSandbox(t, srv, "proxy-fallback", "tunnel-fallback", "running", "developer", "", true)
	rr := callWhoami(t, srv, "Bearer proxy-fallback")
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var out AgentWhoamiResponse
	if err := json.NewDecoder(rr.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.DisplayName != "Sandbox Name" {
		t.Fatalf("display_name = %q, want Sandbox Name", out.DisplayName)
	}
}
```

- [ ] **Step 6: Run whoami tests**

Run:

```bash
go test ./internal/server -run 'TestAgentWhoami'
```

Expected: PASS when `TEST_DATABASE_URL` is set; otherwise tests skip through the shared DB helper.

- [ ] **Step 7: Commit Task 5**

```bash
git add internal/server/agent_whoami_test.go
git commit -m "test(server): cover agent whoami endpoint"
```

## Task 6: Regenerate API Docs And Verify

**Files:**
- Modify: `docs/api/openapi.yaml`
- Modify: `docs/api/openapi.json`
- Modify: `docs/api/reference/agent.md`

- [ ] **Step 1: Regenerate OpenAPI**

Run:

```bash
make openapi
```

Expected: `docs/api/openapi.yaml` and `docs/api/openapi.json` include `GET /api/agent/whoami` and `AgentWhoamiResponse`.

- [ ] **Step 2: Regenerate Markdown API reference**

Run:

```bash
make api-docs
```

Expected: `docs/api/reference/agent.md` lists `GET /api/agent/whoami`.

- [ ] **Step 3: Run verification**

Run:

```bash
go test ./internal/db ./internal/sbxstore ./internal/server
make openapi-check
make api-docs-check
```

Expected: all commands pass; DB-backed tests skip if `TEST_DATABASE_URL` is unset.

- [ ] **Step 4: Commit Task 6**

```bash
git add docs/api/openapi.yaml docs/api/openapi.json docs/api/reference/agent.md
git commit -m "docs(api): document agent whoami endpoint"
```

## Self-Review

Spec coverage:

- The endpoint contract and seven fields are covered by Task 4.
- User lineage persistence is covered by Tasks 1 and 2.
- Strict `proxy_token` parsing and rejection of `tunnel_token` / workspace tokens are covered by Tasks 3, 4, and 5.
- 401 and 403 semantics are covered by Tasks 4 and 5.
- `display_name` fallback is covered by Tasks 3 and 5.
- OpenAPI and generated reference docs are covered by Task 6.

No placeholder steps remain. Type names are consistent across tasks:
`AgentWhoami`, `AgentWhoamiLookupState`, `AgentWhoamiResponse`, and
`handleAgentWhoami`.
