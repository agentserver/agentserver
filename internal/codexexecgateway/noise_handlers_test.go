package codexexecgateway

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/internal/codexexecgateway/noise"
	"github.com/go-chi/chi/v5"
)

func newTestNoiseHandlers(t *testing.T) (*NoiseHandlers, *Store) {
	t.Helper()
	store := newTestStore(t)
	// The shared truncateForTest no longer clears noise_executor_registrations,
	// so wipe it explicitly to keep per-env LookupByEnv deterministic
	// across re-runs.
	_, _ = store.db.Exec(`DELETE FROM noise_executor_registrations`)
	h := NewNoiseHandlers(store, []byte("test-hmac-key-32-bytes-aaaaaaaaaa"), "ws://test")
	return h, store
}

// clearNoiseRegistrations is the same wipe but for tests that build the
// handlers themselves rather than via newTestNoiseHandlers.
func clearNoiseRegistrations(t *testing.T, s *Store) {
	t.Helper()
	_, _ = s.db.Exec(`DELETE FROM noise_executor_registrations`)
}

func mountedRouter(h *NoiseHandlers) http.Handler {
	r := chi.NewRouter()
	h.Mount(r)
	return r
}

func TestNoiseRegister_HappyPath(t *testing.T) {
	h, _ := newTestNoiseHandlers(t)
	srv := httptest.NewServer(mountedRouter(h))
	defer srv.Close()

	executor, err := noise.GenerateIdentity()
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	body, _ := json.Marshal(noiseRegisterRequest{
		SecurityProfile:   NoiseRelaySecurityProfile,
		ExecutorPublicKey: executor.PublicKey(),
	})
	resp, err := http.Post(srv.URL+"/cloud/environment/env-1/register", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, b)
	}
	var reg noiseRegisterResponse
	if err := json.NewDecoder(resp.Body).Decode(&reg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if reg.EnvironmentID != "env-1" {
		t.Errorf("env_id = %q", reg.EnvironmentID)
	}
	if reg.SecurityProfile != NoiseRelaySecurityProfile {
		t.Errorf("security_profile = %q", reg.SecurityProfile)
	}
	if !strings.HasPrefix(reg.ExecutorRegistrationID, "exr_") {
		t.Errorf("registration_id shape = %q", reg.ExecutorRegistrationID)
	}
	if !strings.HasSuffix(reg.URL, "/cloud/relay/"+reg.ExecutorRegistrationID) {
		t.Errorf("url = %q does not contain expected relay path", reg.URL)
	}
}

func TestNoiseRegister_IdempotentOnSamePubkey(t *testing.T) {
	h, _ := newTestNoiseHandlers(t)
	srv := httptest.NewServer(mountedRouter(h))
	defer srv.Close()

	executor, err := noise.GenerateIdentity()
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	body, _ := json.Marshal(noiseRegisterRequest{
		SecurityProfile:   NoiseRelaySecurityProfile,
		ExecutorPublicKey: executor.PublicKey(),
	})
	first := postRegister(t, srv, body)
	second := postRegister(t, srv, body)
	if first.ExecutorRegistrationID != second.ExecutorRegistrationID {
		t.Errorf("registration_id changed on re-register: %q → %q",
			first.ExecutorRegistrationID, second.ExecutorRegistrationID)
	}
}

func TestNoiseRegister_RejectsWrongProfile(t *testing.T) {
	h, _ := newTestNoiseHandlers(t)
	srv := httptest.NewServer(mountedRouter(h))
	defer srv.Close()

	executor, _ := noise.GenerateIdentity()
	body, _ := json.Marshal(noiseRegisterRequest{
		SecurityProfile:   "noise_classic_v0",
		ExecutorPublicKey: executor.PublicKey(),
	})
	resp, _ := http.Post(srv.URL+"/cloud/environment/env-1/register", "application/json", bytes.NewReader(body))
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestNoiseConnect_HappyPath(t *testing.T) {
	h, _ := newTestNoiseHandlers(t)
	srv := httptest.NewServer(mountedRouter(h))
	defer srv.Close()

	executor, _ := noise.GenerateIdentity()
	regBody, _ := json.Marshal(noiseRegisterRequest{
		SecurityProfile:   NoiseRelaySecurityProfile,
		ExecutorPublicKey: executor.PublicKey(),
	})
	reg := postRegister(t, srv, regBody)

	harness, _ := noise.GenerateIdentity()
	connBody, _ := json.Marshal(noiseConnectRequest{HarnessPublicKey: harness.PublicKey()})
	resp, err := http.Post(srv.URL+"/cloud/environment/env-1/connect", "application/json", bytes.NewReader(connBody))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, b)
	}
	var bundle noiseConnectResponse
	if err := json.NewDecoder(resp.Body).Decode(&bundle); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if bundle.ExecutorRegistrationID != reg.ExecutorRegistrationID {
		t.Errorf("bundle reg_id = %q want %q", bundle.ExecutorRegistrationID, reg.ExecutorRegistrationID)
	}
	if bundle.ExecutorPublicKey != executor.PublicKey() {
		t.Errorf("bundle executor pubkey mismatch")
	}
	if bundle.HarnessKeyAuthorization == "" {
		t.Errorf("empty harness_key_authorization")
	}
}

func TestNoiseConnect_NotFoundForUnknownEnv(t *testing.T) {
	h, _ := newTestNoiseHandlers(t)
	srv := httptest.NewServer(mountedRouter(h))
	defer srv.Close()

	harness, _ := noise.GenerateIdentity()
	body, _ := json.Marshal(noiseConnectRequest{HarnessPublicKey: harness.PublicKey()})
	resp, _ := http.Post(srv.URL+"/cloud/environment/no-such-env/connect", "application/json", bytes.NewReader(body))
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestNoiseValidate_RoundTrip(t *testing.T) {
	h, _ := newTestNoiseHandlers(t)
	srv := httptest.NewServer(mountedRouter(h))
	defer srv.Close()

	executor, _ := noise.GenerateIdentity()
	regBody, _ := json.Marshal(noiseRegisterRequest{
		SecurityProfile:   NoiseRelaySecurityProfile,
		ExecutorPublicKey: executor.PublicKey(),
	})
	reg := postRegister(t, srv, regBody)

	harness, _ := noise.GenerateIdentity()
	connBody, _ := json.Marshal(noiseConnectRequest{HarnessPublicKey: harness.PublicKey()})
	connResp, _ := http.Post(srv.URL+"/cloud/environment/env-1/connect", "application/json", bytes.NewReader(connBody))
	var bundle noiseConnectResponse
	_ = json.NewDecoder(connResp.Body).Decode(&bundle)

	// Replay the authorization back to /validate as the executor would.
	vBody, _ := json.Marshal(noiseValidateRequest{
		ExecutorRegistrationID:  reg.ExecutorRegistrationID,
		HarnessPublicKey:        harness.PublicKey(),
		HarnessKeyAuthorization: bundle.HarnessKeyAuthorization,
	})
	vResp, _ := http.Post(srv.URL+"/cloud/environment/env-1/validate", "application/json", bytes.NewReader(vBody))
	if vResp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(vResp.Body)
		t.Fatalf("status = %d, body = %s", vResp.StatusCode, b)
	}
	var got noiseValidateResponse
	_ = json.NewDecoder(vResp.Body).Decode(&got)
	if !got.Valid {
		t.Errorf("valid = false on faithful replay")
	}
}

func TestNoiseValidate_RejectsTamperedAuth(t *testing.T) {
	h, _ := newTestNoiseHandlers(t)
	srv := httptest.NewServer(mountedRouter(h))
	defer srv.Close()

	executor, _ := noise.GenerateIdentity()
	regBody, _ := json.Marshal(noiseRegisterRequest{
		SecurityProfile:   NoiseRelaySecurityProfile,
		ExecutorPublicKey: executor.PublicKey(),
	})
	reg := postRegister(t, srv, regBody)

	harness, _ := noise.GenerateIdentity()
	vBody, _ := json.Marshal(noiseValidateRequest{
		ExecutorRegistrationID:  reg.ExecutorRegistrationID,
		HarnessPublicKey:        harness.PublicKey(),
		HarnessKeyAuthorization: "bogus",
	})
	vResp, _ := http.Post(srv.URL+"/cloud/environment/env-1/validate", "application/json", bytes.NewReader(vBody))
	var got noiseValidateResponse
	_ = json.NewDecoder(vResp.Body).Decode(&got)
	if got.Valid {
		t.Errorf("tampered auth accepted")
	}
}

func TestNoiseValidate_RejectsWrongHarnessForAuth(t *testing.T) {
	// Auth was minted for harness A; replaying with harness B's pubkey
	// must fail because the HMAC binds the pubkey into the token.
	h, _ := newTestNoiseHandlers(t)
	srv := httptest.NewServer(mountedRouter(h))
	defer srv.Close()

	executor, _ := noise.GenerateIdentity()
	regBody, _ := json.Marshal(noiseRegisterRequest{
		SecurityProfile:   NoiseRelaySecurityProfile,
		ExecutorPublicKey: executor.PublicKey(),
	})
	postRegister(t, srv, regBody)

	harnessA, _ := noise.GenerateIdentity()
	connA, _ := json.Marshal(noiseConnectRequest{HarnessPublicKey: harnessA.PublicKey()})
	connRespA, _ := http.Post(srv.URL+"/cloud/environment/env-1/connect", "application/json", bytes.NewReader(connA))
	var bundleA noiseConnectResponse
	_ = json.NewDecoder(connRespA.Body).Decode(&bundleA)

	harnessB, _ := noise.GenerateIdentity()
	vBody, _ := json.Marshal(noiseValidateRequest{
		ExecutorRegistrationID:  bundleA.ExecutorRegistrationID,
		HarnessPublicKey:        harnessB.PublicKey(),
		HarnessKeyAuthorization: bundleA.HarnessKeyAuthorization,
	})
	vResp, _ := http.Post(srv.URL+"/cloud/environment/env-1/validate", "application/json", bytes.NewReader(vBody))
	var got noiseValidateResponse
	_ = json.NewDecoder(vResp.Body).Decode(&got)
	if got.Valid {
		t.Errorf("auth bound to harness A accepted for harness B")
	}
}

func postRegister(t *testing.T, srv *httptest.Server, body []byte) noiseRegisterResponse {
	t.Helper()
	resp, err := http.Post(srv.URL+"/cloud/environment/env-1/register", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("register status = %d, body = %s", resp.StatusCode, b)
	}
	var out noiseRegisterResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("register decode: %v", err)
	}
	return out
}
