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
)

// TestServer_NoiseRoutesGatedByConfig verifies that the noise relay
// endpoints are off by default and only mount when
// CXG_NOISE_RELAY_HMAC_KEY (Config.NoiseRelayHMACKey) is populated.
// The legacy /cloud/environment/{env_id}/register handler must stay
// reachable in the off case so existing executors keep working.
func TestServer_NoiseRoutesGatedByConfig(t *testing.T) {
	t.Run("disabled by default", func(t *testing.T) {
		store := newTestStore(t)
		clearNoiseRegistrations(t, store)
		srv, err := NewServer(Config{
			CapTokenHMACSecret:   []byte("k"),
			InternalSharedSecret: "s",
		}, store)
		if err != nil {
			t.Fatalf("NewServer: %v", err)
		}
		if srv.noiseHandlers != nil {
			t.Fatalf("noise handlers should be nil when key not set")
		}
		// Noise-only endpoints (no legacy fallback) return 405/404, NOT
		// the noise JSON shape.
		body := `{"harness_public_key":{"suite":"x","x25519_public_key":"","mlkem768_public_key":""}}`
		resp := serve(t, srv, http.MethodPost, "/cloud/environment/test/connect", strings.NewReader(body))
		if resp.Code == http.StatusOK {
			t.Errorf("noise /connect should not be mounted when disabled; got 200")
		}
	})

	t.Run("enabled when HMAC key set", func(t *testing.T) {
		store := newTestStore(t)
		clearNoiseRegistrations(t, store)
		srv, err := NewServer(Config{
			CapTokenHMACSecret:   []byte("k"),
			InternalSharedSecret: "s",
			NoiseRelayHMACKey:    []byte("hmac-key-32-bytes-aaaaaaaaaaaaaa"),
		}, store)
		if err != nil {
			t.Fatalf("NewServer: %v", err)
		}
		if srv.noiseHandlers == nil {
			t.Fatalf("noise handlers should be set when key configured")
		}

		// /register accepts noise registration end-to-end.
		execID, err := noise.GenerateIdentity()
		if err != nil {
			t.Fatalf("identity: %v", err)
		}
		regBody, _ := json.Marshal(noiseRegisterRequest{
			SecurityProfile:   NoiseRelaySecurityProfile,
			ExecutorPublicKey: execID.PublicKey(),
		})
		resp := serve(t, srv, http.MethodPost, "/cloud/environment/wire-test-env/register", bytes.NewReader(regBody))
		if resp.Code != http.StatusOK {
			t.Fatalf("/register status = %d, body = %s", resp.Code, resp.Body)
		}
		var reg noiseRegisterResponse
		if err := json.NewDecoder(resp.Body).Decode(&reg); err != nil {
			t.Fatalf("decode register response: %v", err)
		}
		if reg.SecurityProfile != NoiseRelaySecurityProfile {
			t.Errorf("response security_profile = %q", reg.SecurityProfile)
		}
		if !strings.HasPrefix(reg.ExecutorRegistrationID, "exr_") {
			t.Errorf("registration_id shape = %q", reg.ExecutorRegistrationID)
		}

		// /connect follows up with a harness pubkey and gets a bundle.
		harnessID, _ := noise.GenerateIdentity()
		connBody, _ := json.Marshal(noiseConnectRequest{HarnessPublicKey: harnessID.PublicKey()})
		resp = serve(t, srv, http.MethodPost, "/cloud/environment/wire-test-env/connect", bytes.NewReader(connBody))
		if resp.Code != http.StatusOK {
			t.Fatalf("/connect status = %d, body = %s", resp.Code, resp.Body)
		}
		var bundle noiseConnectResponse
		_ = json.NewDecoder(resp.Body).Decode(&bundle)
		if bundle.HarnessKeyAuthorization == "" {
			t.Errorf("empty harness_key_authorization in connect response")
		}
	})
}

func serve(t *testing.T, srv *Server, method, path string, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, body)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)
	return rr
}
