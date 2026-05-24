package audit

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	pb "github.com/agentserver/agentserver/internal/server/exec_audit_pb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// maxWALFrameBytes caps the body of a single WAL record. Realistic body
// size is bounded above by PayloadMaxBytes (4 MiB default) + protobuf
// metadata overhead; 64 MiB leaves ample headroom while preventing
// integer-overflow / runaway allocation if a future code path bypasses
// the payload cap.
const maxWALFrameBytes = 64 * 1024 * 1024

// WALConfig configures an OpenWAL. All fields are required (zero values
// are not sensible defaults — derive from Config.WAL* in production).
type WALConfig struct {
	Dir            string
	FsyncInterval  time.Duration
	FsyncRecords   int
	FileMaxBytes   int64
	DiskQuotaBytes int64
	Overflow       string // "fail" | "drop"
	Logger         *slog.Logger
}

// WAL is an append-only, length-prefixed protobuf log on disk. A single
// background goroutine fsyncs on tick. Append is mutex-serialized so
// callers can write from many goroutines (the production Recorder does).
type WAL struct {
	cfg    WALConfig
	logger *slog.Logger

	mu            sync.Mutex
	cur           *os.File
	curSize       int64
	recsSinceSync int

	stopSync  chan struct{}
	closeOnce sync.Once
}

// OpenWAL creates Dir if needed and opens (or rotates to) a fresh file.
// Starts the background fsync ticker. Caller must Close to stop it.
func OpenWAL(cfg WALConfig) (*WAL, error) {
	if cfg.Dir == "" {
		return nil, errors.New("wal: Dir required")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if err := os.MkdirAll(cfg.Dir, 0o750); err != nil {
		return nil, fmt.Errorf("wal: mkdir %s: %w", cfg.Dir, err)
	}
	w := &WAL{cfg: cfg, logger: cfg.Logger, stopSync: make(chan struct{})}
	if err := w.rotate(); err != nil {
		return nil, err
	}
	go w.fsyncLoop()
	return w, nil
}

// Append serializes one WAL record. Sets rec.WrittenAt to now if unset.
// Returns error if disk quota in "fail" mode is hit or if marshal/write
// fails (caller should treat as fatal and refuse the bridge).
func (w *WAL) Append(rec *pb.WALRecord) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cur == nil {
		return errors.New("wal: closed")
	}
	if err := w.enforceQuotaLocked(); err != nil {
		return err
	}
	if rec.WrittenAt == nil {
		rec.WrittenAt = timestamppb.New(time.Now().UTC())
	}
	body, err := proto.Marshal(rec)
	if err != nil {
		return fmt.Errorf("wal: marshal: %w", err)
	}
	// Defensive bounds check before allocation. Realistic body size is
	// at most PayloadMaxBytes (4 MiB default) + protobuf overhead, well
	// under maxWALFrameBytes. The check exists because the WALRecord
	// payload bytes ultimately come from network input (SDK request
	// body, env-mcp WS frame); guarding the allocation here means a
	// future caller path that bypasses PayloadMaxBytes still can't
	// trigger an integer-overflow in make() or a runaway allocation.
	if len(body) > maxWALFrameBytes {
		return fmt.Errorf("wal: record body %d bytes exceeds maxWALFrameBytes %d",
			len(body), maxWALFrameBytes)
	}
	// W1: Concatenate length-prefix + body into a single Write so a
	// short-write between the two doesn't leave a 4-byte stub that
	// desyncs the reader for the rest of the file. A failure of this
	// single Write may still leave a partial record on disk; WALReader
	// is hardened (W2) to skip records that fail to unmarshal.
	frame := make([]byte, 4+len(body))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(body)))
	copy(frame[4:], body)
	if _, err := w.cur.Write(frame); err != nil {
		return fmt.Errorf("wal: write: %w", err)
	}
	w.curSize += int64(len(frame))
	w.recsSinceSync++

	if w.recsSinceSync >= w.cfg.FsyncRecords {
		if err := w.cur.Sync(); err != nil {
			return fmt.Errorf("wal: sync: %w", err)
		}
		w.recsSinceSync = 0
	}
	if w.curSize >= w.cfg.FileMaxBytes {
		if err := w.rotateLocked(); err != nil {
			return err
		}
	}
	return nil
}

// Sync forces an fsync on the current file. Used in graceful shutdown
// to flush any buffered records before exit.
func (w *WAL) Sync() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cur == nil {
		return nil
	}
	w.recsSinceSync = 0
	return w.cur.Sync()
}

// Close stops the fsync goroutine, flushes, and closes the current
// file. Safe to call multiple times — guarded by sync.Once so the
// channel close doesn't panic on second invocation (T6 review followup).
func (w *WAL) Close() error {
	var closeErr error
	w.closeOnce.Do(func() {
		close(w.stopSync)
		w.mu.Lock()
		defer w.mu.Unlock()
		if w.cur == nil {
			return
		}
		_ = w.cur.Sync()
		closeErr = w.cur.Close()
		w.cur = nil
	})
	return closeErr
}

func (w *WAL) fsyncLoop() {
	t := time.NewTicker(w.cfg.FsyncInterval)
	defer t.Stop()
	for {
		select {
		case <-w.stopSync:
			return
		case <-t.C:
			w.mu.Lock()
			if w.cur != nil && w.recsSinceSync > 0 {
				if err := w.cur.Sync(); err != nil {
					w.logger.Warn("wal: periodic sync", "err", err)
				} else {
					w.recsSinceSync = 0
				}
			}
			w.mu.Unlock()
		}
	}
}

func (w *WAL) rotate() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.rotateLocked()
}

func (w *WAL) rotateLocked() error {
	if w.cur != nil {
		_ = w.cur.Sync()
		_ = w.cur.Close()
		w.cur = nil
	}
	name := time.Now().UTC().Format("wal-20060102-150405.log")
	path := filepath.Join(w.cfg.Dir, name)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
	if err != nil {
		return fmt.Errorf("wal: open %s: %w", path, err)
	}
	w.cur = f
	w.curSize = 0
	w.recsSinceSync = 0
	return nil
}

// enforceQuotaLocked is called with w.mu held. Returns nil if under
// quota OR if the "drop" mode successfully unlinked old files to fit.
// Returns error in "fail" mode (or "drop" mode that couldn't shrink
// enough).
func (w *WAL) enforceQuotaLocked() error {
	entries, err := os.ReadDir(w.cfg.Dir)
	if err != nil {
		return fmt.Errorf("wal: readdir: %w", err)
	}
	// W4: Filter to wal-*.log only. Previously this counted every file
	// in the directory (cursor.json, partial uploads, anything else) and
	// under "drop" mode could even unlink cursor.json — silently
	// resetting the uploader's progress. Now: count only wal files;
	// drop only wal files.
	var total int64
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "wal-") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			w.logger.Warn("wal: stat in quota check", "name", e.Name(), "err", err)
			continue
		}
		total += info.Size()
	}
	if total < w.cfg.DiskQuotaBytes {
		return nil
	}
	if w.cfg.Overflow == "drop" {
		// Drop oldest files (sorted by name = chronological since the
		// naming convention is wal-YYYYMMDD-HHMMSS.log).
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			if !strings.HasPrefix(e.Name(), "wal-") {
				continue
			}
			names = append(names, e.Name())
		}
		sort.Strings(names)
		for _, n := range names {
			if total < w.cfg.DiskQuotaBytes {
				break
			}
			// Never unlink the current file out from under ourselves.
			if w.cur != nil && filepath.Base(w.cur.Name()) == n {
				continue
			}
			p := filepath.Join(w.cfg.Dir, n)
			info, err := os.Stat(p)
			if err != nil {
				w.logger.Warn("wal: stat for drop", "path", p, "err", err)
				continue
			}
			if err := os.Remove(p); err != nil {
				w.logger.Warn("wal: drop unlink", "path", p, "err", err)
				continue
			}
			total -= info.Size()
		}
		if total >= w.cfg.DiskQuotaBytes {
			return fmt.Errorf("wal: disk quota %d still exceeded after drop", w.cfg.DiskQuotaBytes)
		}
		return nil
	}
	// "fail" mode: caller decides what to do (production: refuse bridges).
	return fmt.Errorf("wal: disk quota %d exceeded (mode=fail)", w.cfg.DiskQuotaBytes)
}

// WALReader is a single-consumer forward iterator over every WAL file
// in Dir (sorted by name). Use Next or NextWithSize to pull one record
// at a time.
type WALReader struct {
	dir       string
	files     []string
	cur       *os.File
	curIx     int
	cursor    *Cursor // optional; if set, NextWithSize seeks past offset on first open
	seekedCur bool    // whether we've seeked into the current file already
	logger    *slog.Logger

	// W2: corrupt-record telemetry. NextWithSize increments this every
	// time a record fails to unmarshal and is skipped (rather than
	// returning the error and stalling the Uploader forever on the same
	// poison record). Exposed via CorruptRecordsSkipped() for tests +
	// /metrics.
	corruptRecordsSkipped atomic.Int64
}

func OpenWALReader(dir string) (*WALReader, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "wal-*.log"))
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	return &WALReader{dir: dir, files: matches, logger: slog.Default()}, nil
}

// CorruptRecordsSkipped returns the number of records this reader has
// skipped due to proto.Unmarshal failure since it was opened.
func (r *WALReader) CorruptRecordsSkipped() int64 {
	return r.corruptRecordsSkipped.Load()
}

// SetLogger lets callers route W2 corrupt-record errors through their
// own slog handler. Optional; defaults to slog.Default().
func (r *WALReader) SetLogger(l *slog.Logger) {
	if l != nil {
		r.logger = l
	}
}

// Next reads one WAL record. Returns (rec, fileName, nil) on success or
// (nil, "", io.EOF) when all files are exhausted. Caller closes via Close.
//
// W2: records that fail proto.Unmarshal are skipped (counter
// incremented, logged) so a single poison record can't stall the reader
// for an entire file.
func (r *WALReader) Next() (*pb.WALRecord, string, error) {
	for {
		if r.cur == nil {
			if r.curIx >= len(r.files) {
				return nil, "", io.EOF
			}
			f, err := os.Open(r.files[r.curIx])
			if err != nil {
				return nil, "", err
			}
			r.cur = f
		}
		var lenBuf [4]byte
		if _, err := io.ReadFull(r.cur, lenBuf[:]); err != nil {
			// Partial length prefix at EOF — treat as truncated file end
			// (W2 graceful recovery from torn writes).
			if err == io.ErrUnexpectedEOF {
				r.logger.Error("wal: truncated length prefix, skipping rest of file",
					"file", filepath.Base(r.files[r.curIx]))
				r.corruptRecordsSkipped.Add(1)
			}
			_ = r.cur.Close()
			r.cur = nil
			r.curIx++
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				continue
			}
			return nil, "", err
		}
		length := binary.BigEndian.Uint32(lenBuf[:])
		// Bound the allocation: a corrupt length field on disk could
		// otherwise request gigabytes (or worse, overflow int) before
		// io.ReadFull discovers the truncation. Skip the rest of the
		// file the same way a truncated body is handled.
		if length > maxWALFrameBytes {
			r.logger.Error("wal: implausible record length, skipping rest of file",
				"file", filepath.Base(r.files[r.curIx]), "length", length, "cap", maxWALFrameBytes)
			r.corruptRecordsSkipped.Add(1)
			_ = r.cur.Close()
			r.cur = nil
			r.curIx++
			continue
		}
		body := make([]byte, length)
		if _, err := io.ReadFull(r.cur, body); err != nil {
			// Torn write — file ended mid-body. Skip rest of file.
			r.logger.Error("wal: truncated record body, skipping rest of file",
				"file", filepath.Base(r.files[r.curIx]), "want", length, "err", err)
			r.corruptRecordsSkipped.Add(1)
			_ = r.cur.Close()
			r.cur = nil
			r.curIx++
			continue
		}
		var rec pb.WALRecord
		if err := proto.Unmarshal(body, &rec); err != nil {
			r.logger.Error("wal: corrupt record, skipping",
				"file", filepath.Base(r.files[r.curIx]), "err", err)
			r.corruptRecordsSkipped.Add(1)
			continue
		}
		return &rec, filepath.Base(r.files[r.curIx]), nil
	}
}

func (r *WALReader) Close() error {
	if r.cur != nil {
		_ = r.cur.Close()
		r.cur = nil
	}
	return nil
}

// SeekPastCursor positions the reader to start at the first byte past
// the cursor's offset for each file. Fully-consumed files are dropped
// from the iteration; the first remaining file (if partially consumed)
// has its seek applied lazily on the first NextWithSize call.
func (r *WALReader) SeekPastCursor(c *Cursor) error {
	out := r.files[:0]
	for _, p := range r.files {
		name := filepath.Base(p)
		info, err := os.Stat(p)
		if err != nil {
			return err
		}
		if c.Offset(name) >= info.Size() {
			continue // already fully consumed
		}
		out = append(out, p)
	}
	r.files = out
	r.curIx = 0
	r.cursor = c
	r.seekedCur = false
	return nil
}

// NextWithSize is like Next but also returns the byte count consumed
// (4-byte length prefix + body). The uploader uses this to Advance the
// Cursor by exactly the bytes shipped in a successful batch.
//
// Requires SeekPastCursor to have been called first (it's how the
// reader knows the per-file starting offset).
//
// W2 sentinel: when a record fails proto.Unmarshal, this returns
// (nil, fname, 4+length, nil) — rec is nil but n > 0. The Uploader
// interprets this as "advance cursor by n, do not include in batch" so
// a poison record doesn't stall the uploader forever. A truncated
// length-prefix or body at EOF returns (nil, fname, 0, io.EOF) for the
// file slot (the rest of the file is unrecoverable; the bytes-past-
// cursor remain on disk but will be skipped on the next reader open).
func (r *WALReader) NextWithSize() (*pb.WALRecord, string, int64, error) {
	for {
		if r.cur == nil {
			if r.curIx >= len(r.files) {
				return nil, "", 0, io.EOF
			}
			f, err := os.Open(r.files[r.curIx])
			if err != nil {
				return nil, "", 0, err
			}
			r.cur = f
			r.seekedCur = false
		}
		// Lazy-seek past the cursor offset on first read of each file.
		if !r.seekedCur {
			if r.cursor != nil {
				off := r.cursor.Offset(filepath.Base(r.files[r.curIx]))
				if off > 0 {
					if _, err := r.cur.Seek(off, io.SeekStart); err != nil {
						return nil, "", 0, err
					}
				}
			}
			r.seekedCur = true
		}
		fname := filepath.Base(r.files[r.curIx])
		var lenBuf [4]byte
		if _, err := io.ReadFull(r.cur, lenBuf[:]); err != nil {
			if err == io.ErrUnexpectedEOF {
				// Partial length prefix — torn write at file tail.
				r.logger.Error("wal: truncated length prefix, skipping rest of file",
					"file", fname)
				r.corruptRecordsSkipped.Add(1)
			}
			_ = r.cur.Close()
			r.cur = nil
			r.curIx++
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				continue
			}
			return nil, "", 0, err
		}
		length := binary.BigEndian.Uint32(lenBuf[:])
		// Bound the allocation: a corrupt length field on disk could
		// otherwise request gigabytes (or worse, overflow int) before
		// io.ReadFull discovers the truncation. Skip the rest of the
		// file the same way a truncated body is handled.
		if length > maxWALFrameBytes {
			r.logger.Error("wal: implausible record length, skipping rest of file",
				"file", filepath.Base(r.files[r.curIx]), "length", length, "cap", maxWALFrameBytes)
			r.corruptRecordsSkipped.Add(1)
			_ = r.cur.Close()
			r.cur = nil
			r.curIx++
			continue
		}
		body := make([]byte, length)
		if _, err := io.ReadFull(r.cur, body); err != nil {
			// Torn write — body truncated. Skip rest of file. The 4 bytes
			// of length prefix we already consumed can't be re-read; the
			// uploader's cursor advances on the NEXT successful record,
			// so leaving those 4 bytes unaccounted is harmless — they're
			// past the cursor offset and the file will be retired once
			// fully consumed (or never re-read once a newer wal file
			// rotates in).
			r.logger.Error("wal: truncated record body, skipping rest of file",
				"file", fname, "want", length, "err", err)
			r.corruptRecordsSkipped.Add(1)
			_ = r.cur.Close()
			r.cur = nil
			r.curIx++
			continue
		}
		var rec pb.WALRecord
		if err := proto.Unmarshal(body, &rec); err != nil {
			r.logger.Error("wal: corrupt record, skipping",
				"file", fname, "err", err)
			r.corruptRecordsSkipped.Add(1)
			// Sentinel: nil record, non-zero n — caller advances cursor
			// but does not append to batch.
			return nil, fname, int64(4 + length), nil
		}
		return &rec, fname, int64(4 + length), nil
	}
}
