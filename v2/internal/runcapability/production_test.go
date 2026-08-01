package runcapability

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"
)

const (
	productionTestIssuer = "spiffe://agentserver.test/ns/agentserver/sa/agentserver-core"
	productionTestKeyID  = "run-capability-2026-08"
)

var productionTestNow = time.UnixMilli(1_800_000_000_000).UTC()

func TestProductionCapabilityRoundTripsAudienceSeparatedAuthority(t *testing.T) {
	signer, verifier := productionTestCodecs(t, 0x81)
	for _, audience := range []string{AudienceExecutorMCP, AudienceLLMProxy} {
		t.Run(audience, func(t *testing.T) {
			claims := productionTestClaims(audience)
			token, err := signer.Sign(claims)
			if err != nil {
				t.Fatal(err)
			}
			verified, err := verifier.Verify(token, audience, productionTestNow)
			if err != nil {
				t.Fatal(err)
			}
			if verified != claims || !strings.HasPrefix(token, productionTokenPrefix+".") || strings.Contains(token, productionTestKeyID) {
				t.Fatalf("verified production claims = %+v, token framing %q", verified, token)
			}
			if _, err := verifier.Verify(token, otherProductionAudience(audience), productionTestNow); err == nil {
				t.Fatal("production capability crossed its audience")
			}
		})
	}
}

func TestProductionCapabilityRejectsWrongIssuerKeyTamperingAndDevelopmentTokens(t *testing.T) {
	signer, verifier := productionTestCodecs(t, 0x82)
	claims := productionTestClaims(AudienceExecutorMCP)
	token, err := signer.Sign(claims)
	if err != nil {
		t.Fatal(err)
	}

	wrongIssuer, err := NewProductionVerifier(
		"spiffe://other.test/ns/agentserver/sa/core",
		map[string]ed25519.PublicKey{productionTestKeyID: signer.privateKey.Public().(ed25519.PublicKey)},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wrongIssuer.Verify(token, claims.Audience, productionTestNow); err == nil {
		t.Fatal("production capability crossed issuer authority")
	}

	unknownKey, err := NewProductionVerifier(
		productionTestIssuer,
		map[string]ed25519.PublicKey{"another-key": signer.privateKey.Public().(ed25519.PublicKey)},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := unknownKey.Verify(token, claims.Audience, productionTestNow); err == nil {
		t.Fatal("unknown production signing key fell back to another key")
	}

	_, otherVerifier := productionTestCodecs(t, 0x83)
	if _, err := otherVerifier.Verify(token, claims.Audience, productionTestNow); err == nil {
		t.Fatal("production capability signed by another key was accepted")
	}

	parts := strings.Split(token, ".")
	canonical, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatal(err)
	}
	canonical[len(canonical)/2] ^= 1
	parts[2] = base64.RawURLEncoding.EncodeToString(canonical)
	if _, err := verifier.Verify(strings.Join(parts, "."), claims.Audience, productionTestNow); err == nil {
		t.Fatal("tampered production claims were accepted")
	}

	development := newDevelopmentTestCodec(t, 0x84)
	developmentToken, err := development.Sign(developmentTestClaims(AudienceExecutorMCP))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.Verify(developmentToken, AudienceExecutorMCP, productionTestNow); err == nil {
		t.Fatal("development HMAC token was accepted by production verifier")
	}
	developmentClaims := developmentTestClaims(AudienceExecutorMCP)
	developmentClaims.Issuer = productionTestIssuer
	if _, err := development.Sign(developmentClaims); err == nil {
		t.Fatal("development token accepted production issuer authority")
	}
}

func TestProductionCapabilityEnforcesValidityWindowAndClosedWorldCanonicalClaims(t *testing.T) {
	signer, verifier := productionTestCodecs(t, 0x85)
	claims := productionTestClaims(AudienceExecutorMCP)
	token, err := signer.Sign(claims)
	if err != nil {
		t.Fatal(err)
	}
	for name, now := range map[string]time.Time{
		"zero": {}, "before issue": time.UnixMilli(claims.IssuedAtUnixMS - 1),
		"at expiry": time.UnixMilli(claims.ExpiresAtUnixMS), "after expiry": time.UnixMilli(claims.ExpiresAtUnixMS + 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := verifier.Verify(token, claims.Audience, now); err == nil {
				t.Fatal("production capability was accepted outside its validity window")
			}
		})
	}

	canonicalUnknown := []byte(fmt.Sprintf(
		`{"actorId":"%s","audience":"%s","capabilityId":"%s","executorId":"%s","expectedRunAttemptVersion":5,"expectedRunVersion":4,"expiresAtUnixMs":%d,"holderId":"%s","issuer":"%s","issuedAtUnixMs":%d,"maxApprovalTtlMs":60000,"runAttemptGeneration":3,"runAttemptId":"%s","runDeadlineUnixMs":%d,"runId":"%s","sessionId":"%s","toolCatalogDigest":"%s","unknown":true,"version":1,"workspaceId":"%s"}`,
		claims.ActorID, claims.Audience, claims.CapabilityID, claims.ExecutorID, claims.ExpiresAtUnixMS,
		claims.HolderID, claims.Issuer, claims.IssuedAtUnixMS, claims.RunAttemptID, claims.RunDeadlineUnixMS,
		claims.RunID, claims.SessionID, claims.ToolCatalogDigest, claims.WorkspaceID,
	))
	for name, raw := range map[string][]byte{
		"unknown":      canonicalUnknown,
		"duplicate":    bytes.Replace(canonicalUnknown, []byte(`"unknown":true,`), []byte(`"actorId":"duplicate",`), 1),
		"noncanonical": append([]byte(" "), canonicalUnknown...),
	} {
		t.Run(name, func(t *testing.T) {
			rawToken := rawProductionToken(signer, raw)
			if _, err := verifier.Verify(rawToken, claims.Audience, productionTestNow); err == nil {
				t.Fatal("open-world or noncanonical production claims were accepted")
			}
		})
	}

	for name, malformed := range map[string]string{
		"empty": "", "development prefix": "asv2dev1.a.b", "padded": token + " ",
		"extra segment": token + ".extra", "padded base64": strings.Replace(token, ".", ".=", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := verifier.Verify(malformed, claims.Audience, productionTestNow); err == nil {
				t.Fatal("malformed production token was accepted")
			}
		})
	}
}

func TestProductionSignerAndVerifierValidateAndCopyKeys(t *testing.T) {
	seed := bytesOf(0x86, ed25519.SeedSize)
	privateKey := ed25519.NewKeyFromSeed(seed)
	publicKey := append(ed25519.PublicKey(nil), privateKey.Public().(ed25519.PublicKey)...)
	signer, err := NewProductionSigner(productionTestIssuer, productionTestKeyID, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := NewProductionVerifier(productionTestIssuer, map[string]ed25519.PublicKey{productionTestKeyID: publicKey})
	if err != nil {
		t.Fatal(err)
	}
	clear(privateKey)
	clear(publicKey)
	token, err := signer.Sign(productionTestClaims(AudienceLLMProxy))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.Verify(token, AudienceLLMProxy, productionTestNow); err != nil {
		t.Fatal(err)
	}

	invalidPrivate := ed25519.NewKeyFromSeed(bytesOf(0x87, ed25519.SeedSize))
	invalidPrivate[len(invalidPrivate)-1] ^= 1
	for _, test := range []struct {
		name, issuer, keyID string
		key                 ed25519.PrivateKey
	}{
		{name: "issuer", issuer: " issuer", keyID: productionTestKeyID, key: signer.privateKey},
		{name: "key ID", issuer: productionTestIssuer, keyID: "key\n", key: signer.privateKey},
		{name: "size", issuer: productionTestIssuer, keyID: productionTestKeyID, key: make([]byte, 63)},
		{name: "zero", issuer: productionTestIssuer, keyID: productionTestKeyID, key: ed25519.NewKeyFromSeed(make([]byte, 32))},
		{name: "noncanonical", issuer: productionTestIssuer, keyID: productionTestKeyID, key: invalidPrivate},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewProductionSigner(test.issuer, test.keyID, test.key); err == nil {
				t.Fatal("invalid production signer was accepted")
			}
		})
	}

	tooMany := make(map[string]ed25519.PublicKey, maximumTrustedKeys+1)
	for index := 0; index <= maximumTrustedKeys; index++ {
		tooMany[fmt.Sprintf("key-%02d", index)] = signer.privateKey.Public().(ed25519.PublicKey)
	}
	for _, test := range []struct {
		name, issuer string
		keys         map[string]ed25519.PublicKey
	}{
		{name: "issuer", issuer: "", keys: map[string]ed25519.PublicKey{productionTestKeyID: signer.privateKey.Public().(ed25519.PublicKey)}},
		{name: "empty", issuer: productionTestIssuer, keys: map[string]ed25519.PublicKey{}},
		{name: "too many", issuer: productionTestIssuer, keys: tooMany},
		{name: "bad key ID", issuer: productionTestIssuer, keys: map[string]ed25519.PublicKey{" bad": signer.privateKey.Public().(ed25519.PublicKey)}},
		{name: "bad key", issuer: productionTestIssuer, keys: map[string]ed25519.PublicKey{productionTestKeyID: make([]byte, 31)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewProductionVerifier(test.issuer, test.keys); err == nil {
				t.Fatal("invalid production verifier was accepted")
			}
		})
	}
}

func TestProductionClaimsRejectCrossAudienceAndSignerAuthority(t *testing.T) {
	signer, _ := productionTestCodecs(t, 0x88)
	executor := productionTestClaims(AudienceExecutorMCP)
	model := productionTestClaims(AudienceLLMProxy)
	for name, claims := range map[string]Claims{
		"executor with model":  func() Claims { value := executor; value.Model = "gpt-5"; return value }(),
		"model with executor":  func() Claims { value := model; value.ExecutorID = executor.ExecutorID; return value }(),
		"unsupported audience": func() Claims { value := executor; value.Audience = "somewhere-else"; return value }(),
		"wrong issuer":         func() Claims { value := executor; value.Issuer = "spiffe://other.test/core"; return value }(),
		"missing issuer":       func() Claims { value := executor; value.Issuer = ""; return value }(),
		"control actor":        func() Claims { value := executor; value.ActorID = "actor\nforged"; return value }(),
		"control holder":       func() Claims { value := executor; value.HolderID = "holder\tforged"; return value }(),
		"control model":        func() Claims { value := model; value.Model = "gpt-5\rforged"; return value }(),
		"control provider":     func() Claims { value := model; value.Provider = "provider\nforged"; return value }(),
		"wrong version":        func() Claims { value := executor; value.Version = 2; return value }(),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := signer.Sign(claims); err == nil {
				t.Fatal("invalid production capability authority was accepted")
			}
		})
	}
}

func productionTestCodecs(t *testing.T, fill byte) (*ProductionSigner, *ProductionVerifier) {
	t.Helper()
	privateKey := ed25519.NewKeyFromSeed(bytesOf(fill, ed25519.SeedSize))
	signer, err := NewProductionSigner(productionTestIssuer, productionTestKeyID, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := NewProductionVerifier(
		productionTestIssuer,
		map[string]ed25519.PublicKey{productionTestKeyID: privateKey.Public().(ed25519.PublicKey)},
	)
	if err != nil {
		t.Fatal(err)
	}
	return signer, verifier
}

func productionTestClaims(audience string) Claims {
	claims := developmentTestClaims(audience)
	claims.Version = ProductionVersion
	claims.Issuer = productionTestIssuer
	claims.HolderID = "production-pool-holder"
	if audience == AudienceLLMProxy {
		claims.Provider = "llmproxy"
	}
	return claims
}

func otherProductionAudience(audience string) string {
	if audience == AudienceExecutorMCP {
		return AudienceLLMProxy
	}
	return AudienceExecutorMCP
}

func rawProductionToken(signer *ProductionSigner, raw []byte) string {
	signature := ed25519.Sign(signer.privateKey, productionSignatureMessage(signer.keyID, raw))
	return strings.Join([]string{
		productionTokenPrefix,
		base64.RawURLEncoding.EncodeToString([]byte(signer.keyID)),
		base64.RawURLEncoding.EncodeToString(raw),
		base64.RawURLEncoding.EncodeToString(signature),
	}, ".")
}
