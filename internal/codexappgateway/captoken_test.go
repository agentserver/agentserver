package codexappgateway

import (
	"testing"
	"time"

	"github.com/agentserver/agentserver/internal/codexexecgateway"
)

func TestMintCapToken_VerifiesAtExecGateway(t *testing.T) {
	secret := []byte("shared-cap-secret")
	tok, err := MintCapToken(secret, "trn_42", "ws_a", "u_alice", time.Minute)
	if err != nil {
		t.Fatalf("MintCapToken: %v", err)
	}
	got, err := codexexecgateway.VerifyCapabilityToken(tok, secret)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got.TurnID != "trn_42" || got.WorkspaceID != "ws_a" || got.UserID != "u_alice" {
		t.Errorf("payload = %+v", got)
	}
}

func TestMintCapToken_ExpRespectsTTL(t *testing.T) {
	tok, err := MintCapToken([]byte("k"), "trn_1", "ws_a", "", -time.Second)
	if err != nil {
		t.Fatalf("MintCapToken: %v", err)
	}
	_, err = codexexecgateway.VerifyCapabilityToken(tok, []byte("k"))
	if err != codexexecgateway.ErrExpired {
		t.Fatalf("want ErrExpired, got %v", err)
	}
}

func TestMintCapToken_RejectsEmptySecret(t *testing.T) {
	if _, err := MintCapToken(nil, "trn_1", "ws_a", "", time.Minute); err == nil {
		t.Fatal("expected error for empty secret")
	}
}

func TestMintCapToken_RejectsEmptyFields(t *testing.T) {
	cases := []struct{ turn, ws string }{
		{"", "ws"},
		{"trn", ""},
	}
	for _, tc := range cases {
		if _, err := MintCapToken([]byte("k"), tc.turn, tc.ws, "", time.Minute); err == nil {
			t.Errorf("MintCapToken(%q,%q): want error, got nil", tc.turn, tc.ws)
		}
	}
}
