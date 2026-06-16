package mcppublic

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/agentserver/agentserver/internal/captoken"
)

// publicCapTokenTTL is the TTL for cap-tokens minted by the public
// gateway. Spec § 4.6 sets this to 10 minutes — shorter than the 1h
// in-pod TTL because public exposure has a bigger blast radius if a
// token leaks (the in-pod env-mcp runs in the same network namespace
// as the gateway; a public PAT-holder is on the open internet).
const publicCapTokenTTL = 10 * time.Minute

// capCacheLeadTime is how long before a cached cap-token expires we
// proactively re-mint. Picked at 1 minute so concurrent tool calls
// stretching across the boundary don't half-die mid-stream.
const capCacheLeadTime = 1 * time.Minute

// CapMinter issues short-lived workspace cap-tokens for the public
// gateway, with a small per-(workspace_id, user_id) cache so a burst
// of tools/call dispatches against the same workspace doesn't mint a
// new token per call. The cache is best-effort — a miss just mints.
type CapMinter struct {
	Secret []byte

	mu    sync.Mutex
	cache map[string]capEntry
}

type capEntry struct {
	token   string
	expires time.Time
}

// NewCapMinter wraps secret with a token cache. Secret must be
// non-empty (the underlying captoken.Mint enforces this; we surface it
// here so misconfigured deployments fail-fast at construction rather
// than at first request).
func NewCapMinter(secret []byte) (*CapMinter, error) {
	if len(secret) == 0 {
		return nil, fmt.Errorf("mcppublic: cap-token HMAC secret required")
	}
	return &CapMinter{Secret: secret, cache: map[string]capEntry{}}, nil
}

// MintForPrincipal returns a workspace-scoped cap-token usable against
// codex-exec-gateway's /bridge endpoint. The synthetic turn_id is
// `pub_<random>` so revoke-turn callers can spot it as gateway-minted
// (no real codex turn ever generated it).
//
// SkipAudit is set true on the token: codex-exec-gateway's bridge
// handler does per-frame audit, but the per-frame layer would
// quadruple-log every public-gateway tool call (we already log
// tool-call audit in mcppublic itself — coming in PR F's audit.go).
// Setting SkipAudit avoids the double-log while keeping the cap-token
// otherwise indistinguishable from an in-pod one.
//
// Cache lookup is keyed on (workspace_id, user_id) so two different
// users hitting the same workspace get different tokens (each carries
// its own user_id for downstream attribution).
func (m *CapMinter) MintForPrincipal(p *Principal, workspaceID string) (string, error) {
	if p == nil {
		return "", fmt.Errorf("mcppublic: nil principal")
	}
	if !p.HasWorkspace(workspaceID) {
		// Defensive — the dispatcher should already have rejected this
		// before reaching us. Mint refuses too so an accidentally-wired
		// caller can't bypass the boundary.
		return "", fmt.Errorf("mcppublic: principal lacks workspace %q", workspaceID)
	}

	key := p.UserID + "\x00" + workspaceID
	now := time.Now()

	m.mu.Lock()
	if e, ok := m.cache[key]; ok && e.expires.After(now.Add(capCacheLeadTime)) {
		m.mu.Unlock()
		return e.token, nil
	}
	m.mu.Unlock()

	turnID, err := synthTurnID()
	if err != nil {
		return "", err
	}
	tok, err := captoken.Mint(m.Secret, captoken.Payload{
		TurnID:      turnID,
		WorkspaceID: workspaceID,
		UserID:      p.UserID,
		SkipAudit:   true,
	}, publicCapTokenTTL)
	if err != nil {
		return "", fmt.Errorf("mcppublic: mint cap-token: %w", err)
	}

	m.mu.Lock()
	m.cache[key] = capEntry{token: tok, expires: now.Add(publicCapTokenTTL)}
	m.mu.Unlock()
	return tok, nil
}

// synthTurnID returns a `pub_<24hex>` identifier — 12 random bytes
// give 96 bits of entropy, plenty for a non-collision guarantee within
// the cap-token TTL.
func synthTurnID() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("mcppublic: read random for turn_id: %w", err)
	}
	return "pub_" + hex.EncodeToString(b[:]), nil
}
