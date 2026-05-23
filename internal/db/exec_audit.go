package db

import (
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
)

// AuditPayload is one row in exec_audit_payloads — a zstd-compressed,
// sha256-deduped blob. Multiple calls referencing the same content share
// a single row whose ref_count tracks how many references exist.
type AuditPayload struct {
	ID             string
	Sha256         string
	Compressed     []byte
	OriginalSize   int
	CompressedSize int
}

// UpsertAuditPayload inserts a new payload row keyed by sha256, or bumps
// ref_count on conflict. The returned id is the canonical row id (the
// existing id on dedupe, the freshly-minted id on insert). The caller
// references this id from exec_audit_calls.{request,response}_payload_id.
func (db *DB) UpsertAuditPayload(p AuditPayload) (string, error) {
	if p.Sha256 == "" {
		return "", errors.New("exec_audit: payload sha256 required")
	}
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	const q = `
        INSERT INTO exec_audit_payloads (id, sha256, compressed, original_size, compressed_size, ref_count)
        VALUES ($1, $2, $3, $4, $5, 1)
        ON CONFLICT (sha256) DO UPDATE SET ref_count = exec_audit_payloads.ref_count + 1
        RETURNING id`
	var id string
	if err := db.QueryRow(q,
		p.ID, p.Sha256, p.Compressed, p.OriginalSize, p.CompressedSize,
	).Scan(&id); err != nil {
		return "", err
	}
	return id, nil
}

// AuditSession is one row in exec_audit_sessions — the lifetime of a ws
// bridge between env-mcp (or the SDK REST bridge.Pool) and a single
// codex-exec backend. Open/Close are split: UpsertAuditSession writes
// the row at Open time; UpdateAuditSessionClose stamps the close fields.
type AuditSession struct {
	ID              string
	WorkspaceID     string
	UserID          *string
	ExeID           string
	TurnID          *string
	StreamID        string
	ClientIP        *string // text; cast to ::inet in SQL
	CapIAT          *time.Time
	CapEXP          *time.Time
	OpenedAt        time.Time
	ClosedAt        *time.Time
	CloseReason     *string
	FramesToBackend int
	FramesToClient  int
	BytesToBackend  int64
	BytesToClient   int64
}

// UpsertAuditSession inserts a new session row, no-op on duplicate id.
// Idempotency-by-id lets the gateway-side uploader safely retry whole
// batches without producing duplicate rows.
func (db *DB) UpsertAuditSession(s AuditSession) error {
	if s.ID == "" {
		return errors.New("exec_audit: session id required")
	}
	const q = `
        INSERT INTO exec_audit_sessions (
            id, workspace_id, user_id, exe_id, turn_id, stream_id,
            client_ip, cap_iat, cap_exp, opened_at
        ) VALUES ($1, $2, $3, $4, $5, $6, $7::inet, $8, $9, $10)
        ON CONFLICT (id) DO NOTHING`
	_, err := db.Exec(q,
		s.ID, s.WorkspaceID, s.UserID, s.ExeID, s.TurnID, s.StreamID,
		s.ClientIP, s.CapIAT, s.CapEXP, s.OpenedAt,
	)
	return err
}

// ErrAuditRowMissing is returned by UpdateAuditSessionClose /
// UpdateAuditCallEnd when the target row does not exist. The ingest
// handler treats this as "skip + log", so the next retry from the
// gateway-side WAL (after the matching Open / Start record lands)
// completes the row. Without surfacing this, an out-of-order batch
// would silently drop the close.
var ErrAuditRowMissing = errors.New("exec_audit: row not found")

// UpdateAuditSessionClose stamps the close-time fields on an existing
// session row. Frames/bytes counters are absolute totals (not deltas).
// Returns ErrAuditRowMissing if no row with the given id exists.
func (db *DB) UpdateAuditSessionClose(id string, closedAt time.Time, reason string,
	framesToBackend, framesToClient int,
	bytesToBackend, bytesToClient int64,
) error {
	const q = `
        UPDATE exec_audit_sessions
           SET closed_at = $2, close_reason = $3,
               frames_to_backend = $4, frames_to_client = $5,
               bytes_to_backend = $6, bytes_to_client = $7
         WHERE id = $1`
	res, err := db.Exec(q, id, closedAt, reason, framesToBackend, framesToClient, bytesToBackend, bytesToClient)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrAuditRowMissing
	}
	return nil
}

// AuditCall is one row in exec_audit_calls — a single logical call:
// a JSON-RPC request/response pair, an SDK tool invocation, or a relay
// PUT/GET. Source is the bridge type ("envmcp" | "rest" | "relay") and
// matches the CHECK constraint on the table.
type AuditCall struct {
	ID                string
	SessionID         *string
	WorkspaceID       string
	UserID            *string
	ExeID             string
	Source            string // "envmcp"|"rest"|"relay"
	RPCID             *string
	RPCMethod         *string
	RPCKind           *string
	RequestPayloadID  *string
	RequestSize       int
	RequestSha256     *string
	ResponsePayloadID *string
	ResponseSize      int
	ResponseSha256    *string
	IsError           bool
	ErrorSummary      *string
	StartedAt         time.Time
	CompletedAt       *time.Time
	DurationMs        *int
}

// UpsertAuditCall inserts a new call row with its start-time fields,
// no-op on duplicate id. The matching UpdateAuditCallEnd fills in the
// completion fields; this two-stage shape mirrors the session lifecycle.
func (db *DB) UpsertAuditCall(c AuditCall) error {
	if c.ID == "" {
		return errors.New("exec_audit: call id required")
	}
	if c.Source != "envmcp" && c.Source != "rest" && c.Source != "relay" {
		return errors.New("exec_audit: invalid source: " + c.Source)
	}
	const q = `
        INSERT INTO exec_audit_calls (
            id, session_id, workspace_id, user_id, exe_id, source,
            rpc_id, rpc_method, rpc_kind,
            request_payload_id, request_size, request_sha256,
            started_at
        ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
        ON CONFLICT (id) DO NOTHING`
	_, err := db.Exec(q,
		c.ID, c.SessionID, c.WorkspaceID, c.UserID, c.ExeID, c.Source,
		c.RPCID, c.RPCMethod, c.RPCKind,
		c.RequestPayloadID, c.RequestSize, c.RequestSha256,
		c.StartedAt,
	)
	return err
}

// UpdateAuditCallEnd stamps the completion fields on an existing call
// row and derives duration_ms from (completedAt - started_at) in SQL so
// it stays consistent even if the caller's clock has drifted between
// CallStart and CallEnd. Returns ErrAuditRowMissing if no row with the
// given id exists.
func (db *DB) UpdateAuditCallEnd(callID string,
	completedAt time.Time, isError bool, errorSummary string,
	responsePayloadID *string, responseSize int, responseSha256 *string,
) error {
	const q = `
        UPDATE exec_audit_calls
           SET completed_at = $2,
               is_error = $3,
               error_summary = $4,
               response_payload_id = $5,
               response_size = $6,
               response_sha256 = $7,
               duration_ms = CAST(EXTRACT(EPOCH FROM ($2 - started_at)) * 1000 AS INTEGER)
         WHERE id = $1`
	res, err := db.Exec(q, callID, completedAt, isError, errorSummary,
		responsePayloadID, responseSize, responseSha256)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrAuditRowMissing
	}
	return nil
}

// ListAuditSessionsFilter is the search criteria for ListAuditSessions.
// WorkspaceID is required (workspace isolation); all other fields are
// optional. Limit defaults to 100 and is capped at 1000.
type ListAuditSessionsFilter struct {
	WorkspaceID string // required
	ExeID       string
	UserID      string
	TurnID      string
	Since       time.Time // zero = no lower bound (compared against opened_at)
	Until       time.Time // zero = no upper bound (compared against opened_at)
	Limit       int       // 1..1000, default 100
}

// ListAuditSessions returns audit sessions matching the filter, newest
// first. Always scoped to a workspace.
func (db *DB) ListAuditSessions(f ListAuditSessionsFilter) ([]AuditSession, error) {
	if f.WorkspaceID == "" {
		return nil, errors.New("exec_audit: workspace_id required")
	}
	if f.Limit <= 0 {
		f.Limit = 100
	}
	if f.Limit > 1000 {
		f.Limit = 1000
	}
	q := `SELECT id, workspace_id, user_id, exe_id, turn_id, stream_id,
                 host(client_ip), cap_iat, cap_exp, opened_at, closed_at,
                 close_reason, frames_to_backend, frames_to_client,
                 bytes_to_backend, bytes_to_client
            FROM exec_audit_sessions
           WHERE workspace_id = $1`
	args := []any{f.WorkspaceID}
	add := func(clause string, val any) {
		q += " AND " + clause + " = $" + strconv.Itoa(len(args)+1)
		args = append(args, val)
	}
	if f.ExeID != "" {
		add("exe_id", f.ExeID)
	}
	if f.UserID != "" {
		add("user_id", f.UserID)
	}
	if f.TurnID != "" {
		add("turn_id", f.TurnID)
	}
	if !f.Since.IsZero() {
		q += " AND opened_at >= $" + strconv.Itoa(len(args)+1)
		args = append(args, f.Since)
	}
	if !f.Until.IsZero() {
		q += " AND opened_at < $" + strconv.Itoa(len(args)+1)
		args = append(args, f.Until)
	}
	q += " ORDER BY opened_at DESC LIMIT $" + strconv.Itoa(len(args)+1)
	args = append(args, f.Limit)

	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []AuditSession{}
	for rows.Next() {
		var s AuditSession
		if err := rows.Scan(
			&s.ID, &s.WorkspaceID, &s.UserID, &s.ExeID, &s.TurnID, &s.StreamID,
			&s.ClientIP, &s.CapIAT, &s.CapEXP, &s.OpenedAt, &s.ClosedAt,
			&s.CloseReason, &s.FramesToBackend, &s.FramesToClient,
			&s.BytesToBackend, &s.BytesToClient,
		); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ListAuditCallsFilter is the search criteria for ListAuditCalls.
// WorkspaceID is required. IsError=nil means "both"; non-nil narrows to
// only errors or only successes.
type ListAuditCallsFilter struct {
	WorkspaceID string // required
	SessionID   string
	ExeID       string
	UserID      string
	Source      string // "envmcp"|"rest"|"relay"
	RPCMethod   string
	IsError     *bool
	Since       time.Time
	Until       time.Time
	Limit       int
}

// ListAuditCalls returns audit calls matching the filter, newest first.
func (db *DB) ListAuditCalls(f ListAuditCallsFilter) ([]AuditCall, error) {
	if f.WorkspaceID == "" {
		return nil, errors.New("exec_audit: workspace_id required")
	}
	if f.Source != "" && f.Source != "envmcp" && f.Source != "rest" && f.Source != "relay" {
		return nil, errors.New("exec_audit: invalid source: " + f.Source)
	}
	if f.Limit <= 0 {
		f.Limit = 100
	}
	if f.Limit > 1000 {
		f.Limit = 1000
	}
	q := `SELECT id, session_id, workspace_id, user_id, exe_id, source,
                 rpc_id, rpc_method, rpc_kind,
                 request_payload_id, request_size, request_sha256,
                 response_payload_id, response_size, response_sha256,
                 is_error, error_summary,
                 started_at, completed_at, duration_ms
            FROM exec_audit_calls
           WHERE workspace_id = $1`
	args := []any{f.WorkspaceID}
	add := func(clause string, val any) {
		q += " AND " + clause + " = $" + strconv.Itoa(len(args)+1)
		args = append(args, val)
	}
	if f.SessionID != "" {
		add("session_id", f.SessionID)
	}
	if f.ExeID != "" {
		add("exe_id", f.ExeID)
	}
	if f.UserID != "" {
		add("user_id", f.UserID)
	}
	if f.Source != "" {
		add("source", f.Source)
	}
	if f.RPCMethod != "" {
		add("rpc_method", f.RPCMethod)
	}
	if f.IsError != nil {
		if *f.IsError {
			q += " AND is_error = true"
		} else {
			q += " AND is_error = false"
		}
	}
	if !f.Since.IsZero() {
		q += " AND started_at >= $" + strconv.Itoa(len(args)+1)
		args = append(args, f.Since)
	}
	if !f.Until.IsZero() {
		q += " AND started_at < $" + strconv.Itoa(len(args)+1)
		args = append(args, f.Until)
	}
	q += " ORDER BY started_at DESC LIMIT $" + strconv.Itoa(len(args)+1)
	args = append(args, f.Limit)

	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []AuditCall{}
	for rows.Next() {
		var c AuditCall
		if err := rows.Scan(
			&c.ID, &c.SessionID, &c.WorkspaceID, &c.UserID, &c.ExeID, &c.Source,
			&c.RPCID, &c.RPCMethod, &c.RPCKind,
			&c.RequestPayloadID, &c.RequestSize, &c.RequestSha256,
			&c.ResponsePayloadID, &c.ResponseSize, &c.ResponseSha256,
			&c.IsError, &c.ErrorSummary,
			&c.StartedAt, &c.CompletedAt, &c.DurationMs,
		); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetAuditSession returns a single session row by id. Returns sql.ErrNoRows
// when not found.
func (db *DB) GetAuditSession(id string) (*AuditSession, error) {
	const q = `SELECT id, workspace_id, user_id, exe_id, turn_id, stream_id,
                      host(client_ip), cap_iat, cap_exp, opened_at, closed_at,
                      close_reason, frames_to_backend, frames_to_client,
                      bytes_to_backend, bytes_to_client
                 FROM exec_audit_sessions WHERE id = $1`
	var s AuditSession
	err := db.QueryRow(q, id).Scan(
		&s.ID, &s.WorkspaceID, &s.UserID, &s.ExeID, &s.TurnID, &s.StreamID,
		&s.ClientIP, &s.CapIAT, &s.CapEXP, &s.OpenedAt, &s.ClosedAt,
		&s.CloseReason, &s.FramesToBackend, &s.FramesToClient,
		&s.BytesToBackend, &s.BytesToClient,
	)
	if err != nil {
		return nil, err // includes sql.ErrNoRows
	}
	return &s, nil
}

// GetAuditCall returns a single call row by id. Returns sql.ErrNoRows when
// not found.
func (db *DB) GetAuditCall(id string) (*AuditCall, error) {
	const q = `SELECT id, session_id, workspace_id, user_id, exe_id, source,
                      rpc_id, rpc_method, rpc_kind,
                      request_payload_id, request_size, request_sha256,
                      response_payload_id, response_size, response_sha256,
                      is_error, error_summary,
                      started_at, completed_at, duration_ms
                 FROM exec_audit_calls WHERE id = $1`
	var c AuditCall
	err := db.QueryRow(q, id).Scan(
		&c.ID, &c.SessionID, &c.WorkspaceID, &c.UserID, &c.ExeID, &c.Source,
		&c.RPCID, &c.RPCMethod, &c.RPCKind,
		&c.RequestPayloadID, &c.RequestSize, &c.RequestSha256,
		&c.ResponsePayloadID, &c.ResponseSize, &c.ResponseSha256,
		&c.IsError, &c.ErrorSummary,
		&c.StartedAt, &c.CompletedAt, &c.DurationMs,
	)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// GetAuditPayload returns the payload row (including the compressed bytes
// and dedup metadata) for the given id. Returns sql.ErrNoRows when missing.
func (db *DB) GetAuditPayload(id string) (*AuditPayload, error) {
	const q = `SELECT id, sha256, compressed, original_size, compressed_size
                 FROM exec_audit_payloads WHERE id = $1`
	var p AuditPayload
	err := db.QueryRow(q, id).Scan(
		&p.ID, &p.Sha256, &p.Compressed, &p.OriginalSize, &p.CompressedSize,
	)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// PruneAuditOlderThan deletes audit sessions and standalone calls whose
// timestamps are older than cutoff, then sweeps orphan payloads no longer
// referenced by any surviving call. Returns the number of rows deleted
// from each table.
//
// Cascade behaviour:
//   - Deleting from exec_audit_sessions cascades to exec_audit_calls via
//     the session_id FK (ON DELETE CASCADE), so calls attached to old
//     sessions go away even without the second DELETE — that DELETE is
//     here to catch source="rest"/"relay" rows that have no session.
//   - Payload pruning waits for both: (a) the payload is older than the
//     session/call retention cutoff, AND (b) the payload is at least 1
//     day old. The 1-day grace protects payloads that the gateway has
//     uploaded but whose CallStart row hasn't been ingested yet — if we
//     deleted such a payload, the soon-to-arrive call would dangle.
func (db *DB) PruneAuditOlderThan(cutoff time.Time) (sessions, calls, payloads int64, err error) {
	res, err := db.Exec(`DELETE FROM exec_audit_sessions WHERE opened_at < $1`, cutoff)
	if err != nil {
		return 0, 0, 0, err
	}
	sessions, _ = res.RowsAffected()

	// Standalone calls (source = rest/relay) that were never attached to
	// a session. Cascade above already removed session-attached calls,
	// and we deliberately do NOT delete session-attached calls older than
	// the cutoff — a long-running session keeps its full call history as
	// long as the session itself survives, otherwise its detail view would
	// show silently-truncated middles.
	res, err = db.Exec(`DELETE FROM exec_audit_calls WHERE started_at < $1 AND session_id IS NULL`, cutoff)
	if err != nil {
		return sessions, 0, 0, err
	}
	calls, _ = res.RowsAffected()

	// Effective payload cutoff: min(retention cutoff, now-24h). Never delete
	// payloads created in the last 24h, even if retention says we could.
	payloadCutoff := cutoff
	oneDayAgo := time.Now().UTC().Add(-24 * time.Hour)
	if payloadCutoff.After(oneDayAgo) {
		payloadCutoff = oneDayAgo
	}
	res, err = db.Exec(`
        DELETE FROM exec_audit_payloads
         WHERE created_at < $1
           AND id NOT IN (
               SELECT request_payload_id FROM exec_audit_calls WHERE request_payload_id IS NOT NULL
               UNION
               SELECT response_payload_id FROM exec_audit_calls WHERE response_payload_id IS NOT NULL
           )`, payloadCutoff)
	if err != nil {
		return sessions, calls, 0, err
	}
	payloads, _ = res.RowsAffected()
	return
}
