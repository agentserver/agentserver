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
	"sync"
	"time"

	pb "github.com/agentserver/agentserver/internal/server/exec_audit_pb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

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

	stopSync chan struct{}
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
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(body)))
	if _, err := w.cur.Write(lenBuf[:]); err != nil {
		return fmt.Errorf("wal: write len: %w", err)
	}
	if _, err := w.cur.Write(body); err != nil {
		return fmt.Errorf("wal: write body: %w", err)
	}
	w.curSize += int64(4 + len(body))
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

// Close stops the fsync goroutine, flushes, and closes the current file.
func (w *WAL) Close() error {
	close(w.stopSync)
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cur == nil {
		return nil
	}
	_ = w.cur.Sync()
	err := w.cur.Close()
	w.cur = nil
	return err
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
	var total int64
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
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
// in Dir (sorted by name). Use Next to pull one record at a time.
type WALReader struct {
	dir   string
	files []string
	cur   *os.File
	curIx int
}

func OpenWALReader(dir string) (*WALReader, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "wal-*.log"))
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	return &WALReader{dir: dir, files: matches}, nil
}

// Next reads one WAL record. Returns (rec, fileName, nil) on success or
// (nil, "", io.EOF) when all files are exhausted. Caller closes via Close.
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
			_ = r.cur.Close()
			r.cur = nil
			r.curIx++
			if err == io.EOF {
				continue
			}
			return nil, "", err
		}
		length := binary.BigEndian.Uint32(lenBuf[:])
		body := make([]byte, length)
		if _, err := io.ReadFull(r.cur, body); err != nil {
			return nil, "", err
		}
		var rec pb.WALRecord
		if err := proto.Unmarshal(body, &rec); err != nil {
			return nil, "", err
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
