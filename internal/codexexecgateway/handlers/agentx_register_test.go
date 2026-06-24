package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/internal/codexexecgateway/wsticket"
	"github.com/go-chi/chi/v5"
)

// mockAgentxRegisterStore implements AgentxRegisterStore and exposes
// UserIDForExecutor (matched by assertExeOwnedByUser's ad-hoc interface
// assertion). Lets us exercise AgentxRegister without a real DB.
type mockAgentxRegisterStore struct {
	owner      string
	ownerErr   error
	registered bool // false → UserIDForExecutor returns "" (unknown executor)
}

func (m *mockAgentxRegisterStore) UserIDForExecutor(ctx context.Context, exeID string) (string, error) {
	if !m.registered {
		return "", m.ownerErr
	}
	return m.owner, m.ownerErr
}

func newStoreWithExecutor(t *testing.T, exeID, userID string) *mockAgentxRegisterStore {
	t.Helper()
	return &mockAgentxRegisterStore{owner: userID, registered: true}
}

const testTicketSecret = "ticket-secret"

func TestAgentxRegister_BearerScheme_DelegatesToAgentserver(t *testing.T) {
	agentSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/codex-auth/validate" {
			http.Error(w, "wrong path", 404)
			return
		}
		var req struct {
			Scheme string `json:"scheme"`
			Token  string `json:"token"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		if req.Scheme == "bearer" && req.Token == "valid-bearer" {
			json.NewEncoder(w).Encode(map[string]string{"user_id": "u-1"})
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "no"})
	}))
	defer agentSrv.Close()

	store := newStoreWithExecutor(t, "exe_x", "u-1")
	h := AgentxRegister(store, "wss://test", AgentserverValidator{
		BaseURL:        agentSrv.URL,
		InternalSecret: "shh",
	}, testTicketSecret)
	req := httptest.NewRequest(http.MethodPost, "/agentx/environment/exe_x/register", nil)
	req.Header.Set("Authorization", "Bearer valid-bearer")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("exe_id", "exe_x")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusOK {
		body, _ := io.ReadAll(rr.Body)
		t.Fatalf("status = %d body = %s", rr.Code, body)
	}
	var resp cloudRegisterResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ExecutorID != "exe_x" {
		t.Fatalf("executor_id = %q", resp.ExecutorID)
	}
	if !strings.HasPrefix(resp.URL, "wss://test/agentx/exe_x?token=") {
		t.Fatalf("url = %q", resp.URL)
	}

	// Verify the ticket in the URL passes VerifyWSTicket with the same secret.
	const prefix = "wss://test/agentx/exe_x?token="
	ticket := strings.TrimPrefix(resp.URL, prefix)
	if err := wsticket.Verify(ticket, "exe_x", testTicketSecret); err != nil {
		t.Fatalf("ticket should verify: %v", err)
	}
}

// Wrong-owner: agentserver validates token (returns u-2) but executor
// is owned by u-1 → 403.
func TestAgentxRegister_BearerScheme_WrongOwner(t *testing.T) {
	agentSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"user_id": "u-2"})
	}))
	defer agentSrv.Close()

	store := newStoreWithExecutor(t, "exe_x", "u-1")
	h := AgentxRegister(store, "wss://test", AgentserverValidator{
		BaseURL:        agentSrv.URL,
		InternalSecret: "shh",
	}, testTicketSecret)
	req := httptest.NewRequest(http.MethodPost, "/agentx/environment/exe_x/register", nil)
	req.Header.Set("Authorization", "Bearer some-token")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("exe_id", "exe_x")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d body=%s", rr.Code, rr.Body.String())
	}
}

// Without validator configured (BaseURL empty), every register attempt
// must be rejected — no legacy bcrypt fallback anymore.
func TestAgentxRegister_RejectsWithoutValidator(t *testing.T) {
	store := newStoreWithExecutor(t, "exe_x", "u-1")
	h := AgentxRegister(store, "wss://test", AgentserverValidator{}, testTicketSecret)

	req := httptest.NewRequest(http.MethodPost, "/agentx/environment/exe_x/register", nil)
	req.Header.Set("Authorization", "Bearer any-token")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("exe_id", "exe_x")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d body=%s", rr.Code, rr.Body.String())
	}
}

// The agentx path uses env_id as the chi param. The handler accepts either
// exe_id or env_id (exe_id takes precedence when both present) so that the
// same handler can be tested with either param style.
func TestAgentxRegister_AcceptsEnvIDParam(t *testing.T) {
	agentSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"user_id": "u-1"})
	}))
	defer agentSrv.Close()

	store := newStoreWithExecutor(t, "exe_x", "u-1")
	h := AgentxRegister(store, "wss://test", AgentserverValidator{
		BaseURL: agentSrv.URL, InternalSecret: "shh",
	}, testTicketSecret)

	req := httptest.NewRequest(http.MethodPost, "/agentx/environment/exe_x/register", nil)
	req.Header.Set("Authorization", "Bearer any-token")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("env_id", "exe_x") // agentx path uses env_id
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var resp cloudRegisterResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ExecutorID != "exe_x" {
		t.Fatalf("executor_id = %q", resp.ExecutorID)
	}
}

// TestAgentxRegister_ResponseDecodesAgainstCodexSchemas pins our response
// shape against the agentx exec-server wire format. The response must
// satisfy both executor_id (agentx ≤0.132 compat) and environment_id
// (agentx 0.133+) fields simultaneously.
//
// Agentx exec-server sources (verbatim — keep in sync when bumping):
//   exec-server/src/remote.rs:
//     struct EnvironmentRegistryEnvironmentRegistrationResponse {
//         pub environment_id: String,
//         pub url: String,
//     }
func TestAgentxRegister_ResponseDecodesAgainstCodexSchemas(t *testing.T) {
	agentSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"user_id": "u-1"})
	}))
	defer agentSrv.Close()

	store := newStoreWithExecutor(t, "exe_x", "u-1")
	h := AgentxRegister(store, "wss://test", AgentserverValidator{
		BaseURL: agentSrv.URL, InternalSecret: "shh",
	}, testTicketSecret)
	req := httptest.NewRequest(http.MethodPost, "/agentx/environment/exe_x/register", nil)
	req.Header.Set("Authorization", "Bearer any-token")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("env_id", "exe_x")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	// codex 0.132 schema (struct fields are required — serde with no
	// #[serde(default)] fails the whole decode if missing).
	var v132 struct {
		ExecutorID string `json:"executor_id"`
		URL        string `json:"url"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &v132); err != nil {
		t.Fatalf("codex 0.132 decode failed: %v body=%s", err, rr.Body.String())
	}
	if v132.ExecutorID == "" || v132.URL == "" {
		t.Errorf("codex 0.132 fields missing: %+v", v132)
	}

	// codex 0.133 schema.
	var v133 struct {
		EnvironmentID string `json:"environment_id"`
		URL           string `json:"url"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &v133); err != nil {
		t.Fatalf("codex 0.133 decode failed: %v body=%s", err, rr.Body.String())
	}
	if v133.EnvironmentID == "" || v133.URL == "" {
		t.Errorf("codex 0.133 fields missing: %+v", v133)
	}

	// Both id fields must carry the same opaque value.
	if v132.ExecutorID != v133.EnvironmentID {
		t.Errorf("executor_id (%q) != environment_id (%q)", v132.ExecutorID, v133.EnvironmentID)
	}
}

// Both legacy `exe_id` and new `env_id` chi params resolve to the same
// id when both happen to be set (defensive — production routes never
// set both, but document the precedence).
func TestAgentxRegister_ExeIDTakesPrecedenceOverEnvID(t *testing.T) {
	agentSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"user_id": "u-1"})
	}))
	defer agentSrv.Close()

	store := newStoreWithExecutor(t, "exe_legacy", "u-1")
	h := AgentxRegister(store, "wss://test", AgentserverValidator{
		BaseURL: agentSrv.URL, InternalSecret: "shh",
	}, testTicketSecret)

	req := httptest.NewRequest(http.MethodPost, "/agentx/environment/exe_legacy/register", nil)
	req.Header.Set("Authorization", "Bearer any-token")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("exe_id", "exe_legacy")
	rctx.URLParams.Add("env_id", "exe_new") // ignored when exe_id present
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rr.Code, rr.Body.String())
	}
}
