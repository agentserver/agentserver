package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/devfixtures"
)

func TestAuthenticateSmokeCompletesCodePKCEAndRejectsReplays(t *testing.T) {
	origin, err := url.Parse("https://browser.example")
	if err != nil {
		t.Fatal(err)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	transport := &smokeOAuthRoundTripper{t: t, origin: origin}
	client := &http.Client{
		Transport: transport,
		Jar:       jar,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	token, err := authenticateSmoke(t.Context(), client, origin)
	if err != nil {
		t.Fatal(err)
	}
	if token != "dynamic-browser-access-token" || transport.callbackCalls != 2 || transport.consentCalls != 2 || transport.tokenCalls != 2 {
		t.Fatalf("OAuth smoke token/calls = %q callback=%d consent=%d token=%d", token, transport.callbackCalls, transport.consentCalls, transport.tokenCalls)
	}
}

type smokeOAuthRoundTripper struct {
	t             *testing.T
	origin        *url.URL
	browserState  string
	challenge     string
	callbackCalls int
	consentCalls  int
	tokenCalls    int
	continuations int
}

func (transport *smokeOAuthRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.t.Helper()
	if request.URL.Scheme != "https" || request.URL.Host != transport.origin.Host || request.Header.Get("Authorization") != "" {
		transport.t.Fatalf("OAuth request escaped browser authority: %s %s headers=%v", request.Method, request.URL, request.Header)
	}
	redirect := func(path string, cookies ...string) (*http.Response, error) {
		headers := http.Header{"Location": []string{transport.origin.String() + path}, "Cache-Control": []string{"no-store"}}
		for _, cookie := range cookies {
			headers.Add("Set-Cookie", cookie)
		}
		return smokeOAuthResponse(request, http.StatusFound, headers, ""), nil
	}
	switch {
	case request.Method == http.MethodGet && request.URL.Path == "/auth/config":
		if request.Header.Get("Cookie") != "" || request.URL.RawQuery != "" {
			transport.t.Fatalf("authorization config request = %s headers=%v", request.URL, request.Header)
		}
		return smokeOAuthResponse(request, http.StatusOK, http.Header{
			"Content-Type": []string{"application/json"}, "Cache-Control": []string{"no-store"},
		}, `{"version":1,"authorizationEndpoint":"/oauth2/auth","tokenEndpoint":"/oauth2/token","redirectPath":"/","clientId":"agentserver-browser","scopes":["openid","sessions:read","sessions:create","sessions:update","sessions:archive","runs:read","runs:create","runs:cancel","approvals:decide"],"audience":"agentserver-browser-api","apiOrigin":""}`), nil
	case request.Method == http.MethodGet && request.URL.Path == "/oauth2/auth" && request.URL.Query().Get("response_type") == "code":
		query := request.URL.Query()
		transport.browserState = query.Get("state")
		transport.challenge = query.Get("code_challenge")
		if !validSmokeAccessToken(transport.browserState) || query.Get("nonce") == "" ||
			query.Get("client_id") != devfixtures.BrowserOAuthClientID ||
			query.Get("scope") != strings.Join(devfixtures.BrowserAuthorizationScopes(), " ") ||
			query.Get("audience") != devfixtures.BrowserTokenAudience || query.Get("redirect_uri") != transport.origin.String()+"/" ||
			query.Get("resource") != "urn:agentserver:workspace:"+smokeWorkspaceID ||
			query.Get("code_challenge_method") != "S256" {
			transport.t.Fatalf("initial authorization query = %v", query)
		}
		return redirect("/auth/hydra/login?login_challenge=hydra-login")
	case request.Method == http.MethodGet && request.URL.Path == "/auth/hydra/login":
		return redirect(
			"/auth/idp/authorize?state="+strings.Repeat("s", 43),
			"__Host-agentserver-oidc="+strings.Repeat("b", 43)+"; Path=/; Secure; HttpOnly; SameSite=Lax",
		)
	case request.Method == http.MethodGet && request.URL.Path == "/auth/idp/authorize":
		if !strings.Contains(request.Header.Get("Cookie"), "__Host-agentserver-oidc="+strings.Repeat("b", 43)) {
			transport.t.Fatalf("development IdP request omitted browser binding cookie: %v", request.Header)
		}
		return redirect("/auth/oidc/callback?code=external-code&state=" + strings.Repeat("s", 43) + "&iss=https%3A%2F%2Fissuer.example")
	case request.Method == http.MethodGet && request.URL.Path == "/auth/oidc/callback":
		transport.callbackCalls++
		if !strings.Contains(request.Header.Get("Cookie"), "__Host-agentserver-oidc="+strings.Repeat("b", 43)) {
			transport.t.Fatalf("callback %d omitted captured browser binding: %v", transport.callbackCalls, request.Header)
		}
		cleared := "__Host-agentserver-oidc=; Path=/; Max-Age=0; Expires=Thu, 01 Jan 1970 00:00:01 GMT; Secure; HttpOnly; SameSite=Lax"
		if transport.callbackCalls == 1 {
			return redirect("/oauth2/auth?login_verifier=hydra-login-proof", cleared)
		}
		return smokeOAuthResponse(request, http.StatusBadRequest, http.Header{"Set-Cookie": []string{cleared}}, "callback replay rejected"), nil
	case request.Method == http.MethodGet && request.URL.Path == "/oauth2/auth" && request.URL.Query().Get("login_verifier") != "":
		transport.continuations++
		return redirect("/auth/hydra/consent?consent_challenge=hydra-consent")
	case request.Method == http.MethodGet && request.URL.Path == "/auth/hydra/consent":
		transport.consentCalls++
		if transport.consentCalls == 1 {
			return redirect("/oauth2/auth?consent_verifier=hydra-consent-proof")
		}
		return smokeOAuthResponse(request, http.StatusServiceUnavailable, nil, "consent replay rejected"), nil
	case request.Method == http.MethodGet && request.URL.Path == "/oauth2/auth" && request.URL.Query().Get("consent_verifier") != "":
		transport.continuations++
		return redirect("/?code=browser-code&state=" + url.QueryEscape(transport.browserState))
	case request.Method == http.MethodPost && request.URL.Path == "/oauth2/token":
		transport.tokenCalls++
		if err := request.ParseForm(); err != nil {
			transport.t.Fatal(err)
		}
		verifier := request.PostForm.Get("code_verifier")
		digest := sha256.Sum256([]byte(verifier))
		if request.PostForm.Get("code") != "browser-code" || request.PostForm.Get("client_id") != devfixtures.BrowserOAuthClientID ||
			request.PostForm.Get("redirect_uri") != transport.origin.String()+"/" || request.PostForm.Get("grant_type") != "authorization_code" ||
			base64.RawURLEncoding.EncodeToString(digest[:]) != transport.challenge {
			transport.t.Fatalf("token exchange form = %v", request.PostForm)
		}
		if transport.tokenCalls == 1 {
			return smokeOAuthResponse(request, http.StatusOK, http.Header{"Content-Type": []string{"application/json"}},
				`{"access_token":"dynamic-browser-access-token","token_type":"Bearer","expires_in":900,"scope":"openid sessions:read sessions:create sessions:update sessions:archive runs:read runs:create runs:cancel approvals:decide"}`), nil
		}
		return smokeOAuthResponse(request, http.StatusBadRequest, http.Header{"Content-Type": []string{"application/json"}}, `{"error":"invalid_grant"}`), nil
	default:
		transport.t.Fatalf("unexpected OAuth request: %s %s", request.Method, request.URL)
		return nil, nil
	}
}

func smokeOAuthResponse(request *http.Request, status int, header http.Header, body string) *http.Response {
	if header == nil {
		header = make(http.Header)
	}
	return &http.Response{
		StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body)), Request: request,
	}
}

func TestExecuteSmokeRequiresTLS13AndObservesTerminal(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet && request.URL.Path == "/" {
			if request.Header.Get("Authorization") != "" {
				t.Errorf("reference web request leaked Authorization: %v", request.Header)
			}
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			response.Header().Set("Cache-Control", "no-store")
			response.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; connect-src 'self'")
			_, _ = io.WriteString(response, `<main data-agentserver-browser-web="v2"></main>`)
			return
		}
		if request.Method != http.MethodPost || request.URL.Path != "/v2/workspaces/"+smokeWorkspaceID+"/sessions/"+smokeSessionID+"/agui" ||
			request.Header.Get("Authorization") != "Bearer test-browser-bearer" || request.Header.Get("Idempotency-Key") == "" {
			t.Errorf("smoke request = %s %s headers=%v", request.Method, request.URL.Path, request.Header)
			http.Error(response, "bad request", http.StatusBadRequest)
			return
		}
		response.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(response, "data: {\"type\":\"TEXT_MESSAGE_CONTENT\",\"delta\":\""+finalMessage+"\"}\n\n")
		_, _ = io.WriteString(response, "data: {\"type\":\"CUSTOM\",\"name\":\"a2ui.operations\",\"value\":[{\"version\":\"v0.9\",\"createSurface\":{\"surfaceId\":\"command-event-1\"}},{\"version\":\"v0.9\",\"updateDataModel\":{\"surfaceId\":\"command-event-1\",\"value\":{\"command\":\"[\\\"/bin/pwd\\\"]\",\"output\":\"/workspace\\n\",\"status\":\"succeeded (exit 0)\"}}}]}\n\n")
		_, _ = io.WriteString(response, "data: {\"type\":\"RUN_FINISHED\"}\n\n")
	}))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13}
	server.StartTLS()
	defer server.Close()

	root := t.TempDir()
	caFile := filepath.Join(root, "ca.pem")
	certificate := server.Certificate()
	if certificate == nil {
		t.Fatal("test server has no certificate")
	}
	if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	requestID, stream, err := executeSmoke(t.Context(), smokeOptions{
		origin: server.URL, caFile: caFile, accessToken: "test-browser-bearer",
	})
	if err != nil || !strings.HasPrefix(requestID, "smoke-") || len(stream) == 0 {
		t.Fatalf("executeSmoke() = id %q stream %q error %v", requestID, stream, err)
	}
	if server.TLS.MinVersion != tls.VersionTLS13 {
		t.Fatalf("test server minimum TLS = %x", server.TLS.MinVersion)
	}
	if _, err := x509.ParseCertificate(certificate.Raw); err != nil {
		t.Fatal(err)
	}
}

func TestExecuteSmokeApprovesOnlyCanonicalApprovalCommandEvent(t *testing.T) {
	const (
		approvalID = "70000000-0000-4000-8000-000000000070"
		nonce      = "71000000-0000-4000-8000-000000000071"
	)
	digest := strings.Repeat("a", 64)
	approved := make(chan struct{})
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			response.Header().Set("Cache-Control", "no-store")
			response.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; connect-src 'self'")
			_, _ = io.WriteString(response, `<main data-agentserver-browser-web="v2"></main>`)
		case request.Method == http.MethodPost && request.URL.Path == "/v2/workspaces/"+smokeWorkspaceID+"/sessions/"+smokeSessionID+"/agui":
			flusher, ok := response.(http.Flusher)
			if !ok {
				t.Error("test response does not support streaming")
				return
			}
			response.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(response, `data: {"type":"CUSTOM","name":"agentserver.approval","value":{"approvalId":"`+approvalID+`","nonce":"`+nonce+`","status":"pending","version":1,"contextDigest":{"domain":"approval-context","canonicalizerVersion":"rfc8785-v1","sha256":"`+digest+`"}}}`+"\n\n")
			flusher.Flush()
			select {
			case <-approved:
			case <-request.Context().Done():
				return
			}
			_, _ = io.WriteString(response, "data: {\"type\":\"TEXT_MESSAGE_CONTENT\",\"delta\":\""+finalMessage+"\"}\n\n")
			_, _ = io.WriteString(response, "data: {\"type\":\"CUSTOM\",\"name\":\"a2ui.operations\",\"value\":[{\"version\":\"v0.9\",\"createSurface\":{\"surfaceId\":\"command-event-1\"}},{\"version\":\"v0.9\",\"updateDataModel\":{\"surfaceId\":\"command-event-1\",\"value\":{\"command\":\"[\\\"/bin/pwd\\\"]\",\"output\":\"/workspace\\n\",\"status\":\"succeeded (exit 0)\"}}}]}\n\n")
			_, _ = io.WriteString(response, "data: {\"type\":\"RUN_FINISHED\"}\n\n")
			flusher.Flush()
		case request.Method == http.MethodPost && request.URL.Path == "/v2/workspaces/"+smokeWorkspaceID+"/approvals/"+approvalID+":decide":
			if request.Header.Get("Authorization") != "Bearer test-browser-bearer" || request.Header.Get("Content-Type") != "application/json" {
				t.Errorf("approval request headers = %v", request.Header)
				http.Error(response, "bad request", http.StatusBadRequest)
				return
			}
			var body struct {
				Decision                string `json:"decision"`
				Nonce                   string `json:"nonce"`
				ExpectedApprovalVersion int64  `json:"expectedApprovalVersion"`
				ContextDigest           struct {
					Domain               string `json:"domain"`
					CanonicalizerVersion string `json:"canonicalizerVersion"`
					SHA256               string `json:"sha256"`
				} `json:"contextDigest"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil || body.Decision != "approve" ||
				body.Nonce != nonce || body.ExpectedApprovalVersion != 1 ||
				body.ContextDigest.Domain != "approval-context" ||
				body.ContextDigest.CanonicalizerVersion != "rfc8785-v1" || body.ContextDigest.SHA256 != digest {
				t.Errorf("approval decision body = %+v, error %v", body, err)
				http.Error(response, "bad request", http.StatusBadRequest)
				return
			}
			close(approved)
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{"executionStatus":"pending_approval","approval":{"approvalId":"`+approvalID+`","nonce":"`+nonce+`","status":"approved","decision":"approve","version":2}}`)
		default:
			http.NotFound(response, request)
		}
	}))
	server.EnableHTTP2 = true
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13}
	server.StartTLS()
	defer server.Close()
	caFile := writeSmokeTestAuthority(t, server)

	requestID, stream, err := executeSmoke(t.Context(), smokeOptions{
		origin: server.URL, caFile: caFile, accessToken: "test-browser-bearer",
	})
	if err != nil || !strings.HasPrefix(requestID, "smoke-") || !strings.Contains(string(stream), approvalID) {
		t.Fatalf("executeSmoke() = id %q stream %q error %v", requestID, stream, err)
	}
}

func TestExecuteCancellationSmokeWaitsForRunningHoldAndExplicitTerminal(t *testing.T) {
	const serverRunID = "30000000-0000-4000-8000-000000000003"
	cancelled := make(chan struct{})
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/v2/workspaces/"+smokeWorkspaceID+"/sessions/"+smokeSessionID+"/agui":
			body, err := io.ReadAll(request.Body)
			if err != nil || !strings.Contains(string(body), devfixtures.CancellationHoldMarker) || request.Header.Get("Authorization") != "Bearer test-browser-bearer" {
				t.Errorf("cancellation smoke request = body %q error %v headers=%v", body, err, request.Header)
				http.Error(response, "bad request", http.StatusBadRequest)
				return
			}
			flusher, ok := response.(http.Flusher)
			if !ok {
				t.Error("test response does not support streaming")
				return
			}
			response.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(response, "data: {\"type\":\"RUN_STARTED\",\"runId\":\""+serverRunID+"\"}\n\n")
			_, _ = io.WriteString(response, "data: {\"type\":\"CUSTOM\",\"name\":\"a2ui.operations\",\"value\":[{\"version\":\"v0.9\",\"createSurface\":{\"surfaceId\":\"command-event-1\"}},{\"version\":\"v0.9\",\"updateDataModel\":{\"surfaceId\":\"command-event-1\",\"value\":{\"command\":\"[\\\"/bin/pwd\\\"]\",\"output\":\"/workspace\\n\",\"status\":\"succeeded (exit 0)\"}}}]}\n\n")
			flusher.Flush()
			select {
			case <-cancelled:
			case <-request.Context().Done():
				return
			}
			_, _ = io.WriteString(response, "data: {\"type\":\"RUN_ERROR\",\"runId\":\""+serverRunID+"\",\"code\":\"user_cancelled\",\"message\":\"cancelled\"}\n\n")
			flusher.Flush()
		case request.Method == http.MethodPost && request.URL.Path == "/v2/workspaces/"+smokeWorkspaceID+"/runs/"+serverRunID+":cancel":
			if request.ContentLength != 0 || request.URL.RawQuery != "" || request.Header.Get("Authorization") != "Bearer test-browser-bearer" {
				t.Errorf("explicit cancel request = length %d query %q headers=%v", request.ContentLength, request.URL.RawQuery, request.Header)
				http.Error(response, "bad request", http.StatusBadRequest)
				return
			}
			close(cancelled)
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{"workspaceId":"`+smokeWorkspaceID+`","sessionId":"`+smokeSessionID+`","runId":"`+serverRunID+`","status":"cancelling","runVersion":4,"terminal":false,"changed":true}`)
		default:
			http.NotFound(response, request)
		}
	}))
	server.EnableHTTP2 = true
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13}
	server.StartTLS()
	defer server.Close()
	caFile := writeSmokeTestAuthority(t, server)

	requestID, stream, err := executeCancellationSmoke(t.Context(), smokeOptions{
		origin: server.URL, caFile: caFile, accessToken: "test-browser-bearer",
	})
	if err != nil || !strings.HasPrefix(requestID, "smoke-") ||
		!strings.Contains(string(stream), `"code":"user_cancelled"`) {
		t.Fatalf("executeCancellationSmoke() = id %q stream %q error %v", requestID, stream, err)
	}
}

func TestExecuteApprovalGateSmokeContainsPreDispatchFailures(t *testing.T) {
	const (
		serverRunID = "30000000-0000-4000-8000-000000000013"
		approvalID  = "70000000-0000-4000-8000-000000000070"
		nonce       = "71000000-0000-4000-8000-000000000071"
	)
	digest := strings.Repeat("a", 64)
	tests := []struct {
		mode           approvalGateSmokeMode
		marker         string
		approvalStatus string
		decision       string
		terminal       string
	}{
		{approvalGateSmokeDeny, devfixtures.ApprovalDenyMarker, "denied", "deny", `{"type":"RUN_FINISHED","runId":"` + serverRunID + `"}`},
		{approvalGateSmokeExpiry, devfixtures.ApprovalExpiryMarker, "expired", "", `{"type":"RUN_FINISHED","runId":"` + serverRunID + `"}`},
		{approvalGateSmokePendingCancel, devfixtures.ApprovalCancelMarker, "cancelled", "", `{"type":"RUN_ERROR","runId":"` + serverRunID + `","code":"user_cancelled","message":"cancelled"}`},
	}
	for _, test := range tests {
		t.Run(string(test.mode), func(t *testing.T) {
			action := make(chan struct{})
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				switch {
				case request.Method == http.MethodPost && request.URL.Path == "/v2/workspaces/"+smokeWorkspaceID+"/sessions/"+smokeSessionID+"/agui":
					body, err := io.ReadAll(request.Body)
					if err != nil || !strings.Contains(string(body), test.marker) || request.Header.Get("Authorization") != "Bearer test-browser-bearer" {
						t.Errorf("%s request body=%q headers=%v error=%v", test.mode, body, request.Header, err)
						http.Error(response, "bad request", http.StatusBadRequest)
						return
					}
					flusher := response.(http.Flusher)
					response.Header().Set("Content-Type", "text/event-stream")
					_, _ = fmt.Fprintf(response, "data: {\"type\":\"RUN_STARTED\",\"runId\":%q}\n\n", serverRunID)
					_, _ = io.WriteString(response, testApprovalSSE(approvalID, nonce, digest, "pending", "", 1))
					flusher.Flush()
					if test.mode != approvalGateSmokeExpiry {
						select {
						case <-action:
						case <-request.Context().Done():
							return
						}
					}
					_, _ = io.WriteString(response, testApprovalSSE(approvalID, nonce, digest, test.approvalStatus, test.decision, 2))
					if test.mode != approvalGateSmokePendingCancel {
						_, _ = fmt.Fprintf(response, "data: {\"type\":\"TEXT_MESSAGE_CONTENT\",\"delta\":%q}\n\n", devfixtures.ApprovalFailureMessage)
					}
					_, _ = fmt.Fprintf(response, "data: %s\n\n", test.terminal)
					flusher.Flush()
				case request.Method == http.MethodPost && request.URL.Path == "/v2/workspaces/"+smokeWorkspaceID+"/approvals/"+approvalID+":decide":
					if test.mode != approvalGateSmokeDeny {
						t.Errorf("unexpected decision request for %s", test.mode)
						http.Error(response, "unexpected", http.StatusBadRequest)
						return
					}
					var input struct {
						Decision string `json:"decision"`
					}
					if err := json.NewDecoder(request.Body).Decode(&input); err != nil || input.Decision != smokeApprovalDeny {
						t.Errorf("deny input = %+v, %v", input, err)
						http.Error(response, "bad decision", http.StatusBadRequest)
						return
					}
					close(action)
					response.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(response, `{"executionStatus":"denied","approval":{"approvalId":"`+approvalID+`","nonce":"`+nonce+`","status":"denied","decision":"deny","version":2}}`)
				case request.Method == http.MethodPost && request.URL.Path == "/v2/workspaces/"+smokeWorkspaceID+"/runs/"+serverRunID+":cancel":
					if test.mode != approvalGateSmokePendingCancel {
						t.Errorf("unexpected cancellation request for %s", test.mode)
						http.Error(response, "unexpected", http.StatusBadRequest)
						return
					}
					close(action)
					response.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(response, `{"workspaceId":"`+smokeWorkspaceID+`","sessionId":"`+smokeSessionID+`","runId":"`+serverRunID+`","status":"cancelling","runVersion":4,"terminal":false,"changed":true}`)
				default:
					http.NotFound(response, request)
				}
			}))
			server.EnableHTTP2 = true
			server.TLS = &tls.Config{MinVersion: tls.VersionTLS13}
			server.StartTLS()
			defer server.Close()
			caFile := writeSmokeTestAuthority(t, server)

			result, stream, err := executeApprovalGateSmoke(t.Context(), smokeOptions{
				origin: server.URL, caFile: caFile, accessToken: "test-browser-bearer",
			}, test.mode)
			if err != nil || result.RunID != serverRunID || result.ApprovalID != approvalID || len(stream) == 0 {
				t.Fatalf("executeApprovalGateSmoke(%s) = %+v stream %q error %v", test.mode, result, stream, err)
			}
		})
	}
}

func testApprovalSSE(approvalID, nonce, digest, status, decision string, version int64) string {
	return fmt.Sprintf(
		"data: {\"type\":\"CUSTOM\",\"name\":\"agentserver.approval\",\"value\":{\"approvalId\":%q,\"nonce\":%q,\"status\":%q,\"decision\":%q,\"version\":%d,\"contextDigest\":{\"domain\":\"approval-context\",\"canonicalizerVersion\":\"rfc8785-v1\",\"sha256\":%q}}}\n\n",
		approvalID, nonce, status, decision, version, digest,
	)
}

func TestInspectSSERejectsMalformedDataAndReportsMissingPieces(t *testing.T) {
	if _, _, _, err := inspectSSE([]byte("data: not-json\n")); err == nil {
		t.Fatal("malformed SSE data accepted")
	}
	terminal, scripted, commandSurface, err := inspectSSE([]byte("data: {\"type\":\"RUN_FINISHED\"}\n"))
	if err != nil || !terminal || scripted || commandSurface {
		t.Fatalf("inspectSSE terminal = %v scripted = %v commandSurface = %v error = %v", terminal, scripted, commandSurface, err)
	}
}

func TestRunRejectsIncompleteArguments(t *testing.T) {
	var stderr strings.Builder
	if exitCode := run(context.Background(), []string{"--origin=https://127.0.0.1:17444"}, io.Discard, &stderr); exitCode != 1 ||
		!strings.Contains(stderr.String(), "CA file is required") {
		t.Fatalf("run() = %d stderr=%q", exitCode, stderr.String())
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if exitCode := run(cancelled, nil, io.Discard, io.Discard); exitCode != 1 || time.Since(start) > time.Second {
		t.Fatalf("cancelled run() = %d after %s", exitCode, time.Since(start))
	}
}

func writeSmokeTestAuthority(t *testing.T, server *httptest.Server) string {
	t.Helper()
	root := t.TempDir()
	caFile := filepath.Join(root, "ca.pem")
	certificate := server.Certificate()
	if certificate == nil {
		t.Fatal("test server has no certificate")
	}
	if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	return caFile
}
