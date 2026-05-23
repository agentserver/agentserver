package db

import (
	"errors"
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

// UpdateAuditSessionClose stamps the close-time fields on an existing
// session row. Frames/bytes counters are absolute totals (not deltas).
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
	_, err := db.Exec(q, id, closedAt, reason, framesToBackend, framesToClient, bytesToBackend, bytesToClient)
	return err
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
// CallStart and CallEnd.
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
	_, err := db.Exec(q, callID, completedAt, isError, errorSummary,
		responsePayloadID, responseSize, responseSha256)
	return err
}
