package wsticket

import (
	"strings"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	ticket, err := Mint("exe_x", "secret")
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if err := Verify(ticket, "exe_x", "secret"); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestVerifyRejectsWrongExe(t *testing.T) {
	ticket, _ := Mint("exe_x", "secret")
	if err := Verify(ticket, "exe_y", "secret"); err == nil {
		t.Fatal("expected exe_id mismatch")
	}
}

func TestVerifyRejectsWrongSecret(t *testing.T) {
	ticket, _ := Mint("exe_x", "secret-a")
	if err := Verify(ticket, "exe_x", "secret-b"); err == nil {
		t.Fatal("expected bad signature")
	}
}

func TestVerifyRejectsMalformed(t *testing.T) {
	if err := Verify("onlyonepart", "exe_x", "secret"); err == nil {
		t.Fatal("expected malformed error")
	}
	if err := Verify("a.b", "exe_x", "secret"); err == nil {
		t.Fatal("expected malformed error")
	}
}

func TestMintRequiresSecret(t *testing.T) {
	if _, err := Mint("exe_x", ""); err == nil || !strings.Contains(err.Error(), "secret") {
		t.Fatal("expected secret-required error")
	}
}
