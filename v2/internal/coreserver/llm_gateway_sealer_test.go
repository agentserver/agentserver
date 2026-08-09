package coreserver

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestLLMGatewayGrantSealerAuthenticatesPurposeAndAuthorityScope(t *testing.T) {
	sealer := testLLMGatewaySealer(t, "new", map[string]byte{"new": 0x31})
	scope := LLMGatewaySealScope{
		WorkspaceID: "91000000-0000-4000-8000-000000000001",
		GatewayID:   "91000000-0000-4000-8000-000000000002",
		UserID:      "91000000-0000-4000-8000-000000000003", GatewayVersion: 4,
		TransactionID: "91000000-0000-4000-8000-000000000004",
	}
	sealed, err := sealer.SealAuthorizationSecrets(scope, []byte(`{"nonce":"secret"}`))
	if err != nil {
		t.Fatal(err)
	}
	opened, err := sealer.OpenAuthorizationSecrets(scope, sealed)
	if err != nil || string(opened) != `{"nonce":"secret"}` {
		t.Fatalf("open authorization secrets = %q, %v", opened, err)
	}
	for _, mutate := range []func(*LLMGatewaySealScope){
		func(value *LLMGatewaySealScope) { value.WorkspaceID = "91000000-0000-4000-8000-000000000009" },
		func(value *LLMGatewaySealScope) { value.GatewayID = "91000000-0000-4000-8000-000000000009" },
		func(value *LLMGatewaySealScope) { value.UserID = "91000000-0000-4000-8000-000000000009" },
		func(value *LLMGatewaySealScope) { value.GatewayVersion++ },
		func(value *LLMGatewaySealScope) { value.TransactionID = "91000000-0000-4000-8000-000000000009" },
	} {
		changed := scope
		mutate(&changed)
		if _, err := sealer.OpenAuthorizationSecrets(changed, sealed); err == nil {
			t.Fatal("sealed authorization secrets were accepted in a different authority scope")
		}
	}
	grantScope := scope
	grantScope.TransactionID = ""
	if _, err := sealer.OpenGrantTokenSet(grantScope, sealed); err == nil {
		t.Fatal("authorization ciphertext was accepted as a grant token set")
	}
}

func TestLLMGatewayGrantSealerSupportsExplicitRotationOverlap(t *testing.T) {
	old := testLLMGatewaySealer(t, "old", map[string]byte{"old": 0x21})
	scope := LLMGatewaySealScope{
		WorkspaceID: "92000000-0000-4000-8000-000000000001",
		GatewayID:   "92000000-0000-4000-8000-000000000002",
		UserID:      "92000000-0000-4000-8000-000000000003", GatewayVersion: 1,
	}
	sealed, err := old.SealGrantTokenSet(scope, []byte("token-set"))
	if err != nil {
		t.Fatal(err)
	}
	overlap := testLLMGatewaySealer(t, "new", map[string]byte{"old": 0x21, "new": 0x22})
	opened, err := overlap.OpenGrantTokenSet(scope, sealed)
	if err != nil || string(opened) != "token-set" {
		t.Fatalf("open with overlap = %q, %v", opened, err)
	}
	newSealed, err := overlap.SealGrantTokenSet(scope, []byte("new-token-set"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(sealed, newSealed) || !bytes.Contains(newSealed, []byte("new")) {
		t.Fatal("rotation did not select the active key ID")
	}
	withoutOld := testLLMGatewaySealer(t, "new", map[string]byte{"new": 0x22})
	if _, err := withoutOld.OpenGrantTokenSet(scope, sealed); err == nil || !strings.Contains(err.Error(), "rotation key") {
		t.Fatalf("removed rotation key error = %v", err)
	}
}

func TestLarkGrantSealerAuthenticatesScopeAndDomain(t *testing.T) {
	sealer := testLLMGatewaySealer(t, "active", map[string]byte{"active": 0x51})
	scope := LarkGrantSealScope{
		WorkspaceID:  "93000000-0000-4000-8000-000000000001",
		GrantID:      "93000000-0000-4000-8000-000000000002",
		UserID:       "93000000-0000-4000-8000-000000000003",
		PolicySHA256: [32]byte{1, 2, 3, 4},
	}
	accessExpiry := time.Date(2026, 8, 7, 1, 0, 0, 0, time.UTC)
	refreshExpiry := accessExpiry.Add(30 * 24 * time.Hour)
	tokens := LarkGrantTokenSet{
		Version: 1, AccessToken: "u-lark-access-token", RefreshToken: "u-lark-refresh-token",
		AccessExpiresAt: accessExpiry, RefreshExpiresAt: &refreshExpiry,
	}
	sealed, err := sealer.SealLarkGrantTokenSet(scope, tokens)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := sealer.OpenLarkGrantTokenSet(scope, sealed)
	if err != nil || opened.AccessToken != tokens.AccessToken || opened.RefreshToken != tokens.RefreshToken ||
		!opened.AccessExpiresAt.Equal(accessExpiry) || opened.RefreshExpiresAt == nil || !opened.RefreshExpiresAt.Equal(refreshExpiry) {
		t.Fatalf("opened Lark grant = %#v, %v", opened, err)
	}
	for _, mutate := range []func(*LarkGrantSealScope){
		func(value *LarkGrantSealScope) { value.WorkspaceID = "93000000-0000-4000-8000-000000000009" },
		func(value *LarkGrantSealScope) { value.GrantID = "93000000-0000-4000-8000-000000000009" },
		func(value *LarkGrantSealScope) { value.UserID = "93000000-0000-4000-8000-000000000009" },
		func(value *LarkGrantSealScope) { value.PolicySHA256[0]++ },
	} {
		changed := scope
		mutate(&changed)
		if _, err := sealer.OpenLarkGrantTokenSet(changed, sealed); err == nil {
			t.Fatal("sealed Lark grant was accepted in a different authority scope")
		}
	}
	llmScope := LLMGatewaySealScope{
		WorkspaceID: scope.WorkspaceID, GatewayID: scope.GrantID,
		UserID: scope.UserID, GatewayVersion: 1,
	}
	if _, err := sealer.OpenGrantTokenSet(llmScope, sealed); err == nil {
		t.Fatal("Lark ciphertext was accepted in the LLM gateway sealing domain")
	}
}

func testLLMGatewaySealer(t *testing.T, active string, keys map[string]byte) *LLMGatewayGrantSealer {
	t.Helper()
	document := LLMGatewaySealingKeyringDocument{Version: 1, ActiveKeyID: active}
	for id, fill := range keys {
		document.Keys = append(document.Keys, LLMGatewaySealingKeyDocument{
			KeyID: id, Algorithm: LLMGatewaySealingAlgorithm,
			Key: base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{fill}, 32)),
		})
	}
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	sealer, err := ParseLLMGatewayGrantSealer(raw)
	if err != nil {
		t.Fatal(err)
	}
	sealer.random = bytes.NewReader(bytes.Repeat([]byte{0x41}, 1024))
	return sealer
}
