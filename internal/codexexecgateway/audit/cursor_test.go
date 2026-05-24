package audit_test

import (
	"path/filepath"
	"testing"

	"github.com/agentserver/agentserver/internal/codexexecgateway/audit"
)

func TestCursor_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	c, err := audit.OpenCursor(filepath.Join(dir, "cursor.json"))
	if err != nil {
		t.Fatal(err)
	}

	// Fresh cursor: zero offset for any file.
	if got := c.Offset("wal-1.log"); got != 0 {
		t.Fatalf("expected 0 for fresh cursor, got %d", got)
	}

	// Advance is cumulative (caller passes bytes consumed in the batch,
	// not the new absolute offset).
	c.Advance("wal-1.log", 1024)
	c.Advance("wal-1.log", 1024)
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}

	// Re-open and verify persistence.
	c2, err := audit.OpenCursor(filepath.Join(dir, "cursor.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got := c2.Offset("wal-1.log"); got != 2048 {
		t.Fatalf("expected 2048 after reload, got %d", got)
	}
}

func TestCursor_AtomicSave_NoTmpLeftover(t *testing.T) {
	dir := t.TempDir()
	c, err := audit.OpenCursor(filepath.Join(dir, "cursor.json"))
	if err != nil {
		t.Fatal(err)
	}
	c.Advance("wal-a.log", 100)
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}

	// After Save there should be no .tmp file left behind.
	matches, _ := filepath.Glob(filepath.Join(dir, "cursor*"))
	for _, m := range matches {
		if filepath.Ext(m) == ".tmp" {
			t.Fatalf("found leftover .tmp file: %s", m)
		}
	}
}

func TestCursor_OpenNonexistent_FreshState(t *testing.T) {
	dir := t.TempDir()
	c, err := audit.OpenCursor(filepath.Join(dir, "nonexistent.json"))
	if err != nil {
		t.Fatalf("OpenCursor on missing file should succeed with fresh state, got %v", err)
	}
	if got := c.Offset("any.log"); got != 0 {
		t.Fatalf("fresh cursor: expected 0, got %d", got)
	}
}
