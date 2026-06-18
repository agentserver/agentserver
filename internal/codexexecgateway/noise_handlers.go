package codexexecgateway

import (
	"context"
	"crypto/hmac"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"

	"nhooyr.io/websocket"

	"github.com/agentserver/agentserver/internal/codexexecgateway/noise"
	"github.com/go-chi/chi/v5"
)

// NoiseRelaySecurityProfile is the only profile this gateway speaks.
// Both ends ship it verbatim in registration metadata so any future
// protocol change (e.g. ChaChaPoly suite, ML-KEM-1024) can negotiate.
const NoiseRelaySecurityProfile = "noise_hybrid_ik_v1"

// NoiseHandlers bundles the noise relay endpoints and their shared
// state (the gateway HMAC key, the in-memory relay WS registry,
// optionally the per-executor frame router). One instance per Server,
// mounted under /cloud/...
//
// router is optional: when nil the WS handler still accepts and
// parks the executor connection (Phase 2 stub behaviour). When set,
// each accepted WS is handed off to router.ServeExecutorFrames so
// noise streams can multiplex onto it.
type NoiseHandlers struct {
	store    *Store
	hmacKey  []byte
	wsHub    *noiseWSHub
	wsPubURL string // public ws:// or wss:// base for relay URLs
	router   *NoiseRouter
}

func NewNoiseHandlers(store *Store, hmacKey []byte, wsPublicBaseURL string) *NoiseHandlers {
	return &NoiseHandlers{
		store:    store,
		hmacKey:  hmacKey,
		wsHub:    newNoiseWSHub(),
		wsPubURL: wsPublicBaseURL,
	}
}

// AttachRouter wires a NoiseRouter into the WS handler. After this,
// any accepted executor WS is served by router.ServeExecutorFrames
// instead of the stub discard loop.
func (h *NoiseHandlers) AttachRouter(r *NoiseRouter) { h.router = r }

// WSHub is exposed so the router (constructed externally) can call
// ConnectionFor when opening a new stream.
func (h *NoiseHandlers) WSHub() *noiseWSHub { return h.wsHub }

// Mount wires the four noise endpoints onto a chi router. Caller is
// responsible for applying any auth middleware on the outer route.
func (h *NoiseHandlers) Mount(r chi.Router) {
	r.Post("/cloud/environment/{env_id}/register", h.handleRegister)
	r.Post("/cloud/environment/{env_id}/connect", h.handleConnect)
	r.Post("/cloud/environment/{env_id}/validate", h.handleValidate)
	r.Get("/cloud/relay/{registration_id}", h.handleRelayWS)
}

// --- request / response shapes (must stay byte-compatible with
// codex-rs/exec-server/src/environment_registry.rs)

type noiseRegisterRequest struct {
	SecurityProfile   string          `json:"security_profile"`
	ExecutorPublicKey noise.PublicKey `json:"executor_public_key"`
}

type noiseRegisterResponse struct {
	EnvironmentID          string `json:"environment_id"`
	URL                    string `json:"url"`
	SecurityProfile        string `json:"security_profile"`
	ExecutorRegistrationID string `json:"executor_registration_id"`
}

type noiseConnectRequest struct {
	HarnessPublicKey noise.PublicKey `json:"harness_public_key"`
}

type noiseConnectResponse struct {
	EnvironmentID            string          `json:"environment_id"`
	URL                      string          `json:"url"`
	SecurityProfile          string          `json:"security_profile"`
	ExecutorRegistrationID   string          `json:"executor_registration_id"`
	ExecutorPublicKey        noise.PublicKey `json:"executor_public_key"`
	HarnessKeyAuthorization  string          `json:"harness_key_authorization"`
}

type noiseValidateRequest struct {
	ExecutorRegistrationID   string          `json:"executor_registration_id"`
	HarnessPublicKey         noise.PublicKey `json:"harness_public_key"`
	HarnessKeyAuthorization  string          `json:"harness_key_authorization"`
}

type noiseValidateResponse struct {
	Valid bool `json:"valid"`
}

// --- handlers

func (h *NoiseHandlers) handleRegister(w http.ResponseWriter, r *http.Request) {
	envID := chi.URLParam(r, "env_id")
	if envID == "" {
		writeJSONErr(w, http.StatusBadRequest, "env_id required")
		return
	}
	var req noiseRegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONErr(w, http.StatusBadRequest, fmt.Sprintf("invalid register body: %s", err))
		return
	}
	if req.SecurityProfile != NoiseRelaySecurityProfile {
		writeJSONErr(w, http.StatusBadRequest,
			fmt.Sprintf("unsupported security_profile %q", req.SecurityProfile))
		return
	}
	if req.ExecutorPublicKey.Suite != noise.SuiteName {
		writeJSONErr(w, http.StatusBadRequest,
			fmt.Sprintf("unsupported executor key suite %q", req.ExecutorPublicKey.Suite))
		return
	}
	// Parse to validate the base64 + lengths before persisting.
	if _, _, err := req.ExecutorPublicKey.Decode(); err != nil {
		writeJSONErr(w, http.StatusBadRequest, err.Error())
		return
	}

	reg, err := h.store.UpsertNoiseExecutorRegistration(r.Context(), envID, req.ExecutorPublicKey)
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, noiseRegisterResponse{
		EnvironmentID:          envID,
		URL:                    h.relayWSURL(r, reg.RegistrationID),
		SecurityProfile:        NoiseRelaySecurityProfile,
		ExecutorRegistrationID: reg.RegistrationID,
	})
}

func (h *NoiseHandlers) handleConnect(w http.ResponseWriter, r *http.Request) {
	envID := chi.URLParam(r, "env_id")
	if envID == "" {
		writeJSONErr(w, http.StatusBadRequest, "env_id required")
		return
	}
	var req noiseConnectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONErr(w, http.StatusBadRequest, fmt.Sprintf("invalid connect body: %s", err))
		return
	}
	if req.HarnessPublicKey.Suite != noise.SuiteName {
		writeJSONErr(w, http.StatusBadRequest,
			fmt.Sprintf("unsupported harness key suite %q", req.HarnessPublicKey.Suite))
		return
	}
	if _, _, err := req.HarnessPublicKey.Decode(); err != nil {
		writeJSONErr(w, http.StatusBadRequest, err.Error())
		return
	}

	reg, err := h.store.LookupNoiseExecutorRegistrationByEnv(r.Context(), envID)
	if errors.Is(err, ErrNoiseRegistrationNotFound) {
		writeJSONErr(w, http.StatusNotFound, "no executor registered for env_id")
		return
	}
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	auth := h.mintHarnessAuthorization(reg.RegistrationID, req.HarnessPublicKey)
	writeJSON(w, http.StatusOK, noiseConnectResponse{
		EnvironmentID:            envID,
		URL:                      h.relayWSURL(r, reg.RegistrationID),
		SecurityProfile:          NoiseRelaySecurityProfile,
		ExecutorRegistrationID:   reg.RegistrationID,
		ExecutorPublicKey:        reg.PublicKey,
		HarnessKeyAuthorization:  auth,
	})
}

func (h *NoiseHandlers) handleValidate(w http.ResponseWriter, r *http.Request) {
	envID := chi.URLParam(r, "env_id")
	if envID == "" {
		writeJSONErr(w, http.StatusBadRequest, "env_id required")
		return
	}
	var req noiseValidateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONErr(w, http.StatusBadRequest, fmt.Sprintf("invalid validate body: %s", err))
		return
	}
	// Recompute the HMAC and constant-time compare.
	expected := h.mintHarnessAuthorization(req.ExecutorRegistrationID, req.HarnessPublicKey)
	if !hmac.Equal([]byte(expected), []byte(req.HarnessKeyAuthorization)) {
		writeJSON(w, http.StatusOK, noiseValidateResponse{Valid: false})
		return
	}
	// Verify env_id matches the registration we issued.
	reg, err := h.store.LookupNoiseExecutorRegistration(r.Context(), req.ExecutorRegistrationID)
	if errors.Is(err, ErrNoiseRegistrationNotFound) || (err == nil && reg.EnvID != envID) {
		writeJSON(w, http.StatusOK, noiseValidateResponse{Valid: false})
		return
	}
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, noiseValidateResponse{Valid: true})
}

// handleRelayWS is stubbed in Phase 2.5 — accepts the WS and parks
// the connection in the in-memory hub. Phase 3 wires the actual
// noise frame routing in.
func (h *NoiseHandlers) handleRelayWS(w http.ResponseWriter, r *http.Request) {
	registrationID := chi.URLParam(r, "registration_id")
	if registrationID == "" {
		writeJSONErr(w, http.StatusBadRequest, "registration_id required")
		return
	}
	if _, err := h.store.LookupNoiseExecutorRegistration(r.Context(), registrationID); err != nil {
		writeJSONErr(w, http.StatusNotFound, "unknown registration_id")
		return
	}
	var serveFrames func(ctx context.Context, conn *websocket.Conn) error
	if h.router != nil {
		serveFrames = func(ctx context.Context, conn *websocket.Conn) error {
			return h.router.ServeExecutorFrames(ctx, registrationID, conn)
		}
	}
	if err := h.wsHub.accept(w, r, registrationID, serveFrames); err != nil {
		// accept() has already responded; just log via the writer.
		return
	}
}

// mintHarnessAuthorization derives a short opaque token that proves
// the gateway issued this (registration_id, harness_pubkey) pairing.
// Delegates to the package-level helper so the router (initiator
// side) stays byte-identical with this validator side.
func (h *NoiseHandlers) mintHarnessAuthorization(registrationID string, harnessPK noise.PublicKey) string {
	return mintHarnessAuthorization(h.hmacKey, registrationID, harnessPK)
}

func (h *NoiseHandlers) relayWSURL(r *http.Request, registrationID string) string {
	base := strings.TrimRight(h.wsPubURL, "/")
	if base == "" {
		// Best-effort fallback from the inbound request host.
		scheme := "ws"
		if r.TLS != nil {
			scheme = "wss"
		}
		base = scheme + "://" + r.Host
	}
	return base + "/cloud/relay/" + registrationID
}

// --- in-memory relay hub (per-pod; single-replica per §6 D-3)

type noiseWSHub struct {
	mu          sync.Mutex
	connections map[string]*websocket.Conn // registration_id → live executor connection
}

func newNoiseWSHub() *noiseWSHub {
	return &noiseWSHub{connections: map[string]*websocket.Conn{}}
}

// accept upgrades the request to a WS and registers the connection
// under the executor's registration_id. If a NoiseRouter was attached,
// frames are handed off to its demux loop; otherwise the connection
// is parked (legacy Phase 2 behaviour).
//
// Reconnect wins: if a second executor connects with the same
// registration_id, the previous connection is closed (same semantics
// as bridge.go's single-active-bridge invariant).
func (h *noiseWSHub) accept(w http.ResponseWriter, r *http.Request, registrationID string, serveFrames func(ctx context.Context, conn *websocket.Conn) error) error {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return fmt.Errorf("ws accept: %w", err)
	}
	conn.SetReadLimit(256 * 1024)

	h.mu.Lock()
	if prev := h.connections[registrationID]; prev != nil {
		_ = prev.Close(websocket.StatusGoingAway, "replaced by reconnect")
	}
	h.connections[registrationID] = conn
	h.mu.Unlock()

	defer func() {
		h.mu.Lock()
		if h.connections[registrationID] == conn {
			delete(h.connections, registrationID)
		}
		h.mu.Unlock()
		_ = conn.Close(websocket.StatusNormalClosure, "")
	}()

	ctx := r.Context()
	if serveFrames != nil {
		return serveFrames(ctx, conn)
	}
	// Fallback: read and discard until the executor closes. Keeps the
	// WS open so any in-flight registration check in a test or staging
	// env doesn't blow up.
	for {
		_, _, err := conn.Read(ctx)
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			return nil
		}
	}
}

// ConnectionFor returns the live WS for a registration_id, or nil if
// no executor is currently connected. Phase 3 uses this to forward
// noise frames from harness bridges into the right executor.
func (h *noiseWSHub) ConnectionFor(registrationID string) *websocket.Conn {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.connections[registrationID]
}

// noiseHandlersLogger is a thin wrapper around the stdlib logger to
// keep the handler signatures clean. Real wiring goes through the
// gateway's existing logger config.
var _ = log.Default
var _ context.Context

// writeJSONErr lives in handlers_relay.go; writeJSON is local because
// noise responses use typed structs rather than map[string]string.

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
