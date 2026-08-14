package coreserver

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/bkectlpolicy"
	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/corecredentials"
	"github.com/agentserver/agentserver/v2/internal/managedcredential"
)

func TestResolveExecutionCredentialMaterializesPlatformByteCloudJWTForReadOnlyBkectl(t *testing.T) {
	service, store, request := testBkectlExecutionCredentialService(t)
	result, err := service.ResolveExecutionCredential(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Configured || result.ProviderKind != bkectlpolicy.CredentialKind ||
		result.Credential != "workspace-bytecloud-jwt" || result.ApplicationID != "" ||
		result.BindingID != store.binding.ID || result.AuthorityVersion != store.binding.AuthorityVersion ||
		result.CredentialVersion != store.binding.CredentialVersion || result.PolicySHA256 != bkectlpolicy.SHA256Hex() ||
		store.authorityCalls != 1 || store.useCalls != 1 {
		t.Fatalf("bkectl credential result/store = %#v / %#v", result, store)
	}
	if len(store.events) != 1 || store.events[0].Stage != "process_env" || store.events[0].Decision != "allow" ||
		store.events[0].ProviderKind != bkectlpolicy.CredentialKind || store.events[0].Method != "PROCESS_ENV" {
		t.Fatalf("bkectl credential audit = %#v", store.events)
	}
}

func TestResolveExecutionCredentialDeniesUnsafeBkectlBeforeCredentialLookup(t *testing.T) {
	for name, arguments := range map[string][]string{
		"credential disclosure": {"auth", "get", "jwt", "--json"},
		"write":                 {"bytesd", "node", "block", "--ip", "10.0.0.1"},
		"unknown":               {"future", "command", "get"},
		"debug":                 {"bytetree", "node", "get", "--id", "4428303", "--debug"},
	} {
		t.Run(name, func(t *testing.T) {
			service, store, request := testBkectlExecutionCredentialService(t)
			request.Arguments = arguments
			if result, err := service.ResolveExecutionCredential(t.Context(), request); err == nil || result.Credential != "" ||
				store.authorityCalls != 0 || store.useCalls != 0 || len(store.events) != 0 {
				t.Fatalf("unsafe bkectl request reached credential lookup: %#v, %v / %#v", result, err, store)
			}
		})
	}
}

func TestResolveExecutionCredentialRejectsRotatedByteCloudBinding(t *testing.T) {
	service, store, request := testBkectlExecutionCredentialService(t)
	request.CredentialVersion--
	if result, err := service.ResolveExecutionCredential(t.Context(), request); err == nil || result.Credential != "" ||
		store.authorityCalls != 1 || store.useCalls != 0 || len(store.events) != 0 {
		t.Fatalf("rotated ByteCloud binding was materialized: %#v, %v / %#v", result, err, store)
	}
}

func TestResolveExecutionCredentialRejectsNonOIDCByteCloudBinding(t *testing.T) {
	service, store, request := testBkectlExecutionCredentialService(t)
	store.binding.AuthType = "aksk"
	if result, err := service.ResolveExecutionCredential(t.Context(), request); err == nil || result.Credential != "" ||
		store.authorityCalls != 1 || store.useCalls != 0 || len(store.events) != 0 {
		t.Fatalf("non-OIDC ByteCloud binding reached process materialization: %#v, %v / %#v", result, err, store)
	}
}

func testBkectlExecutionCredentialService(t *testing.T) (*EgressCredentialService, *testExecutionCredentialStore, corecontract.ResolveExecutionCredentialRequest) {
	t.Helper()
	now := time.Date(2026, 8, 14, 8, 0, 0, 0, time.UTC)
	keyring, err := corecredentials.NewKeyring("credential-key-1", map[string][]byte{
		"credential-key-1": bytes.Repeat([]byte{0x4b}, 32),
	})
	if err != nil {
		t.Fatal(err)
	}
	accessExpiry := now.Add(time.Hour)
	refreshExpiry := now.Add(7 * 24 * time.Hour)
	binding := corecredentials.Binding{
		ID: "b1000000-0000-4000-8000-00000000000b", WorkspaceID: "20000000-0000-4000-8000-000000000002",
		Kind: bkectlpolicy.CredentialKind, DisplayName: "ByteCloud user", OwnerScope: corecredentials.OwnerScopeWorkspace,
		AuthType: corecredentials.AuthTypeDeviceOAuth, Status: corecredentials.StatusActive, AuthorityVersion: 5, CredentialVersion: 9,
		IsDefault: true, AccessExpiresAt: accessExpiry, RefreshExpiresAt: &refreshExpiry,
		PublicMetadata: json.RawMessage(`{"site":"i18n-tt","appId":"app-bytecloud","username":"workspace-owner"}`),
	}
	credentialEnvelope, err := json.Marshal(struct {
		Version          int       `json:"version"`
		Site             string    `json:"site"`
		AppID            string    `json:"appId"`
		DeviceCode       string    `json:"deviceCode"`
		AccessToken      string    `json:"accessToken"`
		RefreshToken     string    `json:"refreshToken"`
		TokenType        string    `json:"tokenType"`
		Scope            string    `json:"scope"`
		Username         string    `json:"username"`
		GrantedAt        time.Time `json:"grantedAt"`
		AccessExpiresAt  time.Time `json:"accessExpiresAt"`
		RefreshExpiresAt time.Time `json:"refreshExpiresAt"`
	}{
		Version: 1, Site: "i18n-tt", AppID: "app-bytecloud", DeviceCode: "platform-device-code",
		AccessToken: "workspace-bytecloud-jwt", RefreshToken: "workspace-bytecloud-refresh-token",
		TokenType: "Bearer", Scope: "openid", Username: "workspace-owner", GrantedAt: now.Add(-time.Minute),
		AccessExpiresAt: accessExpiry, RefreshExpiresAt: refreshExpiry,
	})
	if err != nil {
		t.Fatal(err)
	}
	binding.SealedSecret, err = keyring.Seal(corecredentials.BindingSealScope{
		WorkspaceID: binding.WorkspaceID, BindingID: binding.ID, CredentialVersion: binding.CredentialVersion,
	}, credentialEnvelope)
	if err != nil {
		t.Fatal(err)
	}
	store := &testExecutionCredentialStore{binding: binding, ref: corecredentials.BindingReference{
		WorkspaceID: binding.WorkspaceID, Kind: binding.Kind, BindingID: binding.ID,
		AuthorityVersion: binding.AuthorityVersion, CredentialVersion: binding.CredentialVersion,
		CredentialMode: managedcredential.ModeProcessEnv,
	}}
	provider, err := corecredentials.NewByteCloudDeviceFlowProvider(
		bkectlpolicy.CredentialHost, func(context.Context, string, string) (string, time.Time, error) {
			t.Fatal("Platform OIDC credential unexpectedly attempted an AK/SK exchange")
			return "", time.Time{}, nil
		},
		corecredentials.ByteCloudDeviceFlowConfig{Now: func() time.Time { return now }},
	)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := corecredentials.NewRegistry(provider)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewEgressCredentialService(EgressCredentialServiceConfig{
		Store: store, Registry: registry, Sealer: keyring,
		ProcessEnvironmentTAEPSM: "bytedance.sandbox.agentserver", Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	request := corecontract.ResolveExecutionCredentialRequest{
		Operation: corecontract.EgressCredentialOperation{
			WorkspaceID: binding.WorkspaceID, SessionID: "30000000-0000-4000-8000-000000000003",
			ActorID: "40000000-0000-4000-8000-000000000004", EnvironmentID: "50000000-0000-4000-8000-000000000005",
			RunID: "60000000-0000-4000-8000-000000000006", RunAttemptID: "70000000-0000-4000-8000-000000000007",
			RunAttemptGeneration: 2, ExecutionID: "80000000-0000-4000-8000-000000000008",
			OperationID: "90000000-0000-4000-8000-000000000009", SandboxID: "a0000000-0000-4000-8000-00000000000a",
			TargetGeneration: 4,
		},
		TAEPSM: "bytedance.sandbox.agentserver", PolicySHA256: bkectlpolicy.SHA256Hex(),
		ProviderKind: bkectlpolicy.CredentialKind, ToolName: "shell", Executable: bkectlpolicy.Executable,
		Arguments: []string{"bytetree", "node", "get", "--id", "4428303", "--region", "i18nbd", "--json"},
		BindingID: binding.ID, AuthorityVersion: binding.AuthorityVersion, CredentialVersion: binding.CredentialVersion,
	}
	return service, store, request
}
