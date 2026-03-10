package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const basePort = 4096

// RegistryEntry represents a single agent registration keyed by (Dir, WorkspaceID).
type RegistryEntry struct {
	Dir          string `json:"dir"`
	Server       string `json:"server"`
	SandboxID    string `json:"sandbox_id"`
	TunnelToken  string `json:"tunnel_token"`
	WorkspaceID  string `json:"workspace_id"`
	Name         string `json:"name"`
	OpencodePort int    `json:"opencode_port"`
}

// Registry holds all agent registrations on this machine.
type Registry struct {
	Entries []*RegistryEntry `json:"entries"`
}

// Find returns the entry matching (dir, workspaceID), or nil if not found.
func (r *Registry) Find(dir, workspaceID string) *RegistryEntry {
	for _, e := range r.Entries {
		if e.Dir == dir && e.WorkspaceID == workspaceID {
			return e
		}
	}
	return nil
}

// FindByDir returns all entries for the given directory.
func (r *Registry) FindByDir(dir string) []*RegistryEntry {
	var result []*RegistryEntry
	for _, e := range r.Entries {
		if e.Dir == dir {
			result = append(result, e)
		}
	}
	return result
}

// Put adds or replaces an entry keyed by (Dir, WorkspaceID).
func (r *Registry) Put(entry *RegistryEntry) {
	for i, e := range r.Entries {
		if e.Dir == entry.Dir && e.WorkspaceID == entry.WorkspaceID {
			r.Entries[i] = entry
			return
		}
	}
	r.Entries = append(r.Entries, entry)
}

// Remove deletes the entry matching (dir, workspaceID).
// Returns true if an entry was removed, false if not found.
func (r *Registry) Remove(dir, workspaceID string) bool {
	for i, e := range r.Entries {
		if e.Dir == dir && e.WorkspaceID == workspaceID {
			r.Entries = append(r.Entries[:i], r.Entries[i+1:]...)
			return true
		}
	}
	return false
}

// NextPort returns the next available opencode port.
// Empty registry returns basePort; otherwise returns max(existing ports) + 1.
func (r *Registry) NextPort() int {
	if len(r.Entries) == 0 {
		return basePort
	}
	max := 0
	for _, e := range r.Entries {
		if e.OpencodePort > max {
			max = e.OpencodePort
		}
	}
	if max < basePort {
		return basePort
	}
	return max + 1
}

// DefaultRegistryDir returns the default directory for agentserver config.
func DefaultRegistryDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".agentserver")
}

// DefaultRegistryPath returns the default path for the registry file.
func DefaultRegistryPath() string {
	return filepath.Join(DefaultRegistryDir(), "registry.json")
}

// LoadRegistry reads the registry from disk.
// Returns an empty registry if the file does not exist.
func LoadRegistry(path string) (*Registry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Registry{}, nil
		}
		return nil, fmt.Errorf("read registry: %w", err)
	}
	var reg Registry
	if err := json.Unmarshal(data, &reg); err != nil {
		return nil, fmt.Errorf("parse registry: %w", err)
	}
	return &reg, nil
}

// SaveRegistry writes the registry to disk.
func SaveRegistry(path string, reg *Registry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create registry dir: %w", err)
	}
	data, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal registry: %w", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write registry: %w", err)
	}
	return nil
}

// MaybeMigrateLegacy migrates a legacy agent.json to registry.json if the
// registry does not already exist. Returns the migrated registry, or nil if
// no migration was needed. The legacy file is preserved.
func MaybeMigrateLegacy(legacyPath, registryPath, cwd string) (*Registry, error) {
	// If registry already exists on disk, skip migration.
	if _, err := os.Stat(registryPath); err == nil {
		return nil, nil
	}

	// Load legacy config.
	cfg, err := LoadConfig(legacyPath)
	if err != nil {
		return nil, fmt.Errorf("load legacy config: %w", err)
	}
	if cfg == nil || cfg.SandboxID == "" {
		return nil, nil
	}

	reg := &Registry{}
	reg.Put(&RegistryEntry{
		Dir:          cwd,
		Server:       cfg.Server,
		SandboxID:    cfg.SandboxID,
		TunnelToken:  cfg.TunnelToken,
		WorkspaceID:  cfg.WorkspaceID,
		Name:         cfg.Name,
		OpencodePort: basePort,
	})

	if err := SaveRegistry(registryPath, reg); err != nil {
		return nil, fmt.Errorf("save migrated registry: %w", err)
	}
	return reg, nil
}

// ---------------------------------------------------------------------------
// Legacy config (kept for migration and connect.go compatibility)
// ---------------------------------------------------------------------------

// Config holds the local agent's persistent configuration.
type Config struct {
	Server      string `json:"server"`
	SandboxID   string `json:"sandbox_id"`
	TunnelToken string `json:"tunnel_token"`
	WorkspaceID string `json:"workspace_id"`
	Name        string `json:"name"`
}

// DefaultConfigPath returns the default path for the legacy agent config file.
func DefaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".agentserver", "agent.json")
}

// LoadConfig reads the legacy agent config from disk.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return &cfg, nil
}

// SaveConfig writes the legacy agent config to disk.
func SaveConfig(path string, cfg *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}
