package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRegistryRoundTrip(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")

	// New registry, put an entry, save, reload, verify.
	reg := &Registry{}
	entry := &RegistryEntry{
		Dir:         "/home/user/project",
		Server:      "https://example.com",
		SandboxID:   "sbx-123",
		TunnelToken: "tok-abc",
		WorkspaceID: "ws-1",
		Name:        "my-agent",
		OpencodePort: 4096,
	}
	reg.Put(entry)

	if err := SaveRegistry(regPath, reg); err != nil {
		t.Fatalf("SaveRegistry: %v", err)
	}

	loaded, err := LoadRegistry(regPath)
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	if len(loaded.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(loaded.Entries))
	}

	got := loaded.Entries[0]
	if got.Dir != entry.Dir {
		t.Errorf("Dir = %q, want %q", got.Dir, entry.Dir)
	}
	if got.Server != entry.Server {
		t.Errorf("Server = %q, want %q", got.Server, entry.Server)
	}
	if got.SandboxID != entry.SandboxID {
		t.Errorf("SandboxID = %q, want %q", got.SandboxID, entry.SandboxID)
	}
	if got.TunnelToken != entry.TunnelToken {
		t.Errorf("TunnelToken = %q, want %q", got.TunnelToken, entry.TunnelToken)
	}
	if got.WorkspaceID != entry.WorkspaceID {
		t.Errorf("WorkspaceID = %q, want %q", got.WorkspaceID, entry.WorkspaceID)
	}
	if got.Name != entry.Name {
		t.Errorf("Name = %q, want %q", got.Name, entry.Name)
	}
	if got.OpencodePort != entry.OpencodePort {
		t.Errorf("OpencodePort = %d, want %d", got.OpencodePort, entry.OpencodePort)
	}
}

func TestRegistryLookup(t *testing.T) {
	reg := &Registry{}
	reg.Put(&RegistryEntry{
		Dir:         "/home/user/projectA",
		WorkspaceID: "ws-1",
		SandboxID:   "sbx-a1",
	})
	reg.Put(&RegistryEntry{
		Dir:         "/home/user/projectA",
		WorkspaceID: "ws-2",
		SandboxID:   "sbx-a2",
	})
	reg.Put(&RegistryEntry{
		Dir:         "/home/user/projectB",
		WorkspaceID: "ws-1",
		SandboxID:   "sbx-b1",
	})

	// Find by exact (dir, workspace).
	e := reg.Find("/home/user/projectA", "ws-1")
	if e == nil {
		t.Fatal("Find returned nil for existing entry")
	}
	if e.SandboxID != "sbx-a1" {
		t.Errorf("Find: SandboxID = %q, want %q", e.SandboxID, "sbx-a1")
	}

	// Find different workspace in same dir.
	e = reg.Find("/home/user/projectA", "ws-2")
	if e == nil {
		t.Fatal("Find returned nil for ws-2 entry")
	}
	if e.SandboxID != "sbx-a2" {
		t.Errorf("Find: SandboxID = %q, want %q", e.SandboxID, "sbx-a2")
	}

	// FindByDir returns all entries for a directory.
	entries := reg.FindByDir("/home/user/projectA")
	if len(entries) != 2 {
		t.Fatalf("FindByDir: got %d entries, want 2", len(entries))
	}

	entries = reg.FindByDir("/home/user/projectB")
	if len(entries) != 1 {
		t.Fatalf("FindByDir: got %d entries, want 1", len(entries))
	}

	// Miss cases.
	if e := reg.Find("/nonexistent", "ws-1"); e != nil {
		t.Error("Find should return nil for nonexistent dir")
	}
	if e := reg.Find("/home/user/projectA", "ws-999"); e != nil {
		t.Error("Find should return nil for nonexistent workspace")
	}
	if entries := reg.FindByDir("/nonexistent"); len(entries) != 0 {
		t.Error("FindByDir should return empty slice for nonexistent dir")
	}
}

func TestRegistryPutOverwrite(t *testing.T) {
	reg := &Registry{}
	reg.Put(&RegistryEntry{
		Dir:         "/home/user/project",
		WorkspaceID: "ws-1",
		SandboxID:   "sbx-old",
		Name:        "old-name",
	})

	// Overwrite with same (dir, workspace).
	reg.Put(&RegistryEntry{
		Dir:         "/home/user/project",
		WorkspaceID: "ws-1",
		SandboxID:   "sbx-new",
		Name:        "new-name",
	})

	if len(reg.Entries) != 1 {
		t.Fatalf("expected 1 entry after overwrite, got %d", len(reg.Entries))
	}

	e := reg.Find("/home/user/project", "ws-1")
	if e == nil {
		t.Fatal("Find returned nil after overwrite")
	}
	if e.SandboxID != "sbx-new" {
		t.Errorf("SandboxID = %q, want %q", e.SandboxID, "sbx-new")
	}
	if e.Name != "new-name" {
		t.Errorf("Name = %q, want %q", e.Name, "new-name")
	}
}

func TestRegistryRemove(t *testing.T) {
	reg := &Registry{}
	reg.Put(&RegistryEntry{
		Dir:         "/home/user/project",
		WorkspaceID: "ws-1",
		SandboxID:   "sbx-1",
	})
	reg.Put(&RegistryEntry{
		Dir:         "/home/user/project",
		WorkspaceID: "ws-2",
		SandboxID:   "sbx-2",
	})

	// Remove existing entry.
	ok := reg.Remove("/home/user/project", "ws-1")
	if !ok {
		t.Error("Remove returned false for existing entry")
	}
	if len(reg.Entries) != 1 {
		t.Fatalf("expected 1 entry after remove, got %d", len(reg.Entries))
	}
	if e := reg.Find("/home/user/project", "ws-1"); e != nil {
		t.Error("removed entry still found")
	}

	// Remaining entry is still there.
	if e := reg.Find("/home/user/project", "ws-2"); e == nil {
		t.Error("other entry was removed")
	}

	// Remove miss returns false.
	ok = reg.Remove("/home/user/project", "ws-999")
	if ok {
		t.Error("Remove returned true for nonexistent entry")
	}

	ok = reg.Remove("/nonexistent", "ws-2")
	if ok {
		t.Error("Remove returned true for nonexistent dir")
	}
}

func TestRegistryNextPort(t *testing.T) {
	reg := &Registry{}

	// Empty registry returns basePort.
	port := reg.NextPort()
	if port != basePort {
		t.Errorf("NextPort on empty registry = %d, want %d", port, basePort)
	}

	// After adding an entry at basePort, returns basePort+1.
	reg.Put(&RegistryEntry{
		Dir:          "/home/user/project",
		WorkspaceID:  "ws-1",
		OpencodePort: 4096,
	})
	port = reg.NextPort()
	if port != 4097 {
		t.Errorf("NextPort = %d, want 4097", port)
	}

	// Adding a higher port — next port fills the gap.
	reg.Put(&RegistryEntry{
		Dir:          "/home/user/project2",
		WorkspaceID:  "ws-1",
		OpencodePort: 4098,
	})
	port = reg.NextPort()
	if port != 4097 {
		t.Errorf("NextPort = %d, want 4097 (gap fill)", port)
	}

	// Fill the gap — next port is after the contiguous block.
	reg.Put(&RegistryEntry{
		Dir:          "/home/user/project3",
		WorkspaceID:  "ws-1",
		OpencodePort: 4097,
	})
	port = reg.NextPort()
	if port != 4099 {
		t.Errorf("NextPort = %d, want 4099", port)
	}

	// Removing an entry frees its port for reuse.
	reg.Remove("/home/user/project", "ws-1") // frees 4096
	port = reg.NextPort()
	if port != 4096 {
		t.Errorf("NextPort after remove = %d, want 4096 (reuse)", port)
	}
}

func TestMigrateLegacyConfig(t *testing.T) {
	dir := t.TempDir()
	legacyPath := filepath.Join(dir, "agent.json")
	regPath := filepath.Join(dir, "registry.json")
	cwd := "/home/user/myproject"

	// Write a legacy config.
	legacy := &Config{
		Server:      "https://example.com",
		SandboxID:   "sbx-legacy",
		TunnelToken: "tok-legacy",
		WorkspaceID: "ws-legacy",
		Name:        "legacy-agent",
	}
	if err := SaveConfig(legacyPath, legacy); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	// Migrate.
	migrated, err := MaybeMigrateLegacy(legacyPath, regPath, cwd)
	if err != nil {
		t.Fatalf("MaybeMigrateLegacy: %v", err)
	}
	if migrated == nil {
		t.Fatal("expected non-nil registry after migration")
	}
	if len(migrated.Entries) != 1 {
		t.Fatalf("migrated registry: expected 1 entry, got %d", len(migrated.Entries))
	}

	// Verify registry was persisted.
	reg, err := LoadRegistry(regPath)
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	if len(reg.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(reg.Entries))
	}

	e := reg.Entries[0]
	if e.Dir != cwd {
		t.Errorf("Dir = %q, want %q", e.Dir, cwd)
	}
	if e.Server != legacy.Server {
		t.Errorf("Server = %q, want %q", e.Server, legacy.Server)
	}
	if e.SandboxID != legacy.SandboxID {
		t.Errorf("SandboxID = %q, want %q", e.SandboxID, legacy.SandboxID)
	}
	if e.TunnelToken != legacy.TunnelToken {
		t.Errorf("TunnelToken = %q, want %q", e.TunnelToken, legacy.TunnelToken)
	}
	if e.WorkspaceID != legacy.WorkspaceID {
		t.Errorf("WorkspaceID = %q, want %q", e.WorkspaceID, legacy.WorkspaceID)
	}
	if e.Name != legacy.Name {
		t.Errorf("Name = %q, want %q", e.Name, legacy.Name)
	}
	if e.OpencodePort != basePort {
		t.Errorf("OpencodePort = %d, want %d", e.OpencodePort, basePort)
	}

	// Second call is a no-op (registry already exists).
	migrated2, err := MaybeMigrateLegacy(legacyPath, regPath, cwd)
	if err != nil {
		t.Fatalf("MaybeMigrateLegacy (second call): %v", err)
	}
	if migrated2 != nil {
		t.Fatal("expected nil when registry already exists")
	}

	reg, err = LoadRegistry(regPath)
	if err != nil {
		t.Fatalf("LoadRegistry after second migrate: %v", err)
	}
	if len(reg.Entries) != 1 {
		t.Fatalf("expected 1 entry after second migrate, got %d", len(reg.Entries))
	}

	// Verify legacy file still exists (not deleted).
	if _, err := os.Stat(legacyPath); os.IsNotExist(err) {
		t.Error("legacy config was deleted, expected it to remain")
	}
}
