package mcppublic

import (
	"testing"

	"github.com/agentserver/agentserver/internal/secrets"
)

// mintTestPAT returns a syntactically valid agpat_ token built by
// secrets.Mint. Tests that need secrets.Parse to succeed use this
// (vs the bad-CRC string constant in auth_test.go).
func mintTestPAT(t *testing.T) string {
	t.Helper()
	tok, err := secrets.Mint(secrets.MCPPATSpec)
	if err != nil {
		t.Fatalf("mint test PAT: %v", err)
	}
	return tok.Full
}
