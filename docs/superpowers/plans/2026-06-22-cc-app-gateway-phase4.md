# cc-app-gateway Phase 4 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add IM intake on the agentserver side so a WeChat message with `routing_mode=managed_cc` reaches claude via cc-app-gateway and the reply round-trips back to WeChat. This is the "I can talk to claude from my phone" milestone.

**Architecture:** New `cc_im_inbound.go` handler on agentserver mirrors `codex_im_inbound.go`. Adds `claude_session_id` column to `agent_sessions` (pure UUID for cc-app-gateway's strict uuidRe). ccDispatcher is a copy of codexDispatcher (~100 LOC, intentional per spec Open Risk #7). imbridge gains `forwardToManagedCC()`. cc-app-gateway's `CcTurnResponse` amends to include `ErrorMessage`. All inter-service hops use synchronous HTTP with `X-Internal-Secret` (codex pattern, no async/callback).

**Tech Stack:** Go 1.26, stdlib + already-present deps. No new libraries. Touches BOTH cc-app-gateway code (turn_api.go ErrorMessage field) AND agentserver code (~7 new/modified files).

**Spec:** `docs/superpowers/specs/2026-06-22-cc-app-gateway-phase4-design.md` (read § Architecture diagram, § The session ID complication, § Audit revisions before starting).

**Working directory:** `/root/agentserver/.claude/worktrees/cc-app-gateway-phase4` (worktree on `feat/cc-app-gateway-phase4`, stacked on Phase 1+2).

**Module path:** `github.com/agentserver/agentserver`.

## Global Constraints

- Go 1.26, stdlib + already-present deps only — no new direct deps.
- Phase 4 amends BOTH cc-app-gateway (Phase 1 contract) AND agentserver (new code). Treat as one PR.
- `agent_sessions.claude_session_id` is **immutable per session** but **must be settable retroactively** when migrating an existing codex/nanoclaw session to managed_cc (NULL → minted UUID; once set, stays).
- ccDispatcher = literal copy of codexDispatcher; do NOT extract generic package (spec Open Risk #7).
- `Bridge.forwardMessage()` (NOT `routeMessage()`) is the actual function name at imbridge/bridge.go:414.
- `X-Internal-Secret` header is explicit code, not implied by "mirrors codex".
- agentserver pod helm template: wrap entire `- name: CC_APP_GATEWAY_REST_URL` block inside `{{- if .Values.ccAppGateway.enabled }}` (NOT just the value).
- `Server.Close()` MUST wire `ccHandler.Close()` (mirrors codex at server.go:162).
- `claude_session_id` format must satisfy cc-app-gateway's `uuidRe = ^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`. `uuid.NewString()` from `github.com/google/uuid` produces this format (verified — package already in go.mod, used by codex).
- Synchronous HTTP all the way (codex pattern); no callbackUrl async. 61-min HTTP client timeout.
- `managed_cc` is the routing_mode wire string (spec § Naming).

## File Structure

| File | Status | Responsibility |
|---|---|---|
| `internal/db/migrations/036_agent_sessions_claude_session_id.sql` | NEW | Add nullable column + partial index |
| `internal/db/agent_sessions.go` | MODIFY | Row struct + SELECT lists include `claude_session_id`; new `SetSessionClaudeSessionID(ctx, sessionID, claudeSessionID) error` setter |
| `internal/server/codex_im_inbound.go` | MODIFY | `sessionView` struct gains `ClaudeSessionID string` field (shared between codex+cc; codex tests pass zero value) |
| `internal/server/cc_im_inbound.go` | NEW | `ccInboundHandler` + `ccDbSessionStore` + `ccDispatcher` + processTurn + sendText/sendError + Close() |
| `internal/server/cc_im_inbound_test.go` | NEW | Table-driven: new session create, existing session reuse, NULL claude_session_id migration path, error matrix, dispatcher serialization |
| `internal/server/cc_client.go` | NEW | `CcClient.RunTurn(ctx, req) (*CcTurnResponse, error)` with 61-min timeout |
| `internal/server/cc_client_test.go` | NEW | httptest server returns canned CcTurnResponse; assert URL/headers/body shape |
| `internal/server/server.go` | MODIFY | Add `CcAppGatewayURL`, `ccHandler`; wire `/api/internal/imbridge/cc/turn` route conditionally; `ccHandler.Close()` in Server.Close() |
| `internal/ccappgateway/turn_api.go` | MODIFY | Add `ErrorMessage string` to `CcTurnResponse`; populate from `Meta.ErrorMessage` |
| `internal/ccappgateway/turn_api_test.go` | MODIFY | Update `TestServeHTTP_IsErrorReturned200` (or equivalent) to assert errorMessage |
| `internal/imbridge/bridge.go` | MODIFY | `forwardMessage()` switch gains `case "managed_cc":`; new `forwardToManagedCC()`; `BridgeBinding.RoutingMode` doc comment updated |
| `internal/imbridgesvc/handlers.go` | MODIFY | Validator at L977 accepts `managed_cc` |
| `internal/imbridgesvc/handlers_test.go` | MODIFY | Test the validator change |
| `deploy/helm/agentserver/templates/deployment.yaml` (agentserver) | MODIFY | Add `CC_APP_GATEWAY_REST_URL` env var, ENTIRE block inside `{{- if .Values.ccAppGateway.enabled }}` |
| `internal/server/cc_im_integration_test.go` | NEW | `//go:build integration` — extends Phase 2 docker-compose with a mock imbridge listener; sends two IM messages, asserts reply text from claude lands on the imbridge mock |

Total: 7 new + 8 modified files. Estimated LOC: ~1500 (~700 production + ~800 tests).

---

## Task 1: migration 036 + DB layer (agent_sessions.go)

**Files:**
- Create: `internal/db/migrations/036_agent_sessions_claude_session_id.sql`
- Modify: `internal/db/agent_sessions.go`

**Interfaces:**
- Consumes: nothing new.
- Produces (later tasks use):
  - `agent_sessions.claude_session_id TEXT` (nullable)
  - Partial index `idx_agent_sessions_claude_session_id` (WHERE NOT NULL)
  - `(*DB).SetSessionClaudeSessionID(ctx context.Context, sessionID, claudeSessionID string) error` — UPDATE row; error if no row matches.
  - `Session` row struct (or equivalent) gains `ClaudeSessionID sql.NullString` field; existing SELECT statements include the column.

- [ ] **Step 1: Write the failing test for the setter**

Create or append `internal/db/agent_sessions_test.go`:

```go
func TestSetSessionClaudeSessionID(t *testing.T) {
    db := newTestDB(t)  // existing helper (or t.Skip if not available, prefer integration-style)
    ctx := context.Background()

    // Set up a row.
    err := db.CreateAgentSession(ctx, db.CreateAgentSessionInput{
        ID:          "cse_test_abc",
        SandboxID:   sql.NullString{},
        WorkspaceID: "ws_test",
        Title:       "test",
    })
    if err != nil { t.Fatal(err) }

    // SetSessionClaudeSessionID populates the new column.
    cid := "11111111-1111-4111-8111-111111111111"
    if err := db.SetSessionClaudeSessionID(ctx, "cse_test_abc", cid); err != nil {
        t.Fatalf("SetSessionClaudeSessionID: %v", err)
    }

    // Read it back via existing query path.
    sess, err := db.GetAgentSession(ctx, "cse_test_abc")
    if err != nil { t.Fatal(err) }
    if !sess.ClaudeSessionID.Valid || sess.ClaudeSessionID.String != cid {
        t.Errorf("ClaudeSessionID: got %v, want %q", sess.ClaudeSessionID, cid)
    }

    // Updating an existing claude_session_id (e.g. drift) overwrites.
    cid2 := "22222222-2222-4222-8222-222222222222"
    if err := db.SetSessionClaudeSessionID(ctx, "cse_test_abc", cid2); err != nil { t.Fatal(err) }
    sess, _ = db.GetAgentSession(ctx, "cse_test_abc")
    if sess.ClaudeSessionID.String != cid2 {
        t.Errorf("update didn't take: %q", sess.ClaudeSessionID.String)
    }

    // Setting on nonexistent session returns an error.
    if err := db.SetSessionClaudeSessionID(ctx, "cse_does_not_exist", "33333333-3333-4333-8333-333333333333"); err == nil {
        t.Error("SetSessionClaudeSessionID on missing row should error")
    }
}
```

(If the existing test infrastructure doesn't have `newTestDB(t)` ready, look at how other `agent_sessions_test.go` tests work — there should be a pattern in the existing codex_thread_id setter test.)

- [ ] **Step 2: Run test to verify it fails**

```
go test -v -run TestSetSessionClaudeSessionID ./internal/db/
```
Expected: build error (`SetSessionClaudeSessionID` undefined) OR migration error (column doesn't exist).

- [ ] **Step 3: Write the migration**

`internal/db/migrations/036_agent_sessions_claude_session_id.sql`:

```sql
-- 036_agent_sessions_claude_session_id.sql
-- Phase 4 (cc-app-gateway IM intake): cc-app-gateway requires pure-UUID
-- sessionId per Phase 1's turn_api.go uuidRe validation. agent_sessions.id
-- uses "cse_<uuid>" format which doesn't satisfy that regex; this column
-- holds the cc-app-gateway-compatible session identifier. Nullable; only
-- populated for rows that use the managed_cc routing mode.
ALTER TABLE agent_sessions ADD COLUMN IF NOT EXISTS claude_session_id TEXT;

-- Reverse-lookup index for Phase 5+ (NOT used in Phase 4).
CREATE INDEX IF NOT EXISTS idx_agent_sessions_claude_session_id
    ON agent_sessions (claude_session_id)
    WHERE claude_session_id IS NOT NULL;
```

- [ ] **Step 4: Update `agent_sessions.go`**

a) Add field to row struct:

```go
type Session struct {
    // ... existing fields ...
    ClaudeSessionID sql.NullString
}
```

b) Update existing SELECT statements (find every place that loads a `Session` row — `GetAgentSession`, `GetSessionByExternalID`, listers, etc.) to include `claude_session_id` in the column list and scan it into the new field.

c) Add `SetSessionClaudeSessionID`:

```go
// SetSessionClaudeSessionID writes the cc-app-gateway-compatible session
// identifier (pure UUID) for the given agent_sessions row. Used by the
// managed_cc IM handler to record the session ID it minted, and to
// upgrade existing codex/nanoclaw sessions when their channel migrates
// to managed_cc (see spec § Audit Revision #2).
func (db *DB) SetSessionClaudeSessionID(ctx context.Context, sessionID, claudeSessionID string) error {
    res, err := db.pool.ExecContext(ctx,
        `UPDATE agent_sessions SET claude_session_id = $1, updated_at = NOW() WHERE id = $2`,
        claudeSessionID, sessionID,
    )
    if err != nil {
        return fmt.Errorf("SetSessionClaudeSessionID: %w", err)
    }
    n, _ := res.RowsAffected()
    if n == 0 {
        return fmt.Errorf("SetSessionClaudeSessionID: no row with id=%s", sessionID)
    }
    return nil
}
```

(Match the existing style of `SetSessionCodexThreadID` — same package, same pattern.)

- [ ] **Step 5: Run test to verify it passes**

```
go test -v -run TestSetSessionClaudeSessionID ./internal/db/
```

- [ ] **Step 6: Sanity build the full module**

```
go build ./...
```

(Catches any other SELECT * stragglers that need updating.)

- [ ] **Step 7: Commit**

```
git add internal/db/migrations/036_agent_sessions_claude_session_id.sql internal/db/agent_sessions.go internal/db/agent_sessions_test.go
git commit -m "feat(db): add claude_session_id column + SetSessionClaudeSessionID setter (Phase 4)"
```

---

## Task 2: amend `CcTurnResponse` with ErrorMessage (cc-app-gateway side)

**Files:**
- Modify: `internal/ccappgateway/turn_api.go`
- Modify: `internal/ccappgateway/turn_api_test.go`

**Interfaces:**
- Consumes: `runner.ResultMeta.ErrorMessage string` (Phase 1, runner/events.go).
- Produces (Task 5 consumes):
  - `CcTurnResponse.ErrorMessage string` JSON-tagged `errorMessage,omitempty`.

This is a tiny additive change to Phase 1's contract. Lands on the same branch as Phase 4 (PR #281).

- [ ] **Step 1: Write a failing test**

Find the existing `TestServeHTTP_IsErrorReturned200` (or equivalent — search turn_api_test.go for `IsError`). Append to it OR add a new test:

```go
func TestServeHTTP_IsErrorPopulatesErrorMessage(t *testing.T) {
    fakeRunner := func(_ context.Context, _ runner.RunInput) (*runner.RunResult, error) {
        return &runner.RunResult{
            AssistantText: "",
            Meta: &runner.ResultMeta{
                Subtype:      "error",
                IsError:      true,
                ErrorMessage: "context window exceeded",
            },
        }, nil
    }
    srv, _ := newTestServerWithStoreAndRunner(t, newFakeStore(), fakeRunner)
    rr := postTurn(t, srv, `{"workspaceId":"ws_test","sessionId":"00000000-0000-4000-8000-000000000001","userMessage":"hi"}`)
    if rr.Code != http.StatusOK {
        t.Fatalf("status: %d body: %s", rr.Code, rr.Body.String())
    }
    var resp struct {
        IsError      bool   `json:"isError"`
        ErrorMessage string `json:"errorMessage"`
    }
    if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil { t.Fatal(err) }
    if !resp.IsError {
        t.Error("isError should be true")
    }
    if resp.ErrorMessage != "context window exceeded" {
        t.Errorf("errorMessage: got %q, want %q", resp.ErrorMessage, "context window exceeded")
    }
}
```

- [ ] **Step 2: Run test to verify failure**

```
go test -v -run TestServeHTTP_IsErrorPopulatesErrorMessage ./internal/ccappgateway/
```
Expected: assertion fails because `errorMessage` JSON field is empty (struct missing the field).

- [ ] **Step 3: Add `ErrorMessage` field to `CcTurnResponse`**

```go
type CcTurnResponse struct {
    SessionID     string                       `json:"sessionId"`
    AssistantText string                       `json:"assistantText"`
    IsError       bool                         `json:"isError"`
    ErrorMessage  string                       `json:"errorMessage,omitempty"` // NEW (Phase 4)
    DurationMs    int64                        `json:"durationMs"`
    TotalCostUSD  float64                      `json:"totalCostUsd"`
    ModelUsage    map[string]runner.ModelUsage `json:"modelUsage,omitempty"`
}
```

In the response-builder section of `ServeHTTP`, populate it:

```go
if result.Meta != nil {
    resp.IsError = result.Meta.IsError
    resp.ErrorMessage = result.Meta.ErrorMessage  // NEW line
    resp.TotalCostUSD = result.Meta.TotalCostUSD
    resp.ModelUsage = result.Meta.ModelUsage
}
```

- [ ] **Step 4: Run test to verify pass**

```
go test -v ./internal/ccappgateway/
```
All ccappgateway tests must still pass (the new field is additive, won't break Phase 2 tests).

- [ ] **Step 5: Commit**

```
git commit -am "feat(cc-app-gateway): amend CcTurnResponse with ErrorMessage for Phase 4 error matrix"
```

---

## Task 3: `internal/server/cc_client.go` + tests

**Files:**
- Create: `internal/server/cc_client.go`
- Create: `internal/server/cc_client_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces (Task 4 uses):
  - `type CcClient struct { ... }`; `NewCcClient(baseURL, secret string) *CcClient`
  - `type CcTurnRequest struct { WorkspaceID, SessionID, UserMessage, Model string; TimeoutMs int }`
  - `type CcTurnResponse struct { SessionID, AssistantText, ErrorMessage string; IsError bool; DurationMs int64; TotalCostUSD float64; ModelUsage map[string]any }`
  - `func (c *CcClient) RunTurn(ctx context.Context, req CcTurnRequest) (*CcTurnResponse, error)`
  - `func resolveCCAppGatewayRESTURL() string` — reads `CC_APP_GATEWAY_REST_URL`, trims trailing slash, returns "" if unset.

Mirror `codex_client.go` exactly in shape — 61-minute HTTP timeout, X-Internal-Secret header, JSON encode/decode.

- [ ] **Step 1: Write failing tests**

Create `internal/server/cc_client_test.go`:

```go
package server_test

import (
    "context"
    "encoding/json"
    "io"
    "net/http"
    "net/http/httptest"
    "strings"
    "testing"

    "github.com/agentserver/agentserver/internal/server"
)

func TestCcClient_RunTurn_HappyPath(t *testing.T) {
    var gotBody string
    var gotPath string
    var gotSecret string
    ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        gotPath = r.URL.Path
        gotSecret = r.Header.Get("X-Internal-Secret")
        b, _ := io.ReadAll(r.Body)
        gotBody = string(b)
        w.Header().Set("Content-Type", "application/json")
        json.NewEncoder(w).Encode(map[string]any{
            "sessionId":     "abc-123",
            "assistantText": "pong",
            "isError":       false,
            "durationMs":    int64(42),
            "totalCostUsd":  0.0001,
        })
    }))
    defer ts.Close()

    c := server.NewCcClient(ts.URL, "secret123")
    resp, err := c.RunTurn(context.Background(), server.CcTurnRequest{
        WorkspaceID: "ws_test",
        SessionID:   "00000000-0000-4000-8000-000000000001",
        UserMessage: "hi",
    })
    if err != nil { t.Fatalf("RunTurn: %v", err) }
    if gotPath != "/api/turns" { t.Errorf("path: %q", gotPath) }
    if gotSecret != "secret123" { t.Errorf("secret: %q", gotSecret) }
    if !strings.Contains(gotBody, `"workspaceId":"ws_test"`) {
        t.Errorf("body missing workspaceId: %q", gotBody)
    }
    if resp.AssistantText != "pong" {
        t.Errorf("AssistantText: %q", resp.AssistantText)
    }
}

func TestCcClient_RunTurn_HTTPError(t *testing.T) {
    ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusInternalServerError)
        w.Write([]byte(`{"error":"runner_failed","code":"runner_failed"}`))
    }))
    defer ts.Close()

    c := server.NewCcClient(ts.URL, "secret123")
    _, err := c.RunTurn(context.Background(), server.CcTurnRequest{
        WorkspaceID: "ws", SessionID: "00000000-0000-4000-8000-000000000001", UserMessage: "hi",
    })
    if err == nil {
        t.Fatal("expected error on 500")
    }
    if !strings.Contains(err.Error(), "500") {
        t.Errorf("error should mention status: %v", err)
    }
}

func TestCcClient_RunTurn_DecodesErrorMessage(t *testing.T) {
    ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        json.NewEncoder(w).Encode(map[string]any{
            "sessionId":     "abc",
            "assistantText": "",
            "isError":       true,
            "errorMessage":  "context window exceeded",
            "durationMs":    int64(1000),
        })
    }))
    defer ts.Close()

    c := server.NewCcClient(ts.URL, "")
    resp, err := c.RunTurn(context.Background(), server.CcTurnRequest{
        WorkspaceID: "ws", SessionID: "00000000-0000-4000-8000-000000000001", UserMessage: "hi",
    })
    if err != nil { t.Fatal(err) }
    if !resp.IsError {
        t.Error("IsError should be true")
    }
    if resp.ErrorMessage != "context window exceeded" {
        t.Errorf("ErrorMessage: got %q", resp.ErrorMessage)
    }
}

func TestResolveCCAppGatewayRESTURL_Trim(t *testing.T) {
    t.Setenv("CC_APP_GATEWAY_REST_URL", "http://cc-app-gateway.svc:8087/")
    got := server.ResolveCCAppGatewayRESTURL()
    if got != "http://cc-app-gateway.svc:8087" {
        t.Errorf("trailing slash not trimmed: %q", got)
    }
}

func TestResolveCCAppGatewayRESTURL_Empty(t *testing.T) {
    t.Setenv("CC_APP_GATEWAY_REST_URL", "")
    if server.ResolveCCAppGatewayRESTURL() != "" {
        t.Errorf("empty env should return empty string")
    }
}
```

- [ ] **Step 2: Run test to verify failure**

```
go test -v ./internal/server/ -run TestCcClient
```
Expected: build error (CcClient/CcTurnRequest/CcTurnResponse/ResolveCCAppGatewayRESTURL undefined).

- [ ] **Step 3: Implement `cc_client.go`**

```go
package server

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "os"
    "strings"
    "time"
)

// CcClient calls cc-app-gateway's POST /api/turns from agentserver.
// Mirrors CodexClient (codex_client.go) — synchronous HTTP with 61-minute
// timeout (well above cc-app-gateway's 10-minute runner cap).
type CcClient struct {
    baseURL string
    secret  string
    http    *http.Client
}

// CcTurnRequest is the JSON body POSTed to cc-app-gateway /api/turns.
type CcTurnRequest struct {
    WorkspaceID string `json:"workspaceId"`
    SessionID   string `json:"sessionId"`
    UserMessage string `json:"userMessage"`
    Model       string `json:"model,omitempty"`
    TimeoutMs   int    `json:"timeoutMs,omitempty"`
}

// CcTurnResponse is the JSON body returned by cc-app-gateway on 200.
type CcTurnResponse struct {
    SessionID     string         `json:"sessionId"`
    AssistantText string         `json:"assistantText"`
    IsError       bool           `json:"isError"`
    ErrorMessage  string         `json:"errorMessage,omitempty"`
    DurationMs    int64          `json:"durationMs"`
    TotalCostUSD  float64        `json:"totalCostUsd"`
    ModelUsage    map[string]any `json:"modelUsage,omitempty"`
}

func NewCcClient(baseURL, secret string) *CcClient {
    return &CcClient{
        baseURL: strings.TrimRight(baseURL, "/"),
        secret:  secret,
        http:    &http.Client{Timeout: 61 * time.Minute},
    }
}

// RunTurn POSTs the request to cc-app-gateway's /api/turns and decodes
// the response. Non-2xx responses are returned as errors with the body
// preview attached.
func (c *CcClient) RunTurn(ctx context.Context, req CcTurnRequest) (*CcTurnResponse, error) {
    body, err := json.Marshal(req)
    if err != nil {
        return nil, fmt.Errorf("CcClient.RunTurn marshal: %w", err)
    }
    hreq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/api/turns", bytes.NewReader(body))
    if err != nil {
        return nil, fmt.Errorf("CcClient.RunTurn build request: %w", err)
    }
    hreq.Header.Set("Content-Type", "application/json")
    if c.secret != "" {
        hreq.Header.Set("X-Internal-Secret", c.secret)
    }
    hresp, err := c.http.Do(hreq)
    if err != nil {
        return nil, fmt.Errorf("CcClient.RunTurn do: %w", err)
    }
    defer hresp.Body.Close()
    if hresp.StatusCode/100 != 2 {
        b, _ := io.ReadAll(io.LimitReader(hresp.Body, 1024))
        return nil, fmt.Errorf("CcClient.RunTurn: status=%d body=%q", hresp.StatusCode, b)
    }
    var out CcTurnResponse
    if err := json.NewDecoder(hresp.Body).Decode(&out); err != nil {
        return nil, fmt.Errorf("CcClient.RunTurn decode: %w", err)
    }
    return &out, nil
}

// ResolveCCAppGatewayRESTURL reads CC_APP_GATEWAY_REST_URL from env,
// trims trailing slash. Returns "" if unset (caller treats as disabled).
func ResolveCCAppGatewayRESTURL() string {
    return strings.TrimRight(strings.TrimSpace(os.Getenv("CC_APP_GATEWAY_REST_URL")), "/")
}
```

- [ ] **Step 4: Run tests to verify pass**

```
go test -v ./internal/server/ -run TestCcClient
```

- [ ] **Step 5: Commit**

```
git commit -am "feat(server): CcClient for agentserver→cc-app-gateway POST /api/turns"
```

---

## Task 4: `sessionView` ClaudeSessionID extension (codex_im_inbound.go)

**Files:**
- Modify: `internal/server/codex_im_inbound.go`

**Interfaces:**
- Consumes: Task 1's `Session.ClaudeSessionID sql.NullString`.
- Produces (Task 5 uses):
  - `sessionView` struct (declared in codex_im_inbound.go) gains `ClaudeSessionID string` field.
  - `dbSessionStore.GetSessionByExternalID` populates the new field from the DB row.

This is a minimal codex_im_inbound.go change — JUST extending the shared struct. Codex doesn't read the field; cc handler will.

- [ ] **Step 1: Identify the existing sessionView struct + GetSessionByExternalID method**

Open `internal/server/codex_im_inbound.go`, find:

```go
type sessionView struct {
    ID            string
    CodexThreadID string
}
```

And in `dbSessionStore.GetSessionByExternalID`:

```go
return sessionView{ID: row.ID, CodexThreadID: row.CodexThreadID.String}, nil
```

(Exact line numbers may vary — search for `sessionView` and `GetSessionByExternalID`.)

- [ ] **Step 2: Add field + populate from DB**

```go
type sessionView struct {
    ID              string
    CodexThreadID   string
    ClaudeSessionID string  // NEW (Phase 4): cc-app-gateway-compatible session ID; "" for non-managed_cc sessions
}
```

In `GetSessionByExternalID` (and any other call site that constructs sessionView from a DB row):

```go
return sessionView{
    ID:              row.ID,
    CodexThreadID:   row.CodexThreadID.String,
    ClaudeSessionID: row.ClaudeSessionID.String,  // NEW
}, nil
```

- [ ] **Step 3: Run codex tests to verify no regression**

```
go test ./internal/server/ -run TestCodex
```
All codex tests must still pass — they constructed `sessionView{}` with zero-value `ClaudeSessionID`, which compiles fine and codex doesn't read it.

Also run `go build ./...` to catch any other sessionView construction site.

- [ ] **Step 4: Commit**

```
git commit -am "refactor(server): extend sessionView with ClaudeSessionID (used by Phase 4 cc handler)"
```

---

## Task 5: `cc_im_inbound.go` handler + ccDispatcher + processTurn

**Files:**
- Create: `internal/server/cc_im_inbound.go`
- Create: `internal/server/cc_im_inbound_test.go`

**Interfaces:**
- Consumes (from Tasks 1-4): DB setter, CcClient, sessionView with ClaudeSessionID.
- Produces (Task 6 wires):
  - `type ccInboundHandler struct { ... }`
  - `NewCcInboundHandler(cc *CcClient, sessions sessionStore, imbridgeSendURL, internalSecret string) *ccInboundHandler`
  - `(*ccInboundHandler).ServeHTTP(w, r)` — auth + Enqueue + 202.
  - `(*ccInboundHandler).Close()` — stops dispatcher; drains in-flight workers.
- Internal:
  - `type ccInboundRequest struct { ... }` (same fields as codex's)
  - `type ccDispatcher struct { ... }` — copy of codexDispatcher
  - `(*ccInboundHandler).processTurn(req ccInboundRequest)` — main flow
  - `sendText` / `sendError` — POST back to imbridge /send

The whole file is a near-copy of codex_im_inbound.go with these differences:
- Request struct name: `ccInboundRequest` (same fields).
- Handler type: `ccInboundHandler`.
- Dispatcher type: `ccDispatcher` (cap=5, drop-oldest, key = channel_id + ":" + wechat_user_id — same as codex).
- ccDbSessionStore (alongside the handler) has `CreateSession(ctx, workspaceID, externalID, title, imChannelID string) (sessionView, error)` that mints BOTH agent_sessions.id ("cse_" + uuid) AND claude_session_id (uuid.NewString()).
- processTurn flow:
  1. `sess := sessions.GetSessionByExternalID(workspaceID, wechat_user_id)`
  2. If miss: `sess = sessions.CreateSession(...)` — creates row with both IDs.
  3. If hit AND `sess.ClaudeSessionID == ""`: existing codex/nanoclaw session migrating to managed_cc. Mint fresh UUID, `db.SetSessionClaudeSessionID(sess.ID, newUUID)`, update sess.ClaudeSessionID. Codex history is NOT migrated.
  4. Drop media + quoted fields with a log line (Prometheus counter `cc_im_inbound_dropped_media_total` per dropped field — IF Prometheus is already wired; else just log).
  5. `ccClient.RunTurn(ctx, CcTurnRequest{ WorkspaceID, SessionID: sess.ClaudeSessionID, UserMessage: req.Text })`
  6. Error matrix dispatch (see spec § Error handling matrix).
  7. `sendText(channel_id, wechat_user_id, response.AssistantText)`.
- No `ThreadID` concept; no thread-not-found retry (cc-app-gateway handles resume internally).

This is the largest task. Plan ~400 LOC production + ~400 LOC tests.

- [ ] **Step 1: Write failing tests**

Create `internal/server/cc_im_inbound_test.go` with these table-driven cases (use the pattern from `codex_im_inbound_test.go` if it exists; if not, follow this structure):

```go
package server_test

// ... imports ...

// fakeCcClient records calls + returns canned responses.
type fakeCcClient struct {
    calls    []server.CcTurnRequest
    response *server.CcTurnResponse
    err      error
}

func (f *fakeCcClient) RunTurn(ctx context.Context, req server.CcTurnRequest) (*server.CcTurnResponse, error) {
    f.calls = append(f.calls, req)
    return f.response, f.err
}

// fakeSessionStore is a map-backed session store.
type fakeSessionStore struct {
    sessions map[string]server.SessionView  // key: workspace_id+":"+external_id
    created  []server.SessionView
}
// ... GetSessionByExternalID, CreateSession, etc.

func TestCcInbound_NewSession(t *testing.T) {
    // empty store → CreateSession path → assert both IDs minted + RunTurn called
    // with the new claude_session_id.
}

func TestCcInbound_ExistingCcSessionReused(t *testing.T) {
    // store seeded with a session that has claude_session_id → RunTurn called with that ID, no Create.
}

func TestCcInbound_MigrationFromCodex(t *testing.T) {
    // store seeded with a session that has codex_thread_id but no claude_session_id →
    // handler mints UUID, calls SetSessionClaudeSessionID, RunTurn uses new UUID.
}

func TestCcInbound_TransportError_SendsErrorMessage(t *testing.T) {
    // fakeCcClient returns transport error → handler calls sendError with the right Chinese message.
}

func TestCcInbound_IsErrorContextWindow_SendsSpecificMessage(t *testing.T) {
    // response has IsError=true, ErrorMessage contains "context" →
    // user sees "上下文已满" message.
}

func TestCcInbound_EmptyAssistantText_SendsErrorMessage(t *testing.T) {
    // response is 200 + isError=false + assistantText="" → handler treats as error.
}

func TestCcInbound_MediaFieldsDropped(t *testing.T) {
    // request has media_data + quoted_text set → handler logs warning, ignores them,
    // RunTurn receives only req.Text.
}

func TestCcInbound_DispatcherSerializesSameUser(t *testing.T) {
    // submit 2 requests for same (channel_id, wechat_user_id) → first finishes before second runs.
    // (Use a slow fakeCcClient that signals via a channel.)
}

func TestCcInbound_DispatcherParallelDifferentUsers(t *testing.T) {
    // 2 requests for different (channel, user) keys → both run concurrently.
}
```

(Exact test scaffolding depends on existing helpers in codex_im_inbound_test.go — adapt accordingly. If the codex tests use `httptest` for the imbridge-send endpoint, follow that pattern.)

- [ ] **Step 2: Run tests to verify failure**

```
go test -v -run TestCcInbound ./internal/server/
```
Expected: build error.

- [ ] **Step 3: Implement `cc_im_inbound.go`**

Start by copying `codex_im_inbound.go` and renaming:
- `codexInboundHandler` → `ccInboundHandler`
- `codexInboundRequest` → `ccInboundRequest`
- `codexDispatcher` → `ccDispatcher`
- `codex_im` log prefix → `cc_im`
- `dbSessionStore` → `ccDbSessionStore` (separate; doesn't share codex's instance)

Then apply the Phase 4 differences from the spec § cc_im_inbound.go section:
- Remove `ThreadID` concept; remove thread-not-found retry.
- processTurn step 3: NULL claude_session_id handling.
- buildCodexInput → buildCcInput, but for Phase 4 just returns `req.Text` (text only — media/quoted dropped with log).
- error matrix per spec (different from codex's).

The dispatcher is verbatim except for the request type. Don't extract it.

- [ ] **Step 4: Run tests to verify pass**

```
go test -v ./internal/server/ -run TestCcInbound
```

- [ ] **Step 5: Commit**

```
git commit -am "feat(server): ccInboundHandler + ccDispatcher for IM intake (Phase 4)"
```

---

## Task 6: agentserver server.go wiring

**Files:**
- Modify: `internal/server/server.go`

**Interfaces:**
- Consumes (from Tasks 3, 5): `NewCcClient`, `NewCcInboundHandler`, `ResolveCCAppGatewayRESTURL`.
- Produces:
  - `Server.CcAppGatewayURL string` field.
  - `Server.cc *CcClient` field.
  - `Server.ccHandler *ccInboundHandler` field.
  - Route `/api/internal/imbridge/cc/turn` registered conditionally.
  - `Server.Close()` calls `ccHandler.Close()`.

- [ ] **Step 1: Write failing test for the route registration**

Append to `internal/server/server_test.go`:

```go
func TestServer_CcInboundRouteRegistered(t *testing.T) {
    t.Setenv("CC_APP_GATEWAY_REST_URL", "http://cc-app-gateway:8087")
    srv := newTestServer(t)
    defer srv.Close()

    // The route is registered; an unauthenticated POST should return 401 (auth middleware fires),
    // NOT 404 (route not found).
    req := httptest.NewRequest("POST", "/api/internal/imbridge/cc/turn", bytes.NewReader([]byte(`{}`)))
    rr := httptest.NewRecorder()
    srv.Routes().ServeHTTP(rr, req)
    if rr.Code == http.StatusNotFound {
        t.Error("cc inbound route should be registered when CC_APP_GATEWAY_REST_URL is set")
    }
}

func TestServer_CcInboundRouteSkippedWhenURLEmpty(t *testing.T) {
    t.Setenv("CC_APP_GATEWAY_REST_URL", "")
    srv := newTestServer(t)
    defer srv.Close()

    req := httptest.NewRequest("POST", "/api/internal/imbridge/cc/turn", bytes.NewReader([]byte(`{}`)))
    rr := httptest.NewRecorder()
    srv.Routes().ServeHTTP(rr, req)
    if rr.Code != http.StatusNotFound {
        t.Errorf("cc inbound route should NOT be registered when CC_APP_GATEWAY_REST_URL is empty; got %d", rr.Code)
    }
}
```

- [ ] **Step 2: Run tests to verify failure**

- [ ] **Step 3: Wire it in `server.go`**

Find the codex route registration (`r.Post("/api/internal/imbridge/codex/turn", ...)` near server.go:381). Add the analogous cc block:

```go
ccURL := ResolveCCAppGatewayRESTURL()
if ccURL != "" {
    cc := NewCcClient(ccURL, internalSecret)
    sessions := newCcDbSessionStore(s.db)
    s.ccHandler = NewCcInboundHandler(cc, sessions, imbridgeSendURL, internalSecret)
    r.Post("/api/internal/imbridge/cc/turn", func(w http.ResponseWriter, r *http.Request) {
        secret := os.Getenv("INTERNAL_API_SECRET")
        if secret != "" && r.Header.Get("X-Internal-Secret") != secret {
            http.Error(w, "unauthorized", http.StatusUnauthorized)
            return
        }
        s.ccHandler.ServeHTTP(w, r)
    })
}
```

In `Server.Close()` (find existing `codexHandler.Close()` call):

```go
if s.ccHandler != nil {
    s.ccHandler.Close()
}
```

Add startup-time misconfiguration warning per spec § Misconfiguration safeguard:

```go
if ccURL == "" {
    // Check DB for any managed_cc channels — log warning if found.
    n, _ := s.db.CountIMChannelsByRoutingMode(ctx, "managed_cc")
    if n > 0 {
        log.Printf("[server] WARNING: %d IM channels have routing_mode=managed_cc but CC_APP_GATEWAY_REST_URL is unset — those channels will fail until cc-app-gateway is enabled", n)
    }
}
```

(The `CountIMChannelsByRoutingMode` helper may need to be added to `internal/db/im_channels.go`. Trivial — one SELECT COUNT.)

- [ ] **Step 4: Run tests to verify pass**

```
go test ./internal/server/
```

- [ ] **Step 5: Commit**

```
git commit -am "feat(server): wire cc inbound route + Close() lifecycle (Phase 4)"
```

---

## Task 7: imbridge `forwardToManagedCC` + bridge.go switch

**Files:**
- Modify: `internal/imbridge/bridge.go`

**Interfaces:**
- Consumes: nothing new (HTTP call to agentserver).
- Produces: `Bridge.forwardMessage()` recognizes `managed_cc`.

- [ ] **Step 1: Write failing test for routing-mode switch**

If `internal/imbridge/bridge_test.go` exists with a routing test, append. Else create:

```go
func TestForwardMessage_RoutesManagedCC(t *testing.T) {
    var called bool
    ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.URL.Path == "/api/internal/imbridge/cc/turn" {
            called = true
            w.WriteHeader(http.StatusAccepted)
            w.Write([]byte(`{"queued":true}`))
        } else {
            t.Errorf("unexpected path: %s", r.URL.Path)
        }
    }))
    defer ts.Close()

    b := &imbridge.Bridge{AgentserverURL: ts.URL}
    binding := imbridge.BridgeBinding{RoutingMode: "managed_cc", WorkspaceID: "ws_test"}
    msg := imbridge.IncomingMessage{ChannelID: "ch_test", FromUserID: "wxid_test", Text: "hi"}
    b.ForwardMessage(context.Background(), msg, binding)  // assumes exported test helper, or use package internal test

    if !called {
        t.Error("expected POST to /api/internal/imbridge/cc/turn")
    }
}
```

(Exact pattern depends on how `forwardMessage` is structured. If it's unexported, this lives in `package imbridge` rather than `imbridge_test`.)

- [ ] **Step 2: Run test to verify failure**

- [ ] **Step 3: Add `managed_cc` case + `forwardToManagedCC` method**

In `forwardMessage()` at bridge.go:414, add the case before `default`:

```go
case "managed_cc":
    return b.forwardToManagedCC(ctx, msg, binding)
```

Add `forwardToManagedCC()` mirroring `forwardToCodex()` exactly — same payload, different URL:

```go
func (b *Bridge) forwardToManagedCC(ctx context.Context, msg IncomingMessage, binding BridgeBinding) (bool, error) {
    payload := map[string]any{
        "channel_id":         msg.ChannelID,
        "workspace_id":       binding.WorkspaceID,
        "wechat_user_id":     msg.FromUserID,
        "wechat_sender":      msg.SenderName,
        "text":               msg.Text,
        "quoted_text":        msg.QuotedText,
        "quoted_sender":      msg.QuotedSender,
        "media_type":         msg.MediaType,
        "media_data":         msg.MediaData,
        "quoted_media_type":  msg.QuotedMediaType,
        "quoted_media_data":  msg.QuotedMediaData,
    }
    body, _ := json.Marshal(payload)
    req, err := http.NewRequestWithContext(ctx, "POST",
        b.AgentserverURL+"/api/internal/imbridge/cc/turn",
        bytes.NewReader(body))
    if err != nil {
        return false, fmt.Errorf("forwardToManagedCC: build request: %w", err)
    }
    req.Header.Set("Content-Type", "application/json")
    if secret := os.Getenv("INTERNAL_API_SECRET"); secret != "" {
        req.Header.Set("X-Internal-Secret", secret)
    }
    resp, err := b.http.Do(req)
    if err != nil {
        return false, fmt.Errorf("forwardToManagedCC: %w", err)
    }
    defer resp.Body.Close()
    if resp.StatusCode != http.StatusAccepted {
        b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
        return false, fmt.Errorf("forwardToManagedCC: status=%d body=%q", resp.StatusCode, b)
    }
    return true, nil
}
```

Update doc comment on `BridgeBinding.RoutingMode` (bridge.go:51) to include `managed_cc`:

```go
// RoutingMode controls which agent backend the channel's messages route to:
// "nanoclaw" (default), "codex", or "managed_cc" (Phase 4 cc-app-gateway).
RoutingMode string
```

- [ ] **Step 4: Run test to verify pass**

- [ ] **Step 5: Commit**

```
git commit -am "feat(imbridge): forwardToManagedCC + bridge switch case (Phase 4)"
```

---

## Task 8: `imbridgesvc/handlers.go` validator accepts managed_cc

**Files:**
- Modify: `internal/imbridgesvc/handlers.go`
- Modify: `internal/imbridgesvc/handlers_test.go`

**Interfaces:**
- Trivial validator extension.

- [ ] **Step 1: Find existing test or add one**

Search for tests around routing_mode validation — likely in `handlers_test.go`. If a test exists asserting the validator REJECTS "managed_cc", that needs flipping. If none exists, add:

```go
func TestPatchIMChannelRoutingMode_AcceptsManagedCC(t *testing.T) {
    h := newTestHandlers(t)
    req := httptest.NewRequest("PATCH", "/api/workspaces/ws/im-channels/ch?field=routing_mode",
        strings.NewReader(`{"value":"managed_cc"}`))
    rr := httptest.NewRecorder()
    h.PatchIMChannel(rr, req)
    if rr.Code != http.StatusOK {
        t.Errorf("managed_cc should be accepted; got %d", rr.Code)
    }
}
```

- [ ] **Step 2: Update validator at handlers.go:977**

```go
if mode != "nanoclaw" && mode != "codex" && mode != "managed_cc" {
    http.Error(w, `invalid routing_mode: must be nanoclaw, codex, or managed_cc`, http.StatusBadRequest)
    return
}
```

- [ ] **Step 3: Test passes; existing tests don't regress**

```
go test ./internal/imbridgesvc/
```

- [ ] **Step 4: Commit**

```
git commit -am "feat(imbridgesvc): accept managed_cc routing_mode value (Phase 4)"
```

---

## Task 9: helm chart agentserver pod env var

**Files:**
- Modify: `deploy/helm/agentserver/templates/deployment.yaml` (or wherever the agentserver Deployment lives — check existing path)

**Interfaces:**
- No code interfaces; YAML only.

- [ ] **Step 1: Find the existing codex env var pattern**

```
grep -n "CODEX_APP_GATEWAY_REST_URL" deploy/helm/agentserver/templates/*.yaml
```

It should already be in `deployment.yaml` (agentserver pod template). Look at the pattern — should be entirely wrapped in `{{- if .Values.codexAppGateway.enabled }}`.

- [ ] **Step 2: Add the analogous block for cc-app-gateway**

Below the CODEX_APP_GATEWAY_REST_URL block, add:

```yaml
{{- if .Values.ccAppGateway.enabled }}
- name: CC_APP_GATEWAY_REST_URL
  value: "http://{{ .Release.Name }}-cc-app-gateway.{{ .Release.Namespace }}.svc:{{ .Values.ccAppGateway.port }}"
{{- end }}
```

- [ ] **Step 3: Smoke-test helm template**

```bash
cd deploy/helm/agentserver
helm template . --set ccAppGateway.enabled=true \
                --set ccAppGateway.s3.region=us-east-1 \
                --set ccAppGateway.s3.bucket=test \
       | grep -A1 "CC_APP_GATEWAY_REST_URL"
# Expected: env var present with cluster service URL.

helm template . | grep -c "CC_APP_GATEWAY_REST_URL"
# Expected: 0 (default ccAppGateway.enabled=false → env var absent).
```

- [ ] **Step 4: Commit**

```
git commit -am "feat(helm): expose CC_APP_GATEWAY_REST_URL on agentserver pod when ccAppGateway enabled"
```

---

## Task 10: integration test — IM → cc-app-gateway → claude → reply

**Files:**
- Create: `internal/server/cc_im_integration_test.go`

**Interfaces:**
- Reuses Phase 2's docker-compose harness; ADDS a mock imbridge listener service.

This test proves end-to-end: a fake imbridge POSTs an IM message → agentserver routes to cc-app-gateway → claude responds → reply posted back to the fake imbridge listener within timeout.

- [ ] **Step 1: Extend the docker-compose with agentserver + fake-imbridge**

`internal/ccappgateway/testdata/integration/docker-compose.yml` already has minio + fake-agentserver + fake-llmproxy + cc-app-gateway. Phase 4 ADDS:
- A real agentserver instance (or wrap the test's own `httptest.NewServer` running the agentserver chi router).
- A fake-imbridge service that exposes `/api/internal/imbridge/send` and records calls.

OR (simpler): run the integration test as a Go test with `httptest.NewServer` for both agentserver and fake-imbridge, and only docker-compose cc-app-gateway+minio+fake-llmproxy. The test wires:
- Real agentserver (in-process) connects to docker-compose cc-app-gateway via host port 8087.
- Real agentserver's IMBridgeURL points to the test's own httptest server.

Pick the simpler option. The test runner orchestrates.

- [ ] **Step 2: Write the test**

```go
//go:build integration

package server_test

import (
    "bytes"
    "context"
    "encoding/json"
    "io"
    "net/http"
    "net/http/httptest"
    "os"
    "os/exec"
    "path/filepath"
    "strings"
    "sync"
    "testing"
    "time"

    "github.com/agentserver/agentserver/internal/server"
)

func TestIntegration_IMToCcEndToEnd(t *testing.T) {
    if testing.Short() {
        t.Skip("integration test")
    }

    // 1. Bring up the Phase 2 docker-compose (cc-app-gateway + minio + fakes).
    composeDir, _ := filepath.Abs("../ccappgateway/testdata/integration")
    runMake := func(target string) {
        cmd := exec.Command("make", "-C", composeDir, target)
        out, err := cmd.CombinedOutput()
        t.Logf("make %s: %s", target, out)
        if err != nil { t.Fatalf("make %s: %v", target, err) }
    }
    runMake("up")
    t.Cleanup(func() {
        cmd := exec.Command("make", "-C", composeDir, "down")
        out, _ := cmd.CombinedOutput()
        t.Logf("make down: %s", out)
    })

    // Wait for cc-app-gateway readyz.
    waitURL := "http://localhost:8087/readyz"
    deadline := time.Now().Add(90 * time.Second)
    for time.Now().Before(deadline) {
        if resp, err := http.Get(waitURL); err == nil && resp.StatusCode == 200 {
            resp.Body.Close()
            break
        }
        time.Sleep(2 * time.Second)
    }

    // 2. Spin up a fake imbridge listener that records calls.
    var sendCalls []map[string]any
    var mu sync.Mutex
    fakeImbridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.URL.Path == "/api/internal/imbridge/send" {
            var payload map[string]any
            json.NewDecoder(r.Body).Decode(&payload)
            mu.Lock()
            sendCalls = append(sendCalls, payload)
            mu.Unlock()
            w.WriteHeader(200)
            w.Write([]byte(`{"status":"sent"}`))
        } else {
            w.WriteHeader(404)
        }
    }))
    t.Cleanup(fakeImbridge.Close)

    // 3. Spin up a real agentserver in-process.
    t.Setenv("INTERNAL_API_SECRET", "secret123")
    t.Setenv("CC_APP_GATEWAY_REST_URL", "http://localhost:8087")
    t.Setenv("IMBRIDGE_URL", fakeImbridge.URL)
    // Set up a test DB (use existing test DB harness or skip if missing).
    // ...

    srv := server.NewServer(...)  // exact constructor depends on what's available
    agentserverTS := httptest.NewServer(srv.Routes())
    t.Cleanup(agentserverTS.Close)

    // 4. POST an IM message to /api/internal/imbridge/cc/turn (turn 1).
    sessionID := "wxid_alice"
    body1 := `{"channel_id":"ch_test","workspace_id":"ws_test","wechat_user_id":"` + sessionID + `","wechat_sender":"Alice","text":"Remember code DELTA-9."}`
    req, _ := http.NewRequest("POST", agentserverTS.URL+"/api/internal/imbridge/cc/turn",
        strings.NewReader(body1))
    req.Header.Set("X-Internal-Secret", "secret123")
    req.Header.Set("Content-Type", "application/json")
    resp, err := http.DefaultClient.Do(req)
    if err != nil { t.Fatal(err) }
    if resp.StatusCode != 202 {
        b, _ := io.ReadAll(resp.Body)
        t.Fatalf("expected 202, got %d: %s", resp.StatusCode, b)
    }

    // 5. Wait for the fake-imbridge to receive the reply (up to 60s — claude is real).
    deadline = time.Now().Add(60 * time.Second)
    for time.Now().Before(deadline) {
        mu.Lock()
        n := len(sendCalls)
        mu.Unlock()
        if n >= 1 { break }
        time.Sleep(500 * time.Millisecond)
    }
    mu.Lock()
    if len(sendCalls) < 1 {
        mu.Unlock()
        t.Fatal("fake imbridge never received reply for turn 1")
    }
    reply1 := sendCalls[0]
    mu.Unlock()
    t.Logf("turn 1 reply: %v", reply1)

    // 6. POST turn 2 same (channel, user). Assert reply mentions DELTA-9 (resume worked).
    body2 := `{"channel_id":"ch_test","workspace_id":"ws_test","wechat_user_id":"` + sessionID + `","wechat_sender":"Alice","text":"What's the code I just gave you?"}`
    req2, _ := http.NewRequest("POST", agentserverTS.URL+"/api/internal/imbridge/cc/turn",
        strings.NewReader(body2))
    req2.Header.Set("X-Internal-Secret", "secret123")
    req2.Header.Set("Content-Type", "application/json")
    resp2, err := http.DefaultClient.Do(req2)
    if err != nil { t.Fatal(err) }
    if resp2.StatusCode != 202 { t.Fatalf("turn 2: %d", resp2.StatusCode) }

    deadline = time.Now().Add(60 * time.Second)
    for time.Now().Before(deadline) {
        mu.Lock()
        n := len(sendCalls)
        mu.Unlock()
        if n >= 2 { break }
        time.Sleep(500 * time.Millisecond)
    }
    mu.Lock()
    defer mu.Unlock()
    if len(sendCalls) < 2 {
        t.Fatal("fake imbridge never received reply for turn 2")
    }
    reply2Text, _ := sendCalls[1]["text"].(string)
    if !strings.Contains(reply2Text, "DELTA-9") {
        t.Errorf("turn 2 reply should mention DELTA-9 (resume across turns); got: %q", reply2Text)
    }
}
```

- [ ] **Step 2: Run the test**

```
go test -tags integration -v -timeout 10m -run TestIntegration_IMToCcEndToEnd ./internal/server/
```

Iterate if it fails — common causes:
- DB not seeded (need a test DB or migration)
- agentserver Server constructor needs more wiring
- fake-imbridge call doesn't fire (port conflict, hostnames)

- [ ] **Step 3: Commit**

```
git commit -am "test(server): integration test for IM → cc-app-gateway → claude end-to-end (Phase 4)"
```

---

## Final pass

- [ ] **Run full test suite**

```
go test ./...
go test -tags integration -v -timeout 10m -run TestIntegration_IMToCcEndToEnd ./internal/server/
go test -tags integration -v -timeout 10m ./internal/ccappgateway/...
go vet ./...
```

- [ ] **Update memory**

Add a `cc_app_gateway_phase4_landed.md` note cross-linking with Phase 1+2 memories, documenting Phase 4 scope and the user-visible "WeChat now works" milestone.

- [ ] **Open PR (stacked on Phase 2's PR #280)**

```
gh pr create --base feat/cc-app-gateway-phase2 \
             --head feat/cc-app-gateway-phase4 \
             --title "feat(cc-app-gateway): Phase 4 — IM intake (agentserver routes managed_cc → cc-app-gateway)" \
             --body "$(cat /tmp/p4-pr-body.md)"
```

PR body must:
- Mention this is Phase 4 stacked on Phase 2 (PR #280) which is stacked on Phase 1 (PR #279)
- Link spec + plan
- Call out the user-visible milestone: "After this lands, setting `routing_mode=managed_cc` on a WeChat channel routes through claude end-to-end"
- Show integration test output asserting DELTA-9 reply in turn 2 (resume works)
- Document the migration risk: codex → managed_cc loses history (no migration of codex turns)
- List the Phase 4 deferred items (vision via Phase 4.5, per-channel model in Phase 5+, etc.)

- [ ] **Bump chart (post-merge of all three PRs)**

Per `agentserver_release_flow` memory: bump Chart.yaml minor, push `v<version>` git tag.

---

## Out-of-band: confirm Phase 1/2 PRs are mergeable before Phase 4 review

Phase 4 cannot merge until PR #279 + PR #280 land. Before opening Phase 4 PR, sanity-check:

```bash
gh pr view 279 --json mergeStateStatus,state
gh pr view 280 --json mergeStateStatus,state
```

If either is blocked (rebase conflict, CI red, review changes requested), prioritize unblocking those before flooding reviewer's queue with a third PR. Phase 4 implementation can still proceed in the worktree; just don't open the PR until #279/#280 are visibly close to mergeable.

---

## Self-review (run after writing this plan)

Done as part of writing. Checks performed:

1. **Spec coverage:** Every § Component changes item in the spec has a task. Migration → Task 1. CcTurnResponse field → Task 2. cc_client.go → Task 3. sessionView extension → Task 4. cc_im_inbound.go → Task 5. server.go wiring + misconfiguration safeguard → Task 6. imbridge → Task 7. validator → Task 8. helm → Task 9. Integration test → Task 10. Audit revisions all addressed.

2. **Placeholder scan:** No "TBD", "TODO", or "fill in details". Every step has either code or an exact command. A few "exact pattern depends on existing helpers — adapt" notes; those are honest acknowledgments that helpers may differ, not placeholders.

3. **Type consistency:**
   - `CcClient`/`CcTurnRequest`/`CcTurnResponse` field names match between Task 3 (definition) and Task 5 (consumer).
   - `sessionView.ClaudeSessionID string` consistent between Task 4 (definition) and Task 5 (consumer).
   - `SetSessionClaudeSessionID(ctx, sessionID, claudeSessionID string) error` signature consistent across Tasks 1, 5.
   - `ResolveCCAppGatewayRESTURL()` exported in Task 3, used in Task 6.
   - `managed_cc` literal string consistent across Tasks 5 (handler), 7 (imbridge), 8 (validator).
   - HTTP path `/api/internal/imbridge/cc/turn` consistent across Tasks 6 (server route), 7 (imbridge POST target), 10 (integration test).
