package coreserver

import (
	"context"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/coredb"
	"github.com/agentserver/agentserver/v2/internal/executorenrollment"
)

const executorOAuthTestIssuer = "https://hydra.example/"

func TestExecutorOAuthAuthorizerIntrospectsAndReadsLiveMachineAuthorityEveryTime(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	clientID := "agentserver-executor-" + enrollmentTestExecutor
	introspector := &recordingExecutorIntrospector{result: validExecutorIntrospection(now, clientID)}
	publicKey := [32]byte{1, 2, 3}
	machineHash := sha256.Sum256(publicKey[:])
	oauthX, oauthY, oauthHash := executorOAuthTestKey()
	authority := coredb.ExecutorMachineAuthority{
		ExecutorID: enrollmentTestExecutor, WorkspaceID: enrollmentTestWorkspace, OAuthClientID: clientID,
		MachinePublicKeyEd25519: publicKey, MachineKeySHA256: machineHash,
		OAuthPublicKeyP256X: oauthX, OAuthPublicKeyP256Y: oauthY, OAuthKeySHA256: oauthHash,
		ExecutorVersion: 7, AuthorizedAt: now,
	}
	store := &recordingExecutorOAuthStore{result: authority}
	hydra := hydraReaderForExecutorAuthority(authority)
	authorizer, err := NewExecutorOAuthAuthorizer(ExecutorOAuthAuthorizerConfig{
		Introspector: introspector, Store: store, Hydra: hydra,
		ExpectedIssuer: executorOAuthTestIssuer, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		result, err := authorizer.Authorize(t.Context(), "opaque-executor-token")
		if err != nil {
			t.Fatal(err)
		}
		if result.ExecutorID != enrollmentTestExecutor || result.OAuthClientID != clientID || result.ExecutorVersion != 7 || result.MachinePublicKeyEd25519 == "" || !result.AuthorizedAt.Equal(now) {
			t.Fatalf("executor OAuth authority = %+v", result)
		}
	}
	if len(introspector.tokens) != 2 || len(store.clients) != 2 || len(hydra.gets) != 2 {
		t.Fatalf("introspection/store/Hydra calls = %d/%d/%d", len(introspector.tokens), len(store.clients), len(hydra.gets))
	}
}

func TestExecutorOAuthAuthorizerRejectsMixedAuthorityBeforeDatabase(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 30, 0, 0, time.UTC)
	mutations := map[string]func(*UserTokenIntrospection){
		"inactive":          func(value *UserTokenIntrospection) { value.Active = false },
		"missing audience":  func(value *UserTokenIntrospection) { value.Audience = nil },
		"mixed audience":    func(value *UserTokenIntrospection) { value.Audience = append(value.Audience, "agentserver-api") },
		"wrong scope":       func(value *UserTokenIntrospection) { value.Scope = "runs:write" },
		"extra scope":       func(value *UserTokenIntrospection) { value.Scope += " runs:write" },
		"expired":           func(value *UserTokenIntrospection) { value.ExpiresAt = now.Unix() },
		"long lived":        func(value *UserTokenIntrospection) { value.ExpiresAt = value.IssuedAt + 3600 },
		"one second over":   func(value *UserTokenIntrospection) { value.ExpiresAt = value.IssuedAt + 301 },
		"wrong issuer":      func(value *UserTokenIntrospection) { value.Issuer = "https://other.example/" },
		"missing client ID": func(value *UserTokenIntrospection) { value.ClientID = ""; value.Subject = "" },
		"wrong subject":     func(value *UserTokenIntrospection) { value.Subject = "another-client" },
		"wrong token type":  func(value *UserTokenIntrospection) { value.TokenType = "DPoP" },
		"wrong token use":   func(value *UserTokenIntrospection) { value.TokenUse = "refresh_token" },
		"missing issued":    func(value *UserTokenIntrospection) { value.IssuedAt = 0; value.NotBefore = 0 },
		"not-before drift":  func(value *UserTokenIntrospection) { value.NotBefore-- },
		"future issued": func(value *UserTokenIntrospection) {
			value.IssuedAt = now.Add(time.Minute).Unix()
			value.NotBefore = value.IssuedAt
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			introspection := validExecutorIntrospection(now, "client")
			mutate(&introspection)
			store := &recordingExecutorOAuthStore{}
			authorizer, _ := NewExecutorOAuthAuthorizer(ExecutorOAuthAuthorizerConfig{
				Introspector: &recordingExecutorIntrospector{result: introspection}, Store: store,
				Hydra:          &recordingHydraExecutorAdmin{},
				ExpectedIssuer: executorOAuthTestIssuer, Now: func() time.Time { return now },
			})
			if _, err := authorizer.Authorize(t.Context(), "token"); err == nil || len(store.clients) != 0 {
				t.Fatalf("invalid authority error/store calls = %v/%d", err, len(store.clients))
			}
		})
	}
	store := &recordingExecutorOAuthStore{}
	authorizer, _ := NewExecutorOAuthAuthorizer(ExecutorOAuthAuthorizerConfig{
		Introspector: &recordingExecutorIntrospector{}, Store: store, Hydra: &recordingHydraExecutorAdmin{},
		ExpectedIssuer: executorOAuthTestIssuer, Now: func() time.Time { return now },
	})
	if _, err := authorizer.Authorize(t.Context(), " padded"); err == nil || len(store.clients) != 0 {
		t.Fatalf("padded bearer error/store calls = %v/%d", err, len(store.clients))
	}
}

func TestExecutorOAuthAuthorizerRejectsInconsistentStoredDualKeyAuthority(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 45, 0, 0, time.UTC)
	clientID := "agentserver-executor-" + enrollmentTestExecutor
	machineKey := [32]byte{1}
	oauthX, oauthY, oauthHash := executorOAuthTestKey()
	base := coredb.ExecutorMachineAuthority{
		ExecutorID: enrollmentTestExecutor, WorkspaceID: enrollmentTestWorkspace, OAuthClientID: clientID,
		MachinePublicKeyEd25519: machineKey, MachineKeySHA256: sha256.Sum256(machineKey[:]),
		OAuthPublicKeyP256X: oauthX, OAuthPublicKeyP256Y: oauthY, OAuthKeySHA256: oauthHash,
		ExecutorVersion: 1, AuthorizedAt: now,
	}
	mutations := map[string]func(*coredb.ExecutorMachineAuthority){
		"client": func(value *coredb.ExecutorMachineAuthority) { value.OAuthClientID = "other" },
		"zero machine key": func(value *coredb.ExecutorMachineAuthority) {
			value.MachinePublicKeyEd25519 = [32]byte{}
			value.MachineKeySHA256 = sha256.Sum256(make([]byte, 32))
		},
		"machine fingerprint": func(value *coredb.ExecutorMachineAuthority) { value.MachineKeySHA256[0] ^= 1 },
		"OAuth point":         func(value *coredb.ExecutorMachineAuthority) { value.OAuthPublicKeyP256X = [32]byte{} },
		"OAuth fingerprint":   func(value *coredb.ExecutorMachineAuthority) { value.OAuthKeySHA256[0] ^= 1 },
		"version":             func(value *coredb.ExecutorMachineAuthority) { value.ExecutorVersion = 0 },
		"authorization time":  func(value *coredb.ExecutorMachineAuthority) { value.AuthorizedAt = time.Time{} },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			authority := base
			mutate(&authority)
			authorizer, err := NewExecutorOAuthAuthorizer(ExecutorOAuthAuthorizerConfig{
				Introspector: &recordingExecutorIntrospector{result: validExecutorIntrospection(now, clientID)},
				Store:        &recordingExecutorOAuthStore{result: authority}, Hydra: &recordingHydraExecutorAdmin{},
				ExpectedIssuer: executorOAuthTestIssuer,
				Now:            func() time.Time { return now },
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := authorizer.Authorize(t.Context(), "token"); err == nil {
				t.Fatal("inconsistent stored executor authority was accepted")
			}
		})
	}
}

func TestExecutorOAuthAuthorizerRequiresExactIssuerConfiguration(t *testing.T) {
	for _, issuer := range []string{"", "hydra.example", "https://hydra.example/?query", "https://user@hydra.example/", " https://hydra.example/"} {
		if authorizer, err := NewExecutorOAuthAuthorizer(ExecutorOAuthAuthorizerConfig{
			Introspector: &recordingExecutorIntrospector{}, Store: &recordingExecutorOAuthStore{},
			Hydra: &recordingHydraExecutorAdmin{}, ExpectedIssuer: issuer,
		}); err == nil || authorizer != nil {
			t.Fatalf("invalid issuer %q was accepted", issuer)
		}
	}
}

func TestExecutorOAuthAuthorizerRejectsHydraClientDriftAndReadFailure(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 50, 0, 0, time.UTC)
	clientID := "agentserver-executor-" + enrollmentTestExecutor
	machineKey := [32]byte{1}
	oauthX, oauthY, oauthHash := executorOAuthTestKey()
	authority := coredb.ExecutorMachineAuthority{
		ExecutorID: enrollmentTestExecutor, WorkspaceID: enrollmentTestWorkspace, OAuthClientID: clientID,
		MachinePublicKeyEd25519: machineKey, MachineKeySHA256: sha256.Sum256(machineKey[:]),
		OAuthPublicKeyP256X: oauthX, OAuthPublicKeyP256Y: oauthY, OAuthKeySHA256: oauthHash,
		ExecutorVersion: 1, AuthorizedAt: now,
	}
	for name, hydra := range map[string]*recordingHydraExecutorAdmin{
		"drift": hydraReaderForExecutorAuthority(authority),
		"deleted": {
			getErr: &HydraAdminError{StatusCode: 404, Operation: "get executor OAuth client"},
		},
		"read failure": {getErr: errTestHydraExecutorRead},
	} {
		t.Run(name, func(t *testing.T) {
			if name == "drift" {
				hydra.getResult.Scope = "executor:connect runs:write"
			}
			authorizer, err := NewExecutorOAuthAuthorizer(ExecutorOAuthAuthorizerConfig{
				Introspector: &recordingExecutorIntrospector{result: validExecutorIntrospection(now, clientID)},
				Store:        &recordingExecutorOAuthStore{result: authority}, Hydra: hydra,
				ExpectedIssuer: executorOAuthTestIssuer, Now: func() time.Time { return now },
			})
			if err != nil {
				t.Fatal(err)
			}
			_, authorizeErr := authorizer.Authorize(t.Context(), "token")
			if authorizeErr == nil || len(hydra.gets) != 1 {
				t.Fatalf("Hydra %s authority error/reads = %v/%d", name, authorizeErr, len(hydra.gets))
			}
			if (name == "drift" || name == "deleted") != coredb.HasStateErrorCode(authorizeErr, coredb.ErrorForbidden) {
				t.Fatalf("Hydra %s error class = %v", name, authorizeErr)
			}
		})
	}
}

func validExecutorIntrospection(now time.Time, clientID string) UserTokenIntrospection {
	issuedAt := now.Add(-time.Second).Unix()
	return UserTokenIntrospection{
		Active: true, Subject: clientID, ClientID: clientID, Audience: []string{ExecutorOAuthAudience}, Scope: ExecutorOAuthScope,
		ExpiresAt: issuedAt + int64(ExecutorOAuthAccessTokenLifespan/time.Second), IssuedAt: issuedAt, NotBefore: issuedAt,
		Issuer: executorOAuthTestIssuer, TokenType: "Bearer", TokenUse: "access_token",
	}
}

func executorOAuthTestKey() ([32]byte, [32]byte, [32]byte) {
	x, y := elliptic.P256().ScalarBaseMult([]byte{1})
	var encodedX, encodedY [32]byte
	x.FillBytes(encodedX[:])
	y.FillBytes(encodedY[:])
	thumbprint := executorenrollment.OAuthJWKThumbprint(
		base64.RawURLEncoding.EncodeToString(encodedX[:]), base64.RawURLEncoding.EncodeToString(encodedY[:]),
	)
	return encodedX, encodedY, thumbprint
}

var errTestHydraExecutorRead = errors.New("Hydra executor client read failed")

func hydraReaderForExecutorAuthority(authority coredb.ExecutorMachineAuthority) *recordingHydraExecutorAdmin {
	return &recordingHydraExecutorAdmin{getResult: executorOAuthClientDocument(
		authority.OAuthClientID, authority.ExecutorID,
		authority.OAuthPublicKeyP256X, authority.OAuthPublicKeyP256Y, authority.OAuthKeySHA256,
	)}
}

type recordingExecutorIntrospector struct {
	tokens []string
	result UserTokenIntrospection
	err    error
}

func (introspector *recordingExecutorIntrospector) IntrospectUserToken(_ context.Context, token string) (UserTokenIntrospection, error) {
	introspector.tokens = append(introspector.tokens, token)
	return introspector.result, introspector.err
}

type recordingExecutorOAuthStore struct {
	clients []string
	result  coredb.ExecutorMachineAuthority
	err     error
}

func (store *recordingExecutorOAuthStore) AuthorizeExecutorOAuthClient(_ context.Context, clientID string) (coredb.ExecutorMachineAuthority, error) {
	store.clients = append(store.clients, clientID)
	return store.result, store.err
}
