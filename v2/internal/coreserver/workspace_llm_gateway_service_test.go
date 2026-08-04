package coreserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/coredb"
)

func TestWorkspaceLLMGatewayResolutionLogContainsOnlyStage(t *testing.T) {
	var logs bytes.Buffer
	service := &WorkspaceLLMGatewayService{logger: slog.New(slog.NewJSONHandler(&logs, nil))}
	service.logGatewayResolutionFailure("sealed_token_open")
	if !strings.Contains(logs.String(), `"stage":"sealed_token_open"`) ||
		strings.Contains(logs.String(), `"error"`) || strings.Contains(logs.String(), `"gateway_id"`) ||
		strings.Contains(logs.String(), `"authorization"`) {
		t.Fatalf("unsafe or incomplete gateway resolution log = %q", logs.String())
	}
}

const (
	testLLMGatewayWorkspaceID = "93000000-0000-4000-8000-000000000001"
	testLLMGatewayID          = "93000000-0000-4000-8000-000000000002"
	testLLMGatewayUserID      = "93000000-0000-4000-8000-000000000003"
	testLLMGatewayAuthID      = "93000000-0000-4000-8000-000000000004"
	testLLMGatewayGrantID     = "93000000-0000-4000-8000-000000000005"
)

func TestWorkspaceLLMGatewayAuthorizationSealsOneUserGrant(t *testing.T) {
	now := time.Date(2026, 8, 2, 1, 0, 0, 0, time.UTC)
	gateway := testWorkspaceLLMGateway()
	provider := &fakeWorkspaceLLMGatewayProvider{}
	var transaction coredb.WorkspaceLLMGatewayAuthTransaction
	var completed coredb.CompleteWorkspaceLLMGatewayAuthTransactionCommand
	store := &fakeWorkspaceLLMGatewayStore{
		readForAuthorization: func(_ context.Context, workspaceID, gatewayID, userID string) (coredb.WorkspaceLLMGateway, error) {
			if workspaceID != testLLMGatewayWorkspaceID || gatewayID != testLLMGatewayID || userID != testLLMGatewayUserID {
				t.Fatal("authorization read escaped its user scope")
			}
			return gateway, nil
		},
		createTransaction: func(_ context.Context, command coredb.CreateWorkspaceLLMGatewayAuthTransactionCommand) (coredb.WorkspaceLLMGatewayAuthTransaction, error) {
			if command.ID != testLLMGatewayAuthID || command.GatewayVersion != gateway.Version || command.TTL != 5*time.Minute ||
				bytes.Contains(command.SealedSecrets, []byte("browser-binding")) {
				t.Fatalf("create transaction = %+v", command)
			}
			transaction = coredb.WorkspaceLLMGatewayAuthTransaction{
				ID: command.ID, WorkspaceID: command.WorkspaceID, GatewayID: command.GatewayID,
				GatewayVersion: command.GatewayVersion, UserID: command.UserID,
				OIDCStateSHA256: command.OIDCStateSHA256, BrowserBindingSHA256: command.BrowserBindingSHA256,
				SealedSecrets: command.SealedSecrets, Status: coredb.LLMGatewayAuthStatusPending,
				ExpiresAt: now.Add(command.TTL), Version: 1,
			}
			return transaction, nil
		},
		claimTransaction: func(_ context.Context, command coredb.ClaimWorkspaceLLMGatewayAuthTransactionCommand) (coredb.WorkspaceLLMGatewayAuthTransaction, error) {
			if command.OIDCStateSHA256 != transaction.OIDCStateSHA256 || command.BrowserBindingSHA256 != transaction.BrowserBindingSHA256 {
				t.Fatal("callback hashes did not match the created transaction")
			}
			claimed := transaction
			claimed.Status = coredb.LLMGatewayAuthStatusCallbackClaimed
			claimed.Version = 2
			return claimed, nil
		},
		completeTransaction: func(_ context.Context, command coredb.CompleteWorkspaceLLMGatewayAuthTransactionCommand) (coredb.WorkspaceLLMGatewayGrant, error) {
			completed = command
			return coredb.WorkspaceLLMGatewayGrant{
				ID: command.GrantID, GatewayID: testLLMGatewayID, WorkspaceID: testLLMGatewayWorkspaceID,
				UserID: testLLMGatewayUserID, OIDCIssuer: command.OIDCIssuer, OIDCSubject: command.OIDCSubject,
				Status: coredb.LLMGatewayGrantStatusActive, SealedTokenSet: command.SealedTokenSet,
				BearerExpiresAt: command.BearerExpiresAt, Version: 1,
			}, nil
		},
	}
	ids := []string{testLLMGatewayAuthID, testLLMGatewayGrantID}
	service := newTestWorkspaceLLMGatewayService(t, store, provider, now, func() (string, error) {
		value := ids[0]
		ids = ids[1:]
		return value, nil
	})
	binding := strings.Repeat("b", 43)
	begun, err := service.BeginAuthorization(t.Context(), testLLMGatewayWorkspaceID, testLLMGatewayID, testLLMGatewayUserID,
		corecontract.BeginWorkspaceLLMGatewayAuthorizationRequest{BrowserBinding: binding})
	if err != nil || begun.GatewayID != testLLMGatewayID || !begun.ExpiresAt.Equal(now.Add(5*time.Minute)) || provider.state == "" {
		t.Fatalf("begin authorization = %+v, %v", begun, err)
	}
	if transaction.OIDCStateSHA256 != sha256.Sum256([]byte(provider.state)) || transaction.BrowserBindingSHA256 != sha256.Sum256([]byte(binding)) {
		t.Fatal("authorization transaction did not bind state and browser")
	}
	provider.exchangeGrant = WorkspaceLLMGatewayOIDCGrant{
		Issuer: gateway.OIDCIssuer, Subject: "gateway-user-subject",
		Tokens: testWorkspaceLLMGatewayTokens(now.Add(10*time.Minute), "initial"),
	}
	result, err := service.CompleteAuthorization(t.Context(), testLLMGatewayWorkspaceID, testLLMGatewayID, testLLMGatewayUserID,
		corecontract.CompleteWorkspaceLLMGatewayAuthorizationRequest{
			State: provider.state, Code: "authorization-code", BrowserBinding: binding,
		})
	if err != nil || result.GatewayID != testLLMGatewayID || result.GrantStatus != coredb.LLMGatewayGrantStatusActive ||
		!result.BearerExpiresAt.Equal(now.Add(10*time.Minute)) {
		t.Fatalf("complete authorization = %+v, %v", result, err)
	}
	if provider.exchangeCode != "authorization-code" || provider.exchangeVerifier != provider.verifier || provider.exchangeNonce != provider.nonce ||
		completed.TransactionID != testLLMGatewayAuthID || completed.ExpectedVersion != 2 || completed.GrantID != testLLMGatewayGrantID ||
		completed.OIDCSubject != "gateway-user-subject" || bytes.Contains(completed.SealedTokenSet, []byte("initial-id-token")) {
		t.Fatalf("completed grant/provider exchange = %+v / %+v", completed, provider)
	}
	opened, err := service.sealer.OpenGrantTokenSet(LLMGatewaySealScope{
		WorkspaceID: testLLMGatewayWorkspaceID, GatewayID: testLLMGatewayID,
		UserID: testLLMGatewayUserID, GatewayVersion: gateway.Version,
	}, completed.SealedTokenSet)
	if err != nil || !bytes.Contains(opened, []byte("initial-id-token")) || !bytes.Contains(opened, []byte("initial-refresh-token")) {
		t.Fatalf("opened sealed token set = %q, %v", opened, err)
	}
}

func TestWorkspaceLLMGatewayCreateAuthorizesOwnerBeforeDiscovery(t *testing.T) {
	store := &fakeWorkspaceLLMGatewayStore{
		requireOwner: func(context.Context, string, string) error {
			return &coredb.StateError{Code: coredb.ErrorForbidden, Operation: "RequireWorkspaceLLMGatewayOwner"}
		},
	}
	factory := &fakeWorkspaceLLMGatewayProviderFactory{provider: &fakeWorkspaceLLMGatewayProvider{}}
	service, err := NewWorkspaceLLMGatewayService(WorkspaceLLMGatewayServiceConfig{
		Store: store, Sealer: testLLMGatewaySealer(t, "test", map[string]byte{"test": 0x42}),
		Providers: factory, RedirectURL: "https://agent.example.com/auth/llm-gateway/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.CreateGateway(t.Context(), testLLMGatewayWorkspaceID, testLLMGatewayUserID, corecontract.CreateWorkspaceLLMGatewayRequest{
		GatewayID: testLLMGatewayID, Name: "test Gateway", ResponsesURL: "https://llm.example.com/v1/responses",
		OIDCIssuer: "https://id.example.com", OIDCClientID: "workspace-public-client",
		OIDCScopes: []string{"openid", "offline_access"}, DefaultModel: "model-1", MakeDefault: true,
	})
	if !coredb.HasStateErrorCode(err, coredb.ErrorForbidden) || factory.discoverCalls != 0 {
		t.Fatalf("non-owner Gateway creation = %v, discovery calls=%d", err, factory.discoverCalls)
	}
}

func TestWorkspaceLLMGatewayUpdateDiscoversThenCommitsVersionedFence(t *testing.T) {
	gateway := testWorkspaceLLMGateway()
	ownerChecked := false
	var command coredb.UpdateWorkspaceLLMGatewayCommand
	store := &fakeWorkspaceLLMGatewayStore{
		requireOwner: func(_ context.Context, workspaceID, actorID string) error {
			if workspaceID != testLLMGatewayWorkspaceID || actorID != testLLMGatewayUserID {
				t.Fatal("owner preflight escaped the requested workspace")
			}
			ownerChecked = true
			return nil
		},
		updateGateway: func(_ context.Context, input coredb.UpdateWorkspaceLLMGatewayCommand) (coredb.UpdateWorkspaceLLMGatewayResult, error) {
			if !ownerChecked {
				t.Fatal("Gateway update ran before owner preflight")
			}
			command = input
			gateway.Name = input.Name
			gateway.OIDCScopes = input.OIDCScopes
			gateway.BearerTokenType = input.BearerTokenType
			gateway.DefaultModel = input.DefaultModel
			gateway.Default = input.MakeDefault
			gateway.Version++
			gateway.GrantStatus = coredb.LLMGatewayGrantStatusReauthRequired
			return coredb.UpdateWorkspaceLLMGatewayResult{Gateway: gateway, Changed: true}, nil
		},
	}
	factory := &fakeWorkspaceLLMGatewayProviderFactory{provider: &fakeWorkspaceLLMGatewayProvider{}}
	service, err := NewWorkspaceLLMGatewayService(WorkspaceLLMGatewayServiceConfig{
		Store: store, Sealer: testLLMGatewaySealer(t, "test", map[string]byte{"test": 0x42}),
		Providers: factory, RedirectURL: "https://agent.example.com/auth/llm-gateway/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.UpdateGateway(t.Context(), testLLMGatewayWorkspaceID, testLLMGatewayID, testLLMGatewayUserID,
		corecontract.UpdateWorkspaceLLMGatewayRequest{
			Name: "updated Gateway", ResponsesURL: "https://llm.example.com/v1/responses",
			OIDCIssuer: "https://id.example.com", OIDCClientID: "updated-public-client",
			OIDCScopes:      []string{"project:inference", "offline_access", "openid"},
			BearerTokenType: coredb.LLMGatewayBearerAccessToken, DefaultModel: "model-2",
			MakeDefault: true, ExpectedVersion: 3,
		})
	if err != nil || !result.Changed || result.Gateway.Version != 4 || result.Gateway.GrantStatus != coredb.LLMGatewayGrantStatusReauthRequired ||
		factory.discoverCalls != 1 || command.ExpectedVersion != 3 || command.OIDCScopes != "offline_access openid project:inference" {
		t.Fatalf("update Gateway = %+v, %v; discovery=%d command=%+v", result, err, factory.discoverCalls, command)
	}
}

func TestWorkspaceLLMGatewayResolveUpstreamRefreshesAndAcceptsExactRaceWinner(t *testing.T) {
	now := time.Date(2026, 8, 2, 2, 0, 0, 0, time.UTC)
	gateway := testWorkspaceLLMGateway()
	binding := coredb.RunLLMGatewayBinding{
		GatewayID: testLLMGatewayID, ConfigVersion: gateway.Version,
		GrantUserID: testLLMGatewayUserID, Model: gateway.DefaultModel,
	}
	for _, test := range []struct {
		name string
		race bool
	}{
		{name: "refresh winner"},
		{name: "another Core wins", race: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			provider := &fakeWorkspaceLLMGatewayProvider{
				refreshGrant: WorkspaceLLMGatewayOIDCGrant{
					Issuer: gateway.OIDCIssuer, Subject: "gateway-user-subject",
					Tokens: testWorkspaceLLMGatewayTokens(now.Add(20*time.Minute), "refreshed"),
				},
			}
			store := &fakeWorkspaceLLMGatewayStore{}
			service := newTestWorkspaceLLMGatewayService(t, store, provider, now, nil)
			initialTokens := testWorkspaceLLMGatewayTokens(now.Add(30*time.Second), "initial")
			initialSealed, err := service.sealTokenSet(binding, testLLMGatewayWorkspaceID, initialTokens)
			if err != nil {
				t.Fatal(err)
			}
			authority := coredb.LLMGatewayLiveAuthority{
				Gateway: gateway, Model: gateway.DefaultModel,
				Grant: coredb.WorkspaceLLMGatewayGrant{
					ID: testLLMGatewayGrantID, GatewayID: testLLMGatewayID, WorkspaceID: testLLMGatewayWorkspaceID,
					UserID: testLLMGatewayUserID, OIDCIssuer: gateway.OIDCIssuer, OIDCSubject: "gateway-user-subject",
					Status: coredb.LLMGatewayGrantStatusActive, SealedTokenSet: initialSealed,
					BearerExpiresAt: initialTokens.IDTokenExpiresAt, Version: 7,
				},
			}
			store.updateGrantTokens = func(_ context.Context, grantID string, version int64, sealed []byte, expiresAt time.Time) (coredb.WorkspaceLLMGatewayGrant, error) {
				if grantID != testLLMGatewayGrantID || version != 7 || !expiresAt.Equal(now.Add(20*time.Minute)) {
					t.Fatal("refresh update escaped the expected grant")
				}
				if test.race {
					return coredb.WorkspaceLLMGatewayGrant{}, &coredb.StateError{Code: coredb.ErrorVersionConflict, Operation: "UpdateWorkspaceLLMGatewayGrantTokens"}
				}
				updated := authority.Grant
				updated.SealedTokenSet = sealed
				updated.BearerExpiresAt = expiresAt
				updated.Version++
				return updated, nil
			}
			if test.race {
				raceTokens := testWorkspaceLLMGatewayTokens(now.Add(25*time.Minute), "race-winner")
				raceSealed, err := service.sealTokenSet(binding, testLLMGatewayWorkspaceID, raceTokens)
				if err != nil {
					t.Fatal(err)
				}
				store.readLiveAuthority = func(_ context.Context, workspaceID string, actual coredb.RunLLMGatewayBinding) (coredb.LLMGatewayLiveAuthority, error) {
					if workspaceID != testLLMGatewayWorkspaceID || actual != binding {
						t.Fatal("refresh race re-read escaped the frozen binding")
					}
					winner := authority
					winner.Grant.SealedTokenSet = raceSealed
					winner.Grant.BearerExpiresAt = raceTokens.IDTokenExpiresAt
					winner.Grant.Version++
					return winner, nil
				}
			}
			result, err := service.ResolveUpstream(t.Context(), authority)
			if err != nil || result.ResponsesURL != gateway.ResponsesURL || result.Model != gateway.DefaultModel ||
				result.GatewayConfigVersion != gateway.Version || result.GrantUserID != testLLMGatewayUserID {
				t.Fatalf("resolve upstream = %+v, %v", result, err)
			}
			wantAuthorization := "Bearer refreshed-id-token"
			if test.race {
				wantAuthorization = "Bearer race-winner-id-token"
			}
			if result.Authorization != wantAuthorization || provider.refreshCalls != 1 {
				t.Fatalf("resolved authorization = %q, refresh calls %d", result.Authorization, provider.refreshCalls)
			}
		})
	}
}

func TestWorkspaceLLMGatewayAuthorizationRequiresOfflineRefreshGrant(t *testing.T) {
	now := time.Date(2026, 8, 2, 1, 30, 0, 0, time.UTC)
	gateway := testWorkspaceLLMGateway()
	provider := &fakeWorkspaceLLMGatewayProvider{}
	var transaction coredb.WorkspaceLLMGatewayAuthTransaction
	failureCode := ""
	store := &fakeWorkspaceLLMGatewayStore{
		readForAuthorization: func(context.Context, string, string, string) (coredb.WorkspaceLLMGateway, error) {
			return gateway, nil
		},
		createTransaction: func(_ context.Context, command coredb.CreateWorkspaceLLMGatewayAuthTransactionCommand) (coredb.WorkspaceLLMGatewayAuthTransaction, error) {
			transaction = coredb.WorkspaceLLMGatewayAuthTransaction{
				ID: command.ID, WorkspaceID: command.WorkspaceID, GatewayID: command.GatewayID,
				GatewayVersion: command.GatewayVersion, UserID: command.UserID,
				OIDCStateSHA256: command.OIDCStateSHA256, BrowserBindingSHA256: command.BrowserBindingSHA256,
				SealedSecrets: command.SealedSecrets, Status: coredb.LLMGatewayAuthStatusPending,
				ExpiresAt: now.Add(command.TTL), Version: 1,
			}
			return transaction, nil
		},
		claimTransaction: func(context.Context, coredb.ClaimWorkspaceLLMGatewayAuthTransactionCommand) (coredb.WorkspaceLLMGatewayAuthTransaction, error) {
			claimed := transaction
			claimed.Status = coredb.LLMGatewayAuthStatusCallbackClaimed
			claimed.Version = 2
			return claimed, nil
		},
		failTransaction: func(_ context.Context, command coredb.FailWorkspaceLLMGatewayAuthTransactionCommand) (coredb.WorkspaceLLMGatewayAuthTransaction, error) {
			failureCode = command.FailureCode
			return coredb.WorkspaceLLMGatewayAuthTransaction{Status: command.Status}, nil
		},
	}
	service := newTestWorkspaceLLMGatewayService(t, store, provider, now, func() (string, error) { return testLLMGatewayAuthID, nil })
	binding := strings.Repeat("b", 43)
	if _, err := service.BeginAuthorization(t.Context(), testLLMGatewayWorkspaceID, testLLMGatewayID, testLLMGatewayUserID,
		corecontract.BeginWorkspaceLLMGatewayAuthorizationRequest{BrowserBinding: binding}); err != nil {
		t.Fatal(err)
	}
	provider.exchangeGrant = WorkspaceLLMGatewayOIDCGrant{
		Issuer: gateway.OIDCIssuer, Subject: "gateway-user-subject",
		Tokens: testWorkspaceLLMGatewayTokens(now.Add(10*time.Minute), "no-offline-grant"),
	}
	provider.exchangeGrant.Tokens.RefreshToken = ""
	_, err := service.CompleteAuthorization(t.Context(), testLLMGatewayWorkspaceID, testLLMGatewayID, testLLMGatewayUserID,
		corecontract.CompleteWorkspaceLLMGatewayAuthorizationRequest{
			State: provider.state, Code: "authorization-code", BrowserBinding: binding,
		})
	if err == nil || failureCode != "refresh_token_missing" {
		t.Fatalf("missing offline grant error = %v, failure code = %q", err, failureCode)
	}
}

func TestWorkspaceLLMGatewayRefreshFailureFencesGrant(t *testing.T) {
	now := time.Date(2026, 8, 2, 3, 0, 0, 0, time.UTC)
	gateway := testWorkspaceLLMGateway()
	binding := coredb.RunLLMGatewayBinding{GatewayID: gateway.ID, ConfigVersion: gateway.Version, GrantUserID: testLLMGatewayUserID, Model: gateway.DefaultModel}
	provider := &fakeWorkspaceLLMGatewayProvider{refreshError: errors.New("refresh denied")}
	marked := 0
	store := &fakeWorkspaceLLMGatewayStore{markReauth: func(_ context.Context, grantID string, version int64) error {
		if grantID != testLLMGatewayGrantID || version != 1 {
			t.Fatal("reauthorization fence targeted the wrong grant")
		}
		marked++
		return nil
	}}
	service := newTestWorkspaceLLMGatewayService(t, store, provider, now, nil)
	tokens := testWorkspaceLLMGatewayTokens(now.Add(10*time.Second), "expiring")
	sealed, err := service.sealTokenSet(binding, testLLMGatewayWorkspaceID, tokens)
	if err != nil {
		t.Fatal(err)
	}
	authority := coredb.LLMGatewayLiveAuthority{
		Gateway: gateway, Model: gateway.DefaultModel,
		Grant: coredb.WorkspaceLLMGatewayGrant{
			ID: testLLMGatewayGrantID, GatewayID: gateway.ID, WorkspaceID: gateway.WorkspaceID, UserID: testLLMGatewayUserID,
			OIDCIssuer: gateway.OIDCIssuer, OIDCSubject: "gateway-user-subject", Status: coredb.LLMGatewayGrantStatusActive,
			SealedTokenSet: sealed, BearerExpiresAt: tokens.IDTokenExpiresAt, Version: 1,
		},
	}
	if _, err := service.ResolveUpstream(t.Context(), authority); err == nil || marked != 1 {
		t.Fatalf("refresh failure = %v, marked=%d", err, marked)
	}
}

func TestWorkspaceLLMGatewayUnopenableGrantRequiresReauthorization(t *testing.T) {
	now := time.Date(2026, 8, 2, 3, 30, 0, 0, time.UTC)
	gateway := testWorkspaceLLMGateway()
	marked := 0
	store := &fakeWorkspaceLLMGatewayStore{markReauth: func(_ context.Context, grantID string, version int64) error {
		if grantID != testLLMGatewayGrantID || version != 4 {
			t.Fatalf("reauthorization fence = %s/%d", grantID, version)
		}
		marked++
		return nil
	}}
	service := newTestWorkspaceLLMGatewayService(t, store, &fakeWorkspaceLLMGatewayProvider{}, now, nil)
	authority := coredb.LLMGatewayLiveAuthority{
		Gateway: gateway, Model: gateway.DefaultModel,
		Grant: coredb.WorkspaceLLMGatewayGrant{
			ID: testLLMGatewayGrantID, GatewayID: gateway.ID, WorkspaceID: gateway.WorkspaceID,
			UserID: testLLMGatewayUserID, OIDCIssuer: gateway.OIDCIssuer, OIDCSubject: "gateway-user-subject",
			Status: coredb.LLMGatewayGrantStatusActive, SealedTokenSet: bytes.Repeat([]byte{0x99}, 64),
			BearerExpiresAt: now.Add(time.Hour), Version: 4,
		},
	}
	if _, err := service.ResolveUpstream(t.Context(), authority); err == nil || marked != 1 {
		t.Fatalf("unopenable grant resolution = %v, marked=%d", err, marked)
	}
}

func TestWorkspaceLLMGatewayBearerFitsInternalAuthorizationHeader(t *testing.T) {
	tokens := testWorkspaceLLMGatewayTokens(time.Now().UTC().Add(time.Hour), "bounded")
	tokens.IDToken = strings.Repeat("x", maximumLLMGatewayBearerBytes+1)
	if _, _, err := workspaceLLMGatewayBearer(tokens, coredb.LLMGatewayBearerIDToken); err == nil {
		t.Fatal("oversized workspace Gateway bearer was accepted")
	}
}

func TestWorkspaceLLMGatewayDisableUsesOwnerScopedStoreFence(t *testing.T) {
	gateway := testWorkspaceLLMGateway()
	store := &fakeWorkspaceLLMGatewayStore{
		disableGateway: func(_ context.Context, command coredb.DisableWorkspaceLLMGatewayCommand) (coredb.DisableWorkspaceLLMGatewayResult, error) {
			if command.WorkspaceID != testLLMGatewayWorkspaceID || command.GatewayID != testLLMGatewayID || command.ActorID != testLLMGatewayUserID {
				t.Fatalf("disable command = %+v", command)
			}
			gateway.Status = coredb.LLMGatewayStatusDisabled
			gateway.Default = false
			gateway.Version++
			return coredb.DisableWorkspaceLLMGatewayResult{Gateway: gateway, Changed: true}, nil
		},
	}
	service := newTestWorkspaceLLMGatewayService(t, store, &fakeWorkspaceLLMGatewayProvider{}, time.Now().UTC(), nil)
	result, err := service.DisableGateway(t.Context(), testLLMGatewayWorkspaceID, testLLMGatewayID, testLLMGatewayUserID)
	if err != nil || result.GatewayID != testLLMGatewayID || result.Status != coredb.LLMGatewayStatusDisabled ||
		result.Version != 4 || !result.Changed {
		t.Fatalf("disable Gateway = %+v, %v", result, err)
	}
}

func newTestWorkspaceLLMGatewayService(
	t *testing.T,
	store WorkspaceLLMGatewayStore,
	provider WorkspaceLLMGatewayOIDCProvider,
	now time.Time,
	newID func() (string, error),
) *WorkspaceLLMGatewayService {
	t.Helper()
	if newID == nil {
		newID = func() (string, error) { return testLLMGatewayAuthID, nil }
	}
	service, err := NewWorkspaceLLMGatewayService(WorkspaceLLMGatewayServiceConfig{
		Store: store, Sealer: testLLMGatewaySealer(t, "test", map[string]byte{"test": 0x42}),
		Providers:   &fakeWorkspaceLLMGatewayProviderFactory{provider: provider},
		RedirectURL: "https://agent.example.com/auth/llm-gateway/callback",
		NewID:       newID, Random: bytes.NewReader(bytes.Repeat([]byte{0x51}, 1024)), Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func testWorkspaceLLMGateway() coredb.WorkspaceLLMGateway {
	return coredb.WorkspaceLLMGateway{
		ID: testLLMGatewayID, WorkspaceID: testLLMGatewayWorkspaceID, Name: "test Gateway",
		ResponsesURL: "https://llm.example.com/v1/responses", OIDCIssuer: "https://id.example.com",
		OIDCClientID: "agentserver-test", OIDCScopes: "offline_access openid profile",
		BearerTokenType: coredb.LLMGatewayBearerIDToken, DefaultModel: "model-1",
		Status: coredb.LLMGatewayStatusActive, Default: true, Version: 3,
	}
}

func testWorkspaceLLMGatewayTokens(expiry time.Time, prefix string) WorkspaceLLMGatewayOIDCTokenSet {
	return WorkspaceLLMGatewayOIDCTokenSet{
		AccessToken: prefix + "-access-token", AccessTokenExpiresAt: expiry,
		IDToken: prefix + "-id-token", IDTokenExpiresAt: expiry,
		RefreshToken: prefix + "-refresh-token",
	}
}

type fakeWorkspaceLLMGatewayProviderFactory struct {
	provider      WorkspaceLLMGatewayOIDCProvider
	discoverCalls int
	discoverError error
}

func (factory *fakeWorkspaceLLMGatewayProviderFactory) Discover(context.Context, WorkspaceLLMGatewayOIDCConfig) (WorkspaceLLMGatewayOIDCProvider, error) {
	factory.discoverCalls++
	return factory.provider, factory.discoverError
}

type fakeWorkspaceLLMGatewayProvider struct {
	state            string
	nonce            string
	verifier         string
	exchangeCode     string
	exchangeVerifier string
	exchangeNonce    string
	exchangeGrant    WorkspaceLLMGatewayOIDCGrant
	refreshGrant     WorkspaceLLMGatewayOIDCGrant
	refreshError     error
	refreshCalls     int
}

func (provider *fakeWorkspaceLLMGatewayProvider) AuthorizationURL(state, nonce, verifier string) (string, error) {
	provider.state, provider.nonce, provider.verifier = state, nonce, verifier
	return "https://id.example.com/authorize?state=" + state, nil
}

func (provider *fakeWorkspaceLLMGatewayProvider) Exchange(_ context.Context, code, verifier, nonce string) (WorkspaceLLMGatewayOIDCGrant, error) {
	provider.exchangeCode, provider.exchangeVerifier, provider.exchangeNonce = code, verifier, nonce
	return provider.exchangeGrant, nil
}

func (provider *fakeWorkspaceLLMGatewayProvider) Refresh(context.Context, WorkspaceLLMGatewayOIDCTokenSet, string, string) (WorkspaceLLMGatewayOIDCGrant, error) {
	provider.refreshCalls++
	return provider.refreshGrant, provider.refreshError
}

type fakeWorkspaceLLMGatewayStore struct {
	requireOwner         func(context.Context, string, string) error
	updateGateway        func(context.Context, coredb.UpdateWorkspaceLLMGatewayCommand) (coredb.UpdateWorkspaceLLMGatewayResult, error)
	readForAuthorization func(context.Context, string, string, string) (coredb.WorkspaceLLMGateway, error)
	createTransaction    func(context.Context, coredb.CreateWorkspaceLLMGatewayAuthTransactionCommand) (coredb.WorkspaceLLMGatewayAuthTransaction, error)
	claimTransaction     func(context.Context, coredb.ClaimWorkspaceLLMGatewayAuthTransactionCommand) (coredb.WorkspaceLLMGatewayAuthTransaction, error)
	completeTransaction  func(context.Context, coredb.CompleteWorkspaceLLMGatewayAuthTransactionCommand) (coredb.WorkspaceLLMGatewayGrant, error)
	failTransaction      func(context.Context, coredb.FailWorkspaceLLMGatewayAuthTransactionCommand) (coredb.WorkspaceLLMGatewayAuthTransaction, error)
	disableGateway       func(context.Context, coredb.DisableWorkspaceLLMGatewayCommand) (coredb.DisableWorkspaceLLMGatewayResult, error)
	updateGrantTokens    func(context.Context, string, int64, []byte, time.Time) (coredb.WorkspaceLLMGatewayGrant, error)
	markReauth           func(context.Context, string, int64) error
	readLiveAuthority    func(context.Context, string, coredb.RunLLMGatewayBinding) (coredb.LLMGatewayLiveAuthority, error)
}

func (store *fakeWorkspaceLLMGatewayStore) RequireWorkspaceLLMGatewayOwner(ctx context.Context, workspaceID, userID string) error {
	return store.requireOwner(ctx, workspaceID, userID)
}

func (*fakeWorkspaceLLMGatewayStore) CreateWorkspaceLLMGateway(context.Context, coredb.CreateWorkspaceLLMGatewayCommand) (coredb.CreateWorkspaceLLMGatewayResult, error) {
	panic("unexpected CreateWorkspaceLLMGateway")
}
func (store *fakeWorkspaceLLMGatewayStore) UpdateWorkspaceLLMGateway(ctx context.Context, command coredb.UpdateWorkspaceLLMGatewayCommand) (coredb.UpdateWorkspaceLLMGatewayResult, error) {
	return store.updateGateway(ctx, command)
}
func (*fakeWorkspaceLLMGatewayStore) ListWorkspaceLLMGateways(context.Context, string, string) ([]coredb.WorkspaceLLMGateway, error) {
	panic("unexpected ListWorkspaceLLMGateways")
}
func (store *fakeWorkspaceLLMGatewayStore) ReadWorkspaceLLMGatewayForAuthorization(ctx context.Context, workspaceID, gatewayID, userID string) (coredb.WorkspaceLLMGateway, error) {
	return store.readForAuthorization(ctx, workspaceID, gatewayID, userID)
}
func (store *fakeWorkspaceLLMGatewayStore) CreateWorkspaceLLMGatewayAuthTransaction(ctx context.Context, command coredb.CreateWorkspaceLLMGatewayAuthTransactionCommand) (coredb.WorkspaceLLMGatewayAuthTransaction, error) {
	return store.createTransaction(ctx, command)
}
func (store *fakeWorkspaceLLMGatewayStore) ClaimWorkspaceLLMGatewayAuthTransaction(ctx context.Context, command coredb.ClaimWorkspaceLLMGatewayAuthTransactionCommand) (coredb.WorkspaceLLMGatewayAuthTransaction, error) {
	return store.claimTransaction(ctx, command)
}
func (store *fakeWorkspaceLLMGatewayStore) CompleteWorkspaceLLMGatewayAuthTransaction(ctx context.Context, command coredb.CompleteWorkspaceLLMGatewayAuthTransactionCommand) (coredb.WorkspaceLLMGatewayGrant, error) {
	return store.completeTransaction(ctx, command)
}
func (store *fakeWorkspaceLLMGatewayStore) FailWorkspaceLLMGatewayAuthTransaction(ctx context.Context, command coredb.FailWorkspaceLLMGatewayAuthTransactionCommand) (coredb.WorkspaceLLMGatewayAuthTransaction, error) {
	return store.failTransaction(ctx, command)
}
func (*fakeWorkspaceLLMGatewayStore) RevokeWorkspaceLLMGatewayGrant(context.Context, coredb.RevokeWorkspaceLLMGatewayGrantCommand) (coredb.RevokeWorkspaceLLMGatewayGrantResult, error) {
	panic("unexpected RevokeWorkspaceLLMGatewayGrant")
}
func (store *fakeWorkspaceLLMGatewayStore) DisableWorkspaceLLMGateway(ctx context.Context, command coredb.DisableWorkspaceLLMGatewayCommand) (coredb.DisableWorkspaceLLMGatewayResult, error) {
	return store.disableGateway(ctx, command)
}
func (store *fakeWorkspaceLLMGatewayStore) UpdateWorkspaceLLMGatewayGrantTokens(ctx context.Context, grantID string, version int64, sealed []byte, expiry time.Time) (coredb.WorkspaceLLMGatewayGrant, error) {
	return store.updateGrantTokens(ctx, grantID, version, sealed, expiry)
}
func (store *fakeWorkspaceLLMGatewayStore) MarkWorkspaceLLMGatewayGrantReauthRequired(ctx context.Context, grantID string, version int64) error {
	return store.markReauth(ctx, grantID, version)
}
func (store *fakeWorkspaceLLMGatewayStore) ReadWorkspaceLLMGatewayLiveAuthority(ctx context.Context, workspaceID string, binding coredb.RunLLMGatewayBinding) (coredb.LLMGatewayLiveAuthority, error) {
	return store.readLiveAuthority(ctx, workspaceID, binding)
}
