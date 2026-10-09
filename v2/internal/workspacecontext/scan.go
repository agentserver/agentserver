// Package workspacecontext discovers project guidance on the executor. It
// never reads the harness filesystem or executes code from the repository.
package workspacecontext

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/agentserver/agentserver/v2/internal/workspaceauthority"
	"gopkg.in/yaml.v3"
)

const (
	Version               = 1
	MaxInstructionBytes   = 32 * 1024
	MaxSkillManifestBytes = 64 * 1024
	MaxSkills             = 256
	maxScanEntries        = 4096
	maxScanDirectories    = 512
	maxSkillDepth         = 8
	maxAncestors          = 32
	maxIndexBytes         = 128 * 1024
)

type Instruction struct {
	Path string `json:"path"`
	Text string `json:"text"`
}
type Skill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Path        string `json:"path"`
}
type Diagnostic struct {
	Path string `json:"path"`
	Code string `json:"code"`
}

// All paths are repository-relative, including ancestor skills. Readers must
// resolve these against the same frozen repository root, not the tool cwd.
type Snapshot struct {
	Version          int           `json:"version"`
	WorkingDirectory string        `json:"workingDirectory"`
	Instructions     []Instruction `json:"instructions"`
	Skills           []Skill       `json:"skills"`
	Diagnostics      []Diagnostic  `json:"diagnostics"`
}

// Scan must be called while the session's checkout is quiescent. os.Root
// enforces symlink containment even if an unexpected writer races a read.
// Limits are reported explicitly; guidance is never silently dropped.
func Scan(ctx context.Context, rootPath, cwd string) (Snapshot, error) {
	out := Snapshot{Version: Version, WorkingDirectory: cwd, Instructions: []Instruction{}, Skills: []Skill{}, Diagnostics: []Diagnostic{}}
	if err := workspaceauthority.ValidateWorkingDirectory(cwd); err != nil {
		return out, err
	}
	ancestors := []string{"."}
	if cwd != "." {
		for _, segment := range strings.Split(cwd, "/") {
			ancestors = append(ancestors, path.Join(ancestors[len(ancestors)-1], segment))
		}
	}
	if len(ancestors) > maxAncestors {
		return out, errors.New("project working directory exceeds context scan depth")
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return out, errors.New("project root unavailable")
	}
	defer root.Close()
	// A lexical ancestor chain must correspond to the actual cwd. In-root
	// symlink manifests are supported, but symlink cwd components are ambiguous.
	for _, dir := range ancestors {
		info, err := root.Lstat(dir)
		if err != nil || !info.IsDir() {
			return out, errors.New("project working directory must be a contained directory without symlink components")
		}
	}
	s := scanner{ctx: ctx, root: root, out: &out, skills: map[string]Skill{}}
	remaining := MaxInstructionBytes
	for _, dir := range ancestors {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		for _, filename := range []string{"AGENTS.override.md", "AGENTS.md"} {
			name := path.Join(dir, filename)
			raw, err := readBounded(root, name, MaxInstructionBytes+1)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return out, fmt.Errorf("project instruction %s could not be read safely", name)
			}
			if len(strings.TrimSpace(string(raw))) == 0 {
				continue
			}
			if len(raw) > remaining {
				raw = utf8Prefix(raw, remaining)
				s.diagnostic(name, "instruction_limit")
			}
			if !utf8.Valid(raw) {
				return out, fmt.Errorf("project instruction %s is not UTF-8", name)
			}
			if len(raw) > 0 {
				out.Instructions = append(out.Instructions, Instruction{Path: name, Text: string(raw)})
				remaining -= len(raw)
			}
			break
		}
	}
	// Compatibility roots are indexed first; canonical .agents/skills wins
	// same-name collisions at the same directory. More-specific cwd ancestors
	// then override root-level metadata deterministically.
	for _, dir := range ancestors {
		for _, skillsRoot := range []string{"skills", ".dsh/skills", ".codex/skills", ".agents/skills"} {
			if err := s.walk(path.Join(dir, skillsRoot), 0); err != nil {
				return out, err
			}
		}
	}
	for _, skill := range s.skills {
		out.Skills = append(out.Skills, skill)
	}
	sort.Slice(out.Skills, func(i, j int) bool { return out.Skills[i].Path < out.Skills[j].Path })
	return out, nil
}

type scanner struct {
	ctx                              context.Context
	root                             *os.Root
	out                              *Snapshot
	skills                           map[string]Skill
	entries, directories, indexBytes int
	limitReported                    bool
	ancestors                        []os.FileInfo
}

func (s *scanner) diagnostic(name, code string) {
	if len(s.out.Diagnostics) == MaxSkills-1 {
		s.out.Diagnostics = append(s.out.Diagnostics, Diagnostic{Code: "diagnostic_limit"})
	} else if len(s.out.Diagnostics) < MaxSkills-1 {
		s.out.Diagnostics = append(s.out.Diagnostics, Diagnostic{Path: name, Code: code})
	}
}
func (s *scanner) limit(name string) {
	if !s.limitReported {
		s.diagnostic(name, "skill_scan_limit")
		s.limitReported = true
	}
}
func (s *scanner) walk(dir string, depth int) error {
	if err := s.ctx.Err(); err != nil {
		return err
	}
	if s.entries >= maxScanEntries || s.directories >= maxScanDirectories {
		s.limit(dir)
		return nil
	}
	info, err := s.root.Stat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.IsDir() {
		s.diagnostic(dir, "skill_directory_unavailable")
		return nil
	}
	for _, ancestor := range s.ancestors {
		if os.SameFile(info, ancestor) {
			s.diagnostic(dir, "skill_symlink_cycle")
			return nil
		}
	}
	s.ancestors = append(s.ancestors, info)
	defer func() { s.ancestors = s.ancestors[:len(s.ancestors)-1] }()
	if depth > maxSkillDepth {
		s.limit(dir)
		return nil
	}
	s.directories++
	f, err := s.root.OpenFile(dir, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		s.diagnostic(dir, "skill_directory_unavailable")
		return nil
	}
	entries, err := f.ReadDir(maxScanEntries - s.entries + 1)
	f.Close()
	if err != nil && !errors.Is(err, io.EOF) {
		s.diagnostic(dir, "skill_directory_unavailable")
		return nil
	}
	// Do not take an arbitrary filesystem-ordered subset of an oversized dir.
	if len(entries) > maxScanEntries-s.entries {
		s.entries = maxScanEntries
		s.limit(dir)
		return nil
	}
	s.entries += len(entries)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	hasManifest := false
	for _, entry := range entries {
		if entry.Name() == "SKILL.md" {
			hasManifest = true
			break
		}
	}
	for _, entry := range entries {
		if err := s.ctx.Err(); err != nil {
			return err
		}
		name := path.Join(dir, entry.Name())
		if entry.Name() == "SKILL.md" {
			raw, err := readBounded(s.root, name, MaxSkillManifestBytes)
			if err != nil {
				s.diagnostic(name, "skill_manifest_unavailable")
				continue
			}
			skill, err := parseSkill(raw, name)
			if err != nil {
				s.diagnostic(name, "skill_metadata_invalid")
				continue
			}
			previous, exists := s.skills[skill.Name]
			cost := len(skill.Name) + len(skill.Description) + len(skill.Path)
			oldCost := len(previous.Name) + len(previous.Description) + len(previous.Path)
			if (!exists && len(s.skills) >= MaxSkills) || s.indexBytes+cost-oldCost > maxIndexBytes {
				s.limit(name)
				continue
			}
			if exists {
				s.diagnostic(previous.Path, "skill_shadowed")
			}
			s.skills[skill.Name] = skill
			s.indexBytes += cost - oldCost
		} else if !hasManifest && !strings.HasPrefix(entry.Name(), ".") {
			isDir := entry.IsDir()
			if entry.Type()&os.ModeSymlink != 0 {
				info, err := s.root.Stat(name)
				if err != nil {
					s.diagnostic(name, "skill_link_unavailable")
					continue
				}
				isDir = info.IsDir()
			}
			if isDir {
				if err := s.walk(name, depth+1); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func readBounded(root *os.Root, name string, maximum int) ([]byte, error) {
	// O_NONBLOCK prevents a repository FIFO from hanging startup; Stat rejects
	// devices/directories before reading. Root prevents symlink escapes.
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("not a regular project file")
	}
	raw, err := io.ReadAll(io.LimitReader(f, int64(maximum)))
	return raw, err
}

func parseSkill(raw []byte, name string) (Skill, error) {
	invalid := errors.New("invalid skill metadata")
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return Skill{}, invalid
	}
	end := strings.Index(text[4:], "\n---\n")
	if end < 0 || end > 16*1024 || !utf8.ValidString(text[4:4+end]) {
		return Skill{}, invalid
	}
	var fields map[string]yaml.Node
	if yaml.Unmarshal([]byte(text[4:4+end]), &fields) != nil {
		return Skill{}, invalid
	}
	nameField, descriptionField := fields["name"], fields["description"]
	if nameField.Kind != yaml.ScalarNode || nameField.Tag != "!!str" || descriptionField.Kind != yaml.ScalarNode || descriptionField.Tag != "!!str" {
		return Skill{}, invalid
	}
	metadata := struct{ Name, Description string }{nameField.Value, descriptionField.Value}
	if metadata.Name == "" || len(metadata.Name) > 64 || strings.TrimSpace(metadata.Name) != metadata.Name || strings.ContainsAny(metadata.Name, "\r\n\x00") || strings.TrimSpace(metadata.Description) == "" || len(metadata.Description) > 4096 || strings.ContainsRune(metadata.Description, 0) {
		return Skill{}, invalid
	}
	return Skill{Name: metadata.Name, Description: strings.TrimSpace(metadata.Description), Path: name}, nil
}

func utf8Prefix(raw []byte, limit int) []byte {
	if len(raw) <= limit {
		return raw
	}
	raw = raw[:limit]
	// Only remove an incomplete trailing rune. Invalid bytes elsewhere remain
	// invalid and are rejected by the caller rather than silently disappearing.
	start := len(raw) - 1
	for start >= 0 && len(raw)-start < utf8.UTFMax && !utf8.RuneStart(raw[start]) {
		start--
	}
	if start >= 0 && !utf8.FullRune(raw[start:]) {
		return raw[:start]
	}
	return raw
}
