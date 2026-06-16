package captoken_test

import (
	"testing"
	"time"

	"github.com/agentserver/agentserver/internal/captoken"
	"github.com/agentserver/agentserver/internal/codexexecgateway"
)

func TestMint_VerifiesAtExecGateway(t *testing.T) {
	secret := []byte("shared-cap-secret")
	tok, err := captoken.Mint(secret, captoken.Payload{
		TurnID:      "trn_42",
		WorkspaceID: "ws_a",
		UserID:      "u_alice",
	}, time.Minute)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	got, err := codexexecgateway.VerifyCapabilityToken(tok, secret)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got.TurnID != "trn_42" || got.WorkspaceID != "ws_a" || got.UserID != "u_alice" {
		t.Errorf("payload = %+v", got)
	}
	if got.SkipAudit {
		t.Errorf("SkipAudit should default false, got true")
	}
}

func TestMint_SkipAuditRoundtrips(t *testing.T) {
	secret := []byte("k")
	tok, err := captoken.Mint(secret, captoken.Payload{
		TurnID:      "sdk",
		WorkspaceID: "ws_a",
		SkipAudit:   true,
	}, time.Minute)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	got, err := codexexecgateway.VerifyCapabilityToken(tok, secret)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !got.SkipAudit {
		t.Errorf("SkipAudit lost in roundtrip; got %+v", got)
	}
}

func TestMint_ExpRespectsTTL(t *testing.T) {
	tok, err := captoken.Mint([]byte("k"), captoken.Payload{
		TurnID:      "trn_1",
		WorkspaceID: "ws_a",
	}, -time.Second)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	_, err = codexexecgateway.VerifyCapabilityToken(tok, []byte("k"))
	if err != codexexecgateway.ErrExpired {
		t.Fatalf("want ErrExpired, got %v", err)
	}
}

func TestMint_IgnoresCallerSuppliedIATAndEXP(t *testing.T) {
	// Caller-set IAT/EXP must be overridden so a caller cannot mint a
	// never-expiring token by passing EXP=math.MaxInt64.
	secret := []byte("k")
	tok, err := captoken.Mint(secret, captoken.Payload{
		TurnID:      "trn_1",
		WorkspaceID: "ws_a",
		IAT:         1, // would be way in the past
		EXP:         2, // would be already expired
	}, time.Hour)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	got, err := codexexecgateway.VerifyCapabilityToken(tok, secret)
	if err != nil {
		t.Fatalf("verify: %v (caller-supplied EXP should have been overridden)", err)
	}
	if got.EXP <= 2 || got.IAT <= 1 {
		t.Errorf("Mint did not override caller IAT/EXP: got %+v", got)
	}
}

func TestMint_RejectsEmptySecret(t *testing.T) {
	if _, err := captoken.Mint(nil, captoken.Payload{TurnID: "t", WorkspaceID: "w"}, time.Minute); err == nil {
		t.Fatal("expected error for empty secret")
	}
}

func TestMint_RejectsEmptyFields(t *testing.T) {
	cases := []captoken.Payload{
		{TurnID: "", WorkspaceID: "ws"},
		{TurnID: "trn", WorkspaceID: ""},
	}
	for _, p := range cases {
		if _, err := captoken.Mint([]byte("k"), p, time.Minute); err == nil {
			t.Errorf("Mint(%+v): want error, got nil", p)
		}
	}
}
