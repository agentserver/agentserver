package runcapability

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

var developmentTestNow = time.UnixMilli(1_800_000_000_000).UTC()

func TestDevelopmentCodecRoundTripsAudienceSeparatedClaims(t *testing.T) {
	codec := newDevelopmentTestCodec(t, 0x31)
	executorClaims := developmentTestClaims(AudienceExecutorMCP)
	executorToken, err := codec.Sign(executorClaims)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := codec.Sign(executorClaims)
	if err != nil {
		t.Fatal(err)
	}
	if repeated != executorToken {
		t.Fatal("signing identical development claims was not deterministic")
	}
	verified, err := codec.Verify(executorToken, AudienceExecutorMCP, developmentTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if verified != executorClaims {
		t.Fatalf("verified executor claims = %#v, want %#v", verified, executorClaims)
	}
	if _, err := codec.Verify(executorToken, AudienceLLMProxy, developmentTestNow); err == nil {
		t.Fatal("executor capability was accepted for the model audience")
	}

	modelClaims := developmentTestClaims(AudienceLLMProxy)
	modelToken, err := codec.Sign(modelClaims)
	if err != nil {
		t.Fatal(err)
	}
	if modelToken == executorToken {
		t.Fatal("audience-separated capabilities are identical")
	}
	verified, err = codec.Verify(modelToken, AudienceLLMProxy, developmentTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if verified != modelClaims {
		t.Fatalf("verified model claims = %#v, want %#v", verified, modelClaims)
	}
}

func TestDevelopmentCodecRejectsTamperingAndWrongKey(t *testing.T) {
	codec := newDevelopmentTestCodec(t, 0x41)
	token, err := codec.Sign(developmentTestClaims(AudienceExecutorMCP))
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token framing = %q", token)
	}
	tamperedClaims := parts[1]
	if tamperedClaims[0] == 'A' {
		tamperedClaims = "B" + tamperedClaims[1:]
	} else {
		tamperedClaims = "A" + tamperedClaims[1:]
	}
	if _, err := codec.Verify(parts[0]+"."+tamperedClaims+"."+parts[2], AudienceExecutorMCP, developmentTestNow); err == nil {
		t.Fatal("tampered development claims were accepted")
	}

	tamperedSignature := append([]byte(nil), parts[2]...)
	if tamperedSignature[len(tamperedSignature)-1] == 'A' {
		tamperedSignature[len(tamperedSignature)-1] = 'B'
	} else {
		tamperedSignature[len(tamperedSignature)-1] = 'A'
	}
	if _, err := codec.Verify(parts[0]+"."+parts[1]+"."+string(tamperedSignature), AudienceExecutorMCP, developmentTestNow); err == nil {
		t.Fatal("tampered development signature was accepted")
	}

	otherCodec := newDevelopmentTestCodec(t, 0x42)
	if _, err := otherCodec.Verify(token, AudienceExecutorMCP, developmentTestNow); err == nil {
		t.Fatal("development capability was accepted under another key")
	}
}

func TestDevelopmentCodecRejectsNonCanonicalOrOpenWorldClaims(t *testing.T) {
	codec := newDevelopmentTestCodec(t, 0x51)
	claims := developmentTestClaims(AudienceExecutorMCP)
	canonicalToken, err := codec.Sign(claims)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(canonicalToken, ".")
	canonical, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}

	var openWorld map[string]any
	if err := json.Unmarshal(canonical, &openWorld); err != nil {
		t.Fatal(err)
	}
	openWorld["unknownAuthority"] = "must-not-be-ignored"
	unknown, err := json.Marshal(openWorld)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := codec.Verify(developmentRawToken(codec, unknown), AudienceExecutorMCP, developmentTestNow); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown-field error = %v", err)
	}

	duplicate := append([]byte{'{'}, []byte(`"actorId":"duplicate",`)...)
	duplicate = append(duplicate, canonical[1:]...)
	if _, err := codec.Verify(developmentRawToken(codec, duplicate), AudienceExecutorMCP, developmentTestNow); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate-key error = %v", err)
	}

	noncanonical := append(append([]byte(nil), canonical...), '\n')
	if _, err := codec.Verify(developmentRawToken(codec, noncanonical), AudienceExecutorMCP, developmentTestNow); err == nil || !strings.Contains(err.Error(), "not canonical") {
		t.Fatalf("noncanonical-JSON error = %v", err)
	}

	paddedClaims := parts[1] + "="
	if _, err := codec.Verify(parts[0]+"."+paddedClaims+"."+parts[2], AudienceExecutorMCP, developmentTestNow); err == nil || !strings.Contains(err.Error(), "base64url") {
		t.Fatalf("noncanonical-base64 error = %v", err)
	}
}

func TestDevelopmentCodecEnforcesTimeWindow(t *testing.T) {
	codec := newDevelopmentTestCodec(t, 0x61)
	claims := developmentTestClaims(AudienceExecutorMCP)
	token, err := codec.Sign(claims)
	if err != nil {
		t.Fatal(err)
	}
	for name, now := range map[string]time.Time{
		"zero":             {},
		"before-issued-at": time.UnixMilli(claims.IssuedAtUnixMS - 1),
		"at-expiry":        time.UnixMilli(claims.ExpiresAtUnixMS),
		"after-expiry":     time.UnixMilli(claims.ExpiresAtUnixMS + 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := codec.Verify(token, AudienceExecutorMCP, now); err == nil {
				t.Fatal("development capability was accepted outside its validity window")
			}
		})
	}
	for name, now := range map[string]time.Time{
		"at-issued-at":  time.UnixMilli(claims.IssuedAtUnixMS),
		"before-expiry": time.UnixMilli(claims.ExpiresAtUnixMS - 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := codec.Verify(token, AudienceExecutorMCP, now); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDevelopmentClaimsRejectCrossAudienceAuthority(t *testing.T) {
	executor := developmentTestClaims(AudienceExecutorMCP)
	model := developmentTestClaims(AudienceLLMProxy)
	for name, claims := range map[string]Claims{
		"executor-with-model": func() Claims {
			value := executor
			value.Model = "gpt-5"
			return value
		}(),
		"model-with-executor": func() Claims {
			value := model
			value.ExecutorID = "79000000-0000-4000-8000-000000000009"
			return value
		}(),
		"unsupported-audience": func() Claims {
			value := executor
			value.Audience = "somewhere-else"
			return value
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			if err := claims.Validate(); err == nil {
				t.Fatal("invalid development authority was accepted")
			}
		})
	}
}

func TestNewDevelopmentCodecRequiresExactKeySize(t *testing.T) {
	for _, size := range []int{0, 31, 33} {
		if _, err := NewDevelopmentCodec(make([]byte, size)); err == nil {
			t.Fatalf("key size %d was accepted", size)
		}
	}
	if _, err := NewDevelopmentCodec(make([]byte, 32)); err != nil {
		t.Fatal(err)
	}
}

func TestNewDevelopmentCodecFromBase64KeyRequiresCanonicalEncoding(t *testing.T) {
	encoded := base64.RawURLEncoding.EncodeToString(bytesOf(0x72, 32))
	codec, err := NewDevelopmentCodecFromBase64Key(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := codec.Sign(developmentTestClaims(AudienceExecutorMCP)); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{"", encoded + "=", base64.RawURLEncoding.EncodeToString(bytesOf(0x72, 31))} {
		if _, err := NewDevelopmentCodecFromBase64Key(invalid); err == nil {
			t.Fatalf("invalid encoded development key %q was accepted", invalid)
		}
	}
}

func developmentTestClaims(audience string) Claims {
	claims := Claims{
		Version: DevelopmentVersion, CapabilityID: "71000000-0000-4000-8000-000000000001",
		Audience: audience, WorkspaceID: "72000000-0000-4000-8000-000000000002",
		SessionID: "73000000-0000-4000-8000-000000000003", RunID: "74000000-0000-4000-8000-000000000004",
		RunAttemptID: "75000000-0000-4000-8000-000000000005", RunAttemptGeneration: 3,
		ActorID: "76000000-0000-4000-8000-000000000006", HolderID: "development-pool-holder",
		IssuedAtUnixMS:    developmentTestNow.Add(-time.Minute).UnixMilli(),
		RunDeadlineUnixMS: developmentTestNow.Add(30 * time.Minute).UnixMilli(),
		ExpiresAtUnixMS:   developmentTestNow.Add(time.Hour).UnixMilli(),
	}
	if audience == AudienceExecutorMCP {
		claims.ExecutorID = "77000000-0000-4000-8000-000000000007"
		claims.ToolCatalogDigest = strings.Repeat("a", 64)
		claims.ExpectedRunVersion = 4
		claims.ExpectedRunAttemptVersion = 5
		claims.MaxApprovalTTLMillis = 60_000
	} else {
		claims.Model = "gpt-5"
		claims.Provider = "development-llmproxy"
	}
	return claims
}

func newDevelopmentTestCodec(t *testing.T, fill byte) *DevelopmentCodec {
	t.Helper()
	codec, err := NewDevelopmentCodec(bytesOf(fill, 32))
	if err != nil {
		t.Fatal(err)
	}
	return codec
}

func developmentRawToken(codec *DevelopmentCodec, raw []byte) string {
	return developmentTokenPrefix + "." + base64.RawURLEncoding.EncodeToString(raw) + "." +
		base64.RawURLEncoding.EncodeToString(codec.signature(raw))
}

func bytesOf(value byte, count int) []byte {
	result := make([]byte, count)
	for index := range result {
		result[index] = value
	}
	return result
}
