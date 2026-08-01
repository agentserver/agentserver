package devfixtures

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/agentserver/agentserver/v2/internal/braincatalog"
	"github.com/agentserver/agentserver/v2/internal/runcapability"
)

const (
	maximumHydraRequestBytes = int64(32 * 1024)
	maximumModelRequestBytes = int64(4 * 1024 * 1024)
	maximumHeaderBytes       = 32 * 1024
	maximumScriptSessions    = 4096
	fixtureShutdownTimeout   = 5 * time.Second
)

type scriptKey struct {
	capabilityID string
	runID        string
	attemptID    string
	generation   int64
}

type scriptSession struct {
	step                  int
	expiresAtMS           int64
	cancelHold            bool
	acceptApprovalFailure bool
}

type fixtureRuntime struct {
	bundle *Bundle
	now    func() time.Time

	mu          sync.Mutex
	sessions    map[scriptKey]scriptSession
	holdEntered chan struct{}

	authMu             sync.Mutex
	hydraLogins        map[string]hydraLoginFixture
	hydraLoginProofs   map[string]string
	hydraConsents      map[string]hydraConsentFixture
	hydraConsentProofs map[string]string
	idpCodes           map[string]idpCodeFixture
	hydraCodes         map[string]hydraCodeFixture
	accessTokens       map[string]accessTokenFixture
}

type modelRequest struct {
	model string
	input []any
	tools []any
}

type scriptedModelResponse struct {
	body []byte
	hold bool
}

func ServeBundle(ctx context.Context, bundleDirectory string, stdout io.Writer) error {
	bundle, err := LoadBundle(bundleDirectory)
	if err != nil {
		return err
	}
	defer bundle.Close()
	return bundle.Serve(ctx, stdout)
}

func (bundle *Bundle) Close() {
	if bundle != nil {
		clear(bundle.browserToken)
		bundle.browserToken = nil
		clear(bundle.externalOIDCClientSecret)
		bundle.externalOIDCClientSecret = nil
	}
}

func (bundle *Bundle) Serve(ctx context.Context, stdout io.Writer) error {
	if ctx == nil {
		return errors.New("development fixture context is required")
	}
	if bundle == nil || bundle.hydraEndpoint == nil || bundle.llmEndpoint == nil || bundle.codec == nil ||
		len(bundle.browserToken) == 0 || len(bundle.externalOIDCClientSecret) == 0 {
		return errors.New("development fixture bundle is not initialized")
	}
	if stdout == nil {
		stdout = io.Discard
	}
	runtime := &fixtureRuntime{bundle: bundle, now: time.Now, sessions: make(map[scriptKey]scriptSession)}
	hydraListener, err := net.Listen("tcp", bundle.hydraListen)
	if err != nil {
		return fmt.Errorf("listen for development Hydra fixture: %w", err)
	}
	defer hydraListener.Close()
	llmListener, err := net.Listen("tcp", bundle.llmListen)
	if err != nil {
		return fmt.Errorf("listen for development llmproxy fixture: %w", err)
	}
	defer llmListener.Close()

	hydraServer := newHTTPServer(http.HandlerFunc(runtime.serveHydra))
	llmServer := newHTTPServer(http.HandlerFunc(runtime.serveLLMProxy))
	llmServer.TLSConfig = &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{bundle.tlsIdentity},
		NextProtos:   []string{"http/1.1"},
	}
	tlsListener := tls.NewListener(llmListener, llmServer.TLSConfig)

	type serveResult struct {
		name string
		err  error
	}
	results := make(chan serveResult, 2)
	go func() { results <- serveResult{name: "Hydra", err: hydraServer.Serve(hydraListener)} }()
	go func() { results <- serveResult{name: "llmproxy", err: llmServer.Serve(tlsListener)} }()
	fmt.Fprintf(
		stdout,
		"agentserver-dev fixtures: INSECURE DEV Hydra %s; scripted llmproxy %s/responses\n",
		bundle.hydraEndpoint.String(), bundle.llmEndpoint.String(),
	)

	var first serveResult
	select {
	case <-ctx.Done():
	case first = <-results:
	}
	shutdownContext, cancel := context.WithTimeout(context.Background(), fixtureShutdownTimeout)
	defer cancel()
	shutdownErr := errors.Join(hydraServer.Shutdown(shutdownContext), llmServer.Shutdown(shutdownContext))
	if shutdownErr != nil {
		_ = hydraServer.Close()
		_ = llmServer.Close()
	}

	observed := 0
	if first.name != "" {
		observed = 1
	}
	for observed < 2 {
		result := <-results
		observed++
		if first.name == "" || (isExpectedServeError(first.err) && !isExpectedServeError(result.err)) {
			first = result
		}
	}
	if !isExpectedServeError(first.err) {
		return fmt.Errorf("serve development %s fixture: %w", first.name, first.err)
	}
	if shutdownErr != nil && !errors.Is(shutdownErr, context.Canceled) {
		return fmt.Errorf("shut down development fixtures: %w", shutdownErr)
	}
	return nil
}

func newHTTPServer(handler http.Handler) *http.Server {
	return &http.Server{
		Handler: handler, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 10 * time.Second,
		WriteTimeout: 10 * time.Second, IdleTimeout: 15 * time.Second, MaxHeaderBytes: maximumHeaderBytes,
	}
}

func isExpectedServeError(err error) bool {
	return err == nil || errors.Is(err, http.ErrServerClosed) || errors.Is(err, net.ErrClosed)
}

func (runtime *fixtureRuntime) serveHydra(writer http.ResponseWriter, request *http.Request) {
	endpoint := runtime.bundle.hydraEndpoint
	if request.Host != endpoint.Host || request.URL.RawPath != "" {
		writeFixtureError(writer, http.StatusNotFound, "development authorization fixture request rejected")
		return
	}
	switch request.URL.Path {
	case endpoint.Path:
		runtime.serveHydraIntrospection(writer, request)
	case "/oauth2/auth":
		runtime.serveHydraAuthorization(writer, request)
	case "/oauth2/token":
		runtime.serveHydraToken(writer, request)
	case "/admin/oauth2/auth/requests/login":
		runtime.serveHydraAdminLogin(writer, request)
	case "/admin/oauth2/auth/requests/login/accept":
		runtime.serveHydraAdminLoginAccept(writer, request)
	case "/admin/oauth2/auth/requests/login/reject":
		runtime.serveHydraAdminLoginReject(writer, request)
	case "/admin/oauth2/auth/requests/consent":
		runtime.serveHydraAdminConsent(writer, request)
	case "/admin/oauth2/auth/requests/consent/accept":
		runtime.serveHydraAdminConsentAccept(writer, request)
	case "/admin/oauth2/auth/requests/consent/reject":
		runtime.serveHydraAdminConsentReject(writer, request)
	case "/idp/.well-known/openid-configuration":
		runtime.serveExternalOIDCDiscovery(writer, request)
	case "/idp/authorize":
		runtime.serveExternalOIDCAuthorization(writer, request)
	case "/idp/token":
		runtime.serveExternalOIDCToken(writer, request)
	case "/idp/jwks":
		runtime.serveExternalOIDCJWKS(writer, request)
	default:
		writeFixtureError(writer, http.StatusNotFound, "development authorization fixture request rejected")
	}
}

func (runtime *fixtureRuntime) serveHydraIntrospection(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeFixtureError(writer, http.StatusMethodNotAllowed, "development introspection request rejected")
		return
	}
	if request.URL.RawQuery != "" {
		writeFixtureError(writer, http.StatusNotFound, "development introspection request rejected")
		return
	}
	if !exactHeader(request.Header, "Content-Type", "application/x-www-form-urlencoded") ||
		!exactHeader(request.Header, "Accept", "application/json") || request.Header.Get("Content-Encoding") != "" ||
		len(request.TransferEncoding) != 0 || request.ContentLength < 0 || request.ContentLength > maximumHydraRequestBytes {
		writeFixtureError(writer, http.StatusUnsupportedMediaType, "development introspection request rejected")
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maximumHydraRequestBytes)
	body, err := io.ReadAll(request.Body)
	if err != nil || int64(len(body)) != request.ContentLength {
		writeFixtureError(writer, http.StatusBadRequest, "development introspection request rejected")
		return
	}
	form, err := url.ParseQuery(string(body))
	canonicalForm := ""
	if len(form["token"]) == 1 {
		canonicalForm = (url.Values{"token": []string{form["token"][0]}}).Encode()
	}
	if err != nil || len(form) != 1 || len(form["token"]) != 1 || form["token"][0] == "" || len(form["token"][0]) > 4096 ||
		canonicalForm != string(body) {
		writeFixtureError(writer, http.StatusBadRequest, "development introspection request rejected")
		return
	}
	now := runtime.now().UTC()
	if now.IsZero() {
		writeFixtureError(writer, http.StatusServiceUnavailable, "development introspection unavailable")
		return
	}
	if exactTokenEqual(runtime.bundle.browserToken, form["token"][0]) {
		writeJSON(writer, http.StatusOK, map[string]any{
			"active": true, "sub": runtime.bundle.document.Authority.ActorID,
			"aud": []string{runtime.bundle.document.Hydra.Audience}, "scope": runtime.bundle.document.Hydra.Scope,
			"exp": now.Add(runtime.bundle.responseTTL).Unix(),
		})
		return
	}
	access, active := runtime.lookupAccessToken(form["token"][0], now)
	if !active {
		writeJSON(writer, http.StatusOK, map[string]any{"active": false})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"active": true, "sub": access.subject, "aud": append([]string(nil), access.audience...),
		"scope": strings.Join(access.scopes, " "), "exp": access.expiresAt.Unix(),
	})
}

func (runtime *fixtureRuntime) serveLLMProxy(writer http.ResponseWriter, request *http.Request) {
	endpoint := runtime.bundle.llmEndpoint
	if request.TLS == nil {
		writeFixtureError(writer, http.StatusBadRequest, "scripted model request rejected")
		return
	}
	if request.Method != http.MethodPost {
		writeFixtureError(writer, http.StatusMethodNotAllowed, "scripted model request rejected")
		return
	}
	if request.Host != endpoint.Host || request.URL.Path != endpoint.Path+"/responses" || request.URL.RawPath != "" || request.URL.RawQuery != "" {
		writeFixtureError(writer, http.StatusNotFound, "scripted model request rejected")
		return
	}
	mediaType, parameters, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" || len(parameters) != 0 || len(request.Header.Values("Content-Type")) != 1 ||
		(request.Header.Get("Content-Encoding") != "" && request.Header.Get("Content-Encoding") != "identity") {
		writeFixtureError(writer, http.StatusUnsupportedMediaType, "scripted model request rejected")
		return
	}
	token, err := exactBearer(request.Header)
	if err != nil {
		writer.Header().Set("WWW-Authenticate", `Bearer realm="llmproxy"`)
		writeFixtureError(writer, http.StatusUnauthorized, "scripted model request rejected")
		return
	}
	now := runtime.now().UTC()
	claims, err := runtime.bundle.codec.Verify(token, runcapability.AudienceLLMProxy, now)
	if err != nil {
		writer.Header().Set("WWW-Authenticate", `Bearer realm="llmproxy"`)
		writeFixtureError(writer, http.StatusUnauthorized, "scripted model request rejected")
		return
	}
	document := runtime.bundle.document
	if claims.WorkspaceID != document.Authority.WorkspaceID || claims.SessionID != document.Authority.SessionID ||
		claims.ActorID != document.Authority.ActorID || claims.Model != document.LLMProxy.Model || claims.Provider != document.LLMProxy.Provider {
		writeFixtureError(writer, http.StatusForbidden, "scripted model route rejected")
		return
	}
	modelRequest, err := readModelRequest(writer, request)
	if err != nil {
		writeFixtureError(writer, http.StatusBadRequest, "scripted model request rejected")
		return
	}
	if modelRequest.model != claims.Model {
		writeFixtureError(writer, http.StatusForbidden, "scripted model route rejected")
		return
	}
	response, err := runtime.nextResponse(claims, modelRequest, now)
	if err != nil {
		writeFixtureError(writer, http.StatusConflict, "scripted model sequence rejected")
		return
	}
	if response.hold {
		if runtime.holdEntered != nil {
			select {
			case runtime.holdEntered <- struct{}{}:
			default:
			}
		}
		<-request.Context().Done()
		return
	}
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(response.body)
}

func readModelRequest(writer http.ResponseWriter, request *http.Request) (modelRequest, error) {
	request.Body = http.MaxBytesReader(writer, request.Body, maximumModelRequestBytes)
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return modelRequest{}, err
	}
	limits := braincatalog.DefaultLimits()
	limits.MaxJSONValues = 262_144
	limits.MaxJSONDepth = 128
	value, _, err := braincatalog.DecodeCanonicalJSON(body, int(maximumModelRequestBytes), limits)
	if err != nil {
		return modelRequest{}, err
	}
	object, ok := value.(map[string]any)
	if !ok {
		return modelRequest{}, errors.New("Responses request root is not an object")
	}
	model, ok := object["model"].(string)
	if !ok || !canonicalText(model, 256) {
		return modelRequest{}, errors.New("Responses request model is invalid")
	}
	stream, ok := object["stream"].(bool)
	if !ok || !stream {
		return modelRequest{}, errors.New("Responses request must enable streaming")
	}
	input, ok := object["input"].([]any)
	if !ok {
		return modelRequest{}, errors.New("Responses request input is missing")
	}
	tools, ok := object["tools"].([]any)
	if !ok {
		return modelRequest{}, errors.New("Responses request tools are missing")
	}
	return modelRequest{model: model, input: input, tools: tools}, nil
}

func (runtime *fixtureRuntime) nextResponse(claims runcapability.Claims, request modelRequest, now time.Time) (scriptedModelResponse, error) {
	key := scriptKey{
		capabilityID: claims.CapabilityID, runID: claims.RunID,
		attemptID: claims.RunAttemptID, generation: claims.RunAttemptGeneration,
	}
	fragment := scriptFragment(key)
	listCallID := "call-dev-list-" + fragment
	shellCallID := "call-dev-shell-" + fragment
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	runtime.pruneExpiredLocked(now.UnixMilli())
	session, found := runtime.sessions[key]
	if !found {
		if len(runtime.sessions) >= maximumScriptSessions {
			return scriptedModelResponse{}, errors.New("scripted model session bound reached")
		}
		session = scriptSession{expiresAtMS: claims.ExpiresAtUnixMS}
	}
	if session.expiresAtMS != claims.ExpiresAtUnixMS {
		return scriptedModelResponse{}, errors.New("scripted model capability identity changed authority")
	}
	switch session.step {
	case 0:
		if countNamespacedTool(request.tools, runtime.bundle.document.LLMProxy.ToolNamespace, runtime.bundle.document.LLMProxy.ScriptedTool) != 1 ||
			countNamespacedTool(request.tools, runtime.bundle.document.LLMProxy.ToolNamespace, ScriptedShellName) != 1 {
			return scriptedModelResponse{}, errors.New("scripted list_environments and shell tools are not both present exactly once")
		}
		response, err := namespacedFunctionCall(
			"response-dev-list-"+fragment, listCallID,
			runtime.bundle.document.LLMProxy.ToolNamespace,
			runtime.bundle.document.LLMProxy.ScriptedTool, `{}`,
		)
		if err != nil {
			return scriptedModelResponse{}, err
		}
		session.step = 1
		// A resumed stock thread includes prior turns in the Responses input.
		// Scenario markers belong only to the newest user message; scanning the
		// full input would make deny/expiry/cancel behavior leak into every later
		// run resumed from that checkpoint.
		session.cancelHold = latestUserMessageContainsMarker(request.input, CancellationHoldMarker)
		session.acceptApprovalFailure = latestUserMessageContainsMarker(request.input, ApprovalDenyMarker) ||
			latestUserMessageContainsMarker(request.input, ApprovalExpiryMarker)
		runtime.sessions[key] = session
		return scriptedModelResponse{body: response}, nil
	case 1:
		environmentID, err := environmentIDFromFunctionOutput(request.input, listCallID)
		if err != nil {
			return scriptedModelResponse{}, fmt.Errorf("validate scripted list_environments result: %w", err)
		}
		arguments, err := json.Marshal(struct {
			EnvironmentID string   `json:"environment_id"`
			Argv          []string `json:"argv"`
			TimeoutMS     int      `json:"timeout_ms"`
		}{EnvironmentID: environmentID, Argv: []string{"/bin/pwd"}, TimeoutMS: 10_000})
		if err != nil {
			return scriptedModelResponse{}, fmt.Errorf("encode scripted shell arguments: %w", err)
		}
		response, err := namespacedFunctionCall(
			"response-dev-shell-"+fragment, shellCallID,
			runtime.bundle.document.LLMProxy.ToolNamespace,
			ScriptedShellName, string(arguments),
		)
		if err != nil {
			return scriptedModelResponse{}, err
		}
		session.step = 2
		runtime.sessions[key] = session
		return scriptedModelResponse{body: response}, nil
	case 2:
		if session.acceptApprovalFailure {
			if err := validateUnsuccessfulShellFunctionOutput(request.input, shellCallID); err != nil {
				return scriptedModelResponse{}, fmt.Errorf("validate scripted approval failure: %w", err)
			}
		} else if err := validateSuccessfulShellFunctionOutput(request.input, shellCallID); err != nil {
			return scriptedModelResponse{}, fmt.Errorf("validate scripted shell result: %w", err)
		}
		session.step = 3
		runtime.sessions[key] = session
		if session.cancelHold {
			return scriptedModelResponse{hold: true}, nil
		}
		message := runtime.bundle.document.LLMProxy.FinalMessage
		if session.acceptApprovalFailure {
			message = ApprovalFailureMessage
		}
		response, err := assistantMessage(
			"response-dev-final-"+fragment, "message-dev-final-"+fragment,
			message,
		)
		if err != nil {
			return scriptedModelResponse{}, err
		}
		return scriptedModelResponse{body: response}, nil
	default:
		return scriptedModelResponse{}, errors.New("scripted model sequence is exhausted")
	}
}

func latestUserMessageContainsMarker(input []any, marker string) bool {
	if marker == "" {
		return false
	}
	for index := len(input) - 1; index >= 0; index-- {
		item, ok := input[index].(map[string]any)
		if !ok || item["role"] != "user" {
			continue
		}
		return containsMarker(item["content"], marker)
	}
	return false
}

func containsMarker(value any, marker string) bool {
	if marker == "" {
		return false
	}
	switch typed := value.(type) {
	case string:
		return strings.Contains(typed, marker)
	case []any:
		for _, child := range typed {
			if containsMarker(child, marker) {
				return true
			}
		}
	case map[string]any:
		for _, child := range typed {
			if containsMarker(child, marker) {
				return true
			}
		}
	}
	return false
}

func environmentIDFromFunctionOutput(input []any, callID string) (string, error) {
	output, err := functionOutputText(input, callID)
	if err != nil {
		return "", err
	}
	var result struct {
		Environments []struct {
			EnvironmentID string `json:"environment_id"`
		} `json:"environments"`
	}
	decoder := json.NewDecoder(strings.NewReader(output))
	if err := decoder.Decode(&result); err != nil {
		return "", errors.New("scripted function output is not environment JSON")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", errors.New("scripted function output contains trailing JSON")
	}
	if len(result.Environments) != 1 || !uuidPattern.MatchString(result.Environments[0].EnvironmentID) ||
		result.Environments[0].EnvironmentID == "00000000-0000-0000-0000-000000000000" {
		return "", errors.New("scripted function output must contain exactly one canonical environment")
	}
	return result.Environments[0].EnvironmentID, nil
}

func validateSuccessfulShellFunctionOutput(input []any, callID string) error {
	output, err := functionOutputText(input, callID)
	if err != nil {
		return err
	}
	var result struct {
		Status         string `json:"status"`
		ExitCode       *int   `json:"exit_code"`
		SandboxDenied  bool   `json:"sandbox_denied"`
		TimedOut       bool   `json:"timed_out"`
		OutputComplete bool   `json:"output_complete"`
	}
	decoder := json.NewDecoder(strings.NewReader(output))
	if err := decoder.Decode(&result); err != nil {
		return errors.New("scripted shell output is not result JSON")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("scripted shell output contains trailing JSON")
	}
	if result.Status != "succeeded" || result.ExitCode == nil || *result.ExitCode != 0 ||
		result.SandboxDenied || result.TimedOut || !result.OutputComplete {
		return errors.New("scripted /bin/pwd execution did not complete successfully")
	}
	return nil
}

func validateUnsuccessfulShellFunctionOutput(input []any, callID string) error {
	output, err := functionOutputText(input, callID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(output) == "" {
		return errors.New("scripted approval failure output is empty")
	}
	if validateSuccessfulShellFunctionOutput(input, callID) == nil {
		return errors.New("scripted approval failure unexpectedly contains a successful shell result")
	}
	return nil
}

func functionOutputText(input []any, callID string) (string, error) {
	if callID == "" {
		return "", errors.New("scripted call ID is required")
	}
	var output string
	count := 0
	for _, rawItem := range input {
		item, ok := rawItem.(map[string]any)
		if !ok || item["type"] != "function_call_output" || item["call_id"] != callID {
			continue
		}
		value, ok := item["output"].(string)
		if !ok || len(value) == 0 || len(value) > 1024*1024 {
			return "", errors.New("scripted function output must be bounded JSON text")
		}
		output = value
		count++
	}
	if count != 1 {
		return "", errors.New("scripted function output is missing or ambiguous")
	}
	return output, nil
}

func (runtime *fixtureRuntime) pruneExpiredLocked(nowMS int64) {
	for key, session := range runtime.sessions {
		if session.expiresAtMS <= nowMS {
			delete(runtime.sessions, key)
		}
	}
}

func countNamespacedTool(tools []any, namespace, name string) int {
	count := 0
	for _, rawTool := range tools {
		tool, ok := rawTool.(map[string]any)
		if !ok || tool["type"] != "namespace" || tool["name"] != namespace {
			continue
		}
		children, ok := tool["tools"].([]any)
		if !ok {
			continue
		}
		for _, rawChild := range children {
			child, ok := rawChild.(map[string]any)
			if ok && child["name"] == name {
				count++
			}
		}
	}
	return count
}

func scriptFragment(key scriptKey) string {
	hasher := sha256.New()
	_, _ = io.WriteString(hasher, "agentserver-v2/insecure-dev-script-session-v1\x00")
	_, _ = io.WriteString(hasher, key.capabilityID)
	_, _ = io.WriteString(hasher, "\x00"+key.runID+"\x00"+key.attemptID+"\x00")
	_, _ = fmt.Fprintf(hasher, "%d", key.generation)
	return hex.EncodeToString(hasher.Sum(nil))[:20]
}

func exactHeader(header http.Header, name, value string) bool {
	values := header.Values(name)
	return len(values) == 1 && values[0] == value
}

func exactBearer(header http.Header) (string, error) {
	values := header.Values("Authorization")
	if len(values) != 1 || strings.Contains(values[0], ",") || !strings.HasPrefix(values[0], "Bearer ") {
		return "", errors.New("exact bearer is required")
	}
	token := strings.TrimPrefix(values[0], "Bearer ")
	if token == "" || strings.TrimSpace(token) != token || strings.ContainsAny(token, "\r\n\x00") {
		return "", errors.New("exact bearer is invalid")
	}
	return token, nil
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	var body bytes.Buffer
	encoder := json.NewEncoder(&body)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		writeFixtureError(writer, http.StatusInternalServerError, "development fixture response unavailable")
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(status)
	_, _ = writer.Write(body.Bytes())
}

func writeFixtureError(writer http.ResponseWriter, status int, message string) {
	writeJSON(writer, status, map[string]string{"error": message})
}
