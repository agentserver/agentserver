package executorgateway

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/bkectlpolicy"
	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/egresscapability"
	"github.com/agentserver/agentserver/v2/internal/executionbackend"
	"github.com/agentserver/agentserver/v2/internal/larkegresspolicy"
	"github.com/agentserver/agentserver/v2/internal/managedcredential"
	"github.com/agentserver/agentserver/v2/internal/sandboxcontract"
	"github.com/agentserver/agentserver/v2/internal/sandboxgateway"
	"github.com/agentserver/agentserver/v2/internal/sandboxgateway/fakeprovider"
)

func TestManagedShellLarkCLIThroughTAEHTTPAndSandboxGateway(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	environment := testManagedEnvironment(t)
	principal := testExecutorMCPPrincipal("managed-http-e2e")
	providerSessionRef := "tae-provider-session-managed-http-e2e"
	expiresAt := now.Add(time.Hour)
	core := &managedHTTPE2ECore{state: corecontract.ManagedSandboxState{
		SandboxID: environment.Target.ID, WorkspaceID: principal.WorkspaceID, SessionID: principal.SessionID,
		EnvironmentID: environment.EnvironmentID, ProviderKind: "tae", Generation: environment.Target.Generation,
		DesiredState: "ready", ObservedState: "ready", ProviderRegion: "sg", ProviderPSM: "prod.tae.sandbox",
		ProviderSessionRef: providerSessionRef, CreateIdempotencyKey: "managed-http-e2e-create",
		RequestedTTLSeconds: 3600, IdleTTLSeconds: 300, ExpiresAt: &expiresAt,
		Version: 3, CreatedAt: now, UpdatedAt: now,
	}}

	var providerMu sync.Mutex
	var providerRequest sandboxgateway.StartProcessProviderRequest
	provider := fakeprovider.New(func() time.Time { return now }, func(request sandboxgateway.StartProcessProviderRequest) (fakeprovider.CommandResult, error) {
		providerMu.Lock()
		providerRequest = request
		providerMu.Unlock()
		if request.Request.Executable != "lark-cli" || !reflect.DeepEqual(request.Request.Arguments, []string{"docs", "read", "document-token"}) {
			return fakeprovider.CommandResult{}, errors.New("provider received a reconstructed or incorrect argv")
		}
		placeholder := request.Request.Environment[ManagedLarkUserAccessTokenEnvironment]
		if placeholder == "" || strings.Contains(placeholder, "real-lark-token-must-never-enter-sandbox") {
			return fakeprovider.CommandResult{}, errors.New("provider did not receive only the egress placeholder")
		}
		return fakeprovider.CommandResult{
			Stdout:   []byte("{\"source\":\"fake-lark\",\"title\":\"Managed HTTP E2E\"}\n"),
			ExitCode: 0, Status: executionbackend.TerminalSucceeded, OutputComplete: true,
		}, nil
	})
	if _, err := provider.CreateSandbox(t.Context(), sandboxgateway.CreateSandboxRequest{
		SessionRef: providerSessionRef, IdempotencyKey: "managed-http-e2e-create",
		WorkspaceID: principal.WorkspaceID, SessionID: principal.SessionID, EnvironmentID: environment.EnvironmentID,
		Region: "sg", PSM: "prod.tae.sandbox", TTL: time.Hour,
	}); err != nil {
		t.Fatal(err)
	}
	service, err := sandboxgateway.NewService(sandboxgateway.Config{
		Core: core, Provider: provider, Limits: sandboxcontract.DefaultLimits(),
		ProviderRegion: "sg", ProviderPSM: "prod.tae.sandbox", IdleTTL: 5 * time.Minute,
		EnsureTimeout: time.Second, EnsurePollInterval: time.Millisecond,
		Root: "/workspace", Platform: "linux-amd64", Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	tokenAuthority := &managedHTTPE2ETokenAuthority{token: "managed-http-e2e-backend-capability"}
	sandboxHandler, err := sandboxgateway.NewHandler(service, tokenAuthority, 0)
	if err != nil {
		t.Fatal(err)
	}
	backend, err := NewTAEBackend(
		"http://127.0.0.1",
		&http.Client{Transport: managedHTTPE2ETransport{handler: sandboxHandler}},
		tokenAuthority,
	)
	if err != nil {
		t.Fatal(err)
	}
	router, err := executionbackend.NewRouter(backend)
	if err != nil {
		t.Fatal(err)
	}

	seed := bytes.Repeat([]byte{0x6d}, ed25519.SeedSize)
	privateKey := ed25519.NewKeyFromSeed(seed)
	signer, err := egresscapability.NewSigner("execution-gateway", "egress-http-e2e-key", privateKey)
	if err != nil {
		t.Fatal(err)
	}
	authorities, err := NewFrozenManagedCredentialAuthoritySource(ManagedCredentialAuthority{
		CredentialMode: managedcredential.ModeWebhookSwap,
		ProviderKind:   "lark",
		ApplicationID:  "cli_agentserver_sg",
		BindingID:      "90000000-0000-4000-8000-000000000009", AuthorityVersion: 11, CredentialVersion: 13,
		PolicySHA256: larkegresspolicy.SHA256Hex(),
	})
	if err != nil {
		t.Fatal(err)
	}
	placeholderIssuer, err := NewSignedManagedLarkEnvironmentIssuer(
		signer, authorities,
		func() (string, error) { return "91000000-0000-4000-8000-000000000009", nil },
		func() time.Time { return now }, time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	executionAuthority := newFakeShellAuthority()
	executor := newManagedShellExecutor(t, environment, executionAuthority, router, placeholderIssuer)
	result, err := executor.Execute(t.Context(), ShellExecuteRequest{
		Principal: principal, ToolCallID: "call-managed-http-e2e",
		Arguments: json.RawMessage(`{"environment_id":"` + environment.EnvironmentID + `","argv":["lark-cli","docs","read","document-token"],"timeout_ms":10000}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	wantOutput := []byte("{\"source\":\"fake-lark\",\"title\":\"Managed HTTP E2E\"}\n")
	if result.Status != "succeeded" || result.ExitCode == nil || *result.ExitCode != 0 || !result.OutputComplete ||
		len(result.Chunks) != 1 || result.Chunks[0].ChunkBase64 != base64.StdEncoding.EncodeToString(wantOutput) {
		t.Fatalf("managed HTTP E2E result = %+v", result)
	}

	executionAuthority.mu.Lock()
	executionState := executionAuthority.execution
	operationState := executionAuthority.operations[1]
	executionAuthority.mu.Unlock()
	core.mu.Lock()
	authorizations := append([]corecontract.AuthorizeManagedSandboxOperationRequest(nil), core.authorizations...)
	core.mu.Unlock()
	providerMu.Lock()
	started := providerRequest
	providerMu.Unlock()
	if len(authorizations) != 1 || authorizations[0].Action != corecontract.ManagedSandboxActionRunCommand ||
		authorizations[0].ExecutionID != executionState.ExecutionID || authorizations[0].OperationID != operationState.OperationID ||
		authorizations[0].MutationKey != operationState.MutationKey || authorizations[0].SandboxID != environment.Target.ID ||
		authorizations[0].TargetGeneration != environment.Target.Generation {
		t.Fatalf("sandbox Core authorizations = %+v; execution = %+v; operation = %+v", authorizations, executionState, operationState)
	}
	if started.SessionRef != providerSessionRef || started.Request.Target != environment.Target ||
		started.Request.Operation.OperationID != operationState.OperationID || started.Request.Operation.ExecutionID != executionState.ExecutionID ||
		started.Request.WorkspaceRoot != "/workspace" || started.Request.WorkingDirectory != "/workspace" {
		t.Fatalf("fake TAE provider request = %+v", started)
	}
	placeholder := started.Request.Environment[ManagedLarkUserAccessTokenEnvironment]
	verifier, err := egresscapability.NewVerifier([]egresscapability.TrustedKey{{
		Issuer: "execution-gateway", Audience: egresscapability.AudienceForProvider("lark"),
		KeyID: "egress-http-e2e-key", PublicKey: privateKey.Public().(ed25519.PublicKey),
	}})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := verifier.Verify(placeholder, now)
	if err != nil {
		t.Fatal(err)
	}
	if claims.WorkspaceID != principal.WorkspaceID || claims.SessionID != principal.SessionID || claims.ActorID != principal.ActorID ||
		claims.RunID != principal.Run.RunID || claims.RunAttemptID != principal.Run.RunAttemptID ||
		claims.ExecutionID != executionState.ExecutionID || claims.OperationID != operationState.OperationID ||
		claims.SandboxID != environment.Target.ID || claims.TargetGeneration != environment.Target.Generation ||
		claims.Executable != "lark-cli" || claims.BindingID != "90000000-0000-4000-8000-000000000009" ||
		claims.AuthorityVersion != 11 {
		t.Fatalf("managed HTTP E2E placeholder claims = %+v", claims)
	}
	if calls := tokenAuthority.snapshot(); len(calls) != 1 || calls[0].Action != taeActionRunCommand ||
		calls[0].Target != environment.Target || calls[0].Operation.OperationID != operationState.OperationID {
		t.Fatalf("sandbox backend capability calls = %+v", calls)
	}
}

func TestManagedShellBkectlThroughCoreTAEHTTPAndSandboxGateway(t *testing.T) {
	now := time.Date(2026, 8, 14, 8, 0, 0, 0, time.UTC)
	environment := testManagedEnvironment(t)
	principal := testExecutorMCPPrincipal("managed-http-bkectl-e2e")
	providerSessionRef := "tae-provider-session-managed-http-bkectl-e2e"
	expiresAt := now.Add(time.Hour)
	sandboxCore := &managedHTTPE2ECore{state: corecontract.ManagedSandboxState{
		SandboxID: environment.Target.ID, WorkspaceID: principal.WorkspaceID, SessionID: principal.SessionID,
		EnvironmentID: environment.EnvironmentID, ProviderKind: "tae", Generation: environment.Target.Generation,
		DesiredState: "ready", ObservedState: "ready", ProviderRegion: "sg", ProviderPSM: "prod.tae.sandbox",
		ProviderSessionRef: providerSessionRef, CreateIdempotencyKey: "managed-http-bkectl-e2e-create",
		RequestedTTLSeconds: 3600, IdleTTLSeconds: 300, ExpiresAt: &expiresAt,
		Version: 3, CreatedAt: now, UpdatedAt: now,
	}}

	arguments := []string{"bytetree", "node", "get", "--id", "4428303", "--region", "i18nbd", "--json"}
	var providerMu sync.Mutex
	var providerRequest sandboxgateway.StartProcessProviderRequest
	provider := fakeprovider.New(func() time.Time { return now }, func(request sandboxgateway.StartProcessProviderRequest) (fakeprovider.CommandResult, error) {
		providerMu.Lock()
		providerRequest = request
		providerMu.Unlock()
		wantEnvironment := map[string]string{
			ManagedBkectlJWTEnvironment:      "workspace-bytecloud-jwt",
			ManagedBkectlAuthModeEnvironment: ManagedBkectlAuthModeValue,
			ManagedBkectlRegionEnvironment:   ManagedBkectlRegionValue,
			ManagedToolPathEnvironment:       ManagedToolPathValue,
		}
		if request.Request.Executable != bkectlpolicy.Executable ||
			!reflect.DeepEqual(request.Request.Arguments, arguments) ||
			!reflect.DeepEqual(request.Request.Environment, wantEnvironment) {
			return fakeprovider.CommandResult{}, errors.New("provider received an incorrect bkectl process contract")
		}
		return fakeprovider.CommandResult{
			Stdout:   []byte("{\"success\":true,\"data\":{\"id\":4428303}}\n"),
			ExitCode: 0, Status: executionbackend.TerminalSucceeded, OutputComplete: true,
		}, nil
	})
	if _, err := provider.CreateSandbox(t.Context(), sandboxgateway.CreateSandboxRequest{
		SessionRef: providerSessionRef, IdempotencyKey: "managed-http-bkectl-e2e-create",
		WorkspaceID: principal.WorkspaceID, SessionID: principal.SessionID, EnvironmentID: environment.EnvironmentID,
		Region: "sg", PSM: "prod.tae.sandbox", TTL: time.Hour,
	}); err != nil {
		t.Fatal(err)
	}
	sandboxService, err := sandboxgateway.NewService(sandboxgateway.Config{
		Core: sandboxCore, Provider: provider, Limits: sandboxcontract.DefaultLimits(),
		ProviderRegion: "sg", ProviderPSM: "prod.tae.sandbox", IdleTTL: 5 * time.Minute,
		EnsureTimeout: time.Second, EnsurePollInterval: time.Millisecond,
		Root: "/workspace", Platform: "linux-amd64", Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	tokenAuthority := &managedHTTPE2ETokenAuthority{token: "managed-http-bkectl-backend-capability"}
	sandboxHandler, err := sandboxgateway.NewHandler(sandboxService, tokenAuthority, 0)
	if err != nil {
		t.Fatal(err)
	}
	backend, err := NewTAEBackend(
		"http://127.0.0.1",
		&http.Client{Transport: managedHTTPE2ETransport{handler: sandboxHandler}},
		tokenAuthority,
	)
	if err != nil {
		t.Fatal(err)
	}
	router, err := executionbackend.NewRouter(backend)
	if err != nil {
		t.Fatal(err)
	}

	const bindingID = "92000000-0000-4000-8000-000000000009"
	var coreMu sync.Mutex
	var authorityRequests []corecontract.ResolveEgressCredentialAuthorityRequest
	var credentialRequests []corecontract.ResolveExecutionCredentialRequest
	credentialCoreHandler := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		response.Header().Set("Cache-Control", "no-store")
		switch request.URL.Path {
		case corecontract.ResolveExecutionCredentialAuthorityPath:
			var command corecontract.ResolveEgressCredentialAuthorityRequest
			if err := json.NewDecoder(request.Body).Decode(&command); err != nil {
				t.Errorf("decode bkectl authority request: %v", err)
				response.WriteHeader(http.StatusBadRequest)
				return
			}
			coreMu.Lock()
			authorityRequests = append(authorityRequests, command)
			coreMu.Unlock()
			_ = json.NewEncoder(response).Encode(corecontract.ResolveEgressCredentialAuthorityResponse{
				CredentialMode: managedcredential.ModeProcessEnv, ProviderKind: bkectlpolicy.CredentialKind,
				BindingID: bindingID, AuthorityVersion: 5, CredentialVersion: 9,
				PolicySHA256: bkectlpolicy.SHA256Hex(), AuthorizedAt: now,
			})
		case corecontract.ResolveExecutionCredentialPath:
			var command corecontract.ResolveExecutionCredentialRequest
			if err := json.NewDecoder(request.Body).Decode(&command); err != nil {
				t.Errorf("decode bkectl credential request: %v", err)
				response.WriteHeader(http.StatusBadRequest)
				return
			}
			coreMu.Lock()
			credentialRequests = append(credentialRequests, command)
			coreMu.Unlock()
			credentialExpiry := now.Add(time.Hour)
			_ = json.NewEncoder(response).Encode(corecontract.ResolveExecutionCredentialResponse{
				Configured: true, CredentialMode: managedcredential.ModeProcessEnv,
				Credential: "workspace-bytecloud-jwt", ProviderKind: bkectlpolicy.CredentialKind,
				BindingID: bindingID, AuthorityVersion: 5, CredentialVersion: 9,
				PolicySHA256: bkectlpolicy.SHA256Hex(), TAEPSM: "bytedance.sandbox.agentserver",
				ResolvedAt: now, AccessExpiresAt: &credentialExpiry,
			})
		default:
			response.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(response).Encode(corecontract.ErrorResponse{Code: "not_found", Message: "unexpected Core path"})
		}
	})
	credentialCore, err := NewCoreConnectionClient(
		"http://127.0.0.1",
		&http.Client{Transport: managedHTTPE2ETransport{handler: credentialCoreHandler}},
	)
	if err != nil {
		t.Fatal(err)
	}
	credentialCore.authorizationNow = func() time.Time { return now }
	issuer, err := NewDirectWorkspaceManagedEnvironmentIssuer(
		credentialCore, credentialCore, "bytedance.sandbox.agentserver", nil,
	)
	if err != nil {
		t.Fatal(err)
	}

	executionAuthority := newFakeShellAuthority()
	executor := newManagedShellExecutor(t, environment, executionAuthority, router, issuer)
	result, err := executor.Execute(t.Context(), ShellExecuteRequest{
		Principal: principal, ToolCallID: "call-managed-http-bkectl-e2e",
		Arguments: json.RawMessage(`{"environment_id":"` + environment.EnvironmentID + `","argv":["bkectl","bytetree","node","get","--id","4428303","--region","i18nbd","--json"],"timeout_ms":10000}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	wantOutput := []byte("{\"success\":true,\"data\":{\"id\":4428303}}\n")
	if result.Status != "succeeded" || result.ExitCode == nil || *result.ExitCode != 0 || !result.OutputComplete ||
		len(result.Chunks) != 1 || result.Chunks[0].ChunkBase64 != base64.StdEncoding.EncodeToString(wantOutput) {
		t.Fatalf("managed bkectl HTTP E2E result = %+v", result)
	}

	executionAuthority.mu.Lock()
	executionState := executionAuthority.execution
	operationState := executionAuthority.operations[1]
	executionAuthority.mu.Unlock()
	coreMu.Lock()
	resolvedAuthorities := append([]corecontract.ResolveEgressCredentialAuthorityRequest(nil), authorityRequests...)
	resolvedCredentials := append([]corecontract.ResolveExecutionCredentialRequest(nil), credentialRequests...)
	coreMu.Unlock()
	if len(resolvedAuthorities) != 1 || len(resolvedCredentials) != 1 ||
		resolvedAuthorities[0].ProviderKind != bkectlpolicy.CredentialKind ||
		resolvedAuthorities[0].PolicySHA256 != bkectlpolicy.SHA256Hex() ||
		resolvedAuthorities[0].Operation.OperationID != operationState.OperationID ||
		resolvedCredentials[0].Executable != bkectlpolicy.Executable ||
		resolvedCredentials[0].ProviderKind != bkectlpolicy.CredentialKind ||
		!reflect.DeepEqual(resolvedCredentials[0].Arguments, arguments) ||
		resolvedCredentials[0].BindingID != bindingID ||
		resolvedCredentials[0].Operation.ExecutionID != executionState.ExecutionID ||
		resolvedCredentials[0].Operation.OperationID != operationState.OperationID {
		t.Fatalf("bkectl Core authority/credential requests = %+v / %+v", resolvedAuthorities, resolvedCredentials)
	}
	providerMu.Lock()
	started := providerRequest
	providerMu.Unlock()
	if started.SessionRef != providerSessionRef || started.Request.Target != environment.Target ||
		started.Request.Operation.ExecutionID != executionState.ExecutionID ||
		started.Request.Operation.OperationID != operationState.OperationID {
		t.Fatalf("fake TAE bkectl provider request = %+v", started)
	}
}

type managedHTTPE2ECore struct {
	mu             sync.Mutex
	state          corecontract.ManagedSandboxState
	authorizations []corecontract.AuthorizeManagedSandboxOperationRequest
}

func (core *managedHTTPE2ECore) GetManagedSandbox(_ context.Context, sandboxID string, generation int64) (corecontract.GetManagedSandboxResponse, error) {
	core.mu.Lock()
	defer core.mu.Unlock()
	if sandboxID != core.state.SandboxID || generation != core.state.Generation {
		return corecontract.GetManagedSandboxResponse{}, errors.New("managed HTTP E2E target not found")
	}
	return corecontract.GetManagedSandboxResponse{Sandbox: core.state}, nil
}

func (core *managedHTTPE2ECore) AuthorizeManagedSandboxOperation(_ context.Context, request corecontract.AuthorizeManagedSandboxOperationRequest) (corecontract.AuthorizeManagedSandboxOperationResponse, error) {
	core.mu.Lock()
	defer core.mu.Unlock()
	if request.WorkspaceID != core.state.WorkspaceID || request.SessionID != core.state.SessionID ||
		request.EnvironmentID != core.state.EnvironmentID || request.SandboxID != core.state.SandboxID ||
		request.TargetGeneration != core.state.Generation || request.Action != corecontract.ManagedSandboxActionRunCommand {
		return corecontract.AuthorizeManagedSandboxOperationResponse{}, errors.New("managed HTTP E2E operation is outside the frozen target")
	}
	core.authorizations = append(core.authorizations, request)
	return corecontract.AuthorizeManagedSandboxOperationResponse{
		SandboxID: request.SandboxID, TargetGeneration: request.TargetGeneration,
		OperationID: request.OperationID, OperationKind: "process_start", AuthorizedAt: time.Now().UTC(),
	}, nil
}

func (*managedHTTPE2ECore) ReserveManagedSandbox(context.Context, corecontract.ReserveManagedSandboxRequest) (corecontract.ReserveManagedSandboxResponse, error) {
	return corecontract.ReserveManagedSandboxResponse{}, errors.New("unexpected managed sandbox reserve")
}

func (*managedHTTPE2ECore) BeginManagedSandboxCreate(context.Context, corecontract.BeginManagedSandboxCreateRequest) (corecontract.ManagedSandboxMutationResponse, error) {
	return corecontract.ManagedSandboxMutationResponse{}, errors.New("unexpected managed sandbox create")
}

func (*managedHTTPE2ECore) ObserveManagedSandbox(context.Context, corecontract.ObserveManagedSandboxRequest) (corecontract.ManagedSandboxMutationResponse, error) {
	return corecontract.ManagedSandboxMutationResponse{}, errors.New("unexpected managed sandbox observation")
}

func (*managedHTTPE2ECore) RenewManagedSandboxActivity(context.Context, corecontract.RenewManagedSandboxActivityRequest) (corecontract.ManagedSandboxMutationResponse, error) {
	return corecontract.ManagedSandboxMutationResponse{}, errors.New("unexpected managed sandbox activity renewal")
}

func (*managedHTTPE2ECore) ReleaseManagedSandboxActivity(context.Context, corecontract.ReleaseManagedSandboxActivityRequest) (corecontract.ManagedSandboxMutationResponse, error) {
	return corecontract.ManagedSandboxMutationResponse{}, errors.New("unexpected managed sandbox activity release")
}

func (*managedHTTPE2ECore) BeginManagedSandboxDelete(context.Context, corecontract.BeginManagedSandboxDeleteRequest) (corecontract.ManagedSandboxMutationResponse, error) {
	return corecontract.ManagedSandboxMutationResponse{}, errors.New("unexpected managed sandbox delete")
}

func (*managedHTTPE2ECore) ListManagedSandboxesForReconcile(context.Context, corecontract.ListManagedSandboxesForReconcileRequest) (corecontract.ListManagedSandboxesForReconcileResponse, error) {
	return corecontract.ListManagedSandboxesForReconcileResponse{}, errors.New("unexpected managed sandbox reconcile")
}

type managedHTTPE2ETokenAuthority struct {
	mu        sync.Mutex
	token     string
	requests  []SandboxGatewayTokenRequest
	principal sandboxgateway.Principal
}

func (authority *managedHTTPE2ETokenAuthority) Token(_ context.Context, request SandboxGatewayTokenRequest) (string, error) {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	authority.requests = append(authority.requests, request)
	authority.principal = sandboxgateway.Principal{
		Audience:    sandboxgateway.AudienceBackend,
		WorkspaceID: request.Operation.WorkspaceID, SessionID: request.Operation.SessionID,
		EnvironmentID: request.Target.EnvironmentID, RunID: request.Operation.RunID,
		RunAttemptID: request.Operation.RunAttemptID, RunAttemptGeneration: request.Operation.RunAttemptGeneration,
		ExecutionID: request.Operation.ExecutionID, OperationID: request.Operation.OperationID,
		MutationKey: request.Operation.MutationKey, SandboxID: request.Target.ID,
		TargetGeneration: request.Target.Generation,
	}
	return authority.token, nil
}

func (authority *managedHTTPE2ETokenAuthority) Authorize(request *http.Request, action string) (sandboxgateway.Principal, error) {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	if request == nil || request.Header.Get("Authorization") != "Bearer "+authority.token || len(authority.requests) == 0 ||
		action != authority.requests[len(authority.requests)-1].Action {
		return sandboxgateway.Principal{}, errors.New("managed HTTP E2E backend capability was not exact")
	}
	return authority.principal, nil
}

func (authority *managedHTTPE2ETokenAuthority) snapshot() []SandboxGatewayTokenRequest {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	return append([]SandboxGatewayTokenRequest(nil), authority.requests...)
}

type managedHTTPE2ETransport struct {
	handler http.Handler
}

func (transport managedHTTPE2ETransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if transport.handler == nil || request == nil {
		return nil, errors.New("managed HTTP E2E transport is unavailable")
	}
	response := httptest.NewRecorder()
	transport.handler.ServeHTTP(response, request)
	result := response.Result()
	result.Request = request
	return result, nil
}

var _ sandboxgateway.Core = (*managedHTTPE2ECore)(nil)
var _ SandboxGatewayTokenSource = (*managedHTTPE2ETokenAuthority)(nil)
var _ sandboxgateway.Authorizer = (*managedHTTPE2ETokenAuthority)(nil)
var _ http.RoundTripper = managedHTTPE2ETransport{}
