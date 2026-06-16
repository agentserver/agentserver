package mcppublic

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/agentserver/agentserver/internal/db"
	"github.com/agentserver/agentserver/internal/secrets"
)

// Sentinel errors returned by PrincipalResolvers. The middleware maps
// both to 401 (we don't tell the client whether the token was malformed
// or just revoked — same response in both cases avoids enumeration).
var (
	// ErrUnknown means the raw token's prefix doesn't match any
	// resolver this gateway understands (e.g. user pasted an `ast_` or
	// some unrelated string into the Authorization header).
	ErrUnknown = errors.New("mcppublic: unknown token format")

	// ErrInvalid means the token parsed but didn't authenticate:
	// revoked, expired, hash mismatch, or row deleted. Distinct from
	// ErrUnknown so resolvers can log meaningfully without leaking to
	// the wire.
	ErrInvalid = errors.New("mcppublic: invalid token")
)

// dbReader is the DB surface PATResolver needs. Defined as an interface
// (rather than *db.DB directly) so tests can stub it without standing
// up a real postgres for the unit-level resolver tests. The same
// interface is implemented by *db.DB transparently.
//
// 2026-06-15 amendment: ListWorkspacesByUser stays because we still
// need to verify the user is currently a member of the PAT's workspace
// — being kicked out of a workspace should invalidate every PAT that
// targets it, but the FK CASCADE on mcp_pats only fires on workspace
// DELETION, not on membership removal. So we always cross-check at
// resolve time.
type dbReader interface {
	ValidateMCPPATSecret(ctx context.Context, prefix, secret string) (*db.MCPPAT, error)
	TouchMCPPATLastUsed(ctx context.Context, patID string) error
	ListWorkspacesByUser(userID string) ([]*db.Workspace, error)
}

// PATResolver turns an agpat_… bearer into a Principal by validating
// against the mcp_pats table and deriving the tool set from the PAT's
// stored scopes. The workspace is intrinsic to the PAT row (post the
// 2026-06-15 amendment) — no scope-based workspace selection.
type PATResolver struct {
	DB dbReader
	// Logger receives a single attrs-rich log line per Resolve call,
	// so leaked tokens / brute-force attempts are visible in ops. Nil
	// falls back to slog.Default().
	Logger *slog.Logger
}

// Resolve implements PrincipalResolver. The pipeline:
//  1. secrets.Parse splits agpat_<id>_<secret><crc> → (prefix_id, secret),
//     short-circuiting malformed tokens before any DB hit.
//  2. ValidateMCPPATSecret does the active-row + constant-time hash
//     compare in a single SQL round-trip, returning the row's
//     workspace_id intrinsically.
//  3. Scopes are mapped to tool sets (mcp:read → ToolsRead,
//     mcp:exec → ToolsExec). The pre-amendment `workspace:<id>` scope
//     family is gone; any leftover from an old DB row is dropped with
//     a warn log (it doesn't grant anything).
//  4. Membership cross-check: the user must still be a member of the
//     PAT's workspace. If they were kicked out after PAT issuance,
//     treat as ErrInvalid.
//  5. Best-effort TouchMCPPATLastUsed off the hot path.
func (r *PATResolver) Resolve(ctx context.Context, raw string) (*Principal, error) {
	log := r.Logger
	if log == nil {
		log = slog.Default()
	}
	if !strings.HasPrefix(raw, secrets.MCPPATSpec.Prefix) {
		return nil, ErrUnknown
	}
	patID, secret, err := secrets.Parse(secrets.MCPPATSpec, raw)
	if err != nil {
		log.Debug("mcppublic.PAT: parse failed", "err", err)
		return nil, ErrUnknown
	}
	row, err := r.DB.ValidateMCPPATSecret(ctx, patID, secret)
	if err == sql.ErrNoRows {
		log.Info("mcppublic.PAT: validate miss", "pat_id", patID)
		return nil, ErrInvalid
	}
	if err != nil {
		return nil, fmt.Errorf("validate mcp_pat: %w", err)
	}

	// Map scopes → tool set. The scope vocabulary is authoritatively
	// defined in internal/server/mcp_pat_scopes.go; duplicated here as
	// string constants to avoid pulling the server package (huge
	// transitive deps) into the gateway binary.
	const (
		scopeRead = "mcp:read"
		scopeExec = "mcp:exec"
	)
	tools := map[string]struct{}{}
	for _, s := range row.Scopes {
		switch s {
		case scopeRead:
			for k := range ToolsRead {
				tools[k] = struct{}{}
			}
		case scopeExec:
			for k := range ToolsExec {
				tools[k] = struct{}{}
			}
		default:
			// Unknown scopes are dropped silently — they were either
			// never granted by an old gateway version, or got into
			// the DB by an out-of-band path (e.g. a row from a
			// pre-2026-06-15 install with `workspace:<id>` strings).
			// Either way, granting tools based on something the
			// gateway doesn't understand would be unsafe. Log so ops
			// can spot drift.
			log.Warn("mcppublic.PAT: unknown scope dropped", "pat_id", row.ID, "scope", s)
		}
	}

	// Membership cross-check: the user must still be a member of the
	// PAT's workspace. FK CASCADE handles workspace deletion; this
	// catches the kicked-out-of-workspace case.
	memberships, err := r.DB.ListWorkspacesByUser(row.UserID)
	if err != nil {
		return nil, fmt.Errorf("lookup workspace memberships: %w", err)
	}
	stillMember := false
	for _, w := range memberships {
		if w.ID == row.WorkspaceID {
			stillMember = true
			break
		}
	}
	if !stillMember {
		log.Info("mcppublic.PAT: user no longer member of PAT's workspace",
			"pat_id", row.ID, "user_id", row.UserID, "workspace_id", row.WorkspaceID)
		return nil, ErrInvalid
	}

	// Fire-and-forget last_used_at bump. Don't propagate ctx so a
	// request-cancellation race doesn't poison the stat.
	go func(id string) {
		if err := r.DB.TouchMCPPATLastUsed(context.Background(), id); err != nil {
			log.Debug("mcppublic.PAT: touch last_used failed", "pat_id", id, "err", err)
		}
	}(row.ID)

	return &Principal{
		UserID:      row.UserID,
		WorkspaceID: row.WorkspaceID,
		Tools:       tools,
		PATId:       row.ID,
	}, nil
}
