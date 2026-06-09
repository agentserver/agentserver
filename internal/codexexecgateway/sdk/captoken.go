package sdk

import "time"

// sdkCapTokenTTL is how long a workspace-scoped cap-token minted for
// the in-process SDK Pool is valid. The pool keeps the same token for
// the workspace's lifetime in wsCache; renewing on demand is a v2
// concern (sessions today don't outlive a single deploy by 24h). The
// codex-app-gateway path uses a per-turn token (~15min); SDK clients
// hold the token continuously, so we choose a longer window.
//
// Tokens are minted via internal/captoken.Mint with SkipAudit=true —
// see Server.wsCtxFor. The SDK REST handlers in sdk/handlers.go record
// each tool call at CallStart/CallEnd granularity, so per-WS-frame
// audit recording would double-count every SDK call (I10 followup).
const sdkCapTokenTTL = 24 * time.Hour
