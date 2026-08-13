package coreserver

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/corecredentials"
	"github.com/agentserver/agentserver/v2/internal/coredb"
	"github.com/agentserver/agentserver/v2/internal/egresscapability"
	"github.com/agentserver/agentserver/v2/internal/egressgateway"
	"github.com/agentserver/agentserver/v2/internal/larkegresspolicy"
	"github.com/agentserver/agentserver/v2/internal/managedcredential"
)

type testExecutionCredentialStore struct {
	binding        corecredentials.Binding
	ref            corecredentials.BindingReference
	withoutBinding bool
	authorityCalls int
	useCalls       int
	events         []coredb.WorkspaceCredentialUseEvent
}

func (store *testExecutionCredentialStore) Get(_ context.Context, workspaceID, kind, bindingID string) (corecredentials.Binding, error) {
	if store.withoutBinding || workspaceID != store.binding.WorkspaceID || kind != store.binding.Kind || bindingID != store.binding.ID {
		return corecredentials.Binding{}, nil
	}
	result := store.binding
	result.SealedSecret = append([]byte(nil), store.binding.SealedSecret...)
	result.PublicMetadata = append(json.RawMessage(nil), store.binding.PublicMetadata...)
	return result, nil
}

func (*testExecutionCredentialStore) List(context.Context, string, string) ([]corecredentials.BindingMetadata, error) {
	return nil, nil
}

func (store *testExecutionCredentialStore) ResolveCredentialAuthority(_ context.Context, request corecredentials.AuthorityRequest) (corecredentials.BindingReference, error) {
	store.authorityCalls++
	if err := request.Validate(); err != nil {
		return corecredentials.BindingReference{}, err
	}
	if store.withoutBinding {
		return corecredentials.BindingReference{
			WorkspaceID: request.WorkspaceID, Kind: request.ProviderKind,
			CredentialMode: managedcredential.ModeProcessEnv,
		}, nil
	}
	return store.ref, nil
}

func (store *testExecutionCredentialStore) AuthorizeCredentialUse(_ context.Context, request corecredentials.UseRequest) (corecredentials.BindingReference, error) {
	store.useCalls++
	if err := request.ValidateLiveAuthorityScope(); err != nil {
		return corecredentials.BindingReference{}, err
	}
	return store.ref, nil
}

func (store *testExecutionCredentialStore) RecordWorkspaceCredentialUseEvent(_ context.Context, event coredb.WorkspaceCredentialUseEvent) error {
	store.events = append(store.events, event)
	return nil
}

type testExecutionCredentialAuthorizer struct {
	err     error
	actions []string
}

func (authorizer *testExecutionCredentialAuthorizer) AuthorizeWorkload(_ *http.Request, action string) error {
	authorizer.actions = append(authorizer.actions, action)
	return authorizer.err
}

func TestResolveExecutionLarkCredentialMaterializesExactLiveBinding(t *testing.T) {
	service, store, request := testExecutionCredentialService(t)
	result, err := service.ResolveExecutionLarkCredential(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Configured || result.AccessToken != "real-workspace-token" || result.ApplicationID != "cli_agentserver_sg" ||
		result.BindingID != store.binding.ID || result.AuthorityVersion != store.binding.AuthorityVersion ||
		result.CredentialVersion != store.binding.CredentialVersion || store.authorityCalls != 1 || store.useCalls != 1 {
		t.Fatalf("direct credential result/store = %#v / %#v", result, store)
	}
	if len(store.events) != 1 || store.events[0].Stage != "process_env" ||
		store.events[0].Decision != "allow" || store.events[0].Method != "PROCESS_ENV" ||
		store.events[0].CredentialVersion != store.binding.CredentialVersion {
		t.Fatalf("direct credential audit = %#v", store.events)
	}
}

func TestResolveExecutionLarkCredentialRejectsBindingRevokedAfterAuthoritySelection(t *testing.T) {
	service, store, request := testExecutionCredentialService(t)
	store.withoutBinding = true
	if _, err := service.ResolveExecutionLarkCredential(t.Context(), request); err == nil {
		t.Fatal("process credential survived removal of the selected workspace binding")
	}
	if store.authorityCalls != 1 || store.useCalls != 0 || len(store.events) != 0 {
		t.Fatalf("revoked direct credential store = %#v", store)
	}
}

func TestAuthorizeProcessEnvironmentEgressRechecksProofModeVersionAndBearer(t *testing.T) {
	service, store, command := testExecutionCredentialService(t)
	proof := testExecutionProcessProof(t, command, time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC))
	request := testAuthorizeProcessEnvironmentRequest(command, proof, "real-workspace-token")
	result, err := service.AuthorizeProcessEnvironmentEgress(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Headers["Authorization"] != "Bearer real-workspace-token" ||
		result.Headers[managedcredential.LarkAgentTraceHeader] != managedcredential.LarkSanitizedAgentTrace ||
		len(result.Headers) != 2 || result.BindingID != store.binding.ID || result.CredentialVersion != store.binding.CredentialVersion {
		t.Fatalf("process egress result = %#v", result)
	}
	if store.useCalls != 1 || len(store.events) != 1 || store.events[0].Stage != "egress" || store.events[0].Decision != "allow" {
		t.Fatalf("process egress store/audit = %#v", store)
	}

	for name, mutate := range map[string]func(*testExecutionCredentialStore, *corecontract.AuthorizeProcessEnvironmentEgressRequest){
		"proof missing": func(_ *testExecutionCredentialStore, request *corecontract.AuthorizeProcessEnvironmentEgressRequest) {
			request.ProcessProof = ""
			request.Headers[managedcredential.LarkAgentTraceHeader] = ""
		},
		"cross workspace": func(_ *testExecutionCredentialStore, request *corecontract.AuthorizeProcessEnvironmentEgressRequest) {
			request.Operation.WorkspaceID = "21000000-0000-4000-8000-000000000002"
		},
		"mode switched": func(store *testExecutionCredentialStore, _ *corecontract.AuthorizeProcessEnvironmentEgressRequest) {
			store.ref.CredentialMode = managedcredential.ModeWebhookSwap
		},
		"binding rotated": func(store *testExecutionCredentialStore, _ *corecontract.AuthorizeProcessEnvironmentEgressRequest) {
			store.ref.CredentialVersion++
		},
		"bearer changed": func(_ *testExecutionCredentialStore, request *corecontract.AuthorizeProcessEnvironmentEgressRequest) {
			request.Headers["Authorization"] = "Bearer another-workspace-token"
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidateService, candidateStore, candidateCommand := testExecutionCredentialService(t)
			candidateProof := testExecutionProcessProof(t, candidateCommand, time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC))
			candidate := testAuthorizeProcessEnvironmentRequest(candidateCommand, candidateProof, "real-workspace-token")
			mutate(candidateStore, &candidate)
			if result, err := candidateService.AuthorizeProcessEnvironmentEgress(t.Context(), candidate); err == nil || len(result.Headers) != 0 {
				t.Fatalf("unsafe process environment request was authorized: %#v, %v", result, err)
			}
		})
	}
}

func TestExecutionCredentialHandlerRequiresWorkloadAndReturnsNoStore(t *testing.T) {
	service, store, command := testExecutionCredentialService(t)
	authorizer := &testExecutionCredentialAuthorizer{}
	handler, err := NewExecutionCredentialHandler(authorizer, service)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(command)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, corecontract.ResolveExecutionLarkCredentialPath, bytes.NewReader(raw))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" ||
		len(authorizer.actions) != 1 || authorizer.actions[0] != "execution.credentials.lark.resolve" {
		t.Fatalf("handler response/action = %d %#v / %#v", response.Code, response.Header(), authorizer.actions)
	}
	var result corecontract.ResolveExecutionLarkCredentialResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.Configured || result.AccessToken != "real-workspace-token" || result.ApplicationID != "cli_agentserver_sg" || store.useCalls != 1 {
		t.Fatalf("handler direct credential result/store = %#v / %#v", result, store)
	}

	authorityCommand := corecontract.ResolveEgressCredentialAuthorityRequest{
		Operation: command.Operation, ProviderKind: "lark", PolicySHA256: command.PolicySHA256,
	}
	authorityRaw, err := json.Marshal(authorityCommand)
	if err != nil {
		t.Fatal(err)
	}
	authorityRequest := httptest.NewRequest(http.MethodPost, corecontract.ResolveExecutionLarkCredentialAuthorityPath, bytes.NewReader(authorityRaw))
	authorityRequest.Header.Set("Content-Type", "application/json")
	authorityResponse := httptest.NewRecorder()
	handler.ServeHTTP(authorityResponse, authorityRequest)
	if authorityResponse.Code != http.StatusOK || authorityResponse.Header().Get("Cache-Control") != "no-store" ||
		len(authorizer.actions) != 2 || authorizer.actions[1] != "execution.credentials.lark.resolve-authority" {
		t.Fatalf("authority response/action = %d %#v / %#v", authorityResponse.Code, authorityResponse.Header(), authorizer.actions)
	}
	var authority corecontract.ResolveEgressCredentialAuthorityResponse
	if err := json.Unmarshal(authorityResponse.Body.Bytes(), &authority); err != nil {
		t.Fatal(err)
	}
	if authority.CredentialMode != managedcredential.ModeProcessEnv || authority.BindingID != store.binding.ID ||
		authority.AuthorityVersion != store.binding.AuthorityVersion || authority.CredentialVersion != store.binding.CredentialVersion {
		t.Fatalf("handler authority result = %#v", authority)
	}

	authorizer.err = errors.New("wrong workload")
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, httptest.NewRequest(http.MethodPost, corecontract.ResolveExecutionLarkCredentialPath, bytes.NewReader(raw)))
	if denied.Code != http.StatusForbidden || store.useCalls != 1 {
		t.Fatalf("unauthorized handler response/store = %d / %#v", denied.Code, store)
	}
}

func testExecutionCredentialService(t *testing.T) (*EgressCredentialService, *testExecutionCredentialStore, corecontract.ResolveExecutionLarkCredentialRequest) {
	t.Helper()
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	keyring, err := corecredentials.NewKeyring("credential-key-1", map[string][]byte{
		"credential-key-1": bytes.Repeat([]byte{0x4a}, 32),
	})
	if err != nil {
		t.Fatal(err)
	}
	binding := corecredentials.Binding{
		ID: "b0000000-0000-4000-8000-00000000000b", WorkspaceID: "20000000-0000-4000-8000-000000000002", Kind: "lark", DisplayName: "Lark docs",
		OwnerScope: corecredentials.OwnerScopeWorkspace, AuthType: "static", Status: corecredentials.StatusActive,
		AuthorityVersion: 3, CredentialVersion: 7, IsDefault: true, AccessExpiresAt: now.Add(time.Hour),
		PublicMetadata: json.RawMessage(`{"appId":"cli_agentserver_sg"}`),
	}
	binding.SealedSecret, err = keyring.Seal(corecredentials.BindingSealScope{
		WorkspaceID: binding.WorkspaceID, BindingID: binding.ID, CredentialVersion: binding.CredentialVersion,
	}, []byte("real-workspace-token"))
	if err != nil {
		t.Fatal(err)
	}
	store := &testExecutionCredentialStore{binding: binding, ref: corecredentials.BindingReference{
		WorkspaceID: binding.WorkspaceID, Kind: binding.Kind, BindingID: binding.ID,
		AuthorityVersion: binding.AuthorityVersion, CredentialVersion: binding.CredentialVersion,
		CredentialMode: managedcredential.ModeProcessEnv,
	}}
	registry, err := corecredentials.NewRegistry(corecredentials.NewLarkProvider())
	if err != nil {
		t.Fatal(err)
	}
	seed := bytes.Repeat([]byte{0x5a}, ed25519.SeedSize)
	privateKey := ed25519.NewKeyFromSeed(seed)
	proofs, err := egresscapability.NewVerifier([]egresscapability.TrustedKey{{
		Issuer: "executor-gateway/egress", Audience: egresscapability.AudienceForProvider("lark"),
		KeyID: "egress-key-1", PublicKey: privateKey.Public().(ed25519.PublicKey),
	}})
	if err != nil {
		t.Fatal(err)
	}
	placeholderVerifier, err := egressgateway.NewCapabilityPlaceholderVerifier(proofs)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewEgressCredentialService(EgressCredentialServiceConfig{
		Store: store, Registry: registry, Sealer: keyring,
		Placeholders: placeholderVerifier, ProcessProofs: proofs, ProcessEnvironmentTAEPSM: "bytedance.sandbox.agentserver",
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	command := corecontract.ResolveExecutionLarkCredentialRequest{
		Operation: corecontract.EgressCredentialOperation{
			WorkspaceID: binding.WorkspaceID, SessionID: "30000000-0000-4000-8000-000000000003",
			ActorID:              "40000000-0000-4000-8000-000000000004",
			EnvironmentID:        "50000000-0000-4000-8000-000000000005",
			RunID:                "60000000-0000-4000-8000-000000000006",
			RunAttemptID:         "70000000-0000-4000-8000-000000000007",
			RunAttemptGeneration: 2, ExecutionID: "80000000-0000-4000-8000-000000000008",
			OperationID: "90000000-0000-4000-8000-000000000009",
			SandboxID:   "a0000000-0000-4000-8000-00000000000a", TargetGeneration: 4,
		},
		TAEPSM: "bytedance.sandbox.agentserver", PolicySHA256: larkegresspolicy.SHA256Hex(),
		ToolName: "shell", Executable: "lark-cli",
		BindingID: binding.ID, AuthorityVersion: binding.AuthorityVersion,
		CredentialVersion: binding.CredentialVersion,
	}
	if strings.TrimSpace(command.PolicySHA256) == "" {
		t.Fatal("test policy digest is empty")
	}
	return service, store, command
}

func testExecutionProcessProof(
	t *testing.T,
	command corecontract.ResolveExecutionLarkCredentialRequest,
	now time.Time,
) string {
	t.Helper()
	privateKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x5a}, ed25519.SeedSize))
	signer, err := egresscapability.NewSigner("executor-gateway/egress", "egress-key-1", privateKey)
	if err != nil {
		t.Fatal(err)
	}
	operation := command.Operation
	proof, err := signer.SignProcessEnvironment(egresscapability.ProcessEnvironmentClaims{
		Version: egresscapability.ProcessEnvironmentVersion, Issuer: signer.Issuer(),
		CapabilityID: "10000000-0000-4000-8000-000000000001",
		WorkspaceID:  operation.WorkspaceID, SessionID: operation.SessionID, ActorID: operation.ActorID,
		EnvironmentID: operation.EnvironmentID, RunID: operation.RunID, RunAttemptID: operation.RunAttemptID,
		RunAttemptGeneration: operation.RunAttemptGeneration, ExecutionID: operation.ExecutionID,
		OperationID: operation.OperationID, SandboxID: operation.SandboxID, TargetGeneration: operation.TargetGeneration,
		ProviderKind: "lark", BindingID: command.BindingID, AuthorityVersion: command.AuthorityVersion,
		CredentialVersion: command.CredentialVersion, PolicySHA256: command.PolicySHA256,
		IssuedAtUnixMS: now.Add(-time.Second).UnixMilli(), ExpiresAtUnixMS: now.Add(time.Minute).UnixMilli(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return proof
}

func testAuthorizeProcessEnvironmentRequest(
	command corecontract.ResolveExecutionLarkCredentialRequest,
	proof, token string,
) corecontract.AuthorizeProcessEnvironmentEgressRequest {
	return corecontract.AuthorizeProcessEnvironmentEgressRequest{
		ProcessProof: proof, Operation: command.Operation, ProviderKind: "lark",
		BindingID: command.BindingID, AuthorityVersion: command.AuthorityVersion,
		CredentialVersion: command.CredentialVersion, PolicySHA256: command.PolicySHA256,
		TAEPSM: command.TAEPSM, Host: larkegresspolicy.OpenAPIHost,
		Path: "/open-apis/docx/v1/documents/document-1/raw_content", Method: "GET",
		Headers: map[string]string{
			"Authorization":                        "Bearer " + token,
			managedcredential.LarkAgentTraceHeader: proof,
		},
	}
}
