package codexexecgateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/agentserver/agentserver/internal/codexexecgateway/audit"
	"github.com/agentserver/agentserver/internal/codexexecgateway/relay"
	"github.com/go-chi/chi/v5"
)

// relayCountingReader counts bytes read AND hashes them, so the relay
// audit record can carry size + sha256 of the actual stream without
// buffering the whole body (which could be GB).
type relayCountingReader struct {
	r io.Reader
	h hash.Hash
	n int64
}

func newRelayCountingReader(r io.Reader) *relayCountingReader {
	return &relayCountingReader{r: r, h: sha256.New()}
}

func (c *relayCountingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 {
		c.h.Write(p[:n])
		c.n += int64(n)
	}
	return n, err
}

func (c *relayCountingReader) Sum() string { return hex.EncodeToString(c.h.Sum(nil)) }

// relayCountingWriter is the symmetric counter for the GET path.
type relayCountingWriter struct {
	w http.ResponseWriter
	h hash.Hash
	n int64
}

func newRelayCountingWriter(w http.ResponseWriter) *relayCountingWriter {
	return &relayCountingWriter{w: w, h: sha256.New()}
}

func (c *relayCountingWriter) Header() http.Header         { return c.w.Header() }
func (c *relayCountingWriter) WriteHeader(statusCode int)  { c.w.WriteHeader(statusCode) }
func (c *relayCountingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	if n > 0 {
		c.h.Write(p[:n])
		c.n += int64(n)
	}
	return n, err
}
func (c *relayCountingWriter) Sum() string { return hex.EncodeToString(c.h.Sum(nil)) }

// ────────────────────────────────────────────────────────────────────
// Public PUT/GET endpoints (ticket Bearer auth)
// ────────────────────────────────────────────────────────────────────

// handleRelayPut accepts the upload half of a relay session. The
// ticket must match between URL and Authorization header (defence in
// depth: prevents accidental cross-ticket use if a proxy rewrites
// the path).
func (s *Server) handleRelayPut(w http.ResponseWriter, r *http.Request) {
	if s.relayRegistry == nil {
		http.Error(w, "relay disabled (no public HTTPS base URL configured)", http.StatusNotFound)
		return
	}
	urlTicket := chi.URLParam(r, "ticket")
	authTicket, ok := relay.ExtractBearerTicket(r.Header.Get("Authorization"))
	if !ok || authTicket != urlTicket {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	rel, found := s.relayRegistry.Lookup(urlTicket)
	if !found {
		http.Error(w, "ticket not found or expired", http.StatusGone)
		return
	}

	// Audit: relay PUT is a logical "instruction to codex-exec" (DestExeID
	// will write the bytes locally). We never inline the body — could be
	// GB — only record size + sha256 in the CallEnd's Response summary.
	rec := s.recorder
	if rec == nil {
		rec = audit.NewNoopRecorder()
	}
	callID := rec.CallStart(audit.CallStartMeta{
		WorkspaceID: rel.WorkspaceID,
		ExeID:       rel.DestExeID,
		Source:      "relay",
		RPCMethod:   "relay_put",
		StartedAt:   time.Now().UTC(),
	})
	counted := newRelayCountingReader(r.Body)
	status, body := rel.AcceptPut(counted)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)

	end := audit.CallEndMeta{
		CompletedAt: time.Now().UTC(),
		Response: []byte(fmt.Sprintf(
			`{"relay_put_bytes":%d,"relay_put_sha256":"%s"}`,
			counted.n, counted.Sum())),
	}
	if status >= 400 {
		end.IsError = true
		end.ErrorSummary = string(body)
	}
	rec.CallEnd(callID, end)
}

// handleRelayGet accepts the download half. Streams body chunked.
func (s *Server) handleRelayGet(w http.ResponseWriter, r *http.Request) {
	if s.relayRegistry == nil {
		http.Error(w, "relay disabled (no public HTTPS base URL configured)", http.StatusNotFound)
		return
	}
	urlTicket := chi.URLParam(r, "ticket")
	authTicket, ok := relay.ExtractBearerTicket(r.Header.Get("Authorization"))
	if !ok || authTicket != urlTicket {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	rel, found := s.relayRegistry.Lookup(urlTicket)
	if !found {
		http.Error(w, "ticket not found or expired", http.StatusGone)
		return
	}
	// Audit: relay GET is reading bytes FROM SourceExeID. Same size+sha
	// pattern as PUT — never inline the streamed body.
	rec := s.recorder
	if rec == nil {
		rec = audit.NewNoopRecorder()
	}
	callID := rec.CallStart(audit.CallStartMeta{
		WorkspaceID: rel.WorkspaceID,
		ExeID:       rel.SourceExeID,
		Source:      "relay",
		RPCMethod:   "relay_get",
		StartedAt:   time.Now().UTC(),
	})

	// Set Content-Type before AcceptGet because the pairing goroutine's
	// first Write implicitly calls WriteHeader(200) (success path) and
	// any headers we set after that would be silently dropped.
	//
	// We do NOT set Transfer-Encoding: chunked — Go's HTTP server
	// applies it automatically when there's no Content-Length, and
	// setting it manually here would conflict with the framework's own
	// framing on the error path (small JSON body).
	w.Header().Set("Content-Type", "application/octet-stream")
	counted := newRelayCountingWriter(w)
	status, body := rel.AcceptGet(counted)
	// status==0: streamed successfully; headers + 200 already flushed.
	// status!=0: pairing failed before any byte was written, emit the
	// status + JSON body. Override the Content-Type since the body is
	// JSON, not octet-stream.
	if status != 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}

	end := audit.CallEndMeta{
		CompletedAt: time.Now().UTC(),
		Response: []byte(fmt.Sprintf(
			`{"relay_get_bytes":%d,"relay_get_sha256":"%s"}`,
			counted.n, counted.Sum())),
	}
	if status >= 400 {
		end.IsError = true
		end.ErrorSummary = string(body)
	}
	rec.CallEnd(callID, end)
}

// ────────────────────────────────────────────────────────────────────
// Internal: relay ticket mint (X-Internal-Secret auth applied at route)
// ────────────────────────────────────────────────────────────────────

type relayCreateRequest struct {
	WorkspaceID string `json:"workspace_id"`
	SourceExeID string `json:"source_exe_id"`
	DestExeID   string `json:"dest_exe_id"`
	TTLSeconds  int    `json:"ttl_seconds,omitempty"`
	MaxBytes    int64  `json:"max_bytes,omitempty"`
}

type relayCreateResponse struct {
	Ticket      string    `json:"ticket"`
	UploadURL   string    `json:"upload_url"`
	DownloadURL string    `json:"download_url"`
	ExpiresAt   time.Time `json:"expires_at"`
}

func (s *Server) handleRelayCreate(w http.ResponseWriter, r *http.Request) {
	if s.relayRegistry == nil || s.config.PublicHTTPSBaseURL == "" {
		writeJSONErr(w, http.StatusServiceUnavailable, "relay not enabled (PublicHTTPSBaseURL unset)")
		return
	}

	var req relayCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.WorkspaceID == "" || req.SourceExeID == "" || req.DestExeID == "" {
		writeJSONErr(w, http.StatusBadRequest, "workspace_id, source_exe_id, dest_exe_id required")
		return
	}

	// Workspace ownership check — both executors must belong to the
	// caller's workspace. Two separate queries keep error messages
	// specific without leaking information about the other side.
	if s.store != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		for _, exeID := range []string{req.SourceExeID, req.DestExeID} {
			owns, err := s.store.OwnsExecutor(ctx, req.WorkspaceID, exeID)
			if err != nil {
				writeJSONErr(w, http.StatusInternalServerError, "ownership check failed")
				return
			}
			if !owns {
				writeJSONErr(w, http.StatusForbidden, "executor not in workspace: "+exeID)
				return
			}
		}
	}

	ttl := time.Duration(req.TTLSeconds) * time.Second
	rel, err := s.relayRegistry.Create(relay.CreateOptions{
		WorkspaceID: req.WorkspaceID,
		SourceExeID: req.SourceExeID,
		DestExeID:   req.DestExeID,
		TTL:         ttl, // 0 → registry default
		MaxBytes:    req.MaxBytes,
	})
	if err != nil {
		switch err {
		case relay.ErrWorkspaceCapReached:
			writeJSONErr(w, http.StatusTooManyRequests, err.Error())
		default:
			writeJSONErr(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	url := strings.TrimRight(s.config.PublicHTTPSBaseURL, "/") + "/relay/" + rel.Ticket
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(relayCreateResponse{
		Ticket:      rel.Ticket,
		UploadURL:   url,
		DownloadURL: url,
		ExpiresAt:   rel.ExpiresAt,
	})
}

func writeJSONErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
