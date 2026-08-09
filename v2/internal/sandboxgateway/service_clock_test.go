package sandboxgateway

import (
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/sandboxcontract"
)

func TestMatchReadySessionStateUsesSuppliedClock(t *testing.T) {
	now := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	expiresAt := now.Add(time.Minute)
	identity := sandboxcontract.SessionIdentity{
		WorkspaceID:   "10000000-0000-4000-8000-000000000001",
		SessionID:     "20000000-0000-4000-8000-000000000001",
		EnvironmentID: "30000000-0000-4000-8000-000000000001",
	}
	state := corecontract.ManagedSandboxState{
		WorkspaceID: identity.WorkspaceID, SessionID: identity.SessionID,
		EnvironmentID: identity.EnvironmentID, DesiredState: "ready", ObservedState: "ready",
		ProviderSessionRef: "provider-session-1", ExpiresAt: &expiresAt,
	}
	if err := matchReadySessionState(identity, state, now); err != nil {
		t.Fatalf("ready state was rejected against the supplied clock: %v", err)
	}
	if err := matchReadySessionState(identity, state, expiresAt); err == nil {
		t.Fatal("state expiring exactly at the supplied clock was accepted")
	}
}
