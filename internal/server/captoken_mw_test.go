package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

// mintTestCapToken returns a wire-compatible token for the test
// verifier. Matches the bytes captoken.Mint produces for the given
// payload + secret (header is always {"alg":"HS256","typ":"CXG"} per
// the spec).
func mintTestCapToken(t *testing.T, secret []byte, p capTokenPayload) string {
	t.Helper()
	header := []byte(`{"alg":"HS256","typ":"CXG"}`)
	pj, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	enc := base64.RawURLEncoding
	signingInput := enc.EncodeToString(header) + "." + enc.EncodeToString(pj)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(signingInput))
	return signingInput + "." + enc.EncodeToString(mac.Sum(nil))
}

func okHandler(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func runWithMW(secret []byte, urlWID, headerVal string) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	mw := requireCapTokenWithMatchingWID(func() []byte { return secret }, nil)
	r.Post("/api/internal/workspaces/{wid}/scheduled-tasks", mw(okHandler))

	req := httptest.NewRequest(http.MethodPost, "/api/internal/workspaces/"+urlWID+"/scheduled-tasks", nil)
	if headerVal != "" {
		req.Header.Set("Authorization", headerVal)
	}
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	return rr
}

func TestCapTokenMW_NoSecretConfigured(t *testing.T) {
	rr := runWithMW(nil, "ws_a", "Bearer anything")
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503 with no secret, got %d", rr.Code)
	}
}

func TestCapTokenMW_NoBearer(t *testing.T) {
	rr := runWithMW([]byte("secret"), "ws_a", "")
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 with no bearer, got %d", rr.Code)
	}
}

func TestCapTokenMW_BadSignature(t *testing.T) {
	now := time.Now().Unix()
	tok := mintTestCapToken(t, []byte("wrong-secret"), capTokenPayload{
		TurnID: "trn_x", WorkspaceID: "ws_a", IAT: now, EXP: now + 300,
	})
	rr := runWithMW([]byte("real-secret"), "ws_a", "Bearer "+tok)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 for bad signature, got %d", rr.Code)
	}
}

func TestCapTokenMW_Expired(t *testing.T) {
	now := time.Now().Unix()
	secret := []byte("real-secret")
	tok := mintTestCapToken(t, secret, capTokenPayload{
		TurnID: "trn_x", WorkspaceID: "ws_a", IAT: now - 600, EXP: now - 1,
	})
	rr := runWithMW(secret, "ws_a", "Bearer "+tok)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 for expired token, got %d", rr.Code)
	}
}

func TestCapTokenMW_WIDMatch(t *testing.T) {
	now := time.Now().Unix()
	secret := []byte("real-secret")
	tok := mintTestCapToken(t, secret, capTokenPayload{
		TurnID: "trn_x", WorkspaceID: "ws_a", IAT: now, EXP: now + 300,
	})
	rr := runWithMW(secret, "ws_a", "Bearer "+tok)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200 for matching wid, got %d (body=%q)", rr.Code, rr.Body.String())
	}
}

func TestCapTokenMW_WIDMismatch(t *testing.T) {
	// Token says ws_a, URL says ws_b — must 403 (the defense-in-depth
	// check, since handlers downstream read wid from the URL).
	now := time.Now().Unix()
	secret := []byte("real-secret")
	tok := mintTestCapToken(t, secret, capTokenPayload{
		TurnID: "trn_x", WorkspaceID: "ws_a", IAT: now, EXP: now + 300,
	})
	rr := runWithMW(secret, "ws_b", "Bearer "+tok)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("want 403 for wid mismatch, got %d (body=%q)", rr.Code, rr.Body.String())
	}
}

func TestCapTokenMW_MalformedToken(t *testing.T) {
	rr := runWithMW([]byte("secret"), "ws_a", "Bearer not.a.real.token.shape")
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 for malformed token, got %d", rr.Code)
	}
}
