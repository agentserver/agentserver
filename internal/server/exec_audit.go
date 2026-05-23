package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/agentserver/agentserver/internal/db"
	pb "github.com/agentserver/agentserver/internal/server/exec_audit_pb"
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
// CallEnd/SessionClose stamp completion fields on the existing row.
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
			log.Printf("exec-audit: apply failed id=%s err=%v", rec.GetId(), err)
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
	case *pb.WALRecord_CallEnd:
		ce := b.CallEnd
		var respPayloadID *string
		if raw := ce.GetResponseBytes(); len(raw) > 0 {
			id, err := s.upsertPayload(raw)
			if err != nil {
				return fmt.Errorf("upsert response payload: %w", err)
			}
			respPayloadID = &id
		}
		return s.DB.UpdateAuditCallEnd(
			ce.GetCallId(),
			ce.GetCompletedAt().AsTime().UTC(),
			ce.GetIsError(),
			ce.GetErrorSummary(),
			respPayloadID,
			int(ce.GetResponseSize()),
			strPtrOrNil(ce.GetResponseSha256()),
		)
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
