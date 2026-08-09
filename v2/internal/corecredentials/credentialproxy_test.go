package corecredentials

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

type testPlaceholderVerifier struct {
	claims PlaceholderClaims
	err    error
}

func (verifier testPlaceholderVerifier) Verify(string, time.Time) (PlaceholderClaims, error) {
	return verifier.claims, verifier.err
}

type testLiveAuthorizer struct {
	ref BindingReference
	err error
}

func (authorizer testLiveAuthorizer) AuthorizeCredentialUse(context.Context, UseRequest) (BindingReference, error) {
	return authorizer.ref, authorizer.err
}

type testAudit struct {
	records []CredentialUseAudit
	err     error
}

func (audit *testAudit) RecordCredentialUse(_ context.Context, record CredentialUseAudit) error {
	audit.records = append(audit.records, record)
	return audit.err
}

func testService(t *testing.T) (*Service, UseRequest, *MemoryBindingStore, *Keyring, *testAudit) {
	t.Helper()
	now := time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC)
	key := bytes.Repeat([]byte{0x42}, 32)
	keyring, err := NewKeyring("key-1", map[string][]byte{"key-1": key})
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryBindingStore()
	binding := Binding{
		ID: "binding-1", WorkspaceID: "workspace-1", Kind: "lark", DisplayName: "Docs",
		AuthType: "static", AuthorityVersion: 3, CredentialVersion: 7, Status: StatusActive,
		AccessExpiresAt: now.Add(time.Hour), PublicMetadata: json.RawMessage("{\"tenant\":\"sg\"}"),
	}
	binding.SealedSecret, err = keyring.Seal(BindingSealScope{
		WorkspaceID: binding.WorkspaceID, BindingID: binding.ID, CredentialVersion: binding.CredentialVersion,
	}, []byte("opaque-token-value"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(binding); err != nil {
		t.Fatal(err)
	}
	request := UseRequest{
		Placeholder: "placeholder", WorkspaceID: "workspace-1", SessionID: "session-1", ActorID: "actor-1",
		EnvironmentID: "environment-1", RunID: "run-1", RunAttemptID: "attempt-1", RunAttemptGeneration: 2,
		ExecutionID: "execution-1", OperationID: "operation-1", SandboxID: "sandbox-1", TargetGeneration: 4,
		ProviderKind: "lark", BindingID: binding.ID, AuthorityVersion: binding.AuthorityVersion,
		PolicySHA256: strings.Repeat("a", 64), TAEPSM: "bytedance.sandbox.agentserver",
		Host: "open.feishu.cn", Path: "/open-apis/docx/v1/documents/x", Method: "GET",
		Headers: map[string]string{"Authorization": "Bearer placeholder"},
	}
	audit := &testAudit{}
	service, err := NewService(ServiceConfig{
		Registry: NewRegistryMust(t, NewLarkProvider()), Bindings: store,
		LiveAuthorizer: testLiveAuthorizer{ref: BindingReference{
			WorkspaceID: binding.WorkspaceID, Kind: binding.Kind, BindingID: binding.ID, AuthorityVersion: binding.AuthorityVersion,
			CredentialVersion: binding.CredentialVersion,
		}},
		Placeholders: testPlaceholderVerifier{claims: PlaceholderClaims{
			CapabilityID: "capability-1", WorkspaceID: request.WorkspaceID,
			SessionID: request.SessionID, ActorID: request.ActorID,
			EnvironmentID: request.EnvironmentID, RunID: request.RunID, RunAttemptID: request.RunAttemptID,
			RunAttemptGeneration: request.RunAttemptGeneration, ExecutionID: request.ExecutionID, OperationID: request.OperationID,
			SandboxID: request.SandboxID, TargetGeneration: request.TargetGeneration, ProviderKind: request.ProviderKind,
			BindingID: request.BindingID, AuthorityVersion: request.AuthorityVersion, PolicySHA256: request.PolicySHA256,
			ExpiresAt: now.Add(time.Minute),
		}},
		Sealer: keyring, Audit: audit, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	return service, request, store, keyring, audit
}

func NewRegistryMust(t *testing.T, providers ...Provider) *ProviderRegistry {
	t.Helper()
	registry, err := NewRegistry(providers...)
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func TestKeyringBindsCiphertextToBindingVersion(t *testing.T) {
	keyring, err := NewKeyring("key-1", map[string][]byte{"key-1": bytes.Repeat([]byte{1}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	scope := BindingSealScope{WorkspaceID: "workspace-1", BindingID: "binding-1", CredentialVersion: 1}
	sealed, err := keyring.Seal(scope, []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	opened, err := keyring.Open(scope, sealed)
	if err != nil || string(opened) != "secret" {
		t.Fatalf("open = %q, %v", opened, err)
	}
	for _, changed := range []BindingSealScope{
		{WorkspaceID: "workspace-2", BindingID: scope.BindingID, CredentialVersion: 1},
		{WorkspaceID: scope.WorkspaceID, BindingID: "binding-2", CredentialVersion: 1},
		{WorkspaceID: scope.WorkspaceID, BindingID: scope.BindingID, CredentialVersion: 2},
	} {
		if _, err := keyring.Open(changed, sealed); err == nil {
			t.Fatalf("ciphertext opened with changed scope %#v", changed)
		}
	}
}

func TestResolveInjectionMaterializesOnlyAfterLiveChecks(t *testing.T) {
	service, request, _, _, audit := testService(t)
	mutation, result, err := service.ResolveInjection(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if mutation.Headers["Authorization"] != "Bearer opaque-token-value" {
		t.Fatalf("mutation = %#v", mutation)
	}
	if result.Binding.ID != request.BindingID || result.CredentialVersion != 7 {
		t.Fatalf("result = %#v", result)
	}
	if len(audit.records) != 1 || audit.records[0].Decision != "allow" || audit.records[0].CredentialVersion != 7 {
		t.Fatalf("audit = %#v", audit.records)
	}
	raw, _ := json.Marshal(result)
	if bytes.Contains(raw, []byte("opaque-token-value")) {
		t.Fatal("resolve result contains the provider secret")
	}
}

func TestResolveInjectionMissingBindingIsAValidDeny(t *testing.T) {
	service, request, store, _, audit := testService(t)
	if _, err := store.Get(t.Context(), request.WorkspaceID, request.ProviderKind, request.BindingID); err != nil {
		t.Fatal(err)
	}
	// Remove the only binding while retaining a valid operation-bound
	// placeholder. A missing workspace credential is a normal runtime deny.
	store.mu.Lock()
	delete(store.bindings, memoryBindingKey(request.WorkspaceID, request.ProviderKind, request.BindingID))
	store.mu.Unlock()
	_, _, err := service.ResolveInjection(t.Context(), request)
	if ResolveErrorCode(err) != ReasonCredentialNotConfigured {
		t.Fatalf("error = %v, code %q", err, ResolveErrorCode(err))
	}
	if len(audit.records) != 1 || audit.records[0].ReasonCode != ReasonCredentialNotConfigured {
		t.Fatalf("audit = %#v", audit.records)
	}
}

func TestResolveInjectionAuditFailureFailsClosed(t *testing.T) {
	service, request, _, _, audit := testService(t)
	audit.err = errors.New("database down")
	_, _, err := service.ResolveInjection(t.Context(), request)
	if ResolveErrorCode(err) != ReasonCoreUnavailable {
		t.Fatalf("error = %v, code %q", err, ResolveErrorCode(err))
	}
}
