package runcursor

import (
	"strings"
	"testing"
)

var testScope = Scope{
	WorkspaceID: "10000000-0000-4000-8000-000000000001",
	SessionID:   "20000000-0000-4000-8000-000000000002",
	RunID:       "30000000-0000-4000-8000-000000000003",
}

func TestCodecRoundTripsAuthenticatedScopeAndSequence(t *testing.T) {
	codec, err := NewCodec([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	token, err := codec.Encode(testScope, 42)
	if err != nil {
		t.Fatal(err)
	}
	if token == "" || strings.Contains(token, testScope.RunID) {
		t.Fatalf("cursor is not opaque bounded text: %q", token)
	}
	sequence, err := codec.Decode(token, testScope)
	if err != nil || sequence != 42 {
		t.Fatalf("Decode() = %d, %v", sequence, err)
	}
}

func TestCodecRejectsTamperingWrongScopeAndNoncanonicalTokens(t *testing.T) {
	codec, _ := NewCodec([]byte("0123456789abcdef0123456789abcdef"))
	token, _ := codec.Encode(testScope, 7)
	replacement := byte('A')
	if token[len(token)-1] == replacement {
		replacement = 'B'
	}
	tampered := token[:len(token)-1] + string(replacement)
	wrong := testScope
	wrong.RunID = "30000000-0000-4000-8000-000000000004"
	for name, candidate := range map[string]struct {
		token string
		scope Scope
	}{
		"tampered":      {tampered, testScope},
		"wrong scope":   {token, wrong},
		"padding":       {token + "=", testScope},
		"wrong version": {"v2." + strings.TrimPrefix(token, tokenPrefix), testScope},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := codec.Decode(candidate.token, candidate.scope); err == nil {
				t.Fatalf("unsafe cursor %q was accepted", candidate.token)
			}
		})
	}
}

func TestCodecValidatesKeyUUIDAndJSONSafeSequence(t *testing.T) {
	if _, err := NewCodec([]byte("short")); err == nil {
		t.Fatal("short cursor key was accepted")
	}
	codec, _ := NewCodec([]byte("0123456789abcdef0123456789abcdef"))
	invalid := testScope
	invalid.WorkspaceID = "a0000000-0000-4000-8000-000000000001"
	invalid.WorkspaceID = strings.ToUpper(invalid.WorkspaceID)
	if _, err := codec.Encode(invalid, 1); err == nil {
		t.Fatal("noncanonical scope UUID was accepted")
	}
	if _, err := codec.Encode(testScope, 1<<53-1); err == nil {
		t.Fatal("unsafe sequence was accepted")
	}
}
