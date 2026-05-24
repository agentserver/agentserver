package audit

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// Cursor tracks per-file upload progress for the audit WAL. The
// uploader Advances after a successful POST and Saves to persist.
// Atomic save via tmp + rename — a crash mid-save never leaves a
// corrupt canonical file.
type Cursor struct {
	mu      sync.Mutex
	path    string
	offsets map[string]int64
}

type cursorOnDisk struct {
	Files []cursorFile `json:"files"`
}

type cursorFile struct {
	Name           string `json:"name"`
	UploadedOffset int64  `json:"uploaded_offset"`
}

// OpenCursor loads the cursor file at path. A missing file is not an
// error — it returns a fresh Cursor reporting 0 for every offset.
func OpenCursor(path string) (*Cursor, error) {
	c := &Cursor{path: path, offsets: map[string]int64{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	var d cursorOnDisk
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, err
	}
	for _, f := range d.Files {
		c.offsets[f.Name] = f.UploadedOffset
	}
	return c, nil
}

// Offset returns the cumulative bytes already uploaded from a file
// (zero if not yet uploaded).
func (c *Cursor) Offset(name string) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.offsets[name]
}

// Advance adds n bytes to the cumulative offset of name. Caller passes
// the bytes consumed in a batch — Cursor accumulates.
func (c *Cursor) Advance(name string, n int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.offsets[name] += n
}

// Save persists the current state atomically. Writes to <path>.tmp then
// renames. Returns whatever rename / mkdir errors os reports.
func (c *Cursor) Save() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	d := cursorOnDisk{Files: make([]cursorFile, 0, len(c.offsets))}
	for k, v := range c.offsets {
		d.Files = append(d.Files, cursorFile{Name: k, UploadedOffset: v})
	}
	body, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	tmp := c.path + ".tmp"
	if err := os.MkdirAll(filepath.Dir(c.path), 0o750); err != nil {
		return err
	}
	if err := os.WriteFile(tmp, body, 0o640); err != nil {
		return err
	}
	return os.Rename(tmp, c.path)
}
