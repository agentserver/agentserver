package audit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	pb "github.com/agentserver/agentserver/internal/server/exec_audit_pb"
	"google.golang.org/protobuf/proto"
)

// UploaderConfig configures NewUploader. WALDir, Cursor, UploadURL,
// GatewayID are required; remaining fields default to plan-spec values
// if zero.
type UploaderConfig struct {
	WALDir        string
	Cursor        *Cursor
	UploadURL     string
	UploadSecret  string
	BatchRecords  int
	BatchBytes    int
	FlushInterval time.Duration
	GatewayID     string
	Logger        *slog.Logger
	HTTPClient    *http.Client
	BackoffStart  time.Duration
	BackoffMax    time.Duration
}

// Uploader drives the WAL → agentserver upload loop. Single goroutine
// owns the WAL reader; concurrent calls to Run are not supported.
type Uploader struct {
	cfg UploaderConfig
	log *slog.Logger
	hc  *http.Client
}

// NewUploader validates config and returns the uploader. Refuses when
// UploadURL is set but UploadSecret is empty — the upload loop would
// hit a 401/403 forever (errAuthFatal), the goroutine would die, and
// audit records would silently accumulate on disk with no upload. The
// silent-failure mode that bug produces is the worst kind of
// operational disaster, so fail-fast at construction (I11 followup).
func NewUploader(cfg UploaderConfig) (*Uploader, error) {
	if cfg.UploadURL != "" && cfg.UploadSecret == "" {
		return nil, errors.New("uploader: UploadURL set but UploadSecret empty (would 401 forever)")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	if cfg.BackoffStart == 0 {
		cfg.BackoffStart = time.Second
	}
	if cfg.BackoffMax == 0 {
		cfg.BackoffMax = 5 * time.Minute
	}
	if cfg.BatchRecords <= 0 {
		cfg.BatchRecords = 200
	}
	if cfg.BatchBytes <= 0 {
		cfg.BatchBytes = 1 << 20
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = time.Second
	}
	return &Uploader{cfg: cfg, log: cfg.Logger, hc: cfg.HTTPClient}, nil
}

// Run drives uploads until ctx is canceled. Blocks; intended for
// `go u.Run(ctx)`. On 401/403 Run returns immediately (auth misconfig
// is operator-resolvable, not retryable).
func (u *Uploader) Run(ctx context.Context) {
	backoff := u.cfg.BackoffStart
	for {
		if ctx.Err() != nil {
			return
		}
		batch, perFile, totalBytes, err := u.readNextBatch()
		if err != nil {
			u.log.Warn("exec-audit uploader: read batch", "err", err)
			u.sleep(ctx, u.cfg.FlushInterval)
			continue
		}
		if len(batch.Records) == 0 {
			// W2: a batch with no records but non-empty perFile means
			// only corrupt records were skipped. Persist the cursor
			// advance so we don't re-scan the same poison bytes forever.
			if len(perFile) > 0 {
				for fname, n := range perFile {
					u.cfg.Cursor.Advance(fname, n)
				}
				if err := u.cfg.Cursor.Save(); err != nil {
					u.log.Warn("exec-audit uploader: cursor save after skip", "err", err)
				}
			}
			u.sleep(ctx, u.cfg.FlushInterval)
			continue
		}

		if err := u.postBatch(ctx, batch); err != nil {
			if errors.Is(err, errAuthFatal) {
				u.log.Error("exec-audit uploader: auth failure, stopping", "err", err)
				return
			}
			u.log.Warn("exec-audit uploader: post failed",
				"err", err, "backoff", backoff)
			u.sleep(ctx, backoff)
			backoff = nextBackoff(backoff, u.cfg.BackoffMax)
			continue
		}

		// Success — advance cursor by bytes shipped in this batch and persist.
		for fname, n := range perFile {
			u.cfg.Cursor.Advance(fname, n)
		}
		if err := u.cfg.Cursor.Save(); err != nil {
			u.log.Warn("exec-audit uploader: cursor save", "err", err)
		}
		u.log.Debug("exec-audit uploader: sent",
			"records", len(batch.Records), "bytes", totalBytes)
		backoff = u.cfg.BackoffStart
	}
}

var errAuthFatal = errors.New("auth failed")

// readNextBatch opens a fresh WAL reader (so newly-written records since
// last call are visible), seeks past cursor, and pulls up to
// BatchRecords / BatchBytes worth of records. Returns the batch + a
// per-file byte tally so the caller can Advance the cursor symmetrically
// on a successful POST.
//
// TODO(perf): reopening + re-seeking every iteration is O(uploaded
// bytes) per call. For high-throughput deployments we should hold the
// reader open across loop iterations and only reopen when the cursor
// rotates onto a new file. Not done in this commit because the cursor/
// rotation interplay is subtle and the WAL files are typically small
// (10 MiB max-default).
func (u *Uploader) readNextBatch() (*pb.BatchRecords, map[string]int64, int, error) {
	r, err := OpenWALReader(u.cfg.WALDir)
	if err != nil {
		return nil, nil, 0, err
	}
	defer r.Close()

	if err := r.SeekPastCursor(u.cfg.Cursor); err != nil {
		return nil, nil, 0, err
	}

	batch := &pb.BatchRecords{GatewayId: u.cfg.GatewayID}
	perFile := map[string]int64{}
	totalBytes := 0
	for {
		rec, fname, n, err := r.NextWithSize()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, 0, err
		}
		// W2 sentinel: rec=nil, n>0 means "corrupt record skipped".
		// Advance the cursor counter so the uploader moves past the
		// poison bytes on the next commit, but don't include in batch.
		if rec == nil {
			if n > 0 {
				perFile[fname] += n
			}
			continue
		}
		batch.Records = append(batch.Records, rec)
		perFile[fname] += n
		totalBytes += int(n)
		if len(batch.Records) >= u.cfg.BatchRecords {
			break
		}
		if totalBytes >= u.cfg.BatchBytes {
			break
		}
	}
	return batch, perFile, totalBytes, nil
}

// postBatch ships one batch and classifies the response. Returns
// errAuthFatal on 401/403 (caller stops Run); returns a transient error
// on 5xx/429/network (caller backs off); returns nil on 200.
func (u *Uploader) postBatch(ctx context.Context, batch *pb.BatchRecords) error {
	body, err := proto.Marshal(batch)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.cfg.UploadURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-protobuf")
	if u.cfg.UploadSecret != "" {
		req.Header.Set("X-Internal-Secret", u.cfg.UploadSecret)
	}
	resp, err := u.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return fmt.Errorf("%w: status %d", errAuthFatal, resp.StatusCode)
	}
	if resp.StatusCode >= 500 || resp.StatusCode == 429 {
		return fmt.Errorf("server: %d", resp.StatusCode)
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("unexpected: %d", resp.StatusCode)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

func (u *Uploader) sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

func nextBackoff(cur, max time.Duration) time.Duration {
	next := cur * 2
	if next > max {
		return max
	}
	return next
}
