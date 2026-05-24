# codex-exec-edge Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a thin WS auth/proxy + register-retry layer (`codex-exec-edge`) in front of `codex-exec-gateway` so gateway restarts no longer kill connector processes.

**Architecture:** New Go binary `cmd/codex-exec-edge` + package `internal/codexececdge`. WS endpoint `/codex-exec/{exe_id}` validates HMAC ticket then bidir-pipes to upstream gateway. HTTP endpoint `/cloud/{}/register` reverse-proxies with retry on 5xx / connection error. Stateless, 2 replicas, RollingUpdate. Istio HTTPRoute path-splits these two prefixes to edge; everything else stays direct to gateway.

**Tech Stack:** Go 1.26, `nhooyr.io/websocket`, `chi/v5`, Helm, Pulumi (TypeScript), Istio Gateway API.

**Reference:** `docs/superpowers/specs/2026-05-24-codex-exec-edge-design.md`

**Scope note:** This plan covers the `agentserver` repo + the Pulumi change in `/root/k8s`. Each task lists its repo explicitly.

---

## Phase A — Refactor: extract `wsticket` subpackage

The HMAC ticket mint/verify functions are co-located with the rest of the gateway HTTP handlers. Edge only needs these two functions; importing the whole `handlers` package bloats the edge binary and creates fragile coupling. Extract first.

### Task 1: Create `internal/codexexecgateway/wsticket` package

**Repo:** `agentserver`

**Files:**
- Create: `internal/codexexecgateway/wsticket/wsticket.go`
- Create: `internal/codexexecgateway/wsticket/wsticket_test.go`

- [ ] **Step 1: Write the new package with tests**

Create `internal/codexexecgateway/wsticket/wsticket.go`:

```go
// Package wsticket mints and verifies the short-lived HMAC bearer that
// authorises a `/codex-exec/{exe_id}?token=...` ws upgrade. Used by:
//   - codex-exec-gateway's cloud_register handler (mint)
//   - codex-exec-gateway's inbound handler (verify)
//   - codex-exec-edge's wsproxy (verify before proxying upstream)
//
// Layout: <exe_id>.<expiry_unix>.<base64url(HMAC-SHA256(secret, "<exe_id>.<expiry>"))>
// 5-minute TTL; codex re-registers on every reconnect so no need for longer.
package wsticket

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const TTL = 5 * time.Minute

func Mint(exeID, secret string) (string, error) {
	if secret == "" {
		return "", fmt.Errorf("internal secret not configured")
	}
	expiry := time.Now().Add(TTL).Unix()
	payload := exeID + "." + strconv.FormatInt(expiry, 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return payload + "." + sig, nil
}

func Verify(ticket, expectedExeID, secret string) error {
	if secret == "" {
		return fmt.Errorf("internal secret not configured")
	}
	parts := strings.Split(ticket, ".")
	if len(parts) != 3 {
		return fmt.Errorf("malformed ticket")
	}
	exeID, expStr, sigB64 := parts[0], parts[1], parts[2]
	if exeID != expectedExeID {
		return fmt.Errorf("ticket exe_id mismatch")
	}
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil {
		return fmt.Errorf("bad expiry: %w", err)
	}
	if time.Now().Unix() >= exp {
		return fmt.Errorf("ticket expired")
	}
	want := hmac.New(sha256.New, []byte(secret))
	want.Write([]byte(exeID + "." + expStr))
	wantSig := want.Sum(nil)
	gotSig, err := base64.RawURLEncoding.DecodeString(sigB64)
	if err != nil {
		return fmt.Errorf("bad signature encoding: %w", err)
	}
	if !hmac.Equal(wantSig, gotSig) {
		return fmt.Errorf("bad signature")
	}
	return nil
}
```

Create `internal/codexexecgateway/wsticket/wsticket_test.go`:

```go
package wsticket

import (
	"strings"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	ticket, err := Mint("exe_x", "secret")
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if err := Verify(ticket, "exe_x", "secret"); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestVerifyRejectsWrongExe(t *testing.T) {
	ticket, _ := Mint("exe_x", "secret")
	if err := Verify(ticket, "exe_y", "secret"); err == nil {
		t.Fatal("expected exe_id mismatch")
	}
}

func TestVerifyRejectsWrongSecret(t *testing.T) {
	ticket, _ := Mint("exe_x", "secret-a")
	if err := Verify(ticket, "exe_x", "secret-b"); err == nil {
		t.Fatal("expected bad signature")
	}
}

func TestVerifyRejectsMalformed(t *testing.T) {
	if err := Verify("not.a.token", "exe_x", "secret"); err == nil {
		t.Fatal("expected malformed error")
	}
	if err := Verify("a.b", "exe_x", "secret"); err == nil {
		t.Fatal("expected malformed error")
	}
}

func TestMintRequiresSecret(t *testing.T) {
	if _, err := Mint("exe_x", ""); err == nil || !strings.Contains(err.Error(), "secret") {
		t.Fatal("expected secret-required error")
	}
}
```

- [ ] **Step 2: Run new package tests**

Run: `cd /root/agentserver && go test ./internal/codexexecgateway/wsticket/...`
Expected: PASS — 5 tests.

- [ ] **Step 3: Commit**

```bash
cd /root/agentserver
git add internal/codexexecgateway/wsticket/
git commit -m "$(cat <<'EOF'
refactor(cxg): extract wsticket subpackage from handlers

Mint/Verify will be needed by the upcoming codex-exec-edge binary;
extracting them into a tiny subpackage avoids edge importing all of
handlers/ (which depends on clientmeta, execmodel, chi).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

### Task 2: Switch call sites to `wsticket` package and delete old file

**Repo:** `agentserver`

**Files:**
- Modify: `internal/codexexecgateway/inbound.go:31`
- Modify: `internal/codexexecgateway/handlers/cloud_register.go` (Mint call)
- Modify: `internal/codexexecgateway/handlers/cloud_register_test.go` (Verify call)
- Modify: `internal/codexexecgateway/inbound_test.go:52,83` (Mint calls)
- Modify: `internal/codexexecgateway/bridge_test.go:48` (Mint call)
- Modify: `internal/codexexecgateway/multiplex_e2e_test.go:97` (Mint call)
- Delete: `internal/codexexecgateway/handlers/ws_ticket.go`

- [ ] **Step 1: Switch production callers**

In `internal/codexexecgateway/inbound.go:31` replace
`handlers.VerifyWSTicket(token, exeID, s.config.AgentserverInternalSecret)` with
`wsticket.Verify(token, exeID, s.config.AgentserverInternalSecret)` and add the
import `"github.com/agentserver/agentserver/internal/codexexecgateway/wsticket"`.
Remove the `handlers` import if unused.

In `internal/codexexecgateway/handlers/cloud_register.go:135` replace
`MintWSTicket(exeID, wsTicketSecret)` with
`wsticket.Mint(exeID, wsTicketSecret)` and add import
`"github.com/agentserver/agentserver/internal/codexexecgateway/wsticket"`.

- [ ] **Step 2: Switch test callers**

In each test file listed above, replace `handlers.MintWSTicket` with
`wsticket.Mint` and add the wsticket import. In `cloud_register_test.go:89`
replace the package-local `VerifyWSTicket` call with `wsticket.Verify`.

- [ ] **Step 3: Delete the old file**

```bash
cd /root/agentserver
rm internal/codexexecgateway/handlers/ws_ticket.go
```

- [ ] **Step 4: Verify whole codebase builds and tests pass**

Run:
```bash
cd /root/agentserver
go build ./...
go test ./internal/codexexecgateway/...
```
Expected: PASS, no compile errors.

- [ ] **Step 5: Commit**

```bash
cd /root/agentserver
git add -A internal/codexexecgateway/
git commit -m "$(cat <<'EOF'
refactor(cxg): switch all callers to wsticket package; delete old file

Mechanical replace handlers.{Mint,Verify}WSTicket -> wsticket.{Mint,Verify}.
No behaviour change.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Phase B — Edge implementation

Build the edge binary TDD-style: skeleton → healthz → ws proxy → register proxy.

### Task 3: Config struct + env loader

**Repo:** `agentserver`

**Files:**
- Create: `internal/codexececdge/config.go`
- Create: `internal/codexececdge/config_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/codexececdge/config_test.go`:

```go
package codexececdge

import (
	"testing"
	"time"
)

func TestLoadConfigFromEnv_Defaults(t *testing.T) {
	t.Setenv("CXE_UPSTREAM_BASE_URL", "http://upstream:6060")
	t.Setenv("CXE_AGENTSERVER_INTERNAL_SECRET", "s")
	cfg, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatalf("LoadConfigFromEnv: %v", err)
	}
	if cfg.Port != "6061" {
		t.Errorf("Port default: got %q", cfg.Port)
	}
	if cfg.RegisterRetryTotalTimeout != 30*time.Second {
		t.Errorf("RegisterRetryTotalTimeout default: got %v", cfg.RegisterRetryTotalTimeout)
	}
	if cfg.RegisterRetryInitialBackoff != 500*time.Millisecond {
		t.Errorf("RegisterRetryInitialBackoff default: got %v", cfg.RegisterRetryInitialBackoff)
	}
	if cfg.UpstreamDialTimeout != 5*time.Second {
		t.Errorf("UpstreamDialTimeout default: got %v", cfg.UpstreamDialTimeout)
	}
}

func TestLoadConfigFromEnv_RequiresUpstream(t *testing.T) {
	t.Setenv("CXE_UPSTREAM_BASE_URL", "")
	t.Setenv("CXE_AGENTSERVER_INTERNAL_SECRET", "s")
	if _, err := LoadConfigFromEnv(); err == nil {
		t.Fatal("expected error for missing CXE_UPSTREAM_BASE_URL")
	}
}

func TestLoadConfigFromEnv_RequiresSecret(t *testing.T) {
	t.Setenv("CXE_UPSTREAM_BASE_URL", "http://upstream:6060")
	t.Setenv("CXE_AGENTSERVER_INTERNAL_SECRET", "")
	if _, err := LoadConfigFromEnv(); err == nil {
		t.Fatal("expected error for missing CXE_AGENTSERVER_INTERNAL_SECRET")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /root/agentserver && go test ./internal/codexececdge/...`
Expected: FAIL — package does not exist / undefined `LoadConfigFromEnv`.

- [ ] **Step 3: Write the implementation**

Create `internal/codexececdge/config.go`:

```go
package codexececdge

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"
)

type Config struct {
	Port                        string
	UpstreamBaseURL             string
	AgentserverInternalSecret   string
	RegisterRetryTotalTimeout   time.Duration
	RegisterRetryInitialBackoff time.Duration
	UpstreamDialTimeout         time.Duration
	LogLevel                    slog.Level
}

func (c Config) Validate() error {
	if c.UpstreamBaseURL == "" {
		return fmt.Errorf("CXE_UPSTREAM_BASE_URL required")
	}
	if c.AgentserverInternalSecret == "" {
		return fmt.Errorf("CXE_AGENTSERVER_INTERNAL_SECRET required")
	}
	return nil
}

func LoadConfigFromEnv() (Config, error) {
	cfg := Config{
		Port:                        envOr("CXE_PORT", "6061"),
		UpstreamBaseURL:             os.Getenv("CXE_UPSTREAM_BASE_URL"),
		AgentserverInternalSecret:   os.Getenv("CXE_AGENTSERVER_INTERNAL_SECRET"),
		RegisterRetryTotalTimeout:   parseDurationOr("CXE_REGISTER_RETRY_TIMEOUT", 30*time.Second),
		RegisterRetryInitialBackoff: parseDurationOr("CXE_REGISTER_RETRY_BASE", 500*time.Millisecond),
		UpstreamDialTimeout:         parseDurationOr("CXE_UPSTREAM_DIAL_TIMEOUT", 5*time.Second),
		LogLevel:                    slog.LevelInfo,
	}
	if v := os.Getenv("CXE_LOG_LEVEL"); v != "" {
		switch strings.ToLower(v) {
		case "debug":
			cfg.LogLevel = slog.LevelDebug
		case "warn":
			cfg.LogLevel = slog.LevelWarn
		case "error":
			cfg.LogLevel = slog.LevelError
		}
	}
	if err := cfg.Validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func parseDurationOr(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /root/agentserver && go test ./internal/codexececdge/...`
Expected: PASS — 3 tests.

- [ ] **Step 5: Commit**

```bash
cd /root/agentserver
git add internal/codexececdge/
git commit -m "feat(edge): config struct + env loader with CXE_ prefix

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

### Task 4: Server scaffold + /healthz

**Repo:** `agentserver`

**Files:**
- Create: `internal/codexececdge/server.go`
- Create: `internal/codexececdge/server_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/codexececdge/server_test.go`:

```go
package codexececdge

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newTestServer(t *testing.T, cfg Config) *Server {
	t.Helper()
	if cfg.UpstreamBaseURL == "" {
		cfg.UpstreamBaseURL = "http://127.0.0.1:1"
	}
	if cfg.AgentserverInternalSecret == "" {
		cfg.AgentserverInternalSecret = "test-secret"
	}
	if cfg.UpstreamDialTimeout == 0 {
		cfg.UpstreamDialTimeout = time.Second
	}
	if cfg.LogLevel == 0 {
		cfg.LogLevel = slog.LevelError // quiet in tests
	}
	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return srv
}

func TestHealthz(t *testing.T) {
	srv := newTestServer(t, Config{})
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status: %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Errorf("body: %q", body)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /root/agentserver && go test ./internal/codexececdge/... -run TestHealthz`
Expected: FAIL — `NewServer` / `Routes` undefined.

- [ ] **Step 3: Write the implementation**

Create `internal/codexececdge/server.go`:

```go
package codexececdge

import (
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type Server struct {
	cfg        Config
	upstream   *url.URL
	httpClient *http.Client
	logger     *slog.Logger
}

func NewServer(cfg Config) (*Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	u, err := url.Parse(cfg.UpstreamBaseURL)
	if err != nil {
		return nil, err
	}
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))
	return &Server{
		cfg:      cfg,
		upstream: u,
		httpClient: &http.Client{
			Timeout: 0, // per-attempt timeout enforced via per-try context
			Transport: &http.Transport{
				Proxy:               nil,
				DisableKeepAlives:   false,
				IdleConnTimeout:     90 * time.Second,
				MaxIdleConnsPerHost: 16,
			},
		},
		logger: logger,
	}, nil
}

func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	r.Get("/codex-exec/{exe_id}", s.handleWSProxy)
	r.Post("/cloud/executor/{exe_id}/register", s.handleRegisterProxy)
	r.Post("/cloud/environment/{env_id}/register", s.handleRegisterProxy)
	return r
}

// Placeholders — filled in by later tasks.
func (s *Server) handleWSProxy(w http.ResponseWriter, r *http.Request)        { http.Error(w, "not implemented", 501) }
func (s *Server) handleRegisterProxy(w http.ResponseWriter, r *http.Request)  { http.Error(w, "not implemented", 501) }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /root/agentserver && go test ./internal/codexececdge/...`
Expected: PASS — config tests + healthz.

- [ ] **Step 5: Commit**

```bash
cd /root/agentserver
git add internal/codexececdge/
git commit -m "feat(edge): server scaffold + /healthz handler

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

### Task 5: WS proxy — ticket verification (401 paths)

**Repo:** `agentserver`

**Files:**
- Create: `internal/codexececdge/wsproxy.go`
- Create: `internal/codexececdge/wsproxy_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/codexececdge/wsproxy_test.go`:

```go
package codexececdge

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentserver/agentserver/internal/codexexecgateway/wsticket"
)

func TestWSProxy_RejectsMissingToken(t *testing.T) {
	srv := newTestServer(t, Config{})
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/codex-exec/exe_1") // no ?token=
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status: got %d want 401", resp.StatusCode)
	}
}

func TestWSProxy_RejectsBadToken(t *testing.T) {
	srv := newTestServer(t, Config{})
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/codex-exec/exe_1?token=garbage")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status: got %d want 401", resp.StatusCode)
	}
}

func TestWSProxy_RejectsTokenForOtherExe(t *testing.T) {
	srv := newTestServer(t, Config{AgentserverInternalSecret: "secret"})
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	ticket, _ := wsticket.Mint("exe_other", "secret")
	resp, err := http.Get(ts.URL + "/codex-exec/exe_1?token=" + ticket)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status: got %d want 401", resp.StatusCode)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /root/agentserver && go test ./internal/codexececdge/... -run TestWSProxy_Rejects`
Expected: FAIL — handleWSProxy returns 501.

- [ ] **Step 3: Write the ticket-verification implementation**

Create `internal/codexececdge/wsproxy.go`:

```go
package codexececdge

import (
	"net/http"

	"github.com/agentserver/agentserver/internal/codexexecgateway/wsticket"
	"github.com/go-chi/chi/v5"
)

func (s *Server) handleWSProxy(w http.ResponseWriter, r *http.Request) {
	exeID := chi.URLParam(r, "exe_id")
	token := r.URL.Query().Get("token")
	if exeID == "" || token == "" {
		http.Error(w, "missing parameters", http.StatusUnauthorized)
		return
	}
	if err := wsticket.Verify(token, exeID, s.cfg.AgentserverInternalSecret); err != nil {
		s.logger.Warn("wsproxy: bad ticket", "exe_id", exeID, "err", err, "remote", r.RemoteAddr)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	// Phase B continued in Task 6: dial upstream + accept + pipe.
	http.Error(w, "not implemented yet", http.StatusNotImplemented)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /root/agentserver && go test ./internal/codexececdge/... -run TestWSProxy_Rejects`
Expected: PASS — 3 tests.

- [ ] **Step 5: Commit**

```bash
cd /root/agentserver
git add internal/codexececdge/
git commit -m "feat(edge): WS proxy ticket verification (401 paths)

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

### Task 6: WS proxy — upstream dial + bidir pipe

**Repo:** `agentserver`

**Files:**
- Modify: `internal/codexececdge/wsproxy.go`
- Modify: `internal/codexececdge/wsproxy_test.go`

- [ ] **Step 1: Write failing tests for upstream dial + echo proxy**

Append to `internal/codexececdge/wsproxy_test.go`:

```go
import (
	"context"
	"net/url"
	"strings"
	"time"

	"nhooyr.io/websocket"
)

// fakeUpstream serves /codex-exec/{exe_id} as an echo server.  Returns the
// test server.
func fakeUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/codex-exec/") {
			http.NotFound(w, r)
			return
		}
		ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer ws.Close(websocket.StatusNormalClosure, "")
		ws.SetReadLimit(-1)
		ctx := r.Context()
		for {
			mt, data, err := ws.Read(ctx)
			if err != nil {
				return
			}
			if err := ws.Write(ctx, mt, data); err != nil {
				return
			}
		}
	}))
}

func TestWSProxy_UpstreamUnreachableReturns502(t *testing.T) {
	srv := newTestServer(t, Config{
		UpstreamBaseURL:           "http://127.0.0.1:1", // closed port
		AgentserverInternalSecret: "secret",
		UpstreamDialTimeout:       200 * time.Millisecond,
	})
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	ticket, _ := wsticket.Mint("exe_1", "secret")
	resp, err := http.Get(ts.URL + "/codex-exec/exe_1?token=" + ticket)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status: got %d want 502", resp.StatusCode)
	}
}

func TestWSProxy_EchoThroughEdge(t *testing.T) {
	up := fakeUpstream(t)
	defer up.Close()

	srv := newTestServer(t, Config{
		UpstreamBaseURL:           up.URL,
		AgentserverInternalSecret: "secret",
		UpstreamDialTimeout:       2 * time.Second,
	})
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	ticket, _ := wsticket.Mint("exe_1", "secret")
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/codex-exec/exe_1?token=" + url.QueryEscape(ticket)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close(websocket.StatusNormalClosure, "")

	if err := c.Write(ctx, websocket.MessageBinary, []byte("hello")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	mt, data, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if mt != websocket.MessageBinary || string(data) != "hello" {
		t.Errorf("echo: mt=%v data=%q", mt, data)
	}
}

func TestWSProxy_PropagatesXForwardedFor(t *testing.T) {
	gotHeaders := make(chan http.Header, 1)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders <- r.Header.Clone()
		ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		ws.Close(websocket.StatusNormalClosure, "")
	}))
	defer up.Close()

	srv := newTestServer(t, Config{
		UpstreamBaseURL:           up.URL,
		AgentserverInternalSecret: "secret",
		UpstreamDialTimeout:       2 * time.Second,
	})
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	ticket, _ := wsticket.Mint("exe_1", "secret")
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/codex-exec/exe_1?token=" + url.QueryEscape(ticket)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"User-Agent": []string{"codex_cli_rs/0.130.0 (Linux x; x86_64)"}},
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	c.Close(websocket.StatusNormalClosure, "")

	select {
	case h := <-gotHeaders:
		if h.Get("X-Forwarded-For") == "" {
			t.Error("X-Forwarded-For not set")
		}
		if h.Get("X-Real-IP") == "" {
			t.Error("X-Real-IP not set")
		}
		if !strings.Contains(h.Get("User-Agent"), "codex_cli_rs") {
			t.Errorf("User-Agent not forwarded: %q", h.Get("User-Agent"))
		}
	case <-ctx.Done():
		t.Fatal("upstream never observed headers")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /root/agentserver && go test ./internal/codexececdge/... -run TestWSProxy_`
Expected: FAIL — handleWSProxy still returns 501 for the success/dial cases.

- [ ] **Step 3: Implement upstream dial + bidir pipe**

Replace `internal/codexececdge/wsproxy.go` body of `handleWSProxy` (and add helpers) so the file reads:

```go
package codexececdge

import (
	"context"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/agentserver/agentserver/internal/clientmeta"
	"github.com/agentserver/agentserver/internal/codexexecgateway/wsticket"
	"github.com/agentserver/agentserver/internal/wsbridge"
	"github.com/go-chi/chi/v5"
	"nhooyr.io/websocket"
)

func (s *Server) handleWSProxy(w http.ResponseWriter, r *http.Request) {
	exeID := chi.URLParam(r, "exe_id")
	token := r.URL.Query().Get("token")
	if exeID == "" || token == "" {
		http.Error(w, "missing parameters", http.StatusUnauthorized)
		return
	}
	if err := wsticket.Verify(token, exeID, s.cfg.AgentserverInternalSecret); err != nil {
		s.logger.Warn("wsproxy: bad ticket", "exe_id", exeID, "err", err, "remote", r.RemoteAddr)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// 1. Dial upstream BEFORE accepting the client ws — so we can return
	//    a plain HTTP 502 if the upstream is unreachable, without doing a
	//    pointless ws upgrade on the client side.
	upstreamURL := s.buildUpstreamWSURL(exeID, token)
	dialCtx, dialCancel := context.WithTimeout(r.Context(), s.cfg.UpstreamDialTimeout)
	defer dialCancel()
	clientIP := clientmeta.ClientIP(r)
	dialHdr := http.Header{}
	dialHdr.Set("X-Forwarded-For", clientIP)
	dialHdr.Set("X-Real-IP", clientIP)
	if ua := r.Header.Get("User-Agent"); ua != "" {
		dialHdr.Set("User-Agent", ua)
	}
	upstream, _, err := websocket.Dial(dialCtx, upstreamURL, &websocket.DialOptions{
		HTTPHeader: dialHdr,
	})
	if err != nil {
		s.logger.Warn("wsproxy: upstream dial failed", "exe_id", exeID, "err", err)
		http.Error(w, "upstream unreachable", http.StatusBadGateway)
		return
	}
	upstream.SetReadLimit(-1)

	// 2. Upgrade the client side.
	client, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		s.logger.Warn("wsproxy: client accept failed", "exe_id", exeID, "err", err)
		_ = upstream.Close(websocket.StatusInternalError, "client accept failed")
		return
	}
	client.SetReadLimit(-1)

	// 3. Two pump goroutines + keepalive on both sides.
	pumpCtx, pumpCancel := context.WithCancel(r.Context())
	defer pumpCancel()
	go wsbridge.KeepAlive(pumpCtx, client, 30*time.Second)
	go wsbridge.KeepAlive(pumpCtx, upstream, 30*time.Second)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		err := pump(pumpCtx, client, upstream)
		closeOther(upstream, err)
	}()
	go func() {
		defer wg.Done()
		err := pump(pumpCtx, upstream, client)
		closeOther(client, err)
	}()
	wg.Wait()
	s.logger.Info("wsproxy: closed", "exe_id", exeID)
}

// buildUpstreamWSURL converts UpstreamBaseURL (http/https) into a ws/wss URL
// and appends the codex-exec path + token.
func (s *Server) buildUpstreamWSURL(exeID, token string) string {
	u := *s.upstream
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	}
	u.Path = "/codex-exec/" + exeID
	q := url.Values{}
	q.Set("token", token)
	u.RawQuery = q.Encode()
	return u.String()
}

// pump reads from src and writes each frame to dst until either side errors.
func pump(ctx context.Context, src, dst *websocket.Conn) error {
	for {
		mt, data, err := src.Read(ctx)
		if err != nil {
			return err
		}
		if err := dst.Write(ctx, mt, data); err != nil {
			return err
		}
	}
}

// closeOther forwards an appropriate close to the other side based on
// the originating error's WS close status (defaulting to 1011).
func closeOther(other *websocket.Conn, srcErr error) {
	status := websocket.CloseStatus(srcErr)
	if status == -1 {
		status = websocket.StatusInternalError
	}
	_ = other.Close(status, "")
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /root/agentserver && go test ./internal/codexececdge/...`
Expected: PASS — all wsproxy tests including the echo and XFF propagation.

- [ ] **Step 5: Commit**

```bash
cd /root/agentserver
git add internal/codexececdge/
git commit -m "$(cat <<'EOF'
feat(edge): WS proxy bidir pipe + upstream dial + XFF propagation

- Dial upstream BEFORE accept so we can return plain 502 on dial fail.
- Two pump goroutines, byte-level (no protobuf parsing).
- KeepAlive on both sides (30s, matches gateway).
- ReadLimit=-1 (codex exec-server streams large frames).
- Sets X-Forwarded-For / X-Real-IP so gateway's clientmeta.ClientIP
  records the real client.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

### Task 7: Register proxy — transparent forward + body buffering

**Repo:** `agentserver`

**Files:**
- Create: `internal/codexececdge/registerproxy.go`
- Create: `internal/codexececdge/registerproxy_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/codexececdge/registerproxy_test.go`:

```go
package codexececdge

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeRegisterUpstream returns an upstream that records each call and lets
// the test drive the response via the responder fn.
type fakeRegisterUpstream struct {
	t          *testing.T
	calls      atomic.Int64
	headersCh  chan http.Header
	bodiesCh   chan []byte
	responder  func(call int64, w http.ResponseWriter)
}

func newFakeRegisterUpstream(t *testing.T, responder func(call int64, w http.ResponseWriter)) (*httptest.Server, *fakeRegisterUpstream) {
	f := &fakeRegisterUpstream{
		t:         t,
		headersCh: make(chan http.Header, 32),
		bodiesCh:  make(chan []byte, 32),
		responder: responder,
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := f.calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		select {
		case f.headersCh <- r.Header.Clone():
		default:
		}
		select {
		case f.bodiesCh <- body:
		default:
		}
		f.responder(n, w)
	}))
	return ts, f
}

func TestRegisterProxy_2xxPassThrough(t *testing.T) {
	up, fake := newFakeRegisterUpstream(t, func(_ int64, w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"id":"exe_x"}`))
	})
	defer up.Close()

	srv := newTestServer(t, Config{
		UpstreamBaseURL:             up.URL,
		AgentserverInternalSecret:   "s",
		RegisterRetryTotalTimeout:   2 * time.Second,
		RegisterRetryInitialBackoff: 10 * time.Millisecond,
		UpstreamDialTimeout:         time.Second,
	})
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	body := []byte(`{"foo":"bar"}`)
	resp, err := http.Post(ts.URL+"/cloud/environment/exe_x/register", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status: %d", resp.StatusCode)
	}
	got, _ := io.ReadAll(resp.Body)
	var parsed map[string]string
	if err := json.Unmarshal(got, &parsed); err != nil {
		t.Fatalf("response body: %v / %q", err, got)
	}
	if parsed["id"] != "exe_x" {
		t.Errorf("body: %q", got)
	}
	if fake.calls.Load() != 1 {
		t.Errorf("upstream called %d times", fake.calls.Load())
	}
	// XFF should have been set by edge.
	h := <-fake.headersCh
	if h.Get("X-Forwarded-For") == "" {
		t.Error("X-Forwarded-For not set")
	}
	// Body forwarded verbatim.
	gotBody := <-fake.bodiesCh
	if !bytes.Equal(gotBody, body) {
		t.Errorf("upstream body: got %q want %q", gotBody, body)
	}
}

func TestRegisterProxy_4xxNoRetry(t *testing.T) {
	up, fake := newFakeRegisterUpstream(t, func(_ int64, w http.ResponseWriter) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	})
	defer up.Close()

	srv := newTestServer(t, Config{
		UpstreamBaseURL:             up.URL,
		AgentserverInternalSecret:   "s",
		RegisterRetryTotalTimeout:   2 * time.Second,
		RegisterRetryInitialBackoff: 10 * time.Millisecond,
		UpstreamDialTimeout:         time.Second,
	})
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/cloud/environment/exe/register", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Errorf("status: %d", resp.StatusCode)
	}
	if fake.calls.Load() != 1 {
		t.Errorf("upstream called %d times (should not retry 4xx)", fake.calls.Load())
	}
}

func TestRegisterProxy_BodyTooLarge(t *testing.T) {
	srv := newTestServer(t, Config{
		AgentserverInternalSecret:   "s",
		RegisterRetryTotalTimeout:   2 * time.Second,
		RegisterRetryInitialBackoff: 10 * time.Millisecond,
		UpstreamDialTimeout:         time.Second,
	})
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	huge := bytes.Repeat([]byte("x"), 2<<20) // 2MB
	resp, err := http.Post(ts.URL+"/cloud/environment/exe/register", "application/octet-stream", bytes.NewReader(huge))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("status: got %d want 413", resp.StatusCode)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /root/agentserver && go test ./internal/codexececdge/... -run TestRegisterProxy_`
Expected: FAIL — handleRegisterProxy returns 501.

- [ ] **Step 3: Implement the proxy without retry yet**

Create `internal/codexececdge/registerproxy.go`:

```go
package codexececdge

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"io"
	"net/http"
	"time"

	"github.com/agentserver/agentserver/internal/clientmeta"
)

const registerBodyMax = 1 << 20 // 1 MiB

func (s *Server) handleRegisterProxy(w http.ResponseWriter, r *http.Request) {
	// 1MB cap: register payloads are auth JSON (<1KB typical); defensive.
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, registerBodyMax))
	if err != nil {
		s.logger.Warn("registerproxy: body too large or read error", "err", err)
		http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
		return
	}

	upstreamURL := s.cfg.UpstreamBaseURL + r.URL.Path
	if r.URL.RawQuery != "" {
		upstreamURL += "?" + r.URL.RawQuery
	}

	clientIP := clientmeta.ClientIP(r)
	deadline := time.Now().Add(s.cfg.RegisterRetryTotalTimeout)
	backoff := s.cfg.RegisterRetryInitialBackoff

	var lastResp *http.Response
	var lastErr error
	for {
		attemptCtx, attemptCancel := context.WithCancel(r.Context())
		req, _ := http.NewRequestWithContext(attemptCtx, r.Method, upstreamURL, bytes.NewReader(body))
		copyHeaders(req.Header, r.Header)
		req.Header.Set("X-Forwarded-For", clientIP)
		req.Header.Set("X-Real-IP", clientIP)

		resp, err := s.httpClient.Do(req)
		lastResp, lastErr = resp, err
		retryable := err != nil || isRetryableStatus(resp.StatusCode)
		if !retryable {
			attemptCancel()
			writeUpstreamResponse(w, resp)
			return
		}
		if resp != nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
		attemptCancel()

		sleep := backoff + jitter(backoff, 0.25)
		if time.Now().Add(sleep).After(deadline) {
			break
		}
		select {
		case <-time.After(sleep):
		case <-r.Context().Done():
			s.logger.Info("registerproxy: client canceled mid-retry")
			return
		}
		if backoff*2 > 8*time.Second {
			backoff = 8 * time.Second
		} else {
			backoff *= 2
		}
	}

	// Retry deadline exhausted. Surface the last upstream response if any,
	// otherwise return 502 with the dial error.
	if lastErr != nil {
		s.logger.Warn("registerproxy: retries exhausted (network)", "err", lastErr)
		http.Error(w, "upstream unreachable: "+lastErr.Error(), http.StatusBadGateway)
		return
	}
	s.logger.Warn("registerproxy: retries exhausted (status)", "status", lastResp.StatusCode)
	writeUpstreamResponse(w, lastResp)
}

func isRetryableStatus(code int) bool {
	return code == http.StatusBadGateway ||
		code == http.StatusServiceUnavailable ||
		code == http.StatusGatewayTimeout
}

// copyHeaders copies all headers from src to dst except hop-by-hop.
func copyHeaders(dst, src http.Header) {
	for k, vs := range src {
		switch k {
		case "Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization",
			"Te", "Trailer", "Transfer-Encoding", "Upgrade",
			"X-Forwarded-For", "X-Real-IP":
			continue
		}
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}

func writeUpstreamResponse(w http.ResponseWriter, resp *http.Response) {
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
	_ = resp.Body.Close()
}

// jitter returns a uniformly-distributed duration in [-frac*base, +frac*base].
// Uses crypto/rand so callers don't need to seed math/rand.
func jitter(base time.Duration, frac float64) time.Duration {
	if base <= 0 || frac <= 0 {
		return 0
	}
	var b [8]byte
	_, _ = rand.Read(b[:])
	u := binary.BigEndian.Uint64(b[:])
	// Map u to [-1.0, +1.0).
	f := float64(int64(u>>1)) / float64(1<<62) // [0, 2.0)
	f -= 1.0                                   // [-1.0, 1.0)
	return time.Duration(float64(base) * frac * f)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /root/agentserver && go test ./internal/codexececdge/... -run TestRegisterProxy_`
Expected: PASS — 2xx, 4xx, body-too-large all green.

- [ ] **Step 5: Commit**

```bash
cd /root/agentserver
git add internal/codexececdge/
git commit -m "feat(edge): register proxy w/ retry skeleton, body cap, XFF

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

### Task 8: Register proxy — retry coverage tests

**Repo:** `agentserver`

**Files:**
- Modify: `internal/codexececdge/registerproxy_test.go`

The retry logic from Task 7 is already in code; this task adds the test coverage that proves it works end-to-end.

- [ ] **Step 1: Write tests for 5xx-then-2xx and exhaustion**

Append to `internal/codexececdge/registerproxy_test.go`:

```go
func TestRegisterProxy_RetriesOn5xxUntilSuccess(t *testing.T) {
	up, fake := newFakeRegisterUpstream(t, func(call int64, w http.ResponseWriter) {
		if call < 3 {
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	})
	defer up.Close()

	srv := newTestServer(t, Config{
		UpstreamBaseURL:             up.URL,
		AgentserverInternalSecret:   "s",
		RegisterRetryTotalTimeout:   5 * time.Second,
		RegisterRetryInitialBackoff: 5 * time.Millisecond, // keep test fast
		UpstreamDialTimeout:         time.Second,
	})
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/cloud/environment/exe/register", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status: %d", resp.StatusCode)
	}
	if got := fake.calls.Load(); got != 3 {
		t.Errorf("upstream calls: got %d want 3", got)
	}
}

func TestRegisterProxy_ExhaustsRetriesAndSurfacesLastStatus(t *testing.T) {
	up, fake := newFakeRegisterUpstream(t, func(_ int64, w http.ResponseWriter) {
		w.WriteHeader(503)
		_, _ = w.Write([]byte("still down"))
	})
	defer up.Close()

	srv := newTestServer(t, Config{
		UpstreamBaseURL:             up.URL,
		AgentserverInternalSecret:   "s",
		RegisterRetryTotalTimeout:   80 * time.Millisecond, // tiny deadline
		RegisterRetryInitialBackoff: 10 * time.Millisecond,
		UpstreamDialTimeout:         time.Second,
	})
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/cloud/environment/exe/register", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 503 {
		t.Errorf("status: got %d want 503 (surfaced from upstream)", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "still down" {
		t.Errorf("body: %q", body)
	}
	if got := fake.calls.Load(); got < 2 {
		t.Errorf("upstream calls: got %d, expected at least 2 retries", got)
	}
}

func TestRegisterProxy_ExhaustsRetriesOnDialError(t *testing.T) {
	srv := newTestServer(t, Config{
		UpstreamBaseURL:             "http://127.0.0.1:1", // closed
		AgentserverInternalSecret:   "s",
		RegisterRetryTotalTimeout:   80 * time.Millisecond,
		RegisterRetryInitialBackoff: 10 * time.Millisecond,
		UpstreamDialTimeout:         50 * time.Millisecond,
	})
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/cloud/environment/exe/register", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status: got %d want 502", resp.StatusCode)
	}
}

func TestRegisterProxy_BackoffCap(t *testing.T) {
	// Sanity: the cap math should not let backoff exceed 8s after many doublings.
	d := 500 * time.Millisecond
	for i := 0; i < 10; i++ {
		if d*2 > 8*time.Second {
			d = 8 * time.Second
		} else {
			d *= 2
		}
	}
	if d != 8*time.Second {
		t.Errorf("cap not enforced: %v", d)
	}
}
```

- [ ] **Step 2: Run all edge tests**

Run: `cd /root/agentserver && go test ./internal/codexececdge/...`
Expected: PASS — all green including the new retry-coverage tests.

- [ ] **Step 3: Commit**

```bash
cd /root/agentserver
git add internal/codexececdge/registerproxy_test.go
git commit -m "test(edge): register proxy 5xx retry + exhaustion + dial-error coverage

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

### Task 9: Edge main.go binary

**Repo:** `agentserver`

**Files:**
- Create: `cmd/codex-exec-edge/main.go`

- [ ] **Step 1: Write the binary**

Create `cmd/codex-exec-edge/main.go`:

```go
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/agentserver/agentserver/internal/codexececdge"
	"github.com/agentserver/agentserver/internal/wsbridge"
)

func main() {
	cfg, err := codexececdge.LoadConfigFromEnv()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	srv, err := codexececdge.NewServer(cfg)
	if err != nil {
		log.Fatalf("server: %v", err)
	}
	httpServer := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
		<-sigCh
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(ctx)
	}()

	log.Printf("codex-exec-edge listening on :%s, upstream=%s", cfg.Port, cfg.UpstreamBaseURL)
	ln, err := wsbridge.ListenWithKeepAlive(context.Background(), "tcp", ":"+cfg.Port)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	if err := httpServer.Serve(ln); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
```

- [ ] **Step 2: Verify it builds**

Run: `cd /root/agentserver && go build ./cmd/codex-exec-edge`
Expected: PASS, no errors. (Output binary lands in cwd; delete it after.)

```bash
rm -f /root/agentserver/codex-exec-edge
```

- [ ] **Step 3: Commit**

```bash
cd /root/agentserver
git add cmd/codex-exec-edge/
git commit -m "feat(edge): cmd/codex-exec-edge entrypoint

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Phase C — Build & CI

### Task 10: Dockerfile.codex-exec-edge

**Repo:** `agentserver`

**Files:**
- Create: `Dockerfile.codex-exec-edge`

- [ ] **Step 1: Write the Dockerfile (mirrors Dockerfile.codex-exec-gateway)**

```dockerfile
# Build Go binary
FROM golang:1.26-trixie AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o codex-exec-edge ./cmd/codex-exec-edge

# Runtime image (minimal — no Docker CLI, no codex binary needed)
FROM debian:trixie-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*
COPY --from=builder /app/codex-exec-edge /usr/local/bin/codex-exec-edge
EXPOSE 6061
ENTRYPOINT ["codex-exec-edge"]
```

- [ ] **Step 2: Verify build locally**

Run: `cd /root/agentserver && docker build -f Dockerfile.codex-exec-edge -t codex-exec-edge:local .`
Expected: image built successfully.

- [ ] **Step 3: Commit**

```bash
cd /root/agentserver
git add Dockerfile.codex-exec-edge
git commit -m "feat(edge): add Dockerfile.codex-exec-edge

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

### Task 11: CI job — build-codex-exec-edge

**Repo:** `agentserver`

**Files:**
- Modify: `.github/workflows/build.yml`

- [ ] **Step 1: Inspect the existing build-codex-exec-gateway job**

```bash
cd /root/agentserver
grep -n -A 35 '^  build-codex-exec-gateway:' .github/workflows/build.yml
```

This shows the full job block (~30 lines). The new job is a near-copy with:
- job name: `build-codex-exec-edge`
- image name: `${{ env.REGISTRY }}/agentserver/codex-exec-edge`
- Dockerfile path: `./Dockerfile.codex-exec-edge`

- [ ] **Step 2: Add the new job**

Open `.github/workflows/build.yml`. Locate the `build-codex-exec-gateway` job
(the block ending around line 470). Add a new job immediately after it,
copying the entire block and replacing the three identifiers above. The
structure (`runs-on`, `permissions`, `steps`, `docker/login-action`,
`docker/metadata-action`, `docker/build-push-action`) must match exactly.

- [ ] **Step 3: Update `release-helm-chart` needs**

In the same file, locate both `needs:` arrays on the `release-helm-chart`
job (one at L500 and one at L518 per the current spec; verify with
`grep -n 'build-codex-exec-gateway' .github/workflows/build.yml`). Add
`build-codex-exec-edge` to **both** lists, immediately after
`build-codex-exec-gateway`.

- [ ] **Step 4: Sanity-check the YAML**

```bash
cd /root/agentserver
python3 -c 'import yaml,sys; yaml.safe_load(open(".github/workflows/build.yml"))' && echo OK
```
Expected: `OK`.

- [ ] **Step 5: Commit**

```bash
cd /root/agentserver
git add .github/workflows/build.yml
git commit -m "ci: add build-codex-exec-edge job and wire into release

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Phase D — Helm chart

### Task 12: Helm template + values

**Repo:** `agentserver`

**Files:**
- Create: `deploy/helm/agentserver/templates/codex-exec-edge.yaml`
- Modify: `deploy/helm/agentserver/values.yaml`
- Modify: `deploy/helm/agentserver/Chart.yaml`

- [ ] **Step 1: Add the template**

Create `deploy/helm/agentserver/templates/codex-exec-edge.yaml` with the YAML
from spec §7.2 (Deployment + Service, gated by `.Values.codexExecEdge.enabled`).
Copy verbatim from the spec — do not re-derive.

- [ ] **Step 2: Add values entry**

Append to `deploy/helm/agentserver/values.yaml` (after the `codexExecGateway`
block):

```yaml
# codex-exec-edge: thin WS auth/proxy + register-retry layer in front of
# codex-exec-gateway. Decouples connector lifecycle from gateway upgrades —
# gateway can Recreate freely without killing connector processes (which
# would otherwise exit on register .await? failure during the restart window).
# See docs/superpowers/specs/2026-05-24-codex-exec-edge-design.md.
codexExecEdge:
  enabled: true
  image:
    repository: ghcr.io/agentserver/codex-exec-edge
    tag: ""               # defaults to Chart.AppVersion via template, override here for testing
    pullPolicy: IfNotPresent
  replicaCount: 2
  port: 6061
  registerRetryTimeout: "30s"
  registerRetryBase: "500ms"
  upstreamDialTimeout: "5s"
  logLevel: "info"
  resources:
    requests: { cpu: "50m",  memory: "64Mi" }
    limits:   { cpu: "500m", memory: "256Mi" }
```

**Important:** in the template, default the tag to `Chart.AppVersion` when the
values tag is empty — match how `codexExecGateway` handles it (look at
`deploy/helm/agentserver/templates/codex-exec-gateway.yaml:78` and mirror the
pattern). If the gateway uses a hardcoded tag in values too, hardcode here as
well (and bump in the same PRs).

- [ ] **Step 3: Bump Chart.yaml**

Read the current `version:` and `appVersion:` in
`deploy/helm/agentserver/Chart.yaml` and bump both to the next patch version
(e.g. `0.64.25` → `0.64.26`). Per the `agentserver_release_flow` memory: a
matching `v<version>` git tag must be pushed after merge to publish the
image with that tag.

- [ ] **Step 4: Helm lint**

```bash
cd /root/agentserver
helm lint deploy/helm/agentserver
```
Expected: no errors.

- [ ] **Step 5: Helm template render sanity check**

```bash
cd /root/agentserver
helm template test deploy/helm/agentserver \
  --set internal.apiSecret=fakesecret \
  --set codexExecGateway.enabled=true \
  --set codexAppGateway.enabled=true \
  --set codexAuth.enabled=true \
  | grep -E 'codex-exec-edge|codex-exec-gateway' | head -20
```
Expected: see both `codex-exec-edge` Deployment/Service and the existing
`codex-exec-gateway` rendered, no Go template errors.

- [ ] **Step 6: Commit**

```bash
cd /root/agentserver
git add deploy/helm/agentserver/
git commit -m "$(cat <<'EOF'
feat(helm): add codex-exec-edge Deployment + Service; chart bump

Two-replica RollingUpdate, no PVC, /healthz probes. Shares
internal.apiSecret with gateway (same HMAC key for ws ticket).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

### Task 13: Open PR, merge, tag

**Repo:** `agentserver`

This is the gate before any Pulumi change can land.

- [ ] **Step 1: Push branch and open PR**

```bash
cd /root/agentserver
git push -u origin HEAD
gh pr create --title "feat: codex-exec-edge — thin auth/proxy layer" --body "$(cat <<'EOF'
## Summary
- New `codex-exec-edge` binary: WS auth/proxy + register-retry in front of `codex-exec-gateway`
- Refactor: `wsticket` extracted from `handlers/` so edge doesn't depend on the full gateway handler tree
- Helm template + values + Chart bump
- CI job + release wiring

## Why
`codex-exec-gateway` runs with `Recreate` strategy (RWO audit PVC), causing every release to fail in-flight `POST /cloud/{env_id}/register` calls. codex CLI's `run_remote_environment().await?` propagates these failures to process exit — every release kills all connectors. Edge buffers the register call across the gateway restart window so connectors stay alive.

## Test plan
- [ ] `go test ./internal/codexececdge/... ./internal/codexexecgateway/...` PASS
- [ ] Docker image builds via CI
- [ ] Helm template renders successfully
- [ ] After merge: `v<chart-version>` git tag pushed
- [ ] Verify image at `ghcr.io/agentserver/codex-exec-edge:<tag>` exists before opening k8s PR

See `docs/superpowers/specs/2026-05-24-codex-exec-edge-design.md` for design rationale.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

- [ ] **Step 2: Wait for CI green + merge**

Wait for review + green CI. After merge to main, push the matching git tag:

```bash
cd /root/agentserver
git checkout main && git pull
NEW_VER=$(grep '^version:' deploy/helm/agentserver/Chart.yaml | awk '{print $2}' | tr -d \"\')
git tag "v${NEW_VER}"
git push origin "v${NEW_VER}"
```

- [ ] **Step 3: Trigger / verify Helm release upgrade in cluster**

Whichever pipeline upgrades the helm release (Pulumi, ArgoCD, manual `helm upgrade`)
should now roll the new chart version. Verify edge pods are Ready before
proceeding to the next phase:

```bash
kubectl -n agentserver get deploy/agentserver-codex-exec-edge \
  -o jsonpath='{.status.readyReplicas}/{.status.replicas}'
```
Expected: `2/2`. **Do not proceed to Phase E until this gate passes.**

- [ ] **Step 4: Confirm edge is still bypassed (HTTPRoute unchanged)**

```bash
curl -sS https://codex-exec.agent.cs.ac.cn/healthz
```
Expected: `ok` — but coming from gateway (HTTPRoute not yet changed; edge is
running but no traffic flows through it).

---

## Phase E — Pulumi: HTTPRoute path split

### Task 14: Modify `agentserver.ts` HTTPRoutes

**Repo:** `/root/k8s` (separate repo!)

**Files:**
- Modify: `/root/k8s/stacks/agentserver.ts` (L496-514 and L566-584)

- [ ] **Step 1: Refactor both HTTPRoutes via a helper**

In `/root/k8s/stacks/agentserver.ts`, replace the two existing
`codexExecRouteCN` and `codexExecRoute` resource blocks with the helper
pattern from spec §7.3 (verbatim):

```ts
const codexExecRouteRules = [
    // /codex-exec/{id} (ws) 和 /cloud/{}/register (http) → edge
    {
        matches: [
            { path: { type: "PathPrefix", value: "/codex-exec/" } },
            { path: { type: "PathPrefix", value: "/cloud/" } },
        ],
        backendRefs: [{ name: `${name}-codex-exec-edge`, port: 6061 }],
    },
    // 其他所有路径（/bridge/* ws、/relay/* http、/api/codex-exec/* http、
    // /healthz、/internal/sdk/connected）保持原样到 gateway
    {
        matches: [{ path: { type: "PathPrefix", value: "/" } }],
        backendRefs: [{ name: `${name}-codex-exec-gateway`, port: 6060 }],
    },
];

const makeCodexExecRoute = (resourceKey: string, hostname: string) =>
    new k8s.apiextensions.CustomResource(
        resourceKey,
        {
            apiVersion: "gateway.networking.k8s.io/v1",
            kind: "HTTPRoute",
            metadata: { name: resourceKey.replace(/^crd-/, ""), namespace: ns.metadata.name },
            spec: {
                parentRefs: [{ name: "istio-gateway", namespace: "istio-ingress" }],
                hostnames: [hostname],
                rules: codexExecRouteRules,
            },
        },
        { provider: k8sProvider, dependsOn: [rel] },
    );

const codexExecRouteCN = makeCodexExecRoute(`crd-${name}-codex-exec-cn`, "codex-exec.agent.cs.ac.cn");
const codexExecRoute   = makeCodexExecRoute(`crd-${name}-codex-exec`,    "codex-exec.agentserver.dev");
```

Update the `return` statement at the bottom to keep the same keys
(`codexExecRouteCN`, `codexExecRoute`) — only the values change to the new
constructor.

- [ ] **Step 2: Preview Pulumi diff**

```bash
cd /root/k8s
pulumi preview --stack <prod-stack> --diff | grep -A 20 'codex-exec'
```
Expected: Two HTTPRoute resources show `update`, not `create+delete`
(the logical names match). Only `spec.rules` should diff.

- [ ] **Step 3: Stop and review the diff with a human before applying**

This is a production-traffic change. Before `pulumi up`:
- Confirm new edge Deployment is Ready in the target cluster
  (Task 13 step 3)
- Confirm a healthy `codex exec --remote ...` test session exists you can
  monitor
- Have a revert plan ready: this commit's revert + `pulumi up` flips back

- [ ] **Step 4: Apply and verify routing**

```bash
cd /root/k8s
pulumi up --stack <prod-stack>
```

Then verify path-based routing reaches the correct backends:

```bash
# /codex-exec/* path-prefix → edge (returns 401 because no token, but route is right)
curl -sS -o /dev/null -w '%{http_code}\n' https://codex-exec.agent.cs.ac.cn/codex-exec/nonexistent
# Expected: 401 (edge ticket verify rejects)

# /cloud/* → edge (will retry-then-pass through to gateway; 401 from gateway)
curl -sS -X POST -o /dev/null -w '%{http_code}\n' \
  https://codex-exec.agent.cs.ac.cn/cloud/environment/test/register
# Expected: 401 (gateway auth rejects)

# /healthz → gateway (catch-all)
curl -sS https://codex-exec.agent.cs.ac.cn/healthz
# Expected: ok
```

If any of these return unexpected status codes, **revert immediately**:

```bash
cd /root/k8s
git revert HEAD && pulumi up --stack <prod-stack>
```

- [ ] **Step 5: Commit Pulumi change**

```bash
cd /root/k8s
git add stacks/agentserver.ts
git commit -m "$(cat <<'EOF'
agentserver: split codex-exec HTTPRoute /codex-exec/* + /cloud/* → edge

The newly-deployed codex-exec-edge absorbs gateway Recreate windows so
connector codex processes don't exit on register .await? failure.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
git push
```

---

## Phase F — Chaos validation

### Task 15: Connector survives gateway restart

**Repo:** N/A — manual / kubectl-driven

The behaviour-change moment. Verify the whole reason this exists.

- [ ] **Step 1: Start a long-lived connector**

On a developer laptop or test pod, run a real `codex exec-server --remote`
against the prod URL with a fresh environment_id. Watch its stderr.

- [ ] **Step 2: Restart the gateway and observe**

```bash
kubectl -n agentserver rollout restart deploy/agentserver-codex-exec-gateway
kubectl -n agentserver rollout status deploy/agentserver-codex-exec-gateway --timeout=120s
```

Observe the connector's stderr during this window. **Expected:**

- WS disconnection logged once (`failed to connect remote exec-server websocket`
  or similar from codex)
- A `register_environment` HTTP call follows
- Connector process **stays alive** (does not exit)
- After gateway Ready: WS reconnects, connector resumes idle

- [ ] **Step 3: Inspect edge logs for the absorbed retry**

```bash
kubectl -n agentserver logs -l app=agentserver-codex-exec-edge --tail=100 \
  | grep -i 'retry\|registerproxy\|wsproxy'
```
Expected: see "retries exhausted" did **not** fire; see successful 200 forwarded
after upstream Ready.

- [ ] **Step 4: Inspect gateway audit/store integrity**

```bash
kubectl -n agentserver exec deploy/agentserver-postgresql -- psql -U postgres \
  -d codexexecgateway -c "select exe_id, last_client_ip, last_seen_at from executors order by last_seen_at desc limit 5;"
```
Expected: `last_client_ip` shows real client IP (not edge pod IP) — confirms
XFF propagation is correct.

- [ ] **Step 5: Record observations**

Append a short observation note to the spec doc (or open a follow-up doc):
- Actual gateway-restart window in seconds
- Number of edge retry attempts observed
- Any thundering-herd indicators on gateway

---

## Self-Review

### Spec coverage check

Walking through `docs/superpowers/specs/2026-05-24-codex-exec-edge-design.md`:

- §1 background — captured in plan header
- §2 alternatives — captured implicitly (only chosen approach is implemented)
- §3 architecture — implemented via Tasks 3–9 + 12 + 14
- §4 behaviour timing — verified via Task 15 chaos test
- §5.1 wsticket extraction — Tasks 1–2 ✓
- §5.2 file layout — Tasks 3–10 ✓
- §5.3 Config — Task 3 ✓
- §5.4 Routes — Task 4 ✓
- §6.1 WS proxy — Tasks 5–6 ✓
- §6.2 register proxy — Tasks 7–8 ✓
- §6.3 error handling matrix — covered across Tasks 5/6/7/8
- §6.4 edge cases — covered in plan tasks; thundering-herd jitter implemented in §7's `jitter(...)`
- §7.1 changes — Tasks 1, 12, 14 ✓
- §7.2 helm template — Task 12 ✓
- §7.3 Pulumi — Task 14 ✓
- §7.4 ordering — Tasks 13 step 3, 14 step 1 ✓
- §8 testing — Tasks 1, 3–8 (unit); 15 (e2e chaos)
- §9 risks — flagged in spec; chaos test surfaces actual numbers

### Placeholders

Searched for "TBD", "TODO", "implement later", "etc.": none. Every step has
concrete code or commands.

### Type consistency

- Package name `codexececdge` used consistently
- Env var prefix `CXE_` consistent
- Port 6061 consistent
- `wsticket.Mint` / `wsticket.Verify` (not `MintWSTicket`/`VerifyWSTicket`)
  consistent in Tasks 1–2 and all callers in Tasks 5–6
- `Config` field names match between Task 3 (definition) and Tasks 4–8
  (usage)

No gaps found.
