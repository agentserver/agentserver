package mcppublic

import (
	"strings"
	"testing"
)

func TestCapMinter_RequiresSecret(t *testing.T) {
	if _, err := NewCapMinter(nil); err == nil {
		t.Fatal("want error for empty secret")
	}
}

func TestCapMinter_RefusesWorkspaceOutsidePrincipal(t *testing.T) {
	m, err := NewCapMinter([]byte("secret"))
	if err != nil {
		t.Fatalf("NewCapMinter: %v", err)
	}
	p := principalWith("ws_1")
	_, err = m.MintForPrincipal(p, "ws_2")
	if err == nil || !strings.Contains(err.Error(), "lacks workspace") {
		t.Fatalf("want lacks-workspace error, got %v", err)
	}
}

func TestCapMinter_CachesForRepeatedCalls(t *testing.T) {
	m, err := NewCapMinter([]byte("secret"))
	if err != nil {
		t.Fatalf("NewCapMinter: %v", err)
	}
	p := principalWith("ws_1")
	tok1, err := m.MintForPrincipal(p, "ws_1")
	if err != nil {
		t.Fatalf("first mint err: %v", err)
	}
	tok2, err := m.MintForPrincipal(p, "ws_1")
	if err != nil {
		t.Fatalf("second mint err: %v", err)
	}
	if tok1 != tok2 {
		t.Errorf("cache miss between back-to-back mints: token differs")
	}
}

func TestCapMinter_DifferentUsersGetDifferentTokens(t *testing.T) {
	m, _ := NewCapMinter([]byte("secret"))
	p1 := &Principal{UserID: "u1", WorkspaceID: "ws_1"}
	p2 := &Principal{UserID: "u2", WorkspaceID: "ws_1"}
	t1, _ := m.MintForPrincipal(p1, "ws_1")
	t2, _ := m.MintForPrincipal(p2, "ws_1")
	if t1 == t2 {
		t.Errorf("two users sharing one workspace produced identical cap-tokens")
	}
}

func TestSynthTurnID_HasExpectedPrefixAndLength(t *testing.T) {
	id, err := synthTurnID()
	if err != nil {
		t.Fatalf("synthTurnID err: %v", err)
	}
	if !strings.HasPrefix(id, "pub_") {
		t.Errorf("want pub_ prefix, got %q", id)
	}
	if len(id) != len("pub_")+24 {
		t.Errorf("unexpected length %d for %q", len(id), id)
	}
}
