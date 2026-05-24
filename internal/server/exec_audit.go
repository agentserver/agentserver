package server

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/agentserver/agentserver/internal/db"
	pb "github.com/agentserver/agentserver/internal/server/exec_audit_pb"
	"github.com/go-chi/chi/v5"
	"github.com/klauspost/compress/zstd"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	// auditPayloadHardMax — refuse to store payload bodies larger than this.
	// The gateway should size-cap before sending; this is a defence in depth.
	auditPayloadHardMax = 4 * 1024 * 1024 // 4 MiB
	// auditIngestBodyMax — POST body cap. Comfortably accommodates a full
	// batch of records each carrying max-sized request/response payloads.
	auditIngestBodyMax = 32 * 1024 * 1024 // 32 MiB
)

// postInternalExecAuditBatch ingests one batch from the gateway uploader.
// Auth: X-Internal-Secret = INTERNAL_API_SECRET (enforced by the route
// wrapper in server.go). Body Content-Type: application/x-protobuf,
// payload: serialised BatchRecords.
//
// All record types are idempotent: CallStart/SessionOpen upsert-by-id,
// SessionClose stamps completion fields on the existing session row.
// Returns 200 OK with {"processed":N,"skipped":M}. Malformed individual
// records are skipped rather than failing the whole batch so one bad
// row doesn't block the queue; the uploader still retries on 5xx.
//
//	@Summary  Ingest a batch of exec-gateway audit records (internal)
//	@Tags     Exec-Audit
//	@Accept   application/x-protobuf
//	@Produce  json
//	@Param    X-Internal-Secret header string true "Shared secret"
//	@Success  200 {object} ExecAuditBatchAckResponse
//	@Failure  400 {string} string
//	@Failure  401 {string} string
//	@Failure  500 {string} string
//	@Router   /internal/exec-audit/batch [post]
func (s *Server) postInternalExecAuditBatch(w http.ResponseWriter, r *http.Request) {
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/x-protobuf") {
		http.Error(w, "Content-Type: application/x-protobuf required", http.StatusBadRequest)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, auditIngestBodyMax))
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	var batch pb.BatchRecords
	if err := proto.Unmarshal(raw, &batch); err != nil {
		http.Error(w, "unmarshal: "+err.Error(), http.StatusBadRequest)
		return
	}

	processed := 0
	skipped := 0
	for _, rec := range batch.GetRecords() {
		if err := s.applyAuditRecord(r.Context(), rec); err != nil {
			log.Printf("exec-audit: apply failed id=%s %s err=%v",
				rec.GetId(), recordTriageContext(rec), err)
			skipped++
			continue
		}
		processed++
	}
	writeJSON(w, http.StatusOK, ExecAuditBatchAckResponse{Processed: processed, Skipped: skipped})
}

// applyAuditRecord dispatches a single WALRecord to the matching DAL
// helper. Returns an error iff the record was malformed (unknown body
// kind, missing required field) or the DB write failed; the caller
// counts that as a "skipped" record.
func (s *Server) applyAuditRecord(_ context.Context, rec *pb.WALRecord) error {
	if rec == nil {
		return errors.New("nil WALRecord")
	}
	switch b := rec.Body.(type) {
	case *pb.WALRecord_SessionOpen:
		op := b.SessionOpen
		return s.DB.UpsertAuditSession(db.AuditSession{
			ID:          rec.GetId(),
			WorkspaceID: op.GetWorkspaceId(),
			UserID:      strPtrOrNil(op.GetUserId()),
			ExeID:       op.GetExeId(),
			TurnID:      strPtrOrNil(op.GetTurnId()),
			StreamID:    op.GetStreamId(),
			ClientIP:    strPtrOrNil(op.GetClientIp()),
			CapIAT:      tsToTime(op.GetCapIat()),
			CapEXP:      tsToTime(op.GetCapExp()),
			OpenedAt:    op.GetOpenedAt().AsTime().UTC(),
		})
	case *pb.WALRecord_SessionClose:
		cl := b.SessionClose
		return s.DB.UpdateAuditSessionClose(
			cl.GetSessionId(),
			cl.GetClosedAt().AsTime().UTC(),
			cl.GetCloseReason(),
			int(cl.GetFramesToBackend()),
			int(cl.GetFramesToClient()),
			cl.GetBytesToBackend(),
			cl.GetBytesToClient(),
		)
	case *pb.WALRecord_CallStart:
		cs := b.CallStart
		var reqPayloadID *string
		if raw := cs.GetRequestBytes(); len(raw) > 0 {
			id, err := s.upsertPayload(raw)
			if err != nil {
				return fmt.Errorf("upsert request payload: %w", err)
			}
			reqPayloadID = &id
		}
		return s.DB.UpsertAuditCall(db.AuditCall{
			ID:               rec.GetId(),
			SessionID:        strPtrOrNil(cs.GetSessionId()),
			WorkspaceID:      cs.GetWorkspaceId(),
			UserID:           strPtrOrNil(cs.GetUserId()),
			ExeID:            cs.GetExeId(),
			Source:           cs.GetSource(),
			RPCID:            strPtrOrNil(cs.GetRpcId()),
			RPCMethod:        strPtrOrNil(cs.GetRpcMethod()),
			RPCKind:          strPtrOrNil(cs.GetRpcKind()),
			RequestPayloadID: reqPayloadID,
			RequestSize:      int(cs.GetRequestSize()),
			RequestSha256:    strPtrOrNil(cs.GetRequestSha256()),
			StartedAt:        cs.GetStartedAt().AsTime().UTC(),
		})
	}
	return errors.New("exec-audit: unknown WALRecord body")
}

// upsertPayload zstd-compresses raw payload bytes (<= auditPayloadHardMax)
// and stores them via the DAL. Returns the payload row id. Bodies above
// the hard cap are rejected — the gateway is expected to drop or hash-only
// such payloads before sending.
func (s *Server) upsertPayload(raw []byte) (string, error) {
	if len(raw) > auditPayloadHardMax {
		return "", fmt.Errorf("payload %d bytes exceeds hard cap %d", len(raw), auditPayloadHardMax)
	}
	sum := sha256.Sum256(raw)
	hash := hex.EncodeToString(sum[:])
	encoded, err := zstdCompress(raw)
	if err != nil {
		return "", err
	}
	return s.DB.UpsertAuditPayload(db.AuditPayload{
		Sha256:         hash,
		Compressed:     encoded,
		OriginalSize:   len(raw),
		CompressedSize: len(encoded),
	})
}

// Shared zstd codecs — safe for concurrent use per klauspost docs.
var (
	zstdEncoder, _ = zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault))
	zstdDecoder, _ = zstd.NewReader(nil)
)

func zstdCompress(b []byte) ([]byte, error)   { return zstdEncoder.EncodeAll(b, nil), nil }
func zstdDecompress(b []byte) ([]byte, error) { return zstdDecoder.DecodeAll(b, nil) }

// strPtrOrNil maps "" → nil and any other string → &s. Protobuf decodes
// unset string fields as "", but our DAL columns are nullable text.
func strPtrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// tsToTime converts a protobuf Timestamp pointer to a *time.Time, mapping
// the nil-pointer (unset field) case to nil so nullable timestamp columns
// stay NULL rather than getting stamped with 1970-01-01.
func tsToTime(ts *timestamppb.Timestamp) *time.Time {
	if ts == nil {
		return nil
	}
	t := ts.AsTime().UTC()
	return &t
}

// ---------- internal mirrors (X-Internal-Secret) ----------

// getInternalExecAuditSessions lists audit sessions for a workspace.
//
//	@Summary  List exec-audit sessions (internal)
//	@Tags     Exec-Audit
//	@Produce  json
//	@Param    X-Internal-Secret header   string  true  "Shared secret"
//	@Param    workspace_id      query    string  true  "Workspace ID"
//	@Param    exe_id            query    string  false "Executor ID filter"
//	@Param    user_id           query    string  false "User ID filter"
//	@Param    turn_id           query    string  false "Turn ID filter"
//	@Param    since             query    string  false "RFC3339 lower bound (opened_at)"
//	@Param    until             query    string  false "RFC3339 upper bound (opened_at)"
//	@Param    limit             query    int     false "Max rows (default 50)"
//	@Success  200 {object} ListAuditSessionsResponse
//	@Failure  400 {string} string
//	@Failure  401 {string} string
//	@Failure  500 {string} string
//	@Router   /internal/exec-audit/sessions [get]
func (s *Server) getInternalExecAuditSessions(w http.ResponseWriter, r *http.Request) {
	f, err := parseSessionsFilter(r.URL.Query())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	rows, err := s.DB.ListAuditSessions(f)
	if err != nil {
		log.Printf("exec-audit: ListAuditSessions: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, ListAuditSessionsResponse{Sessions: sessionsToDTO(rows)})
}

// getInternalExecAuditSession returns one session plus its first 20 calls.
//
//	@Summary  Get an exec-audit session detail (internal)
//	@Tags     Exec-Audit
//	@Produce  json
//	@Param    X-Internal-Secret header   string  true  "Shared secret"
//	@Param    session_id        path     string  true  "Session ID"
//	@Success  200 {object} AuditSessionDetail
//	@Failure  401 {string} string
//	@Failure  404 {string} string
//	@Failure  500 {string} string
//	@Router   /internal/exec-audit/sessions/{session_id} [get]
func (s *Server) getInternalExecAuditSession(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "session_id")
	sess, err := s.DB.GetAuditSession(id)
	if err == sql.ErrNoRows {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		log.Printf("exec-audit: GetAuditSession: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// Pull the first N calls.
	calls, err := s.DB.ListAuditCalls(db.ListAuditCallsFilter{
		WorkspaceID: sess.WorkspaceID, SessionID: id, Limit: 20,
	})
	if err != nil {
		log.Printf("exec-audit: ListAuditCalls(session): %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, AuditSessionDetail{
		Session:    sessionToDTO(*sess),
		FirstCalls: callsToDTO(calls),
	})
}

// getInternalExecAuditCalls lists audit calls for a workspace.
//
//	@Summary  List exec-audit calls (internal)
//	@Tags     Exec-Audit
//	@Produce  json
//	@Param    X-Internal-Secret header   string  true  "Shared secret"
//	@Param    workspace_id      query    string  true  "Workspace ID"
//	@Param    session_id        query    string  false "Session ID filter"
//	@Param    exe_id            query    string  false "Executor ID filter"
//	@Param    user_id           query    string  false "User ID filter"
//	@Param    source            query    string  false "Source filter (e.g. mcp_rpc, sse_event)"
//	@Param    method            query    string  false "RPC method filter"
//	@Param    since             query    string  false "RFC3339 lower bound (started_at)"
//	@Param    until             query    string  false "RFC3339 upper bound (started_at)"
//	@Param    limit             query    int     false "Max rows (default 50)"
//	@Success  200 {object} ListAuditCallsResponse
//	@Failure  400 {string} string
//	@Failure  401 {string} string
//	@Failure  500 {string} string
//	@Router   /internal/exec-audit/calls [get]
func (s *Server) getInternalExecAuditCalls(w http.ResponseWriter, r *http.Request) {
	f, err := parseCallsFilter(r.URL.Query())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	rows, err := s.DB.ListAuditCalls(f)
	if err != nil {
		log.Printf("exec-audit: ListAuditCalls: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, ListAuditCallsResponse{Calls: callsToDTO(rows)})
}

// getInternalExecAuditCall returns one call plus payload previews.
//
//	@Summary  Get an exec-audit call detail (internal)
//	@Tags     Exec-Audit
//	@Produce  json
//	@Param    X-Internal-Secret header   string  true  "Shared secret"
//	@Param    call_id           path     string  true  "Call ID"
//	@Success  200 {object} AuditCallDetail
//	@Failure  401 {string} string
//	@Failure  404 {string} string
//	@Failure  500 {string} string
//	@Router   /internal/exec-audit/calls/{call_id} [get]
func (s *Server) getInternalExecAuditCall(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "call_id")
	call, err := s.DB.GetAuditCall(id)
	if err == sql.ErrNoRows {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		log.Printf("exec-audit: GetAuditCall: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	detail := AuditCallDetail{AuditCallSummary: callToDTO(*call)}
	if call.RequestPayloadID != nil {
		detail.RequestPreview = previewPayload(s.DB, *call.RequestPayloadID)
	}
	writeJSON(w, http.StatusOK, detail)
}

// ---------- workspace-scoped wrappers ----------

// getWorkspaceExecAuditSessions lists exec-audit sessions for the caller's workspace.
//
//	@Summary   List exec-audit sessions (workspace-scoped)
//	@Tags      Exec-Audit
//	@Produce   json
//	@Param     id         path   string  true  "Workspace ID"
//	@Param     exe_id     query  string  false "Executor ID filter"
//	@Param     user_id    query  string  false "User ID filter"
//	@Param     turn_id    query  string  false "Turn ID filter"
//	@Param     since      query  string  false "RFC3339 lower bound (opened_at)"
//	@Param     until      query  string  false "RFC3339 upper bound (opened_at)"
//	@Param     limit      query  int     false "Max rows (default 50)"
//	@Success   200 {object} ListAuditSessionsResponse
//	@Failure   400 {string} string
//	@Failure   401 {string} string
//	@Failure   403 {string} string
//	@Failure   500 {string} string
//	@Security  CookieAuth
//	@Router    /api/workspaces/{id}/exec-audit/sessions [get]
func (s *Server) getWorkspaceExecAuditSessions(w http.ResponseWriter, r *http.Request) {
	wsID := chi.URLParam(r, "id")
	if wsID == "" {
		http.Error(w, "workspace id required", http.StatusBadRequest)
		return
	}
	if _, ok := s.requireWorkspaceMember(w, r, wsID); !ok {
		return
	}
	q := r.URL.Query()
	q.Set("workspace_id", wsID)
	r.URL.RawQuery = q.Encode()
	s.getInternalExecAuditSessions(w, r)
}

// getWorkspaceExecAuditSession returns one session in the caller's workspace.
//
//	@Summary   Get exec-audit session (workspace-scoped)
//	@Tags      Exec-Audit
//	@Produce   json
//	@Param     id          path  string  true  "Workspace ID"
//	@Param     session_id  path  string  true  "Session ID"
//	@Success   200 {object} AuditSessionDetail
//	@Failure   401 {string} string
//	@Failure   403 {string} string
//	@Failure   404 {string} string
//	@Failure   500 {string} string
//	@Security  CookieAuth
//	@Router    /api/workspaces/{id}/exec-audit/sessions/{session_id} [get]
func (s *Server) getWorkspaceExecAuditSession(w http.ResponseWriter, r *http.Request) {
	wsID := chi.URLParam(r, "id")
	if _, ok := s.requireWorkspaceMember(w, r, wsID); !ok {
		return
	}
	// Workspace membership confirmed; verify the session belongs to this workspace
	// (else a member of ws A could fetch a session id from ws B).
	id := chi.URLParam(r, "session_id")
	sess, err := s.DB.GetAuditSession(id)
	if err == sql.ErrNoRows || (err == nil && sess.WorkspaceID != wsID) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		log.Printf("exec-audit: GetAuditSession: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.getInternalExecAuditSession(w, r) // safe: chi URL param still set
}

// getWorkspaceExecAuditCalls lists exec-audit calls in the caller's workspace.
//
//	@Summary   List exec-audit calls (workspace-scoped)
//	@Tags      Exec-Audit
//	@Produce   json
//	@Param     id          path   string  true  "Workspace ID"
//	@Param     session_id  query  string  false "Session ID filter"
//	@Param     exe_id      query  string  false "Executor ID filter"
//	@Param     user_id     query  string  false "User ID filter"
//	@Param     source      query  string  false "Source filter"
//	@Param     method      query  string  false "RPC method filter"
//	@Param     since       query  string  false "RFC3339 lower bound (started_at)"
//	@Param     until       query  string  false "RFC3339 upper bound (started_at)"
//	@Param     limit       query  int     false "Max rows (default 50)"
//	@Success   200 {object} ListAuditCallsResponse
//	@Failure   400 {string} string
//	@Failure   401 {string} string
//	@Failure   403 {string} string
//	@Failure   500 {string} string
//	@Security  CookieAuth
//	@Router    /api/workspaces/{id}/exec-audit/calls [get]
func (s *Server) getWorkspaceExecAuditCalls(w http.ResponseWriter, r *http.Request) {
	wsID := chi.URLParam(r, "id")
	if _, ok := s.requireWorkspaceMember(w, r, wsID); !ok {
		return
	}
	q := r.URL.Query()
	q.Set("workspace_id", wsID)
	r.URL.RawQuery = q.Encode()
	s.getInternalExecAuditCalls(w, r)
}

// getWorkspaceExecAuditCall returns one call in the caller's workspace.
//
//	@Summary   Get exec-audit call (workspace-scoped)
//	@Tags      Exec-Audit
//	@Produce   json
//	@Param     id       path  string  true  "Workspace ID"
//	@Param     call_id  path  string  true  "Call ID"
//	@Success   200 {object} AuditCallDetail
//	@Failure   401 {string} string
//	@Failure   403 {string} string
//	@Failure   404 {string} string
//	@Failure   500 {string} string
//	@Security  CookieAuth
//	@Router    /api/workspaces/{id}/exec-audit/calls/{call_id} [get]
func (s *Server) getWorkspaceExecAuditCall(w http.ResponseWriter, r *http.Request) {
	wsID := chi.URLParam(r, "id")
	if _, ok := s.requireWorkspaceMember(w, r, wsID); !ok {
		return
	}
	id := chi.URLParam(r, "call_id")
	call, err := s.DB.GetAuditCall(id)
	if err == sql.ErrNoRows || (err == nil && call.WorkspaceID != wsID) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		log.Printf("exec-audit: GetAuditCall: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.getInternalExecAuditCall(w, r)
}

// getWorkspaceExecAuditCallPayload streams the raw request payload for a call.
//
//	@Summary   Get exec-audit call request payload (workspace-scoped)
//	@Tags      Exec-Audit
//	@Produce   application/octet-stream
//	@Param     id       path   string  true   "Workspace ID"
//	@Param     call_id  path   string  true   "Call ID"
//	@Success   200 {string} binary
//	@Failure   401 {string} string
//	@Failure   403 {string} string
//	@Failure   404 {string} string
//	@Failure   500 {string} string
//	@Security  CookieAuth
//	@Router    /api/workspaces/{id}/exec-audit/calls/{call_id}/payload [get]
func (s *Server) getWorkspaceExecAuditCallPayload(w http.ResponseWriter, r *http.Request) {
	wsID := chi.URLParam(r, "id")
	if _, ok := s.requireWorkspaceMember(w, r, wsID); !ok {
		return
	}
	callID := chi.URLParam(r, "call_id")
	call, err := s.DB.GetAuditCall(callID)
	if err == sql.ErrNoRows {
		http.Error(w, "call not found", http.StatusNotFound)
		return
	}
	if err != nil {
		log.Printf("exec-audit: GetAuditCall: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if call.WorkspaceID != wsID {
		http.Error(w, "not found", http.StatusNotFound) // tenant isolation
		return
	}
	payloadID := call.RequestPayloadID
	if payloadID == nil {
		http.Error(w, "payload not stored (size exceeded cap)", http.StatusNotFound)
		return
	}
	p, err := s.DB.GetAuditPayload(*payloadID)
	if err != nil {
		log.Printf("exec-audit: GetAuditPayload: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	raw, err := zstdDecompress(p.Compressed)
	if err != nil {
		log.Printf("exec-audit: decompress: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.Itoa(len(raw)))
	_, _ = w.Write(raw)
}

// ---------- helpers ----------

const auditPreviewBytes = 8 * 1024

// previewPayload best-effort decodes a stored payload to a utf8 preview
// (first auditPreviewBytes). Returns "" if the payload row is missing
// (expected — retention may have pruned it). Other errors (DB connection,
// corrupted blob) are logged before returning "" so a blank preview in
// the UI is never silently hiding an integrity bug.
func previewPayload(d *db.DB, id string) string {
	p, err := d.GetAuditPayload(id)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			log.Printf("exec-audit: preview payload id=%s db: %v", id, err)
		}
		return ""
	}
	raw, err := zstdDecompress(p.Compressed)
	if err != nil {
		log.Printf("exec-audit: preview payload id=%s decompress: %v", id, err)
		return ""
	}
	if len(raw) > auditPreviewBytes {
		raw = raw[:auditPreviewBytes]
	}
	return string(raw) // utf8 lossy if binary — acceptable for preview
}

// recordTriageContext returns a short space-separated string with the
// record kind plus the most useful identifiers per kind, for log lines
// when a record can't be applied. Keeps log volume bounded while giving
// operators enough to find the offending bridge in other logs.
func recordTriageContext(rec *pb.WALRecord) string {
	if rec == nil {
		return "kind=nil"
	}
	switch b := rec.Body.(type) {
	case *pb.WALRecord_SessionOpen:
		op := b.SessionOpen
		return fmt.Sprintf("kind=SessionOpen ws=%s exe=%s stream=%s",
			op.GetWorkspaceId(), op.GetExeId(), op.GetStreamId())
	case *pb.WALRecord_SessionClose:
		return fmt.Sprintf("kind=SessionClose session=%s", b.SessionClose.GetSessionId())
	case *pb.WALRecord_CallStart:
		cs := b.CallStart
		return fmt.Sprintf("kind=CallStart ws=%s exe=%s source=%s method=%s",
			cs.GetWorkspaceId(), cs.GetExeId(), cs.GetSource(), cs.GetRpcMethod())
	default:
		return "kind=unknown"
	}
}

func parseSessionsFilter(q url.Values) (db.ListAuditSessionsFilter, error) {
	f := db.ListAuditSessionsFilter{
		WorkspaceID: q.Get("workspace_id"),
		ExeID:       q.Get("exe_id"),
		UserID:      q.Get("user_id"),
		TurnID:      q.Get("turn_id"),
	}
	if f.WorkspaceID == "" {
		return f, errors.New("workspace_id required")
	}
	if s := q.Get("since"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return f, fmt.Errorf("since: %w", err)
		}
		f.Since = t
	}
	if s := q.Get("until"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return f, fmt.Errorf("until: %w", err)
		}
		f.Until = t
	}
	if s := q.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil {
			return f, fmt.Errorf("limit: %w", err)
		}
		f.Limit = n
	}
	return f, nil
}

func parseCallsFilter(q url.Values) (db.ListAuditCallsFilter, error) {
	f := db.ListAuditCallsFilter{
		WorkspaceID: q.Get("workspace_id"),
		SessionID:   q.Get("session_id"),
		ExeID:       q.Get("exe_id"),
		UserID:      q.Get("user_id"),
		Source:      q.Get("source"),
		RPCMethod:   q.Get("method"),
	}
	if f.WorkspaceID == "" {
		return f, errors.New("workspace_id required")
	}
	if s := q.Get("since"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return f, fmt.Errorf("since: %w", err)
		}
		f.Since = t
	}
	if s := q.Get("until"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return f, fmt.Errorf("until: %w", err)
		}
		f.Until = t
	}
	if s := q.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil {
			return f, fmt.Errorf("limit: %w", err)
		}
		f.Limit = n
	}
	return f, nil
}

func sessionToDTO(s db.AuditSession) AuditSessionSummary {
	return AuditSessionSummary{
		ID:              s.ID,
		WorkspaceID:     s.WorkspaceID,
		UserID:          ptrStr(s.UserID),
		ExeID:           s.ExeID,
		TurnID:          ptrStr(s.TurnID),
		StreamID:        s.StreamID,
		ClientIP:        ptrStr(s.ClientIP),
		OpenedAt:        s.OpenedAt.Format(time.RFC3339),
		ClosedAt:        ptrTimeRFC(s.ClosedAt),
		CloseReason:     ptrStr(s.CloseReason),
		FramesToBackend: s.FramesToBackend,
		FramesToClient:  s.FramesToClient,
		BytesToBackend:  s.BytesToBackend,
		BytesToClient:   s.BytesToClient,
	}
}

func sessionsToDTO(in []db.AuditSession) []AuditSessionSummary {
	out := make([]AuditSessionSummary, 0, len(in))
	for _, s := range in {
		out = append(out, sessionToDTO(s))
	}
	return out
}

func callToDTO(c db.AuditCall) AuditCallSummary {
	return AuditCallSummary{
		ID:            c.ID,
		SessionID:     ptrStr(c.SessionID),
		WorkspaceID:   c.WorkspaceID,
		UserID:        ptrStr(c.UserID),
		ExeID:         c.ExeID,
		Source:        c.Source,
		RPCID:         ptrStr(c.RPCID),
		RPCMethod:     ptrStr(c.RPCMethod),
		RPCKind:       ptrStr(c.RPCKind),
		RequestSize:   c.RequestSize,
		RequestSha256: ptrStr(c.RequestSha256),
		StartedAt:     c.StartedAt.Format(time.RFC3339),
	}
}

func callsToDTO(in []db.AuditCall) []AuditCallSummary {
	out := make([]AuditCallSummary, 0, len(in))
	for _, c := range in {
		out = append(out, callToDTO(c))
	}
	return out
}

func ptrStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func ptrTimeRFC(p *time.Time) string {
	if p == nil {
		return ""
	}
	return p.Format(time.RFC3339)
}
