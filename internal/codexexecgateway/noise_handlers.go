package codexexecgateway

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
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
// state (the gateway HMAC key, the in-memory relay WS registry). One
// instance is built per Server and mounted under /cloud/...
type NoiseHandlers struct {
	store    *Store
	hmacKey  []byte
	wsHub    *noiseWSHub
	wsPubURL string // public ws:// or wss:// base for relay URLs
}

func NewNoiseHandlers(store *Store, hmacKey []byte, wsPublicBaseURL string) *NoiseHandlers {
	return &NoiseHandlers{
		store:    store,
		hmacKey:  hmacKey,
		wsHub:    newNoiseWSHub(),
		wsPubURL: wsPublicBaseURL,
	}
}

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
	if err := h.wsHub.accept(w, r, registrationID); err != nil {
		// accept() has already responded; just log via the writer.
		return
	}
}

// mintHarnessAuthorization derives a short opaque token that proves
// the gateway issued this (registration_id, harness_pubkey) pairing.
// The executor extracts it from the encrypted IK msg1 payload and POSTs
// it back here for validation. HMAC-SHA256 over a length-prefixed
// canonicalization so distinct tuples cannot collide.
func (h *NoiseHandlers) mintHarnessAuthorization(registrationID string, harnessPK noise.PublicKey) string {
	mac := hmac.New(sha256.New, h.hmacKey)
	for _, part := range []string{
		"noise-relay-harness-auth/v1",
		registrationID,
		harnessPK.Suite,
		harnessPK.X25519PublicKey,
		harnessPK.MLKEM768PublicKey,
	} {
		var lenBuf [8]byte
		for i := 7; i >= 0; i-- {
			lenBuf[i] = byte(len(part) >> uint(8*(7-i)))
		}
		mac.Write(lenBuf[:])
		mac.Write([]byte(part))
	}
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
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

// accept upgrades the request to a WS, registers the connection under
// the executor's registration_id, and parks it. Phase 2 only handles
// the transport boundary — no frame parsing or noise routing yet
// (Phase 3 layers that on top of the registered connection).
//
// If a second executor connects with the same registration_id, the
// previous connection is closed first so a reconnecting executor
// always wins over a stale one (same semantics as bridge.go's
// single-active-bridge invariant).
func (h *noiseWSHub) accept(w http.ResponseWriter, r *http.Request, registrationID string) error {
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

	// Phase 2.5 stub loop: read and discard any frames the executor
	// sends so the WS stays open. nhooyr's Reader returns an error
	// on disconnect, which is the loop exit.
	ctx := r.Context()
	for {
		mt, payload, err := conn.Read(ctx)
		if err != nil {
			return nil
		}
		// Drop everything; Phase 3 routes binary frames into the
		// noise wrapper and ignores text.
		_ = mt
		_ = payload
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
